-- FundZim migration: users, profiles, emails and phone numbers (identity, Stage 4)
-- Derived from the Stage 2 design draft design/sql/0004_users_auth.sql (validated in design/sql/validate), adapted for Stage 4
-- (ADR-032). Runs as fundzim_migrator via `fundzimctl migrate up`. Forward-only in production (ADR-028); the
-- Down section exists for local development only.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
-- app.users — one row per account. account_kind separates personal (USER) and staff (STAFF) accounts:
-- a staff identity is never the account used to run personal campaigns (SECURITY §4.2).
-- kyc_level / kyc_status are a read-only MIRROR written only by the kyc module's service (display and
-- coarse gating); decisions that need the live value call kyc. Classification: C1 (ids, status), C2 mirror.
-- Retention: account lifetime, then anonymised (never hard-deleted while financial records reference it).
CREATE TABLE app.users (
  id                      uuid        NOT NULL,
  account_kind            text        NOT NULL,
  status                  text        NOT NULL DEFAULT 'ACTIVE',
  staff_personal_user_id  uuid,        -- STAFF only: the same person's personal account (conflict checks)
  kyc_level               text        NOT NULL DEFAULT 'UNVERIFIED',
  kyc_status              text        NOT NULL DEFAULT 'ACTIVE',
  kyc_mirror_updated_at   timestamptz,
  market_code             char(2)     NOT NULL DEFAULT 'ZW',
  suspended_at            timestamptz,
  deletion_requested_at   timestamptz,
  closed_at               timestamptz,
  anonymised_at           timestamptz,
  is_system               boolean     NOT NULL DEFAULT false, -- non-login system actor (bootstrap ceremony); never authenticates
  version                 integer     NOT NULL DEFAULT 1,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_users PRIMARY KEY (id),
  CONSTRAINT uq_users_id_account_kind UNIQUE (id, account_kind),  -- target of kind-restricted composite FKs
  CONSTRAINT fk_users_market_code FOREIGN KEY (market_code) REFERENCES app.markets (code),
  CONSTRAINT fk_users_staff_personal_user_id FOREIGN KEY (staff_personal_user_id) REFERENCES app.users (id),
  CONSTRAINT uq_users_staff_personal_user_id UNIQUE (staff_personal_user_id),
  CONSTRAINT ck_users_account_kind CHECK (account_kind IN ('USER', 'STAFF')),
  CONSTRAINT ck_users_status CHECK (status IN ('ACTIVE', 'SUSPENDED', 'PENDING_DELETION', 'CLOSED')),
  CONSTRAINT ck_users_staff_link CHECK (staff_personal_user_id IS NULL OR (account_kind = 'STAFF' AND staff_personal_user_id <> id)),
  CONSTRAINT ck_users_kyc_level CHECK (kyc_level IN ('UNVERIFIED', 'BASIC_VERIFIED', 'IDENTITY_VERIFIED', 'PAYOUT_VERIFIED')),
  CONSTRAINT ck_users_kyc_status CHECK (kyc_status IN ('ACTIVE', 'PENDING_REVIEW', 'REJECTED', 'SUSPENDED')),
  CONSTRAINT ck_users_suspended_at CHECK (status <> 'SUSPENDED' OR suspended_at IS NOT NULL),
  CONSTRAINT ck_users_deletion_requested_at CHECK (status <> 'PENDING_DELETION' OR deletion_requested_at IS NOT NULL),
  CONSTRAINT ck_users_closed_at CHECK (status <> 'CLOSED' OR closed_at IS NOT NULL),
  CONSTRAINT ck_users_anonymised CHECK (anonymised_at IS NULL OR status = 'CLOSED'),
  CONSTRAINT ck_users_system_staff CHECK (NOT is_system OR account_kind = 'STAFF')
);
CREATE TRIGGER trg_users_guard_status BEFORE INSERT OR UPDATE OF status ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('user_account');
CREATE TRIGGER trg_users_bump_version BEFORE UPDATE ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_users_set_updated_at BEFORE UPDATE ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_users_keep_created_at BEFORE UPDATE ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_users_immutable_cols BEFORE UPDATE ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('account_kind');
CREATE TRIGGER trg_users_no_delete BEFORE DELETE ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_users_no_truncate BEFORE TRUNCATE ON app.users
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('user_account', '',                 'ACTIVE'),
  ('user_account', 'ACTIVE',           'SUSPENDED'),
  ('user_account', 'SUSPENDED',        'ACTIVE'),
  ('user_account', 'ACTIVE',           'PENDING_DELETION'),
  ('user_account', 'PENDING_DELETION', 'ACTIVE'),            -- user cancels within the grace period
  ('user_account', 'PENDING_DELETION', 'CLOSED'),
  ('user_account', 'SUSPENDED',        'CLOSED');            -- staff closes a suspended account (reason required in service)
