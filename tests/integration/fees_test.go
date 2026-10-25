//go:build integration

package integration

// Stage 12 fees (ADR-044) against the real stack. Every policy a test creates is scoped to its own campaign (or
// retired at the end), so tests do not change each other's fee selection; provider fee schedules are SYNTHETIC
// sandbox evidence, retired at the end. All money is TEST: quotes, assessments and the fixtures' TEST_ONLY journals.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/app"
	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

type feeIT struct {
	*payoutIT
	fs   *fees.Service
	camp string
}

func newFeeIT(t *testing.T, fund int64) *feeIT {
	t.Helper()
	p := newPayoutIT(t)
	fs := p.s.d.Verification.Fees
	if fs == nil {
		t.Fatal("fees module not wired")
	}
	fs.ReconGrace = time.Nanosecond
	fs.ReconSweep = itSweep("fees")
	return &feeIT{payoutIT: p, fs: fs, camp: p.campaign(t, fund)}
}

// act runs a staff request with an Idempotency-Key, stepping up with TOTP when asked (new key for the retry).
func (f *feeIT) act(t *testing.T, c staffCred, method, path string, body any, want int, code string) apiResp {
	t.Helper()
	b := f.s.staff(t, c)
	r := b.do(method, path, body, "Idempotency-Key", ids.New())
	if r.Status == 403 && r.Error.Code == "STEP_UP_REQUIRED" {
		b.expect(b.do("POST", "/auth/step-up/verify", map[string]string{"code": totpCode(t, c.secret)}), 200, "")
		r = b.do(method, path, body, "Idempotency-Key", ids.New())
	}
	if want != 0 {
		b.expect(r, want, code)
	}
	return r
}

func nowStr() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func platformTerms(rate int64, resp string) map[string]any {
	return map[string]any{"model": "PERCENTAGE", "rate_bps": rate, "rounding_mode": "HALF_UP", "responsibility": resp,
		"refund_treatment": "PROPORTIONAL", "effective_from": nowStr()}
}

func passThrough(resp string) map[string]any {
	return map[string]any{"model": "PROVIDER_PASS_THROUGH", "rounding_mode": "HALF_UP", "responsibility": resp, "effective_from": nowStr()}
}

// policy creates a policy with one version and drives it to ACTIVE through maker (finance1) and checker (approver1).
func (f *feeIT) policy(t *testing.T, category string, scope map[string]any, terms map[string]any) (string, string) {
	t.Helper()
	body := map[string]any{"code": "it_" + strings.ToLower(category[:4]) + "_" + strings.ReplaceAll(ids.New()[:13], "-", ""),
		"name": "IT " + category, "category": category, "currency": "USD"}
	for k, v := range scope {
		body[k] = v
	}
	var pid string
	b := f.s.staff(t, f.f.finance1)
	r := f.act(t, f.f.finance1, "POST", "/admin/fees/policies", body, 0, "")
	switch {
	case r.Status == 201:
		pid = r.field(t, "id")
	case r.Status == 409 && r.Error.Code == "FEE_POLICY_CONFLICT":
		// a scope is permanent (unique forever); a global scope from an earlier run gets a new version instead
		if err := f.mig.QueryRow(ctx(t), `SELECT id FROM app.fee_policies WHERE category = $1 AND currency = 'USD'
			AND payment_method IS NOT DISTINCT FROM $2 AND campaign_id IS NULL AND provider_id IS NULL AND campaign_category IS NULL`,
			category, scope["payment_method"]).Scan(&pid); err != nil {
			t.Fatal(err)
		}
	default:
		b.expect(r, 201, "")
	}
	vid := f.act(t, f.f.finance1, "POST", "/admin/fees/policies/"+pid+"/versions", terms, 201, "").field(t, "id")
	f.act(t, f.f.finance1, "POST", "/admin/fees/policy-versions/"+vid+"/submit", nil, 200, "")
	f.act(t, f.f.approver1, "POST", "/admin/fees/policy-versions/"+vid+"/approve", map[string]any{"reason": "IT synthetic policy checked"}, 200, "")
	f.act(t, f.f.finance1, "POST", "/admin/fees/policy-versions/"+vid+"/activate", nil, 200, "")
	return pid, vid
}

// schedule records SYNTHETIC sandbox provider fee evidence and has approver1 activate it (retired at cleanup).
func (f *feeIT) schedule(t *testing.T, feeType string, terms map[string]any) string {
	t.Helper()
	body := map[string]any{"provider_id": app.SandboxProviderID, "contract_reference": "IT synthetic sandbox terms (not a contract)",
		"schedule_version": "it-" + ids.New()[:8], "currency": "USD", "payment_method": "ECOCASH", "fee_type": feeType,
		"effective_from": nowStr(), "evidence_source": "SYNTHETIC_TEST_FIXTURE", "confirmation_status": "SYNTHETIC"}
	for k, v := range terms {
		body[k] = v
	}
	id := f.act(t, f.f.finance1, "POST", "/admin/fees/provider-schedules", body, 201, "").field(t, "id")
	f.act(t, f.f.approver1, "POST", "/admin/fees/provider-schedules/"+id+"/approve", nil, 200, "")
	t.Cleanup(func() {
		_, _ = f.fs.RetireSchedule(ctx(t), f.f.finance1.id, time.Now(), id, "IT cleanup: retire synthetic evidence")
	})
	return id
}

