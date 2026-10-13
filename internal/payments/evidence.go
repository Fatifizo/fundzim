package payments

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/platform/outbox"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Evidence consistency (payment_provider_evidence.consistency).
const (
	EvidenceFirst       = "FIRST"
	EvidenceConsistent  = "CONSISTENT"
	EvidenceProgression = "PROGRESSION"
	EvidenceContradicts = "CONTRADICTS"
)

const reconContradictory = "CONTRADICTORY_EVIDENCE"

// successFamily are statuses that mean the provider took the money at some point.
var successFamily = map[string]bool{psp.StatusSucceeded: true, psp.StatusPartiallyRefunded: true, psp.StatusRefunded: true,
	psp.StatusDisputed: true, psp.StatusChargedBack: true}

// failureFamily are statuses that mean the money was never taken.
var failureFamily = map[string]bool{psp.StatusFailed: true, psp.StatusExpired: true, psp.StatusCancelled: true}

type evidenceRow struct {
	ID       string
	Mapped   string
	Amount   *int64
	Currency *string
}

// classifyEvidence compares a new observation with the earlier evidence of the same payment. A failure-family
// status after success-family evidence, or a reported amount/currency that differs from an earlier report, is a
// contradiction (returned with the contradicted row). Success after failure is not classified here as a
// contradiction: the state machine already treats it as a late success with its own reconciliation reference.
func classifyEvidence(prev []evidenceRow, mapped string, isMapped bool, amount *int64, ccy string) (string, string) {
	if len(prev) == 0 {
		return EvidenceFirst, ""
	}
	if amount != nil && ccy != "" {
		for i := len(prev) - 1; i >= 0; i-- {
			e := prev[i]
			if e.Amount != nil && e.Currency != nil && (*e.Amount != *amount || *e.Currency != ccy) {
				return EvidenceContradicts, e.ID
			}
		}
	}
	if isMapped && failureFamily[mapped] {
		for i := len(prev) - 1; i >= 0; i-- {
			if successFamily[prev[i].Mapped] {
				return EvidenceContradicts, prev[i].ID
			}
		}
	}
	if prev[len(prev)-1].Mapped == mapped {
		return EvidenceConsistent, ""
	}
	return EvidenceProgression, ""
}

// recordEvidence appends one provider observation to the evidence log (never updated or deleted) and escalates a
// contradiction: a reconciliation reference CONTRADICTORY_EVIDENCE, an outbox event and an alert log line. The
// state machine still decides the status; late contradictory evidence is recorded, never silently dropped.
func (s *Service) recordEvidence(ctx context.Context, tx pgx.Tx, p *payRow, o Obs) error {
	rows, err := tx.Query(ctx, `SELECT id, mapped_status, reported_amount_minor, reported_currency FROM app.payment_provider_evidence
		WHERE payment_intent_id = $1 ORDER BY observed_at, id`, p.ID)
	if err != nil {
		return err
	}
	prev, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (evidenceRow, error) {
		var e evidenceRow
		err := r.Scan(&e.ID, &e.Mapped, &e.Amount, &e.Currency)
		return e, err
	})
	if err != nil {
		return err
	}
	mapped := o.Target
	if !o.Mapped || mapped == "" {
		mapped = psp.StatusUnknown
	}
	var amt *int64
	var ccy *string
	if o.ReportedAmount != nil && o.ReportedCcy != "" {
		amt, ccy = o.ReportedAmount, &o.ReportedCcy
	}
	consistency, contradicts := classifyEvidence(prev, mapped, o.Mapped, amt, o.ReportedCcy)
	raw := o.RawStatus
	if len(raw) > 200 {
		raw = raw[:200]
	}
	var ref *string
	if o.ProviderRef != "" {
		ref = &o.ProviderRef
	} else if p.ProviderPaymentRef != nil {
		ref = p.ProviderPaymentRef
	}
	id := ids.New()
	// a re-dispatched delivery of the same provider event is the same evidence: recorded once (DB backstop:
	// uq_payment_provider_evidence_event; callers hold the payment row lock)
	tag, err := tx.Exec(ctx, `INSERT INTO app.payment_provider_evidence (id, payment_intent_id, provider_id, execution_environment, source,
		payment_attempt_id, inbox_event_id, provider_event_id, provider_reference, raw_status, mapped_status, mapped, mapping_version,
		reported_amount_minor, reported_currency, provider_occurred_at, observed_at, consistency, contradicts_evidence_id)
		VALUES ($1, $2, $3, $4, $5, nullif($6, '')::uuid, nullif($7, '')::uuid, nullif($8, ''), $9, $10, $11, $12, $13, $14, $15, $16, $17, $18,
		nullif($19, '')::uuid)
		ON CONFLICT (payment_intent_id, provider_event_id) WHERE provider_event_id IS NOT NULL DO NOTHING`,
		id, p.ID, p.ProviderID, p.ExecEnv, o.Source, o.AttemptID, o.InboxID, o.ProviderEventID, ref, raw, mapped, o.Mapped,
		nonEmpty(o.MappingVersion, "none"), amt, ccy, o.OccurredAt, s.now(), consistency, contradicts)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 || consistency != EvidenceContradicts {
		return nil
	}
	s.Logger.Warn("payments: provider evidence contradicts earlier evidence (ALERT)", "payment_id", p.ID, "evidence_id", id,
		"contradicts", contradicts, "mapped_status", mapped)
	if err := s.openRecon(ctx, tx, p, reconContradictory, o); err != nil {
		return err
	}
	_, err = outbox.Write(ctx, tx, outbox.Event{AggregateType: "payment", AggregateID: p.ID, EventType: "payments.evidence_contradiction",
		Payload: map[string]any{"payment_id": p.ID, "evidence_id": id, "contradicts_evidence_id": contradicts, "mapped_status": mapped,
			"execution_environment": p.ExecEnv}, CorrelationID: o.Correlation, OccurredAt: s.now()})
	return err
}

