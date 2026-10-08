-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate).
-- File 0010: schema `kyc` (owner module: kyc). Baseline §5.11. ADR-009, ADR-015, ADR-016.
-- Explained in docs/database/compliance-schema.md.
--
-- C3 RESTRICTED. Only the `fundzim_kyc` role (kyc module pool) has privileges here (0018). C3 attributes are
-- stored ONLY as application-level envelope-encrypted ciphertext (`*_ciphertext bytea` + `*_key_id text`),
-- with an HMAC-SHA-256 blind index (`*_bidx bytea`, 32 bytes) where equality search is needed. No plaintext
-- identity number, date of birth or address column exists. Documents are object references to
-- app.stored_objects (bucket PRIVATE_KYC) plus evidence records — never document content.
--
-- Cross-schema FKs: kyc -> app.users, app.organisations, app.stored_objects, audit.evidence_records.
-- campaign_id / beneficiary_id are plain uuids: app.campaigns (0014) and app.beneficiaries (0012) load
-- after this file and belong to higher-layer modules (concern recorded in compliance-schema.md §9).
-- =====================================================================================================

-- -----------------------------------------------------------------------------------------------------
-- kyc.verification_profiles — one per user. Level and status are orthogonal (kyc-architecture §3).
-- Stage 1 names: verification_level / verification_status (columns `level` / `status` here so the shared
-- transition guard applies to the status overlay). Every version has one profile_events row (deferred check).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.verification_profiles (
  id                uuid        NOT NULL,
  user_id           uuid        NOT NULL REFERENCES app.users(id),
  level             text        NOT NULL DEFAULT 'UNVERIFIED',
  status            text        NOT NULL DEFAULT 'ACTIVE',
  risk_rating       text        NOT NULL DEFAULT 'STANDARD',
  pep_status        text        NOT NULL DEFAULT 'UNKNOWN',
  paused_scope      text[]      NOT NULL DEFAULT '{}',    -- capabilities paused while PENDING_REVIEW
  level_granted_at  timestamptz,
  status_changed_at timestamptz NOT NULL DEFAULT now(),
  policy_version    text        NOT NULL,                 -- kyc_gate_policy version applied
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_verification_profiles PRIMARY KEY (id),
  CONSTRAINT uq_verification_profiles_user UNIQUE (user_id),
  CONSTRAINT ck_verification_profiles_level CHECK (level IN ('UNVERIFIED','BASIC_VERIFIED','IDENTITY_VERIFIED','PAYOUT_VERIFIED')),
  CONSTRAINT ck_verification_profiles_status CHECK (status IN ('ACTIVE','PENDING_REVIEW','REJECTED','SUSPENDED')),
  CONSTRAINT ck_verification_profiles_risk CHECK (risk_rating IN ('LOW','STANDARD','HIGH')),
  CONSTRAINT ck_verification_profiles_pep CHECK (pep_status IN ('NONE','PEP','PEP_RCA','UNKNOWN')),
  -- PEP raises the rating to HIGH (kyc-architecture §7)
  CONSTRAINT ck_verification_profiles_pep_high CHECK (pep_status NOT IN ('PEP','PEP_RCA') OR risk_rating = 'HIGH'),
  CONSTRAINT ck_verification_profiles_scope CHECK (paused_scope <@ ARRAY['DONATE','DRAFT','PUBLISH','WITHDRAW','REPRESENT_ORG']),
  CONSTRAINT ck_verification_profiles_scope_status CHECK (status = 'PENDING_REVIEW' OR paused_scope = '{}'),
  CONSTRAINT ck_verification_profiles_granted CHECK (level = 'UNVERIFIED' OR level_granted_at IS NOT NULL),
  CONSTRAINT ck_verification_profiles_version CHECK (version >= 1)
);
COMMENT ON TABLE kyc.verification_profiles IS 'C2 (level/status) + C3 context. One per user; mirror of level/status is published by event.';

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('kyc_verification_status', '',               'ACTIVE'),
  ('kyc_verification_status', 'ACTIVE',         'PENDING_REVIEW'),
  ('kyc_verification_status', 'ACTIVE',         'SUSPENDED'),
  ('kyc_verification_status', 'PENDING_REVIEW', 'ACTIVE'),
  ('kyc_verification_status', 'PENDING_REVIEW', 'REJECTED'),
  ('kyc_verification_status', 'PENDING_REVIEW', 'SUSPENDED'),
  ('kyc_verification_status', 'REJECTED',       'PENDING_REVIEW'),
  ('kyc_verification_status', 'SUSPENDED',      'ACTIVE'),
  ('kyc_verification_status', 'SUSPENDED',      'REJECTED');
CREATE TRIGGER trg_verification_profiles_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('kyc_verification_status');
CREATE TRIGGER trg_verification_profiles_updated_at BEFORE UPDATE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_verification_profiles_created_at BEFORE UPDATE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_verification_profiles_no_delete BEFORE DELETE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_verification_profiles_no_truncate BEFORE TRUNCATE ON kyc.verification_profiles
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION kyc.versioned_row_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.id <> OLD.id THEN
    RAISE EXCEPTION '%.%: id is immutable', TG_TABLE_SCHEMA, TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION '%.%: version must increase by 1 on every change', TG_TABLE_SCHEMA, TG_TABLE_NAME
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_verification_profiles_version BEFORE UPDATE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION kyc.versioned_row_guard();
CREATE FUNCTION kyc.verification_profiles_user_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.user_id <> OLD.user_id THEN
    RAISE EXCEPTION 'verification profile: user_id is immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_verification_profiles_user BEFORE UPDATE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION kyc.verification_profiles_user_immutable();

-- -----------------------------------------------------------------------------------------------------
-- kyc.kyb_organisations — KYB record per organisation (Stage 1 `kyc.organisations`). Same overlay.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.kyb_organisations (
  id                           uuid        NOT NULL,
  organisation_id              uuid        NOT NULL REFERENCES app.organisations(id),
  legal_name                   text        NOT NULL,                     -- C2 (registered name; public once verified)
  trading_name                 text,
  org_type                     text        NOT NULL,
  registration_number          text,                                    -- C2 (registry number, not a personal identifier)
  registry                     text,
  registered_address_ciphertext bytea,                                  -- C3
  registered_address_key_id    text,
  formed_on                    date,
  level                        text        NOT NULL DEFAULT 'ORG_UNVERIFIED',
  status                       text        NOT NULL DEFAULT 'ACTIVE',
  risk_rating                  text        NOT NULL DEFAULT 'STANDARD',
  level_granted_at             timestamptz,
  status_changed_at            timestamptz NOT NULL DEFAULT now(),
  policy_version               text        NOT NULL,
  version                      integer     NOT NULL DEFAULT 1,
  created_at                   timestamptz NOT NULL DEFAULT now(),
  updated_at                   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyb_organisations PRIMARY KEY (id),
  CONSTRAINT uq_kyb_organisations_org UNIQUE (organisation_id),
  CONSTRAINT ck_kyb_organisations_type CHECK (org_type IN ('COMPANY','TRUST','PVO','CHURCH_FBO','SCHOOL','HEALTH_INSTITUTION',
                                                           'COMMUNITY_ORGANISATION','SPORTS_CLUB','OTHER')),
  CONSTRAINT ck_kyb_organisations_registry CHECK (registry IS NULL OR registry IN ('COMPANIES_REGISTRY','PVO_REGISTRAR','HIGH_COURT',
                                                  'DEEDS_REGISTRY','MINISTRY_EDUCATION','HEALTH_REGISTRY','OTHER')),
  CONSTRAINT ck_kyb_organisations_address_pair CHECK ((registered_address_ciphertext IS NULL) = (registered_address_key_id IS NULL)),
  CONSTRAINT ck_kyb_organisations_level CHECK (level IN ('ORG_UNVERIFIED','ORG_REGISTERED_VERIFIED','ORG_KYB_VERIFIED','ORG_PAYOUT_VERIFIED')),
  CONSTRAINT ck_kyb_organisations_status CHECK (status IN ('ACTIVE','PENDING_REVIEW','REJECTED','SUSPENDED')),
  CONSTRAINT ck_kyb_organisations_risk CHECK (risk_rating IN ('LOW','STANDARD','ELEVATED','HIGH')),
  CONSTRAINT ck_kyb_organisations_granted CHECK (level = 'ORG_UNVERIFIED' OR level_granted_at IS NOT NULL),
  CONSTRAINT ck_kyb_organisations_version CHECK (version >= 1)
);
COMMENT ON TABLE kyc.kyb_organisations IS 'C2/C3. KYB record; projection to app.organisation_verifications by event.';
CREATE TRIGGER trg_kyb_organisations_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('kyc_verification_status');
CREATE TRIGGER trg_kyb_organisations_updated_at BEFORE UPDATE ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_kyb_organisations_created_at BEFORE UPDATE ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_kyb_organisations_version BEFORE UPDATE ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION kyc.versioned_row_guard();
CREATE TRIGGER trg_kyb_organisations_no_delete BEFORE DELETE ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_kyb_organisations_no_truncate BEFORE TRUNCATE ON kyc.kyb_organisations
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- kyc.kyc_cases — verification attempts and reviews of an individual (Stage 1 verification_attempts +
-- reviews). kyc.kyb_cases — the same for an organisation. Machine 'kyc_case' shared.
-- -----------------------------------------------------------------------------------------------------
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('kyc_case', '',                    'OPEN'),
  ('kyc_case', 'OPEN',                'IN_REVIEW'),
  ('kyc_case', 'OPEN',                'DECIDED'),             -- automated PASS only (never an automated REJECT, LR-071)
  ('kyc_case', 'OPEN',                'ABANDONED'),
  ('kyc_case', 'IN_REVIEW',           'AWAITING_SUBMISSION'),
  ('kyc_case', 'AWAITING_SUBMISSION', 'IN_REVIEW'),
  ('kyc_case', 'AWAITING_SUBMISSION', 'ABANDONED'),
  ('kyc_case', 'IN_REVIEW',           'ESCALATED'),
  ('kyc_case', 'ESCALATED',           'IN_REVIEW'),
  ('kyc_case', 'ESCALATED',           'DECIDED'),
  ('kyc_case', 'IN_REVIEW',           'DECIDED');

