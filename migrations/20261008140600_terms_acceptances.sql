-- FundZim migration: versioned acceptance of legal documents (Stage 4; LR-022 "acceptance versioning").
-- app.user_terms_acceptances records which version of which document a user accepted, when, and from which
-- client address. Append-only: a new version is a new row (re-consent), history is never edited. Owner module:
-- users. Classification: C2 (IP). Retention: account lifetime + legal retention (LR-012).
-- The document texts themselves are placeholders until counsel drafts them (LEGAL_REVIEW_REQUIRED, LR-022).
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
CREATE TABLE app.user_terms_acceptances (
  id           uuid        NOT NULL,
  user_id      uuid        NOT NULL,
  document     text        NOT NULL,
  version      text        NOT NULL,
  accepted_at  timestamptz NOT NULL,
  ip           inet,
  created_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_user_terms_acceptances PRIMARY KEY (id),
  CONSTRAINT fk_user_terms_acceptances_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT uq_user_terms_acceptances_version UNIQUE (user_id, document, version),
  CONSTRAINT ck_user_terms_acceptances_document CHECK (document IN ('TERMS_OF_USE', 'PRIVACY_NOTICE')),
  CONSTRAINT ck_user_terms_acceptances_version CHECK (version ~ '^[A-Za-z0-9._-]{1,64}$')
);
CREATE TRIGGER trg_user_terms_acceptances_no_mutation BEFORE UPDATE OR DELETE ON app.user_terms_acceptances
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_user_terms_acceptances_no_truncate BEFORE TRUNCATE ON app.user_terms_acceptances
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.user_terms_acceptances;
-- +goose StatementEnd
