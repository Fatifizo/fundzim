//go:build integration

package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
)

// Remediation of KI-S19-09 (ADR-052): complete, resumable coverage of the internal reconciliations. These tests run
// each detector in a scope of their own (never the compose worker's "default" checkpoints) with a recency window of a
// single record, so every record the tests judge outside that window can only have been reached by the coverage
// sweep. They are meant for a disposable compose project: each walk covers the whole table (on the contaminated
// development database a walk is long — KI-S19-17).

// itSweep is the shared integration scope of a detector: small budgets so that existing tests stay fast.
func itSweep(name string) reconsweep.Config {
	return reconsweep.Config{Scope: "it." + name, BatchSize: 100, MaxRecords: 200, MaxDuration: time.Minute}
}

// ownSweep is a fresh scope (its first run opens cycle 1) able to walk the whole table in a few runs.
func ownSweep(name string, batch int) reconsweep.Config {
	return reconsweep.Config{Scope: "it." + name + "." + ids.New()[24:], BatchSize: batch, MaxRecords: 100000, MaxDuration: 10 * time.Minute}
}

// payoutCycle runs payout reconciliation until the scope's current cycle completes; it returns the run reports.
func payoutCycle(t *testing.T, svc *payouts.Service) []payouts.ReconReport {
	t.Helper()
	var reps []payouts.ReconReport
	for i := 0; i < 200; i++ {
		rep, err := svc.Reconcile(ctx(t), "it-coverage")
		if err != nil {
			t.Fatalf("reconcile: %v (%+v)", err, rep.Sweep)
		}
		reps = append(reps, rep)
		if rep.Sweep.CycleCompleted {
			return reps
		}
	}
	t.Fatal("the payout sweep cycle never completed")
	return nil
}

