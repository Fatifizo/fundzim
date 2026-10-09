-- FundZim migration: schema `kyc` (owner module: kyc; Stage 2 draft 0010 adapted to ADR-034 vocabularies).
-- C3 RESTRICTED: only the fundzim_kyc role has privileges here (runtime grants v3); audit, evidence and
-- outbox writes go through the gateways (ADR-035). C3 attributes are stored ONLY as AES-256-GCM ciphertext
-- (`*_ciphertext bytea` + `*_key_id`) with HMAC-SHA-256 blind indexes (`*_bidx`) where equality search is
-- needed: there is no plaintext identity-number, date-of-birth, name or address column.
-- Cross-schema FKs (kyc -> app.users, app.organisations, app.stored_objects, audit.evidence_records) are
-- checked as the table owner. beneficiary / payout-destination ids are plain uuids (higher-layer modules).
-- Not built in Stage 5 (documented in docs/stage-5/known-issues.md): fundraising_authorities (PVO s 8,
-- needed by campaigns in Stage 6) and vendor_callback_inbox (no identity vendor selected, PD-25).
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- shared verification-case machine (ADR-034 §1): KYC, KYB and beneficiary cases
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('verification_case', '',                                'DRAFT'),
  ('verification_case', 'DRAFT',                           'SUBMITTED'),
  ('verification_case', 'DRAFT',                           'WITHDRAWN'),
  ('verification_case', 'SUBMITTED',                       'UNDER_REVIEW'),
  ('verification_case', 'SUBMITTED',                       'WITHDRAWN'),
  ('verification_case', 'UNDER_REVIEW',                    'ADDITIONAL_INFORMATION_REQUIRED'),
  ('verification_case', 'UNDER_REVIEW',                    'ESCALATED'),
  ('verification_case', 'UNDER_REVIEW',                    'APPROVED'),
  ('verification_case', 'UNDER_REVIEW',                    'REJECTED'),
  ('verification_case', 'ADDITIONAL_INFORMATION_REQUIRED', 'SUBMITTED'),
  ('verification_case', 'ADDITIONAL_INFORMATION_REQUIRED', 'WITHDRAWN'),
  ('verification_case', 'ADDITIONAL_INFORMATION_REQUIRED', 'EXPIRED'),
  ('verification_case', 'ESCALATED',                       'UNDER_REVIEW'),
  ('verification_case', 'ESCALATED',                       'APPROVED'),
  ('verification_case', 'ESCALATED',                       'REJECTED'),
  ('verification_case', 'APPROVED',                        'EXPIRED'),
  ('verification_case', 'APPROVED',                        'SUSPENDED'),
  ('verification_case', 'APPROVED',                        'REVOKED'),
  ('verification_case', 'SUSPENDED',                       'APPROVED'),
  ('verification_case', 'SUSPENDED',                       'UNDER_REVIEW'),
  ('verification_case', 'SUSPENDED',                       'REVOKED'),
  ('kyc_verification_status', '',               'ACTIVE'),
  ('kyc_verification_status', 'ACTIVE',         'PENDING_REVIEW'),
  ('kyc_verification_status', 'ACTIVE',         'SUSPENDED'),
  ('kyc_verification_status', 'ACTIVE',         'REJECTED'),
  ('kyc_verification_status', 'PENDING_REVIEW', 'ACTIVE'),
  ('kyc_verification_status', 'PENDING_REVIEW', 'REJECTED'),
  ('kyc_verification_status', 'PENDING_REVIEW', 'SUSPENDED'),
  ('kyc_verification_status', 'REJECTED',       'PENDING_REVIEW'),
  ('kyc_verification_status', 'REJECTED',       'ACTIVE'),
  ('kyc_verification_status', 'SUSPENDED',      'ACTIVE'),
  ('kyc_verification_status', 'SUSPENDED',      'REJECTED'),
  ('kyc_verification_status', 'SUSPENDED',      'PENDING_REVIEW'),
  ('verification_policy', '',         'PROPOSED'),
  ('verification_policy', 'PROPOSED', 'APPROVED'),
  ('verification_policy', 'PROPOSED', 'REJECTED'),
  ('verification_policy', 'APPROVED', 'RETIRED');

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

