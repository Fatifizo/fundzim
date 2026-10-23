package reconciliation

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/fees"
	"github.com/Fatifizo/fundzim/internal/platform/errs"
	"github.com/Fatifizo/fundzim/internal/platform/httpx"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// Runs (docs/stage-17/reconciliation-architecture.md §5). A run decides the open lines of one source and currency inside
// a window: QUEUED → RUNNING → COMPLETED / FAILED. At most one run per (source, currency) is active (unique index). The
// matcher is pure; this file gathers its input through the owning modules' services, persists every decision in one
// transaction, and records a hash of the decisions. A run never posts to the ledger and never changes a payment, payout
// or reservation.

// Bounds.
const (
	persistChunk    = 5_000
	MaxRunItems     = 200_000
	refBatch        = 5_000
	staleRunTimeout = 10 * time.Minute
)

// Run is a run as staff see it.
type Run struct {
	ID               string     `json:"id"`
	SourceCode       string     `json:"source_code"`
	SourceKind       string     `json:"source_kind"`
	Currency         string     `json:"currency"`
	WindowStart      time.Time  `json:"window_start"`
	WindowEnd        time.Time  `json:"window_end"`
	Kind             string     `json:"run_kind"`
	Status           string     `json:"status"`
	RuleSetID        *string    `json:"rule_set_id"`
	RuleSetVersion   *int       `json:"rule_set_version"`
	MatcherVersion   string     `json:"matcher_version"`
	ReplayOfRunID    *string    `json:"replay_of_run_id"`
	Reason           *string    `json:"reason"`
	TriggeredByType  string     `json:"triggered_by_type"`
	TriggeredBy      *string    `json:"triggered_by"`
	ItemsConsidered  int        `json:"items_considered"`
	ItemsMatched     int        `json:"items_matched"`
	ExceptionsOpened int        `json:"exceptions_opened"`
	ExceptionsSeen   int        `json:"exceptions_seen"`
	ExceptionsClosed int        `json:"exceptions_closed"`
	DecisionsSHA256  *string    `json:"decisions_sha256"`
	FailureCode      *string    `json:"failure_code"`
	QueuedAt         time.Time  `json:"queued_at"`
	StartedAt        *time.Time `json:"started_at"`
	FinishedAt       *time.Time `json:"finished_at"`
	DurationMS       *int64     `json:"duration_ms"`
	Label            string     `json:"label"`
}

const runCols = `r.id::text, s.code, s.source_kind, r.currency, r.window_start, r.window_end, r.run_kind, r.status, r.rule_set_id::text, r.rule_set_version,
	r.matcher_version, r.replay_of_run_id::text, r.reason, r.triggered_by_type, r.triggered_by::text, r.items_considered, r.items_matched,
	r.exceptions_opened, r.exceptions_seen, r.exceptions_closed, r.decisions_sha256, r.failure_code, r.queued_at, r.started_at, r.finished_at`

func scanRun(r pgx.Row) (Run, error) {
	var x Run
	var sum []byte
	err := r.Scan(&x.ID, &x.SourceCode, &x.SourceKind, &x.Currency, &x.WindowStart, &x.WindowEnd, &x.Kind, &x.Status, &x.RuleSetID,
		&x.RuleSetVersion, &x.MatcherVersion, &x.ReplayOfRunID, &x.Reason, &x.TriggeredByType, &x.TriggeredBy, &x.ItemsConsidered,
		&x.ItemsMatched, &x.ExceptionsOpened, &x.ExceptionsSeen, &x.ExceptionsClosed, &sum, &x.FailureCode, &x.QueuedAt, &x.StartedAt, &x.FinishedAt)
	if err != nil {
		return x, err
	}
	if len(sum) == 32 {
		h := hex.EncodeToString(sum)
		x.DecisionsSHA256 = &h
	}
	if x.StartedAt != nil && x.FinishedAt != nil {
		d := x.FinishedAt.Sub(*x.StartedAt).Milliseconds()
		x.DurationMS = &d
	}
	x.Label = "TEST_ONLY"
	return x, nil
}

