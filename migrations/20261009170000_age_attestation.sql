-- FundZim migration: age attestation and the BASIC_VERIFIED rules (Stage 6, ADR-037 §1–§2; stream R).
--   * kyc.age_attestations — append-only self-declarations "I am at least adult_age years old" (ATTESTED) or
--     the refusal to declare it (DECLINED). A change is a new row. A self-attestation is never documentary proof
--     of age (assurance SELF_ATTESTED); "verified age" comes only from an approved KYC identity's date of birth.
--   * Verification policy v2 = v1 plus a `basic` section (the BASIC_VERIFIED conditions and the current age
--     statement version). It is approved by this migration under ADR-037 through the same guarded transitions
--     as every version (v1 APPROVED -> RETIRED, v2 PROPOSED -> APPROVED), the ADR being the checker — the
--     ADR-034 exemption pattern used for v1, extended to exactly this row. v1 is not edited.
--   adult_age stays INTERNAL_POLICY configuration (seeded 18), not a legal conclusion: LR-021 and LR-015 are OPEN
--   (LEGAL_REVIEW_REQUIRED).
-- Owner module: kyc. C2 (the outcome of a declaration; no date of birth is stored here). Only fundzim_kyc has
-- privileges (runtime grants v3/v4 derivation: SELECT, INSERT — no UPDATE because of the forbid_mutation trigger).
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE kyc.age_attestations (
  id                uuid        NOT NULL,
  user_id           uuid        NOT NULL REFERENCES app.users (id),
  outcome           text        NOT NULL,
  statement_version text        NOT NULL,
  adult_age         integer     NOT NULL,
  policy_version    text        NOT NULL,
  source            text        NOT NULL,
  assurance         text        NOT NULL DEFAULT 'SELF_ATTESTED',
  ip                inet,
  source_event_id   uuid,                  -- outbox event that carried a registration attestation (dedupe)
  attested_at       timestamptz NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_age_attestations PRIMARY KEY (id),
  CONSTRAINT uq_age_attestations_source_event UNIQUE (source_event_id),
  CONSTRAINT ck_age_attestations_outcome CHECK (outcome IN ('ATTESTED', 'DECLINED')),
  CONSTRAINT ck_age_attestations_statement CHECK (statement_version ~ '^[a-z0-9][a-z0-9.-]{0,63}$'),
  CONSTRAINT ck_age_attestations_adult_age CHECK (adult_age BETWEEN 1 AND 30),
  CONSTRAINT ck_age_attestations_policy CHECK (policy_version ~ '^v[0-9]+(\.[0-9]+)?$'),
  CONSTRAINT ck_age_attestations_source CHECK (source IN ('REGISTRATION', 'DASHBOARD', 'CAMPAIGN_FLOW')),
  CONSTRAINT ck_age_attestations_assurance CHECK (assurance = 'SELF_ATTESTED'),
  CONSTRAINT ck_age_attestations_event_source CHECK (source_event_id IS NULL OR source = 'REGISTRATION')
);
COMMENT ON TABLE kyc.age_attestations IS 'C2. Append-only age self-attestations (ADR-037 §1); never documentary proof of age.';
CREATE INDEX ix_age_attestations_user ON kyc.age_attestations (user_id, attested_at DESC, id DESC);
CREATE TRIGGER trg_age_attestations_no_mutation BEFORE UPDATE OR DELETE ON kyc.age_attestations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_age_attestations_no_truncate BEFORE TRUNCATE ON kyc.age_attestations
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- the checker exemption covers v1 (ADR-034) and now exactly v2 (ADR-037); every other version needs a human checker
ALTER TABLE kyc.verification_policies DROP CONSTRAINT ck_verification_policies_checker;
ALTER TABLE kyc.verification_policies ADD CONSTRAINT ck_verification_policies_checker CHECK (status = 'PROPOSED' OR approved_by IS NOT NULL
  OR (made_by = '00000000-0000-0000-0000-000000000001' AND decision_ref = 'ADR-034' AND policy_version = 'v1')
  OR (made_by = '00000000-0000-0000-0000-000000000001' AND decision_ref = 'ADR-037' AND policy_version = 'v2'));

-- v2 = the v1 rules plus the BASIC_VERIFIED section (internal/kyc/policy.go validates the same schema)
INSERT INTO kyc.verification_policies (id, policy_version, rules, status, made_by, approved_at, change_reason, decision_ref, effective_from)
SELECT '0192f000-0000-7000-8000-000000000002', 'v2',
       p.rules || '{"basic": {"requires": ["EMAIL_VERIFIED", "PHONE_VERIFIED", "AGE_ATTESTED", "ACCOUNT_ACTIVE"],
                              "age_statement_version": "age-statement-v1"}}'::jsonb,
       'PROPOSED', '00000000-0000-0000-0000-000000000001', NULL,
       'Stage 6 BASIC_VERIFIED rules and age attestation statement (internal policy, not a legal requirement)', 'ADR-037', now()
  FROM kyc.verification_policies p WHERE p.policy_version = 'v1';
UPDATE kyc.verification_policies SET status = 'RETIRED', retired_at = now() WHERE status = 'APPROVED';
UPDATE kyc.verification_policies SET status = 'APPROVED', approved_at = now() WHERE policy_version = 'v2';

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Development rollback only: policies are immutable and undeletable, so the migrator lifts the guards to remove
-- v2 (and any later version built on it) and re-approve v1.
ALTER TABLE kyc.verification_policies DISABLE TRIGGER USER;
DELETE FROM kyc.verification_policies WHERE policy_version <> 'v1';
UPDATE kyc.verification_policies SET status = 'APPROVED', retired_at = NULL WHERE policy_version = 'v1';
ALTER TABLE kyc.verification_policies ENABLE TRIGGER USER;
ALTER TABLE kyc.verification_policies DROP CONSTRAINT ck_verification_policies_checker;
ALTER TABLE kyc.verification_policies ADD CONSTRAINT ck_verification_policies_checker CHECK (status = 'PROPOSED' OR approved_by IS NOT NULL
  OR (made_by = '00000000-0000-0000-0000-000000000001' AND decision_ref = 'ADR-034' AND policy_version = 'v1'));
DROP TABLE IF EXISTS kyc.age_attestations;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
