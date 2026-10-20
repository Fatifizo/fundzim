-- Stage 16: discovery (ADR-048; docs/stage-16/discovery-architecture.md). Owner module: discovery. Schema: app.
--   app.discovery_campaign_index      search projection of public campaigns (candidates only; eligibility is re-checked
--                                     by campaigns at serve time, so a stale row can drop a result but never expose one)
--   app.discovery_engagement_keys     per-day dedupe keys (keyed hash rotating daily; no IP, no user id); purged after 2 days
--   app.discovery_campaign_daily      aggregate per-campaign daily metrics (views, shares, referrals, impressions, clicks)
--   app.discovery_search_daily        aggregate search counters (never the query text)
--   app.discovery_abuse_daily         aggregate abuse indicators (bot-filtered, capped, rate-limited, invalid referrals)
--   app.discovery_referral_tokens     opaque referral codes (campaign + channel only; no personal data, no privileges)
--   app.discovery_featured_placements staff-requested, checker-approved featured placements with a time window
--   app.discovery_ranking_policies    versioned trending weights (maker-checker; none seeded: an engineering default applies)
--   app.discovery_trending_scores     the latest trending computation per campaign, with its components
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '300s';

-- trigram similarity for typo-tolerant title matching (a trusted extension: the database owner may create it)
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('discovery_featured', '',          'REQUESTED'),
  ('discovery_featured', 'REQUESTED', 'APPROVED'),
  ('discovery_featured', 'REQUESTED', 'REJECTED'),
  ('discovery_featured', 'REQUESTED', 'CANCELLED'),
  ('discovery_featured', 'APPROVED',  'REMOVED'),
  ('discovery_featured', 'APPROVED',  'ENDED'),
  ('discovery_ranking_policy', '',          'SUBMITTED'),
  ('discovery_ranking_policy', 'SUBMITTED', 'ACTIVE'),
  ('discovery_ranking_policy', 'SUBMITTED', 'REJECTED'),
  ('discovery_ranking_policy', 'SUBMITTED', 'WITHDRAWN'),
  ('discovery_ranking_policy', 'ACTIVE',    'RETIRED');

