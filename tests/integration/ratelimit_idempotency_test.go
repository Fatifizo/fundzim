//go:build integration

// Stage 4 work stream B: distributed rate limiting (Valkey), trusted client IP and idempotency
// (PostgreSQL) against the REAL local stack. Valkey outages are simulated with an in-test TCP proxy in
// front of Valkey, so the shared fundzim-redis container keeps running. Set FUNDZIM_IT_STOP_VALKEY=1 (and
// run with docker access, e.g. `sg docker -c "go test ..."`) to also stop and restart the real container.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Fatifizo/fundzim/internal/platform/authz"
	"github.com/Fatifizo/fundzim/internal/platform/cache"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/idempotency"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
)

var itQuiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func cacheClient(t *testing.T, rawURL string) *cache.Client {
	t.Helper()
	c, err := cache.Open(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func rawRedis(t *testing.T) *redis.Client {
	t.Helper()
	opt, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	c := redis.NewClient(opt)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// testPolicy returns a unique policy name so runs never share state.
func testPolicy(limit int, window time.Duration) ratelimit.Policy {
	return ratelimit.Policy{Name: "test." + strings.ReplaceAll(ids.New()[24:], "-", ""), Limit: limit, Window: window, Protective: true}
}

func cleanupPolicyKeys(t *testing.T, rc *redis.Client, policy string) {
	t.Cleanup(func() {
		c := context.Background()
		iter := rc.Scan(c, 0, "rl:"+policy+":*", 100).Iterator()
		for iter.Next(c) {
			rc.Del(c, iter.Val())
		}
	})
}

func TestRateLimitAtomicUnderConcurrency(t *testing.T) {
	need(t, "REDIS_URL")
	l := ratelimit.New(cacheClient(t, os.Getenv("REDIS_URL")).Scripter(), ratelimit.Options{Timeout: 2 * time.Second})
	p := testPolicy(50, time.Hour)
	cleanupPolicyKeys(t, rawRedis(t), p.Name)
	var allowed, degraded atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d, err := l.Allow(context.Background(), p, "same-key")
			if err != nil {
				t.Error(err)
				return
			}
			if d.Degraded {
				degraded.Add(1)
			}
			if d.Allowed {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if degraded.Load() != 0 {
		t.Fatalf("%d decisions were degraded; Valkey must decide", degraded.Load())
	}
	if allowed.Load() != 50 {
		t.Fatalf("allowed %d of 200, want exactly 50", allowed.Load())
	}
}

func TestRateLimitTwoReplicasShareOneLimit(t *testing.T) {
	need(t, "REDIS_URL")
	// Two independent clients + limiters = two API replicas.
	a := ratelimit.New(cacheClient(t, os.Getenv("REDIS_URL")).Scripter(), ratelimit.Options{Timeout: 2 * time.Second})
	b := ratelimit.New(cacheClient(t, os.Getenv("REDIS_URL")).Scripter(), ratelimit.Options{Timeout: 2 * time.Second})
	p := testPolicy(20, time.Hour)
	cleanupPolicyKeys(t, rawRedis(t), p.Name)
	var allowedA, allowedB atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, n := a, &allowedA
			if i%2 == 1 {
				l, n = b, &allowedB
			}
			d, err := l.Allow(context.Background(), p, "shared@example.com")
			if err != nil || d.Degraded {
				t.Errorf("err=%v degraded=%v", err, d.Degraded)
				return
			}
			if d.Allowed {
				n.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if total := allowedA.Load() + allowedB.Load(); total != 20 {
		t.Fatalf("replicas allowed %d+%d=%d, want one shared limit of 20", allowedA.Load(), allowedB.Load(), total)
	}
	if allowedA.Load() == 0 || allowedB.Load() == 0 {
		t.Logf("note: one replica took all tokens (%d/%d)", allowedA.Load(), allowedB.Load())
	}
}

func TestRateLimitKeysAreHashedWithTTL(t *testing.T) {
	need(t, "REDIS_URL")
	rc := rawRedis(t)
	l := ratelimit.New(cacheClient(t, os.Getenv("REDIS_URL")).Scripter(), ratelimit.Options{Timeout: 2 * time.Second})
	p := testPolicy(5, 10*time.Minute)
	cleanupPolicyKeys(t, rc, p.Name)
	raw := []string{"victim-" + ids.New()[:8] + "@example.com", "198.51.100.77", "+263771234567"}
	for _, k := range raw {
		if d, err := l.Allow(context.Background(), p, k); err != nil || !d.Allowed || d.Degraded {
			t.Fatalf("allow %q: %+v %v", k, d, err)
		}
	}
	c := ctx(t)
	var keys []string
	iter := rc.Scan(c, 0, "rl:"+p.Name+":*", 100).Iterator()
	for iter.Next(c) {
		keys = append(keys, iter.Val())
	}
	if len(keys) != len(raw) {
		t.Fatalf("expected %d keys, got %v", len(raw), keys)
	}
	for _, k := range keys {
		for _, r := range raw {
			if strings.Contains(k, r) {
				t.Fatalf("raw input %q stored in Valkey key %q", r, k)
			}
		}
		if len(k) != len("rl:"+p.Name+":")+32 {
			t.Fatalf("unexpected key shape %q", k)
		}
		v, _ := rc.Get(c, k).Result()
		for _, r := range raw {
			if strings.Contains(v, r) {
				t.Fatalf("raw input stored in value")
			}
		}
		ttl, err := rc.PTTL(c, k).Result()
		if err != nil || ttl <= 0 || ttl > p.Window {
			t.Fatalf("key %s TTL %v (err %v), want 0 < ttl <= %v", k, ttl, err, p.Window)
		}
	}
	for _, r := range raw {
		if n, _ := rc.Exists(c, ratelimit.StorageKey(p.Name, r)).Result(); n != 1 {
			t.Fatalf("expected key for %q at StorageKey", r)
		}
	}
}

func TestRateLimitSpoofedForwardedForCannotChangeKey(t *testing.T) {
	need(t, "REDIS_URL")
	l := ratelimit.New(cacheClient(t, os.Getenv("REDIS_URL")).Scripter(), ratelimit.Options{Timeout: 2 * time.Second})
	p := testPolicy(3, time.Hour)
	cleanupPolicyKeys(t, rawRedis(t), p.Name)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	serve := func(trusted []netip.Prefix) *httptest.Server {
		h := httpx.Chain(ok, httpx.RequestIDMiddleware(trusted), httpx.ClientIPMiddleware(trusted),
			ratelimit.Middleware(l, p, ratelimit.ByClientIP, itQuiet))
		s := httptest.NewServer(h)
		t.Cleanup(s.Close)
		return s
	}
	send := func(s *httptest.Server, i int) *http.Response {
		req, _ := http.NewRequest("POST", s.URL+"/x", nil)
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		req.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i))
		req.Header.Set("Forwarded", fmt.Sprintf("for=192.0.2.%d", i))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res
	}
	// The test client connects from 127.0.0.1, which is NOT trusted here: every request shares one key.
	untrusted := serve([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})
	codes := []int{}
	for i := 1; i <= 5; i++ {
		codes = append(codes, send(untrusted, i).StatusCode)
	}
	if fmt.Sprint(codes) != "[204 204 204 429 429]" {
		t.Fatalf("spoofed headers changed the key: %v", codes)
	}
	res := send(untrusted, 9)
	if res.StatusCode != 429 || res.Header.Get("Retry-After") == "" {
		t.Fatalf("expected 429 with Retry-After, got %d", res.StatusCode)
	}
	// Control: when the peer IS a trusted proxy, X-Forwarded-For selects the key.
	trusted := serve([]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")})
	for i := 10; i < 15; i++ {
		if c := send(trusted, i).StatusCode; c != 204 {
			t.Fatalf("trusted proxy, distinct clients should not share a key: %d", c)
		}
	}
}

// tcpProxy forwards to target and can be taken down and brought back on the same address.
type tcpProxy struct {
	addr, target string
	mu           sync.Mutex
	ln           net.Listener
	conns        map[net.Conn]struct{}
}

func newTCPProxy(t *testing.T, target string) *tcpProxy {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tcpProxy{addr: ln.Addr().String(), target: target, conns: map[net.Conn]struct{}{}}
	p.serve(ln)
	t.Cleanup(p.down)
	return p
}

func (p *tcpProxy) serve(ln net.Listener) {
	p.mu.Lock()
	p.ln = ln
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", p.target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns[c], p.conns[up] = struct{}{}, struct{}{}
			p.mu.Unlock()
			go func() { _, _ = io.Copy(up, c); _ = up.Close() }()
			go func() { _, _ = io.Copy(c, up); _ = c.Close() }()
		}
	}()
}

func (p *tcpProxy) down() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		_ = p.ln.Close()
		p.ln = nil
	}
	for c := range p.conns {
		_ = c.Close()
	}
	p.conns = map[net.Conn]struct{}{}
}

