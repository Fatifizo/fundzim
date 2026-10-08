-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0002: platform tables (currencies, markets, idempotency, outbox, inbox, feature flags) and shared
-- helper trigger functions. Owner module: platform. Schema: app.
-- Docs: docs/database/design-principles.md, docs/database/audit-notification-schema.md (outbox/inbox),
--       docs/MONEY.md §2 (currency registry).
-- =====================================================================================================

-- -----------------------------------------------------------------------------------------------------
-- Shared helper trigger functions (platform). They complement app.forbid_mutation / app.set_updated_at /
-- app.keep_created_at / app.guard_transition from 0001.
-- -----------------------------------------------------------------------------------------------------

-- Columns named in the trigger arguments may never change on UPDATE. Attach as:
--   CREATE TRIGGER trg_<t>_immutable_cols BEFORE UPDATE ON <t>
--     FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('col_a','col_b');
CREATE FUNCTION app.forbid_column_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  c text;
  o jsonb := to_jsonb(OLD);
  n jsonb := to_jsonb(NEW);
BEGIN
  FOREACH c IN ARRAY TG_ARGV LOOP
    IF (o -> c) IS DISTINCT FROM (n -> c) THEN
      RAISE EXCEPTION 'column %.%.% is immutable', TG_TABLE_SCHEMA, TG_TABLE_NAME, c
        USING ERRCODE = 'restrict_violation';
    END IF;
  END LOOP;
  RETURN NEW;
END $$;

-- Only the columns named in the trigger arguments (plus updated_at) may change on UPDATE; every other
-- column is immutable. Used for "append-only except one bookkeeping column" tables.
CREATE FUNCTION app.allow_only_column_changes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  allowed text[] := TG_ARGV || ARRAY['updated_at'];
BEGIN
  IF (to_jsonb(OLD) - allowed) IS DISTINCT FROM (to_jsonb(NEW) - allowed) THEN
    RAISE EXCEPTION 'only % may change on %.%', array_to_string(TG_ARGV, ', '), TG_TABLE_SCHEMA, TG_TABLE_NAME
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;

-- Columns named in the trigger arguments may be set once (NULL -> value) and never changed afterwards.
-- Used for revocation/withdrawal columns on otherwise append-only tables.
CREATE FUNCTION app.set_once_columns() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  c text;
  o jsonb := to_jsonb(OLD);
  n jsonb := to_jsonb(NEW);
BEGIN
  FOREACH c IN ARRAY TG_ARGV LOOP
    IF (o -> c) IS DISTINCT FROM (n -> c) AND (o -> c) IS NOT NULL AND (o -> c) <> 'null'::jsonb THEN
      RAISE EXCEPTION 'column %.%.% is set once and cannot change', TG_TABLE_SCHEMA, TG_TABLE_NAME, c
        USING ERRCODE = 'restrict_violation';
    END IF;
  END LOOP;
  RETURN NEW;
END $$;

-- Optimistic-concurrency version maintenance. The application updates with
-- "... WHERE id = $1 AND version = $2"; this trigger makes the stored version OLD.version + 1 so a
-- caller can never skip, reuse or roll back a version number.
CREATE FUNCTION app.bump_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.version := OLD.version + 1;
  RETURN NEW;
END $$;

-- -----------------------------------------------------------------------------------------------------
-- app.currencies — ISO 4217 registry (MONEY.md §2, ADR-005, ADR-010, ADR-018). Reference data.
-- Classification: C1. Retention: OPERATIONAL (reference data, never deleted).
-- Column names follow MONEY.md §2 (normative): enabled, minor_units_verified.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.currencies (
  code                 char(3)     NOT NULL,
  numeric_code         smallint    NOT NULL,
  minor_units          smallint    NOT NULL,
  name                 text        NOT NULL,
  display_symbol       text        NOT NULL,
  enabled              boolean     NOT NULL DEFAULT false,  -- new transactions may use it
  minor_units_verified boolean     NOT NULL DEFAULT false,  -- false => sandbox/test only (LR-043, PCR-018)
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_currencies PRIMARY KEY (code),
  CONSTRAINT uq_currencies_numeric_code UNIQUE (numeric_code),
  CONSTRAINT ck_currencies_code_format CHECK (code ~ '^[A-Z]{3}$'),
  CONSTRAINT ck_currencies_numeric_code CHECK (numeric_code BETWEEN 1 AND 999),
  CONSTRAINT ck_currencies_minor_units CHECK (minor_units BETWEEN 0 AND 4),
  CONSTRAINT ck_currencies_display_symbol CHECK (length(display_symbol) BETWEEN 1 AND 8)
);

