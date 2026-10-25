//go:build perf

package ledgerperf

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
)

// Remediation of KI-S19-09 (ADR-052): historical coverage and volume of the ledger's PAYMENT_JOURNALS and PAYMENT_FACTS
// sweeps on a SCRATCH database (journals are append-only; the planted duplicate capture journal makes invariant L6 fail
// for ever on whatever database it lands in — never the development ledger):
//
//	FUNDZIM_PERF_LEDGER_URL=postgres://fundzim_ledger:…@127.0.0.1:5432/fundzim_perf_r1?sslmode=disable \
//	FUNDZIM_PERF_SWEEP_SCALES=20000,100000 FUNDZIM_PERF_OUT=… \
//	  go test -tags perf -count=1 -timeout 120m -run TestLedgerReconciliationSweepVolume -v ./tests/perf/ledger/
//
// Payment facts are injected in memory (the ledger reads payments only through the PaymentFacts interface), so the
// figures cover the ledger side of the walk and exclude the per-journal payments lookup of a real deployment.
func TestLedgerReconciliationSweepVolume(t *testing.T) {
	url := os.Getenv("FUNDZIM_PERF_LEDGER_URL")
	if url == "" || !strings.Contains(url, "fundzim_perf") {
		t.Skip("set FUNDZIM_PERF_LEDGER_URL to the fundzim_ledger role on a fundzim_perf* scratch database")
	}
	scales := []int{20000}
	if v := os.Getenv("FUNDZIM_PERF_SWEEP_SCALES"); v != "" {
		scales = nil
		for _, p := range strings.Split(v, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 100 {
				t.Fatalf("FUNDZIM_PERF_SWEEP_SCALES: %q", v)
			}
			scales = append(scales, n)
		}
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url+"&pool_max_conns=8")
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
	f("## Ledger reconciliation sweep volume (ADR-052)\n\nstarted %s UTC; GOMAXPROCS=%d; NumCPU=%d; PostgreSQL %s; pool 8; scratch database; facts injected in memory\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), runtime.NumCPU(), pgVersion)

	camp := ids.New()
	capture := func(src string, amount int64, key string) ledger.Posting {
		return ledger.Posting{Rule: "DONATION_CAPTURED", IdempotencyKey: "fixture:" + src + ":" + key, Currency: "USD", OccurredAt: time.Now().UTC(),
			Description: "TEST_ONLY sweep volume", SourceSystem: ledger.SourceSynthetic, SourceType: "payment", SourceID: src,
			SourceEventID: "fixture:" + src + ":" + key, EvidenceClass: ledger.EvidenceSimulated, Actor: ledger.Actor{Type: "SYSTEM", ID: "perf"},
			Metadata: map[string]string{"fixture": "TEST_ONLY", "perf": "sweep"},
			Lines: []ledger.Line{ledger.Dr(ledger.AccountRef{Class: ledger.ClassPSPClearing, OwnerID: ledger.SyntheticProviderID}, amount),
				ledger.Cr(ledger.AccountRef{Class: ledger.ClassCampaignUnsettled, OwnerID: camp}, amount)}}
	}
	facts := &memFacts{m: map[string]ledger.PaymentFact{}}
	old := time.Now().Add(-72 * time.Hour)
	fact := func(id string, amount int64, st string, at time.Time) {
		facts.put(ledger.PaymentFact{ID: id, Status: st, AmountMinor: amount, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR", UpdatedAt: at})
	}
	// planted, all at the very start (the oldest records: outside every newest-first window)
	oldMismatch, dup, unevaluated := ids.New(), ids.New(), ids.New()
	for _, p := range []ledger.Posting{capture(oldMismatch, 1000, "capture"), capture(dup, 700, "capture")} {
		if _, err := svc.PostOnce(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	fact(oldMismatch, 1500, "SUCCEEDED", old)
	fact(dup, 700, "SUCCEEDED", old)
	fact(unevaluated, 900, "SUCCEEDED", old.Add(-time.Hour))

	loaded := 0
	dupPlanted := false
	for _, scale := range scales {
		t0 := time.Now()
		var mu sync.Mutex
		var firstErr error
		var wg sync.WaitGroup
		work := make(chan int)
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range work {
					src := ids.New()
					amt := int64(100 + i%9000)
					if _, err := svc.PostOnce(ctx, capture(src, amt, "capture")); err != nil {
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
						continue
					}
					fact(src, amt, "SUCCEEDED", old)
				}
			}()
		}
		for i := loaded; i < scale; i++ {
			work <- i
		}
		close(work)
		wg.Wait()
		if firstErr != nil {
			t.Fatal(firstErr)
		}
		loaded = scale
		if !dupPlanted { // the duplicate of the first journal lands thousands of batches after it
			if _, err := svc.PostOnce(ctx, capture(dup, 700, "capture-again")); err != nil {
				t.Fatal(err)
			}
			dupPlanted = true
		}
		load := time.Since(t0)

		run := ledger.New(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		run.ReconcileJournalsFrom = ledger.SourceSynthetic
		run.Facts = facts
		run.ReconWindow = 100
		cfg := reconsweep.Config{Scope: "perf." + ids.New()[24:], BatchSize: 1000, MaxRecords: 10000, MaxDuration: 3 * time.Minute}
		run.ReconSweep = cfg
		var journals int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM ledger.ledger_transactions WHERE source_system = 'synthetic_fixture' AND source_type = 'payment'
			AND posting_rule = 'DONATION_CAPTURED'`).Scan(&journals)

		// full cycles, run by run (production budgets: 10 000 records per run per detector)
		cycle := func() (runs int, wall time.Duration, maxRun time.Duration, examined int) {
			s := time.Now()
			jDone, fDone := false, false
			for !(jDone && fDone) {
				r0 := time.Now()
				rep, err := run.Reconcile(ctx, "perf")
				if err != nil {
					t.Fatalf("reconcile: %v", err)
				}
				maxRun = max(maxRun, time.Since(r0))
				runs++
				examined += rep.Sweeps[ledger.SweepPaymentJournals].Examined + rep.Sweeps[ledger.SweepPaymentFacts].Examined
				jDone = jDone || rep.Sweeps[ledger.SweepPaymentJournals].CycleCompleted
				fDone = fDone || rep.Sweeps[ledger.SweepPaymentFacts].CycleCompleted
				if runs > 10000 {
					t.Fatal("cycle never completed")
				}
			}
			return runs, time.Since(s), maxRun, examined
		}
		runs, wall, maxRun, examined := cycle()
		openRef := func(src, typ string) int {
			var n int
			_ = pool.QueryRow(ctx, `SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE source_id = $1 AND exception_type = $2 AND status = 'OPEN'`,
				src, typ).Scan(&n)
			return n
		}
		for src, typ := range map[string]string{oldMismatch: ledger.ExAmountMismatch, dup: ledger.ExDuplicatePosting, unevaluated: ledger.ExMissingPosting} {
			if openRef(src, typ) != 1 {
				t.Fatalf("scale %d: %s for %s not found", scale, typ, src)
			}
		}
		var completedExamined int64
		_ = pool.QueryRow(ctx, `SELECT examined FROM ledger.ledger_reconciliation_sweeps WHERE scope = $1 AND detector = 'PAYMENT_JOURNALS'
			AND status = 'COMPLETED' ORDER BY cycle DESC LIMIT 1`, cfg.Scope+"."+ledger.SourceSynthetic).Scan(&completedExamined)
		if completedExamined != int64(journals) {
			t.Fatalf("scale %d: the journal cycle examined %d of %d journals", scale, completedExamined, journals)
		}
		// interruption and recovery: deadlines inside batches until the next cycle completes
		interrupted, rs := 0, time.Now()
		jDone := false
		for i := 0; !jDone && i < 100000; i++ {
			c, cancel := context.WithTimeout(ctx, time.Duration(50+i%400)*time.Millisecond)
			rep, err := run.Reconcile(c, "perf-interrupted")
			cancel()
			if err != nil {
				interrupted++
				continue
			}
			jDone = rep.Sweeps[ledger.SweepPaymentJournals].CycleCompleted
		}
		recovery := time.Since(rs)
		_ = pool.QueryRow(ctx, `SELECT examined FROM ledger.ledger_reconciliation_sweeps WHERE scope = $1 AND detector = 'PAYMENT_JOURNALS'
			AND status = 'COMPLETED' ORDER BY cycle DESC LIMIT 1`, cfg.Scope+"."+ledger.SourceSynthetic).Scan(&completedExamined)
		if completedExamined != int64(journals) || interrupted == 0 {
			t.Fatalf("scale %d: interrupted cycle examined %d of %d (interrupted runs %d)", scale, completedExamined, journals, interrupted)
		}
		var refs int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE detected_by_run LIKE 'recon:%' AND source_id = ANY($1::uuid[])
			AND status = 'OPEN'`, []string{oldMismatch, dup, unevaluated}).Scan(&refs)
		if refs != 3 {
			t.Fatalf("scale %d: planted findings duplicated or lost: %d", scale, refs)
		}
		f("### %d capture journals (%d in the ledger's synthetic source, %d injected payment facts)\n\n", scale, journals, facts.len())
		f("| measure | value |\n|---|---|\n")
		f("| load (PostOnce, 8 workers) | %s |\n", load.Round(time.Millisecond))
		f("| one full cycle of both sweeps | %d runs, %s wall, slowest run %s |\n", runs, wall.Round(time.Millisecond), maxRun.Round(time.Millisecond))
		f("| records examined in that cycle (journals + facts) | %d (%.0f records/s) |\n", examined, float64(examined)/wall.Seconds())
		f("| planted old AMOUNT_MISMATCH, cross-batch DUPLICATE_POSTING, oldest NOT_EVALUATED | found, once each |\n")
		f("| next cycle under random deadlines (50–449 ms) | %d interrupted runs, completed in %s, examined %d = %d journals |\n\n",
			interrupted, recovery.Round(time.Millisecond), completedExamined, journals)
	}
	out := os.Getenv("FUNDZIM_PERF_OUT")
	if out == "" {
		out = t.TempDir()
	}
	_ = os.MkdirAll(out, 0o755)
	if err := os.WriteFile(filepath.Join(out, "ledger-recon-sweep.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + b.String())
}

type memFacts struct {
	mu sync.RWMutex
	m  map[string]ledger.PaymentFact
}

func (f *memFacts) put(p ledger.PaymentFact) { f.mu.Lock(); f.m[p.ID] = p; f.mu.Unlock() }
func (f *memFacts) len() int                 { f.mu.RLock(); defer f.mu.RUnlock(); return len(f.m) }

func (f *memFacts) Payment(_ context.Context, id string) (ledger.PaymentFact, bool, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	p, ok := f.m[id]
	return p, ok, nil
}

func (f *memFacts) PaymentsInStatus(_ context.Context, st string, before time.Time, limit int) ([]ledger.PaymentFact, error) {
	f.mu.RLock()
	var out []ledger.PaymentFact
	for _, p := range f.m {
		if p.Status == st && p.UpdatedAt.Before(before) {
			out = append(out, p)
		}
	}
	f.mu.RUnlock()
	slices.SortFunc(out, func(a, b ledger.PaymentFact) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *memFacts) PaymentIDsAfter(_ context.Context, after string, sts []string, limit int) ([]string, error) {
	f.mu.RLock()
	var out []string
	for id, p := range f.m {
		if id > after && slices.Contains(sts, p.Status) {
			out = append(out, id)
		}
	}
	f.mu.RUnlock()
	slices.Sort(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
