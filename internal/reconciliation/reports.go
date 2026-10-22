package reconciliation

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/ledger"
	"github.com/Fatifizo/fundzim/internal/payouts"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Read models and reports (docs/stage-17/operations.md). Every figure says what it is: all reconciliation evidence in
// Stage 17 is TEST (nature TEST_ONLY); production capabilities are BLOCKED; a source nobody imported from is NEVER, not
// "fresh". Currencies are never summed together. Reports are bounded and every export is audited (step-up by route).

// Nature of every Stage 17 figure.
const NatureTestOnly = "TEST_ONLY"

// LedgerReads is what the ledger view needs (implemented by *ledger.Service).
type LedgerReads interface {
	LatestIntegrity(ctx context.Context) ([]ledger.Check, error)
	TrialBalance(ctx context.Context, currency, periodID string) (ledger.TrialBalance, error)
	ReconExceptions(ctx context.Context, status, typ string, limit int) ([]ledger.ReconException, error)
}

// FeeReads is what the fee view needs (implemented by *fees.Service).
type FeeReads interface {
	LatestRun(ctx context.Context) (*fees.ReconRun, error)
}

// PayoutReads is what the payout view needs (implemented by *payouts.Service).
type PayoutReads interface {
	ReconQueue(ctx context.Context, limit int) ([]payouts.ReconQueueItem, error)
}

// Count is a labelled figure.
type Count struct {
	Key    string `json:"key"`
	Value  int64  `json:"value"`
	Nature string `json:"nature"`
}

// SourceHealth is one source's freshness.
type SourceHealth struct {
	Code          string     `json:"code"`
	Kind          string     `json:"source_kind"`
	AuthorityRank int        `json:"authority_rank"`
	Environment   string     `json:"environment"`
	LastReceived  *time.Time `json:"last_received_at"`
	LastParsed    *time.Time `json:"last_parsed_at"`
	LastRun       *time.Time `json:"last_completed_run_at"`
	OpenLines     int64      `json:"open_lines"`
	Freshness     string     `json:"freshness"` // NEVER, FRESH, STALE, UNKNOWN (no active rule set to judge)
}