CREATE TRIGGER trg_users_system_immutable BEFORE UPDATE ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('is_system');

-- app.user_profiles — display data. Classification: C2 (display_name C0 only where the user publishes it).
CREATE TABLE app.user_profiles (
  id               uuid        NOT NULL,
  user_id          uuid        NOT NULL,
  display_name     text,                                   -- NULL after anonymisation
  preferred_locale text        NOT NULL DEFAULT 'en-ZW',
  time_zone        text        NOT NULL DEFAULT 'Africa/Harare',
  country_code     char(2),                                -- self-declared country of residence
  avatar_object_id uuid,                                   -- FK app.stored_objects added in 0005
  avatar_bucket_class text       NOT NULL DEFAULT 'PUBLIC_MEDIA',  -- pins the FK to public media
  version          integer     NOT NULL DEFAULT 1,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_user_profiles PRIMARY KEY (id),
  CONSTRAINT uq_user_profiles_user_id UNIQUE (user_id),
  CONSTRAINT fk_user_profiles_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_user_profiles_display_name CHECK (display_name IS NULL OR length(display_name) BETWEEN 1 AND 100),
  CONSTRAINT ck_user_profiles_avatar_bucket_class CHECK (avatar_bucket_class = 'PUBLIC_MEDIA'),
  CONSTRAINT ck_user_profiles_country_code CHECK (country_code IS NULL OR country_code ~ '^[A-Z]{2}$')
);
CREATE TRIGGER trg_user_profiles_bump_version BEFORE UPDATE ON app.user_profiles
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_user_profiles_set_updated_at BEFORE UPDATE ON app.user_profiles
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_user_profiles_keep_created_at BEFORE UPDATE ON app.user_profiles
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();

-- app.user_emails — Classification: C2. A verified, non-deleted normalised email belongs to at most one
-- account; unverified duplicates are allowed so nobody can squat an address without proving control.
-- Anonymisation sets email/email_normalized to NULL (deleted_at must be set).
CREATE TABLE app.user_emails (
  id               uuid        NOT NULL,
  user_id          uuid        NOT NULL,
  email            text,                                   -- as entered (display)
  email_normalized text,                                   -- lower(trim(email)); IDN/plus-address rules in Go
  is_primary       boolean     NOT NULL DEFAULT false,
  is_login         boolean     NOT NULL DEFAULT false,     -- the sign-in address (ADR-032); one account per address
  verified_at      timestamptz,
  deleted_at       timestamptz,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_user_emails PRIMARY KEY (id),
  CONSTRAINT fk_user_emails_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_user_emails_present CHECK ((email IS NOT NULL AND email_normalized IS NOT NULL) OR deleted_at IS NOT NULL),
  CONSTRAINT ck_user_emails_normalized CHECK (email_normalized IS NULL OR
    (email_normalized = lower(email_normalized) AND email_normalized ~ '^[^@\s]+@[^@\s]+\.[^@\s]+$' AND length(email_normalized) <= 254)),
  CONSTRAINT ck_user_emails_primary_verified CHECK (NOT is_primary OR verified_at IS NOT NULL)
);
CREATE UNIQUE INDEX uq_user_emails_verified_email ON app.user_emails (email_normalized)
  WHERE verified_at IS NOT NULL AND deleted_at IS NULL;
