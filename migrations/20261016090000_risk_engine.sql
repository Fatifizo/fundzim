-- Stage 13 (ADR-045; Stage 1 aml-risk-framework, transaction-monitoring, sanctions-screening; Stage 2 baseline: risk owns
-- holds, signals, monitoring rules and alerts): the configurable risk engine on the Stage 5 signal store, risk cases with
-- assignments and immutable evidence, the central hold registry, the provider-neutral screening foundation (TEST
-- simulator only), monitoring events and the risk domain history.
--
-- A risk score is never proof of fraud and no engine output is named as a fraud finding (CHECKs, as in Stage 5).
-- Automated enforcement is limited to temporary WITHDRAWAL_HOLD / PAYOUT_DESTINATION_HOLD holds (≤ 7 days), cases and
-- monitoring events (ADR-045 §5). Screening rows are TEST only (no provider is selected; production screening BLOCKED)
-- and hold no identity data. Rule versions are maker-checker: no rule is seeded or active until two people approve it.
-- Owner module: risk (schema risk; app pool), except the compliance link vocabulary change (compliance). Runs as
-- fundzim_migrator. Forward-only in production (ADR-028); Down is for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

-- -----------------------------------------------------------------------------------------------------
-- 0. The Stage 5 signal store becomes the single, normalised signal store (ADR-045 §1). New columns have defaults;
--    rows stay append-only.
-- -----------------------------------------------------------------------------------------------------
ALTER TABLE risk.risk_signals ADD COLUMN category text NOT NULL DEFAULT 'IDENTITY_RISK';
ALTER TABLE risk.risk_signals ADD COLUMN severity text NOT NULL DEFAULT 'MEDIUM';
ALTER TABLE risk.risk_signals ADD COLUMN confidence smallint NOT NULL DEFAULT 100;
ALTER TABLE risk.risk_signals ADD COLUMN kind text NOT NULL DEFAULT 'OBSERVED';
ALTER TABLE risk.risk_signals ADD COLUMN environment text NOT NULL DEFAULT 'TEST';
ALTER TABLE risk.risk_signals ADD COLUMN correlation_id text;
ALTER TABLE risk.risk_signals ADD COLUMN evidence_ref text;
ALTER TABLE risk.risk_signals ADD COLUMN derived_by_rule_version_id uuid;
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_category CHECK (category IN ('IDENTITY_RISK','CAMPAIGN_RISK','PAYMENT_RISK',
  'WITHDRAWAL_RISK','DESTINATION_RISK','SESSION_RISK','DEVICE_RISK','VELOCITY_RISK','COMPLIANCE_RISK','PROVIDER_RISK','FEE_MANIPULATION_RISK',
  'CHARGEBACK_RISK'));
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_severity CHECK (severity IN ('INFO','LOW','MEDIUM','HIGH'));
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_confidence CHECK (confidence BETWEEN 0 AND 100);
-- an inferred (DERIVED) signal is never a verified fact: it names the rule version that derived it
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_kind CHECK (kind IN ('OBSERVED','DERIVED')
  AND (kind = 'OBSERVED') = (derived_by_rule_version_id IS NULL));
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_environment CHECK (environment IN ('TEST'));
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_correlation CHECK (correlation_id IS NULL OR char_length(correlation_id) <= 128);
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_evidence_ref CHECK (evidence_ref IS NULL OR char_length(evidence_ref) <= 200);
ALTER TABLE risk.risk_signals DROP CONSTRAINT ck_risk_signals_subject;
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_subject CHECK (subject_type IN ('USER', 'ORGANISATION', 'BENEFICIARY',
  'PAYOUT_DESTINATION', 'KYC_CASE', 'KYB_CASE', 'CAMPAIGN', 'PAYMENT', 'PAYOUT', 'DEVICE', 'DONOR_GUEST', 'DONATION_INTENT',
  'AUTHENTICATION_SESSION'));
ALTER TABLE risk.risk_signals DROP CONSTRAINT ck_risk_signals_module;
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_module CHECK (source_module IN ('auth', 'users', 'storage', 'payments', 'payouts',
  'campaigns', 'kyc', 'organisations', 'beneficiaries', 'psp', 'reconciliation', 'compliance', 'risk', 'fees', 'identity'));
CREATE INDEX ix_risk_signals_category ON risk.risk_signals (category, observed_at DESC);
CREATE INDEX ix_risk_signals_recorded ON risk.risk_signals (recorded_at DESC, id DESC);

