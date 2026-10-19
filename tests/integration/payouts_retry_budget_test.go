//go:build integration

package integration

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/payouts"
)

// KI-S15-19: automatic same-reference resubmission after DEFINITELY_NOT_SENT outcomes is bounded. The simulator answers
// amount suffix 09 with "provider unavailable, nothing sent" forever, which used to be retried every 10 minutes without
// end. These tests share the development database with the compose worker, which runs the same dispatcher: assertions
// hold whichever process makes an attempt.

func (p *payoutIT) createAttempts(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (p *payoutIT) openExhaustion(t *testing.T, id string) (string, map[string]any) {
	t.Helper()
	var exc string
	var ev map[string]any
	err := p.mig.QueryRow(ctx(t), `SELECT id::text, evidence FROM app.payout_exceptions WHERE payout_request_id = $1
		AND exception_type = 'SUBMISSION_RETRIES_EXHAUSTED' AND status <> 'RESOLVED'`, id).Scan(&exc, &ev)
	if err != nil {
		return "", nil
	}
	return exc, ev
}

// makeDue moves the payout's retry time into the past (the time a worker would otherwise wait).
func (p *payoutIT) makeDue(t *testing.T, id string) {
	t.Helper()
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.payout_requests SET next_poll_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

// dispatchUntilHalted runs four concurrent dispatchers per round until the exhaustion exception is open.
func (p *payoutIT) dispatchUntilHalted(t *testing.T, svc *payouts.Service, id string) string {
	t.Helper()
	for round := 0; round < 40; round++ {
		if exc, _ := p.openExhaustion(t, id); exc != "" {
			return exc
		}
		p.makeDue(t, id)
		var wg sync.WaitGroup
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = svc.DispatchDue(ctx(t), 50)
			}()
		}
		wg.Wait()
	}
	t.Fatal("the retry budget never halted automatic submission")
	return ""
}

