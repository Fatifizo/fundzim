package notifications

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/money"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Event → notification mapping (docs/stage-15/event-contracts.md). Only existing outbox events are mapped; the four
// operations events are new in Stage 15. Payloads carry ids and codes; texts come from templates.

// Mapped event types.
var (
	campaignEvents = map[string]string{
		"campaigns.created": "CAMPAIGN_CREATED", "campaigns.submitted": "CAMPAIGN_SUBMITTED", "campaigns.changes_requested": "CAMPAIGN_CHANGES_REQUESTED",
		"campaigns.approved": "CAMPAIGN_APPROVED", "campaigns.rejected": "CAMPAIGN_REJECTED", "campaigns.published": "CAMPAIGN_PUBLISHED",
		"campaigns.paused": "CAMPAIGN_PAUSED", "campaigns.suspended": "CAMPAIGN_SUSPENDED", "campaigns.reactivated": "CAMPAIGN_REACTIVATED",
		"campaigns.completed": "CAMPAIGN_COMPLETED",
	}
	verificationEvents = map[string]string{
		"kyc.case_submitted": "KYC_CASE_SUBMITTED", "kyc.information_requested": "KYC_INFORMATION_REQUESTED", "kyc.case_approved": "KYC_CASE_APPROVED",
		"kyc.case_rejected": "KYC_CASE_REJECTED", "kyc.verification_expired": "KYC_VERIFICATION_EXPIRED",
		"kyc.verification_suspended": "KYC_VERIFICATION_SUSPENDED", "kyc.verification_revoked": "KYC_VERIFICATION_REVOKED",
		"beneficiaries.verification_submitted": "BENEFICIARY_VERIFICATION_SUBMITTED", "beneficiaries.verification_information_requested": "BENEFICIARY_INFORMATION_REQUESTED",
		"beneficiaries.verification_approved": "BENEFICIARY_VERIFICATION_APPROVED", "beneficiaries.verification_rejected": "BENEFICIARY_VERIFICATION_REJECTED",
		"beneficiaries.verification_expired": "BENEFICIARY_VERIFICATION_EXPIRED",
		"payouts.destination_verified":       "PAYOUT_DESTINATION_VERIFIED", "payouts.destination_rejected": "PAYOUT_DESTINATION_REJECTED",
		"payouts.destination_information_requested": "PAYOUT_DESTINATION_INFORMATION_REQUESTED", "payouts.destination_suspended": "PAYOUT_DESTINATION_SUSPENDED",
		"payouts.destination_expired": "PAYOUT_DESTINATION_EXPIRED",
	}
	payoutStatusTemplates = map[string]struct{ key, cat string }{
		"PENDING_REVIEW": {"WITHDRAWAL_UNDER_REVIEW", CatWithdrawal}, "APPROVED": {"WITHDRAWAL_APPROVED", CatWithdrawal},
		"REJECTED": {"WITHDRAWAL_REJECTED", CatWithdrawal}, "CANCELLED": {"WITHDRAWAL_CANCELLED", CatWithdrawal},
		"SUBMITTED": {"PAYOUT_SUBMITTED", CatPayout}, "UNKNOWN": {"PAYOUT_OUTCOME_UNKNOWN", CatPayout},
		// COMPLETED is the payout state reached only on the provider's confirmation (Stage 11 machine)
		"COMPLETED": {"PAYOUT_COMPLETED", CatPayout}, "FAILED": {"PAYOUT_FAILED", CatPayout}, "REVERSED": {"PAYOUT_REVERSED", CatPayout},
	}
	mfaChanges = map[string]string{"enabled": "Two-step verification was turned on", "disabled": "Two-step verification was turned off",
		"recovery_code_used": "A recovery code was used to sign in", "recovery_codes_regenerated": "New recovery codes were generated"}
	otherEvents = []string{"identity.password_changed", "identity.mfa_changed", "identity.email_change_requested", "identity.registration_existing_account",
		"identity.account_suspended", "auth.staff_link_confirmed", "payouts.destination_created", "payouts.destination_changed",
		"payments.payment_status_changed", "payments.payment_succeeded", "payouts.requested", "payouts.status_changed", "payouts.exception_opened",
		"payments.refund_requested", "fees.adjustment_requested", "risk.rule_version_changed", "risk.case_opened", "risk.case_escalated",
		"compliance.case_opened", EvSupportCaseOpened, EvSupportCaseAssigned, EvSupportCaseStatusChanged, EvIncidentDeclared,
		EvReconExceptionOpened, EvReconExceptionAssigned, EvReconImportFailed, EvReconResolutionRequested, EvReconUnknownOutcome}
)

