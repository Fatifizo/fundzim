package risk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Event types the risk module consumes (interface contract §6, events marked "risk").
const (
	EvStorageObjectScanned      = "storage.object_scanned"
	EvKYCCaseRejected           = "kyc.case_rejected"
	EvKYCDuplicateIdentity      = "kyc.duplicate_identity_detected"
	EvBeneficiaryRejected       = "beneficiaries.verification_rejected"
	EvBeneficiaryEscalated      = "beneficiaries.verification_escalated"
	EvPayoutDestinationChanged  = "payouts.destination_changed"
	EvPayoutDestinationRejected = "payouts.destination_rejected"
	maxFactString               = 64
	defaultReasonCode           = "UNSPECIFIED"
	ownerUser, ownerOrg         = "USER", "ORGANISATION"
	subjectKYCCase              = "KYC_CASE"
)

// ConsumedEvents lists every event type the risk consumer subscribes to.
var ConsumedEvents = []string{EvStorageObjectScanned, EvKYCCaseRejected, EvKYCDuplicateIdentity, EvBeneficiaryRejected,
	EvBeneficiaryEscalated, EvPayoutDestinationChanged, EvPayoutDestinationRejected}

// ErrInvalidPayload marks an event whose payload cannot yield a signal (missing or malformed ids/codes).
var ErrInvalidPayload = errors.New("risk: invalid event payload")

var (
	codeRE       = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)
	signalTypeRE = codeRE
	factCodeRE   = regexp.MustCompile(`^[A-Z0-9_]*$`)
)

func validateSignal(s Signal) error {
	if !signalTypeRE.MatchString(s.Type) || !ids.Valid(s.SubjectID) || !ids.Valid(s.SourceEventID) ||
		s.SubjectType == "" || s.SourceModule == "" || s.SourceEventType == "" || s.ObservedAt.IsZero() {
		return fmt.Errorf("%w: signal %q for %s/%s", ErrInvalidPayload, s.Type, s.SubjectType, s.SubjectID)
	}
	return nil
}

// payload is the union of the fields the consumed events carry (IDs and codes only).
type payload struct {
	ObjectID          string `json:"object_id"`
	Status            string `json:"status"`
	OwnerModule       string `json:"owner_module"`
	Purpose           string `json:"purpose"`
	UploadedBy        string `json:"uploaded_by"`
	CaseID            string `json:"case_id"`
	Kind              string `json:"kind"`
	SubjectType       string `json:"subject_type"`
	SubjectID         string `json:"subject_id"`
	ReasonCode        string `json:"reason_code"`
	ProfileID         string `json:"profile_id"`
	OtherProfileCount *int   `json:"other_profile_count"`
	BeneficiaryID     string `json:"beneficiary_id"`
	DestinationID     string `json:"destination_id"`
	OwnerType         string `json:"owner_type"`
	OwnerID           string `json:"owner_id"`
}

func owner(t, id string) (string, string, bool) {
	t = strings.ToUpper(strings.TrimSpace(t))
	if (t != ownerUser && t != ownerOrg) || !ids.Valid(id) {
		return "", "", false
	}
	return t, id, true
}

func reason(code string) string {
	if codeRE.MatchString(code) {
		return code
	}
	return defaultReasonCode
}

func shortCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if len(s) > maxFactString || !factCodeRE.MatchString(s) {
		return ""
	}
	return s
}

