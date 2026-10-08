package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// TOTP parameters: RFC 6238 defaults (30-second steps, 6 digits, SHA-1) for authenticator-app
// compatibility; one step of clock skew is tolerated. Each time step is accepted at most once per method
// (totp_last_used_step), so an observed code cannot be replayed.
const (
	totpPeriod      = 30
	totpSkew        = 1
	recoveryCodeNum = 10
	mfaMaxAttempts  = 5
)

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Service) hasConfirmedMFA(ctx context.Context, q queryRower, userID string) (bool, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM app.mfa_methods WHERE user_id = $1 AND method_type = 'TOTP'
		AND confirmed_at IS NOT NULL AND disabled_at IS NULL`, userID).Scan(&n)
	return n > 0, err
}

func aad(methodID string) []byte { return []byte("mfa_methods:" + methodID) }

// checkTOTP validates code against the method and records the step so it cannot be reused. It returns
// false for wrong, expired or replayed codes.
func (s *Service) checkTOTP(ctx context.Context, tx pgx.Tx, methodID string, sealed []byte, code string) (bool, error) {
	code = strings.TrimSpace(code)
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return false, nil
	}
	secret, err := s.AEAD.Open(sealed, aad(methodID))
	if err != nil {
		return false, err
	}
	now := s.now()
	for delta := -totpSkew; delta <= totpSkew; delta++ {
		t := now.Add(time.Duration(delta*totpPeriod) * time.Second)
		ok, err := totp.ValidateCustom(code, string(secret), t, totp.ValidateOpts{Period: totpPeriod, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil || !ok {
			continue
		}
		step := t.Unix() / totpPeriod
		tag, err := tx.Exec(ctx, `UPDATE app.mfa_methods SET totp_last_used_step = $2, last_used_at = $3
			WHERE id = $1 AND (totp_last_used_step IS NULL OR totp_last_used_step < $2)`, methodID, step, now)
		if err != nil {
			return false, err
		}
		return tag.RowsAffected() == 1, nil // 0 rows: this or a later step was already used (replay)
	}
	return false, nil
}

// verifyTOTPForUser checks a code against the user's confirmed TOTP method in its own transaction.
func (s *Service) verifyTOTPForUser(ctx context.Context, userID, code string) (bool, error) {
	var ok bool
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		var sealed []byte
		err := tx.QueryRow(ctx, `SELECT id, totp_secret_ciphertext FROM app.mfa_methods WHERE user_id = $1 AND method_type = 'TOTP'
			AND confirmed_at IS NOT NULL AND disabled_at IS NULL FOR UPDATE`, userID).Scan(&id, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ok, err = s.checkTOTP(ctx, tx, id, sealed, code)
		return err
	})
	return ok, err
}

// startMFAChallenge stores a short-lived challenge (hash only) and sets the HttpOnly challenge cookie.
// No session exists until the second factor succeeds.
func (s *Service) startMFAChallenge(w http.ResponseWriter, r *http.Request, acct users.Account) {
	ctx := r.Context()
	token, err := crypto.NewToken()
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// a new password success supersedes earlier pending challenges
		if _, err := tx.Exec(ctx, `UPDATE app.mfa_login_challenges SET invalidated_at = $2
			WHERE user_id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL`, acct.ID, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.mfa_login_challenges (id, user_id, token_hash, requested_ip, created_at, expires_at)
			VALUES ($1, $2, $3, $4::inet, $5, $6)`, ids.New(), acct.ID, crypto.HashToken(string(token)), ipString(ctx), now, now.Add(s.Cfg.MFAChallengeTTL))
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.setCookie(w, s.mfaCookie(), string(token), int(s.Cfg.MFAChallengeTTL.Seconds()), true, http.SameSiteStrictMode)
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"status": "mfa_required", "methods": []string{"totp", "recovery_code"}})
}

type mfaCodeReq struct {
	Code string `json:"code"`
}

type mfaRecoveryReq struct {
	RecoveryCode string `json:"recovery_code"`
}

