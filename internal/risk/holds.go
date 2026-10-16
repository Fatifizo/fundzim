package risk

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// The central hold registry (ADR-045 §7; brief §35–§37). A hold stops one kind of action on one subject until people
// release it; it never moves money, edits a ledger record or restricts an account legally (restrictions stay in
// compliance). Payout-protective holds (withdrawal, destination, beneficiary) take effect at once; disruptive holds
// (campaign, donation, account review) are requested by one person and activated by another. Every release is
// maker-checker with a fresh step-up, never by anyone linked to the requester or to the held user. Automated holds
// (engine) are temporary (≤ 7 days) and payout-protective only.

// Hold types.
const (
	HoldCampaign      = "CAMPAIGN_HOLD"
	HoldDonation      = "DONATION_HOLD"
	HoldWithdrawal    = "WITHDRAWAL_HOLD"
	HoldDestination   = "PAYOUT_DESTINATION_HOLD"
	HoldAccountReview = "ACCOUNT_REVIEW_HOLD"
	HoldBeneficiary   = "BENEFICIARY_HOLD"
)

// Hold statuses.
const (
	HoldRequested      = "REQUESTED"
	HoldActive         = "ACTIVE"
	HoldUnderReview    = "UNDER_REVIEW"
	HoldReleasePending = "RELEASE_PENDING"
	HoldReleased       = "RELEASED"
	HoldExpired        = "EXPIRED"
	HoldRevoked        = "REVOKED"
)

// Hold events (outbox).
const (
	EvHoldActivated = "risk.hold_activated"
	EvHoldReleased  = "risk.hold_released"
	EvHoldExpired   = "risk.hold_expired"
)

// HoldEdges mirrors the 'risk_hold' machine (keep identical; TestHoldEdgesMatchDatabase).
var HoldEdges = map[string][]string{
	"":                 {HoldRequested, HoldActive},
	HoldRequested:      {HoldActive, HoldRevoked},
	HoldActive:         {HoldUnderReview, HoldReleasePending, HoldExpired},
	HoldUnderReview:    {HoldReleasePending, HoldExpired},
	HoldReleasePending: {HoldReleased, HoldActive},
}

// holdSubjects: which subjects a hold type may apply to (ck_risk_holds_subject).
var holdSubjects = map[string]map[string]bool{
	HoldCampaign:      {"CAMPAIGN": true},
	HoldDonation:      {"CAMPAIGN": true, "USER": true},
	HoldWithdrawal:    {"CAMPAIGN": true, "USER": true, "ORGANISATION": true},
	HoldDestination:   {"PAYOUT_DESTINATION": true},
	HoldAccountReview: {"USER": true, "ORGANISATION": true},
	HoldBeneficiary:   {"BENEFICIARY": true},
}

// needsActivation: disruptive holds are requested by one person and activated by another.
var needsActivation = map[string]bool{HoldCampaign: true, HoldDonation: true, HoldAccountReview: true}

// blockingStatuses are the statuses in which a hold is enforced.
var blockingStatuses = []string{HoldActive, HoldUnderReview, HoldReleasePending}

