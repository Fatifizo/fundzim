//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
)

// Remediation phase 1 volume measurement (opt-in, FUNDZIM_IT_R1_VOLUME=1; local disposable stack only, after a
// TestFinancialLoad run has filled it): one complete cycle of the payout and fee coverage sweeps in a fresh scope with
// the production run budgets (payouts.DefaultReconSweep, fees.DefaultReconSweep), run back to back. Records what was
// actually measured on this host; nothing is extrapolated and nothing here is a capacity claim.
func TestReconciliationCoverageVolume(t *testing.T) {
	if os.Getenv("FUNDZIM_IT_R1_VOLUME") != "1" {
		t.Skip("opt-in: FUNDZIM_IT_R1_VOLUME=1 (local disposable stack only)")
	}
	p := newPayoutIT(t)
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	var pgVersion string
	_ = p.mig.QueryRow(ctx(t), `SHOW server_version`).Scan(&pgVersion)
	f("## Coverage sweep volume — payouts and fees (ADR-052)\n\nstarted %s UTC; GOMAXPROCS=%d; NumCPU=%d; PostgreSQL %s; disposable compose project\n\n",
		time.Now().UTC().Format(time.RFC3339), runtime.GOMAXPROCS(0), runtime.NumCPU(), pgVersion)

	lockWaits := func() int {
		var n int
		_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock'`).Scan(&n)
		return n
	}
	var inScope, submitted int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*), count(*) FILTER (WHERE submitted_at IS NOT NULL) FROM app.payout_requests
		WHERE status <> 'PAYOUT_REQUESTED'`).Scan(&inScope, &submitted)

	rc := *p.po
	rc.ReconSweep = reconsweep.Config{Scope: "it.volume." + time.Now().UTC().Format("150405")}
	var runs, examined, maxLock int
	var maxRun time.Duration
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)
	start := time.Now()
	for runs < 10000 {
		r0 := time.Now()
		rep, err := rc.Reconcile(ctx(t), "it-volume")
		if err != nil {
			t.Fatalf("payout reconcile: %v", err)
		}
		maxRun = max(maxRun, time.Since(r0))
		maxLock = max(maxLock, lockWaits())
		runs++
		examined += rep.Sweep.Examined
		if rep.Sweep.CycleCompleted {
			break
		}
	}
	wall := time.Since(start)
	runtime.ReadMemStats(&ms1)
	var cycExamined int64
	_ = p.mig.QueryRow(ctx(t), `SELECT examined FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'COMPLETED' AND cycle = 1`,
		rc.ReconSweep.Scope).Scan(&cycExamined)
	f("### Payouts (%d in scope, %d submitted → one provider status query each)\n\n| measure | value |\n|---|---|\n", inScope, submitted)
	f("| runs for one cycle (budget %d records/run, batch %d) | %d |\n", 2000, 200, runs)
	f("| examined (runs) / examined (completed checkpoint) | %d / %d |\n", examined, cycExamined)
	f("| cycle wall time (runs back to back, incl. recency pass of 5 000) | %s |\n", wall.Round(time.Millisecond))
	f("| slowest run | %s |\n", maxRun.Round(time.Millisecond))
	f("| sweep records/s (examined ÷ wall) | %.0f |\n", float64(examined)/wall.Seconds())
	f("| test-process heap allocated during the cycle | %.1f MiB total (%.1f MiB live after) |\n", float64(ms1.TotalAlloc-ms0.TotalAlloc)/(1<<20),
		float64(ms1.HeapAlloc)/(1<<20))
	f("| sessions waiting on a lock, max sampled after each run | %d |\n\n", maxLock)
	if cycExamined != int64(inScope) {
		t.Errorf("payout cycle examined %d of %d", cycExamined, inScope)
	}

	fs := p.s.d.Verification.Fees
	if fs == nil {
		t.Fatal("fees module not wired")
	}
	fc := *fs
	fc.ReconSweep = reconsweep.Config{Scope: "it.volume." + time.Now().UTC().Format("150405")}
	var assessments int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.fee_assessments`).Scan(&assessments)
	runs, examined, maxRun, maxLock = 0, 0, 0, 0
	start = time.Now()
	for runs < 10000 {
		r0 := time.Now()
		run, err := fc.Reconcile(ctx(t), "it-volume")
		if err != nil {
			t.Fatalf("fee reconcile: %v", err)
		}
		maxRun = max(maxRun, time.Since(r0))
		maxLock = max(maxLock, lockWaits())
		runs++
		examined += run.Sweep.Examined
		if run.Sweep != nil && run.Sweep.CycleCompleted {
			break
		}
	}
	wall = time.Since(start)
	f("### Fee assessments (%d)\n\n| measure | value |\n|---|---|\n", assessments)
	f("| runs for one cycle (budget %d records/run, batch %d) | %d |\n", 5000, 500, runs)
	f("| examined | %d |\n| cycle wall time (incl. recency pass of 5 000) | %s |\n| slowest run | %s |\n", examined, wall.Round(time.Millisecond),
		maxRun.Round(time.Millisecond))
	f("| sweep records/s | %.0f |\n| sessions waiting on a lock, max sampled | %d |\n\n", float64(examined)/wall.Seconds(), maxLock)
	if examined < assessments {
		t.Errorf("fee cycle examined %d of %d", examined, assessments)
	}

	// query plans of the keyset reads at this volume (the production statements, from the 101st key). F-R1-06: the first
	// version ordered by the text output column (`SELECT id::text … ORDER BY id`), a full sort per batch whose agreement with
	// the uuid WHERE depended on the collation — a sort on the text key must not come back.
	for _, q := range []string{
		`EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT p.id::text FROM app.payout_requests p WHERE p.id > (SELECT id FROM app.payout_requests ORDER BY id OFFSET 100 LIMIT 1)
		  AND p.status <> 'PAYOUT_REQUESTED' ORDER BY p.id LIMIT 200`,
		`EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT a.id::text FROM app.fee_assessments a WHERE a.id > (SELECT id FROM app.fee_assessments ORDER BY id OFFSET 100 LIMIT 1)
		  ORDER BY a.id LIMIT 500`,
		`EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) SELECT p.id::text FROM app.payment_intents p WHERE p.id > (SELECT id FROM app.payment_intents ORDER BY id OFFSET 10 LIMIT 1)
		  AND p.status = ANY('{SUCCEEDED,UNKNOWN}'::text[]) ORDER BY p.id LIMIT 1000`,
	} {
		rows, err := p.mig.Query(ctx(t), q)
		if err != nil {
			t.Fatal(err)
		}
		f("```\n")
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			if strings.Contains(line, "Sort Key:") && strings.Contains(line, "::text") {
				t.Errorf("keyset read sorts on the text key: %s", line)
			}
			f("%s\n", line)
		}
		rows.Close()
		f("```\n\n")
	}
	out := os.Getenv("FUNDZIM_PERF_OUT")
	if out == "" {
		out = t.TempDir()
	}
	_ = os.MkdirAll(out, 0o755)
	if err := os.WriteFile(filepath.Join(out, "coverage-sweep-volume.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + b.String())
}
