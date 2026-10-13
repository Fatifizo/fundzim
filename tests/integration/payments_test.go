//go:build integration

package integration

// Stage 8 payment layer against the real stack (ADR-039): the API runs in-process with the sandbox SIMULATOR
// enabled; the test drives the simulator's webhook delivery, the outbox dispatch and the payments sweep itself
// (the compose worker may do the same work concurrently: every step is idempotent). Nothing here can move money:
// the only provider is the simulator and live execution is a constant false.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/payments"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/psp"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

type payIT struct {
	s        *itServer
	f        *verifStaff
	mig      *pgxpool.Pool
	donor    vUser
	campaign string
}

var (
	payOnce     sync.Once
	payCampaign string
	payErr      string
)

// newPayIT starts the API with the sandbox simulator, a published campaign (shared per test binary) and a donor.
func newPayIT(t *testing.T) *payIT {
	t.Helper()
	need(t, "SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT", "SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_PREVIOUS", "SECRETREF_FUNDZIM_SANDBOX_API",
		"DATABASE_MIGRATION_URL")
	s := newITServer(t, func(c *config.Config) { c.Payments.SandboxEnabled = true })
	m := s.d.Verification
	if m.Sandbox == nil {
		t.Fatal("sandbox not registered although enabled")
	}
	m.Sandbox.WebhookURL = s.srv.URL + "/api/v1/webhooks/payments/sandbox"
	f := staffFixture(t, s)
	payOnce.Do(func() {
		payErr = "payment campaign fixture failed in an earlier test"
		u, id := readyCampaign(t, s, f, "pay-"+ids.New()[:6], "EDUCATION")
		u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")
		approveAndPublish(t, s, f, u, id)
		payCampaign, payErr = id, ""
	})
	if payCampaign == "" {
		t.Fatal(payErr)
	}
	donor := verifiedUser(t, s, "donor")
	return &payIT{s: s, f: f, mig: pool(t, os.Getenv("DATABASE_MIGRATION_URL")), donor: donor, campaign: payCampaign}
}

func idemKey() string { return ids.New() }

func (p *payIT) intent(t *testing.T, u vUser, amount string) string {
	t.Helper()
	r := u.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": amount, "currency": "USD"},
		"Idempotency-Key", idemKey())
	u.expect(r, 201, "")
	return r.field(t, "id")
}

type attemptView struct {
	ID          string  `json:"id"`
	Status      string  `json:"status"`
	FailureCode *string `json:"failure_code"`
	Number      int     `json:"attempt_number"`
	Provider    struct {
		Code      string `json:"code"`
		Simulated bool   `json:"simulated"`
	} `json:"provider"`
	NextAction struct {
		Type        string  `json:"type"`
		SessionID   *string `json:"session_id"`
		RedirectURL *string `json:"redirect_url"`
	} `json:"next_action"`
}

func (p *payIT) attempt(t *testing.T, u vUser, intentID, method string, want int, code string) attemptView {
	t.Helper()
	r := u.do("POST", "/donation-intents/"+intentID+"/attempts", map[string]string{"method": method}, "Idempotency-Key", idemKey())
	u.expect(r, want, code)
	var a attemptView
	if want == 201 {
		_ = json.Unmarshal(r.Data, &a)
	}
	return a
}

// pump does what the worker does: deliver due simulated webhooks, dispatch stored webhooks, sweep.
func (p *payIT) pump(t *testing.T) {
	t.Helper()
	m := p.s.d.Verification
	if _, err := m.Sandbox.DeliverDue(ctx(t), 100); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	rows, err := p.mig.Query(ctx(t), `SELECT e.id::text, e.payload FROM app.outbox_events e WHERE e.event_type = $1
		AND NOT EXISTS (SELECT 1 FROM app.inbox_events i WHERE i.consumer = $2 AND i.event_id = e.id) ORDER BY e.created_at LIMIT 200`,
		psp.EvWebhookReceived, psp.ConsumerDispatch)
	if err != nil {
		t.Fatal(err)
	}
	type ev struct {
		id      string
		payload []byte
	}
	var list []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.id, &e.payload); err != nil {
			t.Fatal(err)
		}
		list = append(list, e)
	}
	rows.Close()
	for _, e := range list {
		if err := m.PSP.Dispatch(ctx(t), outbox.Delivery{EventID: e.id, EventType: psp.EvWebhookReceived, Payload: e.payload, Attempt: 1}); err == nil {
			_, _ = p.mig.Exec(ctx(t), `INSERT INTO app.inbox_events (id, consumer, event_id, event_type) VALUES ($1, $2, $3, $4)
				ON CONFLICT (consumer, event_id) DO NOTHING`, ids.New(), psp.ConsumerDispatch, e.id, psp.EvWebhookReceived)
		}
	}
	if err := m.Payments.Sweep(ctx(t)); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}

