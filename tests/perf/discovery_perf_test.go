//go:build perf

// Stage 16 discovery performance (docs/stage-16/mobile-performance.md §5, docs/stage-16/search.md §8) against the scratch
// database fundzim_perf (tests/perf/seed.sql; this stage's run used a 100 000-campaign variant). The index is built in
// steps by the real reconciliation pass (1 000, 10 000, then every published campaign checked) and each step is
// measured end to end through the HTTP API in-process: candidate query on the projection + serve-time eligibility and
// card rendering by campaigns. Never run against the development database: need() refuses.
//
//	set -a; . ./.env; . tests/perf/env.sh; set +a
//	go test -tags perf -count=1 -timeout 120m -run TestDiscoveryPerf -v ./tests/perf/
package perf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/discovery"
	"github.com/Fatifizo/fundzim/internal/platform/config"
)

func TestDiscoveryPerf(t *testing.T) {
	need(t)
	ctx := context.Background()
	mig, err := pgxpool.New(ctx, mustEnv(t, "DATABASE_MIGRATION_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer mig.Close()
	// scratch database only (need() checked): start every run from an empty projection so the 1k / 10k / all steps
	// measure real indexing; TRUNCATE by the table owner (the row-level no-delete trigger guards runtime roles)
	if _, err := mig.Exec(ctx, `TRUNCATE app.discovery_trending_scores, app.discovery_campaign_index, app.discovery_campaign_daily,
		app.discovery_engagement_keys, app.discovery_search_daily, app.discovery_abuse_daily`); err != nil {
		t.Fatal(err)
	}
	s := startWithoutDiscoveryLimits(t)
	defer s.close()
	d := s.d.Discovery
	if d == nil {
		t.Fatal("discovery not wired")
	}
	var published, live int
	_ = mig.QueryRow(ctx, `SELECT count(*) FILTER (WHERE published_at IS NOT NULL), count(*) FILTER (WHERE status IN ('ACTIVE','PAUSED','COMPLETED')
		AND visibility = 'PUBLIC') FROM app.campaigns`).Scan(&published, &live)
	t.Logf("dataset: %d campaigns published at some point, %d live PUBLIC", published, live)

	c := &http.Client{Timeout: 30 * time.Second}
	q := url.QueryEscape
	type step struct {
		label  string
		checks int // campaigns checked by the reconciliation pass (0 = all)
	}
	fmt.Printf("\n| index step | campaigns checked | discoverable rows | reindexed | build s | per campaign ms |\n|---|---:|---:|---:|---:|---:|\n")
	var report []string
	for _, st := range []step{{"1k", 1000}, {"10k", 10000}, {"all", 0}} {
		t0 := time.Now()
		res, err := d.Reconcile(ctx, st.checks)
		if err != nil {
			t.Fatal(err)
		}
		build := time.Since(t0)
		var disc int
		_ = mig.QueryRow(ctx, `SELECT count(*) FROM app.discovery_campaign_index WHERE discoverable`).Scan(&disc)
		per := 0.0
		if res.Reindexed > 0 {
			per = float64(build.Milliseconds()) / float64(res.Reindexed)
		}
		fmt.Printf("| %s | %d | %d | %d | %.1f | %.2f |\n", st.label, res.Checked, disc, res.Reindexed, build.Seconds(), per)
		// province values in the projection only (the seed has no campaign locations): makes the filter selective
		_, _ = mig.Exec(ctx, `UPDATE app.discovery_campaign_index SET province_code = (ARRAY['PROVINCE_HARARE','PROVINCE_BULAWAYO','PROVINCE_MANICALAND',
			'PROVINCE_MIDLANDS','PROVINCE_MASVINGO'])[1 + abs(hashtext(campaign_id::text)) % 5] WHERE discoverable AND province_code IS NULL`)
		_, _ = mig.Exec(ctx, `ANALYZE app.discovery_campaign_index`)

		var cursor string
		if p, err := d.Search(ctx, discovery.SearchInput{Sort: discovery.SortNewest, Limit: 48}); err == nil && p.NextCursor != nil {
			cursor = *p.NextCursor
		}
		fixed := func(p string) func(int) string { return func(int) string { return p } }
		scen := []scenario{
			{"directory (newest)", fixed("/api/v1/discovery/campaigns")},
			{"search q=harare (common)", fixed("/api/v1/discovery/search?q=harare")},
			{"search q=kidney dialysis", fixed("/api/v1/discovery/search?q=" + q("kidney dialysis"))},
			{"search q=rubatsiro (Shona)", fixed("/api/v1/discovery/search?q=rubatsiro")},
			{"search q=pangolins (rare)", fixed("/api/v1/discovery/search?q=pangolins")},
			{"search typo q=dialisys (fuzzy)", fixed("/api/v1/discovery/search?q=dialisys")},
			{"search q=school&category=education", fixed("/api/v1/discovery/search?q=school&category=education")},
			{"filter province=harare", fixed("/api/v1/discovery/campaigns?province=harare")},
			{"filter status=ended&published=90d", fixed("/api/v1/discovery/campaigns?status=ended&published=90d")},
			{"sort=trending", fixed("/api/v1/discovery/campaigns?sort=trending")},
			{"sort=oldest", fixed("/api/v1/discovery/campaigns?sort=oldest")},
			{"page 2 (keyset cursor)", fixed("/api/v1/discovery/campaigns?limit=48&cursor=" + q(cursor))},
			{"rotating q (varied)", func(i int) string {
				terms := []string{"harare", "surgery", "chikoro", "borehole", "funeral", "mutare", "school fees", "flood relief", "tsime", "church roof",
					"bulawayo", "dialysis", "tuckshop", "goats", "mariro", "masvingo", "kariba", "uniforms", "solar", "cancer"}
				return "/api/v1/discovery/search?q=" + q(terms[i%len(terms)])
			}},
			{"categories (counts)", fixed("/api/v1/discovery/categories")},
			{"locations (counts)", fixed("/api/v1/discovery/locations")},
			{"trending", fixed("/api/v1/discovery/trending")},
			{"featured", fixed("/api/v1/discovery/featured")},
			{"recommendations", fixed("/api/v1/discovery/recommendations")},
		}
		for _, sc := range scen {
			n := 200
			r := run(c, s.srv.URL, sc, n, 8)
			if r.errors > 0 {
				t.Errorf("%s / %s: %d of %d requests failed (results invalid)", st.label, sc.name, r.errors, n)
			}
			report = append(report, fmt.Sprintf("| %s | %s | 8 | %d | %s | %s | %s | %.0f | %d | %d |", st.label, sc.name, n, ms(r.p50), ms(r.p95), ms(r.max), r.rps, r.errors, r.bytes))
		}
		// trending computation over the whole projection
		t1 := time.Now()
		tr, err := d.ComputeTrending(ctx)
		if err != nil {
			t.Fatal(err)
		}
		report = append(report, fmt.Sprintf("| %s | trending computation (%d scored, %d ranked) | 1 | 1 | — | — | %s (total) | — | 0 | — |", st.label, tr.Scored, tr.Ranked, ms(time.Since(t1))))
	}
	fmt.Printf("\n| index | scenario | c | n | p50 ms | p95 ms | max ms | req/s | errors | bytes |\n|---|---|---:|---:|---:|---:|---:|---:|---:|---:|\n%s\n",
		strings.Join(report, "\n"))

	// event ingestion: handler latency (enqueue only) and the batched flush
	var slugs []string
	rows, _ := mig.Query(ctx, `SELECT slug FROM app.discovery_campaign_index WHERE discoverable ORDER BY md5(slug) LIMIT 500`)
	for rows.Next() {
		var sl string
		_ = rows.Scan(&sl)
		slugs = append(slugs, sl)
	}
	rows.Close()
	lat := make([]time.Duration, 4000)
	var next, errsN atomic.Int64
	var wg sync.WaitGroup
	t0 := time.Now()
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(lat) {
					return
				}
				body := fmt.Sprintf(`{"events":[{"type":"CAMPAIGN_VIEW","slug":%q,"source":"SEARCH"},{"type":"SHARE_CLICK","slug":%q,"channel":"WHATSAPP"}]}`,
					slugs[i%len(slugs)], slugs[i%len(slugs)])
				req, _ := http.NewRequest("POST", s.srv.URL+"/api/v1/discovery/events", bytes.NewBufferString(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13) Chrome/120 Mobile Safari/537.36")
				st := time.Now()
				resp, err := c.Do(req)
				lat[i] = time.Since(st)
				if err != nil || resp.StatusCode != http.StatusNoContent {
					if errsN.Add(1) == 1 && resp != nil {
						b, _ := io.ReadAll(resp.Body)
						t.Logf("first ingestion failure: %d %s", resp.StatusCode, b)
					}
				}
				if resp != nil {
					resp.Body.Close()
				}
			}
		}()
	}
	wg.Wait()
	wall := time.Since(t0)
	t2 := time.Now()
	d.Recorder.Flush(ctx)
	flush := time.Since(t2)
	if errsN.Load() > 0 {
		t.Errorf("event ingestion: %d of %d requests failed (results invalid)", errsN.Load(), len(lat))
	}
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	fmt.Printf("\n| event ingestion | c | requests (2 events each) | p50 ms | p95 ms | max ms | req/s | errors | final flush ms |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n"+
		"| POST /discovery/events | 16 | %d | %s | %s | %s | %.0f | %d | %s |\n", len(lat), ms(pct(lat, .5)), ms(pct(lat, .95)), ms(lat[len(lat)-1]),
		float64(len(lat))/wall.Seconds(), errsN.Load(), ms(flush))

	// database footprint of discovery
	var idx, gin, trgm, daily string
	_ = mig.QueryRow(ctx, `SELECT pg_size_pretty(pg_total_relation_size('app.discovery_campaign_index')),
		pg_size_pretty(pg_relation_size('app.ix_discovery_index_doc')), pg_size_pretty(pg_relation_size('app.ix_discovery_index_title_trgm')),
		pg_size_pretty(pg_total_relation_size('app.discovery_campaign_daily'))`).Scan(&idx, &gin, &trgm, &daily)
	fmt.Printf("\nfootprint: discovery_campaign_index total %s (GIN tsvector %s, GIN trigram %s); discovery_campaign_daily %s\n", idx, gin, trgm, daily)
}

func mustEnv(t *testing.T, k string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		t.Fatalf("%s is not set", k)
	}
	return v
}

// startWithoutDiscoveryLimits is start() with the discovery rate limits off as well (harness only: the global per-IP
// limiter is already disabled by start's config; the discovery search and event limiters use the shared limiter, which
// would turn most measured requests into 429s from a single client address).
func startWithoutDiscoveryLimits(t *testing.T) *server {
	t.Helper()
	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.RateLimit.Enabled = false
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d, err := app.NewDeps(context.Background(), cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	d.Limiter = nil
	if d.Discovery != nil {
		d.Discovery.Limiter = nil
	}
	return &server{srv: httptest.NewServer(app.PublicHandler(d, app.NewRouter(d, d.Checker()))), d: d}
}
