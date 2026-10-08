package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
)

func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestPolicyValidate(t *testing.T) {
	bad := []Policy{{}, {Name: "x", Limit: 0, Window: time.Second}, {Name: "x", Limit: 1}, {Name: "x", Limit: 1 << 30, Window: time.Millisecond}}
	for _, p := range bad {
		if p.Validate() == nil {
			t.Fatalf("expected invalid: %+v", p)
		}
	}
	if err := DefaultPolicies().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, p := range DefaultPolicies().All() {
		if !p.Protective {
			t.Fatalf("%s must be protective", p.Name)
		}
	}
}

func TestDefaultPolicyValues(t *testing.T) {
	ps := DefaultPolicies()
	want := map[string][2]any{
		"global.ip": {40, 2 * time.Second}, "auth.login.ip": {20, 5 * time.Minute}, "auth.login.account": {5, 15 * time.Minute},
		"auth.register.ip": {5, time.Hour}, "auth.password_reset.ip": {5, time.Hour}, "auth.password_reset.account": {3, time.Hour},
		"auth.verify_resend.account": {3, time.Hour}, "auth.mfa.challenge": {5, 5 * time.Minute}, "auth.otp.send.phone": {3, time.Hour},
		"auth.otp.send.user": {5, time.Hour}, "auth.otp.verify": {5, 15 * time.Minute}, "auth.step_up": {5, 15 * time.Minute},
		"auth.token.ip": {20, 15 * time.Minute}, "auth.verify_resend.ip": {10, time.Hour},
	}
	seen := map[string]bool{}
	for _, p := range ps.All() {
		w, ok := want[p.Name]
		if !ok || seen[p.Name] {
			t.Fatalf("unexpected or duplicate policy %s", p.Name)
		}
		seen[p.Name] = true
		if p.Limit != w[0].(int) || p.Window != w[1].(time.Duration) {
			t.Fatalf("%s = %d/%s", p.Name, p.Limit, p.Window)
		}
	}
	if len(seen) != len(want) {
		t.Fatal("missing policies")
	}
	g := GlobalIPFromRate(20, 40)
	if g.Limit != 40 || g.Window != 2*time.Second {
		t.Fatalf("GlobalIPFromRate: %+v", g)
	}
}

func TestMemoryGCRA(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	m := NewMemory(clk, 0)
	p := Policy{Name: "t.p", Limit: 5, Window: 15 * time.Minute}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		d, _ := m.Allow(ctx, p, "a@example.com")
		if !d.Allowed || d.Remaining != 4-i {
			t.Fatalf("attempt %d: %+v", i, d)
		}
	}
	d, _ := m.Allow(ctx, p, "a@example.com")
	if d.Allowed || d.RetryAfter != 3*time.Minute {
		t.Fatalf("6th should be denied with retry 3m: %+v", d)
	}
	// Other keys and other policies are independent.
	if d, _ := m.Allow(ctx, p, "b@example.com"); !d.Allowed {
		t.Fatal("other key limited")
	}
	if d, _ := m.Allow(ctx, Policy{Name: "t.q", Limit: 1, Window: time.Minute}, "a@example.com"); !d.Allowed {
		t.Fatal("other policy limited")
	}
	// Denied attempts do not extend the penalty.
	for i := 0; i < 50; i++ {
		m.Allow(ctx, p, "a@example.com")
	}
	clk.Advance(3*time.Minute - time.Second)
	if d, _ := m.Allow(ctx, p, "a@example.com"); d.Allowed {
		t.Fatal("allowed too early")
	}
	clk.Advance(time.Second)
	if d, _ := m.Allow(ctx, p, "a@example.com"); !d.Allowed {
		t.Fatal("one token should have refilled")
	}
	// Full decay after a window: whole burst again (no permanent lockout).
	clk.Advance(15 * time.Minute)
	for i := 0; i < 5; i++ {
		if d, _ := m.Allow(ctx, p, "a@example.com"); !d.Allowed {
			t.Fatalf("after decay attempt %d denied", i)
		}
	}
}

