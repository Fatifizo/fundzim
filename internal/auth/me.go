package auth

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Me is the self view (interface-contracts §4.2). It never includes credentials, token hashes, MFA secrets
// or full phone numbers.
type Me struct {
	ID            string    `json:"id"`
	AccountKind   string    `json:"account_kind"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"email_verified"`
	DisplayName   string    `json:"display_name"`
	PhoneMasked   *string   `json:"phone_masked"`
	PhoneVerified bool      `json:"phone_verified"`
	MFAEnabled    bool      `json:"mfa_enabled"`
	Roles         []string  `json:"roles,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Service) loadMe(ctx context.Context, userID string) (Me, error) {
	a, err := users.ByID(ctx, s.Pool, userID)
	if err != nil {
		return Me{}, err
	}
	me := Me{ID: a.ID, AccountKind: a.Kind, Email: a.Email, EmailVerified: a.EmailVerified, DisplayName: a.DisplayName, CreatedAt: a.CreatedAt}
	if ph, err := users.PrimaryPhone(ctx, s.Pool, userID); err == nil {
		m := users.MaskPhone(ph.E164)
		me.PhoneMasked, me.PhoneVerified = &m, true
	} else if !errors.Is(err, users.ErrNotFound) {
		return Me{}, err
	}
	if me.MFAEnabled, err = s.hasConfirmedMFA(ctx, s.Pool, userID); err != nil {
		return Me{}, err
	}
	if a.Kind == users.KindStaff {
		rows, err := s.Pool.Query(ctx, `SELECT r.code FROM app.role_assignments ra JOIN app.roles r ON r.id = ra.role_id
			WHERE ra.user_id = $1 AND ra.revoked_at IS NULL AND (ra.expires_at IS NULL OR ra.expires_at > $2) ORDER BY r.code`, userID, s.now())
		if err != nil {
			return Me{}, err
		}
		defer rows.Close()
		me.Roles = []string{}
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				return Me{}, err
			}
			me.Roles = append(me.Roles, c)
		}
	}
	return me, nil
}

// GetMe returns the caller's own profile.
func (s *Service) GetMe(w http.ResponseWriter, r *http.Request) {
	me, err := s.loadMe(r.Context(), authz.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteData(w, r, http.StatusOK, me)
}

type patchMeReq struct {
	DisplayName *string `json:"display_name"`
}

// PatchMe updates the display name. Only explicitly listed fields are accepted (unknown fields such as
// roles, status or verification flags are rejected by strict decoding).
func (s *Service) PatchMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req patchMeReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if req.DisplayName != nil {
		name, ok := users.NormalizeDisplayName(*req.DisplayName)
		if !ok {
			httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "display_name", Code: "INVALID_LENGTH"}))
			return
		}
		err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
			if err := users.UpdateDisplayName(ctx, tx, p.UserID, name); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Event{Stream: audit.Business, Action: "user.profile.updated", TargetType: "user",
				TargetID: p.UserID, Metadata: map[string]any{"fields": []string{"display_name"}}})
		})
		if err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
	}
	s.GetMe(w, r)
}

type changePasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword requires the current password (a fresh factor), replaces the password and revokes the
// user's other sessions.
func (s *Service) ChangePassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req changePasswordReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if err := s.throttle(ctx, "auth.step_up", p.UserID); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	acct, err := users.ByID(ctx, s.Pool, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	hash, err := s.currentPasswordHash(ctx, p.UserID)
	ok := false
	if err == nil {
		ok, _ = s.Hasher.Verify(ctx, hash, req.CurrentPassword)
	}
	if !ok {
		s.secEvent(ctx, p.UserID, "PASSWORD_FAILED", nil)
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Unauthenticated, "INVALID_CREDENTIALS", "Your current password is incorrect."))
		return
	}
	if v := s.policy.Check(req.NewPassword, append(emailContext(acct.Email), acct.DisplayName)...); len(v) > 0 {
		httpx.WriteError(w, r, s.Logger, passwordViolation(v))
		return
	}
	newHash, err := s.Hasher.Hash(ctx, req.NewPassword)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, errs.Wrap(err, errs.Unavailable, errs.CodeServiceUnavailable, "Please try again shortly."))
		return
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.replacePassword(ctx, tx, p.UserID, newHash); err != nil {
			return err
		}
		if _, err := s.revokeSessions(ctx, tx, p.UserID, p.SessionID, RevokePasswordChanged); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.sessions SET step_up_at = $2 WHERE id = $1`, p.SessionID, s.now()); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.password.changed", TargetType: "user", TargetID: p.UserID}); err != nil {
			return err
		}
		return s.emit(ctx, tx, EvPasswordChanged, p.UserID, map[string]any{"user_id": p.UserID, "via": "change"})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	s.secEvent(ctx, p.UserID, "PASSWORD_CHANGED", nil)
	w.WriteHeader(http.StatusNoContent)
}

type emailChangeReq struct {
	NewEmail string `json:"new_email"`
	Password string `json:"password"`
}

