-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0004: users module (users, user_profiles, user_emails, user_phone_numbers) and auth module
-- (authentication_identities, password_credentials, otp_challenges, sessions, mfa_methods, recovery_codes,
-- roles, permissions, role_permissions, role_assignments, role_assignment_requests, security_events,
-- break_glass_grants, staff_conflict_declarations).
-- Schema: app. Docs: docs/database/identity-schema.md, docs/SECURITY.md §4–§6, ADR-027.
-- Secrets never stored: passwords as Argon2id PHC strings, OTP codes as HMAC-SHA-256, session tokens as
-- SHA-256, TOTP seeds as application-encrypted ciphertext, recovery codes as keyed hashes.
-- =====================================================================================================

-- =====================================================================================================
-- users module
-- =====================================================================================================

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
  CONSTRAINT ck_users_anonymised CHECK (anonymised_at IS NULL OR status = 'CLOSED')
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

-- Deferred FKs from 0002 (platform tables created before users existed).
ALTER TABLE app.feature_flag_changes
  ADD CONSTRAINT fk_feature_flag_changes_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id);
-- requested_by is not an FK: the migration seed row uses the all-zero system actor id.

-- =====================================================================================================
-- auth module
-- =====================================================================================================

-- app.mfa_methods — WebAuthn credentials (public keys, C2) and TOTP seeds (C4 secret → stored only as
-- application-encrypted ciphertext with key class 'mfa-secret'). Classification: C2 / C4-encrypted.
CREATE TABLE app.mfa_methods (
  id                       uuid        NOT NULL,
  user_id                  uuid        NOT NULL,
  method_type              text        NOT NULL,
  label                    text        NOT NULL,
  webauthn_credential_id   bytea,
  webauthn_public_key      bytea,                          -- COSE key
  webauthn_sign_count      bigint,
  webauthn_aaguid          uuid,
  webauthn_transports      text[],
  totp_secret_ciphertext   bytea,
  totp_secret_key_id       text,
  totp_last_used_step      bigint,                         -- replay protection: a TOTP step is accepted once
  confirmed_at             timestamptz,
  last_used_at             timestamptz,
  disabled_at              timestamptz,
  created_at               timestamptz NOT NULL DEFAULT now(),
  updated_at               timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_mfa_methods PRIMARY KEY (id),
  CONSTRAINT fk_mfa_methods_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT uq_mfa_methods_webauthn_credential_id UNIQUE (webauthn_credential_id),
  CONSTRAINT ck_mfa_methods_method_type CHECK (method_type IN ('WEBAUTHN', 'TOTP')),
  CONSTRAINT ck_mfa_methods_webauthn CHECK (method_type <> 'WEBAUTHN' OR
    (webauthn_credential_id IS NOT NULL AND webauthn_public_key IS NOT NULL AND webauthn_sign_count >= 0
     AND totp_secret_ciphertext IS NULL)),
  CONSTRAINT ck_mfa_methods_totp CHECK (method_type <> 'TOTP' OR
    (totp_secret_ciphertext IS NOT NULL AND totp_secret_key_id IS NOT NULL AND webauthn_credential_id IS NULL)),
  CONSTRAINT ck_mfa_methods_label CHECK (length(label) BETWEEN 1 AND 64)
);
CREATE INDEX ix_mfa_methods_user_id ON app.mfa_methods (user_id) WHERE disabled_at IS NULL;
CREATE TRIGGER trg_mfa_methods_set_updated_at BEFORE UPDATE ON app.mfa_methods
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_mfa_methods_keep_created_at BEFORE UPDATE ON app.mfa_methods
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_mfa_methods_immutable_cols BEFORE UPDATE ON app.mfa_methods
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('user_id', 'method_type', 'webauthn_credential_id', 'webauthn_public_key');

-- app.authentication_identities — which primary login factors a user has (ADR-027).
-- Classification: C1 (references only).
CREATE TABLE app.authentication_identities (
  id                   uuid        NOT NULL,
  user_id              uuid        NOT NULL,
  identity_type        text        NOT NULL,
  user_phone_number_id uuid,
  user_email_id        uuid,
  mfa_method_id        uuid,                               -- WEBAUTHN passkey used as a primary factor
  user_account_kind    text        NOT NULL,               -- copied from users (composite FK) for the staff rule
  disabled_at          timestamptz,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_authentication_identities PRIMARY KEY (id),
  CONSTRAINT fk_authentication_identities_user FOREIGN KEY (user_id, user_account_kind) REFERENCES app.users (id, account_kind),
  -- Staff first factor is a password only (plus mandatory WebAuthn/TOTP); never email or SMS OTP (baseline I-6).
  CONSTRAINT ck_authentication_identities_staff_no_otp CHECK (user_account_kind = 'USER' OR identity_type IN ('PASSWORD', 'WEBAUTHN')),
  CONSTRAINT fk_authentication_identities_user_phone_number_id FOREIGN KEY (user_phone_number_id) REFERENCES app.user_phone_numbers (id),
  CONSTRAINT fk_authentication_identities_user_email_id FOREIGN KEY (user_email_id) REFERENCES app.user_emails (id),
  CONSTRAINT fk_authentication_identities_mfa_method_id FOREIGN KEY (mfa_method_id) REFERENCES app.mfa_methods (id),
  CONSTRAINT ck_authentication_identities_type CHECK (identity_type IN ('PHONE_OTP', 'EMAIL_OTP', 'PASSWORD', 'WEBAUTHN')),
  CONSTRAINT ck_authentication_identities_refs CHECK (
       (identity_type = 'PHONE_OTP' AND user_phone_number_id IS NOT NULL AND user_email_id IS NULL AND mfa_method_id IS NULL)
    OR (identity_type = 'EMAIL_OTP' AND user_email_id IS NOT NULL AND user_phone_number_id IS NULL AND mfa_method_id IS NULL)
    OR (identity_type = 'PASSWORD'  AND user_phone_number_id IS NULL AND user_email_id IS NULL AND mfa_method_id IS NULL)
    OR (identity_type = 'WEBAUTHN'  AND mfa_method_id IS NOT NULL AND user_phone_number_id IS NULL AND user_email_id IS NULL))
);
CREATE UNIQUE INDEX uq_authentication_identities_phone ON app.authentication_identities (user_phone_number_id) WHERE disabled_at IS NULL;
CREATE UNIQUE INDEX uq_authentication_identities_email ON app.authentication_identities (user_email_id) WHERE disabled_at IS NULL;
CREATE UNIQUE INDEX uq_authentication_identities_password ON app.authentication_identities (user_id)
  WHERE identity_type = 'PASSWORD' AND disabled_at IS NULL;
