-- FundZim migration: beneficiaries module (Stage 2 draft 0012 adapted to ADR-034). Schema: app.
-- A beneficiary is who the money is for, modelled separately from the owner who raises for them (ADR-016).
-- No C3 here: date of birth and identity details of a beneficiary live in kyc.identities (subject_kind
-- BENEFICIARY), referenced by kyc_identity_ref without an FK (app never references kyc); evidence documents
-- live in kyc.kyc_documents. Relationship type never establishes authority: authority_basis is separate and
-- must be evidenced (beneficiary-verification §3). Institution payees are deferred (Stage 6+).
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE app.beneficiaries (
  id                          uuid        NOT NULL,
  owner_user_id               uuid,
  owner_organisation_id       uuid,
  created_by                  uuid        NOT NULL,
  beneficiary_type            text        NOT NULL,
  display_name                text        NOT NULL,              -- public, minimised (first name for minors)
  full_name                   text,                              -- C2
  beneficiary_user_id         uuid,
  beneficiary_organisation_id uuid,
  kyc_identity_ref            uuid,                              -- kyc.identities id (no FK by design)
  relationship_type           text        NOT NULL,
  relationship_description    text,
  authority_basis             text        NOT NULL,
  status                      text        NOT NULL,              -- verification status (machine verification_case)
  risk_level                  text        NOT NULL DEFAULT 'STANDARD',
  requires_second_approval    boolean     NOT NULL DEFAULT false,
  policy_version              text        NOT NULL,
  assigned_to                 uuid,
  assigned_at                 timestamptz,
  pending_outcome             text,
  pending_decided_by          uuid,
  pending_reason_code         text,
  submitted_at                timestamptz,
  decided_at                  timestamptz,
  expires_at                  timestamptz,
  closed_at                   timestamptz,
  version                     integer     NOT NULL DEFAULT 1,
  created_at                  timestamptz NOT NULL DEFAULT now(),
  updated_at                  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficiaries PRIMARY KEY (id),
  CONSTRAINT fk_beneficiaries_owner_user_id FOREIGN KEY (owner_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiaries_owner_organisation_id FOREIGN KEY (owner_organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_beneficiaries_created_by FOREIGN KEY (created_by) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiaries_beneficiary_user_id FOREIGN KEY (beneficiary_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiaries_beneficiary_organisation_id FOREIGN KEY (beneficiary_organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_beneficiaries_assigned_to FOREIGN KEY (assigned_to) REFERENCES app.users (id),
  CONSTRAINT fk_beneficiaries_pending_decided_by FOREIGN KEY (pending_decided_by) REFERENCES app.users (id),
  CONSTRAINT ck_beneficiaries_one_owner CHECK (num_nonnulls(owner_user_id, owner_organisation_id) = 1),
  CONSTRAINT ck_beneficiaries_type CHECK (beneficiary_type IN ('SELF','INDIVIDUAL','MINOR','INCAPACITATED_ADULT','ORGANISATION','COMMUNITY_GROUP')),
  CONSTRAINT ck_beneficiaries_self CHECK (beneficiary_type <> 'SELF' OR (owner_user_id IS NOT NULL AND beneficiary_user_id = owner_user_id
                                          AND relationship_type = 'SELF')),
  CONSTRAINT ck_beneficiaries_not_owner CHECK (beneficiary_type = 'SELF' OR beneficiary_user_id IS NULL OR owner_user_id IS NULL
                                               OR beneficiary_user_id <> owner_user_id),
  CONSTRAINT ck_beneficiaries_org_type CHECK (beneficiary_type <> 'ORGANISATION' OR beneficiary_organisation_id IS NOT NULL),
  CONSTRAINT ck_beneficiaries_relationship CHECK (relationship_type IN ('SELF','PARENT_GUARDIAN','FAMILY_MEMBER','AUTHORIZED_REPRESENTATIVE',
                                                  'ORGANISATION_REPRESENTATIVE','THIRD_PARTY_ORGANISER','OTHER')),
  CONSTRAINT ck_beneficiaries_relationship_desc CHECK (relationship_type <> 'OTHER' OR length(coalesce(relationship_description, '')) >= 3),
  CONSTRAINT ck_beneficiaries_authority CHECK (authority_basis IN ('NOT_REQUIRED','BENEFICIARY_CONSENT','PARENTAL_RESPONSIBILITY',
                                               'GUARDIANSHIP_ORDER','LEGAL_REPRESENTATION','ORGANISATION_AUTHORITY','GROUP_MANDATE',
                                               'INSTITUTION_CONFIRMATION')),
  CONSTRAINT ck_beneficiaries_self_authority CHECK ((beneficiary_type = 'SELF') = (authority_basis = 'NOT_REQUIRED')),
  CONSTRAINT ck_beneficiaries_minor_authority CHECK (beneficiary_type <> 'MINOR' OR authority_basis IN ('PARENTAL_RESPONSIBILITY','GUARDIANSHIP_ORDER','LEGAL_REPRESENTATION')),
  CONSTRAINT ck_beneficiaries_status CHECK (status IN ('DRAFT','SUBMITTED','UNDER_REVIEW','ADDITIONAL_INFORMATION_REQUIRED','ESCALATED',
                                                      'APPROVED','REJECTED','EXPIRED','SUSPENDED','REVOKED','WITHDRAWN')),
  CONSTRAINT ck_beneficiaries_risk CHECK (risk_level IN ('LOW','STANDARD','ENHANCED','RESTRICTED')),
  -- four eyes always for minors, incapacitated adults and RESTRICTED risk (beneficiary-verification §5)
  CONSTRAINT ck_beneficiaries_second_approval CHECK (requires_second_approval OR
             (beneficiary_type NOT IN ('MINOR','INCAPACITATED_ADULT') AND risk_level <> 'RESTRICTED')),
  CONSTRAINT ck_beneficiaries_display_name CHECK (length(display_name) BETWEEN 1 AND 100),
  CONSTRAINT ck_beneficiaries_full_name CHECK (full_name IS NOT NULL OR beneficiary_type IN ('SELF','ORGANISATION')),
  CONSTRAINT ck_beneficiaries_assigned CHECK ((assigned_to IS NULL) = (assigned_at IS NULL)),
  CONSTRAINT ck_beneficiaries_review_assigned CHECK (status NOT IN ('UNDER_REVIEW','ESCALATED') OR assigned_to IS NOT NULL),
  CONSTRAINT ck_beneficiaries_submitted CHECK (status IN ('DRAFT','WITHDRAWN') OR submitted_at IS NOT NULL),
  CONSTRAINT ck_beneficiaries_decided CHECK ((status IN ('APPROVED','REJECTED','EXPIRED','SUSPENDED','REVOKED')) <= (decided_at IS NOT NULL)),
  CONSTRAINT ck_beneficiaries_closed CHECK ((status IN ('REJECTED','EXPIRED','REVOKED','WITHDRAWN')) = (closed_at IS NOT NULL)),
  CONSTRAINT ck_beneficiaries_pending CHECK ((pending_outcome IS NULL) = (pending_decided_by IS NULL)
                                             AND (pending_outcome IS NULL OR pending_outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS','REJECT')))
);
CREATE INDEX ix_beneficiaries_owner_user_id ON app.beneficiaries (owner_user_id) WHERE owner_user_id IS NOT NULL;
CREATE INDEX ix_beneficiaries_owner_organisation_id ON app.beneficiaries (owner_organisation_id) WHERE owner_organisation_id IS NOT NULL;
CREATE INDEX ix_beneficiaries_queue ON app.beneficiaries (status, submitted_at) WHERE status IN ('SUBMITTED','UNDER_REVIEW','ESCALATED');
CREATE TRIGGER trg_beneficiaries_guard_status BEFORE INSERT OR UPDATE OF status ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('verification_case');
CREATE TRIGGER trg_beneficiaries_bump_version BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_beneficiaries_set_updated_at BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_beneficiaries_keep_created_at BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_beneficiaries_immutable_cols BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('owner_user_id', 'owner_organisation_id', 'beneficiary_type', 'created_by');
CREATE TRIGGER trg_beneficiaries_no_delete BEFORE DELETE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
-- declared details are frozen once submitted (they can change only while DRAFT or after an information request)
CREATE FUNCTION app.beneficiaries_details_frozen() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status NOT IN ('DRAFT','ADDITIONAL_INFORMATION_REQUIRED')
     AND (NEW.display_name, NEW.full_name, NEW.beneficiary_user_id, NEW.beneficiary_organisation_id, NEW.kyc_identity_ref,
          NEW.relationship_type, NEW.relationship_description, NEW.authority_basis) IS DISTINCT FROM
         (OLD.display_name, OLD.full_name, OLD.beneficiary_user_id, OLD.beneficiary_organisation_id, OLD.kyc_identity_ref,
          OLD.relationship_type, OLD.relationship_description, OLD.authority_basis) THEN
    RAISE EXCEPTION 'beneficiary %: declared details are frozen in status %', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_beneficiaries_details_frozen BEFORE UPDATE ON app.beneficiaries
  FOR EACH ROW EXECUTE FUNCTION app.beneficiaries_details_frozen();

-- app.beneficiary_relationships — append-only history of the declared relationship and authority basis.
CREATE TABLE app.beneficiary_relationships (
  id                  uuid        NOT NULL,
  beneficiary_id      uuid        NOT NULL REFERENCES app.beneficiaries (id),
  relationship_type   text        NOT NULL,
  description         text,
  authority_basis     text        NOT NULL,
  declared_by_user_id uuid        NOT NULL REFERENCES app.users (id),
  declared_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficiary_relationships PRIMARY KEY (id)
);
CREATE INDEX ix_beneficiary_relationships_beneficiary ON app.beneficiary_relationships (beneficiary_id, declared_at);
CREATE TRIGGER trg_beneficiary_relationships_no_mutation BEFORE UPDATE OR DELETE ON app.beneficiary_relationships
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- app.beneficiary_events — append-only verification timeline; one row per beneficiary version.
CREATE TABLE app.beneficiary_events (
  id               uuid        NOT NULL,
  beneficiary_id   uuid        NOT NULL REFERENCES app.beneficiaries (id),
  beneficiary_version integer  NOT NULL,
  event_type       text        NOT NULL,
  from_status      text,
  to_status        text        NOT NULL,
  actor_type       text        NOT NULL,
  actor_id         uuid        REFERENCES app.users (id),
  reason_code      text,
  payload          jsonb       NOT NULL DEFAULT '{}',
  occurred_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficiary_events PRIMARY KEY (id),
  CONSTRAINT uq_beneficiary_events_version UNIQUE (beneficiary_id, beneficiary_version),
  CONSTRAINT ck_beneficiary_events_actor CHECK ((actor_type IN ('USER','STAFF') AND actor_id IS NOT NULL) OR (actor_type = 'SYSTEM' AND actor_id IS NULL)),
  CONSTRAINT ck_beneficiary_events_reason CHECK (reason_code IS NULL OR reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_beneficiary_events_payload CHECK (jsonb_typeof(payload) = 'object')
);
CREATE TRIGGER trg_beneficiary_events_no_mutation BEFORE UPDATE OR DELETE ON app.beneficiary_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION app.beneficiaries_require_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v integer; st text;
BEGIN
  SELECT version, status INTO v, st FROM app.beneficiaries WHERE id = NEW.id;
  IF NOT EXISTS (SELECT 1 FROM app.beneficiary_events e WHERE e.beneficiary_id = NEW.id AND e.beneficiary_version = v AND e.to_status = st) THEN
    RAISE EXCEPTION 'beneficiary % version % has no matching beneficiary_events row', NEW.id, v USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_beneficiaries_require_event AFTER INSERT OR UPDATE ON app.beneficiaries
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.beneficiaries_require_event();

CREATE TABLE app.beneficiary_information_requests (
  id             uuid        NOT NULL,
  beneficiary_id uuid        NOT NULL REFERENCES app.beneficiaries (id),
  message        text        NOT NULL,
  items          text[]      NOT NULL DEFAULT '{}',
  requested_by   uuid        NOT NULL REFERENCES app.users (id),
  requested_at   timestamptz NOT NULL DEFAULT now(),
  responded_at   timestamptz,
  CONSTRAINT pk_beneficiary_information_requests PRIMARY KEY (id),
  CONSTRAINT ck_beneficiary_information_requests_message CHECK (length(message) BETWEEN 3 AND 2000)
);
CREATE TRIGGER trg_beneficiary_information_requests_cols BEFORE UPDATE ON app.beneficiary_information_requests
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('responded_at');
CREATE TRIGGER trg_beneficiary_information_requests_no_delete BEFORE DELETE ON app.beneficiary_information_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- app.beneficiary_verifications — append-only reviewer decisions (KYC_REVIEWER, beneficiary.verification.decide).
CREATE TABLE app.beneficiary_verifications (
  id                       uuid        NOT NULL,
  beneficiary_id           uuid        NOT NULL REFERENCES app.beneficiaries (id),
  outcome                  text        NOT NULL,
  from_status              text        NOT NULL,
  to_status                text        NOT NULL,
  decided_by               uuid        REFERENCES app.users (id),     -- NULL only for SYSTEM (expiry)
  second_approver_id       uuid        REFERENCES app.users (id),
  second_approval_required boolean     NOT NULL,
  reason_code              text        NOT NULL,
  user_message             text,
  evidence_record_ids      uuid[]      NOT NULL DEFAULT '{}',
  policy_version           text        NOT NULL,
  audit_event_id           uuid        NOT NULL,
  decided_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_beneficiary_verifications PRIMARY KEY (id),
  CONSTRAINT ck_beneficiary_verifications_outcome CHECK (outcome IN ('APPROVE','APPROVE_WITH_CONDITIONS','REQUEST_INFORMATION','REJECT',
             'ESCALATE','SUSPEND','REINSTATE','REOPEN','REVOKE','EXPIRE')),
  CONSTRAINT ck_beneficiary_verifications_staff CHECK (outcome = 'EXPIRE' OR decided_by IS NOT NULL),
  CONSTRAINT ck_beneficiary_verifications_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_beneficiary_verifications_four_eyes CHECK (outcome NOT IN ('APPROVE','APPROVE_WITH_CONDITIONS') OR NOT second_approval_required
             OR (second_approver_id IS NOT NULL AND second_approver_id <> decided_by)),
  CONSTRAINT ck_beneficiary_verifications_distinct CHECK (second_approver_id IS NULL OR second_approver_id <> decided_by)
);
CREATE INDEX ix_beneficiary_verifications_beneficiary ON app.beneficiary_verifications (beneficiary_id, decided_at);
CREATE TRIGGER trg_beneficiary_verifications_no_mutation BEFORE UPDATE OR DELETE ON app.beneficiary_verifications
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
-- a decision agrees with the beneficiary's four-eyes flag, and no verifier is the owner or a member of the
-- owning organisation (operational-controls §2)
CREATE FUNCTION app.beneficiary_verifications_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE b app.beneficiaries%ROWTYPE;
BEGIN
  SELECT * INTO b FROM app.beneficiaries WHERE id = NEW.beneficiary_id;
  IF NEW.second_approval_required IS DISTINCT FROM b.requires_second_approval THEN
    RAISE EXCEPTION 'decision second_approval_required must match the beneficiary (%)', b.requires_second_approval
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  IF NEW.decided_by IS NOT NULL AND (NEW.decided_by IN (b.owner_user_id, b.created_by, b.beneficiary_user_id)
       OR NEW.second_approver_id IN (b.owner_user_id, b.created_by, b.beneficiary_user_id)
       OR EXISTS (SELECT 1 FROM app.organisation_members m WHERE m.organisation_id = b.owner_organisation_id
                    AND m.status <> 'REMOVED' AND m.user_id IN (NEW.decided_by, NEW.second_approver_id))) THEN
    RAISE EXCEPTION 'a verifier cannot be the beneficiary owner, its creator or a member of the owning organisation'
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_beneficiary_verifications_check BEFORE INSERT ON app.beneficiary_verifications
  FOR EACH ROW EXECUTE FUNCTION app.beneficiary_verifications_check();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.beneficiary_verifications, app.beneficiary_information_requests, app.beneficiary_events,
  app.beneficiary_relationships, app.beneficiaries;
DROP FUNCTION IF EXISTS app.beneficiaries_details_frozen(), app.beneficiaries_require_event(), app.beneficiary_verifications_check();
-- +goose StatementEnd