func (p *tcpProxy) up(t *testing.T) {
	ln, err := net.Listen("tcp", p.addr)
	if err != nil {
		t.Fatal(err)
	}
	p.serve(ln)
}

// stateMayBeLost: a restarted Valkey without persistence forgets counters (it is never authoritative).
func assertFallbackAndRecovery(t *testing.T, l *ratelimit.Distributed, stop, start func(), stateMayBeLost bool) {
	t.Helper()
	p := testPolicy(3, time.Hour)
	cleanupPolicyKeys(t, rawRedis(t), p.Name)
	if d, err := l.Allow(context.Background(), p, "k"); err != nil || d.Degraded || !d.Allowed {
		t.Fatalf("healthy: %+v %v", d, err)
	}
	stop()
	allowed := 0
	for i := 0; i < 6; i++ {
		d, err := l.Allow(context.Background(), p, "k")
		if err != nil {
			t.Fatal(err)
		}
		if !d.Degraded {
			t.Fatalf("decision %d not degraded while Valkey is down: %+v", i, d)
		}
		if d.Allowed {
			allowed++
		}
	}
	// The fallback is per replica and starts empty, so it allows its own Limit and then denies.
	if allowed != 3 {
		t.Fatalf("protective fallback allowed %d of 6, want 3 (never allow-all)", allowed)
	}
	start()
	deadline := time.Now().Add(30 * time.Second)
	for {
		d, err := l.Allow(context.Background(), p, "k")
		if err != nil {
			t.Fatal(err)
		}
		if !d.Degraded {
			// Valkey still holds the pre-outage state (1 used of 3), so this is the 2nd allowed event.
			if !d.Allowed || (d.Remaining != 1 && !(stateMayBeLost && d.Remaining == 2)) {
				t.Fatalf("after recovery: %+v", d)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("limiter did not recover after Valkey came back")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestRateLimitFallbackWhenValkeyUnavailable(t *testing.T) {
	need(t, "REDIS_URL")
	u, err := url.Parse(os.Getenv("REDIS_URL"))
	if err != nil {
		t.Fatal(err)
	}
	proxy := newTCPProxy(t, u.Host)
	u.Host = proxy.addr
	l := ratelimit.New(cacheClient(t, u.String()).Scripter(), ratelimit.Options{Timeout: time.Second, RetryInterval: 200 * time.Millisecond})
	assertFallbackAndRecovery(t, l, proxy.down, func() { proxy.up(t) }, false)
}

func TestRateLimitFallbackWhenValkeyContainerStopped(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_STOP_VALKEY") != "1" {
		t.Skip("set FUNDZIM_IT_STOP_VALKEY=1 (with docker access) to stop/start the real fundzim-redis container")
	}
	need(t, "REDIS_URL")
	docker := func(args ...string) string {
		out, err := exec.Command("docker", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("docker %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	// The compose service is fundzim-redis; the container name carries the project prefix.
	id := docker("ps", "-q", "--filter", "label=com.docker.compose.service=fundzim-redis")
	if id == "" || strings.Contains(id, "\n") {
		t.Fatalf("expected exactly one running fundzim-redis container, got %q", id)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "start", id).Run() })
	l := ratelimit.New(cacheClient(t, os.Getenv("REDIS_URL")).Scripter(), ratelimit.Options{Timeout: time.Second, RetryInterval: 200 * time.Millisecond})
	assertFallbackAndRecovery(t, l, func() { docker("stop", id) }, func() { docker("start", id) }, true)
}

// ---------------------------------------------------------------------------------------------------
// Idempotency

type idemEnv struct {
	srv   *httptest.Server
	scope string
	runs  atomic.Int64
	// behaviour of the handler, switchable per test
	mu     sync.Mutex
	status int
	delay  time.Duration
}

func newIdemEnv(t *testing.T, mode idempotency.Mode) *idemEnv {
	need(t, "DATABASE_URL")
	p := pool(t, os.Getenv("DATABASE_URL"))
	store := idempotency.NewStore(p, nil, time.Hour)
	store.SetLease(10 * time.Second)
	e := &idemEnv{scope: "test.idem." + strings.ReplaceAll(ids.New()[24:], "-", ""), status: 201}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := p.Exec(c, `DELETE FROM app.idempotency_keys WHERE scope LIKE $1`, e.scope+":%"); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	rt := httpx.NewRouter(itQuiet)
	rt.HandleFunc("POST /it/things", httpx.Public(), func(w http.ResponseWriter, r *http.Request) {
		n := e.runs.Add(1)
		e.mu.Lock()
		status, delay := e.status, e.delay
		e.mu.Unlock()
		time.Sleep(delay)
		if status >= 500 {
			httpx.WriteError(w, r, itQuiet, errors.New("boom"))
			return
		}
		body, _ := io.ReadAll(r.Body)
		httpx.WriteData(w, r, status, map[string]any{"run": n, "echo": json.RawMessage(body)})
	}, httpx.With(idempotency.Middleware(store, mode, e.scope, itQuiet)))
	// Test-only principal injection (the real auth middleware does this from the session).
	withUser := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u := r.Header.Get("X-Test-User"); u != "" {
				r = r.WithContext(authz.WithPrincipal(r.Context(), &authz.Principal{UserID: u, Kind: authz.KindUser}))
			}
			next.ServeHTTP(w, r)
		})
	}
	h := httpx.Chain(rt, httpx.RequestIDMiddleware(nil), httpx.AccessLog(itQuiet, nil), httpx.ClientIPMiddleware(nil), withUser)
	e.srv = httptest.NewServer(h)
	t.Cleanup(e.srv.Close)
	return e
}

type idemResp struct {
	status   int
	replayed bool
	retry    string
	code     string
	run      int64
	raw      string
}

func (e *idemEnv) post(t *testing.T, key, body, user string) idemResp {
	req, _ := http.NewRequest("POST", e.srv.URL+"/it/things", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		return idemResp{}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var env struct {
		Data struct {
			Run int64 `json:"run"`
		} `json:"data"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	return idemResp{status: res.StatusCode, replayed: res.Header.Get("Idempotent-Replayed") == "true",
		retry: res.Header.Get("Retry-After"), code: env.Error.Code, run: env.Data.Run, raw: string(raw)}
}

func TestIdempotencyConcurrentRequestsRunHandlerOnce(t *testing.T) {
	e := newIdemEnv(t, idempotency.Required)
	e.delay = 500 * time.Millisecond
	key := ids.New()
	const n = 50
	results := make([]idemResp, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = e.post(t, key, `{"amount_minor":"1000","currency":"USD"}`, "")
		}(i)
	}
	close(start)
	wg.Wait()
	if got := e.runs.Load(); got != 1 {
		t.Fatalf("handler ran %d times, want exactly 1", got)
	}
	var fresh, replays, inProgress int
	for _, r := range results {
		switch {
		case r.status == 201 && !r.replayed && r.run == 1:
			fresh++
		case r.status == 201 && r.replayed && r.run == 1:
			replays++
		case r.status == 409 && r.code == idempotency.CodeInProgress && r.retry != "":
			inProgress++
		default:
			t.Fatalf("inconsistent response: %+v", r)
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh=%d replays=%d inProgress=%d", fresh, replays, inProgress)
	}
	t.Logf("50 concurrent: fresh=%d replayed=%d in_progress=%d", fresh, replays, inProgress)
	// After completion, the same request is replayed with the stored body.
	r := e.post(t, key, `{"currency":"USD", "amount_minor":"1000"}`, "") // same canonical JSON
	if r.status != 201 || !r.replayed || r.run != 1 || e.runs.Load() != 1 {
		t.Fatalf("replay after completion: %+v", r)
	}
}

func TestIdempotencyKeyReusedWithDifferentBody(t *testing.T) {
	e := newIdemEnv(t, idempotency.Optional)
	key := ids.New()
	if r := e.post(t, key, `{"a":1}`, ""); r.status != 201 || r.replayed {
		t.Fatalf("first: %+v", r)
	}
	r := e.post(t, key, `{"a":2}`, "")
	if r.status != 409 || r.code != idempotency.CodeKeyReused || !strings.Contains(r.raw, `"retryable":false`) {
		t.Fatalf("reuse: %+v", r)
	}
	if e.runs.Load() != 1 {
		t.Fatal("handler ran for a reused key")
	}
	// The same key under another principal is a different scope.
	if r := e.post(t, key, `{"a":1}`, ids.New()); r.status != 201 || r.replayed {
		t.Fatalf("other principal must not see the record: %+v", r)
	}
}

func TestIdempotencyServerErrorIsRetryable(t *testing.T) {
	e := newIdemEnv(t, idempotency.Required)
	key := ids.New()
	e.status = 500
	if r := e.post(t, key, `{"a":1}`, ""); r.status != 500 {
		t.Fatalf("first: %+v", r)
	}
	e.mu.Lock()
	e.status = 201
	e.mu.Unlock()
	r := e.post(t, key, `{"a":1}`, "")
	if r.status != 201 || r.replayed || r.run != 2 {
		t.Fatalf("retry after 5xx must execute again: %+v", r)
	}
	// Deterministic 4xx responses are stored and replayed identically.
	key2 := ids.New()
	e.mu.Lock()
	e.status = 422
	e.mu.Unlock()
	first := e.post(t, key2, `{"b":1}`, "u-1")
	again := e.post(t, key2, `{"b":1}`, "u-1")
	if first.status != 422 || again.status != 422 || !again.replayed || again.run != first.run {
		t.Fatalf("4xx replay: %+v / %+v", first, again)
	}
}

func TestIdempotencyExpiredLeaseIsTakenOver(t *testing.T) {
	e := newIdemEnv(t, idempotency.Required)
	p := pool(t, os.Getenv("DATABASE_URL"))
	body := `{"c":1}`
	insert := func(key string, lockedUntil time.Time) {
		scope := e.scope + ":anon:127.0.0.1"
		hash := idempotency.Fingerprint("POST", "POST /it/things", "/it/things", "", []byte(body))
		now := time.Now().UTC()
		if _, err := p.Exec(ctx(t), `INSERT INTO app.idempotency_keys (id, scope, key, request_hash, status, locked_until, created_at, updated_at, expires_at)
			VALUES ($1, $2, $3, $4, 'IN_PROGRESS', $5, $6, $6, $7)`, ids.New(), scope, key, hash, lockedUntil, now.Add(-time.Minute), now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// A live lease held by "another replica" → 409 in progress, with Retry-After.
	live := ids.New()
	insert(live, time.Now().Add(30*time.Second))
	if r := e.post(t, live, body, ""); r.status != 409 || r.code != idempotency.CodeInProgress || r.retry == "" || e.runs.Load() != 0 {
		t.Fatalf("live lease: %+v", r)
	}
	// An expired lease (crashed owner) → this request takes over and runs once; later replays.
	stale := ids.New()
	insert(stale, time.Now().Add(-time.Second))
	if r := e.post(t, stale, body, ""); r.status != 201 || r.replayed || e.runs.Load() != 1 {
		t.Fatalf("takeover: %+v", r)
	}
	if r := e.post(t, stale, body, ""); r.status != 201 || !r.replayed || e.runs.Load() != 1 {
		t.Fatalf("replay after takeover: %+v", r)
	}
}

func TestIdempotencyRequiredHeaderAndExpiry(t *testing.T) {
	e := newIdemEnv(t, idempotency.Required)
	if r := e.post(t, "", `{}`, ""); r.status != 400 || r.code != idempotency.CodeKeyRequired {
		t.Fatalf("missing: %+v", r)
	}
	if r := e.post(t, "not-a-uuid", `{}`, ""); r.status != 400 || r.code != idempotency.CodeKeyInvalid {
		t.Fatalf("malformed: %+v", r)
	}
	if e.runs.Load() != 0 {
		t.Fatal("handler ran without a valid key")
	}
	// DeleteExpired purges records past expires_at.
	p := pool(t, os.Getenv("DATABASE_URL"))
	id := ids.New()
	past := time.Now().UTC().Add(-48 * time.Hour)
	if _, err := p.Exec(ctx(t), `INSERT INTO app.idempotency_keys (id, scope, key, request_hash, status, locked_until, created_at, updated_at, expires_at)
		VALUES ($1, $2, $3, $4, 'IN_PROGRESS', $5, $5, $5, $6)`, id, e.scope+":anon:x", ids.New(), bytes.Repeat([]byte{1}, 32), past, past.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	n, err := idempotency.NewStore(p, nil, 0).DeleteExpired(ctx(t))
	if err != nil || n < 1 {
		t.Fatalf("DeleteExpired: %d %v", n, err)
	}
	var left int
	_ = p.QueryRow(ctx(t), `SELECT count(*) FROM app.idempotency_keys WHERE id = $1`, id).Scan(&left)
	if left != 0 {
		t.Fatal("expired record not purged")
	}
}
