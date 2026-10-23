//go:build integration

package integration

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

// KI-S18-22 (docs/stage-19/ki-s18-22-payment-polling.md): routine status polling is bounded for every awaiting status.
// An exhausted budget is never evidence of failure: the payment keeps its status, gets a POLL_BUDGET_EXHAUSTED
// reconciliation reference and is not polled again; later authoritative evidence still resolves it.

// shiftClock is the payments service's clock moved forward by an adjustable offset (only that service: sessions and
// step-up in the same test server keep the real clock).
type shiftClock struct{ off atomic.Int64 }

func (c *shiftClock) Now() time.Time      { return time.Now().Add(time.Duration(c.off.Load())) }
func (c *shiftClock) add(d time.Duration) { c.off.Add(int64(d)) }

func (p *payIT) pollCount(t *testing.T, paymentID string) int {
	return p.count(t, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND operation = 'GET_STATUS'`, paymentID)
}

func (p *payIT) openRefs(t *testing.T, paymentID, reason string) int {
	return p.count(t, `SELECT count(*) FROM app.payment_reconciliation_references WHERE payment_intent_id = $1 AND reason = $2
		AND status <> 'RESOLVED_BY_PROVIDER_STATUS'`, paymentID, reason)
}

// A pending payment past the horizon stops being polled without being failed; a late signed webhook still resolves it
// and closes the reference; the success is recorded once.
func TestFinancialPaymentPollBudgetExhaustionAndLateSuccess(t *testing.T) {
	p := newPayIT(t)
	m := p.s.d.Verification
	clk := &shiftClock{}
	m.Payments.Clock = clk
	di := p.intent(t, p.donor, "1024") // suffix 24: pending_forever (no webhook, the provider keeps answering pending)
	a := p.attempt(t, p.donor, di, "ECOCASH", 201, "")
	for i := 0; i < 3; i++ {
		if _, err := m.Payments.PollPayment(ctx(t), a.ID); err != nil {
			t.Fatal(err)
		}
	}
	if st := p.status(t, a.ID); st != psp.StatusPending {
		t.Fatalf("status %s", st)
	}
	clk.add(payments.PollHorizon + time.Minute)
	// queries made by this server after the shift carry the shifted clock; the compose worker (real clock) may still
	// poll until the budget is spent, so "no query after exhaustion" is asserted on shifted timestamps and on the
	// total once polling has stopped
	shifted := func() int {
		return p.count(t, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND operation = 'GET_STATUS'
			AND started_at > now() + interval '1 hour'`, a.ID)
	}
	st, err := m.Payments.PollPayment(ctx(t), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if st != psp.StatusPending || p.status(t, a.ID) != psp.StatusPending {
		t.Fatalf("an exhausted budget changed the status: %s", st)
	}
	if n := shifted(); n != 0 {
		t.Fatalf("the provider was queried after the horizon (%d queries)", n)
	}
	before := p.pollCount(t, a.ID)
	if n := p.openRefs(t, a.ID, "POLL_BUDGET_EXHAUSTED"); n != 1 {
		t.Fatalf("POLL_BUDGET_EXHAUSTED references: %d", n)
	}
	var next *time.Time
	var needs bool
	_ = p.mig.QueryRow(ctx(t), `SELECT next_poll_at, needs_reconciliation FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&next, &needs)
	if next != nil || !needs {
		t.Fatalf("after exhaustion: next_poll_at %v needs_reconciliation %v", next, needs)
	}
	// repeated sweeps and explicit queries: no provider call, no second reference
	for i := 0; i < 3; i++ {
		_ = m.Payments.Sweep(ctx(t))
		_, _ = m.Payments.PollPayment(ctx(t), a.ID)
	}
	if n := p.pollCount(t, a.ID); n != before || shifted() != 0 {
		t.Fatalf("queried after exhaustion (%d → %d)", before, n)
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_reconciliation_references WHERE payment_intent_id = $1`, a.ID); n != 1 {
		t.Fatalf("references multiplied: %d", n)
	}
	// late authoritative evidence: the provider now reports paid and sends a signed webhook
	if err := m.Sandbox.Schedule(ctx(t), a.ID, sandbox.Hook{Status: "paid"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for p.status(t, a.ID) != psp.StatusSucceeded && time.Now().Before(deadline) {
		p.pump(t)
		time.Sleep(200 * time.Millisecond)
	}
	if st := p.status(t, a.ID); st != psp.StatusSucceeded {
		t.Fatalf("late success not applied: %s", st)
	}
	if n := p.openRefs(t, a.ID, "POLL_BUDGET_EXHAUSTED"); n != 0 {
		t.Fatal("the late success did not resolve the reference")
	}
	if n := p.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded' AND aggregate_id = $1`, a.ID); n != 1 {
		t.Fatalf("payment_succeeded emitted %d times", n)
	}
	if n := p.pollCount(t, a.ID); n != before {
		t.Fatalf("queried during resolution (%d → %d)", before, n)
	}
}

// The attempt cap is a second, independent stop: MaxStatusPolls queries and no more, whatever the clock.
func TestFinancialPaymentPollAttemptCap(t *testing.T) {
	p := newPayIT(t)
	m := p.s.d.Verification
	di := p.intent(t, p.donor, "2024")
	a := p.attempt(t, p.donor, di, "ECOCASH", 201, "")
	for i := 0; i < payments.MaxStatusPolls+20; i++ {
		if _, err := m.Payments.PollPayment(ctx(t), a.ID); err != nil {
			t.Fatal(err)
		}
	}
	if n := p.pollCount(t, a.ID); n != payments.MaxStatusPolls {
		t.Fatalf("%d status queries, cap %d", n, payments.MaxStatusPolls)
	}
	if st := p.status(t, a.ID); st != psp.StatusPending {
		t.Fatalf("status %s", st)
	}
	if n := p.openRefs(t, a.ID, "POLL_BUDGET_EXHAUSTED"); n != 1 {
		t.Fatalf("references %d", n)
	}
	// the repeated identical answers were audited once (Stage 18 poll dedupe) — not 500 times
	if n := p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'payments.provider.response_received' AND target_id = $1
		AND metadata->>'operation' = 'GET_STATUS'`, a.ID); n > 2 {
		t.Fatalf("%d status-poll audit events", n)
	}
}

// Many workers sweeping at once query a due payment once: the routine poll re-checks under the row lock that it is
// still due (before Stage 19 a sweep that listed it could query it again right after another worker had).
func TestFinancialPaymentConcurrentSweepsPollOnce(t *testing.T) {
	p := newPayIT(t)
	m := p.s.d.Verification
	di := p.intent(t, p.donor, "3024")
	a := p.attempt(t, p.donor, di, "ECOCASH", 201, "")
	for round := 0; round < 10; round++ {
		waitFor(t, 15*time.Minute, "payment due", func() bool {
			var due bool
			_ = p.mig.QueryRow(ctx(t), `SELECT next_poll_at <= now() FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&due)
			return due
		})
		before := p.pollCount(t, a.ID)
		var wg sync.WaitGroup
		for w := 0; w < 8; w++ {
			wg.Add(1)
			go func() { defer wg.Done(); _ = m.Payments.Sweep(context.Background()) }()
		}
		wg.Wait()
		got := p.pollCount(t, a.ID) - before
		if got > 1 {
			t.Fatalf("one due payment was queried %d times by concurrent sweeps", got)
		}
		if got == 1 {
			t.Logf("round %d: queried once by 8 concurrent sweeps", round)
			return
		}
	}
	t.Fatal("the payment was never polled by the sweeps")
}