-- ---- search index -------------------------------------------------------------------------------------------------
-- One row per campaign ever seen by the indexer. Rows are never deleted: a campaign that leaves public view is marked
-- not discoverable (and its text cleared). Only public, approved-version fields are copied (title, summary, category,
-- coarse location, organiser display name); never the story's private parts, beneficiaries, KYC or risk data.
CREATE TABLE app.discovery_campaign_index (
  campaign_id         uuid        NOT NULL,
  slug                text        NOT NULL DEFAULT '',
  title               text        NOT NULL DEFAULT '',
  summary             text        NOT NULL DEFAULT '',
  category_code       text,
  location_code       text,
  province_code       text,
  organiser_type      text,
  organiser_name      text        NOT NULL DEFAULT '',
  status              text,
  published_at        timestamptz,
  source_version_id   uuid,
  discoverable        boolean     NOT NULL DEFAULT false,
  search_doc          tsvector    NOT NULL DEFAULT ''::tsvector,
  title_norm          text        NOT NULL DEFAULT '',
  snapshot_at         timestamptz,                  -- database time the indexed snapshot was read (newer snapshots win)
  dirty_since         timestamptz,
  last_error          text,
  attempts            integer     NOT NULL DEFAULT 0,
  indexed_at          timestamptz,
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_discovery_campaign_index PRIMARY KEY (campaign_id),
  CONSTRAINT fk_discovery_campaign_index_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT ck_discovery_campaign_index_status CHECK (status IS NULL OR status IN ('ACTIVE', 'PAUSED', 'COMPLETED')),
  CONSTRAINT ck_discovery_campaign_index_organiser CHECK (organiser_type IS NULL OR organiser_type IN ('INDIVIDUAL', 'ORGANISATION')),
  -- a discoverable row carries its public fields; a removed row carries none
  CONSTRAINT ck_discovery_campaign_index_shape CHECK (
    (discoverable AND slug <> '' AND title <> '' AND status IS NOT NULL AND category_code IS NOT NULL AND published_at IS NOT NULL
       AND source_version_id IS NOT NULL AND organiser_type IS NOT NULL)
    OR (NOT discoverable AND title = '' AND summary = '' AND organiser_name = '' AND search_doc = ''::tsvector AND title_norm = '')),
  CONSTRAINT ck_discovery_campaign_index_error CHECK (last_error IS NULL OR last_error ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_discovery_campaign_index_attempts CHECK (attempts >= 0)
);
CREATE INDEX ix_discovery_index_doc ON app.discovery_campaign_index USING gin (search_doc) WHERE discoverable;
CREATE INDEX ix_discovery_index_title_trgm ON app.discovery_campaign_index USING gin (title_norm public.gin_trgm_ops) WHERE discoverable;
CREATE INDEX ix_discovery_index_newest ON app.discovery_campaign_index (published_at DESC, campaign_id DESC) WHERE discoverable;
CREATE INDEX ix_discovery_index_category ON app.discovery_campaign_index (category_code, published_at DESC, campaign_id DESC) WHERE discoverable;
CREATE INDEX ix_discovery_index_province ON app.discovery_campaign_index (province_code, published_at DESC, campaign_id DESC) WHERE discoverable;
CREATE INDEX ix_discovery_index_dirty ON app.discovery_campaign_index (dirty_since) WHERE dirty_since IS NOT NULL;
CREATE TRIGGER trg_discovery_campaign_index_bump_version BEFORE UPDATE ON app.discovery_campaign_index
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_discovery_campaign_index_set_updated_at BEFORE UPDATE ON app.discovery_campaign_index
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_discovery_campaign_index_no_delete BEFORE DELETE ON app.discovery_campaign_index
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- engagement (privacy-preserving) ----------------------------------------------------------------------------
-- Dedupe keys: HMAC(app keyed secret, day | campaign | kind | client address | user-agent hash). The day is part of
-- the input, so keys of different days cannot be linked; nothing reversible is stored. Purged after two days.
CREATE TABLE app.discovery_engagement_keys (
  day          date    NOT NULL,
  campaign_id  uuid    NOT NULL,
  kind         text    NOT NULL,
  visitor_key  bytea   NOT NULL,
  hits         integer NOT NULL DEFAULT 1,
  CONSTRAINT pk_discovery_engagement_keys PRIMARY KEY (day, campaign_id, kind, visitor_key),
  CONSTRAINT ck_discovery_engagement_keys_kind CHECK (kind IN ('VIEW', 'SHARE', 'REFERRAL_VISIT', 'SEARCH_CLICK')),
  CONSTRAINT ck_discovery_engagement_keys_key CHECK (octet_length(visitor_key) = 32),
  CONSTRAINT ck_discovery_engagement_keys_hits CHECK (hits >= 1)
);

-- Aggregates. metric meanings are in docs/stage-16/analytics.md; channel is a share channel or traffic source.
CREATE TABLE app.discovery_campaign_daily (
  day          date   NOT NULL,
  campaign_id  uuid   NOT NULL,
  metric       text   NOT NULL,
  channel      text   NOT NULL DEFAULT '',
  count        bigint NOT NULL,
  CONSTRAINT pk_discovery_campaign_daily PRIMARY KEY (day, campaign_id, metric, channel),
  CONSTRAINT fk_discovery_campaign_daily_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT ck_discovery_campaign_daily_metric CHECK (metric IN ('VIEW_RAW', 'VIEW_UNIQUE', 'VIEW_BOT_EXCLUDED', 'SHARE_RAW', 'SHARE_UNIQUE',
             'REFERRAL_VISIT_RAW', 'REFERRAL_VISIT_UNIQUE', 'SEARCH_IMPRESSION', 'SEARCH_CLICK', 'LISTING_IMPRESSION')),
  CONSTRAINT ck_discovery_campaign_daily_channel CHECK (channel IN ('', 'WHATSAPP', 'FACEBOOK', 'X', 'LINKEDIN', 'EMAIL', 'COPY_LINK', 'WEB_SHARE',
             'DIRECT', 'SEARCH', 'CATEGORY', 'FEATURED', 'TRENDING', 'RECOMMENDED', 'INTERNAL', 'UNKNOWN')),
  CONSTRAINT ck_discovery_campaign_daily_count CHECK (count > 0)
);
CREATE INDEX ix_discovery_campaign_daily_campaign ON app.discovery_campaign_daily (campaign_id, day);

CREATE TABLE app.discovery_search_daily (
  day          date    NOT NULL,
  sort         text    NOT NULL,
  has_query    boolean NOT NULL,
  has_filters  boolean NOT NULL,
  outcome      text    NOT NULL,
  count        bigint  NOT NULL,
  CONSTRAINT pk_discovery_search_daily PRIMARY KEY (day, sort, has_query, has_filters, outcome),
  CONSTRAINT ck_discovery_search_daily_sort CHECK (sort IN ('relevance', 'newest', 'oldest', 'trending')),
  CONSTRAINT ck_discovery_search_daily_outcome CHECK (outcome IN ('RESULTS', 'NO_RESULTS', 'FUZZY_RESULTS', 'TIMEOUT', 'ERROR')),
  CONSTRAINT ck_discovery_search_daily_count CHECK (count > 0)
);

CREATE TABLE app.discovery_abuse_daily (
  day          date   NOT NULL,
  kind         text   NOT NULL,
  campaign_id  uuid,
  count        bigint NOT NULL,
  CONSTRAINT ck_discovery_abuse_daily_kind CHECK (kind IN ('BOT_FILTERED', 'VISITOR_CAP', 'RATE_LIMITED', 'INVALID_REFERRAL', 'INGEST_DROPPED',
             'NOT_DISCOVERABLE')),
  CONSTRAINT ck_discovery_abuse_daily_count CHECK (count > 0)
);
CREATE UNIQUE INDEX uq_discovery_abuse_daily ON app.discovery_abuse_daily (day, kind, coalesce(campaign_id, '00000000-0000-0000-0000-000000000000'::uuid));

-- ---- referrals -------------------------------------------------------------------------------------------------------
-- An opaque code that says only "this campaign was shared through this channel". No user, no device, no IP, no
-- authentication or financial meaning. Expires; revocable by staff (abuse). Attribution is aggregate only.
CREATE TABLE app.discovery_referral_tokens (
  id              uuid        NOT NULL,
  code            text        NOT NULL,
  campaign_id     uuid        NOT NULL,
  channel         text        NOT NULL,
  created_at      timestamptz NOT NULL,
  expires_at      timestamptz NOT NULL,
  revoked_at      timestamptz,
  revoked_by      uuid        REFERENCES app.users (id),
  revoked_reason  text,
  CONSTRAINT pk_discovery_referral_tokens PRIMARY KEY (id),
  CONSTRAINT uq_discovery_referral_tokens_code UNIQUE (code),
  CONSTRAINT fk_discovery_referral_tokens_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT ck_discovery_referral_tokens_code CHECK (code ~ '^[0-9a-hjkmnp-tv-z]{12}$'),
  CONSTRAINT ck_discovery_referral_tokens_channel CHECK (channel IN ('WHATSAPP', 'FACEBOOK', 'X', 'LINKEDIN', 'EMAIL', 'COPY_LINK', 'WEB_SHARE')),
  CONSTRAINT ck_discovery_referral_tokens_window CHECK (expires_at > created_at AND expires_at <= created_at + interval '180 days'),
  CONSTRAINT ck_discovery_referral_tokens_revoked CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)
             AND (revoked_at IS NULL OR length(revoked_reason) BETWEEN 3 AND 500))
);
CREATE INDEX ix_discovery_referral_tokens_campaign ON app.discovery_referral_tokens (campaign_id, created_at);
CREATE TRIGGER trg_discovery_referral_tokens_cols BEFORE UPDATE ON app.discovery_referral_tokens
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('revoked_at', 'revoked_by', 'revoked_reason');
CREATE TRIGGER trg_discovery_referral_tokens_no_delete BEFORE DELETE ON app.discovery_referral_tokens
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- featured placements (maker-checker) ------------------------------------------------------------------------
-- Requested by one staff member (discovery.feature.request), approved by another (discovery.feature.approve; never
-- the requester or a linked account), shown only inside [starts_at, ends_at) and only while the campaign is
-- promotable (re-checked at serve time). Removal is immediate (one person, reason required).
CREATE TABLE app.discovery_featured_placements (
  id               uuid        NOT NULL,
  campaign_id      uuid        NOT NULL,
  position         integer     NOT NULL,
  reason           text        NOT NULL,
  starts_at        timestamptz NOT NULL,
  ends_at          timestamptz NOT NULL,
  status           text        NOT NULL,
  requested_by     uuid        NOT NULL REFERENCES app.users (id),
  requested_at     timestamptz NOT NULL,
  decided_by       uuid        REFERENCES app.users (id),
  decided_at       timestamptz,
  decision_note    text,
  removed_by       uuid        REFERENCES app.users (id),
  removed_at       timestamptz,
  removal_reason   text,
  version          integer     NOT NULL DEFAULT 1,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_discovery_featured_placements PRIMARY KEY (id),
  CONSTRAINT fk_discovery_featured_placements_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT ck_discovery_featured_placements_status CHECK (status IN ('REQUESTED', 'APPROVED', 'REJECTED', 'CANCELLED', 'REMOVED', 'ENDED')),
  CONSTRAINT ck_discovery_featured_placements_position CHECK (position BETWEEN 1 AND 100),
  CONSTRAINT ck_discovery_featured_placements_reason CHECK (length(reason) BETWEEN 10 AND 500),
  CONSTRAINT ck_discovery_featured_placements_window CHECK (ends_at > starts_at AND ends_at <= starts_at + interval '31 days'),
  CONSTRAINT ck_discovery_featured_placements_checker CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT ck_discovery_featured_placements_decided CHECK ((status IN ('REQUESTED', 'CANCELLED')) = (decided_at IS NULL)
             AND (decided_at IS NULL) = (decided_by IS NULL)
             AND (decided_at IS NULL OR length(decision_note) BETWEEN 3 AND 1000)),
  CONSTRAINT ck_discovery_featured_placements_removed CHECK ((status = 'REMOVED') = (removed_at IS NOT NULL)
             AND (removed_at IS NULL) = (removed_by IS NULL) AND (removed_at IS NULL OR length(removal_reason) BETWEEN 3 AND 500))
);
-- one open placement per campaign
CREATE UNIQUE INDEX uq_discovery_featured_open ON app.discovery_featured_placements (campaign_id) WHERE status IN ('REQUESTED', 'APPROVED');
CREATE INDEX ix_discovery_featured_live ON app.discovery_featured_placements (position, starts_at) WHERE status = 'APPROVED';
CREATE TRIGGER trg_discovery_featured_guard_status BEFORE INSERT OR UPDATE OF status ON app.discovery_featured_placements
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('discovery_featured');
CREATE TRIGGER trg_discovery_featured_bump_version BEFORE UPDATE ON app.discovery_featured_placements
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_discovery_featured_set_updated_at BEFORE UPDATE ON app.discovery_featured_placements
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
-- the request is immutable; only the decision and removal fields change
CREATE TRIGGER trg_discovery_featured_cols BEFORE UPDATE ON app.discovery_featured_placements
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'decided_by', 'decided_at', 'decision_note', 'removed_by', 'removed_at',
                                                             'removal_reason', 'version', 'updated_at');