// WorkerHealth is one job's heartbeat.
type WorkerHealth struct {
	Job                 string     `json:"job_kind"`
	LastStarted         *time.Time `json:"last_started_at"`
	LastSucceeded       *time.Time `json:"last_succeeded_at"`
	LastFailed          *time.Time `json:"last_failed_at"`
	LastError           *string    `json:"last_error_code"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	Status              string     `json:"status"` // HEALTHY, DEGRADED, UNKNOWN
}

// Overview is GET /admin/reconciliation/overview.
type Overview struct {
	Environment     string            `json:"environment"`
	Nature          string            `json:"nature"`
	Production      map[string]string `json:"production"`
	RuleSet         *RuleSetRecord    `json:"active_rule_set"`
	RuleSetStatus   string            `json:"rule_set_status"`
	Sources         []SourceHealth    `json:"sources"`
	Imports         []Count           `json:"imports"`
	Lines           []Count           `json:"lines"`
	Exceptions      []Count           `json:"exceptions_open_by_severity"`
	ExceptionTypes  []Count           `json:"exceptions_open_by_type"`
	OldestOpenHours *int64            `json:"oldest_open_exception_hours"`
	PendingApproval int64             `json:"resolutions_pending"`
	Batches         []Count           `json:"settlement_batches"`
	UnknownOutcomes int64             `json:"unknown_outcomes_open"`
	Workers         []WorkerHealth    `json:"workers"`
	RecentRuns      []Run             `json:"recent_runs"`
	GeneratedAt     time.Time         `json:"generated_at"`
}

func (s *Service) counts(ctx context.Context, sql string, args ...any) ([]Count, error) {
	rows, err := s.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Count, error) {
		c := Count{Nature: NatureTestOnly}
		err := r.Scan(&c.Key, &c.Value)
		return c, err
	})
}

// Overview is the reconciliation dashboard's read model.
func (s *Service) Overview(ctx context.Context) (Overview, error) {
	o := Overview{Environment: "TEST", Nature: NatureTestOnly, GeneratedAt: s.now(), Production: map[string]string{
		"imports": "BLOCKED", "settlement_posting": "BLOCKED", "adjustments": "BLOCKED_OUTSIDE_TEST_LEDGER",
		"settlement_evidence": "NOT_AVAILABLE: no provider is selected (ADR-040)"}}
	rs, err := s.RuleSets(ctx)
	if err != nil {
		return o, err
	}
	o.RuleSetStatus = "NOT_CONFIGURED"
	for i := range rs {
		if rs[i].Status == "ACTIVE" {
			o.RuleSet, o.RuleSetStatus = &rs[i], "ACTIVE"
		}
	}
	active, _, err := s.ActiveRuleSet(ctx)
	if err != nil {
		return o, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT s.code, s.source_kind, s.authority_rank, s.environment,
		(SELECT max(received_at) FROM recon.imports i WHERE i.source_id = s.id),
		(SELECT max(parsed_at) FROM recon.imports i WHERE i.source_id = s.id AND i.status IN ('PARSED','SUPERSEDED')),
		(SELECT max(finished_at) FROM recon.runs r WHERE r.source_id = s.id AND r.status = 'COMPLETED'),
		(SELECT count(*) FROM recon.items x WHERE x.source_id = s.id AND x.match_status IN ('UNMATCHED','EXCEPTION'))
		FROM recon.sources s ORDER BY s.authority_rank, s.code`)
	if err != nil {
		return o, err
	}
	o.Sources, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (SourceHealth, error) {
		var x SourceHealth
		err := r.Scan(&x.Code, &x.Kind, &x.AuthorityRank, &x.Environment, &x.LastReceived, &x.LastParsed, &x.LastRun, &x.OpenLines)
		switch {
		case x.LastParsed == nil:
			x.Freshness = "NEVER"
		case active.ID == "":
			x.Freshness = "UNKNOWN"
		case s.now().Sub(*x.LastParsed) > time.Duration(active.StaleAfterHours)*time.Hour:
			x.Freshness = "STALE"
		default:
			x.Freshness = "FRESH"
		}
		return x, err
	})
	if err != nil {
		return o, err
	}
	if o.Imports, err = s.counts(ctx, `SELECT status, count(*) FROM recon.imports GROUP BY 1 ORDER BY 1`); err != nil {
		return o, err
	}
	if o.Lines, err = s.counts(ctx, `SELECT currency || ':' || match_status, count(*) FROM recon.items GROUP BY 1 ORDER BY 1`); err != nil {
		return o, err
	}
	if o.Exceptions, err = s.counts(ctx, `SELECT severity, count(*) FROM recon.exceptions WHERE status NOT IN ('RESOLVED','CLOSED') GROUP BY 1
		ORDER BY CASE severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 ELSE 3 END`); err != nil {
		return o, err
	}
	if o.ExceptionTypes, err = s.counts(ctx, `SELECT exception_type, count(*) FROM recon.exceptions WHERE status NOT IN ('RESOLVED','CLOSED')
		GROUP BY 1 ORDER BY 2 DESC, 1`); err != nil {
		return o, err
	}
	if o.Batches, err = s.counts(ctx, `SELECT currency || ':' || status, count(*) FROM recon.settlement_batches GROUP BY 1 ORDER BY 1`); err != nil {
		return o, err
	}
	var oldest *time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT min(detected_at), (SELECT count(*) FROM recon.resolutions WHERE status IN ('PENDING_REVIEW','APPROVED')),
		count(*) FILTER (WHERE exception_type IN ('UNKNOWN_PAYMENT_OUTCOME','UNKNOWN_PAYOUT_OUTCOME'))
		FROM recon.exceptions WHERE status NOT IN ('RESOLVED','CLOSED')`).Scan(&oldest, &o.PendingApproval, &o.UnknownOutcomes); err != nil {
		return o, err
	}
	if oldest != nil {
		h := int64(s.now().Sub(*oldest).Hours())
		o.OldestOpenHours = &h
	}
	if o.Workers, err = s.Workers(ctx); err != nil {
		return o, err
	}
	o.RecentRuns, err = s.Runs(ctx, "", "", 10)
	return o, err
}

// Workers returns the job heartbeats (a job that never ran is UNKNOWN, never HEALTHY).
func (s *Service) Workers(ctx context.Context) ([]WorkerHealth, error) {
	known := map[string]WorkerHealth{}
	rows, err := s.Pool.Query(ctx, `SELECT job_kind, last_started_at, last_succeeded_at, last_failed_at, last_error_code, consecutive_failures
		FROM recon.worker_heartbeats`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (WorkerHealth, error) {
		var w WorkerHealth
		err := r.Scan(&w.Job, &w.LastStarted, &w.LastSucceeded, &w.LastFailed, &w.LastError, &w.ConsecutiveFailures)
		return w, err
	})
	if err != nil {
		return nil, err
	}
	for _, w := range list {
		known[w.Job] = w
	}
	out := make([]WorkerHealth, 0, 3)
	for _, job := range []string{JobParse, JobMatch, JobAgeing} {
		w, ok := known[job]
		if !ok {
			w = WorkerHealth{Job: job}
		}
		switch {
		case w.LastSucceeded == nil:
			w.Status = "UNKNOWN"
		case w.ConsecutiveFailures > 0 || s.now().Sub(*w.LastSucceeded) > 30*time.Minute:
			w.Status = "DEGRADED"
		default:
			w.Status = "HEALTHY"
		}
		out = append(out, w)
	}
	return out, nil
}

// RecordJob records a job's outcome (heartbeat).
func (s *Service) RecordJob(ctx context.Context, job string, started time.Time, jobErr error) {
	code := ""
	if jobErr != nil {
		code = "JOB_ERROR"
		var e *errs.Error
		if errors.As(jobErr, &e) && e.Code != "" {
			code = e.Code
		}
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO recon.worker_heartbeats AS h (job_kind, last_started_at, last_succeeded_at, last_failed_at, last_error_code,
		consecutive_failures, runs_total) VALUES ($1, $2, CASE WHEN $3 = '' THEN $4::timestamptz END, CASE WHEN $3 <> '' THEN $4::timestamptz END,
		nullif($3, ''), CASE WHEN $3 = '' THEN 0 ELSE 1 END, 1)
		ON CONFLICT (job_kind) DO UPDATE SET last_started_at = EXCLUDED.last_started_at,
		  last_succeeded_at = coalesce(EXCLUDED.last_succeeded_at, h.last_succeeded_at),
		  last_failed_at = coalesce(EXCLUDED.last_failed_at, h.last_failed_at),
		  last_error_code = CASE WHEN $3 = '' THEN h.last_error_code ELSE EXCLUDED.last_error_code END,
		  consecutive_failures = CASE WHEN $3 = '' THEN 0 ELSE h.consecutive_failures + 1 END, runs_total = h.runs_total + 1`,
		job, started, code, s.now())
	if err != nil {
		s.Logger.WarnContext(ctx, "reconciliation: heartbeat not recorded", "job", job, "err", err)
	}
}

