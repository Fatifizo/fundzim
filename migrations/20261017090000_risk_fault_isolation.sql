-- Stage 14 (ADR-046 §8; KI-S13-11): risk-engine fault isolation. A rule version the engine cannot run (unparseable
-- definition, engine version mismatch) no longer blocks signal recording: the signal and an evaluation of the remaining
-- rules are stored, the evaluation is marked degraded, and each failure is recorded here (append-only) for operations.
-- Owner module: risk. Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

ALTER TABLE risk.risk_evaluations ADD COLUMN degraded boolean NOT NULL DEFAULT false;
ALTER TABLE risk.risk_evaluations ADD COLUMN failed_rule_version_ids uuid[] NOT NULL DEFAULT '{}';
ALTER TABLE risk.risk_evaluations ADD CONSTRAINT ck_risk_evaluations_degraded CHECK (degraded = (cardinality(failed_rule_version_ids) > 0));
ALTER TABLE risk.risk_subject_profiles ADD COLUMN degraded boolean NOT NULL DEFAULT false;

CREATE TABLE risk.risk_rule_failures (
  id               uuid        NOT NULL,
  rule_version_id  uuid        NOT NULL,
  signal_id        uuid        NOT NULL,
  evaluation_id    uuid,
  subject_type     text        NOT NULL,
  subject_id       uuid        NOT NULL,
  error_code       text        NOT NULL,
  detail           text        NOT NULL DEFAULT '',
  occurred_at      timestamptz NOT NULL,
  CONSTRAINT pk_risk_rule_failures PRIMARY KEY (id),
  CONSTRAINT fk_risk_rule_failures_version FOREIGN KEY (rule_version_id) REFERENCES risk.risk_rule_versions (id) ON DELETE RESTRICT,
  CONSTRAINT fk_risk_rule_failures_signal FOREIGN KEY (signal_id) REFERENCES risk.risk_signals (id) ON DELETE RESTRICT,
  CONSTRAINT fk_risk_rule_failures_evaluation FOREIGN KEY (evaluation_id) REFERENCES risk.risk_evaluations (id) ON DELETE RESTRICT,
  CONSTRAINT uq_risk_rule_failures UNIQUE (rule_version_id, signal_id),
  CONSTRAINT ck_risk_rule_failures_code CHECK (error_code IN ('UNPARSEABLE_DEFINITION', 'ENGINE_VERSION_MISMATCH', 'EVALUATION_PANIC')),
  CONSTRAINT ck_risk_rule_failures_detail CHECK (char_length(detail) <= 500)
);
CREATE INDEX ix_risk_rule_failures_version ON risk.risk_rule_failures (rule_version_id, occurred_at DESC);
CREATE INDEX ix_risk_rule_failures_recent ON risk.risk_rule_failures (occurred_at DESC, id DESC);
CREATE TRIGGER trg_risk_rule_failures_no_mutation BEFORE UPDATE OR DELETE ON risk.risk_rule_failures FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

ALTER TABLE risk.risk_monitoring_events DROP CONSTRAINT ck_risk_monitoring_events_type;
ALTER TABLE risk.risk_monitoring_events ADD CONSTRAINT ck_risk_monitoring_events_type CHECK (event_type IN ('RULE_MONITORING_MATCH','HOLD_EXPIRED',
  'CASE_OVERDUE','CASE_UNASSIGNED_CRITICAL','SCREENING_TIMEOUT','SCREENING_CALLBACK_REJECTED','SCREENING_STALE','RECON_MISSING_EVALUATION',
  'RECON_DUPLICATE_CASES','RECON_ORPHANED_HOLD','RECON_CONFLICTING_RESTRICTION','RECON_UNPROCESSED_EVENT','RECON_RULE_EXPIRED',
  'RECON_EVIDENCE_HASH_MISMATCH','RECON_RULE_UNPARSEABLE','RECON_HOLD_PAST_EXPIRY','RULE_EVALUATION_FAILED'));

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE risk.risk_monitoring_events DROP CONSTRAINT ck_risk_monitoring_events_type;
ALTER TABLE risk.risk_monitoring_events ADD CONSTRAINT ck_risk_monitoring_events_type CHECK (event_type IN ('RULE_MONITORING_MATCH','HOLD_EXPIRED',
  'CASE_OVERDUE','CASE_UNASSIGNED_CRITICAL','SCREENING_TIMEOUT','SCREENING_CALLBACK_REJECTED','SCREENING_STALE','RECON_MISSING_EVALUATION',
  'RECON_DUPLICATE_CASES','RECON_ORPHANED_HOLD','RECON_CONFLICTING_RESTRICTION','RECON_UNPROCESSED_EVENT','RECON_RULE_EXPIRED',
  'RECON_EVIDENCE_HASH_MISMATCH','RECON_RULE_UNPARSEABLE','RECON_HOLD_PAST_EXPIRY')) NOT VALID;
DROP TABLE risk.risk_rule_failures;
ALTER TABLE risk.risk_subject_profiles DROP COLUMN degraded;
ALTER TABLE risk.risk_evaluations DROP CONSTRAINT ck_risk_evaluations_degraded;
ALTER TABLE risk.risk_evaluations DROP COLUMN failed_rule_version_ids, DROP COLUMN degraded;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
