-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate).
-- File 0008: schema `risk` (owner module: risk). Baseline §5.9, §6 resolutions 1 and 3.
-- Explained in docs/database/compliance-schema.md.
--
-- Tables: limits, limit_change_requests, holds, hold_events, risk_signals, risk_assessments,
--         monitoring_rules, monitoring_alerts.
-- No C3 data lives in this schema (DATABASE §7). Facts/details are IDs, codes and fingerprints only.
-- Cross-schema FKs: only to app.users and app.currencies (baseline §10). References to compliance cases,
-- ledger transactions, campaigns etc. are plain uuids (higher-layer or domain-agnostic owners).
-- No legal threshold, duration or retention value appears in this file: every value is a risk.limits row.
-- =====================================================================================================

-- -----------------------------------------------------------------------------------------------------
-- risk.limits — versioned limit registry (REGULATORY / PROVIDER / INTERNAL_RISK).
-- aml-risk-framework §5, payout-eligibility-and-controls §4. A version is immutable once approved: every
-- change is a new version (supersedes_id). The only permitted mutations are
--   PROPOSED -> APPROVED | REJECTED (sets approved_* / counsel_ref / approval_request_id once), and
--   APPROVED -> RETIRED (sets retired_* once).
-- Engine resolution (Go, platform/limits): for a (limit_key, scope, currency) the effective value of each
-- limit_type is the APPROVED version with the highest version number whose [effective_from, effective_to)
-- contains now(); the most restrictive type wins; no effective row => fail closed (EC-21).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.limits (
  id                   uuid        NOT NULL,
  limit_key            text        NOT NULL,
  version              integer     NOT NULL,
  limit_type           text        NOT NULL,
  scope_type           text        NOT NULL,
  scope_value          text,
  value_kind           text        NOT NULL,
  currency             char(3)     REFERENCES app.currencies(code),
  value_minor          bigint,                          -- MONEY: integer minor units of `currency`
  value_count          bigint,                          -- COUNT
  value_seconds        bigint,                          -- DURATION (seconds)
  value_bp             integer,                         -- BASIS_POINTS (may exceed 10000 for multiples)
  source_type          text        NOT NULL,
  source_ref           text        NOT NULL,            -- REQ-xxx | PCR-xxx | CONTRACT:<ref> | PROVIDER_DOC:<ref> | DEC-<id> | PD-xx
  source_citation      text,                            -- instrument + section + URL + accessed date (REGULATORY)
  legal_review_ref     text,                            -- LR-xxx while applicability is unconfirmed
  counsel_ref          text,                            -- counsel confirmation reference; required to approve REGULATORY
  approval_owner_role  text        NOT NULL,
  status               text        NOT NULL,
  made_by              uuid        NOT NULL REFERENCES app.users(id),
  approved_by          uuid        REFERENCES app.users(id),
  approved_at          timestamptz,
  approval_request_id  uuid,                            -- FK added below (circular with limit_change_requests)
  retired_by           uuid        REFERENCES app.users(id),
  retired_at           timestamptz,
  retire_request_id    uuid,
  effective_from       timestamptz NOT NULL,
  effective_to         timestamptz,
  review_by            date        NOT NULL,
  supersedes_id        uuid        REFERENCES risk.limits(id),
  created_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_limits PRIMARY KEY (id),
  CONSTRAINT uq_limits_version UNIQUE NULLS NOT DISTINCT (limit_key, limit_type, scope_type, scope_value, currency, version),
  CONSTRAINT ck_limits_key CHECK (limit_key ~ '^[a-z][a-z0-9_]*(\.[a-z0-9_{}]+)+$'),
  CONSTRAINT ck_limits_version CHECK (version >= 1 AND ((version = 1) = (supersedes_id IS NULL))),
  CONSTRAINT ck_limits_type CHECK (limit_type IN ('REGULATORY','PROVIDER','INTERNAL_RISK')),
  CONSTRAINT ck_limits_scope_type CHECK (scope_type IN ('GLOBAL','CURRENCY','PROVIDER','RAIL','CATEGORY','ORG_TYPE',
                                                        'RISK_RATING','VERIFICATION_LEVEL','HOLD_TYPE','CASE_TYPE')),
  CONSTRAINT ck_limits_scope_value CHECK ((scope_type = 'GLOBAL') = (scope_value IS NULL)),
  CONSTRAINT ck_limits_value_kind CHECK (value_kind IN ('MONEY','COUNT','DURATION','BASIS_POINTS')),
  -- exactly the value column matching value_kind; currency iff money
  CONSTRAINT ck_limits_value_shape CHECK (
        num_nonnulls(value_minor, value_count, value_seconds, value_bp) = 1
    AND (value_kind <> 'MONEY'        OR value_minor   IS NOT NULL)
    AND (value_kind <> 'COUNT'        OR value_count   IS NOT NULL)
    AND (value_kind <> 'DURATION'     OR value_seconds IS NOT NULL)
    AND (value_kind <> 'BASIS_POINTS' OR value_bp      IS NOT NULL)
    AND ((value_kind = 'MONEY') = (currency IS NOT NULL))),
  CONSTRAINT ck_limits_value_nonneg CHECK (coalesce(value_minor, value_count, value_seconds, value_bp) >= 0),
  CONSTRAINT ck_limits_source_type CHECK (source_type IN ('STATUTE','REGULATION','DIRECTIVE','GUIDELINE',
                                                          'PROVIDER_DOC','PROVIDER_CONTRACT','INTERNAL_DECISION')),
  -- type <-> source consistency: REGULATORY cites a REQ entry and a citation; PROVIDER cites a provider
  -- document/contract (PCR id until confirmed); INTERNAL_RISK cites a decision record.
  CONSTRAINT ck_limits_source_regulatory CHECK (limit_type <> 'REGULATORY' OR (
        source_type IN ('STATUTE','REGULATION','DIRECTIVE','GUIDELINE')
    AND source_ref ~ '^REQ-[0-9]{3}$' AND source_citation IS NOT NULL
    AND (legal_review_ref IS NOT NULL OR counsel_ref IS NOT NULL))),
  CONSTRAINT ck_limits_source_provider CHECK (limit_type <> 'PROVIDER' OR (
        source_type IN ('PROVIDER_DOC','PROVIDER_CONTRACT')
    AND source_ref ~ '^(PCR-[0-9]{3}|CONTRACT:.+|PROVIDER_DOC:.+)$')),
  CONSTRAINT ck_limits_source_internal CHECK (limit_type <> 'INTERNAL_RISK' OR (
        source_type = 'INTERNAL_DECISION' AND source_ref ~ '^(DEC-[A-Za-z0-9-]+|PD-[0-9]{2})$')),
  CONSTRAINT ck_limits_lr_ref CHECK (legal_review_ref IS NULL OR legal_review_ref ~ '^LR-[0-9]{3}$'),
  CONSTRAINT ck_limits_owner_role CHECK (approval_owner_role IN ('COMPLIANCE','FINANCE','COMPLIANCE_AND_FINANCE')),
  CONSTRAINT ck_limits_owner_regulatory CHECK (limit_type <> 'REGULATORY' OR approval_owner_role = 'COMPLIANCE'),
  CONSTRAINT ck_limits_status CHECK (status IN ('PROPOSED','APPROVED','REJECTED','RETIRED')),
  -- maker-checker: the approver is never the maker
  CONSTRAINT ck_limits_maker_checker CHECK (approved_by IS NULL OR approved_by <> made_by),
  CONSTRAINT ck_limits_approved_fields CHECK (
        (status IN ('APPROVED','RETIRED','REJECTED')) = (approved_by IS NOT NULL AND approved_at IS NOT NULL AND approval_request_id IS NOT NULL)),
  CONSTRAINT ck_limits_regulatory_counsel CHECK (limit_type <> 'REGULATORY' OR status NOT IN ('APPROVED','RETIRED') OR counsel_ref IS NOT NULL),
  CONSTRAINT ck_limits_retired_fields CHECK ((status = 'RETIRED') = (retired_at IS NOT NULL AND retired_by IS NOT NULL AND retire_request_id IS NOT NULL)),
  CONSTRAINT ck_limits_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);