// RequestEmailChange verifies the password and sends a confirmation link to the new address. The sign-in
// address changes only when that link is used (VerifyEmail, purpose EMAIL_CHANGE); the old address is notified.
func (s *Service) RequestEmailChange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	var req emailChangeReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	email, err := users.NormalizeEmail(req.NewEmail)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, httpx.Validation(errs.Detail{Field: "new_email", Code: "INVALID_EMAIL"}))
		return
	}
	if err := s.throttle(ctx, "auth.step_up", p.UserID); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	hash, err := s.currentPasswordHash(ctx, p.UserID)
	ok := false
	if err == nil {
		ok, _ = s.Hasher.Verify(ctx, hash, req.Password)
	}
	if !ok {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.Unauthenticated, "INVALID_CREDENTIALS", "Your password is incorrect."))
		return
	}
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		taken, err := users.EmailTakenByOther(ctx, tx, p.UserID, email)
		if err != nil || taken {
			return err // taken: answer 202 without sending (do not reveal other accounts)
		}
		emailID, err := users.AddPendingEmail(ctx, tx, p.UserID, email)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.email.change_requested", TargetType: "user", TargetID: p.UserID}); err != nil {
			return err
		}
		return s.emit(ctx, tx, EvEmailChangeRequested, p.UserID, map[string]any{"user_id": p.UserID, "email_id": emailID})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	httpx.WriteData(w, r, http.StatusAccepted, map[string]string{"status": "verification_sent"})
}

// Security returns the caller's security overview.
func (s *Service) Security(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	me, err := s.loadMe(ctx, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	var remaining int
	var changedAt *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM app.recovery_codes WHERE user_id = $1 AND used_at IS NULL AND superseded_at IS NULL),
		(SELECT created_at FROM app.password_credentials WHERE user_id = $1 AND superseded_at IS NULL)`, p.UserID).Scan(&remaining, &changedAt); err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	rows, err := s.Pool.Query(ctx, `SELECT event_type, occurred_at FROM app.security_events WHERE user_id = $1
		ORDER BY occurred_at DESC LIMIT 20`, p.UserID)
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	defer rows.Close()
	type ev struct {
		Type       string    `json:"type"`
		OccurredAt time.Time `json:"occurred_at"`
	}
	events := []ev{}
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.Type, &e.OccurredAt); err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
		events = append(events, e)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteData(w, r, http.StatusOK, map[string]any{"email_verified": me.EmailVerified, "phone_verified": me.PhoneVerified,
		"mfa_enabled": me.MFAEnabled, "recovery_codes_remaining": remaining, "password_changed_at": changedAt, "recent_events": events})
}

// Sessions lists the caller's live sessions (own sessions only; the query is scoped by user ID).
func (s *Service) Sessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	rows, err := s.Pool.Query(ctx, `SELECT id, created_at, last_seen_at, ip, coalesce(user_agent, ''), auth_method, mfa_verified_at IS NOT NULL
		FROM app.sessions WHERE user_id = $1 AND revoked_at IS NULL AND idle_expires_at > $2 AND absolute_expires_at > $2
		ORDER BY last_seen_at DESC`, p.UserID, s.now())
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	defer rows.Close()
	type sess struct {
		ID         string    `json:"id"`
		Current    bool      `json:"current"`
		CreatedAt  time.Time `json:"created_at"`
		LastSeenAt time.Time `json:"last_seen_at"`
		IPMasked   string    `json:"ip_masked"`
		UserAgent  string    `json:"user_agent"`
		AuthMethod string    `json:"auth_method"`
		MFA        bool      `json:"mfa"`
	}
	out := []sess{}
	for rows.Next() {
		var x sess
		var ip *netip.Prefix
		if err := rows.Scan(&x.ID, &x.CreatedAt, &x.LastSeenAt, &ip, &x.UserAgent, &x.AuthMethod, &x.MFA); err != nil {
			httpx.WriteError(w, r, s.Logger, err)
			return
		}
		x.Current = x.ID == p.SessionID
		x.IPMasked = maskIP(ip)
		x.UserAgent = userAgentSummary(x.UserAgent)
		out = append(out, x)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteData(w, r, http.StatusOK, out)
}

// RevokeSession revokes one of the caller's sessions. Another user's session ID yields 404 (no IDOR).
func (s *Service) RevokeSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p := authz.PrincipalFrom(ctx)
	id := r.PathValue("session_id")
	if !ids.Valid(id) {
		httpx.WriteError(w, r, s.Logger, errs.New(errs.NotFound, "SESSION_NOT_FOUND", "No such session."))
		return
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE app.sessions SET revoked_at = $3, revoked_reason = 'USER_REVOKED'
			WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, p.UserID, s.now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errs.New(errs.NotFound, "SESSION_NOT_FOUND", "No such session.")
		}
		if err := s.sessionEvent(ctx, tx, id, p.UserID, "REVOKED", RevokeUser); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Stream: audit.Security, Action: "auth.session.revoked", TargetType: "session", TargetID: id})
	})
	if err != nil {
		httpx.WriteError(w, r, s.Logger, err)
		return
	}
	if id == p.SessionID {
		s.clearSessionCookies(w)
	}
	w.WriteHeader(http.StatusNoContent)
}
