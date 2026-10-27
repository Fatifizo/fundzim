//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/reconsweep"
)

// Remediation phase 6 (F-R5-01/02): the integrity status on the real database — every verification outcome is
// recorded (a failure replaces a previous VERIFIED as the latest outcome), the last success, the start of the
// unverified period, the consecutive failures and the latest failure are kept apart, an older check never overwrites a
// newer one, the status survives a cycle boundary, and a failed verification changes no checkpoint position, payout,
// attempt or ledger record. Each test uses its own scope (never the worker's "default").

var errInjected = errors.New("injected read failure")

func failingSource(context.Context, string, string) ([]reconsweep.Stamp, error) {
	return nil, errInjected
}

type integrityRow struct {
	status, errCode, lastFailure                   string
	checkedAt, verifiedAt, unverifiedSince, lastAt *time.Time
	failures                                       int
	cursor                                         string
	examined, batches, version                     int64
}

func readIntegrity(t *testing.T, q *pgxpool.Pool, scope string) integrityRow {
	t.Helper()
	var r integrityRow
	if err := q.QueryRow(ctx(t), `SELECT coalesce(integrity_status, ''), coalesce(integrity_error, ''), coalesce(integrity_last_failure, ''),
		integrity_checked_at, integrity_verified_at, integrity_unverified_since, integrity_last_failure_at, integrity_failures,
		coalesce(cursor_key::text, ''), examined, batches, version
		FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'RUNNING'`, scope).
		Scan(&r.status, &r.errCode, &r.lastFailure, &r.checkedAt, &r.verifiedAt, &r.unverifiedSince, &r.lastAt, &r.failures,
			&r.cursor, &r.examined, &r.batches, &r.version); err != nil {
		t.Fatalf("integrity of %s: %v", scope, err)
	}
	return r
}

func sameTime(a *time.Time, b time.Time) bool {
	return a != nil && a.Equal(b.Truncate(time.Microsecond))
}

func verifyScope(c context.Context, q *pgxpool.Pool, scope string, src reconsweep.Source) (reconsweep.IntegrityReport, error) {
	return reconsweep.Verify(c, reconsweep.SQLStore{Pool: q, Table: payoutSweeps}, src,
		reconsweep.IntegrityOptions{Detector: payouts.SweepDetector, Scope: scope, Margin: time.Nanosecond, MaxRuntime: time.Minute})
}