CREATE TRIGGER trg_discovery_featured_no_delete BEFORE DELETE ON app.discovery_featured_placements
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- ranking policies (maker-checker; none seeded) --------------------------------------------------------------
CREATE TABLE app.discovery_ranking_policies (
  id               uuid        NOT NULL,
  policy_version   integer     NOT NULL,
  weights          jsonb       NOT NULL,
  content_sha256   bytea       NOT NULL,
  status           text        NOT NULL,
  reason           text        NOT NULL,
  proposed_by      uuid        NOT NULL REFERENCES app.users (id),
  proposed_at      timestamptz NOT NULL,
  decided_by       uuid        REFERENCES app.users (id),
  decided_at       timestamptz,
  decision_note    text,
  retired_at       timestamptz,
  version          integer     NOT NULL DEFAULT 1,
  CONSTRAINT pk_discovery_ranking_policies PRIMARY KEY (id),
  CONSTRAINT uq_discovery_ranking_policies_version UNIQUE (policy_version),
  CONSTRAINT ck_discovery_ranking_policies_status CHECK (status IN ('SUBMITTED', 'ACTIVE', 'REJECTED', 'WITHDRAWN', 'RETIRED')),
  -- closed key set (validated in Go too: bounded integer weights)
  CONSTRAINT ck_discovery_ranking_policies_weights CHECK (jsonb_typeof(weights) = 'object'
             AND weights ?& ARRAY['freshness_half_life_hours', 'min_unique_visitors', 'share_weight', 'view_weight', 'window_days']
             AND weights - ARRAY['freshness_half_life_hours', 'min_unique_visitors', 'share_weight', 'view_weight', 'window_days'] = '{}'::jsonb
             AND jsonb_typeof(weights -> 'freshness_half_life_hours') = 'number' AND jsonb_typeof(weights -> 'min_unique_visitors') = 'number'
             AND jsonb_typeof(weights -> 'share_weight') = 'number' AND jsonb_typeof(weights -> 'view_weight') = 'number'
             AND jsonb_typeof(weights -> 'window_days') = 'number'),
  CONSTRAINT ck_discovery_ranking_policies_sha CHECK (octet_length(content_sha256) = 32),
  CONSTRAINT ck_discovery_ranking_policies_reason CHECK (length(reason) BETWEEN 10 AND 1000),
  CONSTRAINT ck_discovery_ranking_policies_checker CHECK (decided_by IS NULL OR decided_by <> proposed_by),
  CONSTRAINT ck_discovery_ranking_policies_decided CHECK ((status IN ('SUBMITTED', 'WITHDRAWN')) = (decided_at IS NULL)
             AND (decided_at IS NULL) = (decided_by IS NULL) AND (decided_at IS NULL OR length(decision_note) BETWEEN 3 AND 1000)),
  CONSTRAINT ck_discovery_ranking_policies_retired CHECK ((status = 'RETIRED') = (retired_at IS NOT NULL))
);
CREATE UNIQUE INDEX uq_discovery_ranking_policies_active ON app.discovery_ranking_policies ((true)) WHERE status = 'ACTIVE';
CREATE TRIGGER trg_discovery_ranking_guard_status BEFORE INSERT OR UPDATE OF status ON app.discovery_ranking_policies
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('discovery_ranking_policy');
CREATE TRIGGER trg_discovery_ranking_bump_version BEFORE UPDATE ON app.discovery_ranking_policies
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_discovery_ranking_cols BEFORE UPDATE ON app.discovery_ranking_policies
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'decided_by', 'decided_at', 'decision_note', 'retired_at', 'version');
CREATE TRIGGER trg_discovery_ranking_no_delete BEFORE DELETE ON app.discovery_ranking_policies
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- maker-checker in the database too: the checker is never the maker or a linked account (ADR-037 §4)
CREATE FUNCTION app.discovery_checker_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE maker uuid;
BEGIN
  IF TG_TABLE_NAME = 'discovery_featured_placements' THEN maker := NEW.requested_by; ELSE maker := NEW.proposed_by; END IF;
  IF NEW.decided_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.decided_by IS NULL) AND maker = ANY (app.actor_identities(NEW.decided_by)) THEN
    RAISE EXCEPTION 'maker-checker: % cannot decide % %', NEW.decided_by, TG_TABLE_NAME, NEW.id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_discovery_featured_checker BEFORE INSERT OR UPDATE ON app.discovery_featured_placements
  FOR EACH ROW EXECUTE FUNCTION app.discovery_checker_guard();
