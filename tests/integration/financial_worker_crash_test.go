//go:build integration

package integration

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Stage 19 fault injection (docs/stage-19/fault-injection.md, F-02/F-06/F-07): the real worker process is killed
// while it is submitting payouts and processing payments, then restarted; every financial effect must still happen at
// most once and every state must stay explainable. Opt-in (it kills a process of the local stack):
//
//	FUNDZIM_IT_WORKER_KILL_CMD='docker compose kill fundzim-worker' FUNDZIM_IT_WORKER_START_CMD='docker compose up -d fundzim-worker'
//
// Only this test's own payouts and payments are asserted on; the stack is the local disposable one.
func TestFinancialWorkerCrashMidFlight(t *testing.T) {
	kill, start := os.Getenv("FUNDZIM_IT_WORKER_KILL_CMD"), os.Getenv("FUNDZIM_IT_WORKER_START_CMD")
	if kill == "" || start == "" {
		t.Skip("opt-in: set FUNDZIM_IT_WORKER_KILL_CMD and FUNDZIM_IT_WORKER_START_CMD (local disposable stack only)")
	}
	run := func(cmd string) {
		t.Helper()
		f := strings.Fields(cmd)
		out, err := exec.Command(f[0], f[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", cmd, err, out)
		}
	}
	p := newPayoutIT(t)
	pay := newPayIT(t)
	// payouts approved and moved to SUBMITTED here; the worker sends the provider instructions (DispatchDue)
	var pids []string
	for i := 0; i < 6; i++ {
		camp := p.campaign(t, 20_000)
		id := p.requestDirect(t, camp, int64(2_000+100*i)) // suffix 00: success
		p.approve(t, id)
		if ok, err := p.po.SubmitApproved(ctx(t), id); err != nil || !ok {
			t.Fatalf("submit %s: %v %v", id, ok, err)
		}
		pids = append(pids, id)
	}
	// donations whose webhooks the worker delivers and applies
	var payIDs []string
	for i := 0; i < 6; i++ {
		di := pay.intent(t, pay.donor, "1006") // suffix 06: duplicate webhooks
		payIDs = append(payIDs, pay.attempt(t, pay.donor, di, "ECOCASH", 201, "").ID)
	}
	// kill the worker as soon as it is mid-flight on at least one payout
	waitFor(t, 60*time.Second, "worker dispatching", func() bool {
		var n int
		_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = ANY($1::uuid[]) AND operation = 'CREATE_PAYOUT'`,
			pids).Scan(&n)
		return n >= 1
	})
	run(kill)
	t.Logf("worker killed")
	t.Cleanup(func() { _ = exec.Command(strings.Fields(start)[0], strings.Fields(start)[1:]...).Run() })
	time.Sleep(3 * time.Second) // the outage itself (not an assertion wait)
	run(start)
	t.Logf("worker restarted")
	// everything settles through the restarted worker (recovery, dispatch, polling, webhooks); this process only reads
	waitFor(t, 5*time.Minute, "payouts settled", func() bool {
		for _, id := range pids {
			got := p.payout(t, id)
			if got.Status != payouts.StCompleted || got.Reservation.Status != payouts.ResConsumed {
				return false
			}
		}
		return true
	})
	waitFor(t, 3*time.Minute, "payments settled", func() bool {
		for _, id := range payIDs {
			if pay.status(t, id) != psp.StatusSucceeded {
				return false
			}
		}
		return true
	})
	for _, id := range pids {
		var accepted, refs int
		_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FILTER (WHERE classification = 'ACCEPTED'), count(DISTINCT provider_reference_returned)
			FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&accepted, &refs)
		if accepted > 1 || refs > 1 {
			t.Fatalf("payout %s: %d accepted creates, %d provider references", id, accepted, refs)
		}
		if n := p.eventCount(t, id, "RESERVATION_CONSUMED"); n != 1 {
			t.Fatalf("payout %s: RESERVATION_CONSUMED %d times", id, n)
		}
	}
	for _, id := range payIDs {
		if n := pay.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND to_status = 'SUCCEEDED' AND applied`, id); n != 1 {
			t.Fatalf("payment %s: SUCCEEDED applied %d times", id, n)
		}
		if n := pay.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded' AND aggregate_id = $1`, id); n != 1 {
			t.Fatalf("payment %s: payment_succeeded %d times", id, n)
		}
	}
	if _, err := p.po.Reconcile(ctx(t), "it-s19-crash"); err != nil {
		t.Fatal(err)
	}
	for _, id := range pids {
		if refs := p.reconRefs(t, id); len(refs) != 0 {
			t.Fatalf("payout %s mismatches after the crash: %v", id, refs)
		}
	}
	p.integrity(t)
}
