package logging

import (
	"bytes"
	"context"
	"encoding/json"
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
