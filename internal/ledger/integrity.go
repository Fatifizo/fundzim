package ledger

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Fatifizo/fundzim/internal/audit"
	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// The integrity checker verifies the ledger in one REPEATABLE READ snapshot and records one ledger_invariant_runs
// row per invariant. It reports; it never repairs financial history (a failed run carries a SYSTEM_SNAPSHOT
// evidence record and is an alert).
//
//	L1  every journal balances (debits = credits)        L2  every journal has ≥ 2 lines
//	L4  every line's currency = account currency = journal currency
//	L6  no two journals for the same single-event source and rule (duplicate posting)
//	L7  every reversal references a valid, non-reversal journal and mirrors it exactly
//	L9  per currency, total debits = total credits (trial balance)
//	L10 every balance projection equals a recompute from its entries (drift)
//	L13 no non-negative (obligation/recoverable) account is below zero
//	L15 every account, journal and source reference carries this database's ledger environment (Stage 10)
type Check struct {
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	Status     string   `json:"status"` // PASSED | FAILED | ERROR
	Checked    int64    `json:"checked"`
	Violations int64    `json:"violations"`
	Sample     []string `json:"sample"`
}

// IntegrityReport is one checker run.
type IntegrityReport struct {
	Environment string    `json:"environment"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
	Passed      bool      `json:"passed"`
	Checks      []Check   `json:"checks"`
}

type check struct {
	code, name string
	count      string // total checked
	violations string // ids (text) of violations
}

// journal-keyed rules that represent one business event per source (duplicate detection, L6)
const singleEventRules = `('DONATION_CAPTURED','PSP_FEE','RELEASE','RELEASE_WITH_RESERVE','PAYOUT_RESERVED','PAYOUT_SUBMITTED','PAYOUT_COMPLETED',
	'PAYOUT_FAILED','PAYOUT_RELEASED','REFUND_RESERVED','REFUND_SETTLED','REFUND_FAILED')`

var checks = []check{
	{"L1", "every journal balances",
		`SELECT count(*) FROM ledger.ledger_transactions`,
		`SELECT t.id::text FROM ledger.ledger_transactions t JOIN ledger.ledger_entries e ON e.transaction_id = t.id
		 GROUP BY t.id HAVING sum(e.amount_minor) FILTER (WHERE e.direction = 'DEBIT') IS DISTINCT FROM
		                     sum(e.amount_minor) FILTER (WHERE e.direction = 'CREDIT')`},
	{"L2", "every journal has at least two lines",
		`SELECT count(*) FROM ledger.ledger_transactions`,
		`SELECT t.id::text FROM ledger.ledger_transactions t LEFT JOIN ledger.ledger_entries e ON e.transaction_id = t.id
		 GROUP BY t.id HAVING count(e.id) < 2`},
	{"L4", "line, account and journal currencies match",
		`SELECT count(*) FROM ledger.ledger_entries`,
		`SELECT e.id::text FROM ledger.ledger_entries e JOIN ledger.ledger_accounts a ON a.id = e.account_id
		 JOIN ledger.ledger_transactions t ON t.id = e.transaction_id WHERE e.currency <> a.currency OR e.currency <> t.currency`},
	{"L6", "no duplicate posting of one source event",
		`SELECT count(*) FROM ledger.ledger_transactions WHERE posting_rule IN ` + singleEventRules,
		`SELECT min(id::text) FROM ledger.ledger_transactions WHERE posting_rule IN ` + singleEventRules + `
		 GROUP BY environment, source_type, source_id, posting_rule HAVING count(*) > 1`},
	{"L7", "every reversal is valid and mirrors its original",
		`SELECT count(*) FROM ledger.ledger_transactions WHERE reverses_transaction_id IS NOT NULL`,
		`SELECT r.id::text FROM ledger.ledger_transactions r JOIN ledger.ledger_transactions o ON o.id = r.reverses_transaction_id
		 WHERE o.posting_rule = 'REVERSAL' OR o.currency <> r.currency OR EXISTS (
		   (SELECT account_id, CASE direction WHEN 'DEBIT' THEN 'CREDIT' ELSE 'DEBIT' END, amount_minor FROM ledger.ledger_entries WHERE transaction_id = o.id
		    EXCEPT ALL SELECT account_id, direction, amount_minor FROM ledger.ledger_entries WHERE transaction_id = r.id)
		   UNION ALL
		   (SELECT account_id, direction, amount_minor FROM ledger.ledger_entries WHERE transaction_id = r.id
		    EXCEPT ALL SELECT account_id, CASE direction WHEN 'DEBIT' THEN 'CREDIT' ELSE 'DEBIT' END, amount_minor FROM ledger.ledger_entries WHERE transaction_id = o.id))`},
	{"L9", "trial balance is zero per currency",
		`SELECT count(DISTINCT currency) FROM ledger.ledger_entries`,
		`SELECT currency FROM ledger.v_trial_balance WHERE debit_total_minor IS DISTINCT FROM credit_total_minor`},
	{"L10", "balance projections equal a recompute from entries",
		`SELECT count(*) FROM ledger.ledger_balances`,
		`SELECT account_id::text FROM ledger.v_balance_drift`},
	{"L13", "no obligation or recoverable account below zero",
		`SELECT count(*) FROM ledger.ledger_accounts WHERE non_negative`,
		`SELECT a.id::text FROM ledger.ledger_accounts a JOIN ledger.v_balances_current b ON b.account_id = a.id
		 WHERE a.non_negative AND b.balance_minor < 0`},
	{"L15", "every record carries this database's ledger environment",
		`SELECT (SELECT count(*) FROM ledger.ledger_accounts) + (SELECT count(*) FROM ledger.ledger_transactions)
		      + (SELECT count(*) FROM ledger.ledger_source_references)`,
		`SELECT id::text FROM ledger.ledger_accounts WHERE environment <> ledger.database_environment()
		 UNION ALL SELECT id::text FROM ledger.ledger_transactions WHERE environment <> ledger.database_environment()
		 UNION ALL SELECT id::text FROM ledger.ledger_source_references WHERE environment <> ledger.database_environment()`},
}

// RunIntegrity runs every check and records the results (triggeredBy: SYSTEM component or STAFF user id).
func (s *Service) RunIntegrity(ctx context.Context, triggeredByType, triggeredByID string) (IntegrityReport, error) {
	env, err := s.Environment(ctx)
	if err != nil {
		return IntegrityReport{}, err
	}
	rep := IntegrityReport{Environment: env, StartedAt: s.now(), Passed: true}
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return rep, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, c := range checks {
		res := Check{Code: c.code, Name: c.name, Status: "PASSED", Sample: []string{}}
		if err := tx.QueryRow(ctx, c.count).Scan(&res.Checked); err != nil {
			return rep, err
		}
		rows, err := tx.Query(ctx, c.violations)
		if err != nil {
			return rep, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return rep, err
			}
			res.Violations++
			if len(res.Sample) < 20 {
				res.Sample = append(res.Sample, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return rep, err
		}
		if res.Violations > 0 {
			res.Status, rep.Passed = "FAILED", false
		}
		rep.Checks = append(rep.Checks, res)
	}
	_ = tx.Rollback(ctx)
	rep.FinishedAt = s.now()
	if err := s.recordRuns(ctx, rep, triggeredByType, triggeredByID); err != nil {
		return rep, err
	}
	if !rep.Passed {
		s.Logger.Error("ledger: integrity check FAILED (ALERT)", "environment", env)
	}
	return rep, nil
}

func (s *Service) recordRuns(ctx context.Context, rep IntegrityReport, byType, byID string) error {
	return db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		actorType, actorID := "system", ""
		if byType == "STAFF" {
			actorType, actorID = "staff", byID
		}
		outcome := "success"
		if !rep.Passed {
			outcome = "failed"
		}
		runID := ids.New()
		auditID, err := audit.RecordGateway(ctx, tx, audit.Event{Action: "ledger.integrity.checked", ActorType: actorType, ActorID: actorID,
			TargetType: "ledger_integrity_run", TargetID: runID, Outcome: outcome, Metadata: map[string]any{"environment": rep.Environment,
				"result": map[bool]string{true: "PASSED", false: "FAILED"}[rep.Passed], "checks": len(rep.Checks)}})
		if err != nil {
			return err
		}
		for _, c := range rep.Checks {
			var evID any
			if c.Status == "FAILED" {
				id, err := s.recordSnapshot(ctx, tx, "ledger_integrity_run", runID, "", auditID, map[string]any{"invariant": c.Code,
					"violations": c.Violations, "sample_ids": c.Sample})
				if err != nil {
					return err
				}
				evID = id
			}
			sample := c.Sample
			if sample == nil {
				sample = []string{}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO ledger.ledger_invariant_runs (id, invariant_code, scope, status, started_at, finished_at,
				checked_count, violation_count, violations_sample, evidence_record_id, triggered_by_type, triggered_by_id, job_id)
				VALUES ($1, $2, jsonb_build_object('environment', $3::text), $4, $5, $6, $7, $8, to_jsonb($9::text[]), $10, $11, $12, $13)`,
				ids.New(), c.Code, rep.Environment, c.Status, rep.StartedAt, rep.FinishedAt, c.Checked, c.Violations, sample, evID,
				byType, byID, runID); err != nil {
				return err
			}
		}
		return nil
	})
}

// LatestIntegrity returns the latest recorded run per invariant.
func (s *Service) LatestIntegrity(ctx context.Context) ([]Check, error) {
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT ON (invariant_code) invariant_code, status, checked_count, violation_count,
		ARRAY(SELECT jsonb_array_elements_text(violations_sample)) FROM ledger.ledger_invariant_runs
		ORDER BY invariant_code, started_at DESC`)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, c := range checks {
		names[c.code] = c.name
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Check, error) {
		var c Check
		err := r.Scan(&c.Code, &c.Status, &c.Checked, &c.Violations, &c.Sample)
		c.Name = names[c.Code]
		return c, err
	})
}