-- -----------------------------------------------------------------------------------------------------
-- 1. The risk domain history (rules, holds, cases, screening, signals reviews): append-only.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_events (
  id              uuid        NOT NULL,
  aggregate_type  text        NOT NULL,
  aggregate_id    uuid        NOT NULL,
  event_type      text        NOT NULL,
  actor_type      text        NOT NULL,
  actor_id        text        NOT NULL,
  detail          jsonb       NOT NULL DEFAULT '{}',
  correlation_id  text,
  occurred_at     timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_events PRIMARY KEY (id),
  CONSTRAINT ck_risk_events_aggregate CHECK (aggregate_type IN ('risk_rule','risk_rule_version','risk_case','risk_hold','risk_screening',
    'risk_signal','risk_evaluation')),
  CONSTRAINT ck_risk_events_type CHECK (event_type ~ '^[A-Z][A-Z_]{2,60}$' AND event_type !~* 'FRAUD'),
  CONSTRAINT ck_risk_events_actor CHECK (actor_type IN ('STAFF','SYSTEM')),
  CONSTRAINT ck_risk_events_detail CHECK (jsonb_typeof(detail) = 'object')
);
CREATE INDEX ix_risk_events_aggregate ON risk.risk_events (aggregate_type, aggregate_id, occurred_at, id);
CREATE TRIGGER trg_risk_events_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_events_no_truncate BEFORE TRUNCATE ON risk.risk_events FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 2. Rules and immutable rule versions (machine 'risk_rule_version'; maker-checker like fee policies, ADR-045 §3–§4).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_rules (
  id            uuid        NOT NULL,
  code          text        NOT NULL,
  name          text        NOT NULL,
  description   text        NOT NULL DEFAULT '',
  subject_type  text        NOT NULL,
  category      text        NOT NULL,
  created_by    uuid        NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_rules PRIMARY KEY (id),
  CONSTRAINT uq_risk_rules_code UNIQUE (code),
  CONSTRAINT fk_risk_rules_created_by FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_risk_rules_code CHECK (code ~ '^[A-Z][A-Z0-9_]{2,63}$' AND code !~* 'FRAUD'),
  CONSTRAINT ck_risk_rules_name CHECK (char_length(btrim(name)) BETWEEN 3 AND 120 AND name !~* 'fraud'),
  CONSTRAINT ck_risk_rules_description CHECK (char_length(description) <= 2000),
  CONSTRAINT ck_risk_rules_subject CHECK (subject_type IN ('USER','ORGANISATION','CAMPAIGN','BENEFICIARY','PAYOUT_DESTINATION',
    'DONATION_INTENT','PAYOUT','AUTHENTICATION_SESSION')),
  CONSTRAINT ck_risk_rules_category CHECK (category IN ('IDENTITY_RISK','CAMPAIGN_RISK','PAYMENT_RISK','WITHDRAWAL_RISK','DESTINATION_RISK',
    'SESSION_RISK','DEVICE_RISK','VELOCITY_RISK','COMPLIANCE_RISK','PROVIDER_RISK','FEE_MANIPULATION_RISK','CHARGEBACK_RISK'))
);
CREATE TRIGGER trg_risk_rules_only_presentation BEFORE UPDATE ON risk.risk_rules
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('name', 'description');
CREATE TRIGGER trg_risk_rules_updated_at BEFORE UPDATE ON risk.risk_rules FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_risk_rules_no_delete BEFORE DELETE ON risk.risk_rules FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE risk.risk_rule_versions (
  id                    uuid        NOT NULL,
  rule_id               uuid        NOT NULL,
  version_no            integer     NOT NULL,
  status                text        NOT NULL,
  definition            jsonb       NOT NULL,
  contribution          smallint    NOT NULL,
  severity              text        NOT NULL,
  recommended_action    text        NOT NULL,
  hold_type             text,
  hold_duration_seconds integer,
  case_priority         text        NOT NULL,
  engine_version        text        NOT NULL,
  definition_hash       bytea,
  created_by            uuid        NOT NULL,
  submitted_by          uuid,
  submitted_at          timestamptz,
  approved_by           uuid,
  approved_at           timestamptz,
  approved_step_up_at   timestamptz,
  approval_reason       text,
  rejected_by           uuid,
  rejected_at           timestamptz,
  rejection_reason      text,
  activated_by          uuid,
  activated_at          timestamptz,
  suspended_by          uuid,
  suspended_at          timestamptz,
  suspension_reason     text,
  reactivated_by        uuid,
  reactivated_at        timestamptz,
  retired_by            uuid,
  retired_at            timestamptz,
  retirement_reason     text,
  version               integer     NOT NULL DEFAULT 1,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_rule_versions PRIMARY KEY (id),
  CONSTRAINT fk_risk_rule_versions_rule FOREIGN KEY (rule_id) REFERENCES risk.risk_rules (id) ON DELETE RESTRICT,
  CONSTRAINT fk_risk_rule_versions_created_by FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_risk_rule_versions_no UNIQUE (rule_id, version_no),
  CONSTRAINT ck_risk_rule_versions_status CHECK (status IN ('DRAFT','PENDING_APPROVAL','APPROVED','REJECTED','ACTIVE','SUSPENDED','RETIRED')),
  CONSTRAINT ck_risk_rule_versions_definition CHECK (jsonb_typeof(definition) = 'object' AND octet_length(definition::text) <= 16384),
  CONSTRAINT ck_risk_rule_versions_contribution CHECK (contribution BETWEEN 0 AND 100),
  CONSTRAINT ck_risk_rule_versions_severity CHECK (severity IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  CONSTRAINT ck_risk_rule_versions_action CHECK (recommended_action IN ('ALLOW_WITH_MONITORING','REVIEW_REQUIRED','TEMPORARY_HOLD',
    'ESCALATE','BLOCK')),
  -- automated holds: payout-protective types only, temporary (≤ 7 days) (ADR-045 §5)
  CONSTRAINT ck_risk_rule_versions_hold CHECK ((recommended_action = 'TEMPORARY_HOLD') = (hold_type IS NOT NULL)
    AND (hold_type IS NULL OR (hold_type IN ('WITHDRAWAL_HOLD','PAYOUT_DESTINATION_HOLD') AND hold_duration_seconds BETWEEN 3600 AND 604800))
    AND (hold_type IS NOT NULL OR hold_duration_seconds IS NULL)),
  CONSTRAINT ck_risk_rule_versions_priority CHECK (case_priority IN ('P1','P2','P3','P4')),
  CONSTRAINT ck_risk_rule_versions_engine CHECK (engine_version ~ '^risk-engine-v[0-9]+$'),
  CONSTRAINT ck_risk_rule_versions_submitted CHECK (status = 'DRAFT' OR (submitted_by IS NOT NULL AND submitted_at IS NOT NULL AND
    definition_hash IS NOT NULL AND octet_length(definition_hash) = 32) OR (status = 'RETIRED' AND submitted_by IS NULL)),
  CONSTRAINT ck_risk_rule_versions_approved CHECK (status NOT IN ('APPROVED','ACTIVE','SUSPENDED') OR (approved_by IS NOT NULL AND
    approved_at IS NOT NULL AND approved_step_up_at IS NOT NULL AND approval_reason IS NOT NULL)),
  CONSTRAINT ck_risk_rule_versions_maker_checker CHECK (approved_by IS NULL OR (approved_by <> created_by AND approved_by <> submitted_by)),
  CONSTRAINT ck_risk_rule_versions_rejected CHECK ((status = 'REJECTED') = (rejected_by IS NOT NULL) AND (rejected_by IS NULL OR
    (rejected_by <> created_by AND rejected_by <> submitted_by AND rejection_reason IS NOT NULL))),
  CONSTRAINT ck_risk_rule_versions_activated CHECK (status NOT IN ('ACTIVE','SUSPENDED') OR (activated_by IS NOT NULL AND activated_at IS NOT NULL)),
  CONSTRAINT ck_risk_rule_versions_suspended CHECK (status <> 'SUSPENDED' OR (suspended_by IS NOT NULL AND suspension_reason IS NOT NULL)),
  CONSTRAINT ck_risk_rule_versions_reactivated CHECK (reactivated_by IS NULL OR reactivated_by <> suspended_by),
  CONSTRAINT ck_risk_rule_versions_retired CHECK ((status = 'RETIRED') = (retired_by IS NOT NULL) AND (retired_by IS NULL OR retirement_reason IS NOT NULL))
);
CREATE UNIQUE INDEX uq_risk_rule_versions_one_active ON risk.risk_rule_versions (rule_id) WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX uq_risk_rule_versions_one_draft ON risk.risk_rule_versions (rule_id) WHERE status IN ('DRAFT','PENDING_APPROVAL');
CREATE INDEX ix_risk_rule_versions_active ON risk.risk_rule_versions (status) WHERE status = 'ACTIVE';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('risk_rule_version', '',                 'DRAFT'),
  ('risk_rule_version', 'DRAFT',            'PENDING_APPROVAL'),
  ('risk_rule_version', 'DRAFT',            'RETIRED'),
  ('risk_rule_version', 'PENDING_APPROVAL', 'APPROVED'),
  ('risk_rule_version', 'PENDING_APPROVAL', 'REJECTED'),
  ('risk_rule_version', 'APPROVED',         'ACTIVE'),
  ('risk_rule_version', 'APPROVED',         'RETIRED'),
  ('risk_rule_version', 'ACTIVE',           'SUSPENDED'),
  ('risk_rule_version', 'ACTIVE',           'RETIRED'),
  ('risk_rule_version', 'SUSPENDED',        'ACTIVE'),
  ('risk_rule_version', 'SUSPENDED',        'RETIRED');

CREATE FUNCTION risk.risk_rule_versions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  ids uuid[];
BEGIN
  IF TG_OP = 'INSERT' AND NEW.status <> 'DRAFT' THEN
    RAISE EXCEPTION 'risk rule versions are created as DRAFT' USING ERRCODE = 'check_violation';
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.status <> 'DRAFT' AND (NEW.rule_id, NEW.version_no, NEW.definition, NEW.contribution, NEW.severity,
       NEW.recommended_action, NEW.hold_type, NEW.hold_duration_seconds, NEW.case_priority, NEW.engine_version, NEW.definition_hash,
       NEW.created_by, NEW.submitted_by, NEW.submitted_at) IS DISTINCT FROM (OLD.rule_id, OLD.version_no, OLD.definition, OLD.contribution,
       OLD.severity, OLD.recommended_action, OLD.hold_type, OLD.hold_duration_seconds, OLD.case_priority, OLD.engine_version,
       OLD.definition_hash, OLD.created_by, OLD.submitted_by, OLD.submitted_at) THEN
    RAISE EXCEPTION 'risk rule version % is % and its definition is immutable; create a new version', OLD.id, OLD.status
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.approved_by IS NOT NULL AND (NEW.approved_by, NEW.approved_at, NEW.approval_reason)
       IS DISTINCT FROM (OLD.approved_by, OLD.approved_at, OLD.approval_reason) THEN
    RAISE EXCEPTION 'the approval of risk rule version % is immutable', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  -- linked accounts (staff ↔ personal) cannot bypass maker-checker
  IF NEW.approved_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.approved_by IS NULL) THEN
    ids := app.actor_identities(NEW.approved_by);
    IF NEW.created_by = ANY (ids) OR NEW.submitted_by = ANY (ids) THEN
      RAISE EXCEPTION 'maker-checker: % is (or is linked to) the maker of risk rule version %', NEW.approved_by, NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NEW.rejected_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.rejected_by IS NULL) THEN
    ids := app.actor_identities(NEW.rejected_by);
    IF NEW.created_by = ANY (ids) OR NEW.submitted_by = ANY (ids) THEN
      RAISE EXCEPTION 'maker-checker: % is (or is linked to) the maker of risk rule version %', NEW.rejected_by, NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NEW.reactivated_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.reactivated_by IS DISTINCT FROM NEW.reactivated_by)
     AND NEW.suspended_by = ANY (app.actor_identities(NEW.reactivated_by)) THEN
    RAISE EXCEPTION 'maker-checker: % suspended risk rule version % and cannot reactivate it', NEW.reactivated_by, NEW.id
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_risk_rule_versions_guard BEFORE INSERT OR UPDATE ON risk.risk_rule_versions
  FOR EACH ROW EXECUTE FUNCTION risk.risk_rule_versions_guard();