// MFAVerify completes a login with a TOTP code.
func (s *Service) MFAVerify(w http.ResponseWriter, r *http.Request) {
	var req mfaCodeReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.completeMFA(w, r, "totp", func(ctx context.Context, tx pgx.Tx, userID string) (bool, error) {
		var id string
		var sealed []byte
		err := tx.QueryRow(ctx, `SELECT id, totp_secret_ciphertext FROM app.mfa_methods WHERE user_id = $1 AND method_type = 'TOTP'
			AND confirmed_at IS NOT NULL AND disabled_at IS NULL FOR UPDATE`, userID).Scan(&id, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return s.checkTOTP(ctx, tx, id, sealed, req.Code)
	})
}

// MFARecovery completes a login with a one-time recovery code.
func (s *Service) MFARecovery(w http.ResponseWriter, r *http.Request) {
	var req mfaRecoveryReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.completeMFA(w, r, "recovery_code", func(ctx context.Context, tx pgx.Tx, userID string) (bool, error) {
		return s.consumeRecoveryCode(ctx, tx, userID, req.RecoveryCode)
	})
}

func normalizeRecoveryCode(c string) string {
	c = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(c), " ", ""))
	if len(c) == 10 {
		c = c[:5] + "-" + c[5:]
	}
	return c
}

// consumeRecoveryCode marks a matching unused code as used; concurrent attempts with the same code cannot
// both succeed (single UPDATE ... WHERE used_at IS NULL).
func (s *Service) consumeRecoveryCode(ctx context.Context, tx pgx.Tx, userID, code string) (bool, error) {
	h := s.Keyed.Sum("recovery_code", userID, normalizeRecoveryCode(code))
	tag, err := tx.Exec(ctx, `UPDATE app.recovery_codes SET used_at = $3
		WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL AND superseded_at IS NULL`, userID, h, s.now())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func errMFAInvalid() error {
	return errs.New(errs.Unauthenticated, "MFA_CODE_INVALID", "That code didn't work. Try again.")
}

func errMFAExpired() error {
	return errs.New(errs.Unauthenticated, "MFA_CHALLENGE_EXPIRED", "Your sign-in expired. Start again.")
}

// completeMFA validates the pending challenge, applies check, and on success consumes the challenge and
// creates the session. Failures count against the challenge (max 5) and the per-user throttle.
func (s *Service) completeMFA(w http.ResponseWriter, r *http.Request, method string, check func(context.Context, pgx.Tx, string) (bool, error)) {
	ctx := r.Context()
	c, err := r.Cookie(s.mfaCookie())
	if err != nil || !crypto.ValidTokenFormat(c.Value) {
		httpx.WriteError(w, r, s.Logger, errMFAExpired())
		return
	}
	var (
		userID   string
		ok, used bool
	)
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var chID string
		var attempts int
		err := tx.QueryRow(ctx, `SELECT id, user_id, attempts FROM app.mfa_login_challenges
			WHERE token_hash = $1 AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > $2 FOR UPDATE`,
			crypto.HashToken(c.Value), now).Scan(&chID, &userID, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return errMFAExpired()
		}
		if err != nil {
			return err
		}
		if terr := s.throttle(ctx, "auth.mfa.challenge", userID); terr != nil {
			return terr
		}
		ok, err = check(ctx, tx, userID)
		if err != nil {
			return err
		}
		if !ok {
			attempts++
			var inval any
			if attempts >= mfaMaxAttempts {
				inval = now
			}
			_, err := tx.Exec(ctx, `UPDATE app.mfa_login_challenges SET attempts = $2, invalidated_at = $3 WHERE id = $1`, chID, attempts, inval)
			return err
		}
		used = true
		_, err = tx.Exec(ctx, `UPDATE app.mfa_login_challenges SET consumed_at = $2 WHERE id = $1`, chID, now)
		if err == nil && method == "recovery_code" {
			err = audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.mfa.recovery_used", ActorType: "user",
				ActorID: userID, TargetType: "user", TargetID: userID})
		}
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if !ok || !used {
		s.secEvent(ctx, userID, "MFA_FAILED", map[string]any{"method": method})
		httpx.WriteError(w, r, s.Logger, errMFAInvalid())
		return
	}
	if method == "recovery_code" {
		s.secEvent(ctx, userID, "RECOVERY_CODE_USED", nil)
		_ = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			return s.emit(ctx, tx, EvMFAChanged, userID, map[string]any{"user_id": userID, "change": "recovery_code_used"})
		})
	}
	acct, err := users.ByID(ctx, s.Pool, userID)
	if err != nil || acct.Status != users.StatusActive || acct.IsSystem {
		httpx.WriteError(w, r, s.Logger, errMFAExpired())
		return
	}
	s.completeLogin(w, r, acct, &now, "password+"+method)
}

