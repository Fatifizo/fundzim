package payouts

import (
	"crypto/sha256"
	"encoding/json"
	"sort"
	"time"
)

// Eligibility engine (payout-eligibility-and-controls.md; ADR-043 §5). Evaluate is a PURE function over Facts
// gathered from the owning modules' public interfaces (gather.go), so every rule is unit-testable. It runs at
// REQUEST, PRE_SUBMIT (immediately before submission) and RECHECK (staff re-evaluation), fails closed, and its
// decision — all 22 checks, each exactly once — is stored as an append-only, hashed record
// (app.payout_eligibility_decisions; the database re-validates the result set and outcome).

// EngineVersion identifies this engine's rules in every decision record.
const EngineVersion = "payout-eligibility-v1"

// Check results.
const (
	Pass   = "PASS"
	Fail   = "FAIL"
	Review = "REVIEW"
	Defer  = "DEFER"
	NA     = "NOT_APPLICABLE"
)

// Phases.
const (
	PhaseRequest   = "REQUEST"
	PhasePreSubmit = "PRE_SUBMIT"
	PhaseRecheck   = "RECHECK"
)

// Outcomes.
const (
	OutcomeEligible = "ELIGIBLE"
	OutcomeReview   = "REVIEW"
	OutcomeDeferred = "DEFERRED"
	OutcomeRejected = "REJECTED"
)

// CheckResult is one check's result.
type CheckResult struct {
	CheckID string `json:"check_id"`
	Result  string `json:"result"`
	Detail  string `json:"detail_code"`
}

// Limit is a configured limit value used by a check (combined, most restrictive, from risk.limits).
type Limit struct {
	Configured bool     `json:"configured"`
	Value      int64    `json:"value"`
	LimitIDs   []string `json:"limit_ids"`
}