CREATE TRIGGER trg_risk_rule_versions_guard_status BEFORE INSERT OR UPDATE OF status ON risk.risk_rule_versions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('risk_rule_version');
CREATE TRIGGER trg_risk_rule_versions_updated_at BEFORE UPDATE ON risk.risk_rule_versions FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_risk_rule_versions_no_delete BEFORE DELETE ON risk.risk_rule_versions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_rule_versions_no_truncate BEFORE TRUNCATE ON risk.risk_rule_versions FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 3. Evaluations (immutable, explainable, reproducible) and the actions taken on them (automated enforcement is a
--    separate record from the recommendation; human outcomes live on cases). One evaluation per trigger signal.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_evaluations (
  id                  uuid        NOT NULL,
  subject_type        text        NOT NULL,
  subject_id          uuid        NOT NULL,
  trigger_signal_id   uuid        NOT NULL,
  engine_version      text        NOT NULL,
  rule_version_ids    uuid[]      NOT NULL,
  results             jsonb       NOT NULL,
  score               smallint    NOT NULL,
  classification      text        NOT NULL,
  band_source         text        NOT NULL,
  confidence          smallint    NOT NULL,
  recommended_action  text        NOT NULL,
  explanation         jsonb       NOT NULL,
  inputs_hash         bytea       NOT NULL,
  evaluated_at        timestamptz NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_evaluations PRIMARY KEY (id),
  CONSTRAINT fk_risk_evaluations_signal FOREIGN KEY (trigger_signal_id) REFERENCES risk.risk_signals (id) ON DELETE RESTRICT,
  CONSTRAINT uq_risk_evaluations_trigger UNIQUE (subject_type, subject_id, trigger_signal_id),
  CONSTRAINT ck_risk_evaluations_engine CHECK (engine_version ~ '^risk-engine-v[0-9]+$'),
  CONSTRAINT ck_risk_evaluations_score CHECK (score BETWEEN 0 AND 100),
  CONSTRAINT ck_risk_evaluations_class CHECK (classification IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  CONSTRAINT ck_risk_evaluations_band_source CHECK (band_source IN ('ENGINEERING_DEFAULT','LIMITS')),
  CONSTRAINT ck_risk_evaluations_confidence CHECK (confidence BETWEEN 0 AND 100),
  CONSTRAINT ck_risk_evaluations_action CHECK (recommended_action IN ('ALLOW','ALLOW_WITH_MONITORING','REVIEW_REQUIRED','TEMPORARY_HOLD',
    'ESCALATE','BLOCK')),
  CONSTRAINT ck_risk_evaluations_docs CHECK (jsonb_typeof(results) = 'array' AND jsonb_typeof(explanation) = 'array'),
  CONSTRAINT ck_risk_evaluations_no_fraud_claim CHECK (explanation::text !~* 'fraud'),
  CONSTRAINT ck_risk_evaluations_hash CHECK (octet_length(inputs_hash) = 32)
);
CREATE INDEX ix_risk_evaluations_subject ON risk.risk_evaluations (subject_type, subject_id, evaluated_at DESC, id DESC);
CREATE INDEX ix_risk_evaluations_recent ON risk.risk_evaluations (evaluated_at DESC, id DESC);
CREATE TRIGGER trg_risk_evaluations_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_evaluations FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_evaluations_no_truncate BEFORE TRUNCATE ON risk.risk_evaluations FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- the latest evaluation per subject (a projection rebuilt from risk_evaluations; dashboards only)
CREATE TABLE risk.risk_subject_profiles (
  subject_type        text        NOT NULL,
  subject_id          uuid        NOT NULL,
  latest_evaluation_id uuid       NOT NULL,
  score               smallint    NOT NULL,
  classification      text        NOT NULL,
  evaluated_at        timestamptz NOT NULL,
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_subject_profiles PRIMARY KEY (subject_type, subject_id),
  CONSTRAINT fk_risk_subject_profiles_evaluation FOREIGN KEY (latest_evaluation_id) REFERENCES risk.risk_evaluations (id) ON DELETE RESTRICT,
  CONSTRAINT ck_risk_subject_profiles_class CHECK (classification IN ('LOW','MEDIUM','HIGH','CRITICAL'))
);
CREATE INDEX ix_risk_subject_profiles_class ON risk.risk_subject_profiles (classification, score DESC);

CREATE TABLE risk.risk_evaluation_actions (
  id             uuid        NOT NULL,
  evaluation_id  uuid        NOT NULL,
  action_type    text        NOT NULL,
  automated      boolean     NOT NULL,
  case_id        uuid,
  hold_id        uuid,
  detail         jsonb       NOT NULL DEFAULT '{}',
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_evaluation_actions PRIMARY KEY (id),
  CONSTRAINT fk_risk_evaluation_actions_evaluation FOREIGN KEY (evaluation_id) REFERENCES risk.risk_evaluations (id) ON DELETE RESTRICT,
  CONSTRAINT uq_risk_evaluation_actions UNIQUE (evaluation_id, action_type),
  CONSTRAINT ck_risk_evaluation_actions_type CHECK (action_type IN ('CASE_OPENED','CASE_LINKED','HOLD_CREATED','HOLD_EXISTING',
    'MONITORING_RECORDED','NO_ACTION'))
);
CREATE TRIGGER trg_risk_evaluation_actions_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_evaluation_actions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- false-positive and relevance reviews of signals (signals are never deleted)
CREATE TABLE risk.risk_signal_reviews (
  id              uuid        NOT NULL,
  signal_id       uuid        NOT NULL,
  case_id         uuid,
  classification  text        NOT NULL,
  explanation     text        NOT NULL,
  reviewed_by     uuid        NOT NULL,
  reviewed_at     timestamptz NOT NULL,
  CONSTRAINT pk_risk_signal_reviews PRIMARY KEY (id),
  CONSTRAINT fk_risk_signal_reviews_signal FOREIGN KEY (signal_id) REFERENCES risk.risk_signals (id) ON DELETE RESTRICT,
  CONSTRAINT fk_risk_signal_reviews_by FOREIGN KEY (reviewed_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_risk_signal_reviews UNIQUE (signal_id, case_id),
  CONSTRAINT ck_risk_signal_reviews_class CHECK (classification IN ('FALSE_POSITIVE','RELEVANT','INCONCLUSIVE')),
  CONSTRAINT ck_risk_signal_reviews_explanation CHECK (char_length(btrim(explanation)) BETWEEN 5 AND 2000)
);
CREATE TRIGGER trg_risk_signal_reviews_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_signal_reviews FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 4. Risk cases (machine 'risk_case'), assignments, immutable evidence and the evidence access log.
-- -----------------------------------------------------------------------------------------------------
CREATE SEQUENCE risk.risk_case_number_seq;
CREATE TABLE risk.risk_cases (
  id                  uuid        NOT NULL,
  case_number         bigint      NOT NULL DEFAULT nextval('risk.risk_case_number_seq'),
  case_type           text        NOT NULL,
  subject_type        text        NOT NULL,
  subject_id          uuid        NOT NULL,
  status              text        NOT NULL,
  priority            text        NOT NULL,
  risk_score          smallint,
  classification      text,
  recommended_action  text,
  assigned_team       text        NOT NULL DEFAULT 'RISK_OPERATIONS',
  assigned_to         uuid,
  due_at              timestamptz,
  proposed_outcome    text,
  proposed_by         uuid,
  proposed_at         timestamptz,
  proposed_reason     text,
  decision_outcome    text,
  decided_by          uuid,
  decided_at          timestamptz,
  decision_reason     text,
  decided_step_up_at  timestamptz,
  escalated_at        timestamptz,
  opened_by_type      text        NOT NULL,
  opened_by           text        NOT NULL,
  dedupe_key          text        NOT NULL,
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_cases PRIMARY KEY (id),
  CONSTRAINT uq_risk_cases_number UNIQUE (case_number),
  CONSTRAINT fk_risk_cases_assigned_to FOREIGN KEY (assigned_to) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_risk_cases_type CHECK (case_type IN ('TRANSACTION_MONITORING','CAMPAIGN_RISK','ACCOUNT_TAKEOVER','DESTINATION_RISK',
    'WITHDRAWAL_RISK','SCREENING_REVIEW','FEE_MANIPULATION','IDENTITY_RISK','OTHER')),
  CONSTRAINT ck_risk_cases_subject CHECK (subject_type IN ('USER','ORGANISATION','CAMPAIGN','BENEFICIARY','PAYOUT_DESTINATION',
    'DONATION_INTENT','PAYOUT','AUTHENTICATION_SESSION')),
  CONSTRAINT ck_risk_cases_status CHECK (status IN ('OPEN','TRIAGE','UNDER_INVESTIGATION','AWAITING_INFORMATION','ESCALATED',
    'DECISION_PENDING','RESOLVED','CLOSED','REOPENED')),
  CONSTRAINT ck_risk_cases_priority CHECK (priority IN ('P1','P2','P3','P4')),
  CONSTRAINT ck_risk_cases_score CHECK (risk_score IS NULL OR risk_score BETWEEN 0 AND 100),
  CONSTRAINT ck_risk_cases_class CHECK (classification IS NULL OR classification IN ('LOW','MEDIUM','HIGH','CRITICAL')),
  CONSTRAINT ck_risk_cases_team CHECK (assigned_team IN ('RISK_OPERATIONS','COMPLIANCE')),
  -- human outcomes never declare fraud; legal restrictions come only from compliance resolutions (ADR-045 §6)
  CONSTRAINT ck_risk_cases_outcomes CHECK ((proposed_outcome IS NULL OR proposed_outcome IN ('NO_FURTHER_ACTION','FALSE_POSITIVE',
    'RISK_CONFIRMED_HOLD_MAINTAINED','ESCALATED_TO_COMPLIANCE')) AND (decision_outcome IS NULL OR decision_outcome IN ('NO_FURTHER_ACTION',
    'FALSE_POSITIVE','RISK_CONFIRMED_HOLD_MAINTAINED','ESCALATED_TO_COMPLIANCE'))),
  CONSTRAINT ck_risk_cases_proposal CHECK ((proposed_outcome IS NULL) = (proposed_by IS NULL) AND (proposed_outcome IS NULL OR
    (proposed_at IS NOT NULL AND proposed_reason IS NOT NULL))),
  CONSTRAINT ck_risk_cases_decision CHECK ((decision_outcome IS NULL) = (decided_by IS NULL) AND (decided_by IS NULL OR
    (decided_at IS NOT NULL AND decision_reason IS NOT NULL AND decided_step_up_at IS NOT NULL AND decided_by <> proposed_by))),
  CONSTRAINT ck_risk_cases_resolved CHECK (status NOT IN ('RESOLVED','CLOSED') OR decision_outcome IS NOT NULL),
  CONSTRAINT ck_risk_cases_pending CHECK (status <> 'DECISION_PENDING' OR proposed_outcome IS NOT NULL),
  CONSTRAINT ck_risk_cases_opened_by CHECK (opened_by_type IN ('SYSTEM','STAFF')),
  CONSTRAINT ck_risk_cases_version CHECK (version >= 1)
);
-- one open case per subject and case type: repeated events link to it (deduplication, ADR-045)
CREATE UNIQUE INDEX uq_risk_cases_open ON risk.risk_cases (dedupe_key) WHERE status NOT IN ('RESOLVED','CLOSED');
CREATE INDEX ix_risk_cases_queue ON risk.risk_cases (status, priority, created_at);
CREATE INDEX ix_risk_cases_subject ON risk.risk_cases (subject_type, subject_id, created_at DESC);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('risk_case', '',                     'OPEN'),
  ('risk_case', 'OPEN',                 'TRIAGE'),
  ('risk_case', 'TRIAGE',               'UNDER_INVESTIGATION'),
  ('risk_case', 'REOPENED',             'TRIAGE'),
  ('risk_case', 'UNDER_INVESTIGATION',  'AWAITING_INFORMATION'),
  ('risk_case', 'AWAITING_INFORMATION', 'UNDER_INVESTIGATION'),
  ('risk_case', 'TRIAGE',               'ESCALATED'),
  ('risk_case', 'UNDER_INVESTIGATION',  'ESCALATED'),
  ('risk_case', 'ESCALATED',            'UNDER_INVESTIGATION'),
  ('risk_case', 'TRIAGE',               'DECISION_PENDING'),
  ('risk_case', 'UNDER_INVESTIGATION',  'DECISION_PENDING'),
  ('risk_case', 'ESCALATED',            'DECISION_PENDING'),
  ('risk_case', 'DECISION_PENDING',     'UNDER_INVESTIGATION'),
  ('risk_case', 'DECISION_PENDING',     'RESOLVED'),
  ('risk_case', 'RESOLVED',             'CLOSED'),
  ('risk_case', 'RESOLVED',             'REOPENED'),
  ('risk_case', 'CLOSED',               'REOPENED');

CREATE FUNCTION risk.risk_cases_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  ids uuid[];
BEGIN
  IF TG_OP = 'UPDATE' AND (NEW.case_number, NEW.case_type, NEW.subject_type, NEW.subject_id, NEW.opened_by_type, NEW.opened_by, NEW.dedupe_key,
       NEW.created_at) IS DISTINCT FROM (OLD.case_number, OLD.case_type, OLD.subject_type, OLD.subject_id, OLD.opened_by_type, OLD.opened_by,
       OLD.dedupe_key, OLD.created_at) THEN
    RAISE EXCEPTION 'risk case % identity is immutable', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  -- the checker of a decision is never the proposer, nor linked to the proposer or to the case subject
  IF NEW.decided_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.decided_by IS NULL) THEN
    ids := app.actor_identities(NEW.decided_by);
    IF NEW.proposed_by = ANY (ids) OR (NEW.subject_type = 'USER' AND NEW.subject_id = ANY (ids)) THEN
      RAISE EXCEPTION 'maker-checker: % may not decide risk case %', NEW.decided_by, NEW.id USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  -- an investigator never works a case about themselves or a linked account
  IF NEW.assigned_to IS NOT NULL AND NEW.subject_type = 'USER' AND (TG_OP = 'INSERT' OR OLD.assigned_to IS DISTINCT FROM NEW.assigned_to)
     AND NEW.subject_id = ANY (app.actor_identities(NEW.assigned_to)) THEN
    RAISE EXCEPTION 'conflict of interest: % may not investigate risk case %', NEW.assigned_to, NEW.id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_risk_cases_guard BEFORE INSERT OR UPDATE ON risk.risk_cases FOR EACH ROW EXECUTE FUNCTION risk.risk_cases_guard();
CREATE TRIGGER trg_risk_cases_guard_status BEFORE INSERT OR UPDATE OF status ON risk.risk_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('risk_case');
CREATE TRIGGER trg_risk_cases_updated_at BEFORE UPDATE ON risk.risk_cases FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_risk_cases_no_delete BEFORE DELETE ON risk.risk_cases FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_cases_no_truncate BEFORE TRUNCATE ON risk.risk_cases FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE risk.risk_case_assignments (
  id           uuid        NOT NULL,
  case_id      uuid        NOT NULL,
  assigned_to  uuid        NOT NULL,
  assigned_by  text        NOT NULL,
  reason       text        NOT NULL,
  assigned_at  timestamptz NOT NULL,
  CONSTRAINT pk_risk_case_assignments PRIMARY KEY (id),
  CONSTRAINT fk_risk_case_assignments_case FOREIGN KEY (case_id) REFERENCES risk.risk_cases (id) ON DELETE RESTRICT,
  CONSTRAINT fk_risk_case_assignments_to FOREIGN KEY (assigned_to) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_risk_case_assignments_reason CHECK (char_length(btrim(reason)) BETWEEN 3 AND 500)
);
CREATE INDEX ix_risk_case_assignments_case ON risk.risk_case_assignments (case_id, assigned_at);
CREATE TRIGGER trg_risk_case_assignments_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_case_assignments FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- evidence: immutable, content-hashed; a correction is a new row that supersedes the original (never an overwrite)
CREATE TABLE risk.risk_case_evidence (
  id               uuid        NOT NULL,
  case_id          uuid        NOT NULL,
  evidence_type    text        NOT NULL,
  reference_type   text,
  reference_id     text,
  content          jsonb       NOT NULL,
  content_hash     bytea       NOT NULL,
  source           text        NOT NULL,
  added_by_type    text        NOT NULL,
  added_by         text        NOT NULL,
  retention_class  text        NOT NULL DEFAULT 'RISK_RECORD_LEGAL_REVIEW_REQUIRED',
  supersedes_id    uuid,
  added_at         timestamptz NOT NULL,
  CONSTRAINT pk_risk_case_evidence PRIMARY KEY (id),
  CONSTRAINT fk_risk_case_evidence_case FOREIGN KEY (case_id) REFERENCES risk.risk_cases (id) ON DELETE RESTRICT,
  CONSTRAINT fk_risk_case_evidence_supersedes FOREIGN KEY (supersedes_id) REFERENCES risk.risk_case_evidence (id) ON DELETE RESTRICT,
  CONSTRAINT ck_risk_case_evidence_type CHECK (evidence_type IN ('SIGNAL','EVALUATION','SOURCE_EVENT','VERIFICATION_REF','CAMPAIGN_REF',
    'PAYMENT_REF','PAYOUT_REF','PROVIDER_EVIDENCE','STAFF_NOTE','DOCUMENT_REF','SCREENING_RESULT','HOLD_REF')),
  CONSTRAINT ck_risk_case_evidence_content CHECK (jsonb_typeof(content) = 'object' AND octet_length(content::text) <= 16384),
  CONSTRAINT ck_risk_case_evidence_hash CHECK (octet_length(content_hash) = 32),
  CONSTRAINT ck_risk_case_evidence_actor CHECK (added_by_type IN ('STAFF','SYSTEM')),
  CONSTRAINT ck_risk_case_evidence_retention CHECK (retention_class = 'RISK_RECORD_LEGAL_REVIEW_REQUIRED'),
  CONSTRAINT ck_risk_case_evidence_ref CHECK (reference_id IS NULL OR char_length(reference_id) <= 200)
);
CREATE INDEX ix_risk_case_evidence_case ON risk.risk_case_evidence (case_id, added_at);
CREATE UNIQUE INDEX uq_risk_case_evidence_reference ON risk.risk_case_evidence (case_id, evidence_type, reference_id)
  WHERE reference_id IS NOT NULL AND evidence_type <> 'STAFF_NOTE';
CREATE TRIGGER trg_risk_case_evidence_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_case_evidence FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_case_evidence_no_truncate BEFORE TRUNCATE ON risk.risk_case_evidence FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE risk.risk_case_access_log (
  id         uuid        NOT NULL,
  case_id    uuid        NOT NULL,
  staff_id   uuid        NOT NULL,
  action     text        NOT NULL,
  accessed_at timestamptz NOT NULL,
  CONSTRAINT pk_risk_case_access_log PRIMARY KEY (id),
  CONSTRAINT fk_risk_case_access_log_case FOREIGN KEY (case_id) REFERENCES risk.risk_cases (id) ON DELETE RESTRICT,
  CONSTRAINT ck_risk_case_access_log_action CHECK (action IN ('VIEW_CASE','VIEW_EVIDENCE'))
);
CREATE INDEX ix_risk_case_access_log_case ON risk.risk_case_access_log (case_id, accessed_at DESC);
CREATE TRIGGER trg_risk_case_access_log_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_case_access_log FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 5. Holds (machine 'risk_hold'): the central hold registry (ADR-045 §7). One open hold per type and subject.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_holds (
  id                     uuid        NOT NULL,
  hold_type              text        NOT NULL,
  subject_type           text        NOT NULL,
  subject_id             uuid        NOT NULL,
  status                 text        NOT NULL,
  reason_code            text        NOT NULL,
  description            text        NOT NULL DEFAULT '',
  automated              boolean     NOT NULL,
  evaluation_id          uuid,
  case_id                uuid,
  created_by_type        text        NOT NULL,
  created_by             text        NOT NULL,
  activated_at           timestamptz,
  activated_by           uuid,
  expires_at             timestamptz,
  release_requested_by   uuid,
  release_requested_at   timestamptz,
  release_reason         text,
  released_by            uuid,
  released_at            timestamptz,
  release_step_up_at     timestamptz,
  revoked_by             uuid,
  revoked_at             timestamptz,
  revocation_reason      text,
  version                integer     NOT NULL DEFAULT 1,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_holds PRIMARY KEY (id),
  CONSTRAINT fk_risk_holds_evaluation FOREIGN KEY (evaluation_id) REFERENCES risk.risk_evaluations (id) ON DELETE RESTRICT,
  CONSTRAINT fk_risk_holds_case FOREIGN KEY (case_id) REFERENCES risk.risk_cases (id) ON DELETE RESTRICT,
  CONSTRAINT ck_risk_holds_type CHECK (hold_type IN ('CAMPAIGN_HOLD','DONATION_HOLD','WITHDRAWAL_HOLD','PAYOUT_DESTINATION_HOLD',
    'ACCOUNT_REVIEW_HOLD','BENEFICIARY_HOLD')),
  CONSTRAINT ck_risk_holds_subject CHECK (CASE hold_type
    WHEN 'CAMPAIGN_HOLD'           THEN subject_type = 'CAMPAIGN'
    WHEN 'DONATION_HOLD'           THEN subject_type IN ('CAMPAIGN','USER')
    WHEN 'WITHDRAWAL_HOLD'         THEN subject_type IN ('CAMPAIGN','USER','ORGANISATION')
    WHEN 'PAYOUT_DESTINATION_HOLD' THEN subject_type = 'PAYOUT_DESTINATION'
    WHEN 'ACCOUNT_REVIEW_HOLD'     THEN subject_type IN ('USER','ORGANISATION')
    ELSE subject_type = 'BENEFICIARY' END),
  CONSTRAINT ck_risk_holds_status CHECK (status IN ('REQUESTED','ACTIVE','UNDER_REVIEW','RELEASE_PENDING','RELEASED','EXPIRED','REVOKED')),
  CONSTRAINT ck_risk_holds_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$' AND reason_code !~* 'FRAUD'),
  CONSTRAINT ck_risk_holds_description CHECK (char_length(description) <= 2000),
  -- automated holds: payout-protective types, temporary (≤ 7 days), always from an evaluation (ADR-045 §5)
  CONSTRAINT ck_risk_holds_automated CHECK (NOT automated OR (hold_type IN ('WITHDRAWAL_HOLD','PAYOUT_DESTINATION_HOLD')
    AND evaluation_id IS NOT NULL AND expires_at IS NOT NULL AND activated_at IS NOT NULL AND expires_at <= activated_at + interval '7 days')),
  CONSTRAINT ck_risk_holds_creator CHECK (created_by_type IN ('STAFF','SYSTEM') AND (automated = (created_by_type = 'SYSTEM'))),
  CONSTRAINT ck_risk_holds_active CHECK (status = 'REQUESTED' OR status = 'REVOKED' OR activated_at IS NOT NULL),
  -- a requested (disruptive) hold is activated by a second person
  CONSTRAINT ck_risk_holds_activated_by CHECK (activated_by IS NULL OR (created_by_type = 'STAFF' AND activated_by::text <> created_by)),
  CONSTRAINT ck_risk_holds_expiry CHECK (expires_at IS NULL OR activated_at IS NULL OR expires_at > activated_at),
  CONSTRAINT ck_risk_holds_release_request CHECK ((release_requested_by IS NULL) = (release_requested_at IS NULL)
    AND (release_requested_by IS NULL OR release_reason IS NOT NULL)),
  CONSTRAINT ck_risk_holds_released CHECK ((status = 'RELEASED') = (released_by IS NOT NULL) AND (released_by IS NULL OR
    (released_at IS NOT NULL AND release_step_up_at IS NOT NULL AND release_requested_by IS NOT NULL AND released_by <> release_requested_by))),
  CONSTRAINT ck_risk_holds_revoked CHECK ((status = 'REVOKED') = (revoked_by IS NOT NULL) AND (revoked_by IS NULL OR revocation_reason IS NOT NULL))
);
CREATE UNIQUE INDEX uq_risk_holds_open ON risk.risk_holds (hold_type, subject_type, subject_id)
  WHERE status IN ('REQUESTED','ACTIVE','UNDER_REVIEW','RELEASE_PENDING');
CREATE INDEX ix_risk_holds_subject ON risk.risk_holds (subject_type, subject_id) WHERE status IN ('ACTIVE','UNDER_REVIEW','RELEASE_PENDING');
CREATE INDEX ix_risk_holds_expiry ON risk.risk_holds (expires_at) WHERE status IN ('ACTIVE','UNDER_REVIEW') AND expires_at IS NOT NULL;
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('risk_hold', '',                'REQUESTED'),
  ('risk_hold', '',                'ACTIVE'),
  ('risk_hold', 'REQUESTED',       'ACTIVE'),
  ('risk_hold', 'REQUESTED',       'REVOKED'),
  ('risk_hold', 'ACTIVE',          'UNDER_REVIEW'),
  ('risk_hold', 'ACTIVE',          'RELEASE_PENDING'),
  ('risk_hold', 'UNDER_REVIEW',    'RELEASE_PENDING'),
  ('risk_hold', 'RELEASE_PENDING', 'RELEASED'),
  ('risk_hold', 'RELEASE_PENDING', 'ACTIVE'),
  ('risk_hold', 'ACTIVE',          'EXPIRED'),
  ('risk_hold', 'UNDER_REVIEW',    'EXPIRED');

CREATE FUNCTION risk.risk_holds_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  ids uuid[];
BEGIN
  IF TG_OP = 'UPDATE' AND (NEW.hold_type, NEW.subject_type, NEW.subject_id, NEW.automated, NEW.evaluation_id, NEW.created_by_type,
       NEW.created_by, NEW.created_at, NEW.reason_code) IS DISTINCT FROM (OLD.hold_type, OLD.subject_type, OLD.subject_id, OLD.automated,
       OLD.evaluation_id, OLD.created_by_type, OLD.created_by, OLD.created_at, OLD.reason_code) THEN
    RAISE EXCEPTION 'risk hold % identity is immutable', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  -- release: never by the requester of the release, nor by anyone linked to them or to the held subject
  IF NEW.released_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.released_by IS NULL) THEN
    ids := app.actor_identities(NEW.released_by);
    IF NEW.release_requested_by = ANY (ids) OR (NEW.subject_type IN ('USER') AND NEW.subject_id = ANY (ids)) THEN
      RAISE EXCEPTION 'maker-checker: % may not release risk hold %', NEW.released_by, NEW.id USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NEW.activated_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.activated_by IS NULL)
     AND NEW.created_by = ANY (SELECT x::text FROM unnest(app.actor_identities(NEW.activated_by)) AS x) THEN
    RAISE EXCEPTION 'maker-checker: % requested risk hold % and cannot activate it', NEW.activated_by, NEW.id USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.release_requested_by IS NOT NULL AND NEW.subject_type = 'USER' AND (TG_OP = 'INSERT' OR OLD.release_requested_by IS NULL)
     AND NEW.subject_id = ANY (app.actor_identities(NEW.release_requested_by)) THEN
    RAISE EXCEPTION 'conflict of interest: % may not request the release of risk hold %', NEW.release_requested_by, NEW.id
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_risk_holds_guard BEFORE INSERT OR UPDATE ON risk.risk_holds FOR EACH ROW EXECUTE FUNCTION risk.risk_holds_guard();
CREATE TRIGGER trg_risk_holds_guard_status BEFORE INSERT OR UPDATE OF status ON risk.risk_holds
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('risk_hold');
CREATE TRIGGER trg_risk_holds_updated_at BEFORE UPDATE ON risk.risk_holds FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_risk_holds_no_delete BEFORE DELETE ON risk.risk_holds FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_holds_no_truncate BEFORE TRUNCATE ON risk.risk_holds FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 6. Screening foundation (ADR-045 §8): TEST only, sandbox simulator, synthetic references, no identity data.
--    Provider result ≠ analyst review ≠ compliance decision. Machine 'risk_screening'.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_screening_requests (
  id                  uuid        NOT NULL,
  subject_type        text        NOT NULL,
  subject_id          uuid        NOT NULL,
  provider_code       text        NOT NULL,
  environment         text        NOT NULL DEFAULT 'TEST',
  synthetic_reference text        NOT NULL,
  status              text        NOT NULL,
  provider_reference  text,
  result_sequence     integer     NOT NULL DEFAULT 0,
  match_count         integer     NOT NULL DEFAULT 0,
  timeout_at          timestamptz NOT NULL,
  next_poll_at        timestamptz,
  poll_attempts       integer     NOT NULL DEFAULT 0,
  last_error          text,
  requested_by        uuid        NOT NULL,
  idempotency_key     uuid        NOT NULL,
  reviewed_by         uuid,
  reviewed_at         timestamptz,
  review_step_up_at   timestamptz,
  review_reason       text,
  case_id             uuid,
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_screening_requests PRIMARY KEY (id),
  CONSTRAINT fk_risk_screening_requests_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_risk_screening_requests_case FOREIGN KEY (case_id) REFERENCES risk.risk_cases (id) ON DELETE RESTRICT,
  CONSTRAINT uq_risk_screening_requests_idem UNIQUE (requested_by, idempotency_key),
  CONSTRAINT uq_risk_screening_requests_provider_ref UNIQUE (provider_code, provider_reference),
  CONSTRAINT ck_risk_screening_requests_subject CHECK (subject_type IN ('USER','ORGANISATION','BENEFICIARY')),
  CONSTRAINT ck_risk_screening_requests_provider CHECK (provider_code = 'sandbox'),            -- no provider is selected
  CONSTRAINT ck_risk_screening_requests_test_only_stage13 CHECK (environment = 'TEST'),
  CONSTRAINT ck_risk_screening_requests_reference CHECK (synthetic_reference ~ '^TEST-[A-Z0-9_-]{3,60}$'),
  CONSTRAINT ck_risk_screening_requests_status CHECK (status IN ('PENDING','POTENTIAL_MATCH','NO_MATCH_REPORTED','REVIEW_REQUIRED',
    'CONFIRMED_MATCH','CLEARED','ERROR')),
  -- CONFIRMED_MATCH and CLEARED are analyst decisions, never provider results; the analyst is not the requester
  CONSTRAINT ck_risk_screening_requests_review CHECK ((status IN ('CONFIRMED_MATCH','CLEARED')) = (reviewed_by IS NOT NULL)
    AND (reviewed_by IS NULL OR (reviewed_at IS NOT NULL AND review_step_up_at IS NOT NULL AND review_reason IS NOT NULL
    AND reviewed_by <> requested_by))),
  CONSTRAINT ck_risk_screening_requests_counts CHECK (result_sequence >= 0 AND match_count >= 0 AND poll_attempts >= 0)
);
CREATE INDEX ix_risk_screening_requests_subject ON risk.risk_screening_requests (subject_type, subject_id, created_at DESC);
CREATE INDEX ix_risk_screening_requests_poll ON risk.risk_screening_requests (next_poll_at) WHERE status = 'PENDING';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('risk_screening', '',                  'PENDING'),
  ('risk_screening', 'PENDING',           'NO_MATCH_REPORTED'),
  ('risk_screening', 'PENDING',           'POTENTIAL_MATCH'),
  ('risk_screening', 'PENDING',           'REVIEW_REQUIRED'),
  ('risk_screening', 'PENDING',           'ERROR'),
  ('risk_screening', 'ERROR',             'PENDING'),
  ('risk_screening', 'POTENTIAL_MATCH',   'REVIEW_REQUIRED'),
  ('risk_screening', 'POTENTIAL_MATCH',   'CONFIRMED_MATCH'),
  ('risk_screening', 'POTENTIAL_MATCH',   'CLEARED'),
  ('risk_screening', 'REVIEW_REQUIRED',   'CONFIRMED_MATCH'),
  ('risk_screening', 'REVIEW_REQUIRED',   'CLEARED');
