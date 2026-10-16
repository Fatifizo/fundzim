package payouts

import (
	"strings"
	"testing"
	"time"
)

// baseFacts is a fully eligible TEST payout request.
func baseFacts(phase string) Facts {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	past := now.Add(-time.Hour)
	mn, mx := int64(100), int64(1_000_000)
	b := "b1"
	return Facts{Phase: phase, At: now, Amount: 10_000, Currency: "USD", TestMode: true, ExecutionEnvironment: "LOCAL_SIMULATOR",
		LedgerEnvironment: "TEST", CurrencyPostable: true, RouteFound: true, ProviderMinMinor: &mn, ProviderMaxMinor: &mx, ProviderAvailable: true,
		CampaignStatus: "ACTIVE", RequesterAuthorised: true, RequesterStatus: "ACTIVE", OwnerKind: "USER", OwnerLevel: "PAYOUT_VERIFIED",
		OwnerStatus: "ACTIVE", RequesterLevel: "PAYOUT_VERIFIED", BeneficiaryID: &b, BeneficiaryType: "SELF", BeneficiaryStatus: "DRAFT",
		DestinationFound: true, DestinationStatus: "VERIFIED", DestinationOwnership: "CONFIRMED", DestinationCompliance: "APPROVED",
		DestinationCurrency: "USD", DestinationOwnedByCampaign: true, DestinationPayeeType: "OWNER", DestinationCoolingOffUntil: &past,
		DestinationVersion: 3, SnapshotVersion: 3, PayableMinor: 50_000, ReservationActive: true, PendingMinor: 10_000,
		MinAmount: Limit{Configured: true, Value: 100}, MaxPerTransaction: Limit{Configured: true, Value: 100_000},
		DualApprovalMin: Limit{Configured: true, Value: 50_000}, VelocityCount7d: Limit{Configured: true, Value: 5},
		VelocityAmount7d: Limit{Configured: true, Value: 500_000}, LedgerIntegrityOK: true, RequiredApprovals: 1, ValidApprovals: 1,
		Indicators: []string{}}
}

func TestEvaluateFullyEligible(t *testing.T) {
	for _, ph := range []string{PhaseRequest, PhasePreSubmit} {
		d := Evaluate(baseFacts(ph))
		if d.Outcome != OutcomeEligible || d.ApprovalTier != "SINGLE" || len(d.Results) != 22 {
			t.Fatalf("%s: %+v", ph, d)
		}
		seen := map[string]bool{}
		for _, r := range d.Results {
			if seen[r.CheckID] {
				t.Fatalf("%s: check %s twice", ph, r.CheckID)
			}
			seen[r.CheckID] = true
		}
	}
}