CREATE TABLE kyc.kyc_cases (
  id                 uuid        NOT NULL,
  profile_id         uuid        NOT NULL REFERENCES kyc.verification_profiles(id),
  origin             text        NOT NULL,
  target_level       text,
  trigger_code       text,
  scope_paused       text[]      NOT NULL DEFAULT '{}',
  status             text        NOT NULL,
  vendor_code        text,                                 -- PD-25; NULL for manual-only
  vendor_session_ref text,                                 -- opaque vendor reference (C2)
  assigned_to        uuid        REFERENCES app.users(id),
  assigned_at        timestamptz,
  opened_at          timestamptz NOT NULL DEFAULT now(),
  closed_at          timestamptz,
  version            integer     NOT NULL DEFAULT 1,
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyc_cases PRIMARY KEY (id),
  CONSTRAINT ck_kyc_cases_origin CHECK (origin IN ('VERIFICATION_ATTEMPT','REVERIFICATION_TRIGGER','SCREENING','RISK_SIGNAL',
                                                   'COMPLIANCE_REFERRAL','DUPLICATE_IDENTITY')),
  CONSTRAINT ck_kyc_cases_target CHECK (target_level IS NULL OR target_level IN ('BASIC_VERIFIED','IDENTITY_VERIFIED','PAYOUT_VERIFIED')),
  CONSTRAINT ck_kyc_cases_attempt_target CHECK (origin <> 'VERIFICATION_ATTEMPT' OR target_level IS NOT NULL),
  CONSTRAINT ck_kyc_cases_trigger CHECK (origin = 'VERIFICATION_ATTEMPT' OR trigger_code IS NOT NULL),
  CONSTRAINT ck_kyc_cases_scope CHECK (scope_paused <@ ARRAY['DONATE','DRAFT','PUBLISH','WITHDRAW','REPRESENT_ORG']),
  CONSTRAINT ck_kyc_cases_status CHECK (status IN ('OPEN','IN_REVIEW','AWAITING_SUBMISSION','ESCALATED','DECIDED','ABANDONED')),
  CONSTRAINT ck_kyc_cases_closed CHECK ((status IN ('DECIDED','ABANDONED')) = (closed_at IS NOT NULL)),
  CONSTRAINT ck_kyc_cases_assigned CHECK ((assigned_to IS NULL) = (assigned_at IS NULL))
);
COMMENT ON TABLE kyc.kyc_cases IS 'C3. Individual verification attempts/reviews.';
CREATE INDEX ix_kyc_cases_profile ON kyc.kyc_cases (profile_id, opened_at DESC);
CREATE INDEX ix_kyc_cases_queue ON kyc.kyc_cases (status, assigned_to) WHERE status IN ('OPEN','IN_REVIEW','ESCALATED');
CREATE TRIGGER trg_kyc_cases_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('kyc_case');
CREATE TRIGGER trg_kyc_cases_version BEFORE UPDATE ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION kyc.versioned_row_guard();
CREATE TRIGGER trg_kyc_cases_updated_at BEFORE UPDATE ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_kyc_cases_no_delete BEFORE DELETE ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_kyc_cases_no_truncate BEFORE TRUNCATE ON kyc.kyc_cases
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE kyc.kyb_cases (
  id                  uuid        NOT NULL,
  kyb_organisation_id uuid        NOT NULL REFERENCES kyc.kyb_organisations(id),
  origin              text        NOT NULL,
  target_level        text,
  trigger_code        text,
  campaign_id         uuid,                                -- app.campaigns (no FK: loads later, higher layer)
  status              text        NOT NULL,
  assigned_to         uuid        REFERENCES app.users(id),
  assigned_at         timestamptz,
  opened_at           timestamptz NOT NULL DEFAULT now(),
  closed_at           timestamptz,
  version             integer     NOT NULL DEFAULT 1,
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyb_cases PRIMARY KEY (id),
  CONSTRAINT ck_kyb_cases_origin CHECK (origin IN ('VERIFICATION_ATTEMPT','CHANGE_NOTIFICATION','AUTHORITY_EXPIRY','SCREENING',
                                                   'RISK_SIGNAL','COMPLIANCE_REFERRAL','PERIODIC')),
  CONSTRAINT ck_kyb_cases_target CHECK (target_level IS NULL OR target_level IN ('ORG_REGISTERED_VERIFIED','ORG_KYB_VERIFIED','ORG_PAYOUT_VERIFIED')),
  CONSTRAINT ck_kyb_cases_attempt_target CHECK (origin <> 'VERIFICATION_ATTEMPT' OR target_level IS NOT NULL),
  CONSTRAINT ck_kyb_cases_trigger CHECK (origin = 'VERIFICATION_ATTEMPT' OR trigger_code IS NOT NULL),
  CONSTRAINT ck_kyb_cases_status CHECK (status IN ('OPEN','IN_REVIEW','AWAITING_SUBMISSION','ESCALATED','DECIDED','ABANDONED')),
  CONSTRAINT ck_kyb_cases_closed CHECK ((status IN ('DECIDED','ABANDONED')) = (closed_at IS NOT NULL)),
  CONSTRAINT ck_kyb_cases_assigned CHECK ((assigned_to IS NULL) = (assigned_at IS NULL))
);
COMMENT ON TABLE kyc.kyb_cases IS 'C3. Organisation verification attempts/reviews.';
CREATE INDEX ix_kyb_cases_org ON kyc.kyb_cases (kyb_organisation_id, opened_at DESC);
CREATE TRIGGER trg_kyb_cases_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('kyc_case');
CREATE TRIGGER trg_kyb_cases_version BEFORE UPDATE ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION kyc.versioned_row_guard();
CREATE TRIGGER trg_kyb_cases_updated_at BEFORE UPDATE ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_kyb_cases_no_delete BEFORE DELETE ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_kyb_cases_no_truncate BEFORE TRUNCATE ON kyc.kyb_cases
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- kyc.profile_events — append-only level/status history for individual profiles AND KYB organisations.
-- Each aggregate version has exactly one row (deferred constraint triggers below).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.profile_events (
  id                  uuid        NOT NULL,
  profile_id          uuid        REFERENCES kyc.verification_profiles(id),
  kyb_organisation_id uuid        REFERENCES kyc.kyb_organisations(id),
  aggregate_version   integer     NOT NULL,
  from_level          text,
  to_level            text        NOT NULL,
  from_status         text,
  to_status           text        NOT NULL,
  reason_code         text        NOT NULL,                -- e.g. LEVEL_GRANTED, DOWNGRADE_DOC_EXPIRED, SCREENING_HIT
  kyc_case_id         uuid        REFERENCES kyc.kyc_cases(id),
  kyb_case_id         uuid        REFERENCES kyc.kyb_cases(id),
  actor_type          text        NOT NULL,
  actor_id            uuid        REFERENCES app.users(id),
  actor_job           text,
  audit_event_id      uuid,
  occurred_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_profile_events PRIMARY KEY (id),
  CONSTRAINT ck_profile_events_subject CHECK (num_nonnulls(profile_id, kyb_organisation_id) = 1),
  CONSTRAINT ck_profile_events_case CHECK (num_nonnulls(kyc_case_id, kyb_case_id) <= 1
                                           AND (kyc_case_id IS NULL OR profile_id IS NOT NULL)
                                           AND (kyb_case_id IS NULL OR kyb_organisation_id IS NOT NULL)),
  CONSTRAINT ck_profile_events_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_profile_events_actor CHECK (
       (actor_type = 'STAFF'  AND actor_id IS NOT NULL)
    OR (actor_type = 'SYSTEM' AND actor_id IS NULL AND actor_job IS NOT NULL)
    OR (actor_type = 'USER'   AND actor_id IS NOT NULL))
);
COMMENT ON TABLE kyc.profile_events IS 'C2. Append-only level/status history.';
CREATE UNIQUE INDEX uq_profile_events_profile_version ON kyc.profile_events (profile_id, aggregate_version) WHERE profile_id IS NOT NULL;
CREATE UNIQUE INDEX uq_profile_events_kyb_version ON kyc.profile_events (kyb_organisation_id, aggregate_version) WHERE kyb_organisation_id IS NOT NULL;
CREATE TRIGGER trg_profile_events_no_mutation BEFORE UPDATE OR DELETE ON kyc.profile_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_profile_events_no_truncate BEFORE TRUNCATE ON kyc.profile_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION kyc.profile_requires_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v integer; lvl text; st text;
BEGIN
  IF TG_TABLE_NAME = 'verification_profiles' THEN
    SELECT version, level, status INTO v, lvl, st FROM kyc.verification_profiles WHERE id = NEW.id;
    IF NOT EXISTS (SELECT 1 FROM kyc.profile_events e WHERE e.profile_id = NEW.id AND e.aggregate_version = v
                     AND e.to_level = lvl AND e.to_status = st) THEN
      RAISE EXCEPTION 'verification profile % version % has no matching profile_events row', NEW.id, v USING ERRCODE = 'check_violation';
    END IF;
  ELSE
    SELECT version, level, status INTO v, lvl, st FROM kyc.kyb_organisations WHERE id = NEW.id;
    IF NOT EXISTS (SELECT 1 FROM kyc.profile_events e WHERE e.kyb_organisation_id = NEW.id AND e.aggregate_version = v
                     AND e.to_level = lvl AND e.to_status = st) THEN
      RAISE EXCEPTION 'KYB organisation % version % has no matching profile_events row', NEW.id, v USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_verification_profiles_require_event AFTER INSERT OR UPDATE OF level, status ON kyc.verification_profiles
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kyc.profile_requires_event();
CREATE CONSTRAINT TRIGGER trg_kyb_organisations_require_event AFTER INSERT OR UPDATE OF level, status ON kyc.kyb_organisations
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kyc.profile_requires_event();