CREATE TRIGGER trg_risk_screening_requests_guard_status BEFORE INSERT OR UPDATE OF status ON risk.risk_screening_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('risk_screening');
CREATE TRIGGER trg_risk_screening_requests_identity BEFORE UPDATE ON risk.risk_screening_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('subject_type', 'subject_id', 'provider_code', 'environment', 'synthetic_reference',
    'requested_by', 'idempotency_key', 'created_at');
CREATE TRIGGER trg_risk_screening_requests_updated_at BEFORE UPDATE ON risk.risk_screening_requests FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_risk_screening_requests_no_delete BEFORE DELETE ON risk.risk_screening_requests FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- provider results as received (verified callbacks and polls): immutable; one per provider event
CREATE TABLE risk.risk_screening_results (
  id                 uuid        NOT NULL,
  request_id         uuid        NOT NULL,
  provider_event_id  text        NOT NULL,
  source             text        NOT NULL,
  sequence           integer     NOT NULL,
  reported_status    text        NOT NULL,
  matches            jsonb       NOT NULL DEFAULT '[]',
  applied            boolean     NOT NULL,
  label              text        NOT NULL DEFAULT 'TEST_ONLY',
  received_at        timestamptz NOT NULL,
  CONSTRAINT pk_risk_screening_results PRIMARY KEY (id),
  CONSTRAINT fk_risk_screening_results_request FOREIGN KEY (request_id) REFERENCES risk.risk_screening_requests (id) ON DELETE RESTRICT,
  CONSTRAINT uq_risk_screening_results_event UNIQUE (request_id, provider_event_id),
  CONSTRAINT ck_risk_screening_results_source CHECK (source IN ('CALLBACK','POLL','SUBMIT')),
  CONSTRAINT ck_risk_screening_results_status CHECK (reported_status IN ('PENDING','POTENTIAL_MATCH','NO_MATCH_REPORTED','ERROR')),
  CONSTRAINT ck_risk_screening_results_matches CHECK (jsonb_typeof(matches) = 'array'),
  CONSTRAINT ck_risk_screening_results_label CHECK (label = 'TEST_ONLY')
);
CREATE INDEX ix_risk_screening_results_request ON risk.risk_screening_results (request_id, received_at);
CREATE TRIGGER trg_risk_screening_results_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_screening_results FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- the sandbox screening simulator's own state (development only; like the payment sandbox's tables)
CREATE TABLE risk.sandbox_screenings (
  provider_reference text        NOT NULL,
  scenario           text        NOT NULL,
  request_id         uuid        NOT NULL,
  status             text        NOT NULL,
  ready_at           timestamptz NOT NULL,
  callbacks_sent     integer     NOT NULL DEFAULT 0,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_sandbox_screenings PRIMARY KEY (provider_reference),
  CONSTRAINT ck_sandbox_screenings_scenario CHECK (scenario IN ('NO_MATCH','POTENTIAL_MATCH','MULTIPLE_MATCHES','PROVIDER_TIMEOUT',
    'PROVIDER_ERROR','DUPLICATE_CALLBACK','OUT_OF_ORDER_CALLBACK','INVALID_SIGNATURE','DELAYED_RESULT'))
);

