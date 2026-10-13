-- Stage 8 (ADR-039): payments module — donation intents (parent), payments (payment_intents, the canonical
-- Stage 2 payment; "payment attempt" in the API), provider calls (payment_attempts), provider transactions,
-- provider references, payment sessions, append-only payment events, refund requests (execution impossible:
-- the APPROVED -> EXECUTING edge is not seeded) and reconciliation references.
-- Ported from design/sql/0015_payments.sql (Stage 2, validated in PGlite). Deferred (ADR-039 §4): fee columns,
-- donations (receipts/guests), refund_transactions, payment_disputes, chargebacks, every ledger reference.
-- The payment_intent machine keeps all 42 Stage 2 edges (Go and SQL identical; a unit test diffs them).
-- Owner module: payments. Schema: app. Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Rank of a payment status (transaction-lifecycle §5 / ADR-020 §5).
CREATE FUNCTION app.payment_status_rank(s text) RETURNS smallint LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT CASE s
    WHEN 'CREATED' THEN 0  WHEN 'UNKNOWN' THEN 5   WHEN 'PENDING' THEN 10 WHEN 'REQUIRES_ACTION' THEN 20
    WHEN 'AUTHORISED' THEN 30 WHEN 'FAILED' THEN 50 WHEN 'CANCELLED' THEN 50 WHEN 'EXPIRED' THEN 50
    WHEN 'SUCCEEDED' THEN 60 WHEN 'PARTIALLY_REFUNDED' THEN 70 WHEN 'DISPUTED' THEN 80
    WHEN 'REFUNDED' THEN 90 WHEN 'CHARGED_BACK' THEN 95 END::smallint
$$;

-- Attempt rows are written before the call and completed exactly once (TG_ARGV = mutable result columns).
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

-- =========================================================================================================
-- donation_intents — the donor's validated request to give (ADR-039 §2). Not money received; changes no total.
-- Authenticated donors only in Stage 8 (guest donations: LR-026, LR-035).
-- =========================================================================================================
CREATE TABLE app.donation_intents (
  id                uuid        NOT NULL,
  campaign_id       uuid        NOT NULL,
  donor_user_id     uuid        NOT NULL,
  currency          char(3)     NOT NULL,
  amount_minor      bigint      NOT NULL,
  is_anonymous      boolean     NOT NULL DEFAULT false,   -- public display choice only (LR-026), never anonymous processing
  is_simulated      boolean     NOT NULL,
  status            text        NOT NULL,
  expires_at        timestamptz NOT NULL,
  correlation_id    text        NOT NULL,
  idempotency_key   uuid        NOT NULL,
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_donation_intents PRIMARY KEY (id),
  CONSTRAINT fk_donation_intents_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_donation_intents_donor_user_id FOREIGN KEY (donor_user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_donation_intents_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT uq_donation_intents_idempotency UNIQUE (donor_user_id, idempotency_key),
  CONSTRAINT uq_donation_intents_payment_key UNIQUE (id, campaign_id, currency, amount_minor, is_simulated),
  CONSTRAINT ck_donation_intents_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_donation_intents_status CHECK (status IN ('OPEN','PAID','EXPIRED','CANCELLED')),
  CONSTRAINT ck_donation_intents_expiry CHECK (expires_at > created_at),
  CONSTRAINT ck_donation_intents_correlation CHECK (char_length(correlation_id) BETWEEN 1 AND 128),
  CONSTRAINT ck_donation_intents_version CHECK (version >= 1)
);
COMMENT ON TABLE app.donation_intents IS 'C2. Donor request to give (ADR-039 §2). Not a donation received; no totals derive from it.';
CREATE INDEX ix_donation_intents_donor ON app.donation_intents (donor_user_id, created_at DESC, id DESC);
CREATE INDEX ix_donation_intents_campaign ON app.donation_intents (campaign_id, created_at DESC);
CREATE INDEX ix_donation_intents_open_expiry ON app.donation_intents (expires_at) WHERE status = 'OPEN';
CREATE INDEX ix_donation_intents_created ON app.donation_intents (created_at DESC, id DESC);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('donation_intent', '',          'OPEN'),
  ('donation_intent', 'OPEN',      'PAID'),
  ('donation_intent', 'OPEN',      'EXPIRED'),
  ('donation_intent', 'OPEN',      'CANCELLED'),
  ('donation_intent', 'EXPIRED',   'PAID'),        -- late authoritative success wins
  ('donation_intent', 'CANCELLED', 'PAID');

CREATE FUNCTION app.donation_intents_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.campaign_id, NEW.donor_user_id, NEW.currency, NEW.amount_minor, NEW.is_simulated, NEW.idempotency_key, NEW.correlation_id, NEW.is_anonymous)
     IS DISTINCT FROM (OLD.campaign_id, OLD.donor_user_id, OLD.currency, OLD.amount_minor, OLD.is_simulated, OLD.idempotency_key, OLD.correlation_id, OLD.is_anonymous) THEN
    RAISE EXCEPTION 'donation intent campaign, donor, amount, currency and simulation flag are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_donation_intents_immutable BEFORE UPDATE ON app.donation_intents FOR EACH ROW EXECUTE FUNCTION app.donation_intents_immutable();
