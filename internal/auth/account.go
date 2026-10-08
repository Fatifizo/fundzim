package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/auth/passwords"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Outbox event types emitted by this module (consumed by the worker; see consumers.go).
const (
	EvEmailVerificationRequested = "identity.email_verification_requested"
	EvRegistrationExisting       = "identity.registration_existing_account"
	EvPasswordResetRequested     = "identity.password_reset_requested"
	EvPasswordChanged            = "identity.password_changed"
	EvMFAChanged                 = "identity.mfa_changed"
	EvEmailChangeRequested       = "identity.email_change_requested"
	EvStaffInvited               = "identity.staff_invited"
	EvAccountSuspended           = "identity.account_suspended"
)

func (s *Service) tx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, fn)
}

func (s *Service) emit(ctx context.Context, tx pgx.Tx, typ, aggregateID string, payload map[string]any) error {
	return s.Events(ctx, tx, OutboxEvent{EventType: typ, AggregateID: aggregateID, Payload: payload})
}

// secEvent records high-volume authentication telemetry (append-only app.security_events). Failures are
// recorded here rather than in the hash-chained audit log so a brute-force attack cannot contend on the
// audit chain lock. Best effort: a failure to record never changes the response.
func (s *Service) secEvent(ctx context.Context, userID, typ string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	var uid any
	if userID != "" {
		uid = userID
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO app.security_events (id, user_id, event_type, ip, details, occurred_at)
		VALUES ($1, $2, $3, $4::inet, $5, $6)`, ids.New(), uid, typ, ipString(ctx), details, s.now()); err != nil {
		s.Logger.Warn("security event not recorded", "event_type", typ, "error", err.Error())
	}
}

func (s *Service) throttle(ctx context.Context, policy, key string) error {
	if s.Throttle == nil {
		return nil
	}
	return s.Throttle.Allow(ctx, policy, key)
}

func clientKey(ctx context.Context) string {
	if a := authz.ClientIPFrom(ctx); a.IsValid() {
		return a.String()
	}
	return "unknown"
}

func passwordViolation(v []string) error {
	e := errs.New(errs.Unprocessable, "PASSWORD_POLICY_VIOLATION", "Choose a different password.")
	for _, c := range v {
		e.Details = append(e.Details, errs.Detail{Field: "password", Code: c})
	}
	return e
}

func emailContext(email string) []string {
	local := email
	if i := strings.IndexByte(email, '@'); i > 0 {
		local = email[:i]
	}
	return []string{email, local}
}

// ---- registration -----------------------------------------------------------------------------------

type registerReq struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	AcceptTerms bool   `json:"accept_terms"`
}

// Register creates an unverified account and queues the verification email. The response is identical
// whether or not the address is already registered (no enumeration): an existing account owner receives a
// "someone tried to register" email instead.
func (s *Service) Register(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req registerReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var details []errs.Detail
	email, err := users.NormalizeEmail(req.Email)
	if err != nil {
		details = append(details, errs.Detail{Field: "email", Code: "INVALID_EMAIL"})
	}
	name, ok := users.NormalizeDisplayName(req.DisplayName)
	if !ok {
		details = append(details, errs.Detail{Field: "display_name", Code: "INVALID_LENGTH"})
	}
	if !req.AcceptTerms {
		details = append(details, errs.Detail{Field: "accept_terms", Code: "MUST_ACCEPT"})
	}
	if len(details) > 0 {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(details...))
		return
	}
	if v := s.policy.Check(req.Password, append(emailContext(email), name)...); len(v) > 0 {
		httpx.WriteError(w, r, s.Logger, passwordViolation(v))
		return
	}
	if err := s.throttle(ctx, "auth.register.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	hash, err := s.Hasher.Hash(ctx, req.Password) // always hash: equal timing for new and existing addresses
	if err != nil {
		httpx.WriteError(w, r, s.Logger, errs.Wrap(err, errs.Unavailable, errs.CodeServiceUnavailable, "Please try again shortly."))
		return
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		userID, emailID, err := users.Create(ctx, tx, users.NewAccount{Kind: users.KindUser, Email: email, DisplayName: name})
		if errors.Is(err, users.ErrEmailTaken) {
			existing, err := users.ByLoginEmail(ctx, tx, email)
			if err != nil {
				return err
			}
			return s.emit(ctx, tx, EvRegistrationExisting, existing.ID, map[string]any{"user_id": existing.ID})
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.password_credentials (id, user_id, password_hash) VALUES ($1, $2, $3)`, ids.New(), userID, hash); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.authentication_identities (id, user_id, identity_type, user_account_kind)
			VALUES ($1, $2, 'PASSWORD', 'USER')`, ids.New(), userID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.user.registered", ActorType: "user",
			ActorID: userID, TargetType: "user", TargetID: userID}); err != nil {
			return err
		}
		return s.emit(ctx, tx, EvEmailVerificationRequested, userID, map[string]any{"user_id": userID, "email_id": emailID})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.secEvent(ctx, "", "REGISTRATION_ATTEMPT", nil)
	httpx.WriteData(w, r, http.StatusAccepted, map[string]string{"status": "verification_sent"})
}

// ---- email verification -----------------------------------------------------------------------------

type tokenReq struct {
	Token string `json:"token"`
}

func errTokenInvalid() error {
	return errs.New(errs.Invalid, "TOKEN_INVALID", "This link is invalid or has expired. Request a new one.")
}

// consumeToken atomically consumes a live token of one of the purposes; exactly one concurrent caller wins.
func (s *Service) consumeToken(ctx context.Context, tx pgx.Tx, raw string, purposes ...string) (id, userID, purpose string, emailID *string, err error) {
	if !crypto.ValidTokenFormat(raw) {
		return "", "", "", nil, errTokenInvalid()
	}
	err = tx.QueryRow(ctx, `UPDATE app.auth_tokens SET consumed_at = $2
		WHERE token_hash = $1 AND purpose = ANY($3) AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > $2
		RETURNING id, user_id, purpose, user_email_id`, crypto.HashToken(raw), s.now(), purposes).Scan(&id, &userID, &purpose, &emailID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", nil, errTokenInvalid()
	}
	return id, userID, purpose, emailID, err
}

// VerifyEmail consumes an EMAIL_VERIFICATION or EMAIL_CHANGE token.
func (s *Service) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req tokenReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.token.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, userID, purpose, emailID, err := s.consumeToken(ctx, tx, req.Token, "EMAIL_VERIFICATION", "EMAIL_CHANGE")
		if err != nil {
			return err
		}
		if emailID == nil {
			return errTokenInvalid()
		}
		now := s.now()
		action := "auth.email.verified"
		if purpose == "EMAIL_CHANGE" {
			if err := users.SwapLoginEmail(ctx, tx, userID, *emailID, now); err != nil {
				if errors.Is(err, users.ErrEmailTaken) {
					return errTokenInvalid()
				}
				return err
			}
			action = "auth.email.changed"
			if _, err := s.revokeSessions(ctx, tx, userID, "", RevokePasswordChanged); err != nil {
				return err
			}
		} else if err := users.MarkEmailVerified(ctx, tx, userID, *emailID, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: action, ActorType: "user", ActorID: userID,
			TargetType: "user", TargetID: userID})
	})
	if err != nil {
		if e := errs.As(err); e.Code == "TOKEN_INVALID" {
			s.secEvent(ctx, "", "EMAIL_VERIFICATION_FAILED", nil)
		}
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]string{"status": "verified"})
}

type emailReq struct {
	Email string `json:"email"`
}

// ResendVerification queues a new verification email for an unverified sign-in address. Always 202.
func (s *Service) ResendVerification(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req emailReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	email, err := users.NormalizeEmail(req.Email)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "email", Code: "INVALID_EMAIL"}))
		return
	}
	if err := s.throttle(ctx, "auth.verify_resend.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	// Per-account throttle exceeded is answered like success (do not reveal account existence).
	if s.throttle(ctx, "auth.verify_resend.account", email) == nil {
		err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			a, err := users.ByLoginEmail(ctx, tx, email)
			if errors.Is(err, users.ErrNotFound) || (err == nil && (a.EmailVerified || a.Status != users.StatusActive || a.IsSystem)) {
				return nil
			}
			if err != nil {
				return err
			}
			return s.emit(ctx, tx, EvEmailVerificationRequested, a.ID, map[string]any{"user_id": a.ID, "email_id": a.EmailID})
		})
		if err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
	}
	httpx.WriteData(w, r, http.StatusAccepted, map[string]string{"status": "verification_sent"})
}

// ---- password reset -----------------------------------------------------------------------------------

// ForgotPassword queues a reset email when the address belongs to an active account. Always 202.
func (s *Service) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req emailReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	email, err := users.NormalizeEmail(req.Email)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "email", Code: "INVALID_EMAIL"}))
		return
	}
	if err := s.throttle(ctx, "auth.password_reset.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var userID string
	if s.throttle(ctx, "auth.password_reset.account", email) == nil {
		err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			a, err := users.ByLoginEmail(ctx, tx, email)
			if errors.Is(err, users.ErrNotFound) || (err == nil && (a.Status != users.StatusActive || a.IsSystem)) {
				return nil
			}
			if err != nil {
				return err
			}
			userID = a.ID
			return s.emit(ctx, tx, EvPasswordResetRequested, a.ID, map[string]any{"user_id": a.ID, "email_id": a.EmailID})
		})
		if err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
	}
	s.secEvent(ctx, userID, "PASSWORD_RESET_REQUESTED", nil)
	httpx.WriteData(w, r, http.StatusAccepted, map[string]string{"status": "reset_requested"})
}

type resetReq struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// ResetPassword consumes a PASSWORD_RESET token, replaces the password, verifies the email it was sent to,
// and revokes every session and pending MFA challenge. MFA is still required at the next login.
func (s *Service) ResetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req resetReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.token.ip", clientKey(ctx)); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if v := s.policy.Check(req.NewPassword); len(v) > 0 {
		httpx.WriteError(w, r, s.Logger, passwordViolation(v))
		return
	}
	hash, err := s.Hasher.Hash(ctx, req.NewPassword)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, errs.Wrap(err, errs.Unavailable, errs.CodeServiceUnavailable, "Please try again shortly."))
		return
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, userID, _, emailID, err := s.consumeToken(ctx, tx, req.Token, "PASSWORD_RESET")
		if err != nil {
			return err
		}
		a, err := users.ByIDForUpdate(ctx, tx, userID)
		if err != nil {
			return err
		}
		if a.Status != users.StatusActive {
			return errTokenInvalid()
		}
		if v := s.policy.Check(req.NewPassword, append(emailContext(a.Email), a.DisplayName)...); len(v) > 0 {
			return passwordViolation(v)
		}
		if err := s.replacePassword(ctx, tx, userID, hash); err != nil {
			return err
		}
		if emailID != nil {
			if err := users.MarkEmailVerified(ctx, tx, userID, *emailID, s.now()); err != nil && !errors.Is(err, users.ErrNotFound) {
				return err
			}
		}
		if _, err := s.revokeSessions(ctx, tx, userID, "", RevokePasswordReset); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.password.reset_completed",
			ActorType: "user", ActorID: userID, TargetType: "user", TargetID: userID}); err != nil {
			return err
		}
		return s.emit(ctx, tx, EvPasswordChanged, userID, map[string]any{"user_id": userID, "via": "reset"})
	})
	if err != nil {
		if e := errs.As(err); e.Code == "TOKEN_INVALID" {
			s.secEvent(ctx, "", "PASSWORD_RESET_FAILED", nil)
		}
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.clearSessionCookies(w)
	httpx.WriteData(w, r, http.StatusOK, map[string]string{"status": "password_reset"})
}

func (s *Service) replacePassword(ctx context.Context, tx pgx.Tx, userID, hash string) error {
	if _, err := tx.Exec(ctx, `UPDATE app.password_credentials SET superseded_at = $2 WHERE user_id = $1 AND superseded_at IS NULL`,
		userID, s.now()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO app.password_credentials (id, user_id, password_hash) VALUES ($1, $2, $3)`, ids.New(), userID, hash)
	return err
}

