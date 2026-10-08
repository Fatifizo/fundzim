//go:build integration

// Stage 4 identity: registration, verification, login, sessions, CSRF, password reset, MFA, phone
// verification, RBAC, staff maker-checker and organisation isolation — end to end against the REAL local
// stack. The API runs in-process (httptest) on the shared database and Valkey; emails are produced by the
// RUNNING worker container (outbox → River → SMTP) and read back from Mailpit, so these tests prove the
// whole pipeline. Requires: `docker compose up -d` with current images, migrations applied, .env loaded.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/auth"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/users"
)

const itPassword = "correct horse battery staple 42"

// commonPW is on the embedded common-passwords list, so the policy must reject it.
var commonPW = strings.Repeat("pass", 1) + "word1"

type itServer struct {
	srv *httptest.Server
	d   *app.Deps
	cfg config.Config
}

// newITServer starts the public handler in-process. Loopback is a trusted proxy so each test browser can
// present its own client IP (X-Forwarded-For) and per-IP limits do not interfere between tests.
func newITServer(t *testing.T, mutate func(*config.Config)) *itServer {
	t.Helper()
	need(t, "DATABASE_URL", "REDIS_URL")
	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTP.TrustedProxyCIDRs = []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")}
	if mutate != nil {
		mutate(&cfg)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("FUNDZIM_IT_VERBOSE") == "1" {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	d, err := app.NewDeps(context.Background(), cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(app.PublicHandler(d, app.NewRouter(d, d.Checker())))
	t.Cleanup(func() { s.Close(); d.Close() })
	return &itServer{srv: s, d: d, cfg: cfg}
}

var ipSeq atomic.Uint32

type browser struct {
	t  *testing.T
	s  *itServer
	c  *http.Client
	ip string
}

func (s *itServer) browser(t *testing.T) *browser {
	jar, _ := cookiejar.New(nil)
	n := ipSeq.Add(1) + uint32(time.Now().UnixNano()%50000)
	ip := fmt.Sprintf("198.18.%d.%d", (n/250)%250, n%250+1)
	return &browser{t: t, s: s, c: &http.Client{Jar: jar, Timeout: 30 * time.Second}, ip: ip}
}

type apiResp struct {
	Status int
	Data   json.RawMessage
	Error  struct {
		Code    string `json:"code"`
		Details []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
		} `json:"details"`
	}
	Header http.Header
}

func (r apiResp) field(t *testing.T, name string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Data, &m); err != nil {
		t.Fatalf("decode data: %v (%s)", err, r.Data)
	}
	v, _ := m[name].(string)
	return v
}

func (b *browser) cookie(name string) string {
	u, _ := url.Parse(b.s.srv.URL)
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// do sends a JSON request with the CSRF header taken from the CSRF cookie (as the web client does).
func (b *browser) do(method, path string, body any, hdr ...string) apiResp {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		rd = bytes.NewReader(buf)
	}
	req, _ := http.NewRequest(method, b.s.srv.URL+"/api/v1"+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Forwarded-For", b.ip)
	if c := b.cookie("fz_csrf"); c != "" {
		req.Header.Set("X-CSRF-Token", c)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i+1] == "" {
			req.Header.Del(hdr[i])
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := apiResp{Status: resp.StatusCode, Header: resp.Header}
	var env struct {
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &env); err != nil {
			b.t.Fatalf("%s %s: non-JSON response %d: %s", method, path, resp.StatusCode, raw)
		}
		out.Data = env.Data
		if len(env.Error) > 0 {
			_ = json.Unmarshal(env.Error, &out.Error)
		}
	}
	return out
}

func (b *browser) expect(r apiResp, status int, code string) {
	b.t.Helper()
	if r.Status != status || (code != "" && r.Error.Code != code) {
		b.t.Fatalf("want %d %s, got %d %s (%s)", status, code, r.Status, r.Error.Code, r.Data)
	}
}

func uniqueEmail(prefix string) string {
	return fmt.Sprintf("it-%s-%s@example.test", prefix, strings.ReplaceAll(ids.New()[24:], "-", ""))
}

// --- Mailpit ------------------------------------------------------------------------------------------

// mailTo waits for the newest message to `to` whose subject contains subj and returns its text body.
func mailTo(t *testing.T, to, subj string) string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(mailpitURL() + "/api/v1/search?query=" + url.QueryEscape("to:"+to) + "&limit=50")
		if err != nil {
			t.Fatalf("mailpit: %v", err)
		}
		var list struct {
			Messages []mailpitMessage `json:"messages"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		for _, m := range list.Messages {
			if strings.Contains(m.Subject, subj) {
				r2, err := http.Get(mailpitURL() + "/api/v1/message/" + m.ID)
				if err != nil {
					t.Fatal(err)
				}
				var full struct {
					Text string `json:"Text"`
				}
				_ = json.NewDecoder(r2.Body).Decode(&full)
				r2.Body.Close()
				return full.Text
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("no Mailpit message to %s with subject %q (is the worker running current code?)", to, subj)
	return ""
}

func mailCount(t *testing.T, to string) int {
	t.Helper()
	resp, err := http.Get(mailpitURL() + "/api/v1/search?query=" + url.QueryEscape("to:"+to) + "&limit=50")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Messages []mailpitMessage `json:"messages"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&list)
	return len(list.Messages)
}

var tokenRE = regexp.MustCompile(`token=([A-Za-z0-9_-]{43})`)

func tokenFrom(t *testing.T, text string) string {
	t.Helper()
	m := tokenRE.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no token link in email: %q", text)
	}
	return m[1]
}

// --- TOTP ----------------------------------------------------------------------------------------------

var (
	totpMu   sync.Mutex
	lastStep = map[string]int64{}
)