-- One account per sign-in address, verified or not (concurrent registrations collide here, ADR-032).
CREATE UNIQUE INDEX uq_user_emails_login ON app.user_emails (email_normalized) WHERE is_login AND deleted_at IS NULL;
CREATE UNIQUE INDEX uq_user_emails_login_per_user ON app.user_emails (user_id) WHERE is_login AND deleted_at IS NULL;
CREATE UNIQUE INDEX uq_user_emails_primary ON app.user_emails (user_id) WHERE is_primary AND deleted_at IS NULL;
CREATE UNIQUE INDEX uq_user_emails_user_email ON app.user_emails (user_id, email_normalized) WHERE deleted_at IS NULL;
CREATE INDEX ix_user_emails_email_normalized ON app.user_emails (email_normalized) WHERE deleted_at IS NULL;
CREATE TRIGGER trg_user_emails_set_updated_at BEFORE UPDATE ON app.user_emails
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_user_emails_keep_created_at BEFORE UPDATE ON app.user_emails
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_user_emails_immutable_cols BEFORE UPDATE ON app.user_emails
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('user_id');

-- app.user_phone_numbers — Classification: C2 (masked in logs: +26377****123). Same uniqueness rule as
-- emails. Phone-number recycling: when a second account verifies a number, the unique index rejects it;
-- the auth service runs the recycle flow (old record deleted_at + payout hold on the old account).
CREATE TABLE app.user_phone_numbers (
  id          uuid        NOT NULL,
  user_id     uuid        NOT NULL,
  e164        text,                                        -- normalised E.164, e.g. +263771234567
  is_primary  boolean     NOT NULL DEFAULT false,
  verified_at timestamptz,
  deleted_at  timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_user_phone_numbers PRIMARY KEY (id),
  CONSTRAINT fk_user_phone_numbers_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_user_phone_numbers_present CHECK (e164 IS NOT NULL OR deleted_at IS NOT NULL),
  CONSTRAINT ck_user_phone_numbers_e164 CHECK (e164 IS NULL OR e164 ~ '^\+[1-9][0-9]{6,14}$'),
  CONSTRAINT ck_user_phone_numbers_primary_verified CHECK (NOT is_primary OR verified_at IS NOT NULL)
);
CREATE UNIQUE INDEX uq_user_phone_numbers_verified_e164 ON app.user_phone_numbers (e164)
  WHERE verified_at IS NOT NULL AND deleted_at IS NULL;
CREATE UNIQUE INDEX uq_user_phone_numbers_primary ON app.user_phone_numbers (user_id) WHERE is_primary AND deleted_at IS NULL;
CREATE UNIQUE INDEX uq_user_phone_numbers_user_e164 ON app.user_phone_numbers (user_id, e164) WHERE deleted_at IS NULL;
CREATE INDEX ix_user_phone_numbers_e164 ON app.user_phone_numbers (e164) WHERE deleted_at IS NULL;
CREATE TRIGGER trg_user_phone_numbers_set_updated_at BEFORE UPDATE ON app.user_phone_numbers
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_user_phone_numbers_keep_created_at BEFORE UPDATE ON app.user_phone_numbers
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_user_phone_numbers_immutable_cols BEFORE UPDATE ON app.user_phone_numbers
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('user_id');

-- Deferred FK from the Stage 3 platform migration (design-baseline I-16): added NOT VALID, then validated.
ALTER TABLE app.feature_flag_changes
  ADD CONSTRAINT fk_feature_flag_changes_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id) NOT VALID;
ALTER TABLE app.feature_flag_changes VALIDATE CONSTRAINT fk_feature_flag_changes_approved_by;
-- requested_by stays without FK: the Stage 3 seed row uses the all-zero system actor id.

-- The non-login system actor used by audited ceremonies (super-admin bootstrap, ADR-032). It has no
-- credentials and auth refuses any session for is_system accounts.
INSERT INTO app.users (id, account_kind, status, is_system) VALUES
  ('00000000-0000-0000-0000-000000000001', 'STAFF', 'ACTIVE', true);

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.feature_flag_changes DROP CONSTRAINT IF EXISTS fk_feature_flag_changes_approved_by;
DROP TABLE IF EXISTS app.user_phone_numbers, app.user_emails, app.user_profiles, app.users;
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine = 'user_account';
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
