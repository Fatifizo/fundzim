package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Adjustments are maker-checker requests (ledger.ledger_adjustments) for journals that are themselves a judgement:
//
//	REVERSAL   — one REVERSAL journal mirroring the target exactly (L7: at most once per original)
//	CORRECTION — the REVERSAL of the target plus a replacement journal under posting_rule with the proposed lines
//	MANUAL     — one journal under posting_rule (ADJUSTMENT, WRITE_OFF, …) with the proposed lines
//
// The maker (ledger.adjustment.create) proposes; a different person (ledger.adjustment.approve, step-up) approves the
// exact lines hash; the system posts the journal(s) in the approval transaction (L14: lines equal the approved ones).
const (
	AdjReversal   = "REVERSAL"
	AdjCorrection = "CORRECTION"
	AdjManual     = "MANUAL"
	AdjustmentTTL = 24 * time.Hour
)

var (
	ErrAdjustmentState    = errs.New(errs.Conflict, "LEDGER_ADJUSTMENT_STATE", "The adjustment is not pending approval.")
	ErrAdjustmentExpired  = errs.New(errs.Conflict, "LEDGER_ADJUSTMENT_EXPIRED", "The adjustment request has expired.")
	ErrAlreadyReversed    = errs.New(errs.Conflict, "LEDGER_ALREADY_REVERSED", "This journal has already been reversed.")
	ErrReversalOfReversal = errs.New(errs.Conflict, "LEDGER_REVERSAL_OF_REVERSAL", "A reversal journal cannot itself be reversed; post a new correction instead.")
)

// ProposedLine is one proposed journal line (account code, direction, amount as a digit string).
type ProposedLine struct {
	AccountCode string `json:"account_code"`
	Direction   string `json:"direction"`
	AmountMinor string `json:"amount_minor"`
}

// AdjustmentRequest is the maker's request.
type AdjustmentRequest struct {
	Kind        string         `json:"kind"`
	PostingRule string         `json:"posting_rule"`
	Currency    string         `json:"currency"`
	Target      string         `json:"target_transaction_id"`
	Lines       []ProposedLine `json:"lines"`
	ReasonCode  string         `json:"reason_code"`
	Reason      string         `json:"reason"`
	LinkedType  string         `json:"linked_subject_type"`
	LinkedID    string         `json:"linked_subject_id"`
}

