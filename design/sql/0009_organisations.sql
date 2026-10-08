-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0009: organisations module — organisations, organisation_roles, organisation_members,
-- organisation_invitations, organisation_verifications (KYB projection). Schema: app.
-- Docs: docs/database/organisation-beneficiary-schema.md, docs/compliance/kyb-architecture.md.
-- The authoritative KYB record (legal name, registration, persons, beneficial owners, fundraising
-- authorities) is in the kyc schema (kyc.kyb_organisations etc., FK kyc → app.organisations). Nothing here
-- references kyc (baseline §10).
-- =====================================================================================================

-- app.organisations — the platform-facing organisation account. Classification: C0 once verified and
-- published (display_name, org_type, description), C2 otherwise. Retention: account lifetime; never
-- hard-deleted while campaigns or financial records reference it.
CREATE TABLE app.organisations (
  id                 uuid        NOT NULL,
  display_name       text        NOT NULL,
  slug               text        NOT NULL,
  org_type           text        NOT NULL,
  status             text        NOT NULL DEFAULT 'ACTIVE',  -- account status; KYB level/status is in organisation_verifications
  description        text,
  logo_object_id     uuid,
  logo_bucket_class  text        NOT NULL DEFAULT 'PUBLIC_MEDIA',
  market_code        char(2)     NOT NULL DEFAULT 'ZW',
  created_by_user_id uuid        NOT NULL,
  suspended_at       timestamptz,
  closed_at          timestamptz,
  version            integer     NOT NULL DEFAULT 1,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_organisations PRIMARY KEY (id),
  CONSTRAINT uq_organisations_slug UNIQUE (slug),
  CONSTRAINT fk_organisations_logo FOREIGN KEY (logo_object_id, logo_bucket_class) REFERENCES app.stored_objects (id, bucket_class),
  CONSTRAINT fk_organisations_market_code FOREIGN KEY (market_code) REFERENCES app.markets (code),
  CONSTRAINT fk_organisations_created_by_user_id FOREIGN KEY (created_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_organisations_logo_bucket_class CHECK (logo_bucket_class = 'PUBLIC_MEDIA'),
  CONSTRAINT ck_organisations_org_type CHECK (org_type IN ('COMPANY', 'TRUST', 'PVO', 'FAITH_BASED', 'SCHOOL', 'HEALTH_INSTITUTION',
                                                           'COMMUNITY_BASED', 'SPORTS_CLUB', 'OTHER')),
  CONSTRAINT ck_organisations_status CHECK (status IN ('ACTIVE', 'SUSPENDED', 'CLOSED')),
  CONSTRAINT ck_organisations_slug CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 80),
  CONSTRAINT ck_organisations_display_name CHECK (length(display_name) BETWEEN 2 AND 150),
  CONSTRAINT ck_organisations_suspended CHECK (status <> 'SUSPENDED' OR suspended_at IS NOT NULL),
  CONSTRAINT ck_organisations_closed CHECK (status <> 'CLOSED' OR closed_at IS NOT NULL)
);
CREATE INDEX ix_organisations_created_by_user_id ON app.organisations (created_by_user_id);
CREATE TRIGGER trg_organisations_bump_version BEFORE UPDATE ON app.organisations
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_organisations_set_updated_at BEFORE UPDATE ON app.organisations
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_organisations_keep_created_at BEFORE UPDATE ON app.organisations
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_organisations_immutable_cols BEFORE UPDATE ON app.organisations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('created_by_user_id');
CREATE TRIGGER trg_organisations_no_delete BEFORE DELETE ON app.organisations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- app.organisation_roles — organisation-scoped roles (reference data). Classification: C1.
-- MVP seeds ORG_ADMIN and ORG_MEMBER only (baseline I-1): organisation financial actions (payout request,
-- payout-destination change) are ORG_ADMIN only. A finance-only role (e.g. ORG_FINANCE) can be added later
-- as a data row without a schema change.
CREATE TABLE app.organisation_roles (
  id          uuid        NOT NULL,
  code        text        NOT NULL,
  name        text        NOT NULL,
  permissions text[]      NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_organisation_roles PRIMARY KEY (id),
  CONSTRAINT uq_organisation_roles_code UNIQUE (code),
  CONSTRAINT ck_organisation_roles_code CHECK (code ~ '^ORG_[A-Z_]+$'),
  CONSTRAINT ck_organisation_roles_permissions CHECK (permissions <@ ARRAY[
    'org.view', 'org.settings.manage', 'org.member.manage', 'org.campaign.create', 'org.campaign.edit',
    'org.campaign.submit', 'org.payout.request', 'org.payout_destination.manage', 'org.statement.view']::text[])
);
CREATE TRIGGER trg_organisation_roles_no_delete BEFORE DELETE ON app.organisation_roles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_organisation_roles_immutable_cols BEFORE UPDATE ON app.organisation_roles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'code');