// Hold is a risk hold (staff view).
type Hold struct {
	ID                 string     `json:"id"`
	HoldType           string     `json:"hold_type"`
	SubjectType        string     `json:"subject_type"`
	SubjectID          string     `json:"subject_id"`
	Status             string     `json:"status"`
	ReasonCode         string     `json:"reason_code"`
	Description        string     `json:"description"`
	Automated          bool       `json:"automated"`
	EvaluationID       *string    `json:"evaluation_id"`
	CaseID             *string    `json:"case_id"`
	CreatedByType      string     `json:"created_by_type"`
	CreatedBy          string     `json:"created_by"`
	ActivatedAt        *time.Time `json:"activated_at"`
	ActivatedBy        *string    `json:"activated_by"`
	ExpiresAt          *time.Time `json:"expires_at"`
	ReleaseRequestedBy *string    `json:"release_requested_by"`
	ReleaseRequestedAt *time.Time `json:"release_requested_at"`
	ReleaseReason      *string    `json:"release_reason"`
	ReleasedBy         *string    `json:"released_by"`
	ReleasedAt         *time.Time `json:"released_at"`
	RevokedBy          *string    `json:"revoked_by"`
	RevokedAt          *time.Time `json:"revoked_at"`
	RevocationReason   *string    `json:"revocation_reason"`
	Version            int        `json:"version"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

const holdCols = `id, hold_type, subject_type, subject_id, status, reason_code, description, automated, evaluation_id, case_id, created_by_type,
	created_by, activated_at, activated_by, expires_at, release_requested_by, release_requested_at, release_reason, released_by, released_at,
	revoked_by, revoked_at, revocation_reason, version, created_at, updated_at`

func scanHold(r pgx.Row) (Hold, error) {
	var h Hold
	err := r.Scan(&h.ID, &h.HoldType, &h.SubjectType, &h.SubjectID, &h.Status, &h.ReasonCode, &h.Description, &h.Automated, &h.EvaluationID,
		&h.CaseID, &h.CreatedByType, &h.CreatedBy, &h.ActivatedAt, &h.ActivatedBy, &h.ExpiresAt, &h.ReleaseRequestedBy, &h.ReleaseRequestedAt,
		&h.ReleaseReason, &h.ReleasedBy, &h.ReleasedAt, &h.RevokedBy, &h.RevokedAt, &h.RevocationReason, &h.Version, &h.CreatedAt, &h.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Hold{}, ErrNotFound
	}
	return h, err
}

func (s *Service) holdsWhere(ctx context.Context, where string, args ...any) ([]Hold, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+holdCols+` FROM risk.risk_holds WHERE `+where+` ORDER BY created_at DESC, id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Hold{}
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

type holdSpec struct {
	HoldType, SubjectType, SubjectID, ReasonCode, Description string
	Automated                                                 bool
	EvaluationID, CaseID                                      string
	CreatedByType, CreatedBy                                  string
	Duration                                                  time.Duration // 0: no expiry (staff holds)
}

// createHold places a hold (ACTIVE at once, or REQUESTED for a disruptive staff hold). An open hold of the same type
// on the subject is reused (made=false).
func (s *Service) createHold(ctx context.Context, tx pgx.Tx, h holdSpec, now time.Time) (string, bool, error) {
	if !holdSubjects[h.HoldType][h.SubjectType] || !ids.Valid(h.SubjectID) {
		return "", false, validation("subject_type", "INVALID_FOR_HOLD_TYPE")
	}
	var existing string
	err := tx.QueryRow(ctx, `SELECT id FROM risk.risk_holds WHERE hold_type = $1 AND subject_type = $2 AND subject_id = $3
		AND status IN ('REQUESTED','ACTIVE','UNDER_REVIEW','RELEASE_PENDING') FOR UPDATE`, h.HoldType, h.SubjectType, h.SubjectID).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	status := HoldActive
	var activated, expires any = now, nil
	if !h.Automated && needsActivation[h.HoldType] {
		status, activated = HoldRequested, nil
	}
	if h.Duration > 0 {
		expires = now.Add(h.Duration)
	}
	id := ids.New()
	tag, err := tx.Exec(ctx, `INSERT INTO risk.risk_holds (id, hold_type, subject_type, subject_id, status, reason_code, description, automated,
		evaluation_id, case_id, created_by_type, created_by, activated_at, expires_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, nullif($9, '')::uuid,
		nullif($10, '')::uuid, $11, $12, $13, $14) ON CONFLICT (hold_type, subject_type, subject_id)
		WHERE status IN ('REQUESTED','ACTIVE','UNDER_REVIEW','RELEASE_PENDING') DO NOTHING`,
		id, h.HoldType, h.SubjectType, h.SubjectID, status, h.ReasonCode, h.Description, h.Automated, h.EvaluationID, h.CaseID, h.CreatedByType,
		h.CreatedBy, activated, expires)
	if err != nil {
		return "", false, mapDB(err)
	}
	if tag.RowsAffected() == 0 {
		return "", false, ErrHoldExists // placed concurrently by another transaction
	}
	if err := riskEvent(ctx, tx, "risk_hold", id, "HOLD_"+status, h.CreatedByType, h.CreatedBy, map[string]any{"hold_type": h.HoldType,
		"reason_code": h.ReasonCode, "automated": h.Automated, "expires_at": expires, "case_id": h.CaseID}, now); err != nil {
		return "", false, err
	}
	if status == HoldActive {
		if err := holdOutbox(ctx, tx, EvHoldActivated, id, h.HoldType, h.SubjectType, h.SubjectID, now); err != nil {
			return "", false, err
		}
	}
	actorType, actorID := "system", ""
	if h.CreatedByType == "STAFF" {
		actorType, actorID = "staff", h.CreatedBy
	}
	return id, true, audit.Record(ctx, tx, audit.Event{Action: "risk.hold." + strings.ToLower(status), ActorType: actorType, ActorID: actorID, TargetType: "risk_hold",
		TargetID: id, Metadata: map[string]any{"hold_type": h.HoldType, "subject_type": h.SubjectType, "subject_id": h.SubjectID,
			"reason_code": h.ReasonCode, "automated": h.Automated}})
}

func holdOutbox(ctx context.Context, tx pgx.Tx, evType, id, holdType, subjectType, subjectID string, now time.Time) error {
	_, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "risk_hold", AggregateID: id, EventType: evType,
		Payload: map[string]any{"hold_id": id, "hold_type": holdType, "subject_type": subjectType, "subject_id": subjectID}, OccurredAt: now})
	return err
}