CREATE TRIGGER trg_donation_intents_guard_status BEFORE INSERT OR UPDATE OF status ON app.donation_intents
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('donation_intent');
CREATE TRIGGER trg_donation_intents_updated_at BEFORE UPDATE ON app.donation_intents FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_donation_intents_keep_created_at BEFORE UPDATE ON app.donation_intents FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_donation_intents_no_delete BEFORE DELETE ON app.donation_intents FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =========================================================================================================
-- payment_intents — the canonical payment (payment_id = id = provider merchant reference and idempotency key,
-- never reused). One donation intent may have several (donor retries after a definite failure).
-- =========================================================================================================
CREATE TABLE app.payment_intents (
  id                        uuid        NOT NULL,
  donation_intent_id        uuid        NOT NULL,
  campaign_id               uuid        NOT NULL,
  currency                  char(3)     NOT NULL,
  amount_minor              bigint      NOT NULL,
  method                    text        NOT NULL,
  is_simulated              boolean     NOT NULL,
  provider_id               uuid        NOT NULL,
  provider_account_id       uuid        NOT NULL,
  capability_id             uuid        NOT NULL,
  custody_model             text        NOT NULL,
  capability_routable       boolean     NOT NULL,
  routing_reason            text        NOT NULL,
  attempt_seq               integer     NOT NULL,          -- 1, 2, … within the donation intent
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
  expires_at                timestamptz,
  failure_code              text,
  idempotency_key           uuid        NOT NULL,
  correlation_id            text        NOT NULL,
  version                   integer     NOT NULL DEFAULT 1,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_intents PRIMARY KEY (id),
  CONSTRAINT fk_payment_intents_donation FOREIGN KEY (donation_intent_id, campaign_id, currency, amount_minor, is_simulated)
    REFERENCES app.donation_intents (id, campaign_id, currency, amount_minor, is_simulated) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_intents_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_intents_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  -- a simulated payment can only use a sandbox provider and vice versa
  CONSTRAINT fk_payment_intents_provider_sandbox FOREIGN KEY (provider_id, is_simulated) REFERENCES app.payment_providers (id, is_sandbox) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_intents_provider_account FOREIGN KEY (provider_account_id, provider_id) REFERENCES app.provider_accounts (id, provider_id) ON DELETE RESTRICT,
  -- Model A routing guard, declaratively (payment-schema §3.3)
  CONSTRAINT fk_payment_intents_capability FOREIGN KEY (capability_id, provider_id, method, currency, custody_model, capability_routable)
    REFERENCES app.provider_capabilities (id, provider_id, method, currency, custody_model, routable) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_intents_possible_duplicate_of FOREIGN KEY (possible_duplicate_of) REFERENCES app.payment_intents (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payment_intents_provider_payment_ref UNIQUE (provider_id, provider_payment_ref),
  CONSTRAINT uq_payment_intents_idempotency UNIQUE (donation_intent_id, idempotency_key),
  CONSTRAINT uq_payment_intents_seq UNIQUE (donation_intent_id, attempt_seq),
  CONSTRAINT uq_payment_intents_id_campaign_currency UNIQUE (id, campaign_id, currency),
  CONSTRAINT uq_payment_intents_id_provider UNIQUE (id, provider_id),
  CONSTRAINT ck_payment_intents_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payment_intents_seq CHECK (attempt_seq >= 1),
  CONSTRAINT ck_payment_intents_method CHECK (method IN ('ECOCASH','ONEMONEY','INNBUCKS','OMARI','ZIMSWITCH','BANK_TRANSFER','CARD')),
  CONSTRAINT ck_payment_intents_routable CHECK (capability_routable),
  CONSTRAINT ck_payment_intents_custody_model CHECK (custody_model IN ('PSP_POOL','SPLIT_DIRECT')),
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
  CONSTRAINT ck_payment_intents_failure_code CHECK (failure_code IS NULL OR failure_code ~ '^[A-Z][A-Z0-9_]{1,63}$'),
  CONSTRAINT ck_payment_intents_correlation CHECK (char_length(correlation_id) BETWEEN 1 AND 128),
  CONSTRAINT ck_payment_intents_version CHECK (version >= 1)
);
COMMENT ON TABLE app.payment_intents IS 'C1. Canonical payment (Stage 2 payment_intents; API "payment attempt"). Operational record, never a balance.';
CREATE INDEX ix_payment_intents_donation ON app.payment_intents (donation_intent_id, attempt_seq);
CREATE INDEX ix_payment_intents_campaign_status ON app.payment_intents (campaign_id, status);
CREATE INDEX ix_payment_intents_poll ON app.payment_intents (next_poll_at) WHERE status IN ('UNKNOWN','PENDING','REQUIRES_ACTION','AUTHORISED','CREATED') AND next_poll_at IS NOT NULL;
CREATE INDEX ix_payment_intents_needs_reconciliation ON app.payment_intents (created_at DESC, id DESC) WHERE needs_reconciliation;
CREATE INDEX ix_payment_intents_unknown ON app.payment_intents (unknown_since, id) WHERE status = 'UNKNOWN';
CREATE INDEX ix_payment_intents_status_created ON app.payment_intents (status, created_at DESC, id DESC);
CREATE INDEX ix_payment_intents_created ON app.payment_intents (created_at DESC, id DESC);
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
  ('payment_intent', 'PENDING',            'UNKNOWN'),             -- P13 (INTERNAL source only)
  ('payment_intent', 'UNKNOWN',            'PENDING'),             -- P14
  ('payment_intent', 'UNKNOWN',            'REQUIRES_ACTION'),     -- P14
  ('payment_intent', 'FAILED',             'SUCCEEDED'),           -- P15 late authoritative success
  ('payment_intent', 'EXPIRED',            'SUCCEEDED'),           -- P15
  ('payment_intent', 'CANCELLED',          'SUCCEEDED'),           -- P15
  ('payment_intent', 'SUCCEEDED',          'PARTIALLY_REFUNDED'),  -- P16
  ('payment_intent', 'SUCCEEDED',          'REFUNDED'),            -- P16
  ('payment_intent', 'PARTIALLY_REFUNDED', 'REFUNDED'),            -- P16
  ('payment_intent', 'SUCCEEDED',          'DISPUTED'),            -- P17
  ('payment_intent', 'PARTIALLY_REFUNDED', 'DISPUTED'),            -- P17
  ('payment_intent', 'DISPUTED',           'SUCCEEDED'),           -- P18
  ('payment_intent', 'DISPUTED',           'PARTIALLY_REFUNDED'),  -- P18b
  ('payment_intent', 'DISPUTED',           'CHARGED_BACK'),        -- P19
  ('payment_intent', 'SUCCEEDED',          'CHARGED_BACK'),        -- P20
  ('payment_intent', 'PARTIALLY_REFUNDED', 'CHARGED_BACK');        -- P20

CREATE FUNCTION app.payment_intents_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.donation_intent_id, NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.method, NEW.is_simulated,
      NEW.idempotency_key, NEW.attempt_seq, NEW.correlation_id)
     IS DISTINCT FROM (OLD.donation_intent_id, OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.method, OLD.is_simulated,
      OLD.idempotency_key, OLD.attempt_seq, OLD.correlation_id) THEN
    RAISE EXCEPTION 'payment amount, currency, campaign, method, simulation flag and idempotency are immutable'
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
CREATE TRIGGER trg_payment_intents_no_delete BEFORE DELETE ON app.payment_intents
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payment_intents_no_truncate BEFORE TRUNCATE ON app.payment_intents
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =========================================================================================================
-- payment_attempts — every outbound provider call (Stage 2 meaning), committed IN_FLIGHT before the call and
-- completed exactly once. our_reference is always the payment_id.
-- =========================================================================================================
CREATE TABLE app.payment_attempts (
  id                          uuid        NOT NULL,
  payment_intent_id           uuid        NOT NULL,
  attempt_no                  integer     NOT NULL,
  operation                   text        NOT NULL,
  provider_id                 uuid        NOT NULL,
  our_reference               uuid        NOT NULL,
  correlation_id              text        NOT NULL,
  started_at                  timestamptz NOT NULL,
  deadline_at                 timestamptz NOT NULL,
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
  CONSTRAINT ck_payment_attempts_deadline CHECK (deadline_at > started_at),
  CONSTRAINT ck_payment_attempts_classification CHECK (classification IN ('IN_FLIGHT','ACCEPTED','REJECTED','DEFINITELY_NOT_SENT','OUTCOME_UNKNOWN','NOT_FOUND','AUTH_CONFIG_ERROR','NOT_SUPPORTED','RATE_LIMITED')),
  CONSTRAINT ck_payment_attempts_error_class CHECK (error_class IS NULL OR error_class IN ('DEFINITE_FAILURE','RETRYABLE_BEFORE_SEND','INDETERMINATE','AUTH_CONFIG_ERROR','RATE_LIMITED','NOT_SUPPORTED','INVALID_REQUEST','NOT_FOUND')),
  CONSTRAINT ck_payment_attempts_unknown_reason CHECK (
    (classification = 'OUTCOME_UNKNOWN') = (unknown_reason IS NOT NULL)
    AND (unknown_reason IS NULL OR unknown_reason IN ('TIMEOUT','CONN_RESET_AFTER_SEND','HTTP_5XX','MALFORMED_RESPONSE','SIGNATURE_INVALID_RESPONSE','CRASH_DURING_CREATE'))),
  CONSTRAINT ck_payment_attempts_completed CHECK ((classification = 'IN_FLIGHT') = (completed_at IS NULL)),
  CONSTRAINT ck_payment_attempts_latency CHECK (latency_ms IS NULL OR latency_ms >= 0)
);
COMMENT ON TABLE app.payment_attempts IS 'C1. One row per outbound provider call (create, status, cancel), written before the call.';
CREATE INDEX ix_payment_attempts_in_flight ON app.payment_attempts (deadline_at) WHERE classification = 'IN_FLIGHT';
CREATE INDEX ix_payment_attempts_started ON app.payment_attempts (started_at DESC, id DESC);
CREATE TRIGGER trg_payment_attempts_complete_once BEFORE UPDATE ON app.payment_attempts
  FOR EACH ROW EXECUTE FUNCTION app.attempt_complete_once('completed_at','classification','error_class','unknown_reason','http_status','provider_status_raw','provider_reference_returned','latency_ms');
