package psp

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// SecretResolver resolves a secret REFERENCE (secretref://…) to its value at call time. Values never reach the
// database, logs, traces, errors or API responses (payment-provider-interfaces §4).
type SecretResolver interface {
	Resolve(ctx context.Context, ref string) ([]byte, error)
}

// ErrSecretUnavailable means the reference does not resolve (missing or malformed). The message never contains
// the value.
var ErrSecretUnavailable = errors.New("psp: secret reference does not resolve")

var secretRefRE = regexp.MustCompile(`^secretref://[a-z0-9][a-z0-9/_.-]{2,200}$`)

// ValidSecretRef reports whether s is a well-formed secret reference (same pattern as the DB CHECK).
func ValidSecretRef(s string) bool { return secretRefRE.MatchString(s) }

// EnvVarFor maps a reference to the environment variable that holds it: SECRETREF_ + the path upper-cased with
// every non-alphanumeric character replaced by '_' (secretref://fundzim/sandbox/webhook-current →
// SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT).
func EnvVarFor(ref string) (string, bool) {
	if !ValidSecretRef(ref) {
		return "", false
	}
	path := strings.TrimPrefix(ref, "secretref://")
	var b strings.Builder
	b.WriteString("SECRETREF_")
	for _, r := range strings.ToUpper(path) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String(), true
}

// EnvResolver resolves references from the process environment (the platform's secret mechanism today; a
// KMS/secret-manager resolver replaces it before any real provider, see docs/stage-8/known-issues.md).
type EnvResolver struct {
	Lookup func(string) (string, bool)
}

// Resolve implements SecretResolver. Values shorter than 32 bytes are refused (weak webhook keys).
func (e EnvResolver) Resolve(_ context.Context, ref string) ([]byte, error) {
	name, ok := EnvVarFor(ref)
	if !ok || e.Lookup == nil {
		return nil, ErrSecretUnavailable
	}
	v, ok := e.Lookup(name)
	if !ok || len(v) < 32 {
		return nil, ErrSecretUnavailable
	}
	return []byte(v), nil
}

// Resolvable reports whether a reference resolves, without exposing the value.
func Resolvable(ctx context.Context, r SecretResolver, ref string) bool {
	if r == nil || ref == "" {
		return false
	}
	v, err := r.Resolve(ctx, ref)
	return err == nil && len(v) > 0
}