COMMENT ON TABLE risk.limits IS 'C1. Versioned limit records; immutable once approved. aml-risk-framework §5.';
CREATE UNIQUE INDEX uq_limits_supersedes ON risk.limits (supersedes_id) WHERE supersedes_id IS NOT NULL;
CREATE INDEX ix_limits_lookup ON risk.limits (limit_key, scope_type, scope_value, currency, status, version DESC);
CREATE INDEX ix_limits_review_by ON risk.limits (review_by) WHERE status = 'APPROVED';

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('risk_limit', '',         'PROPOSED'),
  ('risk_limit', 'PROPOSED', 'APPROVED'),
  ('risk_limit', 'PROPOSED', 'REJECTED'),
  ('risk_limit', 'APPROVED', 'RETIRED');

-- -----------------------------------------------------------------------------------------------------
-- risk.limit_change_requests — the maker-checker pending object for a limit version (operational-controls
-- §3 "Limit or threshold change", request expiry 7 d — the expiry itself is computed by the service from
-- limit `approval.limit_change.validity`, never hard-coded here).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.limit_change_requests (
  id               uuid        NOT NULL,
  limit_id         uuid        NOT NULL REFERENCES risk.limits(id),
  action           text        NOT NULL,
  status           text        NOT NULL,
  requested_by     uuid        NOT NULL REFERENCES app.users(id),
  requested_at     timestamptz NOT NULL DEFAULT now(),
  justification    text        NOT NULL,
  payload_sha256   bytea       NOT NULL,                  -- hash of the canonical limit snapshot the checker saw
  expires_at       timestamptz NOT NULL,
  decided_by       uuid        REFERENCES app.users(id),
  decided_at       timestamptz,
  decision_note    text,
  checker_step_up_at timestamptz,                         -- fresh MFA time of the checker (STEP_UP_MAX_AGE in service)
  version          integer     NOT NULL DEFAULT 1,
  CONSTRAINT pk_limit_change_requests PRIMARY KEY (id),
  CONSTRAINT ck_lcr_action CHECK (action IN ('ACTIVATE','RETIRE')),
  CONSTRAINT ck_lcr_status CHECK (status IN ('PENDING','APPROVED','REJECTED','EXPIRED','WITHDRAWN')),
  CONSTRAINT ck_lcr_hash CHECK (octet_length(payload_sha256) = 32),
  CONSTRAINT ck_lcr_expiry CHECK (expires_at > requested_at),
  CONSTRAINT ck_lcr_justification CHECK (length(btrim(justification)) > 0),
  CONSTRAINT ck_lcr_maker_checker CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT ck_lcr_decided CHECK ((status IN ('APPROVED','REJECTED')) = (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CONSTRAINT ck_lcr_step_up CHECK (status <> 'APPROVED' OR checker_step_up_at IS NOT NULL)
);
COMMENT ON TABLE risk.limit_change_requests IS 'C1. Maker-checker requests for limit activation/retirement.';
CREATE UNIQUE INDEX uq_lcr_one_pending ON risk.limit_change_requests (limit_id, action) WHERE status = 'PENDING';

ALTER TABLE risk.limits ADD CONSTRAINT fk_limits_approval_request
  FOREIGN KEY (approval_request_id) REFERENCES risk.limit_change_requests(id);
ALTER TABLE risk.limits ADD CONSTRAINT fk_limits_retire_request
  FOREIGN KEY (retire_request_id) REFERENCES risk.limit_change_requests(id);

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

-- Limit versions: immutable content; approval/retirement require a matching APPROVED request decided by
-- the same checker.
CREATE TRIGGER trg_limits_guard_status BEFORE INSERT OR UPDATE OF status ON risk.limits
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('risk_limit');
CREATE TRIGGER trg_limits_no_delete BEFORE DELETE ON risk.limits
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_limits_no_truncate BEFORE TRUNCATE ON risk.limits
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION risk.limits_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  changed text[];
  req risk.limit_change_requests%ROWTYPE;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.status <> 'PROPOSED' THEN
      RAISE EXCEPTION 'limits: a new version must be inserted as PROPOSED' USING ERRCODE = 'check_violation';
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
  IF OLD.status = 'PROPOSED' AND NEW.status IN ('APPROVED','REJECTED') THEN
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
-- risk.monitoring_rules — versioned rule definitions (transaction-monitoring §3, §5). Parameters are
-- limit_key references only (values live in risk.limits). Content is frozen once a version leaves DRAFT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.monitoring_rules (
  id               uuid        NOT NULL,
  rule_code        text        NOT NULL,
  version          integer     NOT NULL,
  status           text        NOT NULL,
  title            text        NOT NULL,
  severity_default text        NOT NULL,
  action_default   text        NOT NULL,
  param_limit_keys text[]      NOT NULL DEFAULT '{}',
  logic_ref        text        NOT NULL,                  -- Go rule implementation id + version (logic is code)
  scope_types      text[]      NOT NULL DEFAULT '{}',     -- e.g. {CATEGORY} for category-calibrated params
  created_by       uuid        NOT NULL REFERENCES app.users(id),
  approved_by      uuid        REFERENCES app.users(id),
  approved_at      timestamptz,
  activated_at     timestamptz,
  retired_at       timestamptz,
  supersedes_id    uuid        REFERENCES risk.monitoring_rules(id),
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_monitoring_rules PRIMARY KEY (id),
  CONSTRAINT uq_monitoring_rules_version UNIQUE (rule_code, version),
  CONSTRAINT ck_monitoring_rules_code CHECK (rule_code ~ '^[A-Z]{2,5}-[0-9]{2,3}$'),
  CONSTRAINT ck_monitoring_rules_version CHECK (version >= 1),
  CONSTRAINT ck_monitoring_rules_status CHECK (status IN ('DRAFT','SHADOW','ACTIVE','RETIRED')),
  CONSTRAINT ck_monitoring_rules_severity CHECK (severity_default IN ('S1','S2','S3')),
  CONSTRAINT ck_monitoring_rules_action CHECK (action_default IN ('NONE','PAYOUT_HOLD','DESTINATION_HOLD','ACCOUNT_HOLD',
                                               'DISPUTE_HOLD','STEP_UP','BLOCK_CARD_METHOD','DUAL_APPROVAL','EDD','REFUND_HOLD')),
  CONSTRAINT ck_monitoring_rules_params CHECK (array_position(param_limit_keys, NULL) IS NULL),
  CONSTRAINT ck_monitoring_rules_maker_checker CHECK (approved_by IS NULL OR approved_by <> created_by),
  -- leaving DRAFT (shadow or live) requires approval (operational-controls §3: rule activation = limit change)
  CONSTRAINT ck_monitoring_rules_approved CHECK (status = 'DRAFT' OR (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  CONSTRAINT ck_monitoring_rules_active_at CHECK (status <> 'ACTIVE' OR activated_at IS NOT NULL),
  CONSTRAINT ck_monitoring_rules_retired_at CHECK ((status = 'RETIRED') = (retired_at IS NOT NULL))
);
COMMENT ON TABLE risk.monitoring_rules IS 'C1. Versioned monitoring rules; params are limit keys. transaction-monitoring §5.';
-- one live (shadow or active) version per rule
CREATE UNIQUE INDEX uq_monitoring_rules_live ON risk.monitoring_rules (rule_code) WHERE status IN ('SHADOW','ACTIVE');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('monitoring_rule', '',       'DRAFT'),
  ('monitoring_rule', 'DRAFT',  'SHADOW'),
  ('monitoring_rule', 'DRAFT',  'RETIRED'),
  ('monitoring_rule', 'SHADOW', 'ACTIVE'),
  ('monitoring_rule', 'SHADOW', 'RETIRED'),
  ('monitoring_rule', 'ACTIVE', 'RETIRED');
CREATE TRIGGER trg_monitoring_rules_guard_status BEFORE INSERT OR UPDATE OF status ON risk.monitoring_rules
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('monitoring_rule');
CREATE TRIGGER trg_monitoring_rules_no_delete BEFORE DELETE ON risk.monitoring_rules
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_monitoring_rules_no_truncate BEFORE TRUNCATE ON risk.monitoring_rules
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION risk.monitoring_rules_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.status <> 'DRAFT' AND NOT changed <@ ARRAY['status','activated_at','retired_at'] THEN
    RAISE EXCEPTION 'monitoring rule %: content frozen after DRAFT (attempted: %) — create a new version', OLD.rule_code, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_monitoring_rules_guard BEFORE UPDATE ON risk.monitoring_rules
  FOR EACH ROW EXECUTE FUNCTION risk.monitoring_rules_guard();

-- -----------------------------------------------------------------------------------------------------
-- risk.monitoring_alerts — one alert per (rule, subject, window bucket) while open (transaction-monitoring
-- §4). Disposition is set once at closure; corrections are case notes. Repeated firings are recorded as
-- risk_signals rows pointing at the alert (fire_count/last_fired_at are the only mutable counters).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.monitoring_alerts (
  id                    uuid        NOT NULL,
  rule_id               uuid        NOT NULL REFERENCES risk.monitoring_rules(id),   -- the exact rule version
  severity              text        NOT NULL,
  mode                  text        NOT NULL,              -- SHADOW alerts never act
  subject_type          text        NOT NULL,
  subject_id            uuid        NOT NULL,
  related_refs          jsonb       NOT NULL DEFAULT '{}', -- ids only (payment_id, payout_id, destination_id...)
  facts                 jsonb       NOT NULL DEFAULT '{}', -- triggering facts: counts, amounts (minor+currency), fingerprints; never C3
  dedupe_key            text        NOT NULL,
  status                text        NOT NULL,
  fire_count            integer     NOT NULL DEFAULT 1,
  first_fired_at        timestamptz NOT NULL DEFAULT now(),
  last_fired_at         timestamptz NOT NULL DEFAULT now(),
  case_id               uuid,                              -- compliance case (owned by compliance; no FK, layer rule)
  disposition           text,
  disposition_reason    text,
  closed_by             uuid        REFERENCES app.users(id),
  second_reviewer_id    uuid        REFERENCES app.users(id),
  closed_at             timestamptz,
  CONSTRAINT pk_monitoring_alerts PRIMARY KEY (id),
  CONSTRAINT ck_monitoring_alerts_severity CHECK (severity IN ('S1','S2','S3')),
  CONSTRAINT ck_monitoring_alerts_mode CHECK (mode IN ('SHADOW','ACTIVE')),
  CONSTRAINT ck_monitoring_alerts_subject CHECK (subject_type IN ('USER','ORGANISATION','CAMPAIGN','BENEFICIARY',
                                                 'PAYOUT_DESTINATION','PAYMENT','PAYOUT','DEVICE','DONOR_GUEST')),
  CONSTRAINT ck_monitoring_alerts_status CHECK (status IN ('OPEN','CLOSED')),
  CONSTRAINT ck_monitoring_alerts_fire_count CHECK (fire_count >= 1 AND last_fired_at >= first_fired_at),
  CONSTRAINT ck_monitoring_alerts_disposition CHECK (disposition IS NULL OR disposition IN
     ('TRUE_POSITIVE_FRAUD','TRUE_POSITIVE_AML_CONCERN','FALSE_POSITIVE','EXPECTED_BEHAVIOUR','INSUFFICIENT_INFO')),
  CONSTRAINT ck_monitoring_alerts_closed CHECK ((status = 'CLOSED') = (disposition IS NOT NULL AND disposition_reason IS NOT NULL
                                                                       AND closed_by IS NOT NULL AND closed_at IS NOT NULL)),
  -- an S1 false positive needs a second, different reviewer (transaction-monitoring §4)
  CONSTRAINT ck_monitoring_alerts_s1_fp CHECK (NOT (severity = 'S1' AND disposition = 'FALSE_POSITIVE') OR
                                               (second_reviewer_id IS NOT NULL AND second_reviewer_id <> closed_by)),
  CONSTRAINT ck_monitoring_alerts_second CHECK (second_reviewer_id IS NULL OR second_reviewer_id <> closed_by)
);
COMMENT ON TABLE risk.monitoring_alerts IS 'C2. Monitoring alerts; facts contain no C3. transaction-monitoring §4, §7.';
CREATE UNIQUE INDEX uq_monitoring_alerts_open_dedupe ON risk.monitoring_alerts (dedupe_key) WHERE status = 'OPEN';
CREATE INDEX ix_monitoring_alerts_subject ON risk.monitoring_alerts (subject_type, subject_id, status);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('monitoring_alert', '',     'OPEN'),
  ('monitoring_alert', 'OPEN', 'CLOSED');
CREATE TRIGGER trg_monitoring_alerts_guard_status BEFORE INSERT OR UPDATE OF status ON risk.monitoring_alerts
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('monitoring_alert');
CREATE TRIGGER trg_monitoring_alerts_no_delete BEFORE DELETE ON risk.monitoring_alerts
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_monitoring_alerts_no_truncate BEFORE TRUNCATE ON risk.monitoring_alerts
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION risk.monitoring_alerts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.status = 'CLOSED' THEN
    RAISE EXCEPTION 'monitoring alert % is closed; its disposition is final', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.case_id IS NOT NULL AND NEW.case_id IS DISTINCT FROM OLD.case_id THEN
    RAISE EXCEPTION 'monitoring alert %: case_id is set once', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NOT changed <@ ARRAY['fire_count','last_fired_at','case_id','status','disposition','disposition_reason',
                          'closed_by','second_reviewer_id','closed_at'] THEN
    RAISE EXCEPTION 'monitoring alert %: only counters, case link and closure may change (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.fire_count < OLD.fire_count THEN
    RAISE EXCEPTION 'monitoring alert %: fire_count never decreases', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_monitoring_alerts_guard BEFORE UPDATE ON risk.monitoring_alerts
  FOR EACH ROW EXECUTE FUNCTION risk.monitoring_alerts_guard();

-- -----------------------------------------------------------------------------------------------------
-- risk.risk_signals — append-only facts consumed from domain events (idempotent consumer: one row per
-- (source_event_id, signal_type)). Also records repeated firings of an open alert (alert_id).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.risk_signals (
  id               uuid        NOT NULL,
  signal_type      text        NOT NULL,                  -- e.g. DEVICE_REUSE, SIM_SWAP, DESTINATION_CHANGED, CARD_FAILURE
  subject_type     text        NOT NULL,
  subject_id       uuid        NOT NULL,
  source_module    text        NOT NULL,
  source_event_id  uuid        NOT NULL,                  -- outbox event id (dedupe)
  observed_at      timestamptz NOT NULL,
  facts            jsonb       NOT NULL DEFAULT '{}',     -- fingerprints (HMAC) and codes only; never raw instrument/ID numbers
  alert_id         uuid        REFERENCES risk.monitoring_alerts(id),
  recorded_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_signals PRIMARY KEY (id),
  CONSTRAINT uq_risk_signals_source UNIQUE (source_event_id, signal_type),
  CONSTRAINT ck_risk_signals_type CHECK (signal_type ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_risk_signals_subject CHECK (subject_type IN ('USER','ORGANISATION','CAMPAIGN','BENEFICIARY',
                                            'PAYOUT_DESTINATION','PAYMENT','PAYOUT','DEVICE','DONOR_GUEST')),
  CONSTRAINT ck_risk_signals_module CHECK (source_module IN ('auth','users','payments','payouts','campaigns','kyc',
                                           'organisations','beneficiaries','psp','reconciliation','compliance','risk'))
);
COMMENT ON TABLE risk.risk_signals IS 'C2. Append-only risk facts (no C3).';
CREATE INDEX ix_risk_signals_subject ON risk.risk_signals (subject_type, subject_id, observed_at);
CREATE TRIGGER trg_risk_signals_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_signals
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_signals_no_truncate BEFORE TRUNCATE ON risk.risk_signals
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- risk.risk_assessments — append-only explainable scores (aml-risk-framework §3.2). Current score = latest
-- row per subject.
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
  rating_limit_ids   uuid[]      NOT NULL DEFAULT '{}',   -- risk.limits versions used for thresholds (audit trail)
  trigger_event_id   uuid,
  computed_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_risk_assessments PRIMARY KEY (id),
  CONSTRAINT ck_risk_assessments_subject CHECK (subject_type IN ('USER','ORGANISATION','CAMPAIGN','PAYOUT')),
  CONSTRAINT ck_risk_assessments_point CHECK (decision_point IN ('ONBOARDING','CAMPAIGN_SUBMISSION','DONATION','PAYOUT','PERIODIC','EVENT')),
  CONSTRAINT ck_risk_assessments_score CHECK (score BETWEEN 0 AND 1000),
  CONSTRAINT ck_risk_assessments_rating CHECK (rating IN ('LOW','STANDARD','HIGH'))
);
COMMENT ON TABLE risk.risk_assessments IS 'C2. Append-only risk scores with contributing rule codes.';
CREATE INDEX ix_risk_assessments_subject ON risk.risk_assessments (subject_type, subject_id, computed_at DESC);
CREATE TRIGGER trg_risk_assessments_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_assessments
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_risk_assessments_no_truncate BEFORE TRUNCATE ON risk.risk_assessments
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- risk.holds — every hold type (baseline §6 resolution 1; payout-eligibility-and-controls §5). A hold is a
-- record; placing it never edits history. Release fields are set ONCE and never cleared or changed; a
-- hold is never deleted. The only other permitted change is attaching a case (NULL -> id, once).
-- Ledger effects (campaign freeze, campaign-scope COMPLIANCE_HOLD) are posted by `ledger` in the same
-- transaction; their journal ids are recorded here.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.holds (
  id                           uuid        NOT NULL,
  hold_type                    text        NOT NULL,
  scope_type                   text        NOT NULL,
  scope_id                     uuid        NOT NULL,       -- polymorphic: campaign/user/org/beneficiary/destination/provider id
  scope_currency               char(3)     REFERENCES app.currencies(code),  -- PROVIDER_CURRENCY only
  reason_code                  text        NOT NULL,
  reason_text                  text,                       -- internal note; no C3 (C3 goes to evidence)
  confidential                 boolean     NOT NULL DEFAULT false,  -- tipping-off (LR-008/LR-072): owner sees neutral text only
  origin_type                  text        NOT NULL,
  origin_id                    uuid,                       -- alert / dispute / destination change / recovery case / incident
  case_id                      uuid,                       -- compliance case (compliance owns; no FK — risk is a leaf)
  alert_id                     uuid        REFERENCES risk.monitoring_alerts(id),
  placed_by_type               text        NOT NULL,
  placed_by                    uuid        REFERENCES app.users(id),
  placed_by_job                text,
  placed_at                    timestamptz NOT NULL DEFAULT now(),
  review_by                    timestamptz NOT NULL,      -- from limit hold.review_after.{type}; LR-084
  ledger_transaction_id        uuid,                       -- payable -> held journal (ledger owns; no FK, ledger is domain-agnostic)
  released_at                  timestamptz,
  released_by_type             text,
  released_by                  uuid        REFERENCES app.users(id),
  released_by_job              text,
  release_approved_by          uuid        REFERENCES app.users(id),  -- checker for maker-checker releases
  release_reason_code          text,
  release_reason_text          text,
  release_ledger_transaction_id uuid,                      -- inverse journal
  release_request_id           uuid,                       -- risk.hold_release_requests (FK added below)
  version                      integer     NOT NULL DEFAULT 1,
  CONSTRAINT pk_holds PRIMARY KEY (id),
  CONSTRAINT ck_holds_type CHECK (hold_type IN ('CAMPAIGN_FREEZE','COMPLIANCE_HOLD','PAYOUT_HOLD','DISPUTE_HOLD',
                                                'DESTINATION_HOLD','RECOVERY_HOLD','ACCOUNT_HOLD','PROVIDER_HOLD')),
  CONSTRAINT ck_holds_scope_type CHECK (scope_type IN ('CAMPAIGN','USER','ORGANISATION','BENEFICIARY',
                                                       'PAYOUT_DESTINATION','PROVIDER_CURRENCY')),
  -- type <-> scope compatibility (payout-eligibility-and-controls §5 table)
  CONSTRAINT ck_holds_type_scope CHECK (
       (hold_type = 'CAMPAIGN_FREEZE'  AND scope_type = 'CAMPAIGN')
    OR (hold_type = 'COMPLIANCE_HOLD'  AND scope_type IN ('CAMPAIGN','USER','ORGANISATION','BENEFICIARY','PAYOUT_DESTINATION'))
    OR (hold_type = 'PAYOUT_HOLD'      AND scope_type IN ('CAMPAIGN','USER'))
    OR (hold_type = 'DISPUTE_HOLD'     AND scope_type = 'CAMPAIGN')
    OR (hold_type = 'DESTINATION_HOLD' AND scope_type = 'PAYOUT_DESTINATION')
    OR (hold_type = 'RECOVERY_HOLD'    AND scope_type IN ('CAMPAIGN','USER'))
    OR (hold_type = 'ACCOUNT_HOLD'     AND scope_type = 'USER')
    OR (hold_type = 'PROVIDER_HOLD'    AND scope_type = 'PROVIDER_CURRENCY')),
  CONSTRAINT ck_holds_scope_currency CHECK ((scope_type = 'PROVIDER_CURRENCY') = (scope_currency IS NOT NULL)),
  CONSTRAINT ck_holds_reason_code CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_holds_origin CHECK (origin_type IN ('CASE','ALERT','DISPUTE','DESTINATION_CHANGE','RECOVERY_CASE',
                                                    'INCIDENT','RESTRICTION','STAFF_DECISION','SCREENING')),
  CONSTRAINT ck_holds_origin_alert CHECK ((origin_type = 'ALERT') = (alert_id IS NOT NULL)),
  -- a COMPLIANCE_HOLD always names its case; a staff-placed hold needs a case or an incident/recovery origin
  CONSTRAINT ck_holds_compliance_case CHECK (hold_type <> 'COMPLIANCE_HOLD' OR case_id IS NOT NULL),
  CONSTRAINT ck_holds_origin_case CHECK (origin_type <> 'CASE' OR case_id IS NOT NULL),
  CONSTRAINT ck_holds_origin_id CHECK (origin_type IN ('CASE','ALERT','STAFF_DECISION') OR origin_id IS NOT NULL),
  CONSTRAINT ck_holds_placed_by CHECK (
       (placed_by_type = 'STAFF'  AND placed_by IS NOT NULL AND placed_by_job IS NULL)
    OR (placed_by_type = 'SYSTEM' AND placed_by IS NULL     AND placed_by_job IS NOT NULL)),
  -- who may place which type (§5 "Placed by"): system-only and staff-only types
  CONSTRAINT ck_holds_placer_kind CHECK (
       (hold_type IN ('COMPLIANCE_HOLD','CAMPAIGN_FREEZE','RECOVERY_HOLD') AND placed_by_type = 'STAFF')
    OR (hold_type IN ('DISPUTE_HOLD','DESTINATION_HOLD') AND placed_by_type = 'SYSTEM')
    OR  hold_type IN ('PAYOUT_HOLD','ACCOUNT_HOLD','PROVIDER_HOLD')),
  CONSTRAINT ck_holds_review_by CHECK (review_by > placed_at),
  -- ledger effect only for campaign freeze and campaign-scope compliance holds
  CONSTRAINT ck_holds_ledger CHECK (ledger_transaction_id IS NULL OR hold_type = 'CAMPAIGN_FREEZE'
                                    OR (hold_type = 'COMPLIANCE_HOLD' AND scope_type = 'CAMPAIGN')),
  CONSTRAINT ck_holds_freeze_ledger CHECK (hold_type <> 'CAMPAIGN_FREEZE' OR ledger_transaction_id IS NOT NULL),
  CONSTRAINT ck_holds_release_ledger CHECK (release_ledger_transaction_id IS NULL OR
                                            (ledger_transaction_id IS NOT NULL AND released_at IS NOT NULL)),
  CONSTRAINT ck_holds_inverse_journal CHECK (released_at IS NULL OR ledger_transaction_id IS NULL OR release_ledger_transaction_id IS NOT NULL),
  -- release is all-or-nothing
  CONSTRAINT ck_holds_release_shape CHECK (
       (released_at IS NULL AND released_by_type IS NULL AND released_by IS NULL AND released_by_job IS NULL
        AND release_approved_by IS NULL AND release_reason_code IS NULL AND release_reason_text IS NULL
        AND release_request_id IS NULL)
    OR (released_at IS NOT NULL AND release_reason_code IS NOT NULL AND (
            (released_by_type = 'STAFF'  AND released_by IS NOT NULL AND released_by_job IS NULL)
         OR (released_by_type = 'SYSTEM' AND released_by IS NULL AND released_by_job IS NOT NULL)))),
  CONSTRAINT ck_holds_release_after_place CHECK (released_at IS NULL OR released_at >= placed_at),
  -- holds placed by system on an alert are released only by a human disposition (EC-12/EC-13);
  -- compliance/freeze/account/recovery holds are released by staff only
  CONSTRAINT ck_holds_release_human CHECK (released_at IS NULL OR released_by_type = 'STAFF'
                                           OR hold_type IN ('DISPUTE_HOLD','DESTINATION_HOLD','PROVIDER_HOLD')),
  -- maker-checker release for freezes (unfreeze) and compliance holds; whoever placed it cannot approve
  CONSTRAINT ck_holds_release_checker CHECK (released_at IS NULL OR hold_type NOT IN ('CAMPAIGN_FREEZE','COMPLIANCE_HOLD')
                                             OR (release_approved_by IS NOT NULL AND release_approved_by <> released_by
                                                 AND release_approved_by IS DISTINCT FROM placed_by)),
  CONSTRAINT ck_holds_release_checker_distinct CHECK (release_approved_by IS NULL OR release_approved_by IS DISTINCT FROM released_by),
  CONSTRAINT ck_holds_version CHECK (version >= 1)
);
COMMENT ON TABLE risk.holds IS 'C2 (reason_code/case link C3-sensitive when confidential). All hold types; release set once.';
CREATE INDEX ix_holds_active_scope ON risk.holds (scope_type, scope_id) WHERE released_at IS NULL;
CREATE INDEX ix_holds_case ON risk.holds (case_id) WHERE case_id IS NOT NULL;
CREATE INDEX ix_holds_review_due ON risk.holds (review_by) WHERE released_at IS NULL;
-- at most one active freeze per campaign (a second FROZEN transition is impossible while one is active)
CREATE UNIQUE INDEX uq_holds_active_freeze ON risk.holds (scope_id) WHERE hold_type = 'CAMPAIGN_FREEZE' AND released_at IS NULL;

CREATE TRIGGER trg_holds_no_delete BEFORE DELETE ON risk.holds
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_holds_no_truncate BEFORE TRUNCATE ON risk.holds
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION risk.holds_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  changed text[];
  release_cols constant text[] := ARRAY['released_at','released_by_type','released_by','released_by_job',
                                        'release_approved_by','release_reason_code','release_reason_text',
                                        'release_ledger_transaction_id','release_request_id'];
  req record;
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF changed = '{}' THEN
    RETURN NEW;
  END IF;
  IF OLD.released_at IS NOT NULL THEN
    RAISE EXCEPTION 'hold % is released; release fields are set once and never cleared or changed', OLD.id
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF NOT changed <@ (release_cols || ARRAY['case_id','version']) THEN
    RAISE EXCEPTION 'hold %: only release fields and a first case link may change (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.case_id IS NOT NULL AND NEW.case_id IS DISTINCT FROM OLD.case_id THEN
    RAISE EXCEPTION 'hold %: case_id is set once', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'hold %: version must increase by 1 on every change', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  -- maker-checker release (baseline §12 I-3): freezes and compliance holds, and any hold whose release
  -- the service routed through a request, need an APPROVED hold_release_request matching maker and checker
  IF NEW.released_at IS NOT NULL AND (OLD.hold_type IN ('CAMPAIGN_FREEZE','COMPLIANCE_HOLD') OR NEW.release_request_id IS NOT NULL) THEN
    SELECT * INTO req FROM risk.hold_release_requests r WHERE r.id = NEW.release_request_id;
    IF NOT FOUND OR req.hold_id <> OLD.id OR req.status <> 'APPROVED'
       OR req.requested_by IS DISTINCT FROM NEW.released_by OR req.decided_by IS DISTINCT FROM NEW.release_approved_by THEN
      RAISE EXCEPTION 'hold %: release requires an APPROVED hold_release_request (maker = releaser, checker = approver)', OLD.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_holds_guard BEFORE UPDATE ON risk.holds
  FOR EACH ROW EXECUTE FUNCTION risk.holds_guard();

-- -----------------------------------------------------------------------------------------------------
-- risk.hold_release_requests — maker-checker pending object for releasing a hold (baseline §12 I-3;
-- payout-eligibility-and-controls §5 "Released by"; operational-controls §3 campaign unfreeze, compliance
-- hold release). Always used for CAMPAIGN_FREEZE and COMPLIANCE_HOLD (enforced by risk.holds_guard); the
-- service also uses it where policy requires a checker for other types (e.g. high-severity PAYOUT_HOLD).
-- The checker may not be the maker nor whoever placed the hold. Expiry from limit
-- `approval.hold_release.validity`; expired requests are closed as EXPIRED, never auto-approved.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.hold_release_requests (
  id                  uuid        NOT NULL,
  hold_id             uuid        NOT NULL REFERENCES risk.holds(id),
  status              text        NOT NULL,
  requested_by        uuid        NOT NULL REFERENCES app.users(id),
  requested_at        timestamptz NOT NULL DEFAULT now(),
  reason_code         text        NOT NULL,
  justification       text        NOT NULL,
  case_id             uuid,
  payload_sha256      bytea       NOT NULL,                -- hold snapshot the checker saw
  expires_at          timestamptz NOT NULL,
  decided_by          uuid        REFERENCES app.users(id),
  decided_at          timestamptz,
  decision_note       text,
  checker_step_up_at  timestamptz,
  version             integer     NOT NULL DEFAULT 1,
  CONSTRAINT pk_hold_release_requests PRIMARY KEY (id),
  CONSTRAINT ck_hrr_status CHECK (status IN ('PENDING','APPROVED','REJECTED','EXPIRED','WITHDRAWN')),
  CONSTRAINT ck_hrr_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$' AND length(btrim(justification)) > 0),
  CONSTRAINT ck_hrr_hash CHECK (octet_length(payload_sha256) = 32),
  CONSTRAINT ck_hrr_expiry CHECK (expires_at > requested_at),
  CONSTRAINT ck_hrr_maker_checker CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT ck_hrr_decided CHECK ((status IN ('APPROVED','REJECTED')) = (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CONSTRAINT ck_hrr_in_time CHECK (status <> 'APPROVED' OR (decided_at <= expires_at AND checker_step_up_at IS NOT NULL))
);
COMMENT ON TABLE risk.hold_release_requests IS 'C2. Maker-checker requests to release holds.';
CREATE UNIQUE INDEX uq_hold_release_requests_pending ON risk.hold_release_requests (hold_id) WHERE status = 'PENDING';
ALTER TABLE risk.holds ADD CONSTRAINT fk_holds_release_request
  FOREIGN KEY (release_request_id) REFERENCES risk.hold_release_requests(id);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('hold_release_request', '',        'PENDING'),
  ('hold_release_request', 'PENDING', 'APPROVED'),
  ('hold_release_request', 'PENDING', 'REJECTED'),
  ('hold_release_request', 'PENDING', 'EXPIRED'),
  ('hold_release_request', 'PENDING', 'WITHDRAWN');
CREATE TRIGGER trg_hold_release_requests_guard_status BEFORE INSERT OR UPDATE OF status ON risk.hold_release_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('hold_release_request');
CREATE TRIGGER trg_hold_release_requests_no_delete BEFORE DELETE ON risk.hold_release_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_hold_release_requests_no_truncate BEFORE TRUNCATE ON risk.hold_release_requests
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION risk.hold_release_requests_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[]; placer uuid;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF EXISTS (SELECT 1 FROM risk.holds h WHERE h.id = NEW.hold_id AND h.released_at IS NOT NULL) THEN
      RAISE EXCEPTION 'hold % is already released', NEW.hold_id USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.status <> 'PENDING' OR NOT changed <@ ARRAY['status','decided_by','decided_at','decision_note','checker_step_up_at','version']
     OR NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'hold release request %: only the decision may change, once, with version + 1 (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  SELECT placed_by INTO placer FROM risk.holds WHERE id = OLD.hold_id;
  IF NEW.decided_by IS NOT NULL AND NEW.decided_by = placer THEN
    RAISE EXCEPTION 'hold release request %: whoever placed the hold cannot approve its release', OLD.id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_hold_release_requests_guard BEFORE INSERT OR UPDATE ON risk.hold_release_requests
  FOR EACH ROW EXECUTE FUNCTION risk.hold_release_requests_guard();

-- -----------------------------------------------------------------------------------------------------
-- risk.hold_events — append-only history. Every hold version has exactly one event (PLACED for v1,
-- RELEASED / CASE_ATTACHED for later versions), enforced at COMMIT by a deferred constraint trigger.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE risk.hold_events (
  id               uuid        NOT NULL,
  hold_id          uuid        NOT NULL REFERENCES risk.holds(id),
  hold_version     integer,                               -- NULL for non-versioning events (REVIEWED, NOTE)
  event_type       text        NOT NULL,
  actor_type       text        NOT NULL,
  actor_id         uuid        REFERENCES app.users(id),
  actor_job        text,
  reason_code      text,
  case_id          uuid,
  ledger_transaction_id uuid,
  evidence_record_ids uuid[]   NOT NULL DEFAULT '{}',
  audit_event_id   uuid,                                  -- audit event written in the same transaction
  occurred_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_hold_events PRIMARY KEY (id),
  CONSTRAINT ck_hold_events_type CHECK (event_type IN ('PLACED','CASE_ATTACHED','RELEASED','REVIEWED','ESCALATED','NOTE')),
  CONSTRAINT ck_hold_events_versioned CHECK ((event_type IN ('PLACED','CASE_ATTACHED','RELEASED')) = (hold_version IS NOT NULL)),
  CONSTRAINT ck_hold_events_placed_v1 CHECK (event_type <> 'PLACED' OR hold_version = 1),
  CONSTRAINT ck_hold_events_actor CHECK (
       (actor_type = 'STAFF'  AND actor_id IS NOT NULL)
    OR (actor_type = 'SYSTEM' AND actor_id IS NULL AND actor_job IS NOT NULL))
);
COMMENT ON TABLE risk.hold_events IS 'C2. Append-only hold history.';
CREATE UNIQUE INDEX uq_hold_events_version ON risk.hold_events (hold_id, hold_version) WHERE hold_version IS NOT NULL;
CREATE TRIGGER trg_hold_events_no_mutation BEFORE UPDATE OR DELETE ON risk.hold_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_hold_events_no_truncate BEFORE TRUNCATE ON risk.hold_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION risk.holds_require_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cur risk.holds%ROWTYPE;
BEGIN
  SELECT * INTO cur FROM risk.holds WHERE id = NEW.id;      -- state at COMMIT time
  IF NOT EXISTS (SELECT 1 FROM risk.hold_events e WHERE e.hold_id = cur.id AND e.hold_version = cur.version) THEN
    RAISE EXCEPTION 'hold % version % has no hold_events row', cur.id, cur.version USING ERRCODE = 'check_violation';
  END IF;
  IF cur.released_at IS NOT NULL AND NOT EXISTS (SELECT 1 FROM risk.hold_events e
                                                  WHERE e.hold_id = cur.id AND e.event_type = 'RELEASED') THEN
    RAISE EXCEPTION 'hold % is released without a RELEASED event', cur.id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_holds_require_event AFTER INSERT OR UPDATE ON risk.holds
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION risk.holds_require_event();

-- -----------------------------------------------------------------------------------------------------
-- Reporting views for fundzim_readonly (no confidential reasons, no case ids, no subjects). Views named
-- ro_* are the only objects 0018 grants to the reporting role.
-- -----------------------------------------------------------------------------------------------------
CREATE VIEW risk.ro_active_hold_counts AS
  SELECT hold_type, scope_type, count(*) AS active_holds,
         min(placed_at) AS oldest_placed_at, count(*) FILTER (WHERE review_by < now()) AS past_review
    FROM risk.holds WHERE released_at IS NULL GROUP BY hold_type, scope_type;

CREATE VIEW risk.ro_limits_review_status AS
  SELECT limit_key, limit_type, scope_type, scope_value, currency, version, status,
         effective_from, effective_to, review_by, (review_by < current_date) AS past_review_by,
         legal_review_ref
    FROM risk.limits WHERE status IN ('PROPOSED','APPROVED');
