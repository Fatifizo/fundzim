-- Remediation phase 6 — integrity-status reliability (docs/remediation/phase-6/integrity-status-design.md).
--
-- F-R5-01: a failed verification left the previous integrity status in place, so a stale VERIFIED looked current.
-- reconsweep.Verify now records every outcome, including a read failure (new status ERROR with a sanitised code in
-- integrity_error), and keeps the last successful verification apart from the latest outcome:
--   integrity_checked_at        start of the latest recorded verification (any outcome; newer starts win)
--   integrity_status/_detail    the latest outcome
--   integrity_error             a fixed code (never an error text) when the latest outcome was not VERIFIED
--   integrity_verified_at       start of the last VERIFIED check (never cleared by a failure)
--   integrity_unverified_since  start of the first non-VERIFIED check since the last VERIFIED one
--   integrity_failures          consecutive non-VERIFIED checks (0 after a VERIFIED one)
--   integrity_last_failure_at/  the latest non-VERIFIED check and its "STATUS[:CODE]" — kept after a later success, so a
--   integrity_last_failure      recovery never erases a failure
-- The columns are attempt-only (no position change, no version step) and are carried to the next cycle.
--
-- Additive: three nullable columns per checkpoint table, a widened status CHECK, an error-code CHECK and the guard's
-- attempt-only set extended. Compatibility: docs/remediation/phase-6/migration-compatibility.md — code before this
-- migration keeps working on it; the Phase 6 code requires it (it selects the new columns).

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE app.payout_reconciliation_sweeps
  ADD COLUMN integrity_verified_at      timestamptz,
  ADD COLUMN integrity_unverified_since timestamptz,
  ADD COLUMN integrity_error            text,
  ADD COLUMN integrity_failures         integer NOT NULL DEFAULT 0,
  ADD COLUMN integrity_last_failure_at  timestamptz,
  ADD COLUMN integrity_last_failure     text,
  DROP CONSTRAINT ck_payout_reconciliation_sweeps_integrity,
  ADD CONSTRAINT ck_payout_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE','ERROR')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384)),
  ADD CONSTRAINT ck_payout_reconciliation_sweeps_integrity_error CHECK ((integrity_error IS NULL OR integrity_error ~ '^[A-Z_]{1,64}$')
    AND integrity_failures >= 0 AND (integrity_last_failure IS NULL OR integrity_last_failure ~ '^[A-Z_]{1,32}(:[A-Z_]{1,64})?$')
    AND (integrity_last_failure IS NULL) = (integrity_last_failure_at IS NULL));
ALTER TABLE app.fee_reconciliation_sweeps
  ADD COLUMN integrity_verified_at      timestamptz,
  ADD COLUMN integrity_unverified_since timestamptz,
  ADD COLUMN integrity_error            text,
  ADD COLUMN integrity_failures         integer NOT NULL DEFAULT 0,
  ADD COLUMN integrity_last_failure_at  timestamptz,
  ADD COLUMN integrity_last_failure     text,
  DROP CONSTRAINT ck_fee_reconciliation_sweeps_integrity,
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE','ERROR')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384)),
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_integrity_error CHECK ((integrity_error IS NULL OR integrity_error ~ '^[A-Z_]{1,64}$')
    AND integrity_failures >= 0 AND (integrity_last_failure IS NULL OR integrity_last_failure ~ '^[A-Z_]{1,32}(:[A-Z_]{1,64})?$')
    AND (integrity_last_failure IS NULL) = (integrity_last_failure_at IS NULL));
ALTER TABLE ledger.ledger_reconciliation_sweeps
  ADD COLUMN integrity_verified_at      timestamptz,
  ADD COLUMN integrity_unverified_since timestamptz,
  ADD COLUMN integrity_error            text,
  ADD COLUMN integrity_failures         integer NOT NULL DEFAULT 0,
  ADD COLUMN integrity_last_failure_at  timestamptz,
  ADD COLUMN integrity_last_failure     text,
  DROP CONSTRAINT ck_ledger_reconciliation_sweeps_integrity,
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE','ERROR')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384)),
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_integrity_error CHECK ((integrity_error IS NULL OR integrity_error ~ '^[A-Z_]{1,64}$')
    AND integrity_failures >= 0 AND (integrity_last_failure IS NULL OR integrity_last_failure ~ '^[A-Z_]{1,32}(:[A-Z_]{1,64})?$')
    AND (integrity_last_failure IS NULL) = (integrity_last_failure_at IS NULL));

