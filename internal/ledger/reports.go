package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/platform/errs"
)

// Reports are read-only views over posted records (ledger pool, SELECT only). Lists are keyset-paginated
// (newest first, cursor "<RFC3339Nano>|<uuid>", at most 100 rows) and every filter is a bound parameter.

// ErrCursor is an invalid list cursor.
var ErrCursor = errs.New(errs.Invalid, errs.CodeValidationFailed, "Invalid cursor.")

func parseCursor(c string) (*time.Time, string, error) {
	if c == "" {
		return nil, "ffffffff-ffff-ffff-ffff-ffffffffffff", nil
	}
	ts, id, ok := strings.Cut(c, "|")
	t, err := time.Parse(time.RFC3339Nano, ts)
	if !ok || err != nil || !uuidRE.MatchString(id) {
		return nil, "", ErrCursor
	}
	return &t, id, nil
}

func nextCursor(n, limit int, at time.Time, id string) *string {
	if n < limit {
		return nil
	}
	c := at.UTC().Format(time.RFC3339Nano) + "|" + id
	return &c
}

// PageLimit clamps a page size (default 50, max 100).
func PageLimit(n int) int {
	if n <= 0 || n > 100 {
		return 50
	}
	return n
}

// JournalSummary is one row of the journal register.
type JournalSummary struct {
	ID             string    `json:"id"`
	PostingRule    string    `json:"posting_rule"`
	RuleVersion    int       `json:"posting_rule_version"`
	Environment    string    `json:"environment"`
	Currency       string    `json:"currency"`
	TotalMinor     string    `json:"total_minor"`
	LineCount      int       `json:"line_count"`
	Description    string    `json:"description"`
	SourceSystem   string    `json:"source_system"`
	SourceType     string    `json:"source_type"`
	SourceID       string    `json:"source_id"`
	EvidenceClass  string    `json:"evidence_class"`
	IdempotencyKey string    `json:"idempotency_key"`
	OccurredAt     time.Time `json:"occurred_at"`
	PostedAt       time.Time `json:"posted_at"`
	PeriodCode     string    `json:"period_code"`
	PriorPeriod    bool      `json:"is_prior_period"`
	Reverses       *string   `json:"reverses_transaction_id"`
	ReversedBy     *string   `json:"reversed_by_transaction_id"`
	AdjustmentID   *string   `json:"adjustment_id"`
	TestOnly       bool      `json:"test_only"`
}

const journalSelect = `SELECT t.id, t.posting_rule, t.posting_rule_version, t.environment, t.currency,
	(SELECT coalesce(sum(e.amount_minor), 0) FROM ledger.ledger_entries e WHERE e.transaction_id = t.id AND e.direction = 'DEBIT'),
	(SELECT count(*) FROM ledger.ledger_entries e WHERE e.transaction_id = t.id),
	t.description, t.source_system, t.source_type, t.source_id::text, t.evidence_class, t.idempotency_key, t.occurred_at, t.posted_at,
	p.code, t.is_prior_period, t.reverses_transaction_id::text,
	(SELECT r.id::text FROM ledger.ledger_transactions r WHERE r.reverses_transaction_id = t.id), t.adjustment_id::text
	FROM ledger.ledger_transactions t JOIN ledger.ledger_periods p ON p.id = t.accounting_period_id`

func scanJournal(r pgx.Row) (JournalSummary, error) {
	var j JournalSummary
	var total int64
	err := r.Scan(&j.ID, &j.PostingRule, &j.RuleVersion, &j.Environment, &j.Currency, &total, &j.LineCount, &j.Description, &j.SourceSystem,
		&j.SourceType, &j.SourceID, &j.EvidenceClass, &j.IdempotencyKey, &j.OccurredAt, &j.PostedAt, &j.PeriodCode, &j.PriorPeriod,
		&j.Reverses, &j.ReversedBy, &j.AdjustmentID)
	j.TotalMinor = itoa(total)
	j.TestOnly = j.Environment == EnvTest
	return j, err
}

// JournalFilter filters the register.
type JournalFilter struct {
	Rule, SourceType, SourceID, SourceSystem, Currency, AccountID string
}

