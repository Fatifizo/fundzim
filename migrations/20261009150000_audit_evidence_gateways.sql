-- FundZim migration: audit evidence records and holds (Stage 2 draft 0003, deferred from Stage 3 as I-18),
-- write gateways for the restricted roles (Stage 2 lead decision C-3, ADR-035) and runtime grants v3
-- (kyc schema -> fundzim_kyc, compliance schema -> fundzim_compliance). Runs as fundzim_migrator.
-- Forward-only in production (ADR-028).
--
-- Gateways: fundzim_kyc and fundzim_compliance have NO privilege on audit.* or app.outbox_events. They call
-- these SECURITY DEFINER functions (owned by fundzim_migrator, pinned search_path, fully qualified names),
-- which validate the caller (session_user) against an allow-list of action / event-type prefixes, reject
-- metadata keys that would carry C3/C4 values (AUDIT §6 never-embed rule) and append inside the caller's
-- transaction, so domain row + audit event + evidence + outbox commit atomically. The callers cannot read
-- other modules' audit events or outbox rows.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- -----------------------------------------------------------------------------------------------------
-- audit.evidence_records — metadata + SHA-256 of evidence (documents, notes, provider reports); never
-- content. Only legal_hold may change, and only to the value implied by evidence_holds. Classification: C2/C3
-- (metadata). Retention: per retention_class (LR-012).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE audit.evidence_records (
  id                uuid        NOT NULL,
  evidence_type     text        NOT NULL,
  subject_type      text        NOT NULL,
  subject_id        uuid        NOT NULL,
  related_refs      jsonb       NOT NULL DEFAULT '{}',
  collected_at      timestamptz NOT NULL,
  collected_by_type text        NOT NULL,
  collected_by_id   uuid,
  source            text        NOT NULL,
  storage_ref       text,
  content_sha256    bytea       NOT NULL,
  size_bytes        bigint,
  media_type        text,
  classification    text        NOT NULL,
  retention_class   text        NOT NULL,
  legal_hold        boolean     NOT NULL DEFAULT false,
  supersedes_id     uuid,
  audit_event_id    uuid        NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_evidence_records PRIMARY KEY (id),
  CONSTRAINT fk_evidence_records_supersedes_id FOREIGN KEY (supersedes_id) REFERENCES audit.evidence_records (id),
  CONSTRAINT fk_evidence_records_audit_event_id FOREIGN KEY (audit_event_id) REFERENCES audit.audit_events (id),
  CONSTRAINT uq_evidence_records_supersedes_id UNIQUE (supersedes_id),
  CONSTRAINT ck_evidence_records_evidence_type CHECK (evidence_type ~ '^[A-Z][A-Z0-9_]*$'),
  CONSTRAINT ck_evidence_records_subject_type CHECK (subject_type ~ '^[a-z][a-z_]*$'),
  CONSTRAINT ck_evidence_records_collected_by_type CHECK (collected_by_type IN ('user', 'staff', 'system', 'provider', 'vendor')),
  CONSTRAINT ck_evidence_records_collected_by_id CHECK (collected_by_type NOT IN ('user', 'staff') OR collected_by_id IS NOT NULL),
  CONSTRAINT ck_evidence_records_source CHECK (source IN ('upload', 'kyc_vendor', 'psp_report', 'webhook_inbox', 'staff_note',
                                                          'system_snapshot', 'provider_status', 'email', 'call_log')),
  CONSTRAINT ck_evidence_records_content_sha256 CHECK (octet_length(content_sha256) = 32),
  CONSTRAINT ck_evidence_records_size CHECK (size_bytes IS NULL OR size_bytes >= 0),
  CONSTRAINT ck_evidence_records_classification CHECK (classification IN ('C2', 'C3')),
  CONSTRAINT ck_evidence_records_retention_class CHECK (retention_class IN ('KYC', 'FINANCIAL', 'AUDIT', 'CASE', 'CONSENT', 'OPERATIONAL')),
  CONSTRAINT ck_evidence_records_related_refs_object CHECK (jsonb_typeof(related_refs) = 'object'),
  CONSTRAINT ck_evidence_records_locator CHECK (storage_ref IS NOT NULL OR related_refs <> '{}'::jsonb),
  CONSTRAINT ck_evidence_records_storage_ref CHECK (storage_ref IS NULL OR storage_ref ~ '^[a-z0-9_/-]+$'),
  CONSTRAINT ck_evidence_records_not_self CHECK (supersedes_id IS NULL OR supersedes_id <> id)
);
CREATE INDEX ix_evidence_records_subject ON audit.evidence_records (subject_type, subject_id, collected_at);
CREATE INDEX ix_evidence_records_audit_event_id ON audit.evidence_records (audit_event_id);
CREATE INDEX ix_evidence_records_legal_hold ON audit.evidence_records (id) WHERE legal_hold;

