// Package config loads FundZim's typed configuration from environment variables (12-factor).
//
// Load parses everything once at startup. Missing or invalid required values, and unsafe combinations for
// non-development environments, are collected and reported together by variable NAME only (never values).
// Secret fields use the Secret type, which never renders its value in strings, logs or JSON.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Env is the deployment environment.
type Env string

const (
	EnvDevelopment Env = "development"
	EnvTest        Env = "test"
	EnvStaging     Env = "staging"
	EnvProduction  Env = "production"
)

// IsLocal reports whether the environment is a developer or CI environment.
func (e Env) IsLocal() bool { return e == EnvDevelopment || e == EnvTest }

// Secret holds a sensitive value. It redacts itself in every textual representation.
type Secret string

func (Secret) String() string               { return "[REDACTED]" }
func (Secret) GoString() string             { return "[REDACTED]" }
func (Secret) LogValue() slog.Value         { return slog.StringValue("[REDACTED]") }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"[REDACTED]"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte("[REDACTED]"), nil }

// Reveal returns the secret value. Call it only where the value is handed to a client library.
func (s Secret) Reveal() string { return string(s) }

// Config is the complete runtime configuration.
type Config struct {
	App       App
	HTTP      HTTP
	Database  Database
	Redis     Redis
	Storage   Storage
	Log       Log
	RateLimit RateLimit
	Auth      Auth
	Email     Email
	SMS       SMS
	Security  Security
}

// Auth configures authentication and sessions (ADR-027, ADR-032, SECURITY §4–§6).
type Auth struct {
	PublicURL                   string // web origin used in emailed links and as the allowed Origin for CSRF checks
	SessionCookieName           string
	CookieSecure                bool
	SessionIdleTimeout          time.Duration
	SessionAbsoluteTimeout      time.Duration
	StaffSessionIdleTimeout     time.Duration
	StaffSessionAbsoluteTimeout time.Duration
	StepUpMaxAge                time.Duration
	MFAChallengeTTL             time.Duration
	EmailVerificationTTL        time.Duration
	PasswordResetTTL            time.Duration
	StaffInvitationTTL          time.Duration
	OTPTTL                      time.Duration
	OTPMaxAttempts              int
	PasswordMinLength           int
	PasswordMaxLength           int
	HashMemoryKiB               uint32
	HashIterations              uint32
	HashParallelism             uint8
	HashMaxConcurrent           int
	CSRFSecret                  Secret
}

// Email configures outbound email. Local development uses Mailpit.
type Email struct {
	Provider string // smtp | disabled
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass Secret
	SMTPTLS  string // none | starttls | tls
	From     string
}

// SMS configures outbound SMS. Only the development provider exists (no real SMS provider is selected).
type SMS struct {
	Provider string // dev_mailpit | disabled
}

// Security holds application-level key material (DATA-CLASSIFICATION, data-protection-architecture).
type Security struct {
	FieldEncryptionProvider string // local (development/test only) | kms (not implemented yet)
	FieldEncryptionKey      Secret // hex, 32 bytes, AES-256-GCM key for C3/C4 fields (TOTP secrets)
	BlindIndexKey           Secret // hex, 32 bytes, HMAC key for tokens, OTP codes, recovery codes
}

type App struct {
	Env     Env
	Name    string
	Version string // optional override of the build version (APP_VERSION)
}

type HTTP struct {
	Host               string
	Port               int
	InternalHost       string // metrics and detailed health; never exposed publicly
	InternalPort       int
	RequestTimeout     time.Duration
	ShutdownTimeout    time.Duration
	MaxBodyBytes       int64
	TrustedProxyCIDRs  []netip.Prefix
	CORSAllowedOrigins []string
}

// Addr is the public listen address.
func (h HTTP) Addr() string { return net.JoinHostPort(h.Host, strconv.Itoa(h.Port)) }

// InternalAddr is the internal (metrics/health detail) listen address.
func (h HTTP) InternalAddr() string {
	return net.JoinHostPort(h.InternalHost, strconv.Itoa(h.InternalPort))
}

type Database struct {
	URL            Secret // contains credentials
	MigrationURL   Secret // used only by `fundzimctl migrate`
	MaxConns       int32
	ConnectTimeout time.Duration
}