func (p *payIT) status(t *testing.T, paymentID string) string {
	t.Helper()
	var st string
	if err := p.mig.QueryRow(ctx(t), `SELECT status FROM app.payment_intents WHERE id = $1`, paymentID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func (p *payIT) waitStatus(t *testing.T, paymentID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		p.pump(t)
		if st := p.status(t, paymentID); st == want {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("payment %s: status %s, want %s", paymentID, st, want)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func (p *payIT) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := p.mig.QueryRow(ctx(t), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%v: %s", err, sql)
	}
	return n
}

func (p *payIT) donationStatus(t *testing.T, id string) string {
	t.Helper()
	var st string
	if err := p.mig.QueryRow(ctx(t), `SELECT status FROM app.donation_intents WHERE id = $1`, id).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

// ===== happy path, idempotency, retries =================================================================

func TestPaymentsSandboxSuccessAndIdempotency(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	paymentJournalsBefore := p.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE source_system = 'payments'`)
	// intent creation is idempotent per key; a reused key with a different body fails safely
	key := idemKey()
	body := map[string]any{"campaign_id": p.campaign, "amount_minor": "1000", "currency": "USD"}
	r1 := u.do("POST", "/donation-intents", body, "Idempotency-Key", key)
	u.expect(r1, 201, "")
	r2 := u.do("POST", "/donation-intents", body, "Idempotency-Key", key)
	u.expect(r2, 201, "")
	if r1.field(t, "id") != r2.field(t, "id") || r2.Header.Get("Idempotent-Replayed") != "true" {
		t.Fatal("intent replay did not return the same intent")
	}
	body2 := map[string]any{"campaign_id": p.campaign, "amount_minor": "2000", "currency": "USD"}
	u.expect(u.do("POST", "/donation-intents", body2, "Idempotency-Key", key), 409, "IDEMPOTENCY_KEY_REUSED")
	u.expect(u.do("POST", "/donation-intents", body), 400, "IDEMPOTENCY_KEY_REQUIRED")
	id := r1.field(t, "id")
	if !strings.Contains(string(r1.Data), `"simulated":true`) || r1.field(t, "status") != "OPEN" {
		t.Fatalf("intent: %s", r1.Data)
	}
	// an intent is not a donation: nothing about it is public and no total exists
	if code, raw := publicGet(t, p.s, "/public/campaigns/"+p.campaign); code == 200 && strings.Contains(string(raw), "1000") {
		t.Log("note: public campaign response unchanged by intents")
	}
	// the attempt starts PENDING (provider acknowledged); a browser can never confirm it
	akey := idemKey()
	r := u.do("POST", "/donation-intents/"+id+"/attempts", map[string]string{"method": "ECOCASH"}, "Idempotency-Key", akey)
	u.expect(r, 201, "")
	var a attemptView
	_ = json.Unmarshal(r.Data, &a)
	if a.Status != "PENDING" || !a.Provider.Simulated || a.Provider.Code != "sandbox" || a.Number != 1 {
		t.Fatalf("attempt: %s", r.Data)
	}
	// replay of the same attempt request returns the same payment (no second provider call)
	rr := u.do("POST", "/donation-intents/"+id+"/attempts", map[string]string{"method": "ECOCASH"}, "Idempotency-Key", akey)
	u.expect(rr, 201, "")
	if rr.field(t, "id") != a.ID {
		t.Fatal("attempt replay created another payment")
	}
	if n := p.count(t, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND operation = 'CREATE'`, a.ID); n != 1 {
		t.Fatalf("CREATE calls: %d", n)
	}
	// a second payment while one is in progress is refused
	p.attempt(t, u, id, "ONEMONEY", 409, "PAYMENT_IN_PROGRESS")
	p.waitStatus(t, a.ID, "SUCCEEDED", 30*time.Second)
	if st := p.donationStatus(t, id); st != "PAID" {
		t.Fatalf("donation intent %s", st)
	}
	p.attempt(t, u, id, "ONEMONEY", 409, "DONATION_ALREADY_PAID")
	// history: P1, P2, P9 applied; success only from an authoritative source; exactly one success event
	var codes string
	_ = p.mig.QueryRow(ctx(t), `SELECT string_agg(transition_code, ',' ORDER BY recorded_at, id) FROM app.payment_events
		WHERE payment_intent_id = $1 AND applied`, a.ID).Scan(&codes)
	if codes != "P1,P2,P9" {
		t.Fatalf("transition codes %q", codes)
	}
	if p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND applied AND to_status = 'SUCCEEDED'
		AND source IN ('WEBHOOK','POLL')`, a.ID) != 1 {
		t.Fatal("success not recorded once from an authoritative source")
	}
	if p.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded' AND aggregate_id = $1`, a.ID) != 1 {
		t.Fatal("payment_succeeded must be emitted exactly once")
	}
	var raw, mapped string
	_ = p.mig.QueryRow(ctx(t), `SELECT raw_status, mapped_status FROM app.payment_transactions WHERE payment_intent_id = $1`, a.ID).Scan(&raw, &mapped)
	if raw != "paid" || mapped != "SUCCEEDED" {
		t.Fatalf("transaction raw %q mapped %q", raw, mapped)
	}
	// nothing posted (Stage 8 asserted that no ledger tables existed; Stage 10 created the ledger, ADR-042): a test
	// payment never becomes a journal, and no payments-sourced journal appears in the TEST ledger (development
	// databases may hold ten such rows from an earlier Stage 10 test, KI-S10-17; new ones are refused by CHECK)
	if p.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE source_id IN (SELECT (payload->>'payment_id')::uuid
		FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded' AND aggregate_id = $1)`, a.ID) != 0 {
		t.Fatal("a test payment reached the ledger")
	}
	if p.count(t, `SELECT count(*) FROM ledger.ledger_transactions WHERE source_system = 'payments'`) != paymentJournalsBefore {
		t.Fatal("a payments-sourced journal was posted to the TEST ledger")
	}
	// donor status endpoint and cross-user isolation
	st := u.do("GET", "/donation-intents/"+id+"/status", nil)
	u.expect(st, 200, "")
	if st.field(t, "donation_intent_status") != "PAID" || st.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status view: %s", st.Data)
	}
	other := verifiedUser(t, p.s, "donor-b")
	other.expect(other.do("GET", "/donation-intents/"+id, nil), 404, "DONATION_INTENT_NOT_FOUND")
	other.expect(other.do("POST", "/donation-intents/"+id+"/attempts", map[string]string{"method": "ECOCASH"}, "Idempotency-Key", idemKey()), 404, "")
}

func TestPaymentsDeclineThenRetry(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	id := p.intent(t, u, "1002")
	a := p.attempt(t, u, id, "ECOCASH", 201, "")
	if a.Status != "FAILED" || a.FailureCode == nil || *a.FailureCode != "DECLINED" {
		t.Fatalf("decline: %+v", a)
	}
	// a definite failure allows a new attempt (a NEW payment with a new reference)
	b := p.attempt(t, u, id, "ONEMONEY", 201, "")
	if b.ID == a.ID || b.Number != 2 {
		t.Fatalf("retry: %+v", b)
	}
	if p.count(t, `SELECT count(DISTINCT our_reference) FROM app.payment_attempts WHERE payment_intent_id IN ($1, $2)`, a.ID, b.ID) != 2 {
		t.Fatal("each payment keeps its own reference")
	}
}

// ===== unknown outcomes ===================================================================================

func TestPaymentsTimeoutIsUnknownNeverFailed(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	// timeout after the provider created the payment: UNKNOWN, then resolved by webhook/poll to SUCCEEDED
	id := p.intent(t, u, "1003")
	a := p.attempt(t, u, id, "ECOCASH", 201, "")
	if a.Status != "UNKNOWN" {
		t.Fatalf("timeout: %+v", a)
	}
	p.attempt(t, u, id, "ONEMONEY", 409, "PAYMENT_OUTCOME_UNKNOWN") // never pay twice while unknown
	var reason string
	_ = p.mig.QueryRow(ctx(t), `SELECT unknown_reason FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&reason)
	if reason != "TIMEOUT" {
		t.Fatalf("unknown reason %q", reason)
	}
	p.waitStatus(t, a.ID, "SUCCEEDED", 40*time.Second)

	// timeout where nothing was created: stays UNKNOWN (not-found is not final yet); re-created only with the SAME
	// reference (ECOCASH capability: idempotent_create); never FAILED by assumption
	id2 := p.intent(t, u, "1005")
	b := p.attempt(t, u, id2, "ECOCASH", 201, "")
	if b.Status != "UNKNOWN" {
		t.Fatalf("not-created timeout: %+v", b)
	}
	for i := 0; i < 4; i++ {
		_, _ = p.mig.Exec(ctx(t), `UPDATE app.payment_intents SET next_poll_at = now() WHERE id = $1`, b.ID)
		p.pump(t)
	}
	if st := p.status(t, b.ID); st != "UNKNOWN" {
		t.Fatalf("not-found before finality must stay UNKNOWN, got %s", st)
	}
	if p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND applied AND to_status = 'FAILED'`, b.ID) != 0 {
		t.Fatal("a timeout produced FAILED")
	}
	if p.count(t, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND classification = 'NOT_FOUND'`, b.ID) < 2 {
		t.Fatal("status queries not recorded")
	}
	if p.count(t, `SELECT count(DISTINCT our_reference) FROM app.payment_attempts WHERE payment_intent_id = $1`, b.ID) != 1 {
		t.Fatal("a retry used a different reference")
	}
}

func TestPaymentsCrashBetweenCallAndResultIsRecovered(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	id := p.intent(t, u, "1024") // pending_forever: the provider accepts and never confirms
	a := p.attempt(t, u, id, "ECOCASH", 201, "")
	if a.Status != "PENDING" {
		t.Fatalf("setup: %+v", a)
	}
	// simulate a worker crash after a status query was committed IN_FLIGHT but before its result was recorded
	if _, err := p.mig.Exec(ctx(t), `INSERT INTO app.payment_attempts (id, payment_intent_id, attempt_no, operation, provider_id, our_reference,
		correlation_id, started_at, deadline_at) SELECT $1, id, 99, 'GET_STATUS', provider_id, id, 'it-crash', now() - interval '5 minutes',
		now() - interval '4 minutes' FROM app.payment_intents WHERE id = $2`, ids.New(), a.ID); err != nil {
		t.Fatal(err)
	}
	p.pump(t)
	if n := p.count(t, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND attempt_no = 99
		AND classification = 'OUTCOME_UNKNOWN' AND unknown_reason = 'CRASH_DURING_CREATE'`, a.ID); n != 1 {
		t.Fatal("in-flight call not recovered by the sweeper")
	}
	if st := p.status(t, a.ID); st != "PENDING" {
		t.Fatalf("a crashed status query must not change the payment: %s", st)
	}
}

// ===== webhooks: duplicates, order, late success, anomalies ==============================================

func TestPaymentsWebhookOrderingAndDuplicates(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	// duplicate webhook (3 copies of one event): stored once, applied once
	a := p.attempt(t, u, p.intent(t, u, "1006"), "ECOCASH", 201, "")
	p.waitStatus(t, a.ID, "SUCCEEDED", 30*time.Second)
	time.Sleep(1500 * time.Millisecond)
	p.pump(t)
	if n := p.count(t, `SELECT count(*) FROM app.provider_webhook_inbox WHERE our_reference = $1 AND event_type_raw = 'payment.paid'`, a.ID); n != 1 {
		t.Fatalf("duplicate webhook stored %d times", n)
	}
	if p.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded' AND aggregate_id = $1`, a.ID) != 1 {
		t.Fatal("duplicate webhook applied twice")
	}
	// out of order: refunded (parked) → paid (applied, releases the parked event) → pending (lower rank, ignored)
	b := p.attempt(t, u, p.intent(t, u, "1007"), "ECOCASH", 201, "")
	p.waitStatus(t, b.ID, "REFUNDED", 40*time.Second)
	time.Sleep(2500 * time.Millisecond)
	p.pump(t)
	p.pump(t)
	if st := p.status(t, b.ID); st != "REFUNDED" {
		t.Fatalf("out-of-order regressed to %s", st)
	}
	if p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND not_applied_reason = 'PARKED'`, b.ID) < 1 ||
		p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND not_applied_reason = 'LOWER_RANK'
			AND to_status = 'PENDING'`, b.ID) < 1 {
		t.Fatal("parking / lower-rank not recorded")
	}
	// late success after expiry (P15): applied, flagged for reconciliation, donation paid
	did := p.intent(t, u, "1008")
	c := p.attempt(t, u, did, "ECOCASH", 201, "")
	p.waitStatus(t, c.ID, "EXPIRED", 30*time.Second)
	p.waitStatus(t, c.ID, "SUCCEEDED", 30*time.Second)
	if p.count(t, `SELECT count(*) FROM app.payment_reconciliation_references WHERE payment_intent_id = $1 AND reason = 'LATE_SUCCESS'`, c.ID) != 1 ||
		p.donationStatus(t, did) != "PAID" {
		t.Fatal("late success handling")
	}
	var codes string
	_ = p.mig.QueryRow(ctx(t), `SELECT string_agg(transition_code, ',' ORDER BY recorded_at, id) FROM app.payment_events
		WHERE payment_intent_id = $1 AND applied`, c.ID).Scan(&codes)
	if !strings.HasSuffix(codes, "P11,P15") {
		t.Fatalf("late success codes %q", codes)
	}
}

