-- Stage 15 (ADR-047): notification engine. Every notification created from a domain event is recorded once
-- (notification_events, notifications), every channel delivery follows a database-guarded state machine with append-only
-- attempts and provider evidence, templates are versioned and maker-checker, preferences and consent records keep their
-- history, suppressions are explicit, dead letters wait for an operator. No production provider exists: provider
-- readiness can never be VERIFIED or PRODUCTION_ENABLED in this stage (CHECK). Retention of communication records is
-- LEGAL_REVIEW_REQUIRED (LR-012): rows carry a retention class and are never purged here.

-- +goose Up
-- +goose StatementBegin

-- ---- templates ------------------------------------------------------------------------------------------------------
CREATE TABLE app.notification_templates (
  id                uuid        NOT NULL,
  template_key      text        NOT NULL,
  category          text        NOT NULL,
  sensitive         boolean     NOT NULL DEFAULT false,
  allowed_variables text[]      NOT NULL DEFAULT ARRAY[]::text[],
  origin            text        NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_templates PRIMARY KEY (id),
  CONSTRAINT uq_notification_templates_key UNIQUE (template_key),
  CONSTRAINT ck_notification_templates_key CHECK (template_key ~ '^[A-Z][A-Z0-9_]{2,79}$'),
  CONSTRAINT ck_notification_templates_category CHECK (category IN ('SECURITY','TRANSACTIONAL','CAMPAIGN','VERIFICATION','PAYMENT',
    'WITHDRAWAL','PAYOUT','RISK','COMPLIANCE','SUPPORT','ADMINISTRATIVE','MARKETING')),
  CONSTRAINT ck_notification_templates_origin CHECK (origin IN ('STAGE_4_NOTICE','STAGE_5_NOTICE','STAGE_6_NOTICE','STAGE_15_BASELINE','STAFF'))
);
CREATE TRIGGER trg_notification_templates_identity BEFORE UPDATE ON app.notification_templates
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('template_key', 'category', 'created_at');
CREATE TRIGGER trg_notification_templates_no_delete BEFORE DELETE ON app.notification_templates FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('notification_template', '',                 'DRAFT'),
  ('notification_template', '',                 'ACTIVE'),          -- migration baseline only (trigger below)
  ('notification_template', 'DRAFT',            'PENDING_APPROVAL'),
  ('notification_template', 'DRAFT',            'RETIRED'),
  ('notification_template', 'PENDING_APPROVAL', 'APPROVED'),
  ('notification_template', 'PENDING_APPROVAL', 'RETIRED'),         -- rejected
  ('notification_template', 'APPROVED',         'ACTIVE'),
  ('notification_template', 'APPROVED',         'RETIRED'),
  ('notification_template', 'ACTIVE',           'RETIRED');

CREATE TABLE app.notification_template_versions (
  id                       uuid        NOT NULL,
  template_id              uuid        NOT NULL,
  version_no               integer     NOT NULL DEFAULT 1,
  locale                   text        NOT NULL DEFAULT 'en',
  status                   text        NOT NULL DEFAULT 'ACTIVE',
  email_subject            text,
  email_text               text,
  sms_text                 text,
  whatsapp_template_name   text,
  whatsapp_language        text,
  whatsapp_provider_status text        NOT NULL DEFAULT 'NOT_SUBMITTED',
  whatsapp_evidence_ref    text,
  inapp_title              text        NOT NULL,
  inapp_body               text        NOT NULL,
  content_hash             bytea       NOT NULL,
  approval_source          text        NOT NULL DEFAULT 'MIGRATION_BASELINE',
  created_by               uuid,
  submitted_by             uuid,
  submitted_at             timestamptz,
  approved_by              uuid,
  approved_at              timestamptz,
  decision_reason          text,
  activated_at             timestamptz,
  retired_at               timestamptz,
  version                  integer     NOT NULL DEFAULT 1,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_template_versions PRIMARY KEY (id),
  CONSTRAINT uq_notification_template_versions_no UNIQUE (template_id, version_no),
  CONSTRAINT fk_notification_template_versions_template FOREIGN KEY (template_id) REFERENCES app.notification_templates (id) ON DELETE RESTRICT,
  CONSTRAINT fk_notification_template_versions_created_by FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_notification_template_versions_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_notification_template_versions_status CHECK (status IN ('DRAFT','PENDING_APPROVAL','APPROVED','ACTIVE','RETIRED')),
  -- English is the engineering baseline; Shona and Ndebele are reserved for reviewed translations (none exist)
  CONSTRAINT ck_notification_template_versions_locale CHECK (locale IN ('en','sn','nd')),
  CONSTRAINT ck_notification_template_versions_source CHECK (approval_source IN ('MIGRATION_BASELINE','MAKER_CHECKER')),
  CONSTRAINT ck_notification_template_versions_maker_checker CHECK (approval_source = 'MIGRATION_BASELINE' OR status IN ('DRAFT','PENDING_APPROVAL')
    OR (status = 'RETIRED' AND approved_by IS NULL) OR (approved_by IS NOT NULL AND approved_by <> created_by AND approved_by <> submitted_by)),
  CONSTRAINT ck_notification_template_versions_whatsapp CHECK (whatsapp_provider_status IN ('NOT_SUBMITTED','SUBMITTED_TO_SIMULATOR',
    'SIMULATOR_ACCEPTED','SIMULATOR_REJECTED','SUBMITTED','APPROVED','REJECTED')),
  -- a provider approval needs the provider's evidence; no WhatsApp provider exists, so APPROVED is unreachable today
  CONSTRAINT ck_notification_template_versions_whatsapp_evidence CHECK (whatsapp_provider_status <> 'APPROVED' OR whatsapp_evidence_ref IS NOT NULL),
  CONSTRAINT ck_notification_template_versions_texts CHECK (char_length(inapp_title) BETWEEN 1 AND 200 AND char_length(inapp_body) BETWEEN 1 AND 2000
    AND (email_subject IS NULL OR char_length(email_subject) BETWEEN 1 AND 200) AND (email_text IS NULL OR char_length(email_text) BETWEEN 1 AND 8000)
    AND (sms_text IS NULL OR char_length(sms_text) BETWEEN 1 AND 480)),
  CONSTRAINT ck_notification_template_versions_hash CHECK (octet_length(content_hash) = 32)
);
-- one ACTIVE version per template and locale
CREATE UNIQUE INDEX uq_notification_template_versions_active ON app.notification_template_versions (template_id, locale) WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX uq_notification_template_versions_open ON app.notification_template_versions (template_id, locale)
  WHERE status IN ('DRAFT','PENDING_APPROVAL','APPROVED');
CREATE TRIGGER trg_notification_template_versions_guard_status BEFORE INSERT OR UPDATE OF status ON app.notification_template_versions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('notification_template');
CREATE TRIGGER trg_notification_template_versions_updated_at BEFORE UPDATE ON app.notification_template_versions FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_notification_template_versions_no_delete BEFORE DELETE ON app.notification_template_versions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- content is frozen once a version leaves DRAFT; a baseline version can only be inserted by the migrator
CREATE FUNCTION app.notification_template_versions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.approval_source = 'MIGRATION_BASELINE' AND current_user <> 'fundzim_migrator' THEN
      RAISE EXCEPTION 'baseline template versions are created by migrations only' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.approval_source = 'MAKER_CHECKER' AND NEW.status <> 'DRAFT' THEN
      RAISE EXCEPTION 'a template version starts as DRAFT' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;
  IF OLD.status <> 'DRAFT' AND (NEW.email_subject IS DISTINCT FROM OLD.email_subject OR NEW.email_text IS DISTINCT FROM OLD.email_text
      OR NEW.sms_text IS DISTINCT FROM OLD.sms_text OR NEW.inapp_title IS DISTINCT FROM OLD.inapp_title OR NEW.inapp_body IS DISTINCT FROM OLD.inapp_body
      OR NEW.whatsapp_template_name IS DISTINCT FROM OLD.whatsapp_template_name OR NEW.whatsapp_language IS DISTINCT FROM OLD.whatsapp_language
      OR NEW.content_hash IS DISTINCT FROM OLD.content_hash OR NEW.locale IS DISTINCT FROM OLD.locale) THEN
    RAISE EXCEPTION 'template content is immutable after submission' USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.template_id <> OLD.template_id OR NEW.version_no <> OLD.version_no OR NEW.approval_source <> OLD.approval_source
     OR NEW.created_by IS DISTINCT FROM OLD.created_by OR NEW.created_at <> OLD.created_at THEN
    RAISE EXCEPTION 'template version identity is immutable' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_notification_template_versions_guard BEFORE INSERT OR UPDATE ON app.notification_template_versions
  FOR EACH ROW EXECUTE FUNCTION app.notification_template_versions_guard();

