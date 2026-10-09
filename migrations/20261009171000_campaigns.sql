-- Stage 6: campaign engine core (ADR-036; docs/stage-6/interface-contracts.md §3; derived from the Stage 2 draft
-- design/sql/0014_campaigns.sql). Owner module: campaigns. Schema: app.
--
-- Tables: campaign_categories, campaign_policies, campaigns, campaign_goals, campaign_versions,
-- campaign_beneficiaries, campaign_reviews, campaign_status_history, campaign_eligibility_evaluations.
--
-- There is deliberately NO raised, total or balance column: money states are derived from the ledger in later
-- stages (CLAUDE.md financial rules 5 and 11). The goal is amount_minor + currency, never a float.
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ---------------------------------------------------------------------------------------------------------
-- Lifecycle machines (ADR-036 §2). The Go edge list in internal/campaigns/lifecycle.go must stay identical
-- (unit test parses this file).
-- ---------------------------------------------------------------------------------------------------------
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('campaign', '',                  'DRAFT'),
  ('campaign', 'DRAFT',             'SUBMITTED'),
  ('campaign', 'DRAFT',             'CANCELLED'),
  ('campaign', 'SUBMITTED',         'UNDER_REVIEW'),
  ('campaign', 'SUBMITTED',         'DRAFT'),
  ('campaign', 'UNDER_REVIEW',      'APPROVED'),
  ('campaign', 'UNDER_REVIEW',      'REJECTED'),
  ('campaign', 'UNDER_REVIEW',      'CHANGES_REQUESTED'),
  ('campaign', 'CHANGES_REQUESTED', 'SUBMITTED'),
  ('campaign', 'CHANGES_REQUESTED', 'CANCELLED'),
  ('campaign', 'APPROVED',          'ACTIVE'),
  ('campaign', 'APPROVED',          'CANCELLED'),
  ('campaign', 'APPROVED',          'SUSPENDED'),
  ('campaign', 'ACTIVE',            'PAUSED'),
  ('campaign', 'ACTIVE',            'SUSPENDED'),
  ('campaign', 'ACTIVE',            'COMPLETED'),
  ('campaign', 'ACTIVE',            'CANCELLED'),
  ('campaign', 'PAUSED',            'ACTIVE'),
  ('campaign', 'PAUSED',            'SUSPENDED'),
  ('campaign', 'PAUSED',            'COMPLETED'),
  ('campaign', 'PAUSED',            'CANCELLED'),
  ('campaign', 'SUSPENDED',         'ACTIVE'),
  ('campaign', 'SUSPENDED',         'APPROVED'),
  ('campaign', 'SUSPENDED',         'CANCELLED'),
  ('campaign', 'REJECTED',          'DRAFT'),
  ('campaign', 'REJECTED',          'UNDER_REVIEW'),
  ('campaign', 'REJECTED',          'ARCHIVED'),
  ('campaign', 'COMPLETED',         'ARCHIVED'),
  ('campaign', 'CANCELLED',         'ARCHIVED'),
  ('campaign_policy', '',           'PROPOSED'),
  ('campaign_policy', 'PROPOSED',   'APPROVED'),
  ('campaign_policy', 'PROPOSED',   'REJECTED'),
  ('campaign_policy', 'APPROVED',   'RETIRED'),
  ('campaign_review', '',           'QUEUED'),
  ('campaign_review', 'QUEUED',     'ASSIGNED'),
  ('campaign_review', 'QUEUED',     'CANCELLED'),
  ('campaign_review', 'IN_REVIEW',  'ASSIGNED'),
  ('campaign_review', 'ASSIGNED',   'IN_REVIEW'),
  ('campaign_review', 'ASSIGNED',   'CANCELLED'),
  ('campaign_review', 'IN_REVIEW',  'DECIDED'),
  ('campaign_review', 'IN_REVIEW',  'CANCELLED');

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_categories — configuration data (PRODUCT §5). Risk tiers are the provisional values of
-- campaign-approval-policy §4 (INTERNAL_POLICY; COMPLIANCE proposes, business owner approves). Codes are
-- immutable; staff with campaign.category.manage may change the presentation fields and active flag only.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_categories (
  id                     uuid        NOT NULL,
  code                   text        NOT NULL,
  slug                   text        NOT NULL,
  name                   text        NOT NULL,
  description            text        NOT NULL,
  display_order          integer     NOT NULL,
  active                 boolean     NOT NULL DEFAULT true,
  default_risk_tier      text        NOT NULL,
  requires_organisation  boolean     NOT NULL DEFAULT false,
  moderation_policy      text        NOT NULL DEFAULT 'STANDARD',
  verification_policy_ref text,
  sensitive              boolean     NOT NULL DEFAULT false,   -- e.g. medical: privacy-sensitive disclosure
  version                integer     NOT NULL DEFAULT 1,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_categories PRIMARY KEY (id),
  CONSTRAINT uq_campaign_categories_code UNIQUE (code),
  CONSTRAINT uq_campaign_categories_slug UNIQUE (slug),
  CONSTRAINT ck_campaign_categories_code CHECK (code ~ '^[A-Z][A-Z_]{1,39}$'),
  CONSTRAINT ck_campaign_categories_slug CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 40),
  CONSTRAINT ck_campaign_categories_name CHECK (length(name) BETWEEN 2 AND 60),
  CONSTRAINT ck_campaign_categories_description CHECK (length(description) BETWEEN 2 AND 500),
  CONSTRAINT ck_campaign_categories_tier CHECK (default_risk_tier IN ('STANDARD', 'ELEVATED', 'HIGH')),
  CONSTRAINT ck_campaign_categories_moderation CHECK (moderation_policy IN ('STANDARD', 'PRE_MODERATE_UPDATES'))
);
CREATE TRIGGER trg_campaign_categories_bump_version BEFORE UPDATE ON app.campaign_categories
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaign_categories_set_updated_at BEFORE UPDATE ON app.campaign_categories
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_categories_keep_created_at BEFORE UPDATE ON app.campaign_categories
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
-- risk tier, organisation requirement and moderation policy change only through a migration (policy change)
CREATE TRIGGER trg_campaign_categories_immutable_cols BEFORE UPDATE ON app.campaign_categories
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'code', 'slug', 'default_risk_tier', 'requires_organisation',
                                                         'moderation_policy', 'verification_policy_ref', 'sensitive');