-- Currencies are never deleted (history keeps its currency). code/numeric_code never change.
-- minor_units may change only while minor_units_verified is false (LR-043 may change ZWG before
-- enablement); once verified it is frozen, because changing it would silently rescale every amount.
CREATE FUNCTION app.currencies_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    RAISE EXCEPTION 'currencies are never deleted (disable instead): %', OLD.code USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.code <> OLD.code OR NEW.numeric_code <> OLD.numeric_code THEN
    RAISE EXCEPTION 'currency code is immutable: %', OLD.code USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.minor_units <> OLD.minor_units AND OLD.minor_units_verified THEN
    RAISE EXCEPTION 'minor_units of verified currency % is immutable', OLD.code USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;

CREATE TRIGGER trg_currencies_guard BEFORE UPDATE OR DELETE ON app.currencies
  FOR EACH ROW EXECUTE FUNCTION app.currencies_guard();
CREATE TRIGGER trg_currencies_no_truncate BEFORE TRUNCATE ON app.currencies
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_currencies_set_updated_at BEFORE UPDATE ON app.currencies
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_currencies_keep_created_at BEFORE UPDATE ON app.currencies
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();

-- Seed (MONEY.md §2). ZWG is seeded enabled for sandbox/test use, but minor_units_verified = false: the
-- platform refuses production use of a currency whose minor units are unverified (LR-043, PCR-018).
INSERT INTO app.currencies (code, numeric_code, minor_units, name, display_symbol, enabled, minor_units_verified) VALUES
  ('USD', 840, 2, 'US Dollar',     'US$', true, true),
  ('ZWG', 924, 2, 'Zimbabwe Gold', 'ZiG', true, false);

-- -----------------------------------------------------------------------------------------------------
-- app.markets — market configuration (ZW first). Classification: C1.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.markets (
  code               char(2)     NOT NULL,                 -- ISO 3166-1 alpha-2
  name               text        NOT NULL,
  default_currency   char(3)     NOT NULL,
  time_zone          text        NOT NULL,                 -- IANA zone for display and calendar dates
  phone_country_code text        NOT NULL,                 -- E.164 prefix, e.g. +263
  default_locale     text        NOT NULL,
  enabled            boolean     NOT NULL DEFAULT false,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_markets PRIMARY KEY (code),
  CONSTRAINT fk_markets_default_currency FOREIGN KEY (default_currency) REFERENCES app.currencies (code),
  CONSTRAINT ck_markets_code_format CHECK (code ~ '^[A-Z]{2}$'),
  CONSTRAINT ck_markets_phone_country_code CHECK (phone_country_code ~ '^\+[1-9][0-9]{0,3}$')
);
CREATE TRIGGER trg_markets_set_updated_at BEFORE UPDATE ON app.markets
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_markets_keep_created_at BEFORE UPDATE ON app.markets
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_markets_immutable_cols BEFORE UPDATE ON app.markets
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('code');

INSERT INTO app.markets (code, name, default_currency, time_zone, phone_country_code, default_locale, enabled)
VALUES ('ZW', 'Zimbabwe', 'USD', 'Africa/Harare', '+263', 'en-ZW', true);