// Evidence is one evidence-log row (staff view).
type Evidence struct {
	ID                    string     `json:"id"`
	Source                string     `json:"source"`
	ExecutionEnvironment  string     `json:"execution_environment"`
	ProviderReference     *string    `json:"provider_reference"`
	ProviderEventID       *string    `json:"provider_event_id"`
	RawStatus             string     `json:"raw_status"`
	MappedStatus          string     `json:"mapped_status"`
	Mapped                bool       `json:"mapped"`
	MappingVersion        string     `json:"mapping_version"`
	ReportedAmountMinor   *string    `json:"reported_amount_minor"`
	ReportedCurrency      *string    `json:"reported_currency"`
	ProviderOccurredAt    *time.Time `json:"provider_occurred_at"`
	ObservedAt            time.Time  `json:"observed_at"`
	Consistency           string     `json:"consistency"`
	ContradictsEvidenceID *string    `json:"contradicts_evidence_id"`
}

// PaymentEvidence lists a payment's evidence log in observation order.
func (s *Service) PaymentEvidence(ctx context.Context, paymentID string) ([]Evidence, error) {
	if !ids.Valid(paymentID) {
		return nil, ErrPaymentNotFound
	}
	var exists bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.payment_intents WHERE id = $1)`, paymentID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrPaymentNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, source, execution_environment, provider_reference, provider_event_id, raw_status, mapped_status,
		mapped, mapping_version, reported_amount_minor, reported_currency, provider_occurred_at, observed_at, consistency, contradicts_evidence_id
		FROM app.payment_provider_evidence WHERE payment_intent_id = $1 ORDER BY observed_at, id LIMIT 500`, paymentID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Evidence, error) {
		var e Evidence
		var amt *int64
		err := r.Scan(&e.ID, &e.Source, &e.ExecutionEnvironment, &e.ProviderReference, &e.ProviderEventID, &e.RawStatus, &e.MappedStatus,
			&e.Mapped, &e.MappingVersion, &amt, &e.ReportedCurrency, &e.ProviderOccurredAt, &e.ObservedAt, &e.Consistency, &e.ContradictsEvidenceID)
		if amt != nil {
			v := i64s(*amt)
			e.ReportedAmountMinor = &v
		}
		return e, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return []Evidence{}, nil
	}
	return out, err
}

// errorCategoryCounts fills normalised provider error counts of the last 24 hours (provider calls are owned here).
func (s *Service) errorCategoryCounts(ctx context.Context, providerID string, into map[string]int) error {
	rows, err := s.Pool.Query(ctx, `SELECT error_category, count(*) FROM app.payment_attempts
		WHERE provider_id = $1 AND error_category IS NOT NULL AND started_at > $2 GROUP BY error_category`, providerID, s.now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return err
		}
		into[c] = n
	}
	return rows.Err()
}
