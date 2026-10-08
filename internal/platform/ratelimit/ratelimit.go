// Package ratelimit is FundZim's distributed rate limiter (docs/stage-4/interface-contracts.md §3.2,
// docs/api/api-design.md §8, SECURITY §10).
//
// Decisions are made atomically in Valkey by one Lua script implementing GCRA (generic cell rate
// algorithm, a token bucket expressed as a "theoretical arrival time"): a policy of Limit events per
// Window allows a burst of Limit and then refills continuously at Limit/Window. Nothing is ever locked
// permanently; an idle key decays to full capacity after at most Window.
//
// Keys stored in Valkey are rl:<policy>:<hex sha256(key)[:32]>, so no raw email address, phone number
// or IP address is ever written to Valkey. Every key has a TTL equal to the time it takes to decay.
//
// When Valkey is unavailable the limiter never fails open: it decides with an in-process GCRA limiter
// with the same parameters (per replica) and marks the decision Degraded.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/logging"
)

// Policy is one named limit.
type Policy struct {
	Name       string        // e.g. "auth.login.account"; bounded set, used as a metric label
	Limit      int           // events per Window (also the burst size)
	Window     time.Duration // time for an exhausted key to fully recover
	Protective bool          // true for authentication throttles: on Valkey failure use the local fallback, never allow-all
}

// Validate reports whether p is usable.
func (p Policy) Validate() error {
	switch {
	case p.Name == "" || len(p.Name) > 64:
		return errors.New("ratelimit: policy name must be 1-64 characters")
	case p.Limit < 1:
		return fmt.Errorf("ratelimit: policy %s: limit must be >= 1", p.Name)
	case p.Window < time.Millisecond:
		return fmt.Errorf("ratelimit: policy %s: window must be >= 1ms", p.Name)
	case p.Window/time.Duration(p.Limit) < time.Microsecond:
		return fmt.Errorf("ratelimit: policy %s: rate is too high", p.Name)
	}
	return nil
}

// Decision is the outcome of one Allow call.
type Decision struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
	Degraded   bool // decided by the local fallback because Valkey was unavailable
}

// Limiter decides whether one more event for key is allowed under p. It returns an error only for an
// invalid policy; infrastructure failures are absorbed by the fallback (never allow-all).
type Limiter interface {
	Allow(ctx context.Context, p Policy, key string) (Decision, error)
}

// hashKey returns the first 32 hex characters of SHA-256(key).
func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

// StorageKey returns the Valkey key for (policy, key): rl:<policy>:<hex sha256(key)[:32]>.
func StorageKey(policy, key string) string { return "rl:" + policy + ":" + hashKey(key) }

// gcra is the shared algorithm (the Lua script mirrors it exactly). All values are in one time unit.
// interval = Window/Limit (emission interval); burst = interval*Limit.
func gcra(now, storedTAT, interval, burst int64) (allowed bool, newTAT int64, remaining int, retryAfter int64) {
	tat := storedTAT
	if tat < now {
		tat = now
	}
	newTAT = tat + interval
	allowAt := newTAT - burst
	if now < allowAt {
		return false, storedTAT, 0, allowAt - now
	}
	return true, newTAT, int((now - allowAt) / interval), 0
}

func params(p Policy, unit time.Duration) (interval, burst int64) {
	interval = int64(p.Window/time.Duration(p.Limit)) / int64(unit)
	if interval < 1 {
		interval = 1
	}
	return interval, interval * int64(p.Limit)
}

// KeyFunc extracts the limiter key from a request. ok=false means the dimension does not apply (for
// example no principal on ByPrincipal) and the policy is skipped for that request.
type KeyFunc func(r *http.Request) (key string, ok bool)

// ByClientIP keys by the resolved client address (authz.ClientIPFrom, set by
// httpx.ClientIPMiddleware). IPv6 addresses are aggregated to their /64, because a single subscriber
// usually controls a whole /64. Without the middleware it falls back to the direct peer address
// (forwarding headers are never parsed here). It always returns ok=true.
func ByClientIP(r *http.Request) (string, bool) {
	ip := authz.ClientIPFrom(r.Context())
	if !ip.IsValid() {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip, _ = netip.ParseAddr(host)
	}
	return IPKey(ip), true
}

// IPKey normalises an address into a limiter key ("ip:<v4>" or "ip6:<v6 /64>"); "ip:unknown" when invalid.
func IPKey(ip netip.Addr) string {
	if !ip.IsValid() {
		return "ip:unknown"
	}
	ip = ip.Unmap().WithZone("")
	if ip.Is6() {
		p, _ := ip.Prefix(64)
		return "ip6:" + p.String()
	}
	return "ip:" + ip.String()
}

// ByPrincipal keys by the authenticated user ID; ok=false for anonymous requests.
func ByPrincipal(r *http.Request) (string, bool) {
	p := authz.PrincipalFrom(r.Context())
	if p == nil || p.UserID == "" {
		return "", false
	}
	return "user:" + p.UserID, true
}

// ByPrincipalOrIP keys by user ID when authenticated, otherwise by client IP.
func ByPrincipalOrIP(r *http.Request) (string, bool) {
	if k, ok := ByPrincipal(r); ok {
		return k, true
	}
	return ByClientIP(r)
}

// Middleware applies p to the route; on deny it writes 429 RATE_LIMITED with Retry-After.
// Infrastructure probes (/healthz, /readyz) are never limited (RL-HEALTH). It panics on an invalid policy
// (a programming error caught at start-up).
func Middleware(l Limiter, p Policy, key KeyFunc, logger *slog.Logger) httpx.Middleware {
	if err := p.Validate(); err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			k, ok := key(r)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			d, err := l.Allow(r.Context(), p, k)
			if err != nil {
				// Never fail open.
				httpx.WriteError(w, r, logger, errs.Wrap(err, errs.Internal, errs.CodeInternal, "An unexpected error occurred."))
				return
			}
			if !d.Allowed {
				logging.FromContext(r.Context(), logger).Debug("rate limited",
					slog.String("policy", p.Name), slog.Bool("degraded", d.Degraded))
				WriteRateLimited(w, r, logger, d)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteRateLimited writes 429 RATE_LIMITED with Retry-After (whole seconds, at least 1). Handlers that
// enforce account-keyed policies themselves (login per email, OTP per phone) use it after Allow.
func WriteRateLimited(w http.ResponseWriter, r *http.Request, logger *slog.Logger, d Decision) {
	w.Header().Set("Retry-After", strconv.Itoa(RetryAfterSeconds(d.RetryAfter)))
	httpx.WriteError(w, r, logger, errs.New(errs.RateLimited, errs.CodeRateLimited, "Too many requests. Please slow down."))
}

// RetryAfterSeconds rounds d up to whole seconds, minimum 1.
func RetryAfterSeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}