// Adjustment is an adjustment as staff see it.
type Adjustment struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	PostingRule string          `json:"posting_rule"`
	Currency    string          `json:"currency"`
	Target      *string         `json:"target_transaction_id"`
	Lines       json.RawMessage `json:"proposed_lines"`
	ReasonCode  string          `json:"reason_code"`
	Reason      string          `json:"reason"`
	Status      string          `json:"status"`
	RequestedBy string          `json:"requested_by"`
	RequestedAt time.Time       `json:"requested_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
	ApprovedBy  *string         `json:"approved_by"`
	ApprovedAt  *time.Time      `json:"approved_at"`
	RejectedBy  *string         `json:"rejected_by"`
	ReversalTx  *string         `json:"reversal_transaction_id"`
	ResultingTx *string         `json:"resulting_transaction_id"`
	PostedAt    *time.Time      `json:"posted_at"`
}

var reasonCodes = map[string]bool{"POSTING_ERROR": true, "RECON_DISCREPANCY": true, "DUPLICATE_POSTING": true, "PROVIDER_CORRECTION": true,
	"WRITE_OFF": true, "TEST_FIXTURE_CORRECTION": true, "OTHER": true}

// RequestAdjustment records the maker's request (PENDING_APPROVAL, expires in 24 h).
func (s *Service) RequestAdjustment(ctx context.Context, makerID string, in AdjustmentRequest) (Adjustment, error) {
	reason, err := cleanReason(in.Reason)
	if err != nil {
		return Adjustment{}, err
	}
	if !reasonCodes[in.ReasonCode] {
		return Adjustment{}, invalid("reason_code", "INVALID_VALUE")
	}
	env, err := s.Environment(ctx)
	if err != nil {
		return Adjustment{}, err
	}
	if env == EnvProduction && !ProductionPostingEnabled {
		return Adjustment{}, ErrProductionDisabled
	}
	switch in.Kind {
	case AdjReversal:
		in.PostingRule, in.Lines = "REVERSAL", []ProposedLine{}
	case AdjCorrection, AdjManual:
		if len(in.Lines) < 2 || in.PostingRule == "REVERSAL" {
			return Adjustment{}, invalid("lines", "AT_LEAST_TWO")
		}
		if _, err := toLines(in.Lines); err != nil {
			return Adjustment{}, err
		}
	default:
		return Adjustment{}, invalid("kind", "INVALID_VALUE")
	}
	if (in.Kind == AdjManual) != (in.Target == "") {
		return Adjustment{}, invalid("target_transaction_id", "REQUIRED_FOR_REVERSAL_OR_CORRECTION")
	}
	r, ok, err := s.rule(ctx, in.PostingRule)
	if err != nil {
		return Adjustment{}, err
	}
	if !ok || (in.Kind != AdjReversal && !r.RequiresApproval) {
		return Adjustment{}, invalid("posting_rule", "NOT_AN_ADJUSTMENT_RULE")
	}
	if in.Target != "" {
		// the target must exist in this ledger, in the stated currency, not be a reversal, and not be reversed yet
		var ccy, rule string
		var reversed bool
		err := s.Pool.QueryRow(ctx, `SELECT t.currency, t.posting_rule, EXISTS (SELECT 1 FROM ledger.ledger_transactions r
			WHERE r.reverses_transaction_id = t.id) FROM ledger.ledger_transactions t WHERE t.id = $1`, in.Target).Scan(&ccy, &rule, &reversed)
		if errors.Is(err, pgx.ErrNoRows) || !uuidRE.MatchString(in.Target) {
			return Adjustment{}, ErrNotFound
		}
		if err != nil {
			return Adjustment{}, err
		}
		switch {
		case reversed:
			return Adjustment{}, ErrAlreadyReversed
		case rule == "REVERSAL":
			return Adjustment{}, ErrReversalOfReversal
		}
		in.Currency = ccy
	}
	if !currencyRE.MatchString(in.Currency) {
		return Adjustment{}, ErrCurrency
	}
	lines, _ := json.Marshal(in.Lines)
	id := ids.New()
	var linkedID any
	if uuidRE.MatchString(in.LinkedID) {
		linkedID = in.LinkedID
	}
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_adjustments (id, kind, posting_rule, posting_rule_version, currency,
			target_transaction_id, proposed_lines, proposed_lines_sha256, reason_code, reason, linked_subject_type, linked_subject_id,
			status, requested_by, requested_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, nullif($6, '')::uuid, $7::jsonb, sha256(convert_to($7::jsonb::text, 'UTF8')), $8, $9,
			nullif($10, ''), $11, 'PENDING_APPROVAL', $12, $13, $14)`,
			id, in.Kind, in.PostingRule, r.Version, in.Currency, in.Target, string(lines), in.ReasonCode, reason, in.LinkedType, linkedID,
			makerID, s.now(), s.now().Add(AdjustmentTTL)); err != nil {
			return mapDBError(err)
		}
		_, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.adjustment.requested", ActorType: "staff", ActorID: makerID,
			TargetType: "ledger_adjustment", TargetID: id, Reason: reason, Metadata: map[string]any{"kind": in.Kind, "posting_rule": in.PostingRule,
				"currency": in.Currency, "target": in.Target, "reason_code": in.ReasonCode, "line_count": len(in.Lines)}})
		return err
	})
	if err != nil {
		return Adjustment{}, err
	}
	return s.AdjustmentByID(ctx, id)
}

func toLines(in []ProposedLine) ([]Line, error) {
	out := make([]Line, 0, len(in))
	for _, p := range in {
		ref, ok := ParseAccountCode(p.AccountCode)
		if !ok {
			return nil, invalid("lines", "UNKNOWN_ACCOUNT")
		}
		if p.Direction != Debit && p.Direction != Credit {
			return nil, invalid("lines", "INVALID_DIRECTION")
		}
		if !amountRE.MatchString(p.AmountMinor) {
			return nil, invalid("lines", "INVALID_AMOUNT")
		}
		out = append(out, Line{Account: ref, Direction: p.Direction, Amount: i64(p.AmountMinor)})
	}
	return out, nil
}

