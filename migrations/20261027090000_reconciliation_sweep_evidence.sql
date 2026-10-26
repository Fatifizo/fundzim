-- Remediation phase 4 — checkpoint integrity (docs/remediation/phase-4/checkpoint-integrity-design.md).
--
-- 1. Batch evidence: every checkpoint advance stores, in the same transaction, one append-only row per batch in the
--    module's *_reconciliation_sweep_batches table: the range (from_key exclusive, to_key = the new cursor), the keys the
--    batch examined, the keys it newly carried as deferrals and the carried deferrals it resolved.
-- 2. Preventive control: a deferred constraint trigger on the checkpoint tables refuses, at commit, any position change
--    (a version step) that is not justified by exactly that evidence row — the cursor must move from the evidence's
--    from_key to its to_key, `examined` must grow by its key count, `batches` by one, and the carried deferral set must
--    become (old − resolved) ∪ added. A new cycle must start empty, with the previous cycle's final deferrals.
--    So a checkpoint cannot be moved forward without evidence — by any role, through any path.
-- 3. Detective control: reconsweep.Verify re-reads each detector's scope independently and records an integrity status
--    on the running checkpoint (integrity_* columns; attempt-only updates, like the run outcome of 20261026120000).
--
-- Additive: three new tables, three nullable columns per checkpoint table, the guard extended to accept integrity-only
-- updates, two trigger functions. No existing row changes. Cycles running when this is applied have no evidence for
-- their earlier batches: the verifier reports them UNVERIFIABLE until they complete; every advance from now on needs
-- evidence. Retention of the evidence rows is NOT decided (LR-012): nothing deletes them (append-only; no retention
-- job). Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for development only and drops only
-- the objects created here (operational evidence, no financial record).

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE app.payout_reconciliation_sweep_batches (
  id                  uuid        NOT NULL,
  sweep_id            uuid        NOT NULL,
  detector            text        NOT NULL,
  scope               text        NOT NULL,
  cycle               bigint      NOT NULL,
  seq                 bigint      NOT NULL,
  from_key            uuid,
  to_key              uuid,
  keys                uuid[]      NOT NULL,
  deferred_added      uuid[]      NOT NULL DEFAULT '{}',
  deferrals_resolved  uuid[]      NOT NULL DEFAULT '{}',
  completing          boolean     NOT NULL,
  run_id              text,
  examined_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_reconciliation_sweep_batches PRIMARY KEY (id),
  CONSTRAINT fk_payout_reconciliation_sweep_batches_sweep FOREIGN KEY (sweep_id) REFERENCES app.payout_reconciliation_sweeps (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_reconciliation_sweep_batches_seq UNIQUE (sweep_id, seq),
  CONSTRAINT ck_payout_reconciliation_sweep_batches_bounds CHECK (seq >= 1 AND cycle >= 1 AND cardinality(keys) <= 100000
    AND cardinality(deferred_added) <= 100000 AND cardinality(deferrals_resolved) <= 100000 AND (run_id IS NULL OR length(run_id) <= 200))
);
CREATE TRIGGER trg_payout_reconciliation_sweep_batches_no_update BEFORE UPDATE OR DELETE ON app.payout_reconciliation_sweep_batches
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_reconciliation_sweep_batches_no_truncate BEFORE TRUNCATE ON app.payout_reconciliation_sweep_batches
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.fee_reconciliation_sweep_batches (LIKE app.payout_reconciliation_sweep_batches INCLUDING DEFAULTS);
ALTER TABLE app.fee_reconciliation_sweep_batches
  ADD CONSTRAINT pk_fee_reconciliation_sweep_batches PRIMARY KEY (id),
  ADD CONSTRAINT fk_fee_reconciliation_sweep_batches_sweep FOREIGN KEY (sweep_id) REFERENCES app.fee_reconciliation_sweeps (id) ON DELETE RESTRICT,
  ADD CONSTRAINT uq_fee_reconciliation_sweep_batches_seq UNIQUE (sweep_id, seq),
  ADD CONSTRAINT ck_fee_reconciliation_sweep_batches_bounds CHECK (seq >= 1 AND cycle >= 1 AND cardinality(keys) <= 100000
    AND cardinality(deferred_added) <= 100000 AND cardinality(deferrals_resolved) <= 100000 AND (run_id IS NULL OR length(run_id) <= 200));
CREATE TRIGGER trg_fee_reconciliation_sweep_batches_no_update BEFORE UPDATE OR DELETE ON app.fee_reconciliation_sweep_batches
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_reconciliation_sweep_batches_no_truncate BEFORE TRUNCATE ON app.fee_reconciliation_sweep_batches
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE ledger.ledger_reconciliation_sweep_batches (LIKE app.payout_reconciliation_sweep_batches INCLUDING DEFAULTS);
ALTER TABLE ledger.ledger_reconciliation_sweep_batches
  ADD CONSTRAINT pk_ledger_reconciliation_sweep_batches PRIMARY KEY (id),
  ADD CONSTRAINT fk_ledger_reconciliation_sweep_batches_sweep FOREIGN KEY (sweep_id) REFERENCES ledger.ledger_reconciliation_sweeps (id) ON DELETE RESTRICT,
  ADD CONSTRAINT uq_ledger_reconciliation_sweep_batches_seq UNIQUE (sweep_id, seq),
  ADD CONSTRAINT ck_ledger_reconciliation_sweep_batches_bounds CHECK (seq >= 1 AND cycle >= 1 AND cardinality(keys) <= 100000
    AND cardinality(deferred_added) <= 100000 AND cardinality(deferrals_resolved) <= 100000 AND (run_id IS NULL OR length(run_id) <= 200));
CREATE TRIGGER trg_ledger_reconciliation_sweep_batches_no_update BEFORE UPDATE OR DELETE ON ledger.ledger_reconciliation_sweep_batches
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_reconciliation_sweep_batches_no_truncate BEFORE TRUNCATE ON ledger.ledger_reconciliation_sweep_batches
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- integrity status of the latest verification (attempt-only columns: no position change, no version step)
ALTER TABLE app.payout_reconciliation_sweeps
  ADD COLUMN integrity_checked_at timestamptz,
  ADD COLUMN integrity_status     text,
  ADD COLUMN integrity_detail     jsonb,
  ADD CONSTRAINT ck_payout_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384));
