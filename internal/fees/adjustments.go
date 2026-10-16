package fees

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Fee adjustments (ADR-044 §12). Corrections are new immutable rows; an assessment is never edited. REFUND and
// CHARGEBACK adjustments are computed from the policy version's refund treatment (SYSTEM, APPLIED). Every other reason
// is maker-checker (fee.adjust.request → fee.adjust.approve, different people, fresh step-up) and never posts:
// correcting the books is a Stage 10 ledger adjustment, referenced by ledger_adjustment_id.

// Adjustment reasons and directions.
const (
	AdjRefund      = "REFUND"
	AdjChargeback  = "CHARGEBACK"
	AdjProvider    = "PROVIDER_CORRECTION"
	AdjPolicy      = "POLICY_CORRECTION"
	AdjAccounting  = "ACCOUNTING_ADJUSTMENT"
	DirDecrease    = "DECREASE"
	DirIncrease    = "INCREASE"
	AdjApplied     = "APPLIED"
	AdjPending     = "PENDING_APPROVAL"
	AdjApproved    = "APPROVED"
	AdjRejected    = "REJECTED"
	AcctNotPosted  = "NOT_POSTED"
	AcctAdjPosted  = "POSTED"
	AcctAdjNotAppl = "NOT_APPLICABLE"
)

// Adjustment is a fee adjustment.
type Adjustment struct {
	ID                 string          `json:"id"`
	AssessmentID       string          `json:"assessment_id"`
	Currency           string          `json:"currency"`
	Reason             string          `json:"reason"`
	Direction          string          `json:"direction"`
	AmountMinor        string          `json:"amount_minor"`
	SourceType         string          `json:"source_type"`
	SourceID           string          `json:"source_id"`
	BasisMinor         *string         `json:"basis_minor"`
	Calculation        json.RawMessage `json:"calculation"`
	Note               string          `json:"note"`
	Status             string          `json:"status"`
	ActorType          string          `json:"actor_type"`
	RequestedBy        *string         `json:"requested_by"`
	DecidedBy          *string         `json:"decided_by"`
	DecidedAt          *time.Time      `json:"decided_at"`
	DecisionReason     *string         `json:"decision_reason"`
	AccountingStatus   string          `json:"accounting_status"`
	LedgerAdjustmentID *string         `json:"ledger_adjustment_id"`
	FeeEnvironment     string          `json:"fee_environment"`
	CreatedAt          time.Time       `json:"created_at"`
	amount             int64
	basis              *int64
}

const adjustmentCols = `id, assessment_id, currency, reason, direction, amount_minor, source_type, source_id, basis_minor, calculation, note, status,
	actor_type, requested_by, decided_by, decided_at, decision_reason, accounting_status, ledger_adjustment_id::text, fee_environment, created_at`