// Operations events added in Stage 15 (written by internal/operations in the same transaction as the change).
const (
	EvSupportCaseOpened        = "operations.support_case_opened"         // {case_id, case_number, requester_user_id?}
	EvSupportCaseAssigned      = "operations.support_case_assigned"       // {case_id, case_number, assigned_to}
	EvSupportCaseStatusChanged = "operations.support_case_status_changed" // {case_id, case_number, requester_user_id?, from, to}
	EvIncidentDeclared         = "operations.incident_declared"           // {incident_id, incident_number, severity, affected_module}
)

// Reconciliation events added in Stage 17 (written by internal/reconciliation; staff, in-app, always TEST evidence).
const (
	EvReconExceptionOpened     = "reconciliation.exception_opened"          // {exception_id, exception_type, severity}
	EvReconExceptionAssigned   = "reconciliation.exception_assigned"        // {exception_id, exception_type, severity, assignee_id, actor_id}
	EvReconImportFailed        = "reconciliation.import_failed"             // {import_id, failure_code, source}
	EvReconResolutionRequested = "reconciliation.resolution_requested"      // {resolution_id, exception_id, resolution_type, actor_id}
	EvReconUnknownOutcome      = "reconciliation.unknown_outcome_escalated" // {exception_id, exception_type, severity}
)

// EventTypes lists every event type the engine consumes.
func EventTypes() []string {
	out := []string{}
	for k := range campaignEvents {
		out = append(out, k)
	}
	for k := range verificationEvents {
		out = append(out, k)
	}
	return append(out, otherEvents...)
}

type payload map[string]any

func (p payload) str(k string) string {
	switch v := p[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	}
	return ""
}

func (p payload) boolean(k string) bool {
	b, _ := p[k].(bool)
	return b
}

// synthetic: anything that is not production money is TEST (Stage 8–12: every payment and payout is TEST today).
func (p payload) synthetic() bool {
	if p.boolean("test_payment") || p.boolean("test_payout") || p.boolean("simulated") {
		return true
	}
	env := p.str("execution_environment")
	return env != "" && env != "PRODUCTION"
}

func amount(p payload) string {
	minor, err := strconv.ParseInt(p.str("amount_minor"), 10, 64)
	ccy, cerr := money.Default().Lookup(p.str("currency"))
	if err != nil || cerr != nil {
		return "your donation"
	}
	return money.New(minor, ccy).String()
}

func ref(id string) string {
	if len(id) >= 8 {
		return id[:8]
	}
	return id
}

// subjectRecipients resolves a USER or ORGANISATION subject to user ids (organisation: its admins).
func (s *Service) subjectRecipients(ctx context.Context, subjectType, subjectID string) ([]string, error) {
	if !ids.Valid(subjectID) {
		return nil, nil
	}
	switch subjectType {
	case "USER":
		return []string{subjectID}, nil
	case "ORGANISATION":
		if s.Directory == nil {
			return nil, nil
		}
		return s.Directory.OrganisationAdmins(ctx, subjectID)
	}
	return nil, nil
}