// totpCode returns a valid code for a time step not used before for this secret (the server rejects
// replays of a step), waiting for the next step when the ±1 window is exhausted.
func totpCode(t *testing.T, secret string) string {
	t.Helper()
	totpMu.Lock()
	defer totpMu.Unlock()
	for {
		cur := time.Now().Unix() / 30
		for s := cur - 1; s <= cur+1; s++ {
			if s > lastStep[secret] {
				lastStep[secret] = s
				code, err := totp.GenerateCodeCustom(secret, time.Unix(s*30+1, 0), totp.ValidateOpts{Period: 30, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
				if err != nil {
					t.Fatal(err)
				}
				return code
			}
		}
		time.Sleep(time.Second)
	}
}

// --- flows ---------------------------------------------------------------------------------------------

func (b *browser) register(email, pw, name string) {
	b.t.Helper()
	b.expect(b.do("POST", "/auth/register", map[string]any{"email": email, "password": pw, "display_name": name, "accept_terms": true}), 202, "")
}

func (b *browser) registerVerified(email string) {
	b.t.Helper()
	b.register(email, itPassword, "Tariro Moyo")
	tok := tokenFrom(b.t, mailTo(b.t, email, "Confirm your FundZim email"))
	b.expect(b.do("POST", "/auth/verify-email", map[string]string{"token": tok}), 200, "")
}

func (b *browser) login(email, pw string) apiResp {
	b.t.Helper()
	return b.do("POST", "/auth/login", map[string]string{"email": email, "password": pw})
}

func (b *browser) mustLogin(email, pw string) {
	b.t.Helper()
	r := b.login(email, pw)
	if r.Status != 200 || r.field(b.t, "status") != "authenticated" {
		b.t.Fatalf("login: %d %s %s", r.Status, r.Error.Code, r.Data)
	}
}

// enableMFA enrols and confirms TOTP and returns (secret, recovery codes).
func (b *browser) enableMFA() (string, []string) {
	b.t.Helper()
	r := b.do("POST", "/me/mfa/enroll", nil)
	b.expect(r, 200, "")
	secret, enrollment := r.field(b.t, "secret"), r.field(b.t, "enrollment_id")
	r = b.do("POST", "/me/mfa/confirm", map[string]string{"enrollment_id": enrollment, "code": totpCode(b.t, secret)})
	b.expect(r, 200, "")
	var out struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	_ = json.Unmarshal(r.Data, &out)
	if len(out.RecoveryCodes) != 10 {
		b.t.Fatalf("recovery codes: %v", out.RecoveryCodes)
	}
	return secret, out.RecoveryCodes
}

// ===== tests ==============================================================================================

func TestIdentityRegistrationVerificationAndLogin(t *testing.T) {
	s := newITServer(t, nil)
	b := s.browser(t)
	email := uniqueEmail("reg")

	// policy violations are explained, never silently accepted
	r := b.do("POST", "/auth/register", map[string]any{"email": email, "password": commonPW, "display_name": "T", "accept_terms": true})
	b.expect(r, 422, "")
	// unknown fields are rejected (no mass assignment of e.g. account_kind)
	b.expect(b.do("POST", "/auth/register", map[string]any{"email": email, "password": itPassword, "display_name": "T",
		"accept_terms": true, "account_kind": "STAFF"}), 422, "VALIDATION_FAILED")
	b.expect(b.do("POST", "/auth/register", map[string]any{"email": email, "password": itPassword, "display_name": "T"}), 422, "VALIDATION_FAILED")

	b.register(email, itPassword, "Tariro Moyo")
	// a second registration with the same address answers identically (no enumeration) and emails the owner
	other := s.browser(t)
	other.register(strings.ToUpper(email), itPassword, "Someone Else")
	mailTo(t, email, "Someone tried to register")

	// unverified accounts may sign in; /me reports the state
	b.mustLogin(email, itPassword)
	r = b.do("GET", "/me", nil)
	b.expect(r, 200, "")
	var me auth.Me
	_ = json.Unmarshal(r.Data, &me)
	if me.EmailVerified || me.AccountKind != "USER" || me.Email != email {
		t.Fatalf("me: %+v", me)
	}
	if !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("me must not be cached: %q", r.Header.Get("Cache-Control"))
	}

	tok := tokenFrom(t, mailTo(t, email, "Confirm your FundZim email"))
	b.expect(b.do("POST", "/auth/verify-email", map[string]string{"token": tok}), 200, "")
	b.expect(b.do("POST", "/auth/verify-email", map[string]string{"token": tok}), 400, "TOKEN_INVALID") // single use
	b.expect(b.do("POST", "/auth/verify-email", map[string]string{"token": strings.Repeat("A", 43)}), 400, "TOKEN_INVALID")
	_ = json.Unmarshal(b.do("GET", "/me", nil).Data, &me)
	if !me.EmailVerified {
		t.Fatal("email should be verified")
	}

	// the accepted terms version is recorded (LR-022 acceptance versioning)
	p := pool(t, os.Getenv("DATABASE_URL"))
	var termsVersion string
	if err := p.QueryRow(ctx(t), `SELECT version FROM app.user_terms_acceptances WHERE user_id = $1 AND document = 'TERMS_OF_USE'`,
		me.ID).Scan(&termsVersion); err != nil || termsVersion != users.CurrentTermsVersion {
		t.Fatalf("terms acceptance: %q %v", termsVersion, err)
	}
	// the raw token is never stored: only its SHA-256 hash exists
	var n int
	if err := p.QueryRow(ctx(t), `SELECT count(*) FROM app.auth_tokens WHERE token_hash = convert_to($1, 'UTF8')`, tok).Scan(&n); err != nil || n != 0 {
		t.Fatalf("raw token found in database: %d %v", n, err)
	}
}

func TestIdentityLoginGenericErrorsAndCSRF(t *testing.T) {
	s := newITServer(t, nil)
	b := s.browser(t)
	email := uniqueEmail("login")
	b.registerVerified(email)

	// wrong password and unknown account are indistinguishable
	b.expect(b.login(email, "wrong password entirely"), 401, "INVALID_CREDENTIALS")
	b.expect(b.login(uniqueEmail("nobody"), itPassword), 401, "INVALID_CREDENTIALS")

	b.mustLogin(email, itPassword)
	sess := b.cookie("fz_session")
	if sess == "" || b.cookie("fz_csrf") == "" {
		t.Fatal("session and CSRF cookies expected")
	}
	// the session token is stored hashed only
	p := pool(t, os.Getenv("DATABASE_URL"))
	var n int
	_ = p.QueryRow(ctx(t), `SELECT count(*) FROM app.sessions WHERE token_hash = convert_to($1, 'UTF8')`, sess).Scan(&n)
	if n != 0 {
		t.Fatal("raw session token stored")
	}

	// CSRF: missing token, wrong token, foreign Origin, cross-site fetch metadata
	b.expect(b.do("PATCH", "/me", map[string]string{"display_name": "X Y"}, "X-CSRF-Token", ""), 403, "CSRF_TOKEN_INVALID")
	b.expect(b.do("PATCH", "/me", map[string]string{"display_name": "X Y"}, "X-CSRF-Token", "forged"), 403, "CSRF_TOKEN_INVALID")
	b.expect(b.do("PATCH", "/me", map[string]string{"display_name": "X Y"}, "Origin", "https://evil.example"), 403, "CSRF_ORIGIN_MISMATCH")
	b.expect(b.do("POST", "/auth/login", map[string]string{"email": email, "password": itPassword}, "Sec-Fetch-Site", "cross-site"), 403, "CSRF_ORIGIN_MISMATCH")
	b.expect(b.do("PATCH", "/me", map[string]string{"display_name": "Rudo Chari"}, "Origin", s.cfg.Auth.PublicURL), 200, "")

	// another session's CSRF token is not valid for this session
	b2 := s.browser(t)
	b2.mustLogin(email, itPassword)
	b.expect(b.do("PATCH", "/me", map[string]string{"display_name": "X Y"}, "X-CSRF-Token", b2.cookie("fz_csrf")), 403, "CSRF_TOKEN_INVALID")

	// login throttling per account (5 per 15 minutes) answers 429, and a correct password is refused then too
	email2 := uniqueEmail("throttle")
	b3 := s.browser(t)
	b3.registerVerified(email2)
	for i := 0; i < 5; i++ {
		b3.expect(b3.login(email2, "not the password"), 401, "INVALID_CREDENTIALS")
	}
	r := b3.login(email2, itPassword)
	b3.expect(r, 429, "RATE_LIMITED")
}

func TestIdentityEmailChangeAndResend(t *testing.T) {
	s := newITServer(t, nil)
	email := uniqueEmail("chg")
	a := s.browser(t)
	a.register(email, itPassword, "Change Me")
	a.expect(a.do("POST", "/auth/resend-verification", map[string]string{"email": email}), 202, "")
	a.expect(a.do("POST", "/auth/resend-verification", map[string]string{"email": uniqueEmail("nobody")}), 202, "") // generic
	// the resend invalidated the first link: only the newest token works
	deadline := time.Now().Add(20 * time.Second)
	for mailCount(t, email) < 2 && time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
	}
	tok := tokenFrom(t, mailTo(t, email, "Confirm your FundZim email"))
	a.expect(a.do("POST", "/auth/verify-email", map[string]string{"token": tok}), 200, "")
	a.mustLogin(email, itPassword)

	taken := uniqueEmail("taken")
	s.browser(t).register(taken, itPassword, "Other Owner")
	newEmail := uniqueEmail("new")
	a.expect(a.do("POST", "/me/email-change", map[string]string{"new_email": newEmail, "password": "wrong password here"}), 401, "INVALID_CREDENTIALS")
	a.expect(a.do("POST", "/me/email-change", map[string]string{"new_email": taken, "password": itPassword}), 202, "") // no disclosure
	a.expect(a.do("POST", "/me/email-change", map[string]string{"new_email": newEmail, "password": itPassword}), 202, "")
	mailTo(t, email, "is being changed")
	tok = tokenFrom(t, mailTo(t, newEmail, "Confirm your new FundZim email"))
	// still the old address until confirmed
	var me auth.Me
	_ = json.Unmarshal(a.do("GET", "/me", nil).Data, &me)
	if me.Email != email {
		t.Fatalf("email changed before confirmation: %s", me.Email)
	}
	a.expect(a.do("POST", "/auth/verify-email", map[string]string{"token": tok}), 200, "")
	a.expect(a.do("GET", "/me", nil), 401, "") // changing the sign-in address revokes every session
	b := s.browser(t)
	b.expect(b.login(email, itPassword), 401, "INVALID_CREDENTIALS")
	b.mustLogin(newEmail, itPassword)
	me = auth.Me{}
	_ = json.Unmarshal(b.do("GET", "/me", nil).Data, &me)
	if me.Email != newEmail || !me.EmailVerified {
		t.Fatalf("after change: %+v", me)
	}
}

