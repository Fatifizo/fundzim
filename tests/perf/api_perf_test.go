//go:build perf

// Package perf measures the Stage 7 public read API against the scratch database fundzim_perf (seeded by
// tests/perf/seed.sql). It starts the API in-process exactly like tests/integration (app.NewDeps +
// app.PublicHandler over httptest), with every database URL pointed at fundzim_perf and the global per-IP rate
// limit disabled in this harness's config only.
//
// Run (see docs/stage-7/performance.md):
//
//	set -a; . ./.env; . tests/perf/env.sh; set +a
//	go test -tags perf -count=1 -timeout 60m -run TestPublicAPILatency -v ./tests/perf/
//	go test -tags perf -count=1 -run TestSearchStatementTimeout -v ./tests/perf/
//
// Optional: FUNDZIM_PERF_SUPERUSER_URL (a postgres superuser URL for fundzim_perf) enables evicting
// fundzim_perf's pages from shared_buffers (pg_buffercache_evict) before the cold runs. FUNDZIM_PERF_N scales
// the request count per measurement (default 300).
package perf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/platform/config"
)

func need(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "DATABASE_KYC_URL", "DATABASE_COMPLIANCE_URL"} {
		u, err := url.Parse(os.Getenv(k))
		if err != nil || strings.TrimPrefix(u.Path, "/") != "fundzim_perf" {
			t.Fatalf("%s must point at the scratch database fundzim_perf (source .env then tests/perf/env.sh)", k)
		}
	}
}

type server struct {
	srv *httptest.Server
	d   *app.Deps
}

