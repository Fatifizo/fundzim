//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/payouts"
)

// Remediation phase 4 (KI-R1-07). The compose worker submits every APPROVED payout it finds (SubmitDue every few
// seconds): SubmitApproved evaluates eligibility, then — in one transaction, holding the payout row — records the
// pre-submission decision in app.payout_eligibility_decisions and only then moves the payout to SUBMITTED. A test that
// needs an approved payout to stay APPROVED while it acts on it (suspend its destination, re-approve, cancel) races that
// worker. decisionsBarrier removes the race without pausing the worker or changing any rule:
//
//   - it holds SHARE on app.payout_eligibility_decisions in an open transaction of the test, so no eligibility decision
//     can be inserted while it is held — every submission attempt blocks at its decision insert, before the move to
//     SUBMITTED (approval, suspension, reinstatement and cancellation write no decision and are not blocked);
//   - a submission attempt blocked there holds the payout row, so the test terminates exactly those backends: the
//     direct waiters of its own lock whose statement is that decision insert (pg_blocking_pids, never a sleep). Their
//     transactions roll back — nothing of the attempt is committed — and the worker re-evaluates the payout later in its
//     current state.
//
// Every intercepted attempt is counted and logged.
type decisionsBarrier struct {
	t           *testing.T
	tx          pgx.Tx
	pid         int
	su          *pgxpool.Pool
	intercepted int
}

func holdEligibilityDecisions(t *testing.T, mig *pgxpool.Pool) *decisionsBarrier {
	t.Helper()
	su := superuser(t)
	tx, err := mig.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b := &decisionsBarrier{t: t, tx: tx, su: su}
	if err := tx.QueryRow(ctx(t), `SELECT pg_backend_pid()`).Scan(&b.pid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx(t), `LOCK TABLE app.payout_eligibility_decisions IN SHARE MODE`); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("decisions barrier: %v", err)
	}
	t.Cleanup(b.release)
	return b
}

// blocked lists the backends waiting directly on the barrier with an eligibility-decision insert. It observes through
// the superuser connection: pg_stat_activity hides the query text of other roles' sessions from fundzim_migrator.
func (b *decisionsBarrier) blocked() ([]int, error) {
	rows, err := b.su.Query(context.Background(), `SELECT pid FROM pg_stat_activity WHERE $1 = ANY (pg_blocking_pids(pid))
		AND query ILIKE 'INSERT INTO app.payout_eligibility_decisions%'`, b.pid)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}

// do runs fn (an action of the test on the payout, on the test goroutine) while a watcher removes any submission
// attempt that blocks at the barrier — and so would hold the payout row fn needs.
func (b *decisionsBarrier) do(what string, fn func() error) {
	b.t.Helper()
	stop, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
			pids, err := b.blocked()
			if err != nil {
				b.t.Errorf("decisions barrier: %v", err)
				return
			}
			for _, pid := range pids {
				var ok bool
				if err := b.su.QueryRow(context.Background(), `SELECT pg_terminate_backend($1, 5000)`, pid).Scan(&ok); err != nil || !ok {
					b.t.Errorf("decisions barrier: terminate %d: %v %v", pid, ok, err)
					return
				}
				b.intercepted++
				b.t.Logf("decisions barrier: %s — terminated a submission attempt blocked at the decision insert (pid %d)", what, pid)
			}
		}
	}()
	err := fn()
	close(stop)
	<-finished
	if err != nil {
		b.t.Fatalf("%s: %v", what, err)
	}
}

func (b *decisionsBarrier) release() {
	if b.tx != nil {
		_ = b.tx.Rollback(context.Background())
		b.tx = nil
	}
}