-- -----------------------------------------------------------------------------------------------------
-- kyc.verification_profiles — one per user: level and status are orthogonal (kyc-architecture §3); every
-- version has one profile_events row (deferred check). Classification: C2 (level/status).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.verification_profiles (
  id                uuid        NOT NULL,
  user_id           uuid        NOT NULL REFERENCES app.users (id),
  level             text        NOT NULL DEFAULT 'UNVERIFIED',
  status            text        NOT NULL DEFAULT 'ACTIVE',
  risk_level        text        NOT NULL DEFAULT 'STANDARD',
  pep_status        text        NOT NULL DEFAULT 'UNKNOWN',
  level_granted_at  timestamptz,
  level_expires_at  timestamptz,
  status_changed_at timestamptz NOT NULL DEFAULT now(),
  policy_version    text        NOT NULL,
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_verification_profiles PRIMARY KEY (id),
  CONSTRAINT uq_verification_profiles_user UNIQUE (user_id),
  CONSTRAINT ck_verification_profiles_level CHECK (level IN ('UNVERIFIED','BASIC_VERIFIED','IDENTITY_VERIFIED','PAYOUT_VERIFIED')),
  CONSTRAINT ck_verification_profiles_status CHECK (status IN ('ACTIVE','PENDING_REVIEW','REJECTED','SUSPENDED')),
  CONSTRAINT ck_verification_profiles_risk CHECK (risk_level IN ('LOW','STANDARD','ENHANCED','RESTRICTED')),
  CONSTRAINT ck_verification_profiles_pep CHECK (pep_status IN ('NONE','PEP','PEP_RCA','UNKNOWN')),
  CONSTRAINT ck_verification_profiles_pep_risk CHECK (pep_status NOT IN ('PEP','PEP_RCA') OR risk_level IN ('ENHANCED','RESTRICTED')),
  CONSTRAINT ck_verification_profiles_granted CHECK (level = 'UNVERIFIED' OR level_granted_at IS NOT NULL),
  CONSTRAINT ck_verification_profiles_version CHECK (version >= 1)
);
CREATE TRIGGER trg_verification_profiles_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('kyc_verification_status');
CREATE TRIGGER trg_verification_profiles_updated_at BEFORE UPDATE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_verification_profiles_version BEFORE UPDATE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION kyc.versioned_row_guard();
CREATE TRIGGER trg_verification_profiles_immutable BEFORE UPDATE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('user_id', 'created_at');
CREATE TRIGGER trg_verification_profiles_no_delete BEFORE DELETE ON kyc.verification_profiles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- kyc.kyb_organisations — KYB record per organisation (same overlay). Legal details are copied here from the
-- approved KYB case. org_type uses the implemented organisations vocabulary (ADR-034 §7).
CREATE TABLE kyc.kyb_organisations (
  id                            uuid        NOT NULL,
  organisation_id               uuid        NOT NULL REFERENCES app.organisations (id),
  org_type                      text        NOT NULL,
  legal_name                    text,                                    -- C2: registered name (public once verified)
  trading_name                  text,
  registration_number           text,                                    -- C2: registry number, not a personal identifier
  registry                      text,
  country_of_registration       char(2),
  registered_address_ciphertext bytea,                                   -- C3
  registered_address_key_id     text,
  level                         text        NOT NULL DEFAULT 'ORG_UNVERIFIED',
  status                        text        NOT NULL DEFAULT 'ACTIVE',
  risk_level                    text        NOT NULL DEFAULT 'STANDARD',
  level_granted_at              timestamptz,
  level_expires_at              timestamptz,
  status_changed_at             timestamptz NOT NULL DEFAULT now(),
  policy_version                text        NOT NULL,
  version                       integer     NOT NULL DEFAULT 1,
  created_at                    timestamptz NOT NULL DEFAULT now(),
  updated_at                    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyb_organisations PRIMARY KEY (id),
  CONSTRAINT uq_kyb_organisations_org UNIQUE (organisation_id),
  CONSTRAINT ck_kyb_organisations_type CHECK (org_type IN ('COMPANY','TRUST','PVO','FAITH_BASED','SCHOOL','HEALTH_INSTITUTION',
                                                           'COMMUNITY_BASED','SPORTS_CLUB','OTHER')),
  CONSTRAINT ck_kyb_organisations_registry CHECK (registry IS NULL OR registry IN ('COMPANIES_REGISTRY','PVO_REGISTRAR','HIGH_COURT',
                                                  'DEEDS_REGISTRY','MINISTRY_EDUCATION','HEALTH_REGISTRY','OTHER')),
  CONSTRAINT ck_kyb_organisations_country CHECK (country_of_registration IS NULL OR country_of_registration ~ '^[A-Z]{2}$'),
  CONSTRAINT ck_kyb_organisations_address_pair CHECK ((registered_address_ciphertext IS NULL) = (registered_address_key_id IS NULL)
                                                      AND (registered_address_ciphertext IS NULL OR octet_length(registered_address_ciphertext) >= 28)),
  CONSTRAINT ck_kyb_organisations_level CHECK (level IN ('ORG_UNVERIFIED','ORG_REGISTERED_VERIFIED','ORG_KYB_VERIFIED','ORG_PAYOUT_VERIFIED')),
  CONSTRAINT ck_kyb_organisations_status CHECK (status IN ('ACTIVE','PENDING_REVIEW','REJECTED','SUSPENDED')),
  CONSTRAINT ck_kyb_organisations_risk CHECK (risk_level IN ('LOW','STANDARD','ENHANCED','RESTRICTED')),
  CONSTRAINT ck_kyb_organisations_granted CHECK (level = 'ORG_UNVERIFIED' OR (level_granted_at IS NOT NULL AND legal_name IS NOT NULL)),
  CONSTRAINT ck_kyb_organisations_version CHECK (version >= 1)
);
CREATE TRIGGER trg_kyb_organisations_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('kyc_verification_status');
CREATE TRIGGER trg_kyb_organisations_updated_at BEFORE UPDATE ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_kyb_organisations_version BEFORE UPDATE ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION kyc.versioned_row_guard();
CREATE TRIGGER trg_kyb_organisations_immutable BEFORE UPDATE ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('organisation_id', 'created_at');
CREATE TRIGGER trg_kyb_organisations_no_delete BEFORE DELETE ON kyc.kyb_organisations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- kyc.identities — encrypted identity attributes. Content immutable; corrections supersede. PENDING = submitted
-- with a case, not yet approved; ACTIVE = the approved identity (one per profile); duplicate control: one
-- ACTIVE account-holder identity per (id type, blind index).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.identities (
  id                       uuid        NOT NULL,
  subject_kind             text        NOT NULL,
  profile_id               uuid        REFERENCES kyc.verification_profiles (id),
  status                   text        NOT NULL DEFAULT 'PENDING',
  legal_name_ciphertext    bytea       NOT NULL,                 -- JSON {first, last} (or {full})
  legal_name_key_id        text        NOT NULL,
  dob_ciphertext           bytea,
  dob_key_id               text,
  nationality              char(2),
  residence_country        char(2),
  id_type                  text,
  id_number_ciphertext     bytea,
  id_number_key_id         text,
  id_number_bidx           bytea,
  bidx_key_id              text,
  id_number_masked         text,
  doc_expiry               date,
  address_ciphertext       bytea,
  address_key_id           text,
  source                   text        NOT NULL,
  supersedes_id            uuid        REFERENCES kyc.identities (id),
  shredded_at              timestamptz,
  created_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_identities PRIMARY KEY (id),
  CONSTRAINT ck_identities_kind CHECK (subject_kind IN ('ACCOUNT_HOLDER','KYB_PERSON','BENEFICIARY')),
  CONSTRAINT ck_identities_profile CHECK ((subject_kind = 'ACCOUNT_HOLDER') = (profile_id IS NOT NULL)),
  CONSTRAINT ck_identities_status CHECK (status IN ('PENDING','ACTIVE','SUPERSEDED','INACTIVE','SHREDDED')),
  CONSTRAINT ck_identities_shredded CHECK ((status = 'SHREDDED') = (shredded_at IS NOT NULL)),
  CONSTRAINT ck_identities_name_ct CHECK (octet_length(legal_name_ciphertext) >= 28),
  CONSTRAINT ck_identities_dob_pair CHECK ((dob_ciphertext IS NULL) = (dob_key_id IS NULL)
                                           AND (dob_ciphertext IS NULL OR octet_length(dob_ciphertext) >= 28)),
  CONSTRAINT ck_identities_addr_pair CHECK ((address_ciphertext IS NULL) = (address_key_id IS NULL)
                                            AND (address_ciphertext IS NULL OR octet_length(address_ciphertext) >= 28)),
  CONSTRAINT ck_identities_id_type CHECK (id_type IS NULL OR id_type IN ('ZW_NATIONAL_ID','ZW_PASSPORT','FOREIGN_PASSPORT','ZW_BIRTH_CERTIFICATE')),
  CONSTRAINT ck_identities_id_number_shape CHECK (
       (id_type IS NULL AND id_number_ciphertext IS NULL AND id_number_key_id IS NULL AND id_number_bidx IS NULL
        AND bidx_key_id IS NULL AND id_number_masked IS NULL)
    OR (id_type IS NOT NULL AND id_number_ciphertext IS NOT NULL AND id_number_key_id IS NOT NULL
        AND id_number_bidx IS NOT NULL AND bidx_key_id IS NOT NULL AND octet_length(id_number_ciphertext) >= 28)),
  CONSTRAINT ck_identities_bidx_len CHECK (id_number_bidx IS NULL OR octet_length(id_number_bidx) = 32),
  CONSTRAINT ck_identities_masked CHECK (id_number_masked IS NULL OR id_number_masked ~ '^\*{3,}[A-Z0-9]{0,4}$'),
  CONSTRAINT ck_identities_countries CHECK ((nationality IS NULL OR nationality ~ '^[A-Z]{2}$')
                                            AND (residence_country IS NULL OR residence_country ~ '^[A-Z]{2}$')),
  CONSTRAINT ck_identities_source CHECK (source IN ('USER_ENTRY','STAFF_ENTRY','OWNER_DECLARATION'))
);
CREATE UNIQUE INDEX uq_identities_active_id_bidx ON kyc.identities (id_type, id_number_bidx)
  WHERE status = 'ACTIVE' AND subject_kind = 'ACCOUNT_HOLDER' AND id_number_bidx IS NOT NULL;
CREATE UNIQUE INDEX uq_identities_active_profile ON kyc.identities (profile_id) WHERE status = 'ACTIVE' AND profile_id IS NOT NULL;
CREATE UNIQUE INDEX uq_identities_supersedes ON kyc.identities (supersedes_id) WHERE supersedes_id IS NOT NULL;
CREATE INDEX ix_identities_bidx_any ON kyc.identities (id_type, id_number_bidx) WHERE id_number_bidx IS NOT NULL;
CREATE TRIGGER trg_identities_no_delete BEFORE DELETE ON kyc.identities
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.identities_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT changed <@ ARRAY['status','shredded_at'] THEN
    RAISE EXCEPTION 'kyc.identities: content is immutable (attempted: %) — insert a superseding row', changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.status <> OLD.status AND NOT (
       (OLD.status = 'PENDING' AND NEW.status IN ('ACTIVE','INACTIVE','SUPERSEDED','SHREDDED'))
    OR (OLD.status = 'ACTIVE' AND NEW.status IN ('SUPERSEDED','INACTIVE','SHREDDED'))
    OR (OLD.status IN ('SUPERSEDED','INACTIVE') AND NEW.status = 'SHREDDED')) THEN
    RAISE EXCEPTION 'kyc.identities: illegal lifecycle change % -> %', OLD.status, NEW.status USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_identities_guard BEFORE UPDATE ON kyc.identities
  FOR EACH ROW EXECUTE FUNCTION kyc.identities_guard();