-- audit.evidence_holds — append-only legal-hold history; HOLD and RELEASE are both maker-checker.
CREATE TABLE audit.evidence_holds (
  id                 uuid        NOT NULL,
  evidence_record_id uuid        NOT NULL,
  action             text        NOT NULL,
  released_hold_id   uuid,
  reason             text        NOT NULL,
  case_id            uuid,
  requested_by       uuid        NOT NULL,
  approved_by        uuid        NOT NULL,
  occurred_at        timestamptz NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_evidence_holds PRIMARY KEY (id),
  CONSTRAINT uq_evidence_holds_id_evidence_record_id UNIQUE (id, evidence_record_id),
  CONSTRAINT uq_evidence_holds_released_hold_id UNIQUE (released_hold_id),
  CONSTRAINT fk_evidence_holds_evidence_record_id FOREIGN KEY (evidence_record_id) REFERENCES audit.evidence_records (id),
  CONSTRAINT fk_evidence_holds_released_hold_id FOREIGN KEY (released_hold_id, evidence_record_id)
    REFERENCES audit.evidence_holds (id, evidence_record_id),
  CONSTRAINT ck_evidence_holds_action CHECK (action IN ('HOLD', 'RELEASE')),
  CONSTRAINT ck_evidence_holds_release_ref CHECK ((action = 'RELEASE') = (released_hold_id IS NOT NULL)),
  CONSTRAINT ck_evidence_holds_maker_checker CHECK (approved_by <> requested_by),
  CONSTRAINT ck_evidence_holds_reason CHECK (length(reason) >= 3)
);
CREATE INDEX ix_evidence_holds_evidence_record_id ON audit.evidence_holds (evidence_record_id);
CREATE FUNCTION audit.evidence_holds_check_release() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.action = 'RELEASE' AND NOT EXISTS (
       SELECT 1 FROM audit.evidence_holds h WHERE h.id = NEW.released_hold_id AND h.action = 'HOLD') THEN
    RAISE EXCEPTION 'evidence hold release must reference a HOLD row' USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_evidence_holds_check_release BEFORE INSERT ON audit.evidence_holds
  FOR EACH ROW EXECUTE FUNCTION audit.evidence_holds_check_release();
CREATE TRIGGER trg_evidence_holds_no_mutation BEFORE UPDATE OR DELETE ON audit.evidence_holds
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_evidence_holds_no_truncate BEFORE TRUNCATE ON audit.evidence_holds
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION audit.evidence_records_legal_hold_only() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_active boolean;
BEGIN
  IF (to_jsonb(OLD) - 'legal_hold') IS DISTINCT FROM (to_jsonb(NEW) - 'legal_hold') THEN
    RAISE EXCEPTION 'evidence_records is append-only: only legal_hold may change' USING ERRCODE = 'restrict_violation';
  END IF;
  SELECT EXISTS (
    SELECT 1 FROM audit.evidence_holds h
     WHERE h.evidence_record_id = NEW.id AND h.action = 'HOLD'
       AND NOT EXISTS (SELECT 1 FROM audit.evidence_holds r WHERE r.released_hold_id = h.id))
    INTO v_active;
  IF NEW.legal_hold IS DISTINCT FROM v_active THEN
    RAISE EXCEPTION 'legal_hold must match evidence_holds (active hold: %)', v_active USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_evidence_records_legal_hold_only BEFORE UPDATE ON audit.evidence_records
  FOR EACH ROW EXECUTE FUNCTION audit.evidence_records_legal_hold_only();
CREATE TRIGGER trg_evidence_records_no_delete BEFORE DELETE ON audit.evidence_records
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_evidence_records_no_truncate BEFORE TRUNCATE ON audit.evidence_records
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION audit.evidence_records_insert_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.legal_hold THEN
    RAISE EXCEPTION 'evidence records are created without legal_hold; place a hold via evidence_holds'
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_evidence_records_insert_guard BEFORE INSERT ON audit.evidence_records
  FOR EACH ROW EXECUTE FUNCTION audit.evidence_records_insert_guard();