-- -----------------------------------------------------------------------------------------------------
-- kyc.identities — C3 identity attributes, encrypted (Stage 1 kyc.identities). One ACTIVE row per subject;
-- corrections supersede (new row), never edit. Duplicate-identity detection: HMAC blind index over
-- (id_type, normalised number). Uniqueness applies across ACTIVE account-holder identities only (KYB persons
-- and beneficiaries may legitimately be the same human as an account holder).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.identities (
  id                       uuid        NOT NULL,
  subject_kind             text        NOT NULL,
  profile_id               uuid        REFERENCES kyc.verification_profiles(id),   -- ACCOUNT_HOLDER only
  status                   text        NOT NULL DEFAULT 'ACTIVE',
  legal_name_ciphertext    bytea       NOT NULL,
  legal_name_key_id        text        NOT NULL,
  legal_name_bidx          bytea,                          -- HMAC of normalised name (exact-match screening/dedupe aid)
  dob_ciphertext           bytea,
  dob_key_id               text,
  nationality              char(2),                        -- ISO 3166-1 alpha-2 (C2)
  id_type                  text,
  id_number_ciphertext     bytea,
  id_number_key_id         text,
  id_number_bidx           bytea,
  bidx_key_version         integer,                        -- blind-index HMAC key version (rotation = re-index job)
  id_number_masked         text,                           -- C2 display form, e.g. '*******12'
  doc_expiry               date,
  address_ciphertext       bytea,
  address_key_id           text,
  subject_data_key_ref     text        NOT NULL,           -- per-subject DEK (crypto-shredding unit)
  source                   text        NOT NULL,
  supersedes_id            uuid        REFERENCES kyc.identities(id),
  shredded_at              timestamptz,
  created_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_identities PRIMARY KEY (id),
  CONSTRAINT ck_identities_kind CHECK (subject_kind IN ('ACCOUNT_HOLDER','KYB_PERSON','BENEFICIARY')),
  CONSTRAINT ck_identities_profile CHECK ((subject_kind = 'ACCOUNT_HOLDER') = (profile_id IS NOT NULL)),
  CONSTRAINT ck_identities_status CHECK (status IN ('ACTIVE','SUPERSEDED','INACTIVE','SHREDDED')),
  CONSTRAINT ck_identities_shredded CHECK ((status = 'SHREDDED') = (shredded_at IS NOT NULL)),
  -- AEAD output is at least nonce(12) + tag(16) bytes: rejects accidental plaintext of short values
  CONSTRAINT ck_identities_name_ct CHECK (octet_length(legal_name_ciphertext) >= 28),
  CONSTRAINT ck_identities_dob_pair CHECK ((dob_ciphertext IS NULL) = (dob_key_id IS NULL)
                                           AND (dob_ciphertext IS NULL OR octet_length(dob_ciphertext) >= 28)),
  CONSTRAINT ck_identities_addr_pair CHECK ((address_ciphertext IS NULL) = (address_key_id IS NULL)
                                            AND (address_ciphertext IS NULL OR octet_length(address_ciphertext) >= 28)),
  CONSTRAINT ck_identities_id_type CHECK (id_type IS NULL OR id_type IN ('ZW_NATIONAL_ID','ZW_PASSPORT','FOREIGN_PASSPORT','ZW_BIRTH_CERTIFICATE')),
  CONSTRAINT ck_identities_id_number_shape CHECK (
       (id_type IS NULL AND id_number_ciphertext IS NULL AND id_number_key_id IS NULL AND id_number_bidx IS NULL
        AND bidx_key_version IS NULL AND id_number_masked IS NULL)
    OR (id_type IS NOT NULL AND id_number_ciphertext IS NOT NULL AND id_number_key_id IS NOT NULL
        AND id_number_bidx IS NOT NULL AND bidx_key_version IS NOT NULL
        AND octet_length(id_number_ciphertext) >= 28)),
  CONSTRAINT ck_identities_bidx_len CHECK ((id_number_bidx IS NULL OR octet_length(id_number_bidx) = 32)
                                           AND (legal_name_bidx IS NULL OR octet_length(legal_name_bidx) = 32)),
  CONSTRAINT ck_identities_masked CHECK (id_number_masked IS NULL OR id_number_masked ~ '^\*{3,}[A-Z0-9]{0,4}$'),
  CONSTRAINT ck_identities_nationality CHECK (nationality IS NULL OR nationality ~ '^[A-Z]{2}$'),
  CONSTRAINT ck_identities_source CHECK (source IN ('USER_ENTRY','VENDOR_EXTRACTION','STAFF_ENTRY','OWNER_DECLARATION','PSP_EVIDENCE'))
);
COMMENT ON TABLE kyc.identities IS 'C3 RESTRICTED. Envelope-encrypted identity attributes + HMAC blind index. No plaintext.';
-- Duplicate-identity control (kyc-architecture §11): one ACTIVE account-holder identity per (type, number).
CREATE UNIQUE INDEX uq_identities_active_id_bidx ON kyc.identities (id_type, id_number_bidx)
  WHERE status = 'ACTIVE' AND subject_kind = 'ACCOUNT_HOLDER' AND id_number_bidx IS NOT NULL;
CREATE UNIQUE INDEX uq_identities_active_profile ON kyc.identities (profile_id) WHERE status = 'ACTIVE' AND profile_id IS NOT NULL;
CREATE UNIQUE INDEX uq_identities_supersedes ON kyc.identities (supersedes_id) WHERE supersedes_id IS NOT NULL;
CREATE INDEX ix_identities_bidx_any ON kyc.identities (id_type, id_number_bidx) WHERE id_number_bidx IS NOT NULL;   -- cross-kind linkage
CREATE TRIGGER trg_identities_no_delete BEFORE DELETE ON kyc.identities
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_identities_no_truncate BEFORE TRUNCATE ON kyc.identities
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Content is immutable; only the lifecycle moves ACTIVE -> SUPERSEDED|INACTIVE -> SHREDDED (crypto-shred keeps
-- the ciphertext, which becomes undecryptable once the per-subject key is destroyed).
CREATE FUNCTION kyc.identities_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT changed <@ ARRAY['status','shredded_at'] THEN
    RAISE EXCEPTION 'kyc.identities: content is immutable (attempted: %) — insert a superseding row', changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.status = 'SHREDDED'
     OR (OLD.status = 'ACTIVE' AND NEW.status NOT IN ('ACTIVE','SUPERSEDED','INACTIVE','SHREDDED'))
     OR (OLD.status IN ('SUPERSEDED','INACTIVE') AND NEW.status NOT IN (OLD.status,'SHREDDED')) THEN
    RAISE EXCEPTION 'kyc.identities: illegal lifecycle change % -> %', OLD.status, NEW.status USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_identities_guard BEFORE UPDATE ON kyc.identities
  FOR EACH ROW EXECUTE FUNCTION kyc.identities_guard();