CREATE TRIGGER trg_campaign_categories_no_delete BEFORE DELETE ON app.campaign_categories
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.campaign_categories (id, code, slug, name, description, display_order, default_risk_tier, requires_organisation,
  moderation_policy, sensitive)
SELECT md5('campaign_category:' || c.code)::uuid, c.code, c.slug, c.name, c.descr, c.ord, c.tier, c.org, c.moder, c.sens
FROM (VALUES
  ('MEDICAL',          'medical',          'Medical',          'Treatment, operations, medication and care costs.',               10, 'HIGH',     false, 'PRE_MODERATE_UPDATES', true),
  ('EDUCATION',        'education',        'Education',        'School fees, uniforms, books and tuition.',                       20, 'STANDARD', false, 'STANDARD',             false),
  ('EMERGENCY',        'emergency',        'Emergency',        'Urgent needs after an unexpected event.',                         30, 'ELEVATED', false, 'STANDARD',             false),
  ('FUNERAL',          'funeral',          'Funeral',          'Funeral and burial costs.',                                       40, 'ELEVATED', false, 'STANDARD',             false),
  ('DISASTER_RELIEF',  'disaster-relief',  'Disaster relief',  'Relief after floods, droughts, fires and other disasters.',        50, 'ELEVATED', false, 'STANDARD',             false),
  ('COMMUNITY',        'community',        'Community',        'Boreholes, schools, clinics and local projects.',                 60, 'ELEVATED', false, 'STANDARD',             false),
  ('CHARITY',          'charity',          'Charity',          'Causes run by a verified organisation.',                          70, 'ELEVATED', true,  'STANDARD',             false),
  ('RELIGIOUS',        'religious',        'Religious',        'Churches and faith-based organisations.',                         80, 'ELEVATED', true,  'STANDARD',             false),
  ('BUSINESS_SUPPORT', 'business-support', 'Business support', 'Donations to help a small business recover or start (no equity, loans or rewards).', 90, 'HIGH', false, 'STANDARD', false),
  ('ANIMAL_WELFARE',   'animal-welfare',   'Animal welfare',   'Care for animals and wildlife.',                                  100, 'STANDARD', false, 'STANDARD',            false),
  ('OTHER',            'other',            'Other',            'Other permitted causes; reviewed with extra care.',               110, 'HIGH',     false, 'STANDARD',            false)
) AS c(code, slug, name, descr, ord, tier, org, moder, sens);

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_policies — versioned campaign policy (gates, content limits, goal limits, review tiers,
-- material-edit rules). Maker-checker: a different staff member approves (campaign.review_policy.request /
-- .approve). Approved versions are immutable; decisions pin the version they used. All values are
-- INTERNAL_POLICY configuration, not legal requirements.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_policies (
  id             uuid        NOT NULL,
  policy_version text        NOT NULL,
  rules          jsonb       NOT NULL,
  status         text        NOT NULL,
  proposed_by    uuid        NOT NULL REFERENCES app.users (id),
  approved_by    uuid        REFERENCES app.users (id),
  approved_at    timestamptz,
  change_reason  text        NOT NULL,
  decision_ref   text,
  effective_from timestamptz NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  version        integer     NOT NULL DEFAULT 1,
  CONSTRAINT pk_campaign_policies PRIMARY KEY (id),
  CONSTRAINT uq_campaign_policies_version UNIQUE (policy_version),
  CONSTRAINT ck_campaign_policies_status CHECK (status IN ('PROPOSED', 'APPROVED', 'REJECTED', 'RETIRED')),
  CONSTRAINT ck_campaign_policies_rules CHECK (jsonb_typeof(rules) = 'object'),
  CONSTRAINT ck_campaign_policies_reason CHECK (length(change_reason) BETWEEN 10 AND 1000),
  -- four eyes, except the seeded baseline whose approval is the ADR (decision_ref)
  CONSTRAINT ck_campaign_policies_four_eyes CHECK (status <> 'APPROVED' OR approved_at IS NOT NULL),
  CONSTRAINT ck_campaign_policies_checker CHECK (approved_by IS NULL OR approved_by <> proposed_by)
);
CREATE UNIQUE INDEX uq_campaign_policies_one_approved ON app.campaign_policies ((true)) WHERE status = 'APPROVED';
CREATE TRIGGER trg_campaign_policies_guard_status BEFORE INSERT OR UPDATE OF status ON app.campaign_policies
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('campaign_policy');
CREATE TRIGGER trg_campaign_policies_bump_version BEFORE UPDATE ON app.campaign_policies
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaign_policies_immutable_cols BEFORE UPDATE ON app.campaign_policies
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'policy_version', 'rules', 'proposed_by', 'change_reason',
                                                         'decision_ref', 'effective_from', 'created_at');