CREATE INDEX ix_authentication_identities_user_id ON app.authentication_identities (user_id);
CREATE TRIGGER trg_authentication_identities_set_updated_at BEFORE UPDATE ON app.authentication_identities
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_authentication_identities_cols BEFORE UPDATE ON app.authentication_identities
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('disabled_at');

-- app.password_credentials — optional password factor. Argon2id PHC string only (C3 per
-- DATA-CLASSIFICATION §1). History rows are kept (superseded_at) for reuse checks; the hash never changes.
CREATE TABLE app.password_credentials (
  id             uuid        NOT NULL,
  user_id        uuid        NOT NULL,
  password_hash  text        NOT NULL,
  must_change    boolean     NOT NULL DEFAULT false,
  compromised_at timestamptz,                              -- matched a breached-password list later
  superseded_at  timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_password_credentials PRIMARY KEY (id),
  CONSTRAINT fk_password_credentials_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_password_credentials_argon2id CHECK (password_hash ~ '^\$argon2id\$v=[0-9]+\$m=[0-9]+,t=[0-9]+,p=[0-9]+\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$')
);
CREATE UNIQUE INDEX uq_password_credentials_current ON app.password_credentials (user_id) WHERE superseded_at IS NULL;
CREATE TRIGGER trg_password_credentials_cols BEFORE UPDATE ON app.password_credentials
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('must_change', 'compromised_at', 'superseded_at');
CREATE TRIGGER trg_password_credentials_no_delete BEFORE DELETE ON app.password_credentials
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- app.otp_challenges — one-time codes (SECURITY §4.1). code_hmac = HMAC-SHA-256(server key, code ||
-- challenge id); the plain code (C4) is never stored. destination_hmac is the HMAC blind index of the
-- normalised phone/email (binding, rate limits, resend invalidation) so the challenge table holds no
-- contact data. Purpose-bound: a code for one purpose cannot be replayed for another.
-- TTL <= 5 minutes and <= 5 attempts are fixed security rules (SECURITY §4.1), so they are CHECKs.
-- Classification: C3-handled (hashes). Retention: OPERATIONAL (purged shortly after expiry).
CREATE TABLE app.otp_challenges (
  id               uuid        NOT NULL,
  user_id          uuid,                                   -- NULL for a sign-up/login of an unknown destination
  purpose          text        NOT NULL,
  channel          text        NOT NULL,
  destination_hmac bytea       NOT NULL,
  code_hmac        bytea       NOT NULL,
  hmac_key_id      text        NOT NULL,
  flow_id          uuid        NOT NULL,                   -- binds the code to the browser flow that requested it
  attempts         smallint    NOT NULL DEFAULT 0,
  requested_ip     inet,
  created_at       timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,
  consumed_at      timestamptz,
  invalidated_at   timestamptz,                            -- superseded by a resend, or locked after max attempts
  CONSTRAINT pk_otp_challenges PRIMARY KEY (id),
  CONSTRAINT fk_otp_challenges_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_otp_challenges_purpose CHECK (purpose IN ('LOGIN', 'VERIFY_PHONE', 'VERIFY_EMAIL', 'PAYOUT_DESTINATION_CHANGE',
                                                          'STEP_UP', 'ACCOUNT_RECOVERY', 'CONTACT_CHANGE', 'BENEFICIARY_CONSENT')),
  CONSTRAINT ck_otp_challenges_channel CHECK (channel IN ('SMS', 'EMAIL', 'WHATSAPP')),
  CONSTRAINT ck_otp_challenges_destination_hmac CHECK (octet_length(destination_hmac) = 32),
  CONSTRAINT ck_otp_challenges_code_hmac CHECK (octet_length(code_hmac) = 32),
  CONSTRAINT ck_otp_challenges_attempts CHECK (attempts BETWEEN 0 AND 5),
  CONSTRAINT ck_otp_challenges_ttl CHECK (expires_at > created_at AND expires_at <= created_at + interval '5 minutes'),
  CONSTRAINT ck_otp_challenges_consumed CHECK (consumed_at IS NULL OR (consumed_at <= expires_at AND invalidated_at IS NULL)),
  CONSTRAINT ck_otp_challenges_user_bound CHECK (purpose IN ('LOGIN', 'VERIFY_PHONE', 'VERIFY_EMAIL', 'BENEFICIARY_CONSENT') OR user_id IS NOT NULL)
);
-- At most one live challenge per destination and purpose (a resend invalidates the previous one).
CREATE UNIQUE INDEX uq_otp_challenges_live ON app.otp_challenges (destination_hmac, purpose)
  WHERE consumed_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX ix_otp_challenges_expires_at ON app.otp_challenges (expires_at);
CREATE INDEX ix_otp_challenges_destination_created ON app.otp_challenges (destination_hmac, created_at);  -- resend rate limits

-- Only attempts (monotonically increasing), consumed_at and invalidated_at may change, and a consumed or
-- invalidated challenge is frozen.
CREATE FUNCTION app.otp_challenges_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(OLD) - ARRAY['attempts', 'consumed_at', 'invalidated_at'])
     IS DISTINCT FROM (to_jsonb(NEW) - ARRAY['attempts', 'consumed_at', 'invalidated_at']) THEN
    RAISE EXCEPTION 'otp challenge is immutable except attempts/consumed_at/invalidated_at' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.consumed_at IS NOT NULL OR OLD.invalidated_at IS NOT NULL THEN
    RAISE EXCEPTION 'otp challenge % is already consumed or invalidated', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.attempts < OLD.attempts THEN
    RAISE EXCEPTION 'otp attempts cannot decrease' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_otp_challenges_guard BEFORE UPDATE ON app.otp_challenges
  FOR EACH ROW EXECUTE FUNCTION app.otp_challenges_guard();