type Redis struct {
	URL      Secret // empty = Redis disabled
	Required bool
}

// Enabled reports whether a Redis URL is configured.
func (r Redis) Enabled() bool { return r.URL != "" }

// StorageCredential is one of the three scoped object-storage credentials (baseline §12 I-19).
type StorageCredential struct {
	Bucket          string
	AccessKeyID     Secret
	SecretAccessKey Secret
}

func (c StorageCredential) configured() bool {
	return c.Bucket != "" && c.AccessKeyID != "" && c.SecretAccessKey != ""
}

type Storage struct {
	Endpoint       string // empty = object storage disabled (allowed only in local environments)
	Region         string
	ForcePathStyle bool
	Public         StorageCredential // public campaign media bucket
	KYC            StorageCredential // private identity-documents bucket (kyc module only)
	Evidence       StorageCredential // private compliance/evidence bucket (storage module only)
}

// Enabled reports whether object storage is configured.
func (s Storage) Enabled() bool { return s.Endpoint != "" }

type Log struct {
	Level  slog.Level
	Format string // json | text
}

type RateLimit struct {
	Enabled      bool
	RequestsPerS float64
	Burst        int
}

// LookupFunc reads one variable. os.LookupEnv in production; a map in tests.
type LookupFunc func(key string) (string, bool)

// FromEnv loads configuration from the process environment.
func FromEnv() (Config, error) { return Load(os.LookupEnv) }

// ValidationError lists every problem found. It never contains configuration values.
type ValidationError struct{ Problems []string }

func (e *ValidationError) Error() string {
	return "invalid configuration:\n  - " + strings.Join(e.Problems, "\n  - ")
}

type loader struct {
	get      LookupFunc
	problems []string
}

func (l *loader) fail(format string, args ...any) {
	l.problems = append(l.problems, fmt.Sprintf(format, args...))
}

func (l *loader) str(key, def string) string {
	if v, ok := l.get(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func (l *loader) required(key string) string {
	v := l.str(key, "")
	if v == "" {
		l.fail("%s is required", key)
	}
	return v
}

func (l *loader) integer(key string, def, min, max int) int {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		l.fail("%s must be an integer between %d and %d", key, min, max)
		return def
	}
	return n
}

func (l *loader) duration(key string, def, min, max time.Duration) time.Duration {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < min || d > max {
		l.fail("%s must be a duration between %s and %s (e.g. 15s)", key, min, max)
		return def
	}
	return d
}

func (l *loader) boolean(key string, def bool) bool {
	raw := l.str(key, "")
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		l.fail("%s must be true or false", key)
		return def
	}
	return b
}

