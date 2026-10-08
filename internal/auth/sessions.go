package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/crypto"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

// Session revocation reasons (app.sessions CHECK).
const (
	RevokeLogout          = "LOGOUT"
	RevokeLogoutAll       = "LOGOUT_ALL"
	RevokeRotated         = "ROTATED"
	RevokeUser            = "USER_REVOKED"
	RevokeStaff           = "STAFF_REVOKED"
	RevokePasswordReset   = "PASSWORD_RESET"
	RevokePasswordChanged = "PASSWORD_CHANGED"
	RevokeMFAChanged      = "MFA_CHANGED"
	RevokePrivilege       = "PRIVILEGE_CHANGE"
	RevokeSuspended       = "ACCOUNT_SUSPENDED"
)

// Cookie names: the __Host- prefix when cookies are Secure (https), plain names on local http.
func (s *Service) sessionCookie() string { return s.Cfg.SessionCookieName }
func (s *Service) csrfCookie() string    { return s.prefix() + "fz_csrf" }
func (s *Service) mfaCookie() string     { return s.prefix() + "fz_mfa" }
func (s *Service) prefix() string {
	if s.Cfg.CookieSecure {
		return "__Host-"
	}
	return ""
}

// csrfToken derives the session-bound CSRF token: HMAC-SHA-256(CSRF_SECRET, "csrf:"+sessionID).
func (s *Service) csrfToken(sessionID string) string {
	m := hmac.New(sha256.New, []byte(s.Cfg.CSRFSecret.Reveal()))
	m.Write([]byte("csrf:" + sessionID))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Service) setCookie(w http.ResponseWriter, name, value string, maxAge int, httpOnly bool, sameSite http.SameSite) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: httpOnly,
		Secure: s.Cfg.CookieSecure, SameSite: sameSite})
}

func (s *Service) clearCookie(w http.ResponseWriter, name string, httpOnly bool) {
	s.setCookie(w, name, "", -1, httpOnly, http.SameSiteLaxMode)
}