// ApproveAdjustment is the checker step: approve the exact lines (different person, before expiry) and post the
// journal(s) in the same transaction. Returns the adjustment as POSTED.
func (s *Service) ApproveAdjustment(ctx context.Context, checkerID, id, justification string) (Adjustment, error) {
	justification, err := cleanReason(justification)
	if err != nil {
		return Adjustment{}, err
	}
	if !uuidRE.MatchString(id) {
		return Adjustment{}, ErrNotFound
	}
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := scanAdjustment(tx.QueryRow(ctx, adjustmentSelect+` WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		switch {
		case a.Status != "PENDING_APPROVAL":
			return ErrAdjustmentState
		case a.RequestedBy == checkerID:
			return ErrSelfApproval
		case !a.ExpiresAt.After(s.now()):
			return ErrAdjustmentExpired
		}
		if _, err := tx.Exec(ctx, `UPDATE ledger.ledger_adjustments SET status = 'APPROVED', approved_by = $2, approved_at = $3,
			approved_lines_sha256 = proposed_lines_sha256, approval_justification = $4 WHERE id = $1`, id, checkerID, s.now(), justification); err != nil {
			return mapDBError(err)
		}
		if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.adjustment.approved", ActorType: "staff", ActorID: checkerID,
			TargetType: "ledger_adjustment", TargetID: id, Reason: justification, Metadata: map[string]any{"kind": a.Kind, "posting_rule": a.PostingRule}}); err != nil {
			return err
		}
		actor := Actor{Type: "STAFF", ID: checkerID}
		var reversalID, resultID string
		if a.Kind == AdjReversal || a.Kind == AdjCorrection {
			p, err := mirrorPosting(ctx, tx, *a.Target, a.ID, a.Reason, actor, s.now())
			if err != nil {
				return err
			}
			res, err := s.Post(ctx, tx, p)
			if err != nil {
				return err
			}
			reversalID = res.TransactionID
		}
		if a.Kind == AdjCorrection || a.Kind == AdjManual {
			var pl []ProposedLine
			_ = json.Unmarshal(a.Lines, &pl)
			lines, err := toLines(pl)
			if err != nil {
				return err
			}
			srcType := "adjustment"
			r, _, err := s.ruleQ(ctx, tx, a.PostingRule)
			if err != nil {
				return err
			}
			if !contains(r.SourceTypes, srcType) && len(r.SourceTypes) > 0 {
				srcType = r.SourceTypes[0]
			}
			res, err := s.Post(ctx, tx, Posting{Rule: a.PostingRule, IdempotencyKey: "adjustment:" + a.ID, Currency: a.Currency, OccurredAt: s.now(),
				Description: "Approved adjustment " + a.ID, SourceSystem: SourceStaff, SourceType: srcType, SourceID: a.ID,
				SourceEventID: "adjustment:" + a.ID, EvidenceClass: s.adjustmentEvidence(ctx), AdjustmentID: a.ID, Reason: a.Reason, Actor: actor,
				Lines: lines})
			if err != nil {
				return err
			}
			resultID = res.TransactionID
		}
		if _, err := tx.Exec(ctx, `UPDATE ledger.ledger_adjustments SET status = 'POSTED', reversal_transaction_id = nullif($2, '')::uuid,
			resulting_transaction_id = nullif($3, '')::uuid, posted_at = $4 WHERE id = $1`, id, reversalID, resultID, s.now()); err != nil {
			return mapDBError(err)
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.adjustment.posted", ActorType: "system", TargetType: "ledger_adjustment",
			TargetID: id, Metadata: map[string]any{"reversal_transaction": reversalID, "resulting_transaction": resultID}})
		return err
	})
	if err != nil {
		return Adjustment{}, mapDBError(err)
	}
	return s.AdjustmentByID(ctx, id)
}

// adjustmentEvidence: a staff-approved journal is MANUALLY_REVIEWED evidence; in a TEST ledger fixtures stay SIMULATED
// when the target was synthetic — but the approval itself is a manual review, so MANUALLY_REVIEWED is recorded.
func (s *Service) adjustmentEvidence(context.Context) string { return EvidenceManuallyReviewed }

// mirrorPosting builds the exact mirror of a journal (L7).
func mirrorPosting(ctx context.Context, tx pgx.Tx, originalID, adjustmentID, reason string, actor Actor, now time.Time) (Posting, error) {
	var ccy, rule string
	if err := tx.QueryRow(ctx, `SELECT currency, posting_rule FROM ledger.ledger_transactions WHERE id = $1`, originalID).Scan(&ccy, &rule); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Posting{}, ErrNotFound
		}
		return Posting{}, err
	}
	if rule == "REVERSAL" {
		return Posting{}, ErrReversalOfReversal
	}
	rows, err := tx.Query(ctx, `SELECT a.account_class, coalesce(a.owner_id::text, ''), e.direction, e.amount_minor
		FROM ledger.ledger_entries e JOIN ledger.ledger_accounts a ON a.id = e.account_id WHERE e.transaction_id = $1 ORDER BY e.line_no`, originalID)
	if err != nil {
		return Posting{}, err
	}
	var lines []Line
	for rows.Next() {
		var class, owner, dir string
		var amt int64
		if err := rows.Scan(&class, &owner, &dir, &amt); err != nil {
			rows.Close()
			return Posting{}, err
		}
		inv := Credit
		if dir == Credit {
			inv = Debit
		}
		lines = append(lines, Line{Account: AccountRef{Class: class, OwnerID: owner}, Direction: inv, Amount: amt})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Posting{}, err
	}
	return Posting{Rule: "REVERSAL", IdempotencyKey: "reversal:" + originalID, Currency: ccy, OccurredAt: now,
		Description: "Reversal of journal " + originalID, SourceSystem: SourceStaff, SourceType: "adjustment", SourceID: adjustmentID,
		SourceEventID: "reversal:" + originalID, EvidenceClass: EvidenceManuallyReviewed, AdjustmentID: adjustmentID, Reverses: originalID,
		Reason: reason, Actor: actor, Lines: lines}, nil
}

// RejectAdjustment (checker ≠ maker) and WithdrawAdjustment (maker only) end a pending request.
func (s *Service) RejectAdjustment(ctx context.Context, staffID, id, reason string) (Adjustment, error) {
	return s.endAdjustment(ctx, staffID, id, reason, "REJECTED")
}

func (s *Service) WithdrawAdjustment(ctx context.Context, staffID, id, reason string) (Adjustment, error) {
	return s.endAdjustment(ctx, staffID, id, reason, "WITHDRAWN")
}

func (s *Service) endAdjustment(ctx context.Context, staffID, id, reason, to string) (Adjustment, error) {
	reason, err := cleanReason(reason)
	if err != nil {
		return Adjustment{}, err
	}
	if !uuidRE.MatchString(id) {
		return Adjustment{}, ErrNotFound
	}
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := scanAdjustment(tx.QueryRow(ctx, adjustmentSelect+` WHERE id = $1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if a.Status != "PENDING_APPROVAL" {
			return ErrAdjustmentState
		}
		if to == "REJECTED" && a.RequestedBy == staffID {
			return ErrSelfApproval
		}
		if to == "WITHDRAWN" && a.RequestedBy != staffID {
			return errs.New(errs.Forbidden, "LEDGER_NOT_REQUESTER", "Only the requester can withdraw an adjustment.")
		}
		q := `UPDATE ledger.ledger_adjustments SET status = 'WITHDRAWN' WHERE id = $1`
		args := []any{id}
		if to == "REJECTED" {
			q = `UPDATE ledger.ledger_adjustments SET status = 'REJECTED', rejected_by = $2, rejected_at = $3, rejection_reason = $4 WHERE id = $1`
			args = []any{id, staffID, s.now(), reason}
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return mapDBError(err)
		}
		_, err = audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.adjustment." + map[string]string{"REJECTED": "rejected", "WITHDRAWN": "withdrawn"}[to],
			ActorType: "staff", ActorID: staffID, TargetType: "ledger_adjustment", TargetID: id, Reason: reason})
		return err
	})
	if err != nil {
		return Adjustment{}, err
	}
	return s.AdjustmentByID(ctx, id)
}