func (l *loader) list(key string) []string {
	raw := l.str(key, "")
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Load builds a Config using get and validates it.
func Load(get LookupFunc) (Config, error) {
	l := &loader{get: get}
	var c Config

	env := Env(l.str("APP_ENV", ""))
	switch env {
	case EnvDevelopment, EnvTest, EnvStaging, EnvProduction:
	case "":
		l.fail("APP_ENV is required (development, test, staging or production)")
		env = EnvProduction // validate the rest as strictly as possible
	default:
		l.fail("APP_ENV must be one of development, test, staging, production")
		env = EnvProduction
	}
	c.App = App{Env: env, Name: l.str("APP_NAME", "FundZim"), Version: l.str("APP_VERSION", "")}

	// HTTP. Local environments default to loopback; containers set HTTP_HOST=0.0.0.0 explicitly.
	c.HTTP = HTTP{
		Host:            l.str("HTTP_HOST", "127.0.0.1"),
		Port:            l.integer("HTTP_PORT", 8080, 1, 65535),
		InternalHost:    l.str("INTERNAL_HTTP_HOST", "127.0.0.1"),
		InternalPort:    l.integer("INTERNAL_HTTP_PORT", 9090, 1, 65535),
		RequestTimeout:  l.duration("REQUEST_TIMEOUT", 15*time.Second, time.Second, 2*time.Minute),
		ShutdownTimeout: l.duration("SHUTDOWN_TIMEOUT", 20*time.Second, time.Second, 5*time.Minute),
		MaxBodyBytes:    int64(l.integer("HTTP_MAX_BODY_BYTES", 1<<20, 1024, 64<<20)),
	}
	if c.HTTP.Port == c.HTTP.InternalPort {
		l.fail("HTTP_PORT and INTERNAL_HTTP_PORT must differ")
	}
	for _, raw := range l.list("TRUSTED_PROXY_CIDRS") {
		p, err := netip.ParsePrefix(raw)
		if err != nil {
			l.fail("TRUSTED_PROXY_CIDRS contains an invalid CIDR")
			continue
		}
		c.HTTP.TrustedProxyCIDRs = append(c.HTTP.TrustedProxyCIDRs, p.Masked())
	}
	for _, origin := range l.list("CORS_ALLOWED_ORIGINS") {
		if origin == "*" {
			l.fail("CORS_ALLOWED_ORIGINS must list explicit origins; \"*\" is not allowed")
			continue
		}
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || (u.Path != "" && u.Path != "/") {
			l.fail("CORS_ALLOWED_ORIGINS contains an invalid origin (expected scheme://host[:port])")
			continue
		}
		if u.Scheme != "https" && !env.IsLocal() {
			l.fail("CORS_ALLOWED_ORIGINS must use https outside development and test")
		}
		c.HTTP.CORSAllowedOrigins = append(c.HTTP.CORSAllowedOrigins, u.Scheme+"://"+u.Host)
	}

	// Database
	c.Database = Database{
		URL:            Secret(l.required("DATABASE_URL")),
		MigrationURL:   Secret(l.str("DATABASE_MIGRATION_URL", "")),
		MaxConns:       int32(l.integer("DATABASE_MAX_CONNS", 20, 1, 500)),
		ConnectTimeout: l.duration("DATABASE_CONNECT_TIMEOUT", 5*time.Second, 500*time.Millisecond, time.Minute),
	}
	checkDatabaseURL(l, "DATABASE_URL", c.Database.URL.Reveal(), env)
	if c.Database.MigrationURL != "" {
		checkDatabaseURL(l, "DATABASE_MIGRATION_URL", c.Database.MigrationURL.Reveal(), env)
	}

	// Redis (optional and never authoritative)
	c.Redis = Redis{URL: Secret(l.str("REDIS_URL", "")), Required: l.boolean("REDIS_REQUIRED", false)}
	if c.Redis.Enabled() {
		if u, err := url.Parse(c.Redis.URL.Reveal()); err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") {
			l.fail("REDIS_URL must be a redis:// or rediss:// URL")
		} else if u.Scheme != "rediss" && !env.IsLocal() {
			l.fail("REDIS_URL must use rediss:// (TLS) outside development and test")
		}
	} else if c.Redis.Required {
		l.fail("REDIS_URL is required when REDIS_REQUIRED=true")
	}

	// Object storage: three scoped credentials (I-19)
	c.Storage = Storage{
		Endpoint:       l.str("STORAGE_ENDPOINT", ""),
		Region:         l.str("STORAGE_REGION", "us-east-1"),
		ForcePathStyle: l.boolean("STORAGE_FORCE_PATH_STYLE", false),
		Public: StorageCredential{Bucket: l.str("STORAGE_PUBLIC_BUCKET", ""),
			AccessKeyID: Secret(l.str("STORAGE_PUBLIC_ACCESS_KEY_ID", "")), SecretAccessKey: Secret(l.str("STORAGE_PUBLIC_SECRET_ACCESS_KEY", ""))},
		KYC: StorageCredential{Bucket: l.str("STORAGE_KYC_BUCKET", ""),
			AccessKeyID: Secret(l.str("STORAGE_KYC_ACCESS_KEY_ID", "")), SecretAccessKey: Secret(l.str("STORAGE_KYC_SECRET_ACCESS_KEY", ""))},
		Evidence: StorageCredential{Bucket: l.str("STORAGE_EVIDENCE_BUCKET", ""),
			AccessKeyID: Secret(l.str("STORAGE_EVIDENCE_ACCESS_KEY_ID", "")), SecretAccessKey: Secret(l.str("STORAGE_EVIDENCE_SECRET_ACCESS_KEY", ""))},
	}
	if c.Storage.Enabled() {
		u, err := url.Parse(c.Storage.Endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			l.fail("STORAGE_ENDPOINT must be an http(s) URL")
		} else if u.Scheme != "https" && !env.IsLocal() {
			l.fail("STORAGE_ENDPOINT must use https outside development and test")
		}
		for name, cred := range map[string]StorageCredential{"PUBLIC": c.Storage.Public, "KYC": c.Storage.KYC, "EVIDENCE": c.Storage.Evidence} {
			if !cred.configured() {
				l.fail("STORAGE_%s_BUCKET, STORAGE_%s_ACCESS_KEY_ID and STORAGE_%s_SECRET_ACCESS_KEY are required when STORAGE_ENDPOINT is set", name, name, name)
			}
		}
		b := c.Storage
		if b.Public.Bucket != "" && (b.Public.Bucket == b.KYC.Bucket || b.Public.Bucket == b.Evidence.Bucket || b.KYC.Bucket == b.Evidence.Bucket) {
			l.fail("STORAGE_PUBLIC_BUCKET, STORAGE_KYC_BUCKET and STORAGE_EVIDENCE_BUCKET must be three different buckets (I-26)")
		}
		ids := map[Secret]bool{b.Public.AccessKeyID: true, b.KYC.AccessKeyID: true, b.Evidence.AccessKeyID: true}
		if b.Public.AccessKeyID != "" && len(ids) != 3 {
			l.fail("the three STORAGE_*_ACCESS_KEY_ID credentials must all be different (I-19)")
		}
	} else if !env.IsLocal() {
		l.fail("STORAGE_ENDPOINT is required outside development and test")
	}

	// Logging
	c.Log.Format = l.str("LOG_FORMAT", "json")
	if c.Log.Format != "json" && c.Log.Format != "text" {
		l.fail("LOG_FORMAT must be json or text")
	}
	if c.Log.Format != "json" && !env.IsLocal() {
		l.fail("LOG_FORMAT must be json outside development and test")
	}
	switch strings.ToLower(l.str("LOG_LEVEL", "info")) {
	case "debug":
		c.Log.Level = slog.LevelDebug
		if env == EnvProduction {
			l.fail("LOG_LEVEL=debug is not allowed in production")
		}
	case "info":
		c.Log.Level = slog.LevelInfo
	case "warn":
		c.Log.Level = slog.LevelWarn
	case "error":
		c.Log.Level = slog.LevelError
	default:
		l.fail("LOG_LEVEL must be debug, info, warn or error")
	}

	// Rate limiting (coarse per-client protection; per-route limits arrive with features)
	c.RateLimit = RateLimit{
		Enabled:      l.boolean("RATE_LIMIT_ENABLED", true),
		RequestsPerS: float64(l.integer("RATE_LIMIT_RPS", 20, 1, 10000)),
		Burst:        l.integer("RATE_LIMIT_BURST", 40, 1, 100000),
	}
	if !c.RateLimit.Enabled && !env.IsLocal() {
		l.fail("RATE_LIMIT_ENABLED=false is not allowed outside development and test")
	}

	loadAuth(l, &c, env)
	loadEmailSMS(l, &c, env)
	loadSecurity(l, &c, env)

	if len(l.problems) > 0 {
		sort.Strings(l.problems)
		return Config{}, &ValidationError{Problems: l.problems}
	}
	return c, nil
}

