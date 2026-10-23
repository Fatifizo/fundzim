package app

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/config"
)

func lookup(m map[string]string) config.LookupFunc {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadWorkerConfig(t *testing.T) {
	w, err := LoadWorkerConfig(lookup(map[string]string{
		"DATABASE_WORKER_URL":  "postgres://fundzim_worker:" + apiTestPW + "@127.0.0.1:5432/fundzim?sslmode=disable",
		"AUDIT_CHECKPOINT_KEY": testCheckpointKey,
	}), config.EnvDevelopment)
	if err != nil {
		t.Fatal(err)
	}
	if w.InternalHTTPPort != 9091 || w.Concurrency != 4 || w.OutboxRetention.Hours() != 168 || w.AuditVerifyInterval != 15*time.Minute ||
		w.AuditFullVerifyEvery != 24*time.Hour {
		t.Fatalf("defaults %+v", w)
	}

	_, err = LoadWorkerConfig(lookup(map[string]string{
		"DATABASE_WORKER_URL":       "postgres://fundzim_worker:" + apiTestPW + "@db:5432/fundzim?sslmode=disable",
		"WORKER_INTERNAL_HTTP_PORT": "0",
		"WORKER_CONCURRENCY":        "lots",
		"OUTBOX_RETENTION":          "1m",
		"AUDIT_VERIFY_INTERVAL":     "1s",
		"AUDIT_CHECKPOINT_KEY":      "short",
	}), config.EnvProduction)
	if err == nil {
		t.Fatal("expected validation errors")
	}
	msg := err.Error()
	for _, want := range []string{"WORKER_INTERNAL_HTTP_PORT", "WORKER_CONCURRENCY", "OUTBOX_RETENTION", "sslmode=verify-full",
		"AUDIT_VERIFY_INTERVAL", "AUDIT_CHECKPOINT_KEY is required"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in %s", want, msg)
		}
	}
	if strings.Contains(msg, apiTestPW) {
		t.Fatal("validation error leaks the URL")
	}
	if _, err := LoadWorkerConfig(lookup(nil), config.EnvDevelopment); err == nil || !strings.Contains(err.Error(), "DATABASE_WORKER_URL is required") {
		t.Fatalf("missing URL: %v", err)
	}
	// Stage 18: the checkpoint key is never shared with another key
	_, err = LoadWorkerConfig(lookup(map[string]string{
		"DATABASE_WORKER_URL":  "postgres://fundzim_worker:" + apiTestPW + "@127.0.0.1:5432/fundzim?sslmode=disable",
		"AUDIT_CHECKPOINT_KEY": testCheckpointKey, "BLIND_INDEX_KEY": strings.ToUpper(testCheckpointKey),
	}), config.EnvDevelopment)
	if err == nil || !strings.Contains(err.Error(), "AUDIT_CHECKPOINT_KEY must differ from BLIND_INDEX_KEY") || strings.Contains(err.Error(), testCheckpointKey) {
		t.Fatalf("shared checkpoint key: %v", err)
	}
}

const testCheckpointKey = "5f4dcc3b5aa765d61d8327deb882cf995f4dcc3b5aa765d61d8327deb882cf99"

func TestWorkerNotReadyWithoutDatabaseOrRiver(t *testing.T) {
	cfg := testDeps(t).Config
	wcfg, err := LoadWorkerConfig(lookup(map[string]string{
		"DATABASE_WORKER_URL":  "postgres://fundzim_worker:" + apiTestPW + "@127.0.0.1:1/fundzim?sslmode=disable",
		"AUDIT_CHECKPOINT_KEY": testCheckpointKey,
	}), cfg.App.Env)
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewWorkerDeps(context.Background(), cfg, wcfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	h := WorkerInternalHandler(d, d.Checker())
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 503, "/metrics": 200} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != want {
			t.Fatalf("%s: %d want %d", path, rec.Code, want)
		}
		if path == "/readyz" && !strings.Contains(rec.Body.String(), `"name":"river","ok":false`) {
			t.Fatalf("river check missing: %s", rec.Body.String())
		}
		if path == "/metrics" && !strings.Contains(rec.Body.String(), "fundzim_outbox_lag_seconds") {
			t.Fatal("outbox metrics not registered")
		}
	}
	if WorkerInternalAddr(d) != "127.0.0.1:9091" {
		t.Fatal(WorkerInternalAddr(d))
	}
}
