-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0014: campaigns module — campaign_categories, campaign_review_policies, campaigns, campaign_versions,
-- campaign_goals, campaign_media, campaign_updates, campaign_beneficiaries, campaign_reviews,
-- campaign_status_history, campaign_reports, campaign_moderation_actions. Schema: app.
-- Docs: docs/database/campaign-schema.md, docs/PRODUCT.md §6, docs/compliance/campaign-approval-policy.md.
--
-- There is deliberately NO raised/balance column anywhere in this file: raised, settled, available and paid
-- out amounts are derived per currency from the ledger (ledger module), never stored on the campaign.
-- Publication (status, visibility) is separate from financial restriction (risk.holds + ledger moves).
-- =====================================================================================================

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_categories — configuration data (PRODUCT §5). default_risk_tier values are the provisional
-- starting point of campaign-approval-policy §4 (tier assignment is COMPLIANCE-proposed, business-owner
-- approved configuration). Classification: C0.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_categories (
  id                    uuid        NOT NULL,
  code                  text        NOT NULL,
  name                  text        NOT NULL,
  default_risk_tier     text        NOT NULL,
  requires_organisation boolean     NOT NULL DEFAULT false,  -- organisation-run categories (KYB_COMPLETE gate)
  enabled               boolean     NOT NULL DEFAULT true,
  sort_order            integer     NOT NULL DEFAULT 0,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_categories PRIMARY KEY (id),
  CONSTRAINT uq_campaign_categories_code UNIQUE (code),
  CONSTRAINT ck_campaign_categories_code CHECK (code ~ '^[A-Z][A-Z_]*$'),
  CONSTRAINT ck_campaign_categories_risk_tier CHECK (default_risk_tier IN ('STANDARD', 'ELEVATED', 'HIGH'))
);
CREATE TRIGGER trg_campaign_categories_set_updated_at BEFORE UPDATE ON app.campaign_categories
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_categories_immutable_cols BEFORE UPDATE ON app.campaign_categories
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'code', 'created_at');
CREATE TRIGGER trg_campaign_categories_no_delete BEFORE DELETE ON app.campaign_categories
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.campaign_categories (id, code, name, default_risk_tier, requires_organisation, sort_order)
SELECT md5('campaign_category:' || c.code)::uuid, c.code, c.name, c.tier, c.org, c.ord
FROM (VALUES
  ('MEDICAL',                 'Medical',                          'HIGH',     false, 10),
  ('EDUCATION',               'Education',                        'STANDARD', false, 20),
  ('FUNERAL',                 'Funeral',                          'ELEVATED', false, 30),
  ('EMERGENCY',               'Emergency',                        'ELEVATED', false, 40),
  ('COMMUNITY',               'Community',                        'ELEVATED', false, 50),
  ('CHARITY',                 'Charity',                          'ELEVATED', true,  60),
  ('RELIGIOUS_COMMUNITY_ORG', 'Religious / community organisation', 'ELEVATED', true, 70),
  ('SPORTS',                  'Sports',                           'STANDARD', false, 80),
  ('PERSONAL',                'Personal causes',                  'HIGH',     false, 90),
  ('OTHER',                   'Other approved causes',            'HIGH',     false, 100)
) AS c(code, name, tier, org, ord);

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_review_policies — versioned, immutable once PUBLISHED (campaign-approval-policy §5).
-- Policy changes are maker-checker. The DB rejects an ELEVATED/HIGH policy without at least one MANUAL
-- check ("no auto-approve on verified identity", §1). Classification: C1. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_review_policies (
  id                 uuid        NOT NULL,
  category_id        uuid        NOT NULL,
  risk_tier          text        NOT NULL,
  version            integer     NOT NULL,
  checks             jsonb       NOT NULL,   -- [{check_code, required, mode: AUTOMATIC|MANUAL, failure_effect}]
  required_documents jsonb       NOT NULL DEFAULT '[]',
  escalation_rules   jsonb       NOT NULL DEFAULT '[]',
  payout_controls    jsonb       NOT NULL DEFAULT '{}',  -- {first_payout_manual, payout_cap_ref (risk.limits key), reserve_ref}
  sla_ref            text,                               -- PD-31 configuration key
  status             text        NOT NULL DEFAULT 'DRAFT',
  effective_from     timestamptz,
  effective_to       timestamptz,
  proposed_by        uuid        NOT NULL,
  approved_by        uuid,
  approval_reason    text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_review_policies PRIMARY KEY (id),
  CONSTRAINT uq_campaign_review_policies_version UNIQUE (category_id, risk_tier, version),
  CONSTRAINT fk_campaign_review_policies_category_id FOREIGN KEY (category_id) REFERENCES app.campaign_categories (id),
  CONSTRAINT fk_campaign_review_policies_proposed_by FOREIGN KEY (proposed_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_review_policies_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_review_policies_risk_tier CHECK (risk_tier IN ('STANDARD', 'ELEVATED', 'HIGH')),
  CONSTRAINT ck_campaign_review_policies_status CHECK (status IN ('DRAFT', 'PUBLISHED', 'RETIRED')),
  CONSTRAINT ck_campaign_review_policies_checks_array CHECK (jsonb_typeof(checks) = 'array' AND jsonb_array_length(checks) > 0),
  CONSTRAINT ck_campaign_review_policies_human_review CHECK (
    risk_tier = 'STANDARD' OR jsonb_path_exists(checks, '$[*] ? (@.mode == "MANUAL")')),
  CONSTRAINT ck_campaign_review_policies_published CHECK (
    status = 'DRAFT' OR (approved_by IS NOT NULL AND approval_reason IS NOT NULL AND effective_from IS NOT NULL)),
  CONSTRAINT ck_campaign_review_policies_maker_checker CHECK (approved_by IS NULL OR approved_by <> proposed_by),
  CONSTRAINT ck_campaign_review_policies_effective CHECK (effective_to IS NULL OR effective_to > effective_from),
  CONSTRAINT ck_campaign_review_policies_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX uq_campaign_review_policies_current ON app.campaign_review_policies (category_id, risk_tier)
  WHERE status = 'PUBLISHED' AND effective_to IS NULL;
CREATE TRIGGER trg_campaign_review_policies_set_updated_at BEFORE UPDATE ON app.campaign_review_policies
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE FUNCTION app.campaign_review_policies_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'DRAFT' AND (to_jsonb(OLD) - ARRAY['status', 'effective_to', 'updated_at'])
                               IS DISTINCT FROM (to_jsonb(NEW) - ARRAY['status', 'effective_to', 'updated_at']) THEN
    RAISE EXCEPTION 'published review policy % is immutable; create a new version', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF (OLD.status = 'PUBLISHED' AND NEW.status NOT IN ('PUBLISHED', 'RETIRED')) OR (OLD.status = 'RETIRED' AND NEW.status <> 'RETIRED') THEN
    RAISE EXCEPTION 'illegal review policy status change % -> %', OLD.status, NEW.status USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_review_policies_guard BEFORE UPDATE ON app.campaign_review_policies
  FOR EACH ROW EXECUTE FUNCTION app.campaign_review_policies_guard();
CREATE TRIGGER trg_campaign_review_policies_no_delete BEFORE DELETE ON app.campaign_review_policies
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.campaigns — the campaign aggregate (mutable, versioned, with append-only status history).
-- Classification: C1/C2 while unpublished; title/summary/story C0 once published (from approved version).
-- Retention: FINANCIAL (campaigns are referenced by payments, ledger owners and payouts; never deleted).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaigns (
  id                        uuid        NOT NULL,
  public_code               text        NOT NULL,          -- 10 chars Crockford base32, random; /c/{slug}-{public_code}
  slug                      text        NOT NULL,
  owner_user_id             uuid,
  owner_account_kind        text        NOT NULL DEFAULT 'USER',
  owner_organisation_id     uuid,
  created_by_user_id        uuid        NOT NULL,          -- the individual (or org representative) who created it
  category_id               uuid        NOT NULL,
  market_code               char(2)     NOT NULL DEFAULT 'ZW',
  -- working copy (owner edits); the public page renders approved_version_id, never the working copy
  title                     text        NOT NULL,
  summary                   text,
  story                     text,
  status                    text        NOT NULL DEFAULT 'DRAFT',
  visibility                text        NOT NULL DEFAULT 'HIDDEN',
  settlement_model          text        NOT NULL DEFAULT 'MODEL_A',
  goal_currency             char(3)     NOT NULL,
  accepted_currencies       char(3)[]   NOT NULL,          -- PD-02: MVP = {goal_currency}
  fundraising_authority_ref uuid,                          -- kyc.fundraising_authorities id. NO FK (app never references kyc).
  risk_tier                 text,                          -- assigned at submission; may be upgraded, never silently downgraded
  approved_version_id       uuid,
  re_review_required        boolean     NOT NULL DEFAULT false,
  re_review_reason          text,
  ends_at                   timestamptz,                   -- instant computed from a Harare calendar date
  ends_at_time_zone         text        NOT NULL DEFAULT 'Africa/Harare',
  submitted_at              timestamptz,
  approved_at               timestamptz,
  published_at              timestamptz,
  completed_at              timestamptz,
  cancelled_at              timestamptz,
  version                   integer     NOT NULL DEFAULT 1,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaigns PRIMARY KEY (id),
  CONSTRAINT uq_campaigns_public_code UNIQUE (public_code),
  CONSTRAINT uq_campaigns_id_goal_currency UNIQUE (id, goal_currency),
  CONSTRAINT fk_campaigns_owner_user FOREIGN KEY (owner_user_id, owner_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_campaigns_owner_organisation_id FOREIGN KEY (owner_organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_campaigns_created_by_user_id FOREIGN KEY (created_by_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaigns_category_id FOREIGN KEY (category_id) REFERENCES app.campaign_categories (id),
  CONSTRAINT fk_campaigns_market_code FOREIGN KEY (market_code) REFERENCES app.markets (code),
  CONSTRAINT fk_campaigns_goal_currency FOREIGN KEY (goal_currency) REFERENCES app.currencies (code),
  CONSTRAINT ck_campaigns_owner_account_kind CHECK (owner_account_kind = 'USER'),
  CONSTRAINT ck_campaigns_exactly_one_owner CHECK (num_nonnulls(owner_user_id, owner_organisation_id) = 1),
  CONSTRAINT ck_campaigns_public_code CHECK (public_code ~ '^[0-9A-HJKMNP-TV-Z]{10}$'),
  CONSTRAINT ck_campaigns_slug CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 80),
  CONSTRAINT ck_campaigns_title CHECK (length(title) BETWEEN 3 AND 120),
  CONSTRAINT ck_campaigns_status CHECK (status IN ('DRAFT', 'SUBMITTED', 'UNDER_REVIEW', 'APPROVED', 'ACTIVE', 'COMPLETED',
                                                   'REJECTED', 'SUSPENDED', 'FROZEN', 'CANCELLED')),
  CONSTRAINT ck_campaigns_visibility CHECK (visibility IN ('PUBLIC', 'UNLISTED', 'HIDDEN')),
  -- Never visible before publication (APPROVED preview is owner-authenticated, not public).
  CONSTRAINT ck_campaigns_visibility_published CHECK (
    visibility = 'HIDDEN' OR status IN ('ACTIVE', 'COMPLETED', 'SUSPENDED', 'FROZEN', 'CANCELLED')),
  CONSTRAINT ck_campaigns_settlement_model CHECK (settlement_model IN ('MODEL_A', 'MODEL_C')),
  -- Model C (direct beneficiary settlement) is open for verified organisations only (ADR-013).
  CONSTRAINT ck_campaigns_model_c_org_only CHECK (settlement_model = 'MODEL_A' OR owner_organisation_id IS NOT NULL),
  CONSTRAINT ck_campaigns_accepted_currencies CHECK (
    cardinality(accepted_currencies) BETWEEN 1 AND 4 AND goal_currency = ANY (accepted_currencies)),
  CONSTRAINT ck_campaigns_risk_tier CHECK (risk_tier IS NULL OR risk_tier IN ('STANDARD', 'ELEVATED', 'HIGH')),
  CONSTRAINT ck_campaigns_fundraising_authority CHECK (fundraising_authority_ref IS NOT NULL OR status IN ('DRAFT', 'CANCELLED')),
  CONSTRAINT ck_campaigns_submitted CHECK (status IN ('DRAFT', 'CANCELLED') OR (submitted_at IS NOT NULL AND risk_tier IS NOT NULL)),
  CONSTRAINT ck_campaigns_approved CHECK (
    status IN ('DRAFT', 'SUBMITTED', 'UNDER_REVIEW', 'REJECTED', 'CANCELLED') OR (approved_at IS NOT NULL AND approved_version_id IS NOT NULL)),
  CONSTRAINT ck_campaigns_published CHECK (status NOT IN ('ACTIVE', 'COMPLETED', 'SUSPENDED', 'FROZEN') OR published_at IS NOT NULL),
  CONSTRAINT ck_campaigns_completed CHECK (status <> 'COMPLETED' OR completed_at IS NOT NULL),
  CONSTRAINT ck_campaigns_cancelled CHECK ((status = 'CANCELLED') = (cancelled_at IS NOT NULL)),
  CONSTRAINT ck_campaigns_re_review CHECK (NOT re_review_required OR re_review_reason IS NOT NULL),
  CONSTRAINT ck_campaigns_ends_at CHECK (ends_at IS NULL OR ends_at > created_at)
);
CREATE INDEX ix_campaigns_owner_user_id ON app.campaigns (owner_user_id) WHERE owner_user_id IS NOT NULL;
CREATE INDEX ix_campaigns_owner_organisation_id ON app.campaigns (owner_organisation_id) WHERE owner_organisation_id IS NOT NULL;
CREATE INDEX ix_campaigns_status ON app.campaigns (status, submitted_at);                -- review queue, lifecycle jobs
CREATE INDEX ix_campaigns_public_listing ON app.campaigns (category_id, published_at) WHERE visibility = 'PUBLIC';
CREATE INDEX ix_campaigns_ends_at ON app.campaigns (ends_at) WHERE status = 'ACTIVE' AND ends_at IS NOT NULL;

CREATE TRIGGER trg_campaigns_guard_status BEFORE INSERT OR UPDATE OF status ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('campaign');
CREATE TRIGGER trg_campaigns_bump_version BEFORE UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaigns_set_updated_at BEFORE UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaigns_keep_created_at BEFORE UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_campaigns_immutable_cols BEFORE UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('public_code', 'owner_user_id', 'owner_organisation_id',
                                                         'created_by_user_id', 'market_code');
CREATE TRIGGER trg_campaigns_no_delete BEFORE DELETE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaigns_no_truncate BEFORE TRUNCATE ON app.campaigns
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Campaign lifecycle: PRODUCT §6.2, all 22 edges + the initial state.
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('campaign', '',             'DRAFT'),
  ('campaign', 'DRAFT',        'SUBMITTED'),
  ('campaign', 'DRAFT',        'CANCELLED'),
  ('campaign', 'SUBMITTED',    'UNDER_REVIEW'),
  ('campaign', 'SUBMITTED',    'DRAFT'),
  ('campaign', 'UNDER_REVIEW', 'APPROVED'),
  ('campaign', 'UNDER_REVIEW', 'REJECTED'),
  ('campaign', 'UNDER_REVIEW', 'DRAFT'),
  ('campaign', 'APPROVED',     'ACTIVE'),
  ('campaign', 'APPROVED',     'CANCELLED'),
  ('campaign', 'ACTIVE',       'COMPLETED'),
  ('campaign', 'ACTIVE',       'SUSPENDED'),
  ('campaign', 'ACTIVE',       'FROZEN'),
  ('campaign', 'ACTIVE',       'CANCELLED'),
  ('campaign', 'SUSPENDED',    'ACTIVE'),
  ('campaign', 'SUSPENDED',    'FROZEN'),
  ('campaign', 'SUSPENDED',    'CANCELLED'),
  ('campaign', 'COMPLETED',    'FROZEN'),
  ('campaign', 'FROZEN',       'ACTIVE'),
  ('campaign', 'FROZEN',       'SUSPENDED'),
  ('campaign', 'FROZEN',       'COMPLETED'),
  ('campaign', 'FROZEN',       'CANCELLED'),
  ('campaign', 'REJECTED',     'DRAFT');

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_versions — append-only content snapshots taken at submission and on material edits.
-- Public pages show campaigns.approved_version_id. Classification: C1/C2; C0 once it is the approved
-- version of a published campaign. Retention: FINANCIAL (what donors were shown).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_versions (
  id                     uuid        NOT NULL,
  campaign_id            uuid        NOT NULL,
  version_number         integer     NOT NULL,
  change_kind            text        NOT NULL,
  material_change_types  text[]      NOT NULL DEFAULT '{}',
  title                  text        NOT NULL,
  summary                text,
  story                  text,
  category_id            uuid        NOT NULL,
  media_object_ids       uuid[]      NOT NULL DEFAULT '{}',
  content_sha256         bytea       NOT NULL,             -- hash of the canonical snapshot (evidence of what was reviewed)
  created_by_user_id     uuid        NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_versions PRIMARY KEY (id),
  CONSTRAINT uq_campaign_versions_number UNIQUE (campaign_id, version_number),
  CONSTRAINT uq_campaign_versions_id_campaign UNIQUE (id, campaign_id),
  CONSTRAINT fk_campaign_versions_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_versions_category_id FOREIGN KEY (category_id) REFERENCES app.campaign_categories (id),
  CONSTRAINT fk_campaign_versions_created_by FOREIGN KEY (created_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_versions_change_kind CHECK (change_kind IN ('SUBMISSION', 'RESUBMISSION', 'MATERIAL_EDIT', 'MINOR_EDIT')),
  CONSTRAINT ck_campaign_versions_material CHECK (
    material_change_types <@ ARRAY['BENEFICIARY', 'PAYOUT_DESTINATION', 'GOAL_INCREASE', 'GOAL_CURRENCY', 'CATEGORY',
                                   'STORY_REWRITE', 'SUPPORTING_DOCUMENTS', 'ORG_REPRESENTATIVE']::text[]
    AND ((change_kind = 'MATERIAL_EDIT') = (cardinality(material_change_types) > 0))),
  CONSTRAINT ck_campaign_versions_sha256 CHECK (octet_length(content_sha256) = 32),
  CONSTRAINT ck_campaign_versions_number CHECK (version_number >= 1)
);
CREATE TRIGGER trg_campaign_versions_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_versions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaign_versions_no_truncate BEFORE TRUNCATE ON app.campaign_versions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

ALTER TABLE app.campaigns ADD CONSTRAINT fk_campaigns_approved_version
  FOREIGN KEY (approved_version_id, id) REFERENCES app.campaign_versions (id, campaign_id);

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_goals — append-only goal versions. The goal currency is the campaign's goal_currency
-- (exactly one per campaign); the current goal is the highest goal_version. Classification: C0 once
-- published. Retention: FINANCIAL.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_goals (
  id                 uuid        NOT NULL,
  campaign_id        uuid        NOT NULL,
  goal_version       integer     NOT NULL,
  amount_minor       bigint      NOT NULL,
  currency           char(3)     NOT NULL,
  reason             text,
  created_by_user_id uuid        NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_goals PRIMARY KEY (id),
  CONSTRAINT uq_campaign_goals_version UNIQUE (campaign_id, goal_version),
  CONSTRAINT fk_campaign_goals_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_goals_currency FOREIGN KEY (currency) REFERENCES app.currencies (code),
  CONSTRAINT fk_campaign_goals_created_by FOREIGN KEY (created_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_goals_amount_positive CHECK (amount_minor > 0),
  CONSTRAINT ck_campaign_goals_version CHECK (goal_version >= 1)
);
CREATE TRIGGER trg_campaign_goals_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_goals
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaign_goals_no_truncate BEFORE TRUNCATE ON app.campaign_goals
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION app.campaign_goals_currency_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_currency char(3);
BEGIN
  SELECT goal_currency INTO v_currency FROM app.campaigns WHERE id = NEW.campaign_id;
  IF NEW.currency <> v_currency THEN
    RAISE EXCEPTION 'goal currency % does not match campaign goal currency %', NEW.currency, v_currency
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_goals_currency_check BEFORE INSERT ON app.campaign_goals
  FOR EACH ROW EXECUTE FUNCTION app.campaign_goals_currency_check();

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_beneficiaries — campaign ↔ beneficiary link (beneficiaries module owns the beneficiary).
-- MVP: exactly one beneficiary per campaign, the primary (CHECK is_primary); history is kept by unlinking
-- (a beneficiary change is a material edit). Classification: C2. Retention: FINANCIAL.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_beneficiaries (
  id                 uuid        NOT NULL,
  campaign_id        uuid        NOT NULL,
  beneficiary_id     uuid        NOT NULL,
  is_primary         boolean     NOT NULL DEFAULT true,
  linked_by_user_id  uuid        NOT NULL,
  linked_at          timestamptz NOT NULL,
  unlinked_at        timestamptz,
  unlinked_by_user_id uuid,
  unlink_reason      text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_beneficiaries PRIMARY KEY (id),
  CONSTRAINT fk_campaign_beneficiaries_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_beneficiaries_beneficiary_id FOREIGN KEY (beneficiary_id) REFERENCES app.beneficiaries (id),
  CONSTRAINT fk_campaign_beneficiaries_linked_by FOREIGN KEY (linked_by_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_beneficiaries_unlinked_by FOREIGN KEY (unlinked_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_beneficiaries_mvp_primary_only CHECK (is_primary),
  CONSTRAINT ck_campaign_beneficiaries_unlinked CHECK ((unlinked_at IS NULL) = (unlink_reason IS NULL))
);
CREATE UNIQUE INDEX uq_campaign_beneficiaries_one_primary ON app.campaign_beneficiaries (campaign_id)
  WHERE is_primary AND unlinked_at IS NULL;
CREATE INDEX ix_campaign_beneficiaries_beneficiary_id ON app.campaign_beneficiaries (beneficiary_id);
CREATE TRIGGER trg_campaign_beneficiaries_set_updated_at BEFORE UPDATE ON app.campaign_beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_beneficiaries_cols BEFORE UPDATE ON app.campaign_beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('unlinked_at', 'unlinked_by_user_id', 'unlink_reason');
CREATE TRIGGER trg_campaign_beneficiaries_no_delete BEFORE DELETE ON app.campaign_beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- Lifecycle rules the transition table alone cannot express (PRODUCT §6.2/§6.3, ADR-010):
--  * FROZEN → COMPLETED only if the campaign had completed before it was frozen; conversely a campaign that
--    had completed can never return to ACTIVE or SUSPENDED through a freeze.
--  * goal_currency may change only in a DRAFT that has never been submitted (no donations are possible
--    before publication, and after submission a currency change would invalidate the review).
--  * DRAFT → SUBMITTED requires a goal and a linked primary beneficiary (BENEFICIARY_DECLARED gate).
--  * goal currency must stay among accepted_currencies, which must all be registered currencies.
-- -----------------------------------------------------------------------------------------------------
CREATE FUNCTION app.campaigns_lifecycle_rules() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  c text;
BEGIN
  FOREACH c IN ARRAY NEW.accepted_currencies LOOP
    IF NOT EXISTS (SELECT 1 FROM app.currencies WHERE code = c) THEN
      RAISE EXCEPTION 'accepted currency % is not a registered currency', c USING ERRCODE = 'foreign_key_violation';
    END IF;
  END LOOP;
  IF TG_OP = 'INSERT' THEN
    RETURN NEW;
  END IF;
  IF NEW.goal_currency <> OLD.goal_currency AND NOT (OLD.status = 'DRAFT' AND OLD.submitted_at IS NULL) THEN
    RAISE EXCEPTION 'goal currency of campaign % cannot change after submission', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.status IS DISTINCT FROM OLD.status THEN
    IF OLD.status = 'FROZEN' AND NEW.status = 'COMPLETED' AND OLD.completed_at IS NULL THEN
      RAISE EXCEPTION 'FROZEN -> COMPLETED only for a campaign that was COMPLETED before the freeze' USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status = 'FROZEN' AND NEW.status IN ('ACTIVE', 'SUSPENDED') AND OLD.completed_at IS NOT NULL THEN
      RAISE EXCEPTION 'a completed campaign returns to COMPLETED after a freeze, not %', NEW.status USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.status = 'SUBMITTED' THEN
      IF NOT EXISTS (SELECT 1 FROM app.campaign_goals g WHERE g.campaign_id = NEW.id) THEN
        RAISE EXCEPTION 'campaign % cannot be submitted without a goal', NEW.id USING ERRCODE = 'check_violation';
      END IF;
      IF NOT EXISTS (SELECT 1 FROM app.campaign_beneficiaries b
                      WHERE b.campaign_id = NEW.id AND b.is_primary AND b.unlinked_at IS NULL) THEN
        RAISE EXCEPTION 'campaign % cannot be submitted without a primary beneficiary', NEW.id USING ERRCODE = 'check_violation';
      END IF;
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaigns_lifecycle_rules BEFORE INSERT OR UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.campaigns_lifecycle_rules();

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_status_history — append-only; one row per campaigns version that changed status
-- (including creation, from_status = ''). Staff transitions need justification; leaving FROZEN for
-- ACTIVE / COMPLETED / CANCELLED needs a second approver distinct from the actor (unfreeze maker-checker).
-- Classification: C2. Retention: FINANCIAL/AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_status_history (
  id               uuid        NOT NULL,
  campaign_id      uuid        NOT NULL,
  campaign_version integer     NOT NULL,                   -- campaigns.version after the change
  from_status      text        NOT NULL,
  to_status        text        NOT NULL,
  actor_type       text        NOT NULL,
  actor_id         uuid,
  actor_role       text,
  approved_by      uuid,                                   -- checker for maker-checker transitions
  reason_code      text        NOT NULL,
  justification    text,
  request_id       text,
  ledger_transaction_id uuid,                              -- freeze/unfreeze journal (ledger.ledger_transactions id; no FK)
  risk_hold_id     uuid,                                   -- risk.holds id placed/released with the transition (no FK)
  occurred_at      timestamptz NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_status_history PRIMARY KEY (id),
  CONSTRAINT uq_campaign_status_history_version UNIQUE (campaign_id, campaign_version),
  CONSTRAINT fk_campaign_status_history_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_status_history_actor_id FOREIGN KEY (actor_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_status_history_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_status_history_actor_type CHECK (actor_type IN ('user', 'staff', 'system')),
  CONSTRAINT ck_campaign_status_history_actor_id CHECK (actor_type = 'system' OR actor_id IS NOT NULL),
  CONSTRAINT ck_campaign_status_history_staff_justification CHECK (actor_type <> 'staff' OR justification IS NOT NULL),
  CONSTRAINT ck_campaign_status_history_distinct CHECK (approved_by IS NULL OR approved_by IS DISTINCT FROM actor_id),
  CONSTRAINT ck_campaign_status_history_unfreeze_checker CHECK (
    from_status <> 'FROZEN' OR to_status NOT IN ('ACTIVE', 'COMPLETED', 'CANCELLED') OR approved_by IS NOT NULL),
  CONSTRAINT ck_campaign_status_history_change CHECK (from_status <> to_status)
);
CREATE INDEX ix_campaign_status_history_campaign ON app.campaign_status_history (campaign_id, occurred_at);
CREATE TRIGGER trg_campaign_status_history_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_status_history
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaign_status_history_no_truncate BEFORE TRUNCATE ON app.campaign_status_history
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Deferred: every status change (and the creation) has its history row in the same transaction, matching
-- the campaign version that the change produced.
CREATE FUNCTION app.campaigns_require_status_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_from text := CASE WHEN TG_OP = 'INSERT' THEN '' ELSE OLD.status END;
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.status = OLD.status THEN
    RETURN NULL;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM app.campaign_status_history h
                  WHERE h.campaign_id = NEW.id AND h.campaign_version = NEW.version
                    AND h.from_status = v_from AND h.to_status = NEW.status) THEN
    RAISE EXCEPTION 'campaign % status change "%" -> "%" (version %) has no campaign_status_history row',
      NEW.id, v_from, NEW.status, NEW.version USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_campaigns_require_status_history AFTER INSERT OR UPDATE OF status ON app.campaigns
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.campaigns_require_status_history();

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_media — campaign images (public media bucket only, enforced by composite FK).
-- Classification: C1 before publication, C0 after. Retention: OPERATIONAL (removed media are soft-removed).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_media (
  id                  uuid        NOT NULL,
  campaign_id         uuid        NOT NULL,
  stored_object_id    uuid        NOT NULL,
  bucket_class        text        NOT NULL DEFAULT 'PUBLIC_MEDIA',
  media_kind          text        NOT NULL,
  alt_text            text,
  sort_order          integer     NOT NULL DEFAULT 0,
  depicts_minor       boolean     NOT NULL DEFAULT false,  -- triggers PRIVACY_REVIEW; guardian consent required
  guardian_consent_ref uuid,                               -- kyc.consents id. NO FK by design.
  status              text        NOT NULL DEFAULT 'PENDING_SCAN',
  removed_at          timestamptz,
  removed_by_user_id  uuid,
  removal_reason      text,
  created_by_user_id  uuid        NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_media PRIMARY KEY (id),
  CONSTRAINT uq_campaign_media_object UNIQUE (campaign_id, stored_object_id),
  CONSTRAINT fk_campaign_media_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_media_stored_object FOREIGN KEY (stored_object_id, bucket_class) REFERENCES app.stored_objects (id, bucket_class),
  CONSTRAINT fk_campaign_media_removed_by FOREIGN KEY (removed_by_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_media_created_by FOREIGN KEY (created_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_media_bucket_class CHECK (bucket_class = 'PUBLIC_MEDIA'),
  CONSTRAINT ck_campaign_media_kind CHECK (media_kind IN ('COVER_IMAGE', 'GALLERY_IMAGE')),
  CONSTRAINT ck_campaign_media_status CHECK (status IN ('PENDING_SCAN', 'READY', 'REJECTED', 'REMOVED')),
  CONSTRAINT ck_campaign_media_removed CHECK ((status = 'REMOVED') = (removed_at IS NOT NULL)),
  CONSTRAINT ck_campaign_media_minor_consent CHECK (NOT depicts_minor OR status NOT IN ('READY') OR guardian_consent_ref IS NOT NULL),
  CONSTRAINT ck_campaign_media_alt_text CHECK (alt_text IS NULL OR length(alt_text) <= 300)
);
CREATE UNIQUE INDEX uq_campaign_media_one_cover ON app.campaign_media (campaign_id)
  WHERE media_kind = 'COVER_IMAGE' AND status <> 'REMOVED';
CREATE TRIGGER trg_campaign_media_set_updated_at BEFORE UPDATE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_media_immutable_cols BEFORE UPDATE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('campaign_id', 'stored_object_id', 'created_by_user_id', 'created_at');
CREATE TRIGGER trg_campaign_media_no_delete BEFORE DELETE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_updates — owner posts to donors. Classification: C0 when PUBLISHED. Retention: OPERATIONAL.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_updates (
  id                uuid        NOT NULL,
  campaign_id       uuid        NOT NULL,
  author_user_id    uuid        NOT NULL,
  title             text        NOT NULL,
  body              text        NOT NULL,                  -- sanitised rich-text subset
  status            text        NOT NULL DEFAULT 'DRAFT',
  published_at      timestamptz,
  hidden_at         timestamptz,
  hidden_by_user_id uuid,
  hidden_reason     text,
  deleted_at        timestamptz,
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_updates PRIMARY KEY (id),
  CONSTRAINT fk_campaign_updates_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_updates_author FOREIGN KEY (author_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_updates_hidden_by FOREIGN KEY (hidden_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_updates_status CHECK (status IN ('DRAFT', 'PUBLISHED', 'HIDDEN', 'DELETED')),
  CONSTRAINT ck_campaign_updates_published CHECK (status = 'DRAFT' OR status = 'DELETED' OR published_at IS NOT NULL),
  CONSTRAINT ck_campaign_updates_hidden CHECK ((status = 'HIDDEN') = (hidden_at IS NOT NULL AND hidden_reason IS NOT NULL)),
  CONSTRAINT ck_campaign_updates_deleted CHECK ((status = 'DELETED') = (deleted_at IS NOT NULL)),
  CONSTRAINT ck_campaign_updates_title CHECK (length(title) BETWEEN 1 AND 150)
);
CREATE INDEX ix_campaign_updates_campaign ON app.campaign_updates (campaign_id, published_at) WHERE status = 'PUBLISHED';
CREATE TRIGGER trg_campaign_updates_bump_version BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaign_updates_set_updated_at BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_updates_immutable_cols BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('campaign_id', 'author_user_id', 'created_at');
CREATE TRIGGER trg_campaign_updates_no_delete BEFORE DELETE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_reviews — one review per (re)submission, pinned to the submitted campaign version and to a
-- PUBLISHED review policy version. Check results are recorded during the review (check_results jsonb) and
-- frozen with the decision. HIGH tier APPROVE needs a COMPLIANCE sign-off by a different person (four eyes).
-- Reviewer conflict-of-interest attestation is required to claim. Classification: C2 (no C3 in notes).
-- Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_reviews (
  id                       uuid        NOT NULL,
  campaign_id              uuid        NOT NULL,
  campaign_version_id      uuid        NOT NULL,
  review_policy_id         uuid        NOT NULL,
  review_kind              text        NOT NULL,
  risk_tier_at_submission  text        NOT NULL,
  status                   text        NOT NULL DEFAULT 'QUEUED',
  claimed_by               uuid,
  claimed_at               timestamptz,
  coi_attested_at          timestamptz,
  check_results            jsonb       NOT NULL DEFAULT '[]',  -- [{check_code, result PASS|FLAG|FAIL|N_A, performed_by, evidence_record_ids, acknowledged_by, notes}]
  escalation_case_ref      uuid,                              -- compliance.compliance_cases id (no FK)
  outcome                  text,
  reason_code              text,
  justification            text,
  decided_by               uuid,
  decided_at               timestamptz,
  compliance_signoff_by    uuid,
  compliance_signoff_at    timestamptz,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_reviews PRIMARY KEY (id),
  CONSTRAINT fk_campaign_reviews_version FOREIGN KEY (campaign_version_id, campaign_id) REFERENCES app.campaign_versions (id, campaign_id),
  CONSTRAINT fk_campaign_reviews_policy FOREIGN KEY (review_policy_id) REFERENCES app.campaign_review_policies (id),
  CONSTRAINT fk_campaign_reviews_claimed_by FOREIGN KEY (claimed_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_reviews_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_reviews_compliance_signoff_by FOREIGN KEY (compliance_signoff_by) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_reviews_kind CHECK (review_kind IN ('INITIAL', 'RE_REVIEW')),
  CONSTRAINT ck_campaign_reviews_risk_tier CHECK (risk_tier_at_submission IN ('STANDARD', 'ELEVATED', 'HIGH')),
  CONSTRAINT ck_campaign_reviews_status CHECK (status IN ('QUEUED', 'CLAIMED', 'ESCALATED', 'DECIDED', 'CANCELLED')),
  CONSTRAINT ck_campaign_reviews_claim CHECK (
    (claimed_by IS NULL) = (claimed_at IS NULL) AND (claimed_by IS NULL OR coi_attested_at IS NOT NULL)
    AND (status = 'QUEUED' OR status = 'CANCELLED' OR claimed_by IS NOT NULL)),
  CONSTRAINT ck_campaign_reviews_check_results CHECK (jsonb_typeof(check_results) = 'array'),
  CONSTRAINT ck_campaign_reviews_outcome CHECK (outcome IS NULL OR outcome IN ('APPROVE', 'REQUEST_CHANGES', 'REJECT')),
  CONSTRAINT ck_campaign_reviews_decided CHECK (
    (status = 'DECIDED') = (outcome IS NOT NULL AND decided_by IS NOT NULL AND decided_at IS NOT NULL
                            AND reason_code IS NOT NULL AND justification IS NOT NULL)),
  CONSTRAINT ck_campaign_reviews_escalated CHECK (status <> 'ESCALATED' OR escalation_case_ref IS NOT NULL),
  CONSTRAINT ck_campaign_reviews_high_four_eyes CHECK (
    risk_tier_at_submission <> 'HIGH' OR outcome IS DISTINCT FROM 'APPROVE'
    OR (compliance_signoff_by IS NOT NULL AND compliance_signoff_at IS NOT NULL AND compliance_signoff_by <> decided_by)),
  CONSTRAINT ck_campaign_reviews_no_failed_check_approval CHECK (
    outcome IS DISTINCT FROM 'APPROVE' OR NOT jsonb_path_exists(check_results, '$[*] ? (@.result == "FAIL")'))
);
CREATE UNIQUE INDEX uq_campaign_reviews_open ON app.campaign_reviews (campaign_id) WHERE status IN ('QUEUED', 'CLAIMED', 'ESCALATED');
CREATE INDEX ix_campaign_reviews_queue ON app.campaign_reviews (risk_tier_at_submission, created_at) WHERE status = 'QUEUED';
CREATE INDEX ix_campaign_reviews_claimed_by ON app.campaign_reviews (claimed_by) WHERE status IN ('CLAIMED', 'ESCALATED');
CREATE TRIGGER trg_campaign_reviews_set_updated_at BEFORE UPDATE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_reviews_immutable_cols BEFORE UPDATE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('campaign_id', 'campaign_version_id', 'review_policy_id',
                                                         'review_kind', 'risk_tier_at_submission', 'created_at');
CREATE TRIGGER trg_campaign_reviews_no_delete BEFORE DELETE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION app.campaign_reviews_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NOT EXISTS (SELECT 1 FROM app.campaign_review_policies p WHERE p.id = NEW.review_policy_id AND p.status = 'PUBLISHED') THEN
      RAISE EXCEPTION 'review must be pinned to a PUBLISHED review policy' USING ERRCODE = 'integrity_constraint_violation';
    END IF;
    RETURN NEW;
  END IF;
  IF OLD.status IN ('DECIDED', 'CANCELLED') THEN
    RAISE EXCEPTION 'campaign review % is final (%)', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_reviews_guard BEFORE INSERT OR UPDATE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.campaign_reviews_guard();

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_reports — abuse reports from the public (guests allowed). Reporter identity is protected
-- (never shown to the campaign owner). Classification: C2. Retention: CASE.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_reports (
  id                    uuid        NOT NULL,
  campaign_id           uuid        NOT NULL,
  reporter_user_id      uuid,
  reporter_contact_hmac bytea,                             -- guest reporter's contact blind index (rate limit, follow-up)
  reason_code           text        NOT NULL,
  details               text,
  status                text        NOT NULL DEFAULT 'OPEN',
  triaged_by            uuid,
  triaged_at            timestamptz,
  resolution_code       text,
  resolved_by           uuid,
  resolved_at           timestamptz,
  duplicate_of_id       uuid,
  compliance_case_ref   uuid,                              -- compliance case id (no FK)
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_reports PRIMARY KEY (id),
  CONSTRAINT fk_campaign_reports_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_reports_reporter FOREIGN KEY (reporter_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_reports_triaged_by FOREIGN KEY (triaged_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_reports_resolved_by FOREIGN KEY (resolved_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_reports_duplicate_of FOREIGN KEY (duplicate_of_id) REFERENCES app.campaign_reports (id),
  CONSTRAINT ck_campaign_reports_reason CHECK (reason_code IN ('FRAUD', 'MISLEADING', 'PROHIBITED_PURPOSE', 'PRIVACY_VIOLATION',
                                                               'MINOR_SAFETY', 'HATE_OR_VIOLENCE', 'IMPERSONATION', 'SPAM', 'OTHER')),
  CONSTRAINT ck_campaign_reports_details CHECK (details IS NULL OR length(details) <= 4000),
  CONSTRAINT ck_campaign_reports_status CHECK (status IN ('OPEN', 'TRIAGED', 'ACTIONED', 'DISMISSED', 'DUPLICATE')),
  CONSTRAINT ck_campaign_reports_resolved CHECK (
    (status IN ('ACTIONED', 'DISMISSED', 'DUPLICATE')) = (resolved_by IS NOT NULL AND resolved_at IS NOT NULL AND resolution_code IS NOT NULL)),
  CONSTRAINT ck_campaign_reports_duplicate CHECK ((status = 'DUPLICATE') = (duplicate_of_id IS NOT NULL)),
  CONSTRAINT ck_campaign_reports_contact_hmac CHECK (reporter_contact_hmac IS NULL OR octet_length(reporter_contact_hmac) = 32)
);
CREATE INDEX ix_campaign_reports_campaign ON app.campaign_reports (campaign_id, created_at);
CREATE INDEX ix_campaign_reports_open ON app.campaign_reports (created_at) WHERE status IN ('OPEN', 'TRIAGED');
CREATE TRIGGER trg_campaign_reports_set_updated_at BEFORE UPDATE ON app.campaign_reports
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_reports_immutable_cols BEFORE UPDATE ON app.campaign_reports
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('campaign_id', 'reporter_user_id', 'reporter_contact_hmac',
                                                         'reason_code', 'details', 'created_at');
CREATE TRIGGER trg_campaign_reports_no_delete BEFORE DELETE ON app.campaign_reports
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.campaign_moderation_actions — append-only staff actions on a campaign that are not themselves
-- lifecycle transitions, plus the maker-checker home for leaving FROZEN (baseline I-9):
--   UNFREEZE_REQUEST / CANCEL_FROM_FROZEN_REQUEST   maker row (requested_by = actor)
--   UNFREEZE_APPROVE / CANCEL_FROM_FROZEN_APPROVE   checker row: names the request, copies requested_by,
--                                                    approved_by = actor, CHECK approved_by <> requested_by
-- The FROZEN -> ACTIVE | COMPLETED | CANCELLED history row must reference the matching APPROVE row.
-- Classification: C2. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_moderation_actions (
  id                 uuid        NOT NULL,
  campaign_id        uuid        NOT NULL,
  action             text        NOT NULL,
  target_type        text        NOT NULL,
  target_id          uuid        NOT NULL,
  campaign_report_id uuid,
  actor_id           uuid        NOT NULL,
  actor_role         text        NOT NULL,
  request_action_id  uuid,                                 -- APPROVE rows: the REQUEST row approved
  requested_by       uuid,                                 -- maker (maker-checker actions)
  approved_by        uuid,                                 -- checker (APPROVE rows)
  expires_at         timestamptz,                          -- REQUEST rows: 72 h (operational-controls §3)
  reason_code        text        NOT NULL,
  justification      text        NOT NULL,
  details            jsonb       NOT NULL DEFAULT '{}',    -- redacted before/after (allow-listed fields only)
  occurred_at        timestamptz NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_moderation_actions PRIMARY KEY (id),
  CONSTRAINT uq_campaign_moderation_actions_request UNIQUE (request_action_id),          -- one decision per request
  CONSTRAINT uq_campaign_moderation_actions_id_campaign UNIQUE (id, campaign_id),
  CONSTRAINT fk_campaign_moderation_actions_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_moderation_actions_report FOREIGN KEY (campaign_report_id) REFERENCES app.campaign_reports (id),
  CONSTRAINT fk_campaign_moderation_actions_actor FOREIGN KEY (actor_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_moderation_actions_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_moderation_actions_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_moderation_actions_request FOREIGN KEY (request_action_id, campaign_id)
    REFERENCES app.campaign_moderation_actions (id, campaign_id),
  CONSTRAINT ck_campaign_moderation_actions_action CHECK (action IN (
    'HIDE_CONTENT', 'UNHIDE_CONTENT', 'REDACT_STORY', 'REMOVE_MEDIA', 'RESTORE_MEDIA', 'HIDE_UPDATE', 'UNHIDE_UPDATE',
    'SET_VISIBILITY', 'FLAG_RE_REVIEW', 'WARN_OWNER', 'LOCK_EDITS', 'UNLOCK_EDITS',
    'UNFREEZE_REQUEST', 'UNFREEZE_APPROVE', 'CANCEL_FROM_FROZEN_REQUEST', 'CANCEL_FROM_FROZEN_APPROVE')),
  CONSTRAINT ck_campaign_moderation_actions_target CHECK (target_type IN ('CAMPAIGN', 'MEDIA', 'UPDATE', 'VERSION')),
  CONSTRAINT ck_campaign_moderation_actions_details CHECK (jsonb_typeof(details) = 'object'),
  CONSTRAINT ck_campaign_moderation_actions_justification CHECK (length(justification) >= 3),
  CONSTRAINT ck_campaign_moderation_actions_request_row CHECK (
    action NOT IN ('UNFREEZE_REQUEST', 'CANCEL_FROM_FROZEN_REQUEST')
    OR (requested_by = actor_id AND approved_by IS NULL AND request_action_id IS NULL AND expires_at IS NOT NULL)),
  CONSTRAINT ck_campaign_moderation_actions_approve_row CHECK (
    action NOT IN ('UNFREEZE_APPROVE', 'CANCEL_FROM_FROZEN_APPROVE')
    OR (request_action_id IS NOT NULL AND requested_by IS NOT NULL AND approved_by = actor_id)),
  CONSTRAINT ck_campaign_moderation_actions_maker_checker CHECK (approved_by IS NULL OR approved_by <> requested_by),
  CONSTRAINT ck_campaign_moderation_actions_plain_row CHECK (
    action IN ('UNFREEZE_REQUEST', 'UNFREEZE_APPROVE', 'CANCEL_FROM_FROZEN_REQUEST', 'CANCEL_FROM_FROZEN_APPROVE')
    OR (request_action_id IS NULL AND approved_by IS NULL))
);
CREATE INDEX ix_campaign_moderation_actions_campaign ON app.campaign_moderation_actions (campaign_id, occurred_at);
CREATE TRIGGER trg_campaign_moderation_actions_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_moderation_actions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaign_moderation_actions_no_truncate BEFORE TRUNCATE ON app.campaign_moderation_actions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- An APPROVE row must answer a REQUEST of the same kind, copy its maker, and come before the request expires.
CREATE FUNCTION app.campaign_moderation_actions_check_approval() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  r app.campaign_moderation_actions%ROWTYPE;
BEGIN
  IF NEW.action NOT IN ('UNFREEZE_APPROVE', 'CANCEL_FROM_FROZEN_APPROVE') THEN
    RETURN NEW;
  END IF;
  SELECT * INTO r FROM app.campaign_moderation_actions WHERE id = NEW.request_action_id;
  IF r.action IS DISTINCT FROM replace(NEW.action, '_APPROVE', '_REQUEST') OR r.requested_by IS DISTINCT FROM NEW.requested_by THEN
    RAISE EXCEPTION 'approval does not match a % request by the same maker', replace(NEW.action, '_APPROVE', '_REQUEST')
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF NEW.occurred_at > r.expires_at THEN
    RAISE EXCEPTION 'moderation request % expired at %', r.id, r.expires_at USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_moderation_actions_check_approval BEFORE INSERT ON app.campaign_moderation_actions
  FOR EACH ROW EXECUTE FUNCTION app.campaign_moderation_actions_check_approval();

-- Leaving FROZEN for ACTIVE / COMPLETED (unfreeze) or CANCELLED must cite the matching approved
-- moderation action of the same campaign, and the history row's actor/approver must be its maker/checker.
ALTER TABLE app.campaign_status_history ADD COLUMN moderation_action_id uuid;
ALTER TABLE app.campaign_status_history ADD CONSTRAINT fk_campaign_status_history_moderation_action
  FOREIGN KEY (moderation_action_id, campaign_id) REFERENCES app.campaign_moderation_actions (id, campaign_id);
CREATE FUNCTION app.campaign_status_history_check_unfreeze() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  a app.campaign_moderation_actions%ROWTYPE;
  v_needed text;
BEGIN
  IF NEW.from_status <> 'FROZEN' OR NEW.to_status NOT IN ('ACTIVE', 'COMPLETED', 'CANCELLED') THEN
    RETURN NEW;
  END IF;
  v_needed := CASE WHEN NEW.to_status = 'CANCELLED' THEN 'CANCEL_FROM_FROZEN_APPROVE' ELSE 'UNFREEZE_APPROVE' END;
  SELECT * INTO a FROM app.campaign_moderation_actions WHERE id = NEW.moderation_action_id;
  IF a.action IS DISTINCT FROM v_needed OR a.requested_by IS DISTINCT FROM NEW.actor_id OR a.approved_by IS DISTINCT FROM NEW.approved_by THEN
    RAISE EXCEPTION 'FROZEN -> % requires an approved % moderation action matching actor and approver', NEW.to_status, v_needed
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF EXISTS (SELECT 1 FROM app.campaign_status_history h WHERE h.moderation_action_id = NEW.moderation_action_id) THEN
    RAISE EXCEPTION 'moderation approval % already used', NEW.moderation_action_id USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_status_history_check_unfreeze BEFORE INSERT ON app.campaign_status_history
  FOR EACH ROW EXECUTE FUNCTION app.campaign_status_history_check_unfreeze();