// ---- login ----------------------------------------------------------------------------------------------

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func errInvalidCredentials() error {
	return errs.New(errs.Unauthenticated, "INVALID_CREDENTIALS", "The email or password is incorrect.")
}

// Login checks email and password. Responses never reveal whether the address exists: unknown addresses
// run a dummy Argon2id verification for equal timing and get the same INVALID_CREDENTIALS error.
func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var req loginReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	email, emailErr := users.NormalizeEmail(req.Email)
	if err := s.throttle(ctx, "auth.login.ip", clientKey(ctx)); err != nil {
		s.secEvent(ctx, "", "RATE_LIMITED", map[string]any{"policy": "auth.login.ip"})
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if emailErr != nil || req.Password == "" || len(req.Password) > 4*s.Cfg.PasswordMaxLength {
		_, _ = s.Hasher.Verify(ctx, s.dummyHash, "x")
		httpx.WriteError(w, r, s.Logger, errInvalidCredentials())
		return
	}
	if err := s.throttle(ctx, "auth.login.account", email); err != nil {
		s.secEvent(ctx, "", "LOGIN_BLOCKED", map[string]any{"policy": "auth.login.account"})
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	acct, err := users.ByLoginEmail(ctx, s.Pool, email)
	if err != nil && !errors.Is(err, users.ErrNotFound) {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	hash := s.dummyHash
	if err == nil {
		if h, herr := s.currentPasswordHash(ctx, acct.ID); herr == nil {
			hash = h
		}
	}
	ok, verr := s.Hasher.Verify(ctx, hash, req.Password)
	if verr != nil && !errors.Is(verr, passwords.ErrBusy) {
		ok = false
	} else if errors.Is(verr, passwords.ErrBusy) {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Unavailable, errs.CodeServiceUnavailable, "Please try again shortly."))
		return
	}
	if err != nil || !ok || acct.IsSystem || hash == s.dummyHash {
		var uid string
		if err == nil {
			uid = acct.ID
		}
		s.secEvent(ctx, uid, "LOGIN_FAILED", nil)
		httpx.WriteError(w, r, s.Logger, errInvalidCredentials())
		return
	}
	if acct.Status != users.StatusActive {
		s.secEvent(ctx, acct.ID, "LOGIN_FAILED", map[string]any{"reason": "account_suspended"})
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "ACCOUNT_SUSPENDED", "This account is suspended. Contact support."))
		return
	}
	if s.Hasher.NeedsRehash(hash) {
		s.upgradeHash(ctx, acct.ID, req.Password)
	}
	mfa, err := s.hasConfirmedMFA(ctx, s.Pool, acct.ID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if acct.Kind == users.KindStaff && !mfa {
		// Staff accounts are created with MFA (invitation flow); a staff account without MFA cannot sign in.
		s.secEvent(ctx, acct.ID, "LOGIN_FAILED", map[string]any{"reason": "staff_without_mfa"})
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "MFA_REQUIRED", "This account must complete MFA setup before signing in."))
		return
	}
	if mfa {
		s.startMFAChallenge(w, r, acct)
		return
	}
	s.completeLogin(w, r, acct, nil, "password")
}

