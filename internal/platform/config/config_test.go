package config

import (
	"bytes"
	"context"
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
	"CSRF_SECRET":                           strings.Repeat("ab", 32),
	"FIELD_ENCRYPTION_LOCAL_KEY":            strings.Repeat("cd", 32),
	"BLIND_INDEX_KEY":                       strings.Repeat("ef", 32),
	"KYC_FIELD_ENCRYPTION_LOCAL_KEY":        strings.Repeat("a1", 32),
	"KYC_BLIND_INDEX_KEY":                   strings.Repeat("b2", 32),
	"COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY": strings.Repeat("c3", 32),
	"DOCUMENT_TICKET_KEY":                   strings.Repeat("d4", 32),
}

func localEnv() map[string]string {
	return map[string]string{
		"CSRF_SECRET":                           testKeys["CSRF_SECRET"],
		"FIELD_ENCRYPTION_LOCAL_KEY":            testKeys["FIELD_ENCRYPTION_LOCAL_KEY"],
		"BLIND_INDEX_KEY":                       testKeys["BLIND_INDEX_KEY"],
		"KYC_FIELD_ENCRYPTION_LOCAL_KEY":        testKeys["KYC_FIELD_ENCRYPTION_LOCAL_KEY"],
		"KYC_BLIND_INDEX_KEY":                   testKeys["KYC_BLIND_INDEX_KEY"],
		"COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY": testKeys["COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY"],
		"DOCUMENT_TICKET_KEY":                   testKeys["DOCUMENT_TICKET_KEY"],
		"DATABASE_KYC_URL":                      "postgres://fundzim_kyc:" + localTestPW + "@localhost:5432/fundzim?sslmode=disable",
		"DATABASE_COMPLIANCE_URL":               "postgres://fundzim_compliance:" + localTestPW + "@localhost:5432/fundzim?sslmode=disable",
		"DATABASE_LEDGER_URL":                   "postgres://fundzim_ledger:" + localTestPW + "@localhost:5432/fundzim?sslmode=disable",
		"APP_ENV":                               "development",
		"DATABASE_URL":                          "postgres://fundzim_app:" + localTestPW + "@localhost:5432/fundzim?sslmode=disable",
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
		"KYC_BLIND_INDEX_KEY":       testKeys["KYC_BLIND_INDEX_KEY"],
		"DOCUMENT_TICKET_KEY":       testKeys["DOCUMENT_TICKET_KEY"],
		"DATABASE_KYC_URL":          "postgres://k:" + localTestPW + "@db:5432/fundzim?sslmode=verify-full",
		"DATABASE_COMPLIANCE_URL":   "postgres://c:" + localTestPW + "@db:5432/fundzim?sslmode=verify-full",
		"DATABASE_LEDGER_URL":       "postgres://l:" + localTestPW + "@db:5432/fundzim?sslmode=verify-full",
		"MALWARE_SCANNER":           "clamd",
		"CLAMAV_ADDR":               "clamav.internal:3310",
		"SMTP_HOST":                 "smtp.example.com",
		"SMTP_TLS":                  "starttls",
		"EMAIL_FROM":                "FundZim <no-reply@fundzim.example>",
		"SMS_PROVIDER":              "disabled",
		"SECRETS_PROVIDER":          "kv2",
		"SECRETS_KV2_ADDR":          "https://secrets.internal:8200",
		"SECRETS_KV2_ROLE_ID":       "fundzim-api",
		"SECRETS_KV2_SECRET_ID":     "approle-secret-id",
	}
	_, err := Load(env(m))
	var v *ValidationError
	if !errors.As(err, &v) || len(v.Problems) != 1 || !strings.Contains(v.Problems[0], "kms is not implemented") {
		t.Fatalf("expected exactly the KMS refusal, got %v", err)
	}
}

