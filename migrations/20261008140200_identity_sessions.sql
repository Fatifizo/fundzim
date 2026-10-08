-- FundZim migration: sessions and session events (identity, Stage 4)
-- Derived from the Stage 2 design draft design/sql/0004_users_auth.sql (validated in design/sql/validate), adapted for Stage 4
-- (ADR-032). Runs as fundzim_migrator via `fundzimctl migrate up`. Forward-only in production (ADR-028); the
-- Down section exists for local development only.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
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
    'PRIVILEGE_CHANGE', 'ACCOUNT_SUSPENDED', 'ACCOUNT_CLOSED', 'PASSWORD_RESET', 'PASSWORD_CHANGED', 'LOGOUT_ALL', 'MFA_CHANGED')),
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

-- app.session_events — append-only lifecycle history (created, MFA completed, rotated, step-up, revoked).
-- Classification: C2 (IP). Retention: OPERATIONAL (period pending LR-012).
CREATE TABLE app.session_events (
  id          uuid        NOT NULL,
  session_id  uuid        NOT NULL,
  user_id     uuid        NOT NULL,
  event_type  text        NOT NULL,
  reason      text,
  ip          inet,
  occurred_at timestamptz NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_session_events PRIMARY KEY (id),
  CONSTRAINT fk_session_events_session_id FOREIGN KEY (session_id) REFERENCES app.sessions (id),
  CONSTRAINT fk_session_events_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_session_events_type CHECK (event_type IN ('CREATED', 'ROTATED', 'STEP_UP', 'REVOKED'))
);
CREATE INDEX ix_session_events_session_id ON app.session_events (session_id, occurred_at);
CREATE TRIGGER trg_session_events_no_mutation BEFORE UPDATE OR DELETE ON app.session_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_session_events_no_truncate BEFORE TRUNCATE ON app.session_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.session_events, app.sessions;
DROP FUNCTION IF EXISTS app.sessions_no_unrevoke();
-- +goose StatementEnd
