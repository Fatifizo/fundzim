package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) LookupFunc {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

// localTestPW is a test-only value, concatenated into URLs so secret scanners stay strict.
const localTestPW = "pw-local"

// testKeys are fake, test-only key values (hex, 32 bytes).
var testKeys = map[string]string{
	"CSRF_SECRET":                strings.Repeat("ab", 32),
	"FIELD_ENCRYPTION_LOCAL_KEY": strings.Repeat("cd", 32),
	"BLIND_INDEX_KEY":            strings.Repeat("ef", 32),
}

func localEnv() map[string]string {
	return map[string]string{
		"CSRF_SECRET":                testKeys["CSRF_SECRET"],
		"FIELD_ENCRYPTION_LOCAL_KEY": testKeys["FIELD_ENCRYPTION_LOCAL_KEY"],
		"BLIND_INDEX_KEY":            testKeys["BLIND_INDEX_KEY"],
		"APP_ENV":                    "development",
		"DATABASE_URL":               "postgres://fundzim_app:" + localTestPW + "@localhost:5432/fundzim?sslmode=disable",
	}
}

func TestLoadLocalDefaults(t *testing.T) {
	c, err := Load(env(localEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.HTTP.Addr() != "127.0.0.1:8080" || c.HTTP.InternalAddr() != "127.0.0.1:9090" {
		t.Errorf("unexpected listen addresses %s / %s", c.HTTP.Addr(), c.HTTP.InternalAddr())
	}
	if c.HTTP.RequestTimeout != 15*time.Second || c.HTTP.ShutdownTimeout != 20*time.Second {
		t.Errorf("unexpected timeouts")
	}
	if c.App.Name != "FundZim" || c.Log.Format != "json" || !c.RateLimit.Enabled {
		t.Errorf("unexpected defaults: %+v", c.App)
	}
	if c.Redis.Enabled() || c.Storage.Enabled() {
		t.Errorf("optional dependencies must be disabled by default")
	}
}

func TestLoadRequiredMissing(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"APP_ENV is required", "DATABASE_URL is required"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
	if !IsValidationError(err) {
		t.Error("expected a ValidationError")
	}
}

func TestLoadInvalidValues(t *testing.T) {
	cases := map[string]string{
		"HTTP_PORT":           "70000",
		"REQUEST_TIMEOUT":     "forever",
		"LOG_FORMAT":          "xml",
		"LOG_LEVEL":           "loud",
		"APP_ENV":             "prod",
		"REDIS_URL":           "http://x",
		"TRUSTED_PROXY_CIDRS": "not-a-cidr",
	}
	for k, v := range cases {
		t.Run(k, func(t *testing.T) {
			m := localEnv()
			m[k] = v
			_, err := Load(env(m))
			if err == nil || !strings.Contains(err.Error(), k) {
				t.Fatalf("expected an error naming %s, got %v", k, err)
			}
		})
	}
}

func TestProductionRefusesUnsafeSettings(t *testing.T) {
	m := map[string]string{
		"APP_ENV":              "production",
		"DATABASE_URL":         "postgres://u:p@db:5432/fundzim?sslmode=disable",
		"LOG_FORMAT":           "text",
		"LOG_LEVEL":            "debug",
		"CORS_ALLOWED_ORIGINS": "*",
		"RATE_LIMIT_ENABLED":   "false",
		"REDIS_URL":            "redis://cache:6379/0",
	}
	_, err := Load(env(m))
	if err == nil {
		t.Fatal("expected production refusal")
	}
	for _, want := range []string{"sslmode=verify-full", "LOG_FORMAT must be json", "LOG_LEVEL=debug", `"*" is not allowed`,
		"RATE_LIMIT_ENABLED=false", "rediss://", "STORAGE_ENDPOINT is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing refusal %q in:\n%v", want, err)
		}
	}
}

func TestProductionOtherwiseValidRefusedOnlyForMissingKMS(t *testing.T) {
	// Production cannot start until KMS envelope encryption exists (Stage 18): a fully hardened
	// production configuration must be refused for exactly that one reason.
	m := map[string]string{
		"APP_ENV":               "production",
		"DATABASE_URL":          "postgres://u:" + localTestPW + "@db:5432/fundzim?sslmode=verify-full",
		"STORAGE_ENDPOINT":      "https://s3.example.invalid",
		"STORAGE_PUBLIC_BUCKET": "pub", "STORAGE_PUBLIC_ACCESS_KEY_ID": "a", "STORAGE_PUBLIC_SECRET_ACCESS_KEY": "b",
		"STORAGE_KYC_BUCKET": "priv", "STORAGE_KYC_ACCESS_KEY_ID": "c", "STORAGE_KYC_SECRET_ACCESS_KEY": "d",
		"STORAGE_EVIDENCE_BUCKET": "evid", "STORAGE_EVIDENCE_ACCESS_KEY_ID": "e", "STORAGE_EVIDENCE_SECRET_ACCESS_KEY": "f",
		"CORS_ALLOWED_ORIGINS":      "https://partner.example.invalid",
		"APP_PUBLIC_URL":            "https://fundzim.example",
		"CSRF_SECRET":               testKeys["CSRF_SECRET"],
		"BLIND_INDEX_KEY":           testKeys["BLIND_INDEX_KEY"],
		"FIELD_ENCRYPTION_PROVIDER": "kms",
		"SMTP_HOST":                 "smtp.example.com",
		"SMTP_TLS":                  "starttls",
		"EMAIL_FROM":                "FundZim <no-reply@fundzim.example>",
		"SMS_PROVIDER":              "disabled",
	}
	_, err := Load(env(m))
	var v *ValidationError
	if !errors.As(err, &v) || len(v.Problems) != 1 || !strings.Contains(v.Problems[0], "kms is not implemented") {
		t.Fatalf("expected exactly the KMS refusal, got %v", err)
	}
}

func TestStorageCredentialsMustBeSeparate(t *testing.T) {
	m := localEnv()
	for k, v := range map[string]string{
		"STORAGE_ENDPOINT":      "http://localhost:9000",
		"STORAGE_PUBLIC_BUCKET": "pub", "STORAGE_PUBLIC_ACCESS_KEY_ID": "a", "STORAGE_PUBLIC_SECRET_ACCESS_KEY": "b",
		"STORAGE_KYC_BUCKET": "priv", "STORAGE_KYC_ACCESS_KEY_ID": "same", "STORAGE_KYC_SECRET_ACCESS_KEY": "d",
		"STORAGE_EVIDENCE_BUCKET": "evid", "STORAGE_EVIDENCE_ACCESS_KEY_ID": "same", "STORAGE_EVIDENCE_SECRET_ACCESS_KEY": "f",
	} {
		m[k] = v
	}
	_, err := Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "must all be different") {
		t.Fatalf("expected separation refusal, got %v", err)
	}
}