// prodBase is a hardened production configuration (Stage 18: keys come from the secret manager).
func prodBase() map[string]string {
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
		"KYC_BLIND_INDEX_KEY":       testKeys["KYC_BLIND_INDEX_KEY"],
		"DOCUMENT_TICKET_KEY":       testKeys["DOCUMENT_TICKET_KEY"],
		"DATABASE_KYC_URL":          "postgres://k:" + localTestPW + "@db:5432/fundzim?sslmode=verify-full",
		"DATABASE_COMPLIANCE_URL":   "postgres://c:" + localTestPW + "@db:5432/fundzim?sslmode=verify-full",
		"DATABASE_LEDGER_URL":       "postgres://l:" + localTestPW + "@db:5432/fundzim?sslmode=verify-full",
		"MALWARE_SCANNER":           "clamd",
		"CLAMAV_ADDR":               "clamav.internal:3310",
		"SMTP_HOST":                 "smtp.example.com",
		"SMTP_TLS":                  "starttls",
		"EMAIL_FROM":                "FundZim <no-reply@fundzim.example>",
		"SMS_PROVIDER":              "disabled",
		"SECRETS_PROVIDER":          "kv2",
		"SECRETS_KV2_ADDR":          "https://secrets.internal:8200",
		"SECRETS_KV2_ROLE_ID":       "fundzim-api",
		"SECRETS_KV2_SECRET_ID":     "approle-secret-id",
	}
	m["FIELD_ENCRYPTION_PROVIDER"] = "secretstore"
	for k := range testKeys {
		if k != "CSRF_SECRET" {
			m[k] = "secretref://fundzim/production/keys/" + strings.ToLower(k)
		}
	}
	m["STORAGE_SSE_C_KEY"] = "secretref://fundzim/production/keys/storage_sse_c_key"
	return m
}

// Stage 18 (ADR-050): with keys in the secret manager a hardened production configuration validates (the financial
// gates still block every live operation); key material typed into the environment is refused.
func TestProductionKeysFromSecretManager(t *testing.T) {
	c, err := Load(env(prodBase()))
	if err != nil {
		t.Fatalf("hardened production configuration refused: %v", err)
	}
	if !c.Security.Managed {
		t.Fatal("secretstore provider not marked managed")
	}
	m := prodBase()
	m["BLIND_INDEX_KEY"] = testKeys["BLIND_INDEX_KEY"]
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "BLIND_INDEX_KEY must be secretref://") ||
		strings.Contains(err.Error(), testKeys["BLIND_INDEX_KEY"]) {
		t.Fatalf("literal key under secretstore: %v", err)
	}
	m = prodBase()
	m["SECRETS_PROVIDER"] = "env"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "requires SECRETS_PROVIDER=kv2") {
		t.Fatalf("secretstore without kv2: %v", err)
	}
	m = prodBase()
	delete(m, "STORAGE_SSE_C_KEY")
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "STORAGE_SSE_C_KEY is required") {
		t.Fatalf("missing SSE key: %v", err)
	}
	m = prodBase()
	m["FIELD_ENCRYPTION_PROVIDER"] = "local"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "FIELD_ENCRYPTION_PROVIDER=local is refused") {
		t.Fatalf("local provider in production: %v", err)
	}
}