func TestPayoutSubmitRetryBudgetHaltsAndResumes(t *testing.T) {
	p := newPayoutIT(t)
	p.po.SubmitRetryBase, p.po.SubmitRetryMax = time.Millisecond, 5*time.Millisecond // the budget (default 5) is unchanged
	camp := p.campaign(t, 5_000)
	id := p.requestDirect(t, camp, 1_009)
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	before := p.payout(t, id)
	if before.Status != payouts.StSubmitted || before.Reservation.LedgerSubmitTxnID == nil {
		t.Fatalf("after the first refusal: %s %+v", before.Status, before.Reservation)
	}

	// concurrent workers: exactly the budget of attempts, never more, then an operational exception
	exc := p.dispatchUntilHalted(t, p.po, id)
	if n := p.createAttempts(t, id); n != payouts.DefaultSubmitRetryBudget {
		t.Fatalf("CREATE_PAYOUT attempts %d, want exactly the budget %d", n, payouts.DefaultSubmitRetryBudget)
	}
	var notSent int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND classification <> 'DEFINITELY_NOT_SENT'`, id).Scan(&notSent)
	if notSent != 0 {
		t.Fatalf("%d attempts that may have reached the provider", notSent)
	}
	var sev, src string
	_ = p.mig.QueryRow(ctx(t), `SELECT severity, source FROM app.payout_exceptions WHERE id = $1`, exc).Scan(&sev, &src)
	if sev != "SEV2" || src != "ORCHESTRATION" {
		t.Fatalf("exception %s %s", sev, src)
	}

	// money untouched: still SUBMITTED, same reservation and in-transit journal, nothing released or consumed
	after := p.payout(t, id)
	if after.Status != payouts.StSubmitted || after.Reservation.Status != before.Reservation.Status ||
		after.Reservation.AmountMinor != before.Reservation.AmountMinor || *after.Reservation.LedgerSubmitTxnID != *before.Reservation.LedgerSubmitTxnID ||
		after.Reservation.LedgerReleaseTxnID != nil || after.Reservation.LedgerConsumeTxnID != nil {
		t.Fatalf("halting changed the payout or its reservation: %s %+v", after.Status, after.Reservation)
	}

	// halted: more dispatch rounds (and a restarted worker: a fresh service) make no attempt; the database refuses one
	for i := 0; i < 3; i++ {
		p.makeDue(t, id)
		_, _ = p.po.DispatchDue(ctx(t), 50)
	}
	restarted := *p.po
	p.makeDue(t, id)
	if _, err := restarted.DispatchDue(ctx(t), 50); err != nil {
		t.Fatal(err)
	}
	if n := p.createAttempts(t, id); n != payouts.DefaultSubmitRetryBudget {
		t.Fatalf("attempts after halting: %d", n)
	}
	_, err := p.app.Exec(ctx(t), `INSERT INTO app.payout_attempts (id, payout_request_id, attempt_no, operation, provider_id, our_reference,
		correlation_id, started_at) SELECT gen_random_uuid(), id, 999, 'CREATE_PAYOUT', provider_id, id, 'it', now() FROM app.payout_requests WHERE id = $1`, id)
	if err == nil || !strings.Contains(err.Error(), "halted") {
		t.Fatalf("database accepted a CREATE_PAYOUT while halted: %v", err)
	}

	// an operator resolves the exception (audited): a fresh budget, then halted again
	if err := p.po.ResolveException(ctx(t), p.f.finance1.id, exc, "OTHER", "provider restored in TEST; retry with a fresh budget"); err != nil {
		t.Fatal(err)
	}
	if n := countAudit(t, "audit.audit_events", "payout.exception.resolved", exc); n != 1 {
		t.Fatalf("resolution audit: %d", n)
	}
	exc2 := p.dispatchUntilHalted(t, &restarted, id)
	if exc2 == exc {
		t.Fatal("no new exception after the fresh budget")
	}
	if n := p.createAttempts(t, id); n != 2*payouts.DefaultSubmitRetryBudget {
		t.Fatalf("attempts after resuming: %d, want %d", n, 2*payouts.DefaultSubmitRetryBudget)
	}
	p.integrity(t)
}

func TestPayoutHistoricalExcessiveAttemptsHaltedWithoutAnotherCall(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 5_000)
	id := p.requestDirect(t, camp, 1_009)
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	// history from before the budget existed: many refusals, all definitely not sent (as on the development database)
	for i := 0; i < 11; i++ {
		var aid string
		err := p.mig.QueryRow(ctx(t), `INSERT INTO app.payout_attempts (id, payout_request_id, attempt_no, operation, provider_id, our_reference,
			correlation_id, started_at) SELECT gen_random_uuid(), id, (SELECT max(attempt_no) + 1 FROM app.payout_attempts WHERE payout_request_id = $1),
			'CREATE_PAYOUT', provider_id, id, 'it-history', now() - interval '1 hour' FROM app.payout_requests WHERE id = $1 RETURNING id::text`, id).Scan(&aid)
		if err != nil {
			if strings.Contains(err.Error(), "halted") { // the compose worker already halted it: history long enough
				break
			}
			t.Fatal(err)
		}
		if _, err := p.mig.Exec(ctx(t), `UPDATE app.payout_attempts SET completed_at = now() - interval '1 hour', classification = 'DEFINITELY_NOT_SENT',
			latency_ms = 1 WHERE id = $1`, aid); err != nil {
			t.Fatal(err)
		}
	}
	n := p.createAttempts(t, id)
	if n <= payouts.DefaultSubmitRetryBudget {
		t.Fatalf("history too short: %d", n)
	}
	p.makeDue(t, id)
	if _, err := p.po.DispatchDue(ctx(t), 50); err != nil {
		t.Fatal(err)
	}
	exc, ev := p.openExhaustion(t, id)
	if exc == "" {
		t.Fatal("a payout over its budget was not halted")
	}
	if got := p.createAttempts(t, id); got != n {
		t.Fatalf("the provider was called again: attempts %d → %d", n, got)
	}
	if ev["budget"] == nil || ev["definitely_not_sent_attempts"] == nil {
		t.Fatalf("exception evidence %v", ev)
	}
	if po := p.payout(t, id); po.Status != payouts.StSubmitted {
		t.Fatalf("status %s", po.Status)
	}
	p.integrity(t)
}