// Facts are the references and values a decision is based on (stored as the decision's inputs: identifiers,
// statuses, levels and amounts — never documents, account numbers or narrative).
type Facts struct {
	Phase    string    `json:"phase"`
	At       time.Time `json:"at"`
	Amount   int64     `json:"amount_minor,string"`
	Currency string    `json:"currency"`
	TestMode bool      `json:"test_payout"`

	// environment (EC-08)
	ExecutionEnvironment string `json:"execution_environment"`
	LedgerEnvironment    string `json:"ledger_environment"`
	CurrencyPostable     bool   `json:"currency_postable"`
	RouteFound           bool   `json:"route_found"`
	RouteCapabilityID    string `json:"route_capability_id,omitempty"`
	ProviderMinMinor     *int64 `json:"provider_min_minor,omitempty,string"`
	ProviderMaxMinor     *int64 `json:"provider_max_minor,omitempty,string"`
	ProviderAvailable    bool   `json:"provider_available"` // no declared outage / open circuit (EC-18)

	// campaign and parties (EC-01..EC-04, EC-13)
	CampaignStatus       string  `json:"campaign_status"`
	RequesterAuthorised  bool    `json:"requester_authorised"`
	RequesterStatus      string  `json:"requester_status"`
	OwnerKind            string  `json:"owner_kind"` // USER | ORGANISATION
	OwnerLevel           string  `json:"owner_level"`
	OwnerStatus          string  `json:"owner_status"`
	RequesterLevel       string  `json:"requester_level"`
	BeneficiaryID        *string `json:"beneficiary_id"`
	BeneficiaryType      string  `json:"beneficiary_type,omitempty"`
	BeneficiaryStatus    string  `json:"beneficiary_status,omitempty"`
	RestrictionLevel     string  `json:"restriction_level"` // highest compliance restriction (campaign, owner, org, beneficiary)
	PayableAccountStatus string  `json:"payable_account_status,omitempty"`

	// destination (EC-05..EC-07)
	DestinationFound           bool       `json:"destination_found"`
	DestinationStatus          string     `json:"destination_status"`
	DestinationOwnership       string     `json:"destination_ownership"`
	DestinationCompliance      string     `json:"destination_compliance"`
	DestinationCurrency        string     `json:"destination_currency"`
	DestinationOwnedByCampaign bool       `json:"destination_owned_by_campaign_owner"`
	DestinationPayeeType       string     `json:"destination_payee_type"`
	DestinationBeneficiaryID   *string    `json:"destination_beneficiary_id"`
	DestinationCoolingOffUntil *time.Time `json:"destination_cooling_off_until"`
	DestinationExpiresAt       *time.Time `json:"destination_expires_at"`
	DestinationVersion         int        `json:"destination_version"`
	SnapshotVersion            int        `json:"snapshot_destination_version,omitempty"` // PRE_SUBMIT / RECHECK

	// money (EC-09, EC-10, EC-14)
	PayableMinor         int64 `json:"payable_minor,string"`         // liability:campaign_payable (settled AND released funds only)
	RefundExposureMinor  int64 `json:"refund_exposure_minor,string"` // open refund requests (not yet in the ledger)
	ReservationActive    bool  `json:"reservation_active"`           // PRE_SUBMIT: reservation journal posted and intact
	PendingMinor         int64 `json:"payout_pending_minor,string"`  // liability:campaign_payout_pending (PRE_SUBMIT)
	MinAmount            Limit `json:"limit_min_amount"`             // payout.min_amount
	MaxPerTransaction    Limit `json:"limit_max_per_transaction"`    // payout.max_per_transaction (internal)
	DualApprovalMin      Limit `json:"limit_dual_approval_min"`      // payout.dual_approval_min
	VelocityCount7d      Limit `json:"limit_velocity_count_7d"`      // payout.velocity.count_per_7d
	VelocityAmount7d     Limit `json:"limit_velocity_amount_7d"`     // payout.velocity.amount_per_7d
	RecentPayoutCount7d  int64 `json:"recent_payout_count_7d"`
	RecentPayoutAmount7d int64 `json:"recent_payout_amount_7d,string"`

	// in flight, integrity, approvals (EC-11, EC-19, EC-22)
	OtherInFlight       bool     `json:"other_payout_in_flight"`
	LedgerIntegrityOK   bool     `json:"ledger_integrity_ok"`
	RequiredApprovals   int      `json:"required_approvals"`
	ValidApprovals      int      `json:"valid_approvals"`
	RejectingApprovals  int      `json:"rejecting_approvals"`
	AcknowledgedReviews []string `json:"acknowledged_reviews,omitempty"` // review codes the approvers saw (PRE_SUBMIT)

	// risk indicators (EC-16; Stage 13 replaces with a scored model behind the same interface)
	Indicators []string `json:"risk_indicators"`
	// first payout of an organisation campaign (always DUAL)
	FirstOrgPayout bool `json:"first_organisation_payout"`
}

// Decision is the engine's output.
type Decision struct {
	Phase        string        `json:"phase"`
	Results      []CheckResult `json:"results"`
	Outcome      string        `json:"outcome"`
	ApprovalTier string        `json:"approval_tier,omitempty"`
	ReasonCode   string        `json:"reason_code,omitempty"` // owner-safe category for a refusal
}

// FailedChecks lists the failing check ids.
func (d Decision) FailedChecks() []string {
	var out []string
	for _, r := range d.Results {
		if r.Result == Fail {
			out = append(out, r.CheckID)
		}
	}
	return out
}

// ReviewCodes lists "EC-xx:DETAIL" for every REVIEW/DEFER result.
func (d Decision) ReviewCodes() []string {
	var out []string
	for _, r := range d.Results {
		if r.Result == Review || r.Result == Defer {
			out = append(out, r.CheckID+":"+r.Detail)
		}
	}
	sort.Strings(out)
	return out
}

// Result returns one check's result.
func (d Decision) Result(id string) CheckResult {
	for _, r := range d.Results {
		if r.CheckID == id {
			return r
		}
	}
	return CheckResult{}
}

