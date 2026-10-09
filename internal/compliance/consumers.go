package compliance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Event types that open or link compliance cases (interface contract §6).
const (
	EvKYCCaseEscalated          = "kyc.case_escalated"
	EvBeneficiaryEscalated      = "beneficiaries.verification_escalated"
	EvRiskEscalationRecommended = "risk.escalation_recommended"
	EvCampaignReviewEscalated   = "campaigns.review_escalated"
	// ConsumerOpenCase is the consumer name (dedupe key in compliance.compliance_case_triggers). Never rename.
	ConsumerOpenCase = "compliance.open_case"
)

// ErrInvalidPayload marks an event that cannot open a case (missing or malformed ids).
var ErrInvalidPayload = errors.New("compliance: invalid event payload")

// Trigger is a case-opening request derived from one event.
type Trigger struct {
	CaseType   string
	Severity   string
	Source     string
	ReasonCode string
	Links      []LinkInput
	// Anchors are the links used to find an open case to link to instead of opening a new one.
	Anchors []LinkInput
}

type escalationPayload struct {
	CaseID        string `json:"case_id"`
	Kind          string `json:"kind"`
	SubjectType   string `json:"subject_type"`
	SubjectID     string `json:"subject_id"`
	ReasonCode    string `json:"reason_code"`
	BeneficiaryID string `json:"beneficiary_id"`
	OwnerType     string `json:"owner_type"`
	OwnerID       string `json:"owner_id"`
	AssessmentID  string `json:"assessment_id"`
	DecisionID    string `json:"decision_id"`
	Rating        string `json:"rating"`
	// campaigns.review_escalated
	CampaignID          string `json:"campaign_id"`
	OwnerUserID         string `json:"owner_user_id"`
	OwnerOrganisationID string `json:"owner_organisation_id"`
	ReviewID            string `json:"review_id"`
}

func partyType(t string) (string, bool) {
	t = strings.ToUpper(strings.TrimSpace(t))
	return t, t == "USER" || t == "ORGANISATION"
}