func TestErrorsNeverContainValues(t *testing.T) {
	m := localEnv()
	m["DATABASE_URL"] = "mysql://root:" + "SuperSecret123" + "@db/x"
	m["REDIS_URL"] = "http://user:" + "AnotherSecret" + "@x"
	_, err := Load(env(m))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, secret := range []string{"SuperSecret123", "AnotherSecret"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks a value: %v", err)
		}
	}
}

func TestSecretRedaction(t *testing.T) {
	s := Secret("hunter2-value")
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "db", s)
	j, _ := json.Marshal(struct{ S Secret }{s})
	for _, out := range []string{fmt.Sprint(s), fmt.Sprintf("%v %+v %#v %s", s, s, s, s), buf.String(), string(j)} {
		if strings.Contains(out, "hunter2") {
			t.Fatalf("secret leaked: %s", out)
		}
	}
	if s.Reveal() != "hunter2-value" {
		t.Fatal("Reveal must return the value")
	}
	c, err := Load(env(localEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if dump := fmt.Sprintf("%+v", c); strings.Contains(dump, "pw-local") {
		t.Fatalf("config dump leaks the database password: %s", dump)
	}
}

func TestCORSOriginsNormalised(t *testing.T) {
	m := localEnv()
	m["CORS_ALLOWED_ORIGINS"] = "http://localhost:3000, https://a.example.invalid/"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.HTTP.CORSAllowedOrigins) != 2 || c.HTTP.CORSAllowedOrigins[1] != "https://a.example.invalid" {
		t.Fatalf("unexpected origins %v", c.HTTP.CORSAllowedOrigins)
	}
}

func TestStorageBucketsMustBeSeparate(t *testing.T) {
	m := localEnv()
	for k, v := range map[string]string{
		"STORAGE_ENDPOINT":      "http://localhost:3900",
		"STORAGE_PUBLIC_BUCKET": "pub", "STORAGE_PUBLIC_ACCESS_KEY_ID": "a", "STORAGE_PUBLIC_SECRET_ACCESS_KEY": "b",
		"STORAGE_KYC_BUCKET": "priv", "STORAGE_KYC_ACCESS_KEY_ID": "c", "STORAGE_KYC_SECRET_ACCESS_KEY": "d",
		"STORAGE_EVIDENCE_BUCKET": "priv", "STORAGE_EVIDENCE_ACCESS_KEY_ID": "e", "STORAGE_EVIDENCE_SECRET_ACCESS_KEY": "f",
	} {
		m[k] = v
	}
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "three different buckets") {
		t.Fatalf("expected bucket separation refusal, got %v", err)
	}
}

func TestAuthDefaultsAndRefusals(t *testing.T) {
	c, err := Load(env(localEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Auth.SessionCookieName != "fz_session" || c.Auth.CookieSecure {
		t.Fatalf("local http must use fz_session without Secure, got %q secure=%v", c.Auth.SessionCookieName, c.Auth.CookieSecure)
	}
	if c.Auth.PasswordMinLength != 12 || c.Auth.HashMemoryKiB != 65536 || c.SMS.Provider != "dev_mailpit" {
		t.Fatalf("unexpected defaults %+v", c.Auth)
	}
	m := localEnv()
	m["APP_PUBLIC_URL"] = "https://fundzim.example"
	c, err = Load(env(m))
	if err != nil || c.Auth.SessionCookieName != "__Host-fz_session" || !c.Auth.CookieSecure {
		t.Fatalf("https origin must use a Secure __Host- cookie: %v %+v", err, c.Auth)
	}
	for k, v := range map[string]string{"CSRF_SECRET": "short", "BLIND_INDEX_KEY": testKeys["FIELD_ENCRYPTION_LOCAL_KEY"],
		"SESSION_IDLE_TIMEOUT": "721h", "SMS_PROVIDER": "twilio", "SMTP_TLS": "maybe", "SESSION_COOKIE_NAME": "bad name"} {
		m := localEnv()
		m[k] = v
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s=%q accepted", k, v)
		}
	}
	if s := fmt.Sprintf("%+v", c); strings.Contains(s, testKeys["CSRF_SECRET"]) || strings.Contains(s, testKeys["BLIND_INDEX_KEY"]) {
		t.Fatal("key material leaked through the config dump")
	}
}
