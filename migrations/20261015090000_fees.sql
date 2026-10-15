-- Stage 12 (ADR-044; MONEY.md §5–§6; Stage 2 draft design/sql/0007_fees_psp.sql): fee policies and immutable
-- versions with maker-checker, provider fee evidence, exact fee calculations with their components, fee quotes,
-- assessments, adjustments, accounting references to ledger journals, provider-reported fees, reconciliation and the
-- fee event history.
--
-- Stage 12 is NON-PRODUCTION engineering. Every fee row is a TEST row (fee_environment = 'TEST',
-- ck_*_test_only_stage12); provider fee schedules can never be VERIFIED (no provider is selected, ADR-040); the
-- currency-conversion and "other" fee categories cannot be enabled. Fee journals are posted by the ledger module
-- (fundzim_ledger role) with source_system 'fees' (added here; SIMULATED evidence in a TEST ledger). No new account
-- class or posting rule is added. No commercial, tax or provider rate is seeded.
-- Owner module: fees (schema app) except the ledger CHECK changes (ledger), donation_intents.fee_* (payments) and
-- payout_requests.fee_* (payouts). Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for
-- development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

-- -----------------------------------------------------------------------------------------------------
-- 0. Ledger: fee journals get their own source system (ADR-044 §10).
-- -----------------------------------------------------------------------------------------------------
ALTER TABLE ledger.ledger_transactions DROP CONSTRAINT ck_ledger_transactions_source_system;
ALTER TABLE ledger.ledger_transactions ADD CONSTRAINT ck_ledger_transactions_source_system
  CHECK (source_system IN ('payments', 'payouts', 'fees', 'reconciliation', 'staff', 'synthetic_fixture'));
ALTER TABLE ledger.ledger_transactions ADD CONSTRAINT ck_ledger_transactions_fees_simulated_in_test
  CHECK (source_system <> 'fees' OR environment <> 'TEST' OR evidence_class = 'SIMULATED');
ALTER TABLE ledger.ledger_source_references DROP CONSTRAINT ck_ledger_source_references_source_system;
ALTER TABLE ledger.ledger_source_references ADD CONSTRAINT ck_ledger_source_references_source_system
  CHECK (source_system IN ('payments', 'payouts', 'fees', 'reconciliation', 'staff', 'synthetic_fixture'));

