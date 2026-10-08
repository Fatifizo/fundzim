-- FundZim migration: credentials, MFA, recovery codes, auth tokens, OTP and MFA challenges (identity, Stage 4)
-- Derived from the Stage 2 design draft design/sql/0004_users_auth.sql (validated in design/sql/validate), adapted for Stage 4
-- (ADR-032). Runs as fundzim_migrator via `fundzimctl migrate up`. Forward-only in production (ADR-028); the
-- Down section exists for local development only.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
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

-- app.auth_tokens — single-use, emailed tokens (ADR-032): email verification, password reset, staff
-- invitation and email change. Only SHA-256(token) is stored (the raw token exists only in the email). At
-- most one live token per (user, purpose): issuing a new one invalidates the previous one in the same
-- transaction. Consumption is `UPDATE ... SET consumed_at = now() WHERE token_hash = $1 AND consumed_at IS NULL
-- AND invalidated_at IS NULL AND expires_at > now() RETURNING`, so a token can be used exactly once even under
-- concurrency. Classification: C3-handled (hashes). Retention: OPERATIONAL.
CREATE TABLE app.auth_tokens (
  id             uuid        NOT NULL,
  user_id        uuid        NOT NULL,
  purpose        text        NOT NULL,
  user_email_id  uuid,                                   -- the address the token was sent to
  token_hash     bytea       NOT NULL,
  requested_ip   inet,
  created_at     timestamptz NOT NULL DEFAULT now(),
  expires_at     timestamptz NOT NULL,
  consumed_at    timestamptz,
  invalidated_at timestamptz,
  CONSTRAINT pk_auth_tokens PRIMARY KEY (id),
  CONSTRAINT uq_auth_tokens_token_hash UNIQUE (token_hash),
  CONSTRAINT fk_auth_tokens_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT fk_auth_tokens_user_email_id FOREIGN KEY (user_email_id) REFERENCES app.user_emails (id),
  CONSTRAINT ck_auth_tokens_purpose CHECK (purpose IN ('EMAIL_VERIFICATION', 'PASSWORD_RESET', 'STAFF_INVITATION', 'EMAIL_CHANGE')),
  CONSTRAINT ck_auth_tokens_token_hash CHECK (octet_length(token_hash) = 32),
  CONSTRAINT ck_auth_tokens_expiry CHECK (expires_at > created_at AND expires_at <= created_at + interval '7 days'),
  CONSTRAINT ck_auth_tokens_final CHECK (consumed_at IS NULL OR invalidated_at IS NULL)
);
CREATE UNIQUE INDEX uq_auth_tokens_live ON app.auth_tokens (user_id, purpose) WHERE consumed_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX ix_auth_tokens_expires_at ON app.auth_tokens (expires_at);
CREATE FUNCTION app.auth_tokens_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(OLD) - ARRAY['consumed_at', 'invalidated_at']) IS DISTINCT FROM (to_jsonb(NEW) - ARRAY['consumed_at', 'invalidated_at']) THEN
    RAISE EXCEPTION 'auth token is immutable except consumed_at/invalidated_at' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.consumed_at IS NOT NULL OR OLD.invalidated_at IS NOT NULL THEN
    RAISE EXCEPTION 'auth token % is already consumed or invalidated', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_auth_tokens_guard BEFORE UPDATE ON app.auth_tokens
  FOR EACH ROW EXECUTE FUNCTION app.auth_tokens_guard();

-- app.mfa_login_challenges — the state between a correct password and a completed second factor. No
-- session exists in this state (ADR-032). The challenge token travels in an HttpOnly cookie; only its
-- SHA-256 is stored. Attempts are bounded (CHECK) and the challenge is single-use. Classification: C3-handled.
CREATE TABLE app.mfa_login_challenges (
  id           uuid        NOT NULL,
  user_id      uuid        NOT NULL,
  token_hash   bytea       NOT NULL,
  attempts     smallint    NOT NULL DEFAULT 0,
  requested_ip inet,
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  consumed_at  timestamptz,
  invalidated_at timestamptz,
  CONSTRAINT pk_mfa_login_challenges PRIMARY KEY (id),
  CONSTRAINT uq_mfa_login_challenges_token_hash UNIQUE (token_hash),
  CONSTRAINT fk_mfa_login_challenges_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_mfa_login_challenges_token_hash CHECK (octet_length(token_hash) = 32),
  CONSTRAINT ck_mfa_login_challenges_attempts CHECK (attempts BETWEEN 0 AND 5),
  CONSTRAINT ck_mfa_login_challenges_ttl CHECK (expires_at > created_at AND expires_at <= created_at + interval '15 minutes'),
  CONSTRAINT ck_mfa_login_challenges_final CHECK (consumed_at IS NULL OR invalidated_at IS NULL)
);
CREATE INDEX ix_mfa_login_challenges_user_id ON app.mfa_login_challenges (user_id, created_at);
CREATE FUNCTION app.mfa_login_challenges_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF (to_jsonb(OLD) - ARRAY['attempts', 'consumed_at', 'invalidated_at']) IS DISTINCT FROM (to_jsonb(NEW) - ARRAY['attempts', 'consumed_at', 'invalidated_at']) THEN
    RAISE EXCEPTION 'mfa challenge is immutable except attempts/consumed_at/invalidated_at' USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.consumed_at IS NOT NULL OR OLD.invalidated_at IS NOT NULL THEN
    RAISE EXCEPTION 'mfa challenge % is already consumed or invalidated', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.attempts < OLD.attempts THEN
    RAISE EXCEPTION 'mfa challenge attempts cannot decrease' USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_mfa_login_challenges_guard BEFORE UPDATE ON app.mfa_login_challenges
  FOR EACH ROW EXECUTE FUNCTION app.mfa_login_challenges_guard();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.mfa_login_challenges, app.auth_tokens, app.recovery_codes, app.otp_challenges,
  app.password_credentials, app.authentication_identities, app.mfa_methods;
DROP FUNCTION IF EXISTS app.mfa_login_challenges_guard(), app.auth_tokens_guard(), app.otp_challenges_guard();
-- +goose StatementEnd