-- -----------------------------------------------------------------------------------------------------
-- 7. Monitoring events (alerts, velocity breaches, expiries, rejected callbacks, reconciliation findings): append-only,
--    deduplicated.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_monitoring_events (
  id            uuid        NOT NULL,
  event_type    text        NOT NULL,
  severity      text        NOT NULL,
  subject_type  text,
  subject_id    uuid,
  detail        jsonb       NOT NULL DEFAULT '{}',
  dedupe_key    text        NOT NULL,
  occurred_at   timestamptz NOT NULL,
  CONSTRAINT pk_risk_monitoring_events PRIMARY KEY (id),
  CONSTRAINT uq_risk_monitoring_events_dedupe UNIQUE (dedupe_key),
  CONSTRAINT ck_risk_monitoring_events_type CHECK (event_type IN ('RULE_MONITORING_MATCH','HOLD_EXPIRED','CASE_OVERDUE','CASE_UNASSIGNED_CRITICAL',
    'SCREENING_TIMEOUT','SCREENING_CALLBACK_REJECTED','SCREENING_STALE','RECON_MISSING_EVALUATION','RECON_DUPLICATE_CASES',
    'RECON_ORPHANED_HOLD','RECON_CONFLICTING_RESTRICTION','RECON_UNPROCESSED_EVENT','RECON_RULE_EXPIRED','RECON_EVIDENCE_HASH_MISMATCH',
    'RECON_RULE_UNPARSEABLE','RECON_HOLD_PAST_EXPIRY')),
  CONSTRAINT ck_risk_monitoring_events_severity CHECK (severity IN ('INFO','LOW','MEDIUM','HIGH')),
  CONSTRAINT ck_risk_monitoring_events_detail CHECK (jsonb_typeof(detail) = 'object')
);
CREATE INDEX ix_risk_monitoring_events_recent ON risk.risk_monitoring_events (occurred_at DESC, id DESC);
CREATE TRIGGER trg_risk_monitoring_events_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_monitoring_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 8. Compliance: a risk case can be linked to a compliance case (escalation, ADR-045 §6).
-- -----------------------------------------------------------------------------------------------------
ALTER TABLE compliance.compliance_case_links DROP CONSTRAINT ck_compliance_case_links_subject;
ALTER TABLE compliance.compliance_case_links ADD CONSTRAINT ck_compliance_case_links_subject CHECK (subject_type IN ('USER',
  'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION', 'CAMPAIGN', 'KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT',
  'PAYOUT', 'CAMPAIGN_REVIEW', 'RISK_CASE'));