// RunByID returns one run.
func (s *Service) RunByID(ctx context.Context, id string) (Run, error) {
	if !ids.Valid(id) {
		return Run{}, ErrNotFound
	}
	x, err := scanRun(s.Pool.QueryRow(ctx, `SELECT `+runCols+` FROM recon.runs r JOIN recon.sources s ON s.id = r.source_id WHERE r.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return x, err
}

// Runs lists runs, newest first.
func (s *Service) Runs(ctx context.Context, source, status string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+runCols+` FROM recon.runs r JOIN recon.sources s ON s.id = r.source_id
		WHERE ($1 = '' OR s.code = $1) AND ($2 = '' OR r.status = $2) ORDER BY r.queued_at DESC, r.id DESC LIMIT $3`, source, status, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) { return scanRun(r) })
}

// RunInput is POST /admin/reconciliation/runs.
type RunInput struct {
	Source        string `json:"source"`
	Currency      string `json:"currency"`
	Reason        string `json:"reason"`
	ReplayOfRunID string `json:"replay_of_run_id"`
}

// RequestRun queues an on-demand run or a replay of an earlier run's scope (staff; reason required, audited). The worker
// executes it. A replay is a new run: matches are unique per active line and exceptions deduplicate, so it never
// duplicates anything and never posts.
func (s *Service) RequestRun(ctx context.Context, staffID string, in RunInput) (Run, error) {
	reason, err := cleanNote(in.Reason, 5, 500)
	if err != nil {
		return Run{}, httpx.Validation(errs.Detail{Field: "reason", Code: "INVALID_LENGTH"})
	}
	if _, ok, err := s.ActiveRuleSet(ctx); err != nil {
		return Run{}, err
	} else if !ok {
		return Run{}, ErrNoRuleSet
	}
	kind, replay := "ON_DEMAND", ""
	var src Source
	var ws, we time.Time
	if in.ReplayOfRunID != "" {
		prev, err := s.RunByID(ctx, in.ReplayOfRunID)
		if err != nil {
			return Run{}, err
		}
		if prev.Status != "COMPLETED" && prev.Status != "FAILED" {
			return Run{}, ErrState
		}
		if src, err = s.sourceByCode(ctx, prev.SourceCode); err != nil {
			return Run{}, err
		}
		in.Currency, ws, we, kind, replay = prev.Currency, prev.WindowStart, prev.WindowEnd, "REPLAY", prev.ID
	} else {
		if src, err = s.sourceByCode(ctx, in.Source); err != nil {
			return Run{}, err
		}
		if !SupportedCurrencies[in.Currency] {
			return Run{}, httpx.Validation(errs.Detail{Field: "currency", Code: "UNSUPPORTED_CURRENCY"})
		}
		var ok bool
		ws, we, ok, err = s.coverageWindow(ctx, src.ID, in.Currency)
		if err != nil {
			return Run{}, err
		}
		if !ok {
			return Run{}, errs.New(errs.Conflict, "NOTHING_TO_RECONCILE", "No parsed statement of this source and currency exists yet.")
		}
	}
	id, err := s.queueRun(ctx, src.ID, in.Currency, ws, we, kind, staffID, replay, reason)
	if err != nil {
		return Run{}, err
	}
	return s.RunByID(ctx, id)
}

func (s *Service) queueRun(ctx context.Context, sourceID, currency string, ws, we time.Time, kind, staffID, replay, reason string) (string, error) {
	id := ids.New()
	byType := "SYSTEM"
	if staffID != "" {
		byType = "STAFF"
	}
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO recon.runs (id, source_id, currency, window_start, window_end, run_kind, status, matcher_version,
			replay_of_run_id, reason, triggered_by_type, triggered_by, queued_at) VALUES ($1, $2, $3, $4, $5, $6, 'QUEUED', $7, $8, $9, $10, $11, $12)`,
			id, sourceID, currency, ws, we, kind, MatcherVersion, nullStr(replay), nullStr(reason), byType, nullStr(staffID), s.now())
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return ErrRunActive
		}
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.run.queued", ActorType: strings.ToLower(byType), ActorID: staffID,
			TargetType: "reconciliation_run", TargetID: id, Reason: reason, Metadata: map[string]any{"run_kind": kind, "currency": currency,
				"replay_of_run_id": replay, "window_start": ws, "window_end": we}})
	})
	return id, err
}

// coverageWindow: the union of the source's parsed statement periods for a currency within the last 93 days of periods.
func (s *Service) coverageWindow(ctx context.Context, sourceID, currency string) (time.Time, time.Time, bool, error) {
	var ws, we *time.Time
	err := s.Pool.QueryRow(ctx, `SELECT min(period_start), max(period_end) FROM recon.imports i WHERE i.source_id = $1 AND i.status = 'PARSED'
		AND (i.currency = $2 OR (i.currency IS NULL AND EXISTS (SELECT 1 FROM recon.items x WHERE x.import_id = i.id AND x.currency = $2)))
		AND i.period_end > (SELECT coalesce(max(period_end), now()) FROM recon.imports WHERE source_id = $1 AND status = 'PARSED') - interval '93 days'`,
		sourceID, currency).Scan(&ws, &we)
	if err != nil || ws == nil || we == nil {
		return time.Time{}, time.Time{}, false, err
	}
	return *ws, *we, true, nil
}

// ScheduleRuns queues a SCHEDULED run for every (source, currency) with open lines or a statement parsed since its last
// completed run. Nothing is queued while no rule set is active (fail closed; health shows it).
func (s *Service) ScheduleRuns(ctx context.Context) (int, error) {
	if _, ok, err := s.ActiveRuleSet(ctx); err != nil || !ok {
		return 0, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT x.source_id::text, x.currency FROM recon.items x JOIN recon.sources s ON s.id = x.source_id
		WHERE s.enabled AND (x.match_status IN ('UNMATCHED','EXCEPTION') OR x.created_at > coalesce((SELECT max(r.finished_at) FROM recon.runs r
		  WHERE r.source_id = x.source_id AND r.currency = x.currency AND r.status = 'COMPLETED'), '-infinity'))
		AND NOT EXISTS (SELECT 1 FROM recon.runs r WHERE r.source_id = x.source_id AND r.currency = x.currency AND r.status IN ('QUEUED','RUNNING'))`)
	if err != nil {
		return 0, err
	}
	type scope struct{ src, ccy string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (scope, error) {
		var x scope
		err := r.Scan(&x.src, &x.ccy)
		return x, err
	})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sc := range list {
		ws, we, ok, err := s.coverageWindow(ctx, sc.src, sc.ccy)
		if err != nil || !ok {
			continue
		}
		if _, err := s.queueRun(ctx, sc.src, sc.ccy, ws, we, "SCHEDULED", "", "", ""); err == nil {
			n++
		} else if !errors.Is(err, ErrRunActive) {
			return n, err
		}
	}
	return n, nil
}

// ExecuteQueued executes queued runs one by one (each claimed with SKIP LOCKED: concurrent workers never share a run).
func (s *Service) ExecuteQueued(ctx context.Context, limit int) (int, error) {
	n := 0
	for i := 0; i < limit; i++ {
		var id string
		err := s.Pool.QueryRow(ctx, `SELECT id::text FROM recon.runs WHERE status = 'QUEUED' ORDER BY queued_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if err := s.ExecuteRun(ctx, id); err != nil {
			s.Logger.WarnContext(ctx, "reconciliation: run failed", "run_id", id, "err", err)
		}
		n++
	}
	return n, nil
}