ALTER TABLE app.fee_reconciliation_sweeps
  ADD COLUMN integrity_checked_at timestamptz,
  ADD COLUMN integrity_status     text,
  ADD COLUMN integrity_detail     jsonb,
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384));
ALTER TABLE ledger.ledger_reconciliation_sweeps
  ADD COLUMN integrity_checked_at timestamptz,
  ADD COLUMN integrity_status     text,
  ADD COLUMN integrity_detail     jsonb,
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_integrity CHECK ((integrity_status IS NULL OR integrity_status IN
    ('VERIFIED','INCONSISTENT','INCOMPLETE','UNVERIFIABLE')) AND (integrity_detail IS NULL OR octet_length(integrity_detail::text) <= 16384));

-- the guard of 20261025090000/20261026120000, with the integrity columns added to the attempt-only set
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

-- position changes must match their evidence row (deferred to commit: Advance writes the evidence after the update)
CREATE FUNCTION app.reconciliation_sweep_evidence_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  e record;
  expected uuid[];
BEGIN
  IF NEW.version = OLD.version THEN
    RETURN NULL; -- attempt- or integrity-only update
  END IF;
  EXECUTE format('SELECT from_key, to_key, cardinality(keys) AS n, deferred_added, deferrals_resolved FROM %s WHERE sweep_id = $1 AND seq = $2',
    TG_ARGV[0]) INTO e USING NEW.id, NEW.batches;
  IF e IS NULL THEN
    RAISE EXCEPTION 'reconciliation sweep %: position change to batch % without evidence', NEW.id, NEW.batches USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.batches <> OLD.batches + 1 OR e.from_key IS DISTINCT FROM OLD.cursor_key OR e.to_key IS DISTINCT FROM NEW.cursor_key
     OR NEW.examined <> OLD.examined + e.n THEN
    RAISE EXCEPTION 'reconciliation sweep %: position change does not match its evidence (batch %)', NEW.id, NEW.batches
      USING ERRCODE = 'check_violation';
  END IF;
  expected := ARRAY(SELECT k FROM (SELECT unnest(OLD.deferred_keys) EXCEPT SELECT unnest(e.deferrals_resolved)
                                   UNION SELECT unnest(e.deferred_added)) x(k) ORDER BY k);
  IF expected <> ARRAY(SELECT k FROM unnest(NEW.deferred_keys) k ORDER BY k) THEN
    RAISE EXCEPTION 'reconciliation sweep %: carried deferrals do not match the evidence (batch %)', NEW.id, NEW.batches
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;

