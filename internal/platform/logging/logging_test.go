package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestJSONRecordFieldsAndRedaction(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Options{Service: "fundzim-api", Env: "test", Version: "1.2.3", Level: slog.LevelInfo, Format: "json"})
	l.Info("hello",
		slog.String("password", "p@ss"), slog.String("session_token", "tok"), slog.String("Authorization", "Bearer x"),
		slog.String("api_key", "k"), slog.String("card_number", "4111"), slog.String("id_number", "63-123456A78"),
		slog.Group("nested", slog.String("otp_code", "123456"), slog.String("safe", "visible")),
		slog.String("request_id", "abc"))
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	for _, k := range []string{"time", "level", "msg", "service", "env", "version", "request_id"} {
		if _, ok := rec[k]; !ok {
			t.Errorf("missing field %q", k)
		}
	}
	out := buf.String()
	for _, secret := range []string{"p@ss", "\"tok\"", "Bearer x", "\"k\"", "4111", "63-123456A78", "123456"} {
		if strings.Contains(out, secret) {
			t.Errorf("sensitive value %s leaked: %s", secret, out)
		}
	}
	if !strings.Contains(out, "visible") {
		t.Error("non-sensitive nested value was dropped")
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Options{Level: slog.LevelWarn, Format: "json"})
	l.Info("hidden")
	if buf.Len() != 0 {
		t.Fatal("info logged at warn level")
	}
}

func TestContextLogger(t *testing.T) {
	var buf bytes.Buffer
	base := New(&buf, Options{Level: slog.LevelInfo, Format: "json"})
	ctx := WithContext(context.Background(), base.With("request_id", "r-1"))
	FromContext(ctx, nil).Info("x")
	if !strings.Contains(buf.String(), "r-1") {
		t.Fatal("context logger not used")
	}
	if FromContext(context.Background(), base) != base {
		t.Fatal("fallback not returned")
	}
}

func TestSensitiveKeyMatching(t *testing.T) {
	for _, k := range []string{"password", "db_password", "session_id", "card_pan", "pin", "user_pin", "cvv", "X-Api-Key", "refresh_token"} {
		if !IsSensitiveKey(k) {
			t.Errorf("%q should be sensitive", k)
		}
	}
	for _, k := range []string{"company", "spinner", "shipping", "request_id", "route", "status", "duration_ms"} {
		if IsSensitiveKey(k) {
			t.Errorf("%q should not be sensitive", k)
		}
	}
}

// Stage 18 (F-S18-07): error and panic texts are scrubbed of credential-shaped fragments; ordinary text is kept.
func TestErrorTextScrubbed(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, Options{Service: "t", Level: slog.LevelInfo})
	dsnPW := "hunter" + "2-db-pass"
	l.Error("boom",
		slog.String("error", "failed to connect to `postgres://fundzim_app:"+dsnPW+"@db:5432/fundzim`: dial tcp: refused"),
		slog.Any("panic", errors.New("vault: X-Vault-Token: s.abcdefghijklmnop rejected; password="+dsnPW)),
		slog.String("cause", "key "+strings.Repeat("ab", 32)+" wrong"),
		slog.String("detail", "campaign 0192aa00-0000-7000-8000-000000000001 not found"))
	out := buf.String()
	if strings.Contains(out, dsnPW) || strings.Contains(out, "s.abcdefghijklmnop") || strings.Contains(out, strings.Repeat("ab", 32)) {
		t.Fatalf("secret survived scrubbing: %s", out)
	}
	for _, keep := range []string{"fundzim_app", "db:5432", "dial tcp: refused", "0192aa00-0000-7000-8000-000000000001"} {
		if !strings.Contains(out, keep) {
			t.Errorf("useful context %q was scrubbed: %s", keep, out)
		}
	}
}