-- -----------------------------------------------------------------------------------------------------
-- kyc.kyc_cases / kyc.kyb_cases — verification cases (ADR-034 §1). Draft details live encrypted on the case
-- (draft_ciphertext: JSON of the fields the subject entered) until submission, when an identity row (KYC) or
-- the case's KYB details are frozen for review. Every version has one kyc.case_events row (deferred check).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.kyc_cases (
  id                       uuid        NOT NULL,
  profile_id               uuid        NOT NULL REFERENCES kyc.verification_profiles (id),
  origin                   text        NOT NULL DEFAULT 'VERIFICATION_ATTEMPT',
  target_level             text        NOT NULL,
  status                   text        NOT NULL,
  identity_id              uuid        REFERENCES kyc.identities (id),
  draft_ciphertext         bytea,
  draft_key_id             text,
  risk_level               text        NOT NULL DEFAULT 'STANDARD',
  requires_second_approval boolean     NOT NULL DEFAULT false,
  policy_version           text        NOT NULL,
  assigned_to              uuid        REFERENCES app.users (id),
  assigned_at              timestamptz,
  pending_outcome          text,                                   -- first approval awaiting a second approver
  pending_decided_by       uuid        REFERENCES app.users (id),
  pending_reason_code      text,
  pending_evidence_id      uuid        REFERENCES audit.evidence_records (id),
  submitted_at             timestamptz,
  decided_at               timestamptz,
  expires_at               timestamptz,                            -- APPROVED verification lapses (policy validity)
  closed_at                timestamptz,
  version                  integer     NOT NULL DEFAULT 1,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyc_cases PRIMARY KEY (id),
  CONSTRAINT ck_kyc_cases_origin CHECK (origin IN ('VERIFICATION_ATTEMPT','REVERIFICATION_TRIGGER','COMPLIANCE_REFERRAL')),
  CONSTRAINT ck_kyc_cases_target CHECK (target_level IN ('BASIC_VERIFIED','IDENTITY_VERIFIED','PAYOUT_VERIFIED')),
  CONSTRAINT ck_kyc_cases_status CHECK (status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','ADDITIONAL_INFORMATION_REQUIRED','ESCALATED',
                                                   'APPROVED','REJECTED','EXPIRED','SUSPENDED','REVOKED','WITHDRAWN')),
  CONSTRAINT ck_kyc_cases_draft_pair CHECK ((draft_ciphertext IS NULL) = (draft_key_id IS NULL)),
  CONSTRAINT ck_kyc_cases_risk CHECK (risk_level IN ('LOW','STANDARD','ENHANCED','RESTRICTED')),
  CONSTRAINT ck_kyc_cases_assigned CHECK ((assigned_to IS NULL) = (assigned_at IS NULL)),
  CONSTRAINT ck_kyc_cases_review_assigned CHECK (status NOT IN ('UNDER_REVIEW','ESCALATED') OR assigned_to IS NOT NULL),
  CONSTRAINT ck_kyc_cases_submitted CHECK (status IN ('DRAFT','WITHDRAWN') OR submitted_at IS NOT NULL),
  CONSTRAINT ck_kyc_cases_identity CHECK (status IN ('DRAFT','WITHDRAWN') OR identity_id IS NOT NULL),
  CONSTRAINT ck_kyc_cases_decided CHECK ((status IN ('APPROVED','REJECTED','EXPIRED','SUSPENDED','REVOKED')) <= (decided_at IS NOT NULL)),
  CONSTRAINT ck_kyc_cases_closed CHECK ((status IN ('REJECTED','EXPIRED','REVOKED','WITHDRAWN')) = (closed_at IS NOT NULL)),
  CONSTRAINT ck_kyc_cases_pending CHECK ((pending_outcome IS NULL) = (pending_decided_by IS NULL)
                                         AND (pending_outcome IS NULL OR pending_outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS','REJECT'))),
  CONSTRAINT ck_kyc_cases_version CHECK (version >= 1)
);
-- at most one open (non-final) case per profile; concurrent "start verification" requests collide here
CREATE UNIQUE INDEX uq_kyc_cases_open_profile ON kyc.kyc_cases (profile_id)
  WHERE status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','ADDITIONAL_INFORMATION_REQUIRED','ESCALATED');
CREATE INDEX ix_kyc_cases_queue ON kyc.kyc_cases (status, submitted_at) WHERE status IN ('SUBMITTED','UNDER_REVIEW','ESCALATED');
CREATE INDEX ix_kyc_cases_profile ON kyc.kyc_cases (profile_id, created_at DESC);
CREATE TRIGGER trg_kyc_cases_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('verification_case');
CREATE TRIGGER trg_kyc_cases_version BEFORE UPDATE ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION kyc.versioned_row_guard();
CREATE TRIGGER trg_kyc_cases_updated_at BEFORE UPDATE ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_kyc_cases_immutable BEFORE UPDATE ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('profile_id', 'origin', 'target_level', 'created_at');
CREATE TRIGGER trg_kyc_cases_no_delete BEFORE DELETE ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE kyc.kyb_cases (
  id                            uuid        NOT NULL,
  kyb_organisation_id           uuid        NOT NULL REFERENCES kyc.kyb_organisations (id),
  origin                        text        NOT NULL DEFAULT 'VERIFICATION_ATTEMPT',
  target_level                  text        NOT NULL,
  status                        text        NOT NULL,
  created_by                    uuid        NOT NULL REFERENCES app.users (id),
  submitted_by                  uuid        REFERENCES app.users (id),     -- the representative who submitted
  registered_name               text,
  trading_name                  text,
  registration_number           text,
  registry                      text,
  country_of_registration       char(2),
  registered_address_ciphertext bytea,
  registered_address_key_id     text,
  risk_level                    text        NOT NULL DEFAULT 'STANDARD',
  requires_second_approval      boolean     NOT NULL DEFAULT false,
  policy_version                text        NOT NULL,
  assigned_to                   uuid        REFERENCES app.users (id),
  assigned_at                   timestamptz,
  pending_outcome               text,
  pending_decided_by            uuid        REFERENCES app.users (id),
  pending_reason_code           text,
  pending_evidence_id           uuid        REFERENCES audit.evidence_records (id),
  submitted_at                  timestamptz,
  decided_at                    timestamptz,
  expires_at                    timestamptz,
  closed_at                     timestamptz,
  version                       integer     NOT NULL DEFAULT 1,
  created_at                    timestamptz NOT NULL DEFAULT now(),
  updated_at                    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyb_cases PRIMARY KEY (id),
  CONSTRAINT ck_kyb_cases_origin CHECK (origin IN ('VERIFICATION_ATTEMPT','CHANGE_NOTIFICATION','PERIODIC','COMPLIANCE_REFERRAL')),
  CONSTRAINT ck_kyb_cases_target CHECK (target_level IN ('ORG_REGISTERED_VERIFIED','ORG_KYB_VERIFIED','ORG_PAYOUT_VERIFIED')),
  CONSTRAINT ck_kyb_cases_status CHECK (status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','ADDITIONAL_INFORMATION_REQUIRED','ESCALATED',
                                                   'APPROVED','REJECTED','EXPIRED','SUSPENDED','REVOKED','WITHDRAWN')),
  CONSTRAINT ck_kyb_cases_registry CHECK (registry IS NULL OR registry IN ('COMPANIES_REGISTRY','PVO_REGISTRAR','HIGH_COURT',
                                          'DEEDS_REGISTRY','MINISTRY_EDUCATION','HEALTH_REGISTRY','OTHER')),
  CONSTRAINT ck_kyb_cases_country CHECK (country_of_registration IS NULL OR country_of_registration ~ '^[A-Z]{2}$'),
  CONSTRAINT ck_kyb_cases_address_pair CHECK ((registered_address_ciphertext IS NULL) = (registered_address_key_id IS NULL)),
  CONSTRAINT ck_kyb_cases_risk CHECK (risk_level IN ('LOW','STANDARD','ENHANCED','RESTRICTED')),
  CONSTRAINT ck_kyb_cases_assigned CHECK ((assigned_to IS NULL) = (assigned_at IS NULL)),
  CONSTRAINT ck_kyb_cases_review_assigned CHECK (status NOT IN ('UNDER_REVIEW','ESCALATED') OR assigned_to IS NOT NULL),
  CONSTRAINT ck_kyb_cases_submitted CHECK (status IN ('DRAFT','WITHDRAWN') OR (submitted_at IS NOT NULL AND submitted_by IS NOT NULL
                                           AND registered_name IS NOT NULL)),
  CONSTRAINT ck_kyb_cases_decided CHECK ((status IN ('APPROVED','REJECTED','EXPIRED','SUSPENDED','REVOKED')) <= (decided_at IS NOT NULL)),
  CONSTRAINT ck_kyb_cases_closed CHECK ((status IN ('REJECTED','EXPIRED','REVOKED','WITHDRAWN')) = (closed_at IS NOT NULL)),
  CONSTRAINT ck_kyb_cases_pending CHECK ((pending_outcome IS NULL) = (pending_decided_by IS NULL)
                                         AND (pending_outcome IS NULL OR pending_outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS','REJECT'))),
  CONSTRAINT ck_kyb_cases_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX uq_kyb_cases_open_org ON kyc.kyb_cases (kyb_organisation_id)
  WHERE status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','ADDITIONAL_INFORMATION_REQUIRED','ESCALATED');
CREATE INDEX ix_kyb_cases_queue ON kyc.kyb_cases (status, submitted_at) WHERE status IN ('SUBMITTED','UNDER_REVIEW','ESCALATED');
CREATE TRIGGER trg_kyb_cases_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('verification_case');
CREATE TRIGGER trg_kyb_cases_version BEFORE UPDATE ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION kyc.versioned_row_guard();
CREATE TRIGGER trg_kyb_cases_updated_at BEFORE UPDATE ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_kyb_cases_immutable BEFORE UPDATE ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('kyb_organisation_id', 'origin', 'target_level', 'created_by', 'created_at');
CREATE TRIGGER trg_kyb_cases_no_delete BEFORE DELETE ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
-- KYB details are frozen once submitted (resubmission after an information request unfreezes them through the
-- ADDITIONAL_INFORMATION_REQUIRED state only).
CREATE FUNCTION kyc.kyb_cases_details_frozen() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status NOT IN ('DRAFT','ADDITIONAL_INFORMATION_REQUIRED')
     AND (NEW.registered_name, NEW.trading_name, NEW.registration_number, NEW.registry, NEW.country_of_registration,
          NEW.registered_address_ciphertext) IS DISTINCT FROM
         (OLD.registered_name, OLD.trading_name, OLD.registration_number, OLD.registry, OLD.country_of_registration,
          OLD.registered_address_ciphertext) THEN
    RAISE EXCEPTION 'kyb case %: details are frozen in status %', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_kyb_cases_details_frozen BEFORE UPDATE ON kyc.kyb_cases
  FOR EACH ROW EXECUTE FUNCTION kyc.kyb_cases_details_frozen();
CREATE FUNCTION kyc.kyc_cases_draft_frozen() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status NOT IN ('DRAFT','ADDITIONAL_INFORMATION_REQUIRED')
     AND (NEW.draft_ciphertext IS DISTINCT FROM OLD.draft_ciphertext OR NEW.identity_id IS DISTINCT FROM OLD.identity_id) THEN
    RAISE EXCEPTION 'kyc case %: submitted details are frozen in status %', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_kyc_cases_draft_frozen BEFORE UPDATE ON kyc.kyc_cases
  FOR EACH ROW EXECUTE FUNCTION kyc.kyc_cases_draft_frozen();

-- kyc.case_events — append-only case timeline for KYC and KYB cases; exactly one row per case version.
CREATE TABLE kyc.case_events (
  id            uuid        NOT NULL,
  kyc_case_id   uuid        REFERENCES kyc.kyc_cases (id),
  kyb_case_id   uuid        REFERENCES kyc.kyb_cases (id),
  case_version  integer     NOT NULL,
  event_type    text        NOT NULL,
  from_status   text,
  to_status     text        NOT NULL,
  actor_type    text        NOT NULL,
  actor_id      uuid        REFERENCES app.users (id),
  reason_code   text,
  payload       jsonb       NOT NULL DEFAULT '{}',          -- codes and ids only
  occurred_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_case_events PRIMARY KEY (id),
  CONSTRAINT ck_case_events_case CHECK (num_nonnulls(kyc_case_id, kyb_case_id) = 1),
  CONSTRAINT ck_case_events_type CHECK (event_type IN ('CREATED','UPDATED','DOCUMENT_ADDED','DOCUMENT_REMOVED','SUBMITTED','ASSIGNED',
             'REVIEW_STARTED','INFORMATION_REQUESTED','RESUBMITTED','ESCALATED','RETURNED','FIRST_APPROVAL','APPROVED','REJECTED',
             'SUSPENDED','REINSTATED','REOPENED','REVOKED','EXPIRED','WITHDRAWN')),
  CONSTRAINT ck_case_events_actor CHECK ((actor_type IN ('USER','STAFF') AND actor_id IS NOT NULL) OR (actor_type = 'SYSTEM' AND actor_id IS NULL)),
  CONSTRAINT ck_case_events_reason CHECK (reason_code IS NULL OR reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_case_events_payload CHECK (jsonb_typeof(payload) = 'object')
);
CREATE UNIQUE INDEX uq_case_events_kyc_version ON kyc.case_events (kyc_case_id, case_version) WHERE kyc_case_id IS NOT NULL;
CREATE UNIQUE INDEX uq_case_events_kyb_version ON kyc.case_events (kyb_case_id, case_version) WHERE kyb_case_id IS NOT NULL;
CREATE TRIGGER trg_case_events_no_mutation BEFORE UPDATE OR DELETE ON kyc.case_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.cases_require_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v integer; st text;
BEGIN
  IF TG_TABLE_NAME = 'kyc_cases' THEN
    SELECT version, status INTO v, st FROM kyc.kyc_cases WHERE id = NEW.id;
    IF NOT EXISTS (SELECT 1 FROM kyc.case_events e WHERE e.kyc_case_id = NEW.id AND e.case_version = v AND e.to_status = st) THEN
      RAISE EXCEPTION 'kyc case % version % has no matching case_events row', NEW.id, v USING ERRCODE = 'check_violation';
    END IF;
  ELSE
    SELECT version, status INTO v, st FROM kyc.kyb_cases WHERE id = NEW.id;
    IF NOT EXISTS (SELECT 1 FROM kyc.case_events e WHERE e.kyb_case_id = NEW.id AND e.case_version = v AND e.to_status = st) THEN
      RAISE EXCEPTION 'kyb case % version % has no matching case_events row', NEW.id, v USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_kyc_cases_require_event AFTER INSERT OR UPDATE ON kyc.kyc_cases
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kyc.cases_require_event();
CREATE CONSTRAINT TRIGGER trg_kyb_cases_require_event AFTER INSERT OR UPDATE ON kyc.kyb_cases
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION kyc.cases_require_event();

-- kyc.profile_events — append-only level/status history (individual profiles and KYB organisations).
CREATE TABLE kyc.profile_events (
  id                  uuid        NOT NULL,
  profile_id          uuid        REFERENCES kyc.verification_profiles (id),
  kyb_organisation_id uuid        REFERENCES kyc.kyb_organisations (id),
  aggregate_version   integer     NOT NULL,
  from_level          text,
  to_level            text        NOT NULL,
  from_status         text,
  to_status           text        NOT NULL,
  reason_code         text        NOT NULL,
  kyc_case_id         uuid        REFERENCES kyc.kyc_cases (id),
  kyb_case_id         uuid        REFERENCES kyc.kyb_cases (id),
  actor_type          text        NOT NULL,
  actor_id            uuid        REFERENCES app.users (id),
  occurred_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_profile_events PRIMARY KEY (id),
  CONSTRAINT ck_profile_events_subject CHECK (num_nonnulls(profile_id, kyb_organisation_id) = 1),
  CONSTRAINT ck_profile_events_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_profile_events_actor CHECK ((actor_type IN ('USER','STAFF') AND actor_id IS NOT NULL) OR (actor_type = 'SYSTEM' AND actor_id IS NULL))
);
CREATE UNIQUE INDEX uq_profile_events_profile_version ON kyc.profile_events (profile_id, aggregate_version) WHERE profile_id IS NOT NULL;
CREATE UNIQUE INDEX uq_profile_events_kyb_version ON kyc.profile_events (kyb_organisation_id, aggregate_version) WHERE kyb_organisation_id IS NOT NULL;
CREATE TRIGGER trg_profile_events_no_mutation BEFORE UPDATE OR DELETE ON kyc.profile_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
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

-- kyc.information_requests — reviewer requests for more information (shown to the subject; no C3 expected,
-- reviewers are instructed not to write identity data here). Classification: C2.
CREATE TABLE kyc.information_requests (
  id           uuid        NOT NULL,
  kyc_case_id  uuid        REFERENCES kyc.kyc_cases (id),
  kyb_case_id  uuid        REFERENCES kyc.kyb_cases (id),
  message      text        NOT NULL,
  items        text[]      NOT NULL DEFAULT '{}',
  requested_by uuid        NOT NULL REFERENCES app.users (id),
  requested_at timestamptz NOT NULL DEFAULT now(),
  responded_at timestamptz,
  CONSTRAINT pk_information_requests PRIMARY KEY (id),
  CONSTRAINT ck_information_requests_case CHECK (num_nonnulls(kyc_case_id, kyb_case_id) = 1),
  CONSTRAINT ck_information_requests_message CHECK (length(message) BETWEEN 3 AND 2000),
  CONSTRAINT ck_information_requests_items CHECK (items <@ ARRAY['IDENTITY_DETAILS','ID_DOCUMENT','SELFIE','PROOF_OF_ADDRESS',
             'REGISTRATION_DOCUMENT','PERSONS','BENEFICIAL_OWNERS','AUTHORITY_EVIDENCE','OTHER'])
);
CREATE INDEX ix_information_requests_kyc ON kyc.information_requests (kyc_case_id) WHERE kyc_case_id IS NOT NULL;
CREATE INDEX ix_information_requests_kyb ON kyc.information_requests (kyb_case_id) WHERE kyb_case_id IS NOT NULL;
CREATE TRIGGER trg_information_requests_cols BEFORE UPDATE ON kyc.information_requests
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('responded_at');
CREATE TRIGGER trg_information_requests_no_delete BEFORE DELETE ON kyc.information_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- kyc.review_notes — reviewer-only notes, encrypted (AAD = note id). Never shown to subjects. Append-only.
CREATE TABLE kyc.review_notes (
  id              uuid        NOT NULL,
  kyc_case_id     uuid        REFERENCES kyc.kyc_cases (id),
  kyb_case_id     uuid        REFERENCES kyc.kyb_cases (id),
  beneficiary_id  uuid,                                   -- app.beneficiaries (no FK: higher layer)
  destination_id  uuid,                                   -- app.payout_destinations (no FK: higher layer)
  body_ciphertext bytea       NOT NULL,
  body_key_id     text        NOT NULL,
  author_id       uuid        NOT NULL REFERENCES app.users (id),
  created_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_review_notes PRIMARY KEY (id),
  CONSTRAINT ck_review_notes_subject CHECK (num_nonnulls(kyc_case_id, kyb_case_id, beneficiary_id, destination_id) = 1),
  CONSTRAINT ck_review_notes_ct CHECK (octet_length(body_ciphertext) >= 28)
);
CREATE TRIGGER trg_review_notes_no_mutation BEFORE UPDATE OR DELETE ON kyc.review_notes
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- KYB persons, beneficial owners and representative authority (draft 0010).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.organisation_persons (
  id                  uuid        NOT NULL,
  kyb_organisation_id uuid        NOT NULL REFERENCES kyc.kyb_organisations (id),
  kyb_case_id         uuid        REFERENCES kyc.kyb_cases (id),        -- case during which it was declared
  identity_id         uuid        NOT NULL REFERENCES kyc.identities (id),
  user_id             uuid        REFERENCES app.users (id),
  roles               text[]      NOT NULL,
  ownership_bp        integer,                                          -- basis points, never float
  valid_from          date        NOT NULL,
  valid_to            date,
  ended_reason        text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_organisation_persons PRIMARY KEY (id),
  CONSTRAINT ck_organisation_persons_roles CHECK (cardinality(roles) >= 1 AND roles <@ ARRAY['DIRECTOR','TRUSTEE','OFFICE_BEARER',
             'BENEFICIAL_OWNER','CONTROLLER','SETTLOR','TRUST_BENEFICIARY','REPRESENTATIVE']),
  CONSTRAINT ck_organisation_persons_bp CHECK (ownership_bp IS NULL OR ownership_bp BETWEEN 0 AND 10000),
  CONSTRAINT ck_organisation_persons_window CHECK (valid_to IS NULL OR valid_to >= valid_from),
  CONSTRAINT ck_organisation_persons_ended CHECK ((valid_to IS NULL) = (ended_reason IS NULL))
);
CREATE INDEX ix_organisation_persons_org ON kyc.organisation_persons (kyb_organisation_id) WHERE valid_to IS NULL;
CREATE TRIGGER trg_organisation_persons_no_delete BEFORE DELETE ON kyc.organisation_persons
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
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

CREATE TABLE kyc.beneficial_owners (
  id                     uuid        NOT NULL,
  kyb_organisation_id    uuid        NOT NULL REFERENCES kyc.kyb_organisations (id),
  organisation_person_id uuid        NOT NULL REFERENCES kyc.organisation_persons (id),
  kyb_case_id            uuid        NOT NULL REFERENCES kyc.kyb_cases (id),
  threshold_limit_key    text        NOT NULL,
  ownership_bp           integer,
  control_basis          text        NOT NULL,
  determined_by          uuid        NOT NULL REFERENCES app.users (id),
  determined_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficial_owners PRIMARY KEY (id),
  CONSTRAINT uq_beneficial_owners_case_person UNIQUE (kyb_case_id, organisation_person_id),
  CONSTRAINT ck_beneficial_owners_key CHECK (threshold_limit_key ~ '^kyb\.bo_threshold\.[a-z_]+$'),
  CONSTRAINT ck_beneficial_owners_bp CHECK (ownership_bp IS NULL OR ownership_bp BETWEEN 0 AND 10000),
  CONSTRAINT ck_beneficial_owners_basis CHECK (control_basis IN ('SHAREHOLDING','VOTING_RIGHTS','APPOINT_REMOVE_DIRECTORS',
             'SIGNIFICANT_INFLUENCE','SENIOR_MANAGING_OFFICIAL','TRUST_ROLE','OTHER'))
);
CREATE TRIGGER trg_beneficial_owners_no_mutation BEFORE UPDATE OR DELETE ON kyc.beneficial_owners
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE kyc.representative_authorities (
  id                  uuid        NOT NULL,
  kyb_organisation_id uuid        NOT NULL REFERENCES kyc.kyb_organisations (id),
  kyb_case_id         uuid        NOT NULL REFERENCES kyc.kyb_cases (id),
  user_id             uuid        NOT NULL REFERENCES app.users (id),
  permissions         text[]      NOT NULL,
  evidence_record_id  uuid        NOT NULL REFERENCES audit.evidence_records (id),
  verified_by         uuid        NOT NULL REFERENCES app.users (id),
  valid_from          timestamptz NOT NULL,
  valid_to            timestamptz NOT NULL,
  revoked_at          timestamptz,
  revoked_by          uuid        REFERENCES app.users (id),
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
CREATE INDEX ix_representative_authorities_user ON kyc.representative_authorities (user_id, kyb_organisation_id) WHERE revoked_at IS NULL;
CREATE TRIGGER trg_representative_authorities_no_delete BEFORE DELETE ON kyc.representative_authorities
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
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

-- kyc.consents — append-only consent facts; a withdrawal is a new row (draft 0010).
CREATE TABLE kyc.consents (
  id                     uuid        NOT NULL,
  consent_type           text        NOT NULL,
  action                 text        NOT NULL,
  withdraws_consent_id   uuid        REFERENCES kyc.consents (id),
  subject_user_id        uuid        REFERENCES app.users (id),
  subject_beneficiary_id uuid,
  grantor_user_id        uuid        NOT NULL REFERENCES app.users (id),
  grantor_capacity       text        NOT NULL,
  method                 text        NOT NULL,
  text_version           text        NOT NULL,
  evidence_record_id     uuid        REFERENCES audit.evidence_records (id),
  occurred_at            timestamptz NOT NULL,
  recorded_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_consents PRIMARY KEY (id),
  CONSTRAINT uq_consents_one_withdrawal UNIQUE (withdraws_consent_id),
  CONSTRAINT ck_consents_type CHECK (consent_type IN ('MINOR_DATA','HEALTH_DATA','CAMPAIGN_PUBLICATION','DATA_SHARING_INSTITUTION')),
  CONSTRAINT ck_consents_action CHECK (action IN ('GIVEN','WITHDRAWN')),
  CONSTRAINT ck_consents_withdrawal_ref CHECK ((action = 'WITHDRAWN') = (withdraws_consent_id IS NOT NULL)),
  CONSTRAINT ck_consents_subject CHECK (num_nonnulls(subject_user_id, subject_beneficiary_id) = 1),
  CONSTRAINT ck_consents_capacity CHECK (grantor_capacity IN ('SELF','PARENT','GUARDIAN','LEGAL_REP')),
  CONSTRAINT ck_consents_minor CHECK (consent_type <> 'MINOR_DATA' OR grantor_capacity IN ('PARENT','GUARDIAN','LEGAL_REP')),
  CONSTRAINT ck_consents_method CHECK (method IN ('SIGNED_FORM','IN_APP_ATTESTATION')),
  CONSTRAINT ck_consents_signed_evidence CHECK (method <> 'SIGNED_FORM' OR evidence_record_id IS NOT NULL),
  -- written-consent sensitive types never rely on a bare in-app click (LR-063/LR-070)
  CONSTRAINT ck_consents_sensitive_method CHECK (consent_type NOT IN ('HEALTH_DATA','MINOR_DATA') OR action = 'WITHDRAWN' OR method = 'SIGNED_FORM')
);
CREATE INDEX ix_consents_subject_beneficiary ON kyc.consents (subject_beneficiary_id, consent_type) WHERE subject_beneficiary_id IS NOT NULL;
CREATE TRIGGER trg_consents_no_mutation BEFORE UPDATE OR DELETE ON kyc.consents
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE VIEW kyc.v_consents_current AS
  SELECT g.* FROM kyc.consents g
   WHERE g.action = 'GIVEN' AND NOT EXISTS (SELECT 1 FROM kyc.consents w WHERE w.withdraws_consent_id = g.id);

-- -----------------------------------------------------------------------------------------------------
-- kyc.kyc_documents — metadata of every verification document (KYC, KYB, KYB person, beneficiary evidence,
-- payout-destination evidence). Bytes live only in the PRIVATE_KYC bucket (composite FK onto
-- app.stored_objects (id, bucket_class) — a public-media object can never be referenced). Removal while the
-- case is editable is recorded (removed_at, once); the row is never deleted.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.kyc_documents (
  id                     uuid        NOT NULL,
  subject_type           text        NOT NULL,
  profile_id             uuid        REFERENCES kyc.verification_profiles (id),
  kyb_organisation_id    uuid        REFERENCES kyc.kyb_organisations (id),
  organisation_person_id uuid        REFERENCES kyc.organisation_persons (id),
  beneficiary_id         uuid,
  destination_id         uuid,
  kyc_case_id            uuid        REFERENCES kyc.kyc_cases (id),
  kyb_case_id            uuid        REFERENCES kyc.kyb_cases (id),
  document_type          text        NOT NULL,
  side                   text        NOT NULL DEFAULT 'NA',
  stored_object_id       uuid        NOT NULL,
  stored_object_bucket   text        NOT NULL DEFAULT 'PRIVATE_KYC',
  evidence_record_id     uuid        NOT NULL REFERENCES audit.evidence_records (id),
  uploaded_by            uuid        NOT NULL REFERENCES app.users (id),
  recorded_at            timestamptz NOT NULL DEFAULT now(),
  removed_at             timestamptz,
  removed_by             uuid        REFERENCES app.users (id),
  CONSTRAINT pk_kyc_documents PRIMARY KEY (id),
  CONSTRAINT uq_kyc_documents_object UNIQUE (stored_object_id),
  CONSTRAINT ck_kyc_documents_bucket CHECK (stored_object_bucket = 'PRIVATE_KYC'),
  CONSTRAINT fk_kyc_documents_stored_object FOREIGN KEY (stored_object_id, stored_object_bucket)
    REFERENCES app.stored_objects (id, bucket_class),
  CONSTRAINT ck_kyc_documents_subject_type CHECK (subject_type IN ('KYC_CASE','KYB_CASE','KYB_PERSON','BENEFICIARY','PAYOUT_DESTINATION')),
  CONSTRAINT ck_kyc_documents_subject CHECK (
       (subject_type = 'KYC_CASE'           AND profile_id IS NOT NULL AND kyc_case_id IS NOT NULL)
    OR (subject_type = 'KYB_CASE'           AND kyb_organisation_id IS NOT NULL AND kyb_case_id IS NOT NULL)
    OR (subject_type = 'KYB_PERSON'         AND organisation_person_id IS NOT NULL AND kyb_case_id IS NOT NULL)
    OR (subject_type = 'BENEFICIARY'        AND beneficiary_id IS NOT NULL)
    OR (subject_type = 'PAYOUT_DESTINATION' AND destination_id IS NOT NULL)),
  CONSTRAINT ck_kyc_documents_type CHECK (document_type IN ('ZW_NATIONAL_ID','ZW_PASSPORT','FOREIGN_PASSPORT','ZW_BIRTH_CERTIFICATE',
             'SELFIE','PROOF_OF_ADDRESS','REGISTRATION_CERTIFICATE','REGISTRY_EXTRACT','CONSTITUTION','TRUST_DEED',
             'DIRECTOR_REGISTER','BO_DECLARATION','BOARD_RESOLUTION','AUTHORITY_LETTER','PVO_CERTIFICATE',
             'GUARDIANSHIP_EVIDENCE','BIRTH_CERTIFICATE','RELATIONSHIP_EVIDENCE','CONSENT_FORM','MEDICAL_EVIDENCE',
             'BANK_LETTER','BANK_STATEMENT','MOBILE_MONEY_STATEMENT','OTHER')),
  CONSTRAINT ck_kyc_documents_side CHECK (side IN ('FRONT','BACK','PHOTO_PAGE','NA')),
  CONSTRAINT ck_kyc_documents_removed CHECK ((removed_at IS NULL) = (removed_by IS NULL))
);
CREATE INDEX ix_kyc_documents_kyc_case ON kyc.kyc_documents (kyc_case_id) WHERE kyc_case_id IS NOT NULL;
CREATE INDEX ix_kyc_documents_kyb_case ON kyc.kyc_documents (kyb_case_id) WHERE kyb_case_id IS NOT NULL;
CREATE INDEX ix_kyc_documents_beneficiary ON kyc.kyc_documents (beneficiary_id) WHERE beneficiary_id IS NOT NULL;
CREATE INDEX ix_kyc_documents_destination ON kyc.kyc_documents (destination_id) WHERE destination_id IS NOT NULL;
CREATE TRIGGER trg_kyc_documents_no_delete BEFORE DELETE ON kyc.kyc_documents
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_kyc_documents_cols BEFORE UPDATE ON kyc.kyc_documents
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('removed_at', 'removed_by');

-- kyc.kyc_checks / kyc.kyb_checks — append-only check results. An automated pipeline never records a final
-- REJECT (a human decision does). SANCTIONS_PEP_SCREEN is recorded N_A while no provider is approved.
CREATE TABLE kyc.kyc_checks (
  id                  uuid        NOT NULL,
  kyc_case_id         uuid        NOT NULL REFERENCES kyc.kyc_cases (id),
  check_code          text        NOT NULL,
  result              text        NOT NULL,
  valid_until         timestamptz,
  rule_version        text        NOT NULL,
  performed_by_type   text        NOT NULL,
  performed_by_id     uuid        REFERENCES app.users (id),
  reason_code         text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyc_checks PRIMARY KEY (id),
  CONSTRAINT ck_kyc_checks_code CHECK (check_code IN ('EMAIL_VERIFIED','PHONE_VERIFIED','AGE_18_PLUS','IDENTITY_DATA','ID_DOCUMENT',
             'MANUAL_IDENTITY_REVIEW','SANCTIONS_PEP_SCREEN','DUPLICATE_IDENTITY','DOCUMENT_EXPIRY','PAYOUT_DESTINATION_OWNERSHIP','ADDRESS')),
  CONSTRAINT ck_kyc_checks_result CHECK (result IN ('PASS','FAIL','REVIEW','N_A')),
  CONSTRAINT ck_kyc_checks_performer CHECK ((performed_by_type = 'STAFF' AND performed_by_id IS NOT NULL)
                                            OR (performed_by_type = 'SYSTEM' AND performed_by_id IS NULL)),
  CONSTRAINT ck_kyc_checks_manual CHECK (check_code <> 'MANUAL_IDENTITY_REVIEW' OR performed_by_type = 'STAFF'),
  CONSTRAINT ck_kyc_checks_reason CHECK (result = 'PASS' OR reason_code IS NOT NULL)
);
CREATE INDEX ix_kyc_checks_case ON kyc.kyc_checks (kyc_case_id, check_code, created_at DESC);
CREATE TRIGGER trg_kyc_checks_no_mutation BEFORE UPDATE OR DELETE ON kyc.kyc_checks
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE kyc.kyb_checks (
  id                     uuid        NOT NULL,
  kyb_case_id            uuid        NOT NULL REFERENCES kyc.kyb_cases (id),
  organisation_person_id uuid        REFERENCES kyc.organisation_persons (id),
  check_code             text        NOT NULL,
  result                 text        NOT NULL,
  rule_version           text        NOT NULL,
  performed_by_type      text        NOT NULL,
  performed_by_id        uuid        REFERENCES app.users (id),
  reason_code            text,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyb_checks PRIMARY KEY (id),
  CONSTRAINT ck_kyb_checks_code CHECK (check_code IN ('ORG_LEGAL_IDENTITY','ORG_REGISTRATION','ORG_CONTROLLERS','ORG_BENEFICIAL_OWNERS',
             'ORG_REPRESENTATIVE_AUTHORITY','ORG_REPRESENTATIVE_IDENTITY','ORG_SCREEN_ENTITY','ORG_SCREEN_PERSONS')),
  CONSTRAINT ck_kyb_checks_result CHECK (result IN ('PASS','FAIL','REVIEW','N_A')),
  CONSTRAINT ck_kyb_checks_performer CHECK ((performed_by_type = 'STAFF' AND performed_by_id IS NOT NULL)
                                            OR (performed_by_type = 'SYSTEM' AND performed_by_id IS NULL)),
  CONSTRAINT ck_kyb_checks_reason CHECK (result = 'PASS' OR reason_code IS NOT NULL)
);
CREATE TRIGGER trg_kyb_checks_no_mutation BEFORE UPDATE OR DELETE ON kyc.kyb_checks
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- kyc.kyc_decisions — append-only human decisions for KYC and KYB cases (kyc-architecture §9). Second approver
-- where required; never the first decider; never the subject (DB trigger below).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.kyc_decisions (
  id                       uuid        NOT NULL,
  kyc_case_id              uuid        REFERENCES kyc.kyc_cases (id),
  kyb_case_id              uuid        REFERENCES kyc.kyb_cases (id),
  outcome                  text        NOT NULL,
  from_status              text        NOT NULL,
  resulting_status         text        NOT NULL,
  granted_level            text,
  reason_code              text        NOT NULL,
  user_message             text,
  conditions               jsonb       NOT NULL DEFAULT '{}',
  evidence_record_id       uuid        NOT NULL REFERENCES audit.evidence_records (id),   -- REVIEW_NOTE evidence (hash)
  decided_by               uuid        NOT NULL REFERENCES app.users (id),
  second_approval_required boolean     NOT NULL,
  second_approver_id       uuid        REFERENCES app.users (id),
  policy_version           text        NOT NULL,
  audit_event_id           uuid        NOT NULL,
  decided_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_kyc_decisions PRIMARY KEY (id),
  CONSTRAINT ck_kyc_decisions_case CHECK (num_nonnulls(kyc_case_id, kyb_case_id) = 1),
  CONSTRAINT ck_kyc_decisions_outcome CHECK (outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS','REQUEST_INFORMATION','REJECT','ESCALATE',
                                                         'SUSPEND','REINSTATE','REOPEN','REVOKE','EXPIRE')),
  CONSTRAINT ck_kyc_decisions_level CHECK ((outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS')) = (granted_level IS NOT NULL)),
  CONSTRAINT ck_kyc_decisions_level_values CHECK (granted_level IS NULL OR granted_level IN ('BASIC_VERIFIED','IDENTITY_VERIFIED',
             'PAYOUT_VERIFIED','ORG_REGISTERED_VERIFIED','ORG_KYB_VERIFIED','ORG_PAYOUT_VERIFIED')),
  CONSTRAINT ck_kyc_decisions_level_kind CHECK (granted_level IS NULL OR ((kyc_case_id IS NOT NULL) = (granted_level NOT LIKE 'ORG\_%'))),
  CONSTRAINT ck_kyc_decisions_conditions CHECK (jsonb_typeof(conditions) = 'object'
                                                AND (outcome <> 'APPROVE_WITH_CONDITIONS' OR conditions <> '{}'::jsonb)),
  CONSTRAINT ck_kyc_decisions_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_kyc_decisions_fraud_second CHECK (NOT (outcome = 'REJECT' AND reason_code LIKE 'FRAUD\_%') OR second_approval_required),
  CONSTRAINT ck_kyc_decisions_second_present CHECK (NOT second_approval_required OR second_approver_id IS NOT NULL),
  CONSTRAINT ck_kyc_decisions_second_distinct CHECK (second_approver_id IS NULL OR second_approver_id <> decided_by)
);
CREATE INDEX ix_kyc_decisions_kyc_case ON kyc.kyc_decisions (kyc_case_id) WHERE kyc_case_id IS NOT NULL;
CREATE INDEX ix_kyc_decisions_kyb_case ON kyc.kyc_decisions (kyb_case_id) WHERE kyb_case_id IS NOT NULL;
CREATE TRIGGER trg_kyc_decisions_no_mutation BEFORE UPDATE OR DELETE ON kyc.kyc_decisions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
-- A reviewer never decides their own verification or one of an organisation they belong to (operational-
-- controls §2). SECURITY DEFINER so it can read app.organisation_members (the kyc role cannot).
CREATE FUNCTION kyc.kyc_decisions_not_self() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE subject_user uuid; org uuid;
BEGIN
  IF NEW.kyc_case_id IS NOT NULL THEN
    SELECT p.user_id INTO subject_user FROM kyc.kyc_cases c JOIN kyc.verification_profiles p ON p.id = c.profile_id
     WHERE c.id = NEW.kyc_case_id;
    IF subject_user IN (NEW.decided_by, NEW.second_approver_id) THEN
      RAISE EXCEPTION 'kyc decision: a reviewer cannot decide their own verification' USING ERRCODE = 'check_violation';
    END IF;
  ELSE
    SELECT o.organisation_id INTO org FROM kyc.kyb_cases c JOIN kyc.kyb_organisations o ON o.id = c.kyb_organisation_id
     WHERE c.id = NEW.kyb_case_id;
    IF EXISTS (SELECT 1 FROM app.organisation_members m WHERE m.organisation_id = org AND m.status <> 'REMOVED'
                 AND m.user_id IN (NEW.decided_by, NEW.second_approver_id))
       OR EXISTS (SELECT 1 FROM kyc.kyb_cases c WHERE c.id = NEW.kyb_case_id
                    AND (c.submitted_by IN (NEW.decided_by, NEW.second_approver_id) OR c.created_by IN (NEW.decided_by, NEW.second_approver_id))) THEN
      RAISE EXCEPTION 'kyb decision: a member or representative of the organisation cannot decide its verification' USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION kyc.kyc_decisions_not_self() FROM PUBLIC;
CREATE TRIGGER trg_kyc_decisions_not_self BEFORE INSERT ON kyc.kyc_decisions
  FOR EACH ROW EXECUTE FUNCTION kyc.kyc_decisions_not_self();

-- -----------------------------------------------------------------------------------------------------
-- kyc.verification_policies — versioned verification requirements (brief §20): per subject kind and risk
-- level, the documents required, validity period and four-eyes rules, as a JSON rule document validated by
-- the Go policy engine. Maker-checker; immutable once decided; decisions record the version applied, so a
-- new version never changes historical decisions. The baseline version is approved by migration under
-- ADR-034 (maker = system actor, no human checker) and is the only row allowed that exemption.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE kyc.verification_policies (
  id             uuid        NOT NULL,
  policy_version text        NOT NULL,
  rules          jsonb       NOT NULL,
  status         text        NOT NULL,
  made_by        uuid        NOT NULL REFERENCES app.users (id),
  approved_by    uuid        REFERENCES app.users (id),
  approved_at    timestamptz,
  retired_at     timestamptz,
  change_reason  text        NOT NULL,
  decision_ref   text        NOT NULL,
  effective_from timestamptz NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_verification_policies PRIMARY KEY (id),
  CONSTRAINT uq_verification_policies_version UNIQUE (policy_version),
  CONSTRAINT ck_verification_policies_version CHECK (policy_version ~ '^v[0-9]+(\.[0-9]+)?$'),
  CONSTRAINT ck_verification_policies_rules CHECK (jsonb_typeof(rules) = 'object'),
  CONSTRAINT ck_verification_policies_status CHECK (status IN ('PROPOSED','APPROVED','REJECTED','RETIRED')),
  CONSTRAINT ck_verification_policies_maker_checker CHECK (approved_by IS NULL OR approved_by <> made_by),
  CONSTRAINT ck_verification_policies_approved CHECK (status = 'PROPOSED' OR approved_at IS NOT NULL),
  CONSTRAINT ck_verification_policies_checker CHECK (status = 'PROPOSED' OR approved_by IS NOT NULL
             OR (made_by = '00000000-0000-0000-0000-000000000001' AND decision_ref = 'ADR-034' AND policy_version = 'v1')),
  CONSTRAINT ck_verification_policies_retired CHECK ((status = 'RETIRED') = (retired_at IS NOT NULL)),
  CONSTRAINT ck_verification_policies_decision_ref CHECK (decision_ref ~ '^(DEC-[A-Za-z0-9-]+|PD-[0-9]{2}|ADR-[0-9]{3})$')
);
CREATE UNIQUE INDEX uq_verification_policies_one_active ON kyc.verification_policies ((true)) WHERE status = 'APPROVED';
CREATE TRIGGER trg_verification_policies_guard_status BEFORE INSERT OR UPDATE OF status ON kyc.verification_policies
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('verification_policy');
CREATE TRIGGER trg_verification_policies_no_delete BEFORE DELETE ON kyc.verification_policies
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION kyc.verification_policies_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT ((OLD.status = 'PROPOSED' AND changed <@ ARRAY['status','approved_by','approved_at'])
          OR (OLD.status = 'APPROVED' AND NEW.status = 'RETIRED' AND changed <@ ARRAY['status','retired_at'])) THEN
    RAISE EXCEPTION 'verification policy %: immutable once decided (attempted: %) — create a new version', OLD.policy_version, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_verification_policies_guard BEFORE UPDATE ON kyc.verification_policies
  FOR EACH ROW EXECUTE FUNCTION kyc.verification_policies_guard();

-- Baseline policy v1 (ADR-034). Rules are validated by internal/kyc/policy.go (same schema).
INSERT INTO kyc.verification_policies (id, policy_version, rules, status, made_by, approved_at, change_reason, decision_ref, effective_from)
VALUES ('0192f000-0000-7000-8000-000000000001', 'v1', '{
  "kyc": {
    "STANDARD":   {"required_documents": [["ZW_NATIONAL_ID","ZW_PASSPORT","FOREIGN_PASSPORT"], ["SELFIE"]], "validity_days": 730, "second_approval": false},
    "LOW":        {"required_documents": [["ZW_NATIONAL_ID","ZW_PASSPORT","FOREIGN_PASSPORT"], ["SELFIE"]], "validity_days": 730, "second_approval": false},
    "ENHANCED":   {"required_documents": [["ZW_NATIONAL_ID","ZW_PASSPORT","FOREIGN_PASSPORT"], ["SELFIE"], ["PROOF_OF_ADDRESS"]], "validity_days": 365, "second_approval": true},
    "RESTRICTED": {"required_documents": [["ZW_NATIONAL_ID","ZW_PASSPORT","FOREIGN_PASSPORT"], ["SELFIE"], ["PROOF_OF_ADDRESS"]], "validity_days": 180, "second_approval": true}
  },
  "kyb": {
    "default":  {"required_documents": [["REGISTRATION_CERTIFICATE","REGISTRY_EXTRACT","PVO_CERTIFICATE","CONSTITUTION","TRUST_DEED"], ["AUTHORITY_LETTER","BOARD_RESOLUTION"]],
                 "min_persons": 1, "representative_min_level": "IDENTITY_VERIFIED", "validity_days": 365, "second_approval": false},
    "TRUST":    {"required_documents": [["TRUST_DEED"], ["AUTHORITY_LETTER","BOARD_RESOLUTION"]], "min_persons": 1, "representative_min_level": "IDENTITY_VERIFIED", "validity_days": 365, "second_approval": false},
    "PVO":      {"required_documents": [["PVO_CERTIFICATE","REGISTRATION_CERTIFICATE"], ["CONSTITUTION"], ["AUTHORITY_LETTER","BOARD_RESOLUTION"]], "min_persons": 1, "representative_min_level": "IDENTITY_VERIFIED", "validity_days": 365, "second_approval": true}
  },
  "beneficiary": {
    "SELF":                {"required_documents": [], "second_approval": false},
    "INDIVIDUAL":          {"required_documents": [["RELATIONSHIP_EVIDENCE","CONSENT_FORM"]], "second_approval": false},
    "MINOR":               {"required_documents": [["BIRTH_CERTIFICATE"], ["GUARDIANSHIP_EVIDENCE","BIRTH_CERTIFICATE"], ["CONSENT_FORM"]], "second_approval": true},
    "INCAPACITATED_ADULT": {"required_documents": [["GUARDIANSHIP_EVIDENCE"], ["MEDICAL_EVIDENCE"]], "second_approval": true},
    "ORGANISATION":        {"required_documents": [["AUTHORITY_LETTER","REGISTRATION_CERTIFICATE"]], "second_approval": false},
    "COMMUNITY_GROUP":     {"required_documents": [["CONSTITUTION","AUTHORITY_LETTER"]], "second_approval": false}
  },
  "payout_destination": {"ownership_evidence": ["BANK_LETTER","BANK_STATEMENT","MOBILE_MONEY_STATEMENT"], "validity_days": 365},
  "information_request_expiry_days": 30,
  "adult_age": 18,
  "screening": {"provider": null, "status": "NOT_SELECTED"}
}', 'PROPOSED', '00000000-0000-0000-0000-000000000001', NULL,
 'Stage 5 baseline verification requirements (internal policy, not a legal requirement)', 'ADR-034', now());
-- approved through the same guarded transition as every later version (the ADR is the checker here)
UPDATE kyc.verification_policies SET status = 'APPROVED', approved_at = now() WHERE policy_version = 'v1';

-- new staff permissions (contract §7.1); SUPPORT, ADMIN and SUPER_ADMIN receive none of them
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up)
SELECT md5('permission:' || p.code)::uuid, p.code, p.description, p.sensitive, p.step_up
FROM (VALUES
  ('payout_destination.verify',     'Verify payout destination ownership and approve it (Stage 5)', true,  true),
  ('verification_policy.request',   'Propose a verification policy version (maker)',              true,  false),
  ('verification_policy.approve',   'Approve a verification policy version (checker)',            true,  true),
  ('risk.view',                     'View risk signals and assessments',                          false, false),
  ('compliance.case.view',          'View compliance cases (no STR-restricted cases)',            true,  false)
) AS p(code, description, sensitive, step_up);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm_code)::uuid,
       md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm_code)::uuid
FROM (VALUES
  ('KYC_REVIEWER', 'payout_destination.verify'), ('COMPLIANCE', 'payout_destination.verify'),
  ('COMPLIANCE', 'verification_policy.request'), ('COMPLIANCE', 'verification_policy.approve'),
  ('KYC_REVIEWER', 'risk.view'), ('COMPLIANCE', 'risk.view'),
  ('COMPLIANCE', 'compliance.case.view'),
  ('COMPLIANCE', 'kyc.case.review')
) AS rp(role_code, perm_code)
ON CONFLICT DO NOTHING;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN
  ('payout_destination.verify','verification_policy.request','verification_policy.approve','risk.view','compliance.case.view'))
  OR id = md5('role_permission:COMPLIANCE:kyc.case.review')::uuid;
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN
  ('payout_destination.verify','verification_policy.request','verification_policy.approve','risk.view','compliance.case.view');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP VIEW IF EXISTS kyc.v_consents_current;
DROP TABLE IF EXISTS kyc.verification_policies, kyc.kyc_decisions, kyc.kyb_checks, kyc.kyc_checks, kyc.kyc_documents,
  kyc.consents, kyc.representative_authorities, kyc.beneficial_owners, kyc.organisation_persons, kyc.review_notes,
  kyc.information_requests, kyc.profile_events, kyc.case_events, kyc.kyb_cases, kyc.kyc_cases, kyc.identities,
  kyc.kyb_organisations, kyc.verification_profiles;
DROP FUNCTION IF EXISTS kyc.versioned_row_guard(), kyc.identities_guard(), kyc.kyb_cases_details_frozen(), kyc.kyc_cases_draft_frozen(),
  kyc.cases_require_event(), kyc.profile_requires_event(), kyc.organisation_persons_guard(), kyc.representative_authorities_guard(),
  kyc.kyc_decisions_not_self(), kyc.verification_policies_guard();
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine IN ('verification_case','kyc_verification_status','verification_policy');
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