-- -----------------------------------------------------------------------------------------------------
-- 1. fee_policies — identity and immutable scope. scope_key is unique: two policies never share a scope, so
--    selection by specificity can never be ambiguous (ADR-044 §4). Specificity: campaign 16, campaign category 8,
--    provider 4, payment method 2.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_policies (
  id                 uuid        NOT NULL,
  code               text        NOT NULL,
  name               text        NOT NULL,
  description        text        NOT NULL DEFAULT '',
  category           text        NOT NULL,
  transaction_type   text        NOT NULL,
  currency           char(3)     NOT NULL,
  payment_method     text,
  provider_id        uuid,
  campaign_category  text,
  campaign_id        uuid,
  scope_key          text        GENERATED ALWAYS AS (category || '|' || transaction_type || '|' || currency || '|' ||
                       coalesce(payment_method, '*') || '|' || coalesce(provider_id::text, '*') || '|' ||
                       coalesce(campaign_category, '*') || '|' || coalesce(campaign_id::text, '*')) STORED,
  specificity        smallint    GENERATED ALWAYS AS ((CASE WHEN campaign_id IS NULL THEN 0 ELSE 16 END) +
                       (CASE WHEN campaign_category IS NULL THEN 0 ELSE 8 END) + (CASE WHEN provider_id IS NULL THEN 0 ELSE 4 END) +
                       (CASE WHEN payment_method IS NULL THEN 0 ELSE 2 END)) STORED,
  fee_environment    text        NOT NULL DEFAULT 'TEST',
  created_by         uuid        NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_policies PRIMARY KEY (id),
  CONSTRAINT uq_fee_policies_code UNIQUE (code),
  CONSTRAINT uq_fee_policies_scope UNIQUE (scope_key),
  CONSTRAINT uq_fee_policies_id_category UNIQUE (id, category),
  CONSTRAINT fk_fee_policies_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_policies_provider FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_policies_campaign_category FOREIGN KEY (campaign_category) REFERENCES app.campaign_categories (code) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_policies_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_policies_created_by FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_fee_policies_code CHECK (code ~ '^[a-z][a-z0-9_.]{2,63}$'),
  CONSTRAINT ck_fee_policies_name CHECK (char_length(btrim(name)) BETWEEN 3 AND 120),
  CONSTRAINT ck_fee_policies_description CHECK (char_length(description) <= 2000),
  CONSTRAINT ck_fee_policies_category CHECK (category IN ('PLATFORM_FEE','PAYMENT_PROCESSING_FEE','PAYOUT_PROCESSING_FEE',
    'REFUND_FEE','CHARGEBACK_FEE','CURRENCY_CONVERSION_FEE','OTHER_APPROVED_FEE')),
  -- FundZim never converts currencies; no posting rule exists for "other" fees (ADR-044 §5)
  CONSTRAINT ck_fee_policies_category_enabled_stage12 CHECK (category NOT IN ('CURRENCY_CONVERSION_FEE','OTHER_APPROVED_FEE')),
  CONSTRAINT ck_fee_policies_transaction_type CHECK (
    (category IN ('PLATFORM_FEE','PAYMENT_PROCESSING_FEE') AND transaction_type = 'DONATION')
    OR (category = 'PAYOUT_PROCESSING_FEE' AND transaction_type = 'PAYOUT')
    OR (category = 'REFUND_FEE' AND transaction_type = 'REFUND')
    OR (category = 'CHARGEBACK_FEE' AND transaction_type = 'CHARGEBACK')),
  CONSTRAINT ck_fee_policies_method CHECK (payment_method IS NULL OR payment_method ~ '^[A-Z][A-Z0-9_]{1,31}$'),
  CONSTRAINT ck_fee_policies_test_only_stage12 CHECK (fee_environment = 'TEST')
);
CREATE INDEX ix_fee_policies_lookup ON app.fee_policies (category, transaction_type, currency);
CREATE TRIGGER trg_fee_policies_only_presentation BEFORE UPDATE ON app.fee_policies
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('name', 'description');
CREATE TRIGGER trg_fee_policies_updated_at BEFORE UPDATE ON app.fee_policies FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_policies_no_delete BEFORE DELETE ON app.fee_policies FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_policies_no_truncate BEFORE TRUNCATE ON app.fee_policies FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 2. fee_policy_versions — the terms. Editable only in DRAFT; immutable from submission on. Machine
--    'fee_policy_version'. Maker (created_by / submitted_by, fee.config.request) ≠ checker (approved_by,
--    fee.config.approve), including linked accounts; the re-activator of a suspended version ≠ the suspender.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_policy_versions (
  id                    uuid        NOT NULL,
  policy_id             uuid        NOT NULL,
  category              text        NOT NULL,
  version_no            integer     NOT NULL,
  status                text        NOT NULL,
  model                 text        NOT NULL,
  rate_bps              integer,
  fixed_minor           bigint,
  min_fee_minor         bigint,
  max_fee_minor         bigint,
  tiers                 jsonb,       -- [{"up_to_minor": "…" | null, "rate_bps": n, "fixed_minor": "…"}], ascending; last up_to null
  rounding_mode         text        NOT NULL,
  responsibility        text        NOT NULL,
  donor_share_bps       integer,     -- SHARED: the donor's share; the organiser bears the rest
  refund_treatment      text,        -- PLATFORM_FEE only
  effective_from        timestamptz NOT NULL,
  effective_to          timestamptz,
  algorithm_version     text        NOT NULL,
  terms_hash            bytea,       -- SHA-256 of the canonical terms, set at submission; the approval binds to it
  fee_environment       text        NOT NULL DEFAULT 'TEST',
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
  activated_step_up_at  timestamptz,
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
  CONSTRAINT pk_fee_policy_versions PRIMARY KEY (id),
  CONSTRAINT fk_fee_policy_versions_policy FOREIGN KEY (policy_id, category) REFERENCES app.fee_policies (id, category) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_policy_versions_created_by FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_policy_versions_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_fee_policy_versions_no UNIQUE (policy_id, version_no),
  CONSTRAINT ck_fee_policy_versions_version_no CHECK (version_no >= 1),
  CONSTRAINT ck_fee_policy_versions_status CHECK (status IN ('DRAFT','PENDING_APPROVAL','APPROVED','REJECTED','ACTIVE','SUSPENDED','RETIRED')),
  CONSTRAINT ck_fee_policy_versions_model CHECK (model IN ('ZERO_FEE','PERCENTAGE','FIXED','PERCENTAGE_PLUS_FIXED','TIERED','CAPPED',
    'MINIMUM_FEE','PROVIDER_PASS_THROUGH')),
  CONSTRAINT ck_fee_policy_versions_rate CHECK (rate_bps IS NULL OR rate_bps BETWEEN 0 AND 10000),
  CONSTRAINT ck_fee_policy_versions_fixed CHECK (fixed_minor IS NULL OR fixed_minor >= 0),
  CONSTRAINT ck_fee_policy_versions_min CHECK (min_fee_minor IS NULL OR min_fee_minor >= 0),
  CONSTRAINT ck_fee_policy_versions_max CHECK (max_fee_minor IS NULL OR max_fee_minor >= coalesce(min_fee_minor, 0)),
  CONSTRAINT ck_fee_policy_versions_tiers CHECK (tiers IS NULL OR (jsonb_typeof(tiers) = 'array' AND jsonb_array_length(tiers) BETWEEN 1 AND 20)),
  -- the parameters each model needs, and nothing else (ADR-044 §5)
  CONSTRAINT ck_fee_policy_versions_shape CHECK (CASE model
    WHEN 'ZERO_FEE'              THEN rate_bps IS NULL AND fixed_minor IS NULL AND min_fee_minor IS NULL AND max_fee_minor IS NULL AND tiers IS NULL
    WHEN 'PERCENTAGE'            THEN rate_bps IS NOT NULL AND fixed_minor IS NULL AND tiers IS NULL
    WHEN 'FIXED'                 THEN rate_bps IS NULL AND fixed_minor IS NOT NULL AND tiers IS NULL
    WHEN 'PERCENTAGE_PLUS_FIXED' THEN rate_bps IS NOT NULL AND fixed_minor IS NOT NULL AND tiers IS NULL
    WHEN 'TIERED'                THEN rate_bps IS NULL AND fixed_minor IS NULL AND tiers IS NOT NULL
    WHEN 'CAPPED'                THEN rate_bps IS NOT NULL AND fixed_minor IS NULL AND max_fee_minor IS NOT NULL AND tiers IS NULL
    WHEN 'MINIMUM_FEE'           THEN rate_bps IS NOT NULL AND fixed_minor IS NULL AND min_fee_minor IS NOT NULL AND tiers IS NULL
    ELSE rate_bps IS NULL AND fixed_minor IS NULL AND min_fee_minor IS NULL AND max_fee_minor IS NULL AND tiers IS NULL END),
  CONSTRAINT ck_fee_policy_versions_rounding CHECK (rounding_mode IN ('HALF_UP','HALF_EVEN','FLOOR','CEILING')),
  CONSTRAINT ck_fee_policy_versions_responsibility CHECK (responsibility IN ('PLATFORM_ABSORBED','DONOR_COVERED','ORGANISER_COVERED','SHARED')),
  CONSTRAINT ck_fee_policy_versions_share CHECK ((responsibility = 'SHARED') = (donor_share_bps IS NOT NULL)
    AND (donor_share_bps IS NULL OR donor_share_bps BETWEEN 1 AND 9999)),
  CONSTRAINT ck_fee_policy_versions_refund_treatment CHECK (refund_treatment IS NULL OR refund_treatment IN ('PROPORTIONAL','PROPORTIONAL_EXCEPT_FIXED','NON_REFUNDABLE')),
  -- PLATFORM_FEE: FundZim's own fee (never pass-through, never "platform absorbed" — use ZERO_FEE), with a refund treatment;
  -- provider-cost categories: pass-through only; payout, refund and chargeback costs are never borne by donors
  CONSTRAINT ck_fee_policy_versions_category CHECK (CASE category
    WHEN 'PLATFORM_FEE' THEN model <> 'PROVIDER_PASS_THROUGH' AND responsibility <> 'PLATFORM_ABSORBED' AND refund_treatment IS NOT NULL
    WHEN 'PAYMENT_PROCESSING_FEE' THEN model = 'PROVIDER_PASS_THROUGH' AND refund_treatment IS NULL
    ELSE model = 'PROVIDER_PASS_THROUGH' AND refund_treatment IS NULL AND responsibility IN ('ORGANISER_COVERED','PLATFORM_ABSORBED') END),
  CONSTRAINT ck_fee_policy_versions_window CHECK (effective_to IS NULL OR effective_to > effective_from),
  CONSTRAINT ck_fee_policy_versions_algorithm CHECK (algorithm_version ~ '^fees-calc/[0-9]+$'),
  CONSTRAINT ck_fee_policy_versions_submitted CHECK (status = 'DRAFT' OR (submitted_by IS NOT NULL AND submitted_at IS NOT NULL AND
    terms_hash IS NOT NULL AND octet_length(terms_hash) = 32) OR (status = 'RETIRED' AND submitted_by IS NULL)),
  CONSTRAINT ck_fee_policy_versions_approved CHECK (status NOT IN ('APPROVED','ACTIVE','SUSPENDED') OR (approved_by IS NOT NULL AND
    approved_at IS NOT NULL AND approved_step_up_at IS NOT NULL AND approved_step_up_at <= approved_at AND approval_reason IS NOT NULL)),
  CONSTRAINT ck_fee_policy_versions_maker_checker CHECK (approved_by IS NULL OR (approved_by <> created_by AND approved_by <> submitted_by)),
  CONSTRAINT ck_fee_policy_versions_rejected CHECK ((status = 'REJECTED') = (rejected_by IS NOT NULL) AND (rejected_by IS NULL OR
    (rejected_by <> created_by AND rejected_by <> submitted_by AND rejected_at IS NOT NULL AND rejection_reason IS NOT NULL))),
  CONSTRAINT ck_fee_policy_versions_activated CHECK (status NOT IN ('ACTIVE','SUSPENDED') OR (activated_by IS NOT NULL AND
    activated_at IS NOT NULL AND activated_step_up_at IS NOT NULL)),
  CONSTRAINT ck_fee_policy_versions_suspended CHECK (status <> 'SUSPENDED' OR (suspended_by IS NOT NULL AND suspension_reason IS NOT NULL)),
  CONSTRAINT ck_fee_policy_versions_reactivated CHECK (reactivated_by IS NULL OR reactivated_by <> suspended_by),
  CONSTRAINT ck_fee_policy_versions_retired CHECK ((status = 'RETIRED') = (retired_by IS NOT NULL) AND (retired_by IS NULL OR retirement_reason IS NOT NULL)),
  CONSTRAINT ck_fee_policy_versions_test_only_stage12 CHECK (fee_environment = 'TEST'),
  CONSTRAINT ck_fee_policy_versions_version CHECK (version >= 1)
);
-- at most one ACTIVE version of a policy per effective start: selection inside a policy is "greatest effective_from"
CREATE UNIQUE INDEX uq_fee_policy_versions_active_start ON app.fee_policy_versions (policy_id, effective_from) WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX uq_fee_policy_versions_one_draft ON app.fee_policy_versions (policy_id) WHERE status IN ('DRAFT','PENDING_APPROVAL');
CREATE INDEX ix_fee_policy_versions_active ON app.fee_policy_versions (policy_id, effective_from DESC) WHERE status = 'ACTIVE';
CREATE INDEX ix_fee_policy_versions_status ON app.fee_policy_versions (status, updated_at DESC);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('fee_policy_version', '',                 'DRAFT'),
  ('fee_policy_version', 'DRAFT',            'PENDING_APPROVAL'),
  ('fee_policy_version', 'DRAFT',            'RETIRED'),
  ('fee_policy_version', 'PENDING_APPROVAL', 'APPROVED'),
  ('fee_policy_version', 'PENDING_APPROVAL', 'REJECTED'),
  ('fee_policy_version', 'APPROVED',         'ACTIVE'),
  ('fee_policy_version', 'APPROVED',         'RETIRED'),
  ('fee_policy_version', 'ACTIVE',           'SUSPENDED'),
  ('fee_policy_version', 'ACTIVE',           'RETIRED'),
  ('fee_policy_version', 'SUSPENDED',        'ACTIVE'),
  ('fee_policy_version', 'SUSPENDED',        'RETIRED');