-- -----------------------------------------------------------------------------------------------------
-- Gateways for restricted roles.
-- -----------------------------------------------------------------------------------------------------
-- Caller allow-list: which audit actions and outbox event types each restricted login role may write.
CREATE FUNCTION audit.gateway_prefix_ok(p_kind text, p_value text) RETURNS boolean
LANGUAGE sql STABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT CASE
    WHEN session_user = 'fundzim_kyc' AND p_kind = 'action'
      THEN p_value ~ '^(kyc|kyb|beneficiary|evidence)\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_kyc' AND p_kind = 'event'
      THEN p_value ~ '^kyc\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_compliance' AND p_kind = 'action'
      THEN p_value ~ '^(compliance|risk|evidence)\.[a-z_]+(\.[a-z_]+)*$'
    WHEN session_user = 'fundzim_compliance' AND p_kind = 'event'
      THEN p_value ~ '^compliance\.[a-z_]+(\.[a-z_]+)*$'
    ELSE false
  END
$$;

-- never-embed rule (AUDIT §6): keys that would carry C3/C4 values are refused, at any nesting depth.
CREATE FUNCTION audit.gateway_payload_ok(p jsonb) RETURNS boolean
LANGUAGE sql IMMUTABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT jsonb_typeof(p) = 'object'
     AND pg_catalog.octet_length(p::text) <= 16384
     AND NOT EXISTS (
       SELECT 1 FROM (SELECT p AS val UNION ALL SELECT t.val FROM pg_catalog.jsonb_path_query(p, 'strict $.**') AS t(val)) v
        WHERE pg_catalog.jsonb_typeof(v.val) = 'object'
          AND EXISTS (SELECT 1 FROM pg_catalog.jsonb_object_keys(v.val) k
                       WHERE k ~* '(id_number|document_number|passport_number|date_of_birth|document_image|document_text|selfie|liveness|password|otp|token|secret|private_key|account_number|account_identifier|legal_name|full_name|address|msisdn|phone_number)'
                          OR k ~* '(^|_)(dob|pan|cvv|pin)(_|$)'))
$$;

CREATE FUNCTION audit.append_event(
  p_id uuid, p_security boolean, p_occurred_at timestamptz, p_actor_type text, p_actor_id uuid, p_actor_role text,
  p_action text, p_target_type text, p_target_id uuid, p_outcome text, p_request_id text, p_ip inet,
  p_reason text, p_justification text, p_metadata jsonb)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF p_id IS NULL OR p_occurred_at IS NULL OR p_occurred_at > pg_catalog.now() + interval '5 minutes'
     OR NOT audit.gateway_prefix_ok('action', p_action)
     OR NOT audit.gateway_payload_ok(coalesce(p_metadata, '{}'::jsonb)) THEN
    RAISE EXCEPTION 'audit.append_event: rejected input (caller %, action %)', session_user, p_action
      USING ERRCODE = 'invalid_parameter_value';
  END IF;
  IF p_security THEN
    INSERT INTO audit.security_audit_events (id, seq, occurred_at, actor_type, actor_id, actor_role, action, target_type,
      target_id, outcome, request_id, correlation_id, ip, reason, justification, metadata, hash)
    VALUES (p_id, 0, p_occurred_at, p_actor_type, p_actor_id, p_actor_role, p_action, p_target_type, p_target_id,
      coalesce(p_outcome, 'success'), p_request_id, p_request_id, p_ip, p_reason, p_justification,
      coalesce(p_metadata, '{}'::jsonb), '\x00');
  ELSE
    INSERT INTO audit.audit_events (id, seq, occurred_at, actor_type, actor_id, actor_role, action, target_type,
      target_id, outcome, request_id, correlation_id, ip, reason, justification, metadata, hash)
    VALUES (p_id, 0, p_occurred_at, p_actor_type, p_actor_id, p_actor_role, p_action, p_target_type, p_target_id,
      coalesce(p_outcome, 'success'), p_request_id, p_request_id, p_ip, p_reason, p_justification,
      coalesce(p_metadata, '{}'::jsonb), '\x00');
  END IF;
  RETURN p_id;
END $$;