func (s *Service) currentPasswordHash(ctx context.Context, userID string) (string, error) {
	var h string
	err := s.Pool.QueryRow(ctx, `SELECT password_hash FROM app.password_credentials WHERE user_id = $1 AND superseded_at IS NULL`, userID).Scan(&h)
	return h, err
}

func (s *Service) upgradeHash(ctx context.Context, userID, pw string) {
	h, err := s.Hasher.Hash(ctx, pw)
	if err != nil {
		return
	}
	_ = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error { return s.replacePassword(ctx, tx, userID, h) })
}

// completeLogin creates a fresh session (rotating any presented one) and sets the cookies.
func (s *Service) completeLogin(w http.ResponseWriter, r *http.Request, acct users.Account, mfaAt *time.Time, method string) {
	ctx := r.Context()
	var old string
	if p := authz.PrincipalFrom(ctx); p != nil {
		old = p.SessionID
	}
	var (
		token    crypto.Token
		sid      string
		absolute time.Time
	)
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		token, sid, absolute, err = s.newSession(ctx, tx, acct, mfaAt, old, r.UserAgent())
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.login.succeeded", ActorType: actorType(acct.Kind),
			ActorID: acct.ID, TargetType: "session", TargetID: sid, Metadata: map[string]any{"method": method, "mfa": mfaAt != nil}})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.secEvent(ctx, acct.ID, "LOGIN_SUCCEEDED", map[string]any{"method": method})
	s.issueSessionCookies(w, token, sid, absolute)
	me, err := s.loadMe(ctx, acct.ID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"status": "authenticated", "user": me})
}

