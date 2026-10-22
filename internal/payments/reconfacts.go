package payments

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Stage 17 reconciliation surface (ADR-049 §1, §7). Reconciliation reads payments only through these methods and
// changes a payment only through ApplyReconciliationOutcome, which feeds the payment's own state machine (apply) with
// source RECON — never a direct status write, never a manual success override.

// ReconFact is one payment as reconciliation sees it (no personal data).
type ReconFact struct {
	ID                   string
	Status               string
	AmountMinor          int64
	Currency             string
	ExecutionEnvironment string
	ProviderID           string
	ProviderReferences   []string // every PAYMENT reference the provider gave for it
	CreatedAt            time.Time
	SucceededAt          *time.Time // first applied SUCCEEDED transition
}

// TestPayment reports whether the payment is a TEST payment (never posted to the ledger, ADR-042).
func (f ReconFact) TestPayment() bool { return f.ExecutionEnvironment != psp.ExecProduction }

const reconFactSelect = `SELECT p.id::text, p.status, p.amount_minor, p.currency, p.execution_environment, p.provider_id::text,
	coalesce((SELECT array_agg(r.reference ORDER BY r.reference) FROM app.payment_provider_references r
	           WHERE r.payment_intent_id = p.id AND r.ref_kind = 'PAYMENT'), ARRAY[]::text[]),
	p.created_at,
	(SELECT min(e.recorded_at) FROM app.payment_events e WHERE e.payment_intent_id = p.id AND e.applied AND e.to_status = 'SUCCEEDED')
	FROM app.payment_intents p`

func scanReconFact(r pgx.CollectableRow) (ReconFact, error) {
	var f ReconFact
	err := r.Scan(&f.ID, &f.Status, &f.AmountMinor, &f.Currency, &f.ExecutionEnvironment, &f.ProviderID, &f.ProviderReferences,
		&f.CreatedAt, &f.SucceededAt)
	return f, err
}

// ReconFacts returns the payments of the provider identified by our ids or by the provider's references (both lists
// bounded by the caller). A reference that names no payment is simply absent: reconciliation decides what that means.
// providerCode "" looks across every provider by our ids only (bank-statement checks: a FundZim payment id printed on
// a transfer into FundZim's own account).
func (s *Service) ReconFacts(ctx context.Context, providerCode string, paymentIDs, providerRefs []string) ([]ReconFact, error) {
	if providerCode == "" {
		valid := make([]string, 0, len(paymentIDs))
		for _, id := range paymentIDs {
			if ids.Valid(id) {
				valid = append(valid, id)
			}
		}
		rows, err := s.Pool.Query(ctx, reconFactSelect+` WHERE p.id = ANY ($1::uuid[]) ORDER BY p.id`, valid)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, scanReconFact)
	}
	prov, err := s.PSP.ProviderByCode(ctx, providerCode)
	if err != nil {
		return nil, err
	}
	valid := make([]string, 0, len(paymentIDs))
	for _, id := range paymentIDs {
		if ids.Valid(id) {
			valid = append(valid, id)
		}
	}
	rows, err := s.Pool.Query(ctx, reconFactSelect+` WHERE p.provider_id = $1 AND (p.id = ANY ($2::uuid[]) OR p.id IN (
		SELECT r.payment_intent_id FROM app.payment_provider_references r WHERE r.provider_id = $1 AND r.ref_kind = 'PAYMENT'
		AND r.reference = ANY ($3::text[]))) ORDER BY p.id`, prov.ID, valid, providerRefs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanReconFact)
}

// ReconSucceededInWindow lists the provider's payments in one currency that first succeeded inside [from, to): the
// payments a provider statement covering that window must contain (MISSING_SETTLEMENT otherwise). Oldest first.
func (s *Service) ReconSucceededInWindow(ctx context.Context, providerCode, currency string, from, to time.Time, limit int) ([]ReconFact, error) {
	prov, err := s.PSP.ProviderByCode(ctx, providerCode)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, reconFactSelect+` WHERE p.provider_id = $1 AND p.currency = $2 AND p.id IN (
		SELECT e.payment_intent_id FROM app.payment_events e WHERE e.applied AND e.to_status = 'SUCCEEDED'
		AND e.recorded_at >= $3 AND e.recorded_at < $4) ORDER BY p.created_at, p.id LIMIT $5`, prov.ID, currency, from, to, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanReconFact)
}

// ReconOutcome is an approved reconciliation resolution's statement evidence for one payment.
type ReconOutcome struct {
	PaymentID   string
	Target      string // SUCCEEDED or FAILED (the only targets the RECON source may drive, machine.go and SQL)
	ProviderRef string
	AmountMinor int64
	Currency    string
	EvidenceKey string // stable, unique per statement line (idempotency: the same evidence is applied once)
	OccurredAt  time.Time
	Reason      string
}

// ReconApplyResult reports what the payment's state machine did with the evidence.
type ReconApplyResult struct {
	Applied    bool   `json:"applied"`
	NotApplied string `json:"not_applied,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
}

// ErrReconTarget is returned for a target the RECON source can never drive.
var ErrReconTarget = errors.New("payments: reconciliation evidence may only report SUCCEEDED or FAILED")

// ApplyReconciliationOutcome applies statement evidence through apply() with source RECON, in one transaction under the
// payment's row lock: the provider evidence log records it (contradictions are escalated, never discarded), a reported
// amount or currency that differs is an anomaly (not applied), the same evidence key is applied once, and only the
// machine's own edges are possible. Callers (reconciliation) invoke it only for a maker-checker approved resolution.
func (s *Service) ApplyReconciliationOutcome(ctx context.Context, in ReconOutcome) (ReconApplyResult, error) {
	if in.Target != psp.StatusSucceeded && in.Target != psp.StatusFailed {
		return ReconApplyResult{}, ErrReconTarget
	}
	if strings.TrimSpace(in.EvidenceKey) == "" || len(in.EvidenceKey) > 200 {
		return ReconApplyResult{}, errors.New("payments: reconciliation evidence key is required")
	}
	var res ReconApplyResult
	err := db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		p, err := lockPay(ctx, tx, in.PaymentID)
		if err != nil {
			return err
		}
		amt := in.AmountMinor
		at := in.OccurredAt.UTC()
		out, err := s.apply(ctx, tx, &p, Obs{Source: SrcRecon, Target: in.Target, RawStatus: "statement:" + strings.ToLower(in.Target),
			Mapped: true, MappingVersion: "recon-canonical-v1", ProviderRef: in.ProviderRef, ReportedAmount: &amt, ReportedCcy: in.Currency,
			ProviderEventID: in.EvidenceKey, OccurredAt: &at, Reason: in.Reason})
		if err != nil {
			return err
		}
		res = ReconApplyResult{Applied: out.Applied, NotApplied: out.NotApplied, From: out.From, To: out.To}
		return nil
	})
	return res, err
}