// NewHold is a staff hold.
type NewHold struct {
	HoldType    string `json:"hold_type"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
	ReasonCode  string `json:"reason_code"`
	Description string `json:"description"`
	CaseID      string `json:"case_id"`
}

// CreateHold places a staff hold (risk.hold.create; step-up by route policy). Disruptive types are REQUESTED.
func (s *Service) CreateHold(ctx context.Context, staffID string, in NewHold) (Hold, error) {
	if holdSubjects[in.HoldType] == nil {
		return Hold{}, validation("hold_type", "INVALID_VALUE")
	}
	if !holdSubjects[in.HoldType][in.SubjectType] || !ids.Valid(in.SubjectID) {
		return Hold{}, validation("subject_id", "INVALID_VALUE")
	}
	in.ReasonCode = strings.ToUpper(strings.TrimSpace(in.ReasonCode))
	if !codeRE.MatchString(in.ReasonCode) || strings.Contains(in.ReasonCode, "FRAUD") {
		return Hold{}, validation("reason_code", "INVALID_FORMAT")
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(in.Description)); n < 5 || n > 2000 {
		return Hold{}, validation("description", "INVALID_LENGTH")
	}
	if in.CaseID != "" && !ids.Valid(in.CaseID) {
		return Hold{}, validation("case_id", "INVALID_FORMAT")
	}
	if self, err := s.linked(ctx, staffID, subjectUser(in.SubjectType, in.SubjectID)); err != nil {
		return Hold{}, err
	} else if self {
		return Hold{}, ErrConflictOfInt
	}
	now := s.now()
	var id string
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var made bool
		var err error
		id, made, err = s.createHold(ctx, tx, holdSpec{HoldType: in.HoldType, SubjectType: in.SubjectType, SubjectID: in.SubjectID,
			ReasonCode: in.ReasonCode, Description: strings.TrimSpace(in.Description), CaseID: in.CaseID, CreatedByType: "STAFF", CreatedBy: staffID}, now)
		if err != nil {
			return err
		}
		if !made {
			return ErrHoldExists
		}
		if in.CaseID != "" {
			return s.addEvidence(ctx, tx, in.CaseID, "HOLD_REF", "risk_hold", id, map[string]any{"hold_type": in.HoldType}, "staff", "STAFF", staffID, now)
		}
		return nil
	})
	if err != nil {
		return Hold{}, err
	}
	return s.Hold(ctx, id)
}

// Hold returns a hold.
func (s *Service) Hold(ctx context.Context, id string) (Hold, error) {
	if !ids.Valid(id) {
		return Hold{}, ErrNotFound
	}
	return scanHold(s.Pool.QueryRow(ctx, `SELECT `+holdCols+` FROM risk.risk_holds WHERE id = $1`, id))
}

// HoldFilter filters the hold list.
type HoldFilter struct {
	Status      string // a status, "OPEN" (REQUESTED…RELEASE_PENDING) or empty
	HoldType    string
	SubjectType string
	SubjectID   string
}

// Holds lists holds.
func (s *Service) Holds(ctx context.Context, f HoldFilter) ([]Hold, error) {
	where, args := []string{"true"}, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	switch f.Status {
	case "":
	case "OPEN":
		where = append(where, "status IN ('REQUESTED','ACTIVE','UNDER_REVIEW','RELEASE_PENDING')")
	default:
		add("status = ?", f.Status)
	}
	if f.HoldType != "" {
		add("hold_type = ?", f.HoldType)
	}
	if f.SubjectType != "" && ids.Valid(f.SubjectID) {
		add("subject_type = ?", f.SubjectType)
		add("subject_id = ?", f.SubjectID)
	}
	return s.holdsWhere(ctx, strings.Join(where, " AND "), args...)
}

// HoldOp is a staff action on a hold.
type HoldOp struct {
	StaffID         string
	StepUpAt        time.Time
	HoldID          string
	ExpectedVersion int
	Reason          string
}

func (s *Service) holdOp(ctx context.Context, op HoldOp, fn func(ctx context.Context, tx pgx.Tx, h Hold, now time.Time) error) (Hold, error) {
	if !ids.Valid(op.HoldID) {
		return Hold{}, ErrNotFound
	}
	now := s.now()
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		h, err := scanHold(tx.QueryRow(ctx, `SELECT `+holdCols+` FROM risk.risk_holds WHERE id = $1 FOR UPDATE`, op.HoldID))
		if err != nil {
			return err
		}
		if op.ExpectedVersion > 0 && h.Version != op.ExpectedVersion {
			return ErrStale
		}
		if self, err := s.linked(ctx, op.StaffID, subjectUser(h.SubjectType, h.SubjectID)); err != nil {
			return err
		} else if self {
			return ErrConflictOfInt
		}
		return fn(ctx, tx, h, now)
	})
	if err != nil {
		return Hold{}, err
	}
	return s.Hold(ctx, op.HoldID)
}

// ActivateHold is the second person's approval of a REQUESTED disruptive hold (risk.hold.release; step-up).
func (s *Service) ActivateHold(ctx context.Context, op HoldOp) (Hold, error) {
	if op.StepUpAt.IsZero() {
		return Hold{}, ErrStepUp
	}
	return s.holdOp(ctx, op, func(ctx context.Context, tx pgx.Tx, h Hold, now time.Time) error {
		if h.Status != HoldRequested {
			return ErrHoldState
		}
		if self, err := s.linked(ctx, op.StaffID, h.CreatedBy); err != nil {
			return err
		} else if self {
			return ErrSelfApproval
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'ACTIVE', activated_at = $2, activated_by = $3, version = version + 1
			WHERE id = $1`, h.ID, now, op.StaffID); err != nil {
			return mapDB(err)
		}
		if err := riskEvent(ctx, tx, "risk_hold", h.ID, "HOLD_ACTIVATED", "STAFF", op.StaffID, map[string]any{"reason": op.Reason}, now); err != nil {
			return err
		}
		if err := holdOutbox(ctx, tx, EvHoldActivated, h.ID, h.HoldType, h.SubjectType, h.SubjectID, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.hold.activated", TargetType: "risk_hold", TargetID: h.ID})
	})
}