// TriggerFromEvent validates an escalation event and derives the case to open or link. Payloads carry IDs
// and codes only (contract §6); anything else is refused with ErrInvalidPayload.
func TriggerFromEvent(eventType string, raw json.RawMessage) (Trigger, error) {
	var p escalationPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Trigger{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	bad := func(what string) (Trigger, error) {
		return Trigger{}, fmt.Errorf("%w: %s: %s", ErrInvalidPayload, eventType, what)
	}
	reasonOr := func(def string) string {
		if ValidReasonCode(p.ReasonCode) {
			return p.ReasonCode
		}
		return def
	}
	switch eventType {
	case EvKYCCaseEscalated:
		st, ok := partyType(p.SubjectType)
		if !ok || !ids.Valid(p.SubjectID) || !ids.Valid(p.CaseID) {
			return bad("case_id, subject_type (USER|ORGANISATION) and subject_id are required")
		}
		caseType, obj := "KYC_REVIEW", "KYC_CASE"
		switch strings.ToUpper(p.Kind) {
		case "KYC":
		case "KYB":
			caseType, obj = "KYB_REVIEW", "KYB_CASE"
		default:
			return bad("kind must be KYC or KYB")
		}
		subject := LinkInput{SubjectType: st, SubjectID: p.SubjectID, Role: "PRIMARY_SUBJECT"}
		object := LinkInput{SubjectType: obj, SubjectID: p.CaseID, Role: "RELATED_OBJECT"}
		return Trigger{CaseType: caseType, Severity: "S2", Source: "KYC_ESCALATION", ReasonCode: reasonOr(strings.ToUpper(p.Kind) + "_ESCALATED"),
			Links: []LinkInput{subject, object}, Anchors: []LinkInput{object, subject}}, nil
	case EvBeneficiaryEscalated:
		ot, ok := partyType(p.OwnerType)
		if !ok || !ids.Valid(p.OwnerID) || !ids.Valid(p.BeneficiaryID) {
			return bad("beneficiary_id, owner_type (USER|ORGANISATION) and owner_id are required")
		}
		ben := LinkInput{SubjectType: "BENEFICIARY", SubjectID: p.BeneficiaryID, Role: "PRIMARY_SUBJECT"}
		own := LinkInput{SubjectType: ot, SubjectID: p.OwnerID, Role: "RELATED_SUBJECT"}
		return Trigger{CaseType: "BENEFICIARY_REVIEW", Severity: "S2", Source: "BENEFICIARY_ESCALATION",
			ReasonCode: reasonOr("BENEFICIARY_ESCALATED"), Links: []LinkInput{ben, own}, Anchors: []LinkInput{ben}}, nil
	case EvRiskEscalationRecommended:
		st := strings.ToUpper(strings.TrimSpace(p.SubjectType))
		if !ids.Valid(p.SubjectID) || !ids.Valid(p.AssessmentID) || !ids.Valid(p.DecisionID) {
			return bad("subject_id, assessment_id and decision_id are required")
		}
		var subject LinkInput
		switch {
		case PartyTypes[st]:
			subject = LinkInput{SubjectType: st, SubjectID: p.SubjectID, Role: "PRIMARY_SUBJECT"}
		case st == "KYC_CASE" || st == "KYB_CASE":
			subject = LinkInput{SubjectType: st, SubjectID: p.SubjectID, Role: "RELATED_OBJECT"}
		default:
			return bad("unsupported subject_type")
		}
		rating := strings.ToUpper(p.Rating)
		if rating != "RESTRICTED" && rating != "ENHANCED" {
			return bad("rating must be ENHANCED or RESTRICTED")
		}
		sev := "S2"
		if rating == "ENHANCED" {
			sev = "S3"
		}
		return Trigger{CaseType: "RISK_REVIEW", Severity: sev, Source: "RISK_ENGINE", ReasonCode: "RISK_RATING_" + rating,
			Links: []LinkInput{subject, {SubjectType: "RISK_DECISION", SubjectID: p.DecisionID, Role: "RELATED_OBJECT"},
				{SubjectType: "RISK_ASSESSMENT", SubjectID: p.AssessmentID, Role: "RELATED_OBJECT"}},
			Anchors: []LinkInput{subject}}, nil
	case EvCampaignReviewEscalated:
		// the campaign is the primary subject (a restriction applies to it); the owner is a related subject
		if !ids.Valid(p.CampaignID) || !ids.Valid(p.ReviewID) {
			return bad("campaign_id and review_id are required")
		}
		var owner LinkInput
		switch {
		case ids.Valid(p.OwnerUserID) && p.OwnerOrganisationID == "":
			owner = LinkInput{SubjectType: "USER", SubjectID: p.OwnerUserID, Role: "RELATED_SUBJECT"}
		case ids.Valid(p.OwnerOrganisationID) && p.OwnerUserID == "":
			owner = LinkInput{SubjectType: "ORGANISATION", SubjectID: p.OwnerOrganisationID, Role: "RELATED_SUBJECT"}
		default:
			return bad("exactly one of owner_user_id and owner_organisation_id is required")
		}
		campaign := LinkInput{SubjectType: "CAMPAIGN", SubjectID: p.CampaignID, Role: "PRIMARY_SUBJECT"}
		review := LinkInput{SubjectType: "CAMPAIGN_REVIEW", SubjectID: p.ReviewID, Role: "RELATED_OBJECT"}
		return Trigger{CaseType: "CAMPAIGN_REVIEW", Severity: "S2", Source: "CAMPAIGN_ESCALATION", ReasonCode: reasonOr("CAMPAIGN_REVIEW_ESCALATED"),
			Links: []LinkInput{campaign, owner, review}, Anchors: []LinkInput{campaign}}, nil
	}
	return bad("unsupported event type")
}

// Consumer is one outbox subscription for the composition root.
type Consumer struct {
	Name, EventType string
	Handle          outbox.Handler
}

// Consumers returns the compliance subscriptions (worker): each escalation opens a case or links to an open
// case of the same subject, once per event.
func (s *Service) Consumers() []Consumer {
	var out []Consumer
	for _, ev := range []string{EvKYCCaseEscalated, EvBeneficiaryEscalated, EvRiskEscalationRecommended, EvCampaignReviewEscalated} {
		out = append(out, Consumer{Name: ConsumerOpenCase, EventType: ev, Handle: s.HandleEscalation})
	}
	return out
}

// HandleEscalation is the outbox handler. A payload that cannot open a case is logged and acknowledged.
func (s *Service) HandleEscalation(ctx context.Context, d outbox.Delivery) error {
	res, err := s.OpenFromEvent(ctx, d)
	if errors.Is(err, ErrInvalidPayload) {
		s.logger.Error("compliance case not opened: unusable event payload", slog.String("event_type", d.EventType),
			slog.String("event_id", d.EventID), slog.String("error", err.Error()))
		return nil
	}
	if err != nil {
		return err
	}
	if !res.Duplicate {
		s.logger.Info("compliance case trigger processed", slog.String("event_type", d.EventType), slog.String("case_id", res.CaseID),
			slog.String("outcome", res.Outcome))
	}
	return nil
}

// TriggerResult reports what an escalation event did.
type TriggerResult struct {
	CaseID    string
	Outcome   string // OPENED | LINKED
	Duplicate bool
}

// OpenFromEvent opens a case for the event, or links the event to an open (not RESOLVED/CLOSED) case that
// already has one of its anchors as a current link. Exactly once per (consumer, event id): the dedupe row
// in compliance.compliance_case_triggers commits with the case change.
func (s *Service) OpenFromEvent(ctx context.Context, d outbox.Delivery) (TriggerResult, error) {
	if !ids.Valid(d.EventID) {
		return TriggerResult{}, fmt.Errorf("%w: event id", ErrInvalidPayload)
	}
	tr, err := TriggerFromEvent(d.EventType, d.Payload)
	if err != nil {
		return TriggerResult{}, err
	}
	actor := Actor{Type: "SYSTEM", Job: ConsumerOpenCase}
	var res TriggerResult
	err = s.tx(ctx, false, func(ctx context.Context, tx pgx.Tx) error {
		res = TriggerResult{}
		// serialise triggers about the same subjects (sorted keys: no lock-order deadlock)
		keys := make([]string, 0, len(tr.Anchors)+1)
		keys = append(keys, "compliance-event:"+d.EventID)
		for _, a := range tr.Anchors {
			keys = append(keys, "compliance:"+a.SubjectType+":"+a.SubjectID)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, k); err != nil {
				return err
			}
		}
		var seen bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM compliance.compliance_case_triggers WHERE consumer = $1 AND source_event_id = $2)`,
			ConsumerOpenCase, d.EventID).Scan(&seen); err != nil {
			return err
		}
		if seen {
			return errDuplicateTrigger
		}
		var caseID string
		for _, a := range tr.Anchors {
			err := tx.QueryRow(ctx, `SELECT c.id FROM compliance.compliance_cases c
				JOIN compliance.compliance_case_links l ON l.case_id = c.id
				WHERE l.subject_type = $1 AND l.subject_id = $2 AND l.link_action = 'LINKED'
				  AND NOT EXISTS (SELECT 1 FROM compliance.compliance_case_links u WHERE u.unlinks_id = l.id)
				  AND c.status NOT IN ('RESOLVED', 'CLOSED')
				ORDER BY c.opened_at, c.id LIMIT 1 FOR UPDATE OF c`, a.SubjectType, a.SubjectID).Scan(&caseID)
			if err == nil {
				break
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		outcome := "LINKED"
		if caseID == "" {
			caseID, outcome = ids.New(), "OPENED"
			if err := s.openTx(ctx, tx, caseID, actor, OpenInput{CaseType: tr.CaseType, Severity: tr.Severity, Source: tr.Source,
				OpeningReasonCode: tr.ReasonCode, Links: tr.Links}); err != nil {
				return err
			}
		}
		// Claim the event after the case row exists: with ON CONFLICT, PostgreSQL also checks the new row
		// against the SELECT policy (case visibility). A conflict means another delivery already handled it:
		// roll everything back.
		tag, err := tx.Exec(ctx, `INSERT INTO compliance.compliance_case_triggers (id, consumer, source_event_id, event_type, case_id,
			outcome, received_at) VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT (consumer, source_event_id) DO NOTHING`,
			ids.New(), ConsumerOpenCase, d.EventID, d.EventType, caseID, outcome, s.now())
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errDuplicateTrigger
		}
		res.CaseID, res.Outcome = caseID, outcome
		if outcome == "OPENED" {
			return nil
		}
		existing, err := s.currentLinks(ctx, tx, caseID)
		if err != nil {
			return err
		}
		have := map[string]bool{}
		for _, l := range existing {
			have[l.SubjectType+":"+l.SubjectID] = true
		}
		added := 0
		for _, l := range tr.Links {
			if have[l.SubjectType+":"+l.SubjectID] {
				continue
			}
			if l.Role == "PRIMARY_SUBJECT" {
				l.Role = "RELATED_SUBJECT" // the case keeps its own primary subject
			}
			if err := s.addLink(ctx, tx, caseID, l, actor); err != nil {
				return err
			}
			if err := s.event(ctx, tx, caseID, nil, "SUBJECT_LINKED", nil, nil, actor, "",
				map[string]any{"subject_type": l.SubjectType, "subject_id": l.SubjectID, "role": l.Role}, ""); err != nil {
				return err
			}
			added++
		}
		auditID, err := s.audit(ctx, tx, actor, "compliance.case.trigger_linked", caseID,
			map[string]any{"event_type": d.EventType, "source_event_id": d.EventID, "links_added": added})
		if err != nil {
			return err
		}
		return s.event(ctx, tx, caseID, nil, "SOURCE_EVENT_LINKED", nil, nil, actor, tr.ReasonCode,
			map[string]any{"event_type": d.EventType, "source_event_id": d.EventID}, auditID)
	})
	if errors.Is(err, errDuplicateTrigger) {
		return TriggerResult{Duplicate: true}, nil
	}
	return res, err
}

var errDuplicateTrigger = errors.New("compliance: event already processed")
