-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0016: payouts module (schema app): payout_destinations, payout_destination_verifications,
--            payout_requests, payout_attempts, payout_approvals, payout_reservations,
--            payout_provider_references, payout_events, payout_failures, payout_reversals,
--            payout_eligibility_decisions, recovery_cases, recovery_case_events.
-- Explanations: docs/database/payout-schema.md, docs/architecture/payout-state-machine.md.
-- Sources: payout-lifecycle.md (Y1–Y14), payout-eligibility-and-controls.md (EC-01–EC-22, tiers),
--          operational-controls.md §2–§4, ADR-017, ADR-020, refund-and-dispute-architecture.md §3.3.
-- References (declared FKs only): app.currencies, app.users, app.organisations, app.campaigns,
--   app.beneficiaries, app.institution_payees, psp tables (0007), ledger.ledger_transactions, risk.holds,
--   audit.evidence_records. No FK to compliance (payouts reads compliance through its interface).
-- =====================================================================================================

CREATE FUNCTION app.payout_status_rank(s text) RETURNS smallint LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT CASE s
    WHEN 'PAYOUT_REQUESTED' THEN 0 WHEN 'PENDING_REVIEW' THEN 10 WHEN 'APPROVED' THEN 20
    WHEN 'SUBMITTED' THEN 30 WHEN 'UNKNOWN' THEN 35 WHEN 'PROCESSING' THEN 40
    WHEN 'FAILED' THEN 50 WHEN 'REJECTED' THEN 50 WHEN 'CANCELLED' THEN 50
    WHEN 'COMPLETED' THEN 60 WHEN 'REVERSED' THEN 70 END::smallint
$$;

-- Generic deferred check: a status change of <aggregate> must be accompanied, in the same DB transaction,
-- by an applied row in <events table> with the same from/to. TG_ARGV: events table, subject id column.
CREATE FUNCTION app.require_status_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  tbl text := TG_ARGV[0];
  col text := TG_ARGV[1];
  f   text;
  ok  boolean;