-- -----------------------------------------------------------------------------------------------------
-- kyc.consents — append-only. A withdrawal is a NEW row (action = WITHDRAWN) that points at the consent
-- it withdraws; the original is never updated. Rationale: consent is evidence of a lawful basis at a point
-- in time (PRIVACY §3 "who, what text/version, when, how withdrawn"); updating `withdrawn_at` in place
-- would destroy the proof of what was valid before, and would make kyc.consents the only mutable C3
-- evidence table. Current state = kyc.v_consents_current.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.consents (
  id                     uuid        NOT NULL,
  consent_type           text        NOT NULL,
  action                 text        NOT NULL,
  withdraws_consent_id   uuid        REFERENCES kyc.consents(id),
  subject_user_id        uuid        REFERENCES app.users(id),
  subject_beneficiary_id uuid,                              -- app.beneficiaries (no FK: 0012 loads later)
  subject_identity_id    uuid        REFERENCES kyc.identities(id),
  grantor_user_id        uuid        REFERENCES app.users(id),
  grantor_identity_id    uuid        REFERENCES kyc.identities(id),
  grantor_capacity       text        NOT NULL,
  method                 text        NOT NULL,
  text_version           text        NOT NULL,             -- version id of the consent text shown
  scope_campaign_id      uuid,                              -- app.campaigns (no FK: 0014 loads later)
  evidence_record_id     uuid        REFERENCES audit.evidence_records(id),
  occurred_at            timestamptz NOT NULL,
  recorded_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_consents PRIMARY KEY (id),
  CONSTRAINT uq_consents_one_withdrawal UNIQUE (withdraws_consent_id),
  CONSTRAINT ck_consents_type CHECK (consent_type IN ('BIOMETRIC','KYC_VENDOR_TRANSFER','CROSS_BORDER_TRANSFER',
                                     'CAMPAIGN_PUBLICATION','HEALTH_DATA','MINOR_DATA','DATA_SHARING_INSTITUTION')),
  CONSTRAINT ck_consents_action CHECK (action IN ('GIVEN','WITHDRAWN')),
  CONSTRAINT ck_consents_withdrawal_ref CHECK ((action = 'WITHDRAWN') = (withdraws_consent_id IS NOT NULL)),
  CONSTRAINT ck_consents_subject CHECK (num_nonnulls(subject_user_id, subject_beneficiary_id, subject_identity_id) = 1),
  CONSTRAINT ck_consents_grantor CHECK (num_nonnulls(grantor_user_id, grantor_identity_id) = 1),
  CONSTRAINT ck_consents_capacity CHECK (grantor_capacity IN ('SELF','PARENT','GUARDIAN','LEGAL_REP')),
  CONSTRAINT ck_consents_minor CHECK (consent_type <> 'MINOR_DATA' OR grantor_capacity IN ('PARENT','GUARDIAN','LEGAL_REP')),
  CONSTRAINT ck_consents_method CHECK (method IN ('OTP_ECONSENT','SIGNED_FORM','IN_APP_ATTESTATION')),
  -- signed forms are evidence objects; written-consent sensitive types never rely on a bare in-app click
  -- (whether OTP e-consent is "in writing" is LR-063/LR-070)
  CONSTRAINT ck_consents_signed_evidence CHECK (method <> 'SIGNED_FORM' OR evidence_record_id IS NOT NULL),
  CONSTRAINT ck_consents_sensitive_method CHECK (consent_type NOT IN ('BIOMETRIC','HEALTH_DATA','MINOR_DATA')
                                                 OR action = 'WITHDRAWN' OR method IN ('OTP_ECONSENT','SIGNED_FORM'))
);
COMMENT ON TABLE kyc.consents IS 'C3. Append-only consent facts (given / withdrawn).';
CREATE INDEX ix_consents_subject_user ON kyc.consents (subject_user_id, consent_type) WHERE subject_user_id IS NOT NULL;
CREATE INDEX ix_consents_subject_beneficiary ON kyc.consents (subject_beneficiary_id, consent_type) WHERE subject_beneficiary_id IS NOT NULL;
CREATE TRIGGER trg_consents_no_mutation BEFORE UPDATE OR DELETE ON kyc.consents
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_consents_no_truncate BEFORE TRUNCATE ON kyc.consents
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION kyc.consents_withdrawal_matches() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g kyc.consents%ROWTYPE;
BEGIN
  IF NEW.action = 'WITHDRAWN' THEN
    SELECT * INTO g FROM kyc.consents WHERE id = NEW.withdraws_consent_id;
    IF g.action <> 'GIVEN' OR g.consent_type <> NEW.consent_type
       OR g.subject_user_id IS DISTINCT FROM NEW.subject_user_id
       OR g.subject_beneficiary_id IS DISTINCT FROM NEW.subject_beneficiary_id
       OR g.subject_identity_id IS DISTINCT FROM NEW.subject_identity_id
       OR NEW.occurred_at < g.occurred_at THEN
      RAISE EXCEPTION 'consent withdrawal must reference a GIVEN consent of the same type and subject' USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_consents_withdrawal BEFORE INSERT ON kyc.consents
  FOR EACH ROW EXECUTE FUNCTION kyc.consents_withdrawal_matches();

CREATE VIEW kyc.v_consents_current AS
  SELECT g.* FROM kyc.consents g
   WHERE g.action = 'GIVEN'
     AND NOT EXISTS (SELECT 1 FROM kyc.consents w WHERE w.withdraws_consent_id = g.id);

-- -----------------------------------------------------------------------------------------------------
-- kyc.organisation_persons — directors, trustees, office bearers, controllers, settlors, representatives
-- (Stage 1 org_persons). A person is a KYB-person identity, a FundZim user, or both.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.organisation_persons (
  id                  uuid        NOT NULL,
  kyb_organisation_id uuid        NOT NULL REFERENCES kyc.kyb_organisations(id),
  identity_id         uuid        REFERENCES kyc.identities(id),
  user_id             uuid        REFERENCES app.users(id),
  roles               text[]      NOT NULL,
  ownership_bp        integer,                               -- basis points, never float
  control_basis       text,
  valid_from          date        NOT NULL,
  valid_to            date,
  ended_reason        text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_organisation_persons PRIMARY KEY (id),
  CONSTRAINT ck_organisation_persons_ref CHECK (num_nonnulls(identity_id, user_id) >= 1),
  CONSTRAINT ck_organisation_persons_roles CHECK (cardinality(roles) >= 1 AND roles <@ ARRAY['DIRECTOR','TRUSTEE','OFFICE_BEARER',
                                                 'BENEFICIAL_OWNER','CONTROLLER','SETTLOR','TRUST_BENEFICIARY','REPRESENTATIVE']),
  CONSTRAINT ck_organisation_persons_bp CHECK (ownership_bp IS NULL OR ownership_bp BETWEEN 0 AND 10000),
  CONSTRAINT ck_organisation_persons_basis CHECK (control_basis IS NULL OR control_basis IN ('SHAREHOLDING','VOTING_RIGHTS',
             'APPOINT_REMOVE_DIRECTORS','SIGNIFICANT_INFLUENCE','CONTRIBUTIONS','SENIOR_MANAGING_OFFICIAL','TRUST_ROLE','OTHER')),
  CONSTRAINT ck_organisation_persons_window CHECK (valid_to IS NULL OR valid_to >= valid_from),
  CONSTRAINT ck_organisation_persons_ended CHECK ((valid_to IS NULL) = (ended_reason IS NULL))
);
COMMENT ON TABLE kyc.organisation_persons IS 'C3. Natural persons connected to an organisation.';
CREATE INDEX ix_organisation_persons_org ON kyc.organisation_persons (kyb_organisation_id) WHERE valid_to IS NULL;
CREATE TRIGGER trg_organisation_persons_no_delete BEFORE DELETE ON kyc.organisation_persons
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_organisation_persons_no_truncate BEFORE TRUNCATE ON kyc.organisation_persons
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
-- Only the end of the relationship may be recorded (once); a role change is a new row.
CREATE FUNCTION kyc.organisation_persons_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.valid_to IS NOT NULL OR NOT changed <@ ARRAY['valid_to','ended_reason'] THEN
    RAISE EXCEPTION 'organisation person %: only the end date may be set, once (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_organisation_persons_guard BEFORE UPDATE ON kyc.organisation_persons
  FOR EACH ROW EXECUTE FUNCTION kyc.organisation_persons_guard();

-- -----------------------------------------------------------------------------------------------------
-- kyc.beneficial_owners — append-only BO determinations per KYB case (BO subset of organisation_persons).
-- The threshold applied is a limit record (kyb.bo_threshold.*, LR-067); its id is recorded, never a number.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.beneficial_owners (
  id                     uuid        NOT NULL,
  kyb_organisation_id    uuid        NOT NULL REFERENCES kyc.kyb_organisations(id),
  organisation_person_id uuid        NOT NULL REFERENCES kyc.organisation_persons(id),
  kyb_case_id            uuid        NOT NULL REFERENCES kyc.kyb_cases(id),
  threshold_limit_key    text        NOT NULL,                -- e.g. kyb.bo_threshold.company
  threshold_limit_id     uuid,                                -- risk.limits version applied (no FK: kyc -> risk not allowed)
  ownership_bp           integer,
  control_basis          text        NOT NULL,
  is_smo_fallback        boolean     NOT NULL DEFAULT false,  -- senior managing official when no natural person meets threshold
  verified               boolean     NOT NULL DEFAULT false,
  determined_by          uuid        NOT NULL REFERENCES app.users(id),
  determined_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficial_owners PRIMARY KEY (id),
  CONSTRAINT uq_beneficial_owners_case_person UNIQUE (kyb_case_id, organisation_person_id),
  CONSTRAINT ck_beneficial_owners_key CHECK (threshold_limit_key ~ '^kyb\.bo_threshold\.[a-z_]+$'),
  CONSTRAINT ck_beneficial_owners_bp CHECK (ownership_bp IS NULL OR ownership_bp BETWEEN 0 AND 10000),
  CONSTRAINT ck_beneficial_owners_basis CHECK (control_basis IN ('SHAREHOLDING','VOTING_RIGHTS','APPOINT_REMOVE_DIRECTORS',
             'SIGNIFICANT_INFLUENCE','CONTRIBUTIONS','SENIOR_MANAGING_OFFICIAL','TRUST_ROLE','OTHER')),
  CONSTRAINT ck_beneficial_owners_smo CHECK (NOT is_smo_fallback OR control_basis = 'SENIOR_MANAGING_OFFICIAL')
);
COMMENT ON TABLE kyc.beneficial_owners IS 'C3. Append-only beneficial-ownership determinations.';
CREATE TRIGGER trg_beneficial_owners_no_mutation BEFORE UPDATE OR DELETE ON kyc.beneficial_owners
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_beneficial_owners_no_truncate BEFORE TRUNCATE ON kyc.beneficial_owners
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.beneficial_owners_person_check() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM kyc.organisation_persons p
                  WHERE p.id = NEW.organisation_person_id AND p.kyb_organisation_id = NEW.kyb_organisation_id
                    AND ('BENEFICIAL_OWNER' = ANY (p.roles) OR (NEW.is_smo_fallback AND 'CONTROLLER' = ANY (p.roles)))) THEN
    RAISE EXCEPTION 'beneficial owner must be an organisation person of the same organisation with role BENEFICIAL_OWNER'
      USING ERRCODE = 'check_violation';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM kyc.kyb_cases c WHERE c.id = NEW.kyb_case_id AND c.kyb_organisation_id = NEW.kyb_organisation_id) THEN
    RAISE EXCEPTION 'beneficial owner determination must belong to a KYB case of the same organisation' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_beneficial_owners_person BEFORE INSERT ON kyc.beneficial_owners
  FOR EACH ROW EXECUTE FUNCTION kyc.beneficial_owners_person_check();

