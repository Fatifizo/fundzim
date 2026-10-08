-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0012: beneficiaries module — beneficiaries, beneficiary_relationships, beneficiary_verifications,
-- institution_payees. Schema: app. No C3 here (ADR-016, ADR-021): identity numbers, DOB, consents and
-- evidence live in kyc (kyc.identities, kyc.consents, kyc.beneficiary_evidence); this module holds only
-- opaque references to them (kyc_person_ref, consent_ref) WITHOUT foreign keys (app never references kyc).
-- Docs: docs/database/organisation-beneficiary-schema.md, docs/compliance/beneficiary-verification.md.
-- =====================================================================================================

-- app.institution_payees — hospitals, schools, funeral parlours... verified once (KYB-lite) and reused.
-- The payout destination (bank account, C3) is owned by payouts (payout_destinations.institution_payee_id).
-- Classification: C2 (institution data is mostly public; independent contacts C2). Retention: FINANCIAL.
CREATE TABLE app.institution_payees (
  id                        uuid        NOT NULL,
  legal_name                text        NOT NULL,
  institution_type          text        NOT NULL,
  registration_authority    text,                          -- e.g. health professions / education registry
  registration_ref          text,
  physical_address          text        NOT NULL,
  independent_contact_phone text,                          -- from a public source, NOT from the campaign owner
  independent_contact_email text,
  contact_source            text        NOT NULL,          -- where the independent contact was obtained
  organisation_id           uuid,                          -- if the institution also has a FundZim organisation
  market_code               char(2)     NOT NULL DEFAULT 'ZW',
  status                    text        NOT NULL DEFAULT 'PENDING',
  verified_at               timestamptz,
  verified_by               uuid,
  version                   integer     NOT NULL DEFAULT 1,
  created_at                timestamptz NOT NULL DEFAULT now(),
  updated_at                timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_institution_payees PRIMARY KEY (id),
  CONSTRAINT fk_institution_payees_organisation_id FOREIGN KEY (organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_institution_payees_market_code FOREIGN KEY (market_code) REFERENCES app.markets (code),
  CONSTRAINT fk_institution_payees_verified_by FOREIGN KEY (verified_by) REFERENCES app.users (id),
  CONSTRAINT ck_institution_payees_type CHECK (institution_type IN ('HOSPITAL', 'CLINIC', 'SCHOOL', 'UNIVERSITY', 'FUNERAL_PARLOUR',
                                                                    'PHARMACY', 'OTHER')),
  CONSTRAINT ck_institution_payees_status CHECK (status IN ('PENDING', 'VERIFIED', 'REJECTED', 'SUSPENDED')),
  CONSTRAINT ck_institution_payees_verified CHECK (status <> 'VERIFIED' OR (verified_at IS NOT NULL AND verified_by IS NOT NULL)),
  CONSTRAINT ck_institution_payees_contact CHECK (num_nonnulls(independent_contact_phone, independent_contact_email) >= 1),
  CONSTRAINT ck_institution_payees_phone CHECK (independent_contact_phone IS NULL OR independent_contact_phone ~ '^\+[1-9][0-9]{6,14}$')
);
CREATE UNIQUE INDEX uq_institution_payees_registration ON app.institution_payees (institution_type, registration_ref)
  WHERE registration_ref IS NOT NULL;
CREATE TRIGGER trg_institution_payees_bump_version BEFORE UPDATE ON app.institution_payees
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_institution_payees_set_updated_at BEFORE UPDATE ON app.institution_payees
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_institution_payees_keep_created_at BEFORE UPDATE ON app.institution_payees
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_institution_payees_no_delete BEFORE DELETE ON app.institution_payees
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- app.beneficiaries — who the money is for, modelled separately from the campaign owner (ADR-016).
-- Exactly one owner (user or organisation) declares the beneficiary. status is the beneficiary verification
-- state (beneficiary-verification §2): NOT_STARTED → DECLARED → EVIDENCE_SUBMITTED → VERIFIED | REJECTED.
-- VERIFIED requires a VERIFIED decision row (with a second verifier where required — always for MINOR).
-- Classification: C2 (full_name, relationship); display_name C0 once published (minimised for minors).
-- Retention: FINANCIAL (referenced by payouts) — anonymised, never deleted.
CREATE TABLE app.beneficiaries (
  id                         uuid        NOT NULL,
  owner_user_id              uuid,
  owner_organisation_id      uuid,
  beneficiary_type           text        NOT NULL,
  display_name               text        NOT NULL,         -- public, minimised (first name / chosen name for minors)
  full_name                  text,                         -- C2; NULL after anonymisation
  beneficiary_user_id        uuid,                         -- the beneficiary's own FundZim account, if any
  beneficiary_organisation_id uuid,                        -- ORGANISATION type
  institution_payee_id       uuid,                         -- INSTITUTION type (or preferred payee)
  kyc_person_ref             uuid,                         -- kyc.identities id (C3 lives there). NO FK by design.
  status                     text        NOT NULL DEFAULT 'NOT_STARTED',
  risk_tier                  text        NOT NULL DEFAULT 'STANDARD',
  requires_second_verifier   boolean     NOT NULL DEFAULT false,
  latest_verification_id     uuid,
  anonymised_at              timestamptz,
  version                    integer     NOT NULL DEFAULT 1,
  created_at                 timestamptz NOT NULL DEFAULT now(),
  updated_at                 timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficiaries PRIMARY KEY (id),
  CONSTRAINT fk_beneficiaries_owner_user_id FOREIGN KEY (owner_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiaries_owner_organisation_id FOREIGN KEY (owner_organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_beneficiaries_beneficiary_user_id FOREIGN KEY (beneficiary_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiaries_beneficiary_organisation_id FOREIGN KEY (beneficiary_organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_beneficiaries_institution_payee_id FOREIGN KEY (institution_payee_id) REFERENCES app.institution_payees (id),
  CONSTRAINT ck_beneficiaries_one_owner CHECK (num_nonnulls(owner_user_id, owner_organisation_id) = 1),
  CONSTRAINT ck_beneficiaries_type CHECK (beneficiary_type IN ('SELF', 'INDIVIDUAL_OTHER', 'MINOR', 'INCAPACITATED_ADULT',
                                                               'DECEASED_ESTATE_OR_FAMILY', 'INSTITUTION', 'ORGANISATION',
                                                               'COMMUNITY_GROUP')),
  -- SELF: the owner is the beneficiary (individual owner only). Any other type: the beneficiary is a
  -- different person/entity from the owner.
  CONSTRAINT ck_beneficiaries_self CHECK (beneficiary_type <> 'SELF' OR
    (owner_user_id IS NOT NULL AND beneficiary_user_id = owner_user_id)),
  CONSTRAINT ck_beneficiaries_owner_is_not_beneficiary CHECK (beneficiary_type = 'SELF' OR
    ((beneficiary_user_id IS NULL OR owner_user_id IS NULL OR beneficiary_user_id <> owner_user_id)
     AND (beneficiary_organisation_id IS NULL OR owner_organisation_id IS NULL OR beneficiary_organisation_id <> owner_organisation_id))
    OR beneficiary_type = 'ORGANISATION'),
  CONSTRAINT ck_beneficiaries_org_type CHECK (beneficiary_type <> 'ORGANISATION' OR beneficiary_organisation_id IS NOT NULL),
  CONSTRAINT ck_beneficiaries_institution_type CHECK (beneficiary_type <> 'INSTITUTION' OR institution_payee_id IS NOT NULL),
  CONSTRAINT ck_beneficiaries_minor_second_verifier CHECK (beneficiary_type <> 'MINOR' OR requires_second_verifier),
  CONSTRAINT ck_beneficiaries_high_second_verifier CHECK (risk_tier <> 'HIGH' OR requires_second_verifier),
  CONSTRAINT ck_beneficiaries_risk_tier CHECK (risk_tier IN ('STANDARD', 'ELEVATED', 'HIGH')),
  CONSTRAINT ck_beneficiaries_status CHECK (status IN ('NOT_STARTED', 'DECLARED', 'EVIDENCE_SUBMITTED', 'VERIFIED', 'REJECTED')),
  CONSTRAINT ck_beneficiaries_verified_ref CHECK (status NOT IN ('VERIFIED', 'REJECTED') OR latest_verification_id IS NOT NULL),
  CONSTRAINT ck_beneficiaries_display_name CHECK (length(display_name) BETWEEN 1 AND 100),
  CONSTRAINT ck_beneficiaries_full_name CHECK (full_name IS NOT NULL OR anonymised_at IS NOT NULL OR beneficiary_type IN ('SELF', 'INSTITUTION', 'ORGANISATION'))
);
-- The organisation-owned ORGANISATION beneficiary may be the owning organisation itself (a charity's
-- programme campaign); every other type keeps owner and beneficiary distinct (CHECK above).
CREATE INDEX ix_beneficiaries_owner_user_id ON app.beneficiaries (owner_user_id) WHERE owner_user_id IS NOT NULL;
CREATE INDEX ix_beneficiaries_owner_organisation_id ON app.beneficiaries (owner_organisation_id) WHERE owner_organisation_id IS NOT NULL;
CREATE INDEX ix_beneficiaries_institution_payee_id ON app.beneficiaries (institution_payee_id) WHERE institution_payee_id IS NOT NULL;
CREATE INDEX ix_beneficiaries_status ON app.beneficiaries (status) WHERE status = 'EVIDENCE_SUBMITTED';  -- review queue

CREATE TRIGGER trg_beneficiaries_guard_status BEFORE INSERT OR UPDATE OF status ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('beneficiary_verification');
CREATE TRIGGER trg_beneficiaries_bump_version BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_beneficiaries_set_updated_at BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_beneficiaries_keep_created_at BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_beneficiaries_immutable_cols BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('owner_user_id', 'owner_organisation_id', 'beneficiary_type');
CREATE TRIGGER trg_beneficiaries_no_delete BEFORE DELETE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('beneficiary_verification', '',                   'NOT_STARTED'),
  ('beneficiary_verification', 'NOT_STARTED',        'DECLARED'),
  ('beneficiary_verification', 'DECLARED',           'EVIDENCE_SUBMITTED'),
  ('beneficiary_verification', 'EVIDENCE_SUBMITTED', 'VERIFIED'),
  ('beneficiary_verification', 'EVIDENCE_SUBMITTED', 'DECLARED'),            -- resubmission requested
  ('beneficiary_verification', 'EVIDENCE_SUBMITTED', 'REJECTED'),
  ('beneficiary_verification', 'VERIFIED',           'EVIDENCE_SUBMITTED'),  -- material change: re-verify
  ('beneficiary_verification', 'REJECTED',           'DECLARED');            -- owner corrects (limited attempts)

-- app.beneficiary_relationships — who the beneficiary is to the owner, and on what authority the owner
-- raises for them. Append-only with supersession (a change is a new row). consent_ref → kyc.consents (no FK).
-- Classification: C2. Retention: FINANCIAL.
CREATE TABLE app.beneficiary_relationships (
  id                      uuid        NOT NULL,
  beneficiary_id          uuid        NOT NULL,
  related_user_id         uuid,                            -- usually the campaign owner
  related_organisation_id uuid,
  relationship_code       text        NOT NULL,
  authority_basis         text        NOT NULL,
  consent_ref             uuid,                            -- kyc.consents id. NO FK by design.
  declared_by_user_id     uuid        NOT NULL,
  declared_at             timestamptz NOT NULL,
  superseded_at           timestamptz,
  superseded_by_id        uuid,
  created_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficiary_relationships PRIMARY KEY (id),
  CONSTRAINT fk_beneficiary_relationships_beneficiary_id FOREIGN KEY (beneficiary_id) REFERENCES app.beneficiaries (id),
  CONSTRAINT fk_beneficiary_relationships_related_user_id FOREIGN KEY (related_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiary_relationships_related_organisation_id FOREIGN KEY (related_organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_beneficiary_relationships_declared_by FOREIGN KEY (declared_by_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiary_relationships_superseded_by_id FOREIGN KEY (superseded_by_id) REFERENCES app.beneficiary_relationships (id),
  CONSTRAINT ck_beneficiary_relationships_one_party CHECK (num_nonnulls(related_user_id, related_organisation_id) = 1),
  CONSTRAINT ck_beneficiary_relationships_code CHECK (relationship_code IN (
    'SELF', 'PARENT', 'GUARDIAN', 'CHILD', 'SPOUSE', 'SIBLING', 'OTHER_RELATIVE', 'FRIEND', 'NEIGHBOUR',
    'LEGAL_REPRESENTATIVE', 'TREASURER', 'INSTITUTION_REPRESENTATIVE', 'ORGANISATION_REPRESENTATIVE', 'OTHER')),
  CONSTRAINT ck_beneficiary_relationships_authority CHECK (authority_basis IN (
    'NOT_REQUIRED', 'BENEFICIARY_CONSENT', 'GUARDIANSHIP', 'PARENTAL_CONSENT', 'LEGAL_REPRESENTATION',
    'NEXT_OF_KIN_DECLARATION', 'INSTITUTION_CONFIRMATION', 'ORGANISATION_AUTHORITY', 'GROUP_MANDATE')),
  CONSTRAINT ck_beneficiary_relationships_consent CHECK (
    authority_basis NOT IN ('BENEFICIARY_CONSENT', 'PARENTAL_CONSENT') OR consent_ref IS NOT NULL),
  CONSTRAINT ck_beneficiary_relationships_superseded CHECK ((superseded_at IS NULL) = (superseded_by_id IS NULL))
);
CREATE INDEX ix_beneficiary_relationships_beneficiary_id ON app.beneficiary_relationships (beneficiary_id) WHERE superseded_at IS NULL;
CREATE TRIGGER trg_beneficiary_relationships_cols BEFORE UPDATE ON app.beneficiary_relationships
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('superseded_at', 'superseded_by_id');
CREATE TRIGGER trg_beneficiary_relationships_no_delete BEFORE DELETE ON app.beneficiary_relationships
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_beneficiary_relationships_no_truncate BEFORE TRUNCATE ON app.beneficiary_relationships
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- app.beneficiary_verifications — append-only verification decisions (KYC_REVIEWER, permission
-- beneficiary.verification.decide). A VERIFIED decision that requires four eyes carries a second verifier
-- distinct from the decider. Evidence is referenced by audit.evidence_records ids (no C3 here).
-- Classification: C2. Retention: FINANCIAL.
CREATE TABLE app.beneficiary_verifications (
  id                       uuid        NOT NULL,
  beneficiary_id           uuid        NOT NULL,
  decision                 text        NOT NULL,
  from_status              text        NOT NULL,
  to_status                text        NOT NULL,
  decided_by               uuid,                           -- staff; NULL only for system REVERIFICATION_REQUIRED
  second_verifier_id       uuid,
  requires_second_verifier boolean     NOT NULL,
  reason_code              text        NOT NULL,
  justification            text,
  evidence_record_ids      uuid[]      NOT NULL DEFAULT '{}',   -- audit.evidence_records ids
  policy_version           text        NOT NULL,
  audit_event_id           uuid        NOT NULL,           -- audit.audit_events id (same transaction)
  decided_at               timestamptz NOT NULL,
  created_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficiary_verifications PRIMARY KEY (id),
  CONSTRAINT uq_beneficiary_verifications_id_beneficiary UNIQUE (id, beneficiary_id),
  CONSTRAINT fk_beneficiary_verifications_beneficiary_id FOREIGN KEY (beneficiary_id) REFERENCES app.beneficiaries (id),
  CONSTRAINT fk_beneficiary_verifications_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiary_verifications_second_verifier_id FOREIGN KEY (second_verifier_id) REFERENCES app.users (id),
  CONSTRAINT ck_beneficiary_verifications_decision CHECK (decision IN ('VERIFIED', 'REJECTED', 'RESUBMISSION_REQUESTED',
                                                                       'REVERIFICATION_REQUIRED')),
  CONSTRAINT ck_beneficiary_verifications_decision_target CHECK (
       (decision = 'VERIFIED' AND to_status = 'VERIFIED')
    OR (decision = 'REJECTED' AND to_status = 'REJECTED')
    OR (decision = 'RESUBMISSION_REQUESTED' AND to_status = 'DECLARED')
    OR (decision = 'REVERIFICATION_REQUIRED' AND to_status = 'EVIDENCE_SUBMITTED')),
  CONSTRAINT ck_beneficiary_verifications_staff CHECK (decision = 'REVERIFICATION_REQUIRED' OR (decided_by IS NOT NULL AND justification IS NOT NULL)),
  CONSTRAINT ck_beneficiary_verifications_four_eyes CHECK (
    decision <> 'VERIFIED' OR NOT requires_second_verifier
    OR (second_verifier_id IS NOT NULL AND second_verifier_id IS DISTINCT FROM decided_by)),
  CONSTRAINT ck_beneficiary_verifications_distinct CHECK (second_verifier_id IS NULL OR second_verifier_id IS DISTINCT FROM decided_by)
);
CREATE INDEX ix_beneficiary_verifications_beneficiary_id ON app.beneficiary_verifications (beneficiary_id, decided_at);
CREATE TRIGGER trg_beneficiary_verifications_no_mutation BEFORE UPDATE OR DELETE ON app.beneficiary_verifications
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_beneficiary_verifications_no_truncate BEFORE TRUNCATE ON app.beneficiary_verifications
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- A decision row must agree with the beneficiary's flags at the time it is written.
CREATE FUNCTION app.beneficiary_verifications_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  b app.beneficiaries%ROWTYPE;
BEGIN
  SELECT * INTO b FROM app.beneficiaries WHERE id = NEW.beneficiary_id;
  IF NEW.requires_second_verifier IS DISTINCT FROM b.requires_second_verifier THEN
    RAISE EXCEPTION 'decision requires_second_verifier must match the beneficiary (%)', b.requires_second_verifier
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF NEW.decided_by IS NOT NULL AND (NEW.decided_by = b.owner_user_id OR NEW.second_verifier_id = b.owner_user_id) THEN
    RAISE EXCEPTION 'a verifier cannot be the beneficiary owner' USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_beneficiary_verifications_check BEFORE INSERT ON app.beneficiary_verifications
  FOR EACH ROW EXECUTE FUNCTION app.beneficiary_verifications_check();

ALTER TABLE app.beneficiaries ADD CONSTRAINT fk_beneficiaries_latest_verification
  FOREIGN KEY (latest_verification_id, id) REFERENCES app.beneficiary_verifications (id, beneficiary_id);

-- A VERIFIED/REJECTED beneficiary must point at a matching decision row (VERIFIED needs a VERIFIED decision).
CREATE FUNCTION app.beneficiaries_require_decision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status IN ('VERIFIED', 'REJECTED')
     AND (TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status OR NEW.latest_verification_id IS DISTINCT FROM OLD.latest_verification_id)
     AND NOT EXISTS (SELECT 1 FROM app.beneficiary_verifications v
                      WHERE v.id = NEW.latest_verification_id AND v.beneficiary_id = NEW.id AND v.to_status = NEW.status) THEN
    RAISE EXCEPTION 'beneficiary % status % requires a matching beneficiary_verifications decision', NEW.id, NEW.status
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_beneficiaries_require_decision BEFORE INSERT OR UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.beneficiaries_require_decision();
