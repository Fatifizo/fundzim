package payments

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// RefundRequestTTL is the maker-checker expiry (operational-controls §3: 72 h).
const RefundRequestTTL = 72 * time.Hour

var refundReasons = map[string]bool{"DONOR_REQUEST": true, "DUPLICATE_PAYMENT": true, "PAYMENT_ERROR": true, "CAMPAIGN_CANCELLED": true,
	"CAMPAIGN_FRAUD": true, "COMPLIANCE_ORDER": true, "OTHER": true}

// Refund is a refund request (staff view). Execution is DISABLED in Stage 8 (ADR-039 §4).
type Refund struct {
	ID             string     `json:"id"`
	PaymentID      string     `json:"payment_id"`
	AmountMinor    string     `json:"amount_minor"`
	Currency       string     `json:"currency"`
	ReasonCode     string     `json:"reason_code"`
	ReasonText     string     `json:"reason_text"`
	Status         string     `json:"status"`
	RequestedBy    string     `json:"requested_by"`
	RequestedAt    time.Time  `json:"requested_at"`
	DecidedBy      *string    `json:"decided_by"`
	DecidedAt      *time.Time `json:"decided_at"`
	DecisionReason *string    `json:"decision_reason"`
	ExpiresAt      time.Time  `json:"expires_at"`
	Execution      string     `json:"execution"`
}

const refundSelect = `SELECT id, payment_intent_id, amount_minor, currency, reason_code, reason_text, status, requested_by, requested_at,
	decided_by, decided_at, decision_reason, expires_at FROM app.refund_requests`

func scanRefund(r pgx.Row) (Refund, error) {
	var x Refund
	var amt int64
	err := r.Scan(&x.ID, &x.PaymentID, &amt, &x.Currency, &x.ReasonCode, &x.ReasonText, &x.Status, &x.RequestedBy, &x.RequestedAt,
		&x.DecidedBy, &x.DecidedAt, &x.DecisionReason, &x.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrRefundNotFound
	}
	x.AmountMinor, x.Execution = strconv.FormatInt(amt, 10), "DISABLED"
	return x, err
}

// RefundInput is POST /admin/payments/attempts/{payment_id}/refund-requests.
type RefundInput struct {
	AmountMinor string `json:"amount_minor"`
	ReasonCode  string `json:"reason_code"`
	ReasonText  string `json:"reason_text"`
}