// ---- settlement batches ---------------------------------------------------------------------------------------------------

// SettlementBatch is a settlement batch as staff see it.
type SettlementBatch struct {
	ID             string     `json:"id"`
	ImportID       string     `json:"import_id"`
	SourceCode     string     `json:"source_code"`
	ProviderCode   string     `json:"provider_code"`
	Currency       string     `json:"currency"`
	BatchRef       string     `json:"batch_ref"`
	SettlementDate string     `json:"settlement_date"`
	GrossCredits   string     `json:"gross_credits_minor"`
	GrossDebits    string     `json:"gross_debits_minor"`
	Fees           string     `json:"fees_minor"`
	Net            string     `json:"net_minor"`
	ExpectedNet    *string    `json:"expected_net_minor"`
	DeclaredLines  *int       `json:"declared_lines"`
	Lines          int        `json:"lines"`
	LinesSettled   int        `json:"lines_settled"`
	Status         string     `json:"status"`
	LastRunID      *string    `json:"last_run_id"`
	CreatedAt      time.Time  `json:"created_at"`
	Label          string     `json:"label"`
	Items          []ItemView `json:"items,omitempty"`
}

const batchCols = `b.id::text, b.import_id::text, s.code, b.provider_code, b.currency, b.batch_ref, to_char(b.settlement_date, 'YYYY-MM-DD'),
	b.gross_credits_minor::text, b.gross_debits_minor::text, b.fees_minor::text, b.net_minor::text, b.expected_net_minor::text, b.declared_lines,
	(SELECT count(*) FROM recon.items x WHERE x.settlement_batch_id = b.id AND x.item_kind <> 'FEE_REMITTANCE'),
	(SELECT count(*) FROM recon.items x WHERE x.settlement_batch_id = b.id AND x.item_kind <> 'FEE_REMITTANCE' AND x.match_status IN ('MATCHED','IGNORED')),
	b.status, b.last_run_id::text, b.created_at`

