package sandbox

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/psp"
)

// Stage 17 (ADR-049 §2): the simulator can describe its own records as a TEST statement, so reconciliation can be
// exercised end to end without a real provider. This is the SIMULATOR's view of what it did — never a provider's
// statement format, never settlement evidence, never available outside the simulator (the sandbox is refused in
// production). It reports no fees: the simulator has no fee model (an absent fee is NOT_REPORTED, never 0).

// StatementLine is one simulated payment or payout in canonical terms.
type StatementLine struct {
	Kind         string // PAYMENT or PAYOUT
	ProviderRef  string
	OurReference string
	Currency     string
	AmountMinor  int64
	Status       string // SUCCEEDED, FAILED, PENDING (payments); COMPLETED, FAILED, PENDING, RETURNED (payouts)
	BookedAt     time.Time
}

// StatementLines lists the simulator's payments and payouts in one currency last changed inside [from, to), oldest
// first, at most limit lines.
func (a *Adapter) StatementLines(ctx context.Context, currency string, from, to time.Time, limit int) ([]StatementLine, error) {
	rows, err := a.Pool.Query(ctx, `SELECT kind, ref, our, currency, amount_minor, raw_status, updated_at FROM (
		  SELECT 'PAYMENT' AS kind, provider_payment_ref AS ref, our_reference::text AS our, currency, amount_minor, raw_status, updated_at
		    FROM app.sandbox_provider_payments WHERE provider_id = $1 AND currency = $2 AND updated_at >= $3 AND updated_at < $4
		  UNION ALL
		  SELECT 'PAYOUT', provider_payout_ref, our_reference::text, currency, amount_minor, raw_status, updated_at
		    FROM app.sandbox_provider_payouts WHERE provider_id = $1 AND currency = $2 AND updated_at >= $3 AND updated_at < $4) x
		ORDER BY updated_at, ref LIMIT $5`, a.ProviderID, currency, from, to, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (StatementLine, error) {
		var l StatementLine
		var raw string
		if err := r.Scan(&l.Kind, &l.ProviderRef, &l.OurReference, &l.Currency, &l.AmountMinor, &raw, &l.BookedAt); err != nil {
			return l, err
		}
		if l.Kind == "PAYMENT" {
			c, _, _ := a.MapPaymentStatus(raw)
			l.Status = paymentLineStatus(c)
		} else {
			c, _, _ := a.MapPayoutStatus(raw)
			l.Status = payoutLineStatus(c)
		}
		return l, nil
	})
}

func paymentLineStatus(canonical string) string {
	switch canonical {
	case psp.StatusSucceeded, psp.StatusPartiallyRefunded, psp.StatusRefunded, psp.StatusDisputed, psp.StatusChargedBack:
		return "SUCCEEDED"
	case psp.StatusFailed, psp.StatusExpired, psp.StatusCancelled:
		return "FAILED"
	}
	return "PENDING"
}

func payoutLineStatus(canonical string) string {
	switch canonical {
	case psp.PayoutCompleted:
		return "COMPLETED"
	case psp.PayoutReturned:
		return "RETURNED"
	case psp.PayoutFailed:
		return "FAILED"
	}
	return "PENDING"
}