-- Deliberately absent: DRAFT -> APPROVED / ACTIVE (no activation without approval), anything out of REJECTED or RETIRED.

CREATE FUNCTION app.fee_policy_versions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  ids uuid[];
BEGIN
  IF TG_OP = 'UPDATE' AND OLD.status <> 'DRAFT' AND (
       NEW.policy_id, NEW.category, NEW.version_no, NEW.model, NEW.rate_bps, NEW.fixed_minor, NEW.min_fee_minor, NEW.max_fee_minor,
       NEW.tiers, NEW.rounding_mode, NEW.responsibility, NEW.donor_share_bps, NEW.refund_treatment, NEW.effective_from,
       NEW.effective_to, NEW.algorithm_version, NEW.terms_hash, NEW.created_by, NEW.submitted_by, NEW.submitted_at)
     IS DISTINCT FROM (
       OLD.policy_id, OLD.category, OLD.version_no, OLD.model, OLD.rate_bps, OLD.fixed_minor, OLD.min_fee_minor, OLD.max_fee_minor,
       OLD.tiers, OLD.rounding_mode, OLD.responsibility, OLD.donor_share_bps, OLD.refund_treatment, OLD.effective_from,
       OLD.effective_to, OLD.algorithm_version, OLD.terms_hash, OLD.created_by, OLD.submitted_by, OLD.submitted_at) THEN
    RAISE EXCEPTION 'fee policy version % is % and its terms are immutable; create a new version', OLD.id, OLD.status
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.approved_by IS NOT NULL AND (NEW.approved_by, NEW.approved_at, NEW.approved_step_up_at, NEW.approval_reason)
       IS DISTINCT FROM (OLD.approved_by, OLD.approved_at, OLD.approved_step_up_at, OLD.approval_reason) THEN
    RAISE EXCEPTION 'the approval of fee policy version % is immutable', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  -- linked accounts (staff ↔ personal) cannot bypass maker-checker
  IF NEW.approved_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.approved_by IS NULL) THEN
    ids := app.actor_identities(NEW.approved_by);
    IF NEW.created_by = ANY (ids) OR NEW.submitted_by = ANY (ids) THEN
      RAISE EXCEPTION 'maker-checker: % is (or is linked to) the maker of fee policy version %', NEW.approved_by, NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NEW.rejected_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.rejected_by IS NULL) THEN
    ids := app.actor_identities(NEW.rejected_by);
    IF NEW.created_by = ANY (ids) OR NEW.submitted_by = ANY (ids) THEN
      RAISE EXCEPTION 'maker-checker: % is (or is linked to) the maker of fee policy version %', NEW.rejected_by, NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NEW.reactivated_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.reactivated_by IS DISTINCT FROM NEW.reactivated_by) THEN
    ids := app.actor_identities(NEW.reactivated_by);
    IF NEW.suspended_by = ANY (ids) THEN
      RAISE EXCEPTION 'maker-checker: % suspended fee policy version % and cannot reactivate it', NEW.reactivated_by, NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF TG_OP = 'INSERT' AND NEW.status <> 'DRAFT' THEN
    RAISE EXCEPTION 'fee policy versions are created as DRAFT' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_fee_policy_versions_guard BEFORE INSERT OR UPDATE ON app.fee_policy_versions
  FOR EACH ROW EXECUTE FUNCTION app.fee_policy_versions_guard();
