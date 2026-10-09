-- FundZim migration: compliance restriction enforcement (Stage 6, ADR-037 §3; stream R).
--   * compliance.subject_restrictions — projection of APPROVED restricting resolutions onto every PRIMARY_SUBJECT
--     party of the case (RESTRICT -> RESTRICTED, SUSPEND -> SUSPENDED, OFFBOARD / CONFIRMED_FRAUD -> OFFBOARDED),
--     maintained by the compliance module in the same transaction as the approval. A restriction is lifted only
--     when the same case is reopened and resolved again with CLEARED or EDD_CONDITIONS (approved); a different
--     approved restricting decision supersedes it. Rows are never deleted: lifting sets lifted_at once.
--     The table carries no reason, note or confidentiality: restrictions from RESTRICTED_STR cases apply like any
--     other, and nothing read from here can tip a subject off (other modules read levels only, through
--     compliance.Service.Restrictions).
--   * Backfill from existing approved resolutions (no outbox events are emitted for backfilled rows).
--   * Campaign review escalations (campaigns.review_escalated) open cases: case type CAMPAIGN_REVIEW, source
--     CAMPAIGN_ESCALATION and the related object CAMPAIGN_REVIEW are added to the case vocabularies.
-- Owner module: compliance. Only fundzim_compliance has privileges (runtime grants derivation: SELECT, INSERT,
-- UPDATE — the guard trigger allows the single lift update only). Runs as fundzim_migrator. Forward-only in
-- production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

ALTER TABLE compliance.compliance_cases DROP CONSTRAINT ck_compliance_cases_type;
ALTER TABLE compliance.compliance_cases ADD CONSTRAINT ck_compliance_cases_type CHECK (case_type IN ('KYC_REVIEW', 'KYB_REVIEW',
  'BENEFICIARY_REVIEW', 'PAYOUT_DESTINATION_REVIEW', 'RISK_REVIEW', 'SANCTIONS', 'PEP_EDD', 'FRAUD_CAMPAIGN', 'ACCOUNT_TAKEOVER',
  'AML_MONITORING', 'PAYOUT_REVIEW', 'FUNDRAISING_AUTHORITY', 'REGULATOR_REQUEST', 'OTHER', 'CAMPAIGN_REVIEW'));
ALTER TABLE compliance.compliance_cases DROP CONSTRAINT ck_compliance_cases_source;
ALTER TABLE compliance.compliance_cases ADD CONSTRAINT ck_compliance_cases_source CHECK (source IN ('KYC_ESCALATION',
  'BENEFICIARY_ESCALATION', 'RISK_ENGINE', 'STAFF', 'ALERT', 'SCREENING', 'REPORT', 'REGULATOR_REQUEST', 'PROVIDER', 'CAMPAIGN_ESCALATION'));
ALTER TABLE compliance.compliance_case_links DROP CONSTRAINT ck_compliance_case_links_subject;
ALTER TABLE compliance.compliance_case_links ADD CONSTRAINT ck_compliance_case_links_subject CHECK (subject_type IN ('USER',
  'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION', 'CAMPAIGN', 'KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT',
  'PAYOUT', 'CAMPAIGN_REVIEW'));
ALTER TABLE compliance.compliance_case_links DROP CONSTRAINT ck_compliance_case_links_role_kind;
ALTER TABLE compliance.compliance_case_links ADD CONSTRAINT ck_compliance_case_links_role_kind CHECK ((role = 'RELATED_OBJECT') =
  (subject_type IN ('KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT', 'PAYOUT', 'CAMPAIGN_REVIEW')));

CREATE TABLE compliance.subject_restrictions (
  id                  uuid        NOT NULL,
  subject_type        text        NOT NULL,
  subject_id          uuid        NOT NULL,
  level               text        NOT NULL,
  case_id             uuid        NOT NULL,
  decision            text        NOT NULL,
  applied_case_version integer,             -- NULL for backfilled rows
  applied_at          timestamptz NOT NULL,
  lifted_at           timestamptz,
  lift_reason         text,
  lifted_case_version integer,
  CONSTRAINT pk_subject_restrictions PRIMARY KEY (id),
  CONSTRAINT fk_subject_restrictions_case_id FOREIGN KEY (case_id) REFERENCES compliance.compliance_cases (id),
  CONSTRAINT ck_subject_restrictions_subject CHECK (subject_type IN ('USER', 'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION', 'CAMPAIGN')),
  CONSTRAINT ck_subject_restrictions_level CHECK (level IN ('RESTRICTED', 'SUSPENDED', 'OFFBOARDED')),
  CONSTRAINT ck_subject_restrictions_decision CHECK (
       (decision = 'RESTRICT' AND level = 'RESTRICTED') OR (decision = 'SUSPEND' AND level = 'SUSPENDED')
    OR (decision IN ('OFFBOARD', 'CONFIRMED_FRAUD') AND level = 'OFFBOARDED')),
  CONSTRAINT ck_subject_restrictions_lift CHECK ((lifted_at IS NULL) = (lift_reason IS NULL)
    AND (lift_reason IS NULL OR lift_reason IN ('CLEARED', 'EDD_CONDITIONS', 'SUPERSEDED', 'SUBJECT_UNLINKED'))
    AND (lifted_at IS NULL OR lifted_at >= applied_at))
);
COMMENT ON TABLE compliance.subject_restrictions IS 'C2. Restriction projection of approved compliance resolutions (ADR-037 §3); levels only, never reasons.';
CREATE UNIQUE INDEX uq_subject_restrictions_active ON compliance.subject_restrictions (case_id, subject_type, subject_id)
  WHERE lifted_at IS NULL;