ALTER TABLE compliance.compliance_case_links DROP CONSTRAINT ck_compliance_case_links_role_kind;
ALTER TABLE compliance.compliance_case_links ADD CONSTRAINT ck_compliance_case_links_role_kind CHECK ((role = 'RELATED_OBJECT') =
  (subject_type IN ('KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT', 'PAYOUT', 'CAMPAIGN_REVIEW', 'RISK_CASE')));

-- -----------------------------------------------------------------------------------------------------
-- 9. Permissions (all mutations require step-up; maker-checker is enforced per object).
-- -----------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  -- risk.view (Stage 5: signals and assessments, held by KYC reviewers) stays as it is; the operations dashboard has its own permission
  (md5('permission:risk.operations.view')::uuid, 'risk.operations.view', 'View risk operations: evaluations, rules, cases, holds, screening and risk reports', true, false),
  (md5('permission:risk.case.work')::uuid, 'risk.case.work', 'Work risk cases: assign, triage, notes and evidence, propose a decision, escalate', true, true),
  (md5('permission:risk.case.approve')::uuid, 'risk.case.approve', 'Approve or return a proposed risk case decision (checker)', true, true),
  (md5('permission:risk.rule.request')::uuid, 'risk.rule.request', 'Draft, submit, activate, suspend and retire risk rule versions (maker)', true, true),
  (md5('permission:risk.rule.approve')::uuid, 'risk.rule.approve', 'Approve, reject or reactivate risk rule versions (checker)', true, true),
  (md5('permission:risk.hold.create')::uuid, 'risk.hold.create', 'Place risk holds and request their release', true, true),
  (md5('permission:risk.hold.release')::uuid, 'risk.hold.release', 'Approve the release of a risk hold (checker)', true, true),
  (md5('permission:risk.screening.request')::uuid, 'risk.screening.request', 'Request screening of a subject (TEST simulator only)', true, true),
  (md5('permission:risk.screening.review')::uuid, 'risk.screening.review', 'Review screening results (confirm or clear a potential match)', true, true),
  (md5('permission:risk.sandbox.control')::uuid, 'risk.sandbox.control', 'Drive the screening simulator (development and test environments only)', false, false);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES ('COMPLIANCE', 'risk.operations.view'), ('COMPLIANCE', 'risk.case.work'), ('COMPLIANCE', 'risk.case.approve'), ('COMPLIANCE', 'risk.rule.request'),
             ('COMPLIANCE', 'risk.rule.approve'), ('COMPLIANCE', 'risk.hold.create'), ('COMPLIANCE', 'risk.hold.release'),
             ('COMPLIANCE', 'risk.screening.request'), ('COMPLIANCE', 'risk.screening.review'), ('FINANCE', 'risk.operations.view'),
             ('ADMIN', 'risk.sandbox.control')) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN ('risk.operations.view',
  'risk.case.work','risk.case.approve','risk.rule.request','risk.rule.approve','risk.hold.create','risk.hold.release','risk.screening.request',
  'risk.screening.review','risk.sandbox.control'));
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('risk.operations.view','risk.case.work','risk.case.approve','risk.rule.request','risk.rule.approve',
  'risk.hold.create','risk.hold.release','risk.screening.request','risk.screening.review','risk.sandbox.control');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
