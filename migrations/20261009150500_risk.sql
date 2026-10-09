-- FundZim migration: risk foundation (Stage 5, work stream C) — risk.limits, risk.limit_change_requests,
-- risk.risk_signals, risk.risk_assessments, risk.risk_decisions.
-- Derived from the Stage 2 design draft design/sql/0008_risk.sql (limits, limit_change_requests,
-- risk_signals, risk_assessments) with the ADR-034 §2 rating vocabulary (LOW / STANDARD / ENHANCED /
-- RESTRICTED). Holds, monitoring rules and alerts from the draft belong to later stages and are not created.
-- risk.risk_decisions is new: the routing outcome of an assessment (NO_ACTION / MANUAL_REVIEW / ESCALATE).
-- A score or decision is never a finding of fraud: decisions only route a subject to humans (aml-risk-framework
-- §3.2, LR-071); a CHECK refuses any reason or rule code containing "FRAUD".
--
-- Owner module: risk (app pool: fundzim_app / fundzim_worker). fundzim_compliance gets SELECT on
-- risk_signals, risk_assessments, risk_decisions and limits through app.apply_runtime_grants() v3.
-- Classification: C1 (limits) / C2 (signals, assessments, decisions: IDs, codes, counts; never C3).
--
-- Limits (aml-risk-framework §5): every threshold or weight the risk model uses is a risk.limits row of type
-- REGULATORY / PROVIDER / INTERNAL_RISK with source, approval owner, effective window and review date; the Go
-- code references limit keys only. No REGULATORY or PROVIDER value is seeded (none is confirmed).
--
-- Seeding deviation from the draft (explained, ADR-034 §2 / ADR-035 baseline): the draft makes a limit
-- effective only through an APPROVED maker-checker limit_change_request decided by a human checker. A
-- migration has no second human, yet the Stage 5 risk model needs effective INTERNAL_RISK weights and rating
-- thresholds to run (otherwise it fails closed and routes everything to manual review). This migration
-- therefore adds approval_basis = 'MIGRATION_BASELINE', allowed ONLY for:
--   * limit_type INTERNAL_RISK, version 1, made_by the seeded non-login system actor
--     00000000-0000-0000-0000-000000000001, source_ref 'DEC-S5-RISK-BASELINE' (INTERNAL_DECISION);
--   * rows inserted and approved by session_user fundzim_migrator (a runtime role can never create one);
--   * no approved_by / approval_request_id (there was no checker — the row says so honestly).
-- Every other approval keeps the draft's maker-checker semantics unchanged. Baseline values are uncalibrated
-- internal starting points with review_by 2027-04-09; COMPLIANCE must review them and replace them through
-- maker-checker versions (supersedes_id) before any production use.
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- -----------------------------------------------------------------------------------------------------
-- risk.limits — versioned limit registry. A version is immutable once approved; every change is a new
-- version. Permitted mutations: PROPOSED -> APPROVED | REJECTED, APPROVED -> RETIRED (draft 0008).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.limits (
  id                   uuid        NOT NULL,
  limit_key            text        NOT NULL,
  version              integer     NOT NULL,
  limit_type           text        NOT NULL,
  scope_type           text        NOT NULL,
  scope_value          text,
  value_kind           text        NOT NULL,
  currency             char(3),
  value_minor          bigint,                          -- MONEY: integer minor units of `currency`
  value_count          bigint,                          -- COUNT (also rule weights and score thresholds)
  value_seconds        bigint,                          -- DURATION (seconds)
  value_bp             integer,                         -- BASIS_POINTS
  source_type          text        NOT NULL,
  source_ref           text        NOT NULL,            -- REQ-xxx | PCR-xxx | CONTRACT:<ref> | PROVIDER_DOC:<ref> | DEC-<id> | PD-xx
  source_citation      text,
  legal_review_ref     text,
  counsel_ref          text,
  approval_owner_role  text        NOT NULL,
  approval_basis       text        NOT NULL DEFAULT 'MAKER_CHECKER',
  status               text        NOT NULL,
  made_by              uuid        NOT NULL,
  approved_by          uuid,
  approved_at          timestamptz,
  approval_request_id  uuid,
  retired_by           uuid,
  retired_at           timestamptz,
  retire_request_id    uuid,
  effective_from       timestamptz NOT NULL,
  effective_to         timestamptz,
  review_by            date        NOT NULL,
  supersedes_id        uuid,
  created_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_limits PRIMARY KEY (id),
  CONSTRAINT fk_limits_currency FOREIGN KEY (currency) REFERENCES app.currencies (code),
  CONSTRAINT fk_limits_made_by FOREIGN KEY (made_by) REFERENCES app.users (id),
  CONSTRAINT fk_limits_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT fk_limits_retired_by FOREIGN KEY (retired_by) REFERENCES app.users (id),
  CONSTRAINT fk_limits_supersedes_id FOREIGN KEY (supersedes_id) REFERENCES risk.limits (id),
  CONSTRAINT uq_limits_version UNIQUE NULLS NOT DISTINCT (limit_key, limit_type, scope_type, scope_value, currency, version),
  CONSTRAINT ck_limits_key CHECK (limit_key ~ '^[a-z][a-z0-9_]*(\.[a-z0-9_{}]+)+$'),
  CONSTRAINT ck_limits_version CHECK (version >= 1 AND ((version = 1) = (supersedes_id IS NULL))),
  CONSTRAINT ck_limits_type CHECK (limit_type IN ('REGULATORY', 'PROVIDER', 'INTERNAL_RISK')),
  CONSTRAINT ck_limits_scope_type CHECK (scope_type IN ('GLOBAL', 'CURRENCY', 'PROVIDER', 'RAIL', 'CATEGORY', 'ORG_TYPE',
                                                        'RISK_RATING', 'VERIFICATION_LEVEL', 'HOLD_TYPE', 'CASE_TYPE', 'SUBJECT_TYPE')),
  CONSTRAINT ck_limits_scope_value CHECK ((scope_type = 'GLOBAL') = (scope_value IS NULL)),
  CONSTRAINT ck_limits_value_kind CHECK (value_kind IN ('MONEY', 'COUNT', 'DURATION', 'BASIS_POINTS')),
  CONSTRAINT ck_limits_value_shape CHECK (
        num_nonnulls(value_minor, value_count, value_seconds, value_bp) = 1
    AND (value_kind <> 'MONEY'        OR value_minor   IS NOT NULL)
    AND (value_kind <> 'COUNT'        OR value_count   IS NOT NULL)
    AND (value_kind <> 'DURATION'     OR value_seconds IS NOT NULL)
    AND (value_kind <> 'BASIS_POINTS' OR value_bp      IS NOT NULL)
    AND ((value_kind = 'MONEY') = (currency IS NOT NULL))),
  CONSTRAINT ck_limits_value_nonneg CHECK (coalesce(value_minor, value_count, value_seconds, value_bp) >= 0),
  CONSTRAINT ck_limits_source_type CHECK (source_type IN ('STATUTE', 'REGULATION', 'DIRECTIVE', 'GUIDELINE',
                                                          'PROVIDER_DOC', 'PROVIDER_CONTRACT', 'INTERNAL_DECISION')),
  CONSTRAINT ck_limits_source_regulatory CHECK (limit_type <> 'REGULATORY' OR (
        source_type IN ('STATUTE', 'REGULATION', 'DIRECTIVE', 'GUIDELINE')
    AND source_ref ~ '^REQ-[0-9]{3}$' AND source_citation IS NOT NULL
    AND (legal_review_ref IS NOT NULL OR counsel_ref IS NOT NULL))),
  CONSTRAINT ck_limits_source_provider CHECK (limit_type <> 'PROVIDER' OR (
        source_type IN ('PROVIDER_DOC', 'PROVIDER_CONTRACT')
    AND source_ref ~ '^(PCR-[0-9]{3}|CONTRACT:.+|PROVIDER_DOC:.+)$')),
  CONSTRAINT ck_limits_source_internal CHECK (limit_type <> 'INTERNAL_RISK' OR (
        source_type = 'INTERNAL_DECISION' AND source_ref ~ '^(DEC-[A-Za-z0-9-]+|PD-[0-9]{2})$')),
  CONSTRAINT ck_limits_lr_ref CHECK (legal_review_ref IS NULL OR legal_review_ref ~ '^LR-[0-9]{3}$'),
  CONSTRAINT ck_limits_owner_role CHECK (approval_owner_role IN ('COMPLIANCE', 'FINANCE', 'COMPLIANCE_AND_FINANCE')),
  CONSTRAINT ck_limits_owner_regulatory CHECK (limit_type <> 'REGULATORY' OR approval_owner_role = 'COMPLIANCE'),
  CONSTRAINT ck_limits_status CHECK (status IN ('PROPOSED', 'APPROVED', 'REJECTED', 'RETIRED')),
  CONSTRAINT ck_limits_approval_basis CHECK (approval_basis IN ('MAKER_CHECKER', 'MIGRATION_BASELINE')),
  -- maker-checker: the approver is never the maker
  CONSTRAINT ck_limits_maker_checker CHECK (approved_by IS NULL OR approved_by <> made_by),
  CONSTRAINT ck_limits_approved_fields CHECK (approval_basis <> 'MAKER_CHECKER' OR (
        (status IN ('APPROVED', 'RETIRED', 'REJECTED')) = (approved_by IS NOT NULL AND approved_at IS NOT NULL AND approval_request_id IS NOT NULL))),
  -- the migration baseline (see header): INTERNAL_RISK v1 by the system actor, never REJECTED, no checker fields
  CONSTRAINT ck_limits_baseline CHECK (approval_basis <> 'MIGRATION_BASELINE' OR (
        limit_type = 'INTERNAL_RISK' AND version = 1 AND made_by = '00000000-0000-0000-0000-000000000001'
    AND source_ref = 'DEC-S5-RISK-BASELINE' AND approved_by IS NULL AND approval_request_id IS NULL
    AND status <> 'REJECTED' AND (status IN ('APPROVED', 'RETIRED')) = (approved_at IS NOT NULL))),
  CONSTRAINT ck_limits_regulatory_counsel CHECK (limit_type <> 'REGULATORY' OR status NOT IN ('APPROVED', 'RETIRED') OR counsel_ref IS NOT NULL),
  CONSTRAINT ck_limits_retired_fields CHECK ((status = 'RETIRED') = (retired_at IS NOT NULL AND retired_by IS NOT NULL AND retire_request_id IS NOT NULL)),
  CONSTRAINT ck_limits_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);
