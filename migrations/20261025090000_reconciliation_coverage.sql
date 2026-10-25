-- Remediation of KI-S19-09 — complete, resumable coverage of the internal reconciliations (ADR-052). Owner modules:
-- payouts (app.payout_reconciliation_sweeps), fees (app.fee_reconciliation_sweeps) and ledger
-- (ledger.ledger_reconciliation_sweeps, written only by fundzim_ledger). Runs as fundzim_migrator. Forward-only in
-- production (ADR-028); Down is for development.
--
-- Each row is one sweep cycle of one detector: a walk over every record of the detector's scope in primary-key
-- order (internal/platform/reconsweep). The cursor is the last key whose batch was fully examined; it is advanced by
-- compare-and-swap on version after the batch, so it never passes an unexamined record. Deferrals are records the
-- detector could not judge yet (grace period); they are carried until examined. The guard below enforces: one
-- RUNNING cycle per detector and scope (partial unique index), identity columns fixed, the cursor never moving
-- backwards within a cycle, counters never decreasing, version bumped by exactly one, COMPLETED cycles immutable,
-- no deletes. No financial table is changed; no existing record is touched (no backfill: the first run opens cycle 1).

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE FUNCTION app.reconciliation_sweep_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout_reconciliation_sweep', '',        'RUNNING'),
  ('payout_reconciliation_sweep', 'RUNNING', 'COMPLETED'),
  ('fee_reconciliation_sweep',    '',        'RUNNING'),
  ('fee_reconciliation_sweep',    'RUNNING', 'COMPLETED'),
  ('ledger_reconciliation_sweep', '',        'RUNNING'),
  ('ledger_reconciliation_sweep', 'RUNNING', 'COMPLETED');

-- payouts ----------------------------------------------------------------------------------------------------------
CREATE TABLE app.payout_reconciliation_sweeps (
  id                uuid          NOT NULL,
  detector          text          NOT NULL,
  scope             text          NOT NULL,
  cycle             bigint        NOT NULL,
  status            text          NOT NULL,
  started_at        timestamptz   NOT NULL,
  completed_at      timestamptz,
  cursor_key        uuid,
  examined          bigint        NOT NULL DEFAULT 0,
  exceptions        bigint        NOT NULL DEFAULT 0,
  batches           bigint        NOT NULL DEFAULT 0,
  deferred_keys     uuid[]        NOT NULL DEFAULT '{}',
  deferred_since    timestamptz[] NOT NULL DEFAULT '{}',
  last_advanced_at  timestamptz,
  last_run          text,
  version           bigint        NOT NULL DEFAULT 0,
  created_at        timestamptz   NOT NULL DEFAULT now(),
  updated_at        timestamptz   NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_reconciliation_sweeps PRIMARY KEY (id),
  CONSTRAINT uq_payout_reconciliation_sweeps_cycle UNIQUE (detector, scope, cycle),
  CONSTRAINT ck_payout_reconciliation_sweeps_detector CHECK (detector IN ('PAYOUTS')),
  CONSTRAINT ck_payout_reconciliation_sweeps_scope CHECK (scope ~ '^[a-z0-9][a-z0-9_.:-]{0,63}$'),
  CONSTRAINT ck_payout_reconciliation_sweeps_status CHECK (status IN ('RUNNING','COMPLETED')),
  CONSTRAINT ck_payout_reconciliation_sweeps_completed CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL)),
  CONSTRAINT ck_payout_reconciliation_sweeps_counts CHECK (cycle >= 1 AND examined >= 0 AND exceptions >= 0 AND batches >= 0 AND version >= 0),
  CONSTRAINT ck_payout_reconciliation_sweeps_deferred CHECK (cardinality(deferred_keys) = cardinality(deferred_since)),
  CONSTRAINT ck_payout_reconciliation_sweeps_run CHECK (last_run IS NULL OR length(last_run) <= 200)
);
CREATE UNIQUE INDEX uq_payout_reconciliation_sweeps_running ON app.payout_reconciliation_sweeps (detector, scope) WHERE status = 'RUNNING';
CREATE TRIGGER trg_payout_reconciliation_sweeps_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout_reconciliation_sweep');
CREATE TRIGGER trg_payout_reconciliation_sweeps_guard BEFORE UPDATE ON app.payout_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_guard();
CREATE TRIGGER trg_payout_reconciliation_sweeps_no_delete BEFORE DELETE ON app.payout_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_reconciliation_sweeps_no_truncate BEFORE TRUNCATE ON app.payout_reconciliation_sweeps
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- fees -------------------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_reconciliation_sweeps (LIKE app.payout_reconciliation_sweeps INCLUDING DEFAULTS);
ALTER TABLE app.fee_reconciliation_sweeps
  ADD CONSTRAINT pk_fee_reconciliation_sweeps PRIMARY KEY (id),
  ADD CONSTRAINT uq_fee_reconciliation_sweeps_cycle UNIQUE (detector, scope, cycle),
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_detector CHECK (detector IN ('FEE_ASSESSMENTS')),
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_scope CHECK (scope ~ '^[a-z0-9][a-z0-9_.:-]{0,63}$'),
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_status CHECK (status IN ('RUNNING','COMPLETED')),
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_completed CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL)),
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_counts CHECK (cycle >= 1 AND examined >= 0 AND exceptions >= 0 AND batches >= 0 AND version >= 0),
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_deferred CHECK (cardinality(deferred_keys) = cardinality(deferred_since)),
  ADD CONSTRAINT ck_fee_reconciliation_sweeps_run CHECK (last_run IS NULL OR length(last_run) <= 200);
