//go:build integration

package integration

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payouts"
)

// KI-S18-25 (docs/stage-19/ki-s18-25-payout-reservation-race.md). A payout's completion commits first; its completion
// journal is then posted on the ledger pool and the reservation consumed in a separate transaction (syncLedger,
// ADR-043). These tests hold the payout's own reservation row so that whichever process completes the payout blocks
// exactly between "journal posted" and "reservation consumed", and run reconciliation, concurrent recovery and a
// connection loss inside that window.

type completionWindow struct {
	id, reservationID string
	lock              *rowLock
	stop              chan struct{}
}

// completionWindow requests, approves and submits a payout, locks its reservation and drives completion until the
// completer is blocked at the consumption write (payout COMPLETED, completion journal posted, reservation ACTIVE).
func (p *payoutIT) completionWindow(t *testing.T, camp string, amount int64) completionWindow {
	t.Helper()
	for try := 1; try <= 3; try++ {
		id := p.requestDirect(t, camp, amount)
		p.approve(t, id)
		if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
			t.Fatal(err)
		}
		p.journal(t, "payout:"+id+":submitted")
		var resID string
		waitFor(t, 30*time.Second, "submitted journal linked", func() bool {
			r := p.payout(t, id).Reservation
			if r != nil {
				resID = r.ID
			}
			return r != nil && r.LedgerSubmitTxnID != nil
		})
		lock := lockRow(t, p.mig, "app.payout_reservations", resID)
		var st, rs string
		if err := p.mig.QueryRow(ctx(t), `SELECT p.status, r.status FROM app.payout_requests p JOIN app.payout_reservations r
			ON r.payout_request_id = p.id WHERE p.id = $1`, id).Scan(&st, &rs); err != nil {
			t.Fatal(err)
		}
		if rs != payouts.ResActive {
			lock.release() // completed and consumed before the lock was taken: try a fresh payout
			t.Logf("attempt %d: payout settled before the barrier (%s/%s); retrying", try, st, rs)
			continue
		}
		w := completionWindow{id: id, reservationID: resID, lock: lock, stop: make(chan struct{})}
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { // this process drives completion too (CI runs a worker; locally the compose worker may win)
			defer wg.Done()
			for {
				select {
				case <-w.stop:
					return
				default:
				}
				c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				_, _ = p.s.d.Verification.Sandbox.DeliverDue(c, 50)
				_, _ = p.po.DispatchDue(c, 10)
				_, _ = p.po.PollDue(c, 10)
				cancel()
				time.Sleep(200 * time.Millisecond)
			}
		}()
		t.Cleanup(wg.Wait) // runs after the lock is released (cleanups are LIFO)
		t.Cleanup(func() { w.stopDriving() })
		t.Cleanup(lock.release)
		waitFor(t, 60*time.Second, "completer blocked between journal and consumption", func() bool {
			var st string
			var consumeLinked bool
			if err := p.mig.QueryRow(ctx(t), `SELECT p.status, r.ledger_consume_txn_id IS NOT NULL FROM app.payout_requests p
				JOIN app.payout_reservations r ON r.payout_request_id = p.id WHERE p.id = $1`, id).Scan(&st, &consumeLinked); err != nil {
				t.Fatal(err)
			}
			if consumeLinked {
				t.Fatal("the reservation was consumed through the barrier")
			}
			_, posted, _ := p.led.JournalByKey(ctx(t), "payout:"+id+":completed")
			return st == payouts.StCompleted && posted && len(lock.waiters("")) >= 1
		})
		w.stopDriving()
		return w
	}
	t.Fatal("could not hold a payout inside its completion window in 3 attempts")
	return completionWindow{}
}

func (w *completionWindow) stopDriving() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
}