// Success, then a failing read, then an interrupted check, then an out-of-order older result, then recovery — on a real
// payout sweep. Nothing but the integrity columns changes.
func TestReconciliationIntegrityStatusFailureAndRecovery(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 30_000)
	var payoutIDs []string
	for i := 0; i < 3; i++ {
		payoutIDs = append(payoutIDs, p.completedPayout(t, camp, int64(4_000+i*100)))
	}
	svc := *p.po
	svc.ReconWindow = 1
	svc.ReconSweep = integrityScope("status")
	scope := svc.ReconSweep.Scope
	if _, err := svc.Reconcile(ctx(t), "it-integrity-status"); err != nil {
		t.Fatal(err)
	}
	fingerprint := func() string {
		var s string
		if err := p.mig.QueryRow(ctx(t), `SELECT
			coalesce((SELECT md5(string_agg(to_jsonb(r)::text, ',' ORDER BY r.id)) FROM app.payout_requests r WHERE r.id = ANY($1::uuid[])), '') ||
			coalesce((SELECT md5(string_agg(to_jsonb(a)::text, ',' ORDER BY a.id)) FROM app.payout_attempts a WHERE a.payout_request_id = ANY($1::uuid[])), '') ||
			coalesce((SELECT md5(string_agg(to_jsonb(x)::text, ',' ORDER BY x.id)) FROM ledger.ledger_transactions x WHERE x.source_id = ANY($1::uuid[])), '')`,
			payoutIDs).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := fingerprint()

	ok1, err := verifyScope(ctx(t), p.mig, scope, payoutSource(p.mig))
	if err != nil || ok1.Status != reconsweep.IntegrityVerified {
		t.Fatalf("first verification: %s %v %+v", ok1.Status, err, ok1.Problems)
	}
	r := readIntegrity(t, p.app, scope)
	pos := r
	if r.status != "VERIFIED" || !sameTime(r.verifiedAt, ok1.CheckedAt) || r.unverifiedSince != nil || r.failures != 0 || r.lastFailure != "" {
		t.Fatalf("after success: %+v", r)
	}

	bad, err := verifyScope(ctx(t), p.mig, scope, failingSource)
	if !errors.Is(err, errInjected) || bad.Status != reconsweep.IntegrityError {
		t.Fatalf("failing read: %s %v", bad.Status, err)
	}
	r = readIntegrity(t, p.app, scope)
	if r.status != "ERROR" || r.errCode != reconsweep.IntegrityErrSourceRead || !sameTime(r.checkedAt, bad.CheckedAt) ||
		!sameTime(r.verifiedAt, ok1.CheckedAt) || !sameTime(r.unverifiedSince, bad.CheckedAt) || r.failures != 1 ||
		r.lastFailure != "ERROR:SOURCE_READ_FAILED" || !sameTime(r.lastAt, bad.CheckedAt) {
		t.Fatalf("after a failure following a success: %+v", r)
	}
	cov, err := reconsweep.SQLStore{Pool: p.mig, Table: payoutSweeps}.Coverage(ctx(t), payouts.SweepDetector, scope)
	if err != nil {
		t.Fatal(err)
	}
	if h := cov.Health(time.Now(), reconsweep.DefaultThresholds, 10000, 0); h.IntegrityState != reconsweep.IntegrityStateLatestFailed ||
		h.IntegrityMetric("payouts", "k", "l", "p", "s").Status == "HEALTHY" {
		t.Fatalf("a failed latest check shown as %s", h.IntegrityState)
	}
	// the job's context ends during the re-read: INCOMPLETE/INTERRUPTED, recorded although the context is cancelled
	cctx, cancel := context.WithCancel(ctx(t))
	intr, err := verifyScope(cctx, p.mig, scope, func(c context.Context, _, _ string) ([]reconsweep.Stamp, error) {
		cancel()
		return nil, c.Err()
	})
	if !errors.Is(err, context.Canceled) || intr.Status != reconsweep.IntegrityIncomplete {
		t.Fatalf("interrupted: %s %v", intr.Status, err)
	}
	r = readIntegrity(t, p.app, scope)
	if r.status != "INCOMPLETE" || r.errCode != reconsweep.IntegrityErrInterrupted || r.failures != 2 ||
		!sameTime(r.unverifiedSince, bad.CheckedAt) || r.lastFailure != "INCOMPLETE:INTERRUPTED" {
		t.Fatalf("after an interruption: %+v", r)
	}
	// an older check (a slow concurrent run) finishing last never overwrites the newer outcome
	stale := ok1
	stale.CheckedAt = ok1.CheckedAt.Add(bad.CheckedAt.Sub(ok1.CheckedAt) / 2) // started between the success and the failure
	if err := (reconsweep.SQLStore{Pool: p.mig, Table: payoutSweeps}).RecordIntegrity(ctx(t), payouts.SweepDetector, scope, stale); err != nil {
		t.Fatal(err)
	}
	if r2 := readIntegrity(t, p.app, scope); r2.status != "INCOMPLETE" || !sameTime(r2.verifiedAt, ok1.CheckedAt) || r2.failures != 2 {
		t.Fatalf("an older VERIFIED overwrote a newer failure: %+v", r2)
	}
	// recovery: the success is current again; the latest failure is still reported
	ok2, err := verifyScope(ctx(t), p.mig, scope, payoutSource(p.mig))
	if err != nil || ok2.Status != reconsweep.IntegrityVerified {
		t.Fatalf("recovery: %s %v", ok2.Status, err)
	}
	r = readIntegrity(t, p.app, scope)
	if r.status != "VERIFIED" || r.errCode != "" || !sameTime(r.verifiedAt, ok2.CheckedAt) || r.unverifiedSince != nil || r.failures != 0 ||
		r.lastFailure != "INCOMPLETE:INTERRUPTED" || !sameTime(r.lastAt, intr.CheckedAt) {
		t.Fatalf("after recovery: %+v", r)
	}
	if r.cursor != pos.cursor || r.examined != pos.examined || r.batches != pos.batches || r.version != pos.version {
		t.Fatalf("a verification moved the checkpoint: %+v -> %+v", pos, r)
	}
	if after := fingerprint(); after != before {
		t.Fatal("a verification changed a payout, an attempt or a ledger transaction")
	}
}