func TestMemoryBoundedAndConcurrent(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	m := NewMemory(clk, 100)
	p := Policy{Name: "t.p", Limit: 50, Window: time.Hour}
	for i := 0; i < 1000; i++ {
		m.Allow(context.Background(), p, strings.Repeat("k", i%7)+string(rune('a'+i%26))+time.Duration(i).String())
	}
	if m.Len() > 100 {
		t.Fatalf("memory unbounded: %d", m.Len())
	}
	var ok atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if d, _ := m.Allow(context.Background(), p, "same"); d.Allowed {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 50 {
		t.Fatalf("allowed %d, want 50", ok.Load())
	}
}

func TestStorageKeyIsHashed(t *testing.T) {
	k := StorageKey("auth.login.account", "victim@example.com")
	if !strings.HasPrefix(k, "rl:auth.login.account:") || len(k) != len("rl:auth.login.account:")+32 || strings.Contains(k, "victim") {
		t.Fatalf("bad key %q", k)
	}
}

// fakeScripter simulates Valkey being down (err) or returning a fixed result.
type fakeScripter struct {
	err   error
	res   []any
	calls atomic.Int64
}

func (f *fakeScripter) cmd(ctx context.Context) *redis.Cmd {
	f.calls.Add(1)
	if f.err != nil {
		return redis.NewCmdResult(nil, f.err)
	}
	return redis.NewCmdResult(f.res, nil)
}
func (f *fakeScripter) Eval(ctx context.Context, _ string, _ []string, _ ...any) *redis.Cmd {
	return f.cmd(ctx)
}
func (f *fakeScripter) EvalSha(ctx context.Context, _ string, _ []string, _ ...any) *redis.Cmd {
	return f.cmd(ctx)
}
func (f *fakeScripter) EvalRO(ctx context.Context, _ string, _ []string, _ ...any) *redis.Cmd {
	return f.cmd(ctx)
}
func (f *fakeScripter) EvalShaRO(ctx context.Context, _ string, _ []string, _ ...any) *redis.Cmd {
	return f.cmd(ctx)
}
func (f *fakeScripter) ScriptExists(ctx context.Context, _ ...string) *redis.BoolSliceCmd {
	return redis.NewBoolSliceResult(nil, f.err)
}
func (f *fakeScripter) ScriptLoad(ctx context.Context, _ string) *redis.StringCmd {
	return redis.NewStringResult("", f.err)
}

func TestDistributedFallbackNeverFailsOpen(t *testing.T) {
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	reg := prometheus.NewRegistry()
	met := NewMetrics(reg)
	fs := &fakeScripter{err: errors.New("dial tcp: connection refused")}
	l := New(fs, Options{Clock: clk, Metrics: met, Logger: quiet, RetryInterval: 2 * time.Second})
	p := Policy{Name: "auth.login.account", Limit: 3, Window: time.Minute, Protective: true}
	allowed := 0
	for i := 0; i < 10; i++ {
		d, err := l.Allow(context.Background(), p, "x")
		if err != nil {
			t.Fatal(err)
		}
		if !d.Degraded {
			t.Fatal("decision must be degraded")
		}
		if d.Allowed {
			allowed++
		}
	}
	if allowed != 3 {
		t.Fatalf("fallback allowed %d, want 3", allowed)
	}
	if fs.calls.Load() != 1 {
		t.Fatalf("circuit breaker should stop calling Valkey: %d calls", fs.calls.Load())
	}
	if got := counterValue(t, met.degraded.WithLabelValues(p.Name)); got != 10 {
		t.Fatalf("degraded metric %v", got)
	}
	if got := counterValue(t, met.decisions.WithLabelValues(p.Name, "denied")); got != 7 {
		t.Fatalf("denied metric %v", got)
	}
	// Recovery: after the retry interval, Valkey is probed again.
	fs.err = nil
	fs.res = []any{int64(1), int64(2), int64(0)}
	clk.Advance(2 * time.Second)
	d, _ := l.Allow(context.Background(), p, "x")
	if d.Degraded || !d.Allowed || d.Remaining != 2 {
		t.Fatalf("expected Valkey decision after recovery: %+v", d)
	}
	// Non-protective policies also use the fallback (never allow-all).
	np := Policy{Name: "np", Limit: 1, Window: time.Hour}
	fs.err = errors.New("down")
	clk.Advance(time.Hour)
	l.Allow(context.Background(), np, "y")
	if d, _ := l.Allow(context.Background(), np, "y"); d.Allowed || !d.Degraded {
		t.Fatalf("non-protective fallback must still limit: %+v", d)
	}
	// Valkey not configured at all.
	nl := New(nil, Options{Clock: clk})
	nl.Allow(context.Background(), np, "z")
	if d, _ := nl.Allow(context.Background(), np, "z"); d.Allowed || !d.Degraded {
		t.Fatalf("nil scripter must use fallback: %+v", d)
	}
}

type fakeLimiter struct {
	keys []string
	d    Decision
	err  error
}

func (f *fakeLimiter) Allow(_ context.Context, _ Policy, key string) (Decision, error) {
	f.keys = append(f.keys, key)
	return f.d, f.err
}

func TestMiddleware(t *testing.T) {
	p := Policy{Name: "t.mw", Limit: 1, Window: time.Minute}
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	run := func(fl *fakeLimiter, kf KeyFunc, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
		h := httpx.Chain(ok, httpx.RequestIDMiddleware(nil), httpx.ClientIPMiddleware(trusted), Middleware(fl, p, kf, quiet))
		req := httptest.NewRequest("POST", path, nil)
		req.RemoteAddr = "203.0.113.5:1234"
		if mod != nil {
			mod(req)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	fl := &fakeLimiter{d: Decision{Allowed: false, RetryAfter: 1500 * time.Millisecond}}
	rec := run(fl, ByClientIP, "/x", nil)
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("deny: %d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Error.Code != "RATE_LIMITED" || !body.Error.Retryable {
		t.Fatalf("body %+v", body)
	}
	// Spoofing from an untrusted peer does not change the key.
	run(fl, ByClientIP, "/x", func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "1.1.1.1")
		r.Header.Set("X-Real-IP", "2.2.2.2")
		r.Header.Set("Forwarded", "for=3.3.3.3")
	})
	if fl.keys[0] != "ip:203.0.113.5" || fl.keys[1] != "ip:203.0.113.5" {
		t.Fatalf("keys %v", fl.keys)
	}
	// Probes are exempt.
	if rec := run(fl, ByClientIP, "/healthz", nil); rec.Code != 204 {
		t.Fatal("probe limited")
	}
	// Errors fail closed.
	if rec := run(&fakeLimiter{err: errors.New("bad")}, ByClientIP, "/x", nil); rec.Code != 500 {
		t.Fatalf("error must fail closed: %d", rec.Code)
	}
	// ByPrincipal skips anonymous requests; keys signed-in users by ID.
	fl2 := &fakeLimiter{d: Decision{Allowed: true}}
	if rec := run(fl2, ByPrincipal, "/x", nil); rec.Code != 204 || len(fl2.keys) != 0 {
		t.Fatal("anonymous request should skip ByPrincipal")
	}
	withUser := func(r *http.Request) {
		*r = *r.WithContext(authz.WithPrincipal(r.Context(), &authz.Principal{UserID: "u-1"}))
	}
	run(fl2, ByPrincipal, "/x", withUser)
	run(fl2, ByPrincipalOrIP, "/x", withUser)
	run(fl2, ByPrincipalOrIP, "/x", nil)
	if strings.Join(fl2.keys, ",") != "user:u-1,user:u-1,ip:203.0.113.5" {
		t.Fatalf("keys %v", fl2.keys)
	}
}

func TestIPKeyAggregatesIPv6(t *testing.T) {
	a := IPKey(netip.MustParseAddr("2001:db8:1:2:aaaa::1"))
	b := IPKey(netip.MustParseAddr("2001:db8:1:2:bbbb::9"))
	if a != b || a != "ip6:2001:db8:1:2::/64" {
		t.Fatalf("%s %s", a, b)
	}
	if IPKey(netip.MustParseAddr("::ffff:198.51.100.7")) != "ip:198.51.100.7" || IPKey(netip.Addr{}) != "ip:unknown" {
		t.Fatal("v4 normalisation")
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.9:1"
	if k, _ := ByClientIP(req); k != "ip:198.51.100.9" {
		t.Fatalf("no middleware fallback: %s", k)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for in, want := range map[time.Duration]int{0: 1, time.Millisecond: 1, time.Second: 1, 1001 * time.Millisecond: 2, time.Minute: 60} {
		if got := RetryAfterSeconds(in); got != want {
			t.Fatalf("%s: %d", in, got)
		}
	}
}