-- app.sessions — opaque server-side sessions (SECURITY §6, ADR-027). Only SHA-256(token) is stored.
-- kind must equal the user's account_kind (composite FK). A STAFF session can only exist after MFA.
-- Timeouts are configuration (users idle 7 d / absolute 30 d; staff 15 min / 12 h as starting points).
-- Classification: C2 (device info, IP); token_hash C3-handled. Retention: OPERATIONAL.
CREATE TABLE app.sessions (
  id                  uuid        NOT NULL,
  user_id             uuid        NOT NULL,
  kind                text        NOT NULL,
  token_hash          bytea       NOT NULL,
  auth_method         text        NOT NULL,                -- primary factor used
  mfa_verified_at     timestamptz,
  step_up_at          timestamptz,                         -- last fresh factor (step-up freshness window)
  created_at          timestamptz NOT NULL DEFAULT now(),
  last_seen_at        timestamptz NOT NULL DEFAULT now(),
  idle_expires_at     timestamptz NOT NULL,
  absolute_expires_at timestamptz NOT NULL,
  revoked_at          timestamptz,
  revoked_reason      text,
  rotated_from_id     uuid,
  ip                  inet,
  user_agent          text,
  device_label        text,
  CONSTRAINT pk_sessions PRIMARY KEY (id),
  CONSTRAINT uq_sessions_token_hash UNIQUE (token_hash),
  CONSTRAINT fk_sessions_user_id_kind FOREIGN KEY (user_id, kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_sessions_rotated_from_id FOREIGN KEY (rotated_from_id) REFERENCES app.sessions (id) ON DELETE SET NULL,
  CONSTRAINT ck_sessions_kind CHECK (kind IN ('USER', 'STAFF')),
  CONSTRAINT ck_sessions_token_hash CHECK (octet_length(token_hash) = 32),
  CONSTRAINT ck_sessions_auth_method CHECK (auth_method IN ('PHONE_OTP', 'EMAIL_OTP', 'PASSWORD', 'WEBAUTHN', 'RECOVERY_CODE')),
  CONSTRAINT ck_sessions_staff_mfa CHECK (kind <> 'STAFF' OR mfa_verified_at IS NOT NULL),
  CONSTRAINT ck_sessions_staff_first_factor CHECK (kind <> 'STAFF' OR auth_method IN ('PASSWORD', 'WEBAUTHN')),  -- baseline I-6
  CONSTRAINT ck_sessions_expiry CHECK (idle_expires_at > created_at AND idle_expires_at <= absolute_expires_at),
  CONSTRAINT ck_sessions_revoked CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL)),
  CONSTRAINT ck_sessions_revoked_reason CHECK (revoked_reason IS NULL OR revoked_reason IN (
    'LOGOUT', 'ROTATED', 'USER_REVOKED', 'STAFF_REVOKED', 'PRIMARY_FACTOR_CHANGED', 'ATO_SIGNAL',
    'PRIVILEGE_CHANGE', 'ACCOUNT_SUSPENDED', 'ACCOUNT_CLOSED')),
  CONSTRAINT ck_sessions_user_agent CHECK (user_agent IS NULL OR length(user_agent) <= 512)
);
CREATE INDEX ix_sessions_user_id_active ON app.sessions (user_id) WHERE revoked_at IS NULL;
CREATE INDEX ix_sessions_absolute_expires_at ON app.sessions (absolute_expires_at);
CREATE TRIGGER trg_sessions_immutable_cols BEFORE UPDATE ON app.sessions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('user_id', 'kind', 'token_hash', 'auth_method', 'created_at', 'absolute_expires_at');
-- A revoked session is never revived.
CREATE FUNCTION app.sessions_no_unrevoke() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
    RAISE EXCEPTION 'revoked session % cannot be changed', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_sessions_no_unrevoke BEFORE UPDATE ON app.sessions
  FOR EACH ROW EXECUTE FUNCTION app.sessions_no_unrevoke();

-- app.recovery_codes — MFA recovery codes (high-entropy), stored only as a keyed SHA-256 hash.
-- Regeneration supersedes the whole batch. Classification: C3-handled.
CREATE TABLE app.recovery_codes (
  id            uuid        NOT NULL,
  user_id       uuid        NOT NULL,
  batch_id      uuid        NOT NULL,
  code_hash     bytea       NOT NULL,
  hash_key_id   text        NOT NULL,
  used_at       timestamptz,
  superseded_at timestamptz,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_recovery_codes PRIMARY KEY (id),
  CONSTRAINT fk_recovery_codes_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT uq_recovery_codes_user_code UNIQUE (user_id, code_hash),
  CONSTRAINT ck_recovery_codes_code_hash CHECK (octet_length(code_hash) = 32)
);
CREATE INDEX ix_recovery_codes_user_active ON app.recovery_codes (user_id) WHERE used_at IS NULL AND superseded_at IS NULL;
CREATE TRIGGER trg_recovery_codes_cols BEFORE UPDATE ON app.recovery_codes
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('used_at', 'superseded_at');

-- -----------------------------------------------------------------------------------------------------
-- RBAC: roles, permissions, role_permissions (reference data, changed only by reviewed migrations).
-- roles.conflicting_role_codes encodes the role-level ("R") separation-of-duties conflicts of
-- operational-controls §2; role_assignments enforces them. Classification: C1.
-- Seed ids are md5-derived (deterministic across environments).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.roles (
  id                     uuid        NOT NULL,
  code                   text        NOT NULL,
  name                   text        NOT NULL,
  description            text        NOT NULL,
  conflicting_role_codes text[]      NOT NULL DEFAULT '{}',
  requires_mfa           boolean     NOT NULL DEFAULT true,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_roles PRIMARY KEY (id),
  CONSTRAINT uq_roles_code UNIQUE (code),
  CONSTRAINT ck_roles_code CHECK (code ~ '^[A-Z][A-Z_]*$'),
  CONSTRAINT ck_roles_not_self_conflicting CHECK (NOT (code = ANY (conflicting_role_codes)))
);
CREATE TRIGGER trg_roles_no_delete BEFORE DELETE ON app.roles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_roles_immutable_cols BEFORE UPDATE ON app.roles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'code', 'created_at');