// Evaluate runs every check over the facts.
func Evaluate(f Facts) Decision {
	d := Decision{Phase: f.Phase}
	add := func(id, res, detail string) { d.Results = append(d.Results, CheckResult{id, res, detail}) }
	submit := f.Phase == PhasePreSubmit

	// EC-01 campaign payout-eligible: ACTIVE or COMPLETED only (any other status, incl. PAUSED, SUSPENDED and
	// CANCELLED, blocks; a CANCELLED-campaign disposition is LR-019 and not available)
	switch f.CampaignStatus {
	case "ACTIVE", "COMPLETED":
		add("EC-01", Pass, "")
	default:
		add("EC-01", Fail, "CAMPAIGN_STATUS_"+f.CampaignStatus)
	}
	// EC-02 requester authorised and active
	switch {
	case !f.RequesterAuthorised:
		add("EC-02", Fail, "NOT_CAMPAIGN_OWNER_OR_ORG_ADMIN")
	case f.RequesterStatus != "ACTIVE":
		add("EC-02", Fail, "REQUESTER_NOT_ACTIVE")
	default:
		add("EC-02", Pass, "")
	}
	// EC-03 owner verification (individual PAYOUT_VERIFIED; organisation ORG_PAYOUT_VERIFIED and the
	// representative IDENTITY_VERIFIED or higher)
	switch {
	case f.OwnerStatus != "ACTIVE":
		add("EC-03", Fail, "OWNER_VERIFICATION_NOT_ACTIVE")
	case f.OwnerKind == "USER" && f.OwnerLevel != "PAYOUT_VERIFIED":
		add("EC-03", Fail, "OWNER_NOT_PAYOUT_VERIFIED")
	case f.OwnerKind == "ORGANISATION" && f.OwnerLevel != "ORG_PAYOUT_VERIFIED":
		add("EC-03", Fail, "ORGANISATION_NOT_PAYOUT_VERIFIED")
	case f.OwnerKind == "ORGANISATION" && f.RequesterLevel != "IDENTITY_VERIFIED" && f.RequesterLevel != "PAYOUT_VERIFIED":
		add("EC-03", Fail, "REPRESENTATIVE_NOT_IDENTITY_VERIFIED")
	default:
		add("EC-03", Pass, "")
	}
	// EC-04 beneficiary verified (no linked beneficiary: the verified owner is the payee)
	switch {
	case f.BeneficiaryID == nil:
		add("EC-04", Pass, "OWNER_IS_PAYEE")
	case f.BeneficiaryType == "SELF":
		add("EC-04", Pass, "SELF_BENEFICIARY_OWNER_VERIFIED") // the owner's own verification is EC-03
	case f.BeneficiaryStatus != "VERIFIED":
		add("EC-04", Fail, "BENEFICIARY_NOT_VERIFIED")
	default:
		add("EC-04", Pass, "")
	}
	// EC-05 destination verified, owned by the campaign owner, paying the owner or the campaign's own beneficiary
	switch {
	case !f.DestinationFound:
		add("EC-05", Fail, "DESTINATION_NOT_FOUND")
	case !f.DestinationOwnedByCampaign:
		add("EC-05", Fail, "DESTINATION_NOT_OWNED_BY_CAMPAIGN_OWNER")
	case f.DestinationStatus != "VERIFIED" || f.DestinationOwnership != "CONFIRMED" || f.DestinationCompliance != "APPROVED":
		add("EC-05", Fail, "DESTINATION_NOT_VERIFIED")
	case f.DestinationExpiresAt != nil && !f.DestinationExpiresAt.After(f.At):
		add("EC-05", Fail, "DESTINATION_VERIFICATION_EXPIRED")
	case f.DestinationPayeeType == "BENEFICIARY" && (f.BeneficiaryID == nil || f.DestinationBeneficiaryID == nil || *f.DestinationBeneficiaryID != *f.BeneficiaryID):
		add("EC-05", Fail, "DESTINATION_PAYEE_NOT_CAMPAIGN_BENEFICIARY")
	case f.DestinationPayeeType == "OWNER" && f.BeneficiaryID != nil && f.BeneficiaryType != "SELF":
		// a campaign raising for someone else pays that verified beneficiary, never the owner's own account
		add("EC-05", Fail, "DESTINATION_PAYEE_NOT_CAMPAIGN_BENEFICIARY")
	default:
		add("EC-05", Pass, "")
	}
	// EC-06 destination cooling-off (DEFER: wait)
	if f.DestinationCoolingOffUntil != nil && f.DestinationCoolingOffUntil.After(f.At) {
		add("EC-06", Defer, "DESTINATION_COOLING_OFF")
	} else {
		add("EC-06", Pass, "")
	}
	// EC-07 destination snapshot unchanged (submit and re-check)
	switch {
	case f.Phase == PhaseRequest:
		add("EC-07", NA, "")
	case f.DestinationVersion != f.SnapshotVersion:
		add("EC-07", Review, "DESTINATION_CHANGED_SINCE_REQUEST")
	default:
		add("EC-07", Pass, "")
	}
	// EC-08 currency, rail and execution environment: a TEST payout in a TEST ledger through the simulator only
	switch {
	case f.ExecutionEnvironment != "LOCAL_SIMULATOR" || !f.TestMode:
		add("EC-08", Fail, "PAYOUT_EXECUTION_DISABLED")
	case f.LedgerEnvironment != "TEST":
		add("EC-08", Fail, "LEDGER_ENVIRONMENT_NOT_TEST")
	case !f.CurrencyPostable:
		add("EC-08", Fail, "CURRENCY_NOT_POSTABLE")
	case f.DestinationFound && f.DestinationCurrency != f.Currency:
		add("EC-08", Fail, "DESTINATION_CURRENCY_MISMATCH")
	case !f.RouteFound:
		add("EC-08", Fail, "NO_PAYOUT_ROUTE")
	default:
		add("EC-08", Pass, "")
	}
	// EC-09 available balance: campaign_payable (settled and released funds only) less open refund exposure; at
	// submission the reservation must be intact
	if submit {
		switch {
		case !f.ReservationActive:
			add("EC-09", Fail, "RESERVATION_NOT_ACTIVE")
		case f.PendingMinor < f.Amount:
			add("EC-09", Fail, "RESERVATION_NOT_INTACT")
		case f.RefundExposureMinor > f.PayableMinor:
			// open refunds can no longer be covered by what is left: a human decides
			add("EC-09", Review, "REFUND_EXPOSURE_EXCEEDS_REMAINING")
		default:
			add("EC-09", Pass, "")
		}
	} else if f.Phase == PhaseRequest {
		switch {
		case f.Amount <= 0:
			add("EC-09", Fail, "AMOUNT_NOT_POSITIVE")
		case f.PayableAccountStatus != "" && f.PayableAccountStatus != "ACTIVE":
			add("EC-09", Fail, "PAYABLE_ACCOUNT_"+f.PayableAccountStatus)
		case f.Amount > f.PayableMinor-f.RefundExposureMinor:
			add("EC-09", Fail, "INSUFFICIENT_ELIGIBLE_FUNDS")
		default:
			add("EC-09", Pass, "")
		}
	} else { // RECHECK of an existing payout
		if f.ReservationActive {
			add("EC-09", Pass, "")
		} else {
			add("EC-09", Fail, "RESERVATION_NOT_ACTIVE")
		}
	}
	// EC-10 amount limits: provider limits FAIL; internal limits REVIEW; unconfigured REVIEW (fail closed)
	switch {
	case f.ProviderMinMinor != nil && f.Amount < *f.ProviderMinMinor:
		add("EC-10", Fail, "BELOW_PROVIDER_MINIMUM")
	case f.ProviderMaxMinor != nil && f.Amount > *f.ProviderMaxMinor:
		add("EC-10", Fail, "ABOVE_PROVIDER_MAXIMUM")
	case f.ProviderMinMinor == nil || f.ProviderMaxMinor == nil:
		add("EC-10", Review, "PROVIDER_LIMITS_UNKNOWN")
	case f.MinAmount.Configured && f.Amount < f.MinAmount.Value:
		add("EC-10", Fail, "BELOW_MINIMUM")
	case f.MaxPerTransaction.Configured && f.Amount > f.MaxPerTransaction.Value:
		add("EC-10", Review, "ABOVE_INTERNAL_MAXIMUM")
	case !f.MinAmount.Configured || !f.MaxPerTransaction.Configured:
		add("EC-10", Review, "AMOUNT_LIMITS_UNCONFIGURED")
	default:
		add("EC-10", Pass, "")
	}
	// EC-11 single in-flight payout per (campaign, currency) (the partial unique index is the backstop)
	if f.Phase == PhaseRequest && f.OtherInFlight {
		add("EC-11", Fail, "PAYOUT_ALREADY_IN_PROGRESS")
	} else {
		add("EC-11", Pass, "")
	}
	// EC-12 blocking holds: frozen/suspended campaign, frozen ledger account, suspended destination. No hold
	// registry exists yet (risk.holds is not built: KI-S11); campaign and account states are the holds we have.
	switch {
	case f.CampaignStatus == "SUSPENDED" || f.CampaignStatus == "FROZEN":
		add("EC-12", Fail, "CAMPAIGN_HOLD")
	case f.PayableAccountStatus == "FROZEN" || f.PayableAccountStatus == "CLOSED":
		add("EC-12", Fail, "LEDGER_ACCOUNT_HOLD")
	case f.DestinationStatus == "SUSPENDED":
		add("EC-12", Fail, "DESTINATION_HOLD")
	default:
		add("EC-12", Pass, "")
	}
	// EC-13 compliance restriction on the campaign, owner, organisation or beneficiary: blocks payouts. The
	// detail never names the reason (no tipping-off, LR-008).
	if f.RestrictionLevel != "" {
		add("EC-13", Fail, "COMPLIANCE_RESTRICTION")
	} else {
		add("EC-13", Pass, "")
	}
	// EC-14 dispute/chargeback exposure: no dispute source exists (disputes are a later stage) — recorded as not
	// applicable; refund exposure is part of EC-09
	add("EC-14", NA, "NO_DISPUTE_SOURCE")
	// EC-15 sanctions/PEP screening: no screening provider (KI-S5-04). A TEST payout records NOT_APPLICABLE; any
	// other payout fails (production payouts are impossible in Stage 11 anyway).
	if f.TestMode {
		add("EC-15", NA, "SCREENING_PROVIDER_NOT_SELECTED_TEST_PAYOUT")
	} else {
		add("EC-15", Fail, "SCREENING_PROVIDER_NOT_SELECTED")
	}
	// EC-16 risk indicators (minimal rules; Stage 13 replaces them)
	if len(f.Indicators) > 0 {
		add("EC-16", Review, "RISK_INDICATORS")
	} else {
		add("EC-16", Pass, "")
	}
	// EC-17 velocity limits
	switch {
	case !f.VelocityCount7d.Configured || !f.VelocityAmount7d.Configured:
		add("EC-17", Review, "VELOCITY_LIMITS_UNCONFIGURED")
	case f.RecentPayoutCount7d+1 > f.VelocityCount7d.Value || f.RecentPayoutAmount7d+f.Amount > f.VelocityAmount7d.Value:
		add("EC-17", Review, "VELOCITY_EXCEEDED")
	default:
		add("EC-17", Pass, "")
	}
	// EC-18 provider available
	if f.RouteFound && !f.ProviderAvailable {
		add("EC-18", Defer, "PROVIDER_UNAVAILABLE")
	} else {
		add("EC-18", Pass, "")
	}
	// EC-19 pool/ledger integrity: the latest integrity run must have passed
	if f.LedgerIntegrityOK {
		add("EC-19", Pass, "")
	} else {
		add("EC-19", Fail, "LEDGER_INTEGRITY_NOT_CONFIRMED")
	}
	// EC-20 settlement freshness: settlement reconciliation is NOT_PERFORMED (no statement source). A TEST payout
	// of synthetic funds records NOT_APPLICABLE; anything else fails (production available funds = BLOCKED).
	if f.TestMode {
		add("EC-20", NA, "SETTLEMENT_RECONCILIATION_NOT_PERFORMED_TEST_PAYOUT")
	} else {
		add("EC-20", Fail, "SETTLEMENT_EVIDENCE_UNAVAILABLE")
	}
	// EC-21 limits configured (fail closed: unconfigured ⇒ review and the DUAL tier)
	if !f.DualApprovalMin.Configured || !f.MinAmount.Configured || !f.MaxPerTransaction.Configured {
		add("EC-21", Review, "LIMITS_UNCONFIGURED")
	} else {
		add("EC-21", Pass, "")
	}
	// EC-22 approvals obtained (submit only)
	switch {
	case !submit:
		add("EC-22", NA, "")
	case f.RejectingApprovals > 0:
		add("EC-22", Fail, "REJECTED_BY_APPROVER")
	case f.ValidApprovals < f.RequiredApprovals || f.RequiredApprovals < 1:
		add("EC-22", Fail, "APPROVALS_MISSING_OR_EXPIRED")
	default:
		add("EC-22", Pass, "")
	}

	// At submission, REVIEW findings the approvers already saw are acknowledged by their approval; only new
	// findings send the payout back to review (Y7). DEFER stays DEFER (wait and retry).
	if submit && len(f.AcknowledgedReviews) > 0 {
		ack := map[string]bool{}
		for _, c := range f.AcknowledgedReviews {
			ack[c] = true
		}
		for i, r := range d.Results {
			if r.Result == Review && ack[r.CheckID+":"+r.Detail] {
				d.Results[i] = CheckResult{r.CheckID, Pass, "ACKNOWLEDGED_BY_APPROVAL:" + r.Detail}
			}
		}
	}

	fail, review, deferred := false, false, false
	for _, r := range d.Results {
		switch r.Result {
		case Fail:
			fail = true
		case Review:
			review = true
		case Defer:
			deferred = true
		}
	}
	switch {
	case fail:
		d.Outcome, d.ReasonCode = OutcomeRejected, reasonCategory(d.FailedChecks())
	case review:
		d.Outcome = OutcomeReview
	case deferred:
		d.Outcome = OutcomeDeferred
	default:
		d.Outcome = OutcomeEligible
	}
	if !fail {
		d.ApprovalTier = tier(f)
	}
	return d
}