// checkDatabaseURL validates a PostgreSQL URL without ever echoing it.
func checkDatabaseURL(l *loader, key, raw string, env Env) {
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		l.fail("%s must be a postgres:// URL", key)
		return
	}
	mode := u.Query().Get("sslmode")
	if !env.IsLocal() && mode != "verify-full" {
		l.fail("%s must use sslmode=verify-full outside development and test", key)
	}
}

// IsValidationError reports whether err came from configuration validation.
func IsValidationError(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}

func loadAuth(l *loader, c *Config, env Env) {
	a := Auth{
		PublicURL:                   l.str("APP_PUBLIC_URL", "http://localhost:3000"),
		SessionCookieName:           l.str("SESSION_COOKIE_NAME", ""),
		SessionIdleTimeout:          l.duration("SESSION_IDLE_TIMEOUT", 168*time.Hour, 5*time.Minute, 720*time.Hour),
		SessionAbsoluteTimeout:      l.duration("SESSION_ABSOLUTE_TIMEOUT", 720*time.Hour, 10*time.Minute, 2160*time.Hour),
		StaffSessionIdleTimeout:     l.duration("STAFF_SESSION_IDLE_TIMEOUT", 15*time.Minute, time.Minute, 4*time.Hour),
		StaffSessionAbsoluteTimeout: l.duration("STAFF_SESSION_ABSOLUTE_TIMEOUT", 12*time.Hour, 5*time.Minute, 24*time.Hour),
		StepUpMaxAge:                l.duration("STEP_UP_MAX_AGE", 10*time.Minute, time.Minute, time.Hour),
		MFAChallengeTTL:             l.duration("MFA_CHALLENGE_TTL", 5*time.Minute, time.Minute, 15*time.Minute),
		EmailVerificationTTL:        l.duration("EMAIL_VERIFICATION_TTL", 24*time.Hour, 10*time.Minute, 72*time.Hour),
		PasswordResetTTL:            l.duration("PASSWORD_RESET_TTL", 30*time.Minute, 5*time.Minute, 2*time.Hour),
		StaffInvitationTTL:          l.duration("STAFF_INVITATION_TTL", 48*time.Hour, time.Hour, 168*time.Hour),
		OTPTTL:                      l.duration("OTP_TTL", 5*time.Minute, time.Minute, 5*time.Minute),
		OTPMaxAttempts:              l.integer("OTP_MAX_ATTEMPTS", 5, 1, 5),
		PasswordMinLength:           l.integer("PASSWORD_MIN_LENGTH", 12, 8, 64),
		PasswordMaxLength:           l.integer("PASSWORD_MAX_LENGTH", 256, 64, 1024),
		HashMemoryKiB:               uint32(l.integer("PASSWORD_HASH_MEMORY_KIB", 64*1024, 19*1024, 1024*1024)),
		HashIterations:              uint32(l.integer("PASSWORD_HASH_ITERATIONS", 3, 1, 10)),
		HashParallelism:             uint8(l.integer("PASSWORD_HASH_PARALLELISM", 2, 1, 8)),
		HashMaxConcurrent:           l.integer("PASSWORD_HASH_MAX_CONCURRENT", 4, 1, 64),
		CSRFSecret:                  Secret(l.str("CSRF_SECRET", "")),
	}
	if u, err := url.Parse(a.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") {
		l.fail("APP_PUBLIC_URL must be an origin such as https://fundzim.example")
	} else {
		a.PublicURL = u.Scheme + "://" + u.Host
		if u.Scheme != "https" && !env.IsLocal() {
			l.fail("APP_PUBLIC_URL must use https outside development and test")
		}
	}
	// Browsers reject __Host- cookies without Secure, and Secure cookies are not stored over plain http.
	a.CookieSecure = !env.IsLocal() || strings.HasPrefix(a.PublicURL, "https://")
	if a.SessionCookieName == "" {
		a.SessionCookieName = "fz_session"
		if a.CookieSecure {
			a.SessionCookieName = "__Host-fz_session"
		}
	}
	if !env.IsLocal() && !strings.HasPrefix(a.SessionCookieName, "__Host-") {
		l.fail("SESSION_COOKIE_NAME must start with __Host- outside development and test")
	}
	if strings.HasPrefix(a.SessionCookieName, "__Host-") && !a.CookieSecure {
		l.fail("SESSION_COOKIE_NAME with the __Host- prefix needs https (use fz_session for local http)")
	}
	if !validCookieName(a.SessionCookieName) {
		l.fail("SESSION_COOKIE_NAME contains invalid characters")
	}
	if len(a.CSRFSecret.Reveal()) < 32 {
		l.fail("CSRF_SECRET is required (at least 32 characters, e.g. 64 hex characters)")
	}
	if a.SessionIdleTimeout > a.SessionAbsoluteTimeout || a.StaffSessionIdleTimeout > a.StaffSessionAbsoluteTimeout {
		l.fail("session idle timeouts must not exceed the absolute timeouts")
	}
	if a.PasswordMinLength > a.PasswordMaxLength {
		l.fail("PASSWORD_MIN_LENGTH must not exceed PASSWORD_MAX_LENGTH")
	}
	if !env.IsLocal() && a.HashMemoryKiB < 64*1024 {
		l.fail("PASSWORD_HASH_MEMORY_KIB must be at least 65536 outside development and test")
	}
	c.Auth = a
}

