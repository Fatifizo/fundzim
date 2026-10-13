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

// PaymentFactsInStatus lists payments in a status last updated before the cut-off, oldest first.
func (s *Service) PaymentFactsInStatus(ctx context.Context, status string, before time.Time, limit int) ([]Fact, error) {
	rows, err := s.Pool.Query(ctx, factSelect+` WHERE status = $1 AND updated_at < $2 ORDER BY updated_at, id LIMIT $3`, status, before, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Fact, error) { return scanFact(r) })
}