// SignalFromEvent maps one delivered event to at most one risk signal. ok is false when the event carries
// no risk fact (e.g. a CLEAN scan result) or when the payload lacks what the signal needs: the latter returns
// ErrInvalidPayload so the caller can log it (retrying cannot fix a payload).
func SignalFromEvent(d outbox.Delivery) (sig Signal, ok bool, err error) {
	var p payload
	if err := json.Unmarshal(d.Payload, &p); err != nil {
		return Signal{}, false, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	base := Signal{SourceEventID: d.EventID, SourceEventType: d.EventType, ObservedAt: d.OccurredAt.UTC()}
	bad := func(what string) (Signal, bool, error) {
		return Signal{}, false, fmt.Errorf("%w: %s: %s", ErrInvalidPayload, d.EventType, what)
	}
	if d.OccurredAt.IsZero() || !ids.Valid(d.EventID) {
		return bad("event id and occurred_at are required")
	}
	switch d.EventType {
	case EvStorageObjectScanned:
		if strings.ToUpper(p.Status) != "REJECTED" {
			return Signal{}, false, nil
		}
		if !ids.Valid(p.UploadedBy) || !ids.Valid(p.ObjectID) {
			return bad("uploaded_by and object_id are required for a rejected scan")
		}
		base.Type, base.SubjectType, base.SubjectID, base.SourceModule = SigDocumentRejected, ownerUser, p.UploadedBy, "storage"
		base.Facts = map[string]any{"object_id": p.ObjectID, "purpose": shortCode(p.Purpose)}
	case EvKYCCaseRejected:
		st := strings.ToUpper(p.SubjectType)
		if (st != ownerUser && st != ownerOrg) || !ids.Valid(p.SubjectID) || !ids.Valid(p.CaseID) {
			return bad("case_id, subject_type (USER|ORGANISATION) and subject_id are required")
		}
		base.Type = SigKYCRejected
		if strings.ToUpper(p.Kind) == "KYB" {
			base.Type = SigKYBRejected
		}
		base.SubjectType, base.SubjectID, base.SourceModule = st, p.SubjectID, "kyc"
		base.Facts = map[string]any{"case_id": p.CaseID, "reason_code": reason(p.ReasonCode)}
	case EvKYCDuplicateIdentity:
		if !ids.Valid(p.CaseID) {
			return bad("case_id is required")
		}
		base.Type, base.SourceModule = SigDuplicateIdentity, "kyc"
		if st, sid, ok := owner(p.SubjectType, p.SubjectID); ok {
			base.SubjectType, base.SubjectID = st, sid
		} else {
			base.SubjectType, base.SubjectID = subjectKYCCase, p.CaseID
		}
		facts := map[string]any{"case_id": p.CaseID}
		if ids.Valid(p.ProfileID) {
			facts["profile_id"] = p.ProfileID
		}
		if p.OtherProfileCount != nil && *p.OtherProfileCount >= 0 {
			facts["other_profile_count"] = *p.OtherProfileCount
		}
		base.Facts = facts
	case EvBeneficiaryRejected, EvBeneficiaryEscalated:
		st, sid, ok := owner(p.OwnerType, p.OwnerID)
		if !ok || !ids.Valid(p.BeneficiaryID) {
			return bad("beneficiary_id, owner_type (USER|ORGANISATION) and owner_id are required")
		}
		base.Type = SigBeneficiaryRejected
		if d.EventType == EvBeneficiaryEscalated {
			base.Type = SigBeneficiaryEscalated
		}
		base.SubjectType, base.SubjectID, base.SourceModule = st, sid, "beneficiaries"
		base.Facts = map[string]any{"beneficiary_id": p.BeneficiaryID, "reason_code": reason(p.ReasonCode)}
	case EvPayoutDestinationChanged, EvPayoutDestinationRejected:
		st, sid, ok := owner(p.OwnerType, p.OwnerID)
		if !ok || !ids.Valid(p.DestinationID) {
			return bad("destination_id, owner_type (USER|ORGANISATION) and owner_id are required")
		}
		base.Type = SigDestinationChanged
		if d.EventType == EvPayoutDestinationRejected {
			base.Type = SigDestinationRejected
		}
		base.SubjectType, base.SubjectID, base.SourceModule = st, sid, "payouts"
		base.Facts = map[string]any{"destination_id": p.DestinationID}
	default:
		return Signal{}, false, nil
	}
	return base, true, nil
}

// Consumer is one outbox subscription for the composition root.
type Consumer struct {
	Name, EventType string
	Handle          outbox.Handler
}

// Consumers returns the risk subscriptions (worker). Delivery is at least once; the signal's unique
// (source_event_id, signal_type) makes a redelivery a no-op. A payload that cannot yield a signal is logged
// and acknowledged (a retry cannot fix it).
func (s *Service) Consumers() []Consumer {
	out := make([]Consumer, 0, len(ConsumedEvents))
	for _, ev := range ConsumedEvents {
		out = append(out, Consumer{Name: ConsumerName, EventType: ev, Handle: s.HandleEvent})
	}
	return out
}

// HandleEvent is the outbox handler for every consumed event type.
func (s *Service) HandleEvent(ctx context.Context, d outbox.Delivery) error {
	sig, ok, err := SignalFromEvent(d)
	if err != nil {
		s.Logger.Error("risk signal skipped: unusable event payload", slog.String("event_type", d.EventType),
			slog.String("event_id", d.EventID), slog.String("error", err.Error()))
		return nil
	}
	if !ok {
		return nil
	}
	out, err := s.RecordSignal(ctx, sig)
	if err != nil {
		return err
	}
	if !out.Duplicate && out.Decision != "" && out.Decision != DecisionNoAction {
		s.Logger.Info("risk decision recorded", slog.String("subject_type", sig.SubjectType), slog.String("subject_id", sig.SubjectID),
			slog.String("decision", out.Decision), slog.String("rating", out.Rating))
	}
	return nil
}
