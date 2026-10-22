package payouts

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
	"github.com/Fatifizo/fundzim/internal/psp"
)

// Stage 17 reconciliation surface (ADR-049 §1, §7). Reconciliation reads payouts only through these methods and changes
// a payout only through ApplyReconciliationOutcome, which goes through applyStatus — the payout's own state machine with
// its reservation transitions, journals and exceptions — with source RECON. It never writes a payout, attempt or
// reservation row, never submits or resubmits anything, and never touches an UNKNOWN payout except to apply evidence.

// ReconFact is one payout as reconciliation sees it (no destination details beyond the masked form).
type ReconFact struct {
	ID                string
	Status            string
	AmountMinor       int64
	Currency          string
	ProviderID        string
	ProviderReference string
	TestPayout        bool
	SubmittedAt       *time.Time
	CompletedAt       *time.Time
	ReservationStatus string
	CreateAttempts    int // CREATE_PAYOUT attempts (all classifications)
}

const reconFactSelect = `SELECT p.id::text, p.status, p.amount_minor, p.currency, p.provider_id::text, coalesce(p.provider_payout_ref, ''),
	p.test_payout, p.submitted_at, p.completed_at, coalesce(r.status, ''),
	(SELECT count(*) FROM app.payout_attempts a WHERE a.payout_request_id = p.id AND a.operation = 'CREATE_PAYOUT')
	FROM app.payout_requests p LEFT JOIN app.payout_reservations r ON r.payout_request_id = p.id`

func scanReconFact(r pgx.CollectableRow) (ReconFact, error) {
	var f ReconFact
	err := r.Scan(&f.ID, &f.Status, &f.AmountMinor, &f.Currency, &f.ProviderID, &f.ProviderReference, &f.TestPayout, &f.SubmittedAt,
		&f.CompletedAt, &f.ReservationStatus, &f.CreateAttempts)
	return f, err
}

// ReconFacts returns the provider's payouts named by our ids or by the provider's references.
func (s *Service) ReconFacts(ctx context.Context, providerCode string, payoutIDs, providerRefs []string) ([]ReconFact, error) {
	prov, err := s.PSP.ProviderByCode(ctx, providerCode)
	if err != nil {
		return nil, err
	}
	valid := make([]string, 0, len(payoutIDs))
	for _, id := range payoutIDs {
		if ids.Valid(id) {
			valid = append(valid, id)
		}
	}
	rows, err := s.Pool.Query(ctx, reconFactSelect+` WHERE p.provider_id = $1 AND (p.id = ANY ($2::uuid[]) OR p.provider_payout_ref = ANY ($3::text[]))
		ORDER BY p.id`, prov.ID, valid, providerRefs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanReconFact)
}

// ReconCompletedInWindow lists the provider's payouts in one currency completed inside [from, to): the payouts a
// statement covering that window must contain (MISSING_SETTLEMENT otherwise). Oldest first.
func (s *Service) ReconCompletedInWindow(ctx context.Context, providerCode, currency string, from, to time.Time, limit int) ([]ReconFact, error) {
	prov, err := s.PSP.ProviderByCode(ctx, providerCode)
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, reconFactSelect+` WHERE p.provider_id = $1 AND p.currency = $2 AND p.status IN ('COMPLETED','REVERSED')
		AND p.completed_at >= $3 AND p.completed_at < $4 ORDER BY p.completed_at, p.id LIMIT $5`, prov.ID, currency, from, to, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanReconFact)
}

// ReconOutcome is an approved reconciliation resolution's statement evidence for one payout.
type ReconOutcome struct {
	PayoutID         string
	Target           string // psp.PayoutCompleted or psp.PayoutFailed
	ProviderRef      string
	AmountMinor      int64
	Currency         string
	EvidenceKey      string // unique per statement line: the same evidence is applied once
	EvidenceRecordID string // the statement's audit evidence record
	OccurredAt       time.Time
	Reason           string
}

// ErrReconTarget is returned for a target statement evidence can never drive.
var ErrReconTarget = errors.New("payouts: reconciliation evidence may only report completed or failed")