func TestEvaluateEachBlockingCondition(t *testing.T) {
	cases := []struct {
		name, phase, check, result string
		mut                        func(*Facts)
	}{
		{"campaign paused", PhaseRequest, "EC-01", Fail, func(f *Facts) { f.CampaignStatus = "PAUSED" }},
		{"completed campaign is eligible", PhaseRequest, "EC-01", Pass, func(f *Facts) { f.CampaignStatus = "COMPLETED" }},
		{"not owner", PhaseRequest, "EC-02", Fail, func(f *Facts) { f.RequesterAuthorised = false }},
		{"owner identity only", PhaseRequest, "EC-03", Fail, func(f *Facts) { f.OwnerLevel = "IDENTITY_VERIFIED" }},
		{"org not payout verified", PhaseRequest, "EC-03", Fail, func(f *Facts) { f.OwnerKind, f.OwnerLevel = "ORGANISATION", "ORG_KYB_VERIFIED" }},
		{"org representative basic", PhaseRequest, "EC-03", Fail, func(f *Facts) {
			f.OwnerKind, f.OwnerLevel, f.RequesterLevel = "ORGANISATION", "ORG_PAYOUT_VERIFIED", "BASIC_VERIFIED"
		}},
		{"third-party beneficiary unverified", PhaseRequest, "EC-04", Fail, func(f *Facts) { f.BeneficiaryType = "INDIVIDUAL" }},
		{"destination unverified", PhaseRequest, "EC-05", Fail, func(f *Facts) { f.DestinationStatus = "PENDING_VERIFICATION" }},
		{"destination of someone else", PhaseRequest, "EC-05", Fail, func(f *Facts) { f.DestinationOwnedByCampaign = false }},
		{"owner account for a third-party beneficiary", PhaseRequest, "EC-05", Fail, func(f *Facts) {
			f.BeneficiaryType, f.BeneficiaryStatus = "INDIVIDUAL", "VERIFIED"
		}},
		{"cross-beneficiary destination", PhaseRequest, "EC-05", Fail, func(f *Facts) {
			other := "b2"
			f.BeneficiaryType, f.BeneficiaryStatus, f.DestinationPayeeType, f.DestinationBeneficiaryID = "INDIVIDUAL", "VERIFIED", "BENEFICIARY", &other
		}},
		{"verification expired", PhaseRequest, "EC-05", Fail, func(f *Facts) { e := f.At.Add(-time.Minute); f.DestinationExpiresAt = &e }},
		{"cooling-off", PhaseRequest, "EC-06", Defer, func(f *Facts) { c := f.At.Add(time.Hour); f.DestinationCoolingOffUntil = &c }},
		{"destination changed since request", PhasePreSubmit, "EC-07", Review, func(f *Facts) { f.DestinationVersion = 4 }},
		{"execution disabled", PhaseRequest, "EC-08", Fail, func(f *Facts) { f.ExecutionEnvironment = "DISABLED" }},
		{"production ledger", PhaseRequest, "EC-08", Fail, func(f *Facts) { f.LedgerEnvironment = "PRODUCTION" }},
		{"ZWG", PhaseRequest, "EC-08", Fail, func(f *Facts) { f.CurrencyPostable = false }},
		{"currency differs from destination", PhaseRequest, "EC-08", Fail, func(f *Facts) { f.DestinationCurrency = "ZWG" }},
		{"no route", PhaseRequest, "EC-08", Fail, func(f *Facts) { f.RouteFound = false }},
		{"insufficient funds", PhaseRequest, "EC-09", Fail, func(f *Facts) { f.PayableMinor = 9_999 }},
		{"refund exposure", PhaseRequest, "EC-09", Fail, func(f *Facts) { f.RefundExposureMinor = 40_001 }},
		{"frozen payable account", PhaseRequest, "EC-09", Fail, func(f *Facts) { f.PayableAccountStatus = "FROZEN" }},
		{"reservation not intact", PhasePreSubmit, "EC-09", Fail, func(f *Facts) { f.PendingMinor = 9_999 }},
		{"reservation released", PhasePreSubmit, "EC-09", Fail, func(f *Facts) { f.ReservationActive = false }},
		{"below provider minimum", PhaseRequest, "EC-10", Fail, func(f *Facts) { f.Amount = 99 }},
		{"above internal maximum", PhaseRequest, "EC-10", Review, func(f *Facts) { f.Amount, f.PayableMinor = 200_000, 300_000 }},
		{"unconfigured limits", PhaseRequest, "EC-10", Review, func(f *Facts) { f.MinAmount = Limit{} }},
		{"provider limits unknown", PhaseRequest, "EC-10", Review, func(f *Facts) { f.ProviderMaxMinor = nil }},
		{"in flight", PhaseRequest, "EC-11", Fail, func(f *Facts) { f.OtherInFlight = true }},
		{"suspended campaign hold", PhaseRequest, "EC-12", Fail, func(f *Facts) { f.CampaignStatus = "SUSPENDED" }},
		{"suspended destination", PhaseRequest, "EC-12", Fail, func(f *Facts) { f.DestinationStatus = "SUSPENDED" }},
		{"risk hold (Stage 13)", PhaseRequest, "EC-12", Fail, func(f *Facts) { f.RiskHolds = []string{"WITHDRAWAL_HOLD"} }},
		{"risk hold at submission", PhasePreSubmit, "EC-12", Fail, func(f *Facts) { f.RiskHolds = []string{"PAYOUT_DESTINATION_HOLD"} }},
		{"compliance restriction", PhaseRequest, "EC-13", Fail, func(f *Facts) { f.RestrictionLevel = "RESTRICTED" }},
		{"TEST screening potential match", PhaseRequest, "EC-15", Review, func(f *Facts) { f.ScreeningStatus = "POTENTIAL_MATCH" }},
		{"TEST screening confirmed match", PhaseRequest, "EC-15", Fail, func(f *Facts) { f.ScreeningStatus = "CONFIRMED_MATCH" }},
		{"TEST screening timed out is not clear", PhaseRequest, "EC-15", Review, func(f *Facts) { f.ScreeningStatus = "ERROR" }},
		{"TEST screening pending is not clear", PhaseRequest, "EC-15", Review, func(f *Facts) { f.ScreeningStatus = "PENDING" }},
		{"high risk classification", PhaseRequest, "EC-16", Review, func(f *Facts) { f.Indicators = []string{"RISK_CLASSIFICATION_HIGH"} }},
		{"production payout: no screening", PhaseRequest, "EC-15", Fail, func(f *Facts) { f.TestMode = false }},
		{"risk indicator", PhaseRequest, "EC-16", Review, func(f *Facts) { f.Indicators = []string{"RECENT_DESTINATION_CHANGE"} }},
		{"velocity", PhaseRequest, "EC-17", Review, func(f *Facts) { f.RecentPayoutCount7d = 5 }},
		{"provider outage", PhaseRequest, "EC-18", Defer, func(f *Facts) { f.ProviderAvailable = false }},
		{"integrity unconfirmed", PhaseRequest, "EC-19", Fail, func(f *Facts) { f.LedgerIntegrityOK = false }},
		{"production payout: no settlement evidence", PhaseRequest, "EC-20", Fail, func(f *Facts) { f.TestMode = false }},
		{"dual threshold unconfigured", PhaseRequest, "EC-21", Review, func(f *Facts) { f.DualApprovalMin = Limit{} }},
		{"approvals missing", PhasePreSubmit, "EC-22", Fail, func(f *Facts) { f.ValidApprovals = 0 }},
		{"rejected by an approver", PhasePreSubmit, "EC-22", Fail, func(f *Facts) { f.RejectingApprovals = 1 }},
	}
	for _, c := range cases {
		f := baseFacts(c.phase)
		c.mut(&f)
		d := Evaluate(f)
		if r := d.Result(c.check); r.Result != c.result {
			t.Fatalf("%s: %s = %+v, want %s", c.name, c.check, r, c.result)
		}
		want := map[string]string{Fail: OutcomeRejected, Review: OutcomeReview, Defer: OutcomeDeferred, Pass: OutcomeEligible}[c.result]
		if d.Outcome != want {
			t.Fatalf("%s: outcome %s, want %s (%+v)", c.name, d.Outcome, want, d.Results)
		}
		if c.result == Fail && (d.ReasonCode == "" || d.ApprovalTier != "") {
			t.Fatalf("%s: refusal needs a reason category and no tier: %+v", c.name, d)
		}
	}
}

