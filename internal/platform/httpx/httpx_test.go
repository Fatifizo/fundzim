package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

func quietLogger(buf *bytes.Buffer) *slog.Logger {
	if buf == nil {
		return slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
	Meta struct {
		RequestID string `json:"request_id"`
	} `json:"meta"`
}

func decode(t *testing.T, body io.Reader) envelope {
	t.Helper()
	var e envelope
	if err := json.NewDecoder(body).Decode(&e); err != nil {
		t.Fatalf("response is not a JSON envelope: %v", err)
	}
	return e
}

func testStack(logger *slog.Logger, rt *Router) http.Handler {
	return Chain(rt, RequestIDMiddleware(nil), AccessLog(logger, nil), Recover(logger, nil),
		SecurityHeaders(false), BodyLimit(16, logger))
}

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	rt := NewRouter(quietLogger(nil))
	rt.HandleFunc("GET /x", PolicyPublic, func(w http.ResponseWriter, r *http.Request) {
		WriteData(w, r, 200, map[string]string{"ok": "1"})
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set(HeaderRequestID, "forged-request-id-1234") // untrusted peer: must be replaced
	testStack(quietLogger(nil), rt).ServeHTTP(rec, req)
	id := rec.Header().Get(HeaderRequestID)
	if id == "" || id == "forged-request-id-1234" {
		t.Fatalf("expected a generated request id, got %q", id)
	}
	if e := decode(t, rec.Body); e.Meta.RequestID != id {
		t.Fatalf("meta.request_id %q != header %q", e.Meta.RequestID, id)
	}
}

func TestRequestIDAcceptedFromTrustedProxyOnly(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	h := RequestIDMiddleware(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(RequestID(r.Context())))
	}))
	for _, tc := range []struct {
		peer, in string
		keep     bool
	}{
		{"10.1.2.3:5555", "proxy-supplied-id-01", true},
		{"10.1.2.3:5555", "bad id with spaces", false},
		{"203.0.113.9:5555", "proxy-supplied-id-01", false},
	} {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = tc.peer
		req.Header.Set(HeaderRequestID, tc.in)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Body.String(); (got == tc.in) != tc.keep {
			t.Errorf("peer %s id %q: kept=%v", tc.peer, tc.in, got == tc.in)
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	for _, hsts := range []bool{false, true} {
		rec := httptest.NewRecorder()
		SecurityHeaders(hsts)(http.NotFoundHandler()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		h := rec.Header()
		for k, v := range map[string]string{
			"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer",
			"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'", "Cache-Control": "no-store",
		} {
			if h.Get(k) != v {
				t.Errorf("%s = %q, want %q", k, h.Get(k), v)
			}
		}
		if (h.Get("Strict-Transport-Security") != "") != hsts {
			t.Errorf("HSTS presence should be %v", hsts)
		}
	}
}

func TestNotFoundAndMethodNotAllowedAreEnveloped(t *testing.T) {
	rt := NewRouter(quietLogger(nil))
	rt.HandleFunc("GET /api/v1/health", PolicyPublic, func(w http.ResponseWriter, r *http.Request) {})
	h := testStack(quietLogger(nil), rt)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/does-not-exist", nil))
	if rec.Code != 404 {
		t.Fatalf("status %d", rec.Code)
	}
	if e := decode(t, rec.Body); e.Error == nil || e.Error.Code != "ROUTE_NOT_FOUND" || e.Meta.RequestID == "" {
		t.Fatalf("bad 404 envelope: %+v", e)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/health", nil))
	if rec.Code != 405 || !strings.Contains(rec.Header().Get("Allow"), "GET") {
		t.Fatalf("status %d allow %q", rec.Code, rec.Header().Get("Allow"))
	}
	if e := decode(t, rec.Body); e.Error == nil || e.Error.Code != "METHOD_NOT_ALLOWED" {
		t.Fatalf("bad 405 envelope: %+v", e)
	}
}

func TestPanicRecoveredWithoutLeakingDetails(t *testing.T) {
	var logs bytes.Buffer
	logger := quietLogger(&logs)
	rt := NewRouter(logger)
	rt.HandleFunc("GET /boom", PolicyPublic, func(http.ResponseWriter, *http.Request) {
		panic("db password is hunter2 at 10.0.0.5")
	})
	rec := httptest.NewRecorder()
	testStack(logger, rt).ServeHTTP(rec, httptest.NewRequest("GET", "/boom", nil))
	if rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "hunter2") || strings.Contains(body, "10.0.0.5") || strings.Contains(body, "goroutine") {
		t.Fatalf("panic details leaked to client: %s", body)
	}
	if e := decode(t, strings.NewReader(body)); e.Error.Code != "INTERNAL_ERROR" || !e.Error.Retryable {
		t.Fatalf("bad 500 envelope: %s", body)
	}
	if !strings.Contains(logs.String(), "panic recovered") || !strings.Contains(logs.String(), `"status":500`) {
		t.Fatalf("panic or access log missing: %s", logs.String())
	}
}

func TestErrorCauseIsLoggedNotReturned(t *testing.T) {
	var logs bytes.Buffer
	logger := quietLogger(&logs)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	WriteError(rec, req, logger, errors.New(`pq: relation "secret_table" does not exist SELECT * FROM x`))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret_table") || strings.Contains(rec.Body.String(), "SELECT") {
		t.Fatalf("internal error leaked: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "secret_table") {
		t.Fatal("cause should be logged for diagnosis")
	}
}

func TestErrorKindsMapToStatus(t *testing.T) {
	cases := map[errs.Kind]int{errs.Invalid: 400, errs.Unauthenticated: 401, errs.Forbidden: 403, errs.NotFound: 404,
		errs.MethodNotAllowed: 405, errs.Conflict: 409, errs.TooLarge: 413, errs.Unprocessable: 422,
		errs.RateLimited: 429, errs.Internal: 500, errs.Unavailable: 503}
	for kind, want := range cases {
		rec := httptest.NewRecorder()
		WriteError(rec, httptest.NewRequest("GET", "/", nil), quietLogger(nil), errs.New(kind, "SOME_CODE", "msg"))
		if rec.Code != want {
			t.Errorf("kind %d: status %d, want %d", kind, rec.Code, want)
		}
		if (want == 503 || want == 429) && rec.Header().Get("Retry-After") == "" {
			t.Errorf("status %d without Retry-After", want)
		}
	}
}

func TestBodyLimit(t *testing.T) {
	rt := NewRouter(quietLogger(nil))
	rt.HandleFunc("POST /echo", PolicyPublic, func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			WriteError(w, r, quietLogger(nil), errs.New(errs.TooLarge, errs.CodePayloadTooLarge, "too large"))
			return
		}
		WriteData(w, r, 200, nil)
	})
	h := testStack(quietLogger(nil), rt)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/echo", strings.NewReader(strings.Repeat("a", 64))))
	if rec.Code != 413 {
		t.Fatalf("declared oversize: status %d", rec.Code)
	}
	req := httptest.NewRequest("POST", "/echo", io.NopCloser(strings.NewReader(strings.Repeat("a", 64))))
	req.ContentLength = -1 // chunked
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Fatalf("chunked oversize: status %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/echo", strings.NewReader("small")))
	if rec.Code != 200 {
		t.Fatalf("small body: status %d", rec.Code)
	}
}