COMMENT ON TABLE risk.limits IS 'C1. Versioned limit records; immutable once approved. aml-risk-framework §5, draft 0008.';
CREATE UNIQUE INDEX uq_limits_supersedes ON risk.limits (supersedes_id) WHERE supersedes_id IS NOT NULL;
CREATE INDEX ix_limits_lookup ON risk.limits (limit_key, scope_type, scope_value, currency, status, version DESC);
CREATE INDEX ix_limits_review_by ON risk.limits (review_by) WHERE status = 'APPROVED';

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('risk_limit', '',         'PROPOSED'),
  ('risk_limit', 'PROPOSED', 'APPROVED'),
  ('risk_limit', 'PROPOSED', 'REJECTED'),
  ('risk_limit', 'APPROVED', 'RETIRED');

-- -----------------------------------------------------------------------------------------------------
-- risk.limit_change_requests — maker-checker pending object for activating or retiring a limit version
-- (operational-controls §3 "Limit or threshold change"). Expiry is computed by the service, never here.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.limit_change_requests (
  id                 uuid        NOT NULL,
  limit_id           uuid        NOT NULL,
  action             text        NOT NULL,
  status             text        NOT NULL,
  requested_by       uuid        NOT NULL,
  requested_at       timestamptz NOT NULL DEFAULT now(),
  justification      text        NOT NULL,
  payload_sha256     bytea       NOT NULL,
  expires_at         timestamptz NOT NULL,
  decided_by         uuid,
  decided_at         timestamptz,
  decision_note      text,
  checker_step_up_at timestamptz,
  version            integer     NOT NULL DEFAULT 1,
  CONSTRAINT pk_limit_change_requests PRIMARY KEY (id),
  CONSTRAINT fk_lcr_limit_id FOREIGN KEY (limit_id) REFERENCES risk.limits (id),
  CONSTRAINT fk_lcr_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id),
  CONSTRAINT fk_lcr_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id),
  CONSTRAINT ck_lcr_action CHECK (action IN ('ACTIVATE', 'RETIRE')),
  CONSTRAINT ck_lcr_status CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'EXPIRED', 'WITHDRAWN')),
  CONSTRAINT ck_lcr_hash CHECK (octet_length(payload_sha256) = 32),
  CONSTRAINT ck_lcr_expiry CHECK (expires_at > requested_at),
  CONSTRAINT ck_lcr_justification CHECK (length(btrim(justification)) > 0),
  CONSTRAINT ck_lcr_maker_checker CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT ck_lcr_decided CHECK ((status IN ('APPROVED', 'REJECTED')) = (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CONSTRAINT ck_lcr_step_up CHECK (status <> 'APPROVED' OR checker_step_up_at IS NOT NULL)
);
COMMENT ON TABLE risk.limit_change_requests IS 'C1. Maker-checker requests for limit activation/retirement.';
CREATE UNIQUE INDEX uq_lcr_one_pending ON risk.limit_change_requests (limit_id, action) WHERE status = 'PENDING';

ALTER TABLE risk.limits ADD CONSTRAINT fk_limits_approval_request
  FOREIGN KEY (approval_request_id) REFERENCES risk.limit_change_requests (id);
ALTER TABLE risk.limits ADD CONSTRAINT fk_limits_retire_request
  FOREIGN KEY (retire_request_id) REFERENCES risk.limit_change_requests (id);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('limit_change_request', '',        'PENDING'),
  ('limit_change_request', 'PENDING', 'APPROVED'),
  ('limit_change_request', 'PENDING', 'REJECTED'),
  ('limit_change_request', 'PENDING', 'EXPIRED'),
  ('limit_change_request', 'PENDING', 'WITHDRAWN');

CREATE TRIGGER trg_limit_change_requests_guard_status BEFORE INSERT OR UPDATE OF status ON risk.limit_change_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('limit_change_request');
CREATE TRIGGER trg_limit_change_requests_no_delete BEFORE DELETE ON risk.limit_change_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_limit_change_requests_no_truncate BEFORE TRUNCATE ON risk.limit_change_requests
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- A request is a frozen proposal: only the decision columns change, once, from PENDING.
CREATE FUNCTION risk.limit_change_requests_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.status <> 'PENDING' THEN
    RAISE EXCEPTION 'limit change request % is final (%)', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  IF NOT changed <@ ARRAY['status','decided_by','decided_at','decision_note','checker_step_up_at','version'] THEN
    RAISE EXCEPTION 'limit change request: only decision fields may change (attempted: %)', changed USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'limit change request: version must increase by 1' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_limit_change_requests_guard BEFORE UPDATE ON risk.limit_change_requests
  FOR EACH ROW EXECUTE FUNCTION risk.limit_change_requests_guard();

CREATE TRIGGER trg_limits_guard_status BEFORE INSERT OR UPDATE OF status ON risk.limits
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('risk_limit');
CREATE TRIGGER trg_limits_no_delete BEFORE DELETE ON risk.limits
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_limits_no_truncate BEFORE TRUNCATE ON risk.limits
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Limit versions: immutable content; approval/retirement require a matching request decided by the same
-- checker. The only other path is the migration baseline (header), restricted to session_user fundzim_migrator.
CREATE FUNCTION risk.limits_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  changed text[];
  req risk.limit_change_requests%ROWTYPE;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'PROPOSED' THEN
      RAISE EXCEPTION 'limits: a new version must be inserted as PROPOSED' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.approval_basis = 'MIGRATION_BASELINE' AND session_user <> 'fundzim_migrator' THEN
      RAISE EXCEPTION 'limits: MIGRATION_BASELINE rows are created by migrations only' USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF NEW.supersedes_id IS NOT NULL AND NOT EXISTS (
         SELECT 1 FROM risk.limits p WHERE p.id = NEW.supersedes_id AND p.version = NEW.version - 1
           AND p.limit_key = NEW.limit_key AND p.limit_type = NEW.limit_type AND p.scope_type = NEW.scope_type
           AND p.scope_value IS NOT DISTINCT FROM NEW.scope_value AND p.currency IS NOT DISTINCT FROM NEW.currency) THEN
      RAISE EXCEPTION 'limits: supersedes_id must be the previous version of the same limit identity' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;

  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.status = 'PROPOSED' AND NEW.status = 'APPROVED' AND OLD.approval_basis = 'MIGRATION_BASELINE' THEN
    IF session_user <> 'fundzim_migrator' OR NOT changed <@ ARRAY['status','approved_at'] THEN
      RAISE EXCEPTION 'limits: a MIGRATION_BASELINE row is approved only by its migration (attempted: %)', changed
        USING ERRCODE = 'restrict_violation';
    END IF;
  ELSIF OLD.status = 'PROPOSED' AND NEW.status IN ('APPROVED','REJECTED') THEN
    IF NOT changed <@ ARRAY['status','approved_by','approved_at','approval_request_id','counsel_ref'] THEN
      RAISE EXCEPTION 'limits: approval may not change limit content (attempted: %) — create a new version', changed
        USING ERRCODE = 'restrict_violation';
    END IF;
    SELECT * INTO req FROM risk.limit_change_requests WHERE id = NEW.approval_request_id;
    IF NOT FOUND OR req.limit_id <> NEW.id OR req.action <> 'ACTIVATE'
       OR req.status <> (CASE NEW.status WHEN 'APPROVED' THEN 'APPROVED' ELSE 'REJECTED' END)
       OR req.decided_by <> NEW.approved_by THEN
      RAISE EXCEPTION 'limits: approval must match a decided ACTIVATE request by the same checker' USING ERRCODE = 'check_violation';
    END IF;
  ELSIF OLD.status = 'APPROVED' AND NEW.status = 'RETIRED' THEN
    IF NOT changed <@ ARRAY['status','retired_by','retired_at','retire_request_id'] THEN
      RAISE EXCEPTION 'limits: retirement may not change limit content (attempted: %)', changed USING ERRCODE = 'restrict_violation';
    END IF;
    SELECT * INTO req FROM risk.limit_change_requests WHERE id = NEW.retire_request_id;
    IF NOT FOUND OR req.limit_id <> NEW.id OR req.action <> 'RETIRE' OR req.status <> 'APPROVED'
       OR req.decided_by <> NEW.retired_by THEN
      RAISE EXCEPTION 'limits: retirement must match an APPROVED RETIRE request by the same checker' USING ERRCODE = 'check_violation';
    END IF;
  ELSE
    RAISE EXCEPTION 'limits: version % is immutable in status % — create a new version', OLD.id, OLD.status
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_limits_guard BEFORE INSERT OR UPDATE ON risk.limits
  FOR EACH ROW EXECUTE FUNCTION risk.limits_guard();

-- -----------------------------------------------------------------------------------------------------
-- risk.risk_signals — append-only facts consumed from domain events. Idempotent consumer: one row per
-- (source_event_id, signal_type). Facts are IDs, codes and counts only (never C3).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_signals (
  id               uuid        NOT NULL,
  signal_type      text        NOT NULL,
  subject_type     text        NOT NULL,
  subject_id       uuid        NOT NULL,
  source_module    text        NOT NULL,
  source_event_id  uuid        NOT NULL,                  -- outbox event id (dedupe)
  source_event_type text       NOT NULL,
  observed_at      timestamptz NOT NULL,
  facts            jsonb       NOT NULL DEFAULT '{}',
  recorded_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_signals PRIMARY KEY (id),
  CONSTRAINT uq_risk_signals_source UNIQUE (source_event_id, signal_type),
  CONSTRAINT ck_risk_signals_type CHECK (signal_type ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_risk_signals_subject CHECK (subject_type IN ('USER', 'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION',
                                            'KYC_CASE', 'KYB_CASE', 'CAMPAIGN', 'PAYMENT', 'PAYOUT', 'DEVICE', 'DONOR_GUEST')),
  CONSTRAINT ck_risk_signals_module CHECK (source_module IN ('auth', 'users', 'storage', 'payments', 'payouts', 'campaigns', 'kyc',
                                           'organisations', 'beneficiaries', 'psp', 'reconciliation', 'compliance', 'risk')),
  CONSTRAINT ck_risk_signals_event_type CHECK (source_event_type ~ '^[a-z_]+(\.[a-z_]+)+$'),
  CONSTRAINT ck_risk_signals_facts CHECK (jsonb_typeof(facts) = 'object')
);
COMMENT ON TABLE risk.risk_signals IS 'C2. Append-only risk facts (no C3).';
CREATE INDEX ix_risk_signals_subject ON risk.risk_signals (subject_type, subject_id, observed_at);
CREATE TRIGGER trg_risk_signals_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_signals
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_signals_no_truncate BEFORE TRUNCATE ON risk.risk_signals
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- risk.risk_assessments — append-only explainable scores. Current assessment = latest row per subject.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_assessments (
  id                 uuid        NOT NULL,
  subject_type       text        NOT NULL,
  subject_id         uuid        NOT NULL,
  decision_point     text        NOT NULL,
  score              integer     NOT NULL,
  rating             text        NOT NULL,
  model_version      text        NOT NULL,
  contributing_rules text[]      NOT NULL DEFAULT '{}',
  explanation        jsonb       NOT NULL DEFAULT '[]',   -- [{rule, weight, observed, threshold}] — counts and codes only
  rating_limit_ids   uuid[]      NOT NULL DEFAULT '{}',   -- risk.limits versions used (audit trail)
  trigger_signal_id  uuid,
  computed_at        timestamptz NOT NULL,
  CONSTRAINT pk_risk_assessments PRIMARY KEY (id),
  CONSTRAINT fk_risk_assessments_trigger_signal_id FOREIGN KEY (trigger_signal_id) REFERENCES risk.risk_signals (id),
  CONSTRAINT ck_risk_assessments_subject CHECK (subject_type IN ('USER', 'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION',
                                                'KYC_CASE', 'KYB_CASE', 'CAMPAIGN', 'PAYOUT')),
  CONSTRAINT ck_risk_assessments_point CHECK (decision_point IN ('ONBOARDING', 'CAMPAIGN_SUBMISSION', 'DONATION', 'PAYOUT', 'PERIODIC', 'EVENT')),
  CONSTRAINT ck_risk_assessments_score CHECK (score BETWEEN 0 AND 1000),
  CONSTRAINT ck_risk_assessments_rating CHECK (rating IN ('LOW', 'STANDARD', 'ENHANCED', 'RESTRICTED')),
  CONSTRAINT ck_risk_assessments_model CHECK (model_version ~ '^[a-z0-9][a-z0-9_.-]{0,63}$'),
  CONSTRAINT ck_risk_assessments_rules CHECK (array_position(contributing_rules, NULL) IS NULL
                                              AND array_to_string(contributing_rules, ',') ~ '^([A-Z][A-Z0-9_]{2,63}(,|$))*$'),
  -- a score is never proof of fraud (aml-risk-framework §3.2, LR-071): no rule may be named as a fraud finding
  CONSTRAINT ck_risk_assessments_no_fraud_claim CHECK (array_to_string(contributing_rules, ',') !~* 'FRAUD'),
  CONSTRAINT ck_risk_assessments_explanation CHECK (jsonb_typeof(explanation) = 'array')
);
COMMENT ON TABLE risk.risk_assessments IS 'C2. Append-only risk scores with contributing rule codes and their explanation.';
CREATE INDEX ix_risk_assessments_subject ON risk.risk_assessments (subject_type, subject_id, computed_at DESC, id DESC);
CREATE TRIGGER trg_risk_assessments_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_assessments
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_assessments_no_truncate BEFORE TRUNCATE ON risk.risk_assessments
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- risk.risk_decisions — append-only routing outcome of an assessment. NO_ACTION, MANUAL_REVIEW (a human
-- looks at the subject in context) or ESCALATE (a compliance case is recommended). Never a fraud finding.
-- Without effective limits the model cannot run: the decision is MANUAL_REVIEW with reason
-- MODEL_LIMITS_UNAVAILABLE and no assessment (fail closed, aml-risk-framework §5 rule 3).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_decisions (
  id                 uuid        NOT NULL,
  assessment_id      uuid,
  subject_type       text        NOT NULL,
  subject_id         uuid        NOT NULL,
  decision           text        NOT NULL,
  reason_code        text        NOT NULL,
  policy_version     text        NOT NULL,
  decision_limit_ids uuid[]      NOT NULL DEFAULT '{}',
  decided_at         timestamptz NOT NULL,
  CONSTRAINT pk_risk_decisions PRIMARY KEY (id),
  CONSTRAINT fk_risk_decisions_assessment_id FOREIGN KEY (assessment_id) REFERENCES risk.risk_assessments (id),
  CONSTRAINT uq_risk_decisions_assessment_id UNIQUE (assessment_id),
  CONSTRAINT ck_risk_decisions_decision CHECK (decision IN ('NO_ACTION', 'MANUAL_REVIEW', 'ESCALATE')),
  CONSTRAINT ck_risk_decisions_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_risk_decisions_no_fraud_claim CHECK (reason_code !~* 'FRAUD'),
  CONSTRAINT ck_risk_decisions_policy CHECK (policy_version ~ '^[a-z0-9][a-z0-9_.-]{0,63}$'),
  CONSTRAINT ck_risk_decisions_subject CHECK (subject_type IN ('USER', 'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION',
                                              'KYC_CASE', 'KYB_CASE', 'CAMPAIGN', 'PAYOUT')),
  -- only the fail-closed path has no assessment, and it always routes to a human
  CONSTRAINT ck_risk_decisions_assessment CHECK (assessment_id IS NOT NULL OR
                                                 (decision = 'MANUAL_REVIEW' AND reason_code = 'MODEL_LIMITS_UNAVAILABLE'))
);
COMMENT ON TABLE risk.risk_decisions IS 'C2. Append-only routing decisions (never a fraud finding).';
CREATE INDEX ix_risk_decisions_subject ON risk.risk_decisions (subject_type, subject_id, decided_at DESC, id DESC);
CREATE TRIGGER trg_risk_decisions_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_decisions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_decisions_no_truncate BEFORE TRUNCATE ON risk.risk_decisions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- Baseline INTERNAL_RISK limits for risk model v1 (header: MIGRATION_BASELINE). Weights and thresholds are
-- COUNT values on the 0–1000 score scale; the signal window is a DURATION. Uncalibrated starting points,
-- owner COMPLIANCE, review by 2027-04-09. Inserted PROPOSED, then approved by this migration.
-- -----------------------------------------------------------------------------------------------------
INSERT INTO risk.limits (id, limit_key, version, limit_type, scope_type, value_kind, value_count, value_seconds, source_type,
  source_ref, approval_owner_role, approval_basis, status, made_by, effective_from, review_by)
SELECT md5('risk.limit:' || k)::uuid, k, 1, 'INTERNAL_RISK', 'GLOBAL', kind, cnt, secs, 'INTERNAL_DECISION',
       'DEC-S5-RISK-BASELINE', 'COMPLIANCE', 'MIGRATION_BASELINE', 'PROPOSED', '00000000-0000-0000-0000-000000000001',
       '2026-10-09T00:00:00Z', DATE '2027-04-09'
  FROM (VALUES
    ('risk.model_v1.weight.kyc_rejected',                  'COUNT',    250::bigint, NULL::bigint),
    ('risk.model_v1.weight.document_rejected_repeat',      'COUNT',    150, NULL),
    ('risk.model_v1.threshold.document_rejected_repeat',   'COUNT',      3, NULL),
    ('risk.model_v1.weight.duplicate_identity',            'COUNT',    600, NULL),
    ('risk.model_v1.weight.destination_changed_repeat',    'COUNT',    200, NULL),
    ('risk.model_v1.threshold.destination_changed_repeat', 'COUNT',      3, NULL),
    ('risk.model_v1.weight.destination_rejected',          'COUNT',    200, NULL),
    ('risk.model_v1.weight.beneficiary_rejected',          'COUNT',    200, NULL),
    ('risk.model_v1.weight.beneficiary_escalated',         'COUNT',    300, NULL),
    ('risk.model_v1.signal_window',                        'DURATION', NULL, 15552000),   -- 180 days
    ('risk.rating.standard_min',                           'COUNT',    100, NULL),
    ('risk.rating.enhanced_min',                           'COUNT',    300, NULL),
    ('risk.rating.restricted_min',                         'COUNT',    600, NULL)
  ) AS v(k, kind, cnt, secs);
UPDATE risk.limits SET status = 'APPROVED', approved_at = '2026-10-09T00:00:00Z'
 WHERE approval_basis = 'MIGRATION_BASELINE' AND status = 'PROPOSED';

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Development rollback only. DROP removes the append-only tables (row/statement triggers do not fire on DROP).
DROP TABLE IF EXISTS risk.risk_decisions, risk.risk_assessments, risk.risk_signals;
ALTER TABLE risk.limits DROP CONSTRAINT IF EXISTS fk_limits_approval_request, DROP CONSTRAINT IF EXISTS fk_limits_retire_request;
DROP TABLE IF EXISTS risk.limit_change_requests, risk.limits;
DROP FUNCTION IF EXISTS risk.limits_guard(), risk.limit_change_requests_guard();
-- status_transitions is append-only; the migrator (table owner) lifts the guard only for this development rollback.
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine IN ('risk_limit', 'limit_change_request');
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
