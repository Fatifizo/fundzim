-- Stage 8 (ADR-039): psp module — provider registry, versioned capabilities, provider accounts (secret
-- REFERENCES only), webhook inbox, webhook rejections, provider health, and the sandbox simulator's own
-- provider-side state. Ported from design/sql/0007_fees_psp.sql (Stage 2, validated in PGlite); fee tables
-- are deferred (no fees module, LR-028). Owner module: psp. Schema: app.
-- Seeds ONLY the sandbox simulator (is_sandbox = true). No real provider is registered: none is selected
-- (provider-comparison.md, all PENDING), and a row would invite routing on unconfirmed facts.
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- ---------------------------------------------------------------------------------------------------------
-- Provider registry. A real provider can be ENABLED only once selected under an ADR; the sandbox is never
-- "selected". UNIQUE (id, is_sandbox) is the target of payments' composite FKs that pin is_simulated.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.payment_providers (
  id                uuid        NOT NULL,
  code              text        NOT NULL,
  display_name      text        NOT NULL,
  is_sandbox        boolean     NOT NULL DEFAULT false,
  selection_status  text        NOT NULL DEFAULT 'PENDING',
  status            text        NOT NULL DEFAULT 'DISABLED',
  confirmation_mode text        NOT NULL,
  selection_adr     text,
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_providers PRIMARY KEY (id),
  CONSTRAINT uq_payment_providers_code UNIQUE (code),
  CONSTRAINT uq_payment_providers_id_sandbox UNIQUE (id, is_sandbox),
  CONSTRAINT ck_payment_providers_code CHECK (code ~ '^[a-z][a-z0-9_]{1,31}$'),
  CONSTRAINT ck_payment_providers_selection_status CHECK (selection_status IN ('PENDING','SELECTED','REJECTED')),
  CONSTRAINT ck_payment_providers_status CHECK (status IN ('DISABLED','ENABLED','RETIRED')),
  CONSTRAINT ck_payment_providers_confirmation_mode CHECK (confirmation_mode IN ('WEBHOOK_SIGNED','WEBHOOK_UNSIGNED_VERIFY_BY_API','POLL_ONLY')),
  CONSTRAINT ck_payment_providers_enabled_requires_selection CHECK (
    status <> 'ENABLED' OR is_sandbox OR (selection_status = 'SELECTED' AND selection_adr ~ '^ADR-[0-9]{3}$')),
  CONSTRAINT ck_payment_providers_sandbox_not_selected CHECK (NOT is_sandbox OR selection_status = 'PENDING'),
  CONSTRAINT ck_payment_providers_sandbox_code CHECK (is_sandbox = (code LIKE 'sandbox%'))
);
COMMENT ON TABLE app.payment_providers IS 'C1. psp provider registry (ADR-039). Only the sandbox simulator is registered in Stage 8.';
CREATE TRIGGER trg_payment_providers_updated_at BEFORE UPDATE ON app.payment_providers
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payment_providers_keep_created_at BEFORE UPDATE ON app.payment_providers
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_payment_providers_immutable BEFORE UPDATE ON app.payment_providers
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('code', 'is_sandbox');
CREATE TRIGGER trg_payment_providers_no_delete BEFORE DELETE ON app.payment_providers
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---------------------------------------------------------------------------------------------------------
-- Capability registry, versioned and append-only (one row = one evidence-backed statement about
-- (provider, method, currency)). Flags default to false = ABSENT. routable is generated.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.provider_capabilities (
  id                  uuid        NOT NULL,
  provider_id         uuid        NOT NULL,
  capability_version  integer     NOT NULL,
  method              text        NOT NULL,
  currency            char(3)     NOT NULL,
  settlement_currency char(3)     NOT NULL,
  custody_model       text        NOT NULL,
  separate_capture    boolean     NOT NULL DEFAULT false,
  refund_api          boolean     NOT NULL DEFAULT false,
  partial_refund      boolean     NOT NULL DEFAULT false,
  idempotent_create   boolean     NOT NULL DEFAULT false,   -- PCR-009
  idempotent_refund   boolean     NOT NULL DEFAULT false,   -- PCR-006
  supports_cancel     boolean     NOT NULL DEFAULT false,
  payout_api          boolean     NOT NULL DEFAULT false,
  payout_rails        text[]      NOT NULL DEFAULT '{}',
  split_settlement    boolean     NOT NULL DEFAULT false,
  pool_balance_api    boolean     NOT NULL DEFAULT false,
  signed_webhooks     boolean     NOT NULL DEFAULT false,
  status_api          boolean     NOT NULL DEFAULT false,
  documented_expiry   boolean     NOT NULL DEFAULT false,   -- PCR-010
  documented_expiry_seconds integer,
  statement_format    text        NOT NULL DEFAULT 'UNKNOWN',
  evidence_status     text        NOT NULL,
  evidence_source     text,
  pcr_ref             text,
  flag_evidence       jsonb       NOT NULL DEFAULT '{}'::jsonb,
  recorded_by         uuid        NOT NULL,
  recorded_at         timestamptz NOT NULL DEFAULT now(),
  routable            boolean GENERATED ALWAYS AS (
                        evidence_status = 'VERIFIED'
                        AND custody_model IN ('PSP_POOL','SPLIT_DIRECT')
                        AND (signed_webhooks OR status_api)) STORED,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_provider_capabilities PRIMARY KEY (id),
  CONSTRAINT fk_provider_capabilities_provider_id FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_capabilities_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_capabilities_settlement_currency FOREIGN KEY (settlement_currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_capabilities_recorded_by FOREIGN KEY (recorded_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_provider_capabilities_version UNIQUE (provider_id, method, currency, capability_version),
  CONSTRAINT uq_provider_capabilities_routing_key UNIQUE (id, provider_id, method, currency, custody_model, routable),
  CONSTRAINT ck_provider_capabilities_version CHECK (capability_version >= 1),
  CONSTRAINT ck_provider_capabilities_method CHECK (method IN ('ECOCASH','ONEMONEY','INNBUCKS','OMARI','ZIMSWITCH','BANK_TRANSFER','CARD')),
  CONSTRAINT ck_provider_capabilities_custody_model CHECK (custody_model IN ('PSP_POOL','MERCHANT_SETTLEMENT','SPLIT_DIRECT')),
  CONSTRAINT ck_provider_capabilities_evidence_status CHECK (evidence_status IN ('VERIFIED','UNVERIFIED','NOT_SUPPORTED','REQUIRES_PROVIDER_CONFIRMATION')),
  -- ADR-018: a rail is recorded (and offered) only if it settles in the donation currency. No FX, ever.
  CONSTRAINT ck_provider_capabilities_same_currency_settlement CHECK (settlement_currency = currency),
  CONSTRAINT ck_provider_capabilities_partial_refund CHECK (NOT partial_refund OR refund_api),
  CONSTRAINT ck_provider_capabilities_idempotent_refund CHECK (NOT idempotent_refund OR refund_api),
  CONSTRAINT ck_provider_capabilities_payout_rails CHECK (
    (payout_api AND cardinality(payout_rails) > 0) OR (NOT payout_api AND cardinality(payout_rails) = 0)),
  CONSTRAINT ck_provider_capabilities_payout_rail_values CHECK (
    payout_rails <@ ARRAY['ECOCASH','ONEMONEY','INNBUCKS','OMARI','ZIMSWITCH','BANK_TRANSFER']::text[]),
  CONSTRAINT ck_provider_capabilities_expiry CHECK (
    (documented_expiry AND documented_expiry_seconds > 0) OR (NOT documented_expiry AND documented_expiry_seconds IS NULL)),
  CONSTRAINT ck_provider_capabilities_statement_format CHECK (statement_format IN ('API','FILE_CSV','FILE_XLSX','FILE_PDF','DASHBOARD_EXPORT','SFTP','NONE','UNKNOWN')),
  CONSTRAINT ck_provider_capabilities_flag_evidence CHECK (jsonb_typeof(flag_evidence) = 'object'),
  CONSTRAINT ck_provider_capabilities_evidence_ref CHECK (
    (evidence_status = 'VERIFIED' AND evidence_source IS NOT NULL)
    OR (evidence_status = 'REQUIRES_PROVIDER_CONFIRMATION' AND pcr_ref ~ '^PCR-[0-9]{3}$')
    OR evidence_status IN ('UNVERIFIED','NOT_SUPPORTED')),
  CONSTRAINT ck_provider_capabilities_not_supported_flags CHECK (
    evidence_status <> 'NOT_SUPPORTED' OR NOT (separate_capture OR refund_api OR payout_api OR split_settlement
      OR pool_balance_api OR signed_webhooks OR status_api OR documented_expiry OR idempotent_create))
);
COMMENT ON TABLE app.provider_capabilities IS 'C1. Versioned, append-only capability evidence (ADR-029). routable = VERIFIED and not MERCHANT_SETTLEMENT and an authoritative status path.';
CREATE INDEX ix_provider_capabilities_routing ON app.provider_capabilities (method, currency) WHERE routable;
CREATE TRIGGER trg_provider_capabilities_no_mutation BEFORE UPDATE OR DELETE ON app.provider_capabilities
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_provider_capabilities_no_truncate BEFORE TRUNCATE ON app.provider_capabilities
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE VIEW app.provider_capabilities_current AS
  SELECT DISTINCT ON (provider_id, method, currency) *
    FROM app.provider_capabilities
   ORDER BY provider_id, method, currency, capability_version DESC;

-- ---------------------------------------------------------------------------------------------------------
-- Provider accounts per environment: secret REFERENCES only (secretref://...), never values.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.provider_accounts (
  id                            uuid        NOT NULL,
  provider_id                   uuid        NOT NULL,
  environment                   text        NOT NULL,
  account_label                 text        NOT NULL,
  merchant_reference            text,
  api_credentials_secret_ref    text        NOT NULL,
  webhook_secret_ref            text,
  webhook_secret_ref_previous   text,
  webhook_replay_window_seconds integer,
  status                        text        NOT NULL DEFAULT 'DISABLED',
  version                       integer     NOT NULL DEFAULT 1,
  created_at                    timestamptz NOT NULL DEFAULT now(),
  updated_at                    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_provider_accounts PRIMARY KEY (id),
  CONSTRAINT fk_provider_accounts_provider_id FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT uq_provider_accounts_label UNIQUE (provider_id, environment, account_label),
  CONSTRAINT uq_provider_accounts_id_provider UNIQUE (id, provider_id),
  CONSTRAINT ck_provider_accounts_environment CHECK (environment IN ('SANDBOX','STAGING','PRODUCTION')),
  CONSTRAINT ck_provider_accounts_status CHECK (status IN ('DISABLED','ENABLED','RETIRED')),
  CONSTRAINT ck_provider_accounts_api_secret_ref CHECK (api_credentials_secret_ref ~ '^secretref://[a-z0-9][a-z0-9/_.-]{2,200}$'),
  CONSTRAINT ck_provider_accounts_webhook_secret_ref CHECK (webhook_secret_ref IS NULL OR webhook_secret_ref ~ '^secretref://[a-z0-9][a-z0-9/_.-]{2,200}$'),
  CONSTRAINT ck_provider_accounts_webhook_secret_ref_prev CHECK (webhook_secret_ref_previous IS NULL OR webhook_secret_ref_previous ~ '^secretref://[a-z0-9][a-z0-9/_.-]{2,200}$'),
  CONSTRAINT ck_provider_accounts_replay_window CHECK (webhook_replay_window_seconds IS NULL OR webhook_replay_window_seconds BETWEEN 0 AND 3600)
);
COMMENT ON TABLE app.provider_accounts IS 'C1. Provider account configuration. Secret REFERENCES only; values live outside the database.';
CREATE INDEX ix_provider_accounts_provider_id ON app.provider_accounts (provider_id);

CREATE FUNCTION app.provider_accounts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.environment = 'PRODUCTION' AND EXISTS (
       SELECT 1 FROM app.payment_providers p WHERE p.id = NEW.provider_id AND p.is_sandbox) THEN
    RAISE EXCEPTION 'sandbox provider cannot have a PRODUCTION account' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_provider_accounts_guard BEFORE INSERT OR UPDATE ON app.provider_accounts
  FOR EACH ROW EXECUTE FUNCTION app.provider_accounts_guard();
CREATE TRIGGER trg_provider_accounts_updated_at BEFORE UPDATE ON app.provider_accounts
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_provider_accounts_keep_created_at BEFORE UPDATE ON app.provider_accounts
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_provider_accounts_no_delete BEFORE DELETE ON app.provider_accounts
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- ---------------------------------------------------------------------------------------------------------
-- Webhook inbox. Only VERIFIED events are stored. RAW group immutable (trigger); PROCESSING group mutable
-- with a guarded status machine. Dedupe: UNIQUE (provider_id, provider_event_id).
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.provider_webhook_inbox (
  id                    uuid        NOT NULL,
  provider_id           uuid        NOT NULL,
  provider_account_id   uuid,
  provider_event_id     text        NOT NULL,
  event_class           text        NOT NULL,
  event_type_raw        text        NOT NULL,
  provider_reference    text,
  our_reference         uuid,
  provider_occurred_at  timestamptz,
  received_at           timestamptz NOT NULL DEFAULT now(),
  signature_verified    boolean     NOT NULL,
  verification_method   text        NOT NULL,
  signature_key_slot    text,
  payload_redacted      jsonb       NOT NULL,
  raw_body_sha256       bytea       NOT NULL,
  raw_object_id         uuid,
  headers_subset        jsonb       NOT NULL DEFAULT '{}'::jsonb,
  is_simulated          boolean     NOT NULL,
  status                text        NOT NULL DEFAULT 'RECEIVED',
  dispatched_to         text,
  attempts              integer     NOT NULL DEFAULT 0,
  next_attempt_at       timestamptz,
  last_error_code       text,
  parked_subject_id     uuid,
  parked_until_status   text,
  parked_at             timestamptz,
  park_deadline_at      timestamptz,
  processed_at          timestamptz,
  version               integer     NOT NULL DEFAULT 1,
  updated_at            timestamptz NOT NULL DEFAULT now(),
  created_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_provider_webhook_inbox PRIMARY KEY (id),
  CONSTRAINT fk_provider_webhook_inbox_provider FOREIGN KEY (provider_id, is_simulated) REFERENCES app.payment_providers (id, is_sandbox) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_webhook_inbox_account FOREIGN KEY (provider_account_id, provider_id) REFERENCES app.provider_accounts (id, provider_id) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_webhook_inbox_raw_object_id FOREIGN KEY (raw_object_id) REFERENCES app.stored_objects (id) ON DELETE RESTRICT,
  CONSTRAINT uq_provider_webhook_inbox_event UNIQUE (provider_id, provider_event_id),
  CONSTRAINT ck_provider_webhook_inbox_event_id CHECK (char_length(provider_event_id) BETWEEN 1 AND 200),
  CONSTRAINT ck_provider_webhook_inbox_event_class CHECK (event_class IN ('PAYMENT','REFUND','DISPUTE','PAYOUT','OTHER')),
  CONSTRAINT ck_provider_webhook_inbox_verification_method CHECK (verification_method IN ('SIGNATURE','STATUS_API_CONFIRM')),
  CONSTRAINT ck_provider_webhook_inbox_verified CHECK (signature_verified OR verification_method = 'STATUS_API_CONFIRM'),
  CONSTRAINT ck_provider_webhook_inbox_key_slot CHECK (signature_key_slot IS NULL OR signature_key_slot IN ('CURRENT','PREVIOUS')),
  CONSTRAINT ck_provider_webhook_inbox_sha256 CHECK (octet_length(raw_body_sha256) = 32),
  CONSTRAINT ck_provider_webhook_inbox_status CHECK (status IN ('RECEIVED','DISPATCHED','PARKED','FAILED_RETRYING','PROCESSED','UNRECOGNISED','DEAD_LETTERED')),
  CONSTRAINT ck_provider_webhook_inbox_dispatched_to CHECK (dispatched_to IS NULL OR dispatched_to IN ('payments','payouts')),
  CONSTRAINT ck_provider_webhook_inbox_attempts CHECK (attempts >= 0),
  CONSTRAINT ck_provider_webhook_inbox_parked CHECK (
    status <> 'PARKED' OR (parked_subject_id IS NOT NULL AND parked_until_status IS NOT NULL AND parked_at IS NOT NULL AND park_deadline_at IS NOT NULL)),
  CONSTRAINT ck_provider_webhook_inbox_processed CHECK (status <> 'PROCESSED' OR processed_at IS NOT NULL)
);
COMMENT ON TABLE app.provider_webhook_inbox IS 'C2. Verified provider webhooks (redacted payload + hash of the exact verified bytes). Never deleted.';
CREATE INDEX ix_provider_webhook_inbox_work ON app.provider_webhook_inbox (next_attempt_at NULLS FIRST, received_at)
  WHERE status IN ('RECEIVED','FAILED_RETRYING');
CREATE INDEX ix_provider_webhook_inbox_parked ON app.provider_webhook_inbox (parked_subject_id, provider_occurred_at, provider_event_id)
  WHERE status = 'PARKED';
CREATE INDEX ix_provider_webhook_inbox_our_reference ON app.provider_webhook_inbox (our_reference) WHERE our_reference IS NOT NULL;
CREATE INDEX ix_provider_webhook_inbox_status ON app.provider_webhook_inbox (status, received_at DESC, id DESC);
CREATE INDEX ix_provider_webhook_inbox_received ON app.provider_webhook_inbox (received_at DESC, id DESC);

CREATE FUNCTION app.provider_webhook_inbox_raw_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (OLD.provider_id, OLD.provider_account_id, OLD.provider_event_id, OLD.event_class, OLD.event_type_raw,
      OLD.provider_reference, OLD.our_reference, OLD.provider_occurred_at, OLD.received_at,
      OLD.signature_verified, OLD.verification_method, OLD.signature_key_slot, OLD.payload_redacted,
      OLD.raw_body_sha256, OLD.raw_object_id, OLD.headers_subset, OLD.is_simulated, OLD.created_at)
     IS DISTINCT FROM
     (NEW.provider_id, NEW.provider_account_id, NEW.provider_event_id, NEW.event_class, NEW.event_type_raw,
      NEW.provider_reference, NEW.our_reference, NEW.provider_occurred_at, NEW.received_at,
      NEW.signature_verified, NEW.verification_method, NEW.signature_key_slot, NEW.payload_redacted,
      NEW.raw_body_sha256, NEW.raw_object_id, NEW.headers_subset, NEW.is_simulated, NEW.created_at) THEN
    RAISE EXCEPTION 'append-only: raw columns of app.provider_webhook_inbox are immutable'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_provider_webhook_inbox_raw_immutable BEFORE UPDATE ON app.provider_webhook_inbox
  FOR EACH ROW EXECUTE FUNCTION app.provider_webhook_inbox_raw_immutable();
CREATE TRIGGER trg_provider_webhook_inbox_guard_status BEFORE INSERT OR UPDATE OF status ON app.provider_webhook_inbox
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('provider_webhook_inbox');
CREATE TRIGGER trg_provider_webhook_inbox_updated_at BEFORE UPDATE ON app.provider_webhook_inbox
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_provider_webhook_inbox_no_delete BEFORE DELETE ON app.provider_webhook_inbox
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_provider_webhook_inbox_no_truncate BEFORE TRUNCATE ON app.provider_webhook_inbox
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('provider_webhook_inbox', '',                'RECEIVED'),
  ('provider_webhook_inbox', 'RECEIVED',        'DISPATCHED'),
  ('provider_webhook_inbox', 'RECEIVED',        'UNRECOGNISED'),
  ('provider_webhook_inbox', 'DISPATCHED',      'UNRECOGNISED'),
  ('provider_webhook_inbox', 'UNRECOGNISED',    'DISPATCHED'),
  ('provider_webhook_inbox', 'RECEIVED',        'FAILED_RETRYING'),
  ('provider_webhook_inbox', 'DISPATCHED',      'PROCESSED'),
  ('provider_webhook_inbox', 'DISPATCHED',      'PARKED'),
  ('provider_webhook_inbox', 'DISPATCHED',      'FAILED_RETRYING'),
  ('provider_webhook_inbox', 'PARKED',          'DISPATCHED'),
  ('provider_webhook_inbox', 'FAILED_RETRYING', 'DISPATCHED'),
  ('provider_webhook_inbox', 'FAILED_RETRYING', 'DEAD_LETTERED'),
  ('provider_webhook_inbox', 'DEAD_LETTERED',   'DISPATCHED');

-- ---------------------------------------------------------------------------------------------------------
-- Rejected webhooks (ADR-039 §9): NO payload, NO headers — only what operations need to see an attack or a
-- misconfiguration. The ingestion endpoint is rate limited per client before anything is written.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.provider_webhook_rejections (
  id               uuid        NOT NULL,
  provider_id      uuid,                      -- NULL when the path named no known provider
  provider_code    text        NOT NULL,      -- as requested (validated format), for unknown providers
  reason_code      text        NOT NULL,
  body_sha256      bytea,
  body_bytes       integer     NOT NULL,
  received_at      timestamptz NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_provider_webhook_rejections PRIMARY KEY (id),
  CONSTRAINT fk_provider_webhook_rejections_provider FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT ck_provider_webhook_rejections_code CHECK (provider_code ~ '^[a-z][a-z0-9_]{1,31}$'),
  CONSTRAINT ck_provider_webhook_rejections_reason CHECK (reason_code IN ('UNKNOWN_PROVIDER','PROVIDER_DISABLED','NO_ACCOUNT',
    'PAYLOAD_TOO_LARGE','UNSUPPORTED_MEDIA_TYPE','SIGNATURE_MISSING','SIGNATURE_INVALID','UNKNOWN_KEY','TIMESTAMP_OUT_OF_WINDOW',
    'MALFORMED_PAYLOAD','SIMULATION_MISMATCH')),
  CONSTRAINT ck_provider_webhook_rejections_sha256 CHECK (body_sha256 IS NULL OR octet_length(body_sha256) = 32),
  CONSTRAINT ck_provider_webhook_rejections_bytes CHECK (body_bytes >= 0)
);
COMMENT ON TABLE app.provider_webhook_rejections IS 'C1. Rejected webhook deliveries without payload (ADR-039 §9). Append-only.';
CREATE INDEX ix_provider_webhook_rejections_received ON app.provider_webhook_rejections (received_at DESC, id DESC);
CREATE TRIGGER trg_provider_webhook_rejections_no_mutation BEFORE UPDATE OR DELETE ON app.provider_webhook_rejections
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_provider_webhook_rejections_no_truncate BEFORE TRUNCATE ON app.provider_webhook_rejections
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- ---------------------------------------------------------------------------------------------------------
-- Provider health (append-only). Circuit state = latest CIRCUIT_* per (provider, operation, currency).
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.provider_health_events (
  id            uuid        NOT NULL,
  provider_id   uuid        NOT NULL,
  operation     text        NOT NULL,
  method        text,
  currency      char(3),
  event_kind    text        NOT NULL,
  error_class   text,
  latency_ms    integer,
  detail        jsonb       NOT NULL DEFAULT '{}'::jsonb,
  actor_id      uuid,
  observed_at   timestamptz NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_provider_health_events PRIMARY KEY (id),
  CONSTRAINT fk_provider_health_events_provider_id FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_health_events_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_health_events_actor_id FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_provider_health_events_operation CHECK (operation IN ('CREATE_PAYMENT','GET_PAYMENT','CANCEL_PAYMENT','REFUND','GET_REFUND','CREATE_PAYOUT','GET_PAYOUT','WEBHOOK','STATEMENT','ALL')),
  CONSTRAINT ck_provider_health_events_kind CHECK (event_kind IN ('CALL_OK','CALL_ERROR','TIMEOUT','CIRCUIT_OPENED','CIRCUIT_HALF_OPEN','CIRCUIT_CLOSED','OUTAGE_DECLARED','OUTAGE_RESOLVED',
                                                         'SETTLEMENT_RECONCILED','RECON_SEV1_OPENED','RECON_SEV1_CLOSED')),
  CONSTRAINT ck_provider_health_events_recon CHECK (
    event_kind NOT IN ('SETTLEMENT_RECONCILED','RECON_SEV1_OPENED','RECON_SEV1_CLOSED')
    OR (currency IS NOT NULL AND operation = 'STATEMENT')),
  CONSTRAINT ck_provider_health_events_error_class CHECK (error_class IS NULL OR error_class IN ('DEFINITE_FAILURE','RETRYABLE_BEFORE_SEND','INDETERMINATE','AUTH_CONFIG_ERROR','RATE_LIMITED','NOT_SUPPORTED','INVALID_REQUEST','NOT_FOUND')),
  CONSTRAINT ck_provider_health_events_latency CHECK (latency_ms IS NULL OR latency_ms >= 0),
  CONSTRAINT ck_provider_health_events_staff CHECK (event_kind NOT IN ('OUTAGE_DECLARED','OUTAGE_RESOLVED') OR actor_id IS NOT NULL)
);
COMMENT ON TABLE app.provider_health_events IS 'C1. Append-only provider health and circuit events. No payloads, no secrets.';
CREATE INDEX ix_provider_health_events_latest ON app.provider_health_events (provider_id, operation, observed_at DESC);
CREATE TRIGGER trg_provider_health_events_no_mutation BEFORE UPDATE OR DELETE ON app.provider_health_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_provider_health_events_no_truncate BEFORE TRUNCATE ON app.provider_health_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- ---------------------------------------------------------------------------------------------------------
-- Sandbox simulator state: what the SIMULATED provider "knows" (its own ledger of simulated payments). This
-- is the simulator's private store, not FundZim's view of a payment; FundZim's view is app.payment_intents.
-- Never used for a real provider (the FK pins is_sandbox = true).
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.sandbox_provider_payments (
  id                   uuid        NOT NULL,
  provider_id          uuid        NOT NULL,
  is_sandbox           boolean     NOT NULL DEFAULT true,
  provider_payment_ref text        NOT NULL,
  our_reference        uuid        NOT NULL,
  method               text        NOT NULL,
  currency             char(3)     NOT NULL,
  amount_minor         bigint      NOT NULL,
  scenario             text        NOT NULL,
  raw_status           text        NOT NULL,
  return_url           text,                    -- where the simulated provider sends the donor back
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_sandbox_provider_payments PRIMARY KEY (id),
  CONSTRAINT fk_sandbox_provider_payments_provider FOREIGN KEY (provider_id, is_sandbox) REFERENCES app.payment_providers (id, is_sandbox) ON DELETE RESTRICT,
  CONSTRAINT uq_sandbox_provider_payments_ref UNIQUE (provider_id, provider_payment_ref),
  CONSTRAINT uq_sandbox_provider_payments_our_ref UNIQUE (provider_id, our_reference),
  CONSTRAINT ck_sandbox_provider_payments_sandbox CHECK (is_sandbox),
  CONSTRAINT ck_sandbox_provider_payments_amount CHECK (amount_minor > 0)
);
COMMENT ON TABLE app.sandbox_provider_payments IS 'C1. Sandbox SIMULATOR provider-side state (never real). ADR-039 §6.';
CREATE TRIGGER trg_sandbox_provider_payments_updated_at BEFORE UPDATE ON app.sandbox_provider_payments
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- Simulator webhook schedule: webhooks the SIMULATED provider will send later (delays, duplicates, wrong keys,
-- stale timestamps are scenario facts). A worker job signs and POSTs due rows to the real ingestion endpoint.
CREATE TABLE app.sandbox_scheduled_webhooks (
  id                 uuid        NOT NULL,
  sandbox_payment_id uuid        NOT NULL,
  provider_event_id  text        NOT NULL,
  event_type         text        NOT NULL,
  raw_status         text        NOT NULL,
  amount_minor       bigint      NOT NULL,
  currency           char(3)     NOT NULL,
  sign_with          text        NOT NULL DEFAULT 'current',
  timestamp_offset_seconds integer NOT NULL DEFAULT 0,
  due_at             timestamptz NOT NULL,
  delivered_at       timestamptz,
  delivery_status    integer,
  attempts           integer     NOT NULL DEFAULT 0,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_sandbox_scheduled_webhooks PRIMARY KEY (id),
  CONSTRAINT fk_sandbox_scheduled_webhooks_payment FOREIGN KEY (sandbox_payment_id) REFERENCES app.sandbox_provider_payments (id) ON DELETE RESTRICT,
  CONSTRAINT ck_sandbox_scheduled_webhooks_sign_with CHECK (sign_with IN ('current','previous','wrong','none')),
  CONSTRAINT ck_sandbox_scheduled_webhooks_amount CHECK (amount_minor > 0)
);
COMMENT ON TABLE app.sandbox_scheduled_webhooks IS 'C1. Sandbox SIMULATOR outgoing webhook schedule (never real).';
CREATE INDEX ix_sandbox_scheduled_webhooks_due ON app.sandbox_scheduled_webhooks (due_at) WHERE delivered_at IS NULL;

-- ---------------------------------------------------------------------------------------------------------
-- Seed: the sandbox simulator only (docs/stage-8/provider-capability-register.md §2). Deterministic ids.
-- Capabilities are sandbox contract facts, recorded by the seeded system actor.
-- ---------------------------------------------------------------------------------------------------------
INSERT INTO app.payment_providers (id, code, display_name, is_sandbox, selection_status, status, confirmation_mode)
VALUES (md5('payment_provider:sandbox')::uuid, 'sandbox', 'FundZim sandbox simulator (not a real provider)', true, 'PENDING', 'ENABLED', 'WEBHOOK_SIGNED');

INSERT INTO app.provider_accounts (id, provider_id, environment, account_label, merchant_reference,
  api_credentials_secret_ref, webhook_secret_ref, webhook_secret_ref_previous, webhook_replay_window_seconds, status)
VALUES (md5('provider_account:sandbox:SANDBOX:default')::uuid, md5('payment_provider:sandbox')::uuid, 'SANDBOX', 'default', 'fz-sandbox-merchant',
  'secretref://fundzim/sandbox/api', 'secretref://fundzim/sandbox/webhook-current', 'secretref://fundzim/sandbox/webhook-previous', 300, 'ENABLED');

INSERT INTO app.provider_capabilities (id, provider_id, capability_version, method, currency, settlement_currency, custody_model,
  refund_api, partial_refund, idempotent_create, supports_cancel, signed_webhooks, status_api, documented_expiry, documented_expiry_seconds,
  statement_format, evidence_status, evidence_source, recorded_by) VALUES
  (md5('provider_capability:sandbox:ECOCASH:USD:1')::uuid, md5('payment_provider:sandbox')::uuid, 1, 'ECOCASH', 'USD', 'USD', 'PSP_POOL',
   true, true, true, true, true, true, false, NULL, 'API', 'VERIFIED', 'sandbox contract: docs/stage-8/provider-interface.md §6', '00000000-0000-0000-0000-000000000001'),
  (md5('provider_capability:sandbox:ONEMONEY:USD:1')::uuid, md5('payment_provider:sandbox')::uuid, 1, 'ONEMONEY', 'USD', 'USD', 'PSP_POOL',
   true, true, false, true, true, true, false, NULL, 'API', 'VERIFIED', 'sandbox contract: docs/stage-8/provider-interface.md §6', '00000000-0000-0000-0000-000000000001'),
  (md5('provider_capability:sandbox:CARD:USD:1')::uuid, md5('payment_provider:sandbox')::uuid, 1, 'CARD', 'USD', 'USD', 'PSP_POOL',
   true, true, false, true, true, true, true, 900, 'API', 'VERIFIED', 'sandbox contract: docs/stage-8/provider-interface.md §6', '00000000-0000-0000-0000-000000000001'),
  -- Model B in substance: recorded so routing can be proven to refuse it (never routable).
  (md5('provider_capability:sandbox:BANK_TRANSFER:USD:1')::uuid, md5('payment_provider:sandbox')::uuid, 1, 'BANK_TRANSFER', 'USD', 'USD', 'MERCHANT_SETTLEMENT',
   false, false, false, false, true, true, false, NULL, 'API', 'VERIFIED', 'sandbox contract: docs/stage-8/provider-interface.md §6', '00000000-0000-0000-0000-000000000001'),
  -- Unverified evidence: recorded so routing can be proven to refuse it (never routable).
  (md5('provider_capability:sandbox:INNBUCKS:USD:1')::uuid, md5('payment_provider:sandbox')::uuid, 1, 'INNBUCKS', 'USD', 'USD', 'PSP_POOL',
   false, false, false, false, true, true, false, NULL, 'UNKNOWN', 'UNVERIFIED', NULL, '00000000-0000-0000-0000-000000000001');

-- Kill switch for real payment execution (ADR-039 §6). Seeded OFF; turning it on needs maker-checker AND a
-- code change (psp.LiveExecutionEnabled is a constant false).
INSERT INTO app.feature_flags (id, key, description, flag_type, enabled, requires_approval, legal_ref) VALUES
  (md5('feature_flag:payments.live_execution.enabled')::uuid, 'payments.live_execution.enabled',
   'Kill switch for real payment execution through non-sandbox providers. Off: no provider is selected and legal sign-off is missing.',
   'KILL_SWITCH', false, true, 'LR-001, LR-003, LR-004, LR-075');
INSERT INTO app.feature_flag_changes (id, feature_flag_id, flag_version, old_enabled, new_enabled, requested_by, approved_by, reason, occurred_at) VALUES
  (md5('feature_flag_change:payments.live_execution.enabled:1')::uuid, md5('feature_flag:payments.live_execution.enabled')::uuid, 1, NULL, false,
   '00000000-0000-0000-0000-000000000001', NULL, 'seeded by migration (Stage 8, ADR-039)', now());

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.feature_flag_changes DISABLE TRIGGER USER;
DELETE FROM app.feature_flag_changes WHERE feature_flag_id = md5('feature_flag:payments.live_execution.enabled')::uuid;
ALTER TABLE app.feature_flag_changes ENABLE TRIGGER USER;
ALTER TABLE app.feature_flags DISABLE TRIGGER USER;
DELETE FROM app.feature_flags WHERE key = 'payments.live_execution.enabled';
ALTER TABLE app.feature_flags ENABLE TRIGGER USER;
DROP TABLE IF EXISTS app.sandbox_scheduled_webhooks, app.sandbox_provider_payments, app.provider_health_events, app.provider_webhook_rejections,
  app.provider_webhook_inbox, app.provider_accounts;
DROP VIEW IF EXISTS app.provider_capabilities_current;
DROP TABLE IF EXISTS app.provider_capabilities, app.payment_providers;
DROP FUNCTION IF EXISTS app.provider_webhook_inbox_raw_immutable(), app.provider_accounts_guard();
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine = 'provider_webhook_inbox';
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
