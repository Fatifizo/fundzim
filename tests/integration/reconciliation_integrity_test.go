//go:build integration

package integration

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Remediation phase 4: checkpoint integrity on the real database — the constraint triggers refuse checkpoint moves
// without matching evidence (as the module's own role), evidence is append-only, the independent verifier catches a
// forged but self-consistent advance, interrupted runs leave nothing inconsistent, and real sweeps (deferrals, new
// records, concurrent workers) verify. Each test uses its own scopes (never the worker's "default").

const payoutSweeps = "app.payout_reconciliation_sweeps"

func integrityScope(name string) reconsweep.Config {
	return reconsweep.Config{Scope: "it.integrity." + name + "." + ids.New()[24:], BatchSize: 5, MaxRecords: 10, MaxDuration: 5 * time.Minute}
}

type sweepRow struct {
	id, cursor        string
	examined, batches int64
	version           int64
}

func runningSweep(t *testing.T, q *pgxpool.Pool, table, scope string) sweepRow {
	t.Helper()
	var r sweepRow
	if err := q.QueryRow(ctx(t), `SELECT id::text, coalesce(cursor_key::text, ''), examined, batches, version FROM `+table+`
		WHERE scope = $1 AND status = 'RUNNING'`, scope).Scan(&r.id, &r.cursor, &r.examined, &r.batches, &r.version); err != nil {
		t.Fatalf("running sweep of %s: %v", scope, err)
	}
	return r
}

// payoutSource is an independent scope read for tests (the same rule as payouts.Service.sweepScope).
func payoutSource(q *pgxpool.Pool) reconsweep.Source {
	return func(ctx context.Context, from, to string) ([]reconsweep.Stamp, error) {
		rows, err := q.Query(ctx, `SELECT id::text, updated_at FROM app.payout_requests WHERE status <> 'PAYOUT_REQUESTED' AND id > $1::uuid
			AND id <= $2::uuid`, reconsweep.After(from), reconsweep.Until(to))
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (reconsweep.Stamp, error) {
			var s reconsweep.Stamp
			return s, r.Scan(&s.Key, &s.Modified)
		})
	}
}