func TestIdentitySessionsRotationLogoutAndIDOR(t *testing.T) {
	s := newITServer(t, nil)
	email := uniqueEmail("sess")
	a := s.browser(t)
	a.registerVerified(email)
	a.mustLogin(email, itPassword)
	first := a.cookie("fz_session")

	// re-authenticating rotates the session (fixation defence): the old token stops working
	a.mustLogin(email, itPassword)
	if a.cookie("fz_session") == first {
		t.Fatal("session token not rotated on login")
	}
	stale := s.browser(t)
	u, _ := url.Parse(s.srv.URL)
	stale.c.Jar.SetCookies(u, []*http.Cookie{{Name: "fz_session", Value: first, Path: "/"}})
	stale.expect(stale.do("GET", "/me", nil), 401, "AUTHENTICATION_REQUIRED")

	// a second device; list shows both; revoke it from the first
	b := s.browser(t)
	b.mustLogin(email, itPassword)
	r := a.do("GET", "/me/sessions", nil)
	a.expect(r, 200, "")
	var list []struct {
		ID      string `json:"id"`
		Current bool   `json:"current"`
		IP      string `json:"ip_masked"`
	}
	_ = json.Unmarshal(r.Data, &list)
	if len(list) != 2 {
		t.Fatalf("sessions: %+v", list)
	}
	var otherID string
	for _, x := range list {
		if !x.Current {
			otherID = x.ID
		}
		if strings.Count(x.IP, ".") == 3 && !strings.HasSuffix(x.IP, ".0/24") {
			t.Fatalf("IP not masked: %s", x.IP)
		}
	}

	// IDOR: another user cannot see or revoke these sessions
	mallory := s.browser(t)
	mEmail := uniqueEmail("mallory")
	mallory.registerVerified(mEmail)
	mallory.mustLogin(mEmail, itPassword)
	mallory.expect(mallory.do("DELETE", "/me/sessions/"+otherID, nil), 404, "SESSION_NOT_FOUND")
	b.expect(b.do("GET", "/me", nil), 200, "")

	a.expect(a.do("DELETE", "/me/sessions/"+otherID, nil), 204, "")
	b.expect(b.do("GET", "/me", nil), 401, "AUTHENTICATION_REQUIRED")

	// logout-all
	c := s.browser(t)
	c.mustLogin(email, itPassword)
	a.expect(a.do("POST", "/auth/logout-all", nil), 204, "")
	c.expect(c.do("GET", "/me", nil), 401, "")
	a.expect(a.do("GET", "/me", nil), 401, "")

	// logout
	a.mustLogin(email, itPassword)
	a.expect(a.do("POST", "/auth/logout", nil), 204, "")
	a.expect(a.do("GET", "/me", nil), 401, "AUTHENTICATION_REQUIRED")
	r = a.do("GET", "/auth/session", nil)
	if r.Status != 200 || !strings.Contains(string(r.Data), `"authenticated":false`) {
		t.Fatalf("session info after logout: %d %s", r.Status, r.Data)
	}
}