CREATE TABLE app.permissions (
  id                 uuid        NOT NULL,
  code               text        NOT NULL,
  description        text        NOT NULL,
  is_sensitive       boolean     NOT NULL DEFAULT false,   -- requires justification + audit on use
  requires_step_up   boolean     NOT NULL DEFAULT false,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_permissions PRIMARY KEY (id),
  CONSTRAINT uq_permissions_code UNIQUE (code),
  CONSTRAINT ck_permissions_code CHECK (code ~ '^[a-z_]+(\.[a-z_]+)+$')
);
CREATE TRIGGER trg_permissions_no_delete BEFORE DELETE ON app.permissions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_permissions_immutable_cols BEFORE UPDATE ON app.permissions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'code', 'created_at');

CREATE TABLE app.role_permissions (
  id            uuid        NOT NULL,
  role_id       uuid        NOT NULL,
  permission_id uuid        NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_role_permissions PRIMARY KEY (id),
  CONSTRAINT fk_role_permissions_role_id FOREIGN KEY (role_id) REFERENCES app.roles (id),
  CONSTRAINT fk_role_permissions_permission_id FOREIGN KEY (permission_id) REFERENCES app.permissions (id),
  CONSTRAINT uq_role_permissions_role_permission UNIQUE (role_id, permission_id)
);
CREATE INDEX ix_role_permissions_permission_id ON app.role_permissions (permission_id);
CREATE TRIGGER trg_role_permissions_no_update BEFORE UPDATE ON app.role_permissions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.roles (id, code, name, description, conflicting_role_codes) VALUES
  (md5('role:REVIEWER')::uuid,       'REVIEWER',       'Campaign reviewer',      'Trust & safety campaign review',               '{SECURITY_ADMIN}'),
  (md5('role:KYC_REVIEWER')::uuid,   'KYC_REVIEWER',   'KYC reviewer',           'Identity, KYB and beneficiary verification',   '{SECURITY_ADMIN,SUPER_ADMIN}'),
  (md5('role:SUPPORT')::uuid,        'SUPPORT',        'Support agent',          'Customer support with masked PII',             '{SECURITY_ADMIN}'),
  (md5('role:COMPLIANCE')::uuid,     'COMPLIANCE',     'Compliance officer',     'AML/CFT, cases, freezes, holds',               '{SECURITY_ADMIN,SUPER_ADMIN,BUSINESS_APPROVER}'),
  (md5('role:FINANCE')::uuid,        'FINANCE',        'Finance operator',       'Payout/refund approval, reconciliation, ledger adjustments (dual control)', '{SECURITY_ADMIN,SUPER_ADMIN,BUSINESS_APPROVER}'),
  (md5('role:ADMIN')::uuid,          'ADMIN',          'Platform administrator', 'Configuration requests, content moderation',   '{}'),
  (md5('role:SECURITY_ADMIN')::uuid, 'SECURITY_ADMIN', 'Security administrator', 'Access reviews, key rotation, incident tooling', '{REVIEWER,KYC_REVIEWER,SUPPORT,COMPLIANCE,FINANCE,BUSINESS_APPROVER}'),
  (md5('role:BUSINESS_APPROVER')::uuid, 'BUSINESS_APPROVER', 'Business approver', 'Approval-only: fee changes, write-offs above threshold, review-policy and business-owned limit changes (baseline I-4)', '{FINANCE,COMPLIANCE,SECURITY_ADMIN}'),
  (md5('role:SUPER_ADMIN')::uuid,    'SUPER_ADMIN',    'Role administrator',     'Role grants (maker-checker); no sensitive permissions by default', '{KYC_REVIEWER,COMPLIANCE,FINANCE}');