BEGIN
  IF TG_OP = 'UPDATE' AND NEW.status IS NOT DISTINCT FROM OLD.status THEN
    RETURN NULL;
  END IF;
  f := CASE WHEN TG_OP = 'INSERT' THEN '' ELSE OLD.status END;
  EXECUTE format(
    'SELECT EXISTS (SELECT 1 FROM app.%I e WHERE e.%I = $1 AND COALESCE(e.from_status, '''') = $2
                       AND e.to_status = $3 AND e.applied AND e.recorded_at = transaction_timestamp())', tbl, col)
    INTO ok USING NEW.id, f, NEW.status;
  IF NOT ok THEN
    RAISE EXCEPTION 'missing % row for % transition "%" -> "%"', tbl, NEW.id, f, NEW.status
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;

-- =====================================================================================================
-- payout_destinations — C3 account number encrypted (key class 'payout-destination') + HMAC blind index.
-- Versioned: any change to the account data bumps `version`, resets verification and starts cooling-off.
-- =====================================================================================================
CREATE TABLE app.payout_destinations (
  id                         uuid        NOT NULL,
  owner_user_id              uuid,
  owner_organisation_id      uuid,
  owner_ref                  uuid GENERATED ALWAYS AS (COALESCE(owner_user_id, owner_organisation_id)) STORED,
  payee_type                 text        NOT NULL,
  beneficiary_id             uuid,
  institution_payee_id       uuid,
  rail                       text        NOT NULL,
  currency                   char(3)     NOT NULL,
  holder_name                text        NOT NULL,     -- C2; used for name-match
  bank_code                  text,                     -- bank/branch identifier for BANK_TRANSFER / ZIMSWITCH
  account_number_ciphertext  bytea       NOT NULL,     -- C3, envelope-encrypted by the app
  account_number_key_id      text        NOT NULL,
  account_number_bidx        bytea       NOT NULL,     -- HMAC blind index (duplicate / mule detection)
  masked_suffix              text        NOT NULL,     -- last 2–4 characters only, for display
  destination_hash           bytea       NOT NULL,     -- SHA-256 over canonical (rail, currency, bidx, holder, bank, payee, version)
  status                     text        NOT NULL,
  verified_at                timestamptz,
  cooling_off_until          timestamptz NOT NULL,     -- set on create and on every change (EC-06)
  last_changed_by            uuid        NOT NULL,     -- owner, or staff under maker-checker override
  last_verified_by           uuid,                     -- staff who verified (SoD with payout approval)
  version                    integer     NOT NULL DEFAULT 1,
  created_at                 timestamptz NOT NULL DEFAULT now(),
  updated_at                 timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_destinations PRIMARY KEY (id),
  CONSTRAINT fk_payout_destinations_owner_user_id FOREIGN KEY (owner_user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destinations_owner_organisation_id FOREIGN KEY (owner_organisation_id) REFERENCES app.organisations (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destinations_beneficiary_id FOREIGN KEY (beneficiary_id) REFERENCES app.beneficiaries (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destinations_institution_payee_id FOREIGN KEY (institution_payee_id) REFERENCES app.institution_payees (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destinations_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destinations_last_changed_by FOREIGN KEY (last_changed_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destinations_last_verified_by FOREIGN KEY (last_verified_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_destinations_id_currency_rail UNIQUE (id, currency, rail),
  CONSTRAINT ck_payout_destinations_owner CHECK ((owner_user_id IS NULL) <> (owner_organisation_id IS NULL)),
  -- Exactly one payee reference, consistent with payee_type (handover D4).
  CONSTRAINT ck_payout_destinations_payee CHECK (
       (payee_type = 'OWNER'             AND beneficiary_id IS NULL     AND institution_payee_id IS NULL)
    OR (payee_type = 'BENEFICIARY'       AND beneficiary_id IS NOT NULL AND institution_payee_id IS NULL)
    OR (payee_type = 'INSTITUTION_PAYEE' AND beneficiary_id IS NULL     AND institution_payee_id IS NOT NULL)),
  CONSTRAINT ck_payout_destinations_rail CHECK (rail IN ('ECOCASH','ONEMONEY','INNBUCKS','OMARI','ZIMSWITCH','BANK_TRANSFER')),
  CONSTRAINT ck_payout_destinations_bank_code CHECK (rail NOT IN ('BANK_TRANSFER','ZIMSWITCH') OR bank_code IS NOT NULL),
  CONSTRAINT ck_payout_destinations_masked_suffix CHECK (masked_suffix ~ '^[0-9A-Za-z]{2,4}$'),
  CONSTRAINT ck_payout_destinations_hash CHECK (octet_length(destination_hash) = 32),
  CONSTRAINT ck_payout_destinations_bidx CHECK (octet_length(account_number_bidx) = 32),
  CONSTRAINT ck_payout_destinations_status CHECK (status IN ('PENDING_VERIFICATION','VERIFIED','VERIFICATION_FAILED','NEEDS_REVERIFICATION','SUSPENDED','RETIRED')),
  CONSTRAINT ck_payout_destinations_verified CHECK (status <> 'VERIFIED' OR (verified_at IS NOT NULL AND last_verified_by IS NOT NULL)),
  CONSTRAINT ck_payout_destinations_version CHECK (version >= 1)
);
-- One live destination per owner for the same account on the same rail and currency.
CREATE UNIQUE INDEX uq_payout_destinations_live_account ON app.payout_destinations (owner_ref, rail, currency, account_number_bidx)
  WHERE status <> 'RETIRED';
CREATE INDEX ix_payout_destinations_bidx ON app.payout_destinations (account_number_bidx);   -- cross-owner reuse = risk signal
CREATE INDEX ix_payout_destinations_owner ON app.payout_destinations (owner_ref);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout_destination', '',                     'PENDING_VERIFICATION'),
  ('payout_destination', 'PENDING_VERIFICATION', 'VERIFIED'),
  ('payout_destination', 'PENDING_VERIFICATION', 'VERIFICATION_FAILED'),
  ('payout_destination', 'PENDING_VERIFICATION', 'RETIRED'),
  ('payout_destination', 'VERIFICATION_FAILED',  'PENDING_VERIFICATION'),
  ('payout_destination', 'VERIFICATION_FAILED',  'RETIRED'),
  ('payout_destination', 'VERIFIED',             'PENDING_VERIFICATION'),   -- account data changed (new version)
  ('payout_destination', 'VERIFIED',             'NEEDS_REVERIFICATION'),   -- reversal / bad-destination failure
  ('payout_destination', 'VERIFIED',             'SUSPENDED'),
  ('payout_destination', 'VERIFIED',             'RETIRED'),
  ('payout_destination', 'NEEDS_REVERIFICATION', 'PENDING_VERIFICATION'),
  ('payout_destination', 'NEEDS_REVERIFICATION', 'VERIFIED'),
  ('payout_destination', 'NEEDS_REVERIFICATION', 'SUSPENDED'),
  ('payout_destination', 'NEEDS_REVERIFICATION', 'RETIRED'),
  ('payout_destination', 'SUSPENDED',            'PENDING_VERIFICATION'),
  ('payout_destination', 'SUSPENDED',            'RETIRED');

CREATE FUNCTION app.payout_destinations_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.owner_user_id, NEW.owner_organisation_id, NEW.rail, NEW.currency) IS DISTINCT FROM
     (OLD.owner_user_id, OLD.owner_organisation_id, OLD.rail, OLD.currency) THEN
    RAISE EXCEPTION 'destination owner, rail and currency are immutable (create a new destination)'
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF (NEW.account_number_ciphertext, NEW.account_number_bidx, NEW.holder_name, NEW.bank_code, NEW.payee_type,
      NEW.beneficiary_id, NEW.institution_payee_id) IS DISTINCT FROM
     (OLD.account_number_ciphertext, OLD.account_number_bidx, OLD.holder_name, OLD.bank_code, OLD.payee_type,
      OLD.beneficiary_id, OLD.institution_payee_id) THEN
    -- A change of account data is a new version: verification resets, cooling-off restarts, hash changes.
    IF NEW.version <> OLD.version + 1 OR NEW.status <> 'PENDING_VERIFICATION'
       OR NEW.cooling_off_until <= now() OR NEW.destination_hash = OLD.destination_hash THEN
      RAISE EXCEPTION 'destination change requires version+1, PENDING_VERIFICATION, new cooling-off and new hash'
        USING ERRCODE = 'check_violation';
    END IF;
  ELSIF NEW.version <> OLD.version OR NEW.destination_hash <> OLD.destination_hash THEN
    RAISE EXCEPTION 'destination version/hash change only with an account data change' USING ERRCODE = 'check_violation';
  ELSIF NEW.account_number_key_id <> OLD.account_number_key_id THEN
    NULL;  -- key rotation re-encrypts ciphertext under a new key id; caught above because ciphertext changes
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_destinations_guard BEFORE UPDATE ON app.payout_destinations FOR EACH ROW EXECUTE FUNCTION app.payout_destinations_guard();
CREATE TRIGGER trg_payout_destinations_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout_destination');
CREATE TRIGGER trg_payout_destinations_updated_at BEFORE UPDATE ON app.payout_destinations FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payout_destinations_keep_created_at BEFORE UPDATE ON app.payout_destinations FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_payout_destinations_no_delete BEFORE DELETE ON app.payout_destinations FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- Append-only verification checks (ownership / name match / rail validity) per destination version.
CREATE TABLE app.payout_destination_verifications (
  id                     uuid        NOT NULL,
  payout_destination_id  uuid        NOT NULL,
  destination_version    integer     NOT NULL,
  check_kind             text        NOT NULL,
  method                 text        NOT NULL,
  result                 text        NOT NULL,
  name_match_score_bps   integer,                -- integer basis points, never a float
  provider_id            uuid,
  provider_reference     text,
  performed_by           uuid,                   -- staff (manual review) or NULL for system/provider lookups
  evidence_record_id     uuid,
  detail_code            text,
  performed_at           timestamptz NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_destination_verifications PRIMARY KEY (id),
  CONSTRAINT fk_payout_destination_verifications_destination FOREIGN KEY (payout_destination_id) REFERENCES app.payout_destinations (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destination_verifications_provider FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destination_verifications_performed_by FOREIGN KEY (performed_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_destination_verifications_evidence FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_destination_verifications_kind CHECK (check_kind IN ('OWNERSHIP','NAME_MATCH','RAIL_VALIDITY')),
  CONSTRAINT ck_payout_destination_verifications_method CHECK (method IN ('PROVIDER_NAME_LOOKUP','PROVIDER_ACCOUNT_VALIDATION','DOCUMENT_REVIEW','BANK_LETTER','MANUAL_STAFF')),
  CONSTRAINT ck_payout_destination_verifications_result CHECK (result IN ('PASS','FAIL','INCONCLUSIVE')),
  CONSTRAINT ck_payout_destination_verifications_score CHECK (name_match_score_bps IS NULL OR name_match_score_bps BETWEEN 0 AND 10000),
  CONSTRAINT ck_payout_destination_verifications_version CHECK (destination_version >= 1),
  CONSTRAINT ck_payout_destination_verifications_manual CHECK (method NOT IN ('MANUAL_STAFF','DOCUMENT_REVIEW','BANK_LETTER') OR (performed_by IS NOT NULL AND evidence_record_id IS NOT NULL))
);
CREATE INDEX ix_payout_destination_verifications_destination ON app.payout_destination_verifications (payout_destination_id, destination_version);
CREATE TRIGGER trg_payout_destination_verifications_no_mutation BEFORE UPDATE OR DELETE ON app.payout_destination_verifications FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_destination_verifications_no_truncate BEFORE TRUNCATE ON app.payout_destination_verifications FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_destination_override_requests — staff change of a payout destination under maker-checker
-- (baseline §12 I-3; operational-controls §3: maker SUPPORT/COMPLIANCE with user-request evidence, checker
-- FINANCE with payout.destination.override.approve, 24 h expiry; triggers cooling-off and owner notification
-- on all channels). Machine 'payout_destination_override':
--   '' -> PENDING -> APPROVED | REJECTED | EXPIRED | WITHDRAWN ; APPROVED -> APPLIED | EXPIRED.
-- APPLIED means the destination row now carries the proposed data as a NEW version, PENDING_VERIFICATION,
-- with cooling-off running and last_changed_by = the maker (so neither can approve payouts to it, §4.1).
-- =====================================================================================================
CREATE TABLE app.payout_destination_override_requests (
  id                               uuid        NOT NULL,
  payout_destination_id            uuid        NOT NULL,
  base_destination_version         integer     NOT NULL,  -- version the maker saw (optimistic check)
  proposed_holder_name             text        NOT NULL,
  proposed_bank_code               text,
  proposed_account_ciphertext      bytea       NOT NULL,  -- C3, same key class as the destination
  proposed_account_key_id          text        NOT NULL,
  proposed_account_bidx            bytea       NOT NULL,
  proposed_masked_suffix           text        NOT NULL,
  proposed_destination_hash        bytea       NOT NULL,
  reason                           text        NOT NULL,
  evidence_record_ids              uuid[]      NOT NULL,  -- user request evidence etc. (audit.evidence_records ids)
  requested_by                     uuid        NOT NULL,
  requested_at                     timestamptz NOT NULL DEFAULT now(),
  expires_at                       timestamptz NOT NULL,
  status                           text        NOT NULL,
  approved_by                      uuid,
  approved_at                      timestamptz,
  approved_step_up_at              timestamptz,
  decision_reason                  text,
  applied_at                       timestamptz,
  applied_destination_version      integer,
  cooling_off_until                timestamptz,             -- copied from the destination when applied
  version                          integer     NOT NULL DEFAULT 1,
  created_at                       timestamptz NOT NULL DEFAULT now(),
  updated_at                       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_destination_override_requests PRIMARY KEY (id),
  CONSTRAINT fk_pdor_destination FOREIGN KEY (payout_destination_id) REFERENCES app.payout_destinations (id) ON DELETE RESTRICT,
  CONSTRAINT fk_pdor_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_pdor_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_pdor_status CHECK (status IN ('PENDING','APPROVED','REJECTED','EXPIRED','WITHDRAWN','APPLIED')),
  CONSTRAINT ck_pdor_maker_checker CHECK (approved_by IS NULL OR approved_by <> requested_by),
  CONSTRAINT ck_pdor_decided CHECK (status NOT IN ('APPROVED','APPLIED','REJECTED')
                                    OR (approved_by IS NOT NULL AND approved_at IS NOT NULL AND decision_reason IS NOT NULL)),
  CONSTRAINT ck_pdor_step_up CHECK (status NOT IN ('APPROVED','APPLIED') OR approved_step_up_at IS NOT NULL),
  CONSTRAINT ck_pdor_applied CHECK ((status = 'APPLIED') = (applied_at IS NOT NULL AND applied_destination_version IS NOT NULL AND cooling_off_until IS NOT NULL)),
  CONSTRAINT ck_pdor_evidence CHECK (cardinality(evidence_record_ids) >= 1),
  CONSTRAINT ck_pdor_expiry CHECK (expires_at > requested_at),
  CONSTRAINT ck_pdor_masked CHECK (proposed_masked_suffix ~ '^[0-9A-Za-z]{2,4}$'),
  CONSTRAINT ck_pdor_hash CHECK (octet_length(proposed_destination_hash) = 32 AND octet_length(proposed_account_bidx) = 32),
  CONSTRAINT ck_pdor_reason CHECK (char_length(btrim(reason)) >= 10)
);
-- One open override per destination.
CREATE UNIQUE INDEX uq_pdor_one_open ON app.payout_destination_override_requests (payout_destination_id)
  WHERE status IN ('PENDING','APPROVED');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout_destination_override', '',         'PENDING'),
  ('payout_destination_override', 'PENDING',  'APPROVED'),
  ('payout_destination_override', 'PENDING',  'REJECTED'),
  ('payout_destination_override', 'PENDING',  'EXPIRED'),
  ('payout_destination_override', 'PENDING',  'WITHDRAWN'),
  ('payout_destination_override', 'APPROVED', 'APPLIED'),
  ('payout_destination_override', 'APPROVED', 'EXPIRED');

CREATE FUNCTION app.payout_destination_override_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  d app.payout_destinations%ROWTYPE;
BEGIN
  IF TG_OP = 'UPDATE' AND (NEW.payout_destination_id, NEW.base_destination_version, NEW.proposed_account_ciphertext,
       NEW.proposed_account_bidx, NEW.proposed_holder_name, NEW.proposed_bank_code, NEW.proposed_destination_hash,
       NEW.requested_by, NEW.requested_at, NEW.evidence_record_ids, NEW.expires_at)
     IS DISTINCT FROM (OLD.payout_destination_id, OLD.base_destination_version, OLD.proposed_account_ciphertext,
       OLD.proposed_account_bidx, OLD.proposed_holder_name, OLD.proposed_bank_code, OLD.proposed_destination_hash,
       OLD.requested_by, OLD.requested_at, OLD.evidence_record_ids, OLD.expires_at) THEN
    RAISE EXCEPTION 'override request content is immutable (the checker approves exactly what was proposed)'
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.status = 'APPROVED' AND (TG_OP = 'INSERT' OR OLD.status <> 'APPROVED') AND NEW.expires_at <= now() THEN
    RAISE EXCEPTION 'override request % expired', NEW.id USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.status = 'APPLIED' AND (TG_OP = 'INSERT' OR OLD.status <> 'APPLIED') THEN
    SELECT * INTO d FROM app.payout_destinations WHERE id = NEW.payout_destination_id FOR UPDATE;
    -- The change must already be on the destination as the next version, unverified, cooling off,
    -- attributed to the maker (so SoD blocks the maker from approving payouts to it).
    IF d.version <> NEW.base_destination_version + 1 OR NEW.applied_destination_version <> d.version
       OR d.destination_hash <> NEW.proposed_destination_hash OR d.account_number_bidx <> NEW.proposed_account_bidx
       OR d.status <> 'PENDING_VERIFICATION' OR d.cooling_off_until <= now()
       OR NEW.cooling_off_until <> d.cooling_off_until OR d.last_changed_by <> NEW.requested_by THEN
      RAISE EXCEPTION 'override % applied without the matching new destination version, cooling-off and maker attribution', NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_pdor_guard BEFORE INSERT OR UPDATE ON app.payout_destination_override_requests
  FOR EACH ROW EXECUTE FUNCTION app.payout_destination_override_guard();
CREATE TRIGGER trg_pdor_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_destination_override_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout_destination_override');
CREATE TRIGGER trg_pdor_updated_at BEFORE UPDATE ON app.payout_destination_override_requests FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_pdor_keep_created_at BEFORE UPDATE ON app.payout_destination_override_requests FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_pdor_no_delete BEFORE DELETE ON app.payout_destination_override_requests FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_pdor_no_truncate BEFORE TRUNCATE ON app.payout_destination_override_requests FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_requests — Stage 1 `payouts`. id = payout_id = provider reference and idempotency key on every
-- submission attempt, forever. Machine 'payout' (Y1–Y14).
-- =====================================================================================================
CREATE TABLE app.payout_requests (
  id                          uuid        NOT NULL,
  campaign_id                 uuid        NOT NULL,
  currency                    char(3)     NOT NULL,
  amount_minor                bigint      NOT NULL,
  requested_by                uuid        NOT NULL,
  requested_at                timestamptz NOT NULL DEFAULT now(),
  status                      text        NOT NULL,
  status_rank                 smallint    NOT NULL,
  approval_policy             text        NOT NULL,     -- AUTO | SINGLE | DUAL (may only be raised)
  approval_round              integer     NOT NULL DEFAULT 1,   -- bumped by Y7: earlier approvals stop counting
  policy_version              text        NOT NULL,
  -- Destination snapshot (payout-lifecycle §7): submission uses the snapshot; EC-07 compares versions.
  destination_id              uuid        NOT NULL,
  destination_version         integer     NOT NULL,
  destination_hash            bytea       NOT NULL,
  destination_rail            text        NOT NULL,
  destination_masked          text        NOT NULL,
  -- Provider routing (pinned to a routable capability with payout_api, by composite FK).
  provider_id                 uuid        NOT NULL,
  provider_account_id         uuid        NOT NULL,
  capability_id               uuid        NOT NULL,
  capability_routable         boolean     NOT NULL,
  capability_payout_api       boolean     NOT NULL,
  provider_payout_ref         text,
  provider_fee_minor          bigint,                   -- PCR-013, PD-23
  beneficiary_received_minor  bigint,
  -- Unknown-outcome handling
  unknown_since               timestamptz,
  unknown_reason              text,
  next_poll_at                timestamptz,
  poll_attempts               integer     NOT NULL DEFAULT 0,
  poll_horizon_at             timestamptz,
  needs_reconciliation        boolean     NOT NULL DEFAULT false,
  submitted_at                timestamptz,
  completed_at                timestamptz,
  failure_code                text,
  idempotency_scope           text        NOT NULL,
  idempotency_key             uuid        NOT NULL,
  version                     integer     NOT NULL DEFAULT 1,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_requests PRIMARY KEY (id),
  CONSTRAINT fk_payout_requests_campaign_id FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  -- Payout currency = destination currency; rail snapshot = destination rail.
  CONSTRAINT fk_payout_requests_destination FOREIGN KEY (destination_id, currency, destination_rail) REFERENCES app.payout_destinations (id, currency, rail) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_provider_account FOREIGN KEY (provider_account_id, provider_id) REFERENCES app.provider_accounts (id, provider_id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_requests_capability FOREIGN KEY (capability_id, provider_id, currency, capability_routable, capability_payout_api)
    REFERENCES app.provider_capabilities (id, provider_id, currency, routable, payout_api) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_requests_idempotency UNIQUE (idempotency_scope, idempotency_key),
  CONSTRAINT uq_payout_requests_provider_ref UNIQUE (provider_id, provider_payout_ref),
  CONSTRAINT uq_payout_requests_key UNIQUE (id, campaign_id, currency, amount_minor),
  CONSTRAINT uq_payout_requests_id_currency UNIQUE (id, currency),
  CONSTRAINT ck_payout_requests_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payout_requests_status CHECK (status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED','SUBMITTED','PROCESSING','UNKNOWN','COMPLETED','FAILED','REJECTED','CANCELLED','REVERSED')),
  CONSTRAINT ck_payout_requests_status_rank CHECK (status_rank = app.payout_status_rank(status)),
  CONSTRAINT ck_payout_requests_approval_policy CHECK (approval_policy IN ('AUTO','SINGLE','DUAL')),
  CONSTRAINT ck_payout_requests_approval_round CHECK (approval_round >= 1),
  CONSTRAINT ck_payout_requests_destination_version CHECK (destination_version >= 1),
  CONSTRAINT ck_payout_requests_destination_hash CHECK (octet_length(destination_hash) = 32),
  CONSTRAINT ck_payout_requests_routable CHECK (capability_routable AND capability_payout_api),
  CONSTRAINT ck_payout_requests_fee CHECK (provider_fee_minor IS NULL OR provider_fee_minor >= 0),
  CONSTRAINT ck_payout_requests_received CHECK (beneficiary_received_minor IS NULL OR (beneficiary_received_minor > 0 AND beneficiary_received_minor <= amount_minor)),
  CONSTRAINT ck_payout_requests_unknown_reason CHECK (unknown_reason IS NULL OR unknown_reason IN ('TIMEOUT','CONN_RESET_AFTER_SEND','HTTP_5XX','MALFORMED_RESPONSE','SIGNATURE_INVALID_RESPONSE','CRASH_DURING_CREATE')),
  CONSTRAINT ck_payout_requests_unknown CHECK (status <> 'UNKNOWN' OR (unknown_since IS NOT NULL AND unknown_reason IS NOT NULL)),
  CONSTRAINT ck_payout_requests_poll CHECK (poll_attempts >= 0),
  CONSTRAINT ck_payout_requests_submitted CHECK (status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED','REJECTED','CANCELLED') OR submitted_at IS NOT NULL),
  CONSTRAINT ck_payout_requests_completed CHECK (status NOT IN ('COMPLETED','REVERSED') OR completed_at IS NOT NULL),
  CONSTRAINT ck_payout_requests_failed CHECK (status <> 'FAILED' OR failure_code IS NOT NULL),
  CONSTRAINT ck_payout_requests_version CHECK (version >= 1)
);
-- EC-11 single in-flight rule: at most one non-terminal payout per (campaign, currency) (PD-23 default).
CREATE UNIQUE INDEX uq_payout_requests_one_inflight ON app.payout_requests (campaign_id, currency)
  WHERE status IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED','SUBMITTED','PROCESSING','UNKNOWN');
CREATE INDEX ix_payout_requests_campaign ON app.payout_requests (campaign_id, currency, created_at);
CREATE INDEX ix_payout_requests_review_queue ON app.payout_requests (requested_at) WHERE status = 'PENDING_REVIEW';
CREATE INDEX ix_payout_requests_submit_queue ON app.payout_requests (updated_at) WHERE status = 'APPROVED';
CREATE INDEX ix_payout_requests_poll ON app.payout_requests (next_poll_at) WHERE status IN ('SUBMITTED','PROCESSING','UNKNOWN') AND next_poll_at IS NOT NULL;
CREATE INDEX ix_payout_requests_destination ON app.payout_requests (destination_id);

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout', '',                 'PAYOUT_REQUESTED'),   -- Y1 (funds reserved in the same transaction)
  ('payout', 'PAYOUT_REQUESTED', 'PENDING_REVIEW'),     -- Y2
  ('payout', 'PAYOUT_REQUESTED', 'APPROVED'),           -- Y3 (AUTO tier only: guard below)
  ('payout', 'PAYOUT_REQUESTED', 'REJECTED'),           -- Y4
  ('payout', 'PENDING_REVIEW',   'REJECTED'),           -- Y4
  ('payout', 'PAYOUT_REQUESTED', 'CANCELLED'),          -- Y5
  ('payout', 'PENDING_REVIEW',   'CANCELLED'),          -- Y5
  ('payout', 'APPROVED',         'CANCELLED'),          -- Y5
  ('payout', 'PENDING_REVIEW',   'APPROVED'),           -- Y6 (approvals ≠ requester; DUAL distinct)
  ('payout', 'APPROVED',         'PENDING_REVIEW'),     -- Y7 (approvals invalidated: approval_round+1)
  ('payout', 'APPROVED',         'SUBMITTED'),          -- Y8 (PRE_SUBMIT re-check in the same transaction)
  ('payout', 'SUBMITTED',        'PROCESSING'),         -- Y9
  ('payout', 'SUBMITTED',        'COMPLETED'),          -- Y10
  ('payout', 'PROCESSING',       'COMPLETED'),          -- Y10
  ('payout', 'UNKNOWN',          'COMPLETED'),          -- Y10
  ('payout', 'SUBMITTED',        'FAILED'),             -- Y11
  ('payout', 'PROCESSING',       'FAILED'),             -- Y11
  ('payout', 'UNKNOWN',          'FAILED'),             -- Y11 (authoritative final failure / provider evidence only)
  ('payout', 'SUBMITTED',        'UNKNOWN'),            -- Y12
  ('payout', 'PROCESSING',       'UNKNOWN'),            -- Y12
  ('payout', 'UNKNOWN',          'PROCESSING'),         -- Y13
  ('payout', 'COMPLETED',        'REVERSED');           -- Y14
-- Deliberately absent: UNKNOWN -> SUBMITTED (no resubmission, ever), anything out of FAILED / REJECTED /
-- CANCELLED / REVERSED, COMPLETED -> anything but REVERSED.

CREATE FUNCTION app.payout_policy_rank(p text) RETURNS integer LANGUAGE sql IMMUTABLE STRICT AS $$
  SELECT CASE p WHEN 'AUTO' THEN 0 WHEN 'SINGLE' THEN 1 WHEN 'DUAL' THEN 2 END
$$;

CREATE FUNCTION app.payout_requests_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  required  integer;
  approvers integer;
  finance   integer;
  rejects   integer;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    IF (NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.requested_by, NEW.requested_at, NEW.destination_id,
        NEW.destination_version, NEW.destination_hash, NEW.destination_rail, NEW.destination_masked,
        NEW.idempotency_scope, NEW.idempotency_key, NEW.policy_version)
       IS DISTINCT FROM
       (OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.requested_by, OLD.requested_at, OLD.destination_id,
        OLD.destination_version, OLD.destination_hash, OLD.destination_rail, OLD.destination_masked,
        OLD.idempotency_scope, OLD.idempotency_key, OLD.policy_version) THEN
      RAISE EXCEPTION 'payout amount, currency, requester and destination snapshot are immutable (approvals bind to them)'
        USING ERRCODE = 'restrict_violation';
    END IF;
    IF OLD.provider_payout_ref IS NOT NULL AND NEW.provider_payout_ref IS DISTINCT FROM OLD.provider_payout_ref THEN
      RAISE EXCEPTION 'provider_payout_ref is immutable once set' USING ERRCODE = 'restrict_violation';
    END IF;
    IF (NEW.provider_id, NEW.provider_account_id, NEW.capability_id) IS DISTINCT FROM (OLD.provider_id, OLD.provider_account_id, OLD.capability_id)
       AND OLD.status NOT IN ('PAYOUT_REQUESTED','PENDING_REVIEW','APPROVED') THEN
      RAISE EXCEPTION 'payout provider is bound once submitted' USING ERRCODE = 'restrict_violation';
    END IF;
    IF app.payout_policy_rank(NEW.approval_policy) < app.payout_policy_rank(OLD.approval_policy) THEN
      RAISE EXCEPTION 'approval policy may only be raised (AUTO < SINGLE < DUAL)' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.approval_round < OLD.approval_round THEN
      RAISE EXCEPTION 'approval_round never decreases' USING ERRCODE = 'check_violation';
    END IF;
    -- Y7: going back to review invalidates the approvals of the current round.
    IF OLD.status = 'APPROVED' AND NEW.status = 'PENDING_REVIEW' AND NEW.approval_round <= OLD.approval_round THEN
      RAISE EXCEPTION 'APPROVED -> PENDING_REVIEW must start a new approval round' USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.status IS NOT DISTINCT FROM OLD.status THEN
      RETURN NEW;
    END IF;
  END IF;

  IF NEW.status IN ('APPROVED','SUBMITTED') THEN
    IF TG_OP = 'UPDATE' AND OLD.status = 'PAYOUT_REQUESTED' AND NEW.approval_policy <> 'AUTO' THEN
      RAISE EXCEPTION 'only the AUTO tier may skip review (policy %)', NEW.approval_policy USING ERRCODE = 'check_violation';
    END IF;
    required := CASE NEW.approval_policy WHEN 'AUTO' THEN 0 WHEN 'SINGLE' THEN 1 ELSE 2 END;
    SELECT count(DISTINCT a.approver_id) FILTER (WHERE a.decision = 'APPROVE'),
           count(DISTINCT a.approver_id) FILTER (WHERE a.decision = 'APPROVE' AND a.approver_role = 'FINANCE'),
           count(*) FILTER (WHERE a.decision = 'REJECT')
      INTO approvers, finance, rejects
      FROM app.payout_approvals a
     WHERE a.payout_request_id = NEW.id AND a.approval_round = NEW.approval_round AND a.expires_at > now();
    IF rejects > 0 THEN
      RAISE EXCEPTION 'payout % has a REJECT decision in round %', NEW.id, NEW.approval_round USING ERRCODE = 'check_violation';
    END IF;
    -- EC-22: approvals required by the tier, valid (unexpired), current round; DUAL = two distinct FINANCE
    -- approvers (baseline §12 I-20; approver_role CHECK allows FINANCE only).
    IF approvers < required OR (required > 0 AND finance < 1) THEN
      RAISE EXCEPTION 'insufficient approvals for payout % (% of % required, policy %)', NEW.id, approvers, required, NEW.approval_policy
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;

  IF NEW.status = 'SUBMITTED' THEN
    -- Fail closed (CLAUDE.md rule 12): the pre-submission re-check must have run in THIS transaction and
    -- found the payout ELIGIBLE.
    IF NOT EXISTS (SELECT 1 FROM app.payout_eligibility_decisions d
                    WHERE d.payout_request_id = NEW.id AND d.phase = 'PRE_SUBMIT' AND d.outcome = 'ELIGIBLE'
                      AND d.created_at = transaction_timestamp()) THEN
      RAISE EXCEPTION 'payout % cannot be SUBMITTED without an ELIGIBLE PRE_SUBMIT decision in the same transaction', NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_requests_guard BEFORE UPDATE ON app.payout_requests FOR EACH ROW EXECUTE FUNCTION app.payout_requests_guard();
CREATE TRIGGER trg_payout_requests_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout');
CREATE TRIGGER trg_payout_requests_updated_at BEFORE UPDATE ON app.payout_requests FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payout_requests_keep_created_at BEFORE UPDATE ON app.payout_requests FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_payout_requests_no_delete BEFORE DELETE ON app.payout_requests FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_requests_no_truncate BEFORE TRUNCATE ON app.payout_requests FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_approvals — append-only decisions. Approver ≠ requester and ≠ the people who last changed or
-- verified the destination (operational-controls §2, §4.1); one decision per approver per approval round;
-- DUAL distinctness follows from the per-approver uniqueness plus count(DISTINCT approver) in the guard.
-- =====================================================================================================
CREATE TABLE app.payout_approvals (
  id                  uuid        NOT NULL,
  payout_request_id   uuid        NOT NULL,
  approval_round      integer     NOT NULL,
  approver_id         uuid        NOT NULL,
  approver_role       text        NOT NULL,
  decision            text        NOT NULL,
  reason              text        NOT NULL,
  step_up_at          timestamptz NOT NULL,   -- fresh MFA (STEP_UP_MAX_AGE checked by the app)
  conflict_attested   boolean     NOT NULL,   -- approver attests no declared conflict (operational-controls §2)
  decided_at          timestamptz NOT NULL DEFAULT now(),
  expires_at          timestamptz NOT NULL,   -- approval validity window (INTERNAL_RISK limit)
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_approvals PRIMARY KEY (id),
  CONSTRAINT fk_payout_approvals_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_approvals_approver FOREIGN KEY (approver_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_approvals_approver UNIQUE (payout_request_id, approval_round, approver_id),
  CONSTRAINT ck_payout_approvals_role CHECK (approver_role = 'FINANCE'),   -- baseline §12 I-20: COMPLIANCE never approves payouts
  CONSTRAINT ck_payout_approvals_decision CHECK (decision IN ('APPROVE','REJECT')),
  CONSTRAINT ck_payout_approvals_reason CHECK (char_length(btrim(reason)) >= 5),
  CONSTRAINT ck_payout_approvals_attested CHECK (conflict_attested),
  CONSTRAINT ck_payout_approvals_step_up CHECK (step_up_at <= decided_at),
  CONSTRAINT ck_payout_approvals_expiry CHECK (expires_at > decided_at)
);
CREATE INDEX ix_payout_approvals_approver ON app.payout_approvals (approver_id);

CREATE FUNCTION app.payout_approvals_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p app.payout_requests%ROWTYPE;
  d app.payout_destinations%ROWTYPE;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = NEW.payout_request_id FOR UPDATE;
  IF NEW.approver_id = p.requested_by THEN
    RAISE EXCEPTION 'maker-checker: approver cannot be the requester of payout %', p.id USING ERRCODE = 'check_violation';
  END IF;
  SELECT * INTO d FROM app.payout_destinations WHERE id = p.destination_id;
  IF NEW.approver_id = d.last_changed_by OR NEW.approver_id IS NOT DISTINCT FROM d.last_verified_by THEN
    RAISE EXCEPTION 'segregation of duties: approver changed or verified the payout destination' USING ERRCODE = 'check_violation';
  END IF;
  IF p.status <> 'PENDING_REVIEW' THEN
    RAISE EXCEPTION 'payout % is %, approvals are recorded only in PENDING_REVIEW', p.id, p.status USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.approval_round <> p.approval_round THEN
    RAISE EXCEPTION 'approval round % is not the current round % of payout %', NEW.approval_round, p.approval_round, p.id
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_approvals_guard BEFORE INSERT ON app.payout_approvals FOR EACH ROW EXECUTE FUNCTION app.payout_approvals_guard();
CREATE TRIGGER trg_payout_approvals_no_mutation BEFORE UPDATE OR DELETE ON app.payout_approvals FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_approvals_no_truncate BEFORE TRUNCATE ON app.payout_approvals FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_reservations — one per payout; links the ledger journals of the reserved money.
--   RESERVED  : payout:{id}:reserved posted (Y1); submit journal added at Y8 (still RESERVED, in transit)
--   RELEASED  : payout:{id}:released (Y4/Y5, before submit) or payout:{id}:failed (Y11, after submit)
--   CONSUMED  : payout:{id}:completed (Y10). A later reversal (Y14) is recorded in payout_reversals.
-- =====================================================================================================
CREATE TABLE app.payout_reservations (
  id                      uuid        NOT NULL,
  payout_request_id       uuid        NOT NULL,
  campaign_id             uuid        NOT NULL,
  currency                char(3)     NOT NULL,
  amount_minor            bigint      NOT NULL,
  status                  text        NOT NULL,
  ledger_reserve_txn_id   uuid        NOT NULL,
  ledger_submit_txn_id    uuid,
  ledger_release_txn_id   uuid,
  ledger_consume_txn_id   uuid,
  version                 integer     NOT NULL DEFAULT 1,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_reservations PRIMARY KEY (id),
  CONSTRAINT uq_payout_reservations_payout UNIQUE (payout_request_id),
  CONSTRAINT fk_payout_reservations_payout FOREIGN KEY (payout_request_id, campaign_id, currency, amount_minor)
    REFERENCES app.payout_requests (id, campaign_id, currency, amount_minor) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reservations_reserve FOREIGN KEY (ledger_reserve_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reservations_submit FOREIGN KEY (ledger_submit_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reservations_release FOREIGN KEY (ledger_release_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reservations_consume FOREIGN KEY (ledger_consume_txn_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_reservations_reserve_txn UNIQUE (ledger_reserve_txn_id),
  CONSTRAINT ck_payout_reservations_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payout_reservations_status CHECK (status IN ('RESERVED','RELEASED','CONSUMED')),
  CONSTRAINT ck_payout_reservations_released CHECK (status <> 'RELEASED' OR (ledger_release_txn_id IS NOT NULL AND ledger_consume_txn_id IS NULL)),
  CONSTRAINT ck_payout_reservations_consumed CHECK (status <> 'CONSUMED' OR (ledger_consume_txn_id IS NOT NULL AND ledger_submit_txn_id IS NOT NULL AND ledger_release_txn_id IS NULL)),
  CONSTRAINT ck_payout_reservations_reserved CHECK (status <> 'RESERVED' OR (ledger_release_txn_id IS NULL AND ledger_consume_txn_id IS NULL)),
  CONSTRAINT ck_payout_reservations_distinct CHECK (
    ledger_submit_txn_id IS DISTINCT FROM ledger_reserve_txn_id AND ledger_release_txn_id IS DISTINCT FROM ledger_reserve_txn_id
    AND ledger_consume_txn_id IS DISTINCT FROM ledger_reserve_txn_id)
);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout_reservation', '',         'RESERVED'),
  ('payout_reservation', 'RESERVED', 'RELEASED'),
  ('payout_reservation', 'RESERVED', 'CONSUMED');

CREATE FUNCTION app.payout_reservations_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.payout_request_id, NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.ledger_reserve_txn_id)
     IS DISTINCT FROM (OLD.payout_request_id, OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.ledger_reserve_txn_id)
     OR (OLD.ledger_submit_txn_id  IS NOT NULL AND NEW.ledger_submit_txn_id  IS DISTINCT FROM OLD.ledger_submit_txn_id)
     OR (OLD.ledger_release_txn_id IS NOT NULL AND NEW.ledger_release_txn_id IS DISTINCT FROM OLD.ledger_release_txn_id)
     OR (OLD.ledger_consume_txn_id IS NOT NULL AND NEW.ledger_consume_txn_id IS DISTINCT FROM OLD.ledger_consume_txn_id) THEN
    RAISE EXCEPTION 'payout reservation facts and journal links are set once' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_reservations_guard BEFORE UPDATE ON app.payout_reservations FOR EACH ROW EXECUTE FUNCTION app.payout_reservations_guard();
CREATE TRIGGER trg_payout_reservations_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_reservations
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout_reservation');
CREATE TRIGGER trg_payout_reservations_updated_at BEFORE UPDATE ON app.payout_reservations FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payout_reservations_no_delete BEFORE DELETE ON app.payout_reservations FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_reservations_no_truncate BEFORE TRUNCATE ON app.payout_reservations FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_attempts — every provider call. our_reference is ALWAYS payout_requests.id (idempotency ref
-- reused forever). A CREATE_PAYOUT is allowed only while SUBMITTED and only if no earlier CREATE_PAYOUT
-- may have reached the provider (only DEFINITELY_NOT_SENT permits a same-reference retry). This makes
-- "an UNKNOWN payout is never resubmitted" a database rule.
-- =====================================================================================================
CREATE TABLE app.payout_attempts (
  id                          uuid        NOT NULL,
  payout_request_id           uuid        NOT NULL,
  attempt_no                  integer     NOT NULL,
  operation                   text        NOT NULL,
  provider_id                 uuid        NOT NULL,
  our_reference               uuid        NOT NULL,
  correlation_id              text        NOT NULL,
  started_at                  timestamptz NOT NULL,
  completed_at                timestamptz,
  classification              text        NOT NULL DEFAULT 'IN_FLIGHT',
  error_class                 text,
  unknown_reason              text,
  http_status                 smallint,
  provider_status_raw         text,
  provider_reference_returned text,
  latency_ms                  integer,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_attempts PRIMARY KEY (id),
  CONSTRAINT fk_payout_attempts_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_attempts_provider FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_attempts_no UNIQUE (payout_request_id, attempt_no),
  CONSTRAINT ck_payout_attempts_no CHECK (attempt_no >= 1),
  CONSTRAINT ck_payout_attempts_operation CHECK (operation IN ('CREATE_PAYOUT','GET_STATUS','CANCEL')),
  CONSTRAINT ck_payout_attempts_reference CHECK (our_reference = payout_request_id),
  CONSTRAINT ck_payout_attempts_classification CHECK (classification IN ('IN_FLIGHT','ACCEPTED','REJECTED','DEFINITELY_NOT_SENT','OUTCOME_UNKNOWN','NOT_FOUND','AUTH_CONFIG_ERROR','NOT_SUPPORTED','RATE_LIMITED')),
  CONSTRAINT ck_payout_attempts_error_class CHECK (error_class IS NULL OR error_class IN ('DEFINITE_FAILURE','RETRYABLE_BEFORE_SEND','INDETERMINATE','AUTH_CONFIG_ERROR','RATE_LIMITED','NOT_SUPPORTED','INVALID_REQUEST')),
  CONSTRAINT ck_payout_attempts_unknown_reason CHECK (
    (classification = 'OUTCOME_UNKNOWN') = (unknown_reason IS NOT NULL)
    AND (unknown_reason IS NULL OR unknown_reason IN ('TIMEOUT','CONN_RESET_AFTER_SEND','HTTP_5XX','MALFORMED_RESPONSE','SIGNATURE_INVALID_RESPONSE','CRASH_DURING_CREATE'))),
  CONSTRAINT ck_payout_attempts_completed CHECK ((classification = 'IN_FLIGHT') = (completed_at IS NULL)),
  CONSTRAINT ck_payout_attempts_latency CHECK (latency_ms IS NULL OR latency_ms >= 0)
);
CREATE INDEX ix_payout_attempts_in_flight ON app.payout_attempts (started_at) WHERE classification = 'IN_FLIGHT';

CREATE FUNCTION app.payout_attempts_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p app.payout_requests%ROWTYPE;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = NEW.payout_request_id FOR UPDATE;
  IF NEW.provider_id <> p.provider_id THEN
    RAISE EXCEPTION 'payout attempt provider differs from the bound provider' USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.operation = 'CREATE_PAYOUT' THEN
    IF p.status <> 'SUBMITTED' THEN
      RAISE EXCEPTION 'payout resubmission forbidden: CREATE_PAYOUT only while SUBMITTED (payout % is %)', p.id, p.status
        USING ERRCODE = 'check_violation';
    END IF;
    IF EXISTS (SELECT 1 FROM app.payout_attempts a
                WHERE a.payout_request_id = p.id AND a.operation = 'CREATE_PAYOUT'
                  AND a.classification <> 'DEFINITELY_NOT_SENT') THEN
      RAISE EXCEPTION 'payout resubmission forbidden: an earlier CREATE_PAYOUT for % may have reached the provider', p.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_attempts_guard BEFORE INSERT ON app.payout_attempts FOR EACH ROW EXECUTE FUNCTION app.payout_attempts_guard();
CREATE TRIGGER trg_payout_attempts_complete_once BEFORE UPDATE ON app.payout_attempts
  FOR EACH ROW EXECUTE FUNCTION app.attempt_complete_once('completed_at','classification','error_class','unknown_reason','http_status','provider_status_raw','provider_reference_returned','latency_ms');
CREATE TRIGGER trg_payout_attempts_no_delete BEFORE DELETE ON app.payout_attempts FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_attempts_no_truncate BEFORE TRUNCATE ON app.payout_attempts FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_provider_references — every provider reference seen for a payout.
-- =====================================================================================================
CREATE TABLE app.payout_provider_references (
  id                 uuid        NOT NULL,
  provider_id        uuid        NOT NULL,
  ref_kind           text        NOT NULL,
  reference          text        NOT NULL,
  payout_request_id  uuid        NOT NULL,
  source             text        NOT NULL,
  recorded_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_provider_references PRIMARY KEY (id),
  CONSTRAINT fk_payout_provider_references_provider FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_provider_references_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_provider_references UNIQUE (provider_id, ref_kind, reference),
  CONSTRAINT ck_payout_provider_references_kind CHECK (ref_kind IN ('PAYOUT','TRANSFER','BATCH','REVERSAL','STATEMENT_LINE')),
  CONSTRAINT ck_payout_provider_references_reference CHECK (char_length(reference) BETWEEN 1 AND 255),
  CONSTRAINT ck_payout_provider_references_source CHECK (source IN ('SYNC','WEBHOOK','POLL','RECON'))
);
CREATE INDEX ix_payout_provider_references_payout ON app.payout_provider_references (payout_request_id);
CREATE TRIGGER trg_payout_provider_references_no_mutation BEFORE UPDATE OR DELETE ON app.payout_provider_references FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_provider_references_no_truncate BEFORE TRUNCATE ON app.payout_provider_references FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_eligibility_decisions — append-only evidence of every evaluation (REQUEST, PRE_SUBMIT, RECHECK),
-- including refused requests (payout_request_id NULL). results = all 22 checks, each exactly once.
-- =====================================================================================================
CREATE FUNCTION app.eligibility_results_valid(results jsonb, outcome text, phase text) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
  n_ok     integer;
  n_fail   integer;
  n_review integer;
  ec22     text;
BEGIN
  IF results IS NULL OR jsonb_typeof(results) <> 'array' OR jsonb_array_length(results) <> 22 THEN
    RETURN false;
  END IF;
  IF EXISTS (SELECT 1 FROM jsonb_array_elements(results) e WHERE jsonb_typeof(e) <> 'object') THEN
    RETURN false;
  END IF;
  SELECT count(DISTINCT e->>'check_id'),
         count(*) FILTER (WHERE e->>'result' = 'FAIL'),
         count(*) FILTER (WHERE e->>'result' IN ('REVIEW','DEFER')),
         max(e->>'result') FILTER (WHERE e->>'check_id' = 'EC-22')
    INTO n_ok, n_fail, n_review, ec22
    FROM jsonb_array_elements(results) e
   WHERE e->>'check_id' ~ '^EC-(0[1-9]|1[0-9]|2[0-2])$'
     AND e->>'result' IN ('PASS','FAIL','REVIEW','DEFER','NOT_APPLICABLE');
  IF n_ok <> 22 THEN
    RETURN false;                                -- every check exactly once, valid result values
  END IF;
  IF phase = 'PRE_SUBMIT' AND ec22 = 'NOT_APPLICABLE' THEN
    RETURN false;                                -- approvals are always checked before submission
  END IF;
  RETURN CASE
    WHEN n_fail > 0   THEN outcome = 'REJECTED'
    WHEN n_review > 0 THEN outcome IN ('REVIEW','DEFERRED')
    ELSE outcome = 'ELIGIBLE' END;               -- fail closed: ELIGIBLE only if every check passed / N/A
END $$;

CREATE TABLE app.payout_eligibility_decisions (
  id                 uuid        NOT NULL,
  payout_request_id  uuid,                     -- NULL for refused requests (no payout row is created)
  campaign_id        uuid        NOT NULL,
  currency           char(3)     NOT NULL,
  amount_minor       bigint      NOT NULL,
  requested_by       uuid        NOT NULL,
  phase              text        NOT NULL,
  evaluated_at       timestamptz NOT NULL,
  engine_version     text        NOT NULL,
  policy_version     text        NOT NULL,
  inputs             jsonb       NOT NULL,     -- references and values used (no documents, no raw PII)
  results            jsonb       NOT NULL,     -- [{check_id, result, detail_code}] x 22
  outcome            text        NOT NULL,
  approval_tier      text,
  overrides          jsonb       NOT NULL DEFAULT '[]'::jsonb,
  request_id         text,
  correlation_id     text,
  record_hash        bytea       NOT NULL,     -- SHA-256 over the canonical record
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_eligibility_decisions PRIMARY KEY (id),
  -- Deferred: the REQUEST decision is written before the payout row in the same transaction.
  CONSTRAINT fk_payout_eligibility_decisions_payout FOREIGN KEY (payout_request_id, campaign_id, currency, amount_minor)
    REFERENCES app.payout_requests (id, campaign_id, currency, amount_minor) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payout_eligibility_decisions_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_eligibility_decisions_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_eligibility_decisions_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_eligibility_decisions_amount CHECK (amount_minor > 0),
  CONSTRAINT ck_payout_eligibility_decisions_phase CHECK (phase IN ('REQUEST','PRE_SUBMIT','RECHECK')),
  CONSTRAINT ck_payout_eligibility_decisions_outcome CHECK (outcome IN ('ELIGIBLE','REVIEW','DEFERRED','REJECTED')),
  CONSTRAINT ck_payout_eligibility_decisions_tier CHECK (approval_tier IS NULL OR approval_tier IN ('AUTO','SINGLE','DUAL')),
  CONSTRAINT ck_payout_eligibility_decisions_refused CHECK (payout_request_id IS NOT NULL OR (phase = 'REQUEST' AND outcome = 'REJECTED')),
  CONSTRAINT ck_payout_eligibility_decisions_results CHECK (app.eligibility_results_valid(results, outcome, phase)),
  CONSTRAINT ck_payout_eligibility_decisions_inputs CHECK (jsonb_typeof(inputs) = 'object'),
  CONSTRAINT ck_payout_eligibility_decisions_overrides CHECK (jsonb_typeof(overrides) = 'array'),
  CONSTRAINT ck_payout_eligibility_decisions_hash CHECK (octet_length(record_hash) = 32)
);
CREATE INDEX ix_payout_eligibility_decisions_payout ON app.payout_eligibility_decisions (payout_request_id, created_at) WHERE payout_request_id IS NOT NULL;
CREATE INDEX ix_payout_eligibility_decisions_campaign ON app.payout_eligibility_decisions (campaign_id, evaluated_at);
CREATE TRIGGER trg_payout_eligibility_decisions_no_mutation BEFORE UPDATE OR DELETE ON app.payout_eligibility_decisions FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_eligibility_decisions_no_truncate BEFORE TRUNCATE ON app.payout_eligibility_decisions FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_events — append-only status history (applied and not-applied provider observations).
-- =====================================================================================================
CREATE TABLE app.payout_events (
  id                       uuid        NOT NULL,
  payout_request_id        uuid        NOT NULL,
  from_status              text,                 -- NULL = initial
  to_status                text        NOT NULL,
  applied                  boolean     NOT NULL,
  not_applied_reason       text,
  transition_code          text,                 -- 'Y1'..'Y14'
  source                   text        NOT NULL,
  provider_event_id        text,
  inbox_event_id           uuid,
  payout_attempt_id        uuid,
  eligibility_decision_id  uuid,
  raw_status               text,
  actor_type               text        NOT NULL,
  actor_id                 uuid,
  reason                   text,
  evidence_record_id       uuid,
  ledger_transaction_ids   uuid[]      NOT NULL DEFAULT '{}',
  correlation_id           text,
  occurred_at              timestamptz,
  recorded_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_events PRIMARY KEY (id),
  CONSTRAINT fk_payout_events_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_payout_events_inbox FOREIGN KEY (inbox_event_id) REFERENCES app.provider_webhook_inbox (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_events_attempt FOREIGN KEY (payout_attempt_id) REFERENCES app.payout_attempts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_events_decision FOREIGN KEY (eligibility_decision_id) REFERENCES app.payout_eligibility_decisions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_events_actor FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_events_evidence FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_events_source CHECK (source IN ('OWNER','STAFF','SYSTEM','SYNC','WEBHOOK','POLL','RECON')),
  CONSTRAINT ck_payout_events_actor_type CHECK (actor_type IN ('SYSTEM','OWNER','STAFF','PROVIDER')),
  CONSTRAINT ck_payout_events_not_applied CHECK (
    (applied AND not_applied_reason IS NULL)
    OR (NOT applied AND not_applied_reason IS NOT NULL AND not_applied_reason IN ('LOWER_RANK','NOT_AN_EDGE','ANOMALY','DUPLICATE','PARKED'))),
  CONSTRAINT ck_payout_events_transition_code CHECK (transition_code IS NULL OR transition_code ~ '^Y([1-9]|1[0-4])$'),
  CONSTRAINT ck_payout_events_staff CHECK (source <> 'STAFF' OR (actor_type = 'STAFF' AND actor_id IS NOT NULL AND reason IS NOT NULL)),
  CONSTRAINT ck_payout_events_owner CHECK (source <> 'OWNER' OR (actor_type = 'OWNER' AND actor_id IS NOT NULL)),
  -- Completion only from authoritative provider state (payout-lifecycle Y10, §5.1).
  CONSTRAINT ck_payout_events_completed_source CHECK (NOT applied OR to_status NOT IN ('COMPLETED','REVERSED','PROCESSING') OR source IN ('SYNC','WEBHOOK','POLL','RECON')),
  -- UNKNOWN only from our own indeterminate call or query (Y12).
  CONSTRAINT ck_payout_events_unknown_system CHECK (NOT applied OR to_status <> 'UNKNOWN' OR source = 'SYSTEM'),
  -- After submission the owner's browser drives nothing (payout-lifecycle §3 "Not allowed").
  CONSTRAINT ck_payout_events_owner_scope CHECK (NOT applied OR source <> 'OWNER' OR to_status IN ('PAYOUT_REQUESTED','CANCELLED')),
  -- A staff-declared FAILED needs written provider evidence (payout-lifecycle §5.3).
  -- Baseline §12 I-21: FAILED only from SYNC/WEBHOOK/POLL/RECON/STAFF (with evidence) — never SYSTEM.
  CONSTRAINT ck_payout_events_failed_source CHECK (NOT applied OR to_status <> 'FAILED' OR source IN ('SYNC','WEBHOOK','POLL','RECON','STAFF')),
  CONSTRAINT ck_payout_events_staff_failed_evidence CHECK (NOT applied OR to_status <> 'FAILED' OR source <> 'STAFF' OR evidence_record_id IS NOT NULL)
);
CREATE INDEX ix_payout_events_payout ON app.payout_events (payout_request_id, recorded_at);
CREATE TRIGGER trg_payout_events_no_mutation BEFORE UPDATE OR DELETE ON app.payout_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_events_no_truncate BEFORE TRUNCATE ON app.payout_events FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_failures — append-only detail of each failure observation (final or not).
-- =====================================================================================================
CREATE TABLE app.payout_failures (
  id                   uuid        NOT NULL,
  payout_request_id    uuid        NOT NULL,
  payout_attempt_id    uuid,
  failure_class        text        NOT NULL,
  provider_code        text,                  -- verbatim
  raw_status           text,
  is_final             boolean     NOT NULL,  -- "did not and will not happen" (payout-lifecycle §5.3)
  destination_flagged  boolean     NOT NULL DEFAULT false,
  evidence_record_id   uuid,
  occurred_at          timestamptz NOT NULL,
  recorded_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_failures PRIMARY KEY (id),
  CONSTRAINT fk_payout_failures_payout FOREIGN KEY (payout_request_id) REFERENCES app.payout_requests (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_failures_attempt FOREIGN KEY (payout_attempt_id) REFERENCES app.payout_attempts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_failures_evidence FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id) ON DELETE RESTRICT,
  CONSTRAINT ck_payout_failures_class CHECK (failure_class IN ('PROVIDER_DEFINITIVE','INVALID_DESTINATION','LIMIT_EXCEEDED','INSUFFICIENT_POOL','NOT_FOUND_FINAL','STAFF_CONFIRMED_WITH_PROVIDER_EVIDENCE','LATE_COMPLETION_AFTER_FAILED','OTHER')),
  CONSTRAINT ck_payout_failures_evidence CHECK (failure_class <> 'STAFF_CONFIRMED_WITH_PROVIDER_EVIDENCE' OR evidence_record_id IS NOT NULL)
);
CREATE INDEX ix_payout_failures_payout ON app.payout_failures (payout_request_id);
CREATE TRIGGER trg_payout_failures_no_mutation BEFORE UPDATE OR DELETE ON app.payout_failures FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_failures_no_truncate BEFORE TRUNCATE ON app.payout_failures FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- payout_reversals — completed payout returned by the rail (Y14). One per payout; partial returns record
-- the returned amount (payout-lifecycle §6).
-- =====================================================================================================
CREATE TABLE app.payout_reversals (
  id                     uuid        NOT NULL,
  payout_request_id      uuid        NOT NULL,
  currency               char(3)     NOT NULL,
  reversed_amount_minor  bigint      NOT NULL,
  provider_id            uuid        NOT NULL,
  provider_reversal_ref  text        NOT NULL,
  reason_code            text        NOT NULL,
  ledger_transaction_id  uuid        NOT NULL,   -- payout:{id}:reversed
  inbox_event_id         uuid,
  occurred_at            timestamptz NOT NULL,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_reversals PRIMARY KEY (id),
  CONSTRAINT uq_payout_reversals_payout UNIQUE (payout_request_id),
  CONSTRAINT fk_payout_reversals_payout FOREIGN KEY (payout_request_id, currency) REFERENCES app.payout_requests (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reversals_provider FOREIGN KEY (provider_id) REFERENCES app.payment_providers (id) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reversals_ledger FOREIGN KEY (ledger_transaction_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_payout_reversals_inbox FOREIGN KEY (inbox_event_id) REFERENCES app.provider_webhook_inbox (id) ON DELETE RESTRICT,
  CONSTRAINT uq_payout_reversals_provider_ref UNIQUE (provider_id, provider_reversal_ref),
  CONSTRAINT ck_payout_reversals_amount CHECK (reversed_amount_minor > 0),
  CONSTRAINT ck_payout_reversals_reason CHECK (reason_code IN ('DESTINATION_CLOSED','ACCOUNT_MISMATCH','BANK_RETURN','PROVIDER_RECALL','OTHER'))
);
CREATE FUNCTION app.payout_reversals_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p app.payout_requests%ROWTYPE;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = NEW.payout_request_id FOR UPDATE;
  IF p.status NOT IN ('COMPLETED','REVERSED') THEN
    RAISE EXCEPTION 'only a COMPLETED payout can be reversed (payout % is %)', p.id, p.status USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.reversed_amount_minor > p.amount_minor OR NEW.provider_id <> p.provider_id THEN
    RAISE EXCEPTION 'reversal exceeds the payout amount or names another provider' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_reversals_guard BEFORE INSERT ON app.payout_reversals FOR EACH ROW EXECUTE FUNCTION app.payout_reversals_guard();
CREATE TRIGGER trg_payout_reversals_no_mutation BEFORE UPDATE OR DELETE ON app.payout_reversals FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_payout_reversals_no_truncate BEFORE TRUNCATE ON app.payout_reversals FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- =====================================================================================================
-- Deferred payout consistency (checked at COMMIT for every payout insert / status change):
--   * an applied payout_events row exists for the transition (same transaction);
--   * a REQUEST-phase eligibility decision that did not REJECT exists (Y1 needs request-time checks);
--   * exactly the right reservation state for the payout state (money location, payout-lifecycle §2.1).
-- =====================================================================================================
CREATE FUNCTION app.payout_requests_check_consistency() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p        app.payout_requests%ROWTYPE;
  r        app.payout_reservations%ROWTYPE;
  expected text;
BEGIN
  SELECT * INTO p FROM app.payout_requests WHERE id = NEW.id;
  IF NOT EXISTS (SELECT 1 FROM app.payout_eligibility_decisions d
                  WHERE d.payout_request_id = p.id AND d.phase = 'REQUEST' AND d.outcome <> 'REJECTED') THEN
    RAISE EXCEPTION 'payout % has no non-rejecting REQUEST eligibility decision', p.id USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  SELECT * INTO r FROM app.payout_reservations WHERE payout_request_id = p.id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'payout % has no reservation (Y1 payout:{id}:reserved)', p.id USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  expected := CASE WHEN p.status IN ('FAILED','REJECTED','CANCELLED') THEN 'RELEASED'
                   WHEN p.status IN ('COMPLETED','REVERSED') THEN 'CONSUMED'
                   ELSE 'RESERVED' END;
  IF r.status <> expected THEN
    RAISE EXCEPTION 'payout % is % but its reservation is % (expected %)', p.id, p.status, r.status, expected
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF p.status IN ('SUBMITTED','PROCESSING','UNKNOWN','COMPLETED','REVERSED','FAILED') AND r.ledger_submit_txn_id IS NULL THEN
    RAISE EXCEPTION 'payout % is % without its payout:{id}:submitted journal', p.id, p.status USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_payout_requests_check_consistency AFTER INSERT OR UPDATE OF status ON app.payout_requests
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.payout_requests_check_consistency();
CREATE CONSTRAINT TRIGGER trg_payout_requests_require_event AFTER INSERT OR UPDATE OF status ON app.payout_requests
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_status_event('payout_events', 'payout_request_id');

-- =====================================================================================================
-- recovery_cases — shortfalls FundZim funded (refund C2, chargeback after payout, late payout completion)
-- and seeks to recover. Source is referenced by (source_type, source_id) without FK: payments and payouts
-- never import each other; the case is opened from an outbox event. Machine 'recovery_case'.
-- Enforceability: LR-080; set-off: LR-082.
-- =====================================================================================================
CREATE TABLE app.recovery_cases (
  id                            uuid        NOT NULL,
  source_type                   text        NOT NULL,
  source_id                     uuid        NOT NULL,
  campaign_id                   uuid        NOT NULL,
  subject_type                  text        NOT NULL,
  subject_id                    uuid        NOT NULL,
  currency                      char(3)     NOT NULL,
  amount_minor                  bigint      NOT NULL,
  recovered_minor               bigint      NOT NULL DEFAULT 0,
  written_off_minor             bigint      NOT NULL DEFAULT 0,
  status                        text        NOT NULL,
  recovery_hold_id              uuid,                 -- RECOVERY_HOLD placed through risk
  compliance_case_id            uuid,                 -- by id only
  write_off_requested_by        uuid,
  write_off_approved_by         uuid,
  write_off_approver_role       text,                 -- I-9: FINANCE (below threshold) | BUSINESS_APPROVER (above)
  write_off_threshold_limit_ref text,                 -- risk.limits id@version of the write-off threshold applied
  write_off_amount_minor        bigint,               -- amount proposed for write-off
  closed_at                     timestamptz,
  version                       integer     NOT NULL DEFAULT 1,
  created_at                    timestamptz NOT NULL DEFAULT now(),
  updated_at                    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recovery_cases PRIMARY KEY (id),
  CONSTRAINT fk_recovery_cases_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id) ON DELETE RESTRICT,
  CONSTRAINT fk_recovery_cases_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_recovery_cases_hold FOREIGN KEY (recovery_hold_id) REFERENCES risk.holds (id) ON DELETE RESTRICT,
  CONSTRAINT fk_recovery_cases_wo_requested_by FOREIGN KEY (write_off_requested_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_recovery_cases_wo_approved_by FOREIGN KEY (write_off_approved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_recovery_cases_source UNIQUE (source_type, source_id),
  CONSTRAINT ck_recovery_cases_source_type CHECK (source_type IN ('REFUND_TRANSACTION','PAYMENT_DISPUTE','CHARGEBACK','PAYOUT_LATE_COMPLETION','PAYOUT_REVERSAL')),
  CONSTRAINT ck_recovery_cases_subject_type CHECK (subject_type IN ('USER','ORGANISATION','BENEFICIARY')),
  CONSTRAINT ck_recovery_cases_amounts CHECK (amount_minor > 0 AND recovered_minor >= 0 AND written_off_minor >= 0
                                              AND recovered_minor + written_off_minor <= amount_minor),
  CONSTRAINT ck_recovery_cases_status CHECK (status IN ('OPEN','PARTIALLY_RECOVERED','RECOVERED','WRITE_OFF_PENDING','WRITTEN_OFF')),
  CONSTRAINT ck_recovery_cases_partial CHECK (status <> 'PARTIALLY_RECOVERED' OR (recovered_minor > 0 AND recovered_minor < amount_minor)),
  CONSTRAINT ck_recovery_cases_recovered CHECK (status <> 'RECOVERED' OR recovered_minor = amount_minor),
  -- Write-off maker-checker (baseline §12 I-9): WRITE_OFF_PENDING is the pending object. Maker FINANCE;
  -- checker a second FINANCE below the threshold, BUSINESS_APPROVER above it (threshold = limit record; the
  -- role/threshold match is checked by the service, the limit ref and role are recorded here).
  CONSTRAINT ck_recovery_cases_write_off_pending CHECK (status <> 'WRITE_OFF_PENDING'
    OR (write_off_requested_by IS NOT NULL AND write_off_amount_minor > 0 AND write_off_threshold_limit_ref IS NOT NULL)),
  CONSTRAINT ck_recovery_cases_write_off_role CHECK (write_off_approver_role IS NULL OR write_off_approver_role IN ('FINANCE','BUSINESS_APPROVER')),
  CONSTRAINT ck_recovery_cases_written_off CHECK (status <> 'WRITTEN_OFF' OR (written_off_minor > 0
    AND write_off_approved_by IS NOT NULL AND write_off_approver_role IS NOT NULL)),
  CONSTRAINT ck_recovery_cases_write_off_amount CHECK (write_off_amount_minor IS NULL OR write_off_amount_minor <= amount_minor),
  CONSTRAINT ck_recovery_cases_write_off_sod CHECK (write_off_approved_by IS NULL OR write_off_approved_by <> write_off_requested_by),
  CONSTRAINT ck_recovery_cases_closed CHECK ((status IN ('RECOVERED','WRITTEN_OFF')) = (closed_at IS NOT NULL))
);
CREATE INDEX ix_recovery_cases_campaign ON app.recovery_cases (campaign_id, currency) WHERE status NOT IN ('RECOVERED','WRITTEN_OFF');

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('recovery_case', '',                    'OPEN'),
  ('recovery_case', 'OPEN',                'PARTIALLY_RECOVERED'),
  ('recovery_case', 'OPEN',                'RECOVERED'),           -- incl. dispute won (inverse journal)
  ('recovery_case', 'OPEN',                'WRITE_OFF_PENDING'),
  ('recovery_case', 'PARTIALLY_RECOVERED', 'RECOVERED'),
  ('recovery_case', 'PARTIALLY_RECOVERED', 'WRITE_OFF_PENDING'),
  ('recovery_case', 'WRITE_OFF_PENDING',   'WRITTEN_OFF'),
  ('recovery_case', 'WRITE_OFF_PENDING',   'OPEN'),                -- write-off rejected / expired
  ('recovery_case', 'WRITE_OFF_PENDING',   'PARTIALLY_RECOVERED'),
  ('recovery_case', 'WRITE_OFF_PENDING',   'RECOVERED');

CREATE FUNCTION app.recovery_cases_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.source_type, NEW.source_id, NEW.campaign_id, NEW.currency, NEW.amount_minor, NEW.subject_type, NEW.subject_id)
     IS DISTINCT FROM (OLD.source_type, OLD.source_id, OLD.campaign_id, OLD.currency, OLD.amount_minor, OLD.subject_type, OLD.subject_id)
     OR NEW.recovered_minor < OLD.recovered_minor OR NEW.written_off_minor < OLD.written_off_minor THEN
    RAISE EXCEPTION 'recovery case facts are immutable and recovered/written-off amounts never decrease'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_recovery_cases_immutable BEFORE UPDATE ON app.recovery_cases FOR EACH ROW EXECUTE FUNCTION app.recovery_cases_immutable();
CREATE TRIGGER trg_recovery_cases_guard_status BEFORE INSERT OR UPDATE OF status ON app.recovery_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('recovery_case');
CREATE TRIGGER trg_recovery_cases_updated_at BEFORE UPDATE ON app.recovery_cases FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_recovery_cases_keep_created_at BEFORE UPDATE ON app.recovery_cases FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_recovery_cases_no_delete BEFORE DELETE ON app.recovery_cases FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.recovery_case_events (
  id                     uuid        NOT NULL,
  recovery_case_id       uuid        NOT NULL,
  from_status            text,
  to_status              text        NOT NULL,
  applied                boolean     NOT NULL DEFAULT true,
  event_kind             text        NOT NULL,
  currency               char(3)     NOT NULL,
  amount_minor           bigint,                -- recovered / set-off / written-off amount of this step
  recovery_route         text,
  ledger_transaction_id  uuid,                  -- recovery:{case}:{n} / *:written_off
  actor_type             text        NOT NULL,
  actor_id               uuid,
  reason                 text,
  evidence_record_id     uuid,
  recorded_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recovery_case_events PRIMARY KEY (id),
  CONSTRAINT fk_recovery_case_events_case FOREIGN KEY (recovery_case_id) REFERENCES app.recovery_cases (id) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED,
  CONSTRAINT fk_recovery_case_events_currency FOREIGN KEY (currency) REFERENCES app.currencies (code) ON DELETE RESTRICT,
  CONSTRAINT fk_recovery_case_events_ledger FOREIGN KEY (ledger_transaction_id, currency) REFERENCES ledger.ledger_transactions (id, currency) ON DELETE RESTRICT,
  CONSTRAINT fk_recovery_case_events_actor FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_recovery_case_events_evidence FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id) ON DELETE RESTRICT,
  CONSTRAINT ck_recovery_case_events_kind CHECK (event_kind IN ('OPENED','RECOVERY_RECEIVED','SET_OFF','DISPUTE_WON','WRITE_OFF_REQUESTED','WRITE_OFF_APPROVED','WRITE_OFF_REJECTED','NOTE')),
  CONSTRAINT ck_recovery_case_events_amount CHECK (amount_minor IS NULL OR amount_minor > 0),
  CONSTRAINT ck_recovery_case_events_money_kinds CHECK (event_kind NOT IN ('RECOVERY_RECEIVED','SET_OFF','WRITE_OFF_APPROVED') OR (amount_minor IS NOT NULL AND ledger_transaction_id IS NOT NULL)),
  CONSTRAINT ck_recovery_case_events_route CHECK (recovery_route IS NULL OR recovery_route IN ('REPAYMENT','SET_OFF_LATER_DONATIONS','DISPUTE_REVERSAL')),
  CONSTRAINT ck_recovery_case_events_actor_type CHECK (actor_type IN ('SYSTEM','STAFF')),
  CONSTRAINT ck_recovery_case_events_staff CHECK (actor_type <> 'STAFF' OR (actor_id IS NOT NULL AND reason IS NOT NULL))
);
CREATE INDEX ix_recovery_case_events_case ON app.recovery_case_events (recovery_case_id, recorded_at);
CREATE TRIGGER trg_recovery_case_events_no_mutation BEFORE UPDATE OR DELETE ON app.recovery_case_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_recovery_case_events_no_truncate BEFORE TRUNCATE ON app.recovery_case_events FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE CONSTRAINT TRIGGER trg_recovery_cases_require_event AFTER INSERT OR UPDATE OF status ON app.recovery_cases
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.require_status_event('recovery_case_events', 'recovery_case_id');
