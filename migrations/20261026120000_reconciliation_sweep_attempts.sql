-- Remediation phase 3, F-R2-03 — the outcome of every reconciliation sweep run is recorded on its RUNNING checkpoint,
-- so a sweep that fails or stops making progress is visible (reconsweep.Health) even when its position never moves.
-- Additive: four nullable / defaulted columns on each sweep table, and the guard accepts an update that changes ONLY
-- those columns without a version step (a run's outcome must never disturb a concurrent run's compare-and-swap). Every
-- other update keeps the rules of 20261025090000 (cursor never backwards, counters never decrease, version + 1,
-- COMPLETED immutable, no deletes). No existing row is changed (failed_attempts defaults to 0). Runs as
-- fundzim_migrator. Forward-only in production (ADR-028); Down is for development.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE app.payout_reconciliation_sweeps
  ADD COLUMN last_attempt_at      timestamptz,
  ADD COLUMN last_attempt_outcome text,
  ADD COLUMN last_attempt_error   text,
  ADD COLUMN failed_attempts      integer NOT NULL DEFAULT 0,
  ADD CONSTRAINT ck_payout_reconciliation_sweeps_attempt CHECK (failed_attempts >= 0
    AND (last_attempt_outcome IS NULL OR last_attempt_outcome IN ('CYCLE_COMPLETED','RECORD_BUDGET','TIME_BUDGET','CHECKPOINT_CONFLICT','DEFERRAL_LIMIT','ERROR'))
    AND (last_attempt_error IS NULL OR length(last_attempt_error) <= 300));

ALTER TABLE app.fee_reconciliation_sweeps
  ADD COLUMN last_attempt_at      timestamptz,
  ADD COLUMN last_attempt_outcome text,
  ADD COLUMN last_attempt_error   text,
  ADD COLUMN failed_attempts      integer NOT NULL DEFAULT 0,
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_attempt CHECK (failed_attempts >= 0
    AND (last_attempt_outcome IS NULL OR last_attempt_outcome IN ('CYCLE_COMPLETED','RECORD_BUDGET','TIME_BUDGET','CHECKPOINT_CONFLICT','DEFERRAL_LIMIT','ERROR'))
    AND (last_attempt_error IS NULL OR length(last_attempt_error) <= 300));

ALTER TABLE ledger.ledger_reconciliation_sweeps
  ADD COLUMN last_attempt_at      timestamptz,
  ADD COLUMN last_attempt_outcome text,
  ADD COLUMN last_attempt_error   text,
  ADD COLUMN failed_attempts      integer NOT NULL DEFAULT 0,
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_attempt CHECK (failed_attempts >= 0
    AND (last_attempt_outcome IS NULL OR last_attempt_outcome IN ('CYCLE_COMPLETED','RECORD_BUDGET','TIME_BUDGET','CHECKPOINT_CONFLICT','DEFERRAL_LIMIT','ERROR'))
    AND (last_attempt_error IS NULL OR length(last_attempt_error) <= 300));

CREATE OR REPLACE FUNCTION app.reconciliation_sweep_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'COMPLETED' THEN
    RAISE EXCEPTION 'reconciliation sweep %: a completed cycle is immutable', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  -- a run's recorded outcome only (RecordAttempt): the position, counters, deferrals, status and version are unchanged
  IF (to_jsonb(NEW) - ARRAY['last_attempt_at','last_attempt_outcome','last_attempt_error','failed_attempts','updated_at'])
     = (to_jsonb(OLD) - ARRAY['last_attempt_at','last_attempt_outcome','last_attempt_error','failed_attempts','updated_at']) THEN
    NEW.updated_at := now();
    RETURN NEW;
  END IF;
  IF NEW.id <> OLD.id OR NEW.detector <> OLD.detector OR NEW.scope <> OLD.scope OR NEW.cycle <> OLD.cycle
     OR NEW.started_at <> OLD.started_at OR NEW.created_at <> OLD.created_at THEN
    RAISE EXCEPTION 'reconciliation sweep %: identity columns are immutable', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.cursor_key IS NOT NULL AND (NEW.cursor_key IS NULL OR NEW.cursor_key < OLD.cursor_key) THEN
    RAISE EXCEPTION 'reconciliation sweep %: the cursor never moves backwards', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.examined < OLD.examined OR NEW.exceptions < OLD.exceptions OR NEW.batches < OLD.batches THEN
    RAISE EXCEPTION 'reconciliation sweep %: counters never decrease', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'reconciliation sweep %: version must advance by one', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  NEW.updated_at := now();
  RETURN NEW;
END $$;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.reconciliation_sweep_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'COMPLETED' THEN
    RAISE EXCEPTION 'reconciliation sweep %: a completed cycle is immutable', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.id <> OLD.id OR NEW.detector <> OLD.detector OR NEW.scope <> OLD.scope OR NEW.cycle <> OLD.cycle
     OR NEW.started_at <> OLD.started_at OR NEW.created_at <> OLD.created_at THEN
    RAISE EXCEPTION 'reconciliation sweep %: identity columns are immutable', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  IF OLD.cursor_key IS NOT NULL AND (NEW.cursor_key IS NULL OR NEW.cursor_key < OLD.cursor_key) THEN
    RAISE EXCEPTION 'reconciliation sweep %: the cursor never moves backwards', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.examined < OLD.examined OR NEW.exceptions < OLD.exceptions OR NEW.batches < OLD.batches THEN
    RAISE EXCEPTION 'reconciliation sweep %: counters never decrease', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'reconciliation sweep %: version must advance by one', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  NEW.updated_at := now();
  RETURN NEW;
END $$;
ALTER TABLE ledger.ledger_reconciliation_sweeps DROP CONSTRAINT ck_ledger_reconciliation_sweeps_attempt,
  DROP COLUMN failed_attempts, DROP COLUMN last_attempt_error, DROP COLUMN last_attempt_outcome, DROP COLUMN last_attempt_at;
ALTER TABLE app.fee_reconciliation_sweeps DROP CONSTRAINT ck_fee_reconciliation_sweeps_attempt,
  DROP COLUMN failed_attempts, DROP COLUMN last_attempt_error, DROP COLUMN last_attempt_outcome, DROP COLUMN last_attempt_at;
ALTER TABLE app.payout_reconciliation_sweeps DROP CONSTRAINT ck_payout_reconciliation_sweeps_attempt,
  DROP COLUMN failed_attempts, DROP COLUMN last_attempt_error, DROP COLUMN last_attempt_outcome, DROP COLUMN last_attempt_at;
-- +goose StatementEnd