func TestResolveManagedKeys(t *testing.T) {
	values := map[string]string{}
	i := 0
	for _, k := range []string{"FIELD_ENCRYPTION_LOCAL_KEY", "BLIND_INDEX_KEY", "KYC_FIELD_ENCRYPTION_LOCAL_KEY", "KYC_BLIND_INDEX_KEY",
		"COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY", "DOCUMENT_TICKET_KEY", "STORAGE_SSE_C_KEY"} {
		values["secretref://fundzim/production/keys/"+strings.ToLower(k)] = fmt.Sprintf("%064x", 1000+i)
		i++
	}
	resolver := func(vals map[string]string) func(context.Context, string) ([]byte, error) {
		return func(_ context.Context, ref string) ([]byte, error) {
			v, ok := vals[ref]
			if !ok {
				return nil, errors.New("not found")
			}
			return []byte(v + "\n"), nil
		}
	}
	c, err := Load(env(prodBase()))
	if err != nil {
		t.Fatal(err)
	}
	sec := c.Security
	if err := sec.ResolveManaged(context.Background(), resolver(values)); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sec.BlindIndexKey.Reveal() != values["secretref://fundzim/production/keys/blind_index_key"] {
		t.Fatal("resolved value not applied (or not trimmed)")
	}
	// a missing key stops startup; the error names the variable only
	missing := map[string]string{}
	for k, v := range values {
		missing[k] = v
	}
	delete(missing, "secretref://fundzim/production/keys/document_ticket_key")
	sec = c.Security
	err = sec.ResolveManaged(context.Background(), resolver(missing))
	if err == nil || !strings.Contains(err.Error(), "DOCUMENT_TICKET_KEY") || strings.Contains(err.Error(), "secretref://") {
		t.Fatalf("missing key: %v", err)
	}
	// the same key stored for two purposes is refused after resolution
	dup := map[string]string{}
	for k, v := range values {
		dup[k] = v
	}
	dup["secretref://fundzim/production/keys/kyc_blind_index_key"] = dup["secretref://fundzim/production/keys/blind_index_key"]
	sec = c.Security
	if err := sec.ResolveManaged(context.Background(), resolver(dup)); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("duplicate keys: %v", err)
	}
	bad := map[string]string{}
	for k, v := range values {
		bad[k] = v
	}
	bad["secretref://fundzim/production/keys/field_encryption_local_key"] = "not-hex"
	sec = c.Security
	if err := sec.ResolveManaged(context.Background(), resolver(bad)); err == nil || strings.Contains(err.Error(), "not-hex") {
		t.Fatalf("malformed key: %v", err)
	}
}

func TestPreviousKeysValidated(t *testing.T) {
	m := localEnv()
	m["FIELD_ENCRYPTION_PREVIOUS_KEYS"] = strings.Repeat("99", 32) + "," + strings.Repeat("98", 32)
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Security.FieldEncryptionPrevious.List(); len(got) != 2 {
		t.Fatalf("previous keys %v", got)
	}
	m["FIELD_ENCRYPTION_PREVIOUS_KEYS"] = testKeys["BLIND_INDEX_KEY"]
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("previous key reused for another purpose: %v", err)
	}
	m["FIELD_ENCRYPTION_PREVIOUS_KEYS"] = strings.Repeat("91", 32) + "," + strings.Repeat("92", 32) + "," + strings.Repeat("93", 32) + "," + strings.Repeat("94", 32)
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "at most three") {
		t.Fatalf("too many previous keys: %v", err)
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

func TestStage5KeysDistinctAndScannerPolicy(t *testing.T) {
	m := localEnv()
	m["KYC_BLIND_INDEX_KEY"] = m["BLIND_INDEX_KEY"]
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("reused key must be refused: %v", err)
	}
	m = localEnv()
	m["DATABASE_KYC_URL"] = m["DATABASE_URL"]
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "fundzim_kyc") {
		t.Fatalf("shared pool URL must be refused: %v", err)
	}
	m = localEnv()
	m["MALWARE_SCANNER"] = "clamd"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "CLAMAV_ADDR") {
		t.Fatalf("clamd without address must be refused: %v", err)
	}
	c, err := Load(env(localEnv()))
	if err != nil || c.Verification.Scanner != "dev" || c.Verification.UploadMaxBytes != 10<<20 || c.Verification.DocumentTicketTTL != time.Minute {
		t.Fatalf("defaults: %+v %v", c.Verification, err)
	}
}