func scanAdjustment(r pgx.Row) (Adjustment, error) {
	var a Adjustment
	err := r.Scan(&a.ID, &a.AssessmentID, &a.Currency, &a.Reason, &a.Direction, &a.amount, &a.SourceType, &a.SourceID, &a.basis, &a.Calculation,
		&a.Note, &a.Status, &a.ActorType, &a.RequestedBy, &a.DecidedBy, &a.DecidedAt, &a.DecisionReason, &a.AccountingStatus, &a.LedgerAdjustmentID,
		&a.FeeEnvironment, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	a.AmountMinor, a.BasisMinor = amt(a.amount), amtP(a.basis)
	return a, err
}

// systemAdjustment records a computed REFUND or CHARGEBACK adjustment (idempotent per assessment, reason and source).
func (s *Service) systemAdjustment(ctx context.Context, tx pgx.Tx, assessmentID, currency, reasonCode string, amount int64, srcType, srcID string,
	basis int64, calc map[string]any, note, accounting string) (string, bool, error) {
	b, err := json.Marshal(calc)
	if err != nil {
		return "", false, err
	}
	id := ids.New()
	var got string
	err = tx.QueryRow(ctx, `INSERT INTO app.fee_adjustments (id, assessment_id, currency, reason, direction, amount_minor, source_type, source_id,
		basis_minor, calculation, note, status, actor_type, accounting_status)
		VALUES ($1, $2, $3, $4, 'DECREASE', $5, $6, $7, $8, $9, $10, 'APPLIED', 'SYSTEM', $11)
		ON CONFLICT ON CONSTRAINT uq_fee_adjustments_source DO NOTHING RETURNING id`,
		id, assessmentID, currency, reasonCode, amount, srcType, srcID, basis, b, note, accounting).Scan(&got)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id FROM app.fee_adjustments WHERE assessment_id = $1 AND reason = $2 AND source_type = $3 AND source_id = $4`,
			assessmentID, reasonCode, srcType, srcID).Scan(&got)
		return got, false, err
	}
	if err != nil {
		return "", false, mapDB(err)
	}
	if err := event(ctx, tx, "fee_adjustment", got, "ADJUSTMENT_APPLIED", systemActor, map[string]any{"assessment_id": assessmentID,
		"reason": reasonCode, "amount_minor": amt(amount)}, s.now()); err != nil {
		return "", false, err
	}
	return got, true, nil
}

// ManualAdjustment is a staff correction request.
type ManualAdjustment struct {
	Reason             string  `json:"reason"`
	Direction          string  `json:"direction"`
	AmountMinor        string  `json:"amount_minor"`
	Note               string  `json:"note"`
	LedgerAdjustmentID *string `json:"ledger_adjustment_id"`
}

// RequestAdjustment records a manual correction PENDING_APPROVAL (maker: fee.adjust.request).
func (s *Service) RequestAdjustment(ctx context.Context, staffID, assessmentID string, in ManualAdjustment) (Adjustment, error) {
	switch in.Reason {
	case AdjProvider, AdjPolicy, AdjAccounting:
	case AdjRefund, AdjChargeback:
		return Adjustment{}, validation("reason", "SYSTEM_ONLY") // computed from the policy, never typed in
	default:
		return Adjustment{}, validation("reason", "INVALID_VALUE")
	}
	if in.Direction != DirDecrease && in.Direction != DirIncrease {
		return Adjustment{}, validation("direction", "INVALID_VALUE")
	}
	amount, err := parseAmount(in.AmountMinor)
	if err != nil {
		return Adjustment{}, err
	}
	note := strings.TrimSpace(in.Note)
	if n := utf8.RuneCountInString(note); n < 5 || n > 2000 {
		return Adjustment{}, validation("note", "INVALID_LENGTH")
	}
	if in.LedgerAdjustmentID != nil && !ids.Valid(*in.LedgerAdjustmentID) {
		return Adjustment{}, validation("ledger_adjustment_id", "INVALID_FORMAT")
	}
	a, err := s.Assessment(ctx, assessmentID)
	if err != nil {
		return Adjustment{}, err
	}
	if a.fee == nil {
		return Adjustment{}, ErrUnknownAmount
	}
	if in.Direction == DirDecrease && a.net != nil && amount > *a.net {
		return Adjustment{}, ErrAdjustmentBounds
	}
	id := ids.New()
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO app.fee_adjustments (id, assessment_id, currency, reason, direction, amount_minor, source_type, source_id,
			note, status, actor_type, requested_by, accounting_status, ledger_adjustment_id)
			VALUES ($1, $2, $3, $4, $5, $6, 'staff_request', $1, $7, 'PENDING_APPROVAL', 'STAFF', $8, 'NOT_APPLICABLE', $9)`,
			id, assessmentID, a.Currency, in.Reason, in.Direction, amount, note, staffID, in.LedgerAdjustmentID); err != nil {
			return mapDB(err)
		}
		if err := event(ctx, tx, "fee_adjustment", id, "ADJUSTMENT_REQUESTED", staff(staffID), map[string]any{"assessment_id": assessmentID,
			"reason": in.Reason, "direction": in.Direction, "amount_minor": amt(amount)}, now); err != nil {
			return err
		}
		// risk signal source (Stage 13, KI-S12-17): who asked for which adjustment — ids, codes and amounts only
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "fee_adjustment", AggregateID: id, EventType: EvAdjustmentRequested,
			Payload: map[string]any{"adjustment_id": id, "assessment_id": assessmentID, "actor_id": staffID, "reason_code": in.Reason,
				"direction": in.Direction, "amount_minor": amt(amount), "currency": a.Currency}, CorrelationID: correlation(ctx), OccurredAt: now}); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.adjustment.requested", TargetType: "fee_adjustment", TargetID: id,
			Metadata: map[string]any{"reason": in.Reason, "direction": in.Direction}})
	})
	if err != nil {
		return Adjustment{}, err
	}
	return s.Adjustment(ctx, id)
}

