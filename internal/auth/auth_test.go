package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

func testService(t *testing.T) (*Service, *clock.Fake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC))
	s := &Service{Deps: Deps{Cfg: config.Auth{PublicURL: "https://fundzim.test", SessionCookieName: "__Host-fz_session",
		CookieSecure: true, StepUpMaxAge: 10 * time.Minute, CSRFSecret: config.Secret(strings.Repeat("k", 32))},
		Clock: clk, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		stepUpPerms: map[string]bool{"role.assign.approve": true}}
	return s, clk
}

func kindOf(err error) errs.Kind {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return -1
}

func codeOf(err error) string {
	var e *errs.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestAuthorizeMatrix(t *testing.T) {
	s, clk := testService(t)
	now := clk.Now()
	user := &authz.Principal{UserID: "u", SessionID: "s1", Kind: authz.KindUser, StepUpAt: now}
	staff := &authz.Principal{UserID: "st", SessionID: "s2", Kind: authz.KindStaff, StepUpAt: now,
		Permissions: map[string]bool{"user.view": true, "role.assign.approve": true}}
	staleStaff := &authz.Principal{UserID: "st", SessionID: "s3", Kind: authz.KindStaff, StepUpAt: now.Add(-time.Hour),
		Permissions: map[string]bool{"user.view": true, "role.assign.approve": true}}

	cases := []struct {
		name string
		p    *authz.Principal
		pol  httpx.Policy
		code string // "" = allowed
	}{
		{"public anon", nil, httpx.Public(), ""},
		{"authenticated anon", nil, httpx.Authenticated(), "AUTHENTICATION_REQUIRED"},
		{"authenticated user", user, httpx.Authenticated(), ""},
		{"user policy anon", nil, httpx.User(), "AUTHENTICATION_REQUIRED"},
		{"user policy staff", staff, httpx.User(), "PERMISSION_DENIED"},
		{"user policy user", user, httpx.User(), ""},
		{"staff policy anon hidden", nil, httpx.Staff(), errs.CodeRouteNotFound},
		{"staff policy user hidden", user, httpx.Staff(), errs.CodeRouteNotFound},
		{"staff policy staff", staff, httpx.Staff(), ""},
		{"permission user hidden", user, httpx.Permission("user.view"), errs.CodeRouteNotFound},
		{"permission missing", staff, httpx.Permission("payout.approve"), "PERMISSION_DENIED"},
		{"permission held", staff, httpx.Permission("user.view"), ""},
		{"permission step-up fresh", staff, httpx.Permission("role.assign.approve"), ""},
		{"permission step-up stale", staleStaff, httpx.Permission("role.assign.approve"), "STEP_UP_REQUIRED"},
		{"permission without step-up, stale ok", staleStaff, httpx.Permission("user.view"), ""},
		{"unknown policy kind denied", user, httpx.Policy{Kind: "bogus"}, "PERMISSION_DENIED"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/x", nil)
		if c.p != nil {
			r = r.WithContext(authz.WithPrincipal(r.Context(), c.p))
		}
		err := s.Authorize(r, c.pol)
		if got := codeOf(err); got != c.code {
			t.Errorf("%s: got %q (%v), want %q", c.name, got, err, c.code)
		}
	}

	// unknown step-up configuration fails closed: every permission requires a fresh step-up
	s.stepUpPerms = nil
	r := httptest.NewRequest("GET", "/x", nil).WithContext(authz.WithPrincipal(context.Background(), staleStaff))
	if codeOf(s.Authorize(r, httpx.Permission("user.view"))) != "STEP_UP_REQUIRED" {
		t.Error("nil step-up set must require step-up")
	}

	// a session lookup that failed on the database answers 503, never "anonymous"
	r = httptest.NewRequest("GET", "/x", nil)
	r = r.WithContext(context.WithValue(r.Context(), resolvedKey{}, resolved{err: errors.New("db down")}))
	if kindOf(s.Authorize(r, httpx.Authenticated())) != errs.Unavailable {
		t.Error("db failure must be 503")
	}
}

func csrfRequest(s *Service, method string, hdr map[string]string, withSession bool, p *authz.Principal) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/v1/me", strings.NewReader("{}"))
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if withSession {
		r.AddCookie(&http.Cookie{Name: s.sessionCookie(), Value: "x"})
	}
	if p != nil {
		r = r.WithContext(authz.WithPrincipal(r.Context(), p))
	}
	rec := httptest.NewRecorder()
	s.CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(rec, r)
	return rec
}

func TestCSRFMiddleware(t *testing.T) {
	s, _ := testService(t)
	p := &authz.Principal{UserID: "u", SessionID: "0190a0e0-0000-7000-8000-000000000001", Kind: authz.KindUser}
	good := s.csrfToken(p.SessionID)
	if good != s.csrfToken(p.SessionID) || good == s.csrfToken("other") || len(good) < 40 {
		t.Fatal("csrf token must be deterministic per session and differ between sessions")
	}
	cases := []struct {
		name    string
		method  string
		hdr     map[string]string
		session bool
		p       *authz.Principal
		want    int
	}{
		{"safe method passes", "GET", nil, true, p, 204},
		{"anonymous POST without origin passes", "POST", nil, false, nil, 204},
		{"foreign origin", "POST", map[string]string{"Origin": "https://evil.test"}, false, nil, 403},
		{"cross-site fetch", "POST", map[string]string{"Sec-Fetch-Site": "cross-site"}, false, nil, 403},
		{"same origin anonymous", "POST", map[string]string{"Origin": "https://fundzim.test"}, false, nil, 204},
		{"session without token", "POST", nil, true, p, 403},
		{"session wrong token", "DELETE", map[string]string{"X-CSRF-Token": "nope"}, true, p, 403},
		{"session other session's token", "PATCH", map[string]string{"X-CSRF-Token": s.csrfToken("other")}, true, p, 403},
		{"session right token", "PATCH", map[string]string{"X-CSRF-Token": good, "Origin": "https://fundzim.test"}, true, p, 204},
	}
	for _, c := range cases {
		if got := csrfRequest(s, c.method, c.hdr, c.session, c.p).Code; got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	// webhooks are exempt (provider signatures, no cookies)
	r := httptest.NewRequest("POST", "/api/v1/webhooks/psp", nil)
	r.Header.Set("Origin", "https://provider.test")
	rec := httptest.NewRecorder()
	s.CSRFMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(rec, r)
	if rec.Code != 204 {
		t.Errorf("webhook exempt: %d", rec.Code)
	}
}

func TestCookiesAreHardened(t *testing.T) {
	s, clk := testService(t)
	rec := httptest.NewRecorder()
	s.issueSessionCookies(rec, "tok", "sid", clk.Now().Add(time.Hour))
	byName := map[string]*http.Cookie{}
	for _, c := range rec.Result().Cookies() {
		byName[c.Name] = c
	}
	sess, csrf := byName["__Host-fz_session"], byName["__Host-fz_csrf"]
	if sess == nil || !sess.HttpOnly || !sess.Secure || sess.Path != "/" || sess.Domain != "" || sess.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie: %+v", sess)
	}
	if csrf == nil || csrf.HttpOnly || !csrf.Secure || csrf.Value != s.csrfToken("sid") {
		t.Fatalf("csrf cookie: %+v", csrf)
	}
	if m := byName["__Host-fz_mfa"]; m == nil || m.MaxAge >= 0 {
		t.Fatalf("pending MFA cookie must be cleared on login: %+v", m)
	}
}

func TestNormalizePhone(t *testing.T) {
	for in, want := range map[string]string{
		"0772123456":       "+263772123456",
		"+263 77 212 3456": "+263772123456",
		"263772123456":     "+263772123456", // country code without "+" is recognised
		"+447911123456":    "+447911123456",
		"+447700900123":    "", // Ofcom drama (fictional) range is not a valid number
		"12345":            "",
		"+263242700000":    "", // Harare landline: not mobile
	} {
		got, err := NormalizePhone(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q: expected rejection, got %s", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
}

func TestMaskEmailAndJustification(t *testing.T) {
	if MaskEmail("tariro@example.org") != "t***@example.org" || MaskEmail("bad") != "***" {
		t.Fatal("mask email")
	}
	if _, ok := justification("short"); ok {
		t.Fatal("justification under 10 chars accepted")
	}
	if j, ok := justification("  long enough reason  "); !ok || j != "long enough reason" {
		t.Fatal("justification trim")
	}
	if humanDuration(24*time.Hour) != "24 hours" || humanDuration(7*24*time.Hour) != "7 days" || humanDuration(30*time.Minute) != "30 minutes" {
		t.Fatal("humanDuration")
	}
}

func TestEmailLinksUseQueryTokenAndPublicURL(t *testing.T) {
	s, _ := testService(t)
	if got := s.link("/verify-email", "abc_-1"); got != "https://fundzim.test/verify-email?token=abc_-1" {
		t.Fatal(got)
	}
	if got := s.link("/forgot-password", ""); got != "https://fundzim.test/forgot-password" {
		t.Fatal(got)
	}
}
