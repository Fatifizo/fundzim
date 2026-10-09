package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

// testDeps builds dependencies whose database is unreachable (port 1 refuses connections), so
// readiness must report not-ready without the process failing.
// apiTestPW is a fake, test-only value; kept out of URL literals so secret scanners stay strict.
const apiTestPW = "pw-test"

func testDeps(t *testing.T) *Deps {
	t.Helper()
	cfg, err := config.Load(func(k string) (string, bool) {
		v, ok := map[string]string{
			"APP_ENV":                               "test",
			"CSRF_SECRET":                           strings.Repeat("ab", 32),
			"FIELD_ENCRYPTION_LOCAL_KEY":            strings.Repeat("cd", 32),
			"BLIND_INDEX_KEY":                       strings.Repeat("ef", 32),
			"KYC_FIELD_ENCRYPTION_LOCAL_KEY":        strings.Repeat("a1", 32),
			"KYC_BLIND_INDEX_KEY":                   strings.Repeat("b2", 32),
			"COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY": strings.Repeat("c3", 32),
			"DOCUMENT_TICKET_KEY":                   strings.Repeat("d4", 32),
			"DATABASE_KYC_URL":                      "postgres://fundzim_kyc:" + apiTestPW + "@127.0.0.1:1/fundzim?sslmode=disable",
			"DATABASE_COMPLIANCE_URL":               "postgres://fundzim_compliance:" + apiTestPW + "@127.0.0.1:1/fundzim?sslmode=disable",
			"DATABASE_URL":                          "postgres://fundzim_app:" + apiTestPW + "@127.0.0.1:1/fundzim?sslmode=disable",
			"DATABASE_CONNECT_TIMEOUT":              "500ms",
			"HTTP_PORT":                             "18080",
			"INTERNAL_HTTP_PORT":                    "19090",
		}[k]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDeps(context.Background(), cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func get(t *testing.T, h http.Handler, path string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: response is not JSON: %q", path, rec.Body.String())
	}
	return rec, body
}

func TestHealthEndpoints(t *testing.T) {
	d := testDeps(t)
	h := PublicHandler(d, NewRouter(d, d.Checker()))

	rec, body := get(t, h, "/healthz")
	if rec.Code != 200 || body["status"] != "ok" {
		t.Fatalf("/healthz: %d %v", rec.Code, body)
	}
	rec, body = get(t, h, "/api/v1/health")
	if rec.Code != 200 || body["data"].(map[string]any)["status"] != "ok" || rec.Header().Get("X-Request-ID") == "" {
		t.Fatalf("/api/v1/health: %d %v", rec.Code, body)
	}
	if body["meta"].(map[string]any)["request_id"] != rec.Header().Get("X-Request-ID") {
		t.Fatal("meta.request_id does not match header")
	}
}

func TestReadinessWhenDatabaseDown(t *testing.T) {
	d := testDeps(t)
	h := PublicHandler(d, NewRouter(d, d.Checker()))
	start := time.Now()
	rec, body := get(t, h, "/readyz")
	if rec.Code != 503 || body["status"] != "unavailable" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("/readyz: %d %v", rec.Code, body)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("readiness not bounded by timeouts")
	}
	rec, body = get(t, h, "/api/v1/ready")
	if rec.Code != 503 {
		t.Fatalf("/api/v1/ready: %d", rec.Code)
	}
	e := body["error"].(map[string]any)
	if e["code"] != "SERVICE_UNAVAILABLE" || e["retryable"] != true {
		t.Fatalf("bad error %v", e)
	}
	raw := rec.Body.String()
	for _, leak := range []string{"127.0.0.1", "fundzim_app", "pw-test", "5432", "connect", "database"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("readiness leaks %q: %s", leak, raw)
		}
	}
}

func TestVersionEndpoint(t *testing.T) {
	d := testDeps(t)
	h := PublicHandler(d, NewRouter(d, d.Checker()))
	rec, body := get(t, h, "/api/v1/version")
	data := body["data"].(map[string]any)
	if rec.Code != 200 || data["name"] != "FundZim" || data["version"] == "" || data["commit"] == "" || data["build"] == "" {
		t.Fatalf("/api/v1/version: %d %v", rec.Code, body)
	}
	if len(data) != 4 {
		t.Fatalf("version must expose exactly name, version, build, commit: %v", data)
	}
}

func TestInternalReadinessHasDetailsButPublicDoesNot(t *testing.T) {
	d := testDeps(t)
	rec := httptest.NewRecorder()
	InternalHandler(d, d.Checker()).ServeHTTP(rec, httptest.NewRequest("GET", "/internal/readiness", nil))
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"name":"database"`) {
		t.Fatalf("internal readiness: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	InternalHandler(d, d.Checker()).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "fundzim_build_info") {
		t.Fatalf("metrics: %d", rec.Code)
	}
}

func TestEveryRouteHasAPolicyAndIsInOpenAPI(t *testing.T) {
	d := testDeps(t)
	spec, err := os.ReadFile("../../api/openapi/fundzim-v1.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range NewRouter(d, d.Checker()).Routes() {
		if r.Policy.Kind == "" {
			t.Errorf("route %s has no policy", r.Pattern)
		}
		_, path, _ := strings.Cut(r.Pattern, " ")
		// The spec's default server is /api/v1, so its paths are relative to it; probes override the server.
		path = strings.TrimPrefix(path, "/api/v1")
		if !strings.Contains(string(spec), "\n  "+path+":\n") {
			t.Errorf("route %s is not in api/openapi/fundzim-v1.yaml", r.Pattern)
		}
	}
}

func TestMetricsCountRequestsByRouteTemplate(t *testing.T) {
	d := testDeps(t)
	h := PublicHandler(d, NewRouter(d, d.Checker()))
	get(t, h, "/api/v1/health")
	get(t, h, "/api/v1/nope/123")
	rec := httptest.NewRecorder()
	d.Metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	out := rec.Body.String()
	if !strings.Contains(out, `route="GET /api/v1/health",status="200"`) || !strings.Contains(out, `route="unmatched",status="404"`) {
		t.Fatalf("missing request metrics:\n%s", out)
	}
	if strings.Contains(out, "/api/v1/nope/123") {
		t.Fatal("raw paths must never become metric labels")
	}
}

func TestGracefulShutdownCompletesInFlightRequests(t *testing.T) {
	d := testDeps(t)
	var started, finished atomic.Bool
	release := make(chan struct{})
	router := httpx.NewRouter(d.Logger)
	router.HandleFunc("GET /slow", httpx.PolicyPublic, func(w http.ResponseWriter, r *http.Request) {
		started.Store(true)
		<-release
		finished.Store(true)
		httpx.WriteData(w, r, 200, nil)
	})
	srv := &Servers{Public: &http.Server{Handler: PublicHandler(d, router)}, Internal: &http.Server{Handler: http.NotFoundHandler()}}
	pubLn, _ := net.Listen("tcp", "127.0.0.1:0")
	intLn, _ := net.Listen("tcp", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, d, srv, pubLn, intLn) }()

	respc := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + pubLn.Addr().String() + "/slow")
		if err != nil {
			respc <- -1
			return
		}
		resp.Body.Close()
		respc <- resp.StatusCode
	}()
	for !started.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	cancel() // SIGTERM equivalent
	time.Sleep(50 * time.Millisecond)
	if _, err := http.Get("http://" + pubLn.Addr().String() + "/slow"); err == nil {
		t.Error("new connections must be refused during shutdown")
	}
	close(release)
	if code := <-respc; code != 200 || !finished.Load() {
		t.Fatalf("in-flight request was not completed: %d", code)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve returned %v", err)
	}
}