// DecideAdjustment approves or rejects a manual adjustment (checker: fee.adjust.approve; never the requester or
// anyone linked; fresh step-up). Approval re-checks that the assessment does not go below zero (also a trigger).
func (s *Service) DecideAdjustment(ctx context.Context, staffID string, stepUpAt time.Time, id string, approve bool, why string) (Adjustment, error) {
	r, err := reason(why)
	if err != nil {
		return Adjustment{}, err
	}
	if stepUpAt.IsZero() {
		return Adjustment{}, ErrStepUp
	}
	if !ids.Valid(id) {
		return Adjustment{}, ErrNotFound
	}
	now := s.now()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		a, err := scanAdjustment(tx.QueryRow(ctx, `SELECT `+adjustmentCols+` FROM app.fee_adjustments WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if a.Status != AdjPending {
			return ErrAdjustmentState
		}
		if self, err := s.linked(ctx, staffID, deref(a.RequestedBy)); err != nil {
			return err
		} else if self {
			return ErrSelfApproval
		}
		status := map[bool]string{true: AdjApproved, false: AdjRejected}[approve]
		if _, err := tx.Exec(ctx, `UPDATE app.fee_adjustments SET status = $2, decided_by = $3, decided_at = $4, decided_step_up_at = $5,
			decision_reason = $6 WHERE id = $1`, id, status, staffID, now, minTime(stepUpAt.UTC(), now), r); err != nil {
			return mapDB(err)
		}
		if err := event(ctx, tx, "fee_adjustment", id, "ADJUSTMENT_"+status, staff(staffID), map[string]any{"reason": r}, now); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "fee.adjustment." + strings.ToLower(status), TargetType: "fee_adjustment", TargetID: id, Reason: r})
	})
	if err != nil {
		return Adjustment{}, err
	}
	return s.Adjustment(ctx, id)
}

// Adjustment returns one adjustment.
func (s *Service) Adjustment(ctx context.Context, id string) (Adjustment, error) {
	if !ids.Valid(id) {
		return Adjustment{}, ErrNotFound
	}
	return scanAdjustment(s.Pool.QueryRow(ctx, `SELECT `+adjustmentCols+` FROM app.fee_adjustments WHERE id = $1`, id))
}

// AdjustmentFilter filters adjustment lists.
type AdjustmentFilter struct {
	AssessmentID, Status, Reason, CampaignID string
	Limit                                    int
}

// Adjustments lists adjustments (newest first).
func (s *Service) Adjustments(ctx context.Context, f AdjustmentFilter) ([]Adjustment, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+adjustmentCols+` FROM app.fee_adjustments j
		WHERE ($1 = '' OR assessment_id::text = $1) AND ($2 = '' OR status = $2) AND ($3 = '' OR reason = $3)
		  AND ($4 = '' OR EXISTS (SELECT 1 FROM app.fee_assessments a WHERE a.id = j.assessment_id AND a.campaign_id::text = $4))
		ORDER BY created_at DESC, id DESC LIMIT $5`, f.AssessmentID, f.Status, f.Reason, f.CampaignID, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Adjustment{}
	for rows.Next() {
		a, err := scanAdjustment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