// ExpireAdjustments marks overdue pending requests EXPIRED (job), auditing each one in the same transaction.
func (s *Service) ExpireAdjustments(ctx context.Context) (int64, error) {
	var n int64
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `UPDATE ledger.ledger_adjustments SET status = 'EXPIRED' WHERE status = 'PENDING_APPROVAL' AND expires_at <= $1
			RETURNING id::text`, s.now())
		if err != nil {
			return err
		}
		expired, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, id := range expired {
			if _, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.adjustment.expired", ActorType: "system",
				TargetType: "ledger_adjustment", TargetID: id, Outcome: "success"}); err != nil {
				return err
			}
		}
		n = int64(len(expired))
		return nil
	})
	return n, err
}

const adjustmentSelect = `SELECT id, kind, posting_rule, currency, target_transaction_id::text, proposed_lines, reason_code, reason, status,
	requested_by::text, requested_at, expires_at, approved_by::text, approved_at, rejected_by::text, reversal_transaction_id::text,
	resulting_transaction_id::text, posted_at FROM ledger.ledger_adjustments`

func scanAdjustment(r pgx.Row) (Adjustment, error) {
	var a Adjustment
	err := r.Scan(&a.ID, &a.Kind, &a.PostingRule, &a.Currency, &a.Target, &a.Lines, &a.ReasonCode, &a.Reason, &a.Status, &a.RequestedBy,
		&a.RequestedAt, &a.ExpiresAt, &a.ApprovedBy, &a.ApprovedAt, &a.RejectedBy, &a.ReversalTx, &a.ResultingTx, &a.PostedAt)
	return a, err
}

// AdjustmentByID returns one adjustment.
func (s *Service) AdjustmentByID(ctx context.Context, id string) (Adjustment, error) {
	if !uuidRE.MatchString(id) {
		return Adjustment{}, ErrNotFound
	}
	a, err := scanAdjustment(s.Pool.QueryRow(ctx, adjustmentSelect+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Adjustment{}, ErrNotFound
	}
	return a, err
}

// Adjustments lists adjustments, newest first (optionally by status).
func (s *Service) Adjustments(ctx context.Context, status string, limit int) ([]Adjustment, error) {
	rows, err := s.Pool.Query(ctx, adjustmentSelect+` WHERE ($1 = '' OR status = $1) ORDER BY requested_at DESC, id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Adjustment, error) { return scanAdjustment(r) })
}
