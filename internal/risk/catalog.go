package risk

import (
	"encoding/json"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// The Stage 13 signal catalogue (ADR-045 §1): every signal type has a category, a severity and a default confidence.
// Signals are observations ("a destination changed"), never conclusions; a DERIVED signal names the rule version that
// inferred it. No provider risk signal is invented: only events FundZim itself emits are mapped.

// Signal categories.
const (
	CatIdentity      = "IDENTITY_RISK"
	CatCampaign      = "CAMPAIGN_RISK"
	CatPayment       = "PAYMENT_RISK"
	CatWithdrawal    = "WITHDRAWAL_RISK"
	CatDestination   = "DESTINATION_RISK"
	CatSession       = "SESSION_RISK"
	CatDevice        = "DEVICE_RISK"
	CatVelocity      = "VELOCITY_RISK"
	CatCompliance    = "COMPLIANCE_RISK"
	CatProvider      = "PROVIDER_RISK"
	CatFee           = "FEE_MANIPULATION_RISK"
	CatChargeback    = "CHARGEBACK_RISK"
	SevInfo, SevLow  = "INFO", "LOW"
	SevMed, SevHigh  = "MEDIUM", "HIGH"
	KindObserved     = "OBSERVED"
	KindDerived      = "DERIVED"
	EnvTest          = "TEST"
	subjectCampaign  = "CAMPAIGN"
	subjectPayment   = "PAYMENT"
	subjectPayout    = "PAYOUT"
	subjectBenef     = "BENEFICIARY"
	subjectDest      = "PAYOUT_DESTINATION"
	subjectDonIntent = "DONATION_INTENT"
)

// Stage 13 signal types (Stage 5 types are in model.go).
const (
	SigCampaignCreated         = "CAMPAIGN_CREATED"
	SigCampaignReported        = "CAMPAIGN_REPORTED"
	SigCampaignReviewEscalated = "CAMPAIGN_REVIEW_ESCALATED"
	SigDonationIntentCreated   = "DONATION_INTENT_CREATED"
	SigPaymentFailed           = "PAYMENT_FAILED"
	SigProviderContradiction   = "PROVIDER_EVIDENCE_CONTRADICTION"
	SigWithdrawalRequested     = "WITHDRAWAL_REQUESTED"
	SigPayoutFailed            = "PAYOUT_FAILED"
	SigPayoutReturned          = "PAYOUT_RETURNED"
	SigMFAChanged              = "MFA_CHANGED"
	SigPasswordChanged         = "PASSWORD_CHANGED"
	SigPasswordResetRequested  = "PASSWORD_RESET_REQUESTED"
	SigAccountSuspended        = "ACCOUNT_SUSPENDED"
	SigSignedIn                = "SIGNED_IN"
	SigRestrictionApplied      = "RESTRICTION_APPLIED"
	SigFeePolicyChanged        = "FEE_POLICY_CHANGED"
	SigFeeAdjustmentRequested  = "FEE_ADJUSTMENT_REQUESTED"
	SigScreeningPotentialMatch = "SCREENING_POTENTIAL_MATCH"
	SigScreeningConfirmedMatch = "SCREENING_CONFIRMED_MATCH"
)

// Event types consumed from Stage 13 on.
const (
	EvCampaignCreated        = "campaigns.created"
	EvCampaignReportEscal    = "campaigns.report_escalated"
	EvCampaignReviewEscal    = "campaigns.review_escalated"
	EvDonationIntentCreated  = "payments.donation_intent_created"
	EvPaymentStatusChanged   = "payments.payment_status_changed"
	EvPaymentContradiction   = "payments.evidence_contradiction"
	EvPayoutRequested        = "payouts.requested"
	EvPayoutStatusChanged    = "payouts.status_changed"
	EvMFAChanged             = "identity.mfa_changed"
	EvPasswordChanged        = "identity.password_changed"
	EvPasswordResetRequested = "identity.password_reset_requested"
	EvAccountSuspended       = "identity.account_suspended"
	EvSignedIn               = "identity.signed_in"
	EvRestrictionApplied     = "compliance.restriction_applied"
	EvFeePolicyChanged       = "fees.policy_version_changed"
	EvFeeAdjustmentRequested = "fees.adjustment_requested"
)

// SignalMeta describes a signal type.
type SignalMeta struct {
	Known      bool
	Category   string
	Severity   string
	Confidence int
	modelV1    bool // scored by the Stage 5 model v1 (onboarding rating)
}

var catalog = map[string]SignalMeta{
	SigDocumentRejected:        {true, CatIdentity, SevLow, 100, true},
	SigKYCRejected:             {true, CatIdentity, SevMed, 100, true},
	SigKYBRejected:             {true, CatIdentity, SevMed, 100, true},
	SigDuplicateIdentity:       {true, CatIdentity, SevHigh, 90, true},
	SigBeneficiaryRejected:     {true, CatIdentity, SevMed, 100, true},
	SigBeneficiaryEscalated:    {true, CatIdentity, SevMed, 100, true},
	SigDestinationChanged:      {true, CatDestination, SevLow, 100, true},
	SigDestinationRejected:     {true, CatDestination, SevMed, 100, true},
	SigCampaignCreated:         {true, CatCampaign, SevInfo, 100, false},
	SigCampaignReported:        {true, CatCampaign, SevMed, 60, false}, // a report is an allegation, not a fact about the campaign
	SigCampaignReviewEscalated: {true, CatCampaign, SevMed, 80, false},
	SigDonationIntentCreated:   {true, CatVelocity, SevInfo, 100, false},
	SigPaymentFailed:           {true, CatPayment, SevLow, 100, false},
	SigProviderContradiction:   {true, CatProvider, SevMed, 80, false},
	SigWithdrawalRequested:     {true, CatWithdrawal, SevInfo, 100, false},
	SigPayoutFailed:            {true, CatWithdrawal, SevLow, 100, false},
	SigPayoutReturned:          {true, CatWithdrawal, SevMed, 100, false},
	SigMFAChanged:              {true, CatSession, SevLow, 100, false},
	SigPasswordChanged:         {true, CatSession, SevLow, 100, false},
	SigPasswordResetRequested:  {true, CatSession, SevLow, 70, false}, // anyone can request a reset for an address
	SigAccountSuspended:        {true, CatSession, SevMed, 100, false},
	SigSignedIn:                {true, CatSession, SevInfo, 100, false},
	SigRestrictionApplied:      {true, CatCompliance, SevHigh, 100, false},
	SigFeePolicyChanged:        {true, CatFee, SevLow, 100, false},
	SigFeeAdjustmentRequested:  {true, CatFee, SevLow, 100, false},
	SigScreeningPotentialMatch: {true, CatCompliance, SevHigh, 50, false}, // a potential match is not a finding
	SigScreeningConfirmedMatch: {true, CatCompliance, SevHigh, 100, false},
}

// SignalMetaOf returns the catalogue entry of a signal type (Known false when absent).
func SignalMetaOf(t string) SignalMeta { return catalog[t] }

// SignalTypes lists the catalogue (for the rule editor).
func SignalTypes() map[string]SignalMeta {
	out := make(map[string]SignalMeta, len(catalog))
	for k, v := range catalog {
		out[k] = v
	}
	return out
}

// v2Payload is the union of fields the Stage 13 events carry (ids, codes and amounts only).
type v2Payload struct {
	CampaignID          string `json:"campaign_id"`
	OwnerUserID         string `json:"owner_user_id"`
	OwnerOrganisationID string `json:"owner_organisation_id"`
	TitleHash           string `json:"title_hash"`
	ReasonCode          string `json:"reason_code"`
	ReportID            string `json:"report_id"`
	DonationIntentID    string `json:"donation_intent_id"`
	DonorUserID         string `json:"donor_user_id"`
	AmountMinor         string `json:"amount_minor"`
	Currency            string `json:"currency"`
	PaymentID           string `json:"payment_id"`
	To                  string `json:"to"`
	PayoutRequestID     string `json:"payout_request_id"`
	RequestedBy         string `json:"requested_by"`
	DestinationID       string `json:"destination_id"`
	UserID              string `json:"user_id"`
	Change              string `json:"change"`
	UAHash              string `json:"ua_hash"`
	SubjectType         string `json:"subject_type"`
	SubjectID           string `json:"subject_id"`
	Level               string `json:"level"`
	ActorID             string `json:"actor_id"`
	VersionID           string `json:"version_id"`
	Action              string `json:"action"`
	AssessmentID        string `json:"assessment_id"`
	Method              string `json:"method"`
}

var hexRE = func(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

// signalFromV2Event maps the Stage 13 events. handled is false for event types it does not know.
func signalFromV2Event(d outbox.Delivery) (sig Signal, ok, handled bool, err error) {
	var p v2Payload
	switch d.EventType {
	case EvCampaignCreated, EvCampaignReportEscal, EvCampaignReviewEscal, EvDonationIntentCreated, EvPaymentStatusChanged, EvPaymentContradiction,
		EvPayoutRequested, EvPayoutStatusChanged, EvMFAChanged, EvPasswordChanged, EvPasswordResetRequested, EvAccountSuspended, EvSignedIn,
		EvRestrictionApplied, EvFeePolicyChanged, EvFeeAdjustmentRequested:
	default:
		return Signal{}, false, false, nil
	}
	if err := json.Unmarshal(d.Payload, &p); err != nil {
		return Signal{}, false, true, ErrInvalidPayload
	}
	if d.OccurredAt.IsZero() || !ids.Valid(d.EventID) {
		return Signal{}, false, true, ErrInvalidPayload
	}
	base := Signal{SourceEventID: d.EventID, SourceEventType: d.EventType, ObservedAt: d.OccurredAt.UTC(), CorrelationID: d.CorrelationID}
	set := func(typ, subjectType, subjectID, module string, facts map[string]any) (Signal, bool, bool, error) {
		if !ids.Valid(subjectID) {
			return Signal{}, false, true, ErrInvalidPayload
		}
		base.Type, base.SubjectType, base.SubjectID, base.SourceModule, base.Facts = typ, subjectType, subjectID, module, facts
		return base, true, true, nil
	}
	owner := func() (string, string) {
		if ids.Valid(p.OwnerUserID) {
			return ownerUser, p.OwnerUserID
		}
		return ownerOrg, p.OwnerOrganisationID
	}
	switch d.EventType {
	case EvCampaignCreated:
		t, id := owner()
		facts := map[string]any{"campaign_id": p.CampaignID}
		if hexRE(p.TitleHash) {
			facts["title_hash"] = p.TitleHash
		}
		return set(SigCampaignCreated, t, id, "campaigns", facts)
	case EvCampaignReportEscal:
		return set(SigCampaignReported, subjectCampaign, p.CampaignID, "campaigns", map[string]any{"reason_code": reason(p.ReasonCode)})
	case EvCampaignReviewEscal:
		return set(SigCampaignReviewEscalated, subjectCampaign, p.CampaignID, "campaigns", map[string]any{"reason_code": reason(p.ReasonCode)})
	case EvDonationIntentCreated:
		return set(SigDonationIntentCreated, ownerUser, p.DonorUserID, "payments", map[string]any{"campaign_id": p.CampaignID,
			"donation_intent_id": p.DonationIntentID, "amount_minor": digits(p.AmountMinor), "currency": shortCode(p.Currency)})
	case EvPaymentStatusChanged:
		if strings.ToUpper(p.To) != "FAILED" {
			return Signal{}, false, true, nil
		}
		return set(SigPaymentFailed, ownerUser, p.DonorUserID, "payments", map[string]any{"payment_id": p.PaymentID, "campaign_id": p.CampaignID})
	case EvPaymentContradiction:
		return set(SigProviderContradiction, subjectPayment, p.PaymentID, "payments", map[string]any{})
	case EvPayoutRequested:
		return set(SigWithdrawalRequested, ownerUser, p.RequestedBy, "payouts", map[string]any{"payout_request_id": p.PayoutRequestID,
			"campaign_id": p.CampaignID, "amount_minor": digits(p.AmountMinor), "currency": shortCode(p.Currency), "destination_id": p.DestinationID})
	case EvPayoutStatusChanged:
		typ := map[string]string{"FAILED": SigPayoutFailed, "REVERSED": SigPayoutReturned}[strings.ToUpper(p.To)]
		if typ == "" {
			return Signal{}, false, true, nil
		}
		return set(typ, ownerUser, p.RequestedBy, "payouts", map[string]any{"payout_request_id": p.PayoutRequestID, "campaign_id": p.CampaignID})
	case EvMFAChanged:
		return set(SigMFAChanged, ownerUser, p.UserID, "identity", map[string]any{"reason_code": shortCode(p.Change)})
	case EvPasswordChanged:
		return set(SigPasswordChanged, ownerUser, p.UserID, "identity", map[string]any{})
	case EvPasswordResetRequested:
		return set(SigPasswordResetRequested, ownerUser, p.UserID, "identity", map[string]any{})
	case EvAccountSuspended:
		return set(SigAccountSuspended, ownerUser, p.UserID, "identity", map[string]any{})
	case EvSignedIn:
		facts := map[string]any{}
		if hexRE(p.UAHash) {
			facts["ua_hash"] = p.UAHash // a keyed hash of the user agent: no raw device data, no IP address
		}
		return set(SigSignedIn, ownerUser, p.UserID, "identity", facts)
	case EvRestrictionApplied:
		st := strings.ToUpper(p.SubjectType)
		switch st {
		case ownerUser, ownerOrg, subjectCampaign, subjectBenef, subjectDest:
		default:
			return Signal{}, false, true, ErrInvalidPayload
		}
		return set(SigRestrictionApplied, st, p.SubjectID, "compliance", map[string]any{"reason_code": shortCode(p.Level)})
	case EvFeePolicyChanged:
		return set(SigFeePolicyChanged, ownerUser, p.ActorID, "fees", map[string]any{"reason_code": shortCode(p.Action)})
	case EvFeeAdjustmentRequested:
		return set(SigFeeAdjustmentRequested, ownerUser, p.ActorID, "fees", map[string]any{"amount_minor": digits(p.AmountMinor),
			"reason_code": reason(p.ReasonCode)})
	}
	return Signal{}, false, false, nil
}

func digits(s string) string {
	if len(s) == 0 || len(s) > 18 {
		return ""
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return s
}