-- ---- events and notifications -------------------------------------------------------------------------------------------
-- One row per consumed domain event (the outbox event id): creation is exactly once per event.
CREATE TABLE app.notification_events (
  id              uuid        NOT NULL,
  source_event_id uuid        NOT NULL,
  event_type      text        NOT NULL,
  aggregate_type  text        NOT NULL,
  aggregate_id    uuid        NOT NULL,
  correlation_id  text,
  occurred_at     timestamptz NOT NULL,
  received_at     timestamptz NOT NULL DEFAULT now(),
  outcome         text        NOT NULL,
  synthetic       boolean     NOT NULL DEFAULT false,
  CONSTRAINT pk_notification_events PRIMARY KEY (id),
  CONSTRAINT uq_notification_events_source UNIQUE (source_event_id),
  CONSTRAINT ck_notification_events_outcome CHECK (outcome IN ('CREATED','NO_RECIPIENTS','NOT_NOTIFIED'))
);
CREATE INDEX ix_notification_events_received ON app.notification_events (received_at);
CREATE TRIGGER trg_notification_events_no_mutation BEFORE UPDATE OR DELETE ON app.notification_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.notifications (
  id                  uuid        NOT NULL,
  event_id            uuid,
  template_id         uuid        NOT NULL,
  template_version_id uuid        NOT NULL,
  template_key        text        NOT NULL,
  category            text        NOT NULL,
  recipient_user_id   uuid        NOT NULL,
  recipient_kind      text        NOT NULL,
  synthetic           boolean     NOT NULL DEFAULT false,
  priority            text        NOT NULL DEFAULT 'NORMAL',
  related_type        text,
  related_id          uuid,
  deep_link           text,
  variables           jsonb       NOT NULL DEFAULT '{}'::jsonb,
  in_app              boolean     NOT NULL DEFAULT true,
  inapp_title         text        NOT NULL,
  inapp_body          text        NOT NULL,
  read_at             timestamptz,
  dedupe_key          text        NOT NULL,
  retention_class     text        NOT NULL DEFAULT 'COMMUNICATION_RECORD_LEGAL_REVIEW_REQUIRED',
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notifications PRIMARY KEY (id),
  -- idempotency: one notification per source event, recipient and template (or per explicit dedupe key)
  CONSTRAINT uq_notifications_dedupe UNIQUE (dedupe_key, recipient_user_id, template_key),
  CONSTRAINT fk_notifications_event FOREIGN KEY (event_id) REFERENCES app.notification_events (id) ON DELETE RESTRICT,
  CONSTRAINT fk_notifications_template_version FOREIGN KEY (template_version_id) REFERENCES app.notification_template_versions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_notifications_recipient FOREIGN KEY (recipient_user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_notifications_recipient_kind CHECK (recipient_kind IN ('USER','STAFF')),
  CONSTRAINT ck_notifications_priority CHECK (priority IN ('NORMAL','HIGH')),
  CONSTRAINT ck_notifications_category CHECK (category IN ('SECURITY','TRANSACTIONAL','CAMPAIGN','VERIFICATION','PAYMENT','WITHDRAWAL','PAYOUT',
    'RISK','COMPLIANCE','SUPPORT','ADMINISTRATIVE','MARKETING')),
  -- deep links stay inside the application (relative paths only)
  CONSTRAINT ck_notifications_deep_link CHECK (deep_link IS NULL OR deep_link ~ '^/(dashboard|admin|campaigns|settings)(/[A-Za-z0-9_\-]+)*(\?[A-Za-z0-9_=&\-]*)?$'),
  CONSTRAINT ck_notifications_texts CHECK (char_length(inapp_title) BETWEEN 1 AND 200 AND char_length(inapp_body) BETWEEN 1 AND 2000)
);
CREATE INDEX ix_notifications_inbox ON app.notifications (recipient_user_id, created_at DESC, id DESC) WHERE in_app;
CREATE INDEX ix_notifications_unread ON app.notifications (recipient_user_id) WHERE in_app AND read_at IS NULL;
CREATE INDEX ix_notifications_event ON app.notifications (event_id);
CREATE TRIGGER trg_notifications_identity BEFORE UPDATE ON app.notifications
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('event_id', 'template_id', 'template_version_id', 'template_key', 'category',
    'recipient_user_id', 'recipient_kind', 'synthetic', 'related_type', 'related_id', 'deep_link', 'variables', 'in_app', 'inapp_title', 'inapp_body',
    'dedupe_key', 'created_at');
CREATE TRIGGER trg_notifications_no_delete BEFORE DELETE ON app.notifications FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- deliveries ---------------------------------------------------------------------------------------------------------
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('notification_delivery', '',                 'QUEUED'),
  ('notification_delivery', '',                 'SUPPRESSED'),
  ('notification_delivery', '',                 'DELIVERED'),        -- in-app: stored = delivered
  ('notification_delivery', 'QUEUED',           'PROCESSING'),
  ('notification_delivery', 'QUEUED',           'SUPPRESSED'),
  ('notification_delivery', 'QUEUED',           'CANCELLED'),
  ('notification_delivery', 'PROCESSING',       'SUBMITTED'),
  ('notification_delivery', 'PROCESSING',       'ACCEPTED'),
  ('notification_delivery', 'PROCESSING',       'DELIVERED'),
  ('notification_delivery', 'PROCESSING',       'FAILED_RETRYABLE'),
  ('notification_delivery', 'PROCESSING',       'FAILED_PERMANENT'),
  ('notification_delivery', 'PROCESSING',       'SUPPRESSED'),
  ('notification_delivery', 'PROCESSING',       'UNKNOWN'),
  ('notification_delivery', 'SUBMITTED',        'ACCEPTED'),
  ('notification_delivery', 'SUBMITTED',        'DELIVERED'),
  ('notification_delivery', 'SUBMITTED',        'READ'),
  ('notification_delivery', 'SUBMITTED',        'FAILED_PERMANENT'),
  ('notification_delivery', 'ACCEPTED',         'DELIVERED'),
  ('notification_delivery', 'ACCEPTED',         'READ'),
  ('notification_delivery', 'ACCEPTED',         'FAILED_PERMANENT'),
  ('notification_delivery', 'DELIVERED',        'READ'),
  ('notification_delivery', 'FAILED_RETRYABLE', 'PROCESSING'),
  ('notification_delivery', 'FAILED_RETRYABLE', 'FAILED_PERMANENT'),
  ('notification_delivery', 'FAILED_RETRYABLE', 'SUPPRESSED'),
  ('notification_delivery', 'FAILED_RETRYABLE', 'CANCELLED'),
  ('notification_delivery', 'UNKNOWN',          'ACCEPTED'),
  ('notification_delivery', 'UNKNOWN',          'DELIVERED'),
  ('notification_delivery', 'UNKNOWN',          'READ'),
  ('notification_delivery', 'UNKNOWN',          'FAILED_RETRYABLE'),
  ('notification_delivery', 'UNKNOWN',          'FAILED_PERMANENT'),
  ('notification_delivery', 'UNKNOWN',          'QUEUED'),           -- operator replay after review
  ('notification_delivery', 'FAILED_PERMANENT', 'QUEUED'),           -- operator replay after review
  ('notification_delivery', 'FAILED_PERMANENT', 'CANCELLED');

CREATE TABLE app.notification_deliveries (
  id                  uuid        NOT NULL,
  notification_id     uuid        NOT NULL,
  channel             text        NOT NULL,
  status              text        NOT NULL,
  provider_code       text,
  destination_masked  text,
  destination_hash    bytea,
  decision            text        NOT NULL,
  suppression_reason  text,
  attempt_count       integer     NOT NULL DEFAULT 0,
  max_attempts        integer     NOT NULL DEFAULT 6,
  next_attempt_at     timestamptz,
  lease_until         timestamptz,
  provider_message_id text,
  submitted_at        timestamptz,
  accepted_at         timestamptz,
  delivered_at        timestamptz,
  read_at             timestamptz,
  failed_at           timestamptz,
  failure_code        text,
  conflicting_evidence boolean    NOT NULL DEFAULT false,
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_deliveries PRIMARY KEY (id),
  CONSTRAINT uq_notification_deliveries_channel UNIQUE (notification_id, channel),
  CONSTRAINT uq_notification_deliveries_provider_message UNIQUE (provider_code, provider_message_id),
  CONSTRAINT fk_notification_deliveries_notification FOREIGN KEY (notification_id) REFERENCES app.notifications (id) ON DELETE RESTRICT,
  CONSTRAINT ck_notification_deliveries_channel CHECK (channel IN ('EMAIL','SMS','WHATSAPP','IN_APP')),
  CONSTRAINT ck_notification_deliveries_status CHECK (status IN ('QUEUED','PROCESSING','SUBMITTED','ACCEPTED','DELIVERED','READ',
    'FAILED_RETRYABLE','FAILED_PERMANENT','SUPPRESSED','CANCELLED','UNKNOWN')),
  CONSTRAINT ck_notification_deliveries_suppressed CHECK ((status = 'SUPPRESSED') = (suppression_reason IS NOT NULL)),
  CONSTRAINT ck_notification_deliveries_suppression_reason CHECK (suppression_reason IS NULL OR suppression_reason IN ('PREFERENCE_DISABLED',
    'CONSENT_MISSING','CONSENT_WITHDRAWN','CONSENT_EXPIRED','CONSENT_RECONFIRMATION_REQUIRED','MARKETING_DISABLED','CONTACT_MISSING',
    'CONTACT_UNVERIFIED','DESTINATION_SUPPRESSED','RECIPIENT_INACTIVE','SYNTHETIC_SOURCE','LIVE_MESSAGING_DISABLED','CHANNEL_DISABLED',
    'PROVIDER_NOT_CONFIGURED','TEMPLATE_CHANNEL_MISSING','TEMPLATE_NOT_PROVIDER_APPROVED')),
  CONSTRAINT ck_notification_deliveries_in_app CHECK (channel <> 'IN_APP' OR (provider_code IS NULL AND status IN ('DELIVERED','READ','SUPPRESSED'))),
  CONSTRAINT ck_notification_deliveries_attempts CHECK (attempt_count >= 0 AND max_attempts BETWEEN 1 AND 20),
  CONSTRAINT ck_notification_deliveries_hash CHECK (destination_hash IS NULL OR octet_length(destination_hash) = 32)
);
CREATE INDEX ix_notification_deliveries_due ON app.notification_deliveries (next_attempt_at) WHERE status IN ('QUEUED','FAILED_RETRYABLE');
CREATE INDEX ix_notification_deliveries_processing ON app.notification_deliveries (lease_until) WHERE status = 'PROCESSING';
CREATE INDEX ix_notification_deliveries_unknown ON app.notification_deliveries (updated_at) WHERE status = 'UNKNOWN';
CREATE INDEX ix_notification_deliveries_status ON app.notification_deliveries (status, channel);
CREATE TRIGGER trg_notification_deliveries_guard_status BEFORE INSERT OR UPDATE OF status ON app.notification_deliveries
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('notification_delivery');
CREATE TRIGGER trg_notification_deliveries_identity BEFORE UPDATE ON app.notification_deliveries
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('notification_id', 'channel', 'created_at');
CREATE TRIGGER trg_notification_deliveries_updated_at BEFORE UPDATE ON app.notification_deliveries FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_notification_deliveries_no_delete BEFORE DELETE ON app.notification_deliveries FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- every provider call (append-only): no message body, a hash of what was sent
CREATE TABLE app.notification_attempts (
  id                  uuid        NOT NULL,
  delivery_id         uuid        NOT NULL,
  attempt_no          integer     NOT NULL,
  provider_code       text        NOT NULL,
  started_at          timestamptz NOT NULL,
  finished_at         timestamptz NOT NULL,
  outcome             text        NOT NULL,
  error_code          text,
  provider_message_id text,
  content_hash        bytea,
  latency_ms          integer     NOT NULL,
  CONSTRAINT pk_notification_attempts PRIMARY KEY (id),
  CONSTRAINT uq_notification_attempts_no UNIQUE (delivery_id, attempt_no),
  CONSTRAINT fk_notification_attempts_delivery FOREIGN KEY (delivery_id) REFERENCES app.notification_deliveries (id) ON DELETE RESTRICT,
  CONSTRAINT ck_notification_attempts_outcome CHECK (outcome IN ('SUBMITTED','ACCEPTED','DELIVERED','TEMPORARY_FAILURE','RATE_LIMITED',
    'PERMANENT_FAILURE','TIMEOUT','SUPPRESSED','STATUS_CHECK')),
  CONSTRAINT ck_notification_attempts_error CHECK (error_code IS NULL OR error_code ~ '^[A-Z][A-Z0-9_]{1,63}$'),
  CONSTRAINT ck_notification_attempts_latency CHECK (latency_ms >= 0)
);
CREATE INDEX ix_notification_attempts_finished ON app.notification_attempts (finished_at);
CREATE TRIGGER trg_notification_attempts_no_mutation BEFORE UPDATE OR DELETE ON app.notification_attempts FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- every verified provider callback (append-only evidence; duplicates rejected by the unique key)
CREATE TABLE app.notification_provider_events (
  id                  uuid        NOT NULL,
  provider_code       text        NOT NULL,
  provider_event_id   text        NOT NULL,
  provider_message_id text        NOT NULL,
  delivery_id         uuid,
  reported_status     text        NOT NULL,
  occurred_at         timestamptz NOT NULL,
  received_at         timestamptz NOT NULL DEFAULT now(),
  applied             boolean     NOT NULL,
  not_applied_reason  text,
  applied_at          timestamptz,
  CONSTRAINT pk_notification_provider_events PRIMARY KEY (id),
  CONSTRAINT uq_notification_provider_events UNIQUE (provider_code, provider_event_id),
  CONSTRAINT fk_notification_provider_events_delivery FOREIGN KEY (delivery_id) REFERENCES app.notification_deliveries (id) ON DELETE RESTRICT,
  CONSTRAINT ck_notification_provider_events_status CHECK (reported_status IN ('SUBMITTED','ACCEPTED','DELIVERED','READ','BOUNCED','REJECTED',
    'UNDELIVERABLE','FAILED','TEMPORARY_FAILURE')),
  CONSTRAINT ck_notification_provider_events_reason CHECK (applied = (not_applied_reason IS NULL))
);
CREATE INDEX ix_notification_provider_events_delivery ON app.notification_provider_events (delivery_id);
-- evidence is immutable; the one permitted change is applying a callback that arrived while the send was in flight
CREATE FUNCTION app.notification_provider_events_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.not_applied_reason = 'IN_FLIGHT' AND NEW.applied AND NEW.not_applied_reason IS NULL AND NEW.applied_at IS NOT NULL
     AND NEW.id = OLD.id AND NEW.provider_code = OLD.provider_code AND NEW.provider_event_id = OLD.provider_event_id
     AND NEW.provider_message_id = OLD.provider_message_id AND NEW.delivery_id IS NOT DISTINCT FROM OLD.delivery_id
     AND NEW.reported_status = OLD.reported_status AND NEW.occurred_at = OLD.occurred_at AND NEW.received_at = OLD.received_at THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'provider evidence is append-only' USING ERRCODE = 'check_violation';
END $$;
CREATE TRIGGER trg_notification_provider_events_guard BEFORE UPDATE ON app.notification_provider_events
  FOR EACH ROW EXECUTE FUNCTION app.notification_provider_events_guard();
CREATE TRIGGER trg_notification_provider_events_no_delete BEFORE DELETE ON app.notification_provider_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- webhooks refused before any parsing of their content (signature, timestamp, payload): counted, never applied
CREATE TABLE app.notification_webhook_rejections (
  id            uuid        NOT NULL,
  provider_code text        NOT NULL,
  reason        text        NOT NULL,
  received_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_webhook_rejections PRIMARY KEY (id),
  CONSTRAINT ck_notification_webhook_rejections_reason CHECK (reason IN ('SIGNATURE_INVALID','TIMESTAMP_OUT_OF_WINDOW','PAYLOAD_INVALID',
    'UNKNOWN_PROVIDER','UNKNOWN_MESSAGE'))
);
CREATE INDEX ix_notification_webhook_rejections_received ON app.notification_webhook_rejections (received_at);
CREATE TRIGGER trg_notification_webhook_rejections_no_mutation BEFORE UPDATE OR DELETE ON app.notification_webhook_rejections FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- preferences ----------------------------------------------------------------------------------------------------
CREATE TABLE app.notification_preferences (
  user_id    uuid        NOT NULL,
  category   text        NOT NULL,
  channel    text        NOT NULL,
  enabled    boolean     NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_preferences PRIMARY KEY (user_id, category, channel),
  CONSTRAINT fk_notification_preferences_user FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_notification_preferences_channel CHECK (channel IN ('EMAIL','SMS','WHATSAPP','IN_APP')),
  CONSTRAINT ck_notification_preferences_category CHECK (category IN ('SECURITY','TRANSACTIONAL','CAMPAIGN','VERIFICATION','PAYMENT','WITHDRAWAL',
    'PAYOUT','RISK','COMPLIANCE','SUPPORT','ADMINISTRATIVE','MARKETING')),
  -- security notices on e-mail and in-app cannot be switched off
  CONSTRAINT ck_notification_preferences_mandatory CHECK (enabled OR NOT (category = 'SECURITY' AND channel IN ('EMAIL','IN_APP')))
);
CREATE TABLE app.notification_preference_events (
  id         uuid        NOT NULL,
  user_id    uuid        NOT NULL,
  category   text        NOT NULL,
  channel    text        NOT NULL,
  enabled    boolean     NOT NULL,
  changed_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_preference_events PRIMARY KEY (id),
  CONSTRAINT fk_notification_preference_events_user FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT
);
CREATE INDEX ix_notification_preference_events_user ON app.notification_preference_events (user_id, changed_at);
CREATE TRIGGER trg_notification_preference_events_no_mutation BEFORE UPDATE OR DELETE ON app.notification_preference_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- consent --------------------------------------------------------------------------------------------------------
-- NOT_REQUESTED is the absence of a row. Definitions are engineering placeholders pending LR-023 (LEGAL_REVIEW_REQUIRED).
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('communication_consent', '',                        'GRANTED'),
  ('communication_consent', '',                        'WITHDRAWN'),
  ('communication_consent', 'GRANTED',                 'WITHDRAWN'),
  ('communication_consent', 'GRANTED',                 'EXPIRED'),
  ('communication_consent', 'GRANTED',                 'REQUIRES_RECONFIRMATION'),
  ('communication_consent', 'WITHDRAWN',               'GRANTED'),
  ('communication_consent', 'EXPIRED',                 'GRANTED'),
  ('communication_consent', 'EXPIRED',                 'WITHDRAWN'),
  ('communication_consent', 'REQUIRES_RECONFIRMATION', 'GRANTED'),
  ('communication_consent', 'REQUIRES_RECONFIRMATION', 'WITHDRAWN');

CREATE TABLE app.communication_consents (
  id             uuid        NOT NULL,
  user_id        uuid        NOT NULL,
  channel        text        NOT NULL,
  purpose        text        NOT NULL,
  status         text        NOT NULL,
  source         text        NOT NULL,
  policy_version text        NOT NULL,
  evidence_ref   text        NOT NULL,
  granted_at     timestamptz,
  withdrawn_at   timestamptz,
  expires_at     timestamptz,
  version        integer     NOT NULL DEFAULT 1,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_communication_consents PRIMARY KEY (id),
  CONSTRAINT uq_communication_consents UNIQUE (user_id, channel, purpose),
  CONSTRAINT fk_communication_consents_user FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_communication_consents_channel CHECK (channel IN ('EMAIL','SMS','WHATSAPP')),
  CONSTRAINT ck_communication_consents_purpose CHECK (purpose IN ('SERVICE_MESSAGES','MARKETING')),
  CONSTRAINT ck_communication_consents_status CHECK (status IN ('GRANTED','WITHDRAWN','EXPIRED','REQUIRES_RECONFIRMATION')),
  -- consent is recorded only from the person's own action; staff and systems never grant it
  CONSTRAINT ck_communication_consents_source CHECK (source IN ('USER_SETTINGS','USER_UNSUBSCRIBE','PROVIDER_OPT_OUT','SYSTEM_EXPIRY','POLICY_CHANGE')),
  CONSTRAINT ck_communication_consents_grant_source CHECK (status <> 'GRANTED' OR source = 'USER_SETTINGS'),
  CONSTRAINT ck_communication_consents_granted CHECK (status <> 'GRANTED' OR granted_at IS NOT NULL),
  CONSTRAINT ck_communication_consents_evidence CHECK (char_length(evidence_ref) BETWEEN 1 AND 200)
);
CREATE TRIGGER trg_communication_consents_guard_status BEFORE INSERT OR UPDATE OF status ON app.communication_consents
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('communication_consent');
CREATE TRIGGER trg_communication_consents_identity BEFORE UPDATE ON app.communication_consents
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('user_id', 'channel', 'purpose', 'created_at');
CREATE TRIGGER trg_communication_consents_updated_at BEFORE UPDATE ON app.communication_consents FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_communication_consents_no_delete BEFORE DELETE ON app.communication_consents FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.communication_consent_events (
  id             uuid        NOT NULL,
  consent_id     uuid        NOT NULL,
  from_status    text,
  to_status      text        NOT NULL,
  source         text        NOT NULL,
  policy_version text        NOT NULL,
  evidence_ref   text        NOT NULL,
  occurred_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_communication_consent_events PRIMARY KEY (id),
  CONSTRAINT fk_communication_consent_events_consent FOREIGN KEY (consent_id) REFERENCES app.communication_consents (id) ON DELETE RESTRICT
);
CREATE INDEX ix_communication_consent_events_consent ON app.communication_consent_events (consent_id, occurred_at);
CREATE TRIGGER trg_communication_consent_events_no_mutation BEFORE UPDATE OR DELETE ON app.communication_consent_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- suppressions ---------------------------------------------------------------------------------------------------
CREATE TABLE app.notification_suppressions (
  id               uuid        NOT NULL,
  channel          text        NOT NULL,
  destination_hash bytea       NOT NULL,
  user_id          uuid,
  reason           text        NOT NULL,
  source           text        NOT NULL,
  created_by       uuid,
  created_at       timestamptz NOT NULL DEFAULT now(),
  lifted_at        timestamptz,
  lifted_by        uuid,
  lift_reason      text,
  CONSTRAINT pk_notification_suppressions PRIMARY KEY (id),
  CONSTRAINT fk_notification_suppressions_user FOREIGN KEY (user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_notification_suppressions_channel CHECK (channel IN ('EMAIL','SMS','WHATSAPP')),
  CONSTRAINT ck_notification_suppressions_reason CHECK (reason IN ('USER_OPT_OUT','INVALID_DESTINATION','HARD_BOUNCE','PROVIDER_REJECTION',
    'SECURITY_RESTRICTION','LEGAL_RESTRICTION','CONTACT_UNVERIFIED')),
  CONSTRAINT ck_notification_suppressions_source CHECK (source IN ('USER','PROVIDER','STAFF','SYSTEM')),
  CONSTRAINT ck_notification_suppressions_hash CHECK (octet_length(destination_hash) = 32),
  CONSTRAINT ck_notification_suppressions_lift CHECK ((lifted_at IS NULL) = (lifted_by IS NULL) AND (lifted_at IS NULL) = (lift_reason IS NULL))
);
CREATE UNIQUE INDEX uq_notification_suppressions_active ON app.notification_suppressions (channel, destination_hash) WHERE lifted_at IS NULL;
CREATE TRIGGER trg_notification_suppressions_identity BEFORE UPDATE ON app.notification_suppressions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('channel', 'destination_hash', 'user_id', 'reason', 'source', 'created_by', 'created_at');
CREATE TRIGGER trg_notification_suppressions_no_delete BEFORE DELETE ON app.notification_suppressions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- dead letters ---------------------------------------------------------------------------------------------------
CREATE TABLE app.notification_dead_letters (
  id               uuid        NOT NULL,
  delivery_id      uuid        NOT NULL,
  failure_category text        NOT NULL,
  attempt_count    integer     NOT NULL,
  last_error       text,
  next_action      text        NOT NULL DEFAULT 'OPERATOR_REVIEW',
  review_status    text        NOT NULL DEFAULT 'OPEN',
  reviewed_by      uuid,
  reviewed_at      timestamptz,
  review_reason    text,
  replay_count     integer     NOT NULL DEFAULT 0,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_dead_letters PRIMARY KEY (id),
  CONSTRAINT fk_notification_dead_letters_delivery FOREIGN KEY (delivery_id) REFERENCES app.notification_deliveries (id) ON DELETE RESTRICT,
  CONSTRAINT fk_notification_dead_letters_reviewer FOREIGN KEY (reviewed_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_notification_dead_letters_category CHECK (failure_category IN ('RETRIES_EXHAUSTED','PERMANENT_FAILURE','OUTCOME_UNKNOWN',
    'RENDER_FAILURE')),
  CONSTRAINT ck_notification_dead_letters_review CHECK (review_status IN ('OPEN','REPLAYED','DISCARDED')),
  CONSTRAINT ck_notification_dead_letters_reviewed CHECK ((review_status = 'OPEN') = (reviewed_at IS NULL)),
  CONSTRAINT ck_notification_dead_letters_error CHECK (last_error IS NULL OR last_error ~ '^[A-Z][A-Z0-9_]{1,63}$')
);
-- one open dead letter per delivery (a replay that fails again opens a new one)
CREATE UNIQUE INDEX uq_notification_dead_letters_open ON app.notification_dead_letters (delivery_id) WHERE review_status = 'OPEN';
CREATE TRIGGER trg_notification_dead_letters_identity BEFORE UPDATE ON app.notification_dead_letters
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('delivery_id', 'failure_category', 'attempt_count', 'last_error', 'created_at');
CREATE TRIGGER trg_notification_dead_letters_updated_at BEFORE UPDATE ON app.notification_dead_letters FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_notification_dead_letters_no_delete BEFORE DELETE ON app.notification_dead_letters FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---- providers ------------------------------------------------------------------------------------------------------
CREATE TABLE app.notification_provider_configs (
  id                 uuid        NOT NULL,
  provider_code      text        NOT NULL,
  channel            text        NOT NULL,
  display_name       text        NOT NULL,
  environment        text        NOT NULL DEFAULT 'TEST',
  readiness          text        NOT NULL,
  simulated          boolean     NOT NULL,
  capabilities       jsonb       NOT NULL DEFAULT '{}'::jsonb,
  credential_ref     text,
  known_limitations  text        NOT NULL,
  last_health_at     timestamptz,
  last_health_status text,
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_provider_configs PRIMARY KEY (id),
  CONSTRAINT uq_notification_provider_configs_code UNIQUE (provider_code),
  CONSTRAINT ck_notification_provider_configs_code CHECK (provider_code ~ '^[a-z][a-z0-9_]{2,40}$'),
  CONSTRAINT ck_notification_provider_configs_channel CHECK (channel IN ('EMAIL','SMS','WHATSAPP')),
  CONSTRAINT ck_notification_provider_configs_readiness CHECK (readiness IN ('NOT_SELECTED','NOT_CONFIGURED','CONFIGURED_TEST',
    'CERTIFICATION_PENDING','VERIFIED','PRODUCTION_ENABLED')),
  -- no provider is selected (ADR-047): neither verification nor production may be claimed in this stage
  CONSTRAINT ck_notification_provider_configs_no_production_stage15 CHECK (readiness NOT IN ('VERIFIED','PRODUCTION_ENABLED')),
  CONSTRAINT ck_notification_provider_configs_environment CHECK (environment IN ('TEST','PRODUCTION')),
  CONSTRAINT ck_notification_provider_configs_test_only_stage15 CHECK (environment = 'TEST'),
  CONSTRAINT ck_notification_provider_configs_credential CHECK (credential_ref IS NULL OR credential_ref LIKE 'secretref://fundzim/%'),
  CONSTRAINT ck_notification_provider_configs_health CHECK (last_health_status IS NULL OR last_health_status IN ('HEALTHY','DEGRADED','UNAVAILABLE','UNKNOWN'))
);
CREATE TRIGGER trg_notification_provider_configs_updated_at BEFORE UPDATE ON app.notification_provider_configs FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_notification_provider_configs_no_delete BEFORE DELETE ON app.notification_provider_configs FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.notification_provider_configs (id, provider_code, channel, display_name, readiness, simulated, capabilities, known_limitations) VALUES
  (md5('notification_provider:email_simulator')::uuid, 'email_simulator', 'EMAIL', 'E-mail simulator (TEST_ONLY)', 'CONFIGURED_TEST', true,
   '{"html": true, "delivery_status": true, "webhooks": true, "read_receipts": false}', 'Sends nothing. Deterministic scenarios from the destination tag (+sim-<scenario>).'),
  (md5('notification_provider:email_dev_smtp')::uuid, 'email_dev_smtp', 'EMAIL', 'Development SMTP (Mailpit)', 'CONFIGURED_TEST', false,
   '{"html": false, "delivery_status": false, "webhooks": false, "read_receipts": false}', 'Local Mailpit only; refused outside development and test. Acceptance by the SMTP server is not delivery.'),
  (md5('notification_provider:email_production')::uuid, 'email_production', 'EMAIL', 'Production e-mail provider', 'NOT_SELECTED', false,
   '{}', 'No provider selected (LR-010, LR-011, PROVIDER_CONFIRMATION_REQUIRED).'),
  (md5('notification_provider:sms_simulator')::uuid, 'sms_simulator', 'SMS', 'SMS simulator (TEST_ONLY)', 'CONFIGURED_TEST', true,
   '{"unicode": true, "delivery_status": true, "webhooks": true}', 'Sends nothing. Scenarios from the destination number''s last two digits.'),
  (md5('notification_provider:sms_dev_mailpit')::uuid, 'sms_dev_mailpit', 'SMS', 'Development SMS (to Mailpit)', 'CONFIGURED_TEST', false,
   '{"unicode": true, "delivery_status": false, "webhooks": false}', 'Turns an SMS into a Mailpit e-mail; development and test only.'),
  (md5('notification_provider:sms_production')::uuid, 'sms_production', 'SMS', 'Production SMS provider', 'NOT_SELECTED', false,
   '{}', 'No provider selected; network coverage, sender IDs and opt-out handling are PROVIDER_CONFIRMATION_REQUIRED.'),
  (md5('notification_provider:whatsapp_simulator')::uuid, 'whatsapp_simulator', 'WHATSAPP', 'WhatsApp simulator (TEST_ONLY)', 'CONFIGURED_TEST', true,
   '{"template_messages": true, "free_form": false, "read_receipts": true, "webhooks": true}', 'Sends nothing. Template messages only.'),
  (md5('notification_provider:whatsapp_production')::uuid, 'whatsapp_production', 'WHATSAPP', 'WhatsApp Business provider', 'NOT_SELECTED', false,
   '{}', 'No provider or business account; sender verification, template approval, messaging limits and consent are open (LR-023).');

-- TEST_ONLY simulator state (what each simulated message will report, and when)
CREATE TABLE app.notification_sim_messages (
  id                  uuid        NOT NULL,
  provider_code       text        NOT NULL,
  provider_message_id text        NOT NULL,
  idempotency_key     text        NOT NULL,
  scenario            text        NOT NULL,
  pending_callbacks   jsonb       NOT NULL DEFAULT '[]'::jsonb,
  next_callback_at    timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_sim_messages PRIMARY KEY (id),
  CONSTRAINT uq_notification_sim_messages_message UNIQUE (provider_code, provider_message_id),
  CONSTRAINT uq_notification_sim_messages_idempotency UNIQUE (provider_code, idempotency_key),
  CONSTRAINT ck_notification_sim_messages_provider CHECK (provider_code IN ('email_simulator','sms_simulator','whatsapp_simulator'))
);
CREATE INDEX ix_notification_sim_messages_due ON app.notification_sim_messages (next_callback_at) WHERE next_callback_at IS NOT NULL;

-- delivery worker heartbeats (Stage 14 health: a worker that stops sweeping is visible)
CREATE TABLE app.notification_worker_heartbeats (
  worker_id     text        NOT NULL,
  last_seen_at  timestamptz NOT NULL,
  last_claimed  integer     NOT NULL DEFAULT 0,
  CONSTRAINT pk_notification_worker_heartbeats PRIMARY KEY (worker_id),
  CONSTRAINT ck_notification_worker_heartbeats_id CHECK (char_length(worker_id) BETWEEN 1 AND 120)
);

-- ---- permissions ----------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:communications.view')::uuid, 'communications.view', 'View the communications dashboard, deliveries (masked destinations), providers, dead letters and reports', true, false),
  (md5('permission:communications.template.request')::uuid, 'communications.template.request', 'Draft and submit notification template versions (maker)', true, true),
  (md5('permission:communications.template.approve')::uuid, 'communications.template.approve', 'Approve, reject and activate notification template versions (checker)', true, true),
  (md5('permission:communications.replay')::uuid, 'communications.replay', 'Replay or discard dead-lettered deliveries after review', true, true),
  (md5('permission:communications.suppression.manage')::uuid, 'communications.suppression.manage', 'Add or lift delivery suppressions', true, true),
  (md5('permission:communications.consent.view')::uuid, 'communications.consent.view', 'View consent records and their history', true, false);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES
  ('ADMIN','communications.view'), ('SUPPORT','communications.view'), ('COMPLIANCE','communications.view'), ('SECURITY_ADMIN','communications.view'),
  ('ADMIN','communications.template.request'), ('COMPLIANCE','communications.template.approve'),
  ('ADMIN','communications.replay'), ('SECURITY_ADMIN','communications.replay'),
  ('ADMIN','communications.suppression.manage'), ('SECURITY_ADMIN','communications.suppression.manage'),
  ('COMPLIANCE','communications.consent.view')
) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

-- ---- baseline templates (existing Stage 4–6 notice texts verbatim; new texts are engineering baseline copy) ----------
INSERT INTO app.notification_templates (id, template_key, category, sensitive, allowed_variables, origin) VALUES
  (md5('notification_template:CAMPAIGN_CREATED')::uuid, 'CAMPAIGN_CREATED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_SUBMITTED')::uuid, 'CAMPAIGN_SUBMITTED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_CHANGES_REQUESTED')::uuid, 'CAMPAIGN_CHANGES_REQUESTED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_APPROVED')::uuid, 'CAMPAIGN_APPROVED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_REJECTED')::uuid, 'CAMPAIGN_REJECTED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_PUBLISHED')::uuid, 'CAMPAIGN_PUBLISHED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_PAUSED')::uuid, 'CAMPAIGN_PAUSED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_SUSPENDED')::uuid, 'CAMPAIGN_SUSPENDED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_REACTIVATED')::uuid, 'CAMPAIGN_REACTIVATED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:CAMPAIGN_COMPLETED')::uuid, 'CAMPAIGN_COMPLETED', 'CAMPAIGN', false, ARRAY[]::text[], 'STAGE_6_NOTICE'),
  (md5('notification_template:KYC_CASE_SUBMITTED')::uuid, 'KYC_CASE_SUBMITTED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:KYC_INFORMATION_REQUESTED')::uuid, 'KYC_INFORMATION_REQUESTED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:KYC_CASE_APPROVED')::uuid, 'KYC_CASE_APPROVED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:KYC_CASE_REJECTED')::uuid, 'KYC_CASE_REJECTED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:KYC_VERIFICATION_EXPIRED')::uuid, 'KYC_VERIFICATION_EXPIRED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:KYC_VERIFICATION_SUSPENDED')::uuid, 'KYC_VERIFICATION_SUSPENDED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:KYC_VERIFICATION_REVOKED')::uuid, 'KYC_VERIFICATION_REVOKED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:BENEFICIARY_VERIFICATION_SUBMITTED')::uuid, 'BENEFICIARY_VERIFICATION_SUBMITTED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:BENEFICIARY_INFORMATION_REQUESTED')::uuid, 'BENEFICIARY_INFORMATION_REQUESTED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:BENEFICIARY_VERIFICATION_APPROVED')::uuid, 'BENEFICIARY_VERIFICATION_APPROVED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:BENEFICIARY_VERIFICATION_REJECTED')::uuid, 'BENEFICIARY_VERIFICATION_REJECTED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:BENEFICIARY_VERIFICATION_EXPIRED')::uuid, 'BENEFICIARY_VERIFICATION_EXPIRED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:PAYOUT_DESTINATION_VERIFIED')::uuid, 'PAYOUT_DESTINATION_VERIFIED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:PAYOUT_DESTINATION_REJECTED')::uuid, 'PAYOUT_DESTINATION_REJECTED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:PAYOUT_DESTINATION_INFORMATION_REQUESTED')::uuid, 'PAYOUT_DESTINATION_INFORMATION_REQUESTED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:PAYOUT_DESTINATION_SUSPENDED')::uuid, 'PAYOUT_DESTINATION_SUSPENDED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:PAYOUT_DESTINATION_EXPIRED')::uuid, 'PAYOUT_DESTINATION_EXPIRED', 'VERIFICATION', false, ARRAY[]::text[], 'STAGE_5_NOTICE'),
  (md5('notification_template:PASSWORD_CHANGED')::uuid, 'PASSWORD_CHANGED', 'SECURITY', true, ARRAY['forgot_password_url']::text[], 'STAGE_4_NOTICE'),
  (md5('notification_template:MFA_CHANGED')::uuid, 'MFA_CHANGED', 'SECURITY', true, ARRAY['mfa_change','forgot_password_url']::text[], 'STAGE_4_NOTICE'),
  (md5('notification_template:EMAIL_CHANGE_REQUESTED')::uuid, 'EMAIL_CHANGE_REQUESTED', 'SECURITY', true, ARRAY['forgot_password_url']::text[], 'STAGE_4_NOTICE'),
  (md5('notification_template:REGISTRATION_EXISTING_ACCOUNT')::uuid, 'REGISTRATION_EXISTING_ACCOUNT', 'SECURITY', true, ARRAY['forgot_password_url']::text[], 'STAGE_4_NOTICE'),
  (md5('notification_template:STAFF_LINK_CONFIRMED_STAFF')::uuid, 'STAFF_LINK_CONFIRMED_STAFF', 'SECURITY', true, ARRAY[]::text[], 'STAGE_4_NOTICE'),
  (md5('notification_template:STAFF_LINK_CONFIRMED_PERSONAL')::uuid, 'STAFF_LINK_CONFIRMED_PERSONAL', 'SECURITY', true, ARRAY['forgot_password_url']::text[], 'STAGE_4_NOTICE'),
  (md5('notification_template:ACCOUNT_SUSPENDED')::uuid, 'ACCOUNT_SUSPENDED', 'SECURITY', true, ARRAY[]::text[], 'STAGE_4_NOTICE'),
  (md5('notification_template:PAYOUT_DESTINATION_CHANGED')::uuid, 'PAYOUT_DESTINATION_CHANGED', 'SECURITY', true, ARRAY['forgot_password_url']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:PAYMENT_PENDING_CONFIRMATION')::uuid, 'PAYMENT_PENDING_CONFIRMATION', 'PAYMENT', false, ARRAY['amount']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:PAYMENT_FAILED')::uuid, 'PAYMENT_FAILED', 'PAYMENT', false, ARRAY['amount']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:DONATION_CONFIRMED')::uuid, 'DONATION_CONFIRMED', 'PAYMENT', false, ARRAY['amount','donation_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:WITHDRAWAL_REQUESTED')::uuid, 'WITHDRAWAL_REQUESTED', 'WITHDRAWAL', false, ARRAY['amount','withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:WITHDRAWAL_UNDER_REVIEW')::uuid, 'WITHDRAWAL_UNDER_REVIEW', 'WITHDRAWAL', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:WITHDRAWAL_APPROVED')::uuid, 'WITHDRAWAL_APPROVED', 'WITHDRAWAL', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:WITHDRAWAL_REJECTED')::uuid, 'WITHDRAWAL_REJECTED', 'WITHDRAWAL', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:WITHDRAWAL_CANCELLED')::uuid, 'WITHDRAWAL_CANCELLED', 'WITHDRAWAL', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:PAYOUT_SUBMITTED')::uuid, 'PAYOUT_SUBMITTED', 'PAYOUT', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:PAYOUT_OUTCOME_UNKNOWN')::uuid, 'PAYOUT_OUTCOME_UNKNOWN', 'PAYOUT', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:PAYOUT_COMPLETED')::uuid, 'PAYOUT_COMPLETED', 'PAYOUT', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:PAYOUT_FAILED')::uuid, 'PAYOUT_FAILED', 'PAYOUT', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:PAYOUT_REVERSED')::uuid, 'PAYOUT_REVERSED', 'PAYOUT', false, ARRAY['withdrawal_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:SUPPORT_CASE_OPENED')::uuid, 'SUPPORT_CASE_OPENED', 'SUPPORT', false, ARRAY['case_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:SUPPORT_CASE_INFO_REQUESTED')::uuid, 'SUPPORT_CASE_INFO_REQUESTED', 'SUPPORT', false, ARRAY['case_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:SUPPORT_CASE_RESOLVED')::uuid, 'SUPPORT_CASE_RESOLVED', 'SUPPORT', false, ARRAY['case_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:SUPPORT_CASE_REOPENED')::uuid, 'SUPPORT_CASE_REOPENED', 'SUPPORT', false, ARRAY['case_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:SUPPORT_CASE_ASSIGNED')::uuid, 'SUPPORT_CASE_ASSIGNED', 'SUPPORT', false, ARRAY['case_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:INCIDENT_DECLARED')::uuid, 'INCIDENT_DECLARED', 'ADMINISTRATIVE', false, ARRAY['incident_reference','severity','module']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:RISK_RULE_AWAITING_APPROVAL')::uuid, 'RISK_RULE_AWAITING_APPROVAL', 'ADMINISTRATIVE', false, ARRAY[]::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:REFUND_AWAITING_APPROVAL')::uuid, 'REFUND_AWAITING_APPROVAL', 'ADMINISTRATIVE', false, ARRAY[]::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:FEE_ADJUSTMENT_AWAITING_APPROVAL')::uuid, 'FEE_ADJUSTMENT_AWAITING_APPROVAL', 'ADMINISTRATIVE', false, ARRAY[]::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:PAYOUT_EXCEPTION_OPENED')::uuid, 'PAYOUT_EXCEPTION_OPENED', 'ADMINISTRATIVE', false, ARRAY['exception_type','severity']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:RISK_CASE_OPENED_HIGH')::uuid, 'RISK_CASE_OPENED_HIGH', 'RISK', false, ARRAY['case_reference','priority']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:RISK_CASE_ESCALATED')::uuid, 'RISK_CASE_ESCALATED', 'RISK', false, ARRAY['case_reference']::text[], 'STAGE_15_BASELINE'),
  (md5('notification_template:COMPLIANCE_CASE_OPENED')::uuid, 'COMPLIANCE_CASE_OPENED', 'COMPLIANCE', false, ARRAY['case_type','severity']::text[], 'STAGE_15_BASELINE');

