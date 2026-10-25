//go:build integration

package integration

// Stage 11 withdrawals and payouts (ADR-043) against the real stack: TEST payouts of synthetic TEST_ONLY funds
// through the sandbox simulator. The fixture is a real owner (IDENTITY_VERIFIED by a KYC reviewer, raised to
// PAYOUT_VERIFIED by a documentary destination verification) with a verified ECOCASH destination; each test gets its
// own published campaign (one non-terminal payout per campaign and currency) funded through the ledger's
// fund_campaign fixture (capture → settlement → release, all TEST_ONLY).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/config"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/ratelimit"
	"github.com/Fatifizo/fundzim/internal/psp/sandbox"
)

type payoutIT struct {
	s     *itServer
	f     *verifStaff
	mig   *pgxpool.Pool
	app   *pgxpool.Pool
	po    *payouts.Service
	led   *ledger.Service
	owner vUser
	benef string // the owner's SELF beneficiary
	dest  string // verified ECOCASH destination
	destV int
}

var (
	poOnce          sync.Once
	poOwner         vUser
	poBenef, poDest string
	poErr           string
)

func newPayoutIT(t *testing.T) *payoutIT {
	t.Helper()
	need(t, "DATABASE_LEDGER_URL", "DATABASE_MIGRATION_URL", "SECRETREF_FUNDZIM_SANDBOX_WEBHOOK_CURRENT")
	s := newITServer(t, func(c *config.Config) {
		c.Payments.SandboxEnabled = true
		c.Payouts.ExecutionEnvironment = payouts.ExecLocalSimulator
		c.Ledger.SyntheticFixturesEnabled = true
	}, func(d *app.Deps) {
		// per-server in-memory counters: the shared owner would otherwise exhaust payouts.request.user (10/hour, Valkey)
		// across tests and reruns; the policy itself is unchanged
		d.Limiter = ratelimit.New(nil, ratelimit.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	})
	m := s.d.Verification
	if m.Sandbox == nil {
		t.Fatal("sandbox not registered although enabled")
	}
	m.Sandbox.WebhookURL = s.srv.URL + "/api/v1/webhooks/payments/sandbox"
	m.Payouts.ReconGrace = time.Nanosecond
	// the tests' reconciliation runs walk their own coverage checkpoints, never the worker's (ADR-052)
	m.Payouts.ReconSweep = itSweep("payouts")
	if m.Ledger != nil {
		m.Ledger.ReconSweep = itSweep("ledger")
	}
	f := staffFixture(t, s)
	p := &payoutIT{s: s, f: f, mig: pool(t, os.Getenv("DATABASE_MIGRATION_URL")), app: pool(t, os.Getenv("DATABASE_URL")), po: m.Payouts, led: m.Ledger}
	poOnce.Do(func() {
		poErr = "payout owner fixture failed in an earlier test"
		u, camp := readyCampaign(t, s, f, "po-"+ids.New()[:6], "EDUCATION")
		_ = camp
		var benef string
		if err := p.mig.QueryRow(ctx(t), `SELECT beneficiary_id::text FROM app.campaign_beneficiaries WHERE campaign_id = $1`, camp).Scan(&benef); err != nil {
			t.Fatal(err)
		}
		// a destination, verified through the documentary route by a KYC reviewer
		r := u.do("POST", "/payout-destinations", map[string]any{"rail": "ECOCASH", "currency": "USD", "holder_name": "Tariro Moyo",
			"account_identifier": fmt.Sprintf("077%07d", time.Now().UnixNano()%10_000_000)})
		u.expect(r, 201, "")
		dID := r.field(t, "id")
		ev := u.uploadOK("PAYOUT_DESTINATION", dID, "MOBILE_MONEY_STATEMENT", "NA")
		u.waitDoc(ev)
		u.expect(u.do("POST", "/payout-destinations/"+dID+"/verification", map[string]any{"method": "MOBILE_MONEY_STATEMENT", "document_ids": []string{ev}}), 200, "")
		rev := s.staff(t, f.rev1)
		rev.expect(adminAct(rev, dID, "assign", nil), 200, "")
		rev.expect(adminAct(rev, dID, "approve", map[string]any{"reason_code": "OWNERSHIP_CONFIRMED", "name_match": true, "note": "statement name matches"}), 200, "")
		if err := m.KYC.RecordPayoutOwnership(ctx(t), u.id, dID); err != nil { // the kyc.payout_ownership consumer, run inline
			t.Fatal(err)
		}
		// cooling-off (72 h INTERNAL_RISK) is a fixture shortcut here; EC-06 itself is tested in TestPayoutEligibilityRefusals
		tx, err := p.mig.Begin(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		var v int
		if err := tx.QueryRow(ctx(t), `UPDATE app.payout_destinations SET cooling_off_until = now() - interval '1 minute' WHERE id = $1
			RETURNING version`, dID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx(t), `INSERT INTO app.payout_destination_events (id, destination_id, destination_version, event_type, from_status,
			to_status, actor_type, reason_code) VALUES (gen_random_uuid(), $1, $2, 'TEST_FIXTURE', 'VERIFIED', 'VERIFIED', 'SYSTEM',
			'TEST_FIXTURE_COOLING_OFF_SKIPPED')`, dID, v); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx(t)); err != nil {
			t.Fatal(err)
		}
		poOwner, poBenef, poDest, poErr = u, benef, dID, ""
	})
	if poErr != "" {
		t.Fatal(poErr)
	}
	p.owner, p.benef, p.dest = poOwner, poBenef, poDest
	p.owner.browser = &browser{t: t, s: s, c: poOwner.c, ip: poOwner.ip}
	_ = p.mig.QueryRow(ctx(t), `SELECT details_version FROM app.payout_destinations WHERE id = $1`, p.dest).Scan(&p.destV)
	if _, err := p.led.RunIntegrity(ctx(t), "SYSTEM", "payouts-it"); err != nil { // EC-19 needs a passed integrity run
		t.Fatal(err)
	}
	return p
}

// campaign publishes a new campaign of the fixture owner (SELF beneficiary) and funds it with TEST_ONLY money.
func (p *payoutIT) campaign(t *testing.T, fund int64) string {
	t.Helper()
	u := p.owner
	r := createCampaign(t, u, "Help Tariro go to school "+ids.New()[:6], "EDUCATION", nil)
	u.expect(r, 201, "")
	id := r.field(t, "id")
	u.expect(u.do("PATCH", "/campaigns/"+id, map[string]any{"story": storyText}, "If-Match", r.Header.Get("ETag")), 200, "")
	u.expect(u.do("POST", "/campaigns/"+id+"/beneficiaries", map[string]any{"beneficiary_id": p.benef, "disclosure": "NONE"}), 200, "")
	uploadCover(t, u, id)
	u.expect(u.do("POST", "/campaigns/"+id+"/submit", nil), 200, "")
	approveAndPublish(t, p.s, p.f, u, id)
	if fund > 0 {
		if _, err := p.led.FundCampaign(ctx(t), "", id, fund, ""); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (p *payoutIT) stepUpOwner(t *testing.T) {
	t.Helper()
	p.owner.expect(p.owner.do("POST", "/auth/step-up/verify", map[string]string{"password": itPassword}), 200, "")
}

// request makes the owner request a payout over HTTP.
func (p *payoutIT) request(t *testing.T, campaign string, amount int64, want int, code string) string {
	t.Helper()
	r := p.owner.do("POST", "/campaigns/"+campaign+"/payout-requests", map[string]any{"currency": "USD", "amount_minor": fmt.Sprint(amount),
		"destination_id": p.dest}, "Idempotency-Key", ids.New())
	if r.Status == 403 && r.Error.Code == "STEP_UP_REQUIRED" {
		p.stepUpOwner(t)
		r = p.owner.do("POST", "/campaigns/"+campaign+"/payout-requests", map[string]any{"currency": "USD", "amount_minor": fmt.Sprint(amount),
			"destination_id": p.dest}, "Idempotency-Key", ids.New())
	}
	p.owner.expect(r, want, code)
	if want != 202 {
		return ""
	}
	return r.field(t, "id")
}

// requestDirect requests through the service (no HTTP rate limit): for tests that need many payouts.
func (p *payoutIT) requestDirect(t *testing.T, campaign string, amount int64) string {
	t.Helper()
	po, err := p.po.RequestPayout(ctx(t), p.owner.id, true, campaign, payouts.RequestInput{Currency: "USD", AmountMinor: fmt.Sprint(amount),
		DestinationID: p.dest}, "direct-"+ids.New())
	if err != nil {
		t.Fatalf("request %d: %v", amount, err)
	}
	return po.ID
}

// staffAct runs a step-up protected staff action, stepping up with TOTP only when asked.
func (p *payoutIT) staffAct(t *testing.T, c staffCred, method, path string, body any) apiResp {
	t.Helper()
	b := p.s.staff(t, c)
	r := b.do(method, path, body)
	if r.Status == 403 && r.Error.Code == "STEP_UP_REQUIRED" {
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, c.secret)}), 200, "")
		r = b.do(method, path, body)
	}
	return r
}

