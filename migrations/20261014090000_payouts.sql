-- Stage 11 (ADR-043; design ADR-016, ADR-017, ADR-020; Stage 1 payout-lifecycle.md Y1–Y14 and
-- payout-eligibility-and-controls.md EC-01–EC-22; Stage 2 draft design/sql/0016_payouts.sql): withdrawal (payout)
-- requests, reservations, approvals, attempts, events, eligibility decisions, exceptions and reconciliation
-- references, plus the sandbox simulator's payout side.
--
-- Stage 11 is NON-PRODUCTION engineering. Every payout is a TEST payout executed only by the sandbox simulator:
--   * execution_environment PRODUCTION is refused (ck_payout_requests_no_production_stage11), test_payout must be
--     true and the provider must be a sandbox provider (composite FK on is_sandbox);
--   * the ledger environment recorded on every payout must be TEST (ADR-042: the ledger database is TEST);
--   * real execution is also disabled by the Go constant payouts.LiveExecutionEnabled = false and the seeded
--     kill switch payouts.live_execution.enabled (off).
-- Ledger effects are posted by the ledger module (fundzim_ledger role) under the PAYOUT_* rules with
-- source_system 'payouts' (added here; in a TEST ledger such journals must carry SIMULATED evidence).
-- Owner module: payouts (schema app) except app.sandbox_provider_payouts (psp) and the ledger CHECK changes
-- (ledger). Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

-- -----------------------------------------------------------------------------------------------------
-- 0. Ledger: payout journals get their own source system (ADR-043 §4).
-- -----------------------------------------------------------------------------------------------------
ALTER TABLE ledger.ledger_transactions DROP CONSTRAINT ck_ledger_transactions_source_system;
ALTER TABLE ledger.ledger_transactions ADD CONSTRAINT ck_ledger_transactions_source_system
  CHECK (source_system IN ('payments', 'payouts', 'reconciliation', 'staff', 'synthetic_fixture'));
ALTER TABLE ledger.ledger_transactions ADD CONSTRAINT ck_ledger_transactions_payouts_simulated_in_test
  CHECK (source_system <> 'payouts' OR environment <> 'TEST' OR evidence_class = 'SIMULATED');
ALTER TABLE ledger.ledger_source_references DROP CONSTRAINT ck_ledger_source_references_source_system;
ALTER TABLE ledger.ledger_source_references ADD CONSTRAINT ck_ledger_source_references_source_system
  CHECK (source_system IN ('payments', 'payouts', 'reconciliation', 'staff', 'synthetic_fixture'));

-- -----------------------------------------------------------------------------------------------------
-- 1. Keys other tables bind to.
-- -----------------------------------------------------------------------------------------------------
ALTER TABLE app.payout_destinations ADD CONSTRAINT uq_payout_destinations_id_currency_rail UNIQUE (id, currency, rail);
CREATE UNIQUE INDEX uq_provider_capabilities_payout_key ON app.provider_capabilities (id, provider_id, currency, routable, payout_api);

CREATE FUNCTION app.payout_status_rank(s text) RETURNS smallint LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT CASE s
    WHEN 'PAYOUT_REQUESTED' THEN 0 WHEN 'PENDING_REVIEW' THEN 10 WHEN 'APPROVED' THEN 20
    WHEN 'SUBMITTED' THEN 30 WHEN 'UNKNOWN' THEN 35 WHEN 'PROCESSING' THEN 40
    WHEN 'FAILED' THEN 50 WHEN 'REJECTED' THEN 50 WHEN 'CANCELLED' THEN 50
    WHEN 'COMPLETED' THEN 60 WHEN 'REVERSED' THEN 70 END::smallint
$$;

CREATE FUNCTION app.payout_policy_rank(p text) RETURNS integer LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT CASE p WHEN 'AUTO' THEN 0 WHEN 'SINGLE' THEN 1 WHEN 'DUAL' THEN 2 END
$$;