// scriptedProvider answers status queries from a script (one observation class per query) — an in-test double, as in
// the Stage 9 tests, never a provider integration.
type scriptedProvider struct {
	fakeProvider
	mu     sync.Mutex
	script []psp.Observation
	calls  atomic.Int32
}

func (f *scriptedProvider) GetPayment(ctx context.Context, m psp.CallMeta, id string) psp.Observation {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.script) == 0 {
		return f.fakeProvider.GetPayment(ctx, m, id)
	}
	o := f.script[0]
	f.script = f.script[1:]
	return o
}

// Rate limiting, outages, timeouts and malformed answers never fail a payment and never produce a financial effect;
// each keeps the payment scheduled with backoff; the first authoritative answer applies once.
func TestFinancialPaymentPollErrorClassesNeverFail(t *testing.T) {
	p := newPayIT(t)
	code, _ := registerFakeProvider(t, p)
	s := newITServer(t, func(c *config.Config) {
		c.Payments.SandboxEnabled = false
		c.Payments.ExecutionEnvironment = config.ExecProviderSandbox
		c.Payments.ProviderAllowlist = []string{code}
	})
	f := &scriptedProvider{fakeProvider: fakeProvider{code: code, sec: s.d.Verification.PSP.Secrets}}
	f.status.Store("pending")
	s.d.Verification.PSP.Register(f)
	donor := verifiedUser(t, s, "donorpoll")
	r := donor.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "1024", "currency": "USD"},
		"Idempotency-Key", idemKey())
	donor.expect(r, 201, "")
	ar := donor.do("POST", "/donation-intents/"+r.field(t, "id")+"/attempts", map[string]string{"method": "ECOCASH"}, "Idempotency-Key", idemKey())
	donor.expect(ar, 201, "")
	id := ar.field(t, "id")
	f.mu.Lock()
	f.script = []psp.Observation{
		{Class: psp.ClassRateLimited, HTTPStatus: 429},
		{Class: psp.ClassRetryableBeforeSend, HTTPStatus: 503},
		{Class: psp.ClassIndeterminate, UnknownReason: psp.UnknownTimeout},
		{Class: psp.ClassAccepted, RawStatus: "???", Canonical: psp.StatusUnknown, Mapped: false, MappingVersion: "fake-1", HTTPStatus: 200},
	}
	f.mu.Unlock()
	pay := s.d.Verification.Payments
	for i := 0; i < 4; i++ {
		if _, err := pay.PollPayment(ctx(t), id); err != nil {
			t.Fatalf("poll %d: %v", i, err)
		}
		var st string
		var next *time.Time
		_ = p.mig.QueryRow(ctx(t), `SELECT status, next_poll_at FROM app.payment_intents WHERE id = $1`, id).Scan(&st, &next)
		if st == psp.StatusFailed || st == psp.StatusSucceeded {
			t.Fatalf("answer %d changed the status to %s", i, st)
		}
		if next == nil || !next.After(time.Now()) {
			t.Fatalf("answer %d left the payment unscheduled: %v", i, next)
		}
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND operation = 'GET_STATUS'
		AND classification IN ('RATE_LIMITED','DEFINITELY_NOT_SENT','OUTCOME_UNKNOWN')`, id); n != 3 {
		t.Fatalf("error answers recorded as attempts: %d", n)
	}
	// the malformed (unmapped) answer is evidence to reconcile, never a status
	if n := p.count(t, `SELECT count(*) FROM app.payment_reconciliation_references WHERE payment_intent_id = $1 AND reason = 'UNMAPPED_STATUS'`, id); n != 1 {
		t.Fatalf("UNMAPPED_STATUS references: %d", n)
	}
	// concurrent explicit queries and the authoritative answer: one success, one success event
	f.status.Store("paid")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = pay.PollPayment(context.Background(), id) }()
	}
	wg.Wait()
	if st := p.status(t, id); st != psp.StatusSucceeded {
		t.Fatalf("status %s", st)
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND to_status = 'SUCCEEDED' AND applied`, id); n != 1 {
		t.Fatalf("SUCCEEDED applied %d times", n)
	}
	if n := p.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded' AND aggregate_id = $1`, id); n != 1 {
		t.Fatalf("payment_succeeded emitted %d times", n)
	}
	callsAfter := f.calls.Load()
	for i := 0; i < 3; i++ {
		_, _ = pay.PollPayment(ctx(t), id)
		_ = pay.Sweep(ctx(t))
	}
	if f.calls.Load() != callsAfter {
		t.Fatal("a SUCCEEDED payment was queried again")
	}
}