// KI-R1-07 reproduction, deterministic: a submission that commits between the approval and the destination suspension
// leaves the payout SUBMITTED in approval round 1 — the exact CI observation ("after destination suspension: SUBMITTED
// round 1"). This is correct behaviour (a payout submitted before the suspension committed is not taken back), so a
// test that expects APPROVALS_INVALIDATED must keep the worker from submitting inside that window.
func TestPayoutSubmissionCommittedBeforeDestinationSuspension(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.request(t, camp, 5_000, 202, "")
	p.approve(t, id)
	// the test plays the worker inside the window (the compose worker may also do it first: the outcome is the same)
	if _, err := p.po.SubmitApproved(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	if err := p.po.SetSuspended(ctx(t), p.dest, p.f.rev2.id, "SUSPECTED_TAKEOVER", true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.po.SetSuspended(context.Background(), p.dest, p.f.rev2.id, "REVIEW_CLEARED", false) })
	po := p.payout(t, id)
	if (po.Status != payouts.StSubmitted && po.Status != payouts.StProcessing && po.Status != payouts.StCompleted) || po.ApprovalRound != 1 {
		t.Fatalf("a payout submitted before the suspension: %s round %d (it must stay submitted, round 1)", po.Status, po.ApprovalRound)
	}
	var invalidated int
	if err := p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_events WHERE payout_request_id = $1 AND event_type = 'APPROVALS_INVALIDATED'`,
		id).Scan(&invalidated); err != nil || invalidated != 0 {
		t.Fatalf("approvals of a submitted payout were invalidated: %d %v", invalidated, err)
	}
	if err := p.po.SetSuspended(ctx(t), p.dest, p.f.rev2.id, "REVIEW_CLEARED", false); err != nil {
		t.Fatal(err)
	}
}

// KI-R1-07, the barrier itself, deterministic: a submission attempt that starts inside the window (played by the test,
// so it is certain to happen) blocks at its decision insert, is removed when the test suspends the destination, commits
// nothing, and the suspension invalidates the approval (REVIEW, round 2). Nothing is submitted.
func TestPayoutDecisionsBarrierStopsASubmissionInTheWindow(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.request(t, camp, 5_000, 202, "")
	bar := holdEligibilityDecisions(t, p.mig)
	bar.do("approve", func() error { p.approve(t, id); return nil })
	submit := make(chan error, 1)
	go func() { _, err := p.po.SubmitApproved(context.Background(), id); submit <- err }()
	deadline := time.Now().Add(time.Minute)
	for {
		pids, err := bar.blocked()
		if err != nil {
			t.Fatal(err)
		}
		if len(pids) > 0 {
			break // the attempt holds the payout row and waits at its decision insert
		}
		if time.Now().After(deadline) {
			t.Fatal("the submission attempt never reached its decision insert")
		}
		time.Sleep(20 * time.Millisecond)
	}
	bar.do("suspend the destination", func() error {
		return p.po.SetSuspended(ctx(t), p.dest, p.f.rev2.id, "SUSPECTED_TAKEOVER", true)
	})
	t.Cleanup(func() { _ = p.po.SetSuspended(context.Background(), p.dest, p.f.rev2.id, "REVIEW_CLEARED", false) })
	if err := <-submit; err == nil {
		t.Fatal("the intercepted submission reported success")
	}
	if bar.intercepted == 0 {
		t.Fatal("no submission attempt was intercepted")
	}
	po := p.payout(t, id)
	if po.Status != payouts.StReview || po.ApprovalRound != 2 {
		t.Fatalf("after the intercepted submission and the suspension: %s round %d", po.Status, po.ApprovalRound)
	}
	bar.do("reinstate the destination", func() error {
		return p.po.SetSuspended(ctx(t), p.dest, p.f.rev2.id, "REVIEW_CLEARED", false)
	})
	bar.do("cancel", func() error {
		_, err := p.po.CancelByStaff(ctx(t), p.f.finance1.id, id, "test cleanup: cancel before submission")
		return err
	})
	bar.release()
	var submitted, creates, decisions int
	if err := p.mig.QueryRow(ctx(t), `SELECT (SELECT count(*) FROM app.payout_events WHERE payout_request_id = $1 AND to_status = 'SUBMITTED'),
		(SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'),
		(SELECT count(*) FROM app.payout_eligibility_decisions WHERE payout_request_id = $1 AND phase = $2)`, id, payouts.PhasePreSubmit).
		Scan(&submitted, &creates, &decisions); err != nil {
		t.Fatal(err)
	}
	if submitted != 0 || creates != 0 || decisions != 0 {
		t.Fatalf("the intercepted attempt left traces: %d SUBMITTED events, %d CREATE_PAYOUT attempts, %d pre-submission decisions",
			submitted, creates, decisions)
	}
}