// RequestRefund records a refund request (maker; permission refund.request) for a SUCCEEDED or PARTIALLY_REFUNDED
// payment. The database refuses an amount above the refundable remainder (concurrent requests serialise on the
// payment row). It goes straight to PENDING_APPROVAL. Nothing is sent to a provider, ever, in Stage 8.
func (s *Service) RequestRefund(ctx context.Context, staffID, paymentID, idemKey string, in RefundInput) (Refund, error) {
	var det []errs.Detail
	if !amountRE.MatchString(in.AmountMinor) {
		det = append(det, errs.Detail{Field: "amount_minor", Code: "INVALID_FORMAT"})
	}
	if !refundReasons[in.ReasonCode] {
		det = append(det, errs.Detail{Field: "reason_code", Code: "INVALID_VALUE"})
	}
	text, ok := cleanReason(in.ReasonText)
	if !ok {
		det = append(det, errs.Detail{Field: "reason_text", Code: "INVALID_LENGTH"})
	}
	if len(det) > 0 {
		return Refund{}, httpx.Validation(det...)
	}
	if !ids.Valid(idemKey) {
		return Refund{}, errs.New(errs.Invalid, "IDEMPOTENCY_KEY_REQUIRED", "An Idempotency-Key header is required for this request.")
	}
	amount, _ := strconv.ParseInt(in.AmountMinor, 10, 64)
	if r, err := scanRefund(s.Pool.QueryRow(ctx, refundSelect+` WHERE requested_by = $1 AND idempotency_key = $2`, staffID, idemKey)); err == nil {
		if r.PaymentID != paymentID || r.AmountMinor != in.AmountMinor {
			return Refund{}, errs.New(errs.Conflict, "IDEMPOTENCY_KEY_REUSED", "This Idempotency-Key was already used for a different request.")
		}
		return r, nil
	}
	var out Refund
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		p, err := lockPay(ctx, tx, paymentID)
		if err != nil {
			return err
		}
		if p.Status != psp.StatusSucceeded && p.Status != psp.StatusPartiallyRefunded {
			return ErrRefundNotAllowed
		}
		var committed int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(amount_minor), 0) FROM app.refund_requests WHERE payment_intent_id = $1
			AND status IN ('APPROVED','EXECUTING','COMPLETED')`, p.ID).Scan(&committed); err != nil {
			return err
		}
		if committed+amount > p.Amount {
			return ErrRefundExceeds
		}
		now := s.now()
		id := ids.New()
		if _, err := tx.Exec(ctx, `INSERT INTO app.refund_requests (id, payment_intent_id, campaign_id, currency, amount_minor, reason_code,
			reason_text, channel, requested_by, requested_at, status, idempotency_key, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'REQUESTED', $11, $12)`, id, p.ID, p.CampaignID, p.Currency, amount, in.ReasonCode,
			text, channelFor(in.ReasonCode), staffID, now, idemKey, now.Add(RefundRequestTTL)); err != nil {
			return mapRefundDBError(err)
		}
		staff := Obs{Source: SrcStaff, ActorType: "STAFF", ActorID: staffID, Reason: text, Correlation: p.Correlation}
		if err := s.insertEvent(ctx, tx, eventRow{subject: "REFUND_REQUEST", donationID: p.DonationIntentID, paymentID: p.ID, refundID: id,
			to: "REQUESTED", applied: true, obs: staff}); err != nil {
			return err
		}
		if err := s.insertEvent(ctx, tx, eventRow{subject: "REFUND_REQUEST", donationID: p.DonationIntentID, paymentID: p.ID, refundID: id,
			from: "REQUESTED", to: "PENDING_APPROVAL", applied: true, obs: staff}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE app.refund_requests SET status = 'PENDING_APPROVAL', version = version + 1 WHERE id = $1`, id); err != nil {
			return err
		}
		if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "refund_request", AggregateID: id, EventType: "payments.refund_requested",
			Payload: map[string]any{"refund_request_id": id, "payment_id": p.ID, "simulated": p.Simulated}, OccurredAt: now}); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "payments.refund.requested", ActorType: "staff", ActorID: staffID,
			TargetType: "refund_request", TargetID: id, Metadata: map[string]any{"payment_id": p.ID, "reason_code": in.ReasonCode}}); err != nil {
			return err
		}
		out, err = scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1`, id))
		return err
	})
	if errors.Is(err, errConcurrentReplay) {
		return scanRefund(s.Pool.QueryRow(ctx, refundSelect+` WHERE requested_by = $1 AND idempotency_key = $2`, staffID, idemKey))
	}
	return out, err
}

func channelFor(reason string) string {
	if reason == "COMPLIANCE_ORDER" || reason == "CAMPAIGN_FRAUD" {
		return "COMPLIANCE"
	}
	return "SUPPORT"
}

// mapRefundDBError turns the database guards' refusals into API errors (the guards are the last line).
func mapRefundDBError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch {
		case pg.Code == "23505" && strings.Contains(pg.ConstraintName, "idempotency"):
			return errConcurrentReplay
		case pg.Code == "23514" && strings.Contains(pg.Message, "over-refund"):
			return ErrRefundExceeds
		case pg.Code == "23514" && strings.Contains(pg.Message, "refund not allowed"):
			return ErrRefundNotAllowed
		case pg.Code == "23514" && strings.Contains(pg.ConstraintName, "maker_checker"):
			return ErrRefundSelfApproval
		}
	}
	return err
}

// Refund decisions.
const (
	DecideApprove  = "approve"
	DecideReject   = "reject"
	DecideHold     = "hold"
	DecideResume   = "resume"
	DecideWithdraw = "withdraw"
)

// DecideRefund applies a staff decision. approve/reject/hold/resume need refund.approve with step-up (route
// policy) and a checker distinct from the maker (Go and DB); withdraw is the requester's own. Approval records
// the decision only: execution is DISABLED (no APPROVED → EXECUTING edge exists in Stage 8).
func (s *Service) DecideRefund(ctx context.Context, staffID, refundID, decision, reason string, stepUpAt time.Time) (Refund, error) {
	reason, ok := cleanReason(reason)
	if !ok {
		return Refund{}, invalidReason("reason")
	}
	if !ids.Valid(refundID) {
		return Refund{}, ErrRefundNotFound
	}
	var out Refund
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var pid string
		if err := tx.QueryRow(ctx, `SELECT payment_intent_id FROM app.refund_requests WHERE id = $1`, refundID).Scan(&pid); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrRefundNotFound
			}
			return err
		}
		p, err := lockPay(ctx, tx, pid) // lock order: payment, then request (as the DB guard does)
		if err != nil {
			return err
		}
		r, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1 FOR UPDATE`, refundID))
		if err != nil {
			return err
		}
		var to string
		switch {
		case decision == DecideApprove && r.Status == "PENDING_APPROVAL":
			to = "APPROVED"
		case decision == DecideReject && (r.Status == "PENDING_APPROVAL" || r.Status == "ON_HOLD"):
			to = "REJECTED"
		case decision == DecideHold && r.Status == "PENDING_APPROVAL":
			to = "ON_HOLD"
		case decision == DecideResume && r.Status == "ON_HOLD":
			to = "PENDING_APPROVAL"
		case decision == DecideWithdraw && (r.Status == "REQUESTED" || r.Status == "PENDING_APPROVAL"):
			to = "WITHDRAWN"
		default:
			return ErrRefundState
		}
		if decision == DecideWithdraw {
			if r.RequestedBy != staffID {
				return ErrRefundState
			}
		} else if r.RequestedBy == staffID {
			return ErrRefundSelfApproval
		}
		if to == "APPROVED" && !r.ExpiresAt.After(s.now()) {
			return ErrRefundState
		}
		now := s.now()
		if err := s.insertEvent(ctx, tx, eventRow{subject: "REFUND_REQUEST", donationID: p.DonationIntentID, paymentID: p.ID, refundID: r.ID,
			from: r.Status, to: to, applied: true, obs: Obs{Source: SrcStaff, ActorType: "STAFF", ActorID: staffID, Reason: reason,
				Correlation: p.Correlation}}); err != nil {
			return err
		}
		var q string
		args := []any{r.ID, to}
		switch to {
		case "APPROVED", "REJECTED":
			var su *time.Time
			if to == "APPROVED" {
				su = &stepUpAt
			}
			q = `UPDATE app.refund_requests SET status = $2, decided_by = $3, decided_at = $4, decision_reason = $5, decided_step_up_at = $6,
				version = version + 1 WHERE id = $1`
			args = append(args, staffID, now, reason, su)
		default:
			q = `UPDATE app.refund_requests SET status = $2, version = version + 1 WHERE id = $1`
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return mapRefundDBError(err)
		}
		if to == "APPROVED" || to == "REJECTED" {
			if _, err := outbox.Write(ctx, tx, outbox.Event{AggregateType: "refund_request", AggregateID: r.ID, EventType: "payments.refund_decided",
				Payload: map[string]any{"refund_request_id": r.ID, "payment_id": p.ID, "decision": to, "execution": "DISABLED"}, OccurredAt: now}); err != nil {
				return err
			}
		}
		if err := audit.Record(ctx, tx, audit.Event{Action: "payments.refund." + decision, ActorType: "staff", ActorID: staffID,
			TargetType: "refund_request", TargetID: r.ID, Metadata: map[string]any{"payment_id": p.ID, "from": r.Status, "to": to}}); err != nil {
			return err
		}
		out, err = scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1`, r.ID))
		return err
	})
	return out, mapRefundDBError(err)
}

// ListRefunds lists refund requests (oldest open first when filtering by an open status, else newest first).
func (s *Service) ListRefunds(ctx context.Context, status, cursor string, limit int) ([]Refund, *string, error) {
	if status != "" && !map[string]bool{"REQUESTED": true, "PENDING_APPROVAL": true, "ON_HOLD": true, "APPROVED": true, "REJECTED": true,
		"WITHDRAWN": true}[status] {
		return nil, nil, httpx.Validation(errs.Detail{Field: "status", Code: "INVALID_VALUE"})
	}
	before, beforeID, err := psp.ParseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	limit = psp.PageLimit(limit)
	rows, err := s.Pool.Query(ctx, refundSelect+` WHERE ($1 = '' OR status = $1) AND ($2::timestamptz IS NULL OR (requested_at, id) < ($2, $3::uuid))
		ORDER BY requested_at DESC, id DESC LIMIT $4`, status, before, beforeID, limit)
	if err != nil {
		return nil, nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Refund, error) { return scanRefund(r) })
	if err != nil {
		return nil, nil, err
	}
	var next *string
	if len(list) > 0 {
		next = psp.NextCursor(len(list), limit, list[len(list)-1].RequestedAt, list[len(list)-1].ID)
	}
	return list, next, nil
}
