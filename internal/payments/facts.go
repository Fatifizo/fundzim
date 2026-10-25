package payments

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Fact is what reconciliation needs to know about one payment (no personal data). The ledger reads payments only
// through these methods (composition-root adapter), never the payment tables.
type Fact struct {
	ID                   string
	Status               string
	AmountMinor          int64
	Currency             string
	ExecutionEnvironment string
	ProviderReference    string
	UpdatedAt            time.Time
}

const factSelect = `SELECT id::text, status, amount_minor, currency, execution_environment, coalesce(provider_payment_ref, ''), updated_at
	FROM app.payment_intents`

func scanFact(r pgx.Row) (Fact, error) {
	var f Fact
	err := r.Scan(&f.ID, &f.Status, &f.AmountMinor, &f.Currency, &f.ExecutionEnvironment, &f.ProviderReference, &f.UpdatedAt)
	return f, err
}

// PaymentFact returns one payment's facts.
func (s *Service) PaymentFact(ctx context.Context, id string) (Fact, bool, error) {
	if !ids.Valid(id) {
		return Fact{}, false, nil
	}
	f, err := scanFact(s.Pool.QueryRow(ctx, factSelect+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Fact{}, false, nil
	}
	return f, err == nil, err
}

// PaymentFactsInStatus lists payments in a status last updated before the cut-off, newest first (a window of the
// newest records: reading the oldest N would never reach new payments once more than N exist — KI-S19-09, F-R1-01;
// the ledger's PAYMENT_FACTS coverage sweep covers the rest through PaymentFactIDsAfter).
func (s *Service) PaymentFactsInStatus(ctx context.Context, status string, before time.Time, limit int) ([]Fact, error) {
	rows, err := s.Pool.Query(ctx, factSelect+` WHERE status = $1 AND updated_at < $2 ORDER BY updated_at DESC, id DESC LIMIT $3`, status, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Fact, error) { return scanFact(r) })
}

// PaymentFactIDsAfter lists the ids of payments in one of the statuses with an id greater than after, in id order.
func (s *Service) PaymentFactIDsAfter(ctx context.Context, after string, statuses []string, limit int) ([]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id::text FROM app.payment_intents p WHERE p.id > $1::uuid AND p.status = ANY($2::text[]) ORDER BY p.id LIMIT $3`,
		after, statuses, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// OpenRefundExposure is the total of the campaign's refund requests in one currency that are not yet terminal
// (requested, under review, on hold, approved or executing): money a payout must not take (Stage 11, EC-09). No
// refund executes in Stage 8–11, so none of these amounts is yet reflected in the ledger.
func (s *Service) OpenRefundExposure(ctx context.Context, campaignID, currency string) (int64, error) {
	var n int64
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(sum(amount_minor), 0) FROM app.refund_requests WHERE campaign_id = $1 AND currency = $2
		AND status IN ('REQUESTED','PENDING_APPROVAL','ON_HOLD','APPROVED','EXECUTING')`, campaignID, currency).Scan(&n)
	return n, err
}
