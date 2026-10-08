-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0007: fees (fee_schedules, fee_schedule_versions, fee_schedule_change_requests) and
--            psp (payment_providers, provider_capabilities, provider_accounts, provider_webhook_inbox,
--            provider_health_events). Owners: `fees` and `psp` modules. Schema: app.
-- Explanations: docs/database/payment-schema.md §2–§3, docs/architecture/payment-provider-interfaces.md.
-- Depends on: 0001 (shared functions), 0002 (app.currencies), 0004 (app.users), 0005 (app.stored_objects).
-- =====================================================================================================

-- -----------------------------------------------------------------------------------------------------
-- Shared vocabularies used by fees, psp, payments and payouts (CHECK lists, not ENUM types: DATABASE §6).
-- Methods (collection): ECOCASH, ONEMONEY, INNBUCKS, OMARI, ZIMSWITCH, BANK_TRANSFER, CARD (PAYMENTS §3).
-- Payout rails:         ECOCASH, ONEMONEY, INNBUCKS, OMARI, ZIMSWITCH, BANK_TRANSFER.
-- -----------------------------------------------------------------------------------------------------

-- =====================================================================================================
-- FEES
-- =====================================================================================================

-- A fee schedule is a named, long-lived configuration (e.g. the platform fee). Its numbers live only in
-- immutable versions. The schedule row itself carries no money.
CREATE TABLE app.fee_schedules (
  id           uuid        NOT NULL,
  code         text        NOT NULL,
  name         text        NOT NULL,
  fee_kind     text        NOT NULL,
  status       text        NOT NULL DEFAULT 'ACTIVE',
  version      integer     NOT NULL DEFAULT 1,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_schedules PRIMARY KEY (id),
  CONSTRAINT uq_fee_schedules_code UNIQUE (code),
  CONSTRAINT ck_fee_schedules_code CHECK (code ~ '^[a-z][a-z0-9_.]{2,63}$'),
  -- PSP processing fees are reported by providers (T2), not configured here; only FundZim's own fees are.
  CONSTRAINT ck_fee_schedules_fee_kind CHECK (fee_kind IN ('PLATFORM_FEE')),
  CONSTRAINT ck_fee_schedules_status CHECK (status IN ('ACTIVE','RETIRED')),
  CONSTRAINT ck_fee_schedules_version CHECK (version >= 1)
);
CREATE TRIGGER trg_fee_schedules_updated_at BEFORE UPDATE ON app.fee_schedules
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_schedules_keep_created_at BEFORE UPDATE ON app.fee_schedules
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();