// RevokeHold withdraws a REQUESTED hold before it takes effect.
func (s *Service) RevokeHold(ctx context.Context, op HoldOp) (Hold, error) {
	r, err := reasonText(op.Reason)
	if err != nil {
		return Hold{}, err
	}
	return s.holdOp(ctx, op, func(ctx context.Context, tx pgx.Tx, h Hold, now time.Time) error {
		if h.Status != HoldRequested {
			return ErrHoldState
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'REVOKED', revoked_by = $2, revoked_at = $3, revocation_reason = $4,
			version = version + 1 WHERE id = $1`, h.ID, op.StaffID, now, r); err != nil {
			return mapDB(err)
		}
		if err := riskEvent(ctx, tx, "risk_hold", h.ID, "HOLD_REVOKED", "STAFF", op.StaffID, map[string]any{"reason": r}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.hold.revoked", TargetType: "risk_hold", TargetID: h.ID, Reason: r})
	})
}

// ReviewHold marks an ACTIVE hold UNDER_REVIEW (someone is working it; still enforced).
func (s *Service) ReviewHold(ctx context.Context, op HoldOp) (Hold, error) {
	return s.holdOp(ctx, op, func(ctx context.Context, tx pgx.Tx, h Hold, now time.Time) error {
		if h.Status != HoldActive {
			return ErrHoldState
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'UNDER_REVIEW', version = version + 1 WHERE id = $1`, h.ID); err != nil {
			return mapDB(err)
		}
		return riskEvent(ctx, tx, "risk_hold", h.ID, "HOLD_UNDER_REVIEW", "STAFF", op.StaffID, map[string]any{}, now)
	})
}