func actorType(kind string) string {
	if kind == users.KindStaff {
		return "staff"
	}
	return "user"
}

// ---- logout ---------------------------------------------------------------------------------------------

// Logout revokes the current session.
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app.sessions SET revoked_at = $2, revoked_reason = 'LOGOUT' WHERE id = $1 AND revoked_at IS NULL`, p.SessionID, s.now())
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		if err := s.sessionEvent(ctx, tx, p.SessionID, p.UserID, "REVOKED", RevokeLogout); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.logout", TargetType: "session", TargetID: p.SessionID})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// LogoutAll revokes every session of the user, including the current one.
func (s *Service) LogoutAll(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		n, err := s.revokeSessions(ctx, tx, p.UserID, "", RevokeLogoutAll)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.session.revoked_all", TargetType: "user",
			TargetID: p.UserID, Metadata: map[string]any{"count": n}})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.clearSessionCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// ---- session info and step-up -----------------------------------------------------------------------

// SessionInfo reports whether the request is authenticated. Never 401.
func (s *Service) SessionInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	out := map[string]any{"authenticated": false}
	if p := authz.PrincipalFrom(ctx); p != nil {
		me, err := s.loadMe(ctx, p.UserID)
		if err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
		out = map[string]any{"authenticated": true, "user": me}
	} else if c, err := r.Cookie(s.mfaCookie()); err == nil && crypto.ValidTokenFormat(c.Value) {
		out["mfa_pending"] = true
	}
	httpx.WriteData(w, r, http.StatusOK, out)
}

type stepUpReq struct {
	Password string `json:"password,omitempty"`
	Code     string `json:"code,omitempty"`
}

// StepUp refreshes the session's step-up time after a fresh password or TOTP code (staff: TOTP only).
func (s *Service) StepUp(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req stepUpReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.step_up", p.UserID); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var ok bool
	switch {
	case req.Code != "":
		var err error
		ok, err = s.verifyTOTPForUser(ctx, p.UserID, req.Code)
		if err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
	case req.Password != "" && p.Kind != authz.KindStaff: // staff step up with TOTP only
		hash, err := s.currentPasswordHash(ctx, p.UserID)
		if err == nil {
			ok, _ = s.Hasher.Verify(ctx, hash, req.Password)
		}
	}
	if !ok {
		s.secEvent(ctx, p.UserID, "STEP_UP_FAILED", nil)
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Unauthenticated, "STEP_UP_FAILED", "That didn't match. Try again."))
		return
	}
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE app.sessions SET step_up_at = $2 WHERE id = $1 AND revoked_at IS NULL`, p.SessionID, now); err != nil {
			return err
		}
		return s.sessionEvent(ctx, tx, p.SessionID, p.UserID, "STEP_UP", "")
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.secEvent(ctx, p.UserID, "STEP_UP_SUCCEEDED", nil)
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"step_up_expires_at": now.Add(s.Cfg.StepUpMaxAge).UTC()})
}