CREATE UNIQUE INDEX uq_fee_reconciliation_sweeps_running ON app.fee_reconciliation_sweeps (detector, scope) WHERE status = 'RUNNING';
CREATE TRIGGER trg_fee_reconciliation_sweeps_guard_status BEFORE INSERT OR UPDATE OF status ON app.fee_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('fee_reconciliation_sweep');
CREATE TRIGGER trg_fee_reconciliation_sweeps_guard BEFORE UPDATE ON app.fee_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_guard();
CREATE TRIGGER trg_fee_reconciliation_sweeps_no_delete BEFORE DELETE ON app.fee_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_reconciliation_sweeps_no_truncate BEFORE TRUNCATE ON app.fee_reconciliation_sweeps
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- ledger (fundzim_ledger only; apply_runtime_grants gives it SELECT, INSERT, UPDATE — no forbid_mutation UPDATE trigger)
CREATE TABLE ledger.ledger_reconciliation_sweeps (LIKE app.payout_reconciliation_sweeps INCLUDING DEFAULTS);
ALTER TABLE ledger.ledger_reconciliation_sweeps
  ADD CONSTRAINT pk_ledger_reconciliation_sweeps PRIMARY KEY (id),
  ADD CONSTRAINT uq_ledger_reconciliation_sweeps_cycle UNIQUE (detector, scope, cycle),
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_detector CHECK (detector IN ('PAYMENT_JOURNALS','PAYMENT_FACTS')),
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_scope CHECK (scope ~ '^[a-z0-9][a-z0-9_.:-]{0,63}$'),
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_status CHECK (status IN ('RUNNING','COMPLETED')),
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_completed CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL)),
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_counts CHECK (cycle >= 1 AND examined >= 0 AND exceptions >= 0 AND batches >= 0 AND version >= 0),
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_deferred CHECK (cardinality(deferred_keys) = cardinality(deferred_since)),
  ADD CONSTRAINT ck_ledger_reconciliation_sweeps_run CHECK (last_run IS NULL OR length(last_run) <= 200);
CREATE UNIQUE INDEX uq_ledger_reconciliation_sweeps_running ON ledger.ledger_reconciliation_sweeps (detector, scope) WHERE status = 'RUNNING';
CREATE TRIGGER trg_ledger_reconciliation_sweeps_guard_status BEFORE INSERT OR UPDATE OF status ON ledger.ledger_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ledger_reconciliation_sweep');
CREATE TRIGGER trg_ledger_reconciliation_sweeps_guard BEFORE UPDATE ON ledger.ledger_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.reconciliation_sweep_guard();
CREATE TRIGGER trg_ledger_reconciliation_sweeps_no_delete BEFORE DELETE ON ledger.ledger_reconciliation_sweeps
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ledger_reconciliation_sweeps_no_truncate BEFORE TRUNCATE ON ledger.ledger_reconciliation_sweeps
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ledger.ledger_reconciliation_sweeps;
DROP TABLE IF EXISTS app.fee_reconciliation_sweeps;
DROP TABLE IF EXISTS app.payout_reconciliation_sweeps;
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine IN ('payout_reconciliation_sweep','fee_reconciliation_sweep','ledger_reconciliation_sweep');
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
DROP FUNCTION IF EXISTS app.reconciliation_sweep_guard();
-- +goose StatementEnd