-- Representative permission set (SECURITY §5, operational-controls §1, baseline §12). The full matrix is Stage 4/14.
-- ADMIN and SUPER_ADMIN do not hold kyc.status.view (baseline I-5, identity-data-protection.md).
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up)
SELECT md5('permission:' || p.code)::uuid, p.code, p.description, p.sensitive, p.step_up
FROM (VALUES
  ('campaign.view',                        'View campaigns and the review queue',                false, false),
  ('campaign.review',                      'Claim and work campaign reviews',                    false, false),
  ('campaign.decide',                      'Approve, reject or request changes',                 true,  false),
  ('campaign.suspend',                     'Suspend a campaign',                                 true,  true),
  ('campaign.unsuspend',                   'Unsuspend a campaign',                               true,  true),
  ('campaign.freeze',                      'Freeze a campaign and its payouts',                  true,  true),
  ('campaign.unfreeze.request',            'Request unfreeze (maker)',                           true,  true),
  ('campaign.unfreeze.approve',            'Approve unfreeze (checker)',                         true,  true),
  ('content.moderate',                     'Moderate public content',                            false, false),
  ('case.create',                          'Open a compliance/risk case',                        false, false),
  ('case.manage',                          'Manage compliance cases',                            true,  false),
  ('kyc.status.view',                      'View KYC level and status',                          false, false),
  ('kyc.case.review',                      'Review KYC/KYB cases',                               true,  false),
  ('kyc.document.view',                    'View KYC documents (justified, case-bound)',         true,  true),
  ('kyc.identity_number.reveal',           'Reveal a full identity number (justified)',          true,  true),
  ('kyc.decision.record',                  'Record a KYC decision',                              true,  true),
  ('beneficiary.verification.decide',      'Decide beneficiary verification',                    true,  true),
  ('org.verification.decide',              'Decide organisation verification',                   true,  true),
  ('payout.hold',                          'Place or release a payout hold',                     true,  true),
  ('payout.approve',                       'Approve a payout (checker)',                         true,  true),
  ('payout.reject',                        'Reject a payout',                                    true,  true),
  ('payout.destination.override.approve',  'Approve a staff payout-destination override',        true,  true),
  ('refund.request',                       'Request a refund (maker)',                           true,  false),
  ('refund.approve',                       'Approve a refund (checker)',                         true,  true),
  ('ledger.adjustment.create',             'Create a ledger adjustment (maker)',                 true,  true),
  ('ledger.adjustment.approve',            'Approve a ledger adjustment (checker)',              true,  true),
  ('reconciliation.run',                   'Run reconciliation',                                 false, false),
  ('reconciliation.resolve',               'Resolve reconciliation discrepancies',               true,  true),
  ('fee.config.request',                   'Request a fee configuration change',                 true,  true),
  ('compliance.override.request',          'Request a compliance override (maker)',              true,  true),
  ('compliance.override.approve',          'Approve a compliance override (checker)',            true,  true),
  ('str.prepare',                          'Prepare suspicious-transaction reports (LR-008)',    true,  true),
  ('evidence.view',                        'View evidence records',                              true,  false),
  ('evidence.hold',                        'Place/release evidence legal holds',                 true,  true),
  ('donor.identity.unmask',                'Unmask an anonymous donor (justified)',              true,  true),
  ('user.view',                            'View user accounts (masked PII)',                    false, false),
  ('account.recovery.assist',              'Assist account recovery (step-up gated)',            true,  true),
  ('audit.read',                           'Read the audit log (scoped)',                        true,  false),
  ('security_audit.read',                  'Read security audit events',                         true,  false),
  ('access.review.run',                    'Run access reviews',                                 false, false),
  ('session.revoke',                       'Revoke staff or user sessions',                      true,  true),
  ('staff.suspend',                        'Suspend a staff account',                            true,  true),
  ('killswitch.activate',                  'Activate a kill switch (security scopes)',           true,  true),
  ('secret.rotation.trigger',              'Trigger secret/key rotation',                        true,  true),
  ('config.change.request',                'Request a configuration change',                     true,  false),
  ('role.assign.request',                  'Request a role grant or revocation (maker)',         true,  true),
  ('role.assign.approve',                  'Approve a role grant or revocation (checker)',       true,  true),
  ('fee.config.approve',                   'Approve a fee schedule change (checker)',            true,  true),
  ('recovery.write_off.approve',           'Approve a write-off above threshold (checker)',      true,  true),
  ('refund.bulk.request',                  'Request a bulk refund batch (maker, I-24)',          true,  true),
  ('refund.bulk.approve',                  'Approve a bulk refund batch above the limit (I-24)', true,  true),
  ('campaign.review_policy.request',       'Propose a campaign review policy version (maker)',   true,  true),
  ('campaign.review_policy.approve',       'Approve a campaign review policy version (checker)', true,  true),
  ('limit.change.request',                 'Request a limit/threshold change (maker)',           true,  true),
  ('limit.change.approve',                 'Approve a business-owned limit change (checker)',    true,  true)
) AS p(code, description, sensitive, step_up);

INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm_code)::uuid,
       md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm_code)::uuid
FROM (VALUES
  ('REVIEWER', 'campaign.view'), ('REVIEWER', 'campaign.review'), ('REVIEWER', 'campaign.decide'),
  ('REVIEWER', 'campaign.suspend'), ('REVIEWER', 'campaign.unsuspend'), ('REVIEWER', 'case.create'),
  ('REVIEWER', 'kyc.status.view'),
  ('KYC_REVIEWER', 'kyc.status.view'), ('KYC_REVIEWER', 'kyc.case.review'), ('KYC_REVIEWER', 'kyc.document.view'),
  ('KYC_REVIEWER', 'kyc.decision.record'), ('KYC_REVIEWER', 'beneficiary.verification.decide'),
  ('KYC_REVIEWER', 'org.verification.decide'),
  ('SUPPORT', 'campaign.view'), ('SUPPORT', 'user.view'), ('SUPPORT', 'refund.request'), ('SUPPORT', 'case.create'),
  ('SUPPORT', 'account.recovery.assist'), ('SUPPORT', 'kyc.status.view'),
  ('COMPLIANCE', 'campaign.view'), ('COMPLIANCE', 'campaign.decide'), ('COMPLIANCE', 'campaign.suspend'),
  ('COMPLIANCE', 'campaign.unsuspend'), ('COMPLIANCE', 'campaign.freeze'), ('COMPLIANCE', 'campaign.unfreeze.request'),
  ('COMPLIANCE', 'campaign.unfreeze.approve'), ('COMPLIANCE', 'case.create'), ('COMPLIANCE', 'case.manage'),
  ('COMPLIANCE', 'payout.hold'), ('COMPLIANCE', 'kyc.status.view'), ('COMPLIANCE', 'kyc.document.view'),
  ('COMPLIANCE', 'kyc.identity_number.reveal'), ('COMPLIANCE', 'kyc.decision.record'),
  ('COMPLIANCE', 'compliance.override.request'), ('COMPLIANCE', 'compliance.override.approve'),
  ('COMPLIANCE', 'str.prepare'), ('COMPLIANCE', 'evidence.view'), ('COMPLIANCE', 'evidence.hold'),
  ('COMPLIANCE', 'refund.request'), ('COMPLIANCE', 'refund.bulk.request'), ('COMPLIANCE', 'audit.read'), ('COMPLIANCE', 'donor.identity.unmask'),
  ('FINANCE', 'campaign.view'), ('FINANCE', 'campaign.freeze'), ('FINANCE', 'kyc.status.view'),
  ('FINANCE', 'payout.approve'), ('FINANCE', 'payout.reject'), ('FINANCE', 'payout.destination.override.approve'),
  ('FINANCE', 'refund.approve'), ('FINANCE', 'ledger.adjustment.create'), ('FINANCE', 'ledger.adjustment.approve'),
  ('FINANCE', 'reconciliation.run'), ('FINANCE', 'reconciliation.resolve'), ('FINANCE', 'fee.config.request'),
  ('FINANCE', 'audit.read'),
  ('ADMIN', 'campaign.view'), ('ADMIN', 'content.moderate'), ('ADMIN', 'config.change.request'),
  ('SECURITY_ADMIN', 'access.review.run'), ('SECURITY_ADMIN', 'session.revoke'), ('SECURITY_ADMIN', 'staff.suspend'),
  ('SECURITY_ADMIN', 'killswitch.activate'), ('SECURITY_ADMIN', 'secret.rotation.trigger'),
  ('SECURITY_ADMIN', 'security_audit.read'),
  ('SUPER_ADMIN', 'role.assign.request'), ('SUPER_ADMIN', 'role.assign.approve'), ('SUPER_ADMIN', 'audit.read'),
  ('SUPER_ADMIN', 'security_audit.read'), ('SUPER_ADMIN', 'campaign.view'),
  ('BUSINESS_APPROVER', 'fee.config.approve'), ('BUSINESS_APPROVER', 'recovery.write_off.approve'), ('BUSINESS_APPROVER', 'refund.bulk.approve'),
  ('BUSINESS_APPROVER', 'campaign.review_policy.approve'), ('BUSINESS_APPROVER', 'limit.change.approve'),
  ('COMPLIANCE', 'campaign.review_policy.request'), ('COMPLIANCE', 'limit.change.request'), ('FINANCE', 'limit.change.request')
) AS rp(role_code, perm_code);
-- Note: campaign.unfreeze.approve is held by COMPLIANCE, but the requester can never approve their own
-- request (campaign_status_history CHECK in 0014 and the service SoD check).