// RequestRelease is the maker step of a release (risk.hold.create). The hold stays enforced until a checker approves.
func (s *Service) RequestRelease(ctx context.Context, op HoldOp) (Hold, error) {
	r, err := reasonText(op.Reason)
	if err != nil {
		return Hold{}, err
	}
	return s.holdOp(ctx, op, func(ctx context.Context, tx pgx.Tx, h Hold, now time.Time) error {
		if h.Status != HoldActive && h.Status != HoldUnderReview {
			return ErrHoldState
		}
		if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'RELEASE_PENDING', release_requested_by = $2, release_requested_at = $3,
			release_reason = $4, version = version + 1 WHERE id = $1`, h.ID, op.StaffID, now, r); err != nil {
			return mapDB(err)
		}
		if err := riskEvent(ctx, tx, "risk_hold", h.ID, "RELEASE_REQUESTED", "STAFF", op.StaffID, map[string]any{"reason": r}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "risk.hold.release_requested", TargetType: "risk_hold", TargetID: h.ID, Reason: r})
	})
}

// DecideRelease is the checker step (risk.hold.release; fresh step-up; never the requester or anyone linked to them or
// to the held user). Rejection returns the hold to ACTIVE.
func (s *Service) DecideRelease(ctx context.Context, op HoldOp, approve bool) (Hold, error) {
	if op.StepUpAt.IsZero() {
		return Hold{}, ErrStepUp
	}
	r, err := reasonText(op.Reason)
	if err != nil {
		return Hold{}, err
	}
	return s.holdOp(ctx, op, func(ctx context.Context, tx pgx.Tx, h Hold, now time.Time) error {
		if h.Status != HoldReleasePending || h.ReleaseRequestedBy == nil {
			return ErrHoldState
		}
		if self, err := s.linked(ctx, op.StaffID, *h.ReleaseRequestedBy); err != nil {
			return err
		} else if self {
			return ErrSelfApproval
		}
		if !approve {
			if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'ACTIVE', release_requested_by = NULL, release_requested_at = NULL,
				release_reason = NULL, version = version + 1 WHERE id = $1`, h.ID); err != nil {
				return mapDB(err)
			}
			if err := riskEvent(ctx, tx, "risk_hold", h.ID, "RELEASE_REJECTED", "STAFF", op.StaffID, map[string]any{"reason": r}, now); err != nil {
				return err
			}
			return audit.Record(ctx, tx, audit.Event{Action: "risk.hold.release_rejected", TargetType: "risk_hold", TargetID: h.ID, Reason: r})
		}
		return s.release(ctx, tx, h, op.StaffID, op.StepUpAt, r, now)
	})
}