CREATE FUNCTION audit.record_evidence(
  p_id uuid, p_evidence_type text, p_subject_type text, p_subject_id uuid, p_related_refs jsonb,
  p_collected_at timestamptz, p_collected_by_type text, p_collected_by_id uuid, p_source text, p_storage_ref text,
  p_content_sha256 bytea, p_size_bytes bigint, p_media_type text, p_classification text, p_retention_class text,
  p_audit_event_id uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF session_user NOT IN ('fundzim_kyc', 'fundzim_compliance')
     OR NOT audit.gateway_payload_ok(coalesce(p_related_refs, '{}'::jsonb)) THEN
    RAISE EXCEPTION 'audit.record_evidence: rejected input (caller %)', session_user USING ERRCODE = 'invalid_parameter_value';
  END IF;
  INSERT INTO audit.evidence_records (id, evidence_type, subject_type, subject_id, related_refs, collected_at,
    collected_by_type, collected_by_id, source, storage_ref, content_sha256, size_bytes, media_type, classification,
    retention_class, audit_event_id)
  VALUES (p_id, p_evidence_type, p_subject_type, p_subject_id, coalesce(p_related_refs, '{}'::jsonb), p_collected_at,
    p_collected_by_type, p_collected_by_id, p_source, p_storage_ref, p_content_sha256, p_size_bytes, p_media_type,
    p_classification, p_retention_class, p_audit_event_id);
  RETURN p_id;
END $$;

CREATE FUNCTION app.enqueue_outbox(
  p_id uuid, p_aggregate_type text, p_aggregate_id uuid, p_event_type text, p_payload jsonb,
  p_correlation_id text, p_occurred_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF p_id IS NULL OR p_aggregate_id IS NULL OR p_occurred_at IS NULL
     OR NOT audit.gateway_prefix_ok('event', p_event_type)
     OR NOT audit.gateway_payload_ok(coalesce(p_payload, '{}'::jsonb)) THEN
    RAISE EXCEPTION 'app.enqueue_outbox: rejected input (caller %, event %)', session_user, p_event_type
      USING ERRCODE = 'invalid_parameter_value';
  END IF;
  INSERT INTO app.outbox_events (id, aggregate_type, aggregate_id, event_type, payload, correlation_id, occurred_at)
  VALUES (p_id, p_aggregate_type, p_aggregate_id, p_event_type, coalesce(p_payload, '{}'::jsonb), p_correlation_id, p_occurred_at);
  RETURN p_id;
END $$;

REVOKE ALL ON FUNCTION audit.gateway_prefix_ok(text, text), audit.gateway_payload_ok(jsonb),
  audit.append_event(uuid, boolean, timestamptz, text, uuid, text, text, text, uuid, text, text, inet, text, text, jsonb),
  audit.record_evidence(uuid, text, text, uuid, jsonb, timestamptz, text, uuid, text, text, bytea, bigint, text, text, text, uuid),
  app.enqueue_outbox(uuid, text, uuid, text, jsonb, text, timestamptz) FROM PUBLIC;

-- -----------------------------------------------------------------------------------------------------
-- Runtime grants v3: v2 plus the restricted schemas (kyc -> fundzim_kyc, compliance -> fundzim_compliance)
-- and the gateways. Same derivation rules: SELECT, INSERT; UPDATE unless a forbid_mutation UPDATE trigger
-- exists; never DELETE; reference tables SELECT only.
-- -----------------------------------------------------------------------------------------------------
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly';
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles',
                                          'ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines'];
  restricted constant text[][] := ARRAY[ARRAY['kyc','fundzim_kyc'], ARRAY['compliance','fundzim_compliance']];
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

  -- fundzim_app: app, queue, ledger, audit, risk, recon. Never kyc or compliance.
  FOREACH s IN ARRAY ARRAY['app','queue','ledger','audit','risk','recon'] LOOP
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
     WHERE n.nspname IN ('app','queue','ledger','audit','risk','recon')
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
    ELSIF r.sch = 'ledger' THEN
      privs := 'SELECT, INSERT';
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
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_kyc'),
      ('audit.append_event(uuid,boolean,timestamptz,text,uuid,text,text,text,uuid,text,text,inet,text,text,jsonb)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance')
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
DROP FUNCTION IF EXISTS app.enqueue_outbox(uuid, text, uuid, text, jsonb, text, timestamptz);
DROP FUNCTION IF EXISTS audit.record_evidence(uuid, text, text, uuid, jsonb, timestamptz, text, uuid, text, text, bytea, bigint, text, text, text, uuid);
DROP FUNCTION IF EXISTS audit.append_event(uuid, boolean, timestamptz, text, uuid, text, text, text, uuid, text, text, inet, text, text, jsonb);
DROP FUNCTION IF EXISTS audit.gateway_payload_ok(jsonb);
DROP FUNCTION IF EXISTS audit.gateway_prefix_ok(text, text);
DROP TABLE IF EXISTS audit.evidence_holds, audit.evidence_records;
DROP FUNCTION IF EXISTS audit.evidence_holds_check_release(), audit.evidence_records_legal_hold_only(), audit.evidence_records_insert_guard();
-- restore runtime grants v2
CREATE OR REPLACE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly';
  -- fundzim_app may DELETE only these ephemeral rows (expired keys, pruned inbox/outbox, later sessions etc.)
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  -- reference data changed only by migrations
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
                                          'app.roles','app.role_permissions','app.organisation_roles',
                                          'ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines'];
  s text;
  r record;
  fq text;
  privs text;