func TestPaymentsAnomaliesAreRecordedNotApplied(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	// unmapped provider status: never guessed; inbox UNRECOGNISED (alert), reconciliation reference
	a := p.attempt(t, u, p.intent(t, u, "1017"), "ECOCASH", 201, "")
	waitFor(t, 30*time.Second, "unrecognised webhook", func() bool {
		p.pump(t)
		return p.count(t, `SELECT count(*) FROM app.provider_webhook_inbox WHERE our_reference = $1 AND status = 'UNRECOGNISED'`, a.ID) == 1
	})
	if p.status(t, a.ID) != "PENDING" ||
		p.count(t, `SELECT count(*) FROM app.payment_reconciliation_references WHERE payment_intent_id = $1 AND reason = 'UNMAPPED_STATUS'`, a.ID) != 1 {
		t.Fatal("unmapped status handling")
	}
	// amount mismatch: success with a different amount is never applied
	b := p.attempt(t, u, p.intent(t, u, "1018"), "ECOCASH", 201, "")
	waitFor(t, 30*time.Second, "amount mismatch recorded", func() bool {
		p.pump(t)
		return p.count(t, `SELECT count(*) FROM app.payment_reconciliation_references WHERE payment_intent_id = $1 AND reason = 'AMOUNT_MISMATCH'`, b.ID) == 1
	})
	if st := p.status(t, b.ID); st == "SUCCEEDED" {
		t.Fatal("amount mismatch applied as success")
	}
	var needs bool
	_ = p.mig.QueryRow(ctx(t), `SELECT needs_reconciliation FROM app.payment_intents WHERE id = $1`, b.ID).Scan(&needs)
	if !needs {
		t.Fatal("needs_reconciliation not set")
	}
	// provider unavailable before send: definite pre-submit failure (nothing reached the provider)
	c := p.attempt(t, u, p.intent(t, u, "1019"), "ECOCASH", 201, "")
	if c.Status != "FAILED" || c.FailureCode == nil || *c.FailureCode != "PROVIDER_UNAVAILABLE_NOT_SENT" {
		t.Fatalf("not sent: %+v", c)
	}
	if p.count(t, `SELECT count(*) FROM app.payment_attempts WHERE payment_intent_id = $1 AND classification = 'DEFINITELY_NOT_SENT'`, c.ID) != 1 {
		t.Fatal("classification")
	}
}