func TestStoragePublicCredentialOptionalButAllOrNothing(t *testing.T) {
	base := map[string]string{
		"STORAGE_ENDPOINT":   "http://localhost:9000",
		"STORAGE_KYC_BUCKET": "priv", "STORAGE_KYC_ACCESS_KEY_ID": "c", "STORAGE_KYC_SECRET_ACCESS_KEY": "d",
		"STORAGE_EVIDENCE_BUCKET": "evid", "STORAGE_EVIDENCE_ACCESS_KEY_ID": "e", "STORAGE_EVIDENCE_SECRET_ACCESS_KEY": "f",
	}
	m := localEnv()
	for k, v := range base {
		m[k] = v
	}
	if _, err := Load(env(m)); err != nil {
		t.Fatalf("a process without the public credential (worker) must load: %v", err)
	}
	m["STORAGE_PUBLIC_BUCKET"] = "pub"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "set together or not at all") {
		t.Fatalf("expected partial public credential refusal, got %v", err)
	}
	delete(m, "STORAGE_PUBLIC_BUCKET")
	delete(m, "STORAGE_KYC_SECRET_ACCESS_KEY")
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "STORAGE_KYC_BUCKET") {
		t.Fatalf("expected missing private credential refusal, got %v", err)
	}
}

// Stage 8 (ADR-039 §6) and Stage 9: the payment sandbox simulator is refused in production; each execution
// environment is accepted only where it belongs; the old provider-environment variable is refused loudly.
func TestPaymentsSandboxRefusedInProduction(t *testing.T) {
	m := map[string]string{"APP_ENV": "production", "PAYMENTS_SANDBOX_ENABLED": "true"}
	_, err := Load(env(m))
	if err == nil || !strings.Contains(err.Error(), "PAYMENTS_SANDBOX_ENABLED is refused in production") {
		t.Fatalf("sandbox not refused in production: %v", err)
	}
	local := localEnv()
	local["PAYMENTS_SANDBOX_ENABLED"] = "true"
	c, err := Load(env(local))
	if err != nil || !c.Payments.SandboxEnabled || c.Payments.ExecutionEnvironment != ExecLocalSimulator ||
		strings.Join(c.Payments.ProviderAllowlist, ",") != "sandbox" {
		t.Fatalf("local sandbox: %v %+v", err, c.Payments)
	}
	local["PAYMENTS_PROVIDER_ENVIRONMENT"] = "SANDBOX"
	if _, err := Load(env(local)); err == nil || !strings.Contains(err.Error(), "replaced by PAYMENTS_EXECUTION_ENVIRONMENT") {
		t.Fatalf("old variable accepted: %v", err)
	}
	delete(local, "PAYMENTS_PROVIDER_ENVIRONMENT")
	for _, bad := range []struct{ v, want string }{
		{"PRODUCTION", "allowed only in production"},
		{"LIVE", "must be DISABLED, LOCAL_SIMULATOR, PROVIDER_SANDBOX or PRODUCTION"},
		{"PROVIDER_SANDBOX", "requires SECRETS_PROVIDER=kv2"},
	} {
		local["PAYMENTS_EXECUTION_ENVIRONMENT"] = bad.v
		if _, err := Load(env(local)); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Fatalf("%s: want %q, got %v", bad.v, bad.want, err)
		}
	}
	local["PAYMENTS_EXECUTION_ENVIRONMENT"] = "LOCAL_SIMULATOR"
	local["PAYMENTS_SANDBOX_ENABLED"] = "false"
	if _, err := Load(env(local)); err == nil || !strings.Contains(err.Error(), "requires PAYMENTS_SANDBOX_ENABLED=true") {
		t.Fatalf("simulator environment without the simulator: %v", err)
	}
	def := localEnv()
	c, err = Load(env(def))
	if err != nil || c.Payments.SandboxEnabled || c.Payments.ExecutionEnvironment != ExecDisabled || len(c.Payments.ProviderAllowlist) != 0 {
		t.Fatalf("payments must be off unless enabled: %v %+v", err, c.Payments)
	}
	def["PAYMENTS_PROVIDER_ALLOWLIST"] = "Sandbox"
	if _, err := Load(env(def)); err == nil || !strings.Contains(err.Error(), "must be provider codes") {
		t.Fatalf("invalid allowlist accepted: %v", err)
	}
	for _, v := range []string{"LOCAL_SIMULATOR", "PROVIDER_SANDBOX"} {
		m := map[string]string{"APP_ENV": "production", "PAYMENTS_EXECUTION_ENVIRONMENT": v}
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), v+" is refused in production") {
			t.Fatalf("%s accepted in production: %v", v, err)
		}
	}
	m = map[string]string{"APP_ENV": "production", "PAYMENTS_PROVIDER_ALLOWLIST": "sandbox"}
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "must not contain the sandbox simulator in production") {
		t.Fatalf("simulator allow-listed in production: %v", err)
	}
}

