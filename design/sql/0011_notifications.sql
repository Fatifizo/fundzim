-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0011: notifications module — notification_templates, notification_jobs, notification_attempts,
-- notification_preferences, notification_suppressions. Schema: app.
-- Docs: docs/database/audit-notification-schema.md. Fed from the outbox (Stage 15); jobs run on River.
-- No C3/C4 content in stored variables. One-time codes are never persisted here: auth calls the
-- notifications service synchronously for OTP delivery and passes the code in memory only; the job row
-- records the send (template, recipient, outcome) without the code (only auth's HMAC exists in the DB).
-- =====================================================================================================

-- app.notification_templates — versioned; content is immutable once ACTIVE (a change is a new version).
-- Classification: C1. Retention: OPERATIONAL.
CREATE TABLE app.notification_templates (
  id                uuid        NOT NULL,
  template_key      text        NOT NULL,                  -- e.g. 'donation.receipt'
  channel           text        NOT NULL,
  locale            text        NOT NULL,
  version           integer     NOT NULL,
  category          text        NOT NULL,
  is_mandatory      boolean     NOT NULL DEFAULT false,    -- cannot be opted out of (security/transactional)
  subject_template  text,                                  -- EMAIL only
  body_template     text        NOT NULL,
  allowed_variables text[]      NOT NULL DEFAULT '{}',     -- renderer rejects any other variable
  status            text        NOT NULL DEFAULT 'DRAFT',
  created_by        uuid        NOT NULL,
  approved_by       uuid,
  activated_at      timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_templates PRIMARY KEY (id),
  CONSTRAINT uq_notification_templates_version UNIQUE (template_key, channel, locale, version),
  CONSTRAINT fk_notification_templates_created_by FOREIGN KEY (created_by) REFERENCES app.users (id),
  CONSTRAINT fk_notification_templates_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT ck_notification_templates_key CHECK (template_key ~ '^[a-z_]+(\.[a-z_]+)+$'),
  CONSTRAINT ck_notification_templates_channel CHECK (channel IN ('SMS', 'EMAIL', 'WHATSAPP')),
  CONSTRAINT ck_notification_templates_category CHECK (category IN ('SECURITY', 'TRANSACTIONAL', 'CAMPAIGN_ACTIVITY',
                                                                     'CAMPAIGN_UPDATE', 'MARKETING')),
  CONSTRAINT ck_notification_templates_mandatory CHECK (NOT is_mandatory OR category IN ('SECURITY', 'TRANSACTIONAL')),
  CONSTRAINT ck_notification_templates_subject CHECK ((channel = 'EMAIL') = (subject_template IS NOT NULL)),
  CONSTRAINT ck_notification_templates_status CHECK (status IN ('DRAFT', 'ACTIVE', 'RETIRED')),
  CONSTRAINT ck_notification_templates_approved CHECK (status = 'DRAFT' OR (approved_by IS NOT NULL AND activated_at IS NOT NULL)),
  CONSTRAINT ck_notification_templates_maker_checker CHECK (approved_by IS NULL OR approved_by <> created_by),
  CONSTRAINT ck_notification_templates_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX uq_notification_templates_active ON app.notification_templates (template_key, channel, locale)
  WHERE status = 'ACTIVE';
CREATE TRIGGER trg_notification_templates_set_updated_at BEFORE UPDATE ON app.notification_templates
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE FUNCTION app.notification_templates_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'DRAFT' AND (to_jsonb(OLD) - ARRAY['status', 'updated_at']) IS DISTINCT FROM (to_jsonb(NEW) - ARRAY['status', 'updated_at']) THEN
    RAISE EXCEPTION 'notification template % is % and immutable; create a new version', OLD.id, OLD.status
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF (OLD.status = 'ACTIVE' AND NEW.status NOT IN ('ACTIVE', 'RETIRED')) OR (OLD.status = 'RETIRED' AND NEW.status <> 'RETIRED') THEN
    RAISE EXCEPTION 'illegal template status change % -> %', OLD.status, NEW.status USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_notification_templates_guard BEFORE UPDATE ON app.notification_templates
  FOR EACH ROW EXECUTE FUNCTION app.notification_templates_guard();
CREATE TRIGGER trg_notification_templates_no_delete BEFORE DELETE ON app.notification_templates
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- app.notification_jobs — one logical message to one recipient. dedupe_key makes sends idempotent
-- (e.g. 'receipt:payment:<id>:EMAIL' → exactly one receipt per channel). Recipient is a user (contact
-- resolved at send time from verified contacts) or an explicit destination (guest donor receipts).
-- Classification: C2. Retention: OPERATIONAL (destination and variables redacted after the window).
CREATE TABLE app.notification_jobs (
  id                             uuid        NOT NULL,
  template_id                    uuid        NOT NULL,     -- pinned template version
  channel                        text        NOT NULL,
  category                       text        NOT NULL,
  dedupe_key                     text        NOT NULL,
  recipient_user_id              uuid,
  destination                    text,                     -- explicit E.164 / email (guests); NULL once redacted
  destination_hmac               bytea,                    -- blind index used for suppression lookups
  variables                      jsonb       NOT NULL DEFAULT '{}',   -- C2 at most; never C3/C4
  source_event_id                uuid,                     -- outbox event that caused it
  status                         text        NOT NULL DEFAULT 'PENDING',
  attempts                       integer     NOT NULL DEFAULT 0,
  max_attempts                   integer     NOT NULL DEFAULT 5,
  next_attempt_at                timestamptz NOT NULL DEFAULT now(),
  last_error_code                text,
  suppressed_reason              text,
  suppression_id                 uuid,
  sent_at                        timestamptz,
  delivered_at                   timestamptz,
  redacted_at                    timestamptz,
  created_at                     timestamptz NOT NULL DEFAULT now(),
  updated_at                     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_jobs PRIMARY KEY (id),
  CONSTRAINT uq_notification_jobs_dedupe_key UNIQUE (dedupe_key),
  CONSTRAINT fk_notification_jobs_template_id FOREIGN KEY (template_id) REFERENCES app.notification_templates (id),
  CONSTRAINT fk_notification_jobs_recipient_user_id FOREIGN KEY (recipient_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_notification_jobs_channel CHECK (channel IN ('SMS', 'EMAIL', 'WHATSAPP')),
  CONSTRAINT ck_notification_jobs_category CHECK (category IN ('SECURITY', 'TRANSACTIONAL', 'CAMPAIGN_ACTIVITY', 'CAMPAIGN_UPDATE', 'MARKETING')),
  CONSTRAINT ck_notification_jobs_recipient CHECK (recipient_user_id IS NOT NULL OR destination IS NOT NULL OR redacted_at IS NOT NULL),
  CONSTRAINT ck_notification_jobs_destination_hmac CHECK (destination_hmac IS NULL OR octet_length(destination_hmac) = 32),
  CONSTRAINT ck_notification_jobs_variables_object CHECK (jsonb_typeof(variables) = 'object'),
  -- defence in depth: secret-looking variable names are refused (the Go renderer allow-lists variables too)
  CONSTRAINT ck_notification_jobs_no_secret_variables CHECK (NOT (variables ?| ARRAY['code', 'otp', 'otp_code', 'password', 'token', 'pin'])),
  CONSTRAINT ck_notification_jobs_status CHECK (status IN ('PENDING', 'SENDING', 'SENT', 'DELIVERED', 'FAILED', 'DEAD',
                                                           'SUPPRESSED', 'CANCELLED')),
  CONSTRAINT ck_notification_jobs_attempts CHECK (attempts >= 0 AND max_attempts BETWEEN 1 AND 20 AND attempts <= max_attempts),
  CONSTRAINT ck_notification_jobs_suppressed CHECK ((status = 'SUPPRESSED') = (suppressed_reason IS NOT NULL)),
  CONSTRAINT ck_notification_jobs_suppressed_reason CHECK (suppressed_reason IS NULL OR suppressed_reason IN ('SUPPRESSION_LIST', 'PREFERENCE_OPT_OUT')),
  CONSTRAINT ck_notification_jobs_suppression_ref CHECK ((suppressed_reason IS NOT DISTINCT FROM 'SUPPRESSION_LIST') = (suppression_id IS NOT NULL)),
  CONSTRAINT ck_notification_jobs_sent CHECK (status NOT IN ('SENT', 'DELIVERED') OR sent_at IS NOT NULL),
  CONSTRAINT ck_notification_jobs_dedupe_key CHECK (length(dedupe_key) BETWEEN 8 AND 255)
);
CREATE INDEX ix_notification_jobs_due ON app.notification_jobs (next_attempt_at) WHERE status = 'PENDING';
CREATE INDEX ix_notification_jobs_recipient ON app.notification_jobs (recipient_user_id, created_at) WHERE recipient_user_id IS NOT NULL;
CREATE TRIGGER trg_notification_jobs_guard_status BEFORE INSERT OR UPDATE OF status ON app.notification_jobs
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('notification_job');
CREATE TRIGGER trg_notification_jobs_set_updated_at BEFORE UPDATE ON app.notification_jobs
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_notification_jobs_immutable_cols BEFORE UPDATE ON app.notification_jobs
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('template_id', 'channel', 'category', 'dedupe_key', 'recipient_user_id',
                                                         'source_event_id', 'created_at');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('notification_job', '',         'PENDING'),
  ('notification_job', '',         'SUPPRESSED'),   -- suppressed or opted out at creation
  ('notification_job', 'PENDING',  'SENDING'),
  ('notification_job', 'PENDING',  'SUPPRESSED'),
  ('notification_job', 'PENDING',  'CANCELLED'),
  ('notification_job', 'SENDING',  'SENT'),
  ('notification_job', 'SENDING',  'PENDING'),      -- retryable failure: rescheduled with backoff (next_attempt_at)
  ('notification_job', 'SENDING',  'FAILED'),       -- permanent provider rejection
  ('notification_job', 'SENDING',  'DEAD'),         -- max attempts reached (dead letter; alert)
  ('notification_job', 'SENT',     'DELIVERED'),    -- delivery receipt
  ('notification_job', 'SENT',     'FAILED');       -- bounce / undeliverable receipt

-- app.notification_attempts — append-only, one row per provider call. Classification: C2.
-- Retention: OPERATIONAL.
CREATE TABLE app.notification_attempts (
  id                  uuid        NOT NULL,
  notification_job_id uuid        NOT NULL,
  attempt_number      integer     NOT NULL,
  provider_code       text        NOT NULL,                -- e.g. 'mailpit', 'log_sms' (fakes until Stage 15)
  provider_message_id text,
  outcome             text        NOT NULL,
  error_code          text,
  error_detail        text,                                -- redacted
  started_at          timestamptz NOT NULL,
  finished_at         timestamptz NOT NULL,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_attempts PRIMARY KEY (id),
  CONSTRAINT fk_notification_attempts_job_id FOREIGN KEY (notification_job_id) REFERENCES app.notification_jobs (id),
  CONSTRAINT uq_notification_attempts_job_attempt UNIQUE (notification_job_id, attempt_number),
  CONSTRAINT ck_notification_attempts_attempt_number CHECK (attempt_number >= 1),
  CONSTRAINT ck_notification_attempts_outcome CHECK (outcome IN ('ACCEPTED', 'REJECTED', 'RETRYABLE_ERROR', 'TIMEOUT')),
  CONSTRAINT ck_notification_attempts_times CHECK (finished_at >= started_at),
  CONSTRAINT ck_notification_attempts_accepted CHECK (outcome <> 'ACCEPTED' OR provider_message_id IS NOT NULL)
);
CREATE UNIQUE INDEX uq_notification_attempts_provider_message ON app.notification_attempts (provider_code, provider_message_id)
  WHERE provider_message_id IS NOT NULL;
CREATE TRIGGER trg_notification_attempts_no_mutation BEFORE UPDATE OR DELETE ON app.notification_attempts
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_notification_attempts_no_truncate BEFORE TRUNCATE ON app.notification_attempts
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- app.notification_preferences — opt-outs for non-mandatory categories only (SECURITY and TRANSACTIONAL
-- are not preference-controlled). Marketing requires recorded consent (LR-035). Classification: C2.
CREATE TABLE app.notification_preferences (
  id          uuid        NOT NULL,
  user_id     uuid        NOT NULL,
  category    text        NOT NULL,
  channel     text        NOT NULL,
  enabled     boolean     NOT NULL,
  consent_source text,                                     -- where marketing consent was captured
  consented_at   timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_preferences PRIMARY KEY (id),
  CONSTRAINT uq_notification_preferences_user_category_channel UNIQUE (user_id, category, channel),
  CONSTRAINT fk_notification_preferences_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_notification_preferences_category CHECK (category IN ('CAMPAIGN_ACTIVITY', 'CAMPAIGN_UPDATE', 'MARKETING')),
  CONSTRAINT ck_notification_preferences_channel CHECK (channel IN ('SMS', 'EMAIL', 'WHATSAPP')),
  CONSTRAINT ck_notification_preferences_marketing_consent CHECK (
    category <> 'MARKETING' OR NOT enabled OR (consent_source IS NOT NULL AND consented_at IS NOT NULL))
);
CREATE TRIGGER trg_notification_preferences_set_updated_at BEFORE UPDATE ON app.notification_preferences
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_notification_preferences_immutable_cols BEFORE UPDATE ON app.notification_preferences
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('user_id', 'category', 'channel', 'created_at');

-- app.notification_suppressions — destinations we must not message (bounces, complaints, STOP, invalid
-- numbers, SMS-pumping blocks). Keyed by destination HMAC so guests can be suppressed without storing the
-- plain address. scope ALL also blocks mandatory messages (e.g. invalid number); ALL_NON_MANDATORY does not.
-- Lifting is a recorded update, never a delete. Classification: C2. Retention: OPERATIONAL.
CREATE TABLE app.notification_suppressions (
  id               uuid        NOT NULL,
  channel          text        NOT NULL,
  destination_hmac bytea       NOT NULL,
  hmac_key_id      text        NOT NULL,
  reason           text        NOT NULL,
  scope            text        NOT NULL,
  source           text        NOT NULL,                   -- 'provider_webhook' | 'user' | 'staff' | 'risk'
  created_by       uuid,
  created_at       timestamptz NOT NULL DEFAULT now(),
  lifted_at        timestamptz,
  lifted_by        uuid,
  lift_reason      text,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_notification_suppressions PRIMARY KEY (id),
  CONSTRAINT fk_notification_suppressions_created_by FOREIGN KEY (created_by) REFERENCES app.users (id),
  CONSTRAINT fk_notification_suppressions_lifted_by FOREIGN KEY (lifted_by) REFERENCES app.users (id),
  CONSTRAINT ck_notification_suppressions_channel CHECK (channel IN ('SMS', 'EMAIL', 'WHATSAPP')),
  CONSTRAINT ck_notification_suppressions_hmac CHECK (octet_length(destination_hmac) = 32),
  CONSTRAINT ck_notification_suppressions_reason CHECK (reason IN ('HARD_BOUNCE', 'COMPLAINT', 'UNSUBSCRIBE', 'STOP_KEYWORD',
                                                                   'INVALID_DESTINATION', 'SMS_PUMPING', 'STAFF_ACTION')),
  CONSTRAINT ck_notification_suppressions_scope CHECK (scope IN ('ALL', 'ALL_NON_MANDATORY')),
  CONSTRAINT ck_notification_suppressions_source CHECK (source IN ('provider_webhook', 'user', 'staff', 'risk')),
  CONSTRAINT ck_notification_suppressions_lifted CHECK ((lifted_at IS NULL) = (lift_reason IS NULL))
);
CREATE UNIQUE INDEX uq_notification_suppressions_active ON app.notification_suppressions (channel, destination_hmac)
  WHERE lifted_at IS NULL;
CREATE TRIGGER trg_notification_suppressions_set_updated_at BEFORE UPDATE ON app.notification_suppressions
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_notification_suppressions_cols BEFORE UPDATE ON app.notification_suppressions
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('lifted_at', 'lifted_by', 'lift_reason');
CREATE TRIGGER trg_notification_suppressions_no_delete BEFORE DELETE ON app.notification_suppressions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

ALTER TABLE app.notification_jobs ADD CONSTRAINT fk_notification_jobs_suppression_id
  FOREIGN KEY (suppression_id) REFERENCES app.notification_suppressions (id);
