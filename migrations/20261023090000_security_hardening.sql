-- Stage 18 security hardening (ADR-050). Owner modules: audit (chain verification, checkpoints), operations (security
-- alerts, backup evidence), payments (status-poll audit index). Runs as fundzim_migrator. Forward-only in production (ADR-028); Down is for
-- development.
--
-- Audit chain verification (F-S18-08, KI-S17-12): audit.verify_chain recomputes every row's hash (jsonb rendering +
-- SHA-256, ~30 µs a row); a set-based rewrite measured the same (40.4 s vs 40.1 s over 1 314 358 rows), so the cost is
-- the hashing, not the loop. Verification is therefore split, never weakened:
--   * audit.verify_chain_range(table, from, to) checks a contiguous seq range, including the link of its first row to
--     the stored hash of row from-1, so ranges verified on several connections cover the chain exactly like one pass;
--   * audit.chain_checkpoints records a verified head (seq, hash) with an HMAC computed by the worker under a key the
--     database does not hold; incremental verification re-checks the checkpoint row and everything after it. Rewriting
--     an earlier row changes its hash and therefore every later prev_hash up to the checkpoint row, whose stored hash
--     then differs from the MAC-protected checkpoint;
--   * audit.chain_verifications is the append-only log of every run (FULL or INCREMENTAL) and its result.
-- Both tables are append-only (forbid_mutation). A database superuser can still disable triggers: tamper EVIDENCE, not
-- tamper-proof storage (external anchoring of checkpoint heads is EXTERNALLY_PENDING, docs/stage-18/security-monitoring.md).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '600s';

CREATE FUNCTION audit.verify_chain_range(p_table regclass, p_from bigint, p_to bigint)
  RETURNS TABLE (seq bigint, problem text) LANGUAGE plpgsql
  SET timezone = 'UTC' SET bytea_output = 'hex' AS $$
DECLARE
  r          record;
  prev_seq   bigint := p_from - 1;
  prev_hash  bytea  := NULL;