-- -----------------------------------------------------------------------------------------------------
-- kyc.representative_authorities — who may act for the organisation on FundZim (Stage 1
-- org_representative_authorities). Expiry is mandatory (document expiry or kyb.authority_max_age limit).
-- Revocation is set once.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.representative_authorities (
  id                  uuid        NOT NULL,
  kyb_organisation_id uuid        NOT NULL REFERENCES kyc.kyb_organisations(id),
  user_id             uuid        NOT NULL REFERENCES app.users(id),
  permissions         text[]      NOT NULL,
  evidence_record_id  uuid        NOT NULL REFERENCES audit.evidence_records(id),   -- board resolution / letter of authority
  verified_by         uuid        NOT NULL REFERENCES app.users(id),
  valid_from          timestamptz NOT NULL,
  valid_to            timestamptz NOT NULL,
  revoked_at          timestamptz,
  revoked_by          uuid        REFERENCES app.users(id),
  revoke_reason       text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_representative_authorities PRIMARY KEY (id),
  CONSTRAINT ck_representative_authorities_perms CHECK (cardinality(permissions) >= 1 AND permissions <@ ARRAY[
             'org.campaign.create','org.campaign.submit','org.payout.request','org.member.manage','org.verification.submit']),
  CONSTRAINT ck_representative_authorities_window CHECK (valid_to > valid_from),
  CONSTRAINT ck_representative_authorities_revoked CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)
                                                          AND (revoked_at IS NULL) = (revoke_reason IS NULL)),
  CONSTRAINT ck_representative_authorities_not_self CHECK (verified_by <> user_id)
);
COMMENT ON TABLE kyc.representative_authorities IS 'C3. Organisation representative authority with mandatory expiry.';
CREATE INDEX ix_representative_authorities_user ON kyc.representative_authorities (user_id, kyb_organisation_id) WHERE revoked_at IS NULL;
CREATE TRIGGER trg_representative_authorities_no_delete BEFORE DELETE ON kyc.representative_authorities
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_representative_authorities_no_truncate BEFORE TRUNCATE ON kyc.representative_authorities
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.representative_authorities_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.revoked_at IS NOT NULL OR NOT changed <@ ARRAY['revoked_at','revoked_by','revoke_reason'] THEN
    RAISE EXCEPTION 'representative authority %: only revocation may be recorded, once (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_representative_authorities_guard BEFORE UPDATE ON kyc.representative_authorities
  FOR EACH ROW EXECUTE FUNCTION kyc.representative_authorities_guard();

-- -----------------------------------------------------------------------------------------------------
-- kyc.fundraising_authorities — one per organisation or individual campaign (kyb-architecture §4, §7;
-- beneficiary-verification §7). Supersede, never edit: only status (and verification/approval, once) move.
-- SECTION_8_AUTHORITY: valid_to mandatory and valid_to - valid_from <= 180 days. 180 is the structural
-- OUTER bound written into the Stage 1 specification (90 + 90 days, PVO Act s 8 text, LR-068); the
-- operative value is the limit record `pvo.s8_max_days`, enforced by the kyc service. See concern C-4 in
-- compliance-schema.md.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.fundraising_authorities (
  id                     uuid        NOT NULL,
  kyb_organisation_id    uuid        REFERENCES kyc.kyb_organisations(id),
  user_id                uuid        REFERENCES app.users(id),
  campaign_id            uuid,                                 -- app.campaigns (no FK: 0014 loads later)
  authority_basis        text        NOT NULL,
  reference_number       text,
  issuer                 text,
  purpose                text        NOT NULL,
  valid_from             date        NOT NULL,
  valid_to               date,
  conditions             jsonb       NOT NULL DEFAULT '{}',    -- s8 conditions (disclosed account ref = destination id, spend window limit key)
  excluded_body_ground   text,
  evidence_record_ids    uuid[]      NOT NULL DEFAULT '{}',
  status                 text        NOT NULL,
  verified_by            uuid        REFERENCES app.users(id),
  verified_at            timestamptz,
  compliance_approved_by uuid        REFERENCES app.users(id),
  supersedes_id          uuid        REFERENCES kyc.fundraising_authorities(id),
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_fundraising_authorities PRIMARY KEY (id),
  CONSTRAINT ck_fundraising_authorities_subject CHECK (num_nonnulls(kyb_organisation_id, user_id) = 1),
  CONSTRAINT ck_fundraising_authorities_basis CHECK (authority_basis IN ('SELF_FUNDRAISING','REGISTERED_PVO','EXCLUDED_BODY',
                                                     'SECTION_8_AUTHORITY','NOT_REQUIRED_OTHER')),
  CONSTRAINT ck_fundraising_authorities_self CHECK (authority_basis <> 'SELF_FUNDRAISING' OR user_id IS NOT NULL),
  CONSTRAINT ck_fundraising_authorities_s8 CHECK (authority_basis <> 'SECTION_8_AUTHORITY' OR (
             valid_to IS NOT NULL AND valid_to - valid_from <= 180 AND reference_number IS NOT NULL)),
  CONSTRAINT ck_fundraising_authorities_window CHECK (valid_to IS NULL OR valid_to >= valid_from),
  CONSTRAINT ck_fundraising_authorities_pvo CHECK (authority_basis <> 'REGISTERED_PVO' OR reference_number IS NOT NULL),
  CONSTRAINT ck_fundraising_authorities_excluded CHECK ((authority_basis = 'EXCLUDED_BODY') = (excluded_body_ground IS NOT NULL)),
  CONSTRAINT ck_fundraising_authorities_ground CHECK (excluded_body_ground IS NULL OR excluded_body_ground IN ('RELIGIOUS_WORK_ONLY',
             'STATE_INSTITUTION','APPROVED_EDUCATIONAL_TRUST','REGISTERED_HEALTH_INSTITUTION','MEMBERS_ONLY_BODY','STATUTORY_TRUST','OTHER_PRESCRIBED')),
  CONSTRAINT ck_fundraising_authorities_status CHECK (status IN ('PENDING_VERIFICATION','ACTIVE','REJECTED','EXPIRED','REVOKED','SUPERSEDED')),
  CONSTRAINT ck_fundraising_authorities_verified CHECK (status IN ('PENDING_VERIFICATION','REJECTED')
                                                        OR (verified_by IS NOT NULL AND verified_at IS NOT NULL)),
  -- NOT_REQUIRED_OTHER: reviewer note + COMPLIANCE approval by a different person (maker-checker)
  CONSTRAINT ck_fundraising_authorities_nro CHECK (authority_basis <> 'NOT_REQUIRED_OTHER' OR status IN ('PENDING_VERIFICATION','REJECTED')
                                                   OR (compliance_approved_by IS NOT NULL AND compliance_approved_by <> verified_by)),
  CONSTRAINT ck_fundraising_authorities_evidence CHECK (authority_basis = 'SELF_FUNDRAISING' OR status IN ('PENDING_VERIFICATION','REJECTED')
                                                        OR cardinality(evidence_record_ids) >= 1)
);
COMMENT ON TABLE kyc.fundraising_authorities IS 'C3. PVO Act fundraising authority per organisation/individual campaign (LR-068).';
CREATE UNIQUE INDEX uq_fundraising_authorities_active_campaign ON kyc.fundraising_authorities (campaign_id)
  WHERE status = 'ACTIVE' AND campaign_id IS NOT NULL;
CREATE INDEX ix_fundraising_authorities_expiry ON kyc.fundraising_authorities (valid_to) WHERE status = 'ACTIVE';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('fundraising_authority', '',                     'PENDING_VERIFICATION'),
  ('fundraising_authority', 'PENDING_VERIFICATION', 'ACTIVE'),
  ('fundraising_authority', 'PENDING_VERIFICATION', 'REJECTED'),
  ('fundraising_authority', 'ACTIVE',               'EXPIRED'),
  ('fundraising_authority', 'ACTIVE',               'REVOKED'),
  ('fundraising_authority', 'ACTIVE',               'SUPERSEDED');
CREATE TRIGGER trg_fundraising_authorities_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.fundraising_authorities
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('fundraising_authority');
CREATE TRIGGER trg_fundraising_authorities_no_delete BEFORE DELETE ON kyc.fundraising_authorities
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_fundraising_authorities_no_truncate BEFORE TRUNCATE ON kyc.fundraising_authorities
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.fundraising_authorities_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT changed <@ ARRAY['status','verified_by','verified_at','compliance_approved_by'] THEN
    RAISE EXCEPTION 'fundraising authority %: content is immutable (attempted: %) — supersede with a new row', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF (OLD.verified_by IS NOT NULL AND NEW.verified_by IS DISTINCT FROM OLD.verified_by)
     OR (OLD.compliance_approved_by IS NOT NULL AND NEW.compliance_approved_by IS DISTINCT FROM OLD.compliance_approved_by) THEN
    RAISE EXCEPTION 'fundraising authority %: verification fields are set once', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_fundraising_authorities_guard BEFORE UPDATE ON kyc.fundraising_authorities
  FOR EACH ROW EXECUTE FUNCTION kyc.fundraising_authorities_guard();