func verifyPayoutScope(t *testing.T, p *payoutIT, scope string) reconsweep.IntegrityReport {
	t.Helper()
	r, err := reconsweep.Verify(ctx(t), reconsweep.SQLStore{Pool: p.mig, Table: payoutSweeps}, payoutSource(p.mig),
		reconsweep.IntegrityOptions{Detector: payouts.SweepDetector, Scope: scope, Margin: time.Nanosecond, MaxRuntime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The database refuses, as the module's own role, every checkpoint move its evidence does not justify; evidence rows
// are append-only; a new cycle starts empty with the previous cycle's deferrals.
func TestReconciliationIntegrityDatabaseRefusesUnsupportedMoves(t *testing.T) {
	p := newPayoutIT(t)
	svc := *p.po
	svc.ReconWindow = 1
	svc.ReconSweep = integrityScope("refuse")
	if _, err := svc.Reconcile(ctx(t), "it-integrity"); err != nil {
		t.Fatal(err)
	}
	cp := runningSweep(t, p.app, payoutSweeps, svc.ReconSweep.Scope)
	if cp.batches == 0 {
		t.Fatal("fixture: no batch evidenced")
	}
	exec := func(q *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
		tx, err := q.Begin(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit(ctx(t))
	}
	jump := func(tx pgx.Tx, extra string) error {
		_, err := tx.Exec(ctx(t), `UPDATE app.payout_reconciliation_sweeps SET cursor_key = 'ffffffff-ffff-ffff-ffff-ffffffffffff', examined = examined + 1,
			batches = batches + 1, version = version + 1`+extra+` WHERE id = $1`, cp.id)
		return err
	}
	// 1. a forward jump without evidence (as fundzim_app, the payouts module's role)
	if err := exec(p.app, func(tx pgx.Tx) error { return jump(tx, "") }); sqlState(err) != "23514" || !strings.Contains(err.Error(), "without evidence") {
		t.Fatalf("forward jump without evidence: %v", err)
	}
	// 2. with evidence that does not match (examined grows by 1, the evidence holds no key)
	if err := exec(p.app, func(tx pgx.Tx) error {
		if err := jump(tx, ""); err != nil {
			return err
		}
		_, err := tx.Exec(ctx(t), `INSERT INTO app.payout_reconciliation_sweep_batches (id, sweep_id, detector, scope, cycle, seq, from_key, to_key, keys, completing)
			VALUES (gen_random_uuid(), $1, 'PAYOUTS', $2, 1, $3, nullif($4, '')::uuid, 'ffffffff-ffff-ffff-ffff-ffffffffffff', '{}', false)`,
			cp.id, svc.ReconSweep.Scope, cp.batches+1, cp.cursor)
		return err
	}); sqlState(err) != "23514" || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("forward jump with mismatching evidence: %v", err)
	}
	// 3. a deferral added outside any batch
	if err := exec(p.app, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx(t), `UPDATE app.payout_reconciliation_sweeps SET deferred_keys = deferred_keys || gen_random_uuid(),
			deferred_since = deferred_since || now(), batches = batches + 1, version = version + 1 WHERE id = $1`, cp.id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx(t), `INSERT INTO app.payout_reconciliation_sweep_batches (id, sweep_id, detector, scope, cycle, seq, from_key, to_key, keys, completing)
			VALUES (gen_random_uuid(), $1, 'PAYOUTS', $2, 1, $3, nullif($4, '')::uuid, nullif($4, '')::uuid, '{}', false)`,
			cp.id, svc.ReconSweep.Scope, cp.batches+1, cp.cursor)
		return err
	}); sqlState(err) != "23514" || !strings.Contains(err.Error(), "deferrals") {
		t.Fatalf("deferral changed outside the evidence: %v", err)
	}
	// 4. evidence is append-only
	if _, err := p.app.Exec(ctx(t), `UPDATE app.payout_reconciliation_sweep_batches SET keys = '{}' WHERE sweep_id = $1`, cp.id); err == nil {
		t.Fatal("evidence was updated")
	}
	if _, err := p.app.Exec(ctx(t), `DELETE FROM app.payout_reconciliation_sweep_batches WHERE sweep_id = $1`, cp.id); err == nil {
		t.Fatal("evidence was deleted")
	}
	// 5. a new cycle must start empty with the previous cycle's deferrals (as fundzim_ledger on the ledger table)
	led := pool(t, os.Getenv("DATABASE_LEDGER_URL"))
	scope := "it.integrity.ledger." + ids.New()[24:]
	if _, err := led.Exec(ctx(t), `INSERT INTO ledger.ledger_reconciliation_sweeps (id, detector, scope, cycle, status, started_at, cursor_key)
		VALUES (gen_random_uuid(), 'PAYMENT_JOURNALS', $1, 1, 'RUNNING', now(), gen_random_uuid())`, scope); sqlState(err) != "23514" {
		t.Fatalf("a cycle starting with a cursor: %v", err)
	}
	if _, err := led.Exec(ctx(t), `INSERT INTO ledger.ledger_reconciliation_sweeps (id, detector, scope, cycle, status, started_at, deferred_keys, deferred_since)
		VALUES (gen_random_uuid(), 'PAYMENT_JOURNALS', $1, 1, 'RUNNING', now(), ARRAY[gen_random_uuid()], ARRAY[now()])`, scope); sqlState(err) != "23514" {
		t.Fatalf("a first cycle carrying deferrals: %v", err)
	}
	var lid string
	if err := led.QueryRow(ctx(t), `INSERT INTO ledger.ledger_reconciliation_sweeps (id, detector, scope, cycle, status, started_at)
		VALUES (gen_random_uuid(), 'PAYMENT_JOURNALS', $1, 1, 'RUNNING', now()) RETURNING id::text`, scope).Scan(&lid); err != nil {
		t.Fatalf("a valid empty first cycle: %v", err)
	}
	if _, err := led.Exec(ctx(t), `UPDATE ledger.ledger_reconciliation_sweeps SET cursor_key = gen_random_uuid(), examined = 1, batches = 1,
		version = version + 1 WHERE id = $1`, lid); sqlState(err) != "23514" {
		t.Fatalf("ledger forward jump without evidence (fundzim_ledger): %v", err)
	}
	// the checkpoint is where the sweep left it, and its evidence still verifies
	if after := runningSweep(t, p.app, payoutSweeps, svc.ReconSweep.Scope); after != cp {
		t.Fatalf("checkpoint changed by refused statements: %+v → %+v", cp, after)
	}
	if r := verifyPayoutScope(t, p, svc.ReconSweep.Scope); r.Status != reconsweep.IntegrityVerified {
		t.Fatalf("integrity after refused moves: %s %+v", r.Status, r.Problems)
	}
}

// A forged advance that is self-consistent — evidence and checkpoint agree, so the database accepts it — but skips
// real payouts is caught by the independent re-read: RECORD_SKIPPED for exactly the skipped payouts, the integrity
// status INCONSISTENT on the checkpoint, the sweep health INTEGRITY_FAILURE. Nothing is changed automatically.
func TestReconciliationIntegrityDetectsAForgedConsistentAdvance(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 30_000)
	for i := 0; i < 3; i++ { // payouts beyond any existing cursor of a fresh scope's walk
		p.completedPayout(t, camp, int64(4_000+i*100))
	}
	svc := *p.po
	svc.ReconWindow = 1
	svc.ReconSweep = integrityScope("forged")
	if _, err := svc.Reconcile(ctx(t), "it-integrity"); err != nil {
		t.Fatal(err)
	}
	if r := verifyPayoutScope(t, p, svc.ReconSweep.Scope); r.Status != reconsweep.IntegrityVerified {
		t.Fatalf("before forging: %s %+v", r.Status, r.Problems)
	}
	cp := runningSweep(t, p.app, payoutSweeps, svc.ReconSweep.Scope)
	rows, err := p.mig.Query(ctx(t), `SELECT id::text FROM app.payout_requests WHERE status <> 'PAYOUT_REQUESTED' AND id > $1::uuid ORDER BY id LIMIT 3`,
		reconsweep.After(cp.cursor))
	if err != nil {
		t.Fatal(err)
	}
	next, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(next) != 3 {
		t.Fatalf("three payouts after the cursor: %v %v", next, err)
	}
	tx, err := p.app.Begin(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx(t), `UPDATE app.payout_reconciliation_sweeps SET cursor_key = $2::uuid, examined = examined + 1, batches = batches + 1,
		version = version + 1 WHERE id = $1`, cp.id, next[2]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx(t), `INSERT INTO app.payout_reconciliation_sweep_batches (id, sweep_id, detector, scope, cycle, seq, from_key, to_key, keys, completing)
		VALUES (gen_random_uuid(), $1, 'PAYOUTS', $2, 1, $3, nullif($4, '')::uuid, $5::uuid, ARRAY[$5::uuid], false)`,
		cp.id, svc.ReconSweep.Scope, cp.batches+1, cp.cursor, next[2]); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx(t)); err != nil {
		t.Fatalf("the database accepted a self-consistent advance is the expected limit of the preventive layer; got %v", err)
	}
	r := verifyPayoutScope(t, p, svc.ReconSweep.Scope)
	var skipped []string
	for _, pr := range r.Problems {
		if pr.Code == reconsweep.ProblemRecordSkipped {
			skipped = append(skipped, pr.Keys...)
		}
	}
	slices.Sort(skipped)
	if r.Status != reconsweep.IntegrityInconsistent || !slices.Equal(skipped, next[:2]) {
		t.Fatalf("forged advance: %s, skipped %v, want %v (%+v)", r.Status, skipped, next[:2], r.Problems)
	}
	cov, err := reconsweep.SQLStore{Pool: p.mig, Table: payoutSweeps}.Coverage(ctx(t), payouts.SweepDetector, svc.ReconSweep.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if h := cov.Health(time.Now(), reconsweep.DefaultThresholds, 10000, 0); cov.IntegrityStatus != reconsweep.IntegrityInconsistent ||
		h.State != reconsweep.HealthIntegrityFailure {
		t.Fatalf("recorded integrity %q, health %s", cov.IntegrityStatus, h.State)
	}
	if after := runningSweep(t, p.app, payoutSweeps, svc.ReconSweep.Scope); after.cursor != next[2] {
		t.Fatalf("the verifier moved the checkpoint: %+v", after)
	}
}