-- -----------------------------------------------------------------------------------------------------
-- app.role_assignment_requests — maker-checker for role grants and revocations (SECURITY §5.2,
-- operational-controls §3: 24 h expiry). Classification: C1. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.role_assignment_requests (
  id                 uuid        NOT NULL,
  action             text        NOT NULL,
  target_user_id     uuid        NOT NULL,
  target_account_kind text       NOT NULL DEFAULT 'STAFF',
  role_id            uuid        NOT NULL,
  requested_by       uuid        NOT NULL,
  justification      text        NOT NULL,
  status             text        NOT NULL DEFAULT 'PENDING',
  decided_by         uuid,
  decided_at         timestamptz,
  decision_reason    text,
  checker_step_up_at timestamptz,                          -- approver's fresh MFA (step-up) time
  expires_at         timestamptz NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_role_assignment_requests PRIMARY KEY (id),
  CONSTRAINT uq_role_assignment_requests_approval UNIQUE (id, target_user_id, role_id, requested_by, decided_by),
  CONSTRAINT fk_role_assignment_requests_target FOREIGN KEY (target_user_id, target_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_role_assignment_requests_role_id FOREIGN KEY (role_id) REFERENCES app.roles (id),
  CONSTRAINT fk_role_assignment_requests_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id),
  CONSTRAINT fk_role_assignment_requests_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id),
  CONSTRAINT ck_role_assignment_requests_target_kind CHECK (target_account_kind = 'STAFF'),
  CONSTRAINT ck_role_assignment_requests_action CHECK (action IN ('GRANT', 'REVOKE')),
  CONSTRAINT ck_role_assignment_requests_status CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'EXPIRED', 'WITHDRAWN')),
  CONSTRAINT ck_role_assignment_requests_no_self_request CHECK (requested_by <> target_user_id),
  CONSTRAINT ck_role_assignment_requests_no_self_approval CHECK (decided_by IS NULL OR (decided_by <> requested_by AND decided_by <> target_user_id)),
  CONSTRAINT ck_role_assignment_requests_decided CHECK (
    (status IN ('APPROVED', 'REJECTED')) = (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CONSTRAINT ck_role_assignment_requests_approved CHECK (
    status <> 'APPROVED' OR (checker_step_up_at IS NOT NULL AND decided_at <= expires_at)),
  CONSTRAINT ck_role_assignment_requests_justification CHECK (length(justification) >= 10),
  CONSTRAINT ck_role_assignment_requests_expiry CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX uq_role_assignment_requests_pending ON app.role_assignment_requests (target_user_id, role_id, action)
  WHERE status = 'PENDING';
CREATE TRIGGER trg_role_assignment_requests_guard_status BEFORE INSERT OR UPDATE OF status ON app.role_assignment_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('role_assignment_request');
CREATE TRIGGER trg_role_assignment_requests_set_updated_at BEFORE UPDATE ON app.role_assignment_requests
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_role_assignment_requests_cols BEFORE UPDATE ON app.role_assignment_requests
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'decided_by', 'decided_at', 'decision_reason', 'checker_step_up_at');
CREATE TRIGGER trg_role_assignment_requests_no_delete BEFORE DELETE ON app.role_assignment_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('role_assignment_request', '',        'PENDING'),
  ('role_assignment_request', 'PENDING', 'APPROVED'),
  ('role_assignment_request', 'PENDING', 'REJECTED'),
  ('role_assignment_request', 'PENDING', 'EXPIRED'),
  ('role_assignment_request', 'PENDING', 'WITHDRAWN');

-- -----------------------------------------------------------------------------------------------------
-- app.role_assignments — active staff roles. Created only from an APPROVED GRANT request whose maker and
-- checker are copied here (composite FK); revocation sets revoked_* (REVOKE request). Staff accounts only.
-- Role-level SoD conflicts (roles.conflicting_role_codes) are rejected. Classification: C1. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.role_assignments (
  id                   uuid        NOT NULL,
  user_id              uuid        NOT NULL,
  user_account_kind    text        NOT NULL DEFAULT 'STAFF',
  role_id              uuid        NOT NULL,
  request_id           uuid        NOT NULL,
  granted_by           uuid        NOT NULL,                -- maker
  approved_by          uuid        NOT NULL,                -- checker
  valid_from           timestamptz NOT NULL DEFAULT now(),
  expires_at           timestamptz,                         -- optional time-boxed grant
  revoked_at           timestamptz,
  revoked_by           uuid,
  revoke_request_id    uuid,
  revoke_reason        text,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_role_assignments PRIMARY KEY (id),
  CONSTRAINT uq_role_assignments_request_id UNIQUE (request_id),
  CONSTRAINT fk_role_assignments_user FOREIGN KEY (user_id, user_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_role_assignments_role_id FOREIGN KEY (role_id) REFERENCES app.roles (id),
  CONSTRAINT fk_role_assignments_request FOREIGN KEY (request_id, user_id, role_id, granted_by, approved_by)
    REFERENCES app.role_assignment_requests (id, target_user_id, role_id, requested_by, decided_by),
  CONSTRAINT fk_role_assignments_revoked_by FOREIGN KEY (revoked_by) REFERENCES app.users (id),
  CONSTRAINT fk_role_assignments_revoke_request_id FOREIGN KEY (revoke_request_id) REFERENCES app.role_assignment_requests (id),
  CONSTRAINT ck_role_assignments_user_kind CHECK (user_account_kind = 'STAFF'),
  CONSTRAINT ck_role_assignments_no_self_grant CHECK (granted_by <> user_id),
  CONSTRAINT ck_role_assignments_no_self_approval CHECK (approved_by <> user_id AND approved_by <> granted_by),
  CONSTRAINT ck_role_assignments_revoked CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)),
  CONSTRAINT ck_role_assignments_expiry CHECK (expires_at IS NULL OR expires_at > valid_from)
);
CREATE UNIQUE INDEX uq_role_assignments_active ON app.role_assignments (user_id, role_id) WHERE revoked_at IS NULL;
CREATE INDEX ix_role_assignments_role_id ON app.role_assignments (role_id) WHERE revoked_at IS NULL;
CREATE TRIGGER trg_role_assignments_set_updated_at BEFORE UPDATE ON app.role_assignments
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_role_assignments_cols BEFORE UPDATE ON app.role_assignments
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('revoked_at', 'revoked_by', 'revoke_request_id', 'revoke_reason');
CREATE TRIGGER trg_role_assignments_no_delete BEFORE DELETE ON app.role_assignments
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION app.role_assignments_check_grant() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  r app.role_assignment_requests%ROWTYPE;
  v_code text;
  v_conflict text;