CREATE TRIGGER trg_discovery_ranking_checker BEFORE INSERT OR UPDATE ON app.discovery_ranking_policies
  FOR EACH ROW EXECUTE FUNCTION app.discovery_checker_guard();

-- ---- trending ----------------------------------------------------------------------------------------------------------
CREATE TABLE app.discovery_trending_scores (
  campaign_id     uuid          NOT NULL,
  score           numeric(14,6) NOT NULL,
  components      jsonb         NOT NULL,
  policy_version  integer,      -- NULL: the engineering default (docs/stage-16/trending.md)
  rank_eligible   boolean       NOT NULL,
  computed_at     timestamptz   NOT NULL,
  CONSTRAINT pk_discovery_trending_scores PRIMARY KEY (campaign_id),
  CONSTRAINT fk_discovery_trending_scores_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT ck_discovery_trending_scores_score CHECK (score >= 0),
  CONSTRAINT ck_discovery_trending_scores_components CHECK (jsonb_typeof(components) = 'object')
);
CREATE INDEX ix_discovery_trending_rank ON app.discovery_trending_scores (score DESC, campaign_id DESC) WHERE rank_eligible;
CREATE TRIGGER trg_discovery_trending_scores_no_delete BEFORE DELETE ON app.discovery_trending_scores
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- permissions --------------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:discovery.view')::uuid, 'discovery.view', 'View discovery analytics, index health, trending, featured placements and abuse indicators (aggregates only)', false, false),
  (md5('permission:discovery.feature.request')::uuid, 'discovery.feature.request', 'Request a featured placement for a campaign (maker)', true, true),
  (md5('permission:discovery.feature.approve')::uuid, 'discovery.feature.approve', 'Approve or reject featured placement requests (checker)', true, true),
  (md5('permission:discovery.feature.remove')::uuid, 'discovery.feature.remove', 'Remove a featured placement at once (safety)', true, true),
  (md5('permission:discovery.ranking.request')::uuid, 'discovery.ranking.request', 'Propose a trending ranking policy version (maker)', true, true),
  (md5('permission:discovery.ranking.approve')::uuid, 'discovery.ranking.approve', 'Approve or reject trending ranking policy versions (checker)', true, true),
  (md5('permission:discovery.operate')::uuid, 'discovery.operate', 'Revoke abusive referral codes and request a search index reconciliation', true, true);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES
  ('ADMIN','discovery.view'), ('REVIEWER','discovery.view'), ('COMPLIANCE','discovery.view'),
  ('ADMIN','discovery.feature.request'), ('REVIEWER','discovery.feature.request'),
  ('COMPLIANCE','discovery.feature.approve'),
  ('ADMIN','discovery.feature.remove'), ('REVIEWER','discovery.feature.remove'), ('COMPLIANCE','discovery.feature.remove'),
  ('ADMIN','discovery.ranking.request'), ('COMPLIANCE','discovery.ranking.approve'),
  ('ADMIN','discovery.operate'), ('COMPLIANCE','discovery.operate')
) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