-- -----------------------------------------------------------------------------------------------------
-- kyc.kyc_documents — metadata of documents in the private-kyc bucket. Object bytes live only in storage
-- (app.stored_objects row, bucket PRIVATE_KYC); this row holds type, hash and links. Append-only: a
-- re-submission is a new row with supersedes_id. Purging deletes the object and destroys the per-subject
-- key (crypto-shredding); this metadata row is retained as proof of what existed (evidence.purged).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.kyc_documents (
  id                     uuid        NOT NULL,
  profile_id             uuid        REFERENCES kyc.verification_profiles(id),
  kyb_organisation_id    uuid        REFERENCES kyc.kyb_organisations(id),
  organisation_person_id uuid        REFERENCES kyc.organisation_persons(id),
  beneficiary_id         uuid,                                 -- app.beneficiaries (no FK: 0012 loads later)
  kyc_case_id            uuid        REFERENCES kyc.kyc_cases(id),
  kyb_case_id            uuid        REFERENCES kyc.kyb_cases(id),
  document_type          text        NOT NULL,
  side                   text        NOT NULL DEFAULT 'NA',
  stored_object_id       uuid        NOT NULL,
  stored_object_bucket   text        NOT NULL DEFAULT 'PRIVATE_KYC',
  evidence_record_id     uuid        NOT NULL REFERENCES audit.evidence_records(id),
  content_sha256         bytea       NOT NULL,
  media_type             text        NOT NULL,
  size_bytes             bigint      NOT NULL,
  is_biometric           boolean     NOT NULL DEFAULT false,
  biometric_consent_id   uuid        REFERENCES kyc.consents(id),
  retention_class        text        NOT NULL DEFAULT 'KYC',
  supersedes_id          uuid        REFERENCES kyc.kyc_documents(id),
  uploaded_by            uuid        REFERENCES app.users(id),
  recorded_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyc_documents PRIMARY KEY (id),
  CONSTRAINT uq_kyc_documents_object UNIQUE (stored_object_id),
  -- trust boundary in the schema: a KYC document can only point at an object in the PRIVATE_KYC bucket class
  -- (composite FK onto app.stored_objects (id, bucket_class)); a public-media object can never be referenced.
  CONSTRAINT ck_kyc_documents_bucket CHECK (stored_object_bucket = 'PRIVATE_KYC'),
  CONSTRAINT fk_kyc_documents_stored_object FOREIGN KEY (stored_object_id, stored_object_bucket)
    REFERENCES app.stored_objects (id, bucket_class),
  CONSTRAINT ck_kyc_documents_subject CHECK (num_nonnulls(profile_id, kyb_organisation_id, organisation_person_id, beneficiary_id) = 1),
  CONSTRAINT ck_kyc_documents_case CHECK (num_nonnulls(kyc_case_id, kyb_case_id) <= 1),
  CONSTRAINT ck_kyc_documents_type CHECK (document_type IN ('ZW_NATIONAL_ID','ZW_PASSPORT','FOREIGN_PASSPORT','ZW_BIRTH_CERTIFICATE',
             'SELFIE_LIVENESS','PROOF_OF_ADDRESS','BANK_CONFIRMATION_LETTER','REGISTRATION_CERTIFICATE','REGISTRY_EXTRACT',
             'CONSTITUTION','TRUST_DEED','DIRECTOR_REGISTER','BO_DECLARATION','BOARD_RESOLUTION','AUTHORITY_LETTER',
             'PVO_CERTIFICATE','SECTION_8_AUTHORITY','EXCLUSION_EVIDENCE','GUARDIANSHIP_EVIDENCE','OTHER')),
  CONSTRAINT ck_kyc_documents_side CHECK (side IN ('FRONT','BACK','PHOTO_PAGE','NA')),
  CONSTRAINT ck_kyc_documents_hash CHECK (octet_length(content_sha256) = 32),
  CONSTRAINT ck_kyc_documents_media CHECK (media_type IN ('image/jpeg','image/png','application/pdf')),
  CONSTRAINT ck_kyc_documents_size CHECK (size_bytes > 0),
  CONSTRAINT ck_kyc_documents_biometric CHECK ((document_type = 'SELFIE_LIVENESS') = is_biometric),
  -- biometric processing only after a specific BIOMETRIC consent (CDPA s 12; LR-063)
  CONSTRAINT ck_kyc_documents_biometric_consent CHECK (NOT is_biometric OR biometric_consent_id IS NOT NULL),
  CONSTRAINT ck_kyc_documents_retention CHECK (retention_class IN ('KYC','CASE','CONSENT'))
);
COMMENT ON TABLE kyc.kyc_documents IS 'C3. Document metadata only; bytes in private-kyc. Append-only.';
CREATE INDEX ix_kyc_documents_profile ON kyc.kyc_documents (profile_id) WHERE profile_id IS NOT NULL;
CREATE INDEX ix_kyc_documents_kyb ON kyc.kyc_documents (kyb_organisation_id) WHERE kyb_organisation_id IS NOT NULL;
CREATE TRIGGER trg_kyc_documents_no_mutation BEFORE UPDATE OR DELETE ON kyc.kyc_documents
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_kyc_documents_no_truncate BEFORE TRUNCATE ON kyc.kyc_documents
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.kyc_documents_consent_check() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.is_biometric AND NOT EXISTS (
       SELECT 1 FROM kyc.v_consents_current c JOIN kyc.verification_profiles p ON p.user_id = c.subject_user_id
        WHERE c.id = NEW.biometric_consent_id AND c.consent_type = 'BIOMETRIC' AND p.id = NEW.profile_id) THEN
    RAISE EXCEPTION 'biometric document requires a current BIOMETRIC consent of the same person' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_kyc_documents_consent BEFORE INSERT ON kyc.kyc_documents
  FOR EACH ROW EXECUTE FUNCTION kyc.kyc_documents_consent_check();

-- -----------------------------------------------------------------------------------------------------
-- kyc.kyc_checks / kyc.kyb_checks — append-only check results (Stage 1 check_results / org_check_results).
-- A new attempt creates new rows; nothing is updated. Result vocabulary: PASS / FAIL / REVIEW / N_A
-- (an automated pipeline never records a final REJECT — that is a human kyc_decisions row, LR-071).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.kyc_checks (
  id                  uuid        NOT NULL,
  kyc_case_id         uuid        NOT NULL REFERENCES kyc.kyc_cases(id),
  profile_id          uuid        NOT NULL REFERENCES kyc.verification_profiles(id),
  check_code          text        NOT NULL,
  result              text        NOT NULL,
  valid_until         timestamptz,
  rule_version        text        NOT NULL,
  evidence_record_ids uuid[]      NOT NULL DEFAULT '{}',
  performed_by_type   text        NOT NULL,
  performed_by_id     uuid        REFERENCES app.users(id),
  vendor_check_name   text,                                -- mapped vendor name (mapping table in config)
  reason_code         text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyc_checks PRIMARY KEY (id),
  CONSTRAINT ck_kyc_checks_code CHECK (check_code IN ('EMAIL_OTP','PHONE_OTP','AGE_ATTESTATION','IDENTITY_DATA','ID_DOCUMENT',
             'FACE_MATCH_LIVENESS','MANUAL_IDENTITY_REVIEW','AGE_18_PLUS','SANCTIONS_PEP_SCREEN','DUPLICATE_IDENTITY',
             'PAYOUT_DESTINATION_OWNERSHIP','ADDRESS','PURPOSE_OF_RELATIONSHIP')),
  CONSTRAINT ck_kyc_checks_result CHECK (result IN ('PASS','FAIL','REVIEW','N_A')),
  CONSTRAINT ck_kyc_checks_performer CHECK (
       (performed_by_type = 'STAFF'  AND performed_by_id IS NOT NULL)
    OR (performed_by_type = 'VENDOR' AND performed_by_id IS NULL AND vendor_check_name IS NOT NULL)
    OR (performed_by_type = 'SYSTEM' AND performed_by_id IS NULL)),
  CONSTRAINT ck_kyc_checks_manual CHECK (check_code <> 'MANUAL_IDENTITY_REVIEW' OR performed_by_type = 'STAFF'),
  CONSTRAINT ck_kyc_checks_valid_until CHECK (valid_until IS NULL OR valid_until > created_at),
  CONSTRAINT ck_kyc_checks_reason CHECK (result = 'PASS' OR result = 'N_A' OR reason_code IS NOT NULL)
);
COMMENT ON TABLE kyc.kyc_checks IS 'C3. Append-only individual check results.';
CREATE INDEX ix_kyc_checks_profile_code ON kyc.kyc_checks (profile_id, check_code, created_at DESC);
CREATE TRIGGER trg_kyc_checks_no_mutation BEFORE UPDATE OR DELETE ON kyc.kyc_checks
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_kyc_checks_no_truncate BEFORE TRUNCATE ON kyc.kyc_checks
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.kyc_checks_case_profile() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM kyc.kyc_cases c WHERE c.id = NEW.kyc_case_id AND c.profile_id = NEW.profile_id) THEN
    RAISE EXCEPTION 'kyc check: case and profile do not match' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_kyc_checks_case_profile BEFORE INSERT ON kyc.kyc_checks
  FOR EACH ROW EXECUTE FUNCTION kyc.kyc_checks_case_profile();