BEGIN
  SELECT * INTO r FROM app.role_assignment_requests WHERE id = NEW.request_id;
  IF r.status <> 'APPROVED' OR r.action <> 'GRANT' THEN
    RAISE EXCEPTION 'role assignment requires an APPROVED GRANT request (got % %)', r.action, r.status
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  SELECT code INTO v_code FROM app.roles WHERE id = NEW.role_id;
  SELECT ro.code INTO v_conflict
    FROM app.role_assignments ra JOIN app.roles ro ON ro.id = ra.role_id
   WHERE ra.user_id = NEW.user_id AND ra.revoked_at IS NULL
     AND (ro.code = ANY ((SELECT conflicting_role_codes FROM app.roles WHERE id = NEW.role_id)::text[])
          OR v_code = ANY (ro.conflicting_role_codes))
   LIMIT 1;
  IF v_conflict IS NOT NULL THEN
    RAISE EXCEPTION 'separation of duties: role % conflicts with active role %', v_code, v_conflict
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_role_assignments_check_grant BEFORE INSERT ON app.role_assignments
  FOR EACH ROW EXECUTE FUNCTION app.role_assignments_check_grant();

-- -----------------------------------------------------------------------------------------------------
-- app.security_events — authentication telemetry (high volume, append-only, shorter retention). Not the
-- audit log: security-relevant *decisions* go to audit.security_audit_events. Retention by dropping
-- monthly partitions (Stage 17/18), never by row DELETE. Classification: C2 (IP, user agent).
-- Retention: OPERATIONAL (period pending LR-012).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.security_events (
  id               uuid        NOT NULL,
  user_id          uuid,                                   -- NULL when the account is unknown
  session_id       uuid,                                   -- no FK: sessions are purged
  event_type       text        NOT NULL,
  destination_hmac bytea,                                  -- for OTP/rate-limit events on unknown accounts
  ip               inet,
  user_agent       text,
  details          jsonb       NOT NULL DEFAULT '{}',      -- reason codes, counters; never secrets
  occurred_at      timestamptz NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_security_events PRIMARY KEY (id),
  CONSTRAINT fk_security_events_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_security_events_event_type CHECK (event_type IN (
    'LOGIN_SUCCEEDED', 'LOGIN_FAILED', 'OTP_SENT', 'OTP_VERIFY_FAILED', 'OTP_MAX_ATTEMPTS', 'OTP_RESEND_LIMITED',
    'SESSION_CREATED', 'SESSION_ROTATED', 'SESSION_REVOKED', 'SESSION_EXPIRED', 'STEP_UP_SUCCEEDED', 'STEP_UP_FAILED',
    'MFA_ENROLLED', 'MFA_REMOVED', 'MFA_FAILED', 'RECOVERY_CODE_USED', 'PASSWORD_CHANGED', 'PASSWORD_FAILED',
    'RATE_LIMITED', 'SUSPICIOUS_LOGIN', 'CONTACT_CHANGED')),
  CONSTRAINT ck_security_events_details_object CHECK (jsonb_typeof(details) = 'object'),
  CONSTRAINT ck_security_events_user_agent CHECK (user_agent IS NULL OR length(user_agent) <= 512)
);
CREATE INDEX ix_security_events_user_occurred ON app.security_events (user_id, occurred_at) WHERE user_id IS NOT NULL;
CREATE INDEX ix_security_events_type_occurred ON app.security_events (event_type, occurred_at);
CREATE TRIGGER trg_security_events_no_mutation BEFORE UPDATE OR DELETE ON app.security_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_security_events_no_truncate BEFORE TRUNCATE ON app.security_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.break_glass_grants — time-boxed emergency grant of ONE named permission to a staff account
-- (SECURITY §5.5, operational-controls §5). Approver distinct from requester and grantee. The 1-hour bound
-- is the default design bound (SECURITY §5.5 "default <= 1 hour"); a different bound is a reviewed
-- migration, not a runtime setting. Never grants role administration or ledger write permissions.
-- Append-only except revocation (set once). Every use is tagged break_glass in audit events.
-- Classification: C1. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.break_glass_grants (
  id                 uuid        NOT NULL,
  staff_user_id      uuid        NOT NULL,
  staff_account_kind text        NOT NULL DEFAULT 'STAFF',
  permission_id      uuid        NOT NULL,
  justification      text        NOT NULL,
  incident_ref       text        NOT NULL,                -- incident / ticket reference
  requested_by       uuid        NOT NULL,                -- may be the grantee (on-call self-invocation)
  approved_by        uuid        NOT NULL,
  starts_at          timestamptz NOT NULL,
  expires_at         timestamptz NOT NULL,
  revoked_at         timestamptz,
  revoked_by         uuid,
  revoke_reason      text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_break_glass_grants PRIMARY KEY (id),
  CONSTRAINT fk_break_glass_grants_staff FOREIGN KEY (staff_user_id, staff_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_break_glass_grants_permission_id FOREIGN KEY (permission_id) REFERENCES app.permissions (id),
  CONSTRAINT fk_break_glass_grants_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id),
  CONSTRAINT fk_break_glass_grants_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT fk_break_glass_grants_revoked_by FOREIGN KEY (revoked_by) REFERENCES app.users (id),
  CONSTRAINT ck_break_glass_grants_staff_kind CHECK (staff_account_kind = 'STAFF'),
  CONSTRAINT ck_break_glass_grants_maker_checker CHECK (approved_by <> requested_by AND approved_by <> staff_user_id),
  CONSTRAINT ck_break_glass_grants_window CHECK (expires_at > starts_at AND expires_at - starts_at <= interval '1 hour'),
  CONSTRAINT ck_break_glass_grants_justification CHECK (length(justification) >= 10 AND length(incident_ref) >= 3),
  CONSTRAINT ck_break_glass_grants_revoked CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)
                                                 AND (revoked_at IS NULL) = (revoke_reason IS NULL))
);
CREATE INDEX ix_break_glass_grants_active ON app.break_glass_grants (staff_user_id, expires_at) WHERE revoked_at IS NULL;
CREATE TRIGGER trg_break_glass_grants_cols BEFORE UPDATE ON app.break_glass_grants
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('revoked_at', 'revoked_by', 'revoke_reason');
CREATE TRIGGER trg_break_glass_grants_set_once BEFORE UPDATE ON app.break_glass_grants
  FOR EACH ROW EXECUTE FUNCTION app.set_once_columns('revoked_at', 'revoked_by', 'revoke_reason');
