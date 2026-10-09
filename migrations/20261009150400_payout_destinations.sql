-- FundZim migration: payout destinations (payouts module; Stage 2 draft 0016 subset adapted to ADR-034 §4).
-- Schema: app. Only destination records and their verification are built in Stage 5: NO payout requests,
-- attempts, reservations or provider calls exist, and nothing here can move money.
--
-- Three separate outcomes are append-only checks, never inferred from each other (brief §12):
--   FORMAT     — the identifier is syntactically valid for the rail (says nothing about ownership)
--   OWNERSHIP  — evidence that the owner controls the account (provider confirmation, bank letter, statement
--                reviewed by staff); PROVIDER_CONFIRMATION_REQUIRED where a provider check is needed but no
--                provider is integrated
--   COMPLIANCE — staff approval of the destination for this owner
-- VERIFIED requires an OWNERSHIP PASS and a COMPLIANCE PASS for the CURRENT destination version (DB trigger).
-- A development mock result is tagged non_production and can never be an ownership PASS (CHECK).
-- The account identifier is C3: AES-256-GCM ciphertext + HMAC blind index; only the last characters are shown.
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('payout_destination', '',                     'UNVERIFIED'),
  ('payout_destination', 'UNVERIFIED',           'PENDING_VERIFICATION'),
  ('payout_destination', 'UNVERIFIED',           'RETIRED'),
  ('payout_destination', 'PENDING_VERIFICATION', 'VERIFIED'),
  ('payout_destination', 'PENDING_VERIFICATION', 'REJECTED'),
  ('payout_destination', 'PENDING_VERIFICATION', 'UNVERIFIED'),      -- details changed while pending
  ('payout_destination', 'PENDING_VERIFICATION', 'RETIRED'),
  ('payout_destination', 'VERIFIED',             'UNVERIFIED'),       -- details changed: new version, verification resets
  ('payout_destination', 'VERIFIED',             'SUSPENDED'),
  ('payout_destination', 'VERIFIED',             'EXPIRED'),
  ('payout_destination', 'VERIFIED',             'RETIRED'),
  ('payout_destination', 'SUSPENDED',            'VERIFIED'),
  ('payout_destination', 'SUSPENDED',            'PENDING_VERIFICATION'),
  ('payout_destination', 'SUSPENDED',            'RETIRED'),
  ('payout_destination', 'REJECTED',             'PENDING_VERIFICATION'),
  ('payout_destination', 'REJECTED',             'UNVERIFIED'),
  ('payout_destination', 'REJECTED',             'RETIRED'),
  ('payout_destination', 'EXPIRED',              'PENDING_VERIFICATION'),
  ('payout_destination', 'EXPIRED',              'RETIRED');

