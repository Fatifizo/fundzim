//go:build integration

package integration

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/payouts"
)

// Stage 19 payout concurrency (docs/stage-19/concurrency-test-plan.md §5). Every payout is a TEST payout through the
// simulator; approvals are real FINANCE maker-checker decisions over HTTP.

// Two checkers approving concurrently, each several times: exactly two distinct approvals, one transition to
// APPROVED, and the same checker never counts twice.
func TestFinancialPayoutConcurrentApprovals(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.requestDirect(t, camp, 2_000)
	p.journal(t, "payout:"+id+":reserved")
	// step both checkers up first so that the concurrent calls are pure decisions
	for _, c := range []staffCred{p.f.finance1, p.f.finance2} {
		b := p.s.staff(t, c)
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, c.secret)}), 200, "")
	}
	var wg sync.WaitGroup
	var ok, refused atomic.Int32
	for i := 0; i < 6; i++ {
		for _, c := range []staffCred{p.f.finance1, p.f.finance2} {
			wg.Add(1)
			go func(c staffCred) {
				defer wg.Done()
				b := p.s.staff(t, c)
				r := b.do("POST", "/admin/payout-requests/"+id+"/approve", approveBody("concurrent approval under test"))
				switch {
				case r.Status == 200:
					ok.Add(1)
				case r.Status >= 400 && r.Status < 500:
					refused.Add(1)
				default:
					t.Errorf("approval: %d %s", r.Status, r.Error.Code)
				}
			}(c)
		}
	}
	wg.Wait()
	got := p.payout(t, id)
	if got.Status != payouts.StApproved {
		t.Fatalf("status after concurrent approvals: %s (%d ok, %d refused)", got.Status, ok.Load(), refused.Load())
	}
	var approvals, approvers, transitions int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*), count(DISTINCT approver_id) FROM app.payout_approvals WHERE payout_request_id = $1
		AND decision = 'APPROVE'`, id).Scan(&approvals, &approvers)
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_events WHERE payout_request_id = $1 AND to_status = 'APPROVED' AND applied`,
		id).Scan(&transitions)
	t.Logf("12 concurrent approval calls: %d accepted, %d refused; %d approvals by %d checkers; %d transitions", ok.Load(), refused.Load(),
		approvals, approvers, transitions)
	if approvals != 2 || approvers != 2 || transitions != 1 {
		t.Fatalf("approvals %d approvers %d transitions %d", approvals, approvers, transitions)
	}
}

// Cancellation racing submission: exactly one wins. A cancelled payout has a released reservation and never reached
// the provider; a submitted one was not cancelled. Never both, and the ledger agrees.
func TestFinancialPayoutCancelRacesSubmission(t *testing.T) {
	p := newPayoutIT(t)
	for round := 0; round < 3; round++ {
		camp := p.campaign(t, 20_000)
		id := p.requestDirect(t, camp, 2_000)
		p.approve(t, id)
		var wg sync.WaitGroup
		var cancelled, submitted atomic.Int32
		for i := 0; i < 4; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				if _, err := p.po.CancelByStaff(context.Background(), p.f.finance1.id, id, "stage 19 cancellation race"); err == nil {
					cancelled.Add(1)
				}
			}()
			go func() {
				defer wg.Done()
				if ok, err := p.po.SubmitNow(context.Background(), id); err == nil && ok {
					submitted.Add(1)
				}
			}()
		}
		wg.Wait()
		got := p.payout(t, id)
		var creates int
		_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&creates)
		t.Logf("round %d: status %s, %d cancels and %d submissions succeeded, %d CREATE_PAYOUT attempts", round, got.Status, cancelled.Load(),
			submitted.Load(), creates)
		switch got.Status {
		case payouts.StCancelled:
			if submitted.Load() != 0 || creates != 0 {
				t.Fatalf("cancelled payout was submitted (%d) or sent (%d)", submitted.Load(), creates)
			}
			waitFor(t, 30*time.Second, "released", func() bool {
				_ = p.po.Recover(ctx(t))
				return p.payout(t, id).Reservation.Status == payouts.ResReleased
			})
		default:
			if cancelled.Load() != 0 {
				t.Fatalf("status %s although a cancellation succeeded", got.Status)
			}
			if got.SubmittedAt == nil {
				t.Fatalf("neither cancelled nor submitted: %s", got.Status)
			}
			p.drive(t, 60*time.Second, "completed", status(payouts.StCompleted), id)
			if creates > 1 {
				t.Fatalf("%d CREATE_PAYOUT attempts", creates)
			}
		}
	}
	p.integrity(t)
}

// Concurrent reconciliation runs over one genuine mismatch record it once: one reference set, one exception.
func TestFinancialPayoutConcurrentReconciliationRunsRecordOnce(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.requestDirect(t, camp, 6_000)
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	p.drive(t, 60*time.Second, "completed", status(payouts.StCompleted), id)
	waitFor(t, 30*time.Second, "consumed", func() bool {
		_ = p.po.Recover(ctx(t))
		return p.payout(t, id).Reservation.Status == payouts.ResConsumed
	})
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.sandbox_provider_payouts SET raw_status = 'failed' WHERE our_reference = $1`, id); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if _, err := p.po.Reconcile(c, "it-s19-concurrent"); err != nil {
				t.Errorf("reconcile: %v", err)
			}
		}()
	}
	wg.Wait()
	refs := p.reconRefs(t, id)
	var exc int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_exceptions WHERE payout_request_id = $1 AND exception_type = 'RECONCILIATION_MISMATCH'`, id).Scan(&exc)
	t.Logf("6 concurrent runs: references %v, exceptions %d", refs, exc)
	if len(refs) != 1 || refs[0] != "PROVIDER_STATUS_MISMATCH" || exc != 1 {
		t.Fatalf("references %v exceptions %d", refs, exc)
	}
}

// Authorization under concurrency: another verified user hammering the owner's payout and campaign gets only refusals,
// and nothing changes.
func TestFinancialPayoutCrossTenantUnderConcurrency(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.requestDirect(t, camp, 2_000)
	other := verifiedUser(t, p.s, "s19intruder")
	other.expect(other.do("POST", "/auth/step-up/verify", map[string]string{"password": itPassword}), 200, "")
	var wg sync.WaitGroup
	var leaked atomic.Int32
	try := func(method, path string, body any, hdr ...string) {
		defer wg.Done()
		r := other.do(method, path, body, hdr...)
		if r.Status < 400 {
			leaked.Add(1)
			t.Errorf("%s %s by another user: %d", method, path, r.Status)
		}
	}
	for i := 0; i < 10; i++ {
		wg.Add(5)
		go try("GET", "/payout-requests/"+id, nil)
		go try("GET", "/payout-requests/"+id+"/timeline", nil)
		go try("POST", "/payout-requests/"+id+"/cancel", nil)
		go try("GET", "/campaigns/"+camp+"/payout-requests", nil)
		go try("POST", "/campaigns/"+camp+"/payout-requests", map[string]any{"currency": "USD", "amount_minor": "100", "destination_id": p.dest},
			"Idempotency-Key", idemKey())
	}
	wg.Wait()
	if leaked.Load() != 0 {
		t.Fatalf("%d requests by another user succeeded", leaked.Load())
	}
	if st := p.payout(t, id).Status; st == payouts.StCancelled {
		t.Fatal("another user cancelled the payout")
	}
	var n int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_requests WHERE campaign_id = $1`, camp).Scan(&n)
	if n != 1 {
		t.Fatalf("%d payout requests on the campaign", n)
	}
}