// ApplyReconciliationOutcome applies statement evidence to a payout under its row lock through applyStatus (source
// RECON): an UNKNOWN, SUBMITTED or PROCESSING payout can complete or fail by the machine's own edges (Y10, Y11; the
// reservation is consumed or released by those edges and the journals follow through syncLedger); anything else is
// recorded as not applied. A differing amount or currency is an anomaly with a SEV1 exception, never applied. The same
// evidence key is applied once. Nothing is ever (re)submitted to a provider.
func (s *Service) ApplyReconciliationOutcome(ctx context.Context, in ReconOutcome) (applied bool, err error) {
	if in.Target != psp.PayoutCompleted && in.Target != psp.PayoutFailed {
		return false, ErrReconTarget
	}
	if strings.TrimSpace(in.EvidenceKey) == "" || len(in.EvidenceKey) > 200 {
		return false, errors.New("payouts: reconciliation evidence key is required")
	}
	at := in.OccurredAt.UTC()
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		p, err := s.payout(ctx, tx, in.PayoutID, true)
		if err != nil {
			return err
		}
		var dup bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.payout_events WHERE payout_request_id = $1 AND provider_event_id = $2)`,
			p.ID, in.EvidenceKey).Scan(&dup); err != nil {
			return err
		}
		e := event{Source: "RECON", ProviderEventID: in.EvidenceKey, RawStatus: "statement:" + in.Target, OccurredAt: &at,
			EvidenceID: in.EvidenceRecordID, Reason: in.Reason}
		if dup {
			e.Type, e.NotApplied, e.ProviderEventID = "PROVIDER_RESPONSE_RECEIVED", "DUPLICATE", ""
			return s.insertEvent(ctx, tx, p.ID, e)
		}
		if in.AmountMinor != p.AmountMinor || in.Currency != p.Currency {
			e.Type, e.NotApplied = "PROVIDER_RESPONSE_RECEIVED", "ANOMALY"
			if err := s.insertEvent(ctx, tx, p.ID, e); err != nil {
				return err
			}
			return s.openException(ctx, tx, "RECONCILIATION_MISMATCH", "SEV1", "RECONCILIATION", &p, "",
				map[string]any{"reason": "AMOUNT_OR_CURRENCY_MISMATCH", "evidence_key": in.EvidenceKey}, "")
		}
		if in.ProviderRef != "" && p.ProviderPayoutRef == nil {
			if _, err := tx.Exec(ctx, `UPDATE app.payout_requests SET provider_payout_ref = $2 WHERE id = $1`, p.ID, in.ProviderRef); err != nil {
				return err
			}
		}
		before := p.Status
		if err := s.applyStatus(ctx, tx, &p, in.Target, e, psp.Observation{ProviderRef: in.ProviderRef}); err != nil {
			return err
		}
		applied = p.Status != before
		return nil
	})
	if err != nil {
		return false, err
	}
	return applied, s.syncLedger(ctx, in.PayoutID)
}

// ReconQueueItem is a payout that reconciliation must watch: in flight, UNKNOWN, or halted by the retry budget.
type ReconQueueItem struct {
	ID                string     `json:"id"`
	Status            string     `json:"status"`
	AmountMinor       string     `json:"amount_minor"`
	Currency          string     `json:"currency"`
	ProviderReference *string    `json:"provider_reference"`
	TestPayout        bool       `json:"test_payout"`
	SubmittedAt       *time.Time `json:"submitted_at"`
	UnknownSince      *time.Time `json:"unknown_since"`
	UnknownReason     *string    `json:"unknown_reason"`
	ReservationStatus *string    `json:"reservation_status"`
	CreateAttempts    int        `json:"create_attempts"`
	NotSentAttempts   int        `json:"not_sent_attempts"`
	LastAttemptClass  *string    `json:"last_attempt_classification"`
	OpenExceptions    []string   `json:"open_exceptions"`
	RetryHalted       bool       `json:"retry_halted"`
}

// ReconQueue lists payouts that are SUBMITTED, PROCESSING or UNKNOWN (oldest first) with their attempt summary,
// reservation state and open exception types — a read model for the reconciliation payout view (no destination data).
func (s *Service) ReconQueue(ctx context.Context, limit int) ([]ReconQueueItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `SELECT p.id::text, p.status, p.amount_minor::text, p.currency, p.provider_payout_ref, p.test_payout, p.submitted_at,
		p.unknown_since, p.unknown_reason, r.status,
		(SELECT count(*) FROM app.payout_attempts a WHERE a.payout_request_id = p.id AND a.operation = 'CREATE_PAYOUT'),
		(SELECT count(*) FROM app.payout_attempts a WHERE a.payout_request_id = p.id AND a.operation = 'CREATE_PAYOUT'
		   AND a.classification IN ('DEFINITELY_NOT_SENT','RATE_LIMITED')),
		(SELECT a.classification FROM app.payout_attempts a WHERE a.payout_request_id = p.id ORDER BY a.started_at DESC, a.id DESC LIMIT 1),
		coalesce((SELECT array_agg(DISTINCT x.exception_type ORDER BY x.exception_type) FROM app.payout_exceptions x
		   WHERE x.payout_request_id = p.id AND x.status <> 'RESOLVED'), ARRAY[]::text[])
		FROM app.payout_requests p LEFT JOIN app.payout_reservations r ON r.payout_request_id = p.id
		WHERE p.status IN ('SUBMITTED','PROCESSING','UNKNOWN') ORDER BY p.submitted_at NULLS LAST, p.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ReconQueueItem, error) {
		var x ReconQueueItem
		err := r.Scan(&x.ID, &x.Status, &x.AmountMinor, &x.Currency, &x.ProviderReference, &x.TestPayout, &x.SubmittedAt, &x.UnknownSince,
			&x.UnknownReason, &x.ReservationStatus, &x.CreateAttempts, &x.NotSentAttempts, &x.LastAttemptClass, &x.OpenExceptions)
		for _, t := range x.OpenExceptions {
			if t == ExcSubmissionRetriesExhausted {
				x.RetryHalted = true
			}
		}
		return x, err
	})
}