func scanBatch(r pgx.Row) (SettlementBatch, error) {
	var x SettlementBatch
	err := r.Scan(&x.ID, &x.ImportID, &x.SourceCode, &x.ProviderCode, &x.Currency, &x.BatchRef, &x.SettlementDate, &x.GrossCredits, &x.GrossDebits,
		&x.Fees, &x.Net, &x.ExpectedNet, &x.DeclaredLines, &x.Lines, &x.LinesSettled, &x.Status, &x.LastRunID, &x.CreatedAt)
	x.Label = NatureTestOnly
	return x, err
}

// Settlements lists settlement batches, newest first.
func (s *Service) Settlements(ctx context.Context, status, currency string, limit int) ([]SettlementBatch, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+batchCols+` FROM recon.settlement_batches b JOIN recon.sources s ON s.id = b.source_id
		WHERE ($1 = '' OR b.status = $1) AND ($2 = '' OR b.currency = $2) ORDER BY b.settlement_date DESC, b.created_at DESC LIMIT $3`, status, currency, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (SettlementBatch, error) { return scanBatch(r) })
}

// Settlement returns one batch with its lines.
func (s *Service) Settlement(ctx context.Context, id string) (SettlementBatch, error) {
	if !ids.Valid(id) {
		return SettlementBatch{}, ErrNotFound
	}
	x, err := scanBatch(s.Pool.QueryRow(ctx, `SELECT `+batchCols+` FROM recon.settlement_batches b JOIN recon.sources s ON s.id = b.source_id WHERE b.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return SettlementBatch{}, ErrNotFound
	}
	if err != nil {
		return x, err
	}
	x.Items, err = s.Items(ctx, ItemFilter{BatchID: id, Limit: 500})
	return x, err
}

// ---- payout, ledger and fee views ---------------------------------------------------------------------------------------

// PayoutView is GET /admin/reconciliation/payouts.
type PayoutView struct {
	Nature            string                   `json:"nature"`
	Queue             []payouts.ReconQueueItem `json:"queue"`
	Unknown           int                      `json:"unknown"`
	RetryHalted       int                      `json:"retry_halted"`
	Matches           []Count                  `json:"matches_by_ledger_status"`
	Exceptions        []Exception              `json:"exceptions"`
	ResubmissionRules []string                 `json:"resubmission_rules"`
}

// Payouts is the payout reconciliation read model.
func (s *Service) PayoutsView(ctx context.Context) (PayoutView, error) {
	v := PayoutView{Nature: NatureTestOnly, ResubmissionRules: []string{
		"An UNKNOWN payout is never resubmitted; only a status query, a webhook or approved statement evidence resolves it.",
		"Same-reference retries after DEFINITELY_NOT_SENT or RATE_LIMITED are bounded by PAYOUTS_SUBMIT_RETRY_BUDGET; when spent, SUBMISSION_RETRIES_EXHAUSTED halts submission.",
		"Reconciliation never releases or consumes a reservation: the payout's own transitions do."}}
	if s.PayoutReads != nil {
		q, err := s.PayoutReads.ReconQueue(ctx, 200)
		if err != nil {
			return v, err
		}
		v.Queue = q
		for _, x := range q {
			if x.Status == "UNKNOWN" {
				v.Unknown++
			}
			if x.RetryHalted {
				v.RetryHalted++
			}
		}
	}
	var err error
	if v.Matches, err = s.counts(ctx, `SELECT ledger_status, count(*) FROM recon.matches WHERE status = 'ACTIVE' AND subject_type = 'PAYOUT' GROUP BY 1 ORDER BY 1`); err != nil {
		return v, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+excCols+` FROM recon.exceptions e JOIN recon.sources s ON s.id = e.source_id
		WHERE e.status NOT IN ('CLOSED') AND (e.subject_type = 'PAYOUT' OR e.exception_type IN ('MISSING_PAYOUT','UNKNOWN_PAYOUT_OUTCOME','PAYOUT_MISMATCH'))
		ORDER BY e.detected_at LIMIT 200`)
	if err != nil {
		return v, err
	}
	v.Exceptions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (Exception, error) { return s.scanException(r) })
	return v, err
}

// LedgerView is GET /admin/reconciliation/ledger.
type LedgerView struct {
	Nature              string                  `json:"nature"`
	SettlementPosting   string                  `json:"settlement_posting"`
	Integrity           []ledger.Check          `json:"integrity"`
	TrialBalance        []TrialBalanceLine      `json:"trial_balance"`
	LedgerExceptions    []ledger.ReconException `json:"ledger_exceptions_open"`
	MatchLedgerStatus   []Count                 `json:"matches_by_ledger_status"`
	MissingJournalCases []Exception             `json:"missing_ledger_entries"`
}

// TrialBalanceLine is one currency's totals (never summed across currencies).
type TrialBalanceLine struct {
	Currency   string `json:"currency"`
	Debit      string `json:"total_debit_minor"`
	Credit     string `json:"total_credit_minor"`
	Difference string `json:"difference_minor"`
	Balanced   bool   `json:"balanced"`
}

// LedgerViewModel combines the ledger's own integrity and reconciliation with reconciliation's journal checks.
func (s *Service) LedgerViewModel(ctx context.Context) (LedgerView, error) {
	v := LedgerView{Nature: NatureTestOnly, SettlementPosting: "BLOCKED: no settlement postings in Stage 17 (ADR-049 §8; accounting review KI-S10-02)"}
	if s.LedgerReads != nil {
		var err error
		if v.Integrity, err = s.LedgerReads.LatestIntegrity(ctx); err != nil {
			return v, err
		}
		tb, err := s.LedgerReads.TrialBalance(ctx, "", "")
		if err != nil {
			return v, err
		}
		for _, c := range tb.Currencies {
			v.TrialBalance = append(v.TrialBalance, TrialBalanceLine{Currency: c.Currency, Debit: c.TotalDebit, Credit: c.TotalCredit,
				Difference: c.Difference, Balanced: c.Difference == "0"})
		}
		if v.LedgerExceptions, err = s.LedgerReads.ReconExceptions(ctx, "", "", 100); err != nil {
			return v, err
		}
		open := v.LedgerExceptions[:0]
		for _, e := range v.LedgerExceptions {
			if e.Status != "RESOLVED" {
				open = append(open, e)
			}
		}
		v.LedgerExceptions = open
	}
	var err error
	if v.MatchLedgerStatus, err = s.counts(ctx, `SELECT subject_type || ':' || ledger_status, count(*) FROM recon.matches WHERE status = 'ACTIVE' GROUP BY 1 ORDER BY 1`); err != nil {
		return v, err
	}
	v.MissingJournalCases, err = s.Exceptions(ctx, ExceptionFilter{Type: "MISSING_LEDGER_ENTRY", OpenOnly: true, Limit: 100})
	return v, err
}

// FeeView is GET /admin/reconciliation/fees.
type FeeView struct {
	Nature         string         `json:"nature"`
	FeeEvidence    string         `json:"provider_fee_evidence"`
	MatchFeeStatus []Count        `json:"matches_by_fee_status"`
	Mismatches     []Exception    `json:"fee_mismatches"`
	FeeRecompute   *fees.ReconRun `json:"fee_recompute_latest_run"`
	RevenueStatus  string         `json:"revenue_recognition"`
}

// FeesView compares statement fees with FundZim's assessments and shows the fee module's own recompute.
func (s *Service) FeesView(ctx context.Context) (FeeView, error) {
	v := FeeView{Nature: NatureTestOnly, FeeEvidence: "SYNTHETIC or UNVERIFIED until a provider is selected (ADR-040, PCR-016)",
		RevenueStatus: "BLOCKED: no revenue is recognised from reconciliation (production fees disabled; accounting review KI-S10-02)"}
	var err error
	if v.MatchFeeStatus, err = s.counts(ctx, `SELECT subject_type || ':' || fee_status, count(*) FROM recon.matches WHERE status = 'ACTIVE' GROUP BY 1 ORDER BY 1`); err != nil {
		return v, err
	}
	if v.Mismatches, err = s.Exceptions(ctx, ExceptionFilter{Type: "FEE_MISMATCH", Limit: 100}); err != nil {
		return v, err
	}
	if s.FeeReads != nil {
		v.FeeRecompute, err = s.FeeReads.LatestRun(ctx)
	}
	return v, err
}

// ---- reports ----------------------------------------------------------------------------------------------------------------

// ReportKinds lists the reports.
var ReportKinds = []string{"daily_summary", "settlement_summary", "unmatched_lines", "payout_exceptions", "fee_discrepancies", "ledger_integrity",
	"exception_ageing"}

// MaxReportRows bounds every report.
const MaxReportRows = 5000

// Report is a bounded tabular report.
type Report struct {
	Kind        string     `json:"kind"`
	Nature      string     `json:"nature"`
	From        time.Time  `json:"from"`
	To          time.Time  `json:"to"`
	GeneratedAt time.Time  `json:"generated_at"`
	Columns     []string   `json:"columns"`
	Rows        [][]string `json:"rows"`
	Truncated   bool       `json:"truncated"`
}

// BuildReport runs one report over [from, to) (default: the last 30 days).
func (s *Service) BuildReport(ctx context.Context, kind string, from, to time.Time) (Report, error) {
	if to.IsZero() {
		to = s.now()
	}
	if from.IsZero() {
		from = to.Add(-30 * 24 * time.Hour)
	}
	if !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return Report{}, httpx.Validation(errs.Detail{Field: "from", Code: "OUT_OF_RANGE"})
	}
	r := Report{Kind: kind, Nature: NatureTestOnly, From: from, To: to, GeneratedAt: s.now()}
	var sql string
	switch kind {
	case "daily_summary":
		r.Columns = []string{"day", "source", "currency", "runs", "lines_considered", "lines_matched", "exceptions_opened", "exceptions_closed"}
		sql = `SELECT to_char(date_trunc('day', r.finished_at), 'YYYY-MM-DD'), s.code, r.currency, count(*)::text, sum(r.items_considered)::text,
			sum(r.items_matched)::text, sum(r.exceptions_opened)::text, sum(r.exceptions_closed)::text FROM recon.runs r JOIN recon.sources s ON s.id = r.source_id
			WHERE r.status = 'COMPLETED' AND r.finished_at >= $1 AND r.finished_at < $2 GROUP BY 1, 2, 3 ORDER BY 1, 2, 3`
	case "settlement_summary":
		r.Columns = []string{"settlement_date", "batch_ref", "currency", "status", "gross_credits_minor", "gross_debits_minor", "fees_minor", "net_minor",
			"expected_net_minor"}
		sql = `SELECT to_char(settlement_date, 'YYYY-MM-DD'), batch_ref, currency, status, gross_credits_minor::text, gross_debits_minor::text,
			fees_minor::text, net_minor::text, coalesce(expected_net_minor::text, '') FROM recon.settlement_batches
			WHERE created_at >= $1 AND created_at < $2 ORDER BY settlement_date, batch_ref`
	case "unmatched_lines":
		r.Columns = []string{"booked_at", "source", "line_id", "kind", "direction", "status", "amount_minor", "currency", "match_status"}
		sql = `SELECT to_char(x.booked_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), s.code, x.external_line_id, x.item_kind, x.direction, x.line_status,
			x.amount_minor::text, x.currency, x.match_status FROM recon.items x JOIN recon.sources s ON s.id = x.source_id
			WHERE x.match_status IN ('UNMATCHED','EXCEPTION','CONFLICT') AND x.booked_at >= $1 AND x.booked_at < $2 ORDER BY x.booked_at, x.id`
	case "payout_exceptions":
		r.Columns = []string{"detected_at", "exception_type", "severity", "status", "payout_id", "expected_minor", "actual_minor", "currency"}
		sql = `SELECT to_char(detected_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), exception_type, severity, status, coalesce(subject_id::text, ''),
			coalesce(expected_amount_minor::text, ''), coalesce(actual_amount_minor::text, ''), currency FROM recon.exceptions
			WHERE (subject_type = 'PAYOUT' OR exception_type IN ('MISSING_PAYOUT','UNKNOWN_PAYOUT_OUTCOME','PAYOUT_MISMATCH'))
			AND detected_at >= $1 AND detected_at < $2 ORDER BY detected_at, id`
	case "fee_discrepancies":
		r.Columns = []string{"detected_at", "status", "subject_type", "subject_id", "expected_fee_minor", "reported_fee_minor", "currency", "fee_basis"}
		sql = `SELECT to_char(detected_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), status, coalesce(subject_type, ''), coalesce(subject_id::text, ''),
			coalesce(expected_amount_minor::text, ''), coalesce(actual_amount_minor::text, ''), currency, coalesce(detail->>'fee_basis', '')
			FROM recon.exceptions WHERE exception_type = 'FEE_MISMATCH' AND detected_at >= $1 AND detected_at < $2 ORDER BY detected_at, id`
	case "ledger_integrity":
		r.Columns = []string{"check", "status", "checked", "violations"}
		if s.LedgerReads == nil {
			return r, nil
		}
		checks, err := s.LedgerReads.LatestIntegrity(ctx)
		if err != nil {
			return r, err
		}
		for _, c := range checks {
			r.Rows = append(r.Rows, []string{c.Code, c.Status, fmt.Sprint(c.Checked), fmt.Sprint(c.Violations)})
		}
		return r, nil
	case "exception_ageing":
		r.Columns = []string{"exception_type", "severity", "open", "under_24h", "1_to_7_days", "over_7_days"}
		sql = `SELECT exception_type, severity, count(*)::text, count(*) FILTER (WHERE detected_at > $2::timestamptz - interval '24 hours')::text,
			count(*) FILTER (WHERE detected_at <= $2::timestamptz - interval '24 hours' AND detected_at > $2::timestamptz - interval '7 days')::text,
			count(*) FILTER (WHERE detected_at <= $2::timestamptz - interval '7 days')::text FROM recon.exceptions
			WHERE status NOT IN ('RESOLVED','CLOSED') AND detected_at >= $1 GROUP BY 1, 2 ORDER BY 1, 2`
	default:
		return r, httpx.Validation(errs.Detail{Field: "kind", Code: "INVALID_VALUE"})
	}
	rows, err := s.Pool.Query(ctx, sql+fmt.Sprintf(" LIMIT %d", MaxReportRows+1), from, to)
	if err != nil {
		return r, err
	}
	defer rows.Close()
	for rows.Next() {
		vals := make([]string, len(r.Columns))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return r, err
		}
		r.Rows = append(r.Rows, vals)
	}
	if len(r.Rows) > MaxReportRows {
		r.Rows, r.Truncated = r.Rows[:MaxReportRows], true
	}
	if r.Rows == nil {
		r.Rows = [][]string{}
	}
	return r, rows.Err()
}

// CSVSafe neutralises a cell a spreadsheet could evaluate as a formula (CSV injection): a leading =, +, -, @, tab or
// carriage return gets a leading apostrophe. Negative numbers therefore export as text ('-5); amounts stay integers.
func CSVSafe(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}

// ExportCSV renders a report as CSV (every cell neutralised) and audits the export.
func (s *Service) ExportCSV(ctx context.Context, staffID string, r Report) ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	head := append([]string{"nature"}, r.Columns...)
	_ = w.Write(head)
	for _, row := range r.Rows {
		out := make([]string, 0, len(row)+1)
		out = append(out, r.Nature)
		for _, c := range row {
			out = append(out, CSVSafe(c))
		}
		_ = w.Write(out)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.report.exported", ActorType: "staff", ActorID: staffID,
			TargetType: "reconciliation_report", TargetID: ids.New(), Metadata: map[string]any{"kind": r.Kind, "rows": len(r.Rows),
				"truncated": r.Truncated, "from": r.From, "to": r.To, "format": "csv", "nature": r.Nature}})
	})
	return b.Bytes(), err
}

// reportFilename is a safe attachment name.
func reportFilename(kind string, at time.Time) string {
	return "fundzim-reconciliation-" + strings.ReplaceAll(kind, "_", "-") + "-" + at.Format("20060102T150405Z") + "-TEST.csv"
}
