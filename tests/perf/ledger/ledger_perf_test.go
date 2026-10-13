//go:build perf

// Stage 10 ledger performance harness (docs/stage-10/performance.md). It runs against a SEPARATE scratch database
// (never the development ledger: ledger tables are append-only, so a million perf journals could never be removed):
//
//	createdb fundzim_perf (owner fundzim_migrator), run the migrations against it, then
//	FUNDZIM_PERF_LEDGER_URL=postgres://fundzim_ledger:…@127.0.0.1:5432/fundzim_perf FUNDZIM_PERF_OUT=/tmp/ledgerperf \
//	  go test -tags perf -count=1 -timeout 120m -run TestLedgerPerf -v ./tests/perf/ledger/
//
// Journals are bulk-loaded through the real triggers (the deferred balance check runs for every journal) as
// TEST_ONLY synthetic DONATION_CAPTURED journals spread over 1000 campaign accounts; then each scenario is measured
// with the production code paths (ledger.Service). Every number in the report is measured; nothing is extrapolated.
package ledgerperf

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

type res struct {
	name               string
	n, c               int
	p50, p95, p99, max time.Duration
	rps                float64
	errors             int
}

func pct(d []time.Duration, p float64) time.Duration {
	i := int(float64(len(d))*p+0.999999) - 1
	if i < 0 {
		i = 0
	}
	return d[i]
}

func measure(name string, n, c int, f func(i int) error) res {
	lat := make([]time.Duration, n)
	var next atomic.Int64
	var errsN atomic.Int32
	var wg sync.WaitGroup
	t0 := time.Now()
	for w := 0; w < c; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= n {
					return
				}
				s := time.Now()
				if err := f(i); err != nil {
					errsN.Add(1)
				}
				lat[i] = time.Since(s)
			}
		}()
	}
	wg.Wait()
	wall := time.Since(t0)
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	return res{name: name, n: n, c: c, p50: pct(lat, .5), p95: pct(lat, .95), p99: pct(lat, .99), max: lat[n-1], rps: float64(n) / wall.Seconds(), errors: int(errsN.Load())}
}

func ms(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 1, 64)
}

func (r res) row() string {
	return fmt.Sprintf("| %s | %d | %d | %s | %s | %s | %s | %.0f | %d |\n", r.name, r.c, r.n, ms(r.p50), ms(r.p95), ms(r.p99), ms(r.max), r.rps, r.errors)
}

const header = "| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |\n|---|---:|---:|---:|---:|---:|---:|---:|---:|\n"