// ReapStaleRuns fails runs whose worker stopped heart-beating (crash recovery); their scope can run again.
func (s *Service) ReapStaleRuns(ctx context.Context) (int, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE recon.runs SET status = 'FAILED', failure_code = 'WORKER_LOST', finished_at = $1, version = version + 1
		WHERE status = 'RUNNING' AND coalesce(heartbeat_at, started_at) < $2`, s.now(), s.now().Add(-staleRunTimeout))
	return int(tag.RowsAffected()), err
}

func (s *Service) heartbeat(ctx context.Context, runID string) {
	_, _ = s.Pool.Exec(ctx, `UPDATE recon.runs SET heartbeat_at = $2 WHERE id = $1 AND status = 'RUNNING'`, runID, s.now())
}

// ExecuteRun runs one queued run to completion (or FAILED with a code). Safe to call concurrently: the claim is a state
// transition under a row lock.
func (s *Service) ExecuteRun(ctx context.Context, runID string) error {
	rules, ok, err := s.ActiveRuleSet(ctx)
	if err != nil {
		return err
	}
	var claimed bool
	err = s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM recon.runs WHERE id = $1 FOR UPDATE`, runID).Scan(&status); err != nil {
			return err
		}
		if status != "QUEUED" {
			return nil
		}
		if !ok {
			_, err := tx.Exec(ctx, `UPDATE recon.runs SET status = 'FAILED', failure_code = 'NO_ACTIVE_RULE_SET', finished_at = $2, version = version + 1
				WHERE id = $1`, runID, s.now())
			return err
		}
		claimed = true
		_, err := tx.Exec(ctx, `UPDATE recon.runs SET status = 'RUNNING', rule_set_id = $2, rule_set_version = $3, started_at = $4, heartbeat_at = $4,
			version = version + 1 WHERE id = $1`, runID, rules.ID, rules.Version, s.now())
		return err
	})
	if err != nil || !claimed {
		return err
	}
	if err := s.executeRun(ctx, runID, rules); err != nil {
		code := "RUN_ERROR"
		var e *errs.Error
		if errors.As(err, &e) {
			code = e.Code
		}
		// recorded even when the run failed because its context ended (deadline, shutdown): with the caller's context the
		// update failed too and the run stayed RUNNING, blocking its scope until the reaper (Stage 19)
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if _, ferr := s.Pool.Exec(fctx, `UPDATE recon.runs SET status = 'FAILED', failure_code = $2, finished_at = $3, version = version + 1
			WHERE id = $1 AND status = 'RUNNING'`, runID, code, s.now()); ferr != nil {
			return errors.Join(err, ferr)
		}
		return err
	}
	return nil
}

type runScope struct {
	run    Run
	source Source
}

func (s *Service) executeRun(ctx context.Context, runID string, rules RuleSet) error {
	run, err := s.RunByID(ctx, runID)
	if err != nil {
		return err
	}
	src, err := s.sourceByCode(ctx, run.SourceCode)
	if err != nil {
		return err
	}
	items, err := s.openItems(ctx, src.ID, run.Currency, run.WindowStart, run.WindowEnd)
	if err != nil {
		return err
	}
	s.heartbeat(ctx, runID)
	in := MatchInput{SourceID: src.ID, SourceKind: src.Kind, Currency: run.Currency, Rules: rules, Now: s.now(), Items: items}
	periods, err := s.coveredPeriods(ctx, src.ID, run.Currency)
	if err != nil {
		return err
	}
	in.Covered = func(t time.Time) bool {
		for _, p := range periods {
			if !t.Before(p[0]) && t.Before(p[1]) {
				return true
			}
		}
		return false
	}
	if in.AlreadyMatched, err = s.activeSubjects(ctx, src); err != nil {
		return err
	}
	if src.Kind == KindBankStatement {
		if err := s.bankInputs(ctx, &in); err != nil {
			return err
		}
	} else if err := s.providerInputs(ctx, src, run, &in); err != nil {
		return err
	}
	s.heartbeat(ctx, runID)
	res := Match(in)
	return s.persist(ctx, runScope{run, src}, in, res)
}