func TestIdentityPasswordResetAndChange(t *testing.T) {
	s := newITServer(t, nil)
	email := uniqueEmail("reset")
	a := s.browser(t)
	a.registerVerified(email)
	a.mustLogin(email, itPassword)

	anon := s.browser(t)
	anon.expect(anon.do("POST", "/auth/forgot-password", map[string]string{"email": email}), 202, "")
	anon.expect(anon.do("POST", "/auth/forgot-password", map[string]string{"email": uniqueEmail("ghost")}), 202, "") // generic
	tok := tokenFrom(t, mailTo(t, email, "Reset your FundZim password"))
	anon.expect(anon.do("POST", "/auth/reset-password", map[string]string{"token": tok, "new_password": commonPW}), 422, "PASSWORD_POLICY_VIOLATION")
	const newPW = "a completely new passphrase 77"
	anon.expect(anon.do("POST", "/auth/reset-password", map[string]string{"token": tok, "new_password": newPW}), 200, "")
	anon.expect(anon.do("POST", "/auth/reset-password", map[string]string{"token": tok, "new_password": newPW + "x"}), 400, "TOKEN_INVALID")

	a.expect(a.do("GET", "/me", nil), 401, "") // reset revoked every session
	mailTo(t, email, "Your FundZim password was changed")
	anon.expect(anon.login(email, itPassword), 401, "INVALID_CREDENTIALS")
	anon.mustLogin(email, newPW)

	// change password: needs the current one; other sessions are revoked, this one survives
	other := s.browser(t)
	other.mustLogin(email, newPW)
	anon.expect(anon.do("POST", "/me/password", map[string]string{"current_password": "nope nope nope", "new_password": itPassword}), 401, "INVALID_CREDENTIALS")
	anon.expect(anon.do("POST", "/me/password", map[string]string{"current_password": newPW, "new_password": itPassword}), 204, "")
	anon.expect(anon.do("GET", "/me", nil), 200, "")
	other.expect(other.do("GET", "/me", nil), 401, "")

	// stored password is an Argon2id PHC string
	p := pool(t, os.Getenv("DATABASE_URL"))
	var phc string
	if err := p.QueryRow(ctx(t), `SELECT c.password_hash FROM app.password_credentials c JOIN app.user_emails e ON e.user_id = c.user_id
		WHERE e.email_normalized = $1 AND e.is_login AND c.superseded_at IS NULL`, email).Scan(&phc); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$") || strings.Contains(phc, itPassword) {
		t.Fatalf("unexpected hash format")
	}
}

func TestIdentityMFAEnrollLoginRecovery(t *testing.T) {
	s := newITServer(t, nil)
	email := uniqueEmail("mfa")
	a := s.browser(t)
	a.registerVerified(email)
	a.mustLogin(email, itPassword)
	secret, codes := a.enableMFA()
	mailTo(t, email, "Security alert: two-step verification")

	// password alone no longer signs in: a challenge is issued and no session exists yet
	b := s.browser(t)
	r := b.login(email, itPassword)
	if r.Status != 200 || r.field(t, "status") != "mfa_required" || b.cookie("fz_session") != "" || b.cookie("fz_mfa") == "" {
		t.Fatalf("expected MFA challenge: %d %s", r.Status, r.Data)
	}
	b.expect(b.do("GET", "/me", nil), 401, "")
	b.expect(b.do("POST", "/auth/mfa/verify", map[string]string{"code": "000000"}), 401, "MFA_CODE_INVALID")
	b.expect(b.do("POST", "/auth/mfa/verify", map[string]string{"code": totpCode(t, secret)}), 200, "")
	b.expect(b.do("GET", "/me", nil), 200, "")

	// recovery code: works once
	c := s.browser(t)
	c.login(email, itPassword)
	c.expect(c.do("POST", "/auth/mfa/recovery", map[string]string{"recovery_code": codes[0]}), 200, "")
	d := s.browser(t)
	d.login(email, itPassword)
	d.expect(d.do("POST", "/auth/mfa/recovery", map[string]string{"recovery_code": codes[0]}), 401, "MFA_CODE_INVALID")

	// a challenge dies after 5 wrong codes (the 6th answer is refused even with a correct code)
	e := s.browser(t)
	e.login(email, itPassword)
	for i := 0; i < 4; i++ {
		e.do("POST", "/auth/mfa/verify", map[string]string{"code": "111111"})
	}
	r = e.do("POST", "/auth/mfa/verify", map[string]string{"code": "111111"})
	if r.Status != 401 && r.Status != 429 {
		t.Fatalf("5th wrong code: %d %s", r.Status, r.Error.Code)
	}
	r = e.do("POST", "/auth/mfa/verify", map[string]string{"code": totpCode(t, secret)})
	if r.Status == 200 {
		t.Fatal("challenge must be dead after 5 failures")
	}

	// MFA secrets are stored encrypted, recovery codes hashed
	p := pool(t, os.Getenv("DATABASE_URL"))
	var ct []byte
	if err := p.QueryRow(ctx(t), `SELECT m.totp_secret_ciphertext FROM app.mfa_methods m JOIN app.user_emails e ON e.user_id = m.user_id
		WHERE e.email_normalized = $1 AND e.is_login AND m.confirmed_at IS NOT NULL AND m.disabled_at IS NULL`, email).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte(secret)) {
		t.Fatal("TOTP secret stored in plaintext")
	}
	var plain int
	_ = p.QueryRow(ctx(t), `SELECT count(*) FROM app.recovery_codes WHERE convert_from(code_hash, 'SQL_ASCII') LIKE '%' || $1 || '%'`, codes[1]).Scan(&plain)
	if plain != 0 {
		t.Fatal("recovery code stored in plaintext")
	}

	// disable needs step-up + a code (the per-account login limit — 5 attempts per 15 minutes, counting
	// successes — is exhausted by now, so the state is checked instead of signing in again)
	b.expect(b.do("POST", "/me/mfa/disable", map[string]string{"code": totpCode(t, secret)}), 204, "")
	r = b.do("GET", "/me/security", nil)
	if !strings.Contains(string(r.Data), `"mfa_enabled":false`) {
		t.Fatalf("security after disable: %s", r.Data)
	}
}

func TestIdentityPhoneVerification(t *testing.T) {
	s := newITServer(t, nil)
	email := uniqueEmail("phone")
	a := s.browser(t)
	a.registerVerified(email)
	a.mustLogin(email, itPassword)

	a.expect(a.do("POST", "/me/phone/verify-request", map[string]string{"phone": "12345"}), 422, "VALIDATION_FAILED")
	// a unique Econet-format number per run: 077 + 7 digits
	suffix := fmt.Sprintf("%07d", time.Now().UnixNano()%10_000_000)
	local := "077" + suffix
	e164 := "+26377" + suffix
	r := a.do("POST", "/me/phone/verify-request", map[string]string{"phone": local})
	a.expect(r, 202, "")
	if !strings.Contains(string(r.Data), "****") {
		t.Fatalf("phone must be masked in the response: %s", r.Data)
	}
	text := mailTo(t, strings.TrimPrefix(e164, "+")+"@sms.dev.invalid", "")
	code := regexp.MustCompile(`\b(\d{6})\b`).FindStringSubmatch(text)
	if code == nil {
		t.Fatalf("no code in dev SMS: %q", text)
	}
	a.expect(a.do("POST", "/me/phone/verify-confirm", map[string]string{"phone": local, "code": "000000"}), 422, "OTP_INVALID")
	a.expect(a.do("POST", "/me/phone/verify-confirm", map[string]string{"phone": e164, "code": code[1]}), 200, "")
	a.expect(a.do("POST", "/me/phone/verify-confirm", map[string]string{"phone": e164, "code": code[1]}), 422, "OTP_INVALID") // single use
	var me auth.Me
	_ = json.Unmarshal(a.do("GET", "/me", nil).Data, &me)
	if !me.PhoneVerified || me.PhoneMasked == nil || strings.Contains(*me.PhoneMasked, suffix) {
		t.Fatalf("phone state: %+v", me)
	}
	// the OTP is never stored in plaintext
	p := pool(t, os.Getenv("DATABASE_URL"))
	var n int
	_ = p.QueryRow(ctx(t), `SELECT count(*) FROM app.otp_challenges WHERE code_hmac = convert_to($1, 'UTF8')`, code[1]).Scan(&n)
	if n != 0 {
		t.Fatal("plaintext OTP stored")
	}
}

