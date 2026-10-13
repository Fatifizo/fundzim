package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Balances are derived from posted entries only. ledger_balances is a projection: SYNC rows (obligations and
// recoverables) are maintained in the posting transaction under a row lock; DEFERRED rows (platform-wide hot
// accounts) by the fold job. ledger.v_balances_current is exact for both. Nothing updates a balance directly.

// Fold folds DEFERRED entries into the projection (job; idempotent; one runner at a time is enough).
func (s *Service) Fold(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT ledger.fold_deferred_balances()`).Scan(&n)
	return n, err
}

// AccountBalance is an account with its exact current balance (normal-balance terms) and totals.
type AccountBalance struct {
	ID              string    `json:"id"`
	Code            string    `json:"code"`
	AccountClass    string    `json:"account_class"`
	Type            string    `json:"type"`
	NormalBalance   string    `json:"normal_balance"`
	Kind            string    `json:"kind"`
	Currency        string    `json:"currency"`
	OwnerType       string    `json:"owner_type"`
	OwnerID         *string   `json:"owner_id"`
	Environment     string    `json:"environment"`
	Status          string    `json:"status"`
	StatusReason    *string   `json:"status_reason"`
	NonNegative     bool      `json:"non_negative"`
	ProjectionMode  string    `json:"projection_mode"`
	BalanceMinor    string    `json:"balance_minor"`
	DebitTotalMinor string    `json:"debit_total_minor"`
	CreditTotal     string    `json:"credit_total_minor"`
	EntryCount      int64     `json:"entry_count"`
	CreatedAt       time.Time `json:"created_at"`
}

const accountSelect = `SELECT a.id, a.code, a.account_class, a.type, a.normal_balance, a.kind, a.currency, a.owner_type, a.owner_id::text,
	a.environment, a.status, a.status_reason, a.non_negative, a.projection_mode, c.balance_minor,
	t.dr, t.cr, t.n, a.created_at
	FROM ledger.ledger_accounts a
	JOIN ledger.v_balances_current c ON c.account_id = a.id
	JOIN LATERAL (SELECT coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'DEBIT'), 0) AS dr,
	                     coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'CREDIT'), 0) AS cr, count(*) AS n
	                FROM ledger.ledger_entries e WHERE e.account_id = a.id) t ON true`

func scanAccountBalance(r pgx.Row) (AccountBalance, error) {
	var a AccountBalance
	var bal, dr, cr int64
	err := r.Scan(&a.ID, &a.Code, &a.AccountClass, &a.Type, &a.NormalBalance, &a.Kind, &a.Currency, &a.OwnerType, &a.OwnerID,
		&a.Environment, &a.Status, &a.StatusReason, &a.NonNegative, &a.ProjectionMode, &bal, &dr, &cr, &a.EntryCount, &a.CreatedAt)
	a.BalanceMinor, a.DebitTotalMinor, a.CreditTotal = itoa(bal), itoa(dr), itoa(cr)
	return a, err
}

// TrialBalanceRow is one account in a trial balance.
type TrialBalanceRow struct {
	AccountID    string `json:"account_id"`
	Code         string `json:"code"`
	AccountClass string `json:"account_class"`
	Type         string `json:"type"`
	DebitMinor   string `json:"debit_minor"`
	CreditMinor  string `json:"credit_minor"`
}

// TrialBalanceCurrency is the per-currency section (never summed across currencies).
type TrialBalanceCurrency struct {
	Currency    string            `json:"currency"`
	TotalDebit  string            `json:"total_debit_minor"`
	TotalCredit string            `json:"total_credit_minor"`
	Difference  string            `json:"difference_minor"`
	Balanced    bool              `json:"balanced"`
	Rows        []TrialBalanceRow `json:"rows"`
}

// TrialBalance is a trial balance (all time, or one period) at a point in time.
type TrialBalance struct {
	Environment string                 `json:"environment"`
	PeriodID    *string                `json:"period_id"`
	GeneratedAt time.Time              `json:"generated_at"`
	Currencies  []TrialBalanceCurrency `json:"currencies"`
}

func (t TrialBalance) summary() map[string]any {
	out := map[string]any{}
	for _, c := range t.Currencies {
		out[c.Currency] = map[string]any{"debit": c.TotalDebit, "credit": c.TotalCredit, "difference": c.Difference, "accounts": len(c.Rows)}
	}
	return out
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// trialBalance aggregates posted entries per account (debit and credit totals), optionally for one currency and one
// period. Each account's row shows its debit- or credit-side net; the totals must be equal per currency (L9).
func trialBalance(ctx context.Context, q queryer, currency, periodID string) (TrialBalance, error) {
	tb := TrialBalance{Currencies: []TrialBalanceCurrency{}}
	if periodID != "" {
		tb.PeriodID = &periodID
	}
	rows, err := q.Query(ctx, `SELECT a.currency, a.id, a.code, a.account_class, a.type,
			coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'DEBIT'), 0),
			coalesce(sum(e.amount_minor) FILTER (WHERE e.direction = 'CREDIT'), 0)
		FROM ledger.ledger_entries e
		JOIN ledger.ledger_accounts a ON a.id = e.account_id
		JOIN ledger.ledger_transactions t ON t.id = e.transaction_id
		WHERE ($1 = '' OR e.currency = $1) AND ($2 = '' OR t.accounting_period_id = nullif($2, '')::uuid)
		GROUP BY a.currency, a.id, a.code, a.account_class, a.type
		ORDER BY a.currency, a.type, a.code`, currency, periodID)
	if err != nil {
		return tb, err
	}
	defer rows.Close()
	idx := map[string]int{}
	totals := map[string][2]int64{}
	for rows.Next() {
		var ccy, id, code, class, typ string
		var dr, cr int64
		if err := rows.Scan(&ccy, &id, &code, &class, &typ, &dr, &cr); err != nil {
			return tb, err
		}
		i, ok := idx[ccy]
		if !ok {
			tb.Currencies = append(tb.Currencies, TrialBalanceCurrency{Currency: ccy, Rows: []TrialBalanceRow{}})
			i = len(tb.Currencies) - 1
			idx[ccy] = i
		}
		row := TrialBalanceRow{AccountID: id, Code: code, AccountClass: class, Type: typ, DebitMinor: "0", CreditMinor: "0"}
		net := dr - cr
		if net >= 0 {
			row.DebitMinor = itoa(net)
		} else {
			row.CreditMinor = itoa(-net)
		}
		tb.Currencies[i].Rows = append(tb.Currencies[i].Rows, row)
		t := totals[ccy]
		if net >= 0 {
			t[0] += net
		} else {
			t[1] -= net
		}
		totals[ccy] = t
	}
	if err := rows.Err(); err != nil {
		return tb, err
	}
	for i := range tb.Currencies {
		t := totals[tb.Currencies[i].Currency]
		tb.Currencies[i].TotalDebit, tb.Currencies[i].TotalCredit = itoa(t[0]), itoa(t[1])
		tb.Currencies[i].Difference = itoa(t[0] - t[1])
		tb.Currencies[i].Balanced = t[0] == t[1]
	}
	return tb, nil
}

// TrialBalance builds a trial balance in a REPEATABLE READ snapshot.
func (s *Service) TrialBalance(ctx context.Context, currency, periodID string) (TrialBalance, error) {
	env, err := s.Environment(ctx)
	if err != nil {
		return TrialBalance{}, err
	}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return TrialBalance{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tb, err := trialBalance(ctx, tx, currency, periodID)
	tb.Environment, tb.GeneratedAt = env, s.now()
	return tb, err
}

func sha256JSON(v any) [32]byte {
	b, _ := json.Marshal(v)
	return sha256.Sum256(b)
}

func i64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}