BEGIN
  IF p_table NOT IN ('audit.audit_events'::regclass, 'audit.security_audit_events'::regclass) THEN
    RAISE EXCEPTION 'not an audit chain: %', p_table USING ERRCODE = 'invalid_parameter_value';
  END IF;
  IF p_from < 1 OR p_to < p_from THEN
    RAISE EXCEPTION 'invalid range % .. %', p_from, p_to USING ERRCODE = 'invalid_parameter_value';
  END IF;
  IF p_from > 1 THEN
    EXECUTE format('SELECT t.hash FROM %s t WHERE t.seq = $1', p_table) INTO prev_hash USING p_from - 1;
    IF prev_hash IS NULL THEN
      seq := p_from; problem := 'seq gap: row ' || (p_from - 1) || ' is missing'; RETURN NEXT;
    END IF;
  END IF;
  FOR r IN EXECUTE format(
    'SELECT t.seq AS s, t.prev_hash AS ph, t.hash AS h, jsonb_strip_nulls(to_jsonb(t) - ''hash'') AS c
       FROM %s t WHERE t.seq BETWEEN $1 AND $2 ORDER BY t.seq', p_table) USING p_from, p_to
  LOOP
    IF r.s <> prev_seq + 1 THEN
      seq := r.s; problem := 'seq gap after ' || prev_seq; RETURN NEXT;
    END IF;
    IF r.ph IS DISTINCT FROM prev_hash THEN
      seq := r.s; problem := 'prev_hash does not match previous row'; RETURN NEXT;
    END IF;
    IF r.h <> sha256(convert_to(r.c::text, 'UTF8')) THEN
      seq := r.s; problem := 'hash mismatch (row modified)'; RETURN NEXT;
    END IF;
    prev_seq := r.s;
    prev_hash := r.h;
  END LOOP;
  IF prev_seq < p_to THEN
    seq := p_to; problem := 'seq gap: rows after ' || prev_seq || ' up to ' || p_to || ' are missing'; RETURN NEXT;
  END IF;
END $$;

-- the current head of a chain (no row: empty chain)
CREATE FUNCTION audit.chain_head(p_table regclass) RETURNS TABLE (seq bigint, hash bytea) LANGUAGE plpgsql STABLE AS $$
BEGIN
  IF p_table NOT IN ('audit.audit_events'::regclass, 'audit.security_audit_events'::regclass) THEN
    RAISE EXCEPTION 'not an audit chain: %', p_table USING ERRCODE = 'invalid_parameter_value';
  END IF;
  RETURN QUERY EXECUTE format('SELECT t.seq, t.hash FROM %s t ORDER BY t.seq DESC LIMIT 1', p_table);
END $$;

CREATE TABLE audit.chain_checkpoints (
  id            uuid        NOT NULL,
  chain         text        NOT NULL,
  seq           bigint      NOT NULL,
  hash          bytea       NOT NULL,
  mac           bytea       NOT NULL,                 -- HMAC-SHA-256 under the worker's key ('audit.chain_checkpoint/v1')
  mac_key_id    text        NOT NULL,
  verification_id uuid      NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_chain_checkpoints PRIMARY KEY (id),
  CONSTRAINT uq_chain_checkpoints_seq UNIQUE (chain, mac_key_id, seq),
  CONSTRAINT ck_chain_checkpoints_chain CHECK (chain IN ('audit.audit_events', 'audit.security_audit_events')),
  CONSTRAINT ck_chain_checkpoints_seq CHECK (seq >= 1),
  CONSTRAINT ck_chain_checkpoints_hash CHECK (octet_length(hash) = 32 AND octet_length(mac) = 32)
);
CREATE TRIGGER trg_chain_checkpoints_no_mutation BEFORE UPDATE OR DELETE ON audit.chain_checkpoints
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_chain_checkpoints_no_truncate BEFORE TRUNCATE ON audit.chain_checkpoints
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE audit.chain_verifications (
  id            uuid        NOT NULL,
  chain         text        NOT NULL,
  mode          text        NOT NULL,
  from_seq      bigint      NOT NULL,
  to_seq        bigint      NOT NULL,                 -- 0 with from_seq 1: empty chain
  checkpoint_id uuid,                                 -- INCREMENTAL: the checkpoint it started from
  problems      integer     NOT NULL,
  first_problem text,
  outcome       text        NOT NULL,
  workers       integer     NOT NULL,
  duration_ms   bigint      NOT NULL,
  started_at    timestamptz NOT NULL,
  finished_at   timestamptz NOT NULL,
  CONSTRAINT pk_chain_verifications PRIMARY KEY (id),
  CONSTRAINT ck_chain_verifications_chain CHECK (chain IN ('audit.audit_events', 'audit.security_audit_events')),
  CONSTRAINT ck_chain_verifications_mode CHECK (mode IN ('FULL', 'INCREMENTAL')),
  CONSTRAINT ck_chain_verifications_outcome CHECK (outcome IN ('INTACT', 'PROBLEMS_FOUND', 'CHECKPOINT_INVALID')),
  CONSTRAINT ck_chain_verifications_consistent CHECK ((outcome = 'INTACT') = (problems = 0)),
  CONSTRAINT ck_chain_verifications_range CHECK (from_seq >= 1 AND to_seq >= from_seq - 1),
  CONSTRAINT ck_chain_verifications_incremental CHECK ((mode = 'INCREMENTAL') = (checkpoint_id IS NOT NULL)),
  CONSTRAINT fk_chain_verifications_checkpoint FOREIGN KEY (checkpoint_id) REFERENCES audit.chain_checkpoints (id)
);
CREATE INDEX ix_chain_verifications_recent ON audit.chain_verifications (chain, finished_at DESC);
CREATE TRIGGER trg_chain_verifications_no_mutation BEFORE UPDATE OR DELETE ON audit.chain_verifications
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_chain_verifications_no_truncate BEFORE TRUNCATE ON audit.chain_verifications
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
ALTER TABLE audit.chain_checkpoints ADD CONSTRAINT fk_chain_checkpoints_verification
  FOREIGN KEY (verification_id) REFERENCES audit.chain_verifications (id) DEFERRABLE INITIALLY DEFERRED;

-- payments: a status poll looks up the previous completed poll of its payment (repeated answers are not re-audited)
CREATE INDEX ix_payment_attempts_status_polls ON app.payment_attempts (payment_intent_id, completed_at DESC, id DESC)
  WHERE operation = 'GET_STATUS' AND completed_at IS NOT NULL;

-- ---- security alerts (owner: operations; docs/stage-18/security-monitoring.md) ------------------------------------------
-- Persisted, deduplicated alerts raised by the worker's security scan from module snapshots (never from another module's
-- tables). Severity INFO..CRITICAL; lifecycle OPEN → ACKNOWLEDGED → RESOLVED (database-guarded; a person acknowledges
-- and resolves, never the scan). A condition that recurs after resolution opens a new alert. Escalation links an
-- incident. No external notification is sent (Stage 15 rules; no provider is selected).
CREATE TABLE app.ops_security_alerts (
  id                uuid        NOT NULL,
  alert_key         text        NOT NULL,
  dedupe_key        text        NOT NULL,
  severity          text        NOT NULL,
  source_module     text        NOT NULL,
  component         text        NOT NULL,
  title             text        NOT NULL,
  source            text        NOT NULL,
  evidence          jsonb       NOT NULL DEFAULT '{}',
  status            text        NOT NULL,
  condition_active  boolean     NOT NULL DEFAULT true,
  occurrences       integer     NOT NULL DEFAULT 1,
  first_seen_at     timestamptz NOT NULL,
  last_seen_at      timestamptz NOT NULL,
  cleared_at        timestamptz,
  acknowledged_by   uuid,
  acknowledged_at   timestamptz,
  escalation_status text        NOT NULL DEFAULT 'NONE',
  escalated_by      uuid,
  escalated_at      timestamptz,
  incident_id       uuid,
  resolved_by       uuid,
  resolved_at       timestamptz,
  resolution_note   text,
  version           integer     NOT NULL DEFAULT 1,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ops_security_alerts PRIMARY KEY (id),
  CONSTRAINT fk_ops_security_alerts_ack FOREIGN KEY (acknowledged_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_security_alerts_esc FOREIGN KEY (escalated_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_security_alerts_res FOREIGN KEY (resolved_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_security_alerts_incident FOREIGN KEY (incident_id) REFERENCES app.ops_incidents (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_security_alerts_key CHECK (alert_key ~ '^[A-Z][A-Z0-9_]{2,80}$' AND char_length(dedupe_key) BETWEEN 3 AND 200),
  CONSTRAINT ck_ops_security_alerts_severity CHECK (severity IN ('INFO','LOW','MEDIUM','HIGH','CRITICAL')),
  CONSTRAINT ck_ops_security_alerts_status CHECK (status IN ('OPEN','ACKNOWLEDGED','RESOLVED')),
  CONSTRAINT ck_ops_security_alerts_texts CHECK (char_length(title) BETWEEN 3 AND 300 AND char_length(source) <= 300 AND
    source_module ~ '^[a-z][a-z_]{1,40}$' AND char_length(component) BETWEEN 1 AND 80),
  CONSTRAINT ck_ops_security_alerts_evidence CHECK (jsonb_typeof(evidence) = 'object' AND octet_length(evidence::text) <= 4096),
  CONSTRAINT ck_ops_security_alerts_ack CHECK (status = 'OPEN' OR (acknowledged_by IS NOT NULL AND acknowledged_at IS NOT NULL)),
  CONSTRAINT ck_ops_security_alerts_resolved CHECK ((status = 'RESOLVED') = (resolved_by IS NOT NULL AND resolved_at IS NOT NULL AND
    resolution_note IS NOT NULL AND char_length(resolution_note) BETWEEN 5 AND 2000)),
  CONSTRAINT ck_ops_security_alerts_escalation CHECK (escalation_status IN ('NONE','ESCALATED') AND
    ((escalation_status = 'ESCALATED') = (escalated_by IS NOT NULL AND escalated_at IS NOT NULL AND incident_id IS NOT NULL))),
  CONSTRAINT ck_ops_security_alerts_counts CHECK (occurrences >= 1 AND last_seen_at >= first_seen_at)
);
-- one live alert per condition: repeats increment occurrences
CREATE UNIQUE INDEX uq_ops_security_alerts_live ON app.ops_security_alerts (dedupe_key) WHERE status <> 'RESOLVED';
CREATE INDEX ix_ops_security_alerts_queue ON app.ops_security_alerts (status, severity, last_seen_at DESC);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ops_security_alert', '',             'OPEN'),
  ('ops_security_alert', 'OPEN',         'ACKNOWLEDGED'),
  ('ops_security_alert', 'ACKNOWLEDGED', 'RESOLVED');
CREATE TRIGGER trg_ops_security_alerts_guard_status BEFORE INSERT OR UPDATE OF status ON app.ops_security_alerts
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ops_security_alert');
CREATE TRIGGER trg_ops_security_alerts_identity BEFORE UPDATE ON app.ops_security_alerts
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('alert_key', 'dedupe_key', 'source_module', 'first_seen_at', 'created_at');
CREATE TRIGGER trg_ops_security_alerts_updated_at BEFORE UPDATE ON app.ops_security_alerts FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_ops_security_alerts_no_delete BEFORE DELETE ON app.ops_security_alerts FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.ops_security_alert_events (
  id          uuid        NOT NULL,
  alert_id    uuid        NOT NULL,
  event_type  text        NOT NULL,
  actor_id    uuid,
  note        text,
  details     jsonb       NOT NULL DEFAULT '{}',
  occurred_at timestamptz NOT NULL,
  CONSTRAINT pk_ops_security_alert_events PRIMARY KEY (id),
  CONSTRAINT fk_ops_security_alert_events_alert FOREIGN KEY (alert_id) REFERENCES app.ops_security_alerts (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_security_alert_events_actor FOREIGN KEY (actor_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_security_alert_events_type CHECK (event_type IN ('RAISED','RECURRED','CONDITION_CLEARED','ACKNOWLEDGED','ESCALATED','RESOLVED')),
  CONSTRAINT ck_ops_security_alert_events_actor CHECK ((event_type IN ('ACKNOWLEDGED','ESCALATED','RESOLVED')) = (actor_id IS NOT NULL)),
  CONSTRAINT ck_ops_security_alert_events_note CHECK (note IS NULL OR char_length(note) <= 2000),
  CONSTRAINT ck_ops_security_alert_events_details CHECK (jsonb_typeof(details) = 'object' AND octet_length(details::text) <= 4096)
);
CREATE INDEX ix_ops_security_alert_events_alert ON app.ops_security_alert_events (alert_id, occurred_at);
CREATE TRIGGER trg_ops_security_alert_events_no_mutation BEFORE UPDATE OR DELETE ON app.ops_security_alert_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- Backup and restore evidence (owner: operations). Recorded by `fundzimctl backup record` from a backup manifest or a
-- restore-drill report; append-only; the security scan alerts when no successful backup or verified restore is recent.
CREATE TABLE app.ops_backup_runs (
  id              uuid        NOT NULL,
  kind            text        NOT NULL,
  backup_id       text        NOT NULL,
  environment     text        NOT NULL,
  outcome         text        NOT NULL,
  started_at      timestamptz NOT NULL,
  finished_at     timestamptz NOT NULL,
  manifest_sha256 bytea       NOT NULL,
  details         jsonb       NOT NULL DEFAULT '{}',
  recorded_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ops_backup_runs PRIMARY KEY (id),
  CONSTRAINT uq_ops_backup_runs UNIQUE (kind, backup_id, manifest_sha256, started_at),
  CONSTRAINT ck_ops_backup_runs_kind CHECK (kind IN ('BACKUP','RESTORE_DRILL')),
  CONSTRAINT ck_ops_backup_runs_outcome CHECK (outcome IN ('SUCCEEDED','FAILED')),
  CONSTRAINT ck_ops_backup_runs_id CHECK (backup_id ~ '^fundzim-[a-z]+-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$'),
  CONSTRAINT ck_ops_backup_runs_env CHECK (environment IN ('development','test','staging','production')),
  CONSTRAINT ck_ops_backup_runs_times CHECK (finished_at >= started_at),
  CONSTRAINT ck_ops_backup_runs_hash CHECK (octet_length(manifest_sha256) = 32),
  CONSTRAINT ck_ops_backup_runs_details CHECK (jsonb_typeof(details) = 'object' AND octet_length(details::text) <= 16384)
);
CREATE INDEX ix_ops_backup_runs_recent ON app.ops_backup_runs (kind, outcome, finished_at DESC);
CREATE TRIGGER trg_ops_backup_runs_no_mutation BEFORE UPDATE OR DELETE ON app.ops_backup_runs
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:security.alert.view')::uuid, 'security.alert.view', 'View security alerts, their evidence and backup evidence (Stage 18)', true, false),
  (md5('permission:security.alert.manage')::uuid, 'security.alert.manage', 'Acknowledge, escalate and resolve security alerts (Stage 18)', true, true);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES
  ('SECURITY_ADMIN','security.alert.view'), ('SUPER_ADMIN','security.alert.view'), ('ADMIN','security.alert.view'),
  ('SECURITY_ADMIN','security.alert.manage'), ('ADMIN','security.alert.manage')
) AS rp(role_code, perm);

-- ---- retention registry (owner: audit; docs/stage-18/privacy-review.md §5) ----------------------------------------------
-- One row per record family: its classification, owner and retention status. No legal retention period is invented
-- (LR-012, LR-074): every financial, audit, identity and evidence family is LEGAL_REVIEW_REQUIRED with no period, and
-- deletion_mode NEVER_DELETE. OPERATIONAL_ONLY rows describe periods FundZim already applies to operational data that
-- is not a record of anything (expired sessions, dispatched outbox rows, daily dedupe hashes) and point at the code
-- that applies them. A period can be set only together with an approval reference, an approver and the decision time
-- (APPROVED); nothing in FundZim reads this table to delete anything yet. Legal holds on evidence stay in
-- audit.evidence_holds (maker-checker).
CREATE TABLE audit.retention_policies (
  record_family  text        NOT NULL,
  classification text        NOT NULL,
  owner_module   text        NOT NULL,
  description    text        NOT NULL,
  status         text        NOT NULL,
  deletion_mode  text        NOT NULL,
  retention_period interval,
  legal_refs     text[]      NOT NULL DEFAULT '{}',
  applied_by     text,
  approval_ref   text,
  approved_by    uuid,
  approved_at    timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_retention_policies PRIMARY KEY (record_family),
  CONSTRAINT ck_retention_policies_family CHECK (record_family ~ '^[a-z][a-z0-9_.]{2,80}$'),
  CONSTRAINT ck_retention_policies_class CHECK (classification IN ('C1','C2','C3','C4')),
  CONSTRAINT ck_retention_policies_status CHECK (status IN ('LEGAL_REVIEW_REQUIRED','OPERATIONAL_ONLY','APPROVED')),
  CONSTRAINT ck_retention_policies_mode CHECK (deletion_mode IN ('NEVER_DELETE','PURGE_AFTER_PERIOD','ARCHIVE_AFTER_PERIOD')),
  CONSTRAINT ck_retention_policies_review CHECK (status <> 'LEGAL_REVIEW_REQUIRED' OR
    (retention_period IS NULL AND deletion_mode = 'NEVER_DELETE' AND cardinality(legal_refs) > 0)),
  CONSTRAINT ck_retention_policies_operational CHECK (status <> 'OPERATIONAL_ONLY' OR (retention_period IS NOT NULL AND applied_by IS NOT NULL
    AND classification IN ('C1','C2'))),
  CONSTRAINT ck_retention_policies_approved CHECK ((status = 'APPROVED') = (approval_ref IS NOT NULL AND approved_by IS NOT NULL AND approved_at IS NOT NULL))
);
CREATE TRIGGER trg_retention_policies_no_delete BEFORE DELETE ON audit.retention_policies FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
INSERT INTO audit.retention_policies (record_family, classification, owner_module, description, status, deletion_mode, retention_period, legal_refs, applied_by) VALUES
  ('audit.audit_events', 'C2', 'audit', 'Business audit trail (hash-chained)', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('audit.security_audit_events', 'C2', 'audit', 'Security audit trail (hash-chained)', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('audit.evidence_records', 'C2', 'audit', 'Evidence records (hashes and references, never content)', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('audit.chain_verifications', 'C2', 'audit', 'Audit chain verification runs and checkpoints', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('ledger.journals', 'C2', 'ledger', 'Ledger journals, entries and adjustments (TEST ledger)', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('payments.records', 'C2', 'payments', 'Donation intents, payments, attempts, events and provider evidence', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('payouts.records', 'C3', 'payouts', 'Withdrawal requests, approvals, attempts and destinations (encrypted)', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('fees.records', 'C2', 'fees', 'Fee policies, calculations, assessments and adjustments', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('recon.records', 'C2', 'reconciliation', 'Statement imports, lines, matches, exceptions and resolutions', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('kyc.identity', 'C3', 'kyc', 'KYC/KYB cases, identities and documents', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012,LR-074}', NULL),
  ('compliance.cases', 'C3', 'compliance', 'Compliance cases, notes and restrictions', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('risk.cases', 'C3', 'risk', 'Risk cases, evidence, holds and screening requests', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012,LR-074}', NULL),
  ('notifications.records', 'C2', 'notifications', 'Notifications, deliveries, consent records and suppressions', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('identity.security_events', 'C2', 'auth', 'Authentication and security events (IP addresses)', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012,LR-035}', NULL),
  ('operations.records', 'C2', 'operations', 'Support cases, incidents, access reviews, security alerts, backup evidence', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('storage.private_objects', 'C3', 'storage', 'Private documents and statement files in object storage', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012,LR-074}', NULL),
  ('backups.sets', 'C3', 'operations', 'Encrypted backup sets (database, object store, configuration)', 'LEGAL_REVIEW_REQUIRED', 'NEVER_DELETE', NULL, '{LR-012}', NULL),
  ('ops.outbox_dispatched', 'C2', 'platform', 'Dispatched outbox rows (delivery bookkeeping, not a record)', 'OPERATIONAL_ONLY', 'PURGE_AFTER_PERIOD', interval '7 days', '{}',
     'outbox.PurgeWorker (OUTBOX_RETENTION, default 168h)'),
  ('ops.idempotency_keys', 'C2', 'platform', 'Expired idempotency keys (replay protection window)', 'OPERATIONAL_ONLY', 'PURGE_AFTER_PERIOD', interval '24 hours', '{}',
     'jobs.PurgeIdempotencyKeysWorker'),
  ('discovery.dedupe_hashes', 'C2', 'discovery', 'Daily keyed visitor dedupe hashes (analytics)', 'OPERATIONAL_ONLY', 'PURGE_AFTER_PERIOD', interval '2 days', '{}',
     'discovery.PurgeEngagementKeys (EngagementKeysDays = 2)');

-- ---- runtime grants: the Stage 16 procedure plus the Stage 18 audit verification routines -----------------------------
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly, fundzim_ledger';
  -- Stage 16: discovery dedupe keys are purged after two days (they are not history)
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events','app.discovery_engagement_keys'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles',
                                          'app.campaign_locations'];  -- Stage 16
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
  -- Stage 10: the ledger schema belongs to fundzim_ledger alone (fundzim_app has no ledger privileges at all).
  ledger_reference constant text[] := ARRAY['ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines','ledger.ledger_environment'];
  s text;
  i int;
  r record;
  fq text;
  privs text;
BEGIN
  FOREACH s IN ARRAY schemas LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('REVOKE ALL ON SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL ROUTINES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
  END LOOP;

  -- fundzim_app: app, queue, audit, risk, recon. Never kyc, compliance or ledger (Stage 10).
  FOREACH s IN ARRAY ARRAY['app','queue','audit','risk','recon'] LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO fundzim_app', s);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO fundzim_app', s);
  END LOOP;

  FOR r IN
    SELECT n.nspname AS sch, c.relname AS tbl, c.relkind,
           format('%I.%I', n.nspname, c.relname) AS rel,
           coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update,
           coalesce(bool_or((t.tgtype & 8)  <> 0), false) AS no_delete
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                            AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
     WHERE n.nspname IN ('app','queue','audit','risk','recon')
       AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
     GROUP BY n.nspname, c.relname, c.relkind
  LOOP
    fq := r.sch || '.' || r.tbl;
    IF r.relkind IN ('v','m') THEN
      IF r.tbl NOT LIKE 'ro\_%' THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_app', r.rel);
      END IF;
      CONTINUE;
    END IF;
    IF fq = ANY (reference_only) THEN
      privs := 'SELECT';
    ELSIF r.sch = 'queue' THEN
      privs := 'SELECT, INSERT, UPDATE, DELETE';
    ELSIF r.sch = 'audit' THEN
      privs := 'SELECT, INSERT';
      -- legal_hold is the only mutable evidence column (trigger-checked)
      IF fq = 'audit.evidence_records' THEN privs := privs || ', UPDATE'; END IF;
    ELSE                                                        -- app, risk, recon
      privs := 'SELECT, INSERT';
      IF NOT r.no_update THEN privs := privs || ', UPDATE'; END IF;
      IF fq = ANY (app_delete_ok) AND NOT r.no_delete THEN privs := privs || ', DELETE'; END IF;
    END IF;
    EXECUTE format('GRANT %s ON %s TO fundzim_app', privs, r.rel);
  END LOOP;

  IF to_regnamespace('queue') IS NOT NULL THEN
    EXECUTE 'GRANT EXECUTE ON ALL ROUTINES IN SCHEMA queue TO fundzim_app';
    FOR r IN SELECT t.oid::regtype AS typ FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
              WHERE n.nspname = 'queue' AND t.typtype IN ('e','d','c') AND t.typrelid = 0 LOOP
      EXECUTE format('GRANT USAGE ON TYPE %s TO fundzim_app', r.typ);
    END LOOP;
  END IF;

  -- restricted roles: their own schema, what shared triggers read, and the gateways
  GRANT USAGE ON SCHEMA app TO fundzim_kyc, fundzim_compliance;
  GRANT SELECT ON app.status_transitions TO fundzim_kyc, fundzim_compliance;
  GRANT USAGE ON SCHEMA audit TO fundzim_kyc, fundzim_compliance;
  FOR i IN 1 .. array_length(restricted, 1) LOOP
    s := restricted[i][1];
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', s, restricted[i][2]);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I', s, restricted[i][2]);
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = s AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO %I', r.rel, restricted[i][2]);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO %I', r.rel, restricted[i][2]);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO %I', r.rel, restricted[i][2]);
      END IF;
    END LOOP;
  END LOOP;
  -- Stage 10: fundzim_ledger — the ledger schema (append-only tables: SELECT, INSERT; status tables also UPDATE; the
  -- balance projection SELECT only, written by SECURITY DEFINER functions), reference data SELECT, and what shared
  -- triggers read in app. No DELETE or TRUNCATE anywhere. fundzim_readonly: the reporting views.
  IF to_regnamespace('ledger') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA ledger TO fundzim_ledger;
    GRANT USAGE ON SCHEMA app TO fundzim_ledger;
    GRANT SELECT ON app.status_transitions, app.currencies TO fundzim_ledger;
    GRANT USAGE ON SCHEMA audit TO fundzim_ledger;
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             format('%s.%s', n.nspname, c.relname) AS fq,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = 'ledger' AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger, fundzim_readonly', r.rel);
      ELSIF r.fq = ANY (ledger_reference) THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger', r.rel);
      ELSIF r.tbl = 'ledger_balances' THEN
        -- UPDATE only so the posting path can lock projection rows (SELECT ... FOR UPDATE, ascending account id);
        -- trg_ledger_balances_guard_update rejects every direct UPDATE (writes come from SECURITY DEFINER functions)
        EXECUTE format('GRANT SELECT, UPDATE ON %s TO fundzim_ledger', r.rel);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO fundzim_ledger', r.rel);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO fundzim_ledger', r.rel);
      END IF;
    END LOOP;
    EXECUTE 'GRANT USAGE ON SCHEMA ledger TO fundzim_readonly';
  END IF;

  -- compliance reads (never writes) the risk outputs it reviews
  FOR r IN SELECT format('%I.%I', n.nspname, c.relname) AS rel FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = 'risk' AND c.relname IN ('risk_signals','risk_assessments','risk_decisions','limits') AND c.relkind = 'r' LOOP
    EXECUTE 'GRANT USAGE ON SCHEMA risk TO fundzim_compliance';
    EXECUTE format('GRANT SELECT ON %s TO fundzim_compliance', r.rel);
  END LOOP;

  IF NOT pg_has_role('fundzim_worker', 'fundzim_app', 'MEMBER') THEN
    RAISE EXCEPTION 'role fundzim_worker must be a member of fundzim_app (GRANT fundzim_app TO fundzim_worker, run by the role administrator)';
  END IF;

  -- routines: EXECUTE only on an explicit allow-list (missing routines are skipped)
  FOR r IN
    SELECT * FROM (VALUES
      ('audit.verify_chain(regclass)', 'fundzim_worker'),
      ('audit.verify_chain_range(regclass,bigint,bigint)', 'fundzim_worker'), -- Stage 18: parallel and incremental verification
      ('audit.chain_head(regclass)', 'fundzim_worker'),
      ('app.payment_status_rank(text)', 'fundzim_app'),              -- Stage 8: CHECK ck_payment_intents_status_rank
      ('app.payout_status_rank(text)', 'fundzim_app'),               -- Stage 11: CHECK ck_payout_requests_status_rank
      ('app.payout_policy_rank(text)', 'fundzim_app'),               -- Stage 11: payout guard (policy only raised)
      ('app.eligibility_results_valid(jsonb,text,text)', 'fundzim_app'), -- Stage 11: CHECK ck_payout_eligibility_decisions_results
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
      -- Stage 10: the ledger posting role
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_ledger'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_ledger'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_ledger'),
      ('ledger.fold_deferred_balances()', 'fundzim_ledger'),
      ('ledger.database_environment()', 'fundzim_ledger'),
      ('ledger.account_class_signature(text)', 'fundzim_ledger'),
      -- ADR-037 §4: identity resolution for self-decision checks (read-only, SECURITY DEFINER)
      ('app.actor_identities(uuid)', 'fundzim_app'),
      ('app.actor_identities(uuid)', 'fundzim_worker'),
      ('app.actor_identities(uuid)', 'fundzim_kyc')
    ) AS t(fn, grantee)
  LOOP
    IF to_regprocedure(r.fn) IS NOT NULL THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.fn, r.grantee);
    END IF;
  END LOOP;

  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA public TO fundzim_app;
    GRANT SELECT ON public.goose_db_version TO fundzim_app;
  END IF;
END $$;


CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly, fundzim_ledger';
  -- Stage 16: discovery dedupe keys are purged after two days (they are not history)
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events','app.discovery_engagement_keys'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles',
                                          'app.campaign_locations'];  -- Stage 16
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
  -- Stage 10: the ledger schema belongs to fundzim_ledger alone (fundzim_app has no ledger privileges at all).
  ledger_reference constant text[] := ARRAY['ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines','ledger.ledger_environment'];
  s text;
  i int;
  r record;
  fq text;
  privs text;
BEGIN
  FOREACH s IN ARRAY schemas LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('REVOKE ALL ON SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL ROUTINES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
  END LOOP;

  -- fundzim_app: app, queue, audit, risk, recon. Never kyc, compliance or ledger (Stage 10).
  FOREACH s IN ARRAY ARRAY['app','queue','audit','risk','recon'] LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO fundzim_app', s);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO fundzim_app', s);
  END LOOP;

  FOR r IN
    SELECT n.nspname AS sch, c.relname AS tbl, c.relkind,
           format('%I.%I', n.nspname, c.relname) AS rel,
           coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update,
           coalesce(bool_or((t.tgtype & 8)  <> 0), false) AS no_delete
      FROM pg_class c
      JOIN pg_namespace n ON n.oid = c.relnamespace
      LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                            AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
     WHERE n.nspname IN ('app','queue','audit','risk','recon')
       AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
     GROUP BY n.nspname, c.relname, c.relkind
  LOOP
    fq := r.sch || '.' || r.tbl;
    IF r.relkind IN ('v','m') THEN
      IF r.tbl NOT LIKE 'ro\_%' THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_app', r.rel);
      END IF;
      CONTINUE;
    END IF;
    IF fq = ANY (reference_only) THEN
      privs := 'SELECT';
    ELSIF r.sch = 'queue' THEN
      privs := 'SELECT, INSERT, UPDATE, DELETE';
    ELSIF r.sch = 'audit' THEN
      privs := 'SELECT, INSERT';
      -- legal_hold is the only mutable evidence column (trigger-checked)
      IF fq = 'audit.evidence_records' THEN privs := privs || ', UPDATE'; END IF;
    ELSE                                                        -- app, risk, recon
      privs := 'SELECT, INSERT';
      IF NOT r.no_update THEN privs := privs || ', UPDATE'; END IF;
      IF fq = ANY (app_delete_ok) AND NOT r.no_delete THEN privs := privs || ', DELETE'; END IF;
    END IF;
    EXECUTE format('GRANT %s ON %s TO fundzim_app', privs, r.rel);
  END LOOP;

  IF to_regnamespace('queue') IS NOT NULL THEN
    EXECUTE 'GRANT EXECUTE ON ALL ROUTINES IN SCHEMA queue TO fundzim_app';
    FOR r IN SELECT t.oid::regtype AS typ FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
              WHERE n.nspname = 'queue' AND t.typtype IN ('e','d','c') AND t.typrelid = 0 LOOP
      EXECUTE format('GRANT USAGE ON TYPE %s TO fundzim_app', r.typ);
    END LOOP;
  END IF;

  -- restricted roles: their own schema, what shared triggers read, and the gateways
  GRANT USAGE ON SCHEMA app TO fundzim_kyc, fundzim_compliance;
  GRANT SELECT ON app.status_transitions TO fundzim_kyc, fundzim_compliance;
  GRANT USAGE ON SCHEMA audit TO fundzim_kyc, fundzim_compliance;
  FOR i IN 1 .. array_length(restricted, 1) LOOP
    s := restricted[i][1];
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO %I', s, restricted[i][2]);
    EXECUTE format('GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA %I TO %I', s, restricted[i][2]);
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = s AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO %I', r.rel, restricted[i][2]);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO %I', r.rel, restricted[i][2]);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO %I', r.rel, restricted[i][2]);
      END IF;
    END LOOP;
  END LOOP;
  -- Stage 10: fundzim_ledger — the ledger schema (append-only tables: SELECT, INSERT; status tables also UPDATE; the
  -- balance projection SELECT only, written by SECURITY DEFINER functions), reference data SELECT, and what shared
  -- triggers read in app. No DELETE or TRUNCATE anywhere. fundzim_readonly: the reporting views.
  IF to_regnamespace('ledger') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA ledger TO fundzim_ledger;
    GRANT USAGE ON SCHEMA app TO fundzim_ledger;
    GRANT SELECT ON app.status_transitions, app.currencies TO fundzim_ledger;
    GRANT USAGE ON SCHEMA audit TO fundzim_ledger;
    FOR r IN
      SELECT c.relname AS tbl, c.relkind, format('%I.%I', n.nspname, c.relname) AS rel,
             format('%s.%s', n.nspname, c.relname) AS fq,
             coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
        FROM pg_class c
        JOIN pg_namespace n ON n.oid = c.relnamespace
        LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                              AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
       WHERE n.nspname = 'ledger' AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
       GROUP BY n.nspname, c.relname, c.relkind
    LOOP
      IF r.relkind IN ('v','m') THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger, fundzim_readonly', r.rel);
      ELSIF r.fq = ANY (ledger_reference) THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_ledger', r.rel);
      ELSIF r.tbl = 'ledger_balances' THEN
        -- UPDATE only so the posting path can lock projection rows (SELECT ... FOR UPDATE, ascending account id);
        -- trg_ledger_balances_guard_update rejects every direct UPDATE (writes come from SECURITY DEFINER functions)
        EXECUTE format('GRANT SELECT, UPDATE ON %s TO fundzim_ledger', r.rel);
      ELSIF r.no_update THEN
        EXECUTE format('GRANT SELECT, INSERT ON %s TO fundzim_ledger', r.rel);
      ELSE
        EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO fundzim_ledger', r.rel);
      END IF;
    END LOOP;
    EXECUTE 'GRANT USAGE ON SCHEMA ledger TO fundzim_readonly';
  END IF;

  -- compliance reads (never writes) the risk outputs it reviews
  FOR r IN SELECT format('%I.%I', n.nspname, c.relname) AS rel FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
            WHERE n.nspname = 'risk' AND c.relname IN ('risk_signals','risk_assessments','risk_decisions','limits') AND c.relkind = 'r' LOOP
    EXECUTE 'GRANT USAGE ON SCHEMA risk TO fundzim_compliance';
    EXECUTE format('GRANT SELECT ON %s TO fundzim_compliance', r.rel);
  END LOOP;

  IF NOT pg_has_role('fundzim_worker', 'fundzim_app', 'MEMBER') THEN
    RAISE EXCEPTION 'role fundzim_worker must be a member of fundzim_app (GRANT fundzim_app TO fundzim_worker, run by the role administrator)';
  END IF;

  -- routines: EXECUTE only on an explicit allow-list (missing routines are skipped)
  FOR r IN
    SELECT * FROM (VALUES
      ('audit.verify_chain(regclass)', 'fundzim_worker'),
      ('app.payment_status_rank(text)', 'fundzim_app'),              -- Stage 8: CHECK ck_payment_intents_status_rank
      ('app.payout_status_rank(text)', 'fundzim_app'),               -- Stage 11: CHECK ck_payout_requests_status_rank
      ('app.payout_policy_rank(text)', 'fundzim_app'),               -- Stage 11: payout guard (policy only raised)
      ('app.eligibility_results_valid(jsonb,text,text)', 'fundzim_app'), -- Stage 11: CHECK ck_payout_eligibility_decisions_results
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
      -- Stage 10: the ledger posting role
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_ledger'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_ledger'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_ledger'),
      ('ledger.fold_deferred_balances()', 'fundzim_ledger'),
      ('ledger.database_environment()', 'fundzim_ledger'),
      ('ledger.account_class_signature(text)', 'fundzim_ledger'),
      -- ADR-037 §4: identity resolution for self-decision checks (read-only, SECURITY DEFINER)
      ('app.actor_identities(uuid)', 'fundzim_app'),
      ('app.actor_identities(uuid)', 'fundzim_worker'),
      ('app.actor_identities(uuid)', 'fundzim_kyc')
    ) AS t(fn, grantee)
  LOOP
    IF to_regprocedure(r.fn) IS NOT NULL THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.fn, r.grantee);
    END IF;
  END LOOP;

  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA public TO fundzim_app;
    GRANT SELECT ON public.goose_db_version TO fundzim_app;
  END IF;
END $$;


ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN ('security.alert.view','security.alert.manage'));
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('security.alert.view','security.alert.manage');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP TABLE IF EXISTS app.ops_backup_runs, app.ops_security_alert_events, app.ops_security_alerts;
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine = 'ops_security_alert';
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
DROP INDEX app.ix_payment_attempts_status_polls;
-- development rollback only (nothing outside these two tables references them)
DROP TABLE IF EXISTS audit.retention_policies;
DROP TABLE audit.chain_checkpoints, audit.chain_verifications;
DROP FUNCTION audit.chain_head(regclass);
DROP FUNCTION audit.verify_chain_range(regclass, bigint, bigint);
CALL app.apply_runtime_grants();
-- +goose StatementEnd