func TestIdentityConcurrency(t *testing.T) {
	s := newITServer(t, nil)

	// 10 simultaneous registrations of one address create exactly one account
	email := uniqueEmail("race")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := s.browser(t)
			r := b.do("POST", "/auth/register", map[string]any{"email": email, "password": itPassword, "display_name": "Race", "accept_terms": true})
			if r.Status != 202 && r.Status != 429 {
				t.Errorf("register: %d %s", r.Status, r.Error.Code)
			}
		}()
	}
	wg.Wait()
	p := pool(t, os.Getenv("DATABASE_URL"))
	var n int
	if err := p.QueryRow(ctx(t), `SELECT count(*) FROM app.user_emails WHERE email_normalized = $1 AND is_login`, email).Scan(&n); err != nil || n != 1 {
		t.Fatalf("accounts for %s: %d %v", email, n, err)
	}

	// a verification token used concurrently succeeds exactly once
	tok := tokenFrom(t, mailTo(t, email, "Confirm your FundZim email"))
	var ok atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := s.browser(t)
			if r := b.do("POST", "/auth/verify-email", map[string]string{"token": tok}); r.Status == 200 {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("token consumed %d times", ok.Load())
	}

	// a recovery code used concurrently by two pending challenges succeeds once
	a := s.browser(t)
	a.mustLogin(email, itPassword)
	_, codes := a.enableMFA()
	b1, b2 := s.browser(t), s.browser(t)
	b1.login(email, itPassword)
	b2.login(email, itPassword)
	ok.Store(0)
	for _, b := range []*browser{b1, b2} {
		wg.Add(1)
		go func(b *browser) {
			defer wg.Done()
			if r := b.do("POST", "/auth/mfa/recovery", map[string]string{"recovery_code": codes[3]}); r.Status == 200 {
				ok.Add(1)
			}
		}(b)
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Fatalf("recovery code accepted %d times", ok.Load())
	}
}

// resetSuperAdmins revokes any active SUPER_ADMIN assignment so the bootstrap ceremony can run in this
// shared development database (revocation is the only mutation role_assignments allows).
func resetSuperAdmins(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	p := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	if _, err := p.Exec(ctx(t), `UPDATE app.role_assignments SET revoked_at = now(), revoked_by = $1, revoke_reason = 'integration test reset'
		WHERE revoked_at IS NULL AND role_id = md5('role:SUPER_ADMIN')::uuid`, auth.SystemActorID); err != nil {
		t.Fatal(err)
	}
}

// acceptStaffInvitation completes an invitation from the email and returns the TOTP secret.
func acceptStaffInvitation(t *testing.T, s *itServer, email string) string {
	t.Helper()
	tok := tokenFrom(t, mailTo(t, email, "staff account invitation"))
	b := s.browser(t)
	r := b.do("POST", "/auth/staff-invitation/start", map[string]string{"token": tok})
	b.expect(r, 200, "")
	secret, enrollment := r.field(t, "secret"), r.field(t, "enrollment_id")
	b.expect(b.do("POST", "/auth/staff-invitation/finish", map[string]string{"token": tok, "enrollment_id": enrollment,
		"code": "123456", "password": itPassword}), 401, "MFA_CODE_INVALID")
	b.expect(b.do("POST", "/auth/staff-invitation/finish", map[string]string{"token": tok, "enrollment_id": enrollment,
		"code": totpCode(t, secret), "password": itPassword}), 200, "")
	b.expect(b.do("POST", "/auth/staff-invitation/start", map[string]string{"token": tok}), 400, "TOKEN_INVALID")
	return secret
}

func (b *browser) staffLogin(email, secret string) {
	b.t.Helper()
	r := b.login(email, itPassword)
	if r.Status != 200 || r.field(b.t, "status") != "mfa_required" {
		b.t.Fatalf("staff login must require MFA: %d %s", r.Status, r.Data)
	}
	b.expect(b.do("POST", "/auth/mfa/verify", map[string]string{"code": totpCode(b.t, secret)}), 200, "")
}