func (s *Service) openItems(ctx context.Context, sourceID, currency string, ws, we time.Time) ([]Item, error) {
	rows, err := s.Pool.Query(ctx, `SELECT x.id::text, x.source_id::text, x.item_kind, x.direction, x.line_status, x.amount_minor, x.fee_minor, x.currency,
		x.raw_currency_code, coalesce(x.provider_ref, ''), coalesce(x.merchant_ref, ''), coalesce(x.settlement_batch_id::text, ''),
		coalesce(x.batch_ref, ''), coalesce(x.counterparty_ref, ''), x.booked_at
		FROM recon.items x WHERE x.source_id = $1 AND x.currency = $2 AND x.booked_at >= $3 AND x.booked_at < $4
		AND x.match_status IN ('UNMATCHED','EXCEPTION') ORDER BY x.booked_at, x.id LIMIT $5`, sourceID, currency, ws, we, MaxRunItems)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanItem)
}

func scanItem(r pgx.CollectableRow) (Item, error) {
	var x Item
	err := r.Scan(&x.ID, &x.SourceID, &x.Kind, &x.Direction, &x.Status, &x.Amount, &x.Fee, &x.Currency, &x.RawCurrency, &x.ProviderRef,
		&x.MerchantRef, &x.BatchID, &x.BatchRef, &x.CounterpartyRef, &x.BookedAt)
	return x, err
}

func (s *Service) coveredPeriods(ctx context.Context, sourceID, currency string) ([][2]time.Time, error) {
	rows, err := s.Pool.Query(ctx, `SELECT period_start, period_end FROM recon.imports WHERE source_id = $1 AND status = 'PARSED'
		AND (currency IS NULL OR currency = $2)`, sourceID, currency)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]time.Time, error) {
		var p [2]time.Time
		err := r.Scan(&p[0], &p[1])
		return p, err
	})
}

// activeSubjects: subject keys already matched by an active match from a line of this source (duplicate detection).
func (s *Service) activeSubjects(ctx context.Context, src Source) (map[string]string, error) {
	rows, err := s.Pool.Query(ctx, `SELECT m.subject_type, m.subject_id::text, x.item_kind, m.item_id::text FROM recon.matches m
		JOIN recon.items x ON x.id = m.item_id WHERE m.status = 'ACTIVE' AND (x.source_id = $1 OR ($2 AND m.subject_type = 'FEE_REMITTANCE_LINE'))`,
		src.ID, src.Kind == KindBankStatement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var st, sid, kind, item string
		if err := rows.Scan(&st, &sid, &kind, &item); err != nil {
			return nil, err
		}
		out[SubjectKey(st, sid, kind)] = item
	}
	return out, rows.Err()
}

func (s *Service) providerInputs(ctx context.Context, src Source, run Run, in *MatchInput) error {
	provider := *src.ProviderCode
	var payIDs, payRefs, outIDs, outRefs []string
	for _, it := range in.Items {
		isPayout := it.Kind == "PAYOUT" || it.Kind == "PAYOUT_REVERSAL"
		if it.MerchantRef != "" {
			if isPayout {
				outIDs = append(outIDs, strings.ToLower(it.MerchantRef))
			} else {
				payIDs = append(payIDs, strings.ToLower(it.MerchantRef))
			}
		}
		if it.ProviderRef != "" {
			if isPayout {
				outRefs = append(outRefs, it.ProviderRef)
			} else {
				payRefs = append(payRefs, it.ProviderRef)
			}
		}
	}
	horizonEnd := run.WindowEnd
	pays, err := s.paymentFacts(ctx, provider, payIDs, payRefs)
	if err != nil {
		return err
	}
	expPays, err := s.Payments.ReconSucceededInWindow(ctx, provider, run.Currency, run.WindowStart, horizonEnd, MaxRunItems)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range pays {
		seen[p.ID] = true
	}
	for _, p := range expPays {
		in.Expected = append(in.Expected, paymentSubject(p))
		if !seen[p.ID] {
			pays = append(pays, p)
			seen[p.ID] = true
		}
	}
	for _, p := range pays {
		in.Payments = append(in.Payments, paymentSubject(p))
	}
	outs, err := s.payoutFacts(ctx, provider, outIDs, outRefs)
	if err != nil {
		return err
	}
	if src.Kind == KindTransactionReport {
		expOuts, err := s.Payouts.ReconCompletedInWindow(ctx, provider, run.Currency, run.WindowStart, horizonEnd, MaxRunItems)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, p := range outs {
			known[p.ID] = true
		}
		for _, p := range expOuts {
			in.Expected = append(in.Expected, payoutSubject(p))
			if !known[p.ID] {
				outs = append(outs, p)
			}
		}
	}
	for _, p := range outs {
		in.Payouts = append(in.Payouts, payoutSubject(p))
	}
	// journals a matched record must have, and the fees FundZim assessed
	var keys, payoutIDs, paymentIDs []string
	for _, p := range in.Payouts {
		keys, payoutIDs = append(keys, JournalKey(SubjPayout, p.ID)), append(payoutIDs, p.ID)
	}
	for _, p := range in.Payments {
		if !p.Test {
			keys = append(keys, JournalKey(SubjPayment, p.ID))
		}
		paymentIDs = append(paymentIDs, p.ID)
	}
	in.Journals = map[string]Journal{}
	for _, chunk := range chunks(keys, refBatch) {
		js, err := s.Ledger.JournalsByKeys(ctx, chunk)
		if err != nil {
			return err
		}
		for k, j := range js {
			in.Journals[k] = Journal{TransactionID: j.TransactionID, Currency: j.Currency, Amount: j.DebitMinor, Reversed: j.ReversedBy != nil}
		}
	}
	in.Fees = map[string]FeeExpectation{}
	for _, part := range []struct {
		src string
		ids []string
	}{{fees.SrcDonationPayment, paymentIDs}, {fees.SrcPayout, payoutIDs}} {
		for _, chunk := range chunks(part.ids, refBatch) {
			f, err := s.Fees.ProviderFeeFacts(ctx, part.src, chunk)
			if err != nil {
				return err
			}
			for id, x := range f {
				in.Fees[id] = FeeExpectation{Fee: x.FeeMinor, Basis: x.Basis}
			}
		}
	}
	return nil
}