func start(t *testing.T) *server {
	t.Helper()
	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.RateLimit.Enabled = false // harness only: measure the handlers, not the global.ip limiter
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := app.NewDeps(context.Background(), cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(app.PublicHandler(d, app.NewRouter(d, d.Checker())))
	return &server{srv: s, d: d}
}

func (s *server) close() { s.srv.Close(); s.d.Close() }

// fixtures are read from the seeded database.
type fixtures struct {
	slugs       []string // live PUBLIC campaigns (random sample)
	nonCanon    []string // wrong title part + the right public code (canonical-slug fallback path)
	heavySlug   string   // the campaign with the longest update history
	updSlugs    []string // live campaigns with 1..5 updates
	deepCursor  string   // keyset cursor ~20 000 rows deep (default sort)
	catSlugs    []string
	liveCount   int
	totalCount  int
	updateCount int
}

func load(t *testing.T, p *pgxpool.Pool) fixtures {
	t.Helper()
	ctx := context.Background()
	var f fixtures
	collect := func(q string, args ...any) []string {
		rows, err := p.Query(ctx, q, args...)
		if err != nil {
			t.Fatal(err)
		}
		out, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	f.slugs = collect(`SELECT slug FROM app.campaigns WHERE status IN ('ACTIVE','PAUSED','COMPLETED') AND visibility = 'PUBLIC'
		ORDER BY md5(slug) LIMIT 500`)
	f.nonCanon = collect(`SELECT 'old-title-' || lower(public_code) FROM app.campaigns WHERE status IN ('ACTIVE','PAUSED','COMPLETED')
		ORDER BY md5(id::text) LIMIT 200`)
	f.updSlugs = collect(`SELECT c.slug FROM app.campaigns c WHERE c.status IN ('ACTIVE','PAUSED','COMPLETED')
		AND EXISTS (SELECT 1 FROM app.campaign_updates u WHERE u.campaign_id = c.id AND u.status = 'PUBLISHED')
		ORDER BY md5(c.slug) LIMIT 300`)
	f.catSlugs = collect(`SELECT slug FROM app.campaign_categories WHERE active ORDER BY display_order`)
	if err := p.QueryRow(ctx, `SELECT c.slug FROM app.campaigns c JOIN app.campaign_updates u ON u.campaign_id = c.id
		WHERE u.status = 'PUBLISHED' GROUP BY c.slug ORDER BY count(*) DESC LIMIT 1`).Scan(&f.heavySlug); err != nil {
		t.Fatal(err)
	}
	var ts time.Time
	var id string
	if err := p.QueryRow(ctx, `SELECT published_at, id::text FROM app.campaigns WHERE status IN ('ACTIVE','PAUSED','COMPLETED')
		AND visibility = 'PUBLIC' ORDER BY published_at DESC, id DESC OFFSET 20000 LIMIT 1`).Scan(&ts, &id); err != nil {
		t.Fatal(err)
	}
	f.deepCursor = ts.UTC().Format(time.RFC3339Nano) + "|" + id
	_ = p.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('ACTIVE','PAUSED','COMPLETED') AND visibility = 'PUBLIC'), count(*)
		FROM app.campaigns`).Scan(&f.liveCount, &f.totalCount)
	_ = p.QueryRow(ctx, `SELECT count(*) FROM app.campaign_updates`).Scan(&f.updateCount)
	return f
}

type scenario struct {
	name string
	path func(i int) string
}

func scenarios(f fixtures) []scenario {
	fixed := func(p string) func(int) string { return func(int) string { return p } }
	q := url.QueryEscape
	return []scenario{
		{"search: default (no q)", fixed("/api/v1/public/campaigns")},
		{"search: sort=created", fixed("/api/v1/public/campaigns?sort=created")},
		{"search: q=harare (common)", fixed("/api/v1/public/campaigns?q=harare")},
		{"search: q=kidney dialysis", fixed("/api/v1/public/campaigns?q=" + q("kidney dialysis"))},
		{"search: q=rubatsiro (Shona)", fixed("/api/v1/public/campaigns?q=rubatsiro")},
		{"search: q=pangolins (rare)", fixed("/api/v1/public/campaigns?q=pangolins")},
		{"search: q=for (very common)", fixed("/api/v1/public/campaigns?q=for")},
		{"search: category=medical", fixed("/api/v1/public/campaigns?category=medical")},
		{"search: category=animal-welfare", fixed("/api/v1/public/campaigns?category=animal-welfare")},
		{"search: category=education&q=fees", fixed("/api/v1/public/campaigns?category=education&q=fees")},
		{"search: deep cursor (~20k)", fixed("/api/v1/public/campaigns?cursor=" + q(f.deepCursor))},
		{"search: rotating q (varied)", func(i int) string {
			terms := []string{"harare", "surgery", "chikoro", "borehole", "funeral", "mutare", "school fees", "flood relief", "tsime", "church roof",
				"bulawayo", "dialysis", "tuckshop", "goats", "mariro", "masvingo", "kariba", "uniforms", "solar", "cancer"}
			return "/api/v1/public/campaigns?q=" + q(terms[i%len(terms)])
		}},
		{"featured", fixed("/api/v1/public/campaigns/featured")},
		{"categories (live counts)", fixed("/api/v1/public/categories")},
		{"category by slug", func(i int) string { return "/api/v1/public/categories/" + f.catSlugs[i%len(f.catSlugs)] }},
		{"detail by slug", func(i int) string { return "/api/v1/public/campaigns/" + f.slugs[i%len(f.slugs)] }},
		{"detail non-canonical slug", func(i int) string { return "/api/v1/public/campaigns/" + f.nonCanon[i%len(f.nonCanon)] }},
		{"updates (1-5 per campaign)", func(i int) string { return "/api/v1/public/campaigns/" + f.updSlugs[i%len(f.updSlugs)] + "/updates" }},
		{"updates (300, first page)", fixed("/api/v1/public/campaigns/" + f.heavySlug + "/updates")},
		{"sitemap", fixed("/api/v1/public/sitemap")},
	}
}

type result struct {
	n                       int
	errors                  int
	p50, p95, p99, max, avg time.Duration
	rps                     float64
	bytes                   int
}

func pct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	i := int(float64(len(d))*p+0.999999) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(d) {
		i = len(d) - 1
	}
	return d[i]
}

func get(c *http.Client, base, path string) (int, int, error) {
	resp, err := c.Get(base + path)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	n, err := io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, int(n), err
}

// run issues n requests with the given concurrency and returns latency percentiles (client-observed,
// loopback HTTP, includes JSON encoding and ETag hashing).
func run(c *http.Client, base string, sc scenario, n, conc int) result {
	lat := make([]time.Duration, n)
	var next, errsN, bytes atomic.Int64
	var wg sync.WaitGroup
	t0 := time.Now()
	for w := 0; w < conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				s := time.Now()
				code, b, err := get(c, base, sc.path(i))
				lat[i] = time.Since(s)
				bytes.Store(int64(b))
				if err != nil || code != http.StatusOK {
					errsN.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	wall := time.Since(t0)
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	var sum time.Duration
	for _, d := range lat {
		sum += d
	}
	return result{n: n, errors: int(errsN.Load()), p50: pct(lat, .50), p95: pct(lat, .95), p99: pct(lat, .99), max: lat[n-1],
		avg: sum / time.Duration(n), rps: float64(n) / wall.Seconds(), bytes: int(bytes.Load())}
}

func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64)
}

func evictSharedBuffers(t *testing.T) bool {
	su := os.Getenv("FUNDZIM_PERF_SUPERUSER_URL")
	if su == "" {
		return false
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, su)
	if err != nil {
		t.Fatalf("superuser connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_buffercache`); err != nil {
		t.Fatalf("pg_buffercache: %v", err)
	}
	var n int
	if err := conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE x) FROM (SELECT pg_buffercache_evict(bufferid) AS x FROM pg_buffercache
		WHERE reldatabase = (SELECT oid FROM pg_database WHERE datname = 'fundzim_perf')) e`).Scan(&n); err != nil {
		t.Fatalf("evict: %v", err)
	}
	t.Logf("evicted %d fundzim_perf buffers from shared_buffers", n)
	return true
}

func TestPublicAPILatency(t *testing.T) {
	need(t)
	n := 300
	if v, err := strconv.Atoi(os.Getenv("FUNDZIM_PERF_N")); err == nil && v > 0 {
		n = v
	}
	s := start(t)
	f := load(t, s.d.DB)
	scs := scenarios(f)
	s.close()

	var out strings.Builder
	fmt.Fprintf(&out, "dataset: %d campaigns (%d live PUBLIC), %d updates; GOMAXPROCS=%d; DATABASE_MAX_CONNS=%s; n=%d per warm cell\n\n",
		f.totalCount, f.liveCount, f.updateCount, runtime.GOMAXPROCS(0), os.Getenv("DATABASE_MAX_CONNS"), n)

	// Cold: per scenario, a fresh API process state (new pools = new Postgres backends with empty catalog and plan
	// caches) and, when a superuser URL is given, fundzim_perf's pages evicted from shared_buffers. The OS page
	// cache stays warm (Postgres is shared with other work and is not restarted).
	fmt.Fprintf(&out, "### Cold first request\n\n| endpoint | first request ms | second request ms |\n|---|---:|---:|\n")
	for _, sc := range scs {
		evictSharedBuffers(t)
		cs := start(t)
		c := &http.Client{Timeout: 60 * time.Second}
		t0 := time.Now()
		code1, _, err := get(c, cs.srv.URL, sc.path(0))
		first := time.Since(t0)
		t0 = time.Now()
		code2, _, _ := get(c, cs.srv.URL, sc.path(1))
		second := time.Since(t0)
		cs.close()
		if err != nil || code1 != 200 || code2 != 200 {
			t.Errorf("%s: cold status %d/%d err %v", sc.name, code1, code2, err)
		}
		fmt.Fprintf(&out, "| %s | %s | %s |\n", sc.name, ms(first), ms(second))
	}

	// Warm: one long-lived server, warm-up then n requests per concurrency level.
	ws := start(t)
	defer ws.close()
	tr := &http.Transport{MaxIdleConns: 256, MaxIdleConnsPerHost: 256}
	c := &http.Client{Timeout: 60 * time.Second, Transport: tr}
	for _, conc := range []int{1, 8, 32} {
		fmt.Fprintf(&out, "\n### Warm, concurrency %d\n\n| endpoint | n | p50 ms | p95 ms | p99 ms | max ms | req/s | errors | body bytes |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n", conc)
		for _, sc := range scs {
			nn := n
			if sc.name == "sitemap" {
				nn = n / 5
			}
			run(c, ws.srv.URL, sc, max(conc*2, 20), conc) // warm-up
			r := run(c, ws.srv.URL, sc, nn, conc)
			if r.errors > 0 {
				t.Errorf("%s (c=%d): %d non-200 responses", sc.name, conc, r.errors)
			}
			fmt.Fprintf(&out, "| %s | %d | %s | %s | %s | %s | %.0f | %d | %d |\n", sc.name, r.n, ms(r.p50), ms(r.p95), ms(r.p99), ms(r.max), r.rps, r.errors, r.bytes)
		}
	}
	fmt.Println(out.String())
	if p := os.Getenv("FUNDZIM_PERF_OUT"); p != "" {
		if err := os.WriteFile(p, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSearchStatementTimeout checks that a search exceeding the 2 s statement timeout becomes 503
// SEARCH_TIMEOUT, and reports how long plausible "pathological" queries take.
func TestSearchStatementTimeout(t *testing.T) {
	need(t)
	s := start(t)
	defer s.close()
	c := &http.Client{Timeout: 60 * time.Second}
	type env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	call := func(path string) (int, string, time.Duration) {
		t0 := time.Now()
		resp, err := c.Get(s.srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var e env
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return resp.StatusCode, e.Error.Code, time.Since(t0)
	}

	// 1. Pathological but valid inputs (q is capped at 100 characters).
	or := strings.TrimSuffix(strings.Repeat("a or ", 20), " or ")
	for _, q := range []string{"-zzzz", "-a -b -c -d -e -f -g -h -i -j -k", or, `"the family" or "school fees" or -harare`,
		strings.Repeat("x", 100), "for", "a"} {
		code, ec, d := call("/api/v1/public/campaigns?q=" + url.QueryEscape(q))
		t.Logf("q=%q -> %d %s in %s ms", q, code, ec, ms(d))
	}

	// 2. Deterministic timeout: hold an ACCESS EXCLUSIVE lock on app.campaign_versions in fundzim_perf so the search
	// statement waits; statement_timeout (2 s) fires before the role's lock_timeout (3 s).
	mig := os.Getenv("DATABASE_MIGRATION_URL")
	if mig == "" {
		t.Skip("DATABASE_MIGRATION_URL not set")
	}
	ctx := context.Background()
	lock, err := pgx.Connect(ctx, mig)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close(ctx)
	tx, err := lock.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE app.campaign_versions IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	code, ec, d := call("/api/v1/public/campaigns?q=harare")
	_ = tx.Rollback(ctx)
	t.Logf("search under a held lock -> %d %s in %s ms", code, ec, ms(d))
	if code != http.StatusServiceUnavailable || ec != "SEARCH_TIMEOUT" {
		t.Fatalf("expected 503 SEARCH_TIMEOUT, got %d %s", code, ec)
	}
	if d < 1900*time.Millisecond || d > 3*time.Second {
		t.Errorf("timeout fired after %s, expected about 2 s", d)
	}
}