CREATE TRIGGER trg_break_glass_grants_no_delete BEFORE DELETE ON app.break_glass_grants
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_break_glass_grants_no_truncate BEFORE TRUNCATE ON app.break_glass_grants
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION app.break_glass_grants_check_permission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_code text;
BEGIN
  SELECT code INTO v_code FROM app.permissions WHERE id = NEW.permission_id;
  IF v_code LIKE 'role.%' OR v_code LIKE 'ledger.adjustment.%' THEN
    RAISE EXCEPTION 'break-glass never grants %', v_code USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_break_glass_grants_check_permission BEFORE INSERT ON app.break_glass_grants
  FOR EACH ROW EXECUTE FUNCTION app.break_glass_grants_check_permission();

-- -----------------------------------------------------------------------------------------------------
-- app.staff_conflict_declarations — staff-declared relationships to subjects (operational-controls §2):
-- object-level SoD checks refuse actions on declared subjects. Append-only; withdrawal is set once.
-- Classification: C2. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.staff_conflict_declarations (
  id                 uuid        NOT NULL,
  staff_user_id      uuid        NOT NULL,
  staff_account_kind text        NOT NULL DEFAULT 'STAFF',
  subject_type       text        NOT NULL,
  subject_id         uuid        NOT NULL,
  relationship       text        NOT NULL,
  details            text,
  declared_at        timestamptz NOT NULL,
  withdrawn_at       timestamptz,
  withdrawn_reason   text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_staff_conflict_declarations PRIMARY KEY (id),
  CONSTRAINT fk_staff_conflict_declarations_staff FOREIGN KEY (staff_user_id, staff_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT ck_staff_conflict_declarations_staff_kind CHECK (staff_account_kind = 'STAFF'),
  CONSTRAINT ck_staff_conflict_declarations_subject_type CHECK (subject_type IN ('user', 'organisation', 'campaign', 'beneficiary',
                                                                                 'institution_payee', 'payout_destination')),
  CONSTRAINT ck_staff_conflict_declarations_relationship CHECK (relationship IN ('FAMILY', 'FRIEND', 'BUSINESS', 'EMPLOYER',
                                                                                 'DONOR', 'BENEFICIARY', 'OTHER')),
  CONSTRAINT ck_staff_conflict_declarations_withdrawn CHECK ((withdrawn_at IS NULL) = (withdrawn_reason IS NULL))
);
CREATE UNIQUE INDEX uq_staff_conflict_declarations_active ON app.staff_conflict_declarations (staff_user_id, subject_type, subject_id)
  WHERE withdrawn_at IS NULL;
CREATE INDEX ix_staff_conflict_declarations_subject ON app.staff_conflict_declarations (subject_type, subject_id) WHERE withdrawn_at IS NULL;
CREATE TRIGGER trg_staff_conflict_declarations_cols BEFORE UPDATE ON app.staff_conflict_declarations
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('withdrawn_at', 'withdrawn_reason');
CREATE TRIGGER trg_staff_conflict_declarations_set_once BEFORE UPDATE ON app.staff_conflict_declarations
  FOR EACH ROW EXECUTE FUNCTION app.set_once_columns('withdrawn_at', 'withdrawn_reason');
CREATE TRIGGER trg_staff_conflict_declarations_no_delete BEFORE DELETE ON app.staff_conflict_declarations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
