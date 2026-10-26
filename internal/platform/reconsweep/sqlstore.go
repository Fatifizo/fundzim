package reconsweep

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Fatifizo/fundzim/internal/platform/db"
	"github.com/Fatifizo/fundzim/internal/platform/ids"
)

// SQLStore keeps checkpoints in a module's own *_reconciliation_sweeps table (same shape in every module; the
// database guards the invariants: one RUNNING cycle per detector and scope, a cursor that never moves backwards,
// counters that never decrease, a version bumped by exactly one, COMPLETED cycles immutable, no deletes). Timestamps
// of cycles are the database's own (transaction time of the statement that opened or closed the cycle), so the
// coverage watermark does not depend on the application host's clock.
type SQLStore struct {
	Pool  *pgxpool.Pool
	Table string // e.g. "app.payout_reconciliation_sweeps" — named by the owning module
}

var sweepTableRE = regexp.MustCompile(`^(app|ledger)\.[a-z_]+_reconciliation_sweeps$`)

func (s SQLStore) table() (string, error) {
	if !sweepTableRE.MatchString(s.Table) {
		return "", fmt.Errorf("reconsweep: invalid checkpoint table %q", s.Table)
	}
	return s.Table, nil
}

// evidenceTable is the module's append-only batch evidence table beside its checkpoint table
// (app.payout_reconciliation_sweeps → app.payout_reconciliation_sweep_batches).
func (s SQLStore) evidenceTable() (string, error) {
	t, err := s.table()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(t, "_sweeps") + "_sweep_batches", nil
}

const cpCols = `id::text, detector, scope, cycle, started_at, coalesce(cursor_key::text, ''), examined, exceptions, batches,
	deferred_keys::text[], deferred_since, version`

func scanCheckpoint(r pgx.Row) (Checkpoint, error) {
	var c Checkpoint
	var keys []string
	var since []time.Time
	err := r.Scan(&c.ID, &c.Detector, &c.Scope, &c.Cycle, &c.StartedAt, &c.Cursor, &c.Examined, &c.Exceptions, &c.Batches, &keys, &since, &c.Version)
	if err != nil {
		return c, err
	}
	if len(keys) != len(since) {
		return c, fmt.Errorf("reconsweep: checkpoint %s has %d deferral keys and %d times", c.ID, len(keys), len(since))
	}
	for i := range keys {
		c.Deferred = append(c.Deferred, Deferral{Key: keys[i], Since: since[i].UTC()})
	}
	return c, nil
}