// issueSessionCookies sets the session and CSRF cookies. The cookie lives until the absolute expiry; the
// server enforces the idle timeout.
func (s *Service) issueSessionCookies(w http.ResponseWriter, token crypto.Token, sessionID string, absolute time.Time) {
	maxAge := int(time.Until(absolute).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	s.setCookie(w, s.sessionCookie(), string(token), maxAge, true, http.SameSiteLaxMode)
	s.setCookie(w, s.csrfCookie(), s.csrfToken(sessionID), maxAge, false, http.SameSiteLaxMode)
	s.clearCookie(w, s.mfaCookie(), true)
}

func (s *Service) clearSessionCookies(w http.ResponseWriter) {
	s.clearCookie(w, s.sessionCookie(), true)
	s.clearCookie(w, s.csrfCookie(), false)
}

// newSession creates a session in tx. rotatedFrom (optional) is revoked with reason ROTATED, which prevents
// session fixation: authentication always yields a brand-new token.
func (s *Service) newSession(ctx context.Context, tx pgx.Tx, acct users.Account, mfaAt *time.Time, rotatedFrom string, userAgent string) (crypto.Token, string, time.Time, error) {
	token, err := crypto.NewToken()
	if err != nil {
		return "", "", time.Time{}, err
	}
	now := s.now()
	idle, absolute := s.Cfg.SessionIdleTimeout, s.Cfg.SessionAbsoluteTimeout
	if acct.Kind == users.KindStaff {
		idle, absolute = s.Cfg.StaffSessionIdleTimeout, s.Cfg.StaffSessionAbsoluteTimeout
	}
	absAt := now.Add(absolute)
	idleAt := now.Add(idle)
	if idleAt.After(absAt) {
		idleAt = absAt
	}
	id := ids.New()
	var rotated any
	if rotatedFrom != "" {
		tag, err := tx.Exec(ctx, `UPDATE app.sessions SET revoked_at = $2, revoked_reason = 'ROTATED'
			WHERE id = $1 AND revoked_at IS NULL`, rotatedFrom, now)
		if err != nil {
			return "", "", time.Time{}, err
		}
		if tag.RowsAffected() == 1 {
			rotated = rotatedFrom
		}
	}
	ua := userAgent
	if len(ua) > 512 {
		ua = ua[:512]
	}
	var stepUp any = now // authenticating counts as a fresh factor
	if _, err := tx.Exec(ctx, `INSERT INTO app.sessions (id, user_id, kind, token_hash, auth_method, mfa_verified_at, step_up_at,
		created_at, last_seen_at, idle_expires_at, absolute_expires_at, rotated_from_id, ip, user_agent)
		VALUES ($1, $2, $3, $4, 'PASSWORD', $5, $6, $7, $7, $8, $9, $10, $11::inet, $12)`,
		id, acct.ID, acct.Kind, crypto.HashToken(string(token)), mfaAt, stepUp, now, idleAt, absAt, rotated, ipString(ctx), nullable(ua)); err != nil {
		return "", "", time.Time{}, err
	}
	if err := s.sessionEvent(ctx, tx, id, acct.ID, "CREATED", ""); err != nil {
		return "", "", time.Time{}, err
	}
	return token, id, absAt, nil
}

func (s *Service) sessionEvent(ctx context.Context, tx pgx.Tx, sessionID, userID, typ, reason string) error {
	_, err := tx.Exec(ctx, `INSERT INTO app.session_events (id, session_id, user_id, event_type, reason, ip, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6::inet, $7)`, ids.New(), sessionID, userID, typ, nullable(reason), ipString(ctx), s.now())
	return err
}

// revokeSessions revokes the user's live sessions (all, or all except keepID) and records events.
func (s *Service) revokeSessions(ctx context.Context, tx pgx.Tx, userID, keepID, reason string) (int, error) {
	rows, err := tx.Query(ctx, `UPDATE app.sessions SET revoked_at = $3, revoked_reason = $4
		WHERE user_id = $1 AND revoked_at IS NULL AND ($2 = '' OR id::text <> $2) RETURNING id`, userID, keepID, s.now(), reason)
	if err != nil {
		return 0, err
	}
	var revoked []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		revoked = append(revoked, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range revoked {
		if err := s.sessionEvent(ctx, tx, id, userID, "REVOKED", reason); err != nil {
			return 0, err
		}
	}
	// pending MFA challenges must not survive a security change either
	if _, err := tx.Exec(ctx, `UPDATE app.mfa_login_challenges SET invalidated_at = $2
		WHERE user_id = $1 AND consumed_at IS NULL AND invalidated_at IS NULL`, userID, s.now()); err != nil {
		return 0, err
	}
	return len(revoked), nil
}

// resolved is the outcome of looking up a session cookie.
type resolved struct {
	principal *authz.Principal
	err       error // database failure (fail closed: protected routes answer 503)
}

type resolvedKey struct{}

// resolveSession loads a live session for token and slides its idle expiry (at most once a minute).
func (s *Service) resolveSession(ctx context.Context, token string) (*authz.Principal, error) {
	if !crypto.ValidTokenFormat(token) {
		return nil, nil
	}
	now := s.now()
	var (
		p                        authz.Principal
		kind                     string
		mfaAt, stepUpAt          *time.Time
		lastSeen, absolute, idle time.Time
	)
	err := s.Pool.QueryRow(ctx, `SELECT s.id, s.user_id, s.kind, s.mfa_verified_at, s.step_up_at, s.last_seen_at,
			s.absolute_expires_at, s.idle_expires_at
		FROM app.sessions s
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.idle_expires_at > $2 AND s.absolute_expires_at > $2`,
		crypto.HashToken(token), now).
		Scan(&p.SessionID, &p.UserID, &kind, &mfaAt, &stepUpAt, &lastSeen, &absolute, &idle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// account state comes from the users module (module boundary: auth never queries users tables)
	acct, err := users.ByID(ctx, s.Pool, p.UserID)
	if errors.Is(err, users.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if acct.Status != users.StatusActive || acct.IsSystem || acct.Kind != kind {
		return nil, nil
	}
	p.EmailVerified = acct.EmailVerified
	p.Kind = authz.AccountKind(kind)
	if mfaAt != nil {
		p.MFAVerifiedAt = *mfaAt
	}
	if stepUpAt != nil {
		p.StepUpAt = *stepUpAt
	}
	if p.Kind == authz.KindStaff {
		if p.Permissions, err = s.staffPermissions(ctx, p.UserID, now); err != nil {
			return nil, err
		}
	}
	if now.Sub(lastSeen) >= time.Minute {
		idleTimeout := s.Cfg.SessionIdleTimeout
		if p.Kind == authz.KindStaff {
			idleTimeout = s.Cfg.StaffSessionIdleTimeout
		}
		next := now.Add(idleTimeout)
		if next.After(absolute) {
			next = absolute
		}
		if _, err := s.Pool.Exec(ctx, `UPDATE app.sessions SET last_seen_at = $2, idle_expires_at = $3
			WHERE id = $1 AND revoked_at IS NULL`, p.SessionID, now, next); err != nil {
			return nil, err
		}
	}
	return &p, nil
}

// staffPermissions returns the permissions of active role assignments plus active break-glass grants.
func (s *Service) staffPermissions(ctx context.Context, userID string, now time.Time) (map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.code FROM app.role_assignments ra
			JOIN app.role_permissions rp ON rp.role_id = ra.role_id
			JOIN app.permissions p ON p.id = rp.permission_id
		WHERE ra.user_id = $1 AND ra.revoked_at IS NULL AND ra.valid_from <= $2 AND (ra.expires_at IS NULL OR ra.expires_at > $2)
		UNION
		SELECT p.code FROM app.break_glass_grants g JOIN app.permissions p ON p.id = g.permission_id
		WHERE g.staff_user_id = $1 AND g.revoked_at IS NULL AND g.starts_at <= $2 AND g.expires_at > $2`, userID, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	perms := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		perms[c] = true
	}
	return perms, rows.Err()
}

func ipString(ctx context.Context) any {
	a := authz.ClientIPFrom(ctx)
	if !a.IsValid() {
		return nil
	}
	return netip.PrefixFrom(a, a.BitLen()).String()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// maskIP keeps the network part only (IPv4 /24, IPv6 /48) for display in session lists.
func maskIP(ip *netip.Prefix) string {
	if ip == nil {
		return ""
	}
	a := ip.Addr()
	bits := 24
	if a.Is6() {
		bits = 48
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return ""
	}
	return p.String()
}

func userAgentSummary(ua string) string {
	ua = strings.TrimSpace(ua)
	if len(ua) > 120 {
		ua = ua[:120]
	}
	return ua
}
