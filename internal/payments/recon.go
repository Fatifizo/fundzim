package payments

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
)

// Reconciliation reasons.
const (
	reconUnknownHorizon   = "UNKNOWN_HORIZON_EXCEEDED"
	reconNotFoundHorizon  = "NOT_FOUND_AFTER_HORIZON"
	reconAmountMismatch   = "AMOUNT_MISMATCH"
	reconCurrencyMismatch = "CURRENCY_MISMATCH"
	reconAnomaly          = "ANOMALY"
	reconLateSuccess      = "LATE_SUCCESS"
	reconDuplicateSuccess = "DUPLICATE_SUCCESS"
	reconParkedDeadline   = "PARKED_DEADLINE_EXCEEDED"
	reconUnmapped         = "UNMAPPED_STATUS"
)

// openRecon opens (at most one open reference per payment and reason) a reconciliation reference and flags
// the payment. It never marks anything reconciled: that needs settlement data (Stage 17).
func (s *Service) openRecon(ctx context.Context, tx pgx.Tx, p *payRow, reason string, o Obs) error {
	var attempt, inbox *string
	if ids.Valid(o.AttemptID) {
		attempt = &o.AttemptID
	}
	if ids.Valid(o.InboxID) {
		inbox = &o.InboxID
	}
	var ref *string
	if o.ProviderRef != "" {
		ref = &o.ProviderRef
	} else if p.ProviderPaymentRef != nil {
		ref = p.ProviderPaymentRef
	}
	var amt *int64
	var ccy *string
	if o.ReportedAmount != nil && o.ReportedCcy != "" {
		amt, ccy = o.ReportedAmount, &o.ReportedCcy
	}
	var raw *string
	if o.RawStatus != "" {
		r := o.RawStatus
		if len(r) > 200 {
			r = r[:200]
		}
		raw = &r
	}
	evidence := s.now()
	if o.OccurredAt != nil {
		evidence = *o.OccurredAt
	}
	id := ids.New()
	tag, err := tx.Exec(ctx, `INSERT INTO app.payment_reconciliation_references (id, payment_intent_id, provider_id, payment_attempt_id,
		inbox_event_id, provider_reference, reason, expected_amount_minor, expected_currency, reported_amount_minor, reported_currency,
		provider_status_raw, status, evidence_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'OPEN', $13)
		ON CONFLICT (payment_intent_id, reason) WHERE status <> 'RESOLVED_BY_PROVIDER_STATUS' DO NOTHING`,
		id, p.ID, p.ProviderID, attempt, inbox, ref, reason, p.Amount, p.Currency, amt, ccy, raw, evidence)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE app.payment_intents SET needs_reconciliation = true WHERE id = $1 AND NOT needs_reconciliation`, p.ID); err != nil {
		return err
	}
	p.NeedsRecon = true
	if tag.RowsAffected() == 0 {
		return nil
	}
	s.m.recon.WithLabelValues(reason).Inc()
	_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "payment", AggregateID: p.ID, EventType: "payments.reconciliation_required",
		Payload: map[string]any{"payment_id": p.ID, "reason": reason, "simulated": p.Simulated}, CorrelationID: o.Correlation, OccurredAt: s.now()})
	return err
}

// AwaitSettlement moves an OPEN reference to AWAITING_SETTLEMENT_DATA (staff, step-up by route policy). It never
// changes the payment's status.
func (s *Service) AwaitSettlement(ctx context.Context, staffID, refID, note string) error {
	if !ids.Valid(refID) {
		return ErrReconNotFound
	}
	note, ok := cleanReason(note)
	if !ok {
		return invalidReason("note")
	}
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		var st, pid string
		err := tx.QueryRow(ctx, `SELECT status, payment_intent_id FROM app.payment_reconciliation_references WHERE id = $1 FOR UPDATE`, refID).Scan(&st, &pid)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrReconNotFound
		}
		if err != nil {
			return err
		}
		if st != "OPEN" {
			return ErrReconState
		}
		if _, err := tx.Exec(ctx, `UPDATE app.payment_reconciliation_references SET status = 'AWAITING_SETTLEMENT_DATA', version = version + 1
			WHERE id = $1`, refID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "payments.reconciliation.awaiting_settlement", ActorType: "staff", ActorID: staffID,
			TargetType: "payment_reconciliation_reference", TargetID: refID, Metadata: map[string]any{"payment_id": pid, "note": note}})
	})
}