// ---- enrolment (authenticated, step-up) -----------------------------------------------------------------

// MFAEnroll creates an unconfirmed TOTP method and returns its secret once. A previous unconfirmed
// enrolment is discarded. MFA is not active until MFAConfirm succeeds.
func (s *Service) MFAEnroll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	if err := s.requireStepUp(p); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	acct, err := users.ByID(ctx, s.Pool, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "FundZim", AccountName: acct.Email, Period: totpPeriod,
		Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1, SecretSize: 20})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	id := ids.New()
	sealed, err := s.AEAD.Seal([]byte(key.Secret()), aad(id))
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app.mfa_methods SET disabled_at = $2 WHERE user_id = $1 AND confirmed_at IS NULL AND disabled_at IS NULL`,
			p.UserID, s.now()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO app.mfa_methods (id, user_id, method_type, label, totp_secret_ciphertext, totp_secret_key_id)
			VALUES ($1, $2, 'TOTP', 'Authenticator app', $3, $4)`, id, p.UserID, sealed, s.AEAD.KeyID())
		return err
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"enrollment_id": id, "secret": key.Secret(), "otpauth_uri": key.URL()})
}

type mfaConfirmReq struct {
	EnrollmentID string `json:"enrollment_id"`
	Code         string `json:"code"`
}

// MFAConfirm activates a pending TOTP method after a valid code and returns fresh recovery codes once.
// Other sessions are revoked (security change) and the owner is notified.
func (s *Service) MFAConfirm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	if err := s.requireStepUp(p); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var req mfaConfirmReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.step_up", p.UserID); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var codes []string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var sealed []byte
		err := tx.QueryRow(ctx, `SELECT totp_secret_ciphertext FROM app.mfa_methods WHERE id = $1 AND user_id = $2
			AND method_type = 'TOTP' AND confirmed_at IS NULL AND disabled_at IS NULL FOR UPDATE`, req.EnrollmentID, p.UserID).Scan(&sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.New(errs.NotFound, "MFA_ENROLLMENT_NOT_FOUND", "Start MFA setup again.")
		}
		if err != nil {
			return err
		}
		ok, err := s.checkTOTP(ctx, tx, req.EnrollmentID, sealed, req.Code)
		if err != nil {
			return err
		}
		if !ok {
			return errMFAInvalid()
		}
		now := s.now()
		// only one confirmed TOTP method at a time
		if _, err := tx.Exec(ctx, `UPDATE app.mfa_methods SET disabled_at = $3 WHERE user_id = $1 AND id <> $2
			AND confirmed_at IS NOT NULL AND disabled_at IS NULL`, p.UserID, req.EnrollmentID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.mfa_methods SET confirmed_at = $2 WHERE id = $1`, req.EnrollmentID, now); err != nil {
			return err
		}
		if codes, err = s.newRecoveryCodes(ctx, tx, p.UserID); err != nil {
			return err
		}
		if _, err := s.revokeSessions(ctx, tx, p.UserID, p.SessionID, RevokeMFAChanged); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.sessions SET mfa_verified_at = $2 WHERE id = $1`, p.SessionID, now); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.mfa.enabled", TargetType: "user", TargetID: p.UserID}); err != nil {
			return err
		}
		return s.emit(ctx, tx, EvMFAChanged, p.UserID, map[string]any{"user_id": p.UserID, "change": "enabled"})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.secEvent(ctx, p.UserID, "MFA_ENROLLED", nil)
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"recovery_codes": codes})
}