-- referral attribution kill switch (LR-023: production use additionally needs discovery.ProductionReferralsEnabled)
INSERT INTO app.feature_flags (id, key, description, flag_type, enabled, requires_approval, legal_ref) VALUES
  (md5('feature_flag:discovery.referrals.enabled')::uuid, 'discovery.referrals.enabled',
   'Kill switch for referral codes and referral visit counting (aggregate, no personal data). Production use stays blocked in code until LR-023 is answered.',
   'KILL_SWITCH', true, true, 'LR-023');
INSERT INTO app.feature_flag_changes (id, feature_flag_id, flag_version, old_enabled, new_enabled, requested_by, approved_by, reason, occurred_at) VALUES
  (md5('feature_flag_change:discovery.referrals.enabled:1')::uuid, md5('feature_flag:discovery.referrals.enabled')::uuid, 1, NULL, true,
   '00000000-0000-0000-0000-000000000001', NULL, 'seeded by migration (Stage 16, ADR-048)', now());

-- ---- runtime grants: the Stage 11 procedure plus the discovery dedupe purge and the location reference list ------------
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly, fundzim_ledger';
  -- Stage 16: discovery dedupe keys are purged after two days (they are not history)
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events','app.discovery_engagement_keys'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles',
                                          'app.campaign_locations'];  -- Stage 16
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
ALTER TABLE app.feature_flag_changes DISABLE TRIGGER USER;
DELETE FROM app.feature_flag_changes WHERE feature_flag_id = md5('feature_flag:discovery.referrals.enabled')::uuid;
ALTER TABLE app.feature_flag_changes ENABLE TRIGGER USER;
ALTER TABLE app.feature_flags DISABLE TRIGGER USER;
DELETE FROM app.feature_flags WHERE id = md5('feature_flag:discovery.referrals.enabled')::uuid;
ALTER TABLE app.feature_flags ENABLE TRIGGER USER;
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code LIKE 'discovery.%');
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code LIKE 'discovery.%';
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP TABLE app.discovery_trending_scores, app.discovery_ranking_policies, app.discovery_featured_placements, app.discovery_referral_tokens,
  app.discovery_abuse_daily, app.discovery_search_daily, app.discovery_campaign_daily, app.discovery_engagement_keys, app.discovery_campaign_index;
DROP FUNCTION IF EXISTS app.discovery_checker_guard();
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine IN ('discovery_featured', 'discovery_ranking_policy');
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
-- pg_trgm stays installed (harmless; other objects may use it)
-- runtime grants: the Stage 11 procedure
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