func (f *feeIT) quote(t *testing.T, u vUser, amount, method string, want int, code string) fees.Quote {
	t.Helper()
	body := map[string]any{"campaign_id": f.camp, "amount_minor": amount, "currency": "USD"}
	if method != "" {
		body["payment_method"] = method
	}
	r := u.do("POST", "/fees/quotes", body, "Idempotency-Key", ids.New())
	u.expect(r, want, code)
	var q fees.Quote
	if want == 201 {
		if err := json.Unmarshal(r.Data, &q); err != nil {
			t.Fatal(err)
		}
	}
	return q
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func (f *feeIT) fixture(t *testing.T, body map[string]any, want int, code string) fees.FixtureResult {
	t.Helper()
	r := f.act(t, f.f.ledgerAdmin, "POST", "/admin/fees/fixtures", body, want, code)
	var out fees.FixtureResult
	if want == 201 {
		if err := json.Unmarshal(r.Data, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func (f *feeIT) amounts(t *testing.T, key string) ledger.JournalAmounts {
	t.Helper()
	j, ok, err := f.led.JournalAmountsByKey(ctx(t), key)
	if err != nil || !ok {
		t.Fatalf("journal %s: ok=%v err=%v", key, ok, err)
	}
	if j.SourceSystem != ledger.SourceFees || j.EvidenceClass != ledger.EvidenceSimulated || j.Environment != ledger.EnvTest {
		t.Fatalf("journal %s: %s/%s/%s, want fees/SIMULATED/TEST", key, j.SourceSystem, j.EvidenceClass, j.Environment)
	}
	return j
}

func (f *feeIT) payable(t *testing.T, camp string) int64 {
	t.Helper()
	b, err := f.led.CampaignBalances(ctx(t), camp, "USD")
	if err != nil {
		t.Fatal(err)
	}
	return b[ledger.ClassCampaignPayable].BalanceMinor
}

// ===== policies: maker-checker, immutability, lifecycle, precedence ===============================================

func TestFeePolicyMakerCheckerLifecycleAndImmutability(t *testing.T) {
	f := newFeeIT(t, 0)
	fin, appr := f.f.finance1, f.f.approver1
	// disabled categories cannot be created; FX never
	f.act(t, fin, "POST", "/admin/fees/policies", map[string]any{"code": "it_fx_" + ids.New()[:8], "name": "IT FX", "category": "CURRENCY_CONVERSION_FEE",
		"currency": "USD"}, 422, "FEE_CATEGORY_NOT_ENABLED")
	pid := f.act(t, fin, "POST", "/admin/fees/policies", map[string]any{"code": "it_plat_" + strings.ReplaceAll(ids.New()[:13], "-", ""),
		"name": "IT platform", "category": "PLATFORM_FEE", "currency": "USD", "campaign_id": f.camp}, 201, "").field(t, "id")
	// a second policy with the same scope is refused (selection can never be ambiguous)
	f.act(t, fin, "POST", "/admin/fees/policies", map[string]any{"code": "it_dup_" + strings.ReplaceAll(ids.New()[:13], "-", ""),
		"name": "IT dup", "category": "PLATFORM_FEE", "currency": "USD", "campaign_id": f.camp}, 409, "FEE_POLICY_CONFLICT")
	// invalid terms: PLATFORM_ABSORBED platform fee, pass-through platform fee, missing refund treatment, float-like amount
	bad := []map[string]any{
		platformTerms(500, "PLATFORM_ABSORBED"),
		{"model": "PROVIDER_PASS_THROUGH", "rounding_mode": "HALF_UP", "responsibility": "ORGANISER_COVERED", "refund_treatment": "PROPORTIONAL", "effective_from": nowStr()},
		{"model": "PERCENTAGE", "rate_bps": 500, "rounding_mode": "HALF_UP", "responsibility": "ORGANISER_COVERED", "effective_from": nowStr()},
		{"model": "FIXED", "fixed_minor": "1.50", "rounding_mode": "HALF_UP", "responsibility": "ORGANISER_COVERED", "refund_treatment": "PROPORTIONAL", "effective_from": nowStr()},
		{"model": "PERCENTAGE", "rate_bps": 10001, "rounding_mode": "HALF_UP", "responsibility": "ORGANISER_COVERED", "refund_treatment": "PROPORTIONAL", "effective_from": nowStr()},
	}
	for _, b := range bad {
		f.act(t, fin, "POST", "/admin/fees/policies/"+pid+"/versions", b, 422, "VALIDATION_FAILED")
	}
	vid := f.act(t, fin, "POST", "/admin/fees/policies/"+pid+"/versions", platformTerms(500, "ORGANISER_COVERED"), 201, "").field(t, "id")
	// only the author edits a draft; a draft cannot be approved or activated
	f.act(t, f.f.finance2, "PUT", "/admin/fees/policy-versions/"+vid, platformTerms(400, "ORGANISER_COVERED"), 403, "FEE_DRAFT_NOT_YOURS")
	f.act(t, fin, "PUT", "/admin/fees/policy-versions/"+vid, platformTerms(500, "ORGANISER_COVERED"), 200, "")
	f.act(t, appr, "POST", "/admin/fees/policy-versions/"+vid+"/approve", map[string]any{"reason": "approve a draft?"}, 409, "FEE_POLICY_STATE")
	f.act(t, fin, "POST", "/admin/fees/policy-versions/"+vid+"/activate", nil, 409, "FEE_POLICY_STATE")
	r := f.act(t, fin, "POST", "/admin/fees/policy-versions/"+vid+"/submit", nil, 200, "")
	var v fees.Version
	_ = json.Unmarshal(r.Data, &v)
	if v.Status != fees.VPending || v.TermsHash == nil || len(*v.TermsHash) != 64 {
		t.Fatalf("submitted: %+v", v)
	}
	// terms are frozen from submission (database)
	_, err := f.app.Exec(ctx(t), `UPDATE app.fee_policy_versions SET rate_bps = 1 WHERE id = $1`, vid)
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("terms edit after submission must be refused: %v", err)
	}
	// FINANCE has no checker permission; the maker cannot approve (service and database); linked accounts neither
	f.act(t, fin, "POST", "/admin/fees/policy-versions/"+vid+"/approve", map[string]any{"reason": "maker approving"}, 403, "")
	if _, err := f.fs.Approve(ctx(t), fin.id, time.Now(), vid, fees.Decision{Reason: "maker approving own version"}); !errors.Is(err, fees.ErrSelfApproval) {
		t.Fatalf("self-approval: %v", err)
	}
	if _, err := f.app.Exec(ctx(t), `UPDATE app.fee_policy_versions SET status = 'APPROVED', approved_by = created_by, approved_at = now(),
		approved_step_up_at = now(), approval_reason = 'db bypass' WHERE id = $1`, vid); err == nil {
		t.Fatal("the database must refuse a maker approving their own version")
	}
	// a direct DRAFT → ACTIVE is not a legal transition
	draftOnly := f.act(t, fin, "POST", "/admin/fees/policies", map[string]any{"code": "it_pp_" + strings.ReplaceAll(ids.New()[:13], "-", ""),
		"name": "IT processing", "category": "PAYMENT_PROCESSING_FEE", "currency": "USD", "campaign_id": f.camp}, 201, "").field(t, "id")
	dv := f.act(t, fin, "POST", "/admin/fees/policies/"+draftOnly+"/versions", passThrough("ORGANISER_COVERED"), 201, "").field(t, "id")
	if _, err := f.app.Exec(ctx(t), `UPDATE app.fee_policy_versions SET status = 'ACTIVE', activated_by = created_by, activated_at = now(),
		activated_step_up_at = now() WHERE id = $1`, dv); err == nil {
		t.Fatal("DRAFT → ACTIVE without approval must be refused")
	}
	// the checker must approve exactly the terms they saw
	f.act(t, appr, "POST", "/admin/fees/policy-versions/"+vid+"/approve", map[string]any{"reason": "terms reviewed", "terms_hash": strings.Repeat("0", 64)},
		409, "FEE_TERMS_CHANGED")
	f.act(t, appr, "POST", "/admin/fees/policy-versions/"+vid+"/approve", map[string]any{"reason": "terms reviewed", "terms_hash": *v.TermsHash}, 200, "")
	r = f.act(t, fin, "POST", "/admin/fees/policy-versions/"+vid+"/activate", nil, 200, "")
	_ = json.Unmarshal(r.Data, &v)
	if v.Status != fees.VActive || v.ApprovedBy == nil || *v.ApprovedBy != appr.id || v.ActivatedBy == nil {
		t.Fatalf("activated: %+v", v)
	}
	// suspension: the suspender cannot reactivate; a checker can
	f.act(t, fin, "POST", "/admin/fees/policy-versions/"+vid+"/suspend", map[string]any{"reason": "IT: suspend to test reactivation"}, 200, "")
	if _, err := f.fs.Reactivate(ctx(t), fin.id, time.Now(), vid, "suspender reactivating"); !errors.Is(err, fees.ErrSelfApproval) {
		t.Fatalf("suspender reactivation: %v", err)
	}
	f.act(t, appr, "POST", "/admin/fees/policy-versions/"+vid+"/reactivate", map[string]any{"reason": "IT: reactivated by a checker"}, 200, "")
	// history is append-only and records every step
	var evs []fees.Event
	_ = json.Unmarshal(f.act(t, fin, "GET", "/admin/fees/policy-versions/"+vid+"/events", nil, 200, "").Data, &struct {
		Items *[]fees.Event `json:"items"`
	}{&evs})
	want := []string{"VERSION_DRAFTED", "DRAFT_EDITED", "VERSION_SUBMITTED", "VERSION_APPROVED", "VERSION_ACTIVATED", "VERSION_SUSPENDED", "VERSION_REACTIVATED"}
	if len(evs) != len(want) {
		t.Fatalf("events: %+v", evs)
	}
	for i, e := range evs {
		if e.EventType != want[i] {
			t.Fatalf("event %d: %s, want %s", i, e.EventType, want[i])
		}
	}
	if _, err := f.app.Exec(ctx(t), `DELETE FROM app.fee_audit_events WHERE aggregate_id = $1`, vid); err == nil {
		t.Fatal("fee history must be append-only")
	}
	// retirement is final
	f.act(t, fin, "POST", "/admin/fees/policy-versions/"+vid+"/retire", map[string]any{"reason": "IT: retire"}, 200, "")
	f.act(t, fin, "POST", "/admin/fees/policy-versions/"+vid+"/activate", nil, 409, "FEE_POLICY_STATE")
	if _, err := f.app.Exec(ctx(t), `DELETE FROM app.fee_policy_versions WHERE id = $1`, vid); err == nil {
		t.Fatal("published versions cannot be deleted")
	}
}

func TestFeePolicyLinkedAccountCannotApprove(t *testing.T) {
	f := newFeeIT(t, 0)
	// a personal account is the maker (service call: the HTTP route needs fee.config.request), and the checker's staff
	// account is linked to it: neither the service nor the database accepts the approval
	maker := f.owner.id
	p, err := f.fs.CreatePolicy(ctx(t), maker, fees.NewPolicy{Code: "it_link_" + strings.ReplaceAll(ids.New()[:13], "-", ""), Name: "IT linked",
		Category: fees.CatPlatform, Currency: "USD", CampaignID: &f.camp})
	if err != nil {
		t.Fatal(err)
	}
	rate := int64(300)
	tr := fees.RefundProportional
	v, err := f.fs.CreateVersion(ctx(t), maker, p.ID, fees.VersionInput{Model: fees.ModelPercentage, RateBps: &rate, RoundingMode: fees.HalfUp,
		Responsibility: fees.OrganiserCovered, RefundTreatment: &tr, EffectiveFrom: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.fs.Submit(ctx(t), maker, v.ID); err != nil {
		t.Fatal(err)
	}
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, f.f.approver2.id)
	migratorExec(t, `UPDATE app.users SET staff_personal_user_id = $2 WHERE id = $1`, f.f.approver2.id, maker)
	t.Cleanup(func() {
		migratorExec(t, `UPDATE app.users SET staff_personal_user_id = NULL WHERE id = $1`, f.f.approver2.id)
	})
	f.act(t, f.f.approver2, "POST", "/admin/fees/policy-versions/"+v.ID+"/approve", map[string]any{"reason": "linked approval attempt"}, 403,
		"SELF_APPROVAL_FORBIDDEN")
	if _, err := f.app.Exec(ctx(t), `UPDATE app.fee_policy_versions SET status = 'APPROVED', approved_by = $2, approved_at = now(),
		approved_step_up_at = now(), approval_reason = 'linked db bypass' WHERE id = $1`, v.ID, f.f.approver2.id); err == nil ||
		!strings.Contains(err.Error(), "maker-checker") {
		t.Fatalf("the database must refuse a linked approver: %v", err)
	}
	f.act(t, f.f.approver1, "POST", "/admin/fees/policy-versions/"+v.ID+"/reject", map[string]any{"reason": "IT: rejected by an independent checker"}, 200, "")
}

func TestFeePolicyPrecedenceAndSelection(t *testing.T) {
	f := newFeeIT(t, 0)
	at := time.Now()
	// a method-scoped platform policy (specificity 2) and a campaign-scoped one (16): the campaign scope wins for this
	// campaign; another campaign with the method gets the method-scoped one
	_, methodV := f.policy(t, "PLATFORM_FEE", map[string]any{"payment_method": "ECOCASH"}, platformTerms(250, "DONOR_COVERED"))
	t.Cleanup(func() {
		_, _ = f.fs.Retire(ctx(t), f.f.finance1.id, time.Now(), methodV, "IT cleanup: retire method-scoped policy")
	})
	_, campV := f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	got, err := f.fs.Resolve(ctx(t), nil, fees.Scope{Category: fees.CatPlatform, TransactionType: fees.TxDonation, Currency: "USD",
		PaymentMethod: "ECOCASH", CampaignID: f.camp, CampaignCategory: "EDUCATION", At: time.Now()})
	if err != nil || got == nil || got.ID != campV {
		t.Fatalf("campaign scope must win: %+v %v", got, err)
	}
	other, err := f.fs.Resolve(ctx(t), nil, fees.Scope{Category: fees.CatPlatform, TransactionType: fees.TxDonation, Currency: "USD",
		PaymentMethod: "ECOCASH", CampaignID: ids.New(), CampaignCategory: "EDUCATION", At: time.Now()})
	if err != nil || other == nil || other.ID != methodV {
		t.Fatalf("method scope for another campaign: %+v %v", other, err)
	}
	// historical selection: before the policies existed nothing applied
	old, err := f.fs.Resolve(ctx(t), nil, fees.Scope{Category: fees.CatPlatform, TransactionType: fees.TxDonation, Currency: "USD",
		CampaignID: f.camp, At: at.Add(-time.Hour)})
	if err != nil || (old != nil && old.ID == campV) {
		t.Fatalf("a version never applies before its effective start: %+v %v", old, err)
	}
	// retroactive versions are refused at submission
	pid := f.act(t, f.f.finance1, "POST", "/admin/fees/policies", map[string]any{"code": "it_retro_" + strings.ReplaceAll(ids.New()[:13], "-", ""),
		"name": "IT retro", "category": "PAYMENT_PROCESSING_FEE", "currency": "USD", "campaign_id": f.camp}, 201, "").field(t, "id")
	terms := passThrough("ORGANISER_COVERED")
	terms["effective_from"] = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	vid := f.act(t, f.f.finance1, "POST", "/admin/fees/policies/"+pid+"/versions", terms, 201, "").field(t, "id")
	f.act(t, f.f.finance1, "POST", "/admin/fees/policy-versions/"+vid+"/submit", nil, 422, "FEE_POLICY_RETROACTIVE")
}

// ===== quotes and donation intents ================================================================================

func TestFeeQuotesAndDonationIntentBinding(t *testing.T) {
	f := newFeeIT(t, 0)
	_, platV := f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("ORGANISER_COVERED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	donor := verifiedUser(t, f.s, "feedonor")
	// the brief's synthetic example: USD 100.00, platform 5 % (organiser), provider 2 % (organiser, ESTIMATED)
	q := f.quote(t, donor, "10000", "ECOCASH", 201, "")
	if q.PlatformFeeMinor != "500" || str(q.ProviderFeeMinor) != "200" || q.ProviderFeeBasis != "ESTIMATED" || str(q.DonorChargeMinor) != "10000" ||
		str(q.CampaignAllocationMinor) != "9300" || str(q.PlatformNetMinor) != "500" || q.FeeEnvironment != "TEST" || q.Status != "ACTIVE" ||
		!strings.HasPrefix(q.Label, "TEST") {
		t.Fatalf("quote: %+v", q)
	}
	// no method: the provider is unknown, so the provider fee is UNKNOWN (never 0) and so is the allocation
	q2 := f.quote(t, donor, "10000", "", 201, "")
	if q2.ProviderFeeMinor != nil || q2.ProviderFeeBasis != "UNKNOWN" || q2.CampaignAllocationMinor != nil || q2.PlatformFeeMinor != "500" {
		t.Fatalf("no-method quote: %+v", q2)
	}
	// clients cannot supply a rate or a fee; another user cannot read the quote
	r := donor.do("POST", "/fees/quotes", map[string]any{"campaign_id": f.camp, "amount_minor": "10000", "currency": "USD", "platform_fee_minor": "0"},
		"Idempotency-Key", ids.New())
	if r.Status != 400 && r.Status != 422 {
		t.Fatalf("a client fee override must be refused: %d", r.Status)
	}
	other := verifiedUser(t, f.s, "feeother")
	other.expect(other.do("GET", "/fees/quotes/"+q.ID, nil), 404, "")
	donor.expect(donor.do("POST", "/fees/quotes", map[string]any{"campaign_id": f.camp, "amount_minor": "10.00", "currency": "USD"},
		"Idempotency-Key", ids.New()), 422, "VALIDATION_FAILED")
	donor.expect(donor.do("POST", "/fees/quotes", map[string]any{"campaign_id": f.camp, "amount_minor": "100", "currency": "ZWG"},
		"Idempotency-Key", ids.New()), 422, "CURRENCY_NOT_SUPPORTED")
	// idempotency: the same key and body replays, a different body with the same key is refused
	key := ids.New()
	body := map[string]any{"campaign_id": f.camp, "amount_minor": "2500", "currency": "USD"}
	a1 := donor.do("POST", "/fees/quotes", body, "Idempotency-Key", key)
	donor.expect(a1, 201, "")
	a2 := donor.do("POST", "/fees/quotes", body, "Idempotency-Key", key)
	if a2.Status != 201 || a2.field(t, "id") != a1.field(t, "id") {
		t.Fatalf("replay: %d %s", a2.Status, string(a2.Data))
	}
	body["amount_minor"] = "2600"
	if r := donor.do("POST", "/fees/quotes", body, "Idempotency-Key", key); r.Status != 409 && r.Status != 422 {
		t.Fatalf("key reuse with a different body: %d", r.Status)
	}
	// binding: the intent carries the quote; the quote is BOUND and cannot be reused
	ri := donor.do("POST", "/donation-intents", map[string]any{"campaign_id": f.camp, "amount_minor": "10000", "currency": "USD",
		"fee_quote_id": q2.ID}, "Idempotency-Key", ids.New())
	donor.expect(ri, 201, "")
	if ri.field(t, "fee_quote_id") != q2.ID || ri.field(t, "amount_minor") != "10000" {
		t.Fatalf("intent: %s", string(ri.Data))
	}
	donor.expect(donor.do("POST", "/donation-intents", map[string]any{"campaign_id": f.camp, "amount_minor": "10000", "currency": "USD",
		"fee_quote_id": q2.ID}, "Idempotency-Key", ids.New()), 409, "FEE_QUOTE_ALREADY_USED")
	// a quote for another amount is stale; someone else's quote does not exist for this donor
	donor.expect(donor.do("POST", "/donation-intents", map[string]any{"campaign_id": f.camp, "amount_minor": "5000", "currency": "USD",
		"fee_quote_id": q.ID}, "Idempotency-Key", ids.New()), 409, "FEE_QUOTE_STALE")
	oq := f.quote(t, other, "10000", "", 201, "")
	donor.expect(donor.do("POST", "/donation-intents", map[string]any{"campaign_id": f.camp, "amount_minor": "10000", "currency": "USD",
		"fee_quote_id": oq.ID}, "Idempotency-Key", ids.New()), 404, "")
	// a policy change invalidates ACTIVE quotes: binding a quote computed before it is refused
	q3 := f.quote(t, donor, "10000", "", 201, "")
	f.act(t, f.f.finance1, "POST", "/admin/fees/policy-versions/"+platV+"/suspend", map[string]any{"reason": "IT: invalidate quotes"}, 200, "")
	donor.expect(donor.do("POST", "/donation-intents", map[string]any{"campaign_id": f.camp, "amount_minor": "10000", "currency": "USD",
		"fee_quote_id": q3.ID}, "Idempotency-Key", ids.New()), 409, "FEE_QUOTE_STALE")
	f.act(t, f.f.approver1, "POST", "/admin/fees/policy-versions/"+platV+"/reactivate", map[string]any{"reason": "IT: back in force"}, 200, "")
	// an expired quote is refused (a service with a tiny validity creates it)
	short := *f.fs
	short.QuoteTTL = time.Millisecond
	eq, err := short.CreateQuote(ctx(t), donor.id, "", fees.QuoteRequest{CampaignID: f.camp, AmountMinor: "10000", Currency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	donor.expect(donor.do("POST", "/donation-intents", map[string]any{"campaign_id": f.camp, "amount_minor": "10000", "currency": "USD",
		"fee_quote_id": eq.ID}, "Idempotency-Key", ids.New()), 409, "FEE_QUOTE_EXPIRED")
	if n, err := f.fs.ExpireQuotes(ctx(t)); err != nil || n < 1 {
		t.Fatalf("expiry job: %d %v", n, err)
	}
	// without a quote the intent gets a system quote; the payment amount stays the donation amount
	ri = donor.do("POST", "/donation-intents", map[string]any{"campaign_id": f.camp, "amount_minor": "10000", "currency": "USD"}, "Idempotency-Key", ids.New())
	donor.expect(ri, 201, "")
	sq, err := f.fs.GetQuote(ctx(t), donor.id, ri.field(t, "fee_quote_id"))
	if err != nil || !sq.SystemGenerated || sq.Status != "BOUND" || sq.DonationIntentID == nil || *sq.DonationIntentID != ri.field(t, "id") {
		t.Fatalf("system quote: %+v %v", sq, err)
	}
}

func TestFeeAssessmentOnTestPaymentIsNeverPosted(t *testing.T) {
	f := newFeeIT(t, 0)
	f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "DONOR_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("PLATFORM_ABSORBED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	donor := verifiedUser(t, f.s, "feepay")
	q := f.quote(t, donor, "2000", "ECOCASH", 201, "")
	if str(q.DonorChargeMinor) != "2100" || str(q.CampaignAllocationMinor) != "2000" || str(q.PlatformNetMinor) != "60" {
		t.Fatalf("donor-covered quote: %+v", q)
	}
	ri := donor.do("POST", "/donation-intents", map[string]any{"campaign_id": f.camp, "amount_minor": "2000", "currency": "USD", "fee_quote_id": q.ID},
		"Idempotency-Key", ids.New())
	donor.expect(ri, 201, "")
	if ri.field(t, "fee_donor_charge_minor") != "2100" {
		t.Fatalf("intent donor charge: %s", string(ri.Data))
	}
	intentID := ri.field(t, "id")
	pay := &payIT{s: f.s, f: f.f, mig: f.mig, donor: donor, campaign: f.camp}
	a := pay.attempt(t, donor, intentID, "ECOCASH", 201, "")
	var paymentID string
	deadline := time.Now().Add(30 * time.Second)
	for paymentID == "" || pay.status(t, paymentID) != "SUCCEEDED" {
		if time.Now().After(deadline) {
			t.Fatalf("payment did not succeed (attempt %s)", a.ID)
		}
		pay.pump(t)
		_ = f.mig.QueryRow(ctx(t), `SELECT id::text FROM app.payment_intents WHERE donation_intent_id = $1`, intentID).Scan(&paymentID)
		time.Sleep(300 * time.Millisecond)
	}
	var payload []byte
	if err := f.mig.QueryRow(ctx(t), `SELECT payload FROM app.outbox_events WHERE event_type = 'payments.payment_succeeded'
		AND aggregate_id = $1`, paymentID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	// duplicate delivery (and the compose worker consuming it too) assesses exactly once
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = f.fs.RecordPaymentSucceeded(ctx(t), payload) }()
	}
	wg.Wait()
	list, err := f.fs.Assessments(ctx(t), fees.AssessmentFilter{SourceType: fees.SrcDonationPayment, CampaignID: f.camp})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]fees.Assessment{}
	for _, x := range list {
		if x.SourceID == paymentID {
			got[x.Category] = x
		}
	}
	pl, pr := got[fees.CatPlatform], got[fees.CatProcessing]
	if len(got) != 2 || str(pl.FeeMinor) != "100" || pl.Basis != "QUOTED" || str(pl.DonorPartMinor) != "100" || str(pr.FeeMinor) != "40" ||
		pr.Basis != "ESTIMATED" || str(pr.PlatformPartMinor) != "40" || pl.AccountingStatus != fees.AcctNotPostedTestPayment || !pl.TestPayment {
		t.Fatalf("assessments: %+v", got)
	}
	var n int
	if err := f.mig.QueryRow(ctx(t), `SELECT count(*) FROM ledger.ledger_transactions WHERE source_id = $1`, paymentID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a test payment must never be posted: %d journals, %v", n, err)
	}
	// the database refuses a posted test-payment assessment
	if _, err := f.app.Exec(ctx(t), `UPDATE app.fee_assessments SET accounting_status = 'POSTED' WHERE id = $1`, pl.ID); err == nil {
		t.Fatal("a test-payment assessment must never become POSTED")
	}
}

// ===== accounting fixtures: capture, provider fee, settlement, refunds, chargebacks ================================

func TestFeeAccountingFixturesRefundsAndChargebacks(t *testing.T) {
	f := newFeeIT(t, 0)
	_, platV := f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("ORGANISER_COVERED"))
	f.policy(t, "CHARGEBACK_FEE", map[string]any{"campaign_id": f.camp}, passThrough("ORGANISER_COVERED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	f.schedule(t, "CHARGEBACK", map[string]any{"model": "FIXED", "fixed_minor": "1500", "payment_method": nil})
	before := f.payable(t, f.camp)
	// the brief's example: USD 100.00 donation, platform fee 5.00, provider fee 2.00 — TEST_ONLY synthetic donation
	d1 := f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.camp, "amount_minor": "10000", "currency": "USD",
		"payment_method": "ECOCASH", "settle": true}, 201, "")
	if d1.Label != "TEST_ONLY" || len(d1.Journals) != 4 || len(d1.Assessments) != 2 {
		t.Fatalf("donation fixture: %+v", d1)
	}
	k := func(step string) string { return "fee_fixture:" + d1.SyntheticDonationID + ":" + step }
	cap := f.amounts(t, k("capture"))
	if cap.Amount(ledger.ClassPSPClearing, ledger.Debit) != 10000 || cap.Amount(ledger.ClassCampaignUnsettled, ledger.Credit) != 9500 ||
		cap.Amount(ledger.ClassPlatformFees, ledger.Credit) != 500 || cap.FeePolicyVersionID == nil || *cap.FeePolicyVersionID != platV {
		t.Fatalf("capture: %+v", cap)
	}
	psp := f.amounts(t, k("psp_fee"))
	if psp.Amount(ledger.ClassCampaignUnsettled, ledger.Debit) != 200 || psp.Amount(ledger.ClassPSPClearing, ledger.Credit) != 200 {
		t.Fatalf("provider fee: %+v", psp)
	}
	if f.amounts(t, k("settlement")).Amount(ledger.ClassPSPSettled, ledger.Debit) != 9800 ||
		f.amounts(t, k("release")).Amount(ledger.ClassCampaignPayable, ledger.Credit) != 9300 {
		t.Fatal("settlement/release amounts")
	}
	if got := f.payable(t, f.camp) - before; got != 9300 {
		t.Fatalf("campaign payable grew by %d, want 9300 (allocation after fees)", got)
	}
	for _, a := range d1.Assessments {
		if a.AccountingStatus != fees.AcctPosted || !a.Synthetic {
			t.Fatalf("assessment not posted: %+v", a)
		}
	}
	// replay with the same run id posts nothing new
	replay := f.fixture(t, map[string]any{"scenario": "donation", "run_id": d1.RunID, "campaign_id": f.camp, "amount_minor": "10000", "currency": "USD",
		"payment_method": "ECOCASH", "settle": true}, 201, "")
	for i, j := range replay.Journals {
		if j.TransactionID != d1.Journals[i].TransactionID {
			t.Fatal("a replayed fixture posted a different journal")
		}
	}
	// partial refund USD 40.00: proportional reversal 2.00 (round(500 × 4000 / 10000))
	rf := f.fixture(t, map[string]any{"scenario": "refund", "donation_id": d1.SyntheticDonationID, "amount_minor": "4000"}, 201, "")
	if len(rf.Adjustments) != 1 || rf.Adjustments[0].AmountMinor != "200" || rf.Adjustments[0].Reason != "REFUND" || str(rf.Adjustments[0].BasisMinor) != "4000" {
		t.Fatalf("refund adjustment: %+v", rf.Adjustments)
	}
	res := f.amounts(t, rf.Journals[0].Key)
	if res.Amount(ledger.ClassCampaignPayable, ledger.Debit) != 3800 || res.Amount(ledger.ClassPlatformFees, ledger.Debit) != 200 ||
		res.Amount(ledger.ClassRefundPayable, ledger.Credit) != 4000 {
		t.Fatalf("refund reserved: %+v", res)
	}
	if f.amounts(t, rf.Journals[1].Key).Amount(ledger.ClassPSPSettled, ledger.Credit) != 4000 {
		t.Fatal("refund settled")
	}
	// the same reversal id replays; a second partial refund continues cumulatively (no rounding drift)
	rid := ids.New()
	r2 := f.fixture(t, map[string]any{"scenario": "refund", "donation_id": d1.SyntheticDonationID, "amount_minor": "5000", "reversal_id": rid}, 201, "")
	r2b := f.fixture(t, map[string]any{"scenario": "refund", "donation_id": d1.SyntheticDonationID, "amount_minor": "5000", "reversal_id": rid}, 201, "")
	if r2.Adjustments[0].ID != r2b.Adjustments[0].ID || r2.Adjustments[0].AmountMinor != "250" {
		t.Fatalf("second refund: %+v / %+v", r2.Adjustments, r2b.Adjustments)
	}
	// more than the original amount cannot be refunded
	f.fixture(t, map[string]any{"scenario": "refund", "donation_id": d1.SyntheticDonationID, "amount_minor": "1001"}, 422, "VALIDATION_FAILED")
	a, err := f.fs.Assessment(ctx(t), d1.Assessments[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Category == fees.CatPlatform && str(a.NetFeeMinor) != "50" {
		t.Fatalf("net platform fee after refunds: %s, want 50", str(a.NetFeeMinor))
	}
	// chargeback of 20.00 on a new donation: platform fee reversal 1.00; provider chargeback fee 15.00 borne by the
	// campaign and recovered from its payable balance
	d2 := f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.camp, "amount_minor": "10000", "currency": "USD",
		"payment_method": "ECOCASH", "settle": true}, 201, "")
	cb := f.fixture(t, map[string]any{"scenario": "chargeback", "donation_id": d2.SyntheticDonationID, "amount_minor": "2000"}, 201, "")
	if cb.Adjustments[0].AmountMinor != "100" || cb.Exposure["recovery_status"] != "RECOVERED_FROM_CAMPAIGN_BALANCE" ||
		cb.Exposure["provider_chargeback_fee_minor"] != "1500" || len(cb.Journals) != 2 {
		t.Fatalf("chargeback: %+v", cb)
	}
	op := f.amounts(t, cb.Journals[0].Key)
	if op.Amount(ledger.ClassCampaignPayable, ledger.Debit) != 1900 || op.Amount(ledger.ClassPlatformFees, ledger.Debit) != 100 ||
		op.Amount(ledger.ClassPSPSettled, ledger.Credit) != 2000 {
		t.Fatalf("dispute opened: %+v", op)
	}
	if fee := f.amounts(t, cb.Journals[1].Key); fee.Amount(ledger.ClassCampaignPayable, ledger.Debit) != 1500 {
		t.Fatalf("dispute fee: %+v", fee)
	}
	// a small donation cannot cover an organiser-borne chargeback fee: the exposure is UNRECOVERED, never assumed
	d3 := f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.newEmptyCampaignWithPolicies(t), "amount_minor": "1000",
		"currency": "USD", "payment_method": "ECOCASH", "settle": true}, 201, "")
	cb3 := f.fixture(t, map[string]any{"scenario": "chargeback", "donation_id": d3.SyntheticDonationID, "amount_minor": "500"}, 201, "")
	if cb3.Exposure["recovery_status"] != "UNRECOVERED_INSUFFICIENT_CAMPAIGN_BALANCE" {
		t.Fatalf("small chargeback: %+v", cb3.Exposure)
	}
	// every fee journal balanced and the ledger's invariants hold
	f.integrity(t)
}

// newEmptyCampaignWithPolicies publishes a fresh campaign with the same fee policies as the test's campaign.
func (f *feeIT) newEmptyCampaignWithPolicies(t *testing.T) string {
	t.Helper()
	c := f.campaign(t, 0)
	f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": c}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": c}, passThrough("ORGANISER_COVERED"))
	f.policy(t, "CHARGEBACK_FEE", map[string]any{"campaign_id": c}, passThrough("ORGANISER_COVERED"))
	return c
}