func TestLedgerPerf(t *testing.T) {
	url := os.Getenv("FUNDZIM_PERF_LEDGER_URL")
	if url == "" || !strings.Contains(url, "fundzim_perf") {
		t.Skip("set FUNDZIM_PERF_LEDGER_URL to the fundzim_ledger role on the fundzim_perf scratch database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url+"&pool_max_conns=16")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := ledger.New(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.EnsureCurrentPeriods(ctx); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	var pgVersion string
	_ = pool.QueryRow(ctx, `SHOW server_version`).Scan(&pgVersion)
	f("## Ledger performance\n\nstarted %s UTC; GOMAXPROCS=%d; NumCPU=%d; PostgreSQL %s (compose fundzim-postgres, default settings, 1 GiB memory limit); pool 16; scratch database fundzim_perf\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), runtime.NumCPU(), pgVersion)

	// 1000 campaign accounts + the provider clearing account, created through the posting path
	prov := ledger.SyntheticProviderID
	camps := make([]string, 1000)
	for i := range camps {
		camps[i] = ids.New()
	}
	capture := func(camp, key string, amount int64) ledger.Posting {
		src := ids.New()
		return ledger.Posting{Rule: "DONATION_CAPTURED", IdempotencyKey: key, Currency: "USD", OccurredAt: time.Now().UTC(), Description: "TEST_ONLY perf",
			SourceSystem: ledger.SourceSynthetic, SourceType: "payment", SourceID: src, SourceEventID: key, EvidenceClass: ledger.EvidenceSimulated,
			Actor: ledger.Actor{Type: "SYSTEM", ID: "perf"}, Metadata: map[string]string{"fixture": "TEST_ONLY", "perf": "seed"},
			Lines: []ledger.Line{ledger.Dr(ledger.AccountRef{Class: ledger.ClassPSPClearing, OwnerID: prov}, amount),
				ledger.Cr(ledger.AccountRef{Class: ledger.ClassCampaignUnsettled, OwnerID: camp}, amount)}}
	}
	seed := measure("seed: PostOnce, 1000 new accounts", 1000, 8, func(i int) error {
		_, err := svc.PostOnce(ctx, capture(camps[i], "fixture:"+camps[i]+":seed", 1000))
		return err
	})
	var accountIDs []string
	rows, err := pool.Query(ctx, `SELECT id::text FROM ledger.ledger_accounts WHERE account_class = 'liability:campaign_unsettled' ORDER BY id LIMIT 1000`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		accountIDs = append(accountIDs, id)
	}
	rows.Close()
	var clearingID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM ledger.ledger_accounts WHERE code = $1`, "asset:psp_clearing:"+prov).Scan(&clearingID); err != nil {
		t.Fatal(err)
	}
	f("### Setup\n\n%s%s\n", header, seed.row())

	scales := []int{10_000, 100_000, 1_000_000}
	if s := os.Getenv("FUNDZIM_PERF_SCALES"); s != "" {
		scales = nil
		for _, x := range strings.Split(s, ",") {
			n, _ := strconv.Atoi(x)
			scales = append(scales, n)
		}
	}
	loaded := 0
	acctArr := "{" + strings.Join(accountIDs, ",") + "}"
	for _, scale := range scales {
		// bulk load to `scale` journals (batches of 20 000 in one transaction each; triggers and deferred checks run)
		t0 := time.Now()
		for loaded < scale {
			n := 20_000
			if scale-loaded < n {
				n = scale - loaded
			}
			from, to := loaded+1, loaded+n
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = 0`); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_transactions (id, posting_rule, posting_rule_version, environment, source_system,
				evidence_class, metadata, idempotency_key, currency, description, source_type, source_id, occurred_at, accounting_period_id,
				created_by_type, created_by_id)
				SELECT md5('perf:' || i)::uuid, 'DONATION_CAPTURED', 1, 'TEST', 'synthetic_fixture', 'SIMULATED',
				  '{"fixture":"TEST_ONLY","perf":"bulk"}', 'perf:bulk:' || i, 'USD', 'TEST_ONLY perf bulk', 'payment', md5('perf:src:' || i)::uuid,
				  now(), '00000000-0000-0000-0000-000000000000', 'SYSTEM', 'perf'
				FROM generate_series($1::int, $2::int) i`, from, to); err != nil {
				t.Fatalf("headers: %v", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_entries (id, transaction_id, line_no, account_id, direction, amount_minor, currency)
				SELECT gen_random_uuid(), md5('perf:' || i)::uuid, l, CASE l WHEN 1 THEN $3::uuid ELSE ($4::uuid[])[1 + (i % 1000)] END,
				  CASE l WHEN 1 THEN 'DEBIT' ELSE 'CREDIT' END, 100 + (i % 9900), 'USD'
				FROM generate_series($1::int, $2::int) i CROSS JOIN (VALUES (1), (2)) v(l)
				ORDER BY i, CASE l WHEN 1 THEN $3::uuid ELSE ($4::uuid[])[1 + (i % 1000)] END`, from, to, clearingID, acctArr); err != nil {
				t.Fatalf("entries: %v", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit (deferred checks): %v", err)
			}
			loaded += n
		}
		load := time.Since(t0)
		t1 := time.Now()
		folded, err := svc.Fold(ctx)
		if err != nil {
			t.Fatal(err)
		}
		fold := time.Since(t1)
		var nTx, nEntries int64
		_ = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ledger.ledger_transactions), (SELECT count(*) FROM ledger.ledger_entries)`).Scan(&nTx, &nEntries)
		var dbSize string
		_ = pool.QueryRow(ctx, `SELECT pg_size_pretty(pg_database_size(current_database()))`).Scan(&dbSize)
		f("### %d journals\n\ndataset: %d journals, %d entries, 1000 campaign accounts + 1 clearing account; database size %s. Bulk load of this step: %s (%.0f journals/s incl. triggers and deferred checks); fold of DEFERRED balances: %s (%d rows).\n\n",
			scale, nTx, nEntries, dbSize, load.Round(time.Millisecond), float64(scale-(loaded-scale))/load.Seconds(), fold.Round(time.Millisecond), folded)
		f(header)
		var seq atomic.Int64
		post := func(i int) error {
			k := seq.Add(1)
			_, err := svc.PostOnce(ctx, capture(camps[int(k)%1000], fmt.Sprintf("fixture:perf-%d-%d:post", scale, k), 1234))
			return err
		}
		f("%s", measure("PostOnce (2-line capture, new journal)", 300, 1, post).row())
		f("%s", measure("PostOnce (2-line capture, new journal)", 1600, 8, post).row())
		f("%s", measure("PostOnce (2-line capture, new journal)", 3200, 16, post).row())
		f("%s", measure("Account balance (Account: exact current balance + totals)", 500, 8, func(i int) error {
			_, err := svc.Account(ctx, accountIDs[i%len(accountIDs)])
			return err
		}).row())
		f("%s", measure("Account activity (first page of journals touching an account)", 300, 8, func(i int) error {
			_, _, err := svc.Journals(ctx, ledger.JournalFilter{AccountID: accountIDs[i%len(accountIDs)]}, "", 50)
			return err
		}).row())
		f("%s", measure("Journal register, first page", 300, 8, func(int) error {
			_, _, err := svc.Journals(ctx, ledger.JournalFilter{}, "", 50)
			return err
		}).row())
		// a deep page: cursor of the 10 000th newest journal
		var at time.Time
		var cid string
		_ = pool.QueryRow(ctx, `SELECT posted_at, id::text FROM ledger.ledger_transactions ORDER BY posted_at DESC, id DESC OFFSET 9999 LIMIT 1`).Scan(&at, &cid)
		cursor := at.UTC().Format(time.RFC3339Nano) + "|" + cid
		f("%s", measure("Journal register, page after the 10 000th journal (keyset)", 300, 8, func(int) error {
			_, _, err := svc.Journals(ctx, ledger.JournalFilter{}, cursor, 50)
			return err
		}).row())
		f("%s", measure("Trial balance (all USD accounts, REPEATABLE READ)", 5, 1, func(int) error {
			tb, err := svc.TrialBalance(ctx, "USD", "")
			if err == nil && (len(tb.Currencies) != 1 || !tb.Currencies[0].Balanced) {
				return fmt.Errorf("unbalanced")
			}
			return err
		}).row())
		f("%s", measure("Integrity checker (L1–L15, one snapshot, recorded)", 1, 1, func(int) error {
			rep, err := svc.RunIntegrity(ctx, "SYSTEM", "perf")
			if err == nil && !rep.Passed {
				return fmt.Errorf("integrity failed")
			}
			return err
		}).row())
		f("%s", measure("Reconciliation run (no payment facts; suspense scan)", 1, 1, func(int) error {
			_, err := svc.Reconcile(ctx, "perf")
			return err
		}).row())
		f("\n")
	}
	fmt.Println(b.String())
	if dir := os.Getenv("FUNDZIM_PERF_OUT"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "ledger-perf.md"), []byte(b.String()), 0o644)
	}
}

// TestLedgerPerfIntegrityOnly re-measures the integrity checker on the dataset already in the scratch database
// (no loading): used after a change to the checker.
func TestLedgerPerfIntegrityOnly(t *testing.T) {
	url := os.Getenv("FUNDZIM_PERF_LEDGER_URL")
	if url == "" || !strings.Contains(url, "fundzim_perf") || os.Getenv("FUNDZIM_PERF_INTEGRITY_ONLY") == "" {
		t.Skip("set FUNDZIM_PERF_LEDGER_URL (fundzim_perf) and FUNDZIM_PERF_INTEGRITY_ONLY=1")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	svc := ledger.New(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	var nTx int64
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM ledger.ledger_transactions`).Scan(&nTx)
	r := measure("Integrity checker (L1–L15, one snapshot, recorded)", 3, 1, func(int) error {
		rep, err := svc.RunIntegrity(ctx, "SYSTEM", "perf")
		if err == nil && !rep.Passed {
			return fmt.Errorf("integrity failed")
		}
		return err
	})
	out := fmt.Sprintf("%d journals\n\n%s%s", nTx, header, r.row())
	fmt.Println(out)
	if r.errors > 0 {
		t.Fatalf("integrity errors: %d", r.errors)
	}
}