CREATE TRIGGER trg_campaign_policies_no_delete BEFORE DELETE ON app.campaign_policies
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.campaign_policies (id, policy_version, rules, status, proposed_by, change_reason, decision_ref, effective_from)
VALUES ('0192f000-0000-7000-8000-000000000601', 'campaign-v1', '{
  "gates": {"create_draft_min_level": "BASIC_VERIFIED", "submit_min_level": "IDENTITY_VERIFIED",
            "organisation_min_level": "ORG_KYB_VERIFIED", "organisation_submit_permission": "org.campaign.submit"},
  "content": {"title": [10, 120], "summary": [20, 300], "story": [100, 20000], "update_title": [3, 120], "update_body": [10, 10000]},
  "goals": {"USD": {"min_minor": 1000, "max_minor": 100000000}, "ZWG": {"min_minor": 1000, "max_minor": 2500000000}},
  "max_resubmissions": 3,
  "owner_pause_allowed": true,
  "require_cover_image": true,
  "max_gallery_images": 10,
  "material_edit": {"goal_increase_ratio_percent": 20},
  "tiers": {"STANDARD": {"second_approval": false}, "ELEVATED": {"second_approval": false}, "HIGH": {"second_approval": true}},
  "beneficiary": {"minor_disclosure": "NONE", "third_party_disclosure_requires_consent": true},
  "screening": {"provider": null, "status": "SCREENING_PROVIDER_NOT_SELECTED"}
}', 'PROPOSED', '00000000-0000-0000-0000-000000000001',
 'Stage 6 baseline campaign policy (internal policy values, not legal requirements; LR-021, LR-046 remain open)', 'ADR-036', now());
UPDATE app.campaign_policies SET status = 'APPROVED', approved_at = now() WHERE policy_version = 'campaign-v1';