func TestEvaluateTiersFailClosed(t *testing.T) {
	f := baseFacts(PhaseRequest)
	f.DualApprovalMin = Limit{}
	if d := Evaluate(f); d.ApprovalTier != "DUAL" {
		t.Fatalf("unconfigured dual threshold must be DUAL: %s", d.ApprovalTier)
	}
	f = baseFacts(PhaseRequest)
	f.Amount = 50_000
	if d := Evaluate(f); d.ApprovalTier != "DUAL" {
		t.Fatalf("amount at the threshold must be DUAL: %s", d.ApprovalTier)
	}
	f = baseFacts(PhaseRequest)
	f.Indicators = []string{"MULTIPLE_DESTINATIONS"}
	if d := Evaluate(f); d.ApprovalTier != "DUAL" {
		t.Fatal("a risk indicator must raise the tier to DUAL")
	}
	f = baseFacts(PhaseRequest)
	f.OwnerKind, f.OwnerLevel, f.FirstOrgPayout = "ORGANISATION", "ORG_PAYOUT_VERIFIED", true
	if d := Evaluate(f); d.ApprovalTier != "DUAL" {
		t.Fatal("the first payout of an organisation campaign must be DUAL")
	}
}

func TestEvaluateAcknowledgedReviewsAtSubmission(t *testing.T) {
	// a review finding the approvers saw does not block submission; a NEW finding sends the payout back
	f := baseFacts(PhasePreSubmit)
	f.DualApprovalMin = Limit{}
	f.RequiredApprovals, f.ValidApprovals = 2, 2
	if d := Evaluate(f); d.Outcome != OutcomeReview {
		t.Fatalf("unacknowledged review: %s", d.Outcome)
	}
	f.AcknowledgedReviews = []string{"EC-21:LIMITS_UNCONFIGURED"}
	d := Evaluate(f)
	if d.Outcome != OutcomeEligible || !strings.HasPrefix(d.Result("EC-21").Detail, "ACKNOWLEDGED_BY_APPROVAL:") {
		t.Fatalf("acknowledged review: %+v", d.Result("EC-21"))
	}
	f.Indicators = []string{"RECENT_DESTINATION_CHANGE"}
	if d := Evaluate(f); d.Outcome != OutcomeReview || d.Result("EC-16").Result != Review {
		t.Fatalf("new finding must re-route: %s", d.Outcome)
	}
	// DEFER is never acknowledged away
	f = baseFacts(PhasePreSubmit)
	c := f.At.Add(time.Hour)
	f.DestinationCoolingOffUntil = &c
	f.AcknowledgedReviews = []string{"EC-06:DESTINATION_COOLING_OFF"}
	if d := Evaluate(f); d.Outcome != OutcomeDeferred {
		t.Fatalf("cooling-off at submission: %s", d.Outcome)
	}
}