func (p *payoutIT) refsOfType(t *testing.T, id, typ string) int {
	t.Helper()
	var n int
	if err := p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_reconciliation_references WHERE payout_request_id = $1 AND mismatch_type = $2`,
		id, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (p *payoutIT) openReconExceptions(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_exceptions WHERE payout_request_id = $1 AND exception_type = 'RECONCILIATION_MISMATCH'
		AND status <> 'RESOLVED'`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// completedPayout drives one TEST payout to COMPLETED with its reservation consumed (the ledger has caught up).
func (p *payoutIT) completedPayout(t *testing.T, camp string, amount int64) string {
	t.Helper()
	id := p.request(t, camp, amount, 202, "")
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	p.drive(t, 30*time.Second, "completed", status(payouts.StCompleted), id)
	waitFor(t, 30*time.Second, "reservation consumed after completion", func() bool {
		got := p.payout(t, id)
		return got.Reservation != nil && got.Reservation.Status == payouts.ResConsumed
	})
	return id
}

// Payouts: a discrepancy introduced in an old payout after a complete clean cycle — outside the recency window — is
// found by the next cycle; a different discrepancy on the same payout while its exception is open is added as evidence
// (no second exception); a persistent discrepancy is not recorded again; once the cause is gone and the exception is
// resolved through the workflow, later cycles record nothing; the checkpoint counters prove the walk examined every
// payout in scope exactly once per cycle.
func TestReconciliationCoveragePayoutOutsideWindow(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 40_000)
	old := p.completedPayout(t, camp, 5_000)
	newer := p.completedPayout(t, camp, 6_000) // newer than old: the window of one never holds old
	rc := *p.po
	rc.ReconWindow = 1
	rc.ReconSweep = ownSweep("payouts", 50)

	reps := payoutCycle(t, &rc)
	if n := p.refsOfType(t, old, "PROVIDER_STATUS_MISMATCH") + p.refsOfType(t, newer, "PROVIDER_STATUS_MISMATCH"); n != 0 {
		t.Fatalf("clean payouts produced %d provider mismatches", n)
	}
	var walked int
	for _, r := range reps {
		walked += r.Sweep.Examined
		if r.Checked > 1 {
			t.Fatalf("recency window of 1 checked %d payouts", r.Checked)
		}
	}
	// proof of coverage: the completed cycle examined every payout past PAYOUT_REQUESTED up to its final key
	var examined, inScope int64
	var last string
	if err := p.mig.QueryRow(ctx(t), `SELECT examined, cursor_key::text FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'COMPLETED'
		AND cycle = 1`, rc.ReconSweep.Scope).Scan(&examined, &last); err != nil {
		t.Fatal(err)
	}
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_requests WHERE status <> 'PAYOUT_REQUESTED' AND id <= $1::uuid`, last).Scan(&inScope)
	if examined != inScope || int64(walked) != examined {
		t.Fatalf("cycle 1 examined %d (runs walked %d) of %d payouts in scope", examined, walked, inScope)
	}
	if old > last {
		t.Fatal("the walk ended before the test's payout")
	}

	// the provider now reports the old payout FAILED (test-only manipulation of the simulator's own record)
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.sandbox_provider_payouts SET raw_status = 'failed' WHERE our_reference = $1`, old); err != nil {
		t.Fatal(err)
	}
	payoutCycle(t, &rc)
	if p.refsOfType(t, old, "PROVIDER_STATUS_MISMATCH") != 1 || p.openReconExceptions(t, old) != 1 {
		t.Fatal("the sweep did not find the discrepancy in a payout outside the recency window")
	}
	// a changed discrepancy while the exception is open: recorded once as evidence, no second exception
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.sandbox_provider_payouts SET amount_minor = amount_minor + 1 WHERE our_reference = $1`, old); err != nil {
		t.Fatal(err)
	}
	reps = payoutCycle(t, &rc)
	evidence := 0
	for _, r := range reps {
		evidence += r.OpenExceptionEvidence
	}
	if p.refsOfType(t, old, "AMOUNT_MISMATCH") != 1 || p.openReconExceptions(t, old) != 1 || evidence < 1 {
		t.Fatalf("changed discrepancy: amount refs %d, open exceptions %d, evidence %d", p.refsOfType(t, old, "AMOUNT_MISMATCH"),
			p.openReconExceptions(t, old), evidence)
	}
	// persistent: two more cycles add nothing
	payoutCycle(t, &rc)
	payoutCycle(t, &rc)
	if p.refsOfType(t, old, "PROVIDER_STATUS_MISMATCH") != 1 || p.refsOfType(t, old, "AMOUNT_MISMATCH") != 1 {
		t.Fatal("a persistent discrepancy was recorded again")
	}
	// the cause is corrected at the provider and the exception resolved through the workflow: nothing new afterwards
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.sandbox_provider_payouts SET raw_status = 'completed', amount_minor = amount_minor - 1
		WHERE our_reference = $1`, old); err != nil {
		t.Fatal(err)
	}
	var exID string
	_ = p.mig.QueryRow(ctx(t), `SELECT id FROM app.payout_exceptions WHERE payout_request_id = $1 AND exception_type = 'RECONCILIATION_MISMATCH'
		AND status <> 'RESOLVED'`, old).Scan(&exID)
	if err := p.po.ResolveException(ctx(t), p.f.finance1.id, exID, "PROVIDER_CONFIRMED", "IT: simulator record restored"); err != nil {
		t.Fatal(err)
	}
	payoutCycle(t, &rc)
	if p.openReconExceptions(t, old) != 0 || p.refsOfType(t, old, "PROVIDER_STATUS_MISMATCH") != 1 {
		t.Fatal("a resolved and corrected discrepancy was reported again")
	}
	if p.refsOfType(t, newer, "PROVIDER_STATUS_MISMATCH")+p.refsOfType(t, newer, "AMOUNT_MISMATCH") != 0 {
		t.Fatal("false finding on a consistent payout")
	}
	p.integrity(t)
}