CREATE INDEX ix_subject_restrictions_subject ON compliance.subject_restrictions (subject_type, subject_id) WHERE lifted_at IS NULL;
CREATE FUNCTION compliance.subject_restrictions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.lifted_at IS NOT NULL THEN
    RAISE EXCEPTION 'subject restriction % is already lifted', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF (to_jsonb(OLD) - ARRAY['lifted_at','lift_reason','lifted_case_version'])
     IS DISTINCT FROM (to_jsonb(NEW) - ARRAY['lifted_at','lift_reason','lifted_case_version']) THEN
    RAISE EXCEPTION 'subject restriction % is immutable except its lift', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_subject_restrictions_guard BEFORE UPDATE ON compliance.subject_restrictions
  FOR EACH ROW EXECUTE FUNCTION compliance.subject_restrictions_guard();
CREATE TRIGGER trg_subject_restrictions_no_delete BEFORE DELETE ON compliance.subject_restrictions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_subject_restrictions_no_truncate BEFORE TRUNCATE ON compliance.subject_restrictions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- backfill: every currently linked PRIMARY_SUBJECT party of a case whose restricting resolution is APPROVED
-- (the migrator owns the tables and is not subject to RLS, so RESTRICTED_STR cases are included)
INSERT INTO compliance.subject_restrictions (id, subject_type, subject_id, level, case_id, decision, applied_case_version, applied_at)
SELECT md5('subject_restriction:' || c.id || ':' || l.subject_type || ':' || l.subject_id)::uuid, l.subject_type, l.subject_id,
       CASE c.decision WHEN 'RESTRICT' THEN 'RESTRICTED' WHEN 'SUSPEND' THEN 'SUSPENDED' ELSE 'OFFBOARDED' END,
       c.id, c.decision, NULL, coalesce(c.approved_at, c.decided_at)
  FROM compliance.compliance_cases c
  JOIN compliance.compliance_case_links l ON l.case_id = c.id
 WHERE c.status IN ('RESOLVED', 'CLOSED') AND c.resolution_status = 'APPROVED'
   AND c.decision IN ('RESTRICT', 'SUSPEND', 'OFFBOARD', 'CONFIRMED_FRAUD')
   AND l.link_action = 'LINKED' AND l.role = 'PRIMARY_SUBJECT'
   AND l.subject_type IN ('USER', 'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION', 'CAMPAIGN')
   AND NOT EXISTS (SELECT 1 FROM compliance.compliance_case_links u WHERE u.unlinks_id = l.id);

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Development rollback only. Constraints are restored NOT VALID so rows created with the Stage 6 vocabularies
-- (which cannot be deleted) do not block the rollback.
DROP TABLE IF EXISTS compliance.subject_restrictions;
DROP FUNCTION IF EXISTS compliance.subject_restrictions_guard();
ALTER TABLE compliance.compliance_case_links DROP CONSTRAINT ck_compliance_case_links_role_kind;
ALTER TABLE compliance.compliance_case_links ADD CONSTRAINT ck_compliance_case_links_role_kind CHECK ((role = 'RELATED_OBJECT') =
  (subject_type IN ('KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT', 'PAYOUT'))) NOT VALID;
ALTER TABLE compliance.compliance_case_links DROP CONSTRAINT ck_compliance_case_links_subject;
ALTER TABLE compliance.compliance_case_links ADD CONSTRAINT ck_compliance_case_links_subject CHECK (subject_type IN ('USER',
  'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION', 'CAMPAIGN', 'KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT',
  'PAYOUT')) NOT VALID;
ALTER TABLE compliance.compliance_cases DROP CONSTRAINT ck_compliance_cases_source;
ALTER TABLE compliance.compliance_cases ADD CONSTRAINT ck_compliance_cases_source CHECK (source IN ('KYC_ESCALATION',
  'BENEFICIARY_ESCALATION', 'RISK_ENGINE', 'STAFF', 'ALERT', 'SCREENING', 'REPORT', 'REGULATOR_REQUEST', 'PROVIDER')) NOT VALID;
ALTER TABLE compliance.compliance_cases DROP CONSTRAINT ck_compliance_cases_type;
ALTER TABLE compliance.compliance_cases ADD CONSTRAINT ck_compliance_cases_type CHECK (case_type IN ('KYC_REVIEW', 'KYB_REVIEW',
  'BENEFICIARY_REVIEW', 'PAYOUT_DESTINATION_REVIEW', 'RISK_REVIEW', 'SANCTIONS', 'PEP_EDD', 'FRAUD_CAMPAIGN', 'ACCOUNT_TAKEOVER',
  'AML_MONITORING', 'PAYOUT_REVIEW', 'FUNDRAISING_AUTHORITY', 'REGULATOR_REQUEST', 'OTHER')) NOT VALID;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