func (s *Service) bankInputs(ctx context.Context, in *MatchInput) error {
	rows, err := s.Pool.Query(ctx, `SELECT x.id::text, x.source_id::text, x.item_kind, x.direction, x.line_status, x.amount_minor, x.fee_minor, x.currency,
		x.raw_currency_code, coalesce(x.provider_ref, ''), coalesce(x.merchant_ref, ''), coalesce(x.settlement_batch_id::text, ''),
		coalesce(x.batch_ref, ''), coalesce(x.counterparty_ref, ''), x.booked_at
		FROM recon.items x JOIN recon.sources s ON s.id = x.source_id WHERE s.source_kind = 'PROVIDER_SETTLEMENT_REPORT'
		AND x.item_kind = 'FEE_REMITTANCE' AND x.match_status NOT IN ('SUPERSEDED','CONFLICT') AND x.currency = $1
		ORDER BY x.booked_at, x.id LIMIT $2`, in.Currency, MaxRunItems)
	if err != nil {
		return err
	}
	if in.Remittances, err = pgx.CollectRows(rows, scanItem); err != nil {
		return err
	}
	var printed []string
	for _, it := range in.Items {
		printed = append(printed, uuidInText.FindAllString(strings.ToLower(it.CounterpartyRef), 4)...)
	}
	in.KnownPaymentIDs = map[string]bool{}
	for _, chunk := range chunks(printed, refBatch) {
		facts, err := s.Payments.ReconFacts(ctx, "", chunk, nil)
		if err != nil {
			return err
		}
		for _, f := range facts {
			in.KnownPaymentIDs[f.ID] = true
		}
	}
	return nil
}

func chunks(list []string, n int) [][]string {
	var out [][]string
	for len(list) > 0 {
		k := min(n, len(list))
		out = append(out, list[:k])
		list = list[k:]
	}
	return out
}

func (s *Service) paymentFacts(ctx context.Context, provider string, idsList, refs []string) ([]paymentFact, error) {
	var out []paymentFact
	seen := map[string]bool{}
	for len(idsList) > 0 || len(refs) > 0 {
		a, b := min(refBatch, len(idsList)), min(refBatch, len(refs))
		facts, err := s.Payments.ReconFacts(ctx, provider, idsList[:a], refs[:b])
		if err != nil {
			return nil, err
		}
		for _, f := range facts {
			if !seen[f.ID] {
				seen[f.ID] = true
				out = append(out, f)
			}
		}
		idsList, refs = idsList[a:], refs[b:]
	}
	return out, nil
}

func (s *Service) payoutFacts(ctx context.Context, provider string, idsList, refs []string) ([]payoutFact, error) {
	var out []payoutFact
	seen := map[string]bool{}
	for len(idsList) > 0 || len(refs) > 0 {
		a, b := min(refBatch, len(idsList)), min(refBatch, len(refs))
		facts, err := s.Payouts.ReconFacts(ctx, provider, idsList[:a], refs[:b])
		if err != nil {
			return nil, err
		}
		for _, f := range facts {
			if !seen[f.ID] {
				seen[f.ID] = true
				out = append(out, f)
			}
		}
		idsList, refs = idsList[a:], refs[b:]
	}
	return out, nil
}