-- ---------------------------------------------------------------------------------------------------------
-- app.campaigns — the aggregate (owner, content working copy, goal, lifecycle). The public page renders
-- approved_version_id only. Classification: C1/C2 (C0 once published). Retention: FINANCIAL.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaigns (
  id                    uuid        NOT NULL,
  public_code           text        NOT NULL,
  slug                  text        NOT NULL,
  owner_user_id         uuid,
  owner_organisation_id uuid,
  created_by            uuid        NOT NULL,
  category_code         text        NOT NULL,
  title                 text        NOT NULL,
  summary               text        NOT NULL,
  story                 text        NOT NULL DEFAULT '',
  goal_amount_minor     bigint      NOT NULL,
  goal_currency         char(3)     NOT NULL,
  status                text        NOT NULL,
  visibility            text        NOT NULL DEFAULT 'PUBLIC',   -- owner's listing choice; exposure also needs a live status
  risk_tier             text        NOT NULL,
  policy_version        text        NOT NULL,
  fundraising_basis     text,
  approved_version_id   uuid,
  pending_version_id    uuid,
  re_review_required    boolean     NOT NULL DEFAULT false,
  re_review_reason      text,
  resubmission_count    integer     NOT NULL DEFAULT 0,
  submitted_at          timestamptz,
  approved_at           timestamptz,
  published_at          timestamptz,
  paused_at             timestamptz,
  suspended_at          timestamptz,
  completed_at          timestamptz,
  completion_reason     text,
  cancelled_at          timestamptz,
  archived_at           timestamptz,
  version               integer     NOT NULL DEFAULT 1,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaigns PRIMARY KEY (id),
  CONSTRAINT uq_campaigns_public_code UNIQUE (public_code),
  CONSTRAINT uq_campaigns_slug UNIQUE (slug),
  CONSTRAINT fk_campaigns_owner_user_id FOREIGN KEY (owner_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaigns_owner_organisation_id FOREIGN KEY (owner_organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_campaigns_created_by FOREIGN KEY (created_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaigns_category FOREIGN KEY (category_code) REFERENCES app.campaign_categories (code),
  CONSTRAINT fk_campaigns_goal_currency FOREIGN KEY (goal_currency) REFERENCES app.currencies (code),
  CONSTRAINT fk_campaigns_policy FOREIGN KEY (policy_version) REFERENCES app.campaign_policies (policy_version),
  CONSTRAINT ck_campaigns_exactly_one_owner CHECK (num_nonnulls(owner_user_id, owner_organisation_id) = 1),
  CONSTRAINT ck_campaigns_public_code CHECK (public_code ~ '^[0-9A-HJKMNP-TV-Z]{10}$'),
  CONSTRAINT ck_campaigns_slug CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 96),
  CONSTRAINT ck_campaigns_title CHECK (length(title) BETWEEN 3 AND 120),
  CONSTRAINT ck_campaigns_summary CHECK (length(summary) <= 300),
  CONSTRAINT ck_campaigns_story CHECK (length(story) <= 20000),
  CONSTRAINT ck_campaigns_goal_positive CHECK (goal_amount_minor > 0),
  CONSTRAINT ck_campaigns_status CHECK (status IN ('DRAFT', 'SUBMITTED', 'UNDER_REVIEW', 'CHANGES_REQUESTED', 'APPROVED', 'ACTIVE',
             'PAUSED', 'SUSPENDED', 'REJECTED', 'COMPLETED', 'CANCELLED', 'ARCHIVED')),
  -- PUBLIC = listed and searchable; UNLISTED = reachable by its link only. Either way a campaign is exposed only
  -- while ACTIVE, PAUSED or COMPLETED (an APPROVED preview is owner-authenticated, never public).
  CONSTRAINT ck_campaigns_visibility CHECK (visibility IN ('PUBLIC', 'UNLISTED')),
  CONSTRAINT ck_campaigns_risk_tier CHECK (risk_tier IN ('STANDARD', 'ELEVATED', 'HIGH')),
  CONSTRAINT ck_campaigns_basis CHECK (fundraising_basis IS NULL OR fundraising_basis IN
             ('SELF_FUNDRAISING', 'INDIVIDUAL_FOR_OTHERS', 'ORGANISATION_OWN_CAUSE')),
  CONSTRAINT ck_campaigns_submitted CHECK (status IN ('DRAFT', 'CANCELLED', 'ARCHIVED') OR (submitted_at IS NOT NULL AND fundraising_basis IS NOT NULL)),
  CONSTRAINT ck_campaigns_approved CHECK (status NOT IN ('APPROVED', 'ACTIVE', 'PAUSED', 'COMPLETED')
             OR (approved_at IS NOT NULL AND approved_version_id IS NOT NULL)),
  CONSTRAINT ck_campaigns_published CHECK (status NOT IN ('ACTIVE', 'PAUSED', 'COMPLETED') OR published_at IS NOT NULL),
  CONSTRAINT ck_campaigns_paused CHECK ((status = 'PAUSED') = (paused_at IS NOT NULL)),
  CONSTRAINT ck_campaigns_suspended CHECK ((status = 'SUSPENDED') = (suspended_at IS NOT NULL)),
  CONSTRAINT ck_campaigns_completed CHECK ((completed_at IS NULL) = (completion_reason IS NULL)
             AND (status <> 'COMPLETED' OR completed_at IS NOT NULL)),
  CONSTRAINT ck_campaigns_completion_reason CHECK (completion_reason IS NULL OR completion_reason IN
             ('GOAL_REACHED', 'ORGANISER_COMPLETED', 'ADMIN_COMPLETED', 'EXPIRED', 'OTHER')),
  CONSTRAINT ck_campaigns_cancelled CHECK (status NOT IN ('CANCELLED') OR cancelled_at IS NOT NULL),
  CONSTRAINT ck_campaigns_archived CHECK ((status = 'ARCHIVED') = (archived_at IS NOT NULL)),
  CONSTRAINT ck_campaigns_re_review CHECK (NOT re_review_required OR re_review_reason IS NOT NULL),
  CONSTRAINT ck_campaigns_resubmissions CHECK (resubmission_count >= 0)
);
CREATE INDEX ix_campaigns_owner_user_id ON app.campaigns (owner_user_id, created_at) WHERE owner_user_id IS NOT NULL;
CREATE INDEX ix_campaigns_owner_organisation_id ON app.campaigns (owner_organisation_id, created_at) WHERE owner_organisation_id IS NOT NULL;
CREATE INDEX ix_campaigns_review_queue ON app.campaigns (status, submitted_at, id) WHERE status IN ('SUBMITTED', 'UNDER_REVIEW');
CREATE INDEX ix_campaigns_public_published ON app.campaigns (published_at DESC, id DESC)
  WHERE visibility = 'PUBLIC' AND status IN ('ACTIVE', 'PAUSED', 'COMPLETED');
CREATE INDEX ix_campaigns_public_created ON app.campaigns (created_at DESC, id DESC)
  WHERE visibility = 'PUBLIC' AND status IN ('ACTIVE', 'PAUSED', 'COMPLETED');
CREATE INDEX ix_campaigns_public_category ON app.campaigns (category_code, published_at DESC, id DESC)
  WHERE visibility = 'PUBLIC' AND status IN ('ACTIVE', 'PAUSED', 'COMPLETED');

CREATE TRIGGER trg_campaigns_guard_status BEFORE INSERT OR UPDATE OF status ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('campaign');
CREATE TRIGGER trg_campaigns_bump_version BEFORE UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaigns_set_updated_at BEFORE UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaigns_keep_created_at BEFORE UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_campaigns_immutable_cols BEFORE UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'public_code', 'owner_user_id', 'owner_organisation_id', 'created_by');
CREATE TRIGGER trg_campaigns_no_delete BEFORE DELETE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaigns_no_truncate BEFORE TRUNCATE ON app.campaigns
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Rules the edge list cannot express: the goal currency is fixed after the first submission; the slug is fixed
-- once published; content is frozen while a reviewer has it; the owner of a personal campaign is a USER account.
CREATE FUNCTION app.campaigns_rules() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE kind text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.owner_user_id IS NOT NULL THEN
      SELECT account_kind INTO kind FROM app.users WHERE id = NEW.owner_user_id;
      IF kind IS DISTINCT FROM 'USER' THEN
        RAISE EXCEPTION 'campaign owner must be a personal account' USING ERRCODE = 'check_violation';
      END IF;
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.goal_currency <> OLD.goal_currency AND OLD.submitted_at IS NOT NULL THEN
    RAISE EXCEPTION 'campaign %: the goal currency is fixed after submission', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.slug <> OLD.slug AND OLD.published_at IS NOT NULL THEN
    RAISE EXCEPTION 'campaign %: the slug is fixed once published', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.status IN ('SUBMITTED', 'UNDER_REVIEW', 'APPROVED', 'REJECTED', 'CANCELLED', 'ARCHIVED', 'SUSPENDED')
     AND (NEW.title, NEW.summary, NEW.story, NEW.category_code, NEW.goal_amount_minor, NEW.goal_currency)
         IS DISTINCT FROM (OLD.title, OLD.summary, OLD.story, OLD.category_code, OLD.goal_amount_minor, OLD.goal_currency) THEN
    RAISE EXCEPTION 'campaign %: content cannot change in status %', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaigns_rules BEFORE INSERT OR UPDATE ON app.campaigns
  FOR EACH ROW EXECUTE FUNCTION app.campaigns_rules();

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_goals — append-only goal history (amount_minor + currency). The current goal is on campaigns.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_goals (
  id            uuid        NOT NULL,
  campaign_id   uuid        NOT NULL REFERENCES app.campaigns (id),
  goal_version  integer     NOT NULL,
  amount_minor  bigint      NOT NULL,
  currency      char(3)     NOT NULL REFERENCES app.currencies (code),
  set_by        uuid        NOT NULL REFERENCES app.users (id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_goals PRIMARY KEY (id),
  CONSTRAINT uq_campaign_goals_version UNIQUE (campaign_id, goal_version),
  CONSTRAINT ck_campaign_goals_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_campaign_goals_version CHECK (goal_version >= 1)
);
CREATE TRIGGER trg_campaign_goals_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_goals
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_versions — append-only snapshots (submission, material edit, approval pins one). What donors
-- are shown is always an approved snapshot. content_sha256 is evidence of exactly what was reviewed.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_versions (
  id                    uuid        NOT NULL,
  campaign_id           uuid        NOT NULL REFERENCES app.campaigns (id),
  version_number        integer     NOT NULL,
  change_kind           text        NOT NULL,
  material_change_types text[]      NOT NULL DEFAULT '{}',
  title                 text        NOT NULL,
  summary               text        NOT NULL,
  story                 text        NOT NULL,
  category_code         text        NOT NULL REFERENCES app.campaign_categories (code),
  goal_amount_minor     bigint      NOT NULL,
  goal_currency         char(3)     NOT NULL REFERENCES app.currencies (code),
  beneficiary_id        uuid        REFERENCES app.beneficiaries (id),
  beneficiary_disclosure text       NOT NULL DEFAULT 'NONE',
  media_ids             uuid[]      NOT NULL DEFAULT '{}',
  content_sha256        bytea       NOT NULL,
  created_by            uuid        NOT NULL REFERENCES app.users (id),
  created_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_versions PRIMARY KEY (id),
  CONSTRAINT uq_campaign_versions_number UNIQUE (campaign_id, version_number),
  CONSTRAINT uq_campaign_versions_id_campaign UNIQUE (id, campaign_id),
  CONSTRAINT ck_campaign_versions_kind CHECK (change_kind IN ('SUBMISSION', 'RESUBMISSION', 'MATERIAL_EDIT')),
  CONSTRAINT ck_campaign_versions_material CHECK (
    material_change_types <@ ARRAY['BENEFICIARY', 'GOAL_INCREASE', 'GOAL_CHANGE', 'CATEGORY', 'STORY', 'TITLE', 'SUMMARY', 'MEDIA']::text[]
    AND ((change_kind = 'MATERIAL_EDIT') = (cardinality(material_change_types) > 0))),
  CONSTRAINT ck_campaign_versions_disclosure CHECK (beneficiary_disclosure IN ('NONE', 'DISPLAY_NAME')),
  CONSTRAINT ck_campaign_versions_goal CHECK (goal_amount_minor > 0),
  CONSTRAINT ck_campaign_versions_sha CHECK (octet_length(content_sha256) = 32)
);
CREATE TRIGGER trg_campaign_versions_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_versions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
ALTER TABLE app.campaigns ADD CONSTRAINT fk_campaigns_approved_version
  FOREIGN KEY (approved_version_id, id) REFERENCES app.campaign_versions (id, campaign_id);
ALTER TABLE app.campaigns ADD CONSTRAINT fk_campaigns_pending_version
  FOREIGN KEY (pending_version_id, id) REFERENCES app.campaign_versions (id, campaign_id);

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_beneficiaries — link to app.beneficiaries (beneficiaries module owns them). One current primary;
-- changes keep history (unlinked_at). Disclosure: NONE unless consent is declared (and never for minors).
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_beneficiaries (
  id               uuid        NOT NULL,
  campaign_id      uuid        NOT NULL REFERENCES app.campaigns (id),
  beneficiary_id   uuid        NOT NULL REFERENCES app.beneficiaries (id),
  disclosure       text        NOT NULL DEFAULT 'NONE',
  consent_declared boolean     NOT NULL DEFAULT false,
  linked_by        uuid        NOT NULL REFERENCES app.users (id),
  linked_at        timestamptz NOT NULL,
  link_reason      text,
  unlinked_by      uuid        REFERENCES app.users (id),
  unlinked_at      timestamptz,
  unlink_reason    text,
  CONSTRAINT pk_campaign_beneficiaries PRIMARY KEY (id),
  CONSTRAINT ck_campaign_beneficiaries_disclosure CHECK (disclosure IN ('NONE', 'DISPLAY_NAME')),
  CONSTRAINT ck_campaign_beneficiaries_consent CHECK (disclosure = 'NONE' OR consent_declared),
  CONSTRAINT ck_campaign_beneficiaries_unlink CHECK ((unlinked_at IS NULL) = (unlinked_by IS NULL)
             AND (unlinked_at IS NULL OR length(unlink_reason) >= 3))
);
CREATE UNIQUE INDEX uq_campaign_beneficiaries_current ON app.campaign_beneficiaries (campaign_id) WHERE unlinked_at IS NULL;
CREATE INDEX ix_campaign_beneficiaries_beneficiary ON app.campaign_beneficiaries (beneficiary_id) WHERE unlinked_at IS NULL;
CREATE TRIGGER trg_campaign_beneficiaries_cols BEFORE UPDATE ON app.campaign_beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('unlinked_by', 'unlinked_at', 'unlink_reason');
CREATE TRIGGER trg_campaign_beneficiaries_no_delete BEFORE DELETE ON app.campaign_beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_reviews — one review per (re)submission, pinned to a version and the policy in force.
-- Four eyes for tiers whose policy requires it: pending_* holds the first approval; the second approver differs.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_reviews (
  id                  uuid        NOT NULL,
  campaign_id         uuid        NOT NULL REFERENCES app.campaigns (id),
  campaign_version_id uuid        NOT NULL,
  policy_version      text        NOT NULL REFERENCES app.campaign_policies (policy_version),
  risk_tier           text        NOT NULL,
  requires_second_approval boolean NOT NULL,
  kind                text        NOT NULL DEFAULT 'SUBMISSION',
  status              text        NOT NULL,
  assigned_to         uuid        REFERENCES app.users (id),
  assigned_at         timestamptz,
  started_at          timestamptz,
  escalated           boolean     NOT NULL DEFAULT false,
  escalated_at        timestamptz,
  escalated_by        uuid        REFERENCES app.users (id),
  escalation_reason   text,
  pending_outcome     text,
  pending_decided_by  uuid        REFERENCES app.users (id),
  pending_decided_at  timestamptz,
  pending_reason_code text,
  pending_note        text,
  outcome             text,
  decided_by          uuid        REFERENCES app.users (id),
  second_approver     uuid        REFERENCES app.users (id),
  decided_at          timestamptz,
  reason_code         text,
  note                text,
  user_message        text,
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_reviews PRIMARY KEY (id),
  CONSTRAINT fk_campaign_reviews_version FOREIGN KEY (campaign_version_id, campaign_id) REFERENCES app.campaign_versions (id, campaign_id),
  CONSTRAINT ck_campaign_reviews_status CHECK (status IN ('QUEUED', 'ASSIGNED', 'IN_REVIEW', 'DECIDED', 'CANCELLED')),
  CONSTRAINT ck_campaign_reviews_kind CHECK (kind IN ('SUBMISSION', 'RE_REVIEW', 'REOPEN')),
  CONSTRAINT ck_campaign_reviews_tier CHECK (risk_tier IN ('STANDARD', 'ELEVATED', 'HIGH')),
  CONSTRAINT ck_campaign_reviews_assigned CHECK (status IN ('QUEUED', 'CANCELLED') OR (assigned_to IS NOT NULL AND assigned_at IS NOT NULL)),
  CONSTRAINT ck_campaign_reviews_started CHECK (status NOT IN ('IN_REVIEW', 'DECIDED') OR started_at IS NOT NULL),
  CONSTRAINT ck_campaign_reviews_escalated CHECK (NOT escalated OR (escalated_at IS NOT NULL AND escalated_by IS NOT NULL
             AND length(escalation_reason) >= 3)),
  CONSTRAINT ck_campaign_reviews_pending CHECK ((pending_outcome IS NULL) = (pending_decided_by IS NULL)
             AND (pending_outcome IS NULL OR pending_outcome = 'APPROVE')),
  CONSTRAINT ck_campaign_reviews_outcome CHECK (outcome IS NULL OR outcome IN ('APPROVE', 'REJECT', 'REQUEST_CHANGES')),
  CONSTRAINT ck_campaign_reviews_decided CHECK ((status = 'DECIDED') = (outcome IS NOT NULL)
             AND (outcome IS NULL OR (decided_by IS NOT NULL AND decided_at IS NOT NULL AND reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'
                  AND length(note) BETWEEN 3 AND 5000))),
  CONSTRAINT ck_campaign_reviews_four_eyes CHECK (outcome IS DISTINCT FROM 'APPROVE' OR NOT requires_second_approval
             OR (second_approver IS NOT NULL AND second_approver <> decided_by)),
  CONSTRAINT ck_campaign_reviews_second CHECK (second_approver IS NULL OR outcome = 'APPROVE')
);
CREATE UNIQUE INDEX uq_campaign_reviews_open ON app.campaign_reviews (campaign_id) WHERE status IN ('QUEUED', 'ASSIGNED', 'IN_REVIEW');
CREATE INDEX ix_campaign_reviews_assigned ON app.campaign_reviews (assigned_to, status) WHERE status IN ('ASSIGNED', 'IN_REVIEW');
CREATE TRIGGER trg_campaign_reviews_guard_status BEFORE INSERT OR UPDATE OF status ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('campaign_review');
CREATE TRIGGER trg_campaign_reviews_bump_version BEFORE UPDATE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaign_reviews_set_updated_at BEFORE UPDATE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_reviews_immutable_cols BEFORE UPDATE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'campaign_id', 'campaign_version_id', 'policy_version',
                                                         'risk_tier', 'requires_second_approval', 'kind', 'created_at');
CREATE TRIGGER trg_campaign_reviews_no_delete BEFORE DELETE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- Decided reviews are final.
CREATE FUNCTION app.campaign_reviews_final() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status IN ('DECIDED', 'CANCELLED') THEN
    RAISE EXCEPTION 'campaign review %: a % review is final', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_reviews_final BEFORE UPDATE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.campaign_reviews_final();

-- Nobody reviews or decides a campaign that concerns them: the personal owner, a member of the owning
-- organisation, the creator, or any account linked to them (app.actor_identities, ADR-037 §4). SECURITY
-- DEFINER so the check sees memberships and links whatever the caller's grants.
CREATE FUNCTION app.campaign_reviews_not_self() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, app AS $$
DECLARE
  c app.campaigns%ROWTYPE;
  actor uuid;
  ident uuid;
BEGIN
  SELECT * INTO c FROM app.campaigns WHERE id = NEW.campaign_id;
  FOREACH actor IN ARRAY ARRAY[NEW.assigned_to, NEW.pending_decided_by, NEW.decided_by, NEW.second_approver, NEW.escalated_by] LOOP
    CONTINUE WHEN actor IS NULL;
    FOREACH ident IN ARRAY app.actor_identities(actor) LOOP
      IF ident = c.owner_user_id OR ident = c.created_by
         OR (c.owner_organisation_id IS NOT NULL AND EXISTS (SELECT 1 FROM app.organisation_members m
               WHERE m.organisation_id = c.owner_organisation_id AND m.user_id = ident AND m.removed_at IS NULL)) THEN
        RAISE EXCEPTION 'campaign review %: % may not review a campaign that concerns them', NEW.id, actor USING ERRCODE = 'check_violation';
      END IF;
    END LOOP;
  END LOOP;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_reviews_not_self BEFORE INSERT OR UPDATE ON app.campaign_reviews
  FOR EACH ROW EXECUTE FUNCTION app.campaign_reviews_not_self();

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_status_history — append-only; exactly one row per campaign version (deferred check), with the
-- exact from/to status, actor and reason. Staff transitions require a reason.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_status_history (
  id               uuid        NOT NULL,
  campaign_id      uuid        NOT NULL REFERENCES app.campaigns (id),
  campaign_version integer     NOT NULL,
  event_type       text        NOT NULL,
  from_status      text,
  to_status        text        NOT NULL,
  actor_type       text        NOT NULL,
  actor_id         uuid        REFERENCES app.users (id),
  reason_code      text,
  note             text,
  correlation_id   text,
  payload          jsonb       NOT NULL DEFAULT '{}',
  occurred_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_status_history PRIMARY KEY (id),
  CONSTRAINT uq_campaign_status_history_version UNIQUE (campaign_id, campaign_version),
  CONSTRAINT ck_campaign_status_history_actor CHECK ((actor_type IN ('USER', 'STAFF') AND actor_id IS NOT NULL)
             OR (actor_type = 'SYSTEM' AND actor_id IS NULL)),
  CONSTRAINT ck_campaign_status_history_staff_reason CHECK (actor_type <> 'STAFF' OR from_status IS NOT DISTINCT FROM to_status
             OR reason_code IS NOT NULL),
  CONSTRAINT ck_campaign_status_history_reason CHECK (reason_code IS NULL OR reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_campaign_status_history_payload CHECK (jsonb_typeof(payload) = 'object')
);
CREATE INDEX ix_campaign_status_history_campaign ON app.campaign_status_history (campaign_id, campaign_version);
CREATE TRIGGER trg_campaign_status_history_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_status_history
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION app.campaigns_require_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v integer; st text;
BEGIN
  SELECT version, status INTO v, st FROM app.campaigns WHERE id = NEW.id;
  IF NOT EXISTS (SELECT 1 FROM app.campaign_status_history h WHERE h.campaign_id = NEW.id AND h.campaign_version = v AND h.to_status = st) THEN
    RAISE EXCEPTION 'campaign % version % has no matching campaign_status_history row', NEW.id, v USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_campaigns_require_history AFTER INSERT OR UPDATE ON app.campaigns
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.campaigns_require_history();

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_eligibility_evaluations — append-only record of gated evaluations (submit, approve, publish,
-- reactivate): what was checked, under which policy, with which outcome. Reasons are codes, never C3 values.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_eligibility_evaluations (
  id             uuid        NOT NULL,
  campaign_id    uuid        NOT NULL REFERENCES app.campaigns (id),
  action         text        NOT NULL,
  actor_id       uuid        REFERENCES app.users (id),
  policy_version text        NOT NULL,
  allowed        boolean     NOT NULL,
  reasons        jsonb       NOT NULL DEFAULT '[]',
  evaluated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_eligibility_evaluations PRIMARY KEY (id),
  CONSTRAINT ck_campaign_eligibility_action CHECK (action IN ('CREATE_DRAFT', 'EDIT_DRAFT', 'SUBMIT_FOR_REVIEW', 'APPROVE', 'PUBLISH',
             'UPDATE_PUBLISHED', 'REACTIVATE')),
  CONSTRAINT ck_campaign_eligibility_reasons CHECK (jsonb_typeof(reasons) = 'array' AND (allowed = (jsonb_array_length(reasons) = 0)))
);
CREATE INDEX ix_campaign_eligibility_campaign ON app.campaign_eligibility_evaluations (campaign_id, evaluated_at);
CREATE TRIGGER trg_campaign_eligibility_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_eligibility_evaluations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---------------------------------------------------------------------------------------------------------
-- Full-text search over what the public actually sees (approved snapshots), no extension needed: the
-- 'simple' configuration with a GIN index; queries join campaigns.approved_version_id.
-- ---------------------------------------------------------------------------------------------------------
CREATE INDEX ix_campaign_versions_search ON app.campaign_versions USING gin (to_tsvector('simple', title || ' ' || summary));

-- ---------------------------------------------------------------------------------------------------------
-- Permissions (interface-contracts §8). SUPPORT and ADMIN get no decision rights.
-- ---------------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up)
SELECT md5('permission:' || p.code)::uuid, p.code, p.description, p.sensitive, p.step_up
FROM (VALUES
  ('campaign.decide.high',      'Give the second approval for a campaign whose tier requires four eyes', true, true),
  ('campaign.publish',          'Publish an approved campaign on the owner''s behalf',                  true, true),
  ('campaign.category.manage',  'Change campaign category presentation and active flag',                true, true)
) AS p(code, description, sensitive, step_up);
UPDATE app.permissions SET requires_step_up = true WHERE code = 'campaign.decide';
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm_code)::uuid,
       md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm_code)::uuid