// tier: DUAL when the dual threshold is unconfigured (fail closed) or reached, on any risk indicator, and for the
// first payout of an organisation campaign; SINGLE otherwise. There is no AUTO tier (PD-24).
func tier(f Facts) string {
	if !f.DualApprovalMin.Configured || f.Amount >= f.DualApprovalMin.Value || len(f.Indicators) > 0 || f.FirstOrgPayout {
		return "DUAL"
	}
	return "SINGLE"
}

// reasonCategory maps failing checks to a coarse, owner-safe category (no fraud or compliance detail, LR-008).
func reasonCategory(failed []string) string {
	order := []struct{ check, cat string }{
		{"EC-13", "NOT_AVAILABLE"}, {"EC-12", "PAYOUTS_PAUSED"}, {"EC-08", "PAYOUTS_UNAVAILABLE"}, {"EC-15", "PAYOUTS_UNAVAILABLE"},
		{"EC-20", "PAYOUTS_UNAVAILABLE"}, {"EC-19", "PAYOUTS_UNAVAILABLE"}, {"EC-01", "CAMPAIGN_NOT_ELIGIBLE"}, {"EC-02", "NOT_AUTHORISED"},
		{"EC-03", "VERIFICATION_REQUIRED"}, {"EC-04", "VERIFICATION_REQUIRED"}, {"EC-05", "DESTINATION_NOT_READY"},
		{"EC-11", "PAYOUT_IN_PROGRESS"}, {"EC-09", "INSUFFICIENT_FUNDS"}, {"EC-10", "AMOUNT_NOT_ALLOWED"}, {"EC-22", "APPROVAL_REQUIRED"},
	}
	set := map[string]bool{}
	for _, c := range failed {
		set[c] = true
	}
	for _, o := range order {
		if set[o.check] {
			return o.cat
		}
	}
	return "NOT_ELIGIBLE"
}

// recordHash is SHA-256 over the canonical decision record (tamper evidence).
func recordHash(v any) []byte {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return h[:]
}
