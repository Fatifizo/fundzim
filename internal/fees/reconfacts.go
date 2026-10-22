package fees

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Stage 17 reconciliation surface (ADR-049 §1): the provider-fee expectation for payments and payouts, so a statement's
// reported fee can be compared with what FundZim assessed. Read-only; never changes an assessment, a schedule or a
// journal. Provider fee evidence stays SYNTHETIC/UNVERIFIED until a provider is selected (ADR-040, PCR-016).

// ProviderFeeFact is the assessed provider fee of one source (payment or payout).
type ProviderFeeFact struct {
	SourceID     string
	AssessmentID string
	Category     string
	Currency     string
	FeeMinor     *int64 // nil: UNKNOWN (an unknown fee is never 0)
	Basis        string // QUOTED, ESTIMATED, PROVIDER_REPORTED, PROVIDER_CONFIRMED, UNKNOWN
}

// ProviderFeeFacts returns, per source id, the provider processing fee assessed for DONATION_PAYMENT sources
// (PAYMENT_PROCESSING_FEE) or PAYOUT sources (PAYOUT_PROCESSING_FEE). Sources without an assessment are absent.
func (s *Service) ProviderFeeFacts(ctx context.Context, sourceType string, sourceIDs []string) (map[string]ProviderFeeFact, error) {
	cat := CatProcessing
	switch sourceType {
	case SrcDonationPayment:
	case SrcPayout:
		cat = CatPayout
	default:
		return map[string]ProviderFeeFact{}, nil
	}
	valid := make([]string, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		if ids.Valid(id) {
			valid = append(valid, id)
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT source_id::text, id::text, category, currency, fee_minor, basis FROM app.fee_assessments
		WHERE source_type = $1 AND category = $2 AND source_id = ANY ($3::uuid[])`, sourceType, cat, valid)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProviderFeeFact, error) {
		var f ProviderFeeFact
		err := r.Scan(&f.SourceID, &f.AssessmentID, &f.Category, &f.Currency, &f.FeeMinor, &f.Basis)
		return f, err
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]ProviderFeeFact, len(list))
	for _, f := range list {
		out[f.SourceID] = f
	}
	return out, nil
}