func TestReasonCategoriesNeverRevealComplianceOrRisk(t *testing.T) {
	f := baseFacts(PhaseRequest)
	f.RestrictionLevel, f.PayableMinor = "SUSPENDED", 0
	d := Evaluate(f)
	if d.ReasonCode != "NOT_AVAILABLE" {
		t.Fatalf("a compliance restriction must surface only as NOT_AVAILABLE, got %s", d.ReasonCode)
	}
	for _, r := range ownerSafe(d.Results) {
		if (r.CheckID == "EC-13" || r.CheckID == "EC-16") && r.Detail != "" && r.Detail != "NOT_AVAILABLE" {
			t.Fatalf("owner-safe results leak %s: %s", r.CheckID, r.Detail)
		}
	}
}

func TestOwnerViewHidesInternals(t *testing.T) {
	code := "STAFF_REJECTED"
	v := Payout{ID: "p", Status: StRejected, DecisionReasonCode: &code, TestPayout: true}.Owner()
	if v.ReasonCategory == nil || *v.ReasonCategory != "NOT_AVAILABLE" || v.FundsLabel != "TEST_ONLY" || v.CanCancel {
		t.Fatalf("owner view: %+v", v)
	}
	v = Payout{Status: StApproved}.Owner()
	if !v.CanCancel {
		t.Fatal("an approved payout can be cancelled before submission")
	}
	v = Payout{Status: StUnknown}.Owner()
	if v.CanCancel || !strings.Contains(v.StatusLabel, "confirming") {
		t.Fatalf("unknown: %+v", v)
	}
}