// Payouts: two reconciliations running concurrently on the same checkpoints, and runs interrupted at arbitrary points
// (deadline), never duplicate a finding, never move the checkpoint backwards (database guard) and still complete a
// cycle that examined every payout in scope exactly once.
func TestReconciliationCoverageConcurrentAndInterrupted(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	bad := p.completedPayout(t, camp, 4_000)
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.sandbox_provider_payouts SET raw_status = 'failed' WHERE our_reference = $1`, bad); err != nil {
		t.Fatal(err)
	}
	a, b := p.reconService(t, "fundzim-it-cov-a"), p.reconService(t, "fundzim-it-cov-b")
	cfg := ownSweep("payouts-conc", 20)
	a.ReconWindow, b.ReconWindow, a.ReconSweep, b.ReconSweep = 1, 1, cfg, cfg

	// interrupted runs: deadlines that expire mid-batch, repeatedly, until the first cycle completes
	interrupted := 0
	deadlines := []time.Duration{time.Millisecond, 3 * time.Millisecond, 8 * time.Millisecond, 20 * time.Millisecond, 60 * time.Millisecond,
		250 * time.Millisecond, 2 * time.Second}
	for i := 0; i < 400; i++ {
		c, cancel := context.WithTimeout(context.Background(), deadlines[min(i, len(deadlines)-1)])
		rep, err := a.Reconcile(c, "it-cov-interrupted")
		cancel()
		if err != nil {
			interrupted++
		}
		if err == nil && rep.Sweep.CycleCompleted {
			break
		}
	}
	var c1 struct {
		examined int64
		last     string
	}
	if err := p.mig.QueryRow(ctx(t), `SELECT examined, cursor_key::text FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND cycle = 1
		AND status = 'COMPLETED'`, cfg.Scope).Scan(&c1.examined, &c1.last); err != nil {
		t.Fatalf("the interrupted runs never completed cycle 1: %v", err)
	}
	var inScope int64
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_requests WHERE status <> 'PAYOUT_REQUESTED' AND id <= $1::uuid`, c1.last).Scan(&inScope)
	if c1.examined != inScope {
		t.Fatalf("interrupted cycle examined %d of %d payouts", c1.examined, inScope)
	}
	t.Logf("cycle 1: %d payouts, %d interrupted runs", inScope, interrupted)
	if interrupted == 0 {
		t.Fatal("no run was interrupted: the test did not exercise resumption")
	}

	// concurrent runs over cycle 2
	var wg sync.WaitGroup
	errc := make(chan error, 2)
	for _, svc := range []*payouts.Service{a, b} {
		wg.Add(1)
		go func(s *payouts.Service) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rep, err := s.Reconcile(context.Background(), "it-cov-concurrent")
				if err != nil {
					errc <- err
					return
				}
				if rep.Sweep.CycleCompleted || rep.Coverage.RunningCycle > 2 {
					return
				}
			}
		}(svc)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
	var c2 int64
	var last2 string
	if err := p.mig.QueryRow(ctx(t), `SELECT examined, cursor_key::text FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND cycle = 2
		AND status = 'COMPLETED'`, cfg.Scope).Scan(&c2, &last2); err != nil {
		t.Fatalf("cycle 2 not completed by the concurrent runs: %v", err)
	}
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_requests WHERE status <> 'PAYOUT_REQUESTED' AND id <= $1::uuid`, last2).Scan(&inScope)
	if c2 != inScope {
		t.Fatalf("concurrent cycle examined %d of %d payouts", c2, inScope)
	}
	if p.refsOfType(t, bad, "PROVIDER_STATUS_MISMATCH") != 1 || p.openReconExceptions(t, bad) != 1 {
		t.Fatalf("finding duplicated or lost: refs %d, open exceptions %d", p.refsOfType(t, bad, "PROVIDER_STATUS_MISMATCH"), p.openReconExceptions(t, bad))
	}
}

// Fees: tampering with an old assessment's stored calculation after a clean cycle — outside the recency window — is
// detected by the next cycle; valid assessments produce no finding; MISSING_FEE is judged per whole source group.
func TestReconciliationCoverageFeeOutsideWindow(t *testing.T) {
	f := newFeeIT(t, 0)
	f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("ORGANISER_COVERED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	donate := func(amount string) fees.FixtureResult {
		return f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.camp, "amount_minor": amount, "currency": "USD", "payment_method": "ECOCASH"}, 201, "")
	}
	d1 := donate("10000")
	d2 := donate("20000")
	svc := *f.fs
	svc.ReconWindow = 1
	svc.ReconSweep = ownSweep("fees", 200)
	cycle := func() {
		for i := 0; i < 200; i++ {
			run, err := svc.Reconcile(ctx(t), "it-coverage")
			if err != nil {
				t.Fatalf("fee reconcile: %v", err)
			}
			if run.Sweep != nil && run.Sweep.CycleCompleted {
				return
			}
		}
		t.Fatal("the fee sweep cycle never completed")
	}
	kinds := func(ids ...string) map[string]int {
		rows, err := f.mig.Query(ctx(t), `SELECT kind, count(*) FROM app.fee_reconciliation_references WHERE status = 'OPEN' AND assessment_id = ANY($1::uuid[])
			GROUP BY kind`, ids)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		m := map[string]int{}
		for rows.Next() {
			var k string
			var n int
			_ = rows.Scan(&k, &n)
			m[k] = n
		}
		return m
	}
	all := func(d fees.FixtureResult) []string {
		var out []string
		for _, a := range d.Assessments {
			out = append(out, a.ID)
		}
		return out
	}
	cycle()
	if k := kinds(all(d1)...); k[fees.RecRoundingMismatch] != 0 || k[fees.RecAmountMismatch] != 0 || k[fees.RecMissingFee] != 0 {
		t.Fatalf("clean assessments produced findings: %v", k)
	}
	var calc string
	_ = f.mig.QueryRow(ctx(t), `SELECT calculation_id FROM app.fee_assessments WHERE id = $1`, d1.Assessments[0].ID).Scan(&calc)
	migratorExec(t, `ALTER TABLE app.fee_calculations DISABLE TRIGGER trg_fee_calculations_no_mutation`)
	migratorExec(t, `UPDATE app.fee_calculations SET result = jsonb_set(result, '{platform_net_minor}', '"1"') WHERE id = $1`, calc)
	migratorExec(t, `ALTER TABLE app.fee_calculations ENABLE TRIGGER trg_fee_calculations_no_mutation`)
	cycle()
	k := kinds(all(d1)...)
	if k[fees.RecRoundingMismatch] == 0 {
		t.Fatalf("the sweep did not detect the tampered calculation of an assessment outside the window: %v", k)
	}
	cycle()
	if again := kinds(all(d1)...); again[fees.RecRoundingMismatch] != k[fees.RecRoundingMismatch] {
		t.Fatalf("a persistent finding was duplicated: %v → %v", k, again)
	}
	if k2 := kinds(all(d2)...); k2[fees.RecRoundingMismatch] != 0 || k2[fees.RecMissingFee] != 0 {
		t.Fatalf("false finding on a valid assessment: %v", k2)
	}
}

// Ledger: journals and payment facts outside their windows are judged by the PAYMENT_JOURNALS and PAYMENT_FACTS sweeps
// (synthetic capture journals and injected facts, the honest TEST-ledger stand-ins of Stage 10's reconciliation test);
// a payment inside its grace period is deferred, carried, and judged once it leaves it; valid pairs produce nothing.
func TestReconciliationCoverageLedgerSweeps(t *testing.T) {
	l := newLedgerIT(t)
	camp := ids.New()
	journal := func(pid string, amount int64, key string) {
		if _, err := l.svc.PostOnce(ctx(t), synth("DONATION_CAPTURED", "fixture:"+pid+":"+key, "payment", pid,
			ledger.Dr(acct(ledger.ClassPSPClearing, ledger.SyntheticProviderID), amount), ledger.Cr(acct(ledger.ClassCampaignUnsettled, camp), amount))); err != nil {
			t.Fatal(err)
		}
	}
	facts := map[string]ledger.PaymentFact{}
	old := time.Now().Add(-48 * time.Hour)
	var good []string
	for i := 0; i < 30; i++ { // consistent journal/fact pairs
		pid := ids.New()
		journal(pid, int64(100+i), "capture")
		facts[pid] = ledger.PaymentFact{ID: pid, Status: "SUCCEEDED", AmountMinor: int64(100 + i), Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR",
			UpdatedAt: old}
		good = append(good, pid)
	}
	// No duplicate capture journal is planted here: the ledger is append-only and invariant L6 would fail for ever on
	// this database (EC-19 then refuses every payout). Cross-batch duplicate detection is exercised on a scratch
	// fundzim_perf database (tests/perf/ledger, TestLedgerReconciliationSweepVolume).
	oldMismatch, unevaluated, hot, unknownOld := ids.New(), ids.New(), ids.New(), ids.New()
	journal(oldMismatch, 1000, "capture") // facts say 1500
	facts[oldMismatch] = ledger.PaymentFact{ID: oldMismatch, Status: "SUCCEEDED", AmountMinor: 1500, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR",
		UpdatedAt: old}
	for i := 0; i < 40; i++ { // many batches after the mismatch
		pid := ids.New()
		journal(pid, 50, "capture")
		facts[pid] = ledger.PaymentFact{ID: pid, Status: "SUCCEEDED", AmountMinor: 50, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR", UpdatedAt: old}
		good = append(good, pid)
	}
	facts[unevaluated] = ledger.PaymentFact{ID: unevaluated, Status: "SUCCEEDED", AmountMinor: 900, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR",
		UpdatedAt: old.Add(-time.Hour)} // the oldest: never in a newest-first window
	facts[hot] = ledger.PaymentFact{ID: hot, Status: "SUCCEEDED", AmountMinor: 300, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR",
		UpdatedAt: time.Now()} // inside its grace period
	facts[unknownOld] = ledger.PaymentFact{ID: unknownOld, Status: "UNKNOWN", AmountMinor: 400, Currency: "USD", ExecutionEnvironment: "LOCAL_SIMULATOR",
		UpdatedAt: old.Add(-2 * time.Hour)}

	ff := &mutableFacts{m: facts}
	svc := ledger.New(l.lp, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.ReconcileJournalsFrom = ledger.SourceSynthetic
	svc.ReconWindow = 1
	cfg := ownSweep("ledger", 10)
	svc.ReconSweep = cfg
	svc.Facts = ff
	cycle := func() []ledger.ReconcileReport {
		var reps []ledger.ReconcileReport
		jDone, fDone := false, false
		for i := 0; i < 2000 && !(jDone && fDone); i++ {
			rep, err := svc.Reconcile(ctx(t), "it-coverage")
			if err != nil {
				t.Fatalf("ledger reconcile: %v", err)
			}
			reps = append(reps, rep)
			jDone = jDone || rep.Sweeps[ledger.SweepPaymentJournals].CycleCompleted
			fDone = fDone || rep.Sweeps[ledger.SweepPaymentFacts].CycleCompleted
		}
		if !jDone || !fDone {
			t.Fatal("the ledger sweep cycles never completed")
		}
		return reps
	}
	open := func(src, typ string) int {
		return l.count(t, `SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE source_id = $1 AND exception_type = $2 AND status = 'OPEN'`, src, typ)
	}
	reps := cycle()
	for src, typ := range map[string]string{oldMismatch: ledger.ExAmountMismatch, unevaluated: ledger.ExMissingPosting, unknownOld: ledger.ExUnknownOutcome} {
		if open(src, typ) != 1 {
			t.Fatalf("%s for %s not found by the sweep", typ, src)
		}
	}
	for _, pid := range good {
		if n := l.count(t, `SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE source_id = $1 AND exception_type <> 'MISSING_POSTING'
			AND status = 'OPEN'`, pid); n != 0 {
			t.Fatalf("false finding on a consistent journal %s", pid)
		}
	}
	if open(hot, ledger.ExMissingPosting) != 0 {
		t.Fatal("a payment inside its grace period was judged")
	}
	deferred := false
	for _, r := range reps {
		if r.Sweeps[ledger.SweepPaymentFacts].DeferredCarried > 0 {
			deferred = true
		}
	}
	if !deferred {
		t.Fatal("the payment inside its grace period was not carried as a deferral")
	}
	// it leaves its grace period: the next run's deferral retry judges it (no full cycle needed)
	ff.set(hot, func(f *ledger.PaymentFact) { f.UpdatedAt = old })
	if _, err := svc.Reconcile(ctx(t), "it-coverage"); err != nil {
		t.Fatal(err)
	}
	if open(hot, ledger.ExMissingPosting) != 1 {
		t.Fatal("the deferred payment was not judged after its grace period")
	}
	// an old, previously reconciled pair changes (the payment's amount fact differs now): found by the next cycle
	ff.set(good[0], func(f *ledger.PaymentFact) { f.AmountMinor++ })
	cycle()
	if open(good[0], ledger.ExAmountMismatch) != 1 {
		t.Fatal("a change to an old payment after its reconciliation was not re-detected")
	}
	cov, err := (reconsweep.SQLStore{Pool: l.lp, Table: "ledger.ledger_reconciliation_sweeps"}).Coverage(ctx(t), ledger.SweepPaymentJournals,
		cfg.Scope+"."+ledger.SourceSynthetic)
	if err != nil || cov.LastCompletedCycleStartedAt == nil || cov.LastCompletedExamined == 0 {
		t.Fatalf("coverage evidence: %+v %v", cov, err)
	}
	// the run never touched the payments-sourced checkpoints of its own scope
	if n := l.count(t, `SELECT count(*) FROM ledger.ledger_reconciliation_sweeps WHERE scope = $1`, cfg.Scope); n != 0 {
		t.Fatalf("a synthetic-source run wrote %d checkpoints in the payments scope", n)
	}
}

type mutableFacts struct {
	mu sync.Mutex
	m  map[string]ledger.PaymentFact
}

func (f *mutableFacts) set(id string, fn func(*ledger.PaymentFact)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	x := f.m[id]
	fn(&x)
	f.m[id] = x
}

func (f *mutableFacts) snapshot() fakeFacts {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := make(map[string]ledger.PaymentFact, len(f.m))
	for k, v := range f.m {
		m[k] = v
	}
	return fakeFacts{m: m}
}

func (f *mutableFacts) Payment(c context.Context, id string) (ledger.PaymentFact, bool, error) {
	return f.snapshot().Payment(c, id)
}
func (f *mutableFacts) PaymentsInStatus(c context.Context, st string, before time.Time, limit int) ([]ledger.PaymentFact, error) {
	return f.snapshot().PaymentsInStatus(c, st, before, limit)
}
func (f *mutableFacts) PaymentIDsAfter(c context.Context, after string, sts []string, limit int) ([]string, error) {
	return f.snapshot().PaymentIDsAfter(c, after, sts, limit)
}

// The checkpoint tables' database guards, as the runtime roles: one RUNNING cycle per detector and scope, the cursor
// never moves backwards, counters never decrease, version advances by one, completed cycles are immutable, nothing is
// deleted; the app role has no access to the ledger's checkpoints.
func TestReconciliationCoverageCheckpointGuards(t *testing.T) {
	need(t, "DATABASE_URL", "DATABASE_LEDGER_URL")
	app, lp := pool(t, os.Getenv("DATABASE_URL")), pool(t, os.Getenv("DATABASE_LEDGER_URL"))
	check := func(name, table, detector string, exec func(sql string, args ...any) error) {
		scope := "it.guard." + ids.New()[24:]
		id := ids.New()
		if err := exec(`INSERT INTO `+table+` (id, detector, scope, cycle, status, started_at) VALUES ($1, $2, $3, 1, 'RUNNING', now())`, id, detector, scope); err != nil {
			t.Fatalf("%s insert: %v", name, err)
		}
		if err := exec(`INSERT INTO `+table+` (id, detector, scope, cycle, status, started_at) VALUES ($1, $2, $3, 2, 'RUNNING', now())`, ids.New(), detector, scope); err == nil {
			t.Fatalf("%s: a second RUNNING cycle was accepted", name)
		}
		hi, lo := "ffffffff-0000-7000-8000-000000000000", "00000001-0000-7000-8000-000000000000"
		if err := exec(`UPDATE `+table+` SET cursor_key = $2, examined = 10, version = version + 1 WHERE id = $1`, id, hi); err != nil {
			t.Fatalf("%s advance: %v", name, err)
		}
		for what, sql := range map[string]string{
			"cursor backwards":  `UPDATE ` + table + ` SET cursor_key = '` + lo + `', version = version + 1 WHERE id = $1`,
			"cursor reset":      `UPDATE ` + table + ` SET cursor_key = NULL, version = version + 1 WHERE id = $1`,
			"counter decreased": `UPDATE ` + table + ` SET examined = 5, version = version + 1 WHERE id = $1`,
			"version skipped":   `UPDATE ` + table + ` SET examined = 11, version = version + 2 WHERE id = $1`,
			"version kept":      `UPDATE ` + table + ` SET examined = 11 WHERE id = $1`,
			"scope changed":     `UPDATE ` + table + ` SET scope = 'it.other', version = version + 1 WHERE id = $1`,
			"delete":            `DELETE FROM ` + table + ` WHERE id = $1`,
		} {
			if err := exec(sql, id); err == nil {
				t.Fatalf("%s: %s was accepted", name, what)
			}
		}
		if err := exec(`UPDATE `+table+` SET status = 'COMPLETED', completed_at = now(), version = version + 1 WHERE id = $1`, id); err != nil {
			t.Fatalf("%s complete: %v", name, err)
		}
		if err := exec(`UPDATE `+table+` SET examined = 99, version = version + 1 WHERE id = $1`, id); err == nil {
			t.Fatalf("%s: a completed cycle was changed", name)
		}
		if err := exec(`UPDATE `+table+` SET status = 'RUNNING', completed_at = NULL, version = version + 1 WHERE id = $1`, id); err == nil {
			t.Fatalf("%s: a completed cycle was reopened", name)
		}
	}
	appExec := func(sql string, args ...any) error { _, err := app.Exec(ctx(t), sql, args...); return err }
	ledExec := func(sql string, args ...any) error { _, err := lp.Exec(ctx(t), sql, args...); return err }
	check("payouts", "app.payout_reconciliation_sweeps", payouts.SweepDetector, appExec)
	check("fees", "app.fee_reconciliation_sweeps", fees.SweepDetector, appExec)
	check("ledger", "ledger.ledger_reconciliation_sweeps", ledger.SweepPaymentJournals, ledExec)
	if _, err := app.Exec(ctx(t), `SELECT 1 FROM ledger.ledger_reconciliation_sweeps LIMIT 1`); err == nil {
		t.Fatal("fundzim_app can read the ledger's checkpoints")
	}
	var pgErr interface{ SQLState() string }
	if _, err := lp.Exec(ctx(t), `SELECT 1 FROM app.payout_reconciliation_sweeps LIMIT 1`); err == nil || !errors.As(err, &pgErr) {
		t.Fatal("fundzim_ledger can read the payout checkpoints")
	}
}