-- Maker-checker request for a fee change (operational-controls §3: maker FINANCE, checker business owner;
-- versioned; effective in the future, never retroactive; 7-day expiry).
CREATE TABLE app.fee_schedule_change_requests (
  id                 uuid        NOT NULL,
  fee_schedule_id    uuid        NOT NULL,
  currency           char(3)     NOT NULL,
  proposed_rate_bps  integer     NOT NULL,
  proposed_fixed_minor bigint    NOT NULL DEFAULT 0,
  proposed_min_fee_minor bigint,
  proposed_max_fee_minor bigint,
  proposed_rounding_mode text    NOT NULL DEFAULT 'HALF_UP',
  proposed_effective_from timestamptz NOT NULL,
  justification      text        NOT NULL,
  status             text        NOT NULL,
  requested_by       uuid        NOT NULL,
  requested_at       timestamptz NOT NULL DEFAULT now(),
  expires_at         timestamptz NOT NULL,
  decided_by         uuid,
  decided_at         timestamptz,
  decision_reason    text,
  decided_step_up_at timestamptz,           -- fresh MFA timestamp of the checker (STEP_UP_MAX_AGE)
  proposal_hash      bytea       NOT NULL,  -- SHA-256 of the canonical proposal the checker saw
  version            integer     NOT NULL DEFAULT 1,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_schedule_change_requests PRIMARY KEY (id),
  CONSTRAINT fk_fee_schedule_change_requests_fee_schedule_id FOREIGN KEY (fee_schedule_id) REFERENCES app.fee_schedules (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_schedule_change_requests_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_schedule_change_requests_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_schedule_change_requests_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_fee_schedule_change_requests_status CHECK (status IN ('PENDING','APPROVED','REJECTED','EXPIRED','WITHDRAWN')),
  CONSTRAINT ck_fee_schedule_change_requests_rate CHECK (proposed_rate_bps BETWEEN 0 AND 10000),
  CONSTRAINT ck_fee_schedule_change_requests_fixed CHECK (proposed_fixed_minor >= 0),
  CONSTRAINT ck_fee_schedule_change_requests_min CHECK (proposed_min_fee_minor IS NULL OR proposed_min_fee_minor >= 0),
  CONSTRAINT ck_fee_schedule_change_requests_max CHECK (proposed_max_fee_minor IS NULL OR proposed_max_fee_minor >= COALESCE(proposed_min_fee_minor, 0)),
  CONSTRAINT ck_fee_schedule_change_requests_rounding CHECK (proposed_rounding_mode IN ('HALF_UP','FLOOR','CEIL')),
  -- Maker-checker: the requester can never decide their own request.
  CONSTRAINT ck_fee_schedule_change_requests_maker_checker CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT ck_fee_schedule_change_requests_decision CHECK (
    (status IN ('APPROVED','REJECTED') AND decided_by IS NOT NULL AND decided_at IS NOT NULL AND decision_reason IS NOT NULL)
    OR (status NOT IN ('APPROVED','REJECTED') AND decided_by IS NULL)),
  CONSTRAINT ck_fee_schedule_change_requests_step_up CHECK (status <> 'APPROVED' OR decided_step_up_at IS NOT NULL),
  CONSTRAINT ck_fee_schedule_change_requests_expiry CHECK (expires_at > requested_at),
  -- A proposal may only take effect after it was requested (never retroactive).
  CONSTRAINT ck_fee_schedule_change_requests_future CHECK (proposed_effective_from > requested_at)
);
CREATE INDEX ix_fee_schedule_change_requests_fee_schedule_id ON app.fee_schedule_change_requests (fee_schedule_id);
CREATE INDEX ix_fee_schedule_change_requests_pending ON app.fee_schedule_change_requests (expires_at) WHERE status = 'PENDING';
CREATE TRIGGER trg_fee_schedule_change_requests_guard_status BEFORE INSERT OR UPDATE OF status ON app.fee_schedule_change_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('fee_schedule_change_request');
CREATE TRIGGER trg_fee_schedule_change_requests_updated_at BEFORE UPDATE ON app.fee_schedule_change_requests
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_fee_schedule_change_requests_keep_created_at BEFORE UPDATE ON app.fee_schedule_change_requests
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('fee_schedule_change_request', '',        'PENDING'),
  ('fee_schedule_change_request', 'PENDING', 'APPROVED'),
  ('fee_schedule_change_request', 'PENDING', 'REJECTED'),
  ('fee_schedule_change_request', 'PENDING', 'EXPIRED'),
  ('fee_schedule_change_request', 'PENDING', 'WITHDRAWN');

-- One immutable version of a schedule for one currency. Rates are integer basis points (MONEY §5).
-- Resolution: the non-cancelled version with the greatest effective_from <= t. A version is created only
-- from an APPROVED change request, only with a future effective_from, and is immutable once effective.
-- The only permitted change is cancelling a version that is NOT yet effective (cancelled_at/cancelled_by).
CREATE TABLE app.fee_schedule_versions (
  id                 uuid        NOT NULL,
  fee_schedule_id    uuid        NOT NULL,
  version_no         integer     NOT NULL,
  currency           char(3)     NOT NULL,
  rate_bps           integer     NOT NULL,
  fixed_minor        bigint      NOT NULL DEFAULT 0,
  min_fee_minor      bigint,
  max_fee_minor      bigint,
  rounding_mode      text        NOT NULL DEFAULT 'HALF_UP',
  effective_from     timestamptz NOT NULL,
  change_request_id  uuid        NOT NULL,
  cancelled_at       timestamptz,
  cancelled_by       uuid,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fee_schedule_versions PRIMARY KEY (id),
  CONSTRAINT fk_fee_schedule_versions_fee_schedule_id FOREIGN KEY (fee_schedule_id) REFERENCES app.fee_schedules (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_schedule_versions_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_schedule_versions_change_request_id FOREIGN KEY (change_request_id) REFERENCES app.fee_schedule_change_requests (id) ON DELETE RESTRICT,
  CONSTRAINT fk_fee_schedule_versions_cancelled_by FOREIGN KEY (cancelled_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_fee_schedule_versions_no UNIQUE (fee_schedule_id, currency, version_no),
  CONSTRAINT uq_fee_schedule_versions_change_request_id UNIQUE (change_request_id),
  CONSTRAINT ck_fee_schedule_versions_rate CHECK (rate_bps BETWEEN 0 AND 10000),
  CONSTRAINT ck_fee_schedule_versions_fixed CHECK (fixed_minor >= 0),
  CONSTRAINT ck_fee_schedule_versions_min CHECK (min_fee_minor IS NULL OR min_fee_minor >= 0),
  CONSTRAINT ck_fee_schedule_versions_max CHECK (max_fee_minor IS NULL OR max_fee_minor >= COALESCE(min_fee_minor, 0)),
  CONSTRAINT ck_fee_schedule_versions_rounding CHECK (rounding_mode IN ('HALF_UP','FLOOR','CEIL')),
  CONSTRAINT ck_fee_schedule_versions_version_no CHECK (version_no >= 1),
  CONSTRAINT ck_fee_schedule_versions_cancel CHECK ((cancelled_at IS NULL) = (cancelled_by IS NULL))
);
-- Two live versions of one schedule+currency may not start at the same instant.
CREATE UNIQUE INDEX uq_fee_schedule_versions_effective ON app.fee_schedule_versions (fee_schedule_id, currency, effective_from)
  WHERE cancelled_at IS NULL;

CREATE FUNCTION app.fee_schedule_versions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  cr app.fee_schedule_change_requests%ROWTYPE;
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.effective_from <= now() THEN
      RAISE EXCEPTION 'fee schedule version effective_from must be in the future (never retroactive)'
        USING ERRCODE = 'check_violation';
    END IF;
    SELECT * INTO cr FROM app.fee_schedule_change_requests WHERE id = NEW.change_request_id;
    IF cr.status IS DISTINCT FROM 'APPROVED' THEN
      RAISE EXCEPTION 'fee schedule version requires an APPROVED change request' USING ERRCODE = 'check_violation';
    END IF;
    IF cr.fee_schedule_id <> NEW.fee_schedule_id OR cr.currency <> NEW.currency
       OR cr.proposed_rate_bps <> NEW.rate_bps OR cr.proposed_fixed_minor <> NEW.fixed_minor
       OR cr.proposed_min_fee_minor IS DISTINCT FROM NEW.min_fee_minor
       OR cr.proposed_max_fee_minor IS DISTINCT FROM NEW.max_fee_minor
       OR cr.proposed_rounding_mode <> NEW.rounding_mode OR cr.proposed_effective_from <> NEW.effective_from THEN
      RAISE EXCEPTION 'fee schedule version differs from the approved change request' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;
  -- UPDATE: only cancellation of a not-yet-effective version; every other column is immutable.
  IF OLD.effective_from <= now() THEN
    RAISE EXCEPTION 'fee schedule version is effective and immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.cancelled_at IS NOT NULL OR NEW.cancelled_at IS NULL
     OR (to_jsonb(NEW) - 'cancelled_at' - 'cancelled_by') <> (to_jsonb(OLD) - 'cancelled_at' - 'cancelled_by') THEN
    RAISE EXCEPTION 'fee schedule version: only a one-time cancellation before effective_from is allowed'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_fee_schedule_versions_guard BEFORE INSERT OR UPDATE ON app.fee_schedule_versions
  FOR EACH ROW EXECUTE FUNCTION app.fee_schedule_versions_guard();
CREATE TRIGGER trg_fee_schedule_versions_no_delete BEFORE DELETE ON app.fee_schedule_versions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fee_schedule_versions_no_truncate BEFORE TRUNCATE ON app.fee_schedule_versions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- PSP: provider registry
-- =====================================================================================================

-- Provider registry. Candidate providers are listed with selection_status PENDING; nothing here asserts
-- that any provider is licensed, contracted or suitable (provider-comparison.md: selection PENDING).
CREATE TABLE app.payment_providers (
  id                uuid        NOT NULL,
  code              text        NOT NULL,          -- stable adapter id, e.g. 'sandbox'
  display_name      text        NOT NULL,
  is_sandbox        boolean     NOT NULL DEFAULT false,
  selection_status  text        NOT NULL DEFAULT 'PENDING',
  status            text        NOT NULL DEFAULT 'DISABLED',
  confirmation_mode text        NOT NULL,          -- how authoritative state is obtained
  selection_adr     text,                          -- ADR id that selected it (Stage 9); NULL while PENDING
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_providers PRIMARY KEY (id),
  CONSTRAINT uq_payment_providers_code UNIQUE (code),
  CONSTRAINT ck_payment_providers_code CHECK (code ~ '^[a-z][a-z0-9_]{1,31}$'),
  CONSTRAINT ck_payment_providers_selection_status CHECK (selection_status IN ('PENDING','SELECTED','REJECTED')),
  CONSTRAINT ck_payment_providers_status CHECK (status IN ('DISABLED','ENABLED','RETIRED')),
  CONSTRAINT ck_payment_providers_confirmation_mode CHECK (confirmation_mode IN ('WEBHOOK_SIGNED','WEBHOOK_UNSIGNED_VERIFY_BY_API','POLL_ONLY')),
  -- A real provider can be ENABLED only once selected under an ADR. The sandbox is never "selected".
  CONSTRAINT ck_payment_providers_enabled_requires_selection CHECK (
    status <> 'ENABLED' OR is_sandbox OR (selection_status = 'SELECTED' AND selection_adr IS NOT NULL)),
  CONSTRAINT ck_payment_providers_sandbox_not_selected CHECK (NOT is_sandbox OR selection_status = 'PENDING')
);
CREATE TRIGGER trg_payment_providers_updated_at BEFORE UPDATE ON app.payment_providers
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payment_providers_keep_created_at BEFORE UPDATE ON app.payment_providers
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();

-- Capability registry, versioned and append-only: one row = one evidence-backed statement about what a
-- provider can do for (method, currency). A change is a new row with a higher capability_version; the
-- current row for (provider, method, currency) is the highest version. Every boolean flag defaults to
-- false = ABSENT: a flag is true only when its evidence is VERIFIED (capability-matrix rule 2).
-- flag_evidence records, per flag, {status, source, pcr} so unverified claims stay visible but inert.
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
  evidence_source     text,          -- doc URL + access date, or contract reference
  pcr_ref             text,          -- e.g. 'PCR-013' while confirmation is outstanding
  flag_evidence       jsonb       NOT NULL DEFAULT '{}'::jsonb,
  recorded_by         uuid        NOT NULL,
  recorded_at         timestamptz NOT NULL DEFAULT now(),
  -- Routable = may be used by the router for campaign donations. Only VERIFIED evidence, never
  -- MERCHANT_SETTLEMENT (Model B in substance, ADR-013), and an authoritative status path must exist
  -- (signed webhooks or an authenticated status API). Currency equality is a CHECK below.
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
  -- Targets for declarative composite FKs from payments/payouts: they pin the routing facts (provider,
  -- method, currency, custody model, routable, payout_api) of the capability row a payment or payout was
  -- routed on, so a non-routable or MERCHANT_SETTLEMENT capability can never be referenced, without any
  -- cross-module trigger reading psp tables.
  CONSTRAINT uq_provider_capabilities_routing_key UNIQUE (id, provider_id, method, currency, custody_model, routable),
  CONSTRAINT uq_provider_capabilities_payout_key UNIQUE (id, provider_id, currency, routable, payout_api),
  CONSTRAINT ck_provider_capabilities_version CHECK (capability_version >= 1),
  CONSTRAINT ck_provider_capabilities_method CHECK (method IN ('ECOCASH','ONEMONEY','INNBUCKS','OMARI','ZIMSWITCH','BANK_TRANSFER','CARD')),
  CONSTRAINT ck_provider_capabilities_custody_model CHECK (custody_model IN ('PSP_POOL','MERCHANT_SETTLEMENT','SPLIT_DIRECT')),
  CONSTRAINT ck_provider_capabilities_evidence_status CHECK (evidence_status IN ('VERIFIED','UNVERIFIED','NOT_SUPPORTED','REQUIRES_PROVIDER_CONFIRMATION')),
  -- ADR-018 / currency-and-fx-policy rule 5: a rail is recorded (and offered) only if it settles in the
  -- donation currency. FundZim never converts.
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
  -- Evidence that is not VERIFIED must say what confirmation is pending, or where the claim came from.
  CONSTRAINT ck_provider_capabilities_evidence_ref CHECK (
    (evidence_status = 'VERIFIED' AND evidence_source IS NOT NULL)
    OR (evidence_status = 'REQUIRES_PROVIDER_CONFIRMATION' AND pcr_ref ~ '^PCR-[0-9]{3}$')
    OR evidence_status IN ('UNVERIFIED','NOT_SUPPORTED')),
  -- NOT_SUPPORTED rows assert absence: no flag may be true.
  CONSTRAINT ck_provider_capabilities_not_supported_flags CHECK (
    evidence_status <> 'NOT_SUPPORTED' OR NOT (separate_capture OR refund_api OR payout_api OR split_settlement
      OR pool_balance_api OR signed_webhooks OR status_api OR documented_expiry OR idempotent_create))
);
CREATE INDEX ix_provider_capabilities_routing ON app.provider_capabilities (method, currency) WHERE routable;
CREATE TRIGGER trg_provider_capabilities_no_mutation BEFORE UPDATE OR DELETE ON app.provider_capabilities
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_provider_capabilities_no_truncate BEFORE TRUNCATE ON app.provider_capabilities
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Current capability per (provider, method, currency). Routing reads this view only.
CREATE VIEW app.provider_capabilities_current AS
  SELECT DISTINCT ON (provider_id, method, currency) *
    FROM app.provider_capabilities
   ORDER BY provider_id, method, currency, capability_version DESC;

-- Provider account configuration per environment. Holds secret REFERENCES only (a secret-manager path),
-- never secret values. The format CHECK rejects anything that is not a reference.
CREATE TABLE app.provider_accounts (
  id                          uuid        NOT NULL,
  provider_id                 uuid        NOT NULL,
  environment                 text        NOT NULL,
  account_label               text        NOT NULL,
  merchant_reference          text,        -- non-secret merchant/integration id as issued by the provider
  api_credentials_secret_ref  text        NOT NULL,
  webhook_secret_ref          text,
  webhook_secret_ref_previous text,        -- rotation overlap: two active secrets (PAYMENTS §9)
  webhook_replay_window_seconds integer,   -- 0/NULL = provider sends no timestamp
  status                      text        NOT NULL DEFAULT 'DISABLED',
  version                     integer     NOT NULL DEFAULT 1,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
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
CREATE INDEX ix_provider_accounts_provider_id ON app.provider_accounts (provider_id);

-- The sandbox provider can never be configured for PRODUCTION (PAYMENTS §14; startup check is the
-- primary control, this is defence in depth).
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

-- =====================================================================================================
-- PSP: webhook inbox
-- =====================================================================================================
-- One endpoint per provider receives payment, refund, dispute and payout events (baseline §5.8).
-- Only VERIFIED events are stored (signature + replay window); a failed verification is a 401 and a
-- metric, nothing is stored (PAYMENTS §9). For WEBHOOK_UNSIGNED_VERIFY_BY_API providers the event is stored
-- unverified and MUST be confirmed by an authenticated status query before it changes any state.
--
-- Two column groups (DATABASE §9 "processing-status columns may be updated"):
--   * RAW group (immutable, enforced by trigger): identity, payload, verification facts;
--   * PROCESSING group (mutable): status, dispatch, attempts, parking. Grants (0018) give fundzim_app
--     column-level UPDATE on the processing group only.
-- `status` is the processing status (guarded machine 'provider_webhook_inbox'). Events of class OTHER or
-- of an unmapped type are never silently ignored: they are kept with status UNRECOGNISED and raise an
-- alert (unmapped provider event); an unmapped raw payment/payout status maps to UNKNOWN + alert (ADR-024).
CREATE TABLE app.provider_webhook_inbox (
  id                    uuid        NOT NULL,
  -- RAW group --------------------------------------------------------------------------------------
  provider_id           uuid        NOT NULL,
  provider_account_id   uuid,
  provider_event_id     text        NOT NULL,
  event_class           text        NOT NULL,
  event_type_raw        text        NOT NULL,     -- provider's event name, verbatim
  provider_reference    text,                     -- provider's payment/refund/dispute/payout ref, verbatim
  our_reference         uuid,                     -- payment_id / refund_id / payout_id if the event carries it
  provider_occurred_at  timestamptz,              -- provider timestamp, if supplied
  received_at           timestamptz NOT NULL DEFAULT now(),
  signature_verified    boolean     NOT NULL,
  verification_method   text        NOT NULL,
  signature_key_slot    text,                     -- 'CURRENT' | 'PREVIOUS' (rotation overlap)
  payload_redacted      jsonb       NOT NULL,     -- masked per redaction allow-list; never raw MSISDN/PAN
  raw_body_sha256       bytea       NOT NULL,     -- hash of the exact bytes verified
  raw_object_id         uuid,                     -- optional encrypted raw body in the private bucket (evidence)
  headers_subset        jsonb       NOT NULL DEFAULT '{}'::jsonb,
  -- PROCESSING group -------------------------------------------------------------------------------
  status                text        NOT NULL DEFAULT 'RECEIVED',
  dispatched_to         text,                     -- 'payments' | 'payouts'
  attempts              integer     NOT NULL DEFAULT 0,
  next_attempt_at       timestamptz,
  last_error_code       text,
  parked_subject_id     uuid,                     -- the payment/payout whose prerequisite is awaited
  parked_until_status   text,
  parked_at             timestamptz,
  park_deadline_at      timestamptz,              -- after this: SEV2 reconciliation case; never discarded
  processed_at          timestamptz,
  version               integer     NOT NULL DEFAULT 1,
  updated_at            timestamptz NOT NULL DEFAULT now(),
  created_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_provider_webhook_inbox PRIMARY KEY (id),
  CONSTRAINT fk_provider_webhook_inbox_provider_id FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_webhook_inbox_account FOREIGN KEY (provider_account_id, provider_id) REFERENCES app.provider_accounts (id, provider_id) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_webhook_inbox_raw_object_id FOREIGN KEY (raw_object_id) REFERENCES app.stored_objects (id) ON DELETE RESTRICT,
  -- Dedupe: the same provider event is stored once; duplicates are acknowledged with 2xx and dropped.
  CONSTRAINT uq_provider_webhook_inbox_event UNIQUE (provider_id, provider_event_id),
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
CREATE INDEX ix_provider_webhook_inbox_work ON app.provider_webhook_inbox (next_attempt_at NULLS FIRST, received_at)
  WHERE status IN ('RECEIVED','FAILED_RETRYING');
CREATE INDEX ix_provider_webhook_inbox_parked ON app.provider_webhook_inbox (parked_subject_id, provider_occurred_at, provider_event_id)
  WHERE status = 'PARKED';
CREATE INDEX ix_provider_webhook_inbox_our_reference ON app.provider_webhook_inbox (our_reference) WHERE our_reference IS NOT NULL;

CREATE FUNCTION app.provider_webhook_inbox_raw_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (OLD.provider_id, OLD.provider_account_id, OLD.provider_event_id, OLD.event_class, OLD.event_type_raw,
      OLD.provider_reference, OLD.our_reference, OLD.provider_occurred_at, OLD.received_at,
      OLD.signature_verified, OLD.verification_method, OLD.signature_key_slot, OLD.payload_redacted,
      OLD.raw_body_sha256, OLD.raw_object_id, OLD.headers_subset, OLD.created_at)
     IS DISTINCT FROM
     (NEW.provider_id, NEW.provider_account_id, NEW.provider_event_id, NEW.event_class, NEW.event_type_raw,
      NEW.provider_reference, NEW.our_reference, NEW.provider_occurred_at, NEW.received_at,
      NEW.signature_verified, NEW.verification_method, NEW.signature_key_slot, NEW.payload_redacted,
      NEW.raw_body_sha256, NEW.raw_object_id, NEW.headers_subset, NEW.created_at) THEN
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
  ('provider_webhook_inbox', 'RECEIVED',        'UNRECOGNISED'),     -- OTHER / unmapped event type: kept + ALERT, never silently dropped
  ('provider_webhook_inbox', 'DISPATCHED',      'UNRECOGNISED'),     -- owning module cannot map it (unmapped raw status)
  ('provider_webhook_inbox', 'UNRECOGNISED',    'DISPATCHED'),       -- re-processed after the adapter mapping is extended
  ('provider_webhook_inbox', 'RECEIVED',        'FAILED_RETRYING'),
  ('provider_webhook_inbox', 'DISPATCHED',      'PROCESSED'),
  ('provider_webhook_inbox', 'DISPATCHED',      'PARKED'),
  ('provider_webhook_inbox', 'DISPATCHED',      'FAILED_RETRYING'),
  ('provider_webhook_inbox', 'PARKED',          'DISPATCHED'),       -- prerequisite reached: re-apply
  ('provider_webhook_inbox', 'FAILED_RETRYING', 'DISPATCHED'),
  ('provider_webhook_inbox', 'FAILED_RETRYING', 'DEAD_LETTERED'),
  ('provider_webhook_inbox', 'DEAD_LETTERED',   'DISPATCHED');       -- audited staff re-queue (never edit)

-- =====================================================================================================
-- PSP: provider health (append-only). Circuit-breaker state for EC-18 is derived from the latest
-- CIRCUIT_* event per (provider, operation, currency). Settlement freshness for EC-20 is derived from the
-- latest SETTLEMENT_RECONCILED per (provider, currency) plus open RECON_SEV1_OPENED without a later
-- RECON_SEV1_CLOSED; reconciliation writes these through psp's Go interface, payouts reads them the same way.
-- =====================================================================================================
CREATE TABLE app.provider_health_events (
  id            uuid        NOT NULL,
  provider_id   uuid        NOT NULL,
  operation     text        NOT NULL,
  method        text,
  currency      char(3),
  event_kind    text        NOT NULL,
  error_class   text,
  latency_ms    integer,
  detail        jsonb       NOT NULL DEFAULT '{}'::jsonb,   -- no payloads, no secrets
  actor_id      uuid,                                       -- staff declaring/ending an outage
  observed_at   timestamptz NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_provider_health_events PRIMARY KEY (id),
  CONSTRAINT fk_provider_health_events_provider_id FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_health_events_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_provider_health_events_actor_id FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_provider_health_events_operation CHECK (operation IN ('CREATE_PAYMENT','GET_PAYMENT','CANCEL_PAYMENT','REFUND','GET_REFUND','CREATE_PAYOUT','GET_PAYOUT','WEBHOOK','STATEMENT','ALL')),
  CONSTRAINT ck_provider_health_events_kind CHECK (event_kind IN ('CALL_OK','CALL_ERROR','TIMEOUT','CIRCUIT_OPENED','CIRCUIT_HALF_OPEN','CIRCUIT_CLOSED','OUTAGE_DECLARED','OUTAGE_RESOLVED',
                                                         'SETTLEMENT_RECONCILED','RECON_SEV1_OPENED','RECON_SEV1_CLOSED')),
  -- EC-20 settlement freshness: written by reconciliation (through the psp interface), per provider + currency.
  CONSTRAINT ck_provider_health_events_recon CHECK (
    event_kind NOT IN ('SETTLEMENT_RECONCILED','RECON_SEV1_OPENED','RECON_SEV1_CLOSED')
    OR (currency IS NOT NULL AND operation = 'STATEMENT')),
  CONSTRAINT ck_provider_health_events_error_class CHECK (error_class IS NULL OR error_class IN ('DEFINITE_FAILURE','RETRYABLE_BEFORE_SEND','INDETERMINATE','AUTH_CONFIG_ERROR','RATE_LIMITED','NOT_SUPPORTED','INVALID_REQUEST')),
  CONSTRAINT ck_provider_health_events_latency CHECK (latency_ms IS NULL OR latency_ms >= 0),
  CONSTRAINT ck_provider_health_events_staff CHECK (event_kind NOT IN ('OUTAGE_DECLARED','OUTAGE_RESOLVED') OR actor_id IS NOT NULL)
);
CREATE INDEX ix_provider_health_events_latest ON app.provider_health_events (provider_id, operation, observed_at DESC);
CREATE TRIGGER trg_provider_health_events_no_mutation BEFORE UPDATE OR DELETE ON app.provider_health_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_provider_health_events_no_truncate BEFORE TRUNCATE ON app.provider_health_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