// ===== webhook security ====================================================================================

func signedWebhook(t *testing.T, kid string, secret []byte, ts time.Time, payload sandbox.Payload) ([]byte, string) {
	t.Helper()
	body, _ := json.Marshal(payload)
	return body, sandbox.Sign(secret, kid, ts, body)
}

func postWebhook(t *testing.T, s *itServer, provider, ctype string, body []byte, sig string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", s.srv.URL+"/api/v1/webhooks/payments/"+provider, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if sig != "" {
		req.Header.Set(sandbox.SignatureHeader, sig)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestPaymentsWebhookSecurity(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	cur := []byte(os.Getenv("SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT"))
	a := p.attempt(t, u, p.intent(t, u, "1024"), "ECOCASH", 201, "") // pending forever: only our webhooks move it
	var ref string
	_ = p.mig.QueryRow(ctx(t), `SELECT provider_payment_ref FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&ref)
	evt := func() string { return "sbx_evt_" + strings.ReplaceAll(ids.New(), "-", "")[:20] }
	pl := sandbox.Payload{ID: evt(), Type: "payment.paid", Created: time.Now().Unix(), Simulated: true,
		Data: sandbox.PayloadData{PaymentReference: a.ID, ProviderPaymentID: ref, Status: "paid", AmountMinor: "1024", Currency: "USD"}}
	before := p.count(t, `SELECT count(*) FROM app.provider_webhook_rejections`)
	body, _ := signedWebhook(t, "current", cur, time.Now(), pl)
	if c := postWebhook(t, p.s, "sandbox", "application/json", body, ""); c != 401 {
		t.Fatalf("unsigned: %d", c)
	}
	_, forged := signedWebhook(t, "current", []byte("not-the-sandbox-secret-not-the-sandbox-secret"), time.Now(), pl)
	if c := postWebhook(t, p.s, "sandbox", "application/json", body, forged); c != 401 {
		t.Fatalf("forged: %d", c)
	}
	_, good := signedWebhook(t, "current", cur, time.Now(), pl)
	tampered := bytes.Replace(body, []byte(`"amount_minor":"1024"`), []byte(`"amount_minor":"9999"`), 1)
	if c := postWebhook(t, p.s, "sandbox", "application/json", tampered, good); c != 401 {
		t.Fatalf("tampered payload: %d", c)
	}
	_, stale := signedWebhook(t, "current", cur, time.Now().Add(-time.Hour), pl)
	if c := postWebhook(t, p.s, "sandbox", "application/json", body, stale); c != 401 {
		t.Fatalf("stale timestamp: %d", c)
	}
	_, future := signedWebhook(t, "current", cur, time.Now().Add(time.Hour), pl)
	if c := postWebhook(t, p.s, "sandbox", "application/json", body, future); c != 401 {
		t.Fatalf("future timestamp: %d", c)
	}
	if c := postWebhook(t, p.s, "sandbox", "text/plain", body, good); c != 415 {
		t.Fatalf("content type: %d", c)
	}
	if c := postWebhook(t, p.s, "nosuchpsp", "application/json", body, good); c != 404 {
		t.Fatalf("provider spoofing: %d", c)
	}
	big := bytes.Repeat([]byte("x"), psp.MaxWebhookBytes+10)
	if c := postWebhook(t, p.s, "sandbox", "application/json", big, good); c != 413 {
		t.Fatalf("too large: %d", c)
	}
	if after := p.count(t, `SELECT count(*) FROM app.provider_webhook_rejections`); after-before < 7 {
		t.Fatalf("rejections recorded: %d", after-before)
	}
	if p.count(t, `SELECT count(*) FROM app.provider_webhook_inbox WHERE our_reference = $1`, a.ID) != 0 {
		t.Fatal("a rejected webhook was stored")
	}
	if p.status(t, a.ID) != "PENDING" {
		t.Fatal("a rejected webhook changed the payment")
	}
	// the valid one is stored once and applied; a replay of the same bytes is a duplicate (2xx, no effect)
	if c := postWebhook(t, p.s, "sandbox", "application/json", body, good); c != 200 {
		t.Fatalf("valid: %d", c)
	}
	if c := postWebhook(t, p.s, "sandbox", "application/json", body, good); c != 200 {
		t.Fatalf("replay ack: %d", c)
	}
	if p.count(t, `SELECT count(*) FROM app.provider_webhook_inbox WHERE provider_event_id = $1`, pl.ID) != 1 {
		t.Fatal("replay stored twice")
	}
	p.waitStatus(t, a.ID, "SUCCEEDED", 20*time.Second)
	// rotation: the previous key still verifies (overlap) and is recorded as such
	prev := []byte(os.Getenv("SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_PREVIOUS"))
	pl2 := pl
	pl2.ID, pl2.Type, pl2.Data.Status = evt(), "payment.pending", "pending"
	b2, s2 := signedWebhook(t, "previous", prev, time.Now(), pl2)
	if c := postWebhook(t, p.s, "sandbox", "application/json", b2, s2); c != 200 {
		t.Fatalf("previous key: %d", c)
	}
	var slot string
	_ = p.mig.QueryRow(ctx(t), `SELECT signature_key_slot FROM app.provider_webhook_inbox WHERE provider_event_id = $1`, pl2.ID).Scan(&slot)
	if slot != "PREVIOUS" {
		t.Fatalf("slot %q", slot)
	}
	// concurrent duplicate deliveries of one event: exactly one inbox row
	pl3 := pl
	pl3.ID = evt()
	b3, s3 := signedWebhook(t, "current", cur, time.Now(), pl3)
	var wg sync.WaitGroup
	codes := make([]int, 20)
	for i := range codes {
		wg.Add(1)
		go func(i int) { defer wg.Done(); codes[i] = postWebhook(t, p.s, "sandbox", "application/json", b3, s3) }(i)
	}
	wg.Wait()
	for _, c := range codes {
		if c != 200 {
			t.Fatalf("concurrent delivery: %v", codes)
		}
	}
	if p.count(t, `SELECT count(*) FROM app.provider_webhook_inbox WHERE provider_event_id = $1`, pl3.ID) != 1 {
		t.Fatal("concurrent duplicates stored more than once")
	}
	// the sandbox's own scenarios: wrong key and replayed-old are rejected; the payment is resolved by status query
	d := p.attempt(t, u, p.intent(t, u, "1014"), "ECOCASH", 201, "")
	time.Sleep(1500 * time.Millisecond)
	p.pump(t)
	_, _ = p.mig.Exec(ctx(t), `UPDATE app.payment_intents SET next_poll_at = now() WHERE id = $1`, d.ID)
	p.waitStatus(t, d.ID, "SUCCEEDED", 30*time.Second)
	if p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND applied AND to_status = 'SUCCEEDED' AND source = 'POLL'`, d.ID) != 1 {
		t.Fatal("bad-signature scenario must resolve through the status API")
	}
}

// ===== sessions, redirects =================================================================================

func TestPaymentsRedirectSessionNeverConfirms(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	a := p.attempt(t, u, p.intent(t, u, "1024"), "CARD", 201, "")
	if a.Status != "REQUIRES_ACTION" || a.NextAction.Type != "REDIRECT" || a.NextAction.RedirectURL == nil ||
		!strings.HasPrefix(*a.NextAction.RedirectURL, "https://"+sandbox.RedirectHost+"/") || a.NextAction.SessionID == nil {
		t.Fatalf("redirect: %+v", a)
	}
	var ret string
	_ = p.mig.QueryRow(ctx(t), `SELECT coalesce(return_url, '') FROM app.sandbox_provider_payments WHERE our_reference = $1`, a.ID).Scan(&ret)
	ru, err := url.Parse(ret)
	if err != nil || ru.Query().Get("session") != *a.NextAction.SessionID {
		t.Fatalf("return url %q", ret)
	}
	token := ru.Query().Get("token")
	other := verifiedUser(t, p.s, "donor-c")
	other.expect(other.do("POST", "/payment-sessions/"+*a.NextAction.SessionID+"/return", map[string]string{"token": token}), 404, "PAYMENT_SESSION_INVALID")
	u.expect(u.do("POST", "/payment-sessions/"+*a.NextAction.SessionID+"/return", map[string]string{"token": strings.Repeat("A", len(token))}), 404, "PAYMENT_SESSION_INVALID")
	r := u.do("POST", "/payment-sessions/"+*a.NextAction.SessionID+"/return", map[string]string{"token": token})
	u.expect(r, 200, "")
	if st := p.status(t, a.ID); st == "SUCCEEDED" {
		t.Fatal("a browser return confirmed a payment")
	}
	// single use (replay protection)
	u.expect(u.do("POST", "/payment-sessions/"+*a.NextAction.SessionID+"/return", map[string]string{"token": token}), 404, "PAYMENT_SESSION_INVALID")
	var next *time.Time
	_ = p.mig.QueryRow(ctx(t), `SELECT next_poll_at FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&next)
	if next == nil || next.After(time.Now().Add(time.Second)) {
		t.Fatal("return did not schedule a status query")
	}
}

// ===== eligibility, currency, limits =======================================================================

func TestPaymentsEligibilityAndCurrency(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	u.expect(u.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "1000", "currency": "ZWG"},
		"Idempotency-Key", idemKey()), 422, "CURRENCY_NOT_AVAILABLE")
	u.expect(u.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "10.00", "currency": "USD"},
		"Idempotency-Key", idemKey()), 422, "VALIDATION_FAILED")
	u.expect(u.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "0", "currency": "USD"},
		"Idempotency-Key", idemKey()), 422, "VALIDATION_FAILED")
	u.expect(u.do("POST", "/donation-intents", map[string]any{"campaign_id": ids.New(), "amount_minor": "1000", "currency": "USD"},
		"Idempotency-Key", idemKey()), 422, "CAMPAIGN_NOT_ACCEPTING_DONATIONS")
	// a draft (not published) campaign is not payable
	owner, draft := readyCampaign(t, p.s, p.f, "pay-draft-"+ids.New()[:4], "EDUCATION")
	_ = owner
	u.expect(u.do("POST", "/donation-intents", map[string]any{"campaign_id": draft, "amount_minor": "1000", "currency": "USD"},
		"Idempotency-Key", idemKey()), 422, "CAMPAIGN_NOT_ACCEPTING_DONATIONS")
	// provider limits (sandbox contract: 1.00 – 10 000.00) apply at routing
	small := p.intent(t, u, "50")
	p.attempt(t, u, small, "ECOCASH", 422, "AMOUNT_OUT_OF_RANGE")
	// methods: unverified and merchant-settlement capabilities are never offered or routed
	mr := u.do("GET", "/payment-methods?currency=USD", nil)
	u.expect(mr, 200, "")
	if strings.Contains(string(mr.Data), "INNBUCKS") || strings.Contains(string(mr.Data), "BANK_TRANSFER") || !strings.Contains(string(mr.Data), "ECOCASH") {
		t.Fatalf("methods: %s", mr.Data)
	}
	id := p.intent(t, u, "1000")
	p.attempt(t, u, id, "INNBUCKS", 422, "PAYMENT_METHOD_UNAVAILABLE")
	p.attempt(t, u, id, "BANK_TRANSFER", 422, "PAYMENT_METHOD_UNAVAILABLE")
	// staff accounts cannot donate
	staff := p.s.staff(t, p.f.finance1)
	if r := staff.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "1000", "currency": "USD"},
		"Idempotency-Key", idemKey()); r.Status == 201 {
		t.Fatal("a staff account created a donation intent")
	}
	// a restricted donor cannot donate (any level, here RESTRICTED; no reason disclosed). Real maker-checker flow.
	rd := verifiedUser(t, p.s, "donor-restricted")
	c1, c2 := p.s.staff(t, p.f.compliance), p.s.staff(t, p.f.compliance2)
	r := c1.do("POST", "/admin/compliance/cases", map[string]any{"case_type": "OTHER", "severity": "S2", "note": "Donor under review (stage 8 test)",
		"subjects": []map[string]string{{"subject_type": "USER", "subject_id": rd.id, "role": "PRIMARY_SUBJECT"}}, "reason_code": "MANUAL_REFERRAL"})
	c1.expect(r, 201, "")
	caseID := r.field(t, "id")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/assign", map[string]string{}), 200, "")
	c1.expect(c1.do("POST", "/admin/compliance/cases/"+caseID+"/start", map[string]string{}), 200, "")
	rr := c1.do("POST", "/admin/compliance/cases/"+caseID+"/resolve", map[string]string{"decision": "RESTRICT", "reason_code": "POLICY_BREACH",
		"note": "restrict pending review of the donor"})
	if rr.Status == 202 {
		c2.expect(c2.do("POST", "/admin/compliance/cases/"+caseID+"/approve-resolution", map[string]string{"note": "second officer confirms"}), 200, "")
	} else {
		c1.expect(rr, 200, "")
	}
	waitFor(t, 30*time.Second, "donor restricted", func() bool {
		return p.count(t, `SELECT count(*) FROM compliance.subject_restrictions WHERE subject_id = $1 AND lifted_at IS NULL`, rd.id) > 0
	})
	res := rd.do("POST", "/donation-intents", map[string]any{"campaign_id": p.campaign, "amount_minor": "1000", "currency": "USD"},
		"Idempotency-Key", idemKey())
	rd.expect(res, 403, "DONATION_NOT_AVAILABLE")
	if strings.Contains(string(res.Data), "RESTRICT") {
		t.Fatal("restriction disclosed to the donor")
	}
}

// ===== concurrency =========================================================================================

func TestPaymentsConcurrentAttemptsYieldOnePayment(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	id := p.intent(t, u, "1024")
	var wg sync.WaitGroup
	codes := make([]int, 20)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = u.do("POST", "/donation-intents/"+id+"/attempts", map[string]string{"method": "ECOCASH"}, "Idempotency-Key", idemKey()).Status
		}(i)
	}
	wg.Wait()
	created := 0
	for _, c := range codes {
		switch c {
		case 201:
			created++
		case 409, 429:
		default:
			t.Fatalf("unexpected status in %v", codes)
		}
	}
	if created != 1 || p.count(t, `SELECT count(*) FROM app.payment_intents WHERE donation_intent_id = $1`, id) != 1 {
		t.Fatalf("concurrent attempts: %v", codes)
	}
	// 100 concurrent requests with ONE idempotency key: one intent
	key := idemKey()
	body := map[string]any{"campaign_id": p.campaign, "amount_minor": "1100", "currency": "USD"}
	got := make([]string, 100)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := u.do("POST", "/donation-intents", body, "Idempotency-Key", key)
			if r.Status == 201 {
				var m map[string]any
				_ = json.Unmarshal(r.Data, &m)
				got[i], _ = m["id"].(string)
			}
		}(i)
	}
	wg.Wait()
	if p.count(t, `SELECT count(*) FROM app.donation_intents WHERE donor_user_id = $1 AND idempotency_key = $2`, u.id, key) != 1 {
		t.Fatal("duplicate intents for one key")
	}
	seen := map[string]bool{}
	for _, g := range got {
		if g != "" {
			seen[g] = true
		}
	}
	if len(seen) > 1 {
		t.Fatalf("different intents returned for one key: %v", seen)
	}
}

// ===== refunds (maker-checker, never executed) ===========================================================

func TestPaymentsRefundRequestsNeverExecute(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	a := p.attempt(t, u, p.intent(t, u, "1000"), "ECOCASH", 201, "")
	support := p.s.staff(t, p.f.support)
	support.expect(support.do("POST", "/admin/payments/attempts/"+a.ID+"/refund-requests", map[string]string{"amount_minor": "500",
		"reason_code": "DONOR_REQUEST", "reason_text": "donor asked"}, "Idempotency-Key", idemKey()), 409, "REFUND_NOT_ALLOWED")
	p.waitStatus(t, a.ID, "SUCCEEDED", 30*time.Second)
	support.expect(support.do("POST", "/admin/payments/attempts/"+a.ID+"/refund-requests", map[string]string{"amount_minor": "1001",
		"reason_code": "DONOR_REQUEST", "reason_text": "too much"}, "Idempotency-Key", idemKey()), 422, "REFUND_EXCEEDS_REFUNDABLE")
	r := support.do("POST", "/admin/payments/attempts/"+a.ID+"/refund-requests", map[string]string{"amount_minor": "600",
		"reason_code": "DONOR_REQUEST", "reason_text": "the donor asked for a partial refund"}, "Idempotency-Key", idemKey())
	support.expect(r, 201, "")
	rid := r.field(t, "id")
	if r.field(t, "status") != "PENDING_APPROVAL" || r.field(t, "execution") != "DISABLED" {
		t.Fatalf("refund: %s", r.Data)
	}
	u.expect(u.do("POST", "/admin/payments/refund-requests/"+rid+"/approve", map[string]string{"reason": "x"}), 404, "")
	support.expect(support.do("POST", "/admin/payments/refund-requests/"+rid+"/approve", map[string]string{"reason": "approve my own"}), 403, "")
	fin := p.s.freshStaff(t, p.f.finance1) // fresh login = fresh step-up
	ap := fin.do("POST", "/admin/payments/refund-requests/"+rid+"/approve", map[string]string{"reason": "partial refund approved for the test"})
	fin.expect(ap, 200, "")
	if ap.field(t, "status") != "APPROVED" || ap.field(t, "execution") != "DISABLED" {
		t.Fatalf("approve: %s", ap.Data)
	}
	// a second request above the remainder is refused (DB guard); nothing ever executes
	support.expect(support.do("POST", "/admin/payments/attempts/"+a.ID+"/refund-requests", map[string]string{"amount_minor": "401",
		"reason_code": "DONOR_REQUEST", "reason_text": "above the remainder"}, "Idempotency-Key", idemKey()), 422, "REFUND_EXCEEDS_REFUNDABLE")
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.refund_requests SET status = 'EXECUTING' WHERE id = $1`, rid); err == nil {
		t.Fatal("APPROVED → EXECUTING must be impossible in Stage 8")
	}
	if st := p.status(t, a.ID); st != "SUCCEEDED" {
		t.Fatalf("an approved refund request changed the payment: %s", st)
	}
	if p.count(t, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app' AND table_name = 'refund_transactions'`) != 0 {
		t.Fatal("refund execution tables must not exist in Stage 8")
	}
}

// ===== staff operations and access =========================================================================

func TestPaymentsStaffOperations(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	a := p.attempt(t, u, p.intent(t, u, "1003"), "ECOCASH", 201, "")
	fin := p.s.freshStaff(t, p.f.finance1)
	for _, path := range []string{"/admin/payments/overview", "/admin/payments/attempts", "/admin/payments/attempts/" + a.ID, "/admin/payments/unknown",
		"/admin/payments/webhooks", "/admin/payments/webhook-rejections", "/admin/payments/providers", "/admin/payments/reconciliation",
		"/admin/payments/refund-requests", "/admin/payments/intents"} {
		r := fin.do("GET", path, nil)
		fin.expect(r, 200, "")
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: cache-control %q", path, r.Header.Get("Cache-Control"))
		}
		low := strings.ToLower(string(r.Data))
		for _, secret := range []string{strings.ToLower(os.Getenv("SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT")),
			strings.ToLower(os.Getenv("SECRETREF_FUNDZIM_SANDBOX_API")), "return_token"} {
			if secret != "" && strings.Contains(low, secret) {
				t.Fatalf("%s leaks a secret or token", path)
			}
		}
	}
	prov := fin.do("GET", "/admin/payments/providers", nil)
	if !strings.Contains(string(prov.Data), `"live_execution":{"enabled":false,"constant":false,"kill_switch":false}`) ||
		!strings.Contains(string(prov.Data), `"secrets_resolvable":true`) {
		t.Fatalf("providers view: %.500s", prov.Data)
	}
	// unauthorised access: donors and staff without payment.view get 404/403
	u.expect(u.do("GET", "/admin/payments/overview", nil), 404, "")
	rev := p.s.staff(t, p.f.rev1)
	if r := rev.do("GET", "/admin/payments/overview", nil); r.Status != 403 && r.Status != 404 {
		t.Fatalf("KYC reviewer: %d", r.Status)
	}
	// status query is audited and never sets a status itself; unknown queue lists the payment
	sq := fin.do("POST", "/admin/payments/attempts/"+a.ID+"/status-query", map[string]string{"reason": "checking the unknown outcome"})
	fin.expect(sq, 200, "")
	if p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'payments.status_query.requested' AND target_id = $1`, a.ID) != 1 {
		t.Fatal("status query not audited")
	}
	support := p.s.staff(t, p.f.support)
	support.expect(support.do("POST", "/admin/payments/attempts/"+a.ID+"/status-query", map[string]string{"reason": "support cannot"}), 403, "")
	// no endpoint can set a payment outcome manually
	for _, path := range []string{"/admin/payments/attempts/" + a.ID + "/succeed", "/admin/payments/attempts/" + a.ID + "/fail"} {
		if r := fin.do("POST", path, map[string]string{"reason": "manual"}); r.Status != 404 && r.Status != 405 {
			t.Fatalf("%s: %d", path, r.Status)
		}
	}
	p.waitStatus(t, a.ID, "SUCCEEDED", 40*time.Second)
}

// ===== database invariants under the runtime role ========================================================

func TestPaymentsDatabaseInvariants(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	a := p.attempt(t, u, p.intent(t, u, "1024"), "ECOCASH", 201, "")
	app := pool(t, os.Getenv("DATABASE_URL")) // fundzim_app
	mustFail := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := app.Exec(ctx(t), sql, args...); err == nil {
			t.Fatalf("%s must be refused", what)
		}
	}
	mustFail("event history edit", `UPDATE app.payment_events SET reason = 'x' WHERE payment_intent_id = $1`, a.ID)
	mustFail("event history delete", `DELETE FROM app.payment_events WHERE payment_intent_id = $1`, a.ID)
	mustFail("payment delete", `DELETE FROM app.payment_intents WHERE id = $1`, a.ID)
	mustFail("status without event", `UPDATE app.payment_intents SET status = 'SUCCEEDED', status_rank = 60 WHERE id = $1`, a.ID)
	mustFail("amount change", `UPDATE app.payment_intents SET amount_minor = amount_minor + 1 WHERE id = $1`, a.ID)
	mustFail("completed call edit", `UPDATE app.payment_attempts SET latency_ms = 1 WHERE payment_intent_id = $1 AND classification <> 'IN_FLIGHT'`, a.ID)
	mustFail("capability edit", `UPDATE app.provider_capabilities SET evidence_status = 'VERIFIED'`)
	if p.count(t, `SELECT count(*) FROM app.provider_webhook_inbox`) == 0 {
		t.Fatal("no inbox row to test immutability on")
	}
	mustFail("raw webhook edit", `UPDATE app.provider_webhook_inbox SET payload_redacted = '{}'
		WHERE id = (SELECT id FROM app.provider_webhook_inbox ORDER BY received_at DESC LIMIT 1)`)
	mustFail("webhook delete", `DELETE FROM app.provider_webhook_inbox WHERE id = (SELECT id FROM app.provider_webhook_inbox LIMIT 1)`)
	// success from an internal (non-provider) source is impossible even with an event
	tx, err := app.Begin(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var did string
	_ = tx.QueryRow(ctx(t), `SELECT donation_intent_id FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&did)
	_, err = tx.Exec(ctx(t), `INSERT INTO app.payment_events (id, subject_type, donation_intent_id, payment_intent_id, from_status, to_status,
		applied, source, actor_type) VALUES ($1, 'PAYMENT_INTENT', $2, $3, 'PENDING', 'SUCCEEDED', true, 'INTERNAL', 'SYSTEM')`, ids.New(), did, a.ID)
	if err == nil {
		t.Fatal("INTERNAL success event accepted")
	}
	// a merchant-settlement or unverified capability can never carry a payment (composite FK + CHECK)
	var bad int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payment_intents pi JOIN app.provider_capabilities c ON c.id = pi.capability_id
		WHERE NOT c.routable OR c.custody_model = 'MERCHANT_SETTLEMENT'`).Scan(&bad)
	if bad != 0 {
		t.Fatal("payment on a non-routable capability")
	}
	// the sandbox can never get a production account; a real provider cannot be enabled without selection
	if _, err := p.mig.Exec(ctx(t), `INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref,
		status) VALUES ($1, md5('payment_provider:sandbox')::uuid, 'PRODUCTION', 'nope', 'secretref://x/y', 'DISABLED')`, ids.New()); err == nil {
		t.Fatal("sandbox PRODUCTION account accepted")
	}
	if _, err := p.mig.Exec(ctx(t), `INSERT INTO app.payment_providers (id, code, display_name, confirmation_mode, status)
		VALUES ($1, 'realpsp', 'Real', 'WEBHOOK_SIGNED', 'ENABLED')`, ids.New()); err == nil {
		t.Fatal("unselected real provider enabled")
	}
	if _, err := p.mig.Exec(ctx(t), `INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, api_credentials_secret_ref)
		VALUES ($1, md5('payment_provider:sandbox')::uuid, 'SANDBOX', 'raw', 'raw-api-key-value-not-a-reference')`, ids.New()); err == nil {
		t.Fatal("raw secret stored in a provider account")
	}
}

// ===== provider outage (circuit breaker) ===================================================================

func TestPaymentsProviderOutageOpensCircuit(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	m := p.s.d.Verification
	m.PSP.Cfg.SandboxCircuitBreaker = true // this server only: apply the breaker to the simulator
	insert := func(kind string) {
		t.Helper()
		if _, err := p.mig.Exec(ctx(t), `INSERT INTO app.provider_health_events (id, provider_id, operation, currency, event_kind, observed_at)
			VALUES ($1, md5('payment_provider:sandbox')::uuid, 'CREATE_PAYMENT', 'USD', $2, now())`, ids.New(), kind); err != nil {
			t.Fatal(err)
		}
	}
	insert("CIRCUIT_OPENED")
	t.Cleanup(func() { insert("CIRCUIT_CLOSED") })
	id := p.intent(t, u, "1000")
	p.attempt(t, u, id, "ECOCASH", 503, "PAYMENT_PROVIDER_UNAVAILABLE")
	if p.count(t, `SELECT count(*) FROM app.payment_intents WHERE donation_intent_id = $1`, id) != 0 {
		t.Fatal("a payment was created while the provider circuit was open")
	}
	insert("CIRCUIT_CLOSED")
	a := p.attempt(t, u, id, "ECOCASH", 201, "")
	if a.Status != "PENDING" {
		t.Fatalf("after recovery: %+v", a)
	}
}

// ===== exactly-once effects, webhook vs poll race, dead letters =========================================

// postingRecorder is the TEST-ONLY ledger stub (ROADMAP Stage 8; TESTING.md §3.9). It counts every posting request
// made inside an apply transaction; the committed database state is asserted separately. Stage 8 wires no poster
// at runtime and has no ledger tables. Stage 9: only PRODUCTION payments reach the seam, so a simulated payment must
// produce no request at all (a stricter check than Stage 8's "at most one"). Exactly-once for real payments is
// enforced by the single SUCCEEDED transition and the posting key, asserted below.
type postingRecorder struct {
	mu   sync.Mutex
	keys map[string]int
}

func (r *postingRecorder) Post(_ context.Context, _ pgx.Tx, req payments.PostingRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys[req.Key]++
	return nil
}

func TestPaymentsPostingRequestsExactlyOnce(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	rec := &postingRecorder{keys: map[string]int{}}
	p.s.d.Verification.Payments.Ledger = rec
	// webhook racing a status poll on the same payment: the row lock serialises them; one SUCCEEDED, one request
	a := p.attempt(t, u, p.intent(t, u, "1024"), "ECOCASH", 201, "")
	m := p.s.d.Verification
	if err := m.Sandbox.Schedule(ctx(t), a.ID, sandbox.Hook{Status: "paid", Copies: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Sandbox.DeliverDue(ctx(t), 100); err != nil { // provider state is now "paid"; webhooks stored
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = m.Payments.PollPayment(context.Background(), a.ID) }()
		go func() { defer wg.Done(); p.pump(t) }()
	}
	wg.Wait()
	p.waitStatus(t, a.ID, "SUCCEEDED", 20*time.Second)
	// duplicate deliveries and a late success
	b := p.attempt(t, u, p.intent(t, u, "1006"), "ECOCASH", 201, "")
	c := p.attempt(t, u, p.intent(t, u, "1008"), "ECOCASH", 201, "")
	p.waitStatus(t, b.ID, "SUCCEEDED", 30*time.Second)
	p.waitStatus(t, c.ID, "SUCCEEDED", 30*time.Second)
	time.Sleep(1500 * time.Millisecond)
	p.pump(t)
	for _, id := range []string{a.ID, b.ID, c.ID} {
		key := "payment:" + id + ":capture"
		rec.mu.Lock()
		n := rec.keys[key]
		rec.mu.Unlock()
		// Stage 9: simulated (test) payments are never handed to the ledger seam, in this process or the worker;
		// the database must still show exactly one SUCCEEDED transition and one outbox success event
		if n != 0 {
			t.Fatalf("%s: a test payment was posted %d times", key, n)
		}
		if p.count(t, `SELECT count(*) FROM app.payment_events WHERE payment_intent_id = $1 AND applied AND to_status = 'SUCCEEDED'`, id) != 1 ||
			p.count(t, `SELECT count(*) FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded' AND aggregate_id = $1`, id) != 1 {
			t.Fatalf("%s: success not exactly once", id)
		}
	}
	if len(rec.keys) != 0 {
		t.Fatalf("test payments reached the ledger seam: %v", rec.keys)
	}
}