CREATE TABLE app.payout_destinations (
  id                        uuid        NOT NULL,
  owner_user_id             uuid,
  owner_organisation_id     uuid,
  owner_ref                 uuid GENERATED ALWAYS AS (COALESCE(owner_user_id, owner_organisation_id)) STORED,
  created_by                uuid        NOT NULL,
  payee_type                text        NOT NULL,
  beneficiary_id            uuid,
  category                  text        NOT NULL,
  rail                      text        NOT NULL,
  currency                  char(3)     NOT NULL,
  holder_name               text        NOT NULL,              -- C2; used for name match by reviewers
  bank_code                 text,
  account_ciphertext        bytea       NOT NULL,              -- C3
  account_key_id            text        NOT NULL,
  account_bidx              bytea       NOT NULL,              -- duplicate / reuse detection
  masked_suffix             text        NOT NULL,
  status                    text        NOT NULL,
  ownership_status          text        NOT NULL DEFAULT 'NOT_STARTED',
  compliance_status         text        NOT NULL DEFAULT 'PENDING',
  verification_method       text,
  assigned_to               uuid,
  assigned_at               timestamptz,
  requested_at              timestamptz,
  verified_at               timestamptz,
  verified_by               uuid,
  expires_at                timestamptz,
  cooling_off_until         timestamptz NOT NULL,              -- restarts on create and on every change (EC-06)
  last_changed_by           uuid        NOT NULL,
  policy_version            text        NOT NULL,
  details_version           integer     NOT NULL DEFAULT 1,      -- increments only when account data changes; checks bind to it
  version                   integer     NOT NULL DEFAULT 1,      -- increments on every change (events bind to it)
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_destinations PRIMARY KEY (id),
  CONSTRAINT fk_payout_destinations_owner_user_id FOREIGN KEY (owner_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_payout_destinations_owner_organisation_id FOREIGN KEY (owner_organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_payout_destinations_created_by FOREIGN KEY (created_by) REFERENCES app.users (id),
  CONSTRAINT fk_payout_destinations_beneficiary_id FOREIGN KEY (beneficiary_id) REFERENCES app.beneficiaries (id),
  CONSTRAINT fk_payout_destinations_currency FOREIGN KEY (currency) REFERENCES app.currencies (code),
  CONSTRAINT fk_payout_destinations_assigned_to FOREIGN KEY (assigned_to) REFERENCES app.users (id),
  CONSTRAINT fk_payout_destinations_verified_by FOREIGN KEY (verified_by) REFERENCES app.users (id),
  CONSTRAINT fk_payout_destinations_last_changed_by FOREIGN KEY (last_changed_by) REFERENCES app.users (id),
  CONSTRAINT ck_payout_destinations_owner CHECK (num_nonnulls(owner_user_id, owner_organisation_id) = 1),
  CONSTRAINT ck_payout_destinations_payee CHECK ((payee_type = 'OWNER' AND beneficiary_id IS NULL)
                                                 OR (payee_type = 'BENEFICIARY' AND beneficiary_id IS NOT NULL)),
  CONSTRAINT ck_payout_destinations_category CHECK (category IN ('BANK_ACCOUNT','MOBILE_MONEY_WALLET','OTHER_APPROVED_RAIL')),
  CONSTRAINT ck_payout_destinations_rail CHECK (rail IN ('ECOCASH','ONEMONEY','INNBUCKS','OMARI','ZIMSWITCH','BANK_TRANSFER')),
  CONSTRAINT ck_payout_destinations_rail_category CHECK (
       (category = 'MOBILE_MONEY_WALLET' AND rail IN ('ECOCASH','ONEMONEY','INNBUCKS','OMARI'))
    OR (category = 'BANK_ACCOUNT' AND rail IN ('ZIMSWITCH','BANK_TRANSFER'))
    OR (category = 'OTHER_APPROVED_RAIL')),
  CONSTRAINT ck_payout_destinations_bank_code CHECK (category <> 'BANK_ACCOUNT' OR bank_code IS NOT NULL),
  CONSTRAINT ck_payout_destinations_ct CHECK (octet_length(account_ciphertext) >= 28),
  CONSTRAINT ck_payout_destinations_bidx CHECK (octet_length(account_bidx) = 32),
  CONSTRAINT ck_payout_destinations_masked CHECK (masked_suffix ~ '^[0-9A-Za-z]{2,4}$'),
  CONSTRAINT ck_payout_destinations_holder CHECK (length(holder_name) BETWEEN 2 AND 140),
  CONSTRAINT ck_payout_destinations_status CHECK (status IN ('UNVERIFIED','PENDING_VERIFICATION','VERIFIED','REJECTED','SUSPENDED',
                                                             'EXPIRED','RETIRED')),
  CONSTRAINT ck_payout_destinations_ownership CHECK (ownership_status IN ('NOT_STARTED','PENDING','PROVIDER_CONFIRMATION_REQUIRED',
                                                                          'CONFIRMED','FAILED')),
  CONSTRAINT ck_payout_destinations_compliance CHECK (compliance_status IN ('PENDING','APPROVED','REJECTED')),
  CONSTRAINT ck_payout_destinations_verified CHECK (status <> 'VERIFIED' OR (verified_at IS NOT NULL AND verified_by IS NOT NULL
                                                    AND ownership_status = 'CONFIRMED' AND compliance_status = 'APPROVED')),
  CONSTRAINT ck_payout_destinations_method CHECK (verification_method IS NULL OR verification_method IN ('BANK_LETTER',
             'BANK_STATEMENT','MOBILE_MONEY_STATEMENT','PROVIDER_LOOKUP')),
  CONSTRAINT ck_payout_destinations_assigned CHECK ((assigned_to IS NULL) = (assigned_at IS NULL)),
  CONSTRAINT ck_payout_destinations_version CHECK (version >= 1 AND details_version >= 1)
);
-- one live destination per owner for the same account on the same rail and currency
CREATE UNIQUE INDEX uq_payout_destinations_live_account ON app.payout_destinations (owner_ref, rail, currency, account_bidx)
  WHERE status <> 'RETIRED';
CREATE INDEX ix_payout_destinations_bidx ON app.payout_destinations (account_bidx);   -- cross-owner reuse = risk signal
CREATE INDEX ix_payout_destinations_owner ON app.payout_destinations (owner_ref);
CREATE INDEX ix_payout_destinations_queue ON app.payout_destinations (status, requested_at) WHERE status = 'PENDING_VERIFICATION';
CREATE TRIGGER trg_payout_destinations_guard_status BEFORE INSERT OR UPDATE OF status ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('payout_destination');
CREATE TRIGGER trg_payout_destinations_bump_version BEFORE UPDATE ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_payout_destinations_updated_at BEFORE UPDATE ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_payout_destinations_keep_created_at BEFORE UPDATE ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_payout_destinations_immutable BEFORE UPDATE ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('owner_user_id', 'owner_organisation_id', 'created_by', 'rail', 'currency', 'category');
CREATE TRIGGER trg_payout_destinations_no_delete BEFORE DELETE ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
-- A change of account data resets verification and restarts the cooling-off period.
CREATE FUNCTION app.payout_destinations_change_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (NEW.account_ciphertext, NEW.account_bidx, NEW.holder_name, NEW.bank_code, NEW.payee_type, NEW.beneficiary_id) IS DISTINCT FROM
     (OLD.account_ciphertext, OLD.account_bidx, OLD.holder_name, OLD.bank_code, OLD.payee_type, OLD.beneficiary_id) THEN
    IF OLD.status = 'RETIRED' THEN
      RAISE EXCEPTION 'retired destination % cannot change', OLD.id USING ERRCODE = 'restrict_violation';
    END IF;
    IF NEW.status <> 'UNVERIFIED' OR NEW.ownership_status <> 'NOT_STARTED' OR NEW.compliance_status <> 'PENDING'
       OR NEW.cooling_off_until <= OLD.cooling_off_until OR NEW.verified_at IS NOT NULL
       OR NEW.details_version <> OLD.details_version + 1 THEN
      RAISE EXCEPTION 'destination change requires UNVERIFIED, reset checks, details_version + 1 and a new cooling-off period'
        USING ERRCODE = 'check_violation';
    END IF;
  ELSIF NEW.details_version <> OLD.details_version THEN
    RAISE EXCEPTION 'details_version changes only with account data' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_destinations_change_guard BEFORE UPDATE ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.payout_destinations_change_guard();

-- app.payout_destination_checks — append-only check results per destination version.
CREATE TABLE app.payout_destination_checks (
  id                  uuid        NOT NULL,
  destination_id      uuid        NOT NULL REFERENCES app.payout_destinations (id),
  details_version     integer     NOT NULL,                -- payout_destinations.details_version checked
  check_kind          text        NOT NULL,
  method              text        NOT NULL,
  result              text        NOT NULL,
  non_production      boolean     NOT NULL DEFAULT false,
  provider_name       text,
  performed_by        uuid        REFERENCES app.users (id),
  evidence_record_ids uuid[]      NOT NULL DEFAULT '{}',
  detail_code         text,
  performed_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_destination_checks PRIMARY KEY (id),
  CONSTRAINT ck_payout_destination_checks_kind CHECK (check_kind IN ('FORMAT','OWNERSHIP','COMPLIANCE')),
  CONSTRAINT ck_payout_destination_checks_method CHECK (method IN ('FORMAT_RULES','PROVIDER_LOOKUP','DEV_MOCK_PROVIDER','BANK_LETTER',
             'BANK_STATEMENT','MOBILE_MONEY_STATEMENT','STAFF_REVIEW')),
  CONSTRAINT ck_payout_destination_checks_result CHECK (result IN ('PASS','FAIL','INCONCLUSIVE','PROVIDER_CONFIRMATION_REQUIRED')),
  CONSTRAINT ck_payout_destination_checks_kind_method CHECK (
       (check_kind = 'FORMAT'     AND method = 'FORMAT_RULES' AND performed_by IS NULL)
    OR (check_kind = 'OWNERSHIP'  AND method <> 'FORMAT_RULES')
    OR (check_kind = 'COMPLIANCE' AND method = 'STAFF_REVIEW' AND performed_by IS NOT NULL)),
  -- a development mock can never confirm ownership (brief §12)
  CONSTRAINT ck_payout_destination_checks_mock CHECK ((method = 'DEV_MOCK_PROVIDER') <= non_production),
  CONSTRAINT ck_payout_destination_checks_mock_pass CHECK (NOT (non_production AND result = 'PASS' AND check_kind = 'OWNERSHIP')),
  -- documentary ownership evidence is decided by a person and points at evidence
  CONSTRAINT ck_payout_destination_checks_manual CHECK (method NOT IN ('BANK_LETTER','BANK_STATEMENT','MOBILE_MONEY_STATEMENT')
             OR result = 'INCONCLUSIVE' OR (performed_by IS NOT NULL AND cardinality(evidence_record_ids) >= 1)),
  CONSTRAINT ck_payout_destination_checks_version CHECK (details_version >= 1),
  CONSTRAINT ck_payout_destination_checks_detail CHECK (detail_code IS NULL OR detail_code ~ '^[A-Z][A-Z0-9_]{2,63}$')
);
CREATE INDEX ix_payout_destination_checks_destination ON app.payout_destination_checks (destination_id, details_version, check_kind);
CREATE TRIGGER trg_payout_destination_checks_no_mutation BEFORE UPDATE OR DELETE ON app.payout_destination_checks
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
-- a reviewer never verifies their own (or their organisation's) destination, nor one they last changed
CREATE FUNCTION app.payout_destination_checks_not_self() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE d app.payout_destinations%ROWTYPE;
BEGIN
  IF NEW.performed_by IS NULL THEN
    RETURN NEW;
  END IF;
  SELECT * INTO d FROM app.payout_destinations WHERE id = NEW.destination_id;
  IF NEW.performed_by IN (d.owner_user_id, d.created_by, d.last_changed_by)
     OR EXISTS (SELECT 1 FROM app.organisation_members m WHERE m.organisation_id = d.owner_organisation_id
                  AND m.status <> 'REMOVED' AND m.user_id = NEW.performed_by) THEN
    RAISE EXCEPTION 'a reviewer cannot check a payout destination they own, created, changed or whose organisation they belong to'
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_destination_checks_not_self BEFORE INSERT ON app.payout_destination_checks
  FOR EACH ROW EXECUTE FUNCTION app.payout_destination_checks_not_self();
-- VERIFIED needs an ownership PASS (production evidence) and a compliance PASS for the current account details.
CREATE FUNCTION app.payout_destinations_verified_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'VERIFIED' AND (TG_OP = 'INSERT' OR OLD.status <> 'VERIFIED') THEN
    IF NOT EXISTS (SELECT 1 FROM app.payout_destination_checks c WHERE c.destination_id = NEW.id AND c.details_version = NEW.details_version
                     AND c.check_kind = 'OWNERSHIP' AND c.result = 'PASS' AND NOT c.non_production)
       OR NOT EXISTS (SELECT 1 FROM app.payout_destination_checks c WHERE c.destination_id = NEW.id AND c.details_version = NEW.details_version
                     AND c.check_kind = 'COMPLIANCE' AND c.result = 'PASS') THEN
      RAISE EXCEPTION 'destination % cannot be VERIFIED without ownership and compliance PASS checks for details version %', NEW.id, NEW.details_version
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_payout_destinations_verified_guard BEFORE INSERT OR UPDATE ON app.payout_destinations
  FOR EACH ROW EXECUTE FUNCTION app.payout_destinations_verified_guard();

-- app.payout_destination_events — append-only timeline (one row per version).
CREATE TABLE app.payout_destination_events (
  id                  uuid        NOT NULL,
  destination_id      uuid        NOT NULL REFERENCES app.payout_destinations (id),
  destination_version integer     NOT NULL,
  event_type          text        NOT NULL,
  from_status         text,
  to_status           text        NOT NULL,
  actor_type          text        NOT NULL,
  actor_id            uuid        REFERENCES app.users (id),
  reason_code         text,
  occurred_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_payout_destination_events PRIMARY KEY (id),
  CONSTRAINT uq_payout_destination_events_version UNIQUE (destination_id, destination_version),
  CONSTRAINT ck_payout_destination_events_actor CHECK ((actor_type IN ('USER','STAFF') AND actor_id IS NOT NULL)
                                                       OR (actor_type = 'SYSTEM' AND actor_id IS NULL)),
  CONSTRAINT ck_payout_destination_events_reason CHECK (reason_code IS NULL OR reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$')
);
CREATE TRIGGER trg_payout_destination_events_no_mutation BEFORE UPDATE OR DELETE ON app.payout_destination_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION app.payout_destinations_require_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v integer; st text;
BEGIN
  SELECT version, status INTO v, st FROM app.payout_destinations WHERE id = NEW.id;
  IF NOT EXISTS (SELECT 1 FROM app.payout_destination_events e WHERE e.destination_id = NEW.id AND e.destination_version = v AND e.to_status = st) THEN
    RAISE EXCEPTION 'payout destination % version % has no matching event row', NEW.id, v USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_payout_destinations_require_event AFTER INSERT OR UPDATE ON app.payout_destinations
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.payout_destinations_require_event();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.payout_destination_events, app.payout_destination_checks, app.payout_destinations;
DROP FUNCTION IF EXISTS app.payout_destinations_change_guard(), app.payout_destination_checks_not_self(),
  app.payout_destinations_verified_guard(), app.payout_destinations_require_event();
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine = 'payout_destination';
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