// persist writes a run's decisions in one transaction, evaluates the settlement batches it touched, closes cases whose
// condition cleared, and completes the run.
func (s *Service) persist(ctx context.Context, sc runScope, in MatchInput, res MatchResult) error {
	run := sc.run
	var opened, seen, matched, closed int
	err := s.tx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, `SELECT status FROM recon.runs WHERE id = $1 FOR UPDATE`, run.ID).Scan(&status); err != nil {
			return err
		}
		if status != "RUNNING" {
			return errs.New(errs.Conflict, "RUN_NOT_RUNNING", "the run was reaped while it was executing")
		}
		detected := map[string]bool{}
		open := func(d ExceptionDraft) error {
			detected[d.DedupeKey] = true
			isNew, err := s.openException(ctx, tx, run.ID, d, sc.source.ID, run.Currency)
			if isNew {
				opened++
			} else {
				seen++
			}
			return err
		}
		touchedBatches := map[string]bool{}
		subjects := map[string]bool{}
		items := map[string]bool{}
		for _, it := range in.Items {
			items[it.ID] = true
			if it.BatchID != "" {
				touchedBatches[it.BatchID] = true
			}
		}
		// matches and line statuses are written in batches (one statement per chunk); exceptions one by one (each writes its
		// history, audit event and outbox event)
		var mc matchCols
		var lineIDs, lineStatus, remittances []string
		for _, d := range res.Decisions {
			if d.Match != nil {
				m := d.Match
				expl, _ := json.Marshal(m.Explanation)
				mc.add(ids.New(), d.ItemID, m, string(expl))
				subjects[m.SubjectType+":"+m.SubjectID] = true
				if m.SubjectType == SubjRemittance {
					remittances = append(remittances, m.SubjectID)
				}
			}
			for _, e := range d.Exceptions {
				if e.SubjectID != "" {
					subjects[e.SubjectType+":"+e.SubjectID] = true
				}
				if err := open(e); err != nil {
					return err
				}
			}
			lineIDs, lineStatus = append(lineIDs, d.ItemID), append(lineStatus, d.Status)
		}
		for from := 0; from < len(mc.id); from += persistChunk {
			to := min(from+persistChunk, len(mc.id))
			tag, err := tx.Exec(ctx, `INSERT INTO recon.matches (id, run_id, item_id, currency, subject_type, subject_id, confidence, rule_set_id,
				rule_set_version, matcher_version, rule_code, matched_amount_minor, ledger_status, ledger_transaction_id, fee_status, explanation)
				SELECT v.id, $1, v.item, $2, v.st, v.sid, v.conf, $3, $4, $5, v.rule, v.amount, v.ledger, v.ltx, v.fee, v.expl
				FROM unnest($6::uuid[], $7::uuid[], $8::text[], $9::uuid[], $10::text[], $11::text[], $12::bigint[], $13::text[], $14::uuid[], $15::text[],
				  $16::jsonb[]) AS v(id, item, st, sid, conf, rule, amount, ledger, ltx, fee, expl)
				ON CONFLICT (item_id) WHERE status = 'ACTIVE' DO NOTHING`,
				run.ID, run.Currency, in.Rules.ID, in.Rules.Version, MatcherVersion, mc.id[from:to], mc.item[from:to], mc.st[from:to], mc.sid[from:to],
				mc.conf[from:to], mc.rule[from:to], mc.amount[from:to], mc.ledger[from:to], mc.ltx[from:to], mc.fee[from:to], mc.expl[from:to])
			if err != nil {
				return err
			}
			matched += int(tag.RowsAffected())
		}
		for from := 0; from < len(lineIDs); from += persistChunk {
			to := min(from+persistChunk, len(lineIDs))
			if _, err := tx.Exec(ctx, `UPDATE recon.items x SET match_status = v.s FROM unnest($1::uuid[], $2::text[]) AS v(id, s)
				WHERE x.id = v.id AND x.match_status IN ('UNMATCHED','EXCEPTION') AND x.match_status <> v.s`, lineIDs[from:to], lineStatus[from:to]); err != nil {
				return err
			}
		}
		if len(remittances) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE recon.items SET match_status = 'MATCHED' WHERE id = ANY ($1::uuid[]) AND match_status IN ('UNMATCHED','EXCEPTION')`,
				remittances); err != nil {
				return err
			}
		}
		for _, e := range res.Subjects {
			if err := open(e); err != nil {
				return err
			}
		}
		for _, sj := range in.Expected {
			subjects[sj.Type+":"+sj.ID] = true
		}
		// settlement batches touched by this run, and the source's unsettled batches in the window (their lines may all have
		// been settled since: matched by an earlier run, explained or manually matched)
		brows, err := tx.Query(ctx, `SELECT DISTINCT b.id::text FROM recon.settlement_batches b JOIN recon.items x ON x.settlement_batch_id = b.id
			WHERE b.source_id = $1 AND b.currency = $2 AND b.status IN ('RECEIVED','DISCREPANCY') AND x.booked_at >= $3 AND x.booked_at < $4`,
			sc.source.ID, run.Currency, run.WindowStart, run.WindowEnd)
		if err != nil {
			return err
		}
		more, err := pgx.CollectRows(brows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		for _, b := range more {
			touchedBatches[b] = true
		}
		bids := make([]string, 0, len(touchedBatches))
		for b := range touchedBatches {
			bids = append(bids, b)
		}
		sort.Strings(bids)
		for _, bid := range bids {
			d, err := s.evaluateBatch(ctx, tx, bid)
			if err != nil {
				return err
			}
			if d.Exception != nil {
				if err := open(*d.Exception); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE recon.settlement_batches SET status = $2, expected_net_minor = $3, last_run_id = $4, version = version + 1
				WHERE id = $1 AND status <> 'SUPERSEDED' AND (status <> $2 OR expected_net_minor IS DISTINCT FROM $3)`, bid, d.Status, d.ExpectedNet, run.ID); err != nil {
				return err
			}
		}
		// cases whose condition this run looked at and no longer found
		rows, err := tx.Query(ctx, `SELECT id::text, dedupe_key, coalesce(item_id::text, ''), coalesce(subject_type, ''), coalesce(subject_id::text, '')
			FROM recon.exceptions WHERE source_id = $1 AND currency = $2 AND status IN ('OPEN','ASSIGNED','INVESTIGATING','AWAITING_EVIDENCE','REOPENED')
			AND exception_type = ANY ($3::text[])`, sc.source.ID, run.Currency, closableList())
		if err != nil {
			return err
		}
		type cand struct{ id, key, item, st, sid string }
		cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (cand, error) {
			var c cand
			err := r.Scan(&c.id, &c.key, &c.item, &c.st, &c.sid)
			return c, err
		})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if detected[c.key] {
				continue
			}
			if (c.item != "" && items[c.item]) || (c.sid != "" && subjects[c.st+":"+c.sid]) {
				if err := s.closeCleared(ctx, tx, c.id, run.ID); err != nil {
					return err
				}
				closed++
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE recon.runs SET status = 'COMPLETED', items_considered = $2, items_matched = $3, exceptions_opened = $4,
			exceptions_seen = $5, exceptions_closed = $6, decisions_sha256 = $7, finished_at = $8, heartbeat_at = $8, version = version + 1 WHERE id = $1`,
			run.ID, len(in.Items), matched, opened, seen, closed, res.Hash[:], s.now()); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{Action: "reconciliation.run.completed", ActorType: "system", TargetType: "reconciliation_run",
			TargetID: run.ID, Metadata: map[string]any{"source": sc.source.Code, "currency": run.Currency, "items_considered": len(in.Items),
				"items_matched": matched, "exceptions_opened": opened, "exceptions_closed": closed, "rule_set_version": in.Rules.Version,
				"decisions_sha256": hex.EncodeToString(res.Hash[:]), "environment": "TEST"}})
	})
	return err
}

func closableList() []string {
	out := make([]string, 0, len(ClosableTypes))
	for k := range ClosableTypes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// evaluateBatch compares a settlement batch's header with FundZim's expectation of its lines (as they stand in tx).
func (s *Service) evaluateBatch(ctx context.Context, tx pgx.Tx, batchID string) (BatchDecision, error) {
	var headerNet int64
	var sourceID string
	if err := tx.QueryRow(ctx, `SELECT net_minor, source_id::text FROM recon.settlement_batches WHERE id = $1`, batchID).Scan(&headerNet, &sourceID); err != nil {
		return BatchDecision{}, err
	}
	rows, err := tx.Query(ctx, `SELECT x.direction, x.amount_minor, x.fee_minor, x.match_status,
		(SELECT m.explanation->>'expected_fee_minor' FROM recon.matches m WHERE m.item_id = x.id AND m.status = 'ACTIVE' AND m.fee_status = 'MISMATCH')
		FROM recon.items x WHERE x.settlement_batch_id = $1 AND x.item_kind <> 'FEE_REMITTANCE' AND x.match_status NOT IN ('SUPERSEDED','CONFLICT')`, batchID)
	if err != nil {
		return BatchDecision{}, err
	}
	lines, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (BatchLine, error) {
		var l BatchLine
		var st string
		var exp *string
		if err := r.Scan(&l.Direction, &l.Amount, &l.Fee, &st, &exp); err != nil {
			return l, err
		}
		l.Settled = st == "MATCHED" || st == "IGNORED"
		if exp != nil {
			if v, err := strconv.ParseInt(*exp, 10, 64); err == nil {
				l.ExpectedFee = &v
			}
		}
		return l, nil
	})
	if err != nil {
		return BatchDecision{}, err
	}
	net, complete := BatchExpectation(lines)
	d := BatchDecision{BatchID: batchID, ExpectedNet: net, Status: "MATCHED"}
	if !complete {
		d.Status = "DISCREPANCY"
	}
	d = CompareBatch(sourceID, d, headerNet)
	return d, nil
}

// ---- lines ---------------------------------------------------------------------------------------------------------------

// ItemView is a statement line as staff see it (with its active match).
type ItemView struct {
	ID              string     `json:"id"`
	ImportID        string     `json:"import_id"`
	ExternalLineID  string     `json:"external_line_id"`
	Revision        int        `json:"revision"`
	LineNumber      int        `json:"line_number"`
	Kind            string     `json:"item_kind"`
	Direction       string     `json:"direction"`
	Status          string     `json:"line_status"`
	AmountMinor     string     `json:"amount_minor"`
	FeeMinor        *string    `json:"fee_minor"`
	NetMinor        *string    `json:"net_minor"`
	Currency        string     `json:"currency"`
	RawCurrencyCode string     `json:"raw_currency_code"`
	ProviderRef     *string    `json:"provider_ref"`
	MerchantRef     *string    `json:"merchant_ref"`
	BatchRef        *string    `json:"batch_ref"`
	SettlementBatch *string    `json:"settlement_batch_id"`
	CounterpartyRef *string    `json:"counterparty_ref"`
	ValueDate       *string    `json:"value_date"`
	BookedAt        time.Time  `json:"booked_at"`
	RawLineSHA256   string     `json:"raw_line_sha256"`
	RawLineLocator  string     `json:"raw_line_locator"`
	MatchStatus     string     `json:"match_status"`
	Match           *MatchView `json:"match"`
}

// MatchView is an active match with its explanation.
type MatchView struct {
	ID                  string         `json:"id"`
	RunID               *string        `json:"run_id"`
	SubjectType         string         `json:"subject_type"`
	SubjectID           string         `json:"subject_id"`
	Confidence          string         `json:"confidence"`
	RuleCode            string         `json:"rule_code"`
	RuleSetVersion      *int           `json:"rule_set_version"`
	MatcherVersion      string         `json:"matcher_version"`
	MatchedAmountMinor  string         `json:"matched_amount_minor"`
	LedgerStatus        string         `json:"ledger_status"`
	LedgerTransactionID *string        `json:"ledger_transaction_id"`
	FeeStatus           string         `json:"fee_status"`
	Explanation         map[string]any `json:"explanation"`
	CreatedAt           time.Time      `json:"created_at"`
}

const itemCols = `x.id::text, x.import_id::text, x.external_line_id, x.revision, x.line_number, x.item_kind, x.direction, x.line_status, x.amount_minor::text,
	x.fee_minor::text, x.net_minor::text, x.currency, x.raw_currency_code, x.provider_ref, x.merchant_ref, x.batch_ref, x.settlement_batch_id::text,
	x.counterparty_ref, to_char(x.value_date, 'YYYY-MM-DD'), x.booked_at, x.raw_line_sha256, x.raw_line_locator, x.match_status,
	m.id::text, m.run_id::text, m.subject_type, m.subject_id::text, m.confidence, m.rule_code, m.rule_set_version, m.matcher_version,
	m.matched_amount_minor::text, m.ledger_status, m.ledger_transaction_id::text, m.fee_status, m.explanation, m.created_at`

func scanItemView(r pgx.Row) (ItemView, error) {
	var x ItemView
	var sum []byte
	var mid, msubjType, msubj, mconf, mrule, mmver, mamt, mledger, mfee *string
	var mrun, mltx *string
	var mver *int
	var mexpl []byte
	var mat *time.Time
	err := r.Scan(&x.ID, &x.ImportID, &x.ExternalLineID, &x.Revision, &x.LineNumber, &x.Kind, &x.Direction, &x.Status, &x.AmountMinor, &x.FeeMinor,
		&x.NetMinor, &x.Currency, &x.RawCurrencyCode, &x.ProviderRef, &x.MerchantRef, &x.BatchRef, &x.SettlementBatch, &x.CounterpartyRef,
		&x.ValueDate, &x.BookedAt, &sum, &x.RawLineLocator, &x.MatchStatus, &mid, &mrun, &msubjType, &msubj, &mconf, &mrule, &mver, &mmver, &mamt,
		&mledger, &mltx, &mfee, &mexpl, &mat)
	if err != nil {
		return x, err
	}
	x.RawLineSHA256 = hex.EncodeToString(sum)
	if mid != nil {
		m := &MatchView{ID: *mid, RunID: mrun, SubjectType: *msubjType, SubjectID: *msubj, Confidence: *mconf, RuleCode: *mrule, RuleSetVersion: mver,
			MatcherVersion: *mmver, MatchedAmountMinor: *mamt, LedgerStatus: *mledger, LedgerTransactionID: mltx, FeeStatus: *mfee, CreatedAt: *mat,
			Explanation: map[string]any{}}
		_ = json.Unmarshal(mexpl, &m.Explanation)
		x.Match = m
	}
	return x, nil
}

const itemFrom = ` FROM recon.items x LEFT JOIN recon.matches m ON m.item_id = x.id AND m.status = 'ACTIVE'`

// ItemByID returns one line with its active match.
func (s *Service) ItemByID(ctx context.Context, id string) (ItemView, error) {
	if !ids.Valid(id) {
		return ItemView{}, ErrNotFound
	}
	x, err := scanItemView(s.Pool.QueryRow(ctx, `SELECT `+itemCols+itemFrom+` WHERE x.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ItemView{}, ErrNotFound
	}
	return x, err
}