func validCookieName(n string) bool {
	if n == "" {
		return false
	}
	for _, r := range n {
		if !(r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func loadEmailSMS(l *loader, c *Config, env Env) {
	e := Email{
		Provider: l.str("EMAIL_PROVIDER", "smtp"),
		SMTPHost: l.str("SMTP_HOST", "localhost"),
		SMTPPort: l.integer("SMTP_PORT", 1025, 1, 65535),
		SMTPUser: l.str("SMTP_USERNAME", ""),
		SMTPPass: Secret(l.str("SMTP_PASSWORD", "")),
		SMTPTLS:  l.str("SMTP_TLS", "none"),
		From:     l.str("EMAIL_FROM", "FundZim <no-reply@fundzim.invalid>"),
	}
	switch e.Provider {
	case "smtp", "disabled":
	default:
		l.fail("EMAIL_PROVIDER must be smtp or disabled")
	}
	switch e.SMTPTLS {
	case "none", "starttls", "tls":
	default:
		l.fail("SMTP_TLS must be none, starttls or tls")
	}
	if !env.IsLocal() {
		if e.Provider == "smtp" && e.SMTPTLS == "none" {
			l.fail("SMTP_TLS=none is not allowed outside development and test")
		}
		h := strings.ToLower(e.SMTPHost)
		if h == "localhost" || h == "127.0.0.1" || strings.Contains(h, "mailpit") || strings.Contains(h, "fundzim-mail") {
			l.fail("SMTP_HOST points at local development mail infrastructure; not allowed outside development and test")
		}
		if strings.HasSuffix(strings.TrimRight(e.From, ">"), ".invalid") {
			l.fail("EMAIL_FROM must be a real sending address outside development and test")
		}
	}
	c.Email = e

	s := SMS{Provider: l.str("SMS_PROVIDER", "dev_mailpit")}
	switch s.Provider {
	case "dev_mailpit", "disabled":
	default:
		l.fail("SMS_PROVIDER must be dev_mailpit or disabled (no real SMS provider is selected yet)")
	}
	if s.Provider == "dev_mailpit" && !env.IsLocal() {
		l.fail("SMS_PROVIDER=dev_mailpit is refused outside development and test")
	}
	c.SMS = s
}

func loadSecurity(l *loader, c *Config, env Env) {
	s := Security{
		FieldEncryptionProvider: l.str("FIELD_ENCRYPTION_PROVIDER", "local"),
		FieldEncryptionKey:      Secret(l.str("FIELD_ENCRYPTION_LOCAL_KEY", "")),
		BlindIndexKey:           Secret(l.str("BLIND_INDEX_KEY", "")),
	}
	switch s.FieldEncryptionProvider {
	case "local":
		if !env.IsLocal() {
			l.fail("FIELD_ENCRYPTION_PROVIDER=local is refused outside development and test (KMS envelope encryption is required; not implemented yet)")
		}
		if !hex32(s.FieldEncryptionKey.Reveal()) {
			l.fail("FIELD_ENCRYPTION_LOCAL_KEY must be 64 hex characters (32 bytes)")
		}
	case "kms":
		l.fail("FIELD_ENCRYPTION_PROVIDER=kms is not implemented yet (Stage 18)")
	default:
		l.fail("FIELD_ENCRYPTION_PROVIDER must be local or kms")
	}
	if !hex32(s.BlindIndexKey.Reveal()) {
		l.fail("BLIND_INDEX_KEY must be 64 hex characters (32 bytes)")
	}
	if s.BlindIndexKey != "" && s.BlindIndexKey == s.FieldEncryptionKey {
		l.fail("BLIND_INDEX_KEY and FIELD_ENCRYPTION_LOCAL_KEY must differ")
	}
	c.Security = s
}

func hex32(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, r := range v {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