func TestIdentityRBACStaffMakerChecker(t *testing.T) {
	s := newITServer(t, nil)
	resetSuperAdmins(t)

	emailA, emailB := uniqueEmail("sa-a"), uniqueEmail("sa-b")
	got, err := s.d.Auth.BootstrapSuperAdmins(ctx(t), auth.BootstrapAdmin{Email: emailA, DisplayName: "Admin A"},
		auth.BootstrapAdmin{Email: emailB, DisplayName: "Admin B"}, "integration test bootstrap ceremony")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.d.Auth.BootstrapSuperAdmins(ctx(t), auth.BootstrapAdmin{Email: uniqueEmail("x"), DisplayName: "X"},
		auth.BootstrapAdmin{Email: uniqueEmail("y"), DisplayName: "Y"}, "second ceremony must be refused"); err != auth.ErrAlreadyBootstrapped {
		t.Fatalf("second bootstrap: %v", err)
	}
	secretA := acceptStaffInvitation(t, s, emailA)
	secretB := acceptStaffInvitation(t, s, emailB)
	adminA, adminB := s.browser(t), s.browser(t)
	adminA.staffLogin(emailA, secretA)
	adminB.staffLogin(emailB, secretB)
	var me auth.Me
	_ = json.Unmarshal(adminA.do("GET", "/me", nil).Data, &me)
	if me.AccountKind != "STAFF" || len(me.Roles) != 1 || me.Roles[0] != "SUPER_ADMIN" || me.ID != got[0] {
		t.Fatalf("admin A: %+v", me)
	}

	// staff cannot use personal-account routes, users cannot discover admin routes
	adminA.expect(adminA.do("POST", "/organisations", map[string]string{"display_name": "Staff Org", "org_type": "OTHER"}), 403, "PERMISSION_DENIED")
	userEmail := uniqueEmail("plain")
	u := s.browser(t)
	u.registerVerified(userEmail)
	u.mustLogin(userEmail, itPassword)
	u.expect(u.do("GET", "/admin/role-assignment-requests", nil), 404, "ROUTE_NOT_FOUND")
	u.expect(u.do("POST", "/admin/staff", map[string]string{"email": uniqueEmail("z"), "display_name": "Z", "justification": "trying to escalate"}), 404, "")
	anon := s.browser(t)
	anon.expect(anon.do("GET", "/admin/role-assignment-requests", nil), 404, "")
	// staff password-only step-up is refused (TOTP required)
	adminA.expect(adminA.do("POST", "/auth/step-up/verify", map[string]string{"password": itPassword}), 401, "STEP_UP_FAILED")

	// A invites staff C; C accepts; A requests REVIEWER for C
	emailC := uniqueEmail("staff-c")
	r := adminA.do("POST", "/admin/staff", map[string]string{"email": emailC, "display_name": "Reviewer C", "justification": "new campaign reviewer hire"})
	adminA.expect(r, 201, "")
	cID := r.field(t, "user_id")
	secretC := acceptStaffInvitation(t, s, emailC)
	c := s.browser(t)
	c.staffLogin(emailC, secretC)
	c.expect(c.do("GET", "/admin/users/"+got[0], nil), 403, "PERMISSION_DENIED") // staff without user.view

	// nobody requests roles for themselves
	adminA.expect(adminA.do("POST", "/admin/role-assignment-requests", map[string]string{"user_id": got[0], "role_code": "FINANCE",
		"action": "GRANT", "justification": "self escalation attempt"}), 403, "SELF_REQUEST_FORBIDDEN")
	r = adminA.do("POST", "/admin/role-assignment-requests", map[string]string{"user_id": cID, "role_code": "REVIEWER",
		"action": "GRANT", "justification": "joins the campaign review team"})
	adminA.expect(r, 201, "")
	reqID := r.field(t, "id")
	adminA.expect(adminA.do("POST", "/admin/role-assignment-requests", map[string]string{"user_id": cID, "role_code": "REVIEWER",
		"action": "GRANT", "justification": "duplicate request attempt"}), 409, "ROLE_REQUEST_PENDING")

	// maker cannot approve; a user cannot; the second admin can
	adminA.expect(adminA.do("POST", "/admin/role-assignment-requests/"+reqID+"/approve", map[string]string{}), 403, "SELF_APPROVAL_FORBIDDEN")
	u.expect(u.do("POST", "/admin/role-assignment-requests/"+reqID+"/approve", map[string]string{}), 404, "")
	adminB.expect(adminB.do("POST", "/admin/role-assignment-requests/"+reqID+"/approve", map[string]string{"reason": "approved per hiring ticket"}), 200, "")
	adminB.expect(adminB.do("POST", "/admin/role-assignment-requests/"+reqID+"/approve", map[string]string{}), 409, "ROLE_REQUEST_DECIDED")

	// privilege change revoked C's sessions; after signing in again C holds the role
	c.expect(c.do("GET", "/me", nil), 401, "")
	c.staffLogin(emailC, secretC)
	_ = json.Unmarshal(c.do("GET", "/me", nil).Data, &me)
	if len(me.Roles) != 1 || me.Roles[0] != "REVIEWER" {
		t.Fatalf("C roles: %+v", me.Roles)
	}

	// stale step-up: a server with a tiny step-up window refuses step-up-gated permissions
	s2 := newITServer(t, func(c *config.Config) { c.Auth.StepUpMaxAge = time.Millisecond })
	a2 := s2.browser(t)
	a2.staffLogin(emailA, secretA)
	time.Sleep(5 * time.Millisecond)
	a2.expect(a2.do("POST", "/admin/staff", map[string]string{"email": uniqueEmail("w"), "display_name": "W", "justification": "should need step-up"}), 403, "STEP_UP_REQUIRED")

	// suspension: COMPLIANCE-only permission; super admin lacks account.suspend
	adminA.expect(adminA.do("POST", "/admin/users/"+me.ID+"/suspend", map[string]string{"reason": "testing permission boundaries"}), 403, "PERMISSION_DENIED")

	// a COMPLIANCE officer (granted through maker-checker) suspends and reactivates a personal account
	emailD := uniqueEmail("staff-d")
	r = adminA.do("POST", "/admin/staff", map[string]string{"email": emailD, "display_name": "Compliance D", "justification": "new compliance officer"})
	adminA.expect(r, 201, "")
	dID := r.field(t, "user_id")
	secretD := acceptStaffInvitation(t, s, emailD)
	r = adminB.do("POST", "/admin/role-assignment-requests", map[string]string{"user_id": dID, "role_code": "COMPLIANCE",
		"action": "GRANT", "justification": "compliance team onboarding"})
	adminB.expect(r, 201, "")
	adminA.expect(adminA.do("POST", "/admin/role-assignment-requests/"+r.field(t, "id")+"/approve", nil), 200, "") // empty body allowed
	d := s.browser(t)
	d.staffLogin(emailD, secretD)
	var uMe auth.Me
	_ = json.Unmarshal(u.do("GET", "/me", nil).Data, &uMe)
	d.expect(d.do("POST", "/admin/users/"+uMe.ID+"/suspend", map[string]string{"reason": "x"}), 422, "VALIDATION_FAILED")
	d.expect(d.do("POST", "/admin/users/"+got[1]+"/suspend", map[string]string{"reason": "staff via user route"}), 404, "USER_NOT_FOUND")
	d.expect(d.do("POST", "/admin/users/"+uMe.ID+"/suspend", map[string]string{"reason": "fraud investigation case 1"}), 204, "")
	u.expect(u.do("GET", "/me", nil), 401, "")
	u2 := s.browser(t)
	u2.expect(u2.login(userEmail, itPassword), 403, "ACCOUNT_SUSPENDED")
	mailTo(t, userEmail, "has been suspended")
	d.expect(d.do("POST", "/admin/users/"+uMe.ID+"/suspend", map[string]string{"reason": "fraud investigation case 1"}), 409, "INVALID_STATUS")
	d.expect(d.do("POST", "/admin/users/"+uMe.ID+"/reactivate", map[string]string{"reason": "investigation closed, no issue"}), 204, "")
	u2.mustLogin(userEmail, itPassword)
	// revoking a role through maker-checker removes the permission at the next sign-in
	r = adminA.do("POST", "/admin/role-assignment-requests", map[string]string{"user_id": dID, "role_code": "COMPLIANCE",
		"action": "REVOKE", "justification": "role no longer needed"})
	adminA.expect(r, 201, "")
	adminB.expect(adminB.do("POST", "/admin/role-assignment-requests/"+r.field(t, "id")+"/reject", map[string]string{}), 422, "VALIDATION_FAILED")
	adminB.expect(adminB.do("POST", "/admin/role-assignment-requests/"+r.field(t, "id")+"/approve", nil), 200, "")
	d.expect(d.do("GET", "/me", nil), 401, "")
	d.staffLogin(emailD, secretD)
	d.expect(d.do("POST", "/admin/users/"+uMe.ID+"/suspend", map[string]string{"reason": "should no longer work"}), 403, "PERMISSION_DENIED")

	// audit trail of the decision exists and the security chain verifies (worker role)
	w := pool(t, workerURL(t))
	var problems int
	if err := w.QueryRow(ctx(t), `SELECT count(*) FROM audit.verify_chain('audit.security_audit_events')`).Scan(&problems); err != nil || problems != 0 {
		t.Fatalf("security audit chain: %d problems, %v", problems, err)
	}
	var approvals int
	_ = w.QueryRow(ctx(t), `SELECT count(*) FROM audit.security_audit_events WHERE action = 'rbac.role_request.approved' AND target_id = $1`, reqID).Scan(&approvals)
	if approvals != 1 {
		t.Fatalf("approval audit events: %d", approvals)
	}
}