func TestClientIPHonoursForwardedOnlyFromTrustedPeers(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 198.51.100.7, 10.0.0.2")
	req.RemoteAddr = "10.0.0.1:80"
	if got := ClientIP(req, trusted).String(); got != "198.51.100.7" {
		t.Fatalf("trusted peer: got %s", got)
	}
	req.RemoteAddr = "203.0.113.5:80"
	if got := ClientIP(req, trusted).String(); got != "203.0.113.5" {
		t.Fatalf("untrusted peer must not be able to spoof: got %s", got)
	}
}

func TestCORS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	// default: no CORS headers at all
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	CORS(nil)(next).ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("CORS must be off by default")
	}
	h := CORS([]string{"https://ok.example"})(next)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("unlisted origin allowed")
	}
	pre := httptest.NewRequest("OPTIONS", "/", nil)
	pre.Header.Set("Origin", "https://ok.example")
	pre.Header.Set("Access-Control-Request-Method", "POST")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, pre)
	if rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "https://ok.example" {
		t.Fatalf("preflight failed: %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials must not be allowed cross-origin")
	}
}

func TestRouteWithoutPolicyPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	NewRouter(quietLogger(nil)).HandleFunc("GET /x", Policy{}, func(http.ResponseWriter, *http.Request) {})
}

func TestTimeoutSetsDeadline(t *testing.T) {
	h := Timeout(50 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("no deadline on request context")
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestNonPublicRoutesDeniedWithoutAuthorizer(t *testing.T) {
	rt := NewRouter(quietLogger(nil))
	called := false
	for _, p := range []Policy{Authenticated(), User(), Staff(), Permission("audit.read")} {
		rt.HandleFunc("GET /p/"+string(p.Kind), p, func(http.ResponseWriter, *http.Request) { called = true })
	}
	for _, path := range []string{"/p/authenticated", "/p/user", "/p/staff", "/p/permission"} {
		rec := httptest.NewRecorder()
		testStack(quietLogger(nil), rt).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 401 {
			t.Errorf("%s: status %d, want 401", path, rec.Code)
		}
	}
	if called {
		t.Fatal("a protected handler ran without authorization")
	}
}

func TestAuthorizerDecisionAndRouteMiddleware(t *testing.T) {
	rt := NewRouter(quietLogger(nil))
	var seen Policy
	rt.SetAuthorizer(AuthorizerFunc(func(r *http.Request, p Policy) error {
		seen = p
		if r.Header.Get("X-Allow") == "1" {
			return nil
		}
		return errs.New(errs.Forbidden, "PERMISSION_DENIED", "no")
	}))
	order := []string{}
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { order = append(order, name); next.ServeHTTP(w, r) })
		}
	}
	rt.HandleFunc("POST /x", Permission("role.assign.approve"), func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(204)
	}, With(mw("limit"), mw("idem")))
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != 403 || len(order) != 0 || seen.Permission != "role.assign.approve" {
		t.Fatalf("denied request: code %d order %v policy %v", rec.Code, order, seen)
	}
	req := httptest.NewRequest("POST", "/x", nil)
	req.Header.Set("X-Allow", "1")
	rec = httptest.NewRecorder()
	rt.ServeHTTP(rec, req)
	if rec.Code != 204 || strings.Join(order, ",") != "limit,idem,handler" {
		t.Fatalf("allowed request: code %d order %v", rec.Code, order)
	}
}

func TestPermissionPolicyNeedsName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	NewRouter(quietLogger(nil)).HandleFunc("GET /x", Permission(""), func(http.ResponseWriter, *http.Request) {})
}
