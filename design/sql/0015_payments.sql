-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0015: payments module (schema app): donations, payment_intents, payment_attempts,
--            payment_transactions, payment_provider_references, payment_events, refund_requests,
--            refund_transactions, payment_disputes, chargebacks.
-- Explanations: docs/database/payment-schema.md, docs/architecture/payment-state-machine.md.
-- Sources: transaction-lifecycle.md (P1–P20, ranks), refund-and-dispute-architecture.md,
--          refund-and-reversal-flows.md, ADR-020, ADR-024 (baseline §8).
-- References (declared FKs only; no queries into other modules' tables): app.currencies, app.users,
--   app.campaigns, app.payment_providers / provider_accounts / provider_capabilities /
--   provider_webhook_inbox (psp), app.fee_schedule_versions (fees), ledger.ledger_transactions,
--   risk.holds, audit.evidence_records.
-- =====================================================================================================

-- -----------------------------------------------------------------------------------------------------
-- Rank of a payment status (transaction-lifecycle §5 / ADR-020 §5). Used by a CHECK so the stored rank
-- can never disagree with the status, and by payments.applyEvent for precedence.
-- -----------------------------------------------------------------------------------------------------
CREATE FUNCTION app.payment_status_rank(s text) RETURNS smallint LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT CASE s
    WHEN 'CREATED' THEN 0  WHEN 'UNKNOWN' THEN 5   WHEN 'PENDING' THEN 10 WHEN 'REQUIRES_ACTION' THEN 20
    WHEN 'AUTHORISED' THEN 30 WHEN 'FAILED' THEN 50 WHEN 'CANCELLED' THEN 50 WHEN 'EXPIRED' THEN 50
    WHEN 'SUCCEEDED' THEN 60 WHEN 'PARTIALLY_REFUNDED' THEN 70 WHEN 'DISPUTED' THEN 80
    WHEN 'REFUNDED' THEN 90 WHEN 'CHARGED_BACK' THEN 95 END::smallint
$$;

-- Generic "attempt rows are written before the call and completed exactly once" guard. TG_ARGV lists the
-- result columns that may be filled while classification is IN_FLIGHT; everything else is immutable, and
-- a completed attempt is immutable. Reused by app.payout_attempts (0016).
CREATE FUNCTION app.attempt_complete_once() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  mutable_cols text[] := TG_ARGV;
BEGIN
  IF OLD.classification <> 'IN_FLIGHT' THEN
    RAISE EXCEPTION 'append-only: completed attempt on %.% is immutable', TG_TABLE_SCHEMA, TG_TABLE_NAME
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF (to_jsonb(NEW) - mutable_cols) IS DISTINCT FROM (to_jsonb(OLD) - mutable_cols) THEN
    RAISE EXCEPTION 'append-only: only result columns of an in-flight attempt on %.% may be set',
      TG_TABLE_SCHEMA, TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;

-- =====================================================================================================
-- payment_intents — the canonical payment (Stage 1 `payments`; payment_id = payment_intents.id).
-- payment_id is also the merchant reference and provider idempotency key and never changes.
-- =====================================================================================================
CREATE TABLE app.payment_intents (
  id                        uuid        NOT NULL,
  campaign_id               uuid        NOT NULL,
  currency                  char(3)     NOT NULL,
  amount_minor              bigint      NOT NULL,
  method                    text        NOT NULL,
  -- Routing (PAYMENTS §5). Bound for life once a request may have reached the provider.
  provider_id               uuid        NOT NULL,
  provider_account_id       uuid        NOT NULL,
  capability_id             uuid        NOT NULL,
  custody_model             text        NOT NULL,     -- snapshot of the capability row (FK-pinned)
  capability_routable       boolean     NOT NULL,     -- snapshot of provider_capabilities.routable (FK-pinned)
  routing_reason            text        NOT NULL,
  -- Fee quote (fees module computes; payments stores the version used: MONEY §5).
  platform_fee_minor        bigint      NOT NULL,
  fee_schedule_version_id   uuid        NOT NULL,
  -- State
  status                    text        NOT NULL,
  status_rank               smallint    NOT NULL,
  provider_payment_ref      text,
  last_authoritative_source text,
  unknown_since             timestamptz,
  unknown_reason            text,
  next_poll_at              timestamptz,
  poll_attempts             integer     NOT NULL DEFAULT 0,
  poll_horizon_at           timestamptz,
  needs_reconciliation      boolean     NOT NULL DEFAULT false,
  reversal_kind             text,
  possible_duplicate_of     uuid,
  expires_at                timestamptz,             -- provider's documented hard expiry, if any
  -- Client idempotency (backstop to app.idempotency_keys, which expires).
  idempotency_scope         text        NOT NULL,
  idempotency_key           uuid        NOT NULL,
  version                   integer     NOT NULL DEFAULT 1,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_intents PRIMARY KEY (id),
  CONSTRAINT fk_payment_intents_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_intents_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_intents_provider_account FOREIGN KEY (provider_account_id, provider_id) REFERENCES app.provider_accounts (id, provider_id) ON DELETE RESTRICT,
  -- Model A routing guard, declaratively: the referenced capability row must match provider, method,
  -- currency and custody model, and must be routable (VERIFIED, not MERCHANT_SETTLEMENT, authoritative
  -- status path). provider_capabilities is append-only, so the pinned facts cannot change underneath.
  CONSTRAINT fk_payment_intents_capability FOREIGN KEY (capability_id, provider_id, method, currency, custody_model, capability_routable)
    REFERENCES app.provider_capabilities (id, provider_id, method, currency, custody_model, routable) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_intents_fee_schedule_version_id FOREIGN KEY (fee_schedule_version_id) REFERENCES app.fee_schedule_versions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_intents_possible_duplicate_of FOREIGN KEY (possible_duplicate_of) REFERENCES app.payment_intents (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payment_intents_provider_payment_ref UNIQUE (provider_id, provider_payment_ref),  -- NULLs distinct: "where not null"
  CONSTRAINT uq_payment_intents_idempotency UNIQUE (idempotency_scope, idempotency_key),
  CONSTRAINT uq_payment_intents_id_campaign UNIQUE (id, campaign_id),
  CONSTRAINT uq_payment_intents_id_campaign_currency UNIQUE (id, campaign_id, currency),
  CONSTRAINT uq_payment_intents_id_provider UNIQUE (id, provider_id),
  CONSTRAINT ck_payment_intents_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payment_intents_fee CHECK (platform_fee_minor >= 0 AND platform_fee_minor <= amount_minor),
  CONSTRAINT ck_payment_intents_method CHECK (method IN ('ECOCASH','ONEMONEY','INNBUCKS','OMARI','ZIMSWITCH','BANK_TRANSFER','CARD')),
  CONSTRAINT ck_payment_intents_routable CHECK (capability_routable),
  CONSTRAINT ck_payment_intents_custody_model CHECK (custody_model IN ('PSP_POOL','SPLIT_DIRECT')),  -- never MERCHANT_SETTLEMENT
  CONSTRAINT ck_payment_intents_status CHECK (status IN ('CREATED','PENDING','REQUIRES_ACTION','AUTHORISED','UNKNOWN','SUCCEEDED','FAILED','EXPIRED','CANCELLED','PARTIALLY_REFUNDED','REFUNDED','DISPUTED','CHARGED_BACK')),
  CONSTRAINT ck_payment_intents_status_rank CHECK (status_rank = app.payment_status_rank(status)),
  CONSTRAINT ck_payment_intents_source CHECK (last_authoritative_source IS NULL OR last_authoritative_source IN ('SYNC','WEBHOOK','POLL','RECON','INTERNAL','STAFF')),
  CONSTRAINT ck_payment_intents_unknown_reason CHECK (unknown_reason IS NULL OR unknown_reason IN ('TIMEOUT','CONN_RESET_AFTER_SEND','HTTP_5XX','MALFORMED_RESPONSE','SIGNATURE_INVALID_RESPONSE','CRASH_DURING_CREATE')),
  CONSTRAINT ck_payment_intents_unknown CHECK (status <> 'UNKNOWN' OR (unknown_since IS NOT NULL AND unknown_reason IS NOT NULL)),
  CONSTRAINT ck_payment_intents_poll CHECK (poll_attempts >= 0 AND (poll_horizon_at IS NULL OR unknown_since IS NULL OR poll_horizon_at > unknown_since)),
  CONSTRAINT ck_payment_intents_reversal_kind CHECK (reversal_kind IS NULL OR reversal_kind IN ('CARD_CHARGEBACK','PROVIDER_REVERSAL')),
  CONSTRAINT ck_payment_intents_charged_back CHECK ((status = 'CHARGED_BACK') = (reversal_kind IS NOT NULL)),
  CONSTRAINT ck_payment_intents_authorised_needs_capture CHECK (status <> 'AUTHORISED' OR method = 'CARD'),
  CONSTRAINT ck_payment_intents_not_self_duplicate CHECK (possible_duplicate_of IS NULL OR possible_duplicate_of <> id),
  CONSTRAINT ck_payment_intents_version CHECK (version >= 1)
);
CREATE INDEX ix_payment_intents_campaign_status ON app.payment_intents (campaign_id, status);
CREATE INDEX ix_payment_intents_poll ON app.payment_intents (next_poll_at) WHERE status IN ('UNKNOWN','PENDING','REQUIRES_ACTION','AUTHORISED') AND next_poll_at IS NOT NULL;
CREATE INDEX ix_payment_intents_needs_reconciliation ON app.payment_intents (provider_id, created_at) WHERE needs_reconciliation;
CREATE INDEX ix_payment_intents_capability_id ON app.payment_intents (capability_id);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payment_intent', '',                   'CREATED'),             -- P1
  ('payment_intent', 'CREATED',            'PENDING'),             -- P2
  ('payment_intent', 'CREATED',            'UNKNOWN'),             -- P3
  ('payment_intent', 'CREATED',            'FAILED'),              -- P4
  ('payment_intent', 'CREATED',            'CANCELLED'),           -- P5
  ('payment_intent', 'PENDING',            'REQUIRES_ACTION'),     -- P6
  ('payment_intent', 'REQUIRES_ACTION',    'PENDING'),             -- P7
  ('payment_intent', 'PENDING',            'AUTHORISED'),          -- P8
  ('payment_intent', 'REQUIRES_ACTION',    'AUTHORISED'),          -- P8
  ('payment_intent', 'UNKNOWN',            'AUTHORISED'),          -- P8
  ('payment_intent', 'PENDING',            'SUCCEEDED'),           -- P9
  ('payment_intent', 'REQUIRES_ACTION',    'SUCCEEDED'),           -- P9
  ('payment_intent', 'AUTHORISED',         'SUCCEEDED'),           -- P9
  ('payment_intent', 'UNKNOWN',            'SUCCEEDED'),           -- P9
  ('payment_intent', 'PENDING',            'FAILED'),              -- P10
  ('payment_intent', 'REQUIRES_ACTION',    'FAILED'),              -- P10
  ('payment_intent', 'AUTHORISED',         'FAILED'),              -- P10
  ('payment_intent', 'UNKNOWN',            'FAILED'),              -- P10
  ('payment_intent', 'PENDING',            'EXPIRED'),             -- P11
  ('payment_intent', 'REQUIRES_ACTION',    'EXPIRED'),             -- P11
  ('payment_intent', 'AUTHORISED',         'EXPIRED'),             -- P11
  ('payment_intent', 'UNKNOWN',            'EXPIRED'),             -- P11
  ('payment_intent', 'PENDING',            'CANCELLED'),           -- P12
  ('payment_intent', 'REQUIRES_ACTION',    'CANCELLED'),           -- P12
  ('payment_intent', 'AUTHORISED',         'CANCELLED'),           -- P12
  ('payment_intent', 'UNKNOWN',            'CANCELLED'),           -- P12
  ('payment_intent', 'PENDING',            'UNKNOWN'),             -- P13 (INTERNAL source only: payment_events CHECK)
  ('payment_intent', 'UNKNOWN',            'PENDING'),             -- P14
  ('payment_intent', 'UNKNOWN',            'REQUIRES_ACTION'),     -- P14
  ('payment_intent', 'FAILED',             'SUCCEEDED'),           -- P15 late authoritative success
  ('payment_intent', 'EXPIRED',            'SUCCEEDED'),           -- P15
  ('payment_intent', 'CANCELLED',          'SUCCEEDED'),           -- P15
  ('payment_intent', 'SUCCEEDED',          'PARTIALLY_REFUNDED'),  -- P16 (PARTIALLY_REFUNDED -> itself is a self-update)
  ('payment_intent', 'SUCCEEDED',          'REFUNDED'),            -- P16
  ('payment_intent', 'PARTIALLY_REFUNDED', 'REFUNDED'),            -- P16
  ('payment_intent', 'SUCCEEDED',          'DISPUTED'),            -- P17
  ('payment_intent', 'PARTIALLY_REFUNDED', 'DISPUTED'),            -- P17
  ('payment_intent', 'DISPUTED',           'SUCCEEDED'),           -- P18 dispute won
  ('payment_intent', 'DISPUTED',           'PARTIALLY_REFUNDED'),  -- P18b dispute won on a partially refunded payment (Stage 2, ADR-024)
  ('payment_intent', 'DISPUTED',           'CHARGED_BACK'),        -- P19
  ('payment_intent', 'SUCCEEDED',          'CHARGED_BACK'),        -- P20
  ('payment_intent', 'PARTIALLY_REFUNDED', 'CHARGED_BACK');        -- P20
-- Never: anything out of REFUNDED / CHARGED_BACK; SUCCEEDED -> FAILED; any -> CREATED.

-- Immutable facts of an intent; provider binding rules (PAYMENTS §5: fallback only before a request may
-- have reached a provider; provider_payment_ref is set once).
CREATE FUNCTION app.payment_intents_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.campaign_id <> OLD.campaign_id OR NEW.currency <> OLD.currency OR NEW.amount_minor <> OLD.amount_minor
     OR NEW.method <> OLD.method OR NEW.idempotency_scope <> OLD.idempotency_scope
     OR NEW.idempotency_key <> OLD.idempotency_key OR NEW.platform_fee_minor <> OLD.platform_fee_minor
     OR NEW.fee_schedule_version_id <> OLD.fee_schedule_version_id THEN
    RAISE EXCEPTION 'payment intent amount, currency, campaign, method, fee and idempotency are immutable'
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.provider_payment_ref IS NOT NULL AND NEW.provider_payment_ref IS DISTINCT FROM OLD.provider_payment_ref THEN
    RAISE EXCEPTION 'provider_payment_ref is immutable once set' USING ERRCODE = 'restrict_violation';
  END IF;
  IF (NEW.provider_id, NEW.provider_account_id, NEW.capability_id) IS DISTINCT FROM
     (OLD.provider_id, OLD.provider_account_id, OLD.capability_id) THEN
    IF OLD.status <> 'CREATED' OR EXISTS (
         SELECT 1 FROM app.payment_attempts a
          WHERE a.payment_intent_id = OLD.id AND a.operation = 'CREATE'
            AND a.classification <> 'DEFINITELY_NOT_SENT') THEN
      RAISE EXCEPTION 'payment is bound to its provider once a request may have reached it'
        USING ERRCODE = 'restrict_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payment_intents_immutable BEFORE UPDATE ON app.payment_intents
  FOR EACH ROW EXECUTE FUNCTION app.payment_intents_immutable();
CREATE TRIGGER trg_payment_intents_guard_status BEFORE INSERT OR UPDATE OF status ON app.payment_intents
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payment_intent');
CREATE TRIGGER trg_payment_intents_updated_at BEFORE UPDATE ON app.payment_intents
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payment_intents_keep_created_at BEFORE UPDATE ON app.payment_intents
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();

-- =====================================================================================================
-- donations — donor-facing record, 1:1 with a payment intent. Guest contact is C2.
-- No money columns: amount and currency live only on the intent (single source).
-- =====================================================================================================
CREATE TABLE app.donations (
  id                     uuid        NOT NULL,
  payment_intent_id      uuid        NOT NULL,
  campaign_id            uuid        NOT NULL,
  donor_user_id          uuid,                       -- NULL for guest donors
  guest_name             text,                       -- C2
  guest_email            text,                       -- C2
  guest_phone_e164       text,                       -- C2
  is_anonymous           boolean     NOT NULL DEFAULT false,  -- hide donor identity publicly
  display_name           text,                       -- public name when not anonymous
  message                text,
  message_status         text        NOT NULL DEFAULT 'NONE',
  receipt_number         text        NOT NULL,       -- random, unique, not enumerable (baseline §7)
  access_token_hash      bytea,                      -- I-2: SHA-256 of the guest donation access token (shown once);
                                                     -- I-8: an idempotent replay re-issues it (old hash replaced = revoked)
  terms_version          text        NOT NULL,
  privacy_notice_version text        NOT NULL,
  marketing_consent      boolean     NOT NULL DEFAULT false,
  version                integer     NOT NULL DEFAULT 1,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_donations PRIMARY KEY (id),
  CONSTRAINT uq_donations_payment_intent_id UNIQUE (payment_intent_id),
  CONSTRAINT uq_donations_receipt_number UNIQUE (receipt_number),
  CONSTRAINT uq_donations_access_token_hash UNIQUE (access_token_hash),
  CONSTRAINT fk_donations_intent_campaign FOREIGN KEY (payment_intent_id, campaign_id) REFERENCES app.payment_intents (id, campaign_id) ON DELETE RESTRICT,
  CONSTRAINT fk_donations_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_donations_donor_user_id FOREIGN KEY (donor_user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_donations_donor_identified CHECK (donor_user_id IS NOT NULL OR guest_email IS NOT NULL OR guest_phone_e164 IS NOT NULL),
  CONSTRAINT ck_donations_guest_only_without_user CHECK (donor_user_id IS NULL OR (guest_name IS NULL AND guest_email IS NULL AND guest_phone_e164 IS NULL)),
  CONSTRAINT ck_donations_guest_email CHECK (guest_email IS NULL OR (guest_email ~ '^[^@\s]+@[^@\s]+$' AND char_length(guest_email) <= 254)),
  CONSTRAINT ck_donations_guest_phone CHECK (guest_phone_e164 IS NULL OR guest_phone_e164 ~ '^\+[1-9][0-9]{6,14}$'),
  CONSTRAINT ck_donations_display_name CHECK (display_name IS NULL OR char_length(display_name) BETWEEN 1 AND 80),
  CONSTRAINT ck_donations_message CHECK (message IS NULL OR char_length(message) <= 500),
  CONSTRAINT ck_donations_message_status CHECK (message_status IN ('NONE','PENDING_MODERATION','VISIBLE','HIDDEN')),
  CONSTRAINT ck_donations_message_consistent CHECK ((message IS NULL) = (message_status = 'NONE')),
  -- Guest donors (no account) get read-only access to this one donation through the access token (I-2).
  CONSTRAINT ck_donations_guest_token CHECK (donor_user_id IS NOT NULL OR access_token_hash IS NOT NULL),
  CONSTRAINT ck_donations_token_sha256 CHECK (access_token_hash IS NULL OR octet_length(access_token_hash) = 32),
  CONSTRAINT ck_donations_receipt_number CHECK (receipt_number ~ '^[0-9A-HJKMNP-TV-Z]{10,16}$')
);
CREATE INDEX ix_donations_campaign_id ON app.donations (campaign_id, created_at);
CREATE INDEX ix_donations_donor_user_id ON app.donations (donor_user_id) WHERE donor_user_id IS NOT NULL;

CREATE FUNCTION app.donations_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.payment_intent_id, NEW.campaign_id, NEW.donor_user_id, NEW.receipt_number, NEW.terms_version, NEW.privacy_notice_version)
     IS DISTINCT FROM (OLD.payment_intent_id, OLD.campaign_id, OLD.donor_user_id, OLD.receipt_number, OLD.terms_version, OLD.privacy_notice_version) THEN
    RAISE EXCEPTION 'donation identity, receipt and accepted terms are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_donations_immutable BEFORE UPDATE ON app.donations FOR EACH ROW EXECUTE FUNCTION app.donations_immutable();
CREATE TRIGGER trg_donations_updated_at BEFORE UPDATE ON app.donations FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_donations_keep_created_at BEFORE UPDATE ON app.donations FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_donations_no_delete BEFORE DELETE ON app.donations FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payment_attempts — every outbound provider call, recorded IN_FLIGHT before the call (committed), then
-- completed exactly once. our_reference is always the payment_id (the reference never changes).
-- =====================================================================================================
CREATE TABLE app.payment_attempts (
  id                          uuid        NOT NULL,
  payment_intent_id           uuid        NOT NULL,
  attempt_no                  integer     NOT NULL,
  operation                   text        NOT NULL,
  provider_id                 uuid        NOT NULL,
  our_reference               uuid        NOT NULL,
  correlation_id              text        NOT NULL,
  started_at                  timestamptz NOT NULL,
  -- result columns (set once)
  completed_at                timestamptz,
  classification              text        NOT NULL DEFAULT 'IN_FLIGHT',
  error_class                 text,
  unknown_reason              text,
  http_status                 smallint,
  provider_status_raw         text,
  provider_reference_returned text,
  latency_ms                  integer,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_attempts PRIMARY KEY (id),
  CONSTRAINT fk_payment_attempts_intent_provider FOREIGN KEY (payment_intent_id, provider_id) REFERENCES app.payment_intents (id, provider_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  CONSTRAINT uq_payment_attempts_no UNIQUE (payment_intent_id, attempt_no),
  CONSTRAINT ck_payment_attempts_no CHECK (attempt_no >= 1),
  CONSTRAINT ck_payment_attempts_operation CHECK (operation IN ('CREATE','GET_STATUS','CANCEL','CAPTURE')),
  CONSTRAINT ck_payment_attempts_reference CHECK (our_reference = payment_intent_id),
  CONSTRAINT ck_payment_attempts_classification CHECK (classification IN ('IN_FLIGHT','ACCEPTED','REJECTED','DEFINITELY_NOT_SENT','OUTCOME_UNKNOWN','NOT_FOUND','AUTH_CONFIG_ERROR','NOT_SUPPORTED','RATE_LIMITED')),
  CONSTRAINT ck_payment_attempts_error_class CHECK (error_class IS NULL OR error_class IN ('DEFINITE_FAILURE','RETRYABLE_BEFORE_SEND','INDETERMINATE','AUTH_CONFIG_ERROR','RATE_LIMITED','NOT_SUPPORTED','INVALID_REQUEST')),
  CONSTRAINT ck_payment_attempts_unknown_reason CHECK (
    (classification = 'OUTCOME_UNKNOWN') = (unknown_reason IS NOT NULL)
    AND (unknown_reason IS NULL OR unknown_reason IN ('TIMEOUT','CONN_RESET_AFTER_SEND','HTTP_5XX','MALFORMED_RESPONSE','SIGNATURE_INVALID_RESPONSE','CRASH_DURING_CREATE'))),
  CONSTRAINT ck_payment_attempts_completed CHECK ((classification = 'IN_FLIGHT') = (completed_at IS NULL)),
  CONSTRAINT ck_payment_attempts_latency CHECK (latency_ms IS NULL OR latency_ms >= 0)
);
CREATE INDEX ix_payment_attempts_in_flight ON app.payment_attempts (started_at) WHERE classification = 'IN_FLIGHT';
CREATE TRIGGER trg_payment_attempts_complete_once BEFORE UPDATE ON app.payment_attempts
  FOR EACH ROW EXECUTE FUNCTION app.attempt_complete_once('completed_at','classification','error_class','unknown_reason','http_status','provider_status_raw','provider_reference_returned','latency_ms');
CREATE TRIGGER trg_payment_attempts_no_delete BEFORE DELETE ON app.payment_attempts
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payment_attempts_no_truncate BEFORE TRUNCATE ON app.payment_attempts
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payment_transactions — the provider-side transaction as last authoritatively observed: raw provider
-- status kept VERBATIM plus the canonical mapping. Not guarded by the transition machine: it mirrors the
-- provider (which may report anomalies such as SUCCEEDED -> FAILED); payment_intents is the guarded
-- aggregate. Every observation is also appended to payment_events (raw_status), so no raw state is lost.
-- =====================================================================================================
CREATE TABLE app.payment_transactions (
  id                           uuid        NOT NULL,
  payment_intent_id            uuid        NOT NULL,
  provider_id                  uuid        NOT NULL,
  provider_transaction_ref     text        NOT NULL,
  raw_status                   text        NOT NULL,
  raw_status_at                timestamptz,           -- provider time, if given
  mapped_status                text        NOT NULL,
  mapping_version              text        NOT NULL,  -- adapter mapping table version used
  last_source                  text        NOT NULL,
  reported_amount_minor        bigint,
  reported_currency            char(3),
  provider_fee_minor           bigint,                -- PSP fee if reported (T2 timing, PCR-016)
  payer_instrument_fingerprint bytea,                 -- HMAC of provider payer token/MSISDN (duplicate rule, same-instrument refund)
  payer_instrument_masked      text,                  -- e.g. '****1234'
  version                      integer     NOT NULL DEFAULT 1,
  created_at                   timestamptz NOT NULL DEFAULT now(),
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_transactions PRIMARY KEY (id),
  CONSTRAINT fk_payment_transactions_intent_provider FOREIGN KEY (payment_intent_id, provider_id) REFERENCES app.payment_intents (id, provider_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  CONSTRAINT fk_payment_transactions_reported_currency FOREIGN KEY (reported_currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT uq_payment_transactions_provider_ref UNIQUE (provider_id, provider_transaction_ref),
  CONSTRAINT ck_payment_transactions_mapped_status CHECK (mapped_status IN ('CREATED','PENDING','REQUIRES_ACTION','AUTHORISED','UNKNOWN','SUCCEEDED','FAILED','EXPIRED','CANCELLED','PARTIALLY_REFUNDED','REFUNDED','DISPUTED','CHARGED_BACK')),
  CONSTRAINT ck_payment_transactions_source CHECK (last_source IN ('SYNC','WEBHOOK','POLL','RECON')),
  CONSTRAINT ck_payment_transactions_amount CHECK ((reported_amount_minor IS NULL) = (reported_currency IS NULL) AND (reported_amount_minor IS NULL OR reported_amount_minor > 0)),
  CONSTRAINT ck_payment_transactions_fee CHECK (provider_fee_minor IS NULL OR (provider_fee_minor >= 0 AND reported_currency IS NOT NULL)),
  CONSTRAINT ck_payment_transactions_raw_status CHECK (char_length(raw_status) BETWEEN 1 AND 200)
);
CREATE INDEX ix_payment_transactions_payment_intent_id ON app.payment_transactions (payment_intent_id);
CREATE INDEX ix_payment_transactions_payer ON app.payment_transactions (payer_instrument_fingerprint) WHERE payer_instrument_fingerprint IS NOT NULL;
CREATE TRIGGER trg_payment_transactions_updated_at BEFORE UPDATE ON app.payment_transactions FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payment_transactions_keep_created_at BEFORE UPDATE ON app.payment_transactions FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_payment_transactions_no_delete BEFORE DELETE ON app.payment_transactions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- refund_requests — business ask, maker-checker (approver <> requester), machine 'refund_request'.
-- =====================================================================================================
CREATE TABLE app.refund_requests (
  id                  uuid        NOT NULL,
  payment_intent_id   uuid        NOT NULL,
  campaign_id         uuid        NOT NULL,
  currency            char(3)     NOT NULL,
  amount_minor        bigint      NOT NULL,
  reason_code         text        NOT NULL,
  reason_text         text,                  -- staff free text
  donor_reason_text   text,                  -- C2
  channel             text        NOT NULL,
  requested_by        uuid        NOT NULL,  -- user, staff or system actor id
  requested_at        timestamptz NOT NULL DEFAULT now(),
  status              text        NOT NULL,
  decided_by          uuid,                  -- FINANCE approver/rejecter (≠ requested_by)
  decided_at          timestamptz,
  decision_reason     text,
  decided_step_up_at  timestamptz,
  second_approved_by  uuid,                  -- DUAL for funding plan C2
  second_approved_at  timestamptz,
  funding_plan        text,
  execution_method    text        NOT NULL DEFAULT 'PROVIDER_REFUND',
  compliance_case_id  uuid,                  -- by id only: payments does not depend on compliance
  idempotency_key     uuid        NOT NULL,
  expires_at          timestamptz NOT NULL,  -- maker-checker expiry (72 h policy, operational-controls §3)
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_refund_requests PRIMARY KEY (id),
  -- Refund currency = payment currency, and the campaign is the payment's campaign (composite FK).
  CONSTRAINT fk_refund_requests_intent FOREIGN KEY (payment_intent_id, campaign_id, currency) REFERENCES app.payment_intents (id, campaign_id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_refund_requests_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_refund_requests_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_refund_requests_second_approved_by FOREIGN KEY (second_approved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_refund_requests_idempotency UNIQUE (requested_by, idempotency_key),
  CONSTRAINT uq_refund_requests_exec_key UNIQUE (id, payment_intent_id, currency, amount_minor),
  CONSTRAINT ck_refund_requests_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_refund_requests_reason_code CHECK (reason_code IN ('DONOR_REQUEST','DUPLICATE_PAYMENT','PAYMENT_ERROR','CAMPAIGN_CANCELLED','CAMPAIGN_FRAUD','COMPLIANCE_ORDER','OTHER')),
  CONSTRAINT ck_refund_requests_channel CHECK (channel IN ('DONOR','SUPPORT','COMPLIANCE','SYSTEM')),
  CONSTRAINT ck_refund_requests_status CHECK (status IN ('REQUESTED','PENDING_APPROVAL','ON_HOLD','APPROVED','EXECUTING','COMPLETED','FAILED','REJECTED','WITHDRAWN')),
  CONSTRAINT ck_refund_requests_funding_plan CHECK (funding_plan IS NULL OR funding_plan IN ('CAMPAIGN','CAMPAIGN_PLUS_RESERVE','PLATFORM_FUNDED_WITH_RECOVERY','RECOVER_FIRST')),
  CONSTRAINT ck_refund_requests_execution_method CHECK (execution_method IN ('PROVIDER_REFUND','MANUAL_PAYOUT')),
  -- Maker-checker (refund-and-reversal-flows §3.2: DB check approved_by <> requested_by).
  CONSTRAINT ck_refund_requests_maker_checker CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT ck_refund_requests_second_approver CHECK (second_approved_by IS NULL OR (second_approved_by <> requested_by AND second_approved_by <> decided_by)),
  CONSTRAINT ck_refund_requests_decision CHECK (
    status NOT IN ('APPROVED','EXECUTING','COMPLETED','FAILED','REJECTED')
    OR (decided_by IS NOT NULL AND decided_at IS NOT NULL AND decision_reason IS NOT NULL)),
  CONSTRAINT ck_refund_requests_approval_fields CHECK (
    status NOT IN ('APPROVED','EXECUTING','COMPLETED','FAILED') OR (funding_plan IS NOT NULL AND decided_step_up_at IS NOT NULL)),
  -- Platform-funded refunds (option C2) need DUAL approval (refund-and-dispute-architecture §6).
  CONSTRAINT ck_refund_requests_c2_dual CHECK (
    funding_plan IS DISTINCT FROM 'PLATFORM_FUNDED_WITH_RECOVERY'
    OR status NOT IN ('APPROVED','EXECUTING','COMPLETED','FAILED')
    OR (second_approved_by IS NOT NULL AND second_approved_at IS NOT NULL)),
  CONSTRAINT ck_refund_requests_expiry CHECK (expires_at > requested_at)
);
CREATE INDEX ix_refund_requests_payment_intent_id ON app.refund_requests (payment_intent_id);
CREATE INDEX ix_refund_requests_queue ON app.refund_requests (status, expires_at) WHERE status IN ('REQUESTED','PENDING_APPROVAL','ON_HOLD');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('refund_request', '',                 'REQUESTED'),
  ('refund_request', 'REQUESTED',        'PENDING_APPROVAL'),
  ('refund_request', 'REQUESTED',        'WITHDRAWN'),
  ('refund_request', 'PENDING_APPROVAL', 'APPROVED'),
  ('refund_request', 'PENDING_APPROVAL', 'REJECTED'),
  ('refund_request', 'PENDING_APPROVAL', 'ON_HOLD'),
  ('refund_request', 'ON_HOLD',          'PENDING_APPROVAL'),
  ('refund_request', 'ON_HOLD',          'REJECTED'),
  ('refund_request', 'APPROVED',         'EXECUTING'),
  ('refund_request', 'EXECUTING',        'COMPLETED'),
  ('refund_request', 'EXECUTING',        'FAILED');

CREATE FUNCTION app.refund_requests_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.payment_intent_id, NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.requested_by, NEW.requested_at, NEW.idempotency_key)
     IS DISTINCT FROM (OLD.payment_intent_id, OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.requested_by, OLD.requested_at, OLD.idempotency_key) THEN
    RAISE EXCEPTION 'refund request amount, currency, payment and requester are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_refund_requests_immutable BEFORE UPDATE ON app.refund_requests FOR EACH ROW EXECUTE FUNCTION app.refund_requests_immutable();
CREATE TRIGGER trg_refund_requests_guard_status BEFORE INSERT OR UPDATE OF status ON app.refund_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('refund_request');
CREATE TRIGGER trg_refund_requests_updated_at BEFORE UPDATE ON app.refund_requests FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_refund_requests_keep_created_at BEFORE UPDATE ON app.refund_requests FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_refund_requests_no_delete BEFORE DELETE ON app.refund_requests FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- refund_transactions — provider-facing execution (Stage 1 `refunds`). id = refund_id = provider
-- reference and idempotency key on every attempt. Created when the request is APPROVED (same transaction
-- as the reservation journal refund:{id}:reserved). Machine 'refund_transaction' (baseline §8 / ADR-024).
-- =====================================================================================================
CREATE TABLE app.refund_transactions (
  id                         uuid        NOT NULL,
  refund_request_id          uuid        NOT NULL,
  payment_intent_id          uuid        NOT NULL,
  currency                   char(3)     NOT NULL,
  amount_minor               bigint      NOT NULL,
  execution_method           text        NOT NULL,
  provider_id                uuid        NOT NULL,   -- the original provider, or the disbursing provider for MANUAL_PAYOUT
  provider_refund_ref        text,
  status                     text        NOT NULL,
  raw_status                 text,                   -- latest provider status, verbatim
  failure_code               text,
  unknown_since              timestamptz,
  unknown_reason             text,
  next_poll_at               timestamptz,
  poll_attempts              integer     NOT NULL DEFAULT 0,
  poll_horizon_at            timestamptz,
  needs_reconciliation       boolean     NOT NULL DEFAULT false,
  ledger_reservation_txn_id  uuid        NOT NULL,   -- refund:{id}:reserved, posted in the approval transaction
  ledger_settlement_txn_id   uuid,                   -- refund:{id}:settled
  ledger_release_txn_id      uuid,                   -- refund:{id}:failed (exact inverse of the reservation)
  version                    integer     NOT NULL DEFAULT 1,
  created_at                 timestamptz NOT NULL DEFAULT now(),
  updated_at                 timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_refund_transactions PRIMARY KEY (id),
  CONSTRAINT uq_refund_transactions_refund_request_id UNIQUE (refund_request_id),
  -- Same payment, same currency, same amount as the approved request.
  CONSTRAINT fk_refund_transactions_request FOREIGN KEY (refund_request_id, payment_intent_id, currency, amount_minor)
    REFERENCES app.refund_requests (id, payment_intent_id, currency, amount_minor) ON DELETE RESTRICT,
  CONSTRAINT fk_refund_transactions_provider_id FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  -- Journal links carry the currency: a refund's journals are always in the refund's currency.
  CONSTRAINT fk_refund_transactions_reservation FOREIGN KEY (ledger_reservation_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_refund_transactions_settlement FOREIGN KEY (ledger_settlement_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_refund_transactions_release FOREIGN KEY (ledger_release_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT uq_refund_transactions_provider_ref UNIQUE (provider_id, provider_refund_ref),
  CONSTRAINT ck_refund_transactions_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_refund_transactions_execution_method CHECK (execution_method IN ('PROVIDER_REFUND','MANUAL_PAYOUT')),
  CONSTRAINT ck_refund_transactions_status CHECK (status IN ('CREATED','PENDING','UNKNOWN','SUCCEEDED','FAILED')),
  CONSTRAINT ck_refund_transactions_unknown_reason CHECK (unknown_reason IS NULL OR unknown_reason IN ('TIMEOUT','CONN_RESET_AFTER_SEND','HTTP_5XX','MALFORMED_RESPONSE','SIGNATURE_INVALID_RESPONSE','CRASH_DURING_CREATE')),
  CONSTRAINT ck_refund_transactions_unknown CHECK (status <> 'UNKNOWN' OR (unknown_since IS NOT NULL AND unknown_reason IS NOT NULL)),
  CONSTRAINT ck_refund_transactions_poll CHECK (poll_attempts >= 0),
  CONSTRAINT ck_refund_transactions_settled CHECK (ledger_settlement_txn_id IS NULL OR status = 'SUCCEEDED'),
  CONSTRAINT ck_refund_transactions_released CHECK (ledger_release_txn_id IS NULL OR status = 'FAILED'),
  CONSTRAINT ck_refund_transactions_failed CHECK (status <> 'FAILED' OR failure_code IS NOT NULL)
);
CREATE INDEX ix_refund_transactions_payment_intent_id ON app.refund_transactions (payment_intent_id);
CREATE INDEX ix_refund_transactions_poll ON app.refund_transactions (next_poll_at) WHERE status IN ('PENDING','UNKNOWN') AND next_poll_at IS NOT NULL;

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('refund_transaction', '',        'CREATED'),     -- approved + reserved; not yet sent
  ('refund_transaction', 'CREATED', 'PENDING'),     -- provider acknowledged
  ('refund_transaction', 'CREATED', 'UNKNOWN'),     -- submit outcome indeterminate (never FAILED)
  ('refund_transaction', 'CREATED', 'FAILED'),      -- definitive "not created" rejection
  ('refund_transaction', 'CREATED', 'SUCCEEDED'),   -- authenticated synchronous final success
  ('refund_transaction', 'PENDING', 'SUCCEEDED'),
  ('refund_transaction', 'PENDING', 'FAILED'),
  ('refund_transaction', 'PENDING', 'UNKNOWN'),     -- internal follow-up indeterminate
  ('refund_transaction', 'UNKNOWN', 'PENDING'),
  ('refund_transaction', 'UNKNOWN', 'SUCCEEDED'),
  ('refund_transaction', 'UNKNOWN', 'FAILED');      -- only on authoritative final failure / provider evidence
-- FAILED and SUCCEEDED are terminal. A late success after FAILED is a SEV1 incident + correcting journal,
-- not a transition (same rule as payouts, payout-lifecycle §5.3).

CREATE FUNCTION app.refund_transactions_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.refund_request_id, NEW.payment_intent_id, NEW.currency, NEW.amount_minor, NEW.provider_id, NEW.execution_method)
     IS DISTINCT FROM (OLD.refund_request_id, OLD.payment_intent_id, OLD.currency, OLD.amount_minor, OLD.provider_id, OLD.execution_method) THEN
    RAISE EXCEPTION 'refund transaction amount, currency, provider and payment are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.provider_refund_ref IS NOT NULL AND NEW.provider_refund_ref IS DISTINCT FROM OLD.provider_refund_ref THEN
    RAISE EXCEPTION 'provider_refund_ref is immutable once set' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_refund_transactions_immutable BEFORE UPDATE ON app.refund_transactions FOR EACH ROW EXECUTE FUNCTION app.refund_transactions_immutable();
CREATE TRIGGER trg_refund_transactions_guard_status BEFORE INSERT OR UPDATE OF status ON app.refund_transactions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('refund_transaction');
CREATE TRIGGER trg_refund_transactions_updated_at BEFORE UPDATE ON app.refund_transactions FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_refund_transactions_keep_created_at BEFORE UPDATE ON app.refund_transactions FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_refund_transactions_no_delete BEFORE DELETE ON app.refund_transactions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payment_disputes — dispute cases (Stage 1 `dispute_cases`), machine 'payment_dispute'.
-- =====================================================================================================
CREATE TABLE app.payment_disputes (
  id                       uuid        NOT NULL,
  payment_intent_id        uuid        NOT NULL,
  campaign_id              uuid        NOT NULL,
  currency                 char(3)     NOT NULL,
  amount_minor             bigint      NOT NULL,
  provider_id              uuid        NOT NULL,
  provider_dispute_ref     text        NOT NULL,
  kind                     text        NOT NULL,
  reason_code_provider     text,                   -- verbatim
  reason_category          text        NOT NULL,
  debit_timing             text        NOT NULL,   -- from provider capability / notice
  evidence_due_at          timestamptz,            -- exactly as received from the provider (PCR-012)
  status                   text        NOT NULL,
  assigned_to              uuid,
  escalated_from_id        uuid,                   -- INQUIRY escalated to CHARGEBACK
  hold_id                  uuid,                   -- DISPUTE_HOLD placed through risk
  evidence_pack_record_id  uuid,                   -- manifest evidence record
  accept_requested_by      uuid,                   -- maker for ACCEPTED (no contest)
  accept_approved_by       uuid,                   -- checker for ACCEPTED
  outcome_at               timestamptz,
  version                  integer     NOT NULL DEFAULT 1,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_disputes PRIMARY KEY (id),
  CONSTRAINT fk_payment_disputes_intent FOREIGN KEY (payment_intent_id, campaign_id, currency) REFERENCES app.payment_intents (id, campaign_id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_disputes_intent_provider FOREIGN KEY (payment_intent_id, provider_id) REFERENCES app.payment_intents (id, provider_id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_disputes_assigned_to FOREIGN KEY (assigned_to) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_disputes_escalated_from FOREIGN KEY (escalated_from_id) REFERENCES app.payment_disputes (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_disputes_hold_id FOREIGN KEY (hold_id) REFERENCES risk.holds (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_disputes_evidence_pack FOREIGN KEY (evidence_pack_record_id) REFERENCES audit.evidence_records (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_disputes_accept_requested_by FOREIGN KEY (accept_requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_disputes_accept_approved_by FOREIGN KEY (accept_approved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payment_disputes_provider_ref UNIQUE (provider_id, provider_dispute_ref),
  CONSTRAINT ck_payment_disputes_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payment_disputes_kind CHECK (kind IN ('INQUIRY','CHARGEBACK','PROVIDER_REVERSAL')),
  CONSTRAINT ck_payment_disputes_reason_category CHECK (reason_category IN ('FRAUD_CLAIMED','NOT_RECOGNISED','DUPLICATE','NOT_AS_DESCRIBED','PROCESSING_ERROR','OTHER')),
  CONSTRAINT ck_payment_disputes_debit_timing CHECK (debit_timing IN ('ON_OPEN','ON_LOSS','NONE')),
  CONSTRAINT ck_payment_disputes_inquiry_no_debit CHECK (kind <> 'INQUIRY' OR debit_timing = 'NONE'),
  CONSTRAINT ck_payment_disputes_status CHECK (status IN ('OPENED','EVIDENCE_REQUIRED','EVIDENCE_SUBMITTED','UNDER_PROVIDER_REVIEW','WON','LOST','ACCEPTED','EXPIRED')),
  CONSTRAINT ck_payment_disputes_accept_maker_checker CHECK (
    accept_approved_by IS NULL OR (accept_requested_by IS NOT NULL AND accept_approved_by <> accept_requested_by)),
  CONSTRAINT ck_payment_disputes_accepted CHECK (status <> 'ACCEPTED' OR accept_approved_by IS NOT NULL),
  CONSTRAINT ck_payment_disputes_outcome CHECK (status NOT IN ('WON','LOST','ACCEPTED','EXPIRED') OR outcome_at IS NOT NULL),
  CONSTRAINT ck_payment_disputes_not_self_escalation CHECK (escalated_from_id IS NULL OR escalated_from_id <> id)
);
CREATE INDEX ix_payment_disputes_payment_intent_id ON app.payment_disputes (payment_intent_id);
CREATE INDEX ix_payment_disputes_open_due ON app.payment_disputes (evidence_due_at) WHERE status IN ('OPENED','EVIDENCE_REQUIRED');
CREATE INDEX ix_payment_disputes_campaign_open ON app.payment_disputes (campaign_id, currency) WHERE status NOT IN ('WON','LOST','ACCEPTED','EXPIRED');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payment_dispute', '',                      'OPENED'),
  ('payment_dispute', 'OPENED',                'EVIDENCE_REQUIRED'),
  ('payment_dispute', 'OPENED',                'ACCEPTED'),
  ('payment_dispute', 'OPENED',                'LOST'),
  ('payment_dispute', 'EVIDENCE_REQUIRED',     'EVIDENCE_SUBMITTED'),
  ('payment_dispute', 'EVIDENCE_REQUIRED',     'EXPIRED'),
  ('payment_dispute', 'EVIDENCE_SUBMITTED',    'UNDER_PROVIDER_REVIEW'),
  ('payment_dispute', 'UNDER_PROVIDER_REVIEW', 'WON'),
  ('payment_dispute', 'UNDER_PROVIDER_REVIEW', 'LOST');

CREATE FUNCTION app.payment_disputes_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.payment_intent_id, NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.provider_id, NEW.provider_dispute_ref, NEW.kind)
     IS DISTINCT FROM (OLD.payment_intent_id, OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.provider_id, OLD.provider_dispute_ref, OLD.kind) THEN
    RAISE EXCEPTION 'dispute identity, amount and currency are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.evidence_due_at IS NOT NULL AND NEW.evidence_due_at IS DISTINCT FROM OLD.evidence_due_at THEN
    RAISE EXCEPTION 'evidence_due_at is stored exactly as received and never edited' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payment_disputes_immutable BEFORE UPDATE ON app.payment_disputes FOR EACH ROW EXECUTE FUNCTION app.payment_disputes_immutable();
CREATE TRIGGER trg_payment_disputes_guard_status BEFORE INSERT OR UPDATE OF status ON app.payment_disputes
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payment_dispute');
CREATE TRIGGER trg_payment_disputes_updated_at BEFORE UPDATE ON app.payment_disputes FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payment_disputes_keep_created_at BEFORE UPDATE ON app.payment_disputes FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_payment_disputes_no_delete BEFORE DELETE ON app.payment_disputes FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- chargebacks — one row per completed involuntary loss (lost/accepted/expired dispute, or provider
-- reversal without a dispute phase). Append-only fact; never a refund.
-- =====================================================================================================
CREATE TABLE app.chargebacks (
  id                     uuid        NOT NULL,
  payment_intent_id      uuid        NOT NULL,
  campaign_id            uuid        NOT NULL,
  currency               char(3)     NOT NULL,
  amount_minor           bigint      NOT NULL,
  reversal_kind          text        NOT NULL,
  payment_dispute_id     uuid,
  provider_id            uuid        NOT NULL,
  provider_reversal_ref  text        NOT NULL,
  inbox_event_id         uuid,
  ledger_transaction_id  uuid,          -- dispute:{id}:lost / reversal:{payment_id}:{event} (may be NULL when debited at open)
  occurred_at            timestamptz NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_chargebacks PRIMARY KEY (id),
  CONSTRAINT fk_chargebacks_intent FOREIGN KEY (payment_intent_id, campaign_id, currency) REFERENCES app.payment_intents (id, campaign_id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_chargebacks_payment_dispute_id FOREIGN KEY (payment_dispute_id) REFERENCES app.payment_disputes (id) ON DELETE RESTRICT,
  CONSTRAINT fk_chargebacks_provider_id FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_chargebacks_inbox_event_id FOREIGN KEY (inbox_event_id) REFERENCES app.provider_webhook_inbox (id) ON DELETE RESTRICT,
  CONSTRAINT fk_chargebacks_ledger_transaction_id FOREIGN KEY (ledger_transaction_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT uq_chargebacks_provider_ref UNIQUE (provider_id, provider_reversal_ref),
  CONSTRAINT uq_chargebacks_payment_dispute_id UNIQUE (payment_dispute_id),
  CONSTRAINT ck_chargebacks_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_chargebacks_reversal_kind CHECK (reversal_kind IN ('CARD_CHARGEBACK','PROVIDER_REVERSAL')),
  CONSTRAINT ck_chargebacks_dispute_link CHECK ((reversal_kind = 'CARD_CHARGEBACK') = (payment_dispute_id IS NOT NULL))
);
CREATE INDEX ix_chargebacks_payment_intent_id ON app.chargebacks (payment_intent_id);
CREATE TRIGGER trg_chargebacks_no_mutation BEFORE UPDATE OR DELETE ON app.chargebacks FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_chargebacks_no_truncate BEFORE TRUNCATE ON app.chargebacks FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payment_provider_references — every provider reference ever seen, unique per (provider, kind, ref).
-- =====================================================================================================
CREATE TABLE app.payment_provider_references (
  id                     uuid        NOT NULL,
  provider_id            uuid        NOT NULL,
  ref_kind               text        NOT NULL,
  reference              text        NOT NULL,
  payment_intent_id      uuid        NOT NULL,
  refund_transaction_id  uuid,
  payment_dispute_id     uuid,
  source                 text        NOT NULL,
  recorded_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_provider_references PRIMARY KEY (id),
  CONSTRAINT fk_payment_provider_references_provider_id FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_provider_references_intent FOREIGN KEY (payment_intent_id) REFERENCES app.payment_intents (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_provider_references_refund FOREIGN KEY (refund_transaction_id) REFERENCES app.refund_transactions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_provider_references_dispute FOREIGN KEY (payment_dispute_id) REFERENCES app.payment_disputes (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payment_provider_references UNIQUE (provider_id, ref_kind, reference),
  CONSTRAINT ck_payment_provider_references_kind CHECK (ref_kind IN ('PAYMENT','TRANSACTION','CHECKOUT_SESSION','POLL_URL','AUTHORISATION','CAPTURE','REFUND','DISPUTE','REVERSAL')),
  CONSTRAINT ck_payment_provider_references_reference CHECK (char_length(reference) BETWEEN 1 AND 255),
  CONSTRAINT ck_payment_provider_references_refund CHECK (ref_kind <> 'REFUND' OR refund_transaction_id IS NOT NULL),
  CONSTRAINT ck_payment_provider_references_dispute CHECK (ref_kind <> 'DISPUTE' OR payment_dispute_id IS NOT NULL),
  CONSTRAINT ck_payment_provider_references_source CHECK (source IN ('SYNC','WEBHOOK','POLL','RECON'))
);
CREATE INDEX ix_payment_provider_references_intent ON app.payment_provider_references (payment_intent_id);
CREATE TRIGGER trg_payment_provider_references_no_mutation BEFORE UPDATE OR DELETE ON app.payment_provider_references FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payment_provider_references_no_truncate BEFORE TRUNCATE ON app.payment_provider_references FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payment_events — append-only canonical status history for every payments-module machine (payment
-- intents, refund requests, refund transactions, disputes). Also records NOT-applied events (lower rank,
-- non-edge, anomaly, duplicate) so nothing a provider said is lost. raw_status keeps provider vocabulary.
-- =====================================================================================================
CREATE TABLE app.payment_events (
  id                      uuid        NOT NULL,
  subject_type            text        NOT NULL,
  payment_intent_id       uuid        NOT NULL,   -- every subject belongs to one payment
  refund_request_id       uuid,
  refund_transaction_id   uuid,
  payment_dispute_id      uuid,
  from_status             text,                   -- NULL = initial (machine's '' state)
  to_status               text        NOT NULL,
  applied                 boolean     NOT NULL,
  not_applied_reason      text,
  transition_code         text,                   -- e.g. 'P9' for payment intents
  source                  text        NOT NULL,
  provider_event_id       text,
  inbox_event_id          uuid,
  payment_attempt_id      uuid,
  raw_status              text,                   -- provider status verbatim (if provider-originated)
  actor_type              text        NOT NULL,
  actor_id                uuid,
  reason                  text,
  evidence_record_id      uuid,
  ledger_transaction_ids  uuid[]      NOT NULL DEFAULT '{}',
  correlation_id          text,
  occurred_at             timestamptz,            -- provider time, if given
  recorded_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_events PRIMARY KEY (id),
  CONSTRAINT fk_payment_events_intent FOREIGN KEY (payment_intent_id) REFERENCES app.payment_intents (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payment_events_refund_request FOREIGN KEY (refund_request_id) REFERENCES app.refund_requests (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payment_events_refund_transaction FOREIGN KEY (refund_transaction_id) REFERENCES app.refund_transactions (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payment_events_dispute FOREIGN KEY (payment_dispute_id) REFERENCES app.payment_disputes (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payment_events_inbox FOREIGN KEY (inbox_event_id) REFERENCES app.provider_webhook_inbox (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_events_attempt FOREIGN KEY (payment_attempt_id) REFERENCES app.payment_attempts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_events_actor FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_events_evidence FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payment_events_subject_type CHECK (subject_type IN ('PAYMENT_INTENT','REFUND_REQUEST','REFUND_TRANSACTION','PAYMENT_DISPUTE')),
  CONSTRAINT ck_payment_events_subject CHECK (
       (subject_type = 'PAYMENT_INTENT'     AND refund_request_id IS NULL AND refund_transaction_id IS NULL AND payment_dispute_id IS NULL)
    OR (subject_type = 'REFUND_REQUEST'     AND refund_request_id IS NOT NULL AND refund_transaction_id IS NULL AND payment_dispute_id IS NULL)
    OR (subject_type = 'REFUND_TRANSACTION' AND refund_transaction_id IS NOT NULL AND payment_dispute_id IS NULL)
    OR (subject_type = 'PAYMENT_DISPUTE'    AND payment_dispute_id IS NOT NULL AND refund_request_id IS NULL AND refund_transaction_id IS NULL)),
  CONSTRAINT ck_payment_events_source CHECK (source IN ('SYNC','WEBHOOK','POLL','RECON','INTERNAL','STAFF')),
  CONSTRAINT ck_payment_events_actor_type CHECK (actor_type IN ('SYSTEM','DONOR','STAFF','PROVIDER')),
  CONSTRAINT ck_payment_events_not_applied CHECK (
    (applied AND not_applied_reason IS NULL)
    OR (NOT applied AND not_applied_reason IS NOT NULL AND not_applied_reason IN ('LOWER_RANK','NOT_AN_EDGE','ANOMALY','DUPLICATE','PARKED'))),
  CONSTRAINT ck_payment_events_staff CHECK (source <> 'STAFF' OR (actor_type = 'STAFF' AND actor_id IS NOT NULL AND reason IS NOT NULL)),
  CONSTRAINT ck_payment_events_transition_code CHECK (transition_code IS NULL OR transition_code ~ '^P([1-9]|1[0-9]|20|18b)$'),
  -- Money is confirmed only by authoritative provider state: never INTERNAL or STAFF, never a browser.
  CONSTRAINT ck_payment_events_success_source CHECK (
    NOT applied OR to_status <> 'SUCCEEDED' OR subject_type NOT IN ('PAYMENT_INTENT','REFUND_TRANSACTION')
    OR source IN ('SYNC','WEBHOOK','POLL','RECON')),
  -- An external event can never push a payment (back) into UNKNOWN: only our own indeterminate call does.
  CONSTRAINT ck_payment_events_unknown_internal CHECK (
    NOT applied OR to_status <> 'UNKNOWN' OR source = 'INTERNAL'),
  -- Baseline §12 I-21: FAILED only from authoritative provider sources, or INTERNAL for a pre-submit
  -- rejection from CREATED (P4). A timeout handler can never write FAILED.
  CONSTRAINT ck_payment_events_failed_source CHECK (
    NOT applied OR to_status <> 'FAILED' OR subject_type NOT IN ('PAYMENT_INTENT','REFUND_TRANSACTION')
    OR source IN ('WEBHOOK','POLL','RECON','SYNC')
    OR (source = 'INTERNAL' AND from_status = 'CREATED')
    OR source = 'STAFF'),   -- staff only with provider evidence (next constraint; transaction-lifecycle §7.3 step 6)
  -- A staff-declared FAILED needs provider evidence (transaction-lifecycle §7.3 step 6).
  CONSTRAINT ck_payment_events_staff_failed_evidence CHECK (
    NOT applied OR to_status <> 'FAILED' OR source <> 'STAFF' OR evidence_record_id IS NOT NULL)
);
CREATE INDEX ix_payment_events_intent ON app.payment_events (payment_intent_id, recorded_at);
CREATE INDEX ix_payment_events_refund_request ON app.payment_events (refund_request_id) WHERE refund_request_id IS NOT NULL;
CREATE INDEX ix_payment_events_refund_transaction ON app.payment_events (refund_transaction_id) WHERE refund_transaction_id IS NOT NULL;
CREATE INDEX ix_payment_events_dispute ON app.payment_events (payment_dispute_id) WHERE payment_dispute_id IS NOT NULL;
CREATE TRIGGER trg_payment_events_no_mutation BEFORE UPDATE OR DELETE ON app.payment_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payment_events_no_truncate BEFORE TRUNCATE ON app.payment_events FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- Every status change of a payments-module aggregate must be accompanied, in the SAME transaction, by an
-- applied payment_events row with the same from/to (state derivable from history: DATABASE §9).
-- Deferred to COMMIT so the aggregate and its event can be written in either order.
-- TG_ARGV[0] = subject id column in payment_events, TG_ARGV[1] = subject_type.
-- -----------------------------------------------------------------------------------------------------
CREATE FUNCTION app.require_payment_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  col  text := TG_ARGV[0];
  subj text := TG_ARGV[1];
  f    text;
  ok   boolean;
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.status IS NOT DISTINCT FROM OLD.status THEN
    RETURN NULL;
  END IF;
  f := CASE WHEN TG_OP = 'INSERT' THEN '' ELSE OLD.status END;
  EXECUTE format(
    'SELECT EXISTS (SELECT 1 FROM app.payment_events e
                     WHERE e.%I = $1 AND e.subject_type = $2 AND COALESCE(e.from_status, '''') = $3
                       AND e.to_status = $4 AND e.applied AND e.recorded_at = transaction_timestamp())', col)
    INTO ok USING NEW.id, subj, f, NEW.status;
  IF NOT ok THEN
    RAISE EXCEPTION 'missing payment_events row for % % transition "%" -> "%"', subj, NEW.id, f, NEW.status
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_payment_intents_require_event AFTER INSERT OR UPDATE OF status ON app.payment_intents
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_payment_event('payment_intent_id', 'PAYMENT_INTENT');
CREATE CONSTRAINT TRIGGER trg_refund_requests_require_event AFTER INSERT OR UPDATE OF status ON app.refund_requests
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_payment_event('refund_request_id', 'REFUND_REQUEST');
CREATE CONSTRAINT TRIGGER trg_refund_transactions_require_event AFTER INSERT OR UPDATE OF status ON app.refund_transactions
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_payment_event('refund_transaction_id', 'REFUND_TRANSACTION');
CREATE CONSTRAINT TRIGGER trg_payment_disputes_require_event AFTER INSERT OR UPDATE OF status ON app.payment_disputes
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_payment_event('payment_dispute_id', 'PAYMENT_DISPUTE');

-- -----------------------------------------------------------------------------------------------------
-- Over-refund guard (refund-and-reversal-flows §3.2). Refundable remaining = captured − Σ(refunds
-- reserved or settled) − Σ(chargebacks), per payment, in the payment's single currency (composite FKs
-- force every refund and chargeback into the payment's currency, so the sums never mix currencies).
-- "Reserved or settled" = refund requests in APPROVED / EXECUTING / COMPLETED, i.e. refund_transactions
-- CREATED / PENDING / UNKNOWN (pending reservations) and SUCCEEDED. Locks the payment row FOR UPDATE so
-- concurrent approvals serialise (DATABASE §12).
-- -----------------------------------------------------------------------------------------------------
CREATE FUNCTION app.refund_requests_check_refundable() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  pi        app.payment_intents%ROWTYPE;
  reserved  bigint;
  charged   bigint;
BEGIN
  IF NEW.status <> 'APPROVED' OR (TG_OP = 'UPDATE' AND OLD.status = 'APPROVED') THEN
    RETURN NULL;
  END IF;
  SELECT * INTO pi FROM app.payment_intents WHERE id = NEW.payment_intent_id FOR UPDATE;
  IF pi.status NOT IN ('SUCCEEDED','PARTIALLY_REFUNDED') THEN
    RAISE EXCEPTION 'refund not allowed: payment % is %', pi.id, pi.status USING ERRCODE = 'check_violation';
  END IF;
  SELECT COALESCE(sum(r.amount_minor), 0) INTO reserved
    FROM app.refund_requests r
   WHERE r.payment_intent_id = pi.id AND r.currency = pi.currency
     AND r.status IN ('APPROVED','EXECUTING','COMPLETED');
  SELECT COALESCE(sum(c.amount_minor), 0) INTO charged
    FROM app.chargebacks c
   WHERE c.payment_intent_id = pi.id AND c.currency = pi.currency;
  IF reserved + charged > pi.amount_minor THEN
    RAISE EXCEPTION 'over-refund: refunds % + chargebacks % exceed captured % %', reserved, charged, pi.amount_minor, pi.currency
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE TRIGGER trg_refund_requests_check_refundable AFTER INSERT OR UPDATE OF status ON app.refund_requests
  FOR EACH ROW EXECUTE FUNCTION app.refund_requests_check_refundable();

-- Second line: settled refunds can never exceed the captured amount, and a refund transaction only
-- exists for an approved request.
CREATE FUNCTION app.refund_transactions_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  pi       app.payment_intents%ROWTYPE;
  req      text;
  settled  bigint;
BEGIN
  IF TG_OP = 'INSERT' THEN
    SELECT status INTO req FROM app.refund_requests WHERE id = NEW.refund_request_id;
    IF req NOT IN ('APPROVED','EXECUTING') THEN
      RAISE EXCEPTION 'refund transaction requires an APPROVED refund request (is %)', req USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NEW.status = 'SUCCEEDED' THEN
    SELECT * INTO pi FROM app.payment_intents WHERE id = NEW.payment_intent_id FOR UPDATE;
    SELECT COALESCE(sum(t.amount_minor), 0) INTO settled
      FROM app.refund_transactions t
     WHERE t.payment_intent_id = pi.id AND t.currency = pi.currency AND t.status = 'SUCCEEDED';
    IF settled > pi.amount_minor THEN
      RAISE EXCEPTION 'over-refund: settled refunds % exceed captured % %', settled, pi.amount_minor, pi.currency
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NULL;
END $$;
CREATE TRIGGER trg_refund_transactions_check AFTER INSERT OR UPDATE OF status ON app.refund_transactions
  FOR EACH ROW EXECUTE FUNCTION app.refund_transactions_check();