INSERT INTO app.organisation_roles (id, code, name, permissions) VALUES
  (md5('org_role:ORG_ADMIN')::uuid,   'ORG_ADMIN',   'Organisation administrator',
   ARRAY['org.view', 'org.settings.manage', 'org.member.manage', 'org.campaign.create', 'org.campaign.edit',
         'org.campaign.submit', 'org.payout.request', 'org.payout_destination.manage', 'org.statement.view']),
  (md5('org_role:ORG_MEMBER')::uuid,  'ORG_MEMBER',  'Organisation member',
   ARRAY['org.view', 'org.campaign.create', 'org.campaign.edit']);

-- app.organisation_members — personal (USER) accounts only; staff accounts can never be members.
-- Invariant: an ACTIVE organisation always has at least one ACTIVE ORG_ADMIN (deferred check).
-- Classification: C2. Retention: account lifetime (+ AUDIT via audit events).
CREATE TABLE app.organisation_members (
  id                   uuid        NOT NULL,
  organisation_id      uuid        NOT NULL,
  user_id              uuid        NOT NULL,
  user_account_kind    text        NOT NULL DEFAULT 'USER',
  organisation_role_id uuid        NOT NULL,
  status               text        NOT NULL DEFAULT 'ACTIVE',
  invitation_id        uuid,
  joined_at            timestamptz NOT NULL DEFAULT now(),
  removed_at           timestamptz,
  removed_by_user_id   uuid,
  version              integer     NOT NULL DEFAULT 1,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_organisation_members PRIMARY KEY (id),
  CONSTRAINT fk_organisation_members_organisation_id FOREIGN KEY (organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_organisation_members_user FOREIGN KEY (user_id, user_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_organisation_members_organisation_role_id FOREIGN KEY (organisation_role_id) REFERENCES app.organisation_roles (id),
  CONSTRAINT fk_organisation_members_removed_by_user_id FOREIGN KEY (removed_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_organisation_members_user_kind CHECK (user_account_kind = 'USER'),
  CONSTRAINT ck_organisation_members_status CHECK (status IN ('ACTIVE', 'SUSPENDED', 'REMOVED')),
  CONSTRAINT ck_organisation_members_removed CHECK ((status = 'REMOVED') = (removed_at IS NOT NULL))
);
CREATE UNIQUE INDEX uq_organisation_members_active ON app.organisation_members (organisation_id, user_id) WHERE status <> 'REMOVED';
CREATE INDEX ix_organisation_members_user_id ON app.organisation_members (user_id) WHERE status <> 'REMOVED';
CREATE TRIGGER trg_organisation_members_bump_version BEFORE UPDATE ON app.organisation_members
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_organisation_members_set_updated_at BEFORE UPDATE ON app.organisation_members
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_organisation_members_immutable_cols BEFORE UPDATE ON app.organisation_members
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('organisation_id', 'user_id', 'joined_at', 'created_at');
CREATE TRIGGER trg_organisation_members_no_delete BEFORE DELETE ON app.organisation_members
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
-- A removed membership is final (re-joining creates a new row).
CREATE FUNCTION app.organisation_members_removed_final() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status = 'REMOVED' THEN
    RAISE EXCEPTION 'removed organisation membership % is final', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_organisation_members_removed_final BEFORE UPDATE ON app.organisation_members
  FOR EACH ROW EXECUTE FUNCTION app.organisation_members_removed_final();

CREATE FUNCTION app.organisations_require_admin() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_org uuid := (to_jsonb(NEW) ->> CASE WHEN TG_TABLE_NAME = 'organisations' THEN 'id' ELSE 'organisation_id' END)::uuid;
BEGIN
  IF EXISTS (SELECT 1 FROM app.organisations o WHERE o.id = v_org AND o.status = 'ACTIVE')
     AND NOT EXISTS (SELECT 1 FROM app.organisation_members m
                      WHERE m.organisation_id = v_org AND m.status = 'ACTIVE'
                        AND m.organisation_role_id = md5('org_role:ORG_ADMIN')::uuid) THEN
    RAISE EXCEPTION 'organisation % must have at least one active ORG_ADMIN', v_org
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_organisations_require_admin AFTER INSERT OR UPDATE ON app.organisations
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.organisations_require_admin();
CREATE CONSTRAINT TRIGGER trg_organisation_members_require_admin AFTER INSERT OR UPDATE ON app.organisation_members
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.organisations_require_admin();

-- app.organisation_invitations — invitation tokens are stored as SHA-256 only (C4 token never stored).
-- Classification: C2 (invitee contact). Retention: OPERATIONAL.
CREATE TABLE app.organisation_invitations (
  id                   uuid        NOT NULL,
  organisation_id      uuid        NOT NULL,
  organisation_role_id uuid        NOT NULL,
  invited_email_normalized text,
  invited_phone_e164   text,
  token_hash           bytea       NOT NULL,
  invited_by_user_id   uuid        NOT NULL,
  status               text        NOT NULL DEFAULT 'PENDING',
  expires_at           timestamptz NOT NULL,
  accepted_by_user_id  uuid,
  accepted_at          timestamptz,
  revoked_at           timestamptz,
  revoked_by_user_id   uuid,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_organisation_invitations PRIMARY KEY (id),
  CONSTRAINT uq_organisation_invitations_token_hash UNIQUE (token_hash),
  CONSTRAINT fk_organisation_invitations_organisation_id FOREIGN KEY (organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT fk_organisation_invitations_organisation_role_id FOREIGN KEY (organisation_role_id) REFERENCES app.organisation_roles (id),
  CONSTRAINT fk_organisation_invitations_invited_by FOREIGN KEY (invited_by_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_organisation_invitations_accepted_by FOREIGN KEY (accepted_by_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_organisation_invitations_revoked_by FOREIGN KEY (revoked_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_organisation_invitations_one_contact CHECK (num_nonnulls(invited_email_normalized, invited_phone_e164) = 1),
  CONSTRAINT ck_organisation_invitations_phone CHECK (invited_phone_e164 IS NULL OR invited_phone_e164 ~ '^\+[1-9][0-9]{6,14}$'),
  CONSTRAINT ck_organisation_invitations_token_hash CHECK (octet_length(token_hash) = 32),
  CONSTRAINT ck_organisation_invitations_status CHECK (status IN ('PENDING', 'ACCEPTED', 'DECLINED', 'REVOKED', 'EXPIRED')),
  CONSTRAINT ck_organisation_invitations_accepted CHECK ((status = 'ACCEPTED') = (accepted_by_user_id IS NOT NULL AND accepted_at IS NOT NULL)),
  CONSTRAINT ck_organisation_invitations_revoked CHECK ((status = 'REVOKED') = (revoked_at IS NOT NULL)),
  CONSTRAINT ck_organisation_invitations_expiry CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX uq_organisation_invitations_pending_email ON app.organisation_invitations (organisation_id, invited_email_normalized)
  WHERE status = 'PENDING' AND invited_email_normalized IS NOT NULL;
CREATE UNIQUE INDEX uq_organisation_invitations_pending_phone ON app.organisation_invitations (organisation_id, invited_phone_e164)
  WHERE status = 'PENDING' AND invited_phone_e164 IS NOT NULL;
CREATE TRIGGER trg_organisation_invitations_set_updated_at BEFORE UPDATE ON app.organisation_invitations
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_organisation_invitations_cols BEFORE UPDATE ON app.organisation_invitations
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'accepted_by_user_id', 'accepted_at', 'revoked_at', 'revoked_by_user_id');

ALTER TABLE app.organisation_members ADD CONSTRAINT fk_organisation_members_invitation_id
  FOREIGN KEY (invitation_id) REFERENCES app.organisation_invitations (id);

-- app.organisation_verifications — read-only projection of the KYB level/status, fed by kyc events
-- through the outbox (display and filtering only; payout eligibility calls kyc directly).
-- Out-of-order events are rejected: the projection only moves forward in source time.
-- Classification: C1 (level badge is C0 once published). Retention: account lifetime.
CREATE TABLE app.organisation_verifications (
  id                  uuid        NOT NULL,
  organisation_id     uuid        NOT NULL,
  kyb_level           text        NOT NULL DEFAULT 'ORG_UNVERIFIED',
  kyb_status          text        NOT NULL DEFAULT 'ACTIVE',
  source_event_id     uuid,                                -- outbox event that produced the current values
  source_occurred_at  timestamptz,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_organisation_verifications PRIMARY KEY (id),
  CONSTRAINT uq_organisation_verifications_organisation_id UNIQUE (organisation_id),
  CONSTRAINT fk_organisation_verifications_organisation_id FOREIGN KEY (organisation_id) REFERENCES app.organisations (id),
  CONSTRAINT ck_organisation_verifications_kyb_level CHECK (kyb_level IN ('ORG_UNVERIFIED', 'ORG_REGISTERED_VERIFIED',
                                                                          'ORG_KYB_VERIFIED', 'ORG_PAYOUT_VERIFIED')),
  CONSTRAINT ck_organisation_verifications_kyb_status CHECK (kyb_status IN ('ACTIVE', 'PENDING_REVIEW', 'REJECTED', 'SUSPENDED')),
  CONSTRAINT ck_organisation_verifications_source CHECK ((source_event_id IS NULL) = (source_occurred_at IS NULL))
);
CREATE FUNCTION app.organisation_verifications_forward_only() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.organisation_id <> OLD.organisation_id THEN
    RAISE EXCEPTION 'organisation_id is immutable' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.source_occurred_at IS NOT NULL AND (NEW.source_occurred_at IS NULL OR NEW.source_occurred_at < OLD.source_occurred_at) THEN
    RAISE EXCEPTION 'stale KYB projection update (% < %)', NEW.source_occurred_at, OLD.source_occurred_at
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_organisation_verifications_forward_only BEFORE UPDATE ON app.organisation_verifications
  FOR EACH ROW EXECUTE FUNCTION app.organisation_verifications_forward_only();
CREATE TRIGGER trg_organisation_verifications_set_updated_at BEFORE UPDATE ON app.organisation_verifications
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