// Active implements Store.
func (s SQLStore) Active(ctx context.Context, detector, scope string) (Checkpoint, error) {
	t, err := s.table()
	if err != nil {
		return Checkpoint{}, err
	}
	sel := `SELECT ` + cpCols + ` FROM ` + t + ` WHERE detector = $1 AND scope = $2 AND status = 'RUNNING'`
	c, err := scanCheckpoint(s.Pool.QueryRow(ctx, sel, detector, scope))
	if err == nil || !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	// first cycle (a concurrent creator wins the partial unique index; both then read the same row)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO `+t+` (id, detector, scope, cycle, status, started_at)
		SELECT $1, $2, $3, coalesce((SELECT max(cycle) FROM `+t+` WHERE detector = $2 AND scope = $3), 0) + 1, 'RUNNING', now()
		ON CONFLICT DO NOTHING`, ids.New(), detector, scope); err != nil {
		return Checkpoint{}, err
	}
	return scanCheckpoint(s.Pool.QueryRow(ctx, sel, detector, scope))
}

// Advance implements Store.
func (s SQLStore) Advance(ctx context.Context, cur, next Checkpoint, complete bool, runID string, ev Evidence) (bool, error) {
	t, err := s.table()
	if err != nil {
		return false, err
	}
	et, err := s.evidenceTable()
	if err != nil {
		return false, err
	}
	keys, since := make([]string, 0, len(next.Deferred)), make([]time.Time, 0, len(next.Deferred))
	for _, d := range next.Deferred {
		keys, since = append(keys, d.Key), append(since, d.Since)
	}
	won := false
	err = db.WithTx(ctx, s.Pool, db.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE `+t+` SET cursor_key = nullif($3, '')::uuid, examined = $4, exceptions = $5, batches = $6,
			deferred_keys = $7::uuid[], deferred_since = $8::timestamptz[], version = version + 1, last_advanced_at = now(), last_run = nullif($9, ''),
			status = CASE WHEN $10 THEN 'COMPLETED' ELSE 'RUNNING' END, completed_at = CASE WHEN $10 THEN now() END
			WHERE id = $1 AND version = $2 AND status = 'RUNNING'`,
			cur.ID, cur.Version, next.Cursor, next.Examined, next.Exceptions, next.Batches, keys, since, runID, complete)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		won = true
		// the evidence of this move, in the same transaction: the deferred constraint trigger refuses the position change
		// at commit unless exactly this row exists (remediation phase 4)
		if _, err := tx.Exec(ctx, `INSERT INTO `+et+` (id, sweep_id, detector, scope, cycle, seq, from_key, to_key, keys, deferred_added,
			deferrals_resolved, completing, run_id) VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::uuid, nullif($8, '')::uuid, $9::uuid[], $10::uuid[],
			$11::uuid[], $12, nullif($13, ''))`, ids.New(), cur.ID, cur.Detector, cur.Scope, cur.Cycle, ev.Seq, ev.From, ev.To, nonNilKeys(ev.Keys),
			nonNilKeys(ev.Deferred), nonNilKeys(ev.Resolved), ev.Completing, runID); err != nil {
			return err
		}
		if !complete {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO `+t+` (id, detector, scope, cycle, status, started_at, deferred_keys, deferred_since)
			VALUES ($1, $2, $3, $4, 'RUNNING', now(), $5::uuid[], $6::timestamptz[])`, ids.New(), cur.Detector, cur.Scope, cur.Cycle+1, keys, since)
		return err
	})
	if err != nil {
		return false, err
	}
	return won, nil
}

// RecordAttempt implements Store: the run's stop reason and error on the RUNNING checkpoint, and the count of
// consecutive failed runs (reset by a run without error). Only the attempt columns change; the guard allows such an
// update without a version step, so it never disturbs a concurrent run's compare-and-swap.
func (s SQLStore) RecordAttempt(ctx context.Context, detector, scope string, rep Report) error {
	t, err := s.table()
	if err != nil {
		return err
	}
	outcome := rep.StoppedBy
	if outcome == "" {
		outcome = StopError
	}
	failed := rep.Error != ""
	msg := rep.Error
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300])
	}
	_, err = s.Pool.Exec(ctx, `UPDATE `+t+` SET last_attempt_at = now(), last_attempt_outcome = $3, last_attempt_error = nullif($4, ''),
		failed_attempts = CASE WHEN $5 THEN failed_attempts + 1 ELSE 0 END WHERE detector = $1 AND scope = $2 AND status = 'RUNNING'`,
		detector, scope, outcome, msg, failed)
	return err
}

// Coverage implements Store.
func (s SQLStore) Coverage(ctx context.Context, detector, scope string) (Coverage, error) {
	t, err := s.table()
	if err != nil {
		return Coverage{}, err
	}
	c := Coverage{Detector: detector, Scope: scope}
	err = s.Pool.QueryRow(ctx, `SELECT started_at, completed_at, examined FROM `+t+`
		WHERE detector = $1 AND scope = $2 AND status = 'COMPLETED' ORDER BY cycle DESC LIMIT 1`, detector, scope).
		Scan(&c.LastCompletedCycleStartedAt, &c.LastCompletedAt, &c.LastCompletedExamined)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	var since []time.Time
	var outcome, aerr, istatus *string
	err = s.Pool.QueryRow(ctx, `SELECT cycle, started_at, examined, deferred_since, last_advanced_at, last_attempt_at, last_attempt_outcome,
		last_attempt_error, failed_attempts, integrity_status, integrity_checked_at FROM `+t+` WHERE detector = $1 AND scope = $2 AND status = 'RUNNING'`,
		detector, scope).Scan(&c.RunningCycle, &c.RunningStartedAt, &c.RunningExamined, &since, &c.LastAdvancedAt, &c.LastAttemptAt, &outcome, &aerr,
		&c.FailedAttempts, &istatus, &c.IntegrityCheckedAt)
	if istatus != nil {
		c.IntegrityStatus = *istatus
	}
	if outcome != nil {
		c.LastAttemptOutcome = *outcome
	}
	if aerr != nil {
		c.LastAttemptError = *aerr
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	c.DeferredOutstanding = len(since)
	for _, d := range since {
		if c.OldestDeferralSince == nil || d.Before(*c.OldestDeferralSince) {
			x := d.UTC()
			c.OldestDeferralSince = &x
		}
	}
	return c, nil
}

func nonNilKeys(ks []string) []string {
	if ks == nil {
		return []string{}
	}
	return ks
}

// ---- integrity (remediation phase 4) ------------------------------------------------------------------------------

const cycleCols = `id::text, cycle, status, started_at, completed_at, coalesce(cursor_key::text, ''), examined, batches,
	deferred_keys::text[]`

func scanCycle(r pgx.Row) (*CycleRecord, error) {
	var c CycleRecord
	err := r.Scan(&c.ID, &c.Cycle, &c.Status, &c.StartedAt, &c.CompletedAt, &c.Cursor, &c.Examined, &c.Batches, &c.Deferred)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// IntegrityCycles implements IntegrityStore: the RUNNING cycle and the last two COMPLETED ones.
func (s SQLStore) IntegrityCycles(ctx context.Context, detector, scope string) (running, last, beforeLast *CycleRecord, err error) {
	t, err := s.table()
	if err != nil {
		return nil, nil, nil, err
	}
	if running, err = scanCycle(s.Pool.QueryRow(ctx, `SELECT `+cycleCols+` FROM `+t+` WHERE detector = $1 AND scope = $2 AND status = 'RUNNING'`,
		detector, scope)); err != nil {
		return nil, nil, nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+cycleCols+` FROM `+t+` WHERE detector = $1 AND scope = $2 AND status = 'COMPLETED'
		ORDER BY cycle DESC LIMIT 2`, detector, scope)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	var done []*CycleRecord
	for rows.Next() {
		c, err := scanCycle(rows)
		if err != nil {
			return nil, nil, nil, err
		}
		done = append(done, c)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	if len(done) > 0 {
		last = done[0]
	}
	if len(done) > 1 {
		beforeLast = done[1]
	}
	return running, last, beforeLast, nil
}

// Evidence implements IntegrityStore: one cycle's batch evidence in batch order.
func (s SQLStore) Evidence(ctx context.Context, sweepID string) ([]Evidence, error) {
	et, err := s.evidenceTable()
	if err != nil {
		return nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT seq, coalesce(from_key::text, ''), coalesce(to_key::text, ''), keys::text[], deferred_added::text[],
		deferrals_resolved::text[], completing, examined_at FROM `+et+` WHERE sweep_id = $1 ORDER BY seq`, sweepID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Evidence, error) {
		var e Evidence
		err := r.Scan(&e.Seq, &e.From, &e.To, &e.Keys, &e.Deferred, &e.Resolved, &e.Completing, &e.ExaminedAt)
		return e, err
	})
}

// RecordIntegrity implements IntegrityStore: the verification outcome on the RUNNING checkpoint (an attempt-only
// update: no position change, no version step).
func (s SQLStore) RecordIntegrity(ctx context.Context, detector, scope string, r IntegrityReport) error {
	t, err := s.table()
	if err != nil {
		return err
	}
	detail, err := json.Marshal(r.bounded())
	if err != nil {
		return err
	}
	_, err = s.Pool.Exec(ctx, `UPDATE `+t+` SET integrity_checked_at = now(), integrity_status = $3, integrity_detail = $4
		WHERE detector = $1 AND scope = $2 AND status = 'RUNNING'`, detector, scope, r.Status, detail)
	return err
}