-- the guard of 20261027090000, with the Phase 6 integrity columns added to the attempt-only set
CREATE OR REPLACE FUNCTION app.reconciliation_sweep_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  attempt_cols constant text[] := ARRAY['last_attempt_at','last_attempt_outcome','last_attempt_error','failed_attempts',
                                        'integrity_checked_at','integrity_status','integrity_detail','integrity_error',
                                        'integrity_verified_at','integrity_unverified_since','integrity_failures',
                                        'integrity_last_failure_at','integrity_last_failure','updated_at'];
BEGIN
  IF OLD.status = 'COMPLETED' THEN
    RAISE EXCEPTION 'reconciliation sweep %: a completed cycle is immutable', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  -- a run's recorded outcome or integrity status only: the position, counters, deferrals, status and version are unchanged
  IF (to_jsonb(NEW) - attempt_cols) = (to_jsonb(OLD) - attempt_cols) THEN
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
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
-- the evidence constraint triggers run at statement end here, so the UPDATEs below leave no pending trigger events
-- for the ALTER TABLEs (an integrity-only update passes them: its version is unchanged)
SET CONSTRAINTS ALL IMMEDIATE;
-- the guard of 20261027090000 first: the integrity-only rewrite of ERROR below must stay an attempt-only update
CREATE OR REPLACE FUNCTION app.reconciliation_sweep_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  attempt_cols constant text[] := ARRAY['last_attempt_at','last_attempt_outcome','last_attempt_error','failed_attempts',
                                        'integrity_checked_at','integrity_status','integrity_detail','updated_at'];
BEGIN
  IF OLD.status = 'COMPLETED' THEN
    RAISE EXCEPTION 'reconciliation sweep %: a completed cycle is immutable', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  -- a run's recorded outcome or integrity status only: the position, counters, deferrals, status and version are unchanged
  IF (to_jsonb(NEW) - attempt_cols) = (to_jsonb(OLD) - attempt_cols) THEN
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
-- ERROR does not exist before this migration: a running cycle's ERROR becomes UNVERIFIABLE (the older code's "not
-- verified"), so the older code can keep recording on it. Completed cycles are immutable and are never rewritten: the
-- restored CHECK is NOT VALID, so a completed cycle that ended with ERROR stays as recorded (history is kept, new writes
-- are checked)
UPDATE ledger.ledger_reconciliation_sweeps SET integrity_status = 'UNVERIFIABLE' WHERE integrity_status = 'ERROR' AND status = 'RUNNING';
ALTER TABLE ledger.ledger_reconciliation_sweeps DROP CONSTRAINT ck_ledger_reconciliation_sweeps_integrity_error, DROP CONSTRAINT ck_ledger_reconciliation_sweeps_integrity,
  DROP COLUMN integrity_last_failure, DROP COLUMN integrity_last_failure_at, DROP COLUMN integrity_failures, DROP COLUMN integrity_error, DROP COLUMN integrity_unverified_since, DROP COLUMN integrity_verified_at,
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384)) NOT VALID;
UPDATE app.fee_reconciliation_sweeps SET integrity_status = 'UNVERIFIABLE' WHERE integrity_status = 'ERROR' AND status = 'RUNNING';
ALTER TABLE app.fee_reconciliation_sweeps DROP CONSTRAINT ck_fee_reconciliation_sweeps_integrity_error, DROP CONSTRAINT ck_fee_reconciliation_sweeps_integrity,
  DROP COLUMN integrity_last_failure, DROP COLUMN integrity_last_failure_at, DROP COLUMN integrity_failures, DROP COLUMN integrity_error, DROP COLUMN integrity_unverified_since, DROP COLUMN integrity_verified_at,
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384)) NOT VALID;
UPDATE app.payout_reconciliation_sweeps SET integrity_status = 'UNVERIFIABLE' WHERE integrity_status = 'ERROR' AND status = 'RUNNING';
ALTER TABLE app.payout_reconciliation_sweeps DROP CONSTRAINT ck_payout_reconciliation_sweeps_integrity_error, DROP CONSTRAINT ck_payout_reconciliation_sweeps_integrity,
  DROP COLUMN integrity_last_failure, DROP COLUMN integrity_last_failure_at, DROP COLUMN integrity_failures, DROP COLUMN integrity_error, DROP COLUMN integrity_unverified_since, DROP COLUMN integrity_verified_at,
  ADD CONSTRAINT ck_payout_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384)) NOT VALID;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