// ItemFilter filters lines.
type ItemFilter struct {
	ImportID, BatchID, MatchStatus, SubjectType string
	Limit                                       int
}

// Items lists lines (bounded).
func (s *Service) Items(ctx context.Context, f ItemFilter) ([]ItemView, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if (f.ImportID != "" && !ids.Valid(f.ImportID)) || (f.BatchID != "" && !ids.Valid(f.BatchID)) {
		return nil, ErrNotFound
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+itemCols+itemFrom+` WHERE ($1 = '' OR x.import_id::text = $1) AND ($2 = '' OR x.settlement_batch_id::text = $2)
		AND ($3 = '' OR x.match_status = $3) AND ($4 = '' OR m.subject_type = $4) ORDER BY x.booked_at, x.id LIMIT $5`,
		f.ImportID, f.BatchID, f.MatchStatus, f.SubjectType, f.Limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemView, error) { return scanItemView(r) })
}

// matchCols are the columns of a batch of matches (unnest arrays).
type matchCols struct {
	id, item, st, sid, conf, rule, ledger, fee, expl []string
	ltx                                              []*string
	amount                                           []int64
}

func (c *matchCols) add(id, item string, m *MatchDraft, expl string) {
	c.id, c.item, c.st, c.sid = append(c.id, id), append(c.item, item), append(c.st, m.SubjectType), append(c.sid, m.SubjectID)
	c.conf, c.rule, c.ledger, c.fee = append(c.conf, m.Confidence), append(c.rule, m.RuleCode), append(c.ledger, m.LedgerStatus), append(c.fee, m.FeeStatus)
	c.amount, c.expl = append(c.amount, m.Amount), append(c.expl, expl)
	var ltx *string
	if m.LedgerTxID != "" {
		v := m.LedgerTxID
		ltx = &v
	}
	c.ltx = append(c.ltx, ltx)
}
