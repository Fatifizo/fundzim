// Package logging builds FundZim's structured slog logger.
//
// Every record carries service, env and version. Attributes whose key looks sensitive (passwords, tokens,
// secrets, cookies, OTPs, card or identity numbers, account numbers) are replaced with "[REDACTED]" at
// any nesting depth, as a safety net behind the rule that such values are never logged (CLAUDE.md,
// docs/architecture/observability.md §2). Request bodies are never logged.
package logging

import (
	"context"
	"io"
	"log/slog"
	"regexp"
)

// Redacted is the replacement value for sensitive attributes.
const Redacted = "[REDACTED]"

var sensitiveKey = regexp.MustCompile(`(?i)(pass(word|wd)?|secret|token|authorization|cookie|session|otp|api[_-]?key|private[_-]?key|credential|card|id_number|passport|account_number|dsn|database_url|redis_url|(^|[_-])(pan|cvv|pin)([_-]|$))`)

// IsSensitiveKey reports whether an attribute key must be redacted.
func IsSensitiveKey(key string) bool { return sensitiveKey.MatchString(key) }

// Options configure New.
type Options struct {
	Service string
	Env     string
	Version string
	Level   slog.Leveler
	Format  string // json | text
}

// New returns a logger writing to w.
func New(w io.Writer, o Options) *slog.Logger {
	ho := &slog.HandlerOptions{Level: o.Level, ReplaceAttr: redact}
	var h slog.Handler
	if o.Format == "text" {
		h = slog.NewTextHandler(w, ho)
	} else {
		h = slog.NewJSONHandler(w, ho)
	}
	return slog.New(h).With(
		slog.String("service", o.Service),
		slog.String("env", o.Env),
		slog.String("version", o.Version),
	)
}

func redact(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.MessageKey || a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.SourceKey {
		return a
	}
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	if freeTextKey.MatchString(a.Key) {
		// error and panic texts come from drivers and libraries (Stage 18, F-S18-07): scrub credential-shaped fragments
		switch a.Value.Kind() {
		case slog.KindString:
			return slog.String(a.Key, ScrubText(a.Value.String()))
		case slog.KindAny:
			if err, ok := a.Value.Any().(error); ok {
				return slog.String(a.Key, ScrubText(err.Error()))
			}
			return slog.String(a.Key, ScrubText(a.Value.String()))
		}
	}
	return a
}

// freeTextKey names attributes that carry library-produced text (errors, panics, stacks, causes).
var freeTextKey = regexp.MustCompile(`(?i)^(error|err|cause|panic|stack|detail|reason)$`)

var scrubbers = []struct {
	re   *regexp.Regexp
	with string
}{
	// URL user info: the password part of a URL (between "user:" and "@host") becomes [REDACTED]
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s:/@]+):[^\s@/]+@`), "$1:" + Redacted + "@"},
	// key=value / key: value pairs whose key looks sensitive
	{regexp.MustCompile(`(?i)\b(pass(word|wd)?|secret|token|api[_-]?key|authorization|session|otp|sslpassword)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&]+)`), "$1$3" + Redacted},
	// bearer and vault tokens in headers
	{regexp.MustCompile(`(?i)\b(bearer|x-vault-token:?)\s+[A-Za-z0-9._~+/=-]{8,}`), "$1 " + Redacted},
	// long hex or base64url runs (keys, tokens, hashes of secrets)
	{regexp.MustCompile(`\b[0-9a-fA-F]{40,}\b`), Redacted},
	{regexp.MustCompile(`\b[A-Za-z0-9_-]{43,}\b`), Redacted},
}

// ScrubText removes credential-shaped fragments from free text (error messages, panic values). It is a safety net:
// code must still never put a secret into an error.
func ScrubText(s string) string {
	for _, sc := range scrubbers {
		s = sc.re.ReplaceAllString(s, sc.with)
	}
	return s
}

type ctxKey struct{}

// WithContext stores a request-scoped logger in ctx.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext returns the request-scoped logger, or fallback when none is stored.
func FromContext(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return fallback
}
