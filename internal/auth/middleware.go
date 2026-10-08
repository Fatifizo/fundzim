package auth

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
)

// SessionMiddleware resolves the session cookie into a Principal. It never rejects a request: anonymous
// requests continue without a principal, and authorization happens per route. A database failure is
// remembered so protected routes answer 503 instead of pretending the user is anonymous (fail closed).
func (s *Service) SessionMiddleware() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(s.sessionCookie())
			if err != nil || c.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			p, rerr := s.resolveSession(r.Context(), c.Value)
			ctx := context.WithValue(r.Context(), resolvedKey{}, resolved{principal: p, err: rerr})
			switch {
			case rerr != nil:
				logging.FromContext(ctx, s.Logger).Error("session lookup failed", slog.String("error_category", "dependency"),
					slog.String("error", rerr.Error()))
			case p == nil:
				// unknown, expired or revoked: drop the stale cookies so the browser stops sending them
				s.clearSessionCookies(w)
			default:
				ctx = authz.WithPrincipal(ctx, p)
				l := logging.FromContext(ctx, s.Logger).With(slog.String("user_id", p.UserID), slog.String("session_id", p.SessionID))
				ctx = logging.WithContext(ctx, l)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	}
	return true
}

// CSRFMiddleware protects every unsafe request (interface-contracts §4.1):
//   - a present Origin header must equal APP_PUBLIC_URL, and Sec-Fetch-Site must not be cross-site
//     (covers login CSRF on pre-session endpoints too);
//   - when a session cookie is present, X-CSRF-Token must equal the session-bound token (also in the CSRF
//     cookie: double-submit, but validated against the server-side session, not just cookie equality).
//
// Webhook routes (provider signatures, no cookies) are exempt by path prefix.
func (s *Service) CSRFMiddleware() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isUnsafe(r.Method) || len(r.URL.Path) >= len("/api/v1/webhooks/") && r.URL.Path[:len("/api/v1/webhooks/")] == "/api/v1/webhooks/" {
				next.ServeHTTP(w, r)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != s.Cfg.PublicURL {
				httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "CSRF_ORIGIN_MISMATCH", "Request origin not allowed."))
				return
			}
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "CSRF_ORIGIN_MISMATCH", "Cross-site requests are not allowed."))
				return
			}
			if _, err := r.Cookie(s.sessionCookie()); err == nil {
				p := authz.PrincipalFrom(r.Context())
				got := r.Header.Get("X-CSRF-Token")
				if p != nil {
					want := s.csrfToken(p.SessionID)
					if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
						httpx.WriteError(w, r, s.Logger, errs.New(errs.Forbidden, "CSRF_TOKEN_INVALID", "Missing or invalid CSRF token. Reload the page and try again."))
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Authorize implements httpx.Authorizer: deny by default, ownership checks remain in handlers.
//   - Authenticated: any live session.
//   - User: a personal account (staff accounts never act as users).
//   - Staff: a staff session (MFA guaranteed); others get 404 so admin routes are not discoverable.
//   - Permission: a staff session holding the permission; a fresh step-up when the permission requires it.
func (s *Service) Authorize(r *http.Request, pol httpx.Policy) error {
	if res, ok := r.Context().Value(resolvedKey{}).(resolved); ok && res.err != nil {
		return errs.New(errs.Unavailable, errs.CodeServiceUnavailable, "The service is temporarily unavailable. Please retry shortly.")
	}
	p := authz.PrincipalFrom(r.Context())
	notFound := errs.New(errs.NotFound, errs.CodeRouteNotFound, "No such route.")
	switch pol.Kind {
	case httpx.KindPublic:
		return nil
	case httpx.KindAuthenticated:
		if p == nil {
			return errAuthRequired()
		}
		return nil
	case httpx.KindUser:
		if p == nil {
			return errAuthRequired()
		}
		if p.Kind != authz.KindUser {
			return errs.New(errs.Forbidden, "PERMISSION_DENIED", "This action is available to personal accounts only.")
		}
		return nil
	case httpx.KindStaff:
		if p == nil || p.Kind != authz.KindStaff {
			return notFound
		}
		return nil
	case httpx.KindPermission:
		if p == nil || p.Kind != authz.KindStaff {
			return notFound
		}
		if !p.Has(pol.Permission) {
			return errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission for this action.")
		}
		if s.requiresStepUp(pol.Permission) && !p.StepUpFresh(s.now(), s.Cfg.StepUpMaxAge) {
			return errStepUp()
		}
		return nil
	}
	return errs.New(errs.Forbidden, "PERMISSION_DENIED", "You do not have permission for this action.")
}

func errAuthRequired() error {
	return errs.New(errs.Unauthenticated, "AUTHENTICATION_REQUIRED", "Sign in to continue.")
}

func errStepUp() error {
	return errs.New(errs.Forbidden, "STEP_UP_REQUIRED", "Confirm it's you to continue.")
}

// requireStepUp is used by handlers whose route policy is not a permission (e.g. MFA changes).
func (s *Service) requireStepUp(p *authz.Principal) error {
	if !p.StepUpFresh(s.now(), s.Cfg.StepUpMaxAge) {
		return errStepUp()
	}
	return nil
}