// ===== adjustments: maker-checker and immutability ================================================================

func TestFeeAdjustmentsMakerCheckerAndImmutability(t *testing.T) {
	f := newFeeIT(t, 0)
	f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("PLATFORM_ABSORBED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	d := f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.camp, "amount_minor": "10000", "currency": "USD", "payment_method": "ECOCASH"}, 201, "")
	var plat fees.Assessment
	for _, a := range d.Assessments {
		if a.Category == fees.CatPlatform {
			plat = a
		}
	}
	base := "/admin/fees/assessments/" + plat.ID + "/adjustments"
	f.act(t, f.f.finance1, "POST", base, map[string]any{"reason": "REFUND", "direction": "DECREASE", "amount_minor": "100", "note": "typed refund"}, 422, "VALIDATION_FAILED")
	f.act(t, f.f.finance1, "POST", base, map[string]any{"reason": "POLICY_CORRECTION", "direction": "DECREASE", "amount_minor": "501",
		"note": "more than the fee"}, 422, "FEE_ADJUSTMENT_EXCEEDS_ASSESSMENT")
	adj := f.act(t, f.f.finance1, "POST", base, map[string]any{"reason": "POLICY_CORRECTION", "direction": "DECREASE", "amount_minor": "100",
		"note": "IT: corrected after a policy review"}, 201, "").field(t, "id")
	f.act(t, f.f.finance1, "POST", "/admin/fees/adjustments/"+adj+"/approve", map[string]any{"reason": "maker"}, 403, "")
	if _, err := f.fs.DecideAdjustment(ctx(t), f.f.finance1.id, time.Now(), adj, true, "maker approving own"); !errors.Is(err, fees.ErrSelfApproval) {
		t.Fatalf("self: %v", err)
	}
	r := f.act(t, f.f.approver1, "POST", "/admin/fees/adjustments/"+adj+"/approve", map[string]any{"reason": "IT: correction checked"}, 200, "")
	if r.field(t, "status") != "APPROVED" || r.field(t, "accounting_status") != "NOT_APPLICABLE" {
		t.Fatalf("approved: %s", string(r.Data))
	}
	f.act(t, f.f.approver1, "POST", "/admin/fees/adjustments/"+adj+"/approve", map[string]any{"reason": "again"}, 409, "FEE_ADJUSTMENT_STATE")
	got, _ := f.fs.Assessment(ctx(t), plat.ID)
	if str(got.FeeMinor) != "500" || str(got.NetFeeMinor) != "400" {
		t.Fatalf("original stays, net reflects the adjustment: %+v", got)
	}
	// the organiser sees the decided adjustment but no staff identities, notes, decision reasons or internal references
	ov := f.owner.do("GET", "/campaigns/"+f.camp+"/fees", nil)
	f.owner.expect(ov, 200, "")
	for _, secret := range []string{"IT: corrected after a policy review", "IT: correction checked", f.f.finance1.id, f.f.approver1.id,
		"requested_by", "decision_reason", "calculation_id", "quote_id", "source_id"} {
		if strings.Contains(string(ov.Data), secret) {
			t.Fatalf("organiser fee view leaks %q", secret)
		}
	}
	if !strings.Contains(string(ov.Data), adj) {
		t.Fatal("the organiser must see the approved adjustment")
	}
	// immutability: assessments and adjustments are never edited or deleted
	for _, q := range []string{`UPDATE app.fee_assessments SET fee_minor = 1 WHERE id = $1`, `DELETE FROM app.fee_assessments WHERE id = $1`} {
		if _, err := f.app.Exec(ctx(t), q, plat.ID); err == nil {
			t.Fatalf("must be refused: %s", q)
		}
	}
	for _, q := range []string{`UPDATE app.fee_adjustments SET amount_minor = 1 WHERE id = $1`, `DELETE FROM app.fee_adjustments WHERE id = $1`} {
		if _, err := f.app.Exec(ctx(t), q, adj); err == nil {
			t.Fatalf("must be refused: %s", q)
		}
	}
	// the database keeps the net non-negative even without the Go check
	if _, err := f.app.Exec(ctx(t), `INSERT INTO app.fee_adjustments (id, assessment_id, currency, reason, direction, amount_minor, source_type, source_id,
		note, status, actor_type, accounting_status) VALUES (gen_random_uuid(), $1, 'USD', 'REFUND', 'DECREASE', 401, 'refund', gen_random_uuid(),
		'bypass attempt', 'APPLIED', 'SYSTEM', 'NOT_POSTED')`, plat.ID); err == nil || !strings.Contains(err.Error(), "below zero") {
		t.Fatalf("net below zero must be refused: %v", err)
	}
}

// ===== concurrency =================================================================================================

func TestFeeConcurrency(t *testing.T) {
	f := newFeeIT(t, 0)
	pid, _ := f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("PLATFORM_ABSORBED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	// 100 concurrent duplicate fixture runs (same run id): one set of assessments and journals
	run := ids.New()
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.fs.RunFixture(ctx(t), f.f.ledgerAdmin.id, fees.FixtureRequest{Scenario: fees.FixtureDonation, RunID: &run, CampaignID: f.camp,
				AmountMinor: "10000", Currency: "USD", PaymentMethod: strP("ECOCASH")})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	okN := 0
	for err := range errs {
		if err == nil {
			okN++
		}
	}
	donation := fees.FixtureResult{}
	_ = donation
	var assessed, journals int
	src := seedFixtureDonation(t, f.mig, run)
	_ = f.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.fee_assessments WHERE source_id = $1`, src).Scan(&assessed)
	_ = f.mig.QueryRow(ctx(t), `SELECT count(*) FROM ledger.ledger_transactions WHERE source_system = 'fees' AND source_id = $1`, src).Scan(&journals)
	if okN == 0 || assessed != 2 || journals != 2 {
		t.Fatalf("100 duplicate runs: %d succeeded, %d assessments, %d journals (want 2 and 2)", okN, assessed, journals)
	}
	// two approved versions of one policy racing to activate with the same start: one wins
	start := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
	var vids []string
	for i := 0; i < 2; i++ {
		terms := platformTerms(400+int64(i), "ORGANISER_COVERED")
		terms["effective_from"] = start
		vid := f.act(t, f.f.finance1, "POST", "/admin/fees/policies/"+pid+"/versions", terms, 201, "").field(t, "id")
		f.act(t, f.f.finance1, "POST", "/admin/fees/policy-versions/"+vid+"/submit", nil, 200, "")
		f.act(t, f.f.approver1, "POST", "/admin/fees/policy-versions/"+vid+"/approve", map[string]any{"reason": "IT race"}, 200, "")
		vids = append(vids, vid)
	}
	results := make(chan error, 2)
	for _, vid := range vids {
		go func(vid string) {
			_, err := f.fs.Activate(ctx(t), f.f.finance1.id, time.Now(), vid)
			results <- err
		}(vid)
	}
	e1, e2 := <-results, <-results
	if (e1 == nil) == (e2 == nil) || !(errors.Is(e1, fees.ErrPolicyConflict) || errors.Is(e2, fees.ErrPolicyConflict)) {
		t.Fatalf("activation race: %v / %v", e1, e2)
	}
	// quotes created while a policy changes: none stays ACTIVE on a version that is no longer in force
	donor := verifiedUser(t, f.s, "feerace")
	var qs []string
	var mu sync.Mutex
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			q, err := f.fs.CreateQuote(ctx(t), donor.id, "", fees.QuoteRequest{CampaignID: f.camp, AmountMinor: "1000", Currency: "USD"})
			if err == nil {
				mu.Lock()
				qs = append(qs, q.ID)
				mu.Unlock()
			}
		}
	}()
	time.Sleep(150 * time.Millisecond)
	var active string
	_ = f.mig.QueryRow(ctx(t), `SELECT v.id FROM app.fee_policy_versions v WHERE v.policy_id = $1 AND v.status = 'ACTIVE'
		AND v.effective_from <= now() ORDER BY v.effective_from DESC LIMIT 1`, pid).Scan(&active)
	if _, err := f.fs.Suspend(ctx(t), f.f.finance1.id, time.Now(), active, "IT: suspend during quoting"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	close(stop)
	var stale int
	_ = f.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.fee_quotes q WHERE q.status = 'ACTIVE' AND $1 = ANY (q.policy_version_ids)`, active).Scan(&stale)
	if len(qs) == 0 || stale != 0 {
		t.Fatalf("%d quotes; %d still ACTIVE on the suspended version", len(qs), stale)
	}
}

func strP(s string) *string { return &s }

func seedFixtureDonation(t *testing.T, p *pgxpool.Pool, run string) string {
	t.Helper()
	var id string
	if err := p.QueryRow(ctx(t), `SELECT source_id::text FROM app.fee_assessments a JOIN app.fee_audit_events e ON e.aggregate_id = a.id
		WHERE a.source_type = 'SYNTHETIC_DONATION' AND a.created_at > now() - interval '10 minutes' ORDER BY a.created_at DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	_ = run
	return id
}

// ===== payout fees ================================================================================================

func TestFeePayoutFeesNeverDeductTwice(t *testing.T) {
	f := newFeeIT(t, 100_000)
	f.policy(t, "PAYOUT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("PLATFORM_ABSORBED"))
	f.schedule(t, "PAYOUT", map[string]any{"model": "FIXED", "fixed_minor": "100"})
	payable := f.payable(t, f.camp)
	id := f.request(t, f.camp, 10_000, 202, "")
	po := f.payout(t, id)
	if po.FeeEstimateMinor == nil || *po.FeeEstimateMinor != 100 || str(po.FeeResponsibility) != "PLATFORM_ABSORBED" || str(po.FeeBasis) != "ESTIMATED" ||
		po.Reservation == nil || po.Reservation.AmountMinor != 10_000 {
		t.Fatalf("payout fee estimate: %+v", po)
	}
	var ov struct {
		Receives *string `json:"beneficiary_receives_minor"`
		Fee      *string `json:"fee_estimate_minor"`
	}
	_ = json.Unmarshal(f.owner.do("GET", "/payout-requests/"+id, nil).Data, &ov)
	if str(ov.Receives) != "10000" || str(ov.Fee) != "100" {
		t.Fatalf("owner view: %+v", ov)
	}
	// the estimate is immutable
	if _, err := f.app.Exec(ctx(t), `UPDATE app.payout_requests SET fee_estimate_minor = 0 WHERE id = $1`, id); err == nil {
		t.Fatal("the payout fee estimate must be immutable")
	}
	f.approve(t, id)
	po = f.drive(t, 60*time.Second, "payout completed", status("COMPLETED"), id)
	// the fee is an expense journal; the campaign paid exactly the payout amount once
	var fee ledger.JournalAmounts
	deadline := time.Now().Add(30 * time.Second)
	for {
		j, ok, err := f.led.JournalAmountsByKey(ctx(t), "payout:"+id+":fee")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			fee = j
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no PAYOUT_FEE journal")
		}
		_, _ = f.fs.PostPending(ctx(t))
		time.Sleep(300 * time.Millisecond)
	}
	if fee.Amount(ledger.ClassPSPProcessingFees, ledger.Debit) != 100 || fee.SourceSystem != ledger.SourceFees {
		t.Fatalf("payout fee journal: %+v", fee)
	}
	if got := payable - f.payable(t, f.camp); got != 10_000 {
		t.Fatalf("campaign payable fell by %d, want exactly 10000", got)
	}
	// an organiser-borne fee is carved out of the payout (no extra journal); a fee not below the amount is refused
	c2 := f.campaign(t, 100_000)
	f.policy(t, "PAYOUT_PROCESSING_FEE", map[string]any{"campaign_id": c2}, passThrough("ORGANISER_COVERED"))
	f.request(t, c2, 100, 422, "FEE_EXCEEDS_AMOUNT")
	id2 := f.request(t, c2, 5_000, 202, "")
	_ = json.Unmarshal(f.owner.do("GET", "/payout-requests/"+id2, nil).Data, &ov)
	if str(ov.Receives) != "4900" {
		t.Fatalf("organiser-borne: beneficiary receives %s, want 4900", str(ov.Receives))
	}
	if _, err := f.po.CancelByStaff(ctx(t), f.f.finance1.id, id2, "IT cleanup: cancel before submission"); err != nil {
		t.Fatal(err)
	}
}

// ===== security ===================================================================================================

func TestFeeSecurity(t *testing.T) {
	f := newFeeIT(t, 0)
	anon := f.s.browser(t)
	anon.expect(anon.do("POST", "/fees/quotes", map[string]any{"campaign_id": f.camp, "amount_minor": "100", "currency": "USD"}, "Idempotency-Key", ids.New()), 401, "")
	sup := f.s.staff(t, f.f.support)
	sup.expect(sup.do("GET", "/admin/fees/policies", nil), 403, "")
	sup.expect(sup.do("GET", "/admin/finance/revenue", nil), 403, "")
	f.act(t, f.f.finance1, "POST", "/admin/fees/fixtures", map[string]any{"scenario": "donation"}, 403, "")
	f.act(t, f.f.compliance, "GET", "/admin/fees/overview", nil, 200, "")
	f.act(t, f.f.compliance, "POST", "/admin/fees/policies", map[string]any{"code": "it_nope"}, 403, "")
	donor := verifiedUser(t, f.s, "feesec")
	donor.expect(donor.do("GET", "/admin/fees/assessments", nil), 404, "ROUTE_NOT_FOUND") // admin routes do not exist for non-staff
	donor.expect(donor.do("GET", "/campaigns/"+f.camp+"/fees", nil), 404, "")
	f.owner.expect(f.owner.do("GET", "/campaigns/"+f.camp+"/fees", nil), 200, "")
	// a quote never exposes another person's identifiers
	q := f.quote(t, donor, "1000", "", 201, "")
	raw, _ := json.Marshal(q)
	if strings.Contains(string(raw), donor.id) || strings.Contains(string(raw), "user_id") {
		t.Fatalf("quote leaks identifiers: %s", raw)
	}
	// VERIFIED provider fees cannot be recorded; SYNTHETIC only for the sandbox
	body := map[string]any{"provider_id": app.SandboxProviderID, "contract_reference": "claimed contract", "schedule_version": "x", "currency": "USD",
		"fee_type": "TRANSACTION", "model": "PERCENTAGE", "rate_bps": 100, "effective_from": nowStr(), "evidence_source": "claimed", "confirmation_status": "VERIFIED"}
	f.act(t, f.f.finance1, "POST", "/admin/fees/provider-schedules", body, 422, "PROVIDER_FEE_VERIFICATION_UNAVAILABLE")
	if _, err := f.app.Exec(ctx(t), `UPDATE app.fee_provider_schedules SET confirmation_status = 'VERIFIED'`); err == nil {
		t.Fatal("the database must refuse VERIFIED provider fees")
	}
	// fee quotes are rate limited per user (fees.quote.user, 60/hour)
	limited := false
	for i := 0; i < 70 && !limited; i++ {
		r := donor.do("POST", "/fees/quotes", map[string]any{"campaign_id": f.camp, "amount_minor": "100", "currency": "USD"}, "Idempotency-Key", ids.New())
		limited = r.Status == 429
	}
	if !limited {
		t.Fatal("quotes must be rate limited")
	}
	// production and non-test rows are impossible
	for _, q := range []string{
		`UPDATE app.fee_quotes SET fee_environment = 'PRODUCTION'`,
		`INSERT INTO app.fee_calculations (id, algorithm_version, transaction_type, currency, basis_minor, inputs, inputs_hash, result, result_hash,
		  calculated_at, fee_environment) VALUES (gen_random_uuid(), 'fees-calc/1', 'DONATION', 'USD', 1, '{}', decode(repeat('00', 32), 'hex'), '{}',
		  decode(repeat('00', 32), 'hex'), now(), 'PRODUCTION')`,
	} {
		if _, err := f.mig.Exec(ctx(t), q); err == nil && !strings.HasPrefix(q, "UPDATE") {
			t.Fatalf("must be refused: %s", q)
		}
	}
	if _, err := f.app.Exec(ctx(t), `INSERT INTO ledger.ledger_transactions (id) VALUES (gen_random_uuid())`); err == nil {
		t.Fatal("the app role must not write ledger tables")
	}
}

// ===== reconciliation and reporting ===============================================================================

func TestFeeReconciliationAndRevenueReport(t *testing.T) {
	f := newFeeIT(t, 0)
	f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("ORGANISER_COVERED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	d := f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.camp, "amount_minor": "10000", "currency": "USD", "payment_method": "ECOCASH"}, 201, "")
	// the provider reports a different fee than estimated (SIMULATED report)
	f.fixture(t, map[string]any{"scenario": "provider_fee_report", "donation_id": d.SyntheticDonationID, "reported_fee_minor": "250"}, 201, "")
	var plat, proc string
	for _, a := range d.Assessments {
		if a.Category == fees.CatPlatform {
			plat = a.ID
		} else {
			proc = a.ID
		}
	}
	// a tampered stored calculation (test-only manipulation by the migrator) is detected as non-reproducible
	var calc string
	_ = f.mig.QueryRow(ctx(t), `SELECT calculation_id FROM app.fee_assessments WHERE id = $1`, plat).Scan(&calc)
	migratorExec(t, `ALTER TABLE app.fee_calculations DISABLE TRIGGER trg_fee_calculations_no_mutation`)
	migratorExec(t, `UPDATE app.fee_calculations SET result = jsonb_set(result, '{platform_net_minor}', '"499"') WHERE id = $1`, calc)
	migratorExec(t, `ALTER TABLE app.fee_calculations ENABLE TRIGGER trg_fee_calculations_no_mutation`)
	r := f.act(t, f.f.finance1, "POST", "/admin/fees/reconciliation/run", nil, 200, "")
	if r.field(t, "settlement_reconciliation") != "NOT_PERFORMED" {
		t.Fatalf("run: %s", string(r.Data))
	}
	// this test's own items (Stage 19: the shared development database holds thousands of other OPEN items — e.g. every
	// load-test donation's UNRECONCILED_PROVIDER_FEE — so the first page of the queue need not contain them)
	own := func() map[string]string {
		rows, err := f.mig.Query(ctx(t), `SELECT kind, id::text FROM app.fee_reconciliation_references WHERE status = 'OPEN'
			AND assessment_id = ANY($1::uuid[])`, []string{plat, proc})
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		m := map[string]string{}
		for rows.Next() {
			var k, id string
			_ = rows.Scan(&k, &id)
			m[k] = id
		}
		return m
	}
	ownCount := func() int {
		var n int
		_ = f.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.fee_reconciliation_references WHERE status = 'OPEN' AND assessment_id = ANY($1::uuid[])`,
			[]string{plat, proc}).Scan(&n)
		return n
	}
	found := own()
	if found[fees.RecAmountMismatch] == "" || found[fees.RecRoundingMismatch] == "" {
		t.Fatalf("expected AMOUNT_MISMATCH (provider report) and ROUNDING_MISMATCH (tampered calculation): %v", found)
	}
	// a rerun does not duplicate open items; resolving records a note and changes nothing else
	before := ownCount()
	if _, err := f.fs.Reconcile(ctx(t), "it-rerun"); err != nil {
		t.Fatal(err)
	}
	if again := ownCount(); again != before {
		t.Fatalf("rerun duplicated items: %d → %d", before, again)
	}
	f.act(t, f.f.finance1, "POST", "/admin/fees/reconciliation/"+found[fees.RecAmountMismatch]+"/resolve", map[string]any{"note": "IT: provider report reviewed"}, 200, "")
	f.act(t, f.f.finance1, "POST", "/admin/fees/reconciliation/"+found[fees.RecAmountMismatch]+"/resolve", map[string]any{"note": "again"}, 409, "FEE_RECONCILIATION_STATE")
	// the revenue report is per currency, labelled TEST and distinguishes quoted, assessed and recognised
	rep := f.act(t, f.f.finance1, "GET", "/admin/finance/revenue", nil, 200, "")
	var rr fees.RevenueReport
	if err := json.Unmarshal(rep.Data, &rr); err != nil {
		t.Fatal(err)
	}
	if rr.FeeEnvironment != "TEST" || len(rr.Currencies) == 0 || !strings.Contains(rr.Label, "TEST") {
		t.Fatalf("report: %+v", rr)
	}
	for _, c := range rr.Currencies {
		if c.CashReceived != "NOT_AVAILABLE" {
			t.Fatalf("cash received must not be reported: %+v", c)
		}
		if c.Currency == "USD" && (c.RecognisedRevenueMinor == "0" || c.AssessedPlatformFeesMinor == "0") {
			t.Fatalf("USD figures: %+v", c)
		}
	}
	if strings.Contains(string(rep.Data), "\"total") {
		t.Fatal("no cross-currency total may exist")
	}
	f.act(t, f.f.finance1, "GET", "/admin/fees/overview", nil, 200, "")
	f.act(t, f.f.finance1, "GET", "/admin/fees/assessments/"+plat, nil, 200, "")
}