// Journals lists journals newest first.
func (s *Service) Journals(ctx context.Context, f JournalFilter, cursor string, limit int) ([]JournalSummary, *string, error) {
	limit = PageLimit(limit)
	at, id, err := parseCursor(cursor)
	if err != nil {
		return nil, nil, err
	}
	if f.SourceID != "" && !uuidRE.MatchString(f.SourceID) || f.AccountID != "" && !uuidRE.MatchString(f.AccountID) {
		return []JournalSummary{}, nil, nil
	}
	rows, err := s.Pool.Query(ctx, journalSelect+`
		WHERE ($1 = '' OR t.posting_rule = $1) AND ($2 = '' OR t.source_type = $2) AND ($3 = '' OR t.source_id = nullif($3, '')::uuid)
		  AND ($4 = '' OR t.source_system = $4) AND ($5 = '' OR t.currency = $5)
		  AND ($6 = '' OR EXISTS (SELECT 1 FROM ledger.ledger_entries e WHERE e.transaction_id = t.id AND e.account_id = nullif($6, '')::uuid))
		  AND ($7::timestamptz IS NULL OR (t.posted_at, t.id) < ($7, $8::uuid))
		ORDER BY t.posted_at DESC, t.id DESC LIMIT $9`, f.Rule, f.SourceType, f.SourceID, f.SourceSystem, f.Currency, f.AccountID, at, id, limit)
	if err != nil {
		return nil, nil, err
	}
	items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (JournalSummary, error) { return scanJournal(r) })
	if err != nil {
		return nil, nil, err
	}
	var next *string
	if len(items) > 0 {
		last := items[len(items)-1]
		next = nextCursor(len(items), limit, last.PostedAt, last.ID)
	}
	return items, next, nil
}

// JournalLine is one line of a journal.
type JournalLine struct {
	LineNo      int    `json:"line_no"`
	AccountID   string `json:"account_id"`
	AccountCode string `json:"account_code"`
	Class       string `json:"account_class"`
	Direction   string `json:"direction"`
	DebitMinor  string `json:"debit_minor"`
	CreditMinor string `json:"credit_minor"`
	Currency    string `json:"currency"`
}

// SourceReference is a recorded source event and the ledger's decision.
type SourceReference struct {
	ID            string    `json:"id"`
	SourceSystem  string    `json:"source_system"`
	SourceType    string    `json:"source_type"`
	SourceID      string    `json:"source_id"`
	SourceEventID string    `json:"source_event_id"`
	PostingRule   string    `json:"posting_rule"`
	EvidenceClass string    `json:"evidence_class"`
	Decision      string    `json:"decision"`
	ReasonCode    string    `json:"reason_code"`
	TransactionID *string   `json:"transaction_id"`
	AmountMinor   *string   `json:"amount_minor"`
	Currency      *string   `json:"currency"`
	ObservedAt    time.Time `json:"observed_at"`
}

// JournalDetail is a journal with lines, metadata, source trace and reversal history.
type JournalDetail struct {
	JournalSummary
	Reason        *string           `json:"reason"`
	CreatedBy     string            `json:"created_by"`
	CorrelationID *string           `json:"correlation_id"`
	ProviderCode  *string           `json:"provider_code"`
	ProviderRef   *string           `json:"provider_reference"`
	Metadata      json.RawMessage   `json:"metadata"`
	Lines         []JournalLine     `json:"lines"`
	Sources       []SourceReference `json:"source_references"`
	Balanced      bool              `json:"balanced"`
}