func (s *Service) release(ctx context.Context, tx pgx.Tx, h Hold, by string, stepUp time.Time, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'RELEASED', released_by = $2, released_at = $3, release_step_up_at = $4,
		version = version + 1 WHERE id = $1`, h.ID, by, now, minTime(stepUp.UTC(), now)); err != nil {
		return mapDB(err)
	}
	if err := riskEvent(ctx, tx, "risk_hold", h.ID, "HOLD_RELEASED", "STAFF", by, map[string]any{"reason": reason}, now); err != nil {
		return err
	}
	if err := holdOutbox(ctx, tx, EvHoldReleased, h.ID, h.HoldType, h.SubjectType, h.SubjectID, now); err != nil {
		return err
	}
	return audit.Record(ctx, tx, audit.Event{Action: "risk.hold.released", TargetType: "risk_hold", TargetID: h.ID, Reason: reason})
}

// applyOutcomeToHolds applies an approved case outcome to the case's open holds (see DecideCase).
func (s *Service) applyOutcomeToHolds(ctx context.Context, tx pgx.Tx, c Case, outcome, proposer, decider string, stepUp time.Time, reason string,
	now time.Time) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, `SELECT `+holdCols+` FROM risk.risk_holds WHERE case_id = $1 AND status IN ('REQUESTED','ACTIVE','UNDER_REVIEW','RELEASE_PENDING')
		ORDER BY created_at, id FOR UPDATE`, c.ID)
	if err != nil {
		return nil, err
	}
	var holds []Hold
	for rows.Next() {
		h, err := scanHold(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		holds = append(holds, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	affected := []map[string]any{}
	for _, h := range holds {
		switch outcome {
		case OutcomeNoFurtherAction, OutcomeFalsePositive:
			switch h.Status {
			case HoldRequested:
				if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'REVOKED', revoked_by = $2, revoked_at = $3, revocation_reason = $4,
					version = version + 1 WHERE id = $1`, h.ID, decider, now, "case resolved: "+outcome); err != nil {
					return nil, mapDB(err)
				}
				affected = append(affected, map[string]any{"hold_id": h.ID, "result": HoldRevoked})
				continue
			case HoldActive, HoldUnderReview:
				// the case proposer is the release maker, the case decider its checker
				if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'RELEASE_PENDING', release_requested_by = $2, release_requested_at = $3,
					release_reason = $4, version = version + 1 WHERE id = $1`, h.ID, proposer, now, "case resolved: "+outcome); err != nil {
					return nil, mapDB(err)
				}
				h.ReleaseRequestedBy = &proposer
			}
			if h.ReleaseRequestedBy != nil && *h.ReleaseRequestedBy != proposer {
				// a release requested by someone else: the decider may still be its checker only if unlinked
				if self, err := s.linked(ctx, decider, *h.ReleaseRequestedBy); err != nil {
					return nil, err
				} else if self {
					return nil, ErrSelfApproval
				}
			}
			if err := s.release(ctx, tx, h, decider, stepUp, "case resolved: "+outcome+": "+reason, now); err != nil {
				return nil, err
			}
			affected = append(affected, map[string]any{"hold_id": h.ID, "result": HoldReleased})
		case OutcomeHoldMaintained, OutcomeEscalated:
			if !h.Automated || h.Status == HoldReleasePending {
				continue // staff holds already have no expiry; a pending release is decided on its own
			}
			// a temporary automated hold becomes a manual hold without expiry, approved by two people (proposer, decider)
			if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'EXPIRED', expires_at = greatest($2, activated_at + interval '1 second'),
				version = version + 1 WHERE id = $1`, h.ID, now); err != nil {
				return nil, mapDB(err)
			}
			if err := riskEvent(ctx, tx, "risk_hold", h.ID, "HOLD_SUPERSEDED", "STAFF", decider, map[string]any{"case_id": c.ID, "outcome": outcome}, now); err != nil {
				return nil, err
			}
			id, _, err := s.createHold(ctx, tx, holdSpec{HoldType: h.HoldType, SubjectType: h.SubjectType, SubjectID: h.SubjectID,
				ReasonCode: h.ReasonCode, Description: "Maintained by risk case " + strconv.Itoa(int(c.CaseNumber)) + " (" + outcome + ")", CaseID: c.ID,
				CreatedByType: "STAFF", CreatedBy: decider}, now)
			if err != nil {
				return nil, err
			}
			affected = append(affected, map[string]any{"hold_id": h.ID, "result": "SUPERSEDED", "replacement_hold_id": id})
		}
	}
	return affected, nil
}

