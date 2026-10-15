package ledger

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Read API for the fees module (Stage 12, ADR-044). Fees never read ledger tables: they ask the ledger for the class
// amounts of the journal behind a deterministic key (reconciliation) and for fee account movements (reporting).

// JournalAmounts is a posted journal with its net amount per account class and direction.
type JournalAmounts struct {
	TransactionID      string
	Rule               string
	Currency           string
	Environment        string
	SourceSystem       string
	EvidenceClass      string
	FeePolicyVersionID *string
	ReversedBy         *string
	// Amounts[class][direction] sums the journal's entries on accounts of that class.
	Amounts map[string]map[string]int64
}

// Amount returns the journal's total on one class and direction (0 if none).
func (j JournalAmounts) Amount(class, direction string) int64 { return j.Amounts[class][direction] }

// JournalAmountsByKey returns the journal posted under a key (ok=false if none).
func (s *Service) JournalAmountsByKey(ctx context.Context, key string) (JournalAmounts, bool, error) {
	var j JournalAmounts
	err := s.Pool.QueryRow(ctx, `SELECT t.id::text, t.posting_rule, t.currency, t.environment, t.source_system, t.evidence_class,
		t.fee_schedule_version_id::text, (SELECT r.id::text FROM ledger.ledger_transactions r WHERE r.reverses_transaction_id = t.id)
		FROM ledger.ledger_transactions t WHERE t.idempotency_key = $1`, key).
		Scan(&j.TransactionID, &j.Rule, &j.Currency, &j.Environment, &j.SourceSystem, &j.EvidenceClass, &j.FeePolicyVersionID, &j.ReversedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return JournalAmounts{}, false, nil
	}
	if err != nil {
		return JournalAmounts{}, false, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.account_class, e.direction, sum(e.amount_minor) FROM ledger.ledger_entries e
		JOIN ledger.ledger_accounts a ON a.id = e.account_id WHERE e.transaction_id = $1 GROUP BY 1, 2`, j.TransactionID)
	if err != nil {
		return JournalAmounts{}, false, err
	}
	defer rows.Close()
	j.Amounts = map[string]map[string]int64{}
	for rows.Next() {
		var class, dir string
		var amt int64
		if err := rows.Scan(&class, &dir, &amt); err != nil {
			return JournalAmounts{}, false, err
		}
		if j.Amounts[class] == nil {
			j.Amounts[class] = map[string]int64{}
		}
		j.Amounts[class][dir] = amt
	}
	return j, true, rows.Err()
}

// FeeMovement is the movement of one fee-related account class in one currency, month and source system:
// credits and debits are reported separately (never netted across currencies).
type FeeMovement struct {
	Currency     string
	Month        time.Time // first day of the month, UTC
	SourceSystem string
	Class        string
	Environment  string
	DebitMinor   int64
	CreditMinor  int64
	Journals     int64
}

// FeeMovements returns the movements of revenue:platform_fees and expense:psp_processing_fees between from and to
// (posted_at), grouped by currency, month, source system and class.
func (s *Service) FeeMovements(ctx context.Context, from, to time.Time) ([]FeeMovement, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.currency, date_trunc('month', t.posted_at AT TIME ZONE 'UTC'), t.source_system, a.account_class,
		t.environment, coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'DEBIT'), 0),
		coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'CREDIT'), 0), count(DISTINCT t.id)
		FROM ledger.ledger_transactions t
		JOIN ledger.ledger_entries e ON e.transaction_id = t.id
		JOIN ledger.ledger_accounts a ON a.id = e.account_id
		WHERE a.account_class IN ($1, $2) AND t.posted_at >= $3 AND t.posted_at < $4
		GROUP BY 1, 2, 3, 4, 5 ORDER BY 1, 2, 3, 4`, ClassPlatformFees, ClassPSPProcessingFees, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FeeMovement{}
	for rows.Next() {
		var m FeeMovement
		if err := rows.Scan(&m.Currency, &m.Month, &m.SourceSystem, &m.Class, &m.Environment, &m.DebitMinor, &m.CreditMinor, &m.Journals); err != nil {
			return nil, err
		}
		m.Month = m.Month.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// SyntheticAllowed reports whether TEST_ONLY synthetic postings are allowed (TEST ledger and fixtures enabled).
func (s *Service) SyntheticAllowed(ctx context.Context) (bool, error) {
	if !s.SyntheticEnabled {
		return false, nil
	}
	env, err := s.Environment(ctx)
	return err == nil && env == EnvTest, err
}