FROM (VALUES
  ('COMPLIANCE', 'campaign.decide.high'),
  ('REVIEWER', 'campaign.publish'), ('COMPLIANCE', 'campaign.publish'),
  ('ADMIN', 'campaign.category.manage'),
  ('COMPLIANCE', 'campaign.review')
) AS rp(role_code, perm_code)
ON CONFLICT DO NOTHING;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN
  ('campaign.decide.high', 'campaign.publish', 'campaign.category.manage'))
  OR id = md5('role_permission:COMPLIANCE:campaign.review')::uuid;
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('campaign.decide.high', 'campaign.publish', 'campaign.category.manage');
UPDATE app.permissions SET requires_step_up = false WHERE code = 'campaign.decide';
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP TABLE IF EXISTS app.campaign_eligibility_evaluations, app.campaign_status_history, app.campaign_reviews,
  app.campaign_beneficiaries, app.campaign_goals CASCADE;
ALTER TABLE app.campaigns DROP CONSTRAINT IF EXISTS fk_campaigns_approved_version, DROP CONSTRAINT IF EXISTS fk_campaigns_pending_version;
DROP TABLE IF EXISTS app.campaign_versions, app.campaigns, app.campaign_policies, app.campaign_categories CASCADE;
DROP FUNCTION IF EXISTS app.campaigns_rules(), app.campaign_reviews_final(), app.campaign_reviews_not_self(), app.campaigns_require_history();
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine IN ('campaign', 'campaign_policy', 'campaign_review');
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