CREATE TRIGGER trg_fee_policy_versions_guard_status BEFORE INSERT OR UPDATE OF status ON app.fee_policy_versions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('fee_policy_version');
CREATE TRIGGER trg_fee_policy_versions_updated_at BEFORE UPDATE ON app.fee_policy_versions FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_policy_versions_keep_created_at BEFORE UPDATE ON app.fee_policy_versions FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_fee_policy_versions_no_delete BEFORE DELETE ON app.fee_policy_versions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_policy_versions_no_truncate BEFORE TRUNCATE ON app.fee_policy_versions FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 3. fee_provider_schedules — provider fee evidence (ADR-044 §8). SYNTHETIC: sandbox providers only (composite FK
--    on is_sandbox), for TEST. UNVERIFIED: a documented claim not yet verified. VERIFIED is refused in Stage 12
--    (no provider is selected, ADR-040). Maker ≠ checker for activation. Machine 'fee_provider_schedule'.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_provider_schedules (
  id                   uuid        NOT NULL,
  provider_id          uuid        NOT NULL,
  provider_is_sandbox  boolean     NOT NULL,
  contract_reference   text        NOT NULL,
  schedule_version     text        NOT NULL,
  currency             char(3)     NOT NULL,
  payment_method       text,       -- collection method or payout rail; NULL: every method of the provider
  fee_type             text        NOT NULL,
  model                text        NOT NULL,
  rate_bps             integer,
  fixed_minor          bigint,
  min_fee_minor        bigint,
  max_fee_minor        bigint,
  tiers                jsonb,
  rounding_mode        text,       -- NULL: the provider's rounding is UNKNOWN (estimates use HALF_UP and say so)
  effective_from       timestamptz NOT NULL,
  effective_to         timestamptz,
  evidence_source      text        NOT NULL,
  evidence_reference   text,
  confirmation_status  text        NOT NULL,
  status               text        NOT NULL,
  fee_environment      text        NOT NULL DEFAULT 'TEST',
  created_by           uuid        NOT NULL,
  approved_by          uuid,
  approved_at          timestamptz,
  retired_by           uuid,
  retired_at           timestamptz,
  version              integer     NOT NULL DEFAULT 1,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_provider_schedules PRIMARY KEY (id),
  CONSTRAINT fk_fee_provider_schedules_provider FOREIGN KEY (provider_id, provider_is_sandbox) REFERENCES app.payment_providers (id, is_sandbox) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_provider_schedules_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_provider_schedules_created_by FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_provider_schedules_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_fee_provider_schedules_contract CHECK (char_length(btrim(contract_reference)) BETWEEN 3 AND 200),
  CONSTRAINT ck_fee_provider_schedules_schedule_version CHECK (char_length(btrim(schedule_version)) BETWEEN 1 AND 64),
  CONSTRAINT ck_fee_provider_schedules_method CHECK (payment_method IS NULL OR payment_method ~ '^[A-Z][A-Z0-9_]{1,31}$'),
  CONSTRAINT ck_fee_provider_schedules_fee_type CHECK (fee_type IN ('TRANSACTION','SETTLEMENT','REFUND','CHARGEBACK','PAYOUT')),
  CONSTRAINT ck_fee_provider_schedules_model CHECK (model IN ('PERCENTAGE','FIXED','PERCENTAGE_PLUS_FIXED','TIERED','CAPPED','MINIMUM_FEE')),
  CONSTRAINT ck_fee_provider_schedules_rate CHECK (rate_bps IS NULL OR rate_bps BETWEEN 0 AND 10000),
  CONSTRAINT ck_fee_provider_schedules_fixed CHECK (fixed_minor IS NULL OR fixed_minor >= 0),
  CONSTRAINT ck_fee_provider_schedules_min CHECK (min_fee_minor IS NULL OR min_fee_minor >= 0),
  CONSTRAINT ck_fee_provider_schedules_max CHECK (max_fee_minor IS NULL OR max_fee_minor >= coalesce(min_fee_minor, 0)),
  CONSTRAINT ck_fee_provider_schedules_tiers CHECK (tiers IS NULL OR (jsonb_typeof(tiers) = 'array' AND jsonb_array_length(tiers) BETWEEN 1 AND 20)),
  CONSTRAINT ck_fee_provider_schedules_shape CHECK (CASE model
    WHEN 'PERCENTAGE'            THEN rate_bps IS NOT NULL AND fixed_minor IS NULL AND tiers IS NULL
    WHEN 'FIXED'                 THEN rate_bps IS NULL AND fixed_minor IS NOT NULL AND tiers IS NULL
    WHEN 'PERCENTAGE_PLUS_FIXED' THEN rate_bps IS NOT NULL AND fixed_minor IS NOT NULL AND tiers IS NULL
    WHEN 'TIERED'                THEN rate_bps IS NULL AND fixed_minor IS NULL AND tiers IS NOT NULL
    WHEN 'CAPPED'                THEN rate_bps IS NOT NULL AND fixed_minor IS NULL AND max_fee_minor IS NOT NULL AND tiers IS NULL
    ELSE rate_bps IS NOT NULL AND fixed_minor IS NULL AND min_fee_minor IS NOT NULL AND tiers IS NULL END),
  CONSTRAINT ck_fee_provider_schedules_rounding CHECK (rounding_mode IS NULL OR rounding_mode IN ('HALF_UP','HALF_EVEN','FLOOR','CEILING')),
  CONSTRAINT ck_fee_provider_schedules_window CHECK (effective_to IS NULL OR effective_to > effective_from),
  CONSTRAINT ck_fee_provider_schedules_evidence CHECK (char_length(btrim(evidence_source)) BETWEEN 3 AND 200 AND
    (evidence_reference IS NULL OR char_length(evidence_reference) <= 500)),
  CONSTRAINT ck_fee_provider_schedules_confirmation CHECK (confirmation_status IN ('SYNTHETIC','UNVERIFIED')),
  CONSTRAINT ck_fee_provider_schedules_no_verified_stage12 CHECK (confirmation_status <> 'VERIFIED'),
  CONSTRAINT ck_fee_provider_schedules_synthetic_sandbox CHECK (confirmation_status <> 'SYNTHETIC' OR provider_is_sandbox),
  CONSTRAINT ck_fee_provider_schedules_status CHECK (status IN ('PENDING_APPROVAL','ACTIVE','RETIRED')),
  CONSTRAINT ck_fee_provider_schedules_approved CHECK (status <> 'ACTIVE' OR (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  CONSTRAINT ck_fee_provider_schedules_maker_checker CHECK (approved_by IS NULL OR approved_by <> created_by),
  CONSTRAINT ck_fee_provider_schedules_retired CHECK ((status = 'RETIRED') = (retired_by IS NOT NULL)),
  CONSTRAINT ck_fee_provider_schedules_test_only_stage12 CHECK (fee_environment = 'TEST'),
  CONSTRAINT ck_fee_provider_schedules_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX uq_fee_provider_schedules_active ON app.fee_provider_schedules
  (provider_id, fee_type, currency, coalesce(payment_method, '*'), effective_from) WHERE status = 'ACTIVE';
CREATE INDEX ix_fee_provider_schedules_lookup ON app.fee_provider_schedules (provider_id, fee_type, currency) WHERE status = 'ACTIVE';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('fee_provider_schedule', '',                 'PENDING_APPROVAL'),
  ('fee_provider_schedule', 'PENDING_APPROVAL', 'ACTIVE'),
  ('fee_provider_schedule', 'PENDING_APPROVAL', 'RETIRED'),
  ('fee_provider_schedule', 'ACTIVE',           'RETIRED');
CREATE FUNCTION app.fee_provider_schedules_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.approved_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.approved_by IS NULL)
     AND NEW.created_by = ANY (app.actor_identities(NEW.approved_by)) THEN
    RAISE EXCEPTION 'maker-checker: % is (or is linked to) the maker of provider fee schedule %', NEW.approved_by, NEW.id
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_fee_provider_schedules_guard BEFORE INSERT OR UPDATE ON app.fee_provider_schedules
  FOR EACH ROW EXECUTE FUNCTION app.fee_provider_schedules_guard();
CREATE TRIGGER trg_fee_provider_schedules_terms BEFORE UPDATE ON app.fee_provider_schedules
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'approved_by', 'approved_at', 'retired_by', 'retired_at', 'version');
CREATE TRIGGER trg_fee_provider_schedules_guard_status BEFORE INSERT OR UPDATE OF status ON app.fee_provider_schedules
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('fee_provider_schedule');
CREATE TRIGGER trg_fee_provider_schedules_updated_at BEFORE UPDATE ON app.fee_provider_schedules FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_provider_schedules_no_delete BEFORE DELETE ON app.fee_provider_schedules FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_provider_schedules_no_truncate BEFORE TRUNCATE ON app.fee_provider_schedules FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 4. fee_calculations and fee_components — immutable, reproducible results (ADR-044 §6). inputs is the canonical
--    input document; recomputing it with algorithm_version must give result (checked by reconciliation).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_calculations (
  id                 uuid        NOT NULL,
  algorithm_version  text        NOT NULL,
  transaction_type   text        NOT NULL,
  currency           char(3)     NOT NULL,
  basis_minor        bigint      NOT NULL,
  inputs             jsonb       NOT NULL,
  inputs_hash        bytea       NOT NULL,
  result             jsonb       NOT NULL,
  result_hash        bytea       NOT NULL,
  calculated_at      timestamptz NOT NULL,
  fee_environment    text        NOT NULL DEFAULT 'TEST',
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_calculations PRIMARY KEY (id),
  CONSTRAINT uq_fee_calculations_id_currency UNIQUE (id, currency),
  CONSTRAINT fk_fee_calculations_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT ck_fee_calculations_algorithm CHECK (algorithm_version ~ '^fees-calc/[0-9]+$'),
  CONSTRAINT ck_fee_calculations_type CHECK (transaction_type IN ('DONATION','PAYOUT','REFUND','CHARGEBACK')),
  CONSTRAINT ck_fee_calculations_basis CHECK (basis_minor > 0),
  CONSTRAINT ck_fee_calculations_hashes CHECK (octet_length(inputs_hash) = 32 AND octet_length(result_hash) = 32),
  CONSTRAINT ck_fee_calculations_documents CHECK (jsonb_typeof(inputs) = 'object' AND jsonb_typeof(result) = 'object'),
  CONSTRAINT ck_fee_calculations_test_only_stage12 CHECK (fee_environment = 'TEST')
);
CREATE INDEX ix_fee_calculations_created ON app.fee_calculations (created_at DESC);
CREATE TRIGGER trg_fee_calculations_no_mutation BEFORE UPDATE OR DELETE ON app.fee_calculations FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_calculations_no_truncate BEFORE TRUNCATE ON app.fee_calculations FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.fee_components (
  id                    uuid        NOT NULL,
  calculation_id        uuid        NOT NULL,
  seq                   smallint    NOT NULL,
  category              text        NOT NULL,
  policy_version_id     uuid,
  provider_schedule_id  uuid,
  basis                 text        NOT NULL,
  amount_minor          bigint,
  responsibility        text,
  donor_part_minor      bigint,
  organiser_part_minor  bigint,
  platform_part_minor   bigint,
  rounding_mode         text,
  exact_value           text,        -- the unrounded rational "numerator/denominator" (audit of the rounding step)
  created_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_components PRIMARY KEY (id),
  CONSTRAINT fk_fee_components_calculation FOREIGN KEY (calculation_id) REFERENCES app.fee_calculations (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_components_policy_version FOREIGN KEY (policy_version_id) REFERENCES app.fee_policy_versions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_components_provider_schedule FOREIGN KEY (provider_schedule_id) REFERENCES app.fee_provider_schedules (id) ON DELETE RESTRICT,
  CONSTRAINT uq_fee_components_seq UNIQUE (calculation_id, seq),
  CONSTRAINT ck_fee_components_category CHECK (category IN ('PLATFORM_FEE','PAYMENT_PROCESSING_FEE','PAYOUT_PROCESSING_FEE','REFUND_FEE','CHARGEBACK_FEE')),
  CONSTRAINT ck_fee_components_basis CHECK (basis IN ('QUOTED','ESTIMATED','UNKNOWN','NOT_CONFIGURED')),
  -- UNKNOWN has no amount (never 0); NOT_CONFIGURED (no active policy) charges nothing: 0
  CONSTRAINT ck_fee_components_amount CHECK ((amount_minor IS NULL) = (basis = 'UNKNOWN') AND (amount_minor IS NULL OR amount_minor >= 0)
    AND (basis <> 'NOT_CONFIGURED' OR amount_minor = 0)),
  CONSTRAINT ck_fee_components_parts CHECK (amount_minor IS NULL OR (donor_part_minor >= 0 AND organiser_part_minor >= 0 AND platform_part_minor >= 0
    AND donor_part_minor + organiser_part_minor + platform_part_minor = amount_minor)),
  CONSTRAINT ck_fee_components_source CHECK (basis = 'NOT_CONFIGURED' OR policy_version_id IS NOT NULL OR provider_schedule_id IS NOT NULL OR basis = 'UNKNOWN')
);
CREATE TRIGGER trg_fee_components_no_mutation BEFORE UPDATE OR DELETE ON app.fee_components FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_components_no_truncate BEFORE TRUNCATE ON app.fee_components FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 5. fee_quotes — a quote is not a charge and not revenue (ADR-044 §9). Owned by the requesting user; bound at most
--    once to one donation intent. Machine 'fee_quote'.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_quotes (
  id                         uuid        NOT NULL,
  user_id                    uuid        NOT NULL,
  campaign_id                uuid        NOT NULL,
  transaction_type           text        NOT NULL DEFAULT 'DONATION',
  currency                   char(3)     NOT NULL,
  amount_minor               bigint      NOT NULL,
  payment_method             text,
  provider_id                uuid,
  calculation_id             uuid        NOT NULL,
  policy_version_ids         uuid[]      NOT NULL,
  provider_schedule_ids      uuid[]      NOT NULL,
  platform_fee_minor         bigint      NOT NULL,   -- 0 when no platform-fee policy is active (nothing is charged)
  platform_fee_basis         text        NOT NULL,
  provider_fee_minor         bigint,
  provider_fee_basis         text        NOT NULL,
  donor_charge_minor         bigint,
  campaign_allocation_minor  bigint,
  platform_net_minor         bigint,
  status                     text        NOT NULL,
  system_generated           boolean     NOT NULL DEFAULT false,
  idempotency_key            uuid,
  expires_at                 timestamptz NOT NULL,
  donation_intent_id         uuid,
  bound_at                   timestamptz,
  invalidated_at             timestamptz,
  invalidation_reason        text,
  fee_environment            text        NOT NULL DEFAULT 'TEST',
  created_at                 timestamptz NOT NULL DEFAULT now(),
  updated_at                 timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_quotes PRIMARY KEY (id),
  CONSTRAINT fk_fee_quotes_user FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_quotes_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_quotes_calculation FOREIGN KEY (calculation_id, currency) REFERENCES app.fee_calculations (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_quotes_intent FOREIGN KEY (donation_intent_id) REFERENCES app.donation_intents (id) ON DELETE RESTRICT,
  CONSTRAINT uq_fee_quotes_idempotency UNIQUE (user_id, idempotency_key),
  CONSTRAINT uq_fee_quotes_intent UNIQUE (donation_intent_id),
  CONSTRAINT ck_fee_quotes_type CHECK (transaction_type = 'DONATION'),
  CONSTRAINT ck_fee_quotes_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_fee_quotes_status CHECK (status IN ('ACTIVE','BOUND','EXPIRED','INVALIDATED')),
  CONSTRAINT ck_fee_quotes_platform_basis CHECK (platform_fee_basis IN ('QUOTED','NOT_CONFIGURED') AND
    (platform_fee_basis = 'QUOTED' OR platform_fee_minor = 0)),
  -- an unknown provider fee is never zero
  CONSTRAINT ck_fee_quotes_provider_basis CHECK (provider_fee_basis IN ('ESTIMATED','UNKNOWN')),
  CONSTRAINT ck_fee_quotes_provider_fee CHECK ((provider_fee_minor IS NULL) = (provider_fee_basis = 'UNKNOWN') AND (provider_fee_minor IS NULL OR provider_fee_minor >= 0)),
  CONSTRAINT ck_fee_quotes_platform_fee CHECK (platform_fee_minor >= 0),
  CONSTRAINT ck_fee_quotes_breakdown CHECK (donor_charge_minor IS NULL OR campaign_allocation_minor IS NULL OR platform_net_minor IS NULL
    OR provider_fee_minor IS NULL OR donor_charge_minor = campaign_allocation_minor + platform_net_minor + provider_fee_minor),
  CONSTRAINT ck_fee_quotes_bound CHECK ((status = 'BOUND') = (donation_intent_id IS NOT NULL) AND (donation_intent_id IS NULL OR bound_at IS NOT NULL)),
  CONSTRAINT ck_fee_quotes_invalidated CHECK ((status = 'INVALIDATED') = (invalidated_at IS NOT NULL AND invalidation_reason IS NOT NULL)),
  CONSTRAINT ck_fee_quotes_expiry CHECK (expires_at > created_at),
  CONSTRAINT ck_fee_quotes_test_only_stage12 CHECK (fee_environment = 'TEST')
);
CREATE INDEX ix_fee_quotes_user ON app.fee_quotes (user_id, created_at DESC);
CREATE INDEX ix_fee_quotes_active ON app.fee_quotes (currency, expires_at) WHERE status = 'ACTIVE';
CREATE INDEX ix_fee_quotes_created ON app.fee_quotes (created_at DESC, id DESC);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('fee_quote', '',       'ACTIVE'),
  ('fee_quote', 'ACTIVE', 'BOUND'),
  ('fee_quote', 'ACTIVE', 'EXPIRED'),
  ('fee_quote', 'ACTIVE', 'INVALIDATED');
CREATE TRIGGER trg_fee_quotes_terms BEFORE UPDATE ON app.fee_quotes
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'donation_intent_id', 'bound_at', 'invalidated_at', 'invalidation_reason');
CREATE TRIGGER trg_fee_quotes_guard_status BEFORE INSERT OR UPDATE OF status ON app.fee_quotes
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('fee_quote');
CREATE TRIGGER trg_fee_quotes_updated_at BEFORE UPDATE ON app.fee_quotes FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_quotes_no_delete BEFORE DELETE ON app.fee_quotes FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_quotes_no_truncate BEFORE TRUNCATE ON app.fee_quotes FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- payments: the quote bound to each donation intent and the donor-covered charge it implies (ADR-044 §9; the
-- payment amount stays the donation amount — passing a higher charge to a provider needs provider confirmation)
ALTER TABLE app.donation_intents ADD COLUMN fee_quote_id uuid;
ALTER TABLE app.donation_intents ADD COLUMN fee_donor_charge_minor bigint;
ALTER TABLE app.donation_intents ADD CONSTRAINT fk_donation_intents_fee_quote FOREIGN KEY (fee_quote_id) REFERENCES app.fee_quotes (id) ON DELETE RESTRICT;
ALTER TABLE app.donation_intents ADD CONSTRAINT uq_donation_intents_fee_quote UNIQUE (fee_quote_id);
ALTER TABLE app.donation_intents ADD CONSTRAINT ck_donation_intents_fee_charge CHECK (fee_donor_charge_minor IS NULL OR fee_donor_charge_minor >= amount_minor);
CREATE TRIGGER trg_donation_intents_fee_immutable BEFORE UPDATE ON app.donation_intents
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('fee_quote_id', 'fee_donor_charge_minor');

-- -----------------------------------------------------------------------------------------------------
-- 6. fee_assessments — a calculated obligation under an approved policy (not cash, not revenue, not settlement).
--    One per (source, category): duplicates are impossible (ADR-044 §10).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_assessments (
  id                    uuid        NOT NULL,
  source_type           text        NOT NULL,
  source_id             uuid        NOT NULL,
  category              text        NOT NULL,
  campaign_id           uuid,
  currency              char(3)     NOT NULL,
  basis_minor           bigint      NOT NULL,
  fee_minor             bigint,
  basis                 text        NOT NULL,
  responsibility        text,
  donor_part_minor      bigint,
  organiser_part_minor  bigint,
  platform_part_minor   bigint,
  policy_version_id     uuid,
  provider_schedule_id  uuid,
  calculation_id        uuid        NOT NULL,
  component_id          uuid        NOT NULL,
  quote_id              uuid,
  payment_method        text,
  provider_id           uuid,
  accounting_status     text        NOT NULL,
  test_payment          boolean     NOT NULL,
  synthetic             boolean     NOT NULL,
  fee_environment       text        NOT NULL DEFAULT 'TEST',
  assessed_at           timestamptz NOT NULL,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_assessments PRIMARY KEY (id),
  CONSTRAINT uq_fee_assessments_source UNIQUE (source_type, source_id, category),
  CONSTRAINT uq_fee_assessments_id_currency UNIQUE (id, currency),
  CONSTRAINT fk_fee_assessments_calculation FOREIGN KEY (calculation_id, currency) REFERENCES app.fee_calculations (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_assessments_component FOREIGN KEY (component_id) REFERENCES app.fee_components (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_assessments_policy_version FOREIGN KEY (policy_version_id) REFERENCES app.fee_policy_versions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_assessments_provider_schedule FOREIGN KEY (provider_schedule_id) REFERENCES app.fee_provider_schedules (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_assessments_quote FOREIGN KEY (quote_id) REFERENCES app.fee_quotes (id) ON DELETE RESTRICT,
  CONSTRAINT ck_fee_assessments_source_type CHECK (source_type IN ('DONATION_PAYMENT','SYNTHETIC_DONATION','PAYOUT')),
  CONSTRAINT ck_fee_assessments_category CHECK (category IN ('PLATFORM_FEE','PAYMENT_PROCESSING_FEE','PAYOUT_PROCESSING_FEE','REFUND_FEE','CHARGEBACK_FEE')),
  CONSTRAINT ck_fee_assessments_basis_amount CHECK (basis_minor > 0),
  CONSTRAINT ck_fee_assessments_basis CHECK (basis IN ('QUOTED','ESTIMATED','PROVIDER_REPORTED','PROVIDER_CONFIRMED','UNKNOWN')),
  CONSTRAINT ck_fee_assessments_fee CHECK ((fee_minor IS NULL) = (basis = 'UNKNOWN') AND (fee_minor IS NULL OR fee_minor >= 0)),
  CONSTRAINT ck_fee_assessments_parts CHECK (fee_minor IS NULL OR (donor_part_minor >= 0 AND organiser_part_minor >= 0 AND platform_part_minor >= 0
    AND donor_part_minor + organiser_part_minor + platform_part_minor = fee_minor)),
  CONSTRAINT ck_fee_assessments_accounting CHECK (accounting_status IN ('NOT_POSTED_TEST_PAYMENT','PENDING_POSTING','POSTED','NOT_APPLICABLE')),
  -- a real-payment (non-synthetic) assessment is a TEST payment in Stage 12 and is never posted (ADR-042)
  CONSTRAINT ck_fee_assessments_test_payments_not_posted CHECK (source_type <> 'DONATION_PAYMENT' OR (test_payment AND accounting_status = 'NOT_POSTED_TEST_PAYMENT')),
  CONSTRAINT ck_fee_assessments_synthetic CHECK (synthetic = (source_type = 'SYNTHETIC_DONATION')),
  CONSTRAINT ck_fee_assessments_test_only_stage12 CHECK (fee_environment = 'TEST')
);
CREATE INDEX ix_fee_assessments_campaign ON app.fee_assessments (campaign_id, assessed_at DESC) WHERE campaign_id IS NOT NULL;
CREATE INDEX ix_fee_assessments_assessed ON app.fee_assessments (assessed_at DESC, id DESC);
CREATE INDEX ix_fee_assessments_pending ON app.fee_assessments (created_at) WHERE accounting_status = 'PENDING_POSTING';
CREATE TRIGGER trg_fee_assessments_terms BEFORE UPDATE ON app.fee_assessments
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('accounting_status');
CREATE FUNCTION app.fee_assessments_accounting_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.accounting_status <> NEW.accounting_status AND NOT (OLD.accounting_status = 'PENDING_POSTING' AND NEW.accounting_status = 'POSTED') THEN
    RAISE EXCEPTION 'fee assessment % accounting status % cannot become %', OLD.id, OLD.accounting_status, NEW.accounting_status
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_fee_assessments_accounting BEFORE UPDATE ON app.fee_assessments
  FOR EACH ROW EXECUTE FUNCTION app.fee_assessments_accounting_guard();
CREATE TRIGGER trg_fee_assessments_updated_at BEFORE UPDATE ON app.fee_assessments FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_assessments_no_delete BEFORE DELETE ON app.fee_assessments FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_assessments_no_truncate BEFORE TRUNCATE ON app.fee_assessments FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- payouts: the payout fee estimate captured at request time (ADR-044 §13); the reservation is never changed by it
ALTER TABLE app.payout_requests ADD COLUMN fee_calculation_id uuid;
ALTER TABLE app.payout_requests ADD COLUMN fee_estimate_minor bigint;
ALTER TABLE app.payout_requests ADD COLUMN fee_basis text;
ALTER TABLE app.payout_requests ADD COLUMN fee_responsibility text;
ALTER TABLE app.payout_requests ADD CONSTRAINT fk_payout_requests_fee_calculation FOREIGN KEY (fee_calculation_id) REFERENCES app.fee_calculations (id) ON DELETE RESTRICT;
ALTER TABLE app.payout_requests ADD CONSTRAINT ck_payout_requests_fee_estimate CHECK (
  (fee_calculation_id IS NULL AND fee_estimate_minor IS NULL AND fee_basis IS NULL AND fee_responsibility IS NULL)
  OR (fee_calculation_id IS NOT NULL AND fee_basis IN ('ESTIMATED','UNKNOWN')
      AND (fee_estimate_minor IS NULL) = (fee_basis = 'UNKNOWN')
      AND (fee_responsibility IS NULL OR fee_responsibility IN ('ORGANISER_COVERED','PLATFORM_ABSORBED'))));
-- an organiser-borne fee that is known must be below the payout amount (the beneficiary receives amount − fee)
ALTER TABLE app.payout_requests ADD CONSTRAINT ck_payout_requests_fee_below_amount CHECK (
  fee_estimate_minor IS NULL OR fee_responsibility IS DISTINCT FROM 'ORGANISER_COVERED' OR fee_estimate_minor < amount_minor);
CREATE TRIGGER trg_payout_requests_fee_immutable BEFORE UPDATE ON app.payout_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('fee_calculation_id', 'fee_estimate_minor', 'fee_basis', 'fee_responsibility');

-- -----------------------------------------------------------------------------------------------------
-- 7. fee_adjustments — corrections are new rows, never edits (ADR-044 §12). The net of APPLIED/APPROVED
--    adjustments never takes an assessment below zero. Machine 'fee_adjustment'.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_adjustments (
  id                   uuid        NOT NULL,
  assessment_id        uuid        NOT NULL,
  currency             char(3)     NOT NULL,
  reason               text        NOT NULL,
  direction            text        NOT NULL,
  amount_minor         bigint      NOT NULL,
  source_type          text        NOT NULL,
  source_id            uuid        NOT NULL,
  basis_minor          bigint,      -- e.g. the refunded amount
  calculation          jsonb       NOT NULL DEFAULT '{}',
  note                 text        NOT NULL,
  status               text        NOT NULL,
  actor_type           text        NOT NULL,
  requested_by         uuid,
  decided_by           uuid,
  decided_at           timestamptz,
  decided_step_up_at   timestamptz,
  decision_reason      text,
  accounting_status    text        NOT NULL,
  ledger_adjustment_id uuid,        -- a Stage 10 maker-checker ledger adjustment that corrects the books, if any
  fee_environment      text        NOT NULL DEFAULT 'TEST',
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_adjustments PRIMARY KEY (id),
  CONSTRAINT fk_fee_adjustments_assessment FOREIGN KEY (assessment_id, currency) REFERENCES app.fee_assessments (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_adjustments_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_adjustments_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_fee_adjustments_source UNIQUE (assessment_id, reason, source_type, source_id),
  CONSTRAINT ck_fee_adjustments_reason CHECK (reason IN ('REFUND','CHARGEBACK','PROVIDER_CORRECTION','POLICY_CORRECTION','ACCOUNTING_ADJUSTMENT')),
  CONSTRAINT ck_fee_adjustments_direction CHECK (direction IN ('DECREASE','INCREASE')),
  -- a refund or chargeback that reverses no fee (e.g. NON_REFUNDABLE) is still recorded, with amount 0, so later
  -- partial refunds are bounded by the original amount; every other adjustment moves a positive amount
  CONSTRAINT ck_fee_adjustments_amount CHECK (amount_minor > 0 OR (amount_minor = 0 AND reason IN ('REFUND','CHARGEBACK'))),
  CONSTRAINT ck_fee_adjustments_source_type CHECK (source_type ~ '^[a-z][a-z_]{2,40}$'),
  CONSTRAINT ck_fee_adjustments_note CHECK (char_length(btrim(note)) BETWEEN 5 AND 2000),
  CONSTRAINT ck_fee_adjustments_status CHECK (status IN ('APPLIED','PENDING_APPROVAL','APPROVED','REJECTED')),
  CONSTRAINT ck_fee_adjustments_actor CHECK (actor_type IN ('SYSTEM','STAFF') AND (actor_type = 'SYSTEM' OR requested_by IS NOT NULL)),
  -- refunds and chargebacks are computed from the policy (SYSTEM); every other reason is maker-checker
  CONSTRAINT ck_fee_adjustments_manual CHECK ((reason IN ('REFUND','CHARGEBACK')) = (status = 'APPLIED')),
  CONSTRAINT ck_fee_adjustments_decision CHECK ((status IN ('APPROVED','REJECTED')) = (decided_by IS NOT NULL) AND (decided_by IS NULL OR
    (decided_at IS NOT NULL AND decided_step_up_at IS NOT NULL AND decision_reason IS NOT NULL AND decided_by <> requested_by))),
  CONSTRAINT ck_fee_adjustments_accounting CHECK (accounting_status IN ('NOT_POSTED','POSTED','NOT_APPLICABLE')),
  CONSTRAINT ck_fee_adjustments_test_only_stage12 CHECK (fee_environment = 'TEST')
);
CREATE INDEX ix_fee_adjustments_assessment ON app.fee_adjustments (assessment_id);
CREATE INDEX ix_fee_adjustments_created ON app.fee_adjustments (created_at DESC, id DESC);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('fee_adjustment', '',                 'APPLIED'),
  ('fee_adjustment', '',                 'PENDING_APPROVAL'),
  ('fee_adjustment', 'PENDING_APPROVAL', 'APPROVED'),
  ('fee_adjustment', 'PENDING_APPROVAL', 'REJECTED');

CREATE FUNCTION app.fee_adjustments_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  a app.fee_assessments%ROWTYPE;
  net bigint;
BEGIN
  IF NEW.decided_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.decided_by IS NULL)
     AND NEW.requested_by = ANY (app.actor_identities(NEW.decided_by)) THEN
    RAISE EXCEPTION 'maker-checker: % is (or is linked to) the requester of fee adjustment %', NEW.decided_by, NEW.id
      USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.status IN ('APPLIED','APPROVED') AND (TG_OP = 'INSERT' OR OLD.status <> NEW.status) THEN
    SELECT * INTO a FROM app.fee_assessments WHERE id = NEW.assessment_id FOR UPDATE;
    IF a.fee_minor IS NULL THEN
      RAISE EXCEPTION 'fee assessment % has an UNKNOWN amount and cannot be adjusted', a.id USING ERRCODE = 'check_violation';
    END IF;
    SELECT a.fee_minor + coalesce(sum(CASE direction WHEN 'INCREASE' THEN amount_minor ELSE -amount_minor END), 0) INTO net
      FROM app.fee_adjustments WHERE assessment_id = NEW.assessment_id AND status IN ('APPLIED','APPROVED') AND id <> NEW.id;
    net := net + CASE NEW.direction WHEN 'INCREASE' THEN NEW.amount_minor ELSE -NEW.amount_minor END;
    IF net < 0 THEN
      RAISE EXCEPTION 'fee adjustments would take assessment % below zero', a.id USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_fee_adjustments_guard BEFORE INSERT OR UPDATE ON app.fee_adjustments FOR EACH ROW EXECUTE FUNCTION app.fee_adjustments_guard();
CREATE TRIGGER trg_fee_adjustments_terms BEFORE UPDATE ON app.fee_adjustments
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'decided_by', 'decided_at', 'decided_step_up_at', 'decision_reason',
    'accounting_status', 'ledger_adjustment_id');
CREATE TRIGGER trg_fee_adjustments_guard_status BEFORE INSERT OR UPDATE OF status ON app.fee_adjustments
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('fee_adjustment');
CREATE TRIGGER trg_fee_adjustments_updated_at BEFORE UPDATE ON app.fee_adjustments FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_adjustments_no_delete BEFORE DELETE ON app.fee_adjustments FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_adjustments_no_truncate BEFORE TRUNCATE ON app.fee_adjustments FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 8. fee_accounting_references — the ledger journals that carry fee effects (cross-schema FK: the journal is
--    committed by the ledger before the reference is written). Immutable; one per journal key.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_accounting_references (
  id                     uuid        NOT NULL,
  assessment_id          uuid,
  adjustment_id          uuid,
  ledger_transaction_id  uuid        NOT NULL,
  journal_key            text        NOT NULL,
  posting_rule           text        NOT NULL,
  purpose                text        NOT NULL,
  currency               char(3)     NOT NULL,
  fee_amount_minor       bigint      NOT NULL,
  policy_version_id      uuid,
  recorded_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_accounting_references PRIMARY KEY (id),
  CONSTRAINT fk_fee_accounting_references_assessment FOREIGN KEY (assessment_id) REFERENCES app.fee_assessments (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_accounting_references_adjustment FOREIGN KEY (adjustment_id) REFERENCES app.fee_adjustments (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_accounting_references_journal FOREIGN KEY (ledger_transaction_id) REFERENCES ledger.ledger_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT uq_fee_accounting_references_key UNIQUE (journal_key),
  CONSTRAINT ck_fee_accounting_references_subject CHECK (assessment_id IS NOT NULL OR adjustment_id IS NOT NULL),
  CONSTRAINT ck_fee_accounting_references_purpose CHECK (purpose IN ('CAPTURE','PROVIDER_FEE','SETTLEMENT','RELEASE','REFUND','REFUND_SETTLED',
    'CHARGEBACK','CHARGEBACK_FEE','PAYOUT_FEE')),
  CONSTRAINT ck_fee_accounting_references_amount CHECK (fee_amount_minor >= 0)
);
CREATE INDEX ix_fee_accounting_references_assessment ON app.fee_accounting_references (assessment_id);
CREATE INDEX ix_fee_accounting_references_adjustment ON app.fee_accounting_references (adjustment_id) WHERE adjustment_id IS NOT NULL;
CREATE TRIGGER trg_fee_accounting_references_no_mutation BEFORE UPDATE OR DELETE ON app.fee_accounting_references FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_accounting_references_no_truncate BEFORE TRUNCATE ON app.fee_accounting_references FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 9. fee_provider_reports — provider-reported or provider-confirmed fees for an assessment. In Stage 12 only the
--    TEST fixture writes them (evidence SIMULATED). Immutable.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_provider_reports (
  id                  uuid        NOT NULL,
  assessment_id       uuid        NOT NULL,
  currency            char(3)     NOT NULL,
  reported_fee_minor  bigint      NOT NULL,
  basis               text        NOT NULL,
  evidence_class      text        NOT NULL,
  source_reference    text        NOT NULL,
  reported_at         timestamptz NOT NULL,
  recorded_by         text        NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_provider_reports PRIMARY KEY (id),
  CONSTRAINT fk_fee_provider_reports_assessment FOREIGN KEY (assessment_id, currency) REFERENCES app.fee_assessments (id, currency) ON DELETE RESTRICT,
  CONSTRAINT uq_fee_provider_reports_source UNIQUE (assessment_id, source_reference),
  CONSTRAINT ck_fee_provider_reports_amount CHECK (reported_fee_minor >= 0),
  CONSTRAINT ck_fee_provider_reports_basis CHECK (basis IN ('PROVIDER_REPORTED','PROVIDER_CONFIRMED')),
  CONSTRAINT ck_fee_provider_reports_evidence CHECK (evidence_class = 'SIMULATED'),  -- Stage 12: no real provider evidence exists
  CONSTRAINT ck_fee_provider_reports_reference CHECK (char_length(source_reference) BETWEEN 1 AND 200)
);
CREATE TRIGGER trg_fee_provider_reports_no_mutation BEFORE UPDATE OR DELETE ON app.fee_provider_reports FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_provider_reports_no_truncate BEFORE TRUNCATE ON app.fee_provider_reports FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 10. Reconciliation: runs and references (detections are immutable; only the resolution fields change).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_reconciliation_runs (
  id            uuid        NOT NULL,
  triggered_by  text        NOT NULL,
  started_at    timestamptz NOT NULL,
  finished_at   timestamptz,
  checked       integer     NOT NULL DEFAULT 0,
  matched       integer     NOT NULL DEFAULT 0,
  mismatches    integer     NOT NULL DEFAULT 0,
  CONSTRAINT pk_fee_reconciliation_runs PRIMARY KEY (id),
  CONSTRAINT ck_fee_reconciliation_runs_counts CHECK (checked >= 0 AND matched >= 0 AND mismatches >= 0)
);
CREATE INDEX ix_fee_reconciliation_runs_started ON app.fee_reconciliation_runs (started_at DESC);
CREATE TRIGGER trg_fee_reconciliation_runs_no_delete BEFORE DELETE ON app.fee_reconciliation_runs FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.fee_reconciliation_references (
  id                     uuid        NOT NULL,
  run_id                 uuid        NOT NULL,
  kind                   text        NOT NULL,
  severity               text        NOT NULL,
  assessment_id          uuid,
  adjustment_id          uuid,
  ledger_transaction_id  uuid,
  currency               char(3),
  expected               jsonb       NOT NULL DEFAULT '{}',
  observed               jsonb       NOT NULL DEFAULT '{}',
  detail                 text        NOT NULL,
  dedupe_key             text        NOT NULL,
  status                 text        NOT NULL,
  detected_at            timestamptz NOT NULL,
  resolved_by            uuid,
  resolved_at            timestamptz,
  resolution_note        text,
  fee_environment        text        NOT NULL DEFAULT 'TEST',
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_reconciliation_references PRIMARY KEY (id),
  CONSTRAINT fk_fee_reconciliation_references_run FOREIGN KEY (run_id) REFERENCES app.fee_reconciliation_runs (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_reconciliation_references_assessment FOREIGN KEY (assessment_id) REFERENCES app.fee_assessments (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_reconciliation_references_resolved_by FOREIGN KEY (resolved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_fee_reconciliation_references_kind CHECK (kind IN ('MISSING_FEE','DUPLICATE_FEE','AMOUNT_MISMATCH','CURRENCY_MISMATCH',
    'POLICY_MISMATCH','ROUNDING_MISMATCH','UNRECONCILED_PROVIDER_FEE','MISSING_POSTING')),
  CONSTRAINT ck_fee_reconciliation_references_severity CHECK (severity IN ('SEV1','SEV2','SEV3')),
  CONSTRAINT ck_fee_reconciliation_references_status CHECK (status IN ('OPEN','RESOLVED')),
  CONSTRAINT ck_fee_reconciliation_references_resolution CHECK ((status = 'RESOLVED') = (resolved_by IS NOT NULL AND resolved_at IS NOT NULL
    AND resolution_note IS NOT NULL)),
  CONSTRAINT ck_fee_reconciliation_references_test_only_stage12 CHECK (fee_environment = 'TEST')
);
CREATE UNIQUE INDEX uq_fee_reconciliation_references_open ON app.fee_reconciliation_references (dedupe_key) WHERE status = 'OPEN';
CREATE INDEX ix_fee_reconciliation_references_status ON app.fee_reconciliation_references (status, detected_at DESC);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('fee_reconciliation', '',     'OPEN'),
  ('fee_reconciliation', 'OPEN', 'RESOLVED');
CREATE TRIGGER trg_fee_reconciliation_references_terms BEFORE UPDATE ON app.fee_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'resolved_by', 'resolved_at', 'resolution_note');
CREATE TRIGGER trg_fee_reconciliation_references_guard_status BEFORE INSERT OR UPDATE OF status ON app.fee_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('fee_reconciliation');
CREATE TRIGGER trg_fee_reconciliation_references_updated_at BEFORE UPDATE ON app.fee_reconciliation_references FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_reconciliation_references_no_delete BEFORE DELETE ON app.fee_reconciliation_references FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_reconciliation_references_no_truncate BEFORE TRUNCATE ON app.fee_reconciliation_references FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 11. fee_audit_events — append-only domain history of every fee aggregate (the platform audit log is written too).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.fee_audit_events (
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
  CONSTRAINT pk_fee_audit_events PRIMARY KEY (id),
  CONSTRAINT ck_fee_audit_events_aggregate CHECK (aggregate_type IN ('fee_policy','fee_policy_version','fee_provider_schedule','fee_quote',
    'fee_assessment','fee_adjustment','fee_reconciliation','fee_fixture')),
  CONSTRAINT ck_fee_audit_events_type CHECK (event_type ~ '^[A-Z][A-Z_]{2,60}$'),
  CONSTRAINT ck_fee_audit_events_actor CHECK (actor_type IN ('USER','STAFF','SYSTEM'))
);
CREATE INDEX ix_fee_audit_events_aggregate ON app.fee_audit_events (aggregate_type, aggregate_id, occurred_at);
CREATE TRIGGER trg_fee_audit_events_no_mutation BEFORE UPDATE OR DELETE ON app.fee_audit_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_audit_events_no_truncate BEFORE TRUNCATE ON app.fee_audit_events FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 12. Permissions (fee.config.request / fee.config.approve exist since Stage 4; both require step-up).
-- -----------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:fee.view')::uuid, 'fee.view', 'View fee policies, provider fee evidence, quotes, assessments, adjustments, fee reconciliation and revenue reports', true, false),
  (md5('permission:fee.adjust.request')::uuid, 'fee.adjust.request', 'Request a manual fee adjustment and run fee reconciliation (maker)', true, true),
  (md5('permission:fee.adjust.approve')::uuid, 'fee.adjust.approve', 'Approve or reject a manual fee adjustment (checker)', true, true),
  (md5('permission:fee.fixtures')::uuid, 'fee.fixtures', 'Run TEST_ONLY synthetic fee accounting fixtures (development and test environments only)', false, false);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES ('FINANCE', 'fee.view'), ('COMPLIANCE', 'fee.view'), ('BUSINESS_APPROVER', 'fee.view'),
             ('FINANCE', 'fee.adjust.request'), ('BUSINESS_APPROVER', 'fee.adjust.approve'), ('ADMIN', 'fee.fixtures')) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN ('fee.view','fee.adjust.request','fee.adjust.approve','fee.fixtures'));
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('fee.view','fee.adjust.request','fee.adjust.approve','fee.fixtures');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP TABLE app.fee_audit_events;
DROP TABLE app.fee_reconciliation_references;
DROP TABLE app.fee_reconciliation_runs;
DROP TABLE app.fee_provider_reports;
DROP TABLE app.fee_accounting_references;
DROP TABLE app.fee_adjustments;
DROP FUNCTION app.fee_adjustments_guard();
DROP TRIGGER trg_payout_requests_fee_immutable ON app.payout_requests;
ALTER TABLE app.payout_requests DROP CONSTRAINT ck_payout_requests_fee_below_amount;
ALTER TABLE app.payout_requests DROP CONSTRAINT ck_payout_requests_fee_estimate;
ALTER TABLE app.payout_requests DROP CONSTRAINT fk_payout_requests_fee_calculation;
ALTER TABLE app.payout_requests DROP COLUMN fee_responsibility;
ALTER TABLE app.payout_requests DROP COLUMN fee_basis;
ALTER TABLE app.payout_requests DROP COLUMN fee_estimate_minor;
ALTER TABLE app.payout_requests DROP COLUMN fee_calculation_id;
DROP TABLE app.fee_assessments;
DROP FUNCTION app.fee_assessments_accounting_guard();
DROP TRIGGER trg_donation_intents_fee_immutable ON app.donation_intents;
ALTER TABLE app.donation_intents DROP CONSTRAINT ck_donation_intents_fee_charge;
ALTER TABLE app.donation_intents DROP CONSTRAINT uq_donation_intents_fee_quote;
ALTER TABLE app.donation_intents DROP CONSTRAINT fk_donation_intents_fee_quote;
ALTER TABLE app.donation_intents DROP COLUMN fee_donor_charge_minor;
ALTER TABLE app.donation_intents DROP COLUMN fee_quote_id;
DROP TABLE app.fee_quotes;
DROP TABLE app.fee_components;
DROP TABLE app.fee_calculations;
DROP TABLE app.fee_provider_schedules;
DROP FUNCTION app.fee_provider_schedules_guard();
DROP TABLE app.fee_policy_versions;
DROP FUNCTION app.fee_policy_versions_guard();
DROP TABLE app.fee_policies;
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine IN ('fee_policy_version','fee_provider_schedule','fee_quote','fee_adjustment','fee_reconciliation');
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
ALTER TABLE ledger.ledger_source_references DROP CONSTRAINT ck_ledger_source_references_source_system;
ALTER TABLE ledger.ledger_source_references ADD CONSTRAINT ck_ledger_source_references_source_system
  CHECK (source_system IN ('payments', 'payouts', 'reconciliation', 'staff', 'synthetic_fixture')) NOT VALID;
ALTER TABLE ledger.ledger_transactions DROP CONSTRAINT ck_ledger_transactions_fees_simulated_in_test;
ALTER TABLE ledger.ledger_transactions DROP CONSTRAINT ck_ledger_transactions_source_system;
-- NOT VALID: fee journals posted while this migration was applied are append-only and stay
ALTER TABLE ledger.ledger_transactions ADD CONSTRAINT ck_ledger_transactions_source_system
  CHECK (source_system IN ('payments', 'payouts', 'reconciliation', 'staff', 'synthetic_fixture')) NOT VALID;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