// newRecoveryCodes supersedes the previous batch and stores keyed hashes of ten new codes.
func (s *Service) newRecoveryCodes(ctx context.Context, tx pgx.Tx, userID string) ([]string, error) {
	now := s.now()
	if _, err := tx.Exec(ctx, `UPDATE app.recovery_codes SET superseded_at = $2 WHERE user_id = $1 AND used_at IS NULL AND superseded_at IS NULL`,
		userID, now); err != nil {
		return nil, err
	}
	batch := ids.New()
	codes := make([]string, 0, recoveryCodeNum)
	for len(codes) < recoveryCodeNum {
		c, err := crypto.RecoveryCode()
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.recovery_codes (id, user_id, batch_id, code_hash, hash_key_id) VALUES ($1, $2, $3, $4, $5)`,
			ids.New(), userID, batch, s.Keyed.Sum("recovery_code", userID, c), s.Keyed.KeyID()); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	return codes, nil
}

type mfaDisableReq struct {
	Code string `json:"code"`
}

// MFADisable turns MFA off for a personal account (step-up plus a current TOTP or recovery code). Staff
// accounts can never disable MFA.
func (s *Service) MFADisable(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	if p.Kind == authz.KindStaff {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "MFA_REQUIRED_FOR_ROLE", "Staff accounts must keep MFA enabled."))
		return
	}
	if err := s.requireStepUp(p); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var req mfaDisableReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.step_up", p.UserID); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var id string
		var sealed []byte
		err := tx.QueryRow(ctx, `SELECT id, totp_secret_ciphertext FROM app.mfa_methods WHERE user_id = $1 AND method_type = 'TOTP'
			AND confirmed_at IS NOT NULL AND disabled_at IS NULL FOR UPDATE`, p.UserID).Scan(&id, &sealed)
		if errors.Is(err, pgx.ErrNoRows) {
			return errs.New(errs.Conflict, "MFA_NOT_ENABLED", "MFA is not enabled.")
		}
		if err != nil {
			return err
		}
		ok, err := s.checkTOTP(ctx, tx, id, sealed, req.Code)
		if err == nil && !ok {
			ok, err = s.consumeRecoveryCode(ctx, tx, p.UserID, req.Code)
		}
		if err != nil {
			return err
		}
		if !ok {
			return errMFAInvalid()
		}
		now := s.now()
		if _, err := tx.Exec(ctx, `UPDATE app.mfa_methods SET disabled_at = $2 WHERE user_id = $1 AND disabled_at IS NULL`, p.UserID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.recovery_codes SET superseded_at = $2 WHERE user_id = $1 AND used_at IS NULL AND superseded_at IS NULL`, p.UserID, now); err != nil {
			return err
		}
		if _, err := s.revokeSessions(ctx, tx, p.UserID, p.SessionID, RevokeMFAChanged); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.mfa.disabled", TargetType: "user", TargetID: p.UserID}); err != nil {
			return err
		}
		return s.emit(ctx, tx, EvMFAChanged, p.UserID, map[string]any{"user_id": p.UserID, "change": "disabled"})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.secEvent(ctx, p.UserID, "MFA_REMOVED", nil)
	w.WriteHeader(http.StatusNoContent)
}

// MFARegenerateRecoveryCodes replaces the recovery-code batch (step-up required).
func (s *Service) MFARegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	if err := s.requireStepUp(p); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var codes []string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ok, err := s.hasConfirmedMFA(ctx, tx, p.UserID)
		if err != nil {
			return err
		}
		if !ok {
			return errs.New(errs.Conflict, "MFA_NOT_ENABLED", "MFA is not enabled.")
		}
		if codes, err = s.newRecoveryCodes(ctx, tx, p.UserID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.mfa.recovery_codes_regenerated", TargetType: "user", TargetID: p.UserID}); err != nil {
			return err
		}
		return s.emit(ctx, tx, EvMFAChanged, p.UserID, map[string]any{"user_id": p.UserID, "change": "recovery_codes_regenerated"})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"recovery_codes": codes})
}