BEGIN
  -- reset
  FOREACH s IN ARRAY schemas LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('REVOKE ALL ON SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL ROUTINES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
  END LOOP;

  -- fundzim_app: app, queue, ledger, audit, risk, recon. Never kyc; compliance only through approved views.
  FOREACH s IN ARRAY ARRAY['app','queue','ledger','audit','risk','recon'] LOOP
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
     WHERE n.nspname IN ('app','queue','ledger','audit','risk','recon')
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
    ELSIF r.sch IN ('audit', 'ledger') THEN
      privs := 'SELECT, INSERT';                                -- append-only history
    ELSE                                                        -- app, risk, recon
      privs := 'SELECT, INSERT';
      IF NOT r.no_update THEN privs := privs || ', UPDATE'; END IF;
      IF fq = ANY (app_delete_ok) AND NOT r.no_delete THEN privs := privs || ', DELETE'; END IF;
    END IF;
    EXECUTE format('GRANT %s ON %s TO fundzim_app', privs, r.rel);
  END LOOP;

  -- queue (River): routines the job library calls at runtime (Stage 4)
  IF to_regnamespace('queue') IS NOT NULL THEN
    EXECUTE 'GRANT EXECUTE ON ALL ROUTINES IN SCHEMA queue TO fundzim_app';
    FOR r IN SELECT t.oid::regtype AS typ FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
              WHERE n.nspname = 'queue' AND t.typtype IN ('e','d','c') AND t.typrelid = 0 LOOP
      EXECUTE format('GRANT USAGE ON TYPE %s TO fundzim_app', r.typ);
    END LOOP;
  END IF;

  -- restricted roles: only what shared triggers read; their own schemas get grants when their tables exist
  GRANT USAGE ON SCHEMA app TO fundzim_kyc, fundzim_compliance;
  GRANT SELECT ON app.status_transitions TO fundzim_kyc, fundzim_compliance;

  -- fundzim_worker inherits every fundzim_app privilege through role membership. Membership is
  -- infrastructure (like the roles themselves): the local init script and production provisioning grant it,
  -- because the migrator has no admin rights on roles. Fail closed if it is missing.
  IF NOT pg_has_role('fundzim_worker', 'fundzim_app', 'MEMBER') THEN
    RAISE EXCEPTION 'role fundzim_worker must be a member of fundzim_app (GRANT fundzim_app TO fundzim_worker, run by the role administrator)';
  END IF;

  -- routines: EXECUTE only on an explicit allow-list (missing routines are skipped)
  FOR r IN
    SELECT * FROM (VALUES
      ('audit.verify_chain(regclass)', 'fundzim_worker')
    ) AS t(fn, grantee)
  LOOP
    IF to_regprocedure(r.fn) IS NOT NULL THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.fn, r.grantee);
    END IF;
  END LOOP;

  -- readiness: the runtime roles may read the goose version table (never write it)
  IF to_regclass('public.goose_db_version') IS NOT NULL THEN
    GRANT USAGE ON SCHEMA public TO fundzim_app;
    GRANT SELECT ON public.goose_db_version TO fundzim_app;
  END IF;
END $$;

CALL app.apply_runtime_grants();
-- +goose StatementEnd