func (p *payoutIT) eventCount(t *testing.T, id, typ string) int {
	t.Helper()
	var n int
	if err := p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_events WHERE payout_request_id = $1 AND event_type = $2`, id, typ).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (p *payoutIT) reconRefs(t *testing.T, id string) []string {
	t.Helper()
	rows, err := p.mig.Query(ctx(t), `SELECT mismatch_type FROM app.payout_reconciliation_references WHERE payout_request_id = $1 ORDER BY detected_at`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var m string
		_ = rows.Scan(&m)
		out = append(out, m)
	}
	return out
}

// reconService is the payout service on its own pool, so its backends are identifiable (application_name).
func (p *payoutIT) reconService(t *testing.T, appName string) *payouts.Service {
	rc := *p.po
	rc.Pool = namedPool(t, os.Getenv("DATABASE_URL"), appName)
	return &rc
}

// Reconciliation inside the completion window must not report the in-flight transition as a mismatch, concurrent
// recovery must consume the reservation once and record it once, and no journal is duplicated.
func TestFinancialPayoutCompletionWindowVersusReconciliation(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	w := p.completionWindow(t, camp, 5_000)

	const reconApp = "fundzim-it-s19-recon"
	rc := p.reconService(t, reconApp)
	type result struct {
		rep payouts.ReconReport
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		rep, err := rc.Reconcile(c, "it-s19-window")
		done <- result{rep, err}
	}()
	// four concurrent recoveries of the same payout (the recovery job of several workers)
	const recApp = "fundzim-it-s19-recover"
	rec := p.reconService(t, recApp)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = rec.Recover(c)
		}()
	}
	var res result
	finished := false
	waitFor(t, 60*time.Second, "reconciliation finishes or waits for the transition", func() bool {
		select {
		case res = <-done:
			finished = true
			return true
		default:
		}
		return len(w.lock.waiters(reconApp)) >= 1
	})
	waitFor(t, 30*time.Second, "recoveries reach the barrier", func() bool { return len(w.lock.waiters(recApp)) >= 1 })
	if finished {
		t.Logf("reconciliation finished inside the window: %+v", res.rep)
	}
	w.lock.release()
	if !finished {
		res = <-done
	}
	wg.Wait()
	if res.err != nil {
		t.Fatal(res.err)
	}
	if refs := p.reconRefs(t, w.id); len(refs) != 0 {
		t.Fatalf("the completion window was reported as a mismatch: %v (KI-S18-25)", refs)
	}
	waitFor(t, 30*time.Second, "reservation consumed", func() bool { return p.payout(t, w.id).Reservation.Status == payouts.ResConsumed })
	if n := p.eventCount(t, w.id, "RESERVATION_CONSUMED"); n != 1 {
		t.Fatalf("RESERVATION_CONSUMED recorded %d times", n)
	}
	if n := p.eventCount(t, w.id, "LEDGER_POSTED"); n != 1 { // the submitted journal's link
		t.Fatalf("LEDGER_POSTED recorded %d times", n)
	}
	var journals int
	if err := p.mig.QueryRow(ctx(t), `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key LIKE $1`, "payout:"+w.id+":%").Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 3 { // reserved, submitted, completed
		t.Fatalf("%d journals for the payout, want 3", journals)
	}
	// a later run over the settled payout: still nothing
	if _, err := p.po.Reconcile(ctx(t), "it-s19-after"); err != nil {
		t.Fatal(err)
	}
	if refs := p.reconRefs(t, w.id); len(refs) != 0 {
		t.Fatalf("mismatch after settlement: %v", refs)
	}
	p.integrity(t)
}

// Connection loss at the boundary: the completer's consumption write dies with its connection (process crash or
// database failover between the two commits). The payout stays COMPLETED with its journal posted and the
// reservation ACTIVE until recovery; recovery consumes it exactly once; reconciliation reports nothing once it has.
func TestFinancialPayoutConnectionLostBetweenJournalAndConsumption(t *testing.T) {
	p := newPayoutIT(t)
	su := superuser(t)
	camp := p.campaign(t, 20_000)
	w := p.completionWindow(t, camp, 5_000)
	victims := w.lock.waiters("")
	terminate(t, su, victims)
	waitFor(t, 15*time.Second, "terminated backends gone", func() bool {
		for _, v := range w.lock.waiters("") {
			for _, x := range victims {
				if v == x {
					return false
				}
			}
		}
		return true
	})
	w.lock.release()
	// stranded (or already re-driven by a worker's recovery job): never a second journal, never double consumption
	got := p.payout(t, w.id)
	if got.Status != payouts.StCompleted {
		t.Fatalf("status after connection loss: %s", got.Status)
	}
	t.Logf("after the fault: reservation %s", got.Reservation.Status)
	if err := p.po.Recover(ctx(t)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "recovery consumes the reservation", func() bool {
		_ = p.po.Recover(ctx(t))
		return p.payout(t, w.id).Reservation.Status == payouts.ResConsumed
	})
	if n := p.eventCount(t, w.id, "RESERVATION_CONSUMED"); n != 1 {
		t.Fatalf("RESERVATION_CONSUMED recorded %d times", n)
	}
	var journals int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key = $1`, "payout:"+w.id+":completed").Scan(&journals)
	if journals != 1 {
		t.Fatalf("%d completion journals", journals)
	}
	if _, err := p.po.Reconcile(ctx(t), "it-s19-connloss"); err != nil {
		t.Fatal(err)
	}
	if refs := p.reconRefs(t, w.id); len(refs) != 0 {
		t.Fatalf("mismatch after recovery: %v", refs)
	}
	p.integrity(t)
}