ALTER TABLE compliance.compliance_case_links DROP CONSTRAINT ck_compliance_case_links_role_kind;
ALTER TABLE compliance.compliance_case_links ADD CONSTRAINT ck_compliance_case_links_role_kind CHECK ((role = 'RELATED_OBJECT') =
  (subject_type IN ('KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT', 'PAYOUT', 'CAMPAIGN_REVIEW', 'RISK_CASE'))) NOT VALID;
ALTER TABLE compliance.compliance_case_links DROP CONSTRAINT ck_compliance_case_links_subject;
-- NOT VALID: links to risk cases created while this migration was applied stay (compliance history is never deleted)
ALTER TABLE compliance.compliance_case_links ADD CONSTRAINT ck_compliance_case_links_subject CHECK (subject_type IN ('USER',
  'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION', 'CAMPAIGN', 'KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT',
  'PAYOUT', 'CAMPAIGN_REVIEW')) NOT VALID;
DROP TABLE risk.risk_monitoring_events;
DROP TABLE risk.sandbox_screenings;
DROP TABLE risk.risk_screening_results;
DROP TABLE risk.risk_screening_requests;
DROP TABLE risk.risk_holds;
DROP FUNCTION risk.risk_holds_guard();
DROP TABLE risk.risk_case_access_log;
DROP TABLE risk.risk_case_evidence;
DROP TABLE risk.risk_case_assignments;
DROP TABLE risk.risk_signal_reviews;
DROP TABLE risk.risk_evaluation_actions;
DROP TABLE risk.risk_subject_profiles;
DROP TABLE risk.risk_evaluations;
DROP TABLE risk.risk_cases;
DROP FUNCTION risk.risk_cases_guard();
DROP SEQUENCE risk.risk_case_number_seq;
DROP TABLE risk.risk_rule_versions;
DROP FUNCTION risk.risk_rule_versions_guard();
DROP TABLE risk.risk_rules;
DROP TABLE risk.risk_events;
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine IN ('risk_rule_version','risk_case','risk_hold','risk_screening');
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
DROP INDEX risk.ix_risk_signals_recorded;
DROP INDEX risk.ix_risk_signals_category;
ALTER TABLE risk.risk_signals DROP CONSTRAINT ck_risk_signals_module;
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_module CHECK (source_module IN ('auth', 'users', 'storage', 'payments', 'payouts',
  'campaigns', 'kyc', 'organisations', 'beneficiaries', 'psp', 'reconciliation', 'compliance', 'risk')) NOT VALID;