// Stage 9 (ADR-041): production refuses environment-variable secrets, plain HTTP and static tokens.
func TestSecretsManagerRules(t *testing.T) {
	m := map[string]string{"APP_ENV": "production"}
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "SECRETS_PROVIDER=env is refused in production") {
		t.Fatalf("env secrets accepted in production: %v", err)
	}
	m["SECRETS_PROVIDER"] = "kv2"
	m["SECRETS_KV2_ADDR"] = "http://bao:8200"
	m["SECRETS_KV2_TOKEN"] = "t"
	_, err := Load(env(m))
	for _, want := range []string{"must use https outside development and test", "requires AppRole authentication"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q, got %v", want, err)
		}
	}
	m["SECRETS_KV2_ADDR"] = "https://bao:8200"
	delete(m, "SECRETS_KV2_TOKEN")
	m["SECRETS_KV2_ROLE_ID"] = "r"
	m["SECRETS_KV2_SECRET_ID"] = "s"
	if _, err := Load(env(m)); err != nil && strings.Contains(err.Error(), "SECRETS_") {
		t.Fatalf("valid production secret manager refused: %v", err)
	}
	local := localEnv()
	local["SECRETS_PROVIDER"] = "kv2"
	if _, err := Load(env(local)); err == nil || !strings.Contains(err.Error(), "SECRETS_KV2_ADDR must be an http(s) URL") {
		t.Fatalf("missing address accepted: %v", err)
	}
	local["SECRETS_KV2_ADDR"] = "http://127.0.0.1:8200"
	if _, err := Load(env(local)); err == nil || !strings.Contains(err.Error(), "needs SECRETS_KV2_TOKEN") {
		t.Fatalf("missing credentials accepted: %v", err)
	}
	local["SECRETS_KV2_TOKEN"] = "dev"
	local["SECRETS_KV2_ROLE_ID"] = "r"
	if _, err := Load(env(local)); err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("half an AppRole accepted: %v", err)
	}
	delete(local, "SECRETS_KV2_ROLE_ID")
	local["PAYMENTS_EXECUTION_ENVIRONMENT"] = "PROVIDER_SANDBOX"
	c, err := Load(env(local))
	if err != nil || c.Secrets.Provider != "kv2" || c.Secrets.KV2Mount != "secret" || c.Payments.ExecutionEnvironment != ExecProviderSandbox {
		t.Fatalf("development kv2 + provider sandbox: %v %+v", err, c.Secrets.Provider)
	}
}