CREATE TABLE kyc.kyb_checks (
  id                     uuid        NOT NULL,
  kyb_case_id            uuid        NOT NULL REFERENCES kyc.kyb_cases(id),
  kyb_organisation_id    uuid        NOT NULL REFERENCES kyc.kyb_organisations(id),
  organisation_person_id uuid        REFERENCES kyc.organisation_persons(id),   -- ORG_SCREEN_PERSONS per person
  check_code             text        NOT NULL,
  result                 text        NOT NULL,
  valid_until            timestamptz,
  rule_version           text        NOT NULL,
  evidence_record_ids    uuid[]      NOT NULL DEFAULT '{}',
  performed_by_type      text        NOT NULL,
  performed_by_id        uuid        REFERENCES app.users(id),
  reason_code            text,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyb_checks PRIMARY KEY (id),
  CONSTRAINT ck_kyb_checks_code CHECK (check_code IN ('ORG_LEGAL_IDENTITY','ORG_REGISTRATION','ORG_PURPOSE','ORG_CONTROLLERS',
             'ORG_BENEFICIAL_OWNERS','ORG_REPRESENTATIVE_AUTHORITY','ORG_SCREEN_ENTITY','ORG_SCREEN_PERSONS',
             'ORG_FUNDRAISING_AUTHORITY','ORG_PAYOUT_ACCOUNT','ORG_TAX_STATUS')),
  CONSTRAINT ck_kyb_checks_result CHECK (result IN ('PASS','FAIL','REVIEW','N_A')),
  CONSTRAINT ck_kyb_checks_person CHECK ((check_code = 'ORG_SCREEN_PERSONS') = (organisation_person_id IS NOT NULL)),
  CONSTRAINT ck_kyb_checks_performer CHECK (
       (performed_by_type = 'STAFF'  AND performed_by_id IS NOT NULL)
    OR (performed_by_type IN ('VENDOR','SYSTEM') AND performed_by_id IS NULL)),
  CONSTRAINT ck_kyb_checks_valid_until CHECK (valid_until IS NULL OR valid_until > created_at),
  CONSTRAINT ck_kyb_checks_reason CHECK (result = 'PASS' OR result = 'N_A' OR reason_code IS NOT NULL)
);
COMMENT ON TABLE kyc.kyb_checks IS 'C3. Append-only organisation check results.';
CREATE INDEX ix_kyb_checks_org_code ON kyc.kyb_checks (kyb_organisation_id, check_code, created_at DESC);
CREATE TRIGGER trg_kyb_checks_no_mutation BEFORE UPDATE OR DELETE ON kyc.kyb_checks
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_kyb_checks_no_truncate BEFORE TRUNCATE ON kyc.kyb_checks
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- kyc.kyc_decisions — append-only human review decisions (kyc-architecture §9) for KYC and KYB cases.
-- Second approver where required; never the first decider; never the subject themself.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.kyc_decisions (
  id                       uuid        NOT NULL,
  kyc_case_id              uuid        REFERENCES kyc.kyc_cases(id),
  kyb_case_id              uuid        REFERENCES kyc.kyb_cases(id),
  outcome                  text        NOT NULL,
  granted_level            text,
  resulting_status         text        NOT NULL,
  reason_code              text        NOT NULL,
  conditions               jsonb       NOT NULL DEFAULT '{}',   -- e.g. limit override record ids, EDD conditions
  checklist_evidence_id    uuid        NOT NULL REFERENCES audit.evidence_records(id),   -- REVIEW_NOTE (C3)
  decided_by               uuid        NOT NULL REFERENCES app.users(id),
  second_approval_required boolean     NOT NULL,
  second_approver_id       uuid        REFERENCES app.users(id),
  policy_version           text        NOT NULL,
  decided_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyc_decisions PRIMARY KEY (id),
  CONSTRAINT ck_kyc_decisions_case CHECK (num_nonnulls(kyc_case_id, kyb_case_id) = 1),
  CONSTRAINT ck_kyc_decisions_outcome CHECK (outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS','REQUEST_RESUBMISSION','REJECT','ESCALATE')),
  CONSTRAINT ck_kyc_decisions_level CHECK ((outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS')) = (granted_level IS NOT NULL)),
  CONSTRAINT ck_kyc_decisions_level_values CHECK (granted_level IS NULL OR granted_level IN ('BASIC_VERIFIED','IDENTITY_VERIFIED',
             'PAYOUT_VERIFIED','ORG_REGISTERED_VERIFIED','ORG_KYB_VERIFIED','ORG_PAYOUT_VERIFIED')),
  CONSTRAINT ck_kyc_decisions_level_kind CHECK (granted_level IS NULL OR
             ((kyc_case_id IS NOT NULL) = (granted_level NOT LIKE 'ORG\_%'))),
  CONSTRAINT ck_kyc_decisions_status CHECK (resulting_status IN ('ACTIVE','PENDING_REVIEW','REJECTED','SUSPENDED')),
  CONSTRAINT ck_kyc_decisions_reject_status CHECK (outcome <> 'REJECT' OR resulting_status IN ('REJECTED','SUSPENDED')),
  CONSTRAINT ck_kyc_decisions_escalate_status CHECK (outcome <> 'ESCALATE' OR resulting_status = 'PENDING_REVIEW'),
  CONSTRAINT ck_kyc_decisions_conditions CHECK (outcome <> 'APPROVE_WITH_CONDITIONS' OR conditions <> '{}'::jsonb),
  CONSTRAINT ck_kyc_decisions_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  -- fraud rejections always need a second person (kyc-architecture §9; service also sets the flag for
  -- ESCALATE resolutions linked to a campaign with funds and for HIGH-tier/minor beneficiary cases)
  CONSTRAINT ck_kyc_decisions_fraud_second CHECK (NOT (outcome = 'REJECT' AND reason_code LIKE 'FRAUD\_%') OR second_approval_required),
  CONSTRAINT ck_kyc_decisions_second_present CHECK (NOT second_approval_required OR second_approver_id IS NOT NULL),
  CONSTRAINT ck_kyc_decisions_second_distinct CHECK (second_approver_id IS NULL OR second_approver_id <> decided_by)
);
COMMENT ON TABLE kyc.kyc_decisions IS 'C3. Append-only human review decisions.';
CREATE INDEX ix_kyc_decisions_kyc_case ON kyc.kyc_decisions (kyc_case_id) WHERE kyc_case_id IS NOT NULL;
CREATE INDEX ix_kyc_decisions_kyb_case ON kyc.kyc_decisions (kyb_case_id) WHERE kyb_case_id IS NOT NULL;
CREATE TRIGGER trg_kyc_decisions_no_mutation BEFORE UPDATE OR DELETE ON kyc.kyc_decisions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_kyc_decisions_no_truncate BEFORE TRUNCATE ON kyc.kyc_decisions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
-- A reviewer never decides their own verification (operational-controls §2 object-level conflict).
CREATE FUNCTION kyc.kyc_decisions_not_self() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE subject_user uuid;
BEGIN
  IF NEW.kyc_case_id IS NOT NULL THEN
    SELECT p.user_id INTO subject_user FROM kyc.kyc_cases c JOIN kyc.verification_profiles p ON p.id = c.profile_id
     WHERE c.id = NEW.kyc_case_id;
    IF subject_user = NEW.decided_by OR subject_user = NEW.second_approver_id THEN
      RAISE EXCEPTION 'kyc decision: a reviewer cannot decide their own verification' USING ERRCODE = 'check_violation';
    END IF;
  ELSIF EXISTS (SELECT 1 FROM kyc.kyb_cases c JOIN kyc.representative_authorities r ON r.kyb_organisation_id = c.kyb_organisation_id
                 WHERE c.id = NEW.kyb_case_id AND r.revoked_at IS NULL AND r.user_id IN (NEW.decided_by, NEW.second_approver_id)) THEN
    RAISE EXCEPTION 'kyb decision: a representative of the organisation cannot decide its verification' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_kyc_decisions_not_self BEFORE INSERT ON kyc.kyc_decisions
  FOR EACH ROW EXECUTE FUNCTION kyc.kyc_decisions_not_self();

-- -----------------------------------------------------------------------------------------------------
-- kyc.beneficiary_evidence — append-only links from a beneficiary to C3 evidence (medical letters,
-- death certificates, guardianship, consent). Health data requires the beneficiary's (or guardian's)
-- HEALTH_DATA consent (beneficiary-verification §4; LR-070).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.beneficiary_evidence (
  id                 uuid        NOT NULL,
  beneficiary_id     uuid        NOT NULL,                     -- app.beneficiaries (no FK: 0012 loads later)
  campaign_id        uuid,                                     -- app.campaigns (no FK: 0014 loads later)
  evidence_type      text        NOT NULL,
  evidence_record_id uuid        NOT NULL REFERENCES audit.evidence_records(id),
  kyc_document_id    uuid        REFERENCES kyc.kyc_documents(id),
  is_health_data     boolean     NOT NULL DEFAULT false,
  consent_id         uuid        REFERENCES kyc.consents(id),
  submitted_by       uuid        NOT NULL REFERENCES app.users(id),
  submitted_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficiary_evidence PRIMARY KEY (id),
  CONSTRAINT ck_beneficiary_evidence_type CHECK (evidence_type IN ('BENEFICIARY_IDENTITY','NEED_MEDICAL','NEED_INVOICE',
             'DEATH_EVIDENCE','FUNERAL_QUOTATION','GUARDIANSHIP','LEGAL_REPRESENTATION','RELATIONSHIP_DECLARATION',
             'INSTITUTION_CONFIRMATION','GROUP_CONSTITUTION','CONSENT_EVIDENCE','OTHER')),
  CONSTRAINT ck_beneficiary_evidence_health CHECK (evidence_type <> 'NEED_MEDICAL' OR is_health_data),
  CONSTRAINT ck_beneficiary_evidence_consent CHECK (NOT is_health_data OR consent_id IS NOT NULL)
);
COMMENT ON TABLE kyc.beneficiary_evidence IS 'C3. Append-only beneficiary evidence links.';
CREATE INDEX ix_beneficiary_evidence_beneficiary ON kyc.beneficiary_evidence (beneficiary_id);
CREATE TRIGGER trg_beneficiary_evidence_no_mutation BEFORE UPDATE OR DELETE ON kyc.beneficiary_evidence
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_beneficiary_evidence_no_truncate BEFORE TRUNCATE ON kyc.beneficiary_evidence
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.beneficiary_evidence_consent_check() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.is_health_data AND NOT EXISTS (
       SELECT 1 FROM kyc.v_consents_current c
        WHERE c.id = NEW.consent_id AND c.consent_type = 'HEALTH_DATA' AND c.subject_beneficiary_id = NEW.beneficiary_id) THEN
    RAISE EXCEPTION 'health evidence requires a current HEALTH_DATA consent for the same beneficiary' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_beneficiary_evidence_consent BEFORE INSERT ON kyc.beneficiary_evidence
  FOR EACH ROW EXECUTE FUNCTION kyc.beneficiary_evidence_consent_check();

-- -----------------------------------------------------------------------------------------------------
-- kyc.gate_policies — versioned kyc_gate_policy (kyc-architecture §3.1; baseline §5.11 amendment):
-- action -> minimum verification level. Maker-checker; immutable once approved (new version instead).
-- Raising a gate is configuration; LOWERING a gate below the Stage 0 defaults (PRODUCT §7.1) needs an ADR,
-- so the floors are CHECK constraints that only a migration (with the ADR) can change.
-- verification_profiles.policy_version / kyc_decisions.policy_version name the version applied.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.gate_policies (
  id               uuid        NOT NULL,
  policy_version   text        NOT NULL,
  subject_kind     text        NOT NULL,
  action           text        NOT NULL,
  min_level        text        NOT NULL,
  status           text        NOT NULL,
  made_by          uuid        NOT NULL REFERENCES app.users(id),
  approved_by      uuid        REFERENCES app.users(id),
  approved_at      timestamptz,
  retired_by       uuid        REFERENCES app.users(id),
  retired_at       timestamptz,
  change_reason    text        NOT NULL,
  decision_ref     text        NOT NULL,                  -- DEC-/PD-/ADR- reference
  effective_from   timestamptz NOT NULL,
  effective_to     timestamptz,
  review_by        date        NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_gate_policies PRIMARY KEY (id),
  CONSTRAINT uq_gate_policies_version_action UNIQUE (policy_version, subject_kind, action),
  CONSTRAINT ck_gate_policies_kind CHECK (subject_kind IN ('INDIVIDUAL','ORGANISATION')),
  CONSTRAINT ck_gate_policies_action CHECK (action IN ('DONATE','CREATE_DRAFT','SUBMIT_CAMPAIGN','PUBLISH_CAMPAIGN','WITHDRAW','REPRESENT_ORG')),
  CONSTRAINT ck_gate_policies_level CHECK (
       (subject_kind = 'INDIVIDUAL'   AND min_level IN ('UNVERIFIED','BASIC_VERIFIED','IDENTITY_VERIFIED','PAYOUT_VERIFIED'))
    OR (subject_kind = 'ORGANISATION' AND min_level IN ('ORG_UNVERIFIED','ORG_REGISTERED_VERIFIED','ORG_KYB_VERIFIED','ORG_PAYOUT_VERIFIED'))),
  -- Stage 0 floors (PRODUCT §7.1, kyb-architecture §2): lowering needs an ADR + migration
  CONSTRAINT ck_gate_policies_floor CHECK (
       (subject_kind = 'INDIVIDUAL' AND (
            (action = 'DONATE')
         OR (action = 'CREATE_DRAFT' AND min_level IN ('BASIC_VERIFIED','IDENTITY_VERIFIED','PAYOUT_VERIFIED'))
         OR (action IN ('SUBMIT_CAMPAIGN','PUBLISH_CAMPAIGN','REPRESENT_ORG') AND min_level IN ('IDENTITY_VERIFIED','PAYOUT_VERIFIED'))
         OR (action = 'WITHDRAW' AND min_level = 'PAYOUT_VERIFIED')))
    OR (subject_kind = 'ORGANISATION' AND (
            (action = 'CREATE_DRAFT')
         OR (action IN ('SUBMIT_CAMPAIGN','PUBLISH_CAMPAIGN') AND min_level IN ('ORG_REGISTERED_VERIFIED','ORG_KYB_VERIFIED','ORG_PAYOUT_VERIFIED'))
         OR (action = 'WITHDRAW' AND min_level = 'ORG_PAYOUT_VERIFIED')))),
  CONSTRAINT ck_gate_policies_status CHECK (status IN ('PROPOSED','APPROVED','REJECTED','RETIRED')),
  CONSTRAINT ck_gate_policies_maker_checker CHECK (approved_by IS NULL OR approved_by <> made_by),
  CONSTRAINT ck_gate_policies_approved CHECK ((status IN ('APPROVED','RETIRED','REJECTED')) = (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  CONSTRAINT ck_gate_policies_retired CHECK ((status = 'RETIRED') = (retired_by IS NOT NULL AND retired_at IS NOT NULL)),
  CONSTRAINT ck_gate_policies_decision_ref CHECK (decision_ref ~ '^(DEC-[A-Za-z0-9-]+|PD-[0-9]{2}|ADR-[0-9]{3})$'),
  CONSTRAINT ck_gate_policies_window CHECK (effective_to IS NULL OR effective_to > effective_from)
);
COMMENT ON TABLE kyc.gate_policies IS 'C1. Versioned verification gates (kyc_gate_policy); maker-checker.';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('kyc_gate_policy', '',         'PROPOSED'),
  ('kyc_gate_policy', 'PROPOSED', 'APPROVED'),
  ('kyc_gate_policy', 'PROPOSED', 'REJECTED'),
  ('kyc_gate_policy', 'APPROVED', 'RETIRED');
CREATE TRIGGER trg_gate_policies_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.gate_policies
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('kyc_gate_policy');
CREATE TRIGGER trg_gate_policies_no_delete BEFORE DELETE ON kyc.gate_policies
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_gate_policies_no_truncate BEFORE TRUNCATE ON kyc.gate_policies
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.gate_policies_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT ((OLD.status = 'PROPOSED' AND changed <@ ARRAY['status','approved_by','approved_at'])
          OR (OLD.status = 'APPROVED' AND NEW.status = 'RETIRED' AND changed <@ ARRAY['status','retired_by','retired_at'])) THEN
    RAISE EXCEPTION 'gate policy %: immutable once decided (attempted: %) — create a new policy_version', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_gate_policies_guard BEFORE UPDATE ON kyc.gate_policies
  FOR EACH ROW EXECUTE FUNCTION kyc.gate_policies_guard();

-- -----------------------------------------------------------------------------------------------------
-- kyc.vendor_callback_inbox — identity-vendor callbacks (kyc-architecture §10: same rules as payment
-- webhooks). Only signature-verified callbacks are stored (failures are security_audit_events, not rows).
-- Dedup UNIQUE(vendor_code, vendor_event_id). The raw body may carry extracted identity data, so it is
-- stored envelope-encrypted (C3) with its hash; raw columns are append-only, only processing state moves.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.vendor_callback_inbox (
  id                     uuid        NOT NULL,
  vendor_code            text        NOT NULL,
  vendor_event_id        text        NOT NULL,
  event_type             text        NOT NULL,
  vendor_session_ref     text,
  signature_verified     boolean     NOT NULL,
  verification_method    text        NOT NULL,
  signing_key_ref        text        NOT NULL,              -- which webhook secret/key version verified it (never the secret)
  raw_payload_ciphertext bytea       NOT NULL,
  raw_payload_key_id     text        NOT NULL,
  raw_payload_sha256     bytea       NOT NULL,
  received_at            timestamptz NOT NULL DEFAULT now(),
  processing_status      text        NOT NULL DEFAULT 'RECEIVED',
  attempts               integer     NOT NULL DEFAULT 0,
  last_error_code        text,
  processed_at           timestamptz,
  kyc_case_id            uuid        REFERENCES kyc.kyc_cases(id),
  CONSTRAINT pk_vendor_callback_inbox PRIMARY KEY (id),
  CONSTRAINT uq_vendor_callback_inbox_event UNIQUE (vendor_code, vendor_event_id),
  CONSTRAINT ck_vendor_callback_inbox_verified CHECK (signature_verified),
  CONSTRAINT ck_vendor_callback_inbox_method CHECK (verification_method IN ('HMAC','ASYMMETRIC_SIGNATURE','AUTHENTICATED_STATUS_QUERY')),
  CONSTRAINT ck_vendor_callback_inbox_hash CHECK (octet_length(raw_payload_sha256) = 32 AND octet_length(raw_payload_ciphertext) >= 28),
  CONSTRAINT ck_vendor_callback_inbox_status CHECK (processing_status IN ('RECEIVED','PROCESSED','IGNORED_UNKNOWN_TYPE','FAILED')),
  CONSTRAINT ck_vendor_callback_inbox_processed CHECK ((processing_status IN ('PROCESSED','IGNORED_UNKNOWN_TYPE')) = (processed_at IS NOT NULL)),
  CONSTRAINT ck_vendor_callback_inbox_attempts CHECK (attempts >= 0)
);
COMMENT ON TABLE kyc.vendor_callback_inbox IS 'C3. Verified, deduplicated KYC vendor callbacks; raw body encrypted and immutable.';
CREATE INDEX ix_vendor_callback_inbox_pending ON kyc.vendor_callback_inbox (received_at) WHERE processing_status IN ('RECEIVED','FAILED');
CREATE TRIGGER trg_vendor_callback_inbox_no_delete BEFORE DELETE ON kyc.vendor_callback_inbox
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_vendor_callback_inbox_no_truncate BEFORE TRUNCATE ON kyc.vendor_callback_inbox
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.vendor_callback_inbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT changed <@ ARRAY['processing_status','attempts','last_error_code','processed_at','kyc_case_id']
     OR OLD.processing_status IN ('PROCESSED','IGNORED_UNKNOWN_TYPE') THEN
    RAISE EXCEPTION 'vendor callback %: raw callback is append-only; processing is final once done (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_vendor_callback_inbox_guard BEFORE UPDATE ON kyc.vendor_callback_inbox
  FOR EACH ROW EXECUTE FUNCTION kyc.vendor_callback_inbox_guard();