ALTER TABLE risk.risk_signals DROP CONSTRAINT ck_risk_signals_subject;
ALTER TABLE risk.risk_signals ADD CONSTRAINT ck_risk_signals_subject CHECK (subject_type IN ('USER', 'ORGANISATION', 'BENEFICIARY',
  'PAYOUT_DESTINATION', 'KYC_CASE', 'KYB_CASE', 'CAMPAIGN', 'PAYMENT', 'PAYOUT', 'DEVICE', 'DONOR_GUEST')) NOT VALID;
ALTER TABLE risk.risk_signals DROP CONSTRAINT ck_risk_signals_category, DROP CONSTRAINT ck_risk_signals_severity,
  DROP CONSTRAINT ck_risk_signals_confidence, DROP CONSTRAINT ck_risk_signals_kind, DROP CONSTRAINT ck_risk_signals_environment,
  DROP CONSTRAINT ck_risk_signals_correlation, DROP CONSTRAINT ck_risk_signals_evidence_ref;
ALTER TABLE risk.risk_signals DROP COLUMN derived_by_rule_version_id, DROP COLUMN evidence_ref, DROP COLUMN correlation_id,
  DROP COLUMN environment, DROP COLUMN kind, DROP COLUMN confidence, DROP COLUMN severity, DROP COLUMN category;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