-- -----------------------------------------------------------------------------------------------------
-- app.idempotency_keys — client → API idempotency (Idempotency-Key header). DATABASE §6.
-- scope includes the principal and the operation, e.g. 'payments.create:user:<uuid>' or
-- 'payments.create:guest:<device-session-id>', so two principals can never collide on a key.
-- Classification: C2 (response body may contain the caller's own data; never C3/C4).
-- Retention: OPERATIONAL (purged after expires_at by a retention job).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.idempotency_keys (
  id                   uuid        NOT NULL,
  scope                text        NOT NULL,
  key                  text        NOT NULL,
  request_hash         bytea       NOT NULL,                -- SHA-256 of method + path + canonical body
  status               text        NOT NULL DEFAULT 'IN_PROGRESS',
  locked_until         timestamptz,                         -- lease of the request currently executing
  response_status_code smallint,
  response_body        jsonb,                               -- the API envelope returned (redacted; no secrets)
  resource_type        text,                                -- what was created, e.g. 'payment_intent'
  resource_id          uuid,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  completed_at         timestamptz,
  expires_at           timestamptz NOT NULL,
  CONSTRAINT pk_idempotency_keys PRIMARY KEY (id),
  CONSTRAINT uq_idempotency_keys_scope_key UNIQUE (scope, key),
  CONSTRAINT ck_idempotency_keys_key_length CHECK (length(key) BETWEEN 8 AND 255),
  CONSTRAINT ck_idempotency_keys_scope_length CHECK (length(scope) BETWEEN 3 AND 255),
  CONSTRAINT ck_idempotency_keys_request_hash CHECK (octet_length(request_hash) = 32),
  CONSTRAINT ck_idempotency_keys_status CHECK (status IN ('IN_PROGRESS', 'COMPLETED')),
  CONSTRAINT ck_idempotency_keys_completed CHECK (
    (status = 'COMPLETED') = (completed_at IS NOT NULL AND response_status_code IS NOT NULL)),
  CONSTRAINT ck_idempotency_keys_response_status CHECK (response_status_code IS NULL OR response_status_code BETWEEN 100 AND 599),
  CONSTRAINT ck_idempotency_keys_expiry CHECK (expires_at > created_at)
);
CREATE INDEX ix_idempotency_keys_expires_at ON app.idempotency_keys (expires_at);

-- scope/key/request_hash never change; a COMPLETED record is frozen (its stored response is replayed).
CREATE FUNCTION app.idempotency_keys_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.scope <> OLD.scope OR NEW.key <> OLD.key OR NEW.request_hash <> OLD.request_hash
     OR NEW.created_at <> OLD.created_at OR NEW.id <> OLD.id THEN
    RAISE EXCEPTION 'idempotency key identity is immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.status = 'COMPLETED' THEN
    RAISE EXCEPTION 'completed idempotency record is immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_idempotency_keys_guard BEFORE UPDATE ON app.idempotency_keys
  FOR EACH ROW EXECUTE FUNCTION app.idempotency_keys_guard();
CREATE TRIGGER trg_idempotency_keys_set_updated_at BEFORE UPDATE ON app.idempotency_keys
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- -----------------------------------------------------------------------------------------------------
-- app.outbox_events — transactional outbox (ADR-025). Written in the same transaction as the business
-- change; a relay job enqueues River jobs and marks rows dispatched. Delivery is at-least-once; consumers
-- dedupe through app.inbox_events. Details: docs/architecture/background-processing.md.
-- Classification: C2 (payload carries IDs and minimal fields; never C3/C4).
-- Retention: OPERATIONAL (dispatched rows purged after a window; undispatched rows are never deleted).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.outbox_events (
  id               uuid        NOT NULL,
  aggregate_type   text        NOT NULL,                  -- e.g. 'campaign'
  aggregate_id     uuid        NOT NULL,
  event_type       text        NOT NULL,                  -- e.g. 'campaign.status_changed'
  event_version    smallint    NOT NULL DEFAULT 1,        -- payload schema version
  payload          jsonb       NOT NULL,
  correlation_id   text,
  occurred_at      timestamptz NOT NULL,                  -- business time (injected clock)
  created_at       timestamptz NOT NULL DEFAULT now(),
  available_at     timestamptz NOT NULL DEFAULT now(),    -- not dispatched before this (backoff)
  dispatched_at    timestamptz,
  attempts         integer     NOT NULL DEFAULT 0,
  last_error       text,                                  -- redacted error code/message
  dead_lettered_at timestamptz,                           -- relay gave up; operator action required
  CONSTRAINT pk_outbox_events PRIMARY KEY (id),
  CONSTRAINT ck_outbox_events_event_type CHECK (event_type ~ '^[a-z_]+(\.[a-z_]+)+$'),
  CONSTRAINT ck_outbox_events_payload_object CHECK (jsonb_typeof(payload) = 'object'),
  CONSTRAINT ck_outbox_events_attempts CHECK (attempts >= 0),
  CONSTRAINT ck_outbox_events_event_version CHECK (event_version >= 1),
  CONSTRAINT ck_outbox_events_terminal CHECK (dispatched_at IS NULL OR dead_lettered_at IS NULL)
);
-- Relay hot path: oldest undispatched, available events.
CREATE INDEX ix_outbox_events_undispatched ON app.outbox_events (available_at, id)
  WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL;
CREATE INDEX ix_outbox_events_aggregate ON app.outbox_events (aggregate_type, aggregate_id, created_at);
CREATE INDEX ix_outbox_events_dead_lettered ON app.outbox_events (dead_lettered_at) WHERE dead_lettered_at IS NOT NULL;

-- Event content is immutable; only relay bookkeeping may change. Undispatched events are never deleted.
CREATE TRIGGER trg_outbox_events_bookkeeping_only BEFORE UPDATE ON app.outbox_events
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('available_at', 'dispatched_at', 'attempts', 'last_error', 'dead_lettered_at');
CREATE FUNCTION app.outbox_events_delete_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.dispatched_at IS NULL THEN
    RAISE EXCEPTION 'undispatched outbox event % cannot be deleted', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN OLD;
END $$;
CREATE TRIGGER trg_outbox_events_delete_guard BEFORE DELETE ON app.outbox_events
  FOR EACH ROW EXECUTE FUNCTION app.outbox_events_delete_guard();
CREATE TRIGGER trg_outbox_events_no_truncate BEFORE TRUNCATE ON app.outbox_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.inbox_events — consumer-side dedupe. A consumer inserts (consumer, event_id) in the same
-- transaction as its side effects; a duplicate delivery fails the unique constraint and is skipped.
-- No FK to outbox_events (outbox rows are purged after dispatch). Classification: C1.
-- Retention: OPERATIONAL (rows older than the outbox replay window may be purged by the retention job).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.inbox_events (
  id           uuid        NOT NULL,
  consumer     text        NOT NULL,                      -- e.g. 'notifications.campaign_status'
  event_id     uuid        NOT NULL,                      -- app.outbox_events.id
  event_type   text        NOT NULL,
  processed_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_inbox_events PRIMARY KEY (id),
  CONSTRAINT uq_inbox_events_consumer_event_id UNIQUE (consumer, event_id),
  CONSTRAINT ck_inbox_events_consumer CHECK (consumer ~ '^[a-z_]+(\.[a-z_]+)+$')
);
CREATE INDEX ix_inbox_events_processed_at ON app.inbox_events (processed_at);
CREATE TRIGGER trg_inbox_events_no_update BEFORE UPDATE ON app.inbox_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.feature_flags (+ append-only app.feature_flag_changes) — audited flags, kill switches and policy
-- switches. Every change bumps version and must have a matching change row in the same transaction
-- (deferred check). Flags with requires_approval need maker-checker (approved_by <> requested_by).
-- Classification: C1. Retention: OPERATIONAL (changes: AUDIT class).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.feature_flags (
  id                uuid        NOT NULL,
  key               text        NOT NULL,                 -- e.g. 'campaign.individual_for_others.enabled'
  description       text        NOT NULL,
  flag_type         text        NOT NULL,
  enabled           boolean     NOT NULL DEFAULT false,
  requires_approval boolean     NOT NULL DEFAULT true,
  legal_ref         text,                                 -- e.g. 'LR-046..LR-048, PD-27' where a flag gates an open legal item
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_feature_flags PRIMARY KEY (id),
  CONSTRAINT uq_feature_flags_key UNIQUE (key),
  CONSTRAINT ck_feature_flags_key_format CHECK (key ~ '^[a-z0-9_]+(\.[a-z0-9_]+)+$'),
  CONSTRAINT ck_feature_flags_flag_type CHECK (flag_type IN ('RELEASE', 'KILL_SWITCH', 'POLICY', 'OPS')),
  CONSTRAINT ck_feature_flags_kill_switch_approval CHECK (flag_type NOT IN ('KILL_SWITCH', 'POLICY') OR requires_approval),
  CONSTRAINT ck_feature_flags_version CHECK (version >= 1)
);
CREATE TRIGGER trg_feature_flags_bump_version BEFORE UPDATE ON app.feature_flags
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_feature_flags_set_updated_at BEFORE UPDATE ON app.feature_flags
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_feature_flags_keep_created_at BEFORE UPDATE ON app.feature_flags
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_feature_flags_immutable_cols BEFORE UPDATE ON app.feature_flags
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('key', 'flag_type');
CREATE TRIGGER trg_feature_flags_no_delete BEFORE DELETE ON app.feature_flags
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.feature_flag_changes (
  id              uuid        NOT NULL,
  feature_flag_id uuid        NOT NULL,
  flag_version    integer     NOT NULL,                   -- the version the flag has after this change
  old_enabled     boolean,                                -- NULL for creation
  new_enabled     boolean     NOT NULL,
  requested_by    uuid        NOT NULL,                   -- FK app.users added in 0004
  approved_by     uuid,                                   -- FK app.users added in 0004
  reason          text        NOT NULL,
  occurred_at     timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_feature_flag_changes PRIMARY KEY (id),
  CONSTRAINT fk_feature_flag_changes_feature_flag_id FOREIGN KEY (feature_flag_id) REFERENCES app.feature_flags (id),
  CONSTRAINT uq_feature_flag_changes_flag_version UNIQUE (feature_flag_id, flag_version),
  CONSTRAINT ck_feature_flag_changes_maker_checker CHECK (approved_by IS NULL OR approved_by <> requested_by),
  CONSTRAINT ck_feature_flag_changes_reason CHECK (length(reason) >= 3)
);
CREATE TRIGGER trg_feature_flag_changes_no_mutation BEFORE UPDATE OR DELETE ON app.feature_flag_changes
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_feature_flag_changes_no_truncate BEFORE TRUNCATE ON app.feature_flag_changes
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Deferred: every flag version (creation and each update) has exactly one change row, and flags that
-- require approval have an approver distinct from the requester.
CREATE FUNCTION app.feature_flags_require_change() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  c app.feature_flag_changes%ROWTYPE;
BEGIN
  SELECT * INTO c FROM app.feature_flag_changes
   WHERE feature_flag_id = NEW.id AND flag_version = NEW.version;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'feature flag % version % has no feature_flag_changes row', NEW.key, NEW.version
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF c.new_enabled <> NEW.enabled THEN
    RAISE EXCEPTION 'feature flag % change row does not match the flag value', NEW.key
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF NEW.requires_approval AND TG_OP = 'UPDATE' AND c.approved_by IS NULL THEN
    RAISE EXCEPTION 'feature flag % requires an approver (maker-checker)', NEW.key
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_feature_flags_require_change AFTER INSERT OR UPDATE ON app.feature_flags
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.feature_flags_require_change();

-- Seed: the PVO fundraising-authority policy switch (PRODUCT §6.3, beneficiary-verification §7).
-- Seeded rows use deterministic md5-derived UUIDs so every environment has the same ids; the seed change
-- row names the system actor (all-zero UUID) because no user exists at migration time.
INSERT INTO app.feature_flags (id, key, description, flag_type, enabled, requires_approval, legal_ref) VALUES
  (md5('feature_flag:campaign.individual_for_others.enabled')::uuid,
   'campaign.individual_for_others.enabled',
   'Allows individuals to raise funds for someone else. Disabled until counsel answers the PVO Act questions.',
   'POLICY', false, true, 'LR-046, LR-047, LR-048, LR-068, PD-27');
INSERT INTO app.feature_flag_changes (id, feature_flag_id, flag_version, old_enabled, new_enabled, requested_by, approved_by, reason, occurred_at) VALUES
  (md5('feature_flag_change:campaign.individual_for_others.enabled:1')::uuid,
   md5('feature_flag:campaign.individual_for_others.enabled')::uuid, 1, NULL, false,
   '00000000-0000-0000-0000-000000000000', NULL, 'seeded by migration', now());