func (s *Service) staff(ctx context.Context, perm string, exclude ...string) ([]string, error) {
	if s.Directory == nil {
		return nil, nil
	}
	all, err := s.Directory.StaffWithPermission(ctx, perm)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, id := range all {
		if !contains(exclude, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// intentsFor maps one outbox event to notification intents (none: the event notifies nobody).
func (s *Service) intentsFor(ctx context.Context, d outbox.Delivery) ([]Intent, error) {
	var p payload
	if err := json.Unmarshal(d.Payload, &p); err != nil {
		return nil, fmt.Errorf("notifications: %s: malformed payload", d.EventType)
	}
	one := func(in Intent, recips []string, err error) ([]Intent, error) {
		if err != nil {
			return nil, err
		}
		in.Recipients = recips
		return []Intent{in}, nil
	}
	if key, ok := campaignEvents[d.EventType]; ok {
		st, sid := "USER", p.str("owner_user_id")
		if org := p.str("owner_organisation_id"); org != "" {
			st, sid = "ORGANISATION", org
		}
		r, err := s.subjectRecipients(ctx, st, sid)
		cid := p.str("campaign_id")
		return one(Intent{TemplateKey: key, Category: CatCampaign, RelatedType: "CAMPAIGN", RelatedID: cid, DeepLink: "/dashboard/campaigns/" + cid}, r, err)
	}
	if key, ok := verificationEvents[d.EventType]; ok {
		st, sid := p.str("subject_type"), p.str("subject_id")
		if st == "" {
			st, sid = p.str("owner_type"), p.str("owner_id")
		}
		r, err := s.subjectRecipients(ctx, st, sid)
		return one(Intent{TemplateKey: key, Category: CatVerification, DeepLink: "/dashboard/verification"}, r, err)
	}
	switch d.EventType {
	case "identity.password_changed":
		return one(Intent{TemplateKey: "PASSWORD_CHANGED", Category: CatSecurity, Priority: "HIGH", DeepLink: "/settings/security"},
			[]string{p.str("user_id")}, nil)
	case "identity.mfa_changed":
		what := mfaChanges[p.str("change")]
		if what == "" {
			what = "Your two-step verification settings changed"
		}
		return one(Intent{TemplateKey: "MFA_CHANGED", Category: CatSecurity, Priority: "HIGH", Variables: map[string]string{"mfa_change": what},
			DeepLink: "/settings/security"}, []string{p.str("user_id")}, nil)
	case "identity.email_change_requested":
		return one(Intent{TemplateKey: "EMAIL_CHANGE_REQUESTED", Category: CatSecurity, Priority: "HIGH"}, []string{p.str("user_id")}, nil)
	case "identity.registration_existing_account":
		return one(Intent{TemplateKey: "REGISTRATION_EXISTING_ACCOUNT", Category: CatSecurity}, []string{p.str("user_id")}, nil)
	case "identity.account_suspended":
		return one(Intent{TemplateKey: "ACCOUNT_SUSPENDED", Category: CatSecurity, Priority: "HIGH"}, []string{p.str("user_id")}, nil)
	case "auth.staff_link_confirmed":
		out := []Intent{{TemplateKey: "STAFF_LINK_CONFIRMED_STAFF", Category: CatSecurity, Recipients: []string{firstValid(p.str("staff_user_id"), p.str("user_id"))}}}
		if ids.Valid(p.str("personal_user_id")) {
			out = append(out, Intent{TemplateKey: "STAFF_LINK_CONFIRMED_PERSONAL", Category: CatSecurity, Recipients: []string{p.str("personal_user_id")}})
		}
		return out, nil
	case "payouts.destination_created", "payouts.destination_changed":
		r, err := s.subjectRecipients(ctx, p.str("owner_type"), p.str("owner_id"))
		return one(Intent{TemplateKey: "PAYOUT_DESTINATION_CHANGED", Category: CatSecurity, Priority: "HIGH",
			DeepLink: "/dashboard/verification/payout-destinations"}, r, err)
	case "payments.payment_status_changed":
		// FAILED and UNKNOWN only; a success notice comes from payments.payment_succeeded alone (ADR-047 §8)
		key := map[string]string{"FAILED": "PAYMENT_FAILED", "UNKNOWN": "PAYMENT_PENDING_CONFIRMATION"}[p.str("to")]
		if key == "" || !ids.Valid(p.str("donor_user_id")) {
			return nil, nil
		}
		return one(Intent{TemplateKey: key, Category: CatPayment, Synthetic: p.synthetic(), Variables: map[string]string{"amount": amount(p)},
			RelatedType: "PAYMENT", RelatedID: p.str("payment_id")}, []string{p.str("donor_user_id")}, nil)
	case "payments.payment_succeeded":
		if !ids.Valid(p.str("donor_user_id")) {
			return nil, nil
		}
		return one(Intent{TemplateKey: "DONATION_CONFIRMED", Category: CatPayment, Synthetic: p.synthetic(),
			Variables:   map[string]string{"amount": amount(p), "donation_reference": ref(p.str("donation_intent_id"))},
			RelatedType: "PAYMENT", RelatedID: p.str("payment_id")}, []string{p.str("donor_user_id")}, nil)
	case "payouts.requested":
		id := p.str("payout_request_id")
		return one(Intent{TemplateKey: "WITHDRAWAL_REQUESTED", Category: CatWithdrawal, Synthetic: p.synthetic(),
			Variables:   map[string]string{"amount": amount(p), "withdrawal_reference": ref(id)},
			RelatedType: "PAYOUT_REQUEST", RelatedID: id, DeepLink: "/dashboard/withdrawals/" + id}, []string{p.str("requested_by")}, nil)
	case "payouts.status_changed":
		t, ok := payoutStatusTemplates[p.str("to")]
		if !ok {
			return nil, nil
		}
		id := p.str("payout_request_id")
		return one(Intent{TemplateKey: t.key, Category: t.cat, Synthetic: p.synthetic(), Variables: map[string]string{"withdrawal_reference": ref(id)},
			RelatedType: "PAYOUT_REQUEST", RelatedID: id, DeepLink: "/dashboard/withdrawals/" + id}, []string{p.str("requested_by")}, nil)
	case "payouts.exception_opened":
		r, err := s.staff(ctx, "payout.view")
		return one(Intent{TemplateKey: "PAYOUT_EXCEPTION_OPENED", Category: CatAdmin, Staff: true, Synthetic: true,
			Variables: map[string]string{"exception_type": p.str("exception_type"), "severity": p.str("severity")},
			DeepLink:  "/admin/finance/payouts/exceptions"}, r, err)
	case EvReconExceptionOpened:
		if sev := p.str("severity"); sev != "HIGH" && sev != "CRITICAL" {
			return nil, nil // lower severities wait in the queue; no notification noise
		}
		r, err := s.staff(ctx, "reconciliation.investigate")
		return one(Intent{TemplateKey: "RECON_EXCEPTION_OPENED", Category: CatAdmin, Staff: true, Synthetic: true,
			Variables: map[string]string{"exception_type": p.str("exception_type"), "severity": p.str("severity")},
			DeepLink:  "/admin/reconciliation/exceptions/" + p.str("exception_id")}, r, err)
	case EvReconExceptionAssigned:
		r, err := s.staff(ctx, "reconciliation.investigate")
		var to []string
		for _, id := range r {
			if id == p.str("assignee_id") && id != p.str("actor_id") {
				to = append(to, id) // the assignee only, while still an investigator, and never for a self-assignment
			}
		}
		return one(Intent{TemplateKey: "RECON_EXCEPTION_ASSIGNED", Category: CatAdmin, Staff: true, Synthetic: true,
			Variables: map[string]string{"exception_type": p.str("exception_type"), "severity": p.str("severity")},
			DeepLink:  "/admin/reconciliation/exceptions/" + p.str("exception_id")}, to, err)
	case EvReconImportFailed:
		r, err := s.staff(ctx, "reconciliation.import")
		return one(Intent{TemplateKey: "RECON_IMPORT_FAILED", Category: CatAdmin, Staff: true, Synthetic: true,
			Variables: map[string]string{"failure_code": p.str("failure_code")}, DeepLink: "/admin/reconciliation/batches/" + p.str("import_id")}, r, err)
	case EvReconResolutionRequested:
		r, err := s.staff(ctx, "reconciliation.resolve", p.str("actor_id"))
		return one(Intent{TemplateKey: "RECON_RESOLUTION_AWAITING_APPROVAL", Category: CatAdmin, Staff: true, Synthetic: true,
			Variables: map[string]string{"resolution_type": p.str("resolution_type")},
			DeepLink:  "/admin/reconciliation/exceptions/" + p.str("exception_id")}, r, err)
	case EvReconUnknownOutcome:
		r, err := s.staff(ctx, "reconciliation.resolve")
		return one(Intent{TemplateKey: "RECON_UNKNOWN_OUTCOME_ESCALATED", Category: CatAdmin, Staff: true, Synthetic: true, Priority: "HIGH",
			Variables: map[string]string{"exception_type": p.str("exception_type")},
			DeepLink:  "/admin/reconciliation/exceptions/" + p.str("exception_id")}, r, err)
	case "payments.refund_requested":
		r, err := s.staff(ctx, "refund.approve")
		return one(Intent{TemplateKey: "REFUND_AWAITING_APPROVAL", Category: CatAdmin, Staff: true, Synthetic: true, DeepLink: "/admin/payments/refunds"}, r, err)
	case "fees.adjustment_requested":
		r, err := s.staff(ctx, "fee.adjust.approve", p.str("actor_id"))
		return one(Intent{TemplateKey: "FEE_ADJUSTMENT_AWAITING_APPROVAL", Category: CatAdmin, Staff: true, Synthetic: true,
			DeepLink: "/admin/finance/fees/adjustments"}, r, err)
	case "risk.rule_version_changed":
		if p.str("change") != "VERSION_SUBMITTED" {
			return nil, nil
		}
		r, err := s.staff(ctx, "risk.rule.approve")
		return one(Intent{TemplateKey: "RISK_RULE_AWAITING_APPROVAL", Category: CatAdmin, Staff: true, DeepLink: "/admin/risk/rules/" + p.str("rule_id")}, r, err)
	case "risk.case_opened":
		if prio := p.str("priority"); prio != "P1" && prio != "P2" {
			return nil, nil
		}
		r, err := s.staff(ctx, "risk.case.work")
		return one(Intent{TemplateKey: "RISK_CASE_OPENED_HIGH", Category: CatRisk, Staff: true, Priority: "HIGH",
			Variables: map[string]string{"case_reference": ref(p.str("case_id")), "priority": p.str("priority")},
			DeepLink:  "/admin/risk/cases/" + p.str("case_id")}, r, err)
	case "risk.case_escalated":
		r, err := s.staff(ctx, "compliance.case.view")
		return one(Intent{TemplateKey: "RISK_CASE_ESCALATED", Category: CatRisk, Staff: true, Priority: "HIGH",
			Variables: map[string]string{"case_reference": firstNonEmpty(p.str("case_number"), ref(p.str("case_id")))},
			DeepLink:  "/admin/risk/cases/" + p.str("case_id")}, r, err)
	case "compliance.case_opened":
		if p.str("case_type") == "RESTRICTED_STR" { // never surfaced outside compliance (LR-008)
			return nil, nil
		}
		r, err := s.staff(ctx, "compliance.case.view")
		return one(Intent{TemplateKey: "COMPLIANCE_CASE_OPENED", Category: CatCompliance, Staff: true,
			Variables: map[string]string{"case_type": p.str("case_type"), "severity": p.str("severity")},
			DeepLink:  "/admin/compliance/cases/" + p.str("case_id")}, r, err)
	case EvSupportCaseOpened:
		if !ids.Valid(p.str("requester_user_id")) {
			return nil, nil
		}
		return one(Intent{TemplateKey: "SUPPORT_CASE_OPENED", Category: CatSupport, Variables: map[string]string{"case_reference": "#" + p.str("case_number")}},
			[]string{p.str("requester_user_id")}, nil)
	case EvSupportCaseAssigned:
		return one(Intent{TemplateKey: "SUPPORT_CASE_ASSIGNED", Category: CatSupport, Staff: true,
			Variables: map[string]string{"case_reference": "#" + p.str("case_number")}, DeepLink: "/admin/support/" + p.str("case_id")},
			[]string{p.str("assigned_to")}, nil)
	case EvSupportCaseStatusChanged:
		key := map[string]string{"WAITING_FOR_CUSTOMER": "SUPPORT_CASE_INFO_REQUESTED", "RESOLVED": "SUPPORT_CASE_RESOLVED", "REOPENED": "SUPPORT_CASE_REOPENED"}[p.str("to")]
		if key == "" || !ids.Valid(p.str("requester_user_id")) {
			return nil, nil
		}
		return one(Intent{TemplateKey: key, Category: CatSupport, Variables: map[string]string{"case_reference": "#" + p.str("case_number")}},
			[]string{p.str("requester_user_id")}, nil)
	case EvIncidentDeclared:
		r, err := s.staff(ctx, "ops.incident.manage")
		return one(Intent{TemplateKey: "INCIDENT_DECLARED", Category: CatAdmin, Staff: true, Priority: "HIGH",
			Variables: map[string]string{"incident_reference": "INC-" + p.str("incident_number"), "severity": p.str("severity"), "module": p.str("affected_module")},
			DeepLink:  "/admin/operations/incidents/" + p.str("incident_id")}, r, err)
	}
	return nil, nil
}

func firstValid(xs ...string) string {
	for _, x := range xs {
		if ids.Valid(x) {
			return x
		}
	}
	return ""
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