-- -----------------------------------------------------------------------------------------------------
-- 2. payout_requests — the withdrawal request (Stage 1 `payouts`). id = payout_id = our provider reference and
--    idempotency reference on every submission attempt, forever. Machine 'payout' (ADR-020 states).
--    PAYOUT_REQUESTED: the request passed request-time eligibility and claimed the single in-flight slot; the
--    reservation journal is being posted (ADR-043 §3). The reservation row exists from PENDING_REVIEW on.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.payout_requests (
  id                          uuid        NOT NULL,
  campaign_id                 uuid        NOT NULL,
  owner_user_id               uuid,
  owner_organisation_id       uuid,
  beneficiary_id              uuid,                     -- campaign beneficiary at request time (snapshot)
  currency                    char(3)     NOT NULL,
  amount_minor                bigint      NOT NULL,
  purpose                     text,
  requested_by                uuid        NOT NULL,
  requested_at                timestamptz NOT NULL,
  status                      text        NOT NULL,
  status_rank                 smallint    NOT NULL,
  approval_policy             text        NOT NULL,     -- SINGLE | DUAL in Stage 11 (AUTO disabled, PD-24); only raised
  approval_round              integer     NOT NULL DEFAULT 1,
  policy_version              text        NOT NULL,
  -- environment isolation (ADR-043 §1)
  execution_environment       text        NOT NULL,
  test_payout                 boolean     NOT NULL,
  ledger_environment          text        NOT NULL,
  -- destination snapshot: submission uses it; EC-07 compares versions
  destination_id              uuid        NOT NULL,
  destination_version         integer     NOT NULL,
  destination_hash            bytea       NOT NULL,
  destination_rail            text        NOT NULL,
  destination_masked          text        NOT NULL,
  destination_category        text        NOT NULL,
  -- provider binding (a sandbox provider with a payout capability)
  provider_id                 uuid        NOT NULL,
  provider_is_sandbox         boolean     NOT NULL,
  provider_account_id         uuid        NOT NULL,
  capability_id               uuid        NOT NULL,
  capability_routable         boolean     NOT NULL,
  capability_payout_api       boolean     NOT NULL,
  provider_payout_ref         text,
  provider_fee_minor          bigint,
  beneficiary_received_minor  bigint,
  reversed_amount_minor       bigint,
  -- unknown-outcome handling
  unknown_since               timestamptz,
  unknown_reason              text,
  next_poll_at                timestamptz,
  poll_attempts               integer     NOT NULL DEFAULT 0,
  needs_reconciliation        boolean     NOT NULL DEFAULT false,
  submitted_at                timestamptz,
  completed_at                timestamptz,
  failure_code                text,
  decision_reason_code        text,                     -- REJECTED / CANCELLED reason category (owner-safe)
  idempotency_scope           text        NOT NULL,
  idempotency_key             text        NOT NULL,
  correlation_id              text,
  version                     integer     NOT NULL DEFAULT 1,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_requests PRIMARY KEY (id),
  CONSTRAINT fk_payout_requests_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_owner_user FOREIGN KEY (owner_user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_owner_org FOREIGN KEY (owner_organisation_id) REFERENCES app.organisations (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_beneficiary FOREIGN KEY (beneficiary_id) REFERENCES app.beneficiaries (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_destination FOREIGN KEY (destination_id, currency, destination_rail)
    REFERENCES app.payout_destinations (id, currency, rail) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_provider FOREIGN KEY (provider_id, provider_is_sandbox) REFERENCES app.payment_providers (id, is_sandbox) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_provider_account FOREIGN KEY (provider_account_id) REFERENCES app.provider_accounts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_capability FOREIGN KEY (capability_id, provider_id, currency, capability_routable, capability_payout_api)
    REFERENCES app.provider_capabilities (id, provider_id, currency, routable, payout_api) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_requests_idempotency UNIQUE (idempotency_scope, idempotency_key),
  CONSTRAINT uq_payout_requests_provider_ref UNIQUE (provider_id, provider_payout_ref),
  CONSTRAINT uq_payout_requests_key UNIQUE (id, campaign_id, currency, amount_minor),
  CONSTRAINT ck_payout_requests_owner CHECK ((owner_user_id IS NULL) <> (owner_organisation_id IS NULL)),
  CONSTRAINT ck_payout_requests_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payout_requests_purpose CHECK (purpose IS NULL OR char_length(purpose) BETWEEN 1 AND 500),
  CONSTRAINT ck_payout_requests_status CHECK (status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED','SUBMITTED','PROCESSING','UNKNOWN',
    'COMPLETED','FAILED','REJECTED','CANCELLED','REVERSED')),
  CONSTRAINT ck_payout_requests_status_rank CHECK (status_rank = app.payout_status_rank(status)),
  CONSTRAINT ck_payout_requests_approval_policy CHECK (approval_policy IN ('SINGLE','DUAL')),
  CONSTRAINT ck_payout_requests_approval_round CHECK (approval_round >= 1),
  CONSTRAINT ck_payout_requests_environment CHECK (execution_environment IN ('LOCAL_SIMULATOR','PROVIDER_SANDBOX','PRODUCTION')),
  CONSTRAINT ck_payout_requests_no_production_stage11 CHECK (execution_environment <> 'PRODUCTION'),
  CONSTRAINT ck_payout_requests_test_only_stage11 CHECK (test_payout AND provider_is_sandbox),
  CONSTRAINT ck_payout_requests_ledger_environment CHECK (ledger_environment = 'TEST'),
  CONSTRAINT ck_payout_requests_destination_version CHECK (destination_version >= 1),
  CONSTRAINT ck_payout_requests_destination_hash CHECK (octet_length(destination_hash) = 32),
  CONSTRAINT ck_payout_requests_destination_category CHECK (destination_category IN ('BANK_ACCOUNT','MOBILE_MONEY_WALLET','PROVIDER_ACCOUNT','OTHER_APPROVED_DESTINATION')),
  CONSTRAINT ck_payout_requests_routable CHECK (capability_routable AND capability_payout_api),
  CONSTRAINT ck_payout_requests_fee CHECK (provider_fee_minor IS NULL OR provider_fee_minor >= 0),
  CONSTRAINT ck_payout_requests_received CHECK (beneficiary_received_minor IS NULL OR (beneficiary_received_minor > 0 AND beneficiary_received_minor <= amount_minor)),
  CONSTRAINT ck_payout_requests_reversed CHECK (reversed_amount_minor IS NULL OR (status = 'REVERSED' AND reversed_amount_minor > 0 AND reversed_amount_minor <= amount_minor)),
  CONSTRAINT ck_payout_requests_unknown_reason CHECK (unknown_reason IS NULL OR unknown_reason IN ('TIMEOUT','CONN_RESET_AFTER_SEND','HTTP_5XX',
    'MALFORMED_RESPONSE','SIGNATURE_INVALID_RESPONSE','CRASH_DURING_CREATE')),
  CONSTRAINT ck_payout_requests_unknown CHECK (status <> 'UNKNOWN' OR (unknown_since IS NOT NULL AND unknown_reason IS NOT NULL)),
  CONSTRAINT ck_payout_requests_poll CHECK (poll_attempts >= 0),
  CONSTRAINT ck_payout_requests_submitted CHECK (status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED','REJECTED','CANCELLED') OR submitted_at IS NOT NULL),
  CONSTRAINT ck_payout_requests_completed CHECK (status NOT IN ('COMPLETED','REVERSED') OR completed_at IS NOT NULL),
  CONSTRAINT ck_payout_requests_failed CHECK (status <> 'FAILED' OR failure_code IS NOT NULL),
  CONSTRAINT ck_payout_requests_decided CHECK (status NOT IN ('REJECTED','CANCELLED') OR decision_reason_code IS NOT NULL),
  CONSTRAINT ck_payout_requests_idempotency CHECK (char_length(idempotency_key) BETWEEN 8 AND 255),
  CONSTRAINT ck_payout_requests_version CHECK (version >= 1)
);
COMMENT ON TABLE app.payout_requests IS 'C2. Withdrawal (payout) requests; TEST payouts only in Stage 11 (ADR-043).';
-- EC-11 single in-flight rule (PD-23 default): at most one non-terminal payout per (campaign, currency).
CREATE UNIQUE INDEX uq_payout_requests_one_inflight ON app.payout_requests (campaign_id, currency)
  WHERE status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED','SUBMITTED','PROCESSING','UNKNOWN');
CREATE INDEX ix_payout_requests_campaign ON app.payout_requests (campaign_id, created_at DESC, id DESC);
CREATE INDEX ix_payout_requests_requester ON app.payout_requests (requested_by, created_at DESC);
CREATE INDEX ix_payout_requests_status ON app.payout_requests (status, created_at DESC, id DESC);
CREATE INDEX ix_payout_requests_submit_queue ON app.payout_requests (updated_at) WHERE status = 'APPROVED';
CREATE INDEX ix_payout_requests_poll ON app.payout_requests (next_poll_at) WHERE status IN ('SUBMITTED','PROCESSING','UNKNOWN');
CREATE INDEX ix_payout_requests_destination ON app.payout_requests (destination_id) WHERE status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout', '',                 'PAYOUT_REQUESTED'),   -- Y1 (request-time eligibility passed; reservation follows)
  ('payout', 'PAYOUT_REQUESTED', 'PENDING_REVIEW'),     -- Y2 (reservation journal posted)
  ('payout', 'PAYOUT_REQUESTED', 'REJECTED'),           -- Y4 (e.g. the ledger refused the reservation)
  ('payout', 'PENDING_REVIEW',   'REJECTED'),           -- Y4
  ('payout', 'PAYOUT_REQUESTED', 'CANCELLED'),          -- Y5
  ('payout', 'PENDING_REVIEW',   'CANCELLED'),          -- Y5
  ('payout', 'APPROVED',         'CANCELLED'),          -- Y5
  ('payout', 'PENDING_REVIEW',   'APPROVED'),           -- Y6
  ('payout', 'APPROVED',         'PENDING_REVIEW'),     -- Y7 (approvals invalidated: approval_round + 1)
  ('payout', 'APPROVED',         'REJECTED'),           -- pre-submission re-check FAIL (blocking condition)
  ('payout', 'APPROVED',         'SUBMITTED'),          -- Y8
  ('payout', 'SUBMITTED',        'PROCESSING'),         -- Y9
  ('payout', 'SUBMITTED',        'COMPLETED'),          -- Y10
  ('payout', 'PROCESSING',       'COMPLETED'),          -- Y10
  ('payout', 'UNKNOWN',          'COMPLETED'),          -- Y10
  ('payout', 'SUBMITTED',        'FAILED'),             -- Y11
  ('payout', 'PROCESSING',       'FAILED'),             -- Y11
  ('payout', 'UNKNOWN',          'FAILED'),             -- Y11 (authoritative final failure only)
  ('payout', 'SUBMITTED',        'UNKNOWN'),            -- Y12
  ('payout', 'PROCESSING',       'UNKNOWN'),            -- Y12
  ('payout', 'UNKNOWN',          'PROCESSING'),         -- Y13
  ('payout', 'COMPLETED',        'REVERSED');           -- Y14
-- Deliberately absent: PAYOUT_REQUESTED -> APPROVED (no AUTO tier in Stage 11), UNKNOWN -> SUBMITTED (never
-- resubmitted), anything out of FAILED / REJECTED / CANCELLED / REVERSED, COMPLETED -> anything but REVERSED.

CREATE TRIGGER trg_payout_requests_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout');
CREATE TRIGGER trg_payout_requests_updated_at BEFORE UPDATE ON app.payout_requests FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payout_requests_keep_created_at BEFORE UPDATE ON app.payout_requests FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_payout_requests_no_delete BEFORE DELETE ON app.payout_requests FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_requests_no_truncate BEFORE TRUNCATE ON app.payout_requests FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 3. payout_approvals — append-only decisions. Approver ≠ requester and ≠ the campaign owner or anyone linked to
--    them (app.actor_identities: staff ↔ personal account links), ≠ the people who last changed or verified the
--    destination; FINANCE only (I-20); one decision per approver per round; fresh step-up recorded.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.payout_approvals (
  id                  uuid        NOT NULL,
  payout_request_id   uuid        NOT NULL,
  approval_round      integer     NOT NULL,
  approver_id         uuid        NOT NULL,
  approver_role       text        NOT NULL,
  decision            text        NOT NULL,
  reason              text        NOT NULL,
  step_up_at          timestamptz NOT NULL,
  conflict_attested   boolean     NOT NULL,
  decided_at          timestamptz NOT NULL,
  expires_at          timestamptz,              -- NULL: no time-based validity configured (payout.approval_validity)
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_approvals PRIMARY KEY (id),
  CONSTRAINT fk_payout_approvals_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_approvals_approver FOREIGN KEY (approver_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_approvals_approver UNIQUE (payout_request_id, approval_round, approver_id),
  CONSTRAINT ck_payout_approvals_role CHECK (approver_role = 'FINANCE'),
  CONSTRAINT ck_payout_approvals_decision CHECK (decision IN ('APPROVE','REJECT')),
  CONSTRAINT ck_payout_approvals_reason CHECK (char_length(btrim(reason)) BETWEEN 5 AND 2000),
  CONSTRAINT ck_payout_approvals_attested CHECK (conflict_attested),
  CONSTRAINT ck_payout_approvals_step_up CHECK (step_up_at <= decided_at),
  CONSTRAINT ck_payout_approvals_expiry CHECK (expires_at IS NULL OR expires_at > decided_at)
);
CREATE INDEX ix_payout_approvals_approver ON app.payout_approvals (approver_id);

CREATE FUNCTION app.payout_approvals_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p app.payout_requests%ROWTYPE;
  d app.payout_destinations%ROWTYPE;
  ids uuid[];
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = NEW.payout_request_id FOR UPDATE;
  ids := app.actor_identities(NEW.approver_id);
  IF p.requested_by = ANY (ids) OR p.owner_user_id = ANY (ids) THEN
    RAISE EXCEPTION 'maker-checker: approver % is (or is linked to) the requester or campaign owner of payout %', NEW.approver_id, p.id
      USING ERRCODE = 'check_violation';
  END IF;
  SELECT * INTO d FROM app.payout_destinations WHERE id = p.destination_id;
  IF d.last_changed_by = ANY (ids) OR d.verified_by = ANY (ids) THEN
    RAISE EXCEPTION 'segregation of duties: approver changed or verified the payout destination' USING ERRCODE = 'check_violation';
  END IF;
  IF p.status <> 'PENDING_REVIEW' THEN
    RAISE EXCEPTION 'payout % is %, decisions are recorded only in PENDING_REVIEW', p.id, p.status USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.approval_round <> p.approval_round THEN
    RAISE EXCEPTION 'approval round % is not the current round % of payout %', NEW.approval_round, p.approval_round, p.id
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_approvals_guard BEFORE INSERT ON app.payout_approvals FOR EACH ROW EXECUTE FUNCTION app.payout_approvals_guard();
CREATE TRIGGER trg_payout_approvals_no_mutation BEFORE UPDATE OR DELETE ON app.payout_approvals FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_approvals_no_truncate BEFORE TRUNCATE ON app.payout_approvals FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 4. payout_eligibility_decisions — append-only evidence of every evaluation (REQUEST, PRE_SUBMIT, RECHECK),
--    including refused requests. results = the 22 checks EC-01..EC-22, each exactly once; fail closed.
-- -----------------------------------------------------------------------------------------------------
CREATE FUNCTION app.eligibility_results_valid(results jsonb, outcome text, phase text) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  n_ok     integer;
  n_fail   integer;
  n_review integer;
  ec22     text;
BEGIN
  IF results IS NULL OR jsonb_typeof(results) <> 'array' OR jsonb_array_length(results) <> 22 THEN
    RETURN false;
  END IF;
  IF EXISTS (SELECT 1 FROM jsonb_array_elements(results) e WHERE jsonb_typeof(e) <> 'object') THEN
    RETURN false;
  END IF;
  SELECT count(DISTINCT e->>'check_id'),
         count(*) FILTER (WHERE e->>'result' = 'FAIL'),
         count(*) FILTER (WHERE e->>'result' IN ('REVIEW','DEFER')),
         max(e->>'result') FILTER (WHERE e->>'check_id' = 'EC-22')
    INTO n_ok, n_fail, n_review, ec22
    FROM jsonb_array_elements(results) e
   WHERE e->>'check_id' ~ '^EC-(0[1-9]|1[0-9]|2[0-2])$'
     AND e->>'result' IN ('PASS','FAIL','REVIEW','DEFER','NOT_APPLICABLE');
  IF n_ok <> 22 THEN
    RETURN false;
  END IF;
  IF phase = 'PRE_SUBMIT' AND ec22 = 'NOT_APPLICABLE' THEN
    RETURN false;
  END IF;
  RETURN CASE
    WHEN n_fail > 0   THEN outcome = 'REJECTED'
    WHEN n_review > 0 THEN outcome IN ('REVIEW','DEFERRED')
    ELSE outcome = 'ELIGIBLE' END;
END $$;

CREATE TABLE app.payout_eligibility_decisions (
  id                 uuid        NOT NULL,
  payout_request_id  uuid,
  campaign_id        uuid        NOT NULL,
  currency           char(3)     NOT NULL,
  amount_minor       bigint      NOT NULL,
  requested_by       uuid        NOT NULL,
  phase              text        NOT NULL,
  evaluated_at       timestamptz NOT NULL,
  engine_version     text        NOT NULL,
  policy_version     text        NOT NULL,
  inputs             jsonb       NOT NULL,
  results            jsonb       NOT NULL,
  outcome            text        NOT NULL,
  approval_tier      text,
  request_id         text,
  correlation_id     text,
  record_hash        bytea       NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_eligibility_decisions PRIMARY KEY (id),
  CONSTRAINT fk_payout_eligibility_decisions_payout FOREIGN KEY (payout_request_id, campaign_id, currency, amount_minor)
    REFERENCES app.payout_requests (id, campaign_id, currency, amount_minor) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payout_eligibility_decisions_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_eligibility_decisions_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_eligibility_decisions_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_eligibility_decisions_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payout_eligibility_decisions_phase CHECK (phase IN ('REQUEST','PRE_SUBMIT','RECHECK')),
  CONSTRAINT ck_payout_eligibility_decisions_outcome CHECK (outcome IN ('ELIGIBLE','REVIEW','DEFERRED','REJECTED')),
  CONSTRAINT ck_payout_eligibility_decisions_tier CHECK (approval_tier IS NULL OR approval_tier IN ('SINGLE','DUAL')),
  CONSTRAINT ck_payout_eligibility_decisions_refused CHECK (payout_request_id IS NOT NULL OR (phase = 'REQUEST' AND outcome = 'REJECTED')),
  CONSTRAINT ck_payout_eligibility_decisions_results CHECK (app.eligibility_results_valid(results, outcome, phase)),
  CONSTRAINT ck_payout_eligibility_decisions_inputs CHECK (jsonb_typeof(inputs) = 'object'),
  CONSTRAINT ck_payout_eligibility_decisions_hash CHECK (octet_length(record_hash) = 32)
);
CREATE INDEX ix_payout_eligibility_decisions_payout ON app.payout_eligibility_decisions (payout_request_id, created_at) WHERE payout_request_id IS NOT NULL;
CREATE INDEX ix_payout_eligibility_decisions_campaign ON app.payout_eligibility_decisions (campaign_id, evaluated_at);
CREATE TRIGGER trg_payout_eligibility_decisions_no_mutation BEFORE UPDATE OR DELETE ON app.payout_eligibility_decisions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_eligibility_decisions_no_truncate BEFORE TRUNCATE ON app.payout_eligibility_decisions FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- APPROVED / SUBMITTED need the approvals of the current round; SUBMITTED needs an ELIGIBLE PRE_SUBMIT decision
-- written in the same transaction (fail closed, CLAUDE.md rule 12). Facts the approvals bind to are immutable.
CREATE FUNCTION app.payout_requests_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  required  integer;
  approvers integer;
  rejects   integer;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF (NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.requested_by, NEW.requested_at, NEW.destination_id, NEW.destination_version,
        NEW.destination_hash, NEW.destination_rail, NEW.destination_masked, NEW.destination_category, NEW.idempotency_scope, NEW.idempotency_key,
        NEW.policy_version, NEW.execution_environment, NEW.test_payout, NEW.ledger_environment, NEW.owner_user_id, NEW.owner_organisation_id,
        NEW.beneficiary_id, NEW.purpose)
       IS DISTINCT FROM
       (OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.requested_by, OLD.requested_at, OLD.destination_id, OLD.destination_version,
        OLD.destination_hash, OLD.destination_rail, OLD.destination_masked, OLD.destination_category, OLD.idempotency_scope, OLD.idempotency_key,
        OLD.policy_version, OLD.execution_environment, OLD.test_payout, OLD.ledger_environment, OLD.owner_user_id, OLD.owner_organisation_id,
        OLD.beneficiary_id, OLD.purpose) THEN
      RAISE EXCEPTION 'payout amount, currency, parties, destination snapshot and environment are immutable (approvals bind to them)'
        USING ERRCODE = 'restrict_violation';
    END IF;
    IF OLD.provider_payout_ref IS NOT NULL AND NEW.provider_payout_ref IS DISTINCT FROM OLD.provider_payout_ref THEN
      RAISE EXCEPTION 'provider_payout_ref is immutable once set' USING ERRCODE = 'restrict_violation';
    END IF;
    IF (NEW.provider_id, NEW.provider_account_id, NEW.capability_id) IS DISTINCT FROM (OLD.provider_id, OLD.provider_account_id, OLD.capability_id)
       AND OLD.status NOT IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED') THEN
      RAISE EXCEPTION 'payout provider is bound once submitted' USING ERRCODE = 'restrict_violation';
    END IF;
    IF app.payout_policy_rank(NEW.approval_policy) < app.payout_policy_rank(OLD.approval_policy) THEN
      RAISE EXCEPTION 'approval policy may only be raised (SINGLE < DUAL)' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.approval_round < OLD.approval_round THEN
      RAISE EXCEPTION 'approval_round never decreases' USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status = 'APPROVED' AND NEW.status = 'PENDING_REVIEW' AND NEW.approval_round <= OLD.approval_round THEN
      RAISE EXCEPTION 'APPROVED -> PENDING_REVIEW must start a new approval round' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.status IS NOT DISTINCT FROM OLD.status THEN
      RETURN NEW;
    END IF;
  END IF;
  IF NEW.status = 'APPROVED' OR (NEW.status = 'SUBMITTED' AND OLD.status = 'APPROVED') THEN
    required := CASE NEW.approval_policy WHEN 'SINGLE' THEN 1 ELSE 2 END;
    SELECT count(DISTINCT a.approver_id) FILTER (WHERE a.decision = 'APPROVE'), count(*) FILTER (WHERE a.decision = 'REJECT')
      INTO approvers, rejects
      FROM app.payout_approvals a
     WHERE a.payout_request_id = NEW.id AND a.approval_round = NEW.approval_round AND (a.expires_at IS NULL OR a.expires_at > now());
    IF rejects > 0 THEN
      RAISE EXCEPTION 'payout % has a REJECT decision in round %', NEW.id, NEW.approval_round USING ERRCODE = 'check_violation';
    END IF;
    IF approvers < required THEN
      RAISE EXCEPTION 'insufficient approvals for payout % (% of % required, policy %)', NEW.id, approvers, required, NEW.approval_policy
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NEW.status = 'SUBMITTED' THEN
    IF NOT EXISTS (SELECT 1 FROM app.payout_eligibility_decisions d
                    WHERE d.payout_request_id = NEW.id AND d.phase = 'PRE_SUBMIT' AND d.outcome = 'ELIGIBLE'
                      AND d.created_at = transaction_timestamp()) THEN
      RAISE EXCEPTION 'payout % cannot be SUBMITTED without an ELIGIBLE PRE_SUBMIT decision in the same transaction', NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_requests_guard BEFORE INSERT OR UPDATE ON app.payout_requests FOR EACH ROW EXECUTE FUNCTION app.payout_requests_guard();

-- -----------------------------------------------------------------------------------------------------
-- 5. payout_reservations — one per payout; links the ledger journals of the reserved money (cross-schema FKs to
--    ledger.ledger_transactions: the journal is committed by the ledger before the link is written).
--      ACTIVE   : payout:{id}:reserved posted (submit journal added at Y8: money in transit, still reserved)
--      BLOCKED  : the outcome is UNKNOWN or under reconciliation — cannot be released or consumed until resolved
--      CONSUMED : payout:{id}:completed posted (Y10)
--      RELEASED : payout:{id}:released (Y4/Y5) or payout:{id}:failed (Y11)
--      EXPIRED  : released because the request's validity window passed before submission
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.payout_reservations (
  id                      uuid        NOT NULL,
  payout_request_id       uuid        NOT NULL,
  campaign_id             uuid        NOT NULL,
  currency                char(3)     NOT NULL,
  amount_minor            bigint      NOT NULL,
  account_scope           text        NOT NULL,     -- the ledger account reserved from (liability:campaign_payable:{campaign})
  status                  text        NOT NULL,
  ledger_reserve_txn_id   uuid        NOT NULL,
  ledger_submit_txn_id    uuid,
  ledger_release_txn_id   uuid,
  ledger_consume_txn_id   uuid,
  expires_at              timestamptz,
  version                 integer     NOT NULL DEFAULT 1,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_reservations PRIMARY KEY (id),
  CONSTRAINT uq_payout_reservations_payout UNIQUE (payout_request_id),
  CONSTRAINT fk_payout_reservations_payout FOREIGN KEY (payout_request_id, campaign_id, currency, amount_minor)
    REFERENCES app.payout_requests (id, campaign_id, currency, amount_minor) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reservations_reserve FOREIGN KEY (ledger_reserve_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reservations_submit FOREIGN KEY (ledger_submit_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reservations_release FOREIGN KEY (ledger_release_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reservations_consume FOREIGN KEY (ledger_consume_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_reservations_reserve_txn UNIQUE (ledger_reserve_txn_id),
  CONSTRAINT ck_payout_reservations_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payout_reservations_scope CHECK (account_scope = 'liability:campaign_payable:' || campaign_id::text),
  CONSTRAINT ck_payout_reservations_status CHECK (status IN ('ACTIVE','BLOCKED','CONSUMED','RELEASED','EXPIRED')),
  CONSTRAINT ck_payout_reservations_released CHECK (status NOT IN ('RELEASED','EXPIRED') OR (ledger_release_txn_id IS NOT NULL AND ledger_consume_txn_id IS NULL)),
  CONSTRAINT ck_payout_reservations_consumed CHECK (status <> 'CONSUMED' OR (ledger_consume_txn_id IS NOT NULL AND ledger_submit_txn_id IS NOT NULL AND ledger_release_txn_id IS NULL)),
  CONSTRAINT ck_payout_reservations_open CHECK (status NOT IN ('ACTIVE','BLOCKED') OR (ledger_release_txn_id IS NULL AND ledger_consume_txn_id IS NULL)),
  CONSTRAINT ck_payout_reservations_expired CHECK (status <> 'EXPIRED' OR ledger_submit_txn_id IS NULL),
  CONSTRAINT ck_payout_reservations_distinct CHECK (
    ledger_submit_txn_id IS DISTINCT FROM ledger_reserve_txn_id AND ledger_release_txn_id IS DISTINCT FROM ledger_reserve_txn_id
    AND ledger_consume_txn_id IS DISTINCT FROM ledger_reserve_txn_id)
);
CREATE INDEX ix_payout_reservations_campaign ON app.payout_reservations (campaign_id, currency) WHERE status IN ('ACTIVE','BLOCKED');
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout_reservation', '',        'ACTIVE'),
  ('payout_reservation', 'ACTIVE',  'BLOCKED'),
  ('payout_reservation', 'BLOCKED', 'ACTIVE'),
  ('payout_reservation', 'ACTIVE',  'CONSUMED'),
  ('payout_reservation', 'BLOCKED', 'CONSUMED'),
  ('payout_reservation', 'ACTIVE',  'RELEASED'),
  ('payout_reservation', 'BLOCKED', 'RELEASED'),
  ('payout_reservation', 'ACTIVE',  'EXPIRED');

CREATE FUNCTION app.payout_reservations_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  ps text;
BEGIN
  IF (NEW.payout_request_id, NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.ledger_reserve_txn_id, NEW.account_scope)
     IS DISTINCT FROM (OLD.payout_request_id, OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.ledger_reserve_txn_id, OLD.account_scope)
     OR (OLD.ledger_submit_txn_id  IS NOT NULL AND NEW.ledger_submit_txn_id  IS DISTINCT FROM OLD.ledger_submit_txn_id)
     OR (OLD.ledger_release_txn_id IS NOT NULL AND NEW.ledger_release_txn_id IS DISTINCT FROM OLD.ledger_release_txn_id)
     OR (OLD.ledger_consume_txn_id IS NOT NULL AND NEW.ledger_consume_txn_id IS DISTINCT FROM OLD.ledger_consume_txn_id) THEN
    RAISE EXCEPTION 'payout reservation facts and journal links are set once' USING ERRCODE = 'restrict_violation';
  END IF;
  -- never released while the provider outcome is unknown or in flight (brief §18, §36)
  IF NEW.status IN ('RELEASED','EXPIRED') AND OLD.status IS DISTINCT FROM NEW.status THEN
    SELECT status INTO ps FROM app.payout_requests WHERE id = NEW.payout_request_id;
    IF ps NOT IN ('REJECTED','CANCELLED','FAILED') THEN
      RAISE EXCEPTION 'reservation of payout % (status %) cannot be released', NEW.payout_request_id, ps USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NEW.status = 'CONSUMED' AND OLD.status IS DISTINCT FROM NEW.status THEN
    SELECT status INTO ps FROM app.payout_requests WHERE id = NEW.payout_request_id;
    IF ps NOT IN ('COMPLETED','REVERSED') THEN
      RAISE EXCEPTION 'reservation of payout % (status %) cannot be consumed without authoritative completion', NEW.payout_request_id, ps
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_reservations_guard BEFORE UPDATE ON app.payout_reservations FOR EACH ROW EXECUTE FUNCTION app.payout_reservations_guard();
CREATE TRIGGER trg_payout_reservations_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_reservations
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout_reservation');
CREATE TRIGGER trg_payout_reservations_updated_at BEFORE UPDATE ON app.payout_reservations FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payout_reservations_no_delete BEFORE DELETE ON app.payout_reservations FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_reservations_no_truncate BEFORE TRUNCATE ON app.payout_reservations FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 6. payout_attempts — every provider call, committed IN_FLIGHT before the call. our_reference is ALWAYS the
--    payout id. CREATE_PAYOUT only while SUBMITTED, only after the in-transit journal, and only if no earlier
--    CREATE_PAYOUT may have reached the provider ("an UNKNOWN payout is never resubmitted" as a database rule).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.payout_attempts (
  id                          uuid        NOT NULL,
  payout_request_id           uuid        NOT NULL,
  attempt_no                  integer     NOT NULL,
  operation                   text        NOT NULL,
  provider_id                 uuid        NOT NULL,
  our_reference               uuid        NOT NULL,
  correlation_id              text        NOT NULL,
  started_at                  timestamptz NOT NULL,
  completed_at                timestamptz,
  classification              text        NOT NULL DEFAULT 'IN_FLIGHT',
  unknown_reason              text,
  http_status                 smallint,
  provider_status_raw         text,
  provider_reference_returned text,
  failure_code                text,
  latency_ms                  integer,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_attempts PRIMARY KEY (id),
  CONSTRAINT fk_payout_attempts_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_attempts_provider FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_attempts_no UNIQUE (payout_request_id, attempt_no),
  CONSTRAINT ck_payout_attempts_no CHECK (attempt_no >= 1),
  CONSTRAINT ck_payout_attempts_operation CHECK (operation IN ('CREATE_PAYOUT','GET_STATUS','CANCEL')),
  CONSTRAINT ck_payout_attempts_reference CHECK (our_reference = payout_request_id),
  CONSTRAINT ck_payout_attempts_classification CHECK (classification IN ('IN_FLIGHT','ACCEPTED','DEFINITE_FAILURE','DEFINITELY_NOT_SENT',
    'OUTCOME_UNKNOWN','NOT_FOUND','AUTH_CONFIG_ERROR','NOT_SUPPORTED','RATE_LIMITED','INVALID_REQUEST')),
  CONSTRAINT ck_payout_attempts_unknown_reason CHECK (
    (classification = 'OUTCOME_UNKNOWN') = (unknown_reason IS NOT NULL)
    AND (unknown_reason IS NULL OR unknown_reason IN ('TIMEOUT','CONN_RESET_AFTER_SEND','HTTP_5XX','MALFORMED_RESPONSE',
      'SIGNATURE_INVALID_RESPONSE','CRASH_DURING_CREATE'))),
  CONSTRAINT ck_payout_attempts_completed CHECK ((classification = 'IN_FLIGHT') = (completed_at IS NULL)),
  CONSTRAINT ck_payout_attempts_latency CHECK (latency_ms IS NULL OR latency_ms >= 0)
);
CREATE INDEX ix_payout_attempts_in_flight ON app.payout_attempts (started_at) WHERE classification = 'IN_FLIGHT';

CREATE FUNCTION app.payout_attempts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p app.payout_requests%ROWTYPE;
  submit_txn uuid;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = NEW.payout_request_id FOR UPDATE;
  IF NEW.provider_id <> p.provider_id THEN
    RAISE EXCEPTION 'payout attempt provider differs from the bound provider' USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.operation = 'CREATE_PAYOUT' THEN
    IF p.status <> 'SUBMITTED' THEN
      RAISE EXCEPTION 'payout resubmission forbidden: CREATE_PAYOUT only while SUBMITTED (payout % is %)', p.id, p.status
        USING ERRCODE = 'check_violation';
    END IF;
    SELECT ledger_submit_txn_id INTO submit_txn FROM app.payout_reservations WHERE payout_request_id = p.id;
    IF submit_txn IS NULL THEN
      RAISE EXCEPTION 'payout % has no in-transit journal: the provider must not be called before it commits', p.id
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM app.payout_attempts a WHERE a.payout_request_id = p.id AND a.operation = 'CREATE_PAYOUT'
                  AND a.classification <> 'DEFINITELY_NOT_SENT') THEN
      RAISE EXCEPTION 'payout resubmission forbidden: an earlier CREATE_PAYOUT for % may have reached the provider', p.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_attempts_guard BEFORE INSERT ON app.payout_attempts FOR EACH ROW EXECUTE FUNCTION app.payout_attempts_guard();
CREATE TRIGGER trg_payout_attempts_complete_once BEFORE UPDATE ON app.payout_attempts
  FOR EACH ROW EXECUTE FUNCTION app.attempt_complete_once('completed_at','classification','unknown_reason','http_status','provider_status_raw',
    'provider_reference_returned','failure_code','latency_ms');
CREATE TRIGGER trg_payout_attempts_no_delete BEFORE DELETE ON app.payout_attempts FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_attempts_no_truncate BEFORE TRUNCATE ON app.payout_attempts FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 7. payout_events — append-only history (applied transitions and non-applied provider observations).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.payout_events (
  id                       uuid        NOT NULL,
  payout_request_id        uuid        NOT NULL,
  event_type               text        NOT NULL,
  from_status              text,
  to_status                text,
  applied                  boolean     NOT NULL,
  not_applied_reason       text,
  transition_code          text,
  source                   text        NOT NULL,
  provider_event_id        text,
  inbox_event_id           uuid,
  payout_attempt_id        uuid,
  eligibility_decision_id  uuid,
  raw_status               text,
  actor_type               text        NOT NULL,
  actor_id                 uuid,
  reason                   text,
  evidence_record_id       uuid,
  ledger_transaction_ids   uuid[]      NOT NULL DEFAULT '{}',
  correlation_id           text,
  occurred_at              timestamptz,
  recorded_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_events PRIMARY KEY (id),
  CONSTRAINT fk_payout_events_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payout_events_inbox FOREIGN KEY (inbox_event_id) REFERENCES app.provider_webhook_inbox (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_events_attempt FOREIGN KEY (payout_attempt_id) REFERENCES app.payout_attempts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_events_decision FOREIGN KEY (eligibility_decision_id) REFERENCES app.payout_eligibility_decisions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_events_actor FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_events_evidence FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_events_type CHECK (event_type IN ('WITHDRAWAL_REQUESTED','ELIGIBILITY_CHECKED','RESERVATION_CREATED','APPROVAL_REQUESTED',
    'APPROVAL_GRANTED','APPROVAL_REJECTED','APPROVALS_INVALIDATED','DESTINATION_CHANGED','PAYOUT_SUBMITTED_SANDBOX','PROVIDER_RESPONSE_RECEIVED',
    'PAYOUT_STATUS_CHANGED','PAYOUT_UNKNOWN','RECONCILIATION_REQUIRED','RESERVATION_RELEASED','RESERVATION_CONSUMED','RESERVATION_BLOCKED',
    'RESERVATION_EXPIRED','WITHDRAWAL_CANCELLED','WITHDRAWAL_REJECTED','LEDGER_POSTED','DUPLICATE_SUBMISSION_BLOCKED')),
  CONSTRAINT ck_payout_events_status_pair CHECK ((from_status IS NULL OR to_status IS NOT NULL)),
  CONSTRAINT ck_payout_events_source CHECK (source IN ('OWNER','STAFF','SYSTEM','SYNC','WEBHOOK','POLL','RECON')),
  CONSTRAINT ck_payout_events_actor_type CHECK (actor_type IN ('SYSTEM','OWNER','STAFF','PROVIDER')),
  CONSTRAINT ck_payout_events_not_applied CHECK (
    (applied AND not_applied_reason IS NULL)
    OR (NOT applied AND not_applied_reason IN ('LOWER_RANK','NOT_AN_EDGE','ANOMALY','DUPLICATE','TERMINAL'))),
  CONSTRAINT ck_payout_events_transition_code CHECK (transition_code IS NULL OR transition_code ~ '^Y([1-9]|1[0-4])$'),
  CONSTRAINT ck_payout_events_staff CHECK (source <> 'STAFF' OR (actor_type = 'STAFF' AND actor_id IS NOT NULL AND reason IS NOT NULL)),
  CONSTRAINT ck_payout_events_owner CHECK (source <> 'OWNER' OR (actor_type = 'OWNER' AND actor_id IS NOT NULL)),
  -- completion and processing only from authoritative provider state (Y9, Y10, Y14)
  CONSTRAINT ck_payout_events_completed_source CHECK (NOT applied OR to_status IS NULL OR to_status NOT IN ('COMPLETED','REVERSED','PROCESSING')
    OR source IN ('SYNC','WEBHOOK','POLL','RECON')),
  -- UNKNOWN only from our own indeterminate call, query or crash recovery (Y12)
  CONSTRAINT ck_payout_events_unknown_system CHECK (NOT applied OR to_status IS DISTINCT FROM 'UNKNOWN' OR source = 'SYSTEM'),
  -- after submission the owner drives nothing
  CONSTRAINT ck_payout_events_owner_scope CHECK (NOT applied OR source <> 'OWNER' OR to_status IS NULL OR to_status IN ('PAYOUT_REQUESTED','CANCELLED')),
  -- FAILED never from SYSTEM (never by assumption); a staff-declared FAILED needs an evidence record
  CONSTRAINT ck_payout_events_failed_source CHECK (NOT applied OR to_status IS DISTINCT FROM 'FAILED' OR source IN ('SYNC','WEBHOOK','POLL','RECON','STAFF')),
  CONSTRAINT ck_payout_events_staff_failed_evidence CHECK (NOT applied OR to_status IS DISTINCT FROM 'FAILED' OR source <> 'STAFF' OR evidence_record_id IS NOT NULL)
);
CREATE INDEX ix_payout_events_payout ON app.payout_events (payout_request_id, recorded_at);
CREATE UNIQUE INDEX uq_payout_events_provider_event ON app.payout_events (payout_request_id, provider_event_id) WHERE provider_event_id IS NOT NULL AND applied;
CREATE TRIGGER trg_payout_events_no_mutation BEFORE UPDATE OR DELETE ON app.payout_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_events_no_truncate BEFORE TRUNCATE ON app.payout_events FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- every status change is accompanied by an applied event in the same transaction (deferred check)
CREATE FUNCTION app.require_payout_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  f  text;
  ok boolean;
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.status IS NOT DISTINCT FROM OLD.status THEN
    RETURN NULL;
  END IF;
  f := CASE WHEN TG_OP = 'INSERT' THEN '' ELSE OLD.status END;
  SELECT EXISTS (SELECT 1 FROM app.payout_events e WHERE e.payout_request_id = NEW.id AND COALESCE(e.from_status, '') = f
                    AND e.to_status = NEW.status AND e.applied AND e.recorded_at = transaction_timestamp()) INTO ok;
  IF NOT ok THEN
    RAISE EXCEPTION 'missing payout_events row for payout % transition "%" -> "%"', NEW.id, f, NEW.status
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_payout_requests_require_event AFTER INSERT OR UPDATE OF status ON app.payout_requests
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_payout_event();

-- -----------------------------------------------------------------------------------------------------
-- 8. payout_reconciliation_references (immutable detections) and payout_exceptions (the operations queue, with an
--    append-only resolution history).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.payout_reconciliation_references (
  id                    uuid        NOT NULL,
  payout_request_id     uuid,
  mismatch_type         text        NOT NULL,
  expected_amount_minor bigint,
  actual_amount_minor   bigint,
  currency              char(3),
  provider_reference    text,
  ledger_transaction_id uuid,
  detail                jsonb       NOT NULL DEFAULT '{}'::jsonb,
  detected_at           timestamptz NOT NULL,
  detected_by_run       text        NOT NULL,
  CONSTRAINT pk_payout_reconciliation_references PRIMARY KEY (id),
  CONSTRAINT fk_payout_recon_refs_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_recon_refs_type CHECK (mismatch_type IN ('MISSING_PROVIDER_CONFIRMATION','DUPLICATE_PAYOUT_REFERENCE','AMOUNT_MISMATCH',
    'CURRENCY_MISMATCH','UNKNOWN_OUTCOME','RESERVATION_MISMATCH','LEDGER_MISMATCH','PROVIDER_STATUS_MISMATCH')),
  CONSTRAINT ck_payout_recon_refs_detail CHECK (jsonb_typeof(detail) = 'object')
);
CREATE INDEX ix_payout_recon_refs_payout ON app.payout_reconciliation_references (payout_request_id, detected_at);
CREATE TRIGGER trg_payout_recon_refs_no_mutation BEFORE UPDATE OR DELETE ON app.payout_reconciliation_references FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_recon_refs_no_truncate BEFORE TRUNCATE ON app.payout_reconciliation_references FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.payout_exceptions (
  id                          uuid        NOT NULL,
  payout_request_id           uuid,
  campaign_id                 uuid,
  exception_type              text        NOT NULL,
  severity                    text        NOT NULL,
  source                      text        NOT NULL,
  status                      text        NOT NULL,
  owner_id                    uuid,
  reconciliation_reference_id uuid,
  evidence                    jsonb       NOT NULL DEFAULT '{}'::jsonb,
  resolution_code             text,
  detected_at                 timestamptz NOT NULL,
  resolved_at                 timestamptz,
  version                     integer     NOT NULL DEFAULT 1,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_exceptions PRIMARY KEY (id),
  CONSTRAINT fk_payout_exceptions_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_exceptions_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_exceptions_owner FOREIGN KEY (owner_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_exceptions_recon FOREIGN KEY (reconciliation_reference_id) REFERENCES app.payout_reconciliation_references (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_exceptions_type CHECK (exception_type IN ('UNKNOWN_PROVIDER_OUTCOME','DESTINATION_MISMATCH','INSUFFICIENT_ELIGIBLE_FUNDS',
    'COMPLIANCE_HOLD','APPROVAL_EXPIRED','PROVIDER_UNAVAILABLE','RECONCILIATION_MISMATCH','DUPLICATE_SUBMISSION_ATTEMPT','LEDGER_POSTING_FAILED',
    'LATE_COMPLETION_AFTER_FAILURE','PAYOUT_RETURNED')),
  CONSTRAINT ck_payout_exceptions_severity CHECK (severity IN ('SEV1','SEV2','SEV3')),
  CONSTRAINT ck_payout_exceptions_source CHECK (source IN ('ORCHESTRATION','ELIGIBILITY','WEBHOOK','RECONCILIATION','LEDGER')),
  CONSTRAINT ck_payout_exceptions_status CHECK (status IN ('OPEN','INVESTIGATING','RESOLVED')),
  CONSTRAINT ck_payout_exceptions_evidence CHECK (jsonb_typeof(evidence) = 'object'),
  CONSTRAINT ck_payout_exceptions_resolved CHECK ((status = 'RESOLVED') = (resolved_at IS NOT NULL AND resolution_code IS NOT NULL)),
  CONSTRAINT ck_payout_exceptions_owner_set CHECK (status <> 'INVESTIGATING' OR owner_id IS NOT NULL),
  CONSTRAINT ck_payout_exceptions_subject CHECK (payout_request_id IS NOT NULL OR campaign_id IS NOT NULL)
);
-- one unresolved exception per (type, payout)
CREATE UNIQUE INDEX uq_payout_exceptions_open ON app.payout_exceptions (exception_type, payout_request_id) WHERE status <> 'RESOLVED' AND payout_request_id IS NOT NULL;
CREATE INDEX ix_payout_exceptions_queue ON app.payout_exceptions (status, severity, detected_at);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout_exception', '',              'OPEN'),
  ('payout_exception', 'OPEN',          'INVESTIGATING'),
  ('payout_exception', 'OPEN',          'RESOLVED'),
  ('payout_exception', 'INVESTIGATING', 'RESOLVED');
CREATE FUNCTION app.payout_exceptions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.payout_request_id, NEW.campaign_id, NEW.exception_type, NEW.severity, NEW.source, NEW.reconciliation_reference_id, NEW.evidence, NEW.detected_at)
     IS DISTINCT FROM (OLD.payout_request_id, OLD.campaign_id, OLD.exception_type, OLD.severity, OLD.source, OLD.reconciliation_reference_id,
                       OLD.evidence, OLD.detected_at) THEN
    RAISE EXCEPTION 'payout exception detection fields are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_exceptions_guard BEFORE UPDATE ON app.payout_exceptions FOR EACH ROW EXECUTE FUNCTION app.payout_exceptions_guard();
CREATE TRIGGER trg_payout_exceptions_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_exceptions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout_exception');
CREATE TRIGGER trg_payout_exceptions_updated_at BEFORE UPDATE ON app.payout_exceptions FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payout_exceptions_no_delete BEFORE DELETE ON app.payout_exceptions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_exceptions_no_truncate BEFORE TRUNCATE ON app.payout_exceptions FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.payout_exception_events (
  id                  uuid        NOT NULL,
  payout_exception_id uuid        NOT NULL,
  from_status         text,
  to_status           text        NOT NULL,
  actor_type          text        NOT NULL,
  actor_id            uuid,
  note                text,
  recorded_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_exception_events PRIMARY KEY (id),
  CONSTRAINT fk_payout_exception_events_exception FOREIGN KEY (payout_exception_id) REFERENCES app.payout_exceptions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_exception_events_actor FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_exception_events_actor CHECK (actor_type IN ('SYSTEM','STAFF') AND (actor_type = 'SYSTEM' OR actor_id IS NOT NULL)),
  CONSTRAINT ck_payout_exception_events_note CHECK (note IS NULL OR char_length(note) <= 2000)
);
CREATE INDEX ix_payout_exception_events_exception ON app.payout_exception_events (payout_exception_id, recorded_at);
CREATE TRIGGER trg_payout_exception_events_no_mutation BEFORE UPDATE OR DELETE ON app.payout_exception_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_exception_events_no_truncate BEFORE TRUNCATE ON app.payout_exception_events FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 9. Sandbox simulator: the simulated provider's payouts (psp-owned, like sandbox_provider_payments) and payout
--    webhooks in the existing schedule. Synthetic destinations only: the simulator stores a masked reference.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.sandbox_provider_payouts (
  id                   uuid        NOT NULL,
  provider_id          uuid        NOT NULL,
  is_sandbox           boolean     NOT NULL DEFAULT true,
  provider_payout_ref  text        NOT NULL,
  our_reference        uuid        NOT NULL,
  rail                 text        NOT NULL,
  destination_masked   text        NOT NULL,
  currency             char(3)     NOT NULL,
  amount_minor         bigint      NOT NULL,
  scenario             text        NOT NULL,
  raw_status           text        NOT NULL,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_sandbox_provider_payouts PRIMARY KEY (id),
  CONSTRAINT fk_sandbox_provider_payouts_provider FOREIGN KEY (provider_id, is_sandbox) REFERENCES app.payment_providers (id, is_sandbox) ON DELETE RESTRICT,
  CONSTRAINT uq_sandbox_provider_payouts_ref UNIQUE (provider_id, provider_payout_ref),
  CONSTRAINT uq_sandbox_provider_payouts_our_ref UNIQUE (provider_id, our_reference),
  CONSTRAINT ck_sandbox_provider_payouts_sandbox CHECK (is_sandbox),
  CONSTRAINT ck_sandbox_provider_payouts_amount CHECK (amount_minor > 0)
);
COMMENT ON TABLE app.sandbox_provider_payouts IS 'C1. Sandbox SIMULATOR provider-side payout state (never real). ADR-043.';
CREATE TRIGGER trg_sandbox_provider_payouts_updated_at BEFORE UPDATE ON app.sandbox_provider_payouts FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

ALTER TABLE app.sandbox_scheduled_webhooks ALTER COLUMN sandbox_payment_id DROP NOT NULL;
ALTER TABLE app.sandbox_scheduled_webhooks ADD COLUMN sandbox_payout_id uuid;
ALTER TABLE app.sandbox_scheduled_webhooks ADD CONSTRAINT fk_sandbox_scheduled_webhooks_payout
  FOREIGN KEY (sandbox_payout_id) REFERENCES app.sandbox_provider_payouts (id) ON DELETE RESTRICT;
ALTER TABLE app.sandbox_scheduled_webhooks ADD CONSTRAINT ck_sandbox_scheduled_webhooks_subject
  CHECK ((sandbox_payment_id IS NULL) <> (sandbox_payout_id IS NULL));

-- The simulator's payout capability (sandbox contract facts, never statements about a real rail): version 2 of
-- the ECOCASH/USD row with the same collection flags plus payouts to three synthetic rails.
INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
  refund_api, partial_refund, idempotent_create, supports_cancel, payout_api, payout_rails, signed_webhooks, status_api, documented_expiry,
  documented_expiry_seconds, statement_format, evidence_status, evidence_source, recorded_by) VALUES
  (md5('provider_capability:sandbox:ECOCASH:USD:2')::uuid, md5('payment_provider:sandbox')::uuid, 2, 'ECOCASH', 'USD', 'USD', 'PSP_POOL',
   true, true, true, true, true, ARRAY['ECOCASH','ONEMONEY','BANK_TRANSFER'], true, true, false, NULL, 'API', 'VERIFIED',
   'sandbox contract: docs/stage-8/provider-interface.md §6; payouts docs/stage-11/payout-simulator.md', '00000000-0000-0000-0000-000000000001');

-- Kill switch for real payout execution (off; payouts.LiveExecutionEnabled is a constant false).
INSERT INTO app.feature_flags (id, key, description, flag_type, enabled, requires_approval, legal_ref) VALUES
  (md5('feature_flag:payouts.live_execution.enabled')::uuid, 'payouts.live_execution.enabled',
   'Kill switch for real payout execution. Off: no payout provider is confirmed, no settlement evidence exists and legal sign-off is missing.',
   'KILL_SWITCH', false, true, 'LR-001, LR-003, LR-004, LR-020, LR-024, LR-030');
INSERT INTO app.feature_flag_changes (id, feature_flag_id, flag_version, old_enabled, new_enabled, requested_by, approved_by, reason, occurred_at) VALUES
  (md5('feature_flag_change:payouts.live_execution.enabled:1')::uuid, md5('feature_flag:payouts.live_execution.enabled')::uuid, 1, NULL, false,
   '00000000-0000-0000-0000-000000000001', NULL, 'seeded by migration (Stage 11, ADR-043)', now());

-- -----------------------------------------------------------------------------------------------------
-- 10. Permissions (payout.approve / payout.reject / payout.hold exist since Stage 4).
-- -----------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:payout.view')::uuid, 'payout.view', 'View withdrawal requests, payouts, reservations, exceptions and payout reconciliation', true, false),
  (md5('permission:payout.operate')::uuid, 'payout.operate', 'Operate payouts: status queries, exception workflow, staff cancellation, reconciliation runs', true, true),
  (md5('permission:payout.sandbox.control')::uuid, 'payout.sandbox.control', 'Drive the payout simulator (development and test environments only)', false, false);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES ('FINANCE', 'payout.view'), ('COMPLIANCE', 'payout.view'), ('FINANCE', 'payout.operate'), ('ADMIN', 'payout.sandbox.control')) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

-- -----------------------------------------------------------------------------------------------------
-- 11. Runtime grants: the Stage 10 procedure plus EXECUTE on the payout CHECK helpers for the app role.
-- -----------------------------------------------------------------------------------------------------
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly, fundzim_ledger';
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles'];
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
  -- Stage 10: the ledger schema belongs to fundzim_ledger alone (fundzim_app has no ledger privileges at all).
  ledger_reference constant text[] := ARRAY['ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines','ledger.ledger_environment'];
  s text;
  i int;
  r record;
  fq text;
  privs text;
BEGIN
  FOREACH s IN ARRAY schemas LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('REVOKE ALL ON SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL ROUTINES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
  END LOOP;

  -- fundzim_app: app, queue, audit, risk, recon. Never kyc, compliance or ledger (Stage 10).
  FOREACH s IN ARRAY ARRAY['app','queue','audit','risk','recon'] LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO fundzim_app', s);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO fundzim_app', s);
  END LOOP;

  FOR r IN
    SELECT n.nspname AS sch, c.relname AS tbl, c.relkind,
           format('%I.%I', n.nspname, c.relname) AS rel,
           coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update,
           coalesce(bool_or((t.tgtype & 8)  <> 0), false) AS no_delete
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                            AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
     WHERE n.nspname IN ('app','queue','audit','risk','recon')
       AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
     GROUP BY n.nspname, c.relname, c.relkind
  LOOP
    fq := r.sch || '.' || r.tbl;
    IF r.relkind IN ('v','m') THEN
      IF r.tbl NOT LIKE 'ro\_%' THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_app', r.rel);
      END IF;
      CONTINUE;
    END IF;
    IF fq = ANY (reference_only) THEN
      privs := 'SELECT';
    ELSIF r.sch = 'queue' THEN
      privs := 'SELECT, INSERT, UPDATE, DELETE';
    ELSIF r.sch = 'audit' THEN
      privs := 'SELECT, INSERT';
      -- legal_hold is the only mutable evidence column (trigger-checked)
      IF fq = 'audit.evidence_records' THEN privs := privs || ', UPDATE'; END IF;
    ELSE                                                        -- app, risk, recon
      privs := 'SELECT, INSERT';
      IF NOT r.no_update THEN privs := privs || ', UPDATE'; END IF;
      IF fq = ANY (app_delete_ok) AND NOT r.no_delete THEN privs := privs || ', DELETE'; END IF;
    END IF;
    EXECUTE format('GRANT %s ON %s TO fundzim_app', privs, r.rel);
  END LOOP;

  IF to_regnamespace('queue') IS NOT NULL THEN
    EXECUTE 'GRANT EXECUTE ON ALL ROUTINES IN SCHEMA queue TO fundzim_app';
    FOR r IN SELECT t.oid::regtype AS typ FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
              WHERE n.nspname = 'queue' AND t.typtype IN ('e','d','c') AND t.typrelid = 0 LOOP
      EXECUTE format('GRANT USAGE ON TYPE %s TO fundzim_app', r.typ);
    END LOOP;
  END IF;

  -- restricted roles: their own schema, what shared triggers read, and the gateways
  GRANT USAGE ON SCHEMA app TO fundzim_kyc, fundzim_compliance;
  GRANT SELECT ON app.status_transitions TO fundzim_kyc, fundzim_compliance;
  GRANT USAGE ON SCHEMA audit TO fundzim_kyc, fundzim_compliance;
  FOR i IN 1 .. array_length(restricted, 1) LOOP
    s := restricted[i][1];
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', s, restricted[i][2]);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I', s, restricted[i][2]);
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = s AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO %I', r.rel, restricted[i][2]);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO %I', r.rel, restricted[i][2]);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO %I', r.rel, restricted[i][2]);
      END IF;
    END LOOP;
  END LOOP;
  -- Stage 10: fundzim_ledger — the ledger schema (append-only tables: SELECT, INSERT; status tables also UPDATE; the
  -- balance projection SELECT only, written by SECURITY DEFINER functions), reference data SELECT, and what shared
  -- triggers read in app. No DELETE or TRUNCATE anywhere. fundzim_readonly: the reporting views.
  IF to_regnamespace('ledger') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA ledger TO fundzim_ledger;
    GRANT USAGE ON SCHEMA app TO fundzim_ledger;
    GRANT SELECT ON app.status_transitions, app.currencies TO fundzim_ledger;
    GRANT USAGE ON SCHEMA audit TO fundzim_ledger;
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             format('%s.%s', n.nspname, c.relname) AS fq,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = 'ledger' AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger, fundzim_readonly', r.rel);
      ELSIF r.fq = ANY (ledger_reference) THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger', r.rel);
      ELSIF r.tbl = 'ledger_balances' THEN
        -- UPDATE only so the posting path can lock projection rows (SELECT ... FOR UPDATE, ascending account id);
        -- trg_ledger_balances_guard_update rejects every direct UPDATE (writes come from SECURITY DEFINER functions)
        EXECUTE format('GRANT SELECT, UPDATE ON %s TO fundzim_ledger', r.rel);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO fundzim_ledger', r.rel);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO fundzim_ledger', r.rel);
      END IF;
    END LOOP;
    EXECUTE 'GRANT USAGE ON SCHEMA ledger TO fundzim_readonly';
  END IF;

  -- compliance reads (never writes) the risk outputs it reviews
  FOR r IN SELECT format('%I.%I', n.nspname, c.relname) AS rel FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = 'risk' AND c.relname IN ('risk_signals','risk_assessments','risk_decisions','limits') AND c.relkind = 'r' LOOP
    EXECUTE 'GRANT USAGE ON SCHEMA risk TO fundzim_compliance';
    EXECUTE format('GRANT SELECT ON %s TO fundzim_compliance', r.rel);
  END LOOP;

  IF NOT pg_has_role('fundzim_worker', 'fundzim_app', 'MEMBER') THEN
    RAISE EXCEPTION 'role fundzim_worker must be a member of fundzim_app (GRANT fundzim_app TO fundzim_worker, run by the role administrator)';
  END IF;

  -- routines: EXECUTE only on an explicit allow-list (missing routines are skipped)
  FOR r IN
    SELECT * FROM (VALUES
      ('audit.verify_chain(regclass)', 'fundzim_worker'),
      ('app.payment_status_rank(text)', 'fundzim_app'),              -- Stage 8: CHECK ck_payment_intents_status_rank
      ('app.payout_status_rank(text)', 'fundzim_app'),               -- Stage 11: CHECK ck_payout_requests_status_rank
      ('app.payout_policy_rank(text)', 'fundzim_app'),               -- Stage 11: payout guard (policy only raised)
      ('app.eligibility_results_valid(jsonb,text,text)', 'fundzim_app'), -- Stage 11: CHECK ck_payout_eligibility_decisions_results
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
      -- Stage 10: the ledger posting role
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_ledger'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_ledger'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_ledger'),
      ('ledger.fold_deferred_balances()', 'fundzim_ledger'),
      ('ledger.database_environment()', 'fundzim_ledger'),
      ('ledger.account_class_signature(text)', 'fundzim_ledger'),
      -- ADR-037 §4: identity resolution for self-decision checks (read-only, SECURITY DEFINER)
      ('app.actor_identities(uuid)', 'fundzim_app'),
      ('app.actor_identities(uuid)', 'fundzim_worker'),
      ('app.actor_identities(uuid)', 'fundzim_kyc')
    ) AS t(fn, grantee)
  LOOP
    IF to_regprocedure(r.fn) IS NOT NULL THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.fn, r.grantee);
    END IF;
  END LOOP;

  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA public TO fundzim_app;
    GRANT SELECT ON public.goose_db_version TO fundzim_app;
  END IF;
END $$;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN ('payout.view','payout.operate','payout.sandbox.control'));
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('payout.view','payout.operate','payout.sandbox.control');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
ALTER TABLE app.feature_flag_changes DISABLE TRIGGER USER;
DELETE FROM app.feature_flag_changes WHERE feature_flag_id = md5('feature_flag:payouts.live_execution.enabled')::uuid;
ALTER TABLE app.feature_flag_changes ENABLE TRIGGER USER;
ALTER TABLE app.feature_flags DISABLE TRIGGER USER;
DELETE FROM app.feature_flags WHERE id = md5('feature_flag:payouts.live_execution.enabled')::uuid;
ALTER TABLE app.feature_flags ENABLE TRIGGER USER;
ALTER TABLE app.provider_capabilities DISABLE TRIGGER USER;
DELETE FROM app.provider_capabilities WHERE id = md5('provider_capability:sandbox:ECOCASH:USD:2')::uuid;
ALTER TABLE app.provider_capabilities ENABLE TRIGGER USER;
ALTER TABLE app.sandbox_scheduled_webhooks DISABLE TRIGGER USER;
DELETE FROM app.sandbox_scheduled_webhooks WHERE sandbox_payout_id IS NOT NULL;
ALTER TABLE app.sandbox_scheduled_webhooks ENABLE TRIGGER USER;
ALTER TABLE app.sandbox_scheduled_webhooks DROP CONSTRAINT ck_sandbox_scheduled_webhooks_subject;
ALTER TABLE app.sandbox_scheduled_webhooks DROP CONSTRAINT fk_sandbox_scheduled_webhooks_payout;
ALTER TABLE app.sandbox_scheduled_webhooks DROP COLUMN sandbox_payout_id;
ALTER TABLE app.sandbox_scheduled_webhooks ALTER COLUMN sandbox_payment_id SET NOT NULL;
DROP TABLE app.sandbox_provider_payouts;
DROP TABLE app.payout_exception_events;
DROP TABLE app.payout_exceptions;
DROP FUNCTION app.payout_exceptions_guard();
DROP TABLE app.payout_reconciliation_references;
DROP TABLE app.payout_events;
DROP TABLE app.payout_attempts;
DROP FUNCTION app.payout_attempts_guard();
DROP TABLE app.payout_reservations;
DROP FUNCTION app.payout_reservations_guard();
DROP TABLE app.payout_eligibility_decisions;
DROP FUNCTION app.eligibility_results_valid(jsonb, text, text);
DROP TABLE app.payout_approvals;
DROP FUNCTION app.payout_approvals_guard();
DROP TABLE app.payout_requests;
DROP FUNCTION app.require_payout_event();
DROP FUNCTION app.payout_requests_guard();
DROP FUNCTION app.payout_policy_rank(text);
DROP FUNCTION app.payout_status_rank(text);
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine IN ('payout', 'payout_reservation', 'payout_exception');
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
DROP INDEX app.uq_provider_capabilities_payout_key;
ALTER TABLE app.payout_destinations DROP CONSTRAINT uq_payout_destinations_id_currency_rail;
ALTER TABLE ledger.ledger_source_references DROP CONSTRAINT ck_ledger_source_references_source_system;
ALTER TABLE ledger.ledger_source_references ADD CONSTRAINT ck_ledger_source_references_source_system
  CHECK (source_system IN ('payments', 'reconciliation', 'staff', 'synthetic_fixture'));
ALTER TABLE ledger.ledger_transactions DROP CONSTRAINT ck_ledger_transactions_payouts_simulated_in_test;
ALTER TABLE ledger.ledger_transactions DROP CONSTRAINT ck_ledger_transactions_source_system;
ALTER TABLE ledger.ledger_transactions ADD CONSTRAINT ck_ledger_transactions_source_system
  CHECK (source_system IN ('payments', 'reconciliation', 'staff', 'synthetic_fixture'));
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly, fundzim_ledger';
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles'];
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
  -- Stage 10: the ledger schema belongs to fundzim_ledger alone (fundzim_app has no ledger privileges at all).
  ledger_reference constant text[] := ARRAY['ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines','ledger.ledger_environment'];
  s text;
  i int;
  r record;
  fq text;
  privs text;
BEGIN
  FOREACH s IN ARRAY schemas LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('REVOKE ALL ON SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL ROUTINES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
  END LOOP;

  -- fundzim_app: app, queue, audit, risk, recon. Never kyc, compliance or ledger (Stage 10).
  FOREACH s IN ARRAY ARRAY['app','queue','audit','risk','recon'] LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO fundzim_app', s);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO fundzim_app', s);
  END LOOP;

  FOR r IN
    SELECT n.nspname AS sch, c.relname AS tbl, c.relkind,
           format('%I.%I', n.nspname, c.relname) AS rel,
           coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update,
           coalesce(bool_or((t.tgtype & 8)  <> 0), false) AS no_delete
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                            AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
     WHERE n.nspname IN ('app','queue','audit','risk','recon')
       AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
     GROUP BY n.nspname, c.relname, c.relkind
  LOOP
    fq := r.sch || '.' || r.tbl;
    IF r.relkind IN ('v','m') THEN
      IF r.tbl NOT LIKE 'ro\_%' THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_app', r.rel);
      END IF;
      CONTINUE;
    END IF;
    IF fq = ANY (reference_only) THEN
      privs := 'SELECT';
    ELSIF r.sch = 'queue' THEN
      privs := 'SELECT, INSERT, UPDATE, DELETE';
    ELSIF r.sch = 'audit' THEN
      privs := 'SELECT, INSERT';
      -- legal_hold is the only mutable evidence column (trigger-checked)
      IF fq = 'audit.evidence_records' THEN privs := privs || ', UPDATE'; END IF;
    ELSE                                                        -- app, risk, recon
      privs := 'SELECT, INSERT';
      IF NOT r.no_update THEN privs := privs || ', UPDATE'; END IF;
      IF fq = ANY (app_delete_ok) AND NOT r.no_delete THEN privs := privs || ', DELETE'; END IF;
    END IF;
    EXECUTE format('GRANT %s ON %s TO fundzim_app', privs, r.rel);
  END LOOP;

  IF to_regnamespace('queue') IS NOT NULL THEN
    EXECUTE 'GRANT EXECUTE ON ALL ROUTINES IN SCHEMA queue TO fundzim_app';
    FOR r IN SELECT t.oid::regtype AS typ FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
              WHERE n.nspname = 'queue' AND t.typtype IN ('e','d','c') AND t.typrelid = 0 LOOP
      EXECUTE format('GRANT USAGE ON TYPE %s TO fundzim_app', r.typ);
    END LOOP;
  END IF;

  -- restricted roles: their own schema, what shared triggers read, and the gateways
  GRANT USAGE ON SCHEMA app TO fundzim_kyc, fundzim_compliance;
  GRANT SELECT ON app.status_transitions TO fundzim_kyc, fundzim_compliance;
  GRANT USAGE ON SCHEMA audit TO fundzim_kyc, fundzim_compliance;
  FOR i IN 1 .. array_length(restricted, 1) LOOP
    s := restricted[i][1];
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', s, restricted[i][2]);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I', s, restricted[i][2]);
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = s AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO %I', r.rel, restricted[i][2]);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO %I', r.rel, restricted[i][2]);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO %I', r.rel, restricted[i][2]);
      END IF;
    END LOOP;
  END LOOP;
  -- Stage 10: fundzim_ledger — the ledger schema (append-only tables: SELECT, INSERT; status tables also UPDATE; the
  -- balance projection SELECT only, written by SECURITY DEFINER functions), reference data SELECT, and what shared
  -- triggers read in app. No DELETE or TRUNCATE anywhere. fundzim_readonly: the reporting views.
  IF to_regnamespace('ledger') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA ledger TO fundzim_ledger;
    GRANT USAGE ON SCHEMA app TO fundzim_ledger;
    GRANT SELECT ON app.status_transitions, app.currencies TO fundzim_ledger;
    GRANT USAGE ON SCHEMA audit TO fundzim_ledger;
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             format('%s.%s', n.nspname, c.relname) AS fq,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = 'ledger' AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger, fundzim_readonly', r.rel);
      ELSIF r.fq = ANY (ledger_reference) THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger', r.rel);
      ELSIF r.tbl = 'ledger_balances' THEN
        -- UPDATE only so the posting path can lock projection rows (SELECT ... FOR UPDATE, ascending account id);
        -- trg_ledger_balances_guard_update rejects every direct UPDATE (writes come from SECURITY DEFINER functions)
        EXECUTE format('GRANT SELECT, UPDATE ON %s TO fundzim_ledger', r.rel);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO fundzim_ledger', r.rel);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO fundzim_ledger', r.rel);
      END IF;
    END LOOP;
    EXECUTE 'GRANT USAGE ON SCHEMA ledger TO fundzim_readonly';
  END IF;

  -- compliance reads (never writes) the risk outputs it reviews
  FOR r IN SELECT format('%I.%I', n.nspname, c.relname) AS rel FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = 'risk' AND c.relname IN ('risk_signals','risk_assessments','risk_decisions','limits') AND c.relkind = 'r' LOOP
    EXECUTE 'GRANT USAGE ON SCHEMA risk TO fundzim_compliance';
    EXECUTE format('GRANT SELECT ON %s TO fundzim_compliance', r.rel);
  END LOOP;

  IF NOT pg_has_role('fundzim_worker', 'fundzim_app', 'MEMBER') THEN
    RAISE EXCEPTION 'role fundzim_worker must be a member of fundzim_app (GRANT fundzim_app TO fundzim_worker, run by the role administrator)';
  END IF;

  -- routines: EXECUTE only on an explicit allow-list (missing routines are skipped)
  FOR r IN
    SELECT * FROM (VALUES
      ('audit.verify_chain(regclass)', 'fundzim_worker'),
      ('app.payment_status_rank(text)', 'fundzim_app'),              -- Stage 8: CHECK ck_payment_intents_status_rank
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
      -- Stage 10: the ledger posting role
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_ledger'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_ledger'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_ledger'),
      ('ledger.fold_deferred_balances()', 'fundzim_ledger'),
      ('ledger.database_environment()', 'fundzim_ledger'),
      ('ledger.account_class_signature(text)', 'fundzim_ledger'),
      -- ADR-037 §4: identity resolution for self-decision checks (read-only, SECURITY DEFINER)
      ('app.actor_identities(uuid)', 'fundzim_app'),
      ('app.actor_identities(uuid)', 'fundzim_worker'),
      ('app.actor_identities(uuid)', 'fundzim_kyc')
    ) AS t(fn, grantee)
  LOOP
    IF to_regprocedure(r.fn) IS NOT NULL THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.fn, r.grantee);
    END IF;
  END LOOP;

  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA public TO fundzim_app;
    GRANT SELECT ON public.goose_db_version TO fundzim_app;
  END IF;
END $$;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