// Journal returns one journal.
func (s *Service) Journal(ctx context.Context, id string) (JournalDetail, error) {
	if !uuidRE.MatchString(id) {
		return JournalDetail{}, ErrNotFound
	}
	sum, err := scanJournal(s.Pool.QueryRow(ctx, journalSelect+` WHERE t.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return JournalDetail{}, ErrNotFound
	}
	if err != nil {
		return JournalDetail{}, err
	}
	d := JournalDetail{JournalSummary: sum, Lines: []JournalLine{}, Sources: []SourceReference{}}
	var byType, byID string
	if err := s.Pool.QueryRow(ctx, `SELECT reason, created_by_type, created_by_id, correlation_id, provider_code, provider_reference, metadata
		FROM ledger.ledger_transactions WHERE id = $1`, id).Scan(&d.Reason, &byType, &byID, &d.CorrelationID, &d.ProviderCode, &d.ProviderRef,
		&d.Metadata); err != nil {
		return d, err
	}
	d.CreatedBy = byType + ":" + byID
	rows, err := s.Pool.Query(ctx, `SELECT e.line_no, a.id, a.code, a.account_class, e.direction, e.amount_minor, e.currency
		FROM ledger.ledger_entries e JOIN ledger.ledger_accounts a ON a.id = e.account_id WHERE e.transaction_id = $1 ORDER BY e.line_no`, id)
	if err != nil {
		return d, err
	}
	var dr, cr int64
	for rows.Next() {
		var l JournalLine
		var amt int64
		if err := rows.Scan(&l.LineNo, &l.AccountID, &l.AccountCode, &l.Class, &l.Direction, &amt, &l.Currency); err != nil {
			rows.Close()
			return d, err
		}
		l.DebitMinor, l.CreditMinor = "0", "0"
		if l.Direction == Debit {
			l.DebitMinor, dr = itoa(amt), dr+amt
		} else {
			l.CreditMinor, cr = itoa(amt), cr+amt
		}
		d.Lines = append(d.Lines, l)
	}
	rows.Close()
	d.Balanced = dr == cr && len(d.Lines) >= 2
	d.Sources, err = s.sourceRefs(ctx, `WHERE transaction_id = $1`, id)
	return d, err
}

func (s *Service) sourceRefs(ctx context.Context, where string, args ...any) ([]SourceReference, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, source_system, source_type, source_id::text, source_event_id, posting_rule, evidence_class, decision,
		reason_code, transaction_id::text, amount_minor::text, currency, observed_at FROM ledger.ledger_source_references `+where+`
		ORDER BY created_at DESC, id DESC LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SourceReference, error) {
		var x SourceReference
		err := r.Scan(&x.ID, &x.SourceSystem, &x.SourceType, &x.SourceID, &x.SourceEventID, &x.PostingRule, &x.EvidenceClass, &x.Decision,
			&x.ReasonCode, &x.TransactionID, &x.AmountMinor, &x.Currency, &x.ObservedAt)
		return x, err
	})
}

// SourceReferences lists source references (optionally by decision), newest first.
func (s *Service) SourceReferences(ctx context.Context, decision string) ([]SourceReference, error) {
	return s.sourceRefs(ctx, `WHERE ($1 = '' OR decision = $1)`, decision)
}

// AccountFilter filters the account list.
type AccountFilter struct{ Class, OwnerType, Status, Currency string }

// Accounts lists accounts (with exact current balances), by code.
func (s *Service) Accounts(ctx context.Context, f AccountFilter, limit int) ([]AccountBalance, error) {
	rows, err := s.Pool.Query(ctx, accountSelect+` WHERE ($1 = '' OR a.account_class = $1) AND ($2 = '' OR a.owner_type = $2)
		AND ($3 = '' OR a.status = $3) AND ($4 = '' OR a.currency = $4) ORDER BY a.type, a.code, a.currency LIMIT $5`,
		f.Class, f.OwnerType, f.Status, f.Currency, PageLimit(limit))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AccountBalance, error) { return scanAccountBalance(r) })
}

// Account returns one account with its balance.
func (s *Service) Account(ctx context.Context, id string) (AccountBalance, error) {
	if !uuidRE.MatchString(id) {
		return AccountBalance{}, ErrNotFound
	}
	a, err := scanAccountBalance(s.Pool.QueryRow(ctx, accountSelect+` WHERE a.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountBalance{}, ErrNotFound
	}
	return a, err
}

// Overview summarises the ledger for the dashboard (counts and per-currency totals; never a withdrawable balance).
type Overview struct {
	Environment              string                 `json:"environment"`
	ProductionPostingEnabled bool                   `json:"production_posting_enabled"`
	Journals                 int64                  `json:"journals"`
	Accounts                 int64                  `json:"accounts"`
	OpenExceptions           int64                  `json:"open_exceptions"`
	PendingAdjustments       int64                  `json:"pending_adjustments"`
	TrialBalance             []TrialBalanceCurrency `json:"trial_balance"`
	Integrity                []Check                `json:"integrity"`
	OpenPeriods              []Period               `json:"open_periods"`
	SyntheticEnabled         bool                   `json:"synthetic_fixtures_enabled"`
	Notice                   string                 `json:"notice"`
}

// Overview builds the dashboard summary.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	env, err := s.Environment(ctx)
	if err != nil {
		return Overview{}, err
	}
	o := Overview{Environment: env, ProductionPostingEnabled: ProductionPostingEnabled, SyntheticEnabled: s.SyntheticEnabled && env == EnvTest,
		Notice: "TEST ledger: synthetic TEST_ONLY fixtures only. No real donation, refund, settlement or payout is recorded; no balance is withdrawable."}
	if env == EnvProduction {
		o.Notice = "PRODUCTION ledger."
	}
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ledger.ledger_transactions), (SELECT count(*) FROM ledger.ledger_accounts),
		(SELECT count(*) FROM ledger.ledger_reconciliation_references WHERE status <> 'RESOLVED'),
		(SELECT count(*) FROM ledger.ledger_adjustments WHERE status = 'PENDING_APPROVAL')`).
		Scan(&o.Journals, &o.Accounts, &o.OpenExceptions, &o.PendingAdjustments); err != nil {
		return o, err
	}
	tb, err := s.TrialBalance(ctx, "", "")
	if err != nil {
		return o, err
	}
	for _, c := range tb.Currencies {
		c.Rows = []TrialBalanceRow{} // totals only on the overview
		o.TrialBalance = append(o.TrialBalance, c)
	}
	if o.TrialBalance == nil {
		o.TrialBalance = []TrialBalanceCurrency{}
	}
	if o.Integrity, err = s.LatestIntegrity(ctx); err != nil {
		return o, err
	}
	ps, err := s.Periods(ctx)
	if err != nil {
		return o, err
	}
	o.OpenPeriods = []Period{}
	for _, p := range ps {
		if p.Status != "CLOSED" {
			o.OpenPeriods = append(o.OpenPeriods, p)
		}
	}
	return o, nil
}
