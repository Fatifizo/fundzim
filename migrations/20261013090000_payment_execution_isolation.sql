-- Stage 9 (ADR-040, ADR-041): payment execution isolation and provider evidence.
--   * execution_environment on donation and payment intents (LOCAL_SIMULATOR | PROVIDER_SANDBOX | PRODUCTION),
--     consistent with the provider account's environment; PRODUCTION rows are refused outright in Stage 9
--     (ck_*_no_production_stage9 — lifting it needs an ADR, legal sign-off and owner acceptance);
--   * environment-scoped secret references on provider accounts (secretref://fundzim/<environment>/...);
--   * per-provider sandbox enablement (sandbox_status), separate from production enablement (status);
--   * normalised provider error categories on provider calls;
--   * an append-only provider evidence log (every provider observation, never overwritten) with contradiction
--     detection, and the CONTRADICTORY_EVIDENCE reconciliation reason.
-- No ledger tables, no balances. Owner modules: psp (registry, accounts), payments (intents, attempts, evidence).
-- Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ---------------------------------------------------------------------------------------------------------
-- Provider registry: sandbox enablement is separate from production enablement. A provider SANDBOX can be
-- enabled before selection (sandbox certification precedes selection); production still needs SELECTED + ADR.
-- ---------------------------------------------------------------------------------------------------------
ALTER TABLE app.payment_providers ADD COLUMN sandbox_status text NOT NULL DEFAULT 'DISABLED';
ALTER TABLE app.payment_providers ADD CONSTRAINT ck_payment_providers_sandbox_status CHECK (sandbox_status IN ('DISABLED','ENABLED'));
UPDATE app.payment_providers SET sandbox_status = 'ENABLED' WHERE is_sandbox AND status = 'ENABLED';
COMMENT ON COLUMN app.payment_providers.sandbox_status IS 'Stage 9: whether sandbox execution (simulator or provider sandbox) may use this provider. Production uses status.';

-- ---------------------------------------------------------------------------------------------------------
-- Provider accounts: a secret reference must live under the account's own environment, so a SANDBOX account
-- can never point at a production credential (and vice versa). Secret-manager policies enforce the same split.
-- ---------------------------------------------------------------------------------------------------------
ALTER TABLE app.provider_accounts ADD CONSTRAINT ck_provider_accounts_secret_env CHECK (
      api_credentials_secret_ref LIKE 'secretref://fundzim/' || lower(environment) || '/%'
  AND (webhook_secret_ref IS NULL OR webhook_secret_ref LIKE 'secretref://fundzim/' || lower(environment) || '/%')
  AND (webhook_secret_ref_previous IS NULL OR webhook_secret_ref_previous LIKE 'secretref://fundzim/' || lower(environment) || '/%'));

-- ---------------------------------------------------------------------------------------------------------
-- Execution environment on donation intents and payments. Simulated ⇔ LOCAL_SIMULATOR. A PROVIDER_SANDBOX
-- payment is not simulated but is still a TEST payment: it never counts as a donation (Stage 10 posts only
-- PRODUCTION payments, which cannot exist yet).
-- ---------------------------------------------------------------------------------------------------------
ALTER TABLE app.donation_intents ADD COLUMN execution_environment text;
UPDATE app.donation_intents SET execution_environment = CASE WHEN is_simulated THEN 'LOCAL_SIMULATOR' ELSE 'PROVIDER_SANDBOX' END;
ALTER TABLE app.donation_intents ALTER COLUMN execution_environment SET NOT NULL;
ALTER TABLE app.donation_intents ADD CONSTRAINT ck_donation_intents_execution_environment
  CHECK (execution_environment IN ('LOCAL_SIMULATOR','PROVIDER_SANDBOX','PRODUCTION'));
ALTER TABLE app.donation_intents ADD CONSTRAINT ck_donation_intents_simulated_environment
  CHECK (is_simulated = (execution_environment = 'LOCAL_SIMULATOR'));
ALTER TABLE app.donation_intents ADD CONSTRAINT ck_donation_intents_no_production_stage9
  CHECK (execution_environment <> 'PRODUCTION');

ALTER TABLE app.payment_intents ADD COLUMN execution_environment text;
UPDATE app.payment_intents SET execution_environment = CASE WHEN is_simulated THEN 'LOCAL_SIMULATOR' ELSE 'PROVIDER_SANDBOX' END;
ALTER TABLE app.payment_intents ALTER COLUMN execution_environment SET NOT NULL;
ALTER TABLE app.payment_intents ADD CONSTRAINT ck_payment_intents_execution_environment
  CHECK (execution_environment IN ('LOCAL_SIMULATOR','PROVIDER_SANDBOX','PRODUCTION'));
ALTER TABLE app.payment_intents ADD CONSTRAINT ck_payment_intents_simulated_environment
  CHECK (is_simulated = (execution_environment = 'LOCAL_SIMULATOR'));
ALTER TABLE app.payment_intents ADD CONSTRAINT ck_payment_intents_no_production_stage9
  CHECK (execution_environment <> 'PRODUCTION');

CREATE FUNCTION app.execution_environment_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.execution_environment IS DISTINCT FROM OLD.execution_environment THEN
    RAISE EXCEPTION 'execution_environment is immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_donation_intents_env_immutable BEFORE UPDATE ON app.donation_intents
  FOR EACH ROW EXECUTE FUNCTION app.execution_environment_immutable();
CREATE TRIGGER trg_payment_intents_env_immutable BEFORE UPDATE ON app.payment_intents
  FOR EACH ROW EXECUTE FUNCTION app.execution_environment_immutable();

-- A payment's environment matches its donation intent's and its provider account's environment:
-- LOCAL_SIMULATOR and PROVIDER_SANDBOX use SANDBOX accounts; PRODUCTION uses PRODUCTION accounts only.
CREATE FUNCTION app.payment_intents_environment_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  donation_env text;
  account_env  text;
BEGIN
  SELECT execution_environment INTO donation_env FROM app.donation_intents WHERE id = NEW.donation_intent_id;
  IF donation_env IS DISTINCT FROM NEW.execution_environment THEN
    RAISE EXCEPTION 'payment execution environment differs from its donation intent' USING ERRCODE = 'check_violation';
  END IF;
  SELECT environment INTO account_env FROM app.provider_accounts WHERE id = NEW.provider_account_id;
  IF (NEW.execution_environment = 'PRODUCTION') <> (account_env = 'PRODUCTION') OR account_env = 'STAGING' THEN
    RAISE EXCEPTION 'payment execution environment % cannot use a % provider account', NEW.execution_environment, account_env
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payment_intents_environment_guard BEFORE INSERT OR UPDATE OF provider_account_id ON app.payment_intents
  FOR EACH ROW EXECUTE FUNCTION app.payment_intents_environment_guard();

-- ---------------------------------------------------------------------------------------------------------
-- Normalised provider error category on provider calls (set once, with the other result columns).
-- ---------------------------------------------------------------------------------------------------------
ALTER TABLE app.payment_attempts ADD COLUMN error_category text;
ALTER TABLE app.payment_attempts ADD CONSTRAINT ck_payment_attempts_error_category CHECK (error_category IS NULL OR error_category IN (
  'AUTHENTICATION_FAILED','INVALID_REQUEST','UNSUPPORTED_METHOD','UNSUPPORTED_CURRENCY','RATE_LIMITED','PROVIDER_UNAVAILABLE',
  'PROVIDER_TIMEOUT','UNKNOWN_OUTCOME','PAYMENT_DECLINED','RECONCILIATION_REQUIRED'));
DROP TRIGGER trg_payment_attempts_complete_once ON app.payment_attempts;
CREATE TRIGGER trg_payment_attempts_complete_once BEFORE UPDATE ON app.payment_attempts
  FOR EACH ROW EXECUTE FUNCTION app.attempt_complete_once('completed_at','classification','error_class','unknown_reason','http_status',
    'provider_status_raw','provider_reference_returned','latency_ms','error_category');

-- ---------------------------------------------------------------------------------------------------------
-- Reconciliation: a provider observation that contradicts earlier provider evidence (e.g. FAILED after
-- SUCCEEDED, or a different amount) is recorded and escalated, never silently discarded.
-- ---------------------------------------------------------------------------------------------------------
ALTER TABLE app.payment_reconciliation_references DROP CONSTRAINT ck_payment_reconciliation_references_reason;
ALTER TABLE app.payment_reconciliation_references ADD CONSTRAINT ck_payment_reconciliation_references_reason CHECK (reason IN (
  'UNKNOWN_HORIZON_EXCEEDED','NOT_FOUND_AFTER_HORIZON','AMOUNT_MISMATCH','CURRENCY_MISMATCH','ANOMALY','LATE_SUCCESS',
  'DUPLICATE_SUCCESS','PARKED_DEADLINE_EXCEEDED','UNMAPPED_STATUS','CONTRADICTORY_EVIDENCE'));

-- ---------------------------------------------------------------------------------------------------------
-- Provider evidence log: one immutable row per provider observation of a payment (synchronous response,
-- webhook, status query). payment_transactions keeps only the latest; this keeps all of them.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.payment_provider_evidence (
  id                     uuid        NOT NULL,
  payment_intent_id      uuid        NOT NULL,
  provider_id            uuid        NOT NULL,
  execution_environment  text        NOT NULL,
  source                 text        NOT NULL,
  payment_attempt_id     uuid,
  inbox_event_id         uuid,
  provider_event_id      text,
  provider_reference     text,
  raw_status             text        NOT NULL,
  mapped_status          text        NOT NULL,
  mapped                 boolean     NOT NULL,
  mapping_version        text        NOT NULL,
  reported_amount_minor  bigint,
  reported_currency      char(3),
  provider_occurred_at   timestamptz,
  observed_at            timestamptz NOT NULL,
  consistency            text        NOT NULL,
  contradicts_evidence_id uuid,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_provider_evidence PRIMARY KEY (id),
  CONSTRAINT fk_payment_provider_evidence_payment FOREIGN KEY (payment_intent_id, provider_id)
    REFERENCES app.payment_intents (id, provider_id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_provider_evidence_attempt FOREIGN KEY (payment_attempt_id) REFERENCES app.payment_attempts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_provider_evidence_inbox FOREIGN KEY (inbox_event_id) REFERENCES app.provider_webhook_inbox (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_provider_evidence_currency FOREIGN KEY (reported_currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_provider_evidence_contradicts FOREIGN KEY (contradicts_evidence_id) REFERENCES app.payment_provider_evidence (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payment_provider_evidence_environment CHECK (execution_environment IN ('LOCAL_SIMULATOR','PROVIDER_SANDBOX','PRODUCTION')),
  CONSTRAINT ck_payment_provider_evidence_source CHECK (source IN ('SYNC','WEBHOOK','POLL','RECON')),
  CONSTRAINT ck_payment_provider_evidence_mapped_status CHECK (mapped_status IN ('CREATED','PENDING','REQUIRES_ACTION','AUTHORISED','UNKNOWN',
    'SUCCEEDED','FAILED','EXPIRED','CANCELLED','PARTIALLY_REFUNDED','REFUNDED','DISPUTED','CHARGED_BACK')),
  CONSTRAINT ck_payment_provider_evidence_unmapped CHECK (mapped OR mapped_status = 'UNKNOWN'),
  CONSTRAINT ck_payment_provider_evidence_raw_status CHECK (char_length(raw_status) BETWEEN 1 AND 200),
  CONSTRAINT ck_payment_provider_evidence_amount CHECK ((reported_amount_minor IS NULL) = (reported_currency IS NULL)),
  CONSTRAINT ck_payment_provider_evidence_consistency CHECK (consistency IN ('FIRST','CONSISTENT','PROGRESSION','CONTRADICTS')),
  CONSTRAINT ck_payment_provider_evidence_contradiction CHECK ((consistency = 'CONTRADICTS') = (contradicts_evidence_id IS NOT NULL))
);
COMMENT ON TABLE app.payment_provider_evidence IS 'C2. Append-only log of every provider observation of a payment (Stage 9). Evidence, not balances: no settlement or ledger meaning.';
CREATE INDEX ix_payment_provider_evidence_payment ON app.payment_provider_evidence (payment_intent_id, observed_at, id);
CREATE UNIQUE INDEX uq_payment_provider_evidence_event ON app.payment_provider_evidence (payment_intent_id, provider_event_id)
  WHERE provider_event_id IS NOT NULL;
CREATE INDEX ix_payment_provider_evidence_contradicts ON app.payment_provider_evidence (observed_at DESC) WHERE consistency = 'CONTRADICTS';
CREATE TRIGGER trg_payment_provider_evidence_no_mutation BEFORE UPDATE OR DELETE ON app.payment_provider_evidence
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payment_provider_evidence_no_truncate BEFORE TRUNCATE ON app.payment_provider_evidence
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- The evidence environment always equals the payment's (a simulator answer can never be filed as provider evidence).
CREATE FUNCTION app.payment_provider_evidence_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM app.payment_intents p WHERE p.id = NEW.payment_intent_id
                   AND p.execution_environment = NEW.execution_environment) THEN
    RAISE EXCEPTION 'evidence execution environment differs from its payment' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payment_provider_evidence_guard BEFORE INSERT ON app.payment_provider_evidence
  FOR EACH ROW EXECUTE FUNCTION app.payment_provider_evidence_guard();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.payment_provider_evidence;
DROP FUNCTION IF EXISTS app.payment_provider_evidence_guard();
ALTER TABLE app.payment_reconciliation_references DROP CONSTRAINT ck_payment_reconciliation_references_reason;
ALTER TABLE app.payment_reconciliation_references ADD CONSTRAINT ck_payment_reconciliation_references_reason CHECK (reason IN (
  'UNKNOWN_HORIZON_EXCEEDED','NOT_FOUND_AFTER_HORIZON','AMOUNT_MISMATCH','CURRENCY_MISMATCH','ANOMALY','LATE_SUCCESS',
  'DUPLICATE_SUCCESS','PARKED_DEADLINE_EXCEEDED','UNMAPPED_STATUS')) NOT VALID;
DROP TRIGGER trg_payment_attempts_complete_once ON app.payment_attempts;
ALTER TABLE app.payment_attempts DROP COLUMN error_category;
CREATE TRIGGER trg_payment_attempts_complete_once BEFORE UPDATE ON app.payment_attempts
  FOR EACH ROW EXECUTE FUNCTION app.attempt_complete_once('completed_at','classification','error_class','unknown_reason','http_status',
    'provider_status_raw','provider_reference_returned','latency_ms');
DROP TRIGGER trg_payment_intents_environment_guard ON app.payment_intents;
DROP TRIGGER trg_payment_intents_env_immutable ON app.payment_intents;
DROP TRIGGER trg_donation_intents_env_immutable ON app.donation_intents;
DROP FUNCTION app.payment_intents_environment_guard();
DROP FUNCTION app.execution_environment_immutable();
ALTER TABLE app.payment_intents DROP COLUMN execution_environment;
ALTER TABLE app.donation_intents DROP COLUMN execution_environment;
ALTER TABLE app.provider_accounts DROP CONSTRAINT ck_provider_accounts_secret_env;
ALTER TABLE app.payment_providers DROP COLUMN sandbox_status;
-- +goose StatementEnd