CREATE TRIGGER trg_payment_attempts_no_delete BEFORE DELETE ON app.payment_attempts
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payment_attempts_no_truncate BEFORE TRUNCATE ON app.payment_attempts
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =========================================================================================================
-- payment_transactions — the provider-side transaction as last authoritatively observed (raw status
-- verbatim + mapping). Not machine-guarded: it mirrors the provider, anomalies included.
-- =========================================================================================================
CREATE TABLE app.payment_transactions (
  id                           uuid        NOT NULL,
  payment_intent_id            uuid        NOT NULL,
  provider_id                  uuid        NOT NULL,
  provider_transaction_ref     text        NOT NULL,
  raw_status                   text        NOT NULL,
  raw_status_at                timestamptz,
  mapped_status                text        NOT NULL,
  mapping_version              text        NOT NULL,
  last_source                  text        NOT NULL,
  reported_amount_minor        bigint,
  reported_currency            char(3),
  provider_fee_minor           bigint,
  payer_instrument_fingerprint bytea,
  payer_instrument_masked      text,
  version                      integer     NOT NULL DEFAULT 1,
  created_at                   timestamptz NOT NULL DEFAULT now(),
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_transactions PRIMARY KEY (id),
  CONSTRAINT fk_payment_transactions_intent_provider FOREIGN KEY (payment_intent_id, provider_id) REFERENCES app.payment_intents (id, provider_id) ON DELETE RESTRICT ON UPDATE RESTRICT,
  CONSTRAINT fk_payment_transactions_reported_currency FOREIGN KEY (reported_currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT uq_payment_transactions_provider_ref UNIQUE (provider_id, provider_transaction_ref),
  CONSTRAINT uq_payment_transactions_intent UNIQUE (payment_intent_id),
  CONSTRAINT ck_payment_transactions_mapped_status CHECK (mapped_status IN ('CREATED','PENDING','REQUIRES_ACTION','AUTHORISED','UNKNOWN','SUCCEEDED','FAILED','EXPIRED','CANCELLED','PARTIALLY_REFUNDED','REFUNDED','DISPUTED','CHARGED_BACK')),
  CONSTRAINT ck_payment_transactions_source CHECK (last_source IN ('SYNC','WEBHOOK','POLL','RECON')),
  CONSTRAINT ck_payment_transactions_amount CHECK ((reported_amount_minor IS NULL) = (reported_currency IS NULL) AND (reported_amount_minor IS NULL OR reported_amount_minor > 0)),
  CONSTRAINT ck_payment_transactions_fee CHECK (provider_fee_minor IS NULL OR (provider_fee_minor >= 0 AND reported_currency IS NOT NULL)),
  CONSTRAINT ck_payment_transactions_raw_status CHECK (char_length(raw_status) BETWEEN 1 AND 200),
  CONSTRAINT ck_payment_transactions_masked CHECK (payer_instrument_masked IS NULL OR payer_instrument_masked ~ '^[*]{2,8}[0-9A-Za-z]{0,4}$')
);
COMMENT ON TABLE app.payment_transactions IS 'C2. Latest authoritative provider observation per payment (raw status verbatim).';
CREATE TRIGGER trg_payment_transactions_updated_at BEFORE UPDATE ON app.payment_transactions FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payment_transactions_keep_created_at BEFORE UPDATE ON app.payment_transactions FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_payment_transactions_no_delete BEFORE DELETE ON app.payment_transactions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =========================================================================================================
-- payment_provider_references — every provider reference ever seen; unique per (provider, kind, ref).
-- (REFUND/DISPUTE kinds arrive with refund_transactions / disputes; ADR-039 §4.)
-- =========================================================================================================
CREATE TABLE app.payment_provider_references (
  id                 uuid        NOT NULL,
  provider_id        uuid        NOT NULL,
  ref_kind           text        NOT NULL,
  reference          text        NOT NULL,
  payment_intent_id  uuid        NOT NULL,
  source             text        NOT NULL,
  recorded_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_provider_references PRIMARY KEY (id),
  CONSTRAINT fk_payment_provider_references_intent FOREIGN KEY (payment_intent_id, provider_id) REFERENCES app.payment_intents (id, provider_id) ON DELETE RESTRICT,
  CONSTRAINT uq_payment_provider_references UNIQUE (provider_id, ref_kind, reference),
  CONSTRAINT ck_payment_provider_references_kind CHECK (ref_kind IN ('PAYMENT','TRANSACTION','CHECKOUT_SESSION','POLL_URL','AUTHORISATION','CAPTURE')),
  CONSTRAINT ck_payment_provider_references_reference CHECK (char_length(reference) BETWEEN 1 AND 255),
  CONSTRAINT ck_payment_provider_references_source CHECK (source IN ('SYNC','WEBHOOK','POLL','RECON'))
);
CREATE INDEX ix_payment_provider_references_intent ON app.payment_provider_references (payment_intent_id);
CREATE TRIGGER trg_payment_provider_references_no_mutation BEFORE UPDATE OR DELETE ON app.payment_provider_references FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payment_provider_references_no_truncate BEFORE TRUNCATE ON app.payment_provider_references FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =========================================================================================================
-- payment_sessions — the donor-facing next step a provider asked for (hosted redirect, handset approval,
-- instructions). Bound to the donor; the return token is single-use (replay protection). A return NEVER
-- confirms a payment: it only triggers a status query (CLAUDE.md financial rule 6).
-- =========================================================================================================
CREATE TABLE app.payment_sessions (
  id                 uuid        NOT NULL,
  payment_intent_id  uuid        NOT NULL,
  donor_user_id      uuid        NOT NULL,
  kind               text        NOT NULL,
  status             text        NOT NULL,
  redirect_url       text,
  instructions_code  text,
  return_token_hash  bytea       NOT NULL,
  expires_at         timestamptz NOT NULL,
  returned_at        timestamptz,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_sessions PRIMARY KEY (id),
  CONSTRAINT fk_payment_sessions_intent FOREIGN KEY (payment_intent_id) REFERENCES app.payment_intents (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_sessions_donor FOREIGN KEY (donor_user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payment_sessions_intent UNIQUE (payment_intent_id),
  CONSTRAINT uq_payment_sessions_token UNIQUE (return_token_hash),
  CONSTRAINT ck_payment_sessions_kind CHECK (kind IN ('REDIRECT','HANDSET_APPROVAL','INSTRUCTIONS')),
  CONSTRAINT ck_payment_sessions_status CHECK (status IN ('OPEN','RETURNED','EXPIRED','CLOSED')),
  CONSTRAINT ck_payment_sessions_redirect CHECK ((kind = 'REDIRECT') = (redirect_url IS NOT NULL)
    AND (redirect_url IS NULL OR (redirect_url ~ '^https://[^/?#@]+/' AND char_length(redirect_url) <= 2048))),
  CONSTRAINT ck_payment_sessions_token CHECK (octet_length(return_token_hash) = 32),
  CONSTRAINT ck_payment_sessions_returned CHECK ((status = 'RETURNED') = (returned_at IS NOT NULL)),
  CONSTRAINT ck_payment_sessions_expiry CHECK (expires_at > created_at)
);
COMMENT ON TABLE app.payment_sessions IS 'C2. Donor next-step session (redirect/handset/instructions). A return never confirms a payment.';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payment_session', '',       'OPEN'),
  ('payment_session', 'OPEN',   'RETURNED'),
  ('payment_session', 'OPEN',   'EXPIRED'),
  ('payment_session', 'OPEN',   'CLOSED'),
  ('payment_session', 'RETURNED', 'CLOSED');
CREATE FUNCTION app.payment_sessions_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.payment_intent_id, NEW.donor_user_id, NEW.kind, NEW.redirect_url, NEW.instructions_code, NEW.return_token_hash, NEW.expires_at)
     IS DISTINCT FROM (OLD.payment_intent_id, OLD.donor_user_id, OLD.kind, OLD.redirect_url, OLD.instructions_code, OLD.return_token_hash, OLD.expires_at) THEN
    RAISE EXCEPTION 'payment session facts are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payment_sessions_immutable BEFORE UPDATE ON app.payment_sessions FOR EACH ROW EXECUTE FUNCTION app.payment_sessions_immutable();
CREATE TRIGGER trg_payment_sessions_guard_status BEFORE INSERT OR UPDATE OF status ON app.payment_sessions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payment_session');
CREATE TRIGGER trg_payment_sessions_updated_at BEFORE UPDATE ON app.payment_sessions FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payment_sessions_no_delete BEFORE DELETE ON app.payment_sessions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =========================================================================================================
-- refund_requests — business ask under maker-checker. Stage 8: execution impossible (no APPROVED->EXECUTING
-- edge; no refund_transactions). Over-refund guard counts approved requests (no chargebacks table yet).
-- =========================================================================================================
CREATE TABLE app.refund_requests (
  id                  uuid        NOT NULL,
  payment_intent_id   uuid        NOT NULL,
  campaign_id         uuid        NOT NULL,
  currency            char(3)     NOT NULL,
  amount_minor        bigint      NOT NULL,
  reason_code         text        NOT NULL,
  reason_text         text        NOT NULL,
  channel             text        NOT NULL,
  requested_by        uuid        NOT NULL,
  requested_at        timestamptz NOT NULL,
  status              text        NOT NULL,
  decided_by          uuid,
  decided_at          timestamptz,
  decision_reason     text,
  decided_step_up_at  timestamptz,
  execution_method    text        NOT NULL DEFAULT 'PROVIDER_REFUND',
  idempotency_key     uuid        NOT NULL,
  expires_at          timestamptz NOT NULL,
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_refund_requests PRIMARY KEY (id),
  CONSTRAINT fk_refund_requests_intent FOREIGN KEY (payment_intent_id, campaign_id, currency) REFERENCES app.payment_intents (id, campaign_id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_refund_requests_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_refund_requests_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_refund_requests_idempotency UNIQUE (requested_by, idempotency_key),
  CONSTRAINT ck_refund_requests_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_refund_requests_reason_code CHECK (reason_code IN ('DONOR_REQUEST','DUPLICATE_PAYMENT','PAYMENT_ERROR','CAMPAIGN_CANCELLED','CAMPAIGN_FRAUD','COMPLIANCE_ORDER','OTHER')),
  CONSTRAINT ck_refund_requests_reason_text CHECK (char_length(reason_text) BETWEEN 3 AND 2000),
  CONSTRAINT ck_refund_requests_channel CHECK (channel IN ('DONOR','SUPPORT','COMPLIANCE','SYSTEM')),
  CONSTRAINT ck_refund_requests_status CHECK (status IN ('REQUESTED','PENDING_APPROVAL','ON_HOLD','APPROVED','EXECUTING','COMPLETED','FAILED','REJECTED','WITHDRAWN')),
  CONSTRAINT ck_refund_requests_execution_method CHECK (execution_method IN ('PROVIDER_REFUND','MANUAL_PAYOUT')),
  CONSTRAINT ck_refund_requests_maker_checker CHECK (decided_by IS NULL OR decided_by <> requested_by),
  CONSTRAINT ck_refund_requests_decision CHECK (
    status NOT IN ('APPROVED','EXECUTING','COMPLETED','FAILED','REJECTED')
    OR (decided_by IS NOT NULL AND decided_at IS NOT NULL AND decision_reason IS NOT NULL)),
  CONSTRAINT ck_refund_requests_approval_step_up CHECK (status NOT IN ('APPROVED','EXECUTING','COMPLETED','FAILED') OR decided_step_up_at IS NOT NULL),
  CONSTRAINT ck_refund_requests_expiry CHECK (expires_at > requested_at)
);
COMMENT ON TABLE app.refund_requests IS 'C2. Refund requests (maker-checker). Stage 8: recorded and decided only; never executed (ADR-039 §4).';
CREATE INDEX ix_refund_requests_payment_intent_id ON app.refund_requests (payment_intent_id);
CREATE INDEX ix_refund_requests_queue ON app.refund_requests (status, requested_at, id);

-- Stage 8 edges only: the request can be decided, never executed (APPROVED is final until Stage 11).
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('refund_request', '',                 'REQUESTED'),
  ('refund_request', 'REQUESTED',        'PENDING_APPROVAL'),
  ('refund_request', 'REQUESTED',        'WITHDRAWN'),
  ('refund_request', 'PENDING_APPROVAL', 'APPROVED'),
  ('refund_request', 'PENDING_APPROVAL', 'REJECTED'),
  ('refund_request', 'PENDING_APPROVAL', 'ON_HOLD'),
  ('refund_request', 'PENDING_APPROVAL', 'WITHDRAWN'),
  ('refund_request', 'ON_HOLD',          'PENDING_APPROVAL'),
  ('refund_request', 'ON_HOLD',          'REJECTED');

CREATE FUNCTION app.refund_requests_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.payment_intent_id, NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.requested_by, NEW.requested_at, NEW.idempotency_key, NEW.reason_code, NEW.reason_text)
     IS DISTINCT FROM (OLD.payment_intent_id, OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.requested_by, OLD.requested_at, OLD.idempotency_key, OLD.reason_code, OLD.reason_text) THEN
    RAISE EXCEPTION 'refund request amount, currency, payment, reason and requester are immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_refund_requests_immutable BEFORE UPDATE ON app.refund_requests FOR EACH ROW EXECUTE FUNCTION app.refund_requests_immutable();
CREATE TRIGGER trg_refund_requests_guard_status BEFORE INSERT OR UPDATE OF status ON app.refund_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('refund_request');
CREATE TRIGGER trg_refund_requests_updated_at BEFORE UPDATE ON app.refund_requests FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_refund_requests_keep_created_at BEFORE UPDATE ON app.refund_requests FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_refund_requests_no_delete BEFORE DELETE ON app.refund_requests FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- Over-refund guard (refund-and-reversal-flows §3.2): on insert of any live request and on approval, the
-- sum of approved requests (and the new one) must not exceed the captured amount; the payment must be
-- SUCCEEDED or PARTIALLY_REFUNDED. Locks the payment row FOR UPDATE so concurrent requests serialise.
CREATE FUNCTION app.refund_requests_check_refundable() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  pi        app.payment_intents%ROWTYPE;
  committed bigint;
BEGIN
  IF NEW.status NOT IN ('REQUESTED','APPROVED') OR (TG_OP = 'UPDATE' AND OLD.status = NEW.status) THEN
    RETURN NULL;
  END IF;
  SELECT * INTO pi FROM app.payment_intents WHERE id = NEW.payment_intent_id FOR UPDATE;
  IF pi.status NOT IN ('SUCCEEDED','PARTIALLY_REFUNDED') THEN
    RAISE EXCEPTION 'refund not allowed: payment % is %', pi.id, pi.status USING ERRCODE = 'check_violation';
  END IF;
  SELECT COALESCE(sum(r.amount_minor), 0) INTO committed
    FROM app.refund_requests r
   WHERE r.payment_intent_id = pi.id AND r.currency = pi.currency
     AND r.status IN ('APPROVED','EXECUTING','COMPLETED') AND r.id <> NEW.id;
  IF committed + NEW.amount_minor > pi.amount_minor THEN
    RAISE EXCEPTION 'over-refund: approved % + requested % exceed captured % %', committed, NEW.amount_minor, pi.amount_minor, pi.currency
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE TRIGGER trg_refund_requests_check_refundable AFTER INSERT OR UPDATE OF status ON app.refund_requests
  FOR EACH ROW EXECUTE FUNCTION app.refund_requests_check_refundable();

-- =========================================================================================================
-- payment_reconciliation_references — the foundation for Stage 17: why a payment needs reconciliation and
-- what the provider reported. Never marks anything "reconciled" (no settlement data exists in Stage 8).
-- =========================================================================================================
CREATE TABLE app.payment_reconciliation_references (
  id                     uuid        NOT NULL,
  payment_intent_id      uuid        NOT NULL,
  provider_id            uuid        NOT NULL,
  payment_attempt_id     uuid,
  inbox_event_id         uuid,
  provider_reference     text,
  reason                 text        NOT NULL,
  expected_amount_minor  bigint      NOT NULL,
  expected_currency      char(3)     NOT NULL,
  reported_amount_minor  bigint,
  reported_currency      char(3),
  provider_status_raw    text,
  status                 text        NOT NULL,
  evidence_at            timestamptz NOT NULL,
  resolved_at            timestamptz,
  resolved_by            uuid,
  resolution_note        text,
  version                integer     NOT NULL DEFAULT 1,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_reconciliation_references PRIMARY KEY (id),
  CONSTRAINT fk_payment_reconciliation_references_intent FOREIGN KEY (payment_intent_id, provider_id) REFERENCES app.payment_intents (id, provider_id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_reconciliation_references_attempt FOREIGN KEY (payment_attempt_id) REFERENCES app.payment_attempts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_reconciliation_references_inbox FOREIGN KEY (inbox_event_id) REFERENCES app.provider_webhook_inbox (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_reconciliation_references_expected_currency FOREIGN KEY (expected_currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_reconciliation_references_reported_currency FOREIGN KEY (reported_currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_reconciliation_references_resolved_by FOREIGN KEY (resolved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payment_reconciliation_references_reason CHECK (reason IN ('UNKNOWN_HORIZON_EXCEEDED','NOT_FOUND_AFTER_HORIZON','AMOUNT_MISMATCH',
    'CURRENCY_MISMATCH','ANOMALY','LATE_SUCCESS','DUPLICATE_SUCCESS','PARKED_DEADLINE_EXCEEDED','UNMAPPED_STATUS')),
  CONSTRAINT ck_payment_reconciliation_references_status CHECK (status IN ('OPEN','AWAITING_SETTLEMENT_DATA','RESOLVED_BY_PROVIDER_STATUS')),
  CONSTRAINT ck_payment_reconciliation_references_reported CHECK ((reported_amount_minor IS NULL) = (reported_currency IS NULL)),
  CONSTRAINT ck_payment_reconciliation_references_resolved CHECK (
    (status = 'RESOLVED_BY_PROVIDER_STATUS') = (resolved_at IS NOT NULL) AND (resolved_at IS NULL OR resolution_note IS NOT NULL))
);
COMMENT ON TABLE app.payment_reconciliation_references IS 'C1. Reconciliation foundation (Stage 17 completes). Never records a payment as reconciled.';
CREATE INDEX ix_payment_reconciliation_references_open ON app.payment_reconciliation_references (status, created_at DESC, id DESC);
CREATE INDEX ix_payment_reconciliation_references_intent ON app.payment_reconciliation_references (payment_intent_id);
CREATE UNIQUE INDEX uq_payment_reconciliation_references_open_reason ON app.payment_reconciliation_references (payment_intent_id, reason)
  WHERE status <> 'RESOLVED_BY_PROVIDER_STATUS';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payment_reconciliation_reference', '',                         'OPEN'),
  ('payment_reconciliation_reference', 'OPEN',                     'AWAITING_SETTLEMENT_DATA'),
  ('payment_reconciliation_reference', 'OPEN',                     'RESOLVED_BY_PROVIDER_STATUS'),
  ('payment_reconciliation_reference', 'AWAITING_SETTLEMENT_DATA', 'RESOLVED_BY_PROVIDER_STATUS');
CREATE FUNCTION app.payment_reconciliation_references_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.payment_intent_id, NEW.provider_id, NEW.payment_attempt_id, NEW.inbox_event_id, NEW.provider_reference, NEW.reason,
      NEW.expected_amount_minor, NEW.expected_currency, NEW.reported_amount_minor, NEW.reported_currency, NEW.provider_status_raw, NEW.evidence_at)
     IS DISTINCT FROM (OLD.payment_intent_id, OLD.provider_id, OLD.payment_attempt_id, OLD.inbox_event_id, OLD.provider_reference, OLD.reason,
      OLD.expected_amount_minor, OLD.expected_currency, OLD.reported_amount_minor, OLD.reported_currency, OLD.provider_status_raw, OLD.evidence_at) THEN
    RAISE EXCEPTION 'reconciliation evidence is immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payment_reconciliation_references_immutable BEFORE UPDATE ON app.payment_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION app.payment_reconciliation_references_immutable();
CREATE TRIGGER trg_payment_reconciliation_references_guard_status BEFORE INSERT OR UPDATE OF status ON app.payment_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payment_reconciliation_reference');
CREATE TRIGGER trg_payment_reconciliation_references_updated_at BEFORE UPDATE ON app.payment_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payment_reconciliation_references_no_delete BEFORE DELETE ON app.payment_reconciliation_references
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- =========================================================================================================
-- payment_events — append-only history of every payments machine (donation intents, payments, refund
-- requests). Also records NOT-applied observations so nothing a provider said is lost.
-- =========================================================================================================
CREATE TABLE app.payment_events (
  id                   uuid        NOT NULL,
  subject_type         text        NOT NULL,
  donation_intent_id   uuid        NOT NULL,
  payment_intent_id    uuid,
  refund_request_id    uuid,
  from_status          text,
  to_status            text        NOT NULL,
  applied              boolean     NOT NULL,
  not_applied_reason   text,
  transition_code      text,
  source               text        NOT NULL,
  provider_event_id    text,
  inbox_event_id       uuid,
  payment_attempt_id   uuid,
  raw_status           text,
  actor_type           text        NOT NULL,
  actor_id             uuid,
  reason               text,
  evidence_record_id   uuid,
  correlation_id       text,
  occurred_at          timestamptz,
  recorded_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payment_events PRIMARY KEY (id),
  CONSTRAINT fk_payment_events_donation FOREIGN KEY (donation_intent_id) REFERENCES app.donation_intents (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payment_events_intent FOREIGN KEY (payment_intent_id) REFERENCES app.payment_intents (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payment_events_refund_request FOREIGN KEY (refund_request_id) REFERENCES app.refund_requests (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payment_events_inbox FOREIGN KEY (inbox_event_id) REFERENCES app.provider_webhook_inbox (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_events_attempt FOREIGN KEY (payment_attempt_id) REFERENCES app.payment_attempts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_events_actor FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payment_events_evidence FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payment_events_subject_type CHECK (subject_type IN ('DONATION_INTENT','PAYMENT_INTENT','REFUND_REQUEST')),
  CONSTRAINT ck_payment_events_subject CHECK (
       (subject_type = 'DONATION_INTENT' AND payment_intent_id IS NULL AND refund_request_id IS NULL)
    OR (subject_type = 'PAYMENT_INTENT'  AND payment_intent_id IS NOT NULL AND refund_request_id IS NULL)
    OR (subject_type = 'REFUND_REQUEST'  AND payment_intent_id IS NOT NULL AND refund_request_id IS NOT NULL)),
  CONSTRAINT ck_payment_events_source CHECK (source IN ('SYNC','WEBHOOK','POLL','RECON','INTERNAL','STAFF','DONOR')),
  CONSTRAINT ck_payment_events_actor_type CHECK (actor_type IN ('SYSTEM','DONOR','STAFF','PROVIDER')),
  CONSTRAINT ck_payment_events_not_applied CHECK (
    (applied AND not_applied_reason IS NULL)
    OR (NOT applied AND not_applied_reason IN ('LOWER_RANK','NOT_AN_EDGE','ANOMALY','DUPLICATE','PARKED'))),
  CONSTRAINT ck_payment_events_staff CHECK (source <> 'STAFF' OR (actor_type = 'STAFF' AND actor_id IS NOT NULL AND reason IS NOT NULL)),
  CONSTRAINT ck_payment_events_donor CHECK (source <> 'DONOR' OR (actor_type = 'DONOR' AND actor_id IS NOT NULL)),
  CONSTRAINT ck_payment_events_transition_code CHECK (transition_code IS NULL OR transition_code ~ '^P([1-9]|1[0-9]|20|18b)$'),
  -- Money is confirmed only by authoritative provider state: never INTERNAL, STAFF or DONOR (browser).
  CONSTRAINT ck_payment_events_success_source CHECK (
    NOT applied OR to_status <> 'SUCCEEDED' OR subject_type <> 'PAYMENT_INTENT'
    OR source IN ('SYNC','WEBHOOK','POLL','RECON')),
  -- Only our own indeterminate call can put a payment into UNKNOWN.
  CONSTRAINT ck_payment_events_unknown_internal CHECK (
    NOT applied OR to_status <> 'UNKNOWN' OR subject_type <> 'PAYMENT_INTENT' OR source = 'INTERNAL'),
  -- I-21: FAILED only from authoritative sources, INTERNAL from CREATED (pre-submit, P4), or STAFF with evidence.
  CONSTRAINT ck_payment_events_failed_source CHECK (
    NOT applied OR to_status <> 'FAILED' OR subject_type <> 'PAYMENT_INTENT'
    OR source IN ('WEBHOOK','POLL','RECON','SYNC')
    OR (source = 'INTERNAL' AND from_status = 'CREATED')
    OR source = 'STAFF'),
  CONSTRAINT ck_payment_events_staff_failed_evidence CHECK (
    NOT applied OR to_status <> 'FAILED' OR source <> 'STAFF' OR evidence_record_id IS NOT NULL),
  -- A donation intent becomes PAID only because a payment succeeded (recorded by the system in that tx).
  CONSTRAINT ck_payment_events_paid_internal CHECK (
    NOT applied OR subject_type <> 'DONATION_INTENT' OR to_status <> 'PAID' OR source = 'INTERNAL')
);
COMMENT ON TABLE app.payment_events IS 'C1. Append-only history of donation intents, payments and refund requests (applied and not applied).';
CREATE INDEX ix_payment_events_donation ON app.payment_events (donation_intent_id, recorded_at);
CREATE INDEX ix_payment_events_intent ON app.payment_events (payment_intent_id, recorded_at) WHERE payment_intent_id IS NOT NULL;
CREATE INDEX ix_payment_events_refund_request ON app.payment_events (refund_request_id) WHERE refund_request_id IS NOT NULL;
CREATE UNIQUE INDEX uq_payment_events_applied_provider_event ON app.payment_events (payment_intent_id, provider_event_id)
  WHERE applied AND provider_event_id IS NOT NULL;
CREATE TRIGGER trg_payment_events_no_mutation BEFORE UPDATE OR DELETE ON app.payment_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payment_events_no_truncate BEFORE TRUNCATE ON app.payment_events FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Every status change must be accompanied, in the SAME transaction, by an applied payment_events row with the
-- same from/to (state derivable from history: DATABASE §9). Deferred to COMMIT.
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
CREATE CONSTRAINT TRIGGER trg_donation_intents_require_event AFTER INSERT OR UPDATE OF status ON app.donation_intents
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_payment_event('donation_intent_id', 'DONATION_INTENT');
CREATE CONSTRAINT TRIGGER trg_payment_intents_require_event AFTER INSERT OR UPDATE OF status ON app.payment_intents
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_payment_event('payment_intent_id', 'PAYMENT_INTENT');
CREATE CONSTRAINT TRIGGER trg_refund_requests_require_event AFTER INSERT OR UPDATE OF status ON app.refund_requests
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_payment_event('refund_request_id', 'REFUND_REQUEST');

-- ---------------------------------------------------------------------------------------------------------
-- Permissions (refund.request / refund.approve exist since Stage 4).
-- ---------------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:payment.view')::uuid, 'payment.view', 'View payments, provider calls, webhooks and reconciliation references', true, false),
  (md5('permission:payment.operate')::uuid, 'payment.operate', 'Request a provider status query, re-queue a dead-lettered webhook, record reconciliation status', true, true),
  (md5('permission:payment.sandbox.control')::uuid, 'payment.sandbox.control', 'Drive the sandbox simulator (development and test environments only)', false, false);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES ('FINANCE', 'payment.view'), ('COMPLIANCE', 'payment.view'), ('SUPPORT', 'payment.view'),
             ('FINANCE', 'payment.operate'), ('ADMIN', 'payment.sandbox.control')) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

-- Runtime grants: the procedure is re-created with app.payment_status_rank(text) added to the routine allow-list
-- (the CHECK on payment_intents.status_rank calls it under the runtime role). Otherwise identical to Stage 6.
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly';
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles',
                                          'ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines'];
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
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

  -- fundzim_app: app, queue, ledger, audit, risk, recon. Never kyc or compliance.
  FOREACH s IN ARRAY ARRAY['app','queue','ledger','audit','risk','recon'] LOOP
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
     WHERE n.nspname IN ('app','queue','ledger','audit','risk','recon')
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
    ELSIF r.sch = 'ledger' THEN
      privs := 'SELECT, INSERT';
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
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
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
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (md5('permission:payment.view')::uuid, md5('permission:payment.operate')::uuid, md5('permission:payment.sandbox.control')::uuid);
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('payment.view', 'payment.operate', 'payment.sandbox.control');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP TABLE IF EXISTS app.payment_events, app.payment_reconciliation_references, app.refund_requests, app.payment_sessions,
  app.payment_provider_references, app.payment_transactions, app.payment_attempts, app.payment_intents, app.donation_intents;
DROP FUNCTION IF EXISTS app.require_payment_event(), app.payment_reconciliation_references_immutable(), app.refund_requests_check_refundable(),
  app.refund_requests_immutable(), app.payment_sessions_immutable(), app.payment_intents_immutable(), app.donation_intents_immutable(),
  app.attempt_complete_once(), app.payment_status_rank(text);
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine IN ('donation_intent', 'payment_intent', 'payment_session', 'refund_request', 'payment_reconciliation_reference');
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly';
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles',
                                          'ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines'];
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
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

  -- fundzim_app: app, queue, ledger, audit, risk, recon. Never kyc or compliance.
  FOREACH s IN ARRAY ARRAY['app','queue','ledger','audit','risk','recon'] LOOP
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
     WHERE n.nspname IN ('app','queue','ledger','audit','risk','recon')
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
    ELSIF r.sch = 'ledger' THEN
      privs := 'SELECT, INSERT';
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
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
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