// Stage 15: messaging is disabled by default, a production provider cannot be named, simulators need their switch,
// development senders are development/test only and the simulator is refused in production.
func TestNotificationsConfig(t *testing.T) {
	c, err := Load(env(localEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Notifications.SimulatorEnabled || c.Notifications.EmailProvider != "disabled" || c.Notifications.SMSProvider != "disabled" ||
		c.Notifications.WhatsAppProvider != "disabled" {
		t.Fatalf("defaults: %+v", c.Notifications)
	}
	for name, tc := range map[string]struct {
		set  map[string]string
		want string
	}{
		"unknown provider":       {map[string]string{"NOTIFICATIONS_EMAIL_PROVIDER": "sendgrid"}, "NOTIFICATIONS_EMAIL_PROVIDER must be one of"},
		"whatsapp production":    {map[string]string{"NOTIFICATIONS_WHATSAPP_PROVIDER": "meta"}, "NOTIFICATIONS_WHATSAPP_PROVIDER must be one of"},
		"simulator without flag": {map[string]string{"NOTIFICATIONS_SMS_PROVIDER": "simulator"}, "needs NOTIFICATIONS_SIMULATOR_ENABLED=true"},
		"dev sender in staging": {map[string]string{"APP_ENV": "staging", "NOTIFICATIONS_SMS_PROVIDER": "dev_mailpit", "SMS_PROVIDER": "dev_mailpit"},
			"allowed in development and test only"},
		"dev smtp needs smtp": {map[string]string{"NOTIFICATIONS_EMAIL_PROVIDER": "dev_smtp", "EMAIL_PROVIDER": "disabled"}, "needs EMAIL_PROVIDER=smtp"},
	} {
		t.Run(name, func(t *testing.T) {
			m := localEnv()
			for k, v := range tc.set {
				m[k] = v
			}
			_, err := Load(env(m))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	m := localEnv()
	m["APP_ENV"], m["NOTIFICATIONS_SIMULATOR_ENABLED"] = "production", "true"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "NOTIFICATIONS_SIMULATOR_ENABLED is refused in production") {
		t.Fatalf("simulator in production: %v", err)
	}
}

// KI-S15-19: the payout submission retry budget and its backoff are bounded configuration.
func TestPayoutSubmitRetryConfig(t *testing.T) {
	c, err := Load(env(localEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Payouts.SubmitRetryBudget != 5 || c.Payouts.SubmitRetryBase != 5*time.Second || c.Payouts.SubmitRetryMax != 10*time.Minute {
		t.Fatalf("defaults: %+v", c.Payouts)
	}
	m := localEnv()
	m["PAYOUTS_SUBMIT_RETRY_BUDGET"], m["PAYOUTS_SUBMIT_RETRY_BASE"], m["PAYOUTS_SUBMIT_RETRY_MAX"] = "8", "10s", "1h"
	if c, err = Load(env(m)); err != nil || c.Payouts.SubmitRetryBudget != 8 || c.Payouts.SubmitRetryMax != time.Hour {
		t.Fatalf("explicit values: %+v %v", c.Payouts, err)
	}
	for k, v := range map[string]string{"PAYOUTS_SUBMIT_RETRY_BUDGET": "0", "PAYOUTS_SUBMIT_RETRY_BASE": "0s", "PAYOUTS_SUBMIT_RETRY_MAX": "48h"} {
		m := localEnv()
		m[k] = v
		if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), k) {
			t.Fatalf("%s=%s accepted: %v", k, v, err)
		}
	}
	m = localEnv()
	m["PAYOUTS_SUBMIT_RETRY_BASE"], m["PAYOUTS_SUBMIT_RETRY_MAX"] = "5m", "1m"
	if _, err := Load(env(m)); err == nil || !strings.Contains(err.Error(), "PAYOUTS_SUBMIT_RETRY_MAX must not be shorter") {
		t.Fatalf("max < base accepted: %v", err)
	}
}

// Stage 18 (F-S18-06): staging is production-like for transport and logging.
func TestStagingIsProductionLike(t *testing.T) {
	m := prodBase()
	m["APP_ENV"] = "staging"
	m["LOG_LEVEL"] = "debug"
	m["SECRETS_KV2_ADDR"] = "http://secrets.internal:8200"
	m["PUBLIC_SITE_URL"] = "http://fundzim.example"
	_, err := Load(env(m))
	if err == nil {
		t.Fatal("staging accepted debug logging and plain HTTP")
	}
	for _, want := range []string{"LOG_LEVEL=debug is not allowed outside development and test",
		"SECRETS_KV2_ADDR must use https outside development and test", "PUBLIC_SITE_URL must use https outside development and test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in %v", want, err)
		}
	}
	m = prodBase()
	m["APP_ENV"] = "staging"
	if _, err := Load(env(m)); err != nil {
		t.Fatalf("hardened staging configuration refused: %v", err)
	}
}