// A genuinely stuck payout stays detectable: the ledger refuses the in-transit journal its submission needs (the
// campaign's payout-pending account is frozen). The provider is never instructed (the database refuses CREATE_PAYOUT
// without that journal); reconciliation tries the same recovery, fails, and reports it (SEV1, carrying the ledger's
// refusal); after the account is unfrozen recovery posts the journal and the payout completes and settles once.
func TestFinancialPayoutLedgerRefusalIsStillReported(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.requestDirect(t, camp, 4_000)
	p.approve(t, id)
	p.journal(t, "payout:"+id+":reserved")
	var accountID string
	if err := p.mig.QueryRow(ctx(t), `SELECT id FROM ledger.ledger_accounts WHERE code = $1`,
		ledger.ClassCampaignPayoutPend+":"+camp).Scan(&accountID); err != nil {
		t.Fatalf("pending account: %v", err)
	}
	if err := p.led.SetAccountStatus(ctx(t), p.f.finance1.id, accountID, "FROZEN", "stage 19: ledger refuses the payout journals"); err != nil {
		t.Fatal(err)
	}
	unfrozen := false
	unfreeze := func() {
		if !unfrozen {
			unfrozen = true
			if err := p.led.SetAccountStatus(context.Background(), p.f.finance1.id, accountID, "ACTIVE", "stage 19: unfreeze after the test"); err != nil {
				t.Errorf("unfreeze: %v", err)
			}
		}
	}
	t.Cleanup(unfreeze)
	// the state moves first (SUBMITTED); the refused journal opens LEDGER_POSTING_FAILED and the direct dispatch is
	// refused by the database (no CREATE_PAYOUT without the in-transit journal)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Logf("submit: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := p.po.DispatchDue(ctx(t), 50); err != nil {
			t.Fatal(err)
		}
	}
	got := p.payout(t, id)
	if got.Status != payouts.StSubmitted || got.Reservation.LedgerSubmitTxnID != nil {
		t.Fatalf("state after the refusal: %s %+v", got.Status, got.Reservation)
	}
	var creates int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&creates)
	if creates != 0 {
		t.Fatalf("the provider was instructed without the in-transit journal (%d attempts)", creates)
	}
	exc := map[string]int{}
	rows, err := p.mig.Query(ctx(t), `SELECT exception_type FROM app.payout_exceptions WHERE payout_request_id = $1`, id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var x string
		_ = rows.Scan(&x)
		exc[x]++
	}
	rows.Close()
	if exc["LEDGER_POSTING_FAILED"] != 1 || exc["DUPLICATE_SUBMISSION_ATTEMPT"] != 0 {
		t.Fatalf("exceptions %v: the refusal must be a ledger posting failure, not a duplicate submission", exc)
	}
	if _, err := p.po.Reconcile(ctx(t), "it-s19-refusal"); err != nil {
		t.Fatal(err)
	}
	refs := p.reconRefs(t, id)
	if len(refs) == 0 || refs[0] != "LEDGER_MISMATCH" {
		t.Fatalf("a genuinely stuck payout was not reported: %v", refs)
	}
	var sev string
	var missing, syncState, code *string
	if err := p.mig.QueryRow(ctx(t), `SELECT e.severity, r.detail->>'missing_journal', r.detail->>'ledger_sync', r.detail->>'error_code'
		FROM app.payout_exceptions e JOIN app.payout_reconciliation_references r ON r.id = e.reconciliation_reference_id
		WHERE e.payout_request_id = $1 AND e.exception_type = 'RECONCILIATION_MISMATCH'`, id).Scan(&sev, &missing, &syncState, &code); err != nil {
		t.Fatal(err)
	}
	if sev != "SEV1" || missing == nil || *missing != "submitted" {
		t.Fatalf("finding %s missing=%v", sev, missing)
	}
	if syncState == nil || *syncState != "FAILED" || code == nil || *code != "LEDGER_ACCOUNT_FROZEN" {
		t.Fatalf("finding does not carry the ledger refusal: %v %v", syncState, code)
	}
	n := len(refs)
	if _, err := p.po.Reconcile(ctx(t), "it-s19-refusal-2"); err != nil {
		t.Fatal(err)
	}
	if m := len(p.reconRefs(t, id)); m != n {
		t.Fatalf("rerun while under investigation added references: %d → %d", n, m)
	}
	unfreeze()
	_ = p.po.Recover(ctx(t))
	p.drive(t, 60*time.Second, "completed after the unfreeze", status(payouts.StCompleted), id)
	waitFor(t, 30*time.Second, "reservation consumed", func() bool {
		_ = p.po.Recover(ctx(t))
		return p.payout(t, id).Reservation.Status == payouts.ResConsumed
	})
	if n := p.eventCount(t, id, "RESERVATION_CONSUMED"); n != 1 {
		t.Fatalf("RESERVATION_CONSUMED recorded %d times", n)
	}
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'
		AND classification = 'ACCEPTED'`, id).Scan(&creates)
	if creates != 1 {
		t.Fatalf("%d accepted CREATE_PAYOUT attempts", creates)
	}
	p.integrity(t)
}

// The database refuses reservation states that contradict the payout model, whoever writes them (here the schema
// owner): consumption without journals, consumed → released, a second reservation for one payout, one reservation
// moved to another payout.
func TestFinancialReservationInvariantsInTheDatabase(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	a := p.requestDirect(t, camp, 3_000)
	b := p.requestDirect(t, p.campaign(t, 20_000), 3_000) // one payout in progress per campaign and currency
	p.journal(t, "payout:"+a+":reserved")
	p.journal(t, "payout:"+b+":reserved")
	ra, rb := p.payout(t, a).Reservation, p.payout(t, b).Reservation
	refuse := func(what, q string, args ...any) {
		t.Helper()
		if _, err := p.mig.Exec(ctx(t), q, args...); err == nil {
			t.Fatalf("%s was accepted", what)
		} else {
			t.Logf("%s refused: %s", what, ecode(err)+" "+err.Error())
		}
	}
	refuse("CONSUMED without consumption journal", `UPDATE app.payout_reservations SET status = 'CONSUMED' WHERE id = $1`, ra.ID)
	refuse("second reservation for one payout", `INSERT INTO app.payout_reservations (id, payout_request_id, campaign_id, currency, amount_minor,
		account_scope, status, ledger_reserve_txn_id) SELECT gen_random_uuid(), payout_request_id, campaign_id, currency, amount_minor, account_scope,
		'ACTIVE', ledger_reserve_txn_id FROM app.payout_reservations WHERE id = $1`, ra.ID)
	refuse("reservation moved to another payout", `UPDATE app.payout_reservations SET payout_request_id = $2 WHERE id = $1`, ra.ID, b)
	refuse("reservation shares another's reserve journal", `UPDATE app.payout_reservations SET ledger_reserve_txn_id = $2 WHERE id = $1`,
		rb.ID, ra.LedgerReserveTxnID)
	// cancel b: its reservation is released, and a released reservation never becomes consumable again
	if _, err := p.po.CancelByOwner(ctx(t), p.owner.id, true, b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "b released", func() bool {
		_ = p.po.Recover(ctx(t))
		return p.payout(t, b).Reservation.Status == payouts.ResReleased
	})
	refuse("RELEASED → CONSUMED", `UPDATE app.payout_reservations SET status = 'CONSUMED', ledger_consume_txn_id = ledger_release_txn_id,
		ledger_release_txn_id = NULL WHERE id = $1`, rb.ID)
	refuse("RELEASED → ACTIVE", `UPDATE app.payout_reservations SET status = 'ACTIVE', ledger_release_txn_id = NULL WHERE id = $1`, rb.ID)
	if st := p.payout(t, a).Reservation.Status; st != payouts.ResActive {
		t.Fatalf("a's reservation changed: %s", st)
	}
	p.integrity(t)
}