func TestIdentityOrganisationIsolation(t *testing.T) {
	s := newITServer(t, nil)
	ownerEmail, memberEmail, outsiderEmail := uniqueEmail("owner"), uniqueEmail("member"), uniqueEmail("outsider")
	owner, member, outsider := s.browser(t), s.browser(t), s.browser(t)

	// unverified users cannot create organisations
	unv := s.browser(t)
	unvEmail := uniqueEmail("unverified")
	unv.register(unvEmail, itPassword, "Unverified")
	unv.mustLogin(unvEmail, itPassword)
	unv.expect(unv.do("POST", "/organisations", map[string]string{"display_name": "Nope Org", "org_type": "OTHER"}), 403, "EMAIL_NOT_VERIFIED")

	for _, x := range []struct {
		b *browser
		e string
	}{{owner, ownerEmail}, {member, memberEmail}, {outsider, outsiderEmail}} {
		x.b.registerVerified(x.e)
		x.b.mustLogin(x.e, itPassword)
	}
	r := owner.do("POST", "/organisations", map[string]string{"display_name": "Harare Community Trust", "org_type": "TRUST"})
	owner.expect(r, 201, "")
	orgID := r.field(t, "id")

	// outsiders get 404 everywhere (existence not revealed)
	outsider.expect(outsider.do("GET", "/organisations/"+orgID, nil), 404, "ORGANISATION_NOT_FOUND")
	outsider.expect(outsider.do("GET", "/organisations/"+orgID+"/members", nil), 404, "ORGANISATION_NOT_FOUND")
	outsider.expect(outsider.do("POST", "/organisations/"+orgID+"/invitations", map[string]string{"email": outsiderEmail, "role_code": "ORG_ADMIN"}), 404, "")

	// invite → only the invited (verified) address sees and accepts it
	r = owner.do("POST", "/organisations/"+orgID+"/invitations", map[string]string{"email": memberEmail, "role_code": "ORG_MEMBER"})
	owner.expect(r, 201, "")
	invID := r.field(t, "id")
	mailTo(t, memberEmail, "invited to join an organisation")
	outsider.expect(outsider.do("POST", "/me/organisation-invitations/"+invID+"/accept", nil), 404, "INVITATION_NOT_FOUND")
	r = member.do("GET", "/me/organisation-invitations", nil)
	if !strings.Contains(string(r.Data), invID) {
		t.Fatalf("invitee should see the invitation: %s", r.Data)
	}
	member.expect(member.do("POST", "/me/organisation-invitations/"+invID+"/accept", nil), 200, "")
	member.expect(member.do("POST", "/me/organisation-invitations/"+invID+"/accept", nil), 404, "")

	// ORG_MEMBER can view but not manage
	member.expect(member.do("GET", "/organisations/"+orgID+"/members", nil), 200, "")
	member.expect(member.do("POST", "/organisations/"+orgID+"/invitations", map[string]string{"email": outsiderEmail, "role_code": "ORG_ADMIN"}), 403, "PERMISSION_DENIED")

	// the last ORG_ADMIN cannot leave or be demoted
	var members []struct {
		ID     string `json:"id"`
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	_ = json.Unmarshal(owner.do("GET", "/organisations/"+orgID+"/members", nil).Data, &members)
	var ownerMember, memberMember string
	for _, m := range members {
		if m.Role == "ORG_ADMIN" {
			ownerMember = m.ID
		} else {
			memberMember = m.ID
		}
	}
	owner.expect(owner.do("DELETE", "/organisations/"+orgID+"/members/"+ownerMember, nil), 409, "LAST_ORG_ADMIN")
	owner.expect(owner.do("PATCH", "/organisations/"+orgID+"/members/"+ownerMember, map[string]string{"role_code": "ORG_MEMBER"}), 409, "LAST_ORG_ADMIN")
	// promote, then the original admin may step down
	owner.expect(owner.do("PATCH", "/organisations/"+orgID+"/members/"+memberMember, map[string]string{"role_code": "ORG_ADMIN"}), 204, "")
	owner.expect(owner.do("PATCH", "/organisations/"+orgID+"/members/"+ownerMember, map[string]string{"role_code": "ORG_MEMBER"}), 204, "")

	// a second organisation's IDs cannot be used through the first one
	r = outsider.do("POST", "/organisations", map[string]string{"display_name": "Bulawayo Sports Club", "org_type": "SPORTS_CLUB"})
	outsider.expect(r, 201, "")
	org2 := r.field(t, "id")
	member.expect(member.do("DELETE", "/organisations/"+org2+"/members/"+memberMember, nil), 404, "ORGANISATION_NOT_FOUND")
	r = outsider.do("GET", "/me/organisations", nil)
	if strings.Contains(string(r.Data), orgID) || !strings.Contains(string(r.Data), org2) {
		t.Fatalf("list must contain only own organisations: %s", r.Data)
	}
}

func TestIdentityAuditChainsIntact(t *testing.T) {
	need(t, "DATABASE_URL", "POSTGRES_WORKER_PASSWORD")
	w := pool(t, workerURL(t))
	for _, tbl := range []string{"audit.audit_events", "audit.security_audit_events"} {
		var problems int
		if err := w.QueryRow(ctx(t), `SELECT count(*) FROM audit.verify_chain($1::regclass)`, tbl).Scan(&problems); err != nil || problems != 0 {
			t.Fatalf("%s: %d problems, %v", tbl, problems, err)
		}
	}
	// no audit metadata carries secrets
	var leaks int
	if err := w.QueryRow(ctx(t), `SELECT count(*) FROM audit.security_audit_events
		WHERE metadata::text ~* '(password|secret|token|recovery_code|otp)"\s*:'`).Scan(&leaks); err != nil {
		t.Fatal(err)
	}
	if leaks != 0 {
		t.Fatalf("%d security audit events carry secret-like metadata keys", leaks)
	}
}

// TestIdentityPerformanceSample reports latency of authenticated requests (session lookup + CSRF +
// authorization on every request) against the local stack. Opt-in: FUNDZIM_IT_PERF=1. It records numbers;
// the only assertion is a generous ceiling that catches pathological regressions (e.g. a hash per request).
func TestIdentityPerformanceSample(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1")
	}
	s := newITServer(t, func(c *config.Config) { c.RateLimit.Enabled = false })
	email := uniqueEmail("perf")
	b := s.browser(t)
	b.registerVerified(email)
	b.mustLogin(email, itPassword)
	measure := func(name string, n, workers int, fn func(*browser)) {
		lat := make([]time.Duration, n)
		var next atomic.Int64
		var wg sync.WaitGroup
		start := time.Now()
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					i := next.Add(1) - 1
					if i >= int64(n) {
						return
					}
					t0 := time.Now()
					fn(b)
					lat[i] = time.Since(t0)
				}
			}()
		}
		wg.Wait()
		total := time.Since(start)
		sortDur(lat)
		t.Logf("%s: n=%d workers=%d p50=%s p95=%s p99=%s max=%s throughput=%.0f req/s", name, n, workers,
			lat[n/2], lat[n*95/100], lat[n*99/100], lat[n-1], float64(n)/total.Seconds())
		if lat[n*95/100] > 500*time.Millisecond {
			t.Errorf("%s p95 %s exceeds 500ms", name, lat[n*95/100])
		}
	}
	measure("GET /me sequential", 300, 1, func(b *browser) { b.expect(b.do("GET", "/me", nil), 200, "") })
	measure("GET /me concurrent", 1000, 20, func(b *browser) { b.expect(b.do("GET", "/me", nil), 200, "") })
	measure("PATCH /me concurrent", 300, 10, func(b *browser) {
		b.expect(b.do("PATCH", "/me", map[string]string{"display_name": "Perf User"}), 200, "")
	})
}