// A worker killed after it examined a batch (and recorded its findings) but before it advanced the checkpoint leaves
// the checkpoint and the evidence untouched; the failed run is recorded; the next run re-examines the batch and the
// integrity verifies.
func TestReconciliationIntegrityKilledBeforeCheckpointAdvance(t *testing.T) {
	p := newPayoutIT(t)
	svc := *p.po
	svc.ReconWindow = 1
	svc.ReconSweep = integrityScope("killed")
	if _, err := svc.Reconcile(ctx(t), "it-integrity"); err != nil {
		t.Fatal(err)
	}
	cp := runningSweep(t, p.app, payoutSweeps, svc.ReconSweep.Scope)
	const app = "fundzim-it-r4-killed"
	rc := svc
	rc.Pool = namedPool(t, os.Getenv("DATABASE_URL"), app)
	lock := lockRow(t, p.mig, payoutSweeps, cp.id) // the checkpoint row: the run blocks exactly at its advance
	su := superuser(t)
	done := make(chan error, 1)
	go func() { _, err := rc.Reconcile(context.Background(), "it-integrity-killed"); done <- err }()
	deadline := time.Now().Add(2 * time.Minute)
	var victims []int
	for len(victims) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the run never reached its checkpoint advance")
		}
		time.Sleep(20 * time.Millisecond)
		victims = lock.waiters(app)
	}
	terminate(t, su, victims) // the batch was examined; its advance dies with the connection
	lock.release()
	if err := <-done; err == nil {
		t.Fatal("the killed run reported success")
	}
	after := runningSweep(t, p.app, payoutSweeps, svc.ReconSweep.Scope)
	if after.cursor != cp.cursor || after.batches != cp.batches || after.examined != cp.examined {
		t.Fatalf("the checkpoint moved without its advance: %+v → %+v", cp, after)
	}
	var evidence int64
	var failed int
	if err := p.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM app.payout_reconciliation_sweep_batches WHERE sweep_id = $1),
		(SELECT failed_attempts FROM app.payout_reconciliation_sweeps WHERE id = $1)`, cp.id).Scan(&evidence, &failed); err != nil {
		t.Fatal(err)
	}
	if evidence != cp.batches || failed != 1 {
		t.Fatalf("evidence rows %d (want %d), failed attempts %d (want 1)", evidence, cp.batches, failed)
	}
	if _, err := svc.Reconcile(ctx(t), "it-integrity-resume"); err != nil {
		t.Fatal(err)
	}
	if r := verifyPayoutScope(t, p, svc.ReconSweep.Scope); r.Status != reconsweep.IntegrityVerified {
		t.Fatalf("after resuming: %s %+v", r.Status, r.Problems)
	}
	if again := runningSweep(t, p.app, payoutSweeps, svc.ReconSweep.Scope); again.batches <= cp.batches {
		t.Fatalf("the resumed run did not advance: %+v", again)
	}
}

// Real sweeps verify: payouts with a failing provider lookup (deferred, not reconciled) and new payouts arriving during
// the cycle, two concurrent workers, a completed cycle; fees; ledger journals. Each run records its integrity status.
func TestReconciliationIntegrityRealSweepsVerify(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 30_000)
	first := p.completedPayout(t, camp, 4_000)
	fa := &faultAdapter{Adapter: p.s.d.Verification.Sandbox, t: t, status: map[string]func(psp.Observation) psp.Observation{}, calls: map[string]int{}}
	fa.set(first, func(psp.Observation) psp.Observation {
		return psp.Observation{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout}
	})
	reg := psp.New(p.po.PSP.Pool, p.po.PSP.Clock, p.po.PSP.Logger, p.po.PSP.Secrets, p.po.PSP.Cfg, nil)
	reg.Register(fa)
	svc := *p.po
	svc.PSP = reg
	svc.ReconWindow = 1
	svc.ReconSweep = reconsweep.Config{Scope: "it.integrity.real." + ids.New()[24:], BatchSize: 50, MaxRecords: 200, MaxDuration: 5 * time.Minute}
	rep, err := svc.Reconcile(ctx(t), "it-integrity")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Integrity == nil || rep.Integrity.Status != reconsweep.IntegrityVerified {
		t.Fatalf("first run integrity: %+v", rep.Integrity)
	}
	p.completedPayout(t, camp, 4_100) // a new in-scope payout during the cycle
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ { // two concurrent workers on the same scope
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := svc
			if _, err := w.Reconcile(context.Background(), "it-integrity-concurrent"); err != nil && !t.Failed() {
				t.Errorf("concurrent: %v", err)
			}
		}()
	}
	wg.Wait()
	reps := payoutCycle(t, &svc)
	last := reps[len(reps)-1]
	if last.Integrity == nil || last.Integrity.Status != reconsweep.IntegrityVerified || len(last.Integrity.Cycles) == 0 {
		t.Fatalf("integrity after the cycle completed: %+v", last.Integrity)
	}
	if !slices.Contains(checkpointDeferrals(t, p, svc.ReconSweep.Scope), first) {
		t.Fatal("the payout with a failing lookup is not carried as a deferral")
	}
	// fees
	f := &feeIT{payoutIT: p, fs: p.s.d.Verification.Fees, camp: camp}
	fsvc := *f.fs
	fsvc.ReconWindow = 1
	fsvc.ReconSweep = reconsweep.Config{Scope: "it.integrity.fees." + ids.New()[24:], BatchSize: 100, MaxRecords: 100000, MaxDuration: 5 * time.Minute}
	var frun fees.ReconRun
	for i := 0; i < 200; i++ {
		if frun, err = fsvc.Reconcile(ctx(t), "it-integrity"); err != nil {
			t.Fatal(err)
		}
		if frun.Sweep != nil && frun.Sweep.CycleCompleted {
			break
		}
	}
	if frun.Integrity == nil || frun.Integrity.Status != reconsweep.IntegrityVerified {
		t.Fatalf("fee integrity: %+v", frun.Integrity)
	}
	// ledger journals (synthetic source, own scope)
	led := ledger.New(pool(t, os.Getenv("DATABASE_LEDGER_URL")), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	led.ReconcileJournalsFrom = ledger.SourceSynthetic
	led.ReconSweep = reconsweep.Config{Scope: "it.integrity.ledger." + ids.New()[24:], BatchSize: 100, MaxRecords: 100000, MaxDuration: 5 * time.Minute}
	lrep, err := led.Reconcile(ctx(t), "it-integrity")
	if err != nil {
		t.Fatal(err)
	}
	if li, ok := lrep.Integrity["PAYMENT_JOURNALS"]; !ok || li.Status != reconsweep.IntegrityVerified {
		t.Fatalf("ledger journal integrity: %+v", lrep.Integrity)
	}
}
