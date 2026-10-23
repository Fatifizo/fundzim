//go:build integration

package integration

// Stage 18 security performance sample (docs/stage-18/security-testing.md §6). Opt-in: FUNDZIM_IT_PERF=1. It records
// numbers on the development host; the only assertions are generous ceilings that catch pathological regressions.

import (
	"context"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/operations"
	"github.com/Fatifizo/fundzim/internal/platform/clock"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/opsview"
	"github.com/Fatifizo/fundzim/internal/platform/secretstore"
)

type perfStats struct {
	n                         int
	p50, p95, p99, max, total time.Duration
}

func runPerf(n, workers int, fn func(i int) error) (perfStats, error) {
	lat := make([]time.Duration, n)
	var next atomic.Int64
	var wg sync.WaitGroup
	var firstErr atomic.Value
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= n {
					return
				}
				t0 := time.Now()
				if err := fn(i); err != nil {
					firstErr.CompareAndSwap(nil, err)
				}
				lat[i] = time.Since(t0)
			}
		}()
	}
	wg.Wait()
	total := time.Since(start)
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	st := perfStats{n: n, p50: lat[n/2], p95: lat[n*95/100], p99: lat[n*99/100], max: lat[n-1], total: total}
	if e, ok := firstErr.Load().(error); ok {
		return st, e
	}
	return st, nil
}

func logPerf(t *testing.T, name string, workers int, st perfStats) {
	t.Logf("%s: n=%d workers=%d p50=%s p95=%s p99=%s max=%s throughput=%.1f/s", name, st.n, workers, st.p50, st.p95, st.p99, st.max,
		float64(st.n)/st.total.Seconds())
}

func TestSecurityPerformanceSample(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_PERF") != "1" {
		t.Skip("set FUNDZIM_IT_PERF=1 to record the Stage 18 security performance sample")
	}
	need(t, "DATABASE_URL", "SECRETS_KV2_ADDR", "SECRETS_KV2_TOKEN")
	s := newITServer(t, func(c *config.Config) { c.RateLimit.Enabled = false })
	// the per-account login limit (5 per 15 minutes) always applies: 10 accounts, 4 logins each
	emails := make([]string, 10)
	for i := range emails {
		emails[i] = uniqueEmail("secperf")
		s.browser(t).registerVerified(emails[i])
	}
	b := s.browser(t)

	// authentication: Argon2id (64 MiB, t=3, p=2) with at most 4 concurrent hashes (config)
	for round, w := range []int{1, 8} {
		st, err := runPerf(20, w, func(i int) error {
			c := s.browser(t)
			if r := c.login(emails[(i+round*20)%len(emails)], itPassword); r.Status != 200 {
				t.Errorf("login: %d", r.Status)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		logPerf(t, "POST /auth/login (Argon2id)", w, st)
		if st.p95 > 5*time.Second {
			t.Errorf("login p95 %s", st.p95)
		}
	}
	// authorization overhead: session lookup + permissions + CSRF on every request
	b.mustLogin(emails[9], itPassword)
	st, _ := runPerf(500, 10, func(int) error { b.expect(b.do("GET", "/me", nil), 200, ""); return nil })
	logPerf(t, "GET /me (authenticated)", 10, st)
	pub := s.browser(t)
	st, _ = runPerf(500, 10, func(int) error { pub.expect(pub.do("GET", "/version", nil), 200, ""); return nil })
	logPerf(t, "GET /version (public, no session)", 10, st)

	// secret retrieval: the KV v2 client, uncached (a fresh client per call) and cached
	path := "fundzim/test/perf/it-" + randHex(t)[:8]
	baoPut(t, path, randHex(t))
	ref := "secretref://" + path
	newKV := func(ttl time.Duration) *secretstore.KV2 {
		c, err := secretstore.New(secretstore.Config{Addr: os.Getenv("SECRETS_KV2_ADDR"), Mount: os.Getenv("SECRETS_KV2_MOUNT"), Token: os.Getenv("SECRETS_KV2_TOKEN"), CacheTTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	st, err := runPerf(200, 4, func(int) error { _, err := newKV(time.Minute).Resolve(context.Background(), ref); return err })
	if err != nil {
		t.Fatal(err)
	}
	logPerf(t, "secret manager resolve (uncached)", 4, st)
	cached := newKV(time.Minute)
	_, _ = cached.Resolve(context.Background(), ref)
	st, _ = runPerf(2000, 4, func(int) error { _, err := cached.Resolve(context.Background(), ref); return err })
	logPerf(t, "secret manager resolve (cached)", 4, st)

	// audit writes: one event per transaction (the hash-chain trigger serialises writers per table)
	app := pool(t, os.Getenv("DATABASE_URL"))
	for _, w := range []int{1, 8} {
		st, err := runPerf(400, w, func(int) error {
			return pgx.BeginFunc(context.Background(), app, func(tx pgx.Tx) error {
				return audit.Record(context.Background(), tx, audit.Event{Action: "platform.perf.sample", ActorType: "system", TargetType: "perf"})
			})
		})
		if err != nil {
			t.Fatal(err)
		}
		logPerf(t, "audit event write (own transaction)", w, st)
	}

	// incremental audit verification after those writes, and one security scan
	assertChainsIntact(t, ctx(t), "audit.audit_events")
	key := testVerifier(t, 1)
	inc, err := key.VerifyIncremental(context.Background(), "audit.audit_events")
	if err == nil {
		t.Logf("audit incremental verification: seq %d..%d in %s", inc.FromSeq, inc.ToSeq, inc.Duration)
	}
	scanner := &operations.Service{Pool: app, Clock: clock.System, ClearScope: []string{"it_perf_none"},
		Sources: []operations.Source{{Module: "it_perf_none", Snapshot: func(context.Context) (opsview.Snapshot, error) { return opsview.Snapshot{}, nil }}}}
	st, err = runPerf(5, 1, func(int) error { _, err := scanner.SecurityScan(context.Background()); return err })
	if err != nil {
		t.Fatal(err)
	}
	logPerf(t, "security scan (one source, health and backup evidence)", 1, st)
}