// ExpireHolds ends temporary holds whose time has passed (job). Expiry is recorded and monitored; the subject's open
// case (if any) still needs a human decision.
func (s *Service) ExpireHolds(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := s.now()
	n := 0
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+holdCols+` FROM risk.risk_holds WHERE status IN ('ACTIVE','UNDER_REVIEW') AND expires_at IS NOT NULL
			AND expires_at <= $1 ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED`, now, limit)
		if err != nil {
			return err
		}
		var hs []Hold
		for rows.Next() {
			h, err := scanHold(rows)
			if err != nil {
				rows.Close()
				return err
			}
			hs = append(hs, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, h := range hs {
			if _, err := tx.Exec(ctx, `UPDATE risk.risk_holds SET status = 'EXPIRED', version = version + 1 WHERE id = $1`, h.ID); err != nil {
				return mapDB(err)
			}
			if err := riskEvent(ctx, tx, "risk_hold", h.ID, "HOLD_EXPIRED", "SYSTEM", "risk.jobs", map[string]any{}, now); err != nil {
				return err
			}
			if err := s.monitor(ctx, tx, "HOLD_EXPIRED", SevMed, h.SubjectType, h.SubjectID, h.ID, map[string]any{"hold_id": h.ID,
				"hold_type": h.HoldType, "case_id": h.CaseID}, now); err != nil {
				return err
			}
			if err := holdOutbox(ctx, tx, EvHoldExpired, h.ID, h.HoldType, h.SubjectType, h.SubjectID, now); err != nil {
				return err
			}
			if err := audit.Record(ctx, tx, audit.Event{Action: "risk.hold.expired", ActorType: "system", TargetType: "risk_hold", TargetID: h.ID}); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// ---- enforcement -----------------------------------------------------------------------------------------------------

// HoldSubject is a subject an enforcement point checks.
type HoldSubject struct {
	Type string
	ID   string
}

// BlockingHolds returns the enforced holds of the given types on the given subjects (ACTIVE, UNDER_REVIEW,
// RELEASE_PENDING). Enforcement points call it and refuse the action when it returns any hold.
func (s *Service) BlockingHolds(ctx context.Context, types []string, subjects ...HoldSubject) ([]Hold, error) {
	var st, sid []string
	for _, x := range subjects {
		if x.Type != "" && ids.Valid(x.ID) {
			st, sid = append(st, x.Type), append(sid, x.ID)
		}
	}
	if len(st) == 0 || len(types) == 0 {
		return []Hold{}, nil
	}
	return s.holdsWhere(ctx, `status = ANY($1) AND hold_type = ANY($2) AND (subject_type, subject_id) IN
		(SELECT t, i::uuid FROM unnest($3::text[], $4::text[]) AS u(t, i))`, blockingStatuses, types, st, sid)
}

// DonationHolds: holds that stop a donation to a campaign (by a signed-in donor, when known).
func (s *Service) DonationHolds(ctx context.Context, campaignID, donorUserID string) ([]Hold, error) {
	return s.BlockingHolds(ctx, []string{HoldCampaign, HoldDonation}, HoldSubject{"CAMPAIGN", campaignID}, HoldSubject{"USER", donorUserID})
}

// PublicationHolds: holds that stop a campaign from being published.
func (s *Service) PublicationHolds(ctx context.Context, campaignID, ownerUserID, ownerOrgID string) ([]Hold, error) {
	return s.BlockingHolds(ctx, []string{HoldCampaign, HoldAccountReview}, HoldSubject{"CAMPAIGN", campaignID}, HoldSubject{"USER", ownerUserID},
		HoldSubject{"ORGANISATION", ownerOrgID})
}

// WithdrawalScope identifies everything a withdrawal touches.
type WithdrawalScope struct {
	CampaignID, OwnerUserID, OwnerOrgID, DestinationID, BeneficiaryID string
}

// WithdrawalHolds: holds that stop a withdrawal (request, approval and submission).
func (s *Service) WithdrawalHolds(ctx context.Context, w WithdrawalScope) ([]Hold, error) {
	return s.BlockingHolds(ctx, []string{HoldCampaign, HoldWithdrawal, HoldDestination, HoldAccountReview, HoldBeneficiary},
		HoldSubject{"CAMPAIGN", w.CampaignID}, HoldSubject{"USER", w.OwnerUserID}, HoldSubject{"ORGANISATION", w.OwnerOrgID},
		HoldSubject{"PAYOUT_DESTINATION", w.DestinationID}, HoldSubject{"BENEFICIARY", w.BeneficiaryID})
}

// HoldTypes lists the distinct types of holds (for coarse owner-facing reasons and eligibility details).
func HoldTypes(hs []Hold) []string {
	seen, out := map[string]bool{}, []string{}
	for _, h := range hs {
		if !seen[h.HoldType] {
			seen[h.HoldType] = true
			out = append(out, h.HoldType)
		}
	}
	return out
}