func sortDur(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		for j := i; j > 0 && d[j] < d[j-1]; j-- {
			d[j], d[j-1] = d[j-1], d[j]
		}
	}
}

// TestDistributedLoginLimitAcrossReplicas proves the per-account login limit is shared by two real API
// containers (Valkey-backed): attempts alternate between replicas and the 6th is refused wherever it lands.
// Opt-in: FUNDZIM_IT_API_URL and FUNDZIM_IT_API2_URL (docker compose --profile scale-test up -d fundzim-api-2).
func TestDistributedLoginLimitAcrossReplicas(t *testing.T) {
	a, b := os.Getenv("FUNDZIM_IT_API_URL"), os.Getenv("FUNDZIM_IT_API2_URL")
	if a == "" || b == "" {
		t.Skip("set FUNDZIM_IT_API_URL and FUNDZIM_IT_API2_URL (scale-test profile)")
	}
	email := uniqueEmail("replicas")
	post := func(base string) int {
		body := strings.NewReader(fmt.Sprintf(`{"email":%q,"password":"not the right password"}`, email))
		resp, err := http.Post(base+"/api/v1/auth/login", "application/json", body)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	var codes []int
	for i := 0; i < 8; i++ {
		base := a
		if i%2 == 1 {
			base = b
		}
		codes = append(codes, post(base))
	}
	t.Logf("status sequence across replicas: %v", codes)
	for i, c := range codes {
		want := 401
		if i >= 5 {
			want = 429
		}
		if c != want {
			t.Fatalf("attempt %d: %d, want %d (sequence %v)", i+1, c, want, codes)
		}
	}
}

// TestIdentitySessionExpiry proves idle and absolute expiry are enforced server-side and that the idle expiry
// slides (at most once a minute) but never past the absolute expiry. Short timeouts are injected through
// config on dedicated in-process servers.
func TestIdentitySessionExpiry(t *testing.T) {
	need(t, "DATABASE_MIGRATION_URL")
	email := uniqueEmail("expiry")
	base := newITServer(t, nil)
	b0 := base.browser(t)
	b0.registerVerified(email)

	// idle expiry: 1.5 s idle, 1 h absolute
	idle := newITServer(t, func(c *config.Config) { c.Auth.SessionIdleTimeout = 1500 * time.Millisecond })
	b := idle.browser(t)
	b.mustLogin(email, itPassword)
	b.expect(b.do("GET", "/me", nil), 200, "")
	time.Sleep(2 * time.Second)
	b.expect(b.do("GET", "/me", nil), 401, "AUTHENTICATION_REQUIRED")

	// absolute expiry: idle (1 h) is clamped to the 2 s absolute lifetime; cookie Max-Age matches
	abs := newITServer(t, func(c *config.Config) { c.Auth.SessionAbsoluteTimeout = 2 * time.Second })
	c := abs.browser(t)
	r := c.login(email, itPassword)
	c.expect(r, 200, "")
	for _, ck := range (&http.Response{Header: r.Header}).Cookies() {
		if ck.Name == "fz_session" && (ck.MaxAge < 1 || ck.MaxAge > 2) {
			t.Fatalf("session cookie Max-Age %d, want ≤ absolute lifetime", ck.MaxAge)
		}
	}
	c.expect(c.do("GET", "/me", nil), 200, "")
	time.Sleep(2500 * time.Millisecond)
	c.expect(c.do("GET", "/me", nil), 401, "AUTHENTICATION_REQUIRED")

	// sliding idle expiry: a request after > 1 minute of inactivity pushes idle_expires_at forward
	d := base.browser(t)
	d.mustLogin(email, itPassword)
	var me auth.Me
	_ = json.Unmarshal(d.do("GET", "/me", nil).Data, &me)
	mig := pool(t, os.Getenv("DATABASE_MIGRATION_URL"))
	if _, err := mig.Exec(ctx(t), `UPDATE app.sessions SET last_seen_at = now() - interval '2 minutes',
		idle_expires_at = now() + interval '1 minute' WHERE id = (SELECT id FROM app.sessions WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC LIMIT 1)`, me.ID); err != nil {
		t.Fatal(err)
	}
	d.expect(d.do("GET", "/me", nil), 200, "")
	var idleLeft, absLeft time.Duration
	var idleAt, absAt, now time.Time
	if err := mig.QueryRow(ctx(t), `SELECT idle_expires_at, absolute_expires_at, now() FROM app.sessions
		WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 1`, me.ID).Scan(&idleAt, &absAt, &now); err != nil {
		t.Fatal(err)
	}
	idleLeft, absLeft = idleAt.Sub(now), absAt.Sub(now)
	if idleLeft < base.cfg.Auth.SessionIdleTimeout-time.Minute || idleAt.After(absAt) {
		t.Fatalf("idle expiry did not slide: idle in %s, absolute in %s", idleLeft, absLeft)
	}
}
