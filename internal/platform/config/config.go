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