-- a new cycle starts empty and carries exactly the previous cycle's final deferrals
CREATE FUNCTION app.reconciliation_sweep_cycle_start_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  prev uuid[];
BEGIN
  IF NEW.cursor_key IS NOT NULL OR NEW.examined <> 0 OR NEW.batches <> 0 OR NEW.version <> 0 THEN
    RAISE EXCEPTION 'reconciliation sweep %: a new cycle starts empty', NEW.id USING ERRCODE = 'check_violation';
  END IF;
  EXECUTE format('SELECT deferred_keys FROM %s WHERE detector = $1 AND scope = $2 AND cycle = $3', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME)
    INTO prev USING NEW.detector, NEW.scope, NEW.cycle - 1;
  IF ARRAY(SELECT k FROM unnest(coalesce(prev, '{}'::uuid[])) k ORDER BY k) <> ARRAY(SELECT k FROM unnest(NEW.deferred_keys) k ORDER BY k) THEN
    RAISE EXCEPTION 'reconciliation sweep %: a new cycle must carry exactly the previous cycle''s deferrals', NEW.id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;

CREATE CONSTRAINT TRIGGER trg_payout_reconciliation_sweeps_evidence AFTER UPDATE ON app.payout_reconciliation_sweeps
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_evidence_check('app.payout_reconciliation_sweep_batches');
CREATE CONSTRAINT TRIGGER trg_fee_reconciliation_sweeps_evidence AFTER UPDATE ON app.fee_reconciliation_sweeps
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_evidence_check('app.fee_reconciliation_sweep_batches');
CREATE CONSTRAINT TRIGGER trg_ledger_reconciliation_sweeps_evidence AFTER UPDATE ON ledger.ledger_reconciliation_sweeps
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_evidence_check('ledger.ledger_reconciliation_sweep_batches');
CREATE TRIGGER trg_payout_reconciliation_sweeps_cycle_start AFTER INSERT ON app.payout_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_cycle_start_check();
CREATE TRIGGER trg_fee_reconciliation_sweeps_cycle_start AFTER INSERT ON app.fee_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_cycle_start_check();
CREATE TRIGGER trg_ledger_reconciliation_sweeps_cycle_start AFTER INSERT ON ledger.ledger_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_cycle_start_check();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER trg_ledger_reconciliation_sweeps_cycle_start ON ledger.ledger_reconciliation_sweeps;
DROP TRIGGER trg_fee_reconciliation_sweeps_cycle_start ON app.fee_reconciliation_sweeps;
DROP TRIGGER trg_payout_reconciliation_sweeps_cycle_start ON app.payout_reconciliation_sweeps;
DROP TRIGGER trg_ledger_reconciliation_sweeps_evidence ON ledger.ledger_reconciliation_sweeps;
DROP TRIGGER trg_fee_reconciliation_sweeps_evidence ON app.fee_reconciliation_sweeps;
DROP TRIGGER trg_payout_reconciliation_sweeps_evidence ON app.payout_reconciliation_sweeps;
DROP FUNCTION app.reconciliation_sweep_cycle_start_check();
DROP FUNCTION app.reconciliation_sweep_evidence_check();
CREATE OR REPLACE FUNCTION app.reconciliation_sweep_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'COMPLETED' THEN
    RAISE EXCEPTION 'reconciliation sweep %: a completed cycle is immutable', OLD.id USING ERRCODE = 'check_violation';
  END IF;
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
ALTER TABLE ledger.ledger_reconciliation_sweeps DROP CONSTRAINT ck_ledger_reconciliation_sweeps_integrity,
  DROP COLUMN integrity_detail, DROP COLUMN integrity_status, DROP COLUMN integrity_checked_at;
ALTER TABLE app.fee_reconciliation_sweeps DROP CONSTRAINT ck_fee_reconciliation_sweeps_integrity,
  DROP COLUMN integrity_detail, DROP COLUMN integrity_status, DROP COLUMN integrity_checked_at;
ALTER TABLE app.payout_reconciliation_sweeps DROP CONSTRAINT ck_payout_reconciliation_sweeps_integrity,
  DROP COLUMN integrity_detail, DROP COLUMN integrity_status, DROP COLUMN integrity_checked_at;
DROP TABLE ledger.ledger_reconciliation_sweep_batches;
DROP TABLE app.fee_reconciliation_sweep_batches;
DROP TABLE app.payout_reconciliation_sweep_batches;
-- +goose StatementEnd