INSERT INTO app.notification_template_versions (id, template_id, email_subject, email_text, sms_text, inapp_title, inapp_body, content_hash)
SELECT v.id, v.template_id, v.s, v.b, v.sms, v.it, v.ib,
  sha256(convert_to(coalesce(v.s,'') || chr(31) || coalesce(v.b,'') || chr(31) || coalesce(v.sms,'') || chr(31) || coalesce(v.it,'') || chr(31) || coalesce(v.ib,''), 'UTF8'))
FROM (VALUES
  (md5('notification_template_version:CAMPAIGN_CREATED:1')::uuid, md5('notification_template:CAMPAIGN_CREATED')::uuid, 'Your campaign draft was created', 'Your campaign draft is saved. Complete it and submit it for review when you are ready.', NULL, 'Your campaign draft was created', 'Your campaign draft is saved. Complete it and submit it for review when you are ready.'),
  (md5('notification_template_version:CAMPAIGN_SUBMITTED:1')::uuid, md5('notification_template:CAMPAIGN_SUBMITTED')::uuid, 'We received your campaign', 'Your campaign was submitted and is waiting for review. Submitting does not guarantee approval; we will email you when it has been reviewed.', NULL, 'We received your campaign', 'Your campaign was submitted and is waiting for review. Submitting does not guarantee approval; we will email you when it has been reviewed.'),
  (md5('notification_template_version:CAMPAIGN_CHANGES_REQUESTED:1')::uuid, md5('notification_template:CAMPAIGN_CHANGES_REQUESTED')::uuid, 'Changes requested for your campaign', 'A reviewer asked for changes to your campaign. Sign in to FundZim to see what to update.', NULL, 'Changes requested for your campaign', 'A reviewer asked for changes to your campaign. Sign in to FundZim to see what to update.'),
  (md5('notification_template_version:CAMPAIGN_APPROVED:1')::uuid, md5('notification_template:CAMPAIGN_APPROVED')::uuid, 'Your campaign was approved', 'Your campaign was approved. Sign in to FundZim to publish it.', NULL, 'Your campaign was approved', 'Your campaign was approved. Sign in to FundZim to publish it.'),
  (md5('notification_template_version:CAMPAIGN_REJECTED:1')::uuid, md5('notification_template:CAMPAIGN_REJECTED')::uuid, 'Your campaign was not approved', 'Your campaign was not approved. Sign in to FundZim to see the reason and your options.', NULL, 'Your campaign was not approved', 'Your campaign was not approved. Sign in to FundZim to see the reason and your options.'),
  (md5('notification_template_version:CAMPAIGN_PUBLISHED:1')::uuid, md5('notification_template:CAMPAIGN_PUBLISHED')::uuid, 'Your campaign is live', 'Your campaign is now published. Donations are not yet available on FundZim.', NULL, 'Your campaign is live', 'Your campaign is now published. Donations are not yet available on FundZim.'),
  (md5('notification_template_version:CAMPAIGN_PAUSED:1')::uuid, md5('notification_template:CAMPAIGN_PAUSED')::uuid, 'Your campaign is paused', 'Your campaign is paused. You can resume it from your dashboard.', NULL, 'Your campaign is paused', 'Your campaign is paused. You can resume it from your dashboard.'),
  (md5('notification_template_version:CAMPAIGN_SUSPENDED:1')::uuid, md5('notification_template:CAMPAIGN_SUSPENDED')::uuid, 'Your campaign is suspended', 'Your campaign has been suspended pending review. Contact support if you have questions.', NULL, 'Your campaign is suspended', 'Your campaign has been suspended pending review. Contact support if you have questions.'),
  (md5('notification_template_version:CAMPAIGN_REACTIVATED:1')::uuid, md5('notification_template:CAMPAIGN_REACTIVATED')::uuid, 'Your campaign was reactivated', 'Your campaign has been reactivated.', NULL, 'Your campaign was reactivated', 'Your campaign has been reactivated.'),
  (md5('notification_template_version:CAMPAIGN_COMPLETED:1')::uuid, md5('notification_template:CAMPAIGN_COMPLETED')::uuid, 'Your campaign is completed', 'Your campaign has been marked as completed.', NULL, 'Your campaign is completed', 'Your campaign has been marked as completed.'),
  (md5('notification_template_version:KYC_CASE_SUBMITTED:1')::uuid, md5('notification_template:KYC_CASE_SUBMITTED')::uuid, 'We received your verification', 'Thank you. Your verification was submitted and is waiting for review. We will email you when it has been reviewed.', NULL, 'We received your verification', 'Thank you. Your verification was submitted and is waiting for review. We will email you when it has been reviewed.'),
  (md5('notification_template_version:KYC_INFORMATION_REQUESTED:1')::uuid, md5('notification_template:KYC_INFORMATION_REQUESTED')::uuid, 'More information needed for your verification', 'A reviewer needs more information to complete your verification. Sign in to FundZim and open your verification dashboard to respond.', NULL, 'More information needed for your verification', 'A reviewer needs more information to complete your verification. Sign in to FundZim and open your verification dashboard to respond.'),
  (md5('notification_template_version:KYC_CASE_APPROVED:1')::uuid, md5('notification_template:KYC_CASE_APPROVED')::uuid, 'Your verification was approved', 'Your verification was approved. Sign in to see what you can now do on FundZim.', NULL, 'Your verification was approved', 'Your verification was approved. Sign in to see what you can now do on FundZim.'),
  (md5('notification_template_version:KYC_CASE_REJECTED:1')::uuid, md5('notification_template:KYC_CASE_REJECTED')::uuid, 'Your verification was not approved', 'Your verification was not approved. Sign in to FundZim to see the reason and your options.', NULL, 'Your verification was not approved', 'Your verification was not approved. Sign in to FundZim to see the reason and your options.'),
  (md5('notification_template_version:KYC_VERIFICATION_EXPIRED:1')::uuid, md5('notification_template:KYC_VERIFICATION_EXPIRED')::uuid, 'Your verification has expired', 'Your verification is no longer current. Sign in to FundZim to verify again.', NULL, 'Your verification has expired', 'Your verification is no longer current. Sign in to FundZim to verify again.'),
  (md5('notification_template_version:KYC_VERIFICATION_SUSPENDED:1')::uuid, md5('notification_template:KYC_VERIFICATION_SUSPENDED')::uuid, 'Your verification is suspended', 'Your verification has been suspended pending review. Contact support if you have questions.', NULL, 'Your verification is suspended', 'Your verification has been suspended pending review. Contact support if you have questions.'),
  (md5('notification_template_version:KYC_VERIFICATION_REVOKED:1')::uuid, md5('notification_template:KYC_VERIFICATION_REVOKED')::uuid, 'Your verification was revoked', 'Your verification was revoked. Contact support if you have questions.', NULL, 'Your verification was revoked', 'Your verification was revoked. Contact support if you have questions.'),
  (md5('notification_template_version:BENEFICIARY_VERIFICATION_SUBMITTED:1')::uuid, md5('notification_template:BENEFICIARY_VERIFICATION_SUBMITTED')::uuid, 'Beneficiary submitted for verification', 'A beneficiary you declared was submitted for verification.', NULL, 'Beneficiary submitted for verification', 'A beneficiary you declared was submitted for verification.'),
  (md5('notification_template_version:BENEFICIARY_INFORMATION_REQUESTED:1')::uuid, md5('notification_template:BENEFICIARY_INFORMATION_REQUESTED')::uuid, 'More information needed for a beneficiary', 'A reviewer needs more information about a beneficiary you declared. Sign in to respond.', NULL, 'More information needed for a beneficiary', 'A reviewer needs more information about a beneficiary you declared. Sign in to respond.'),
  (md5('notification_template_version:BENEFICIARY_VERIFICATION_APPROVED:1')::uuid, md5('notification_template:BENEFICIARY_VERIFICATION_APPROVED')::uuid, 'Beneficiary verified', 'A beneficiary you declared has been verified.', NULL, 'Beneficiary verified', 'A beneficiary you declared has been verified.'),
  (md5('notification_template_version:BENEFICIARY_VERIFICATION_REJECTED:1')::uuid, md5('notification_template:BENEFICIARY_VERIFICATION_REJECTED')::uuid, 'Beneficiary not verified', 'A beneficiary you declared was not verified. Sign in to see the reason.', NULL, 'Beneficiary not verified', 'A beneficiary you declared was not verified. Sign in to see the reason.'),
  (md5('notification_template_version:BENEFICIARY_VERIFICATION_EXPIRED:1')::uuid, md5('notification_template:BENEFICIARY_VERIFICATION_EXPIRED')::uuid, 'Beneficiary verification expired', 'A beneficiary verification expired because the requested information was not provided.', NULL, 'Beneficiary verification expired', 'A beneficiary verification expired because the requested information was not provided.'),
  (md5('notification_template_version:PAYOUT_DESTINATION_VERIFIED:1')::uuid, md5('notification_template:PAYOUT_DESTINATION_VERIFIED')::uuid, 'Payout destination review completed', 'Your payout destination was verified. Payouts are not available on FundZim yet.', NULL, 'Payout destination review completed', 'Your payout destination was verified. Payouts are not available on FundZim yet.'),
  (md5('notification_template_version:PAYOUT_DESTINATION_REJECTED:1')::uuid, md5('notification_template:PAYOUT_DESTINATION_REJECTED')::uuid, 'Payout destination review completed', 'Your payout destination could not be verified. Sign in to see the reason.', NULL, 'Payout destination review completed', 'Your payout destination could not be verified. Sign in to see the reason.'),
  (md5('notification_template_version:PAYOUT_DESTINATION_INFORMATION_REQUESTED:1')::uuid, md5('notification_template:PAYOUT_DESTINATION_INFORMATION_REQUESTED')::uuid, 'More information needed for a payout destination', 'A reviewer needs more information about your payout destination. Sign in to respond.', NULL, 'More information needed for a payout destination', 'A reviewer needs more information about your payout destination. Sign in to respond.'),
  (md5('notification_template_version:PAYOUT_DESTINATION_SUSPENDED:1')::uuid, md5('notification_template:PAYOUT_DESTINATION_SUSPENDED')::uuid, 'Payout destination suspended', 'One of your payout destinations was suspended pending review.', NULL, 'Payout destination suspended', 'One of your payout destinations was suspended pending review.'),
  (md5('notification_template_version:PAYOUT_DESTINATION_EXPIRED:1')::uuid, md5('notification_template:PAYOUT_DESTINATION_EXPIRED')::uuid, 'Payout destination verification expired', 'One of your payout destinations needs to be verified again.', NULL, 'Payout destination verification expired', 'One of your payout destinations needs to be verified again.'),
  (md5('notification_template_version:PASSWORD_CHANGED:1')::uuid, md5('notification_template:PASSWORD_CHANGED')::uuid, 'Your FundZim password was changed', 'The password for your FundZim account was just changed and other devices were signed out.

If you did not do this, reset your password now at {{forgot_password_url}} and contact support.', NULL, 'Your FundZim password was changed', 'The password for your FundZim account was just changed and other devices were signed out.'),
  (md5('notification_template_version:MFA_CHANGED:1')::uuid, md5('notification_template:MFA_CHANGED')::uuid, 'Security alert: two-step verification', '{{mfa_change}} on your FundZim account.

If you did not do this, reset your password at {{forgot_password_url}} and contact support.', NULL, 'Security alert: two-step verification', '{{mfa_change}} on your FundZim account.'),
  (md5('notification_template_version:EMAIL_CHANGE_REQUESTED:1')::uuid, md5('notification_template:EMAIL_CHANGE_REQUESTED')::uuid, 'Your FundZim email address is being changed', 'Someone asked to change the sign-in email address of your FundZim account. The change happens only after the new address is confirmed.

If this was not you, reset your password at {{forgot_password_url}} and contact support.', NULL, 'Your FundZim email address is being changed', 'Someone asked to change the sign-in email address of your FundZim account. The change happens only after the new address is confirmed.'),
  (md5('notification_template_version:REGISTRATION_EXISTING_ACCOUNT:1')::uuid, md5('notification_template:REGISTRATION_EXISTING_ACCOUNT')::uuid, 'Someone tried to register with your email', 'Someone tried to create a FundZim account with this email address. You already have an account.

If this was you, sign in or reset your password at {{forgot_password_url}}.
If it was not you, you can ignore this email.', NULL, 'Someone tried to register with your email', 'Someone tried to create a FundZim account with this email address. You already have an account.'),
  (md5('notification_template_version:STAFF_LINK_CONFIRMED_STAFF:1')::uuid, md5('notification_template:STAFF_LINK_CONFIRMED_STAFF')::uuid, 'Your FundZim staff account is now linked', 'Your staff account was linked to your personal FundZim account. You will not be able to review or decide anything that concerns that personal account. Links cannot be removed.

If you did not do this, contact the FundZim security team now.', NULL, 'Your FundZim staff account is now linked', 'Your staff account was linked to your personal FundZim account. You will not be able to review or decide anything that concerns that personal account. Links cannot be removed.'),
  (md5('notification_template_version:STAFF_LINK_CONFIRMED_PERSONAL:1')::uuid, md5('notification_template:STAFF_LINK_CONFIRMED_PERSONAL')::uuid, 'Your FundZim account is now linked to a staff account', 'Your personal FundZim account was linked to a FundZim staff account at your confirmation. Links cannot be removed.

If you did not do this, reset your password at {{forgot_password_url}} and contact support.', NULL, 'Your FundZim account is now linked to a staff account', 'Your personal FundZim account was linked to a FundZim staff account at your confirmation. Links cannot be removed.'),
  (md5('notification_template_version:ACCOUNT_SUSPENDED:1')::uuid, md5('notification_template:ACCOUNT_SUSPENDED')::uuid, 'Your FundZim account has been suspended', 'Your FundZim account has been suspended and all sessions were signed out. Contact support if you have questions.', NULL, 'Your FundZim account has been suspended', 'Your FundZim account has been suspended and all sessions were signed out. Contact support if you have questions.'),
  (md5('notification_template_version:PAYOUT_DESTINATION_CHANGED:1')::uuid, md5('notification_template:PAYOUT_DESTINATION_CHANGED')::uuid, 'A payout destination on your account changed', 'A payout destination was added or changed on your FundZim account. Payouts to a new or changed destination wait for verification.

If you did not do this, reset your password at {{forgot_password_url}} and contact support.', 'FundZim: a payout destination on your account changed. If this was not you, contact support.', 'A payout destination on your account changed', 'A payout destination was added or changed on your FundZim account. Payouts to a new or changed destination wait for verification.'),
  (md5('notification_template_version:PAYMENT_PENDING_CONFIRMATION:1')::uuid, md5('notification_template:PAYMENT_PENDING_CONFIRMATION')::uuid, 'Your donation is being confirmed', 'We are waiting for the payment provider to confirm your donation of {{amount}}. Do not pay again: we will tell you when the outcome is known.', NULL, 'Your donation is being confirmed', 'We are waiting for the payment provider to confirm your donation of {{amount}}. Do not pay again: we will tell you when the outcome is known.'),
  (md5('notification_template_version:PAYMENT_FAILED:1')::uuid, md5('notification_template:PAYMENT_FAILED')::uuid, 'Your donation was not completed', 'Your donation of {{amount}} was not completed and you have not been charged by FundZim. You can try again from the campaign page.', NULL, 'Your donation was not completed', 'Your donation of {{amount}} was not completed and you have not been charged by FundZim. You can try again from the campaign page.'),
  (md5('notification_template_version:DONATION_CONFIRMED:1')::uuid, md5('notification_template:DONATION_CONFIRMED')::uuid, 'Thank you: your donation is confirmed', 'Your donation of {{amount}} was confirmed by the payment provider. Donation reference: {{donation_reference}}. This is a confirmation, not a tax receipt.', NULL, 'Thank you: your donation is confirmed', 'Your donation of {{amount}} was confirmed by the payment provider. Donation reference: {{donation_reference}}. This is a confirmation, not a tax receipt.'),
  (md5('notification_template_version:WITHDRAWAL_REQUESTED:1')::uuid, md5('notification_template:WITHDRAWAL_REQUESTED')::uuid, 'We received your withdrawal request', 'Your withdrawal request for {{amount}} (reference {{withdrawal_reference}}) was received and will be checked before anything is paid.', NULL, 'We received your withdrawal request', 'Your withdrawal request for {{amount}} (reference {{withdrawal_reference}}) was received and will be checked before anything is paid.'),
  (md5('notification_template_version:WITHDRAWAL_UNDER_REVIEW:1')::uuid, md5('notification_template:WITHDRAWAL_UNDER_REVIEW')::uuid, 'Your withdrawal is being reviewed', 'Your withdrawal request {{withdrawal_reference}} is being reviewed. We will tell you when it has been decided.', NULL, 'Your withdrawal is being reviewed', 'Your withdrawal request {{withdrawal_reference}} is being reviewed. We will tell you when it has been decided.'),
  (md5('notification_template_version:WITHDRAWAL_APPROVED:1')::uuid, md5('notification_template:WITHDRAWAL_APPROVED')::uuid, 'Your withdrawal was approved', 'Your withdrawal request {{withdrawal_reference}} was approved. It has not been paid yet: we will tell you when the payout is confirmed.', NULL, 'Your withdrawal was approved', 'Your withdrawal request {{withdrawal_reference}} was approved. It has not been paid yet: we will tell you when the payout is confirmed.'),
  (md5('notification_template_version:WITHDRAWAL_REJECTED:1')::uuid, md5('notification_template:WITHDRAWAL_REJECTED')::uuid, 'Your withdrawal was not approved', 'Your withdrawal request {{withdrawal_reference}} was not approved. Sign in to FundZim to see your options.', NULL, 'Your withdrawal was not approved', 'Your withdrawal request {{withdrawal_reference}} was not approved. Sign in to FundZim to see your options.'),
  (md5('notification_template_version:WITHDRAWAL_CANCELLED:1')::uuid, md5('notification_template:WITHDRAWAL_CANCELLED')::uuid, 'Your withdrawal was cancelled', 'Your withdrawal request {{withdrawal_reference}} was cancelled.', NULL, 'Your withdrawal was cancelled', 'Your withdrawal request {{withdrawal_reference}} was cancelled.'),
  (md5('notification_template_version:PAYOUT_SUBMITTED:1')::uuid, md5('notification_template:PAYOUT_SUBMITTED')::uuid, 'Your payout was sent to the payment provider', 'The payout for withdrawal {{withdrawal_reference}} was sent to the payment provider. It is not confirmed yet.', NULL, 'Your payout was sent to the payment provider', 'The payout for withdrawal {{withdrawal_reference}} was sent to the payment provider. It is not confirmed yet.'),
  (md5('notification_template_version:PAYOUT_OUTCOME_UNKNOWN:1')::uuid, md5('notification_template:PAYOUT_OUTCOME_UNKNOWN')::uuid, 'We are confirming your payout', 'We have not yet received a confirmed outcome for the payout of withdrawal {{withdrawal_reference}}. We are checking with the payment provider; it is not paid until we confirm it.', NULL, 'We are confirming your payout', 'We have not yet received a confirmed outcome for the payout of withdrawal {{withdrawal_reference}}. We are checking with the payment provider; it is not paid until we confirm it.'),
  (md5('notification_template_version:PAYOUT_COMPLETED:1')::uuid, md5('notification_template:PAYOUT_COMPLETED')::uuid, 'Your payout is confirmed', 'The payment provider confirmed the payout for withdrawal {{withdrawal_reference}}.', NULL, 'Your payout is confirmed', 'The payment provider confirmed the payout for withdrawal {{withdrawal_reference}}.'),
  (md5('notification_template_version:PAYOUT_FAILED:1')::uuid, md5('notification_template:PAYOUT_FAILED')::uuid, 'Your payout did not go through', 'The payout for withdrawal {{withdrawal_reference}} did not go through. Sign in to FundZim to see your options.', NULL, 'Your payout did not go through', 'The payout for withdrawal {{withdrawal_reference}} did not go through. Sign in to FundZim to see your options.'),
  (md5('notification_template_version:PAYOUT_REVERSED:1')::uuid, md5('notification_template:PAYOUT_REVERSED')::uuid, 'Your payout was reversed', 'The payout for withdrawal {{withdrawal_reference}} was reversed by the payment provider. Our team is reconciling it; sign in to FundZim for details.', NULL, 'Your payout was reversed', 'The payout for withdrawal {{withdrawal_reference}} was reversed by the payment provider. Our team is reconciling it; sign in to FundZim for details.'),
  (md5('notification_template_version:SUPPORT_CASE_OPENED:1')::uuid, md5('notification_template:SUPPORT_CASE_OPENED')::uuid, 'We opened support case {{case_reference}}', 'We opened support case {{case_reference}} for you. Our support team will reply.', NULL, 'We opened support case {{case_reference}}', 'We opened support case {{case_reference}} for you. Our support team will reply.'),
  (md5('notification_template_version:SUPPORT_CASE_INFO_REQUESTED:1')::uuid, md5('notification_template:SUPPORT_CASE_INFO_REQUESTED')::uuid, 'Support case {{case_reference}}: we need more information', 'Our support team needs more information about case {{case_reference}}. Sign in to FundZim or reply through the support channel you used.', NULL, 'Support case {{case_reference}}: we need more information', 'Our support team needs more information about case {{case_reference}}. Sign in to FundZim or reply through the support channel you used.'),
  (md5('notification_template_version:SUPPORT_CASE_RESOLVED:1')::uuid, md5('notification_template:SUPPORT_CASE_RESOLVED')::uuid, 'Support case {{case_reference}} is resolved', 'Support case {{case_reference}} was resolved. If you still need help, contact support and mention the case reference.', NULL, 'Support case {{case_reference}} is resolved', 'Support case {{case_reference}} was resolved. If you still need help, contact support and mention the case reference.'),
  (md5('notification_template_version:SUPPORT_CASE_REOPENED:1')::uuid, md5('notification_template:SUPPORT_CASE_REOPENED')::uuid, 'Support case {{case_reference}} was reopened', 'Support case {{case_reference}} was reopened and our support team is working on it again.', NULL, 'Support case {{case_reference}} was reopened', 'Support case {{case_reference}} was reopened and our support team is working on it again.'),
  (md5('notification_template_version:SUPPORT_CASE_ASSIGNED:1')::uuid, md5('notification_template:SUPPORT_CASE_ASSIGNED')::uuid, 'Support case {{case_reference}} assigned to you', 'Support case {{case_reference}} was assigned to you.', NULL, 'Support case {{case_reference}} assigned to you', 'Support case {{case_reference}} was assigned to you.'),
  (md5('notification_template_version:INCIDENT_DECLARED:1')::uuid, md5('notification_template:INCIDENT_DECLARED')::uuid, 'Incident {{incident_reference}} declared ({{severity}})', 'An operational incident was declared: {{incident_reference}}, severity {{severity}}, module {{module}}.', NULL, 'Incident {{incident_reference}} declared ({{severity}})', 'An operational incident was declared: {{incident_reference}}, severity {{severity}}, module {{module}}.'),
  (md5('notification_template_version:RISK_RULE_AWAITING_APPROVAL:1')::uuid, md5('notification_template:RISK_RULE_AWAITING_APPROVAL')::uuid, 'A risk rule version is waiting for a checker', 'A risk rule version was submitted and needs a second person''s decision.', NULL, 'A risk rule version is waiting for a checker', 'A risk rule version was submitted and needs a second person''s decision.'),
  (md5('notification_template_version:REFUND_AWAITING_APPROVAL:1')::uuid, md5('notification_template:REFUND_AWAITING_APPROVAL')::uuid, 'A refund request is waiting for a checker', 'A TEST refund request needs a second person''s decision.', NULL, 'A refund request is waiting for a checker', 'A TEST refund request needs a second person''s decision.'),
  (md5('notification_template_version:FEE_ADJUSTMENT_AWAITING_APPROVAL:1')::uuid, md5('notification_template:FEE_ADJUSTMENT_AWAITING_APPROVAL')::uuid, 'A fee adjustment is waiting for a checker', 'A TEST fee adjustment needs a second person''s decision.', NULL, 'A fee adjustment is waiting for a checker', 'A TEST fee adjustment needs a second person''s decision.'),
  (md5('notification_template_version:PAYOUT_EXCEPTION_OPENED:1')::uuid, md5('notification_template:PAYOUT_EXCEPTION_OPENED')::uuid, 'A payout exception needs attention ({{severity}})', 'A payout exception of type {{exception_type}} was opened (severity {{severity}}).', NULL, 'A payout exception needs attention ({{severity}})', 'A payout exception of type {{exception_type}} was opened (severity {{severity}}).'),
  (md5('notification_template_version:RISK_CASE_OPENED_HIGH:1')::uuid, md5('notification_template:RISK_CASE_OPENED_HIGH')::uuid, 'High-priority risk case {{case_reference}} opened', 'Risk case {{case_reference}} ({{priority}}) was opened and needs an investigator.', NULL, 'High-priority risk case {{case_reference}} opened', 'Risk case {{case_reference}} ({{priority}}) was opened and needs an investigator.'),
  (md5('notification_template_version:RISK_CASE_ESCALATED:1')::uuid, md5('notification_template:RISK_CASE_ESCALATED')::uuid, 'Risk case {{case_reference}} escalated to compliance', 'Risk case {{case_reference}} was escalated to compliance for review.', NULL, 'Risk case {{case_reference}} escalated to compliance', 'Risk case {{case_reference}} was escalated to compliance for review.'),
  (md5('notification_template_version:COMPLIANCE_CASE_OPENED:1')::uuid, md5('notification_template:COMPLIANCE_CASE_OPENED')::uuid, 'Compliance case opened ({{severity}})', 'A compliance case of type {{case_type}} was opened (severity {{severity}}).', NULL, 'Compliance case opened ({{severity}})', 'A compliance case of type {{case_type}} was opened (severity {{severity}}).')
) AS v(id, template_id, s, b, sms, it, ib);

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN ('communications.view',
  'communications.template.request','communications.template.approve','communications.replay','communications.suppression.manage',
  'communications.consent.view'));
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('communications.view','communications.template.request','communications.template.approve',
  'communications.replay','communications.suppression.manage','communications.consent.view');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP TABLE app.notification_worker_heartbeats;
DROP TABLE app.notification_sim_messages;
DROP TABLE app.notification_provider_configs;
DROP TABLE app.notification_dead_letters;
DROP TABLE app.notification_suppressions;
DROP TABLE app.communication_consent_events;
DROP TABLE app.communication_consents;
DROP TABLE app.notification_preference_events;
DROP TABLE app.notification_preferences;
DROP TABLE app.notification_webhook_rejections;
DROP TABLE app.notification_provider_events;
DROP FUNCTION IF EXISTS app.notification_provider_events_guard();
DROP TABLE app.notification_attempts;
DROP TABLE app.notification_deliveries;
DROP TABLE app.notifications;
DROP TABLE app.notification_events;
DROP TABLE app.notification_template_versions;
DROP FUNCTION app.notification_template_versions_guard();
DROP TABLE app.notification_templates;
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine IN ('notification_template','notification_delivery','communication_consent');
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