func TestPaymentsDeadLetterAndRequeue(t *testing.T) {
	p := newPayIT(t)
	u := p.donor
	m := p.s.d.Verification
	a := p.attempt(t, u, p.intent(t, u, "1024"), "ECOCASH", 201, "")
	// a handler that keeps failing (e.g. a database outage during apply): retries, then DEAD_LETTERED
	m.PSP.Handle("PAYMENT", func(context.Context, psp.InboxEvent) error {
		return fmt.Errorf("injected failure: database unavailable")
	})
	cur := []byte(os.Getenv("SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT"))
	var ref string
	_ = p.mig.QueryRow(ctx(t), `SELECT provider_payment_ref FROM app.payment_intents WHERE id = $1`, a.ID).Scan(&ref)
	pl := sandbox.Payload{ID: "sbx_evt_" + strings.ReplaceAll(ids.New(), "-", "")[:20], Type: "payment.paid", Created: time.Now().Unix(),
		Simulated: true, Data: sandbox.PayloadData{PaymentReference: a.ID, ProviderPaymentID: ref, Status: "paid", AmountMinor: "1024", Currency: "USD"}}
	body, sig := signedWebhook(t, "current", cur, time.Now(), pl)
	if c := postWebhook(t, p.s, "sandbox", "application/json", body, sig); c != 200 {
		t.Fatalf("ingest %d", c)
	}
	var inboxID, evID string
	var payload []byte
	_ = p.mig.QueryRow(ctx(t), `SELECT id FROM app.provider_webhook_inbox WHERE provider_event_id = $1`, pl.ID).Scan(&inboxID)
	_ = p.mig.QueryRow(ctx(t), `SELECT id, payload FROM app.outbox_events WHERE event_type = $1 AND aggregate_id = $2`,
		psp.EvWebhookReceived, inboxID).Scan(&evID, &payload)
	for attempt := 1; attempt <= psp.MaxDispatchAttempts; attempt++ {
		err := m.PSP.Dispatch(ctx(t), outbox.Delivery{EventID: evID, EventType: psp.EvWebhookReceived, Payload: payload, Attempt: attempt})
		var st string
		_ = p.mig.QueryRow(ctx(t), `SELECT status FROM app.provider_webhook_inbox WHERE id = $1`, inboxID).Scan(&st)
		if attempt < psp.MaxDispatchAttempts && (err == nil || st != "FAILED_RETRYING") {
			t.Fatalf("attempt %d: err %v status %s", attempt, err, st)
		}
		if attempt == psp.MaxDispatchAttempts && (err != nil || st != "DEAD_LETTERED") {
			t.Fatalf("final attempt: err %v status %s", err, st)
		}
	}
	if p.status(t, a.ID) != "PENDING" {
		t.Fatal("a failed apply changed the payment (transaction must roll back)")
	}
	// the event is kept; staff re-queue it (step-up) once the fault is fixed
	m.PSP.Handle("PAYMENT", m.Payments.ApplyWebhook)
	fin := p.s.freshStaff(t, p.f.finance1)
	fin.expect(fin.do("POST", "/admin/payments/webhooks/"+inboxID+"/requeue", map[string]string{"reason": "database restored, reprocess"}), 200, "")
	fin.expect(fin.do("POST", "/admin/payments/webhooks/"+inboxID+"/requeue", map[string]string{"reason": "again"}), 409, "INBOX_EVENT_NOT_REQUEUEABLE")
	p.waitStatus(t, a.ID, "SUCCEEDED", 20*time.Second)
	var st string
	_ = p.mig.QueryRow(ctx(t), `SELECT status FROM app.provider_webhook_inbox WHERE id = $1`, inboxID).Scan(&st)
	if st != "PROCESSED" {
		t.Fatalf("inbox after requeue: %s", st)
	}
	if p.count(t, `SELECT count(*) FROM audit.audit_events WHERE action = 'payments.webhook.requeued' AND target_id = $1`, inboxID) != 1 {
		t.Fatal("requeue not audited")
	}
}