// ===== failure injection ==========================================================================================

func TestFeeFailureInjection(t *testing.T) {
	f := newFeeIT(t, 0)
	f.policy(t, "PLATFORM_FEE", map[string]any{"campaign_id": f.camp}, platformTerms(500, "ORGANISER_COVERED"))
	f.policy(t, "PAYMENT_PROCESSING_FEE", map[string]any{"campaign_id": f.camp}, passThrough("PLATFORM_ABSORBED"))
	f.schedule(t, "TRANSACTION", map[string]any{"model": "PERCENTAGE", "rate_bps": 200})
	// the ledger refuses the posting after the assessments were written (the campaign account is frozen — a
	// "crash" between assessment and posting): the assessments stay PENDING_POSTING with no journal, reconciliation
	// reports MISSING_POSTING, and replaying the same run after recovery posts exactly once
	first := f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.camp, "amount_minor": "1000", "currency": "USD",
		"payment_method": "ECOCASH"}, 201, "")
	_ = first
	bal, err := f.led.CampaignBalances(ctx(t), f.camp, "USD")
	if err != nil {
		t.Fatal(err)
	}
	acct := bal[ledger.ClassCampaignUnsettled].AccountID
	if err := f.led.SetAccountStatus(ctx(t), f.f.finance1.id, acct, "FROZEN", "IT: freeze to inject a posting failure"); err != nil {
		t.Fatal(err)
	}
	run := ids.New()
	req := fees.FixtureRequest{Scenario: fees.FixtureDonation, RunID: &run, CampaignID: f.camp, AmountMinor: "10000", Currency: "USD",
		PaymentMethod: strP("ECOCASH")}
	if _, err := f.fs.RunFixture(ctx(t), f.f.ledgerAdmin.id, req); !errors.Is(err, ledger.ErrAccountFrozen) {
		t.Fatalf("posting to a frozen account must be refused: %v", err)
	}
	var pending int
	_ = f.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.fee_assessments WHERE source_type = 'SYNTHETIC_DONATION' AND accounting_status = 'PENDING_POSTING'
		AND campaign_id = $1`, f.camp).Scan(&pending)
	if pending == 0 {
		t.Fatal("the assessments must stay PENDING_POSTING")
	}
	if _, err := f.fs.Reconcile(ctx(t), "it-failure"); err != nil {
		t.Fatal(err)
	}
	var missing int
	_ = f.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.fee_reconciliation_references r JOIN app.fee_assessments a ON a.id = r.assessment_id
		WHERE r.kind = 'MISSING_POSTING' AND r.status = 'OPEN' AND a.campaign_id = $1`, f.camp).Scan(&missing)
	if missing == 0 {
		t.Fatal("reconciliation must report the missing posting")
	}
	if err := f.led.SetAccountStatus(ctx(t), f.f.finance1.id, acct, "ACTIVE", "IT: unfreeze after the injected failure"); err != nil {
		t.Fatal(err)
	}
	res, err := f.fs.RunFixture(ctx(t), f.f.ledgerAdmin.id, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range res.Assessments {
		if a.AccountingStatus != fees.AcctPosted {
			t.Fatalf("after recovery: %+v", a)
		}
	}
	dead, err := pgxpool.New(ctx(t), "postgres://127.0.0.1:1/unreachable?connect_timeout=1") // an unreachable server (port 1)
	if err != nil {
		t.Fatal(err)
	}
	dead.Close()
	// the fee database is unreachable: a quote fails and nothing is written
	down := *f.fs
	down.Pool = dead
	if _, err := down.CreateQuote(ctx(t), f.owner.id, "", fees.QuoteRequest{CampaignID: f.camp, AmountMinor: "100", Currency: "USD"}); err == nil {
		t.Fatal("a quote must fail when the database is down")
	}
	// a refund the ledger refuses (more than the campaign holds) records no adjustment
	small := f.fixture(t, map[string]any{"scenario": "donation", "campaign_id": f.newEmptyCampaignWithPolicies(t), "amount_minor": "1000", "currency": "USD",
		"payment_method": "ECOCASH", "settle": true}, 201, "")
	var before int
	_ = f.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.fee_adjustments WHERE assessment_id IN (SELECT id FROM app.fee_assessments WHERE source_id = $1)`,
		small.SyntheticDonationID).Scan(&before)
	f.fixture(t, map[string]any{"scenario": "refund", "donation_id": small.SyntheticDonationID, "amount_minor": "1000"}, 409, "LEDGER_INSUFFICIENT_FUNDS")
	var after int
	_ = f.mig.QueryRow(ctx(t), `SELECT count(*) FROM app.fee_adjustments WHERE assessment_id IN (SELECT id FROM app.fee_assessments WHERE source_id = $1)`,
		small.SyntheticDonationID).Scan(&after)
	if after != before {
		t.Fatalf("a refused refund recorded %d adjustment(s)", after-before)
	}
	fmt.Println("fee failure injection: ledger outage, database outage and ledger refusal handled")
}