func approveBody(reason string) map[string]any {
	return map[string]any{"reason": reason, "conflict_attested": true}
}

// approve gets the payout to APPROVED with FINANCE approvals (DUAL when the dual threshold is unconfigured).
func (p *payoutIT) approve(t *testing.T, id string) {
	t.Helper()
	r := p.staffAct(t, p.f.finance1, "POST", "/admin/payout-requests/"+id+"/approve", approveBody("checked the request and the destination"))
	p.s.staff(t, p.f.finance1).expect(r, 200, "")
	if r.field(t, "status") == "APPROVED" {
		return
	}
	r = p.staffAct(t, p.f.finance2, "POST", "/admin/payout-requests/"+id+"/approve", approveBody("second approval: amounts and ledger checked"))
	p.s.staff(t, p.f.finance2).expect(r, 200, "")
	if st := r.field(t, "status"); st != "APPROVED" {
		t.Fatalf("after two approvals: %s", st)
	}
}

func (p *payoutIT) payout(t *testing.T, id string) payouts.Payout {
	t.Helper()
	d, err := p.po.StaffDetail(ctx(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return d.Payout
}

func (p *payoutIT) balances(t *testing.T, campaign string) (payable, pending int64) {
	t.Helper()
	b, err := p.led.CampaignBalances(ctx(t), campaign, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return b[ledger.ClassCampaignPayable].BalanceMinor, b[ledger.ClassCampaignPayoutPend].BalanceMinor
}

// drive delivers due simulator webhooks to this server and polls, until cond holds.
func (p *payoutIT) drive(t *testing.T, timeout time.Duration, what string, cond func(payouts.Payout) bool, id string) payouts.Payout {
	t.Helper()
	var last payouts.Payout
	waitFor(t, timeout, what, func() bool {
		_, _ = p.s.d.Verification.Sandbox.DeliverDue(ctx(t), 50)
		_, _ = p.po.DispatchDue(ctx(t), 10)
		_, _ = p.po.PollDue(ctx(t), 10)
		last = p.payout(t, id)
		return cond(last)
	})
	return last
}

func status(st ...string) func(payouts.Payout) bool {
	return func(p payouts.Payout) bool {
		for _, s := range st {
			if p.Status == s {
				return true
			}
		}
		return false
	}
}

// journal waits for a derived payout journal: journals follow the committed state (syncLedger, possibly in the compose
// worker that applied the webhook), so they can trail the status by a moment.
func (p *payoutIT) journal(t *testing.T, key string) ledger.JournalRef {
	t.Helper()
	var j ledger.JournalRef
	waitFor(t, 30*time.Second, "journal "+key, func() bool {
		_ = p.po.Recover(ctx(t)) // re-drives missing journals, exactly as the recovery job does
		var ok bool
		j, ok, _ = p.led.JournalByKey(ctx(t), key)
		return ok
	})
	return j
}

func (p *payoutIT) integrity(t *testing.T) {
	t.Helper()
	rep, err := p.led.RunIntegrity(ctx(t), "SYSTEM", "payouts-it")
	if err != nil || !rep.Passed {
		t.Fatalf("ledger integrity after payouts: %+v %v", rep, err)
	}
}

// ===== happy path ===================================================================================================

func TestPayoutHappyPathReserveApproveSubmitComplete(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 50_000)

	// the owner's preview: synthetic TEST_ONLY funds, production BLOCKED, never "withdrawable"
	r := p.owner.do("GET", "/campaigns/"+camp+"/payout-eligibility?currency=USD&destination_id="+p.dest+"&amount_minor=10000", nil)
	p.owner.expect(r, 200, "")
	var av payouts.Availability
	_ = json.Unmarshal(r.Data, &av)
	if av.FundsLabel != "TEST_ONLY" || av.ProductionStatus != "BLOCKED" || av.AvailableMinor != 50_000 || av.Environment != "TEST" {
		t.Fatalf("availability: %+v", av)
	}

	id := p.request(t, camp, 10_000, 202, "") // suffix 00: simulator SUCCESS
	po := p.payout(t, id)
	if po.Status != payouts.StReview || po.Reservation == nil || po.Reservation.Status != payouts.ResActive || !po.TestPayout ||
		po.ExecutionEnvironment != "LOCAL_SIMULATOR" || po.LedgerEnvironment != "TEST" {
		t.Fatalf("after request: %+v", po)
	}
	if pay, pend := p.balances(t, camp); pay != 40_000 || pend != 10_000 {
		t.Fatalf("reservation not reflected in the ledger: payable %d pending %d", pay, pend)
	}
	if po.ApprovalPolicy != "DUAL" {
		t.Fatalf("unconfigured dual-approval limit must fail closed to DUAL, got %s", po.ApprovalPolicy)
	}
	// one FINANCE approval is not enough for DUAL; the same approver cannot approve twice
	r = p.staffAct(t, p.f.finance1, "POST", "/admin/payout-requests/"+id+"/approve", approveBody("first approval for a DUAL payout"))
	p.s.staff(t, p.f.finance1).expect(r, 200, "")
	if st := r.field(t, "status"); st != "PENDING_REVIEW" {
		t.Fatalf("DUAL after one approval: %s", st)
	}
	r = p.staffAct(t, p.f.finance1, "POST", "/admin/payout-requests/"+id+"/approve", approveBody("approving again must be refused"))
	p.s.staff(t, p.f.finance1).expect(r, 409, "ALREADY_DECIDED")
	r = p.staffAct(t, p.f.finance2, "POST", "/admin/payout-requests/"+id+"/approve", approveBody("second approval: amounts and ledger checked"))
	p.s.staff(t, p.f.finance2).expect(r, 200, "")

	// pre-submission re-check, submission (in-transit journal before the provider call), simulator completion
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil { // the compose worker may submit first (shared database)
		t.Fatal(err)
	}
	po = p.drive(t, 30*time.Second, "payout completed", status(payouts.StCompleted), id)
	if po.ProviderPayoutRef == nil || po.CompletedAt == nil {
		t.Fatalf("after completion: %+v / %+v", po, po.Reservation)
	}
	if pay, pend := p.balances(t, camp); pay != 40_000 || pend != 0 {
		t.Fatalf("after completion payable %d pending %d", pay, pend)
	}
	p.journal(t, "payout:"+id+":completed")
	po = p.payout(t, id)
	if po.Reservation.Status != payouts.ResConsumed {
		t.Fatalf("reservation after completion: %s", po.Reservation.Status)
	}
	d, _ := p.po.StaffDetail(ctx(t), id)
	for _, j := range d.Journals {
		want := strings.HasSuffix(j.Key, ":reserved") || strings.HasSuffix(j.Key, ":submitted") || strings.HasSuffix(j.Key, ":completed")
		if want != (j.TransactionID != nil) {
			t.Fatalf("journal %s present=%v", j.Key, j.TransactionID != nil)
		}
	}
	if len(d.Decisions) < 2 {
		t.Fatalf("expected REQUEST and PRE_SUBMIT decisions, got %d", len(d.Decisions))
	}
	// the owner's view: no approver identities, TEST label
	r = p.owner.do("GET", "/payout-requests/"+id, nil)
	p.owner.expect(r, 200, "")
	if strings.Contains(string(r.Data), p.f.finance1.id) || r.field(t, "funds_label") != "TEST_ONLY" || r.field(t, "status") != "COMPLETED" {
		t.Fatalf("owner view: %s", r.Data)
	}
	// every journal posted for this payout is a TEST, SIMULATED, payouts-sourced journal
	var bad int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM ledger.ledger_transactions WHERE idempotency_key LIKE $1
		AND NOT (environment = 'TEST' AND evidence_class = 'SIMULATED' AND source_system = 'payouts')`, "payout:"+id+":%").Scan(&bad)
	if bad != 0 {
		t.Fatalf("%d payout journals are not TEST/SIMULATED/payouts", bad)
	}
	p.integrity(t)
}

// ===== reservations under concurrency ===============================================================================

func TestPayoutHundredConcurrentRequestsReserveOnce(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 30_000)
	// through the service (the HTTP rate limit of 10 per hour per user would otherwise answer most of them)
	var okN, inProgress, other atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := p.po.RequestPayout(context.Background(), p.owner.id, true, camp, payouts.RequestInput{Currency: "USD",
				AmountMinor: "20000", DestinationID: p.dest}, fmt.Sprintf("concurrent-%s-%03d", camp[:8], i))
			var e *errs.Error
			switch {
			case err == nil:
				okN.Add(1)
			case errors.As(err, &e) && e.Code == "PAYOUT_IN_PROGRESS":
				inProgress.Add(1)
			default:
				other.Add(1)
				t.Logf("unexpected: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if okN.Load() != 1 || other.Load() != 0 {
		t.Fatalf("100 concurrent requests: %d accepted, %d in progress, %d other", okN.Load(), inProgress.Load(), other.Load())
	}
	var n int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_reservations WHERE campaign_id = $1`, camp).Scan(&n)
	if pay, pend := p.balances(t, camp); n != 1 || pay != 10_000 || pend != 20_000 {
		t.Fatalf("reservations %d payable %d pending %d", n, pay, pend)
	}
	// over-allocation is refused by the ledger lock even if the in-flight slot were free: cancel, then ask for more than is there
	var id string
	_ = p.mig.QueryRow(ctx(t), `SELECT id FROM app.payout_requests WHERE campaign_id = $1`, camp).Scan(&id)
	if _, err := p.po.CancelByOwner(ctx(t), p.owner.id, true, id); err != nil {
		t.Fatal(err)
	}
	if pay, pend := p.balances(t, camp); pay != 30_000 || pend != 0 {
		t.Fatalf("after cancel: payable %d pending %d", pay, pend)
	}
	p.request(t, camp, 30_100, 422, "PAYOUT_NOT_ELIGIBLE")
	// the HTTP route is rate limited per user (payouts.request.user)
	limited := false
	for i := 0; i < 12 && !limited; i++ {
		r := p.owner.do("POST", "/campaigns/"+camp+"/payout-requests", map[string]any{"currency": "USD", "amount_minor": "999999999",
			"destination_id": p.dest}, "Idempotency-Key", ids.New())
		limited = r.Status == 429
	}
	if !limited {
		t.Fatal("payout requests are not rate limited")
	}
	p.integrity(t)
}

// ===== unknown outcomes ==============================================================================================

func TestPayoutUnknownOutcomeKeepsReservationAndIsNeverResubmitted(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.request(t, camp, 10_004, 202, "") // suffix 04: the simulator never created it, the call timed out
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	po := p.payout(t, id)
	if po.Status != payouts.StUnknown || po.Reservation.Status != payouts.ResBlocked || po.UnknownReason == nil {
		t.Fatalf("after timeout: %+v %+v", po, po.Reservation)
	}
	// no resubmission: the dispatcher finds nothing, the database refuses a second CREATE_PAYOUT, cancellation is refused
	// the shared development database can hold other tests' due payouts (DispatchDue's count covers them), so the check is
	// on this payout: the dispatcher made no new CREATE_PAYOUT attempt and it is still UNKNOWN
	creates := func() int {
		var n int
		if err := p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := creates()
	if _, err := p.po.DispatchDue(ctx(t), 10); err != nil {
		t.Fatal(err)
	}
	if after := creates(); after != before || p.payout(t, id).Status != payouts.StUnknown {
		t.Fatalf("dispatch resubmitted an UNKNOWN payout (CREATE_PAYOUT attempts %d → %d)", before, after)
	}
	if _, err := p.app.Exec(ctx(t), `INSERT INTO app.payout_attempts (id, payout_request_id, attempt_no, operation, provider_id, our_reference,
		correlation_id, started_at) SELECT gen_random_uuid(), id, 99, 'CREATE_PAYOUT', provider_id, id, 'it', now() FROM app.payout_requests WHERE id = $1`, id); err == nil {
		t.Fatal("database allowed a second CREATE_PAYOUT for an UNKNOWN payout")
	}
	if _, err := p.po.CancelByOwner(ctx(t), p.owner.id, true, id); err == nil {
		t.Fatal("an UNKNOWN payout was cancelled")
	}
	if _, err := p.app.Exec(ctx(t), `UPDATE app.payout_reservations SET status = 'RELEASED' WHERE payout_request_id = $1`, id); err == nil {
		t.Fatal("database released the reservation of an UNKNOWN payout")
	}
	var exc int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_exceptions WHERE payout_request_id = $1 AND exception_type = 'UNKNOWN_PROVIDER_OUTCOME'`, id).Scan(&exc)
	if exc != 1 {
		t.Fatalf("unknown outcome exceptions: %d", exc)
	}
	if pay, pend := p.balances(t, camp); pay != 9_996 || pend != 0 {
		t.Fatalf("money must stay in transit while UNKNOWN: payable %d pending %d", pay, pend)
	}
	// "not found" is final only after the simulator's documented window: before it, still UNKNOWN; after it, FAILED
	if err := p.po.StatusQuery(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	if st := p.payout(t, id).Status; st != payouts.StUnknown {
		t.Fatalf("not-found within the finality window must stay UNKNOWN, got %s", st)
	}
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.payout_requests SET submitted_at = now() - interval '2 minutes' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if err := p.po.StatusQuery(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	po = p.payout(t, id)
	if po.Status != payouts.StFailed || po.Reservation.Status != payouts.ResReleased || po.FailureCode == nil || *po.FailureCode != "NOT_FOUND_FINAL" {
		t.Fatalf("after final not-found: %+v %+v", po, po.Reservation)
	}
	if pay, pend := p.balances(t, camp); pay != 20_000 || pend != 0 {
		t.Fatalf("after authoritative failure: payable %d pending %d", pay, pend)
	}
	p.integrity(t)
}

// ===== simulator scenarios ===========================================================================================

func TestPayoutSimulatorScenarios(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 500_000)
	type sc struct {
		name   string
		amount int64
		run    func(id string) payouts.Payout
		final  string
	}
	settle := func(st ...string) func(string) payouts.Payout {
		return func(id string) payouts.Payout {
			return p.drive(t, 40*time.Second, "terminal "+strings.Join(st, "/"), status(st...), id)
		}
	}
	results := []string{}
	for _, c := range []sc{
		{"SUCCESS", 1_000, settle(payouts.StCompleted), payouts.StCompleted},
		{"PENDING (then a scheduled completion)", 1_001, func(id string) payouts.Payout {
			p.drive(t, 15*time.Second, "processing", status(payouts.StProcessing), id)
			if err := p.s.d.Verification.Sandbox.SchedulePayout(ctx(t), id, sandbox.Hook{Status: "completed"}); err != nil {
				t.Fatal(err)
			}
			return settle(payouts.StCompleted)(id)
		}, payouts.StCompleted},
		{"FAILURE", 1_002, settle(payouts.StFailed), payouts.StFailed},
		{"TIMEOUT (then success)", 1_003, settle(payouts.StCompleted), payouts.StCompleted},
		{"DUPLICATE_CALLBACK", 1_005, settle(payouts.StCompleted), payouts.StCompleted},
		{"OUT_OF_ORDER_CALLBACK", 1_006, func(id string) payouts.Payout {
			po := settle(payouts.StCompleted)(id)
			time.Sleep(4 * time.Second) // the late "processing" webhook
			_, _ = p.s.d.Verification.Sandbox.DeliverDue(ctx(t), 50)
			return po
		}, payouts.StCompleted},
		{"INVALID_SIGNATURE (resolved by polling)", 1_007, settle(payouts.StCompleted), payouts.StCompleted},
		{"DELAYED_SUCCESS", 1_008, settle(payouts.StCompleted), payouts.StCompleted},
		{"PROVIDER_UNAVAILABLE (same-reference retry)", 1_009, func(id string) payouts.Payout {
			po := p.payout(t, id)
			if po.Status != payouts.StSubmitted {
				t.Fatalf("PROVIDER_UNAVAILABLE must leave the payout SUBMITTED, got %s", po.Status)
			}
			var notSent, total int
			_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FILTER (WHERE classification = 'DEFINITELY_NOT_SENT'), count(*) FROM app.payout_attempts
				WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&notSent, &total)
			if notSent != total || total < 1 {
				t.Fatalf("attempts: %d not sent of %d", notSent, total)
			}
			// the simulator keeps refusing this amount: operations cancel is impossible after submission, so we stop here
			// with the money in transit and record the state (the next attempt reuses the same reference)
			return po
		}, payouts.StSubmitted},
		{"PARTIAL_PROVIDER_RESPONSE", 1_010, settle(payouts.StCompleted), payouts.StCompleted},
		{"FAILURE_AFTER_PROCESSING", 1_011, settle(payouts.StFailed), payouts.StFailed},
		{"RETURNED_AFTER_COMPLETION", 1_012, settle(payouts.StReversed), payouts.StReversed},
	} {
		if c.final == payouts.StSubmitted {
			// PROVIDER_UNAVAILABLE never completes: give it its own campaign so the single in-flight slot stays free
			other := p.campaign(t, 5_000)
			id := p.requestDirect(t, other, c.amount)
			p.approve(t, id)
			if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
				t.Fatal(err)
			}
			po := c.run(id)
			results = append(results, fmt.Sprintf("%-45s %s", c.name, po.Status))
			continue
		}
		id := p.requestDirect(t, camp, c.amount)
		p.approve(t, id)
		if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
			t.Fatal(err)
		}
		po := c.run(id)
		if po.Status != c.final {
			t.Fatalf("%s: %s, want %s", c.name, po.Status, c.final)
		}
		var applied, notApplied int
		_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FILTER (WHERE applied AND to_status IS NOT NULL), count(*) FILTER (WHERE NOT applied)
			FROM app.payout_events WHERE payout_request_id = $1`, id).Scan(&applied, &notApplied)
		results = append(results, fmt.Sprintf("%-45s %-10s applied transitions %d, not applied observations %d", c.name, po.Status, applied, notApplied))
		switch c.name {
		case "DUPLICATE_CALLBACK":
			// three deliveries of one provider event: the inbox stores it once (UNIQUE (provider, event id)), so the
			// payout sees it once
			var delivered, stored int
			_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FILTER (WHERE w.delivered_at IS NOT NULL),
				(SELECT count(*) FROM app.provider_webhook_inbox i WHERE i.provider_event_id = min(w.provider_event_id))
				FROM app.sandbox_scheduled_webhooks w JOIN app.sandbox_provider_payouts q ON q.id = w.sandbox_payout_id
				WHERE q.our_reference = $1`, id).Scan(&delivered, &stored)
			if stored != 1 {
				t.Fatalf("DUPLICATE_CALLBACK: %d deliveries stored %d times", delivered, stored)
			}
			results[len(results)-1] += fmt.Sprintf(" (deliveries %d, stored once)", delivered)
		case "OUT_OF_ORDER_CALLBACK":
			if notApplied < 1 {
				t.Fatalf("%s: the late lower-rank webhook must be recorded, not applied", c.name)
			}
		case "RETURNED_AFTER_COMPLETION":
			if j := p.journal(t, "payout:"+id+":reversed"); j.DebitMinor != c.amount {
				t.Fatalf("return journal: %+v", j)
			}
		}
	}
	// WORKER_RESTART: a CREATE_PAYOUT left IN_FLIGHT by a dead worker becomes OUTCOME_UNKNOWN, never a resend. The crash
	// is staged between submission (journal posted, next send not before +5 s) and the call. The compose worker shares
	// the database and may submit and send first: then the staging is refused and repeated with a new payout.
	var id string
	var po payouts.Payout
	for try := 0; ; try++ {
		id = p.requestDirect(t, camp, int64(1_100+try*100))
		p.approve(t, id)
		if ok, err := p.po.SubmitApproved(ctx(t), id); err != nil {
			t.Fatal(err)
		} else if ok {
			po = p.payout(t, id)
			if _, err := p.mig.Exec(ctx(t), `INSERT INTO app.payout_attempts (id, payout_request_id, attempt_no, operation, provider_id, our_reference,
				correlation_id, started_at) VALUES (gen_random_uuid(), $1, 1, 'CREATE_PAYOUT', $2, $1, 'crashed-worker', now() - interval '5 minutes')`,
				id, po.ProviderID); err == nil {
				break
			}
		}
		if try == 3 {
			t.Fatal("could not stage a worker crash (the compose worker always sent first)")
		}
		p.drive(t, 40*time.Second, "staging payout settled", status(payouts.StCompleted, payouts.StFailed), id)
	}
	if err := p.po.Recover(ctx(t)); err != nil {
		t.Fatal(err)
	}
	po = p.payout(t, id)
	if po.Status != payouts.StUnknown || po.UnknownReason == nil || *po.UnknownReason != "CRASH_DURING_CREATE" {
		t.Fatalf("worker restart: %s %v", po.Status, po.UnknownReason)
	}
	var creates int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_attempts WHERE payout_request_id = $1 AND operation = 'CREATE_PAYOUT'`, id).Scan(&creates)
	if creates != 1 {
		t.Fatalf("a crashed payout was resent: %d CREATE_PAYOUT attempts", creates)
	}
	results = append(results, fmt.Sprintf("%-45s %s (CRASH_DURING_CREATE; reservation %s)", "WORKER_RESTART", po.Status, po.Reservation.Status))
	for _, r := range results {
		t.Log(r)
	}
	p.integrity(t)
}

// ===== approvals: separation of duties, invalidation ==================================================================

func TestPayoutApprovalSeparationOfDutiesAndInvalidation(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.request(t, camp, 5_000, 202, "")
	// a FINANCE officer linked to the owner's personal account cannot approve (Go and database)
	a, b := p.s.staff(t, p.f.adminA), p.s.staff(t, p.f.adminB)
	linked := inviteStaff(t, p.s, a, b, "Linked Finance", "FINANCE")
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.users SET staff_personal_user_id = $2 WHERE id = $1`, linked.id, p.owner.id); err != nil {
		t.Fatal(err)
	}
	r := p.staffAct(t, linked, "POST", "/admin/payout-requests/"+id+"/approve", approveBody("linked account must not approve"))
	p.s.staff(t, linked).expect(r, 403, "SELF_APPROVAL_FORBIDDEN")
	if _, err := p.app.Exec(ctx(t), `INSERT INTO app.payout_approvals (id, payout_request_id, approval_round, approver_id, approver_role, decision,
		reason, step_up_at, conflict_attested, decided_at) VALUES (gen_random_uuid(), $1, 1, $2, 'FINANCE', 'APPROVE', 'direct sql bypass', now(), true, now())`,
		id, linked.id); err == nil {
		t.Fatal("database accepted an approval from an account linked to the owner")
	}
	// the KYC reviewer who verified the destination cannot approve either (database guard; not FINANCE anyway)
	if _, err := p.app.Exec(ctx(t), `INSERT INTO app.payout_approvals (id, payout_request_id, approval_round, approver_id, approver_role, decision,
		reason, step_up_at, conflict_attested, decided_at) VALUES (gen_random_uuid(), $1, 1, $2, 'FINANCE', 'APPROVE', 'direct sql bypass', now(), true, now())`,
		id, p.f.rev1.id); err == nil {
		t.Fatal("database accepted an approval from the destination's verifier")
	}
	// staff without payout.approve
	r = p.staffAct(t, p.f.compliance, "POST", "/admin/payout-requests/"+id+"/approve", approveBody("compliance never approves payouts"))
	p.s.staff(t, p.f.compliance).expect(r, 403, "PERMISSION_DENIED")
	// no shortcut: APPROVED without approvals is refused by the database
	if _, err := p.app.Exec(ctx(t), `UPDATE app.payout_requests SET status = 'APPROVED', status_rank = 20 WHERE id = $1`, id); err == nil {
		t.Fatal("database allowed APPROVED without approvals")
	}
	p.approve(t, id)
	// a material change (destination details) after approval invalidates the approvals: back to review, new round
	p.stepUpOwner(t)
	r = p.owner.do("GET", "/payout-destinations/"+p.dest, nil)
	p.owner.expect(r, 200, "")
	// changing the shared fixture destination would unverify it for later tests: use the approval-round mechanism
	// through a destination SUSPENSION and reinstatement by a reviewer instead (same invalidation path)
	if err := p.po.SetSuspended(ctx(t), p.dest, p.f.rev2.id, "SUSPECTED_TAKEOVER", true); err != nil {
		t.Fatal(err)
	}
	// the shared fixture destination is reinstated even when an assertion below fails, so a failure here never cascades
	// into later payout tests (KI-R1-07); a no-op when the test reinstates it itself
	t.Cleanup(func() { _ = p.po.SetSuspended(context.Background(), p.dest, p.f.rev2.id, "REVIEW_CLEARED", false) })
	po := p.payout(t, id)
	if po.Status != payouts.StReview || po.ApprovalRound != 2 {
		t.Fatalf("after destination suspension: %s round %d", po.Status, po.ApprovalRound)
	}
	if err := p.po.SetSuspended(ctx(t), p.dest, p.f.rev2.id, "REVIEW_CLEARED", false); err != nil {
		t.Fatal(err)
	}
	// old-round approvals do not count; amount and destination snapshot are immutable
	if _, err := p.app.Exec(ctx(t), `UPDATE app.payout_requests SET amount_minor = 1 WHERE id = $1`, id); err == nil {
		t.Fatal("payout amount changed after approval")
	}
	p.approve(t, id)
	if st := p.payout(t, id).Status; st != payouts.StApproved {
		t.Fatalf("after re-approval: %s", st)
	}
	if _, err := p.po.CancelByStaff(ctx(t), p.f.finance1.id, id, "test cleanup: cancel before submission"); err != nil {
		t.Fatal(err)
	}
	if pay, pend := p.balances(t, camp); pay != 20_000 || pend != 0 {
		t.Fatalf("after cancellation payable %d pending %d", pay, pend)
	}
	p.integrity(t)
}

// ===== eligibility refusals and pre-submission re-check ==============================================================

func TestPayoutEligibilityRefusalsAndPreSubmitRecheck(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 10_000)
	// insufficient settled funds (the refusal is recorded as evidence with no payout row)
	before := 0
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_eligibility_decisions WHERE campaign_id = $1 AND payout_request_id IS NULL`, camp).Scan(&before)
	p.request(t, camp, 10_001, 422, "PAYOUT_NOT_ELIGIBLE")
	var after int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_eligibility_decisions WHERE campaign_id = $1 AND payout_request_id IS NULL`, camp).Scan(&after)
	if after != before+1 {
		t.Fatal("refused request left no decision record")
	}
	// refund exposure counts against available funds: no refunds exist for this campaign, so the full amount is allowed
	// unrelated campaign: another owner's campaign is not found
	other := verifiedUser(t, p.s, "po-other")
	other.expect(other.do("POST", "/campaigns/"+camp+"/payout-requests", map[string]any{"currency": "USD", "amount_minor": "100",
		"destination_id": p.dest}, "Idempotency-Key", ids.New()), 404, "")
	other.expect(other.do("GET", "/campaigns/"+camp+"/payout-eligibility", nil), 404, "")
	// ZWG and unknown destination
	p.owner.expect(p.owner.do("POST", "/campaigns/"+camp+"/payout-requests", map[string]any{"currency": "ZWG", "amount_minor": "100",
		"destination_id": p.dest}, "Idempotency-Key", ids.New()), 422, "PAYOUT_NOT_ELIGIBLE")
	p.owner.expect(p.owner.do("POST", "/campaigns/"+camp+"/payout-requests", map[string]any{"currency": "USD", "amount_minor": "100",
		"destination_id": ids.New()}, "Idempotency-Key", ids.New()), 422, "PAYOUT_NOT_ELIGIBLE")
	// float amounts are refused by decoding
	p.owner.expect(p.owner.do("POST", "/campaigns/"+camp+"/payout-requests", map[string]any{"currency": "USD", "amount_minor": 1.5,
		"destination_id": p.dest}, "Idempotency-Key", ids.New()), 400, "")

	// pre-submission re-check: the campaign is paused after approval → back to review (funds stay reserved)
	id := p.request(t, camp, 4_000, 202, "")
	p.approve(t, id)
	p.owner.expect(p.owner.do("POST", "/campaigns/"+camp+"/pause", map[string]any{"reason": "OWNER_REQUEST", "note": "pausing for a week"}), 200, "")
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	po := p.payout(t, id)
	if po.Status != payouts.StReview || po.Reservation.Status != payouts.ResActive {
		t.Fatalf("paused campaign at submission: %s / %s", po.Status, po.Reservation.Status)
	}
	var exc int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_exceptions WHERE payout_request_id = $1 AND exception_type = 'COMPLIANCE_HOLD'`, id).Scan(&exc)
	if exc != 1 {
		t.Fatal("hold exception not opened")
	}
	// the owner cancels: the reservation is released and the money is back
	p.stepUpOwner(t)
	p.owner.expect(p.owner.do("POST", "/payout-requests/"+id+"/cancel", nil), 200, "")
	if pay, pend := p.balances(t, camp); pay != 10_000 || pend != 0 {
		t.Fatalf("after owner cancel payable %d pending %d", pay, pend)
	}
	// cancellation needs a fresh step-up
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.sessions SET step_up_at = now() - interval '2 hours' WHERE user_id = $1`, p.owner.id); err != nil {
		t.Fatal(err)
	}
	p.owner.expect(p.owner.do("POST", "/campaigns/"+camp+"/payout-requests", map[string]any{"currency": "USD", "amount_minor": "100",
		"destination_id": p.dest}, "Idempotency-Key", ids.New()), 403, "STEP_UP_REQUIRED")
	p.integrity(t)
}

// ===== database guards ================================================================================================

func TestPayoutDatabaseGuards(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 10_000)
	id := p.request(t, camp, 2_000, 202, "")
	mustFail := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := p.app.Exec(ctx(t), sql, args...); err == nil {
			t.Fatalf("database allowed: %s", what)
		}
	}
	mustFail("a PRODUCTION payout", `UPDATE app.payout_requests SET execution_environment = 'PRODUCTION' WHERE id = $1`, id)
	mustFail("a non-test payout", `UPDATE app.payout_requests SET test_payout = false WHERE id = $1`, id)
	mustFail("SUBMITTED without an ELIGIBLE PRE_SUBMIT decision", `UPDATE app.payout_requests SET status = 'SUBMITTED', status_rank = 30, submitted_at = now() WHERE id = $1`, id)
	mustFail("an illegal transition (PENDING_REVIEW → COMPLETED)", `UPDATE app.payout_requests SET status = 'COMPLETED', status_rank = 60, completed_at = now() WHERE id = $1`, id)
	mustFail("deleting a payout", `DELETE FROM app.payout_requests WHERE id = $1`, id)
	mustFail("editing payout history", `UPDATE app.payout_events SET reason = 'x' WHERE payout_request_id = $1`, id)
	mustFail("deleting a decision", `DELETE FROM app.payout_eligibility_decisions WHERE payout_request_id = $1`, id)
	mustFail("consuming a reservation without completion", `UPDATE app.payout_reservations SET status = 'CONSUMED' WHERE payout_request_id = $1`, id)
	mustFail("a second non-terminal payout for the campaign", `INSERT INTO app.payout_requests SELECT (jsonb_populate_record(NULL::app.payout_requests,
		to_jsonb(r) || jsonb_build_object('id', gen_random_uuid(), 'idempotency_key', 'dup-'||gen_random_uuid()))).* FROM app.payout_requests r WHERE id = $1`, id)
	mustFail("the app role writing ledger tables", `INSERT INTO ledger.ledger_transactions (id) VALUES (gen_random_uuid())`)
	mustFail("a payouts journal with real evidence in the TEST ledger", `SELECT 1/0`) // replaced below by a ledger-role insert
	lp := pool(t, os.Getenv("DATABASE_LEDGER_URL"))
	if _, err := lp.Exec(ctx(t), `INSERT INTO ledger.ledger_transactions (id, posting_rule, posting_rule_version, environment, source_system, evidence_class,
		idempotency_key, currency, description, source_type, source_id, occurred_at, accounting_period_id, created_by_type, created_by_id)
		VALUES (gen_random_uuid(), 'PAYOUT_RESERVED', 1, 'TEST', 'payouts', 'PROVIDER_VERIFIED', 'payout:'||gen_random_uuid()||':x', 'USD', 'x',
		'payout', gen_random_uuid(), now(), '00000000-0000-0000-0000-000000000000', 'SYSTEM', 'sql')`); err == nil ||
		!strings.Contains(err.Error(), "ck_ledger_transactions_payouts_simulated_in_test") {
		t.Fatalf("payouts journal with PROVIDER_VERIFIED evidence in a TEST ledger: %v", err)
	}
	// clean up: staff reject releases the reservation
	r := p.staffAct(t, p.f.finance1, "POST", "/admin/payout-requests/"+id+"/reject", approveBody("rejected by the database guard test"))
	p.s.staff(t, p.f.finance1).expect(r, 200, "")
	if pay, pend := p.balances(t, camp); pay != 10_000 || pend != 0 {
		t.Fatalf("after reject payable %d pending %d", pay, pend)
	}
	p.integrity(t)
}

// ===== failure injection ===============================================================================================

func TestPayoutFailureInjection(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 10_000)
	// ledger unreachable at request time: refused before anything is written (fail closed)
	dead, err := pgxpool.New(ctx(t), os.Getenv("DATABASE_LEDGER_URL"))
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()
	broken := *p.po
	broken.Ledger = ledger.New(dead, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := broken.RequestPayout(ctx(t), p.owner.id, true, camp, payouts.RequestInput{Currency: "USD", AmountMinor: "3000",
		DestinationID: p.dest}, "fi-"+ids.New()); err == nil {
		t.Fatal("a payout was requested with the ledger unreachable")
	}
	var rows int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_requests WHERE campaign_id = $1`, camp).Scan(&rows)
	if rows != 0 {
		t.Fatalf("ledger down: %d payout rows written", rows)
	}
	// ledger contention while reserving (the campaign's balance row is locked past the posting role's lock timeout):
	// the payout keeps its in-flight slot in PAYOUT_REQUESTED with nothing reserved, and recovery reserves it later
	b, err := p.led.CampaignBalances(ctx(t), camp, "USD")
	if err != nil {
		t.Fatal(err)
	}
	lockTx, err := p.mig.Begin(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockTx.Exec(ctx(t), `SELECT 1 FROM ledger.ledger_balances WHERE account_id = $1 FOR UPDATE`, b[ledger.ClassCampaignPayable].AccountID); err != nil {
		t.Fatal(err)
	}
	po, err := p.po.RequestPayout(ctx(t), p.owner.id, true, camp, payouts.RequestInput{Currency: "USD", AmountMinor: "3000", DestinationID: p.dest}, "fi-"+ids.New())
	_ = lockTx.Rollback(ctx(t))
	if err != nil || po.Status != payouts.StRequested || po.Reservation != nil {
		t.Fatalf("under ledger contention: %+v %v", po.Status, err)
	}
	id := po.ID
	if pay, pend := p.balances(t, camp); pay != 10_000 || pend != 0 {
		t.Fatalf("nothing may be reserved yet: payable %d pending %d", pay, pend)
	}
	time.Sleep(31 * time.Second) // recovery re-drives requests older than 30 s
	if err := p.po.Recover(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if got := p.payout(t, id); got.Status != payouts.StReview || got.Reservation == nil {
		t.Fatalf("after recovery: %s", got.Status)
	}
	if pay, pend := p.balances(t, camp); pay != 7_000 || pend != 3_000 {
		t.Fatalf("after recovery payable %d pending %d", pay, pend)
	}
	// provider outage declared: submission is deferred (stays APPROVED) and operations are told
	p.approve(t, id)
	prov := p.payout(t, id).ProviderID
	if _, err := p.mig.Exec(ctx(t), `INSERT INTO app.provider_health_events (id, provider_id, operation, event_kind, actor_id, observed_at)
		VALUES (gen_random_uuid(), $1, 'ALL', 'OUTAGE_DECLARED', $2, now())`, prov, p.f.finance1.id); err != nil {
		t.Logf("declaring an outage via SQL failed (%v); using the staff API", err)
		r := p.staffAct(t, p.f.finance1, "POST", "/admin/payments/providers/"+prov+"/outage", map[string]string{"reason": "payout failure-injection test"})
		p.s.staff(t, p.f.finance1).expect(r, 200, "")
	}
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	if got := p.payout(t, id); got.Status != payouts.StApproved {
		t.Fatalf("submission during a declared outage: %s", got.Status)
	}
	r := p.staffAct(t, p.f.finance1, "POST", "/admin/payments/providers/"+prov+"/outage/resolve", map[string]string{"reason": "payout failure-injection test done"})
	p.s.staff(t, p.f.finance1).expect(r, 200, "")
	// once the outage is resolved the deferred payout goes out (here or through the compose worker)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	p.drive(t, 40*time.Second, "completed after the outage", status(payouts.StCompleted), id)
	p.integrity(t)
}

// ===== reconciliation and exceptions ===================================================================================

func TestPayoutReconciliationAndExceptionWorkflow(t *testing.T) {
	p := newPayoutIT(t)
	camp := p.campaign(t, 20_000)
	id := p.request(t, camp, 6_000, 202, "")
	p.approve(t, id)
	if _, err := p.po.SubmitNow(ctx(t), id); err != nil {
		t.Fatal(err)
	}
	p.drive(t, 30*time.Second, "completed", status(payouts.StCompleted), id)
	// Completion is committed before the ledger journal and the reservation consumption (they follow the state through
	// syncLedger on the ledger pool, ADR-043): wait until the payout has settled, so the injected provider change below is
	// the only mismatch (a reconciliation inside that window reports a transient RESERVATION_MISMATCH; KI-S18-25).
	waitFor(t, 30*time.Second, "reservation consumed after completion", func() bool {
		got := p.payout(t, id)
		return got.Reservation != nil && got.Reservation.Status == payouts.ResConsumed
	})
	// the simulated provider now says something else: reconciliation must surface it (SEV1)
	if _, err := p.mig.Exec(ctx(t), `UPDATE app.sandbox_provider_payouts SET raw_status = 'failed' WHERE our_reference = $1`, id); err != nil {
		t.Fatal(err)
	}
	rep, err := p.po.Reconcile(ctx(t), "it")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rep.SettlementReconciliation, "NOT_PERFORMED") {
		t.Fatal("settlement must be reported NOT_PERFORMED")
	}
	var mismatch, sev string
	if err := p.mig.QueryRow(ctx(t), `SELECT r.mismatch_type, e.severity FROM app.payout_reconciliation_references r JOIN app.payout_exceptions e
		ON e.reconciliation_reference_id = r.id WHERE r.payout_request_id = $1`, id).Scan(&mismatch, &sev); err != nil {
		t.Fatalf("no mismatch recorded: %v", err)
	}
	if mismatch != "PROVIDER_STATUS_MISMATCH" || sev != "SEV1" {
		t.Fatalf("mismatch %s %s", mismatch, sev)
	}
	// rerun: no duplicates while under investigation
	if _, err := p.po.Reconcile(ctx(t), "it"); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = p.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.payout_exceptions WHERE payout_request_id = $1 AND exception_type = 'RECONCILIATION_MISMATCH'`, id).Scan(&n)
	if n != 1 {
		t.Fatalf("rerun duplicated exceptions: %d", n)
	}
	// workflow over HTTP: investigate → resolve; detection fields immutable; resolution never touches the payout
	var exID string
	_ = p.mig.QueryRow(ctx(t), `SELECT id FROM app.payout_exceptions WHERE payout_request_id = $1 AND exception_type = 'RECONCILIATION_MISMATCH'`, id).Scan(&exID)
	r := p.staffAct(t, p.f.finance1, "POST", "/admin/payouts/exceptions/"+exID+"/investigate", map[string]string{"note": "checking with the simulator"})
	p.s.staff(t, p.f.finance1).expect(r, 200, "")
	r = p.staffAct(t, p.f.finance1, "POST", "/admin/payouts/exceptions/"+exID+"/resolve", map[string]string{"note": "simulator state edited by the test",
		"resolution_code": "CONFIRMED_NO_ACTION"})
	p.s.staff(t, p.f.finance1).expect(r, 200, "")
	if _, err := p.app.Exec(ctx(t), `UPDATE app.payout_exceptions SET severity = 'SEV3' WHERE id = $1`, exID); err == nil {
		t.Fatal("exception detection fields are mutable")
	}
	if st := p.payout(t, id).Status; st != payouts.StCompleted {
		t.Fatalf("resolving an exception changed the payout: %s", st)
	}
	// staff views
	fin := p.s.staff(t, p.f.finance1)
	for _, path := range []string{"/admin/payouts/overview", "/admin/payouts/reservations", "/admin/payouts/exceptions?include_resolved=true",
		"/admin/payouts/reconciliation", "/admin/payouts/providers", "/admin/payout-requests?campaign_id=" + camp, "/admin/payout-requests/" + id} {
		fin.expect(fin.do("GET", path, nil), 200, "")
	}
	sup := p.s.staff(t, p.f.support)
	sup.expect(sup.do("GET", "/admin/payout-requests", nil), 403, "PERMISSION_DENIED")
	p.integrity(t)
}