// The status carries across a cycle boundary; concurrent verifications leave the newest outcome; the database refuses
// an error text in place of a code (as the module's own role).
func TestReconciliationIntegrityStatusCarriesAndOrders(t *testing.T) {
	need(t, "DATABASE_URL")
	app := pool(t, os.Getenv("DATABASE_URL"))
	scope := "it.integrity.carry." + ids.New()[24:]
	keys := make([]string, 6)
	for i := range keys {
		keys[i] = ids.New()
	}
	slices.Sort(keys)
	modified := time.Now().Add(-time.Hour)
	next := func(_ context.Context, after string, limit int) ([]string, error) {
		var out []string
		for _, k := range keys {
			if k > after && len(out) < limit {
				out = append(out, k)
			}
		}
		return out, nil
	}
	src := func(_ context.Context, from, to string) ([]reconsweep.Stamp, error) {
		var out []reconsweep.Stamp
		for _, k := range keys {
			if (from == "" || k > from) && k <= to {
				out = append(out, reconsweep.Stamp{Key: k, Modified: modified})
			}
		}
		return out, nil
	}
	examine := func(context.Context, string) (reconsweep.Outcome, error) { return reconsweep.Outcome{}, nil }
	st := reconsweep.SQLStore{Pool: app, Table: payoutSweeps}
	o := reconsweep.Options{Config: reconsweep.Config{Scope: scope, BatchSize: 2, MaxRecords: 2, MaxDuration: time.Minute}, Detector: payouts.SweepDetector, RunID: "it"}
	if _, err := reconsweep.Run(ctx(t), st, o, next, examine); err != nil {
		t.Fatal(err)
	}
	ok, err := verifyScope(ctx(t), app, scope, src)
	if err != nil || ok.Status != reconsweep.IntegrityVerified {
		t.Fatalf("verify: %s %v %+v", ok.Status, err, ok.Problems)
	}
	bad, _ := verifyScope(ctx(t), app, scope, failingSource)
	for i := 0; i < 5; i++ { // finish cycle 1 (6 keys, 2 per run, then the completing read)
		if _, err := reconsweep.Run(ctx(t), st, o, next, examine); err != nil {
			t.Fatal(err)
		}
	}
	var cycle int64
	if err := app.QueryRow(ctx(t), `SELECT cycle FROM app.payout_reconciliation_sweeps WHERE scope = $1 AND status = 'RUNNING'`, scope).Scan(&cycle); err != nil || cycle < 2 {
		t.Fatalf("cycle 1 did not complete: %d %v", cycle, err)
	}
	r := readIntegrity(t, app, scope)
	if r.status != "ERROR" || r.errCode != reconsweep.IntegrityErrSourceRead || !sameTime(r.verifiedAt, ok.CheckedAt) ||
		!sameTime(r.unverifiedSince, bad.CheckedAt) || r.failures != 1 || r.lastFailure != "ERROR:SOURCE_READ_FAILED" {
		t.Fatalf("the cycle boundary lost the integrity status: %+v", r)
	}
	// concurrent verifications, half of them failing: the outcome of the newest start is the one recorded
	var mu sync.Mutex
	var reps []reconsweep.IntegrityReport
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := reconsweep.Source(src)
			if i%2 == 1 {
				s = failingSource
			}
			rep, _ := verifyScope(ctx(t), app, scope, s)
			mu.Lock()
			reps = append(reps, rep)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	newest := reps[0]
	for _, rep := range reps {
		if rep.CheckedAt.After(newest.CheckedAt) {
			newest = rep
		}
	}
	r = readIntegrity(t, app, scope)
	if !sameTime(r.checkedAt, newest.CheckedAt) || r.status != newest.Status {
		t.Fatalf("concurrent checks: recorded %s at %v, newest %s at %v", r.status, r.checkedAt, newest.Status, newest.CheckedAt)
	}
	if r.status == "VERIFIED" && (r.unverifiedSince != nil || r.failures != 0 || !sameTime(r.verifiedAt, newest.CheckedAt)) {
		t.Fatalf("incoherent success: %+v", r)
	}
	if r.status != "VERIFIED" && (r.unverifiedSince == nil || r.failures < 1) {
		t.Fatalf("incoherent failure: %+v", r)
	}
	// a raw error text is refused; an inconsistent failure history is refused
	for name, q := range map[string]string{
		"error text":       `UPDATE app.payout_reconciliation_sweeps SET integrity_error = 'pq: connection refused' WHERE scope = $1 AND status = 'RUNNING'`,
		"failure w/o time": `UPDATE app.payout_reconciliation_sweeps SET integrity_last_failure = 'ERROR', integrity_last_failure_at = NULL WHERE scope = $1 AND status = 'RUNNING'`,
		"negative count":   `UPDATE app.payout_reconciliation_sweeps SET integrity_failures = -1 WHERE scope = $1 AND status = 'RUNNING'`,
		"unknown status":   `UPDATE app.payout_reconciliation_sweeps SET integrity_status = 'HEALTHY' WHERE scope = $1 AND status = 'RUNNING'`,
	} {
		_, err := app.Exec(ctx(t), q, scope)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23514" {
			t.Errorf("%s: want a check violation, got %v", name, err)
		}
	}
}
