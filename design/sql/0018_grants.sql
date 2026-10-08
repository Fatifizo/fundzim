-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate).
-- File 0018 (LAST): object ownership, privileges, default privileges and row-level security.
-- DATABASE §8, baseline §4, ADR-009, ADR-022. Explained in docs/security/data-protection-architecture.md §6
-- and docs/database/compliance-schema.md §6.
--
-- Written DYNAMICALLY: privileges are derived from the catalogue (pg_class / pg_trigger), so tables added
-- by any domain file are covered without editing this file:
--   * a table guarded by app.forbid_mutation() on UPDATE gets no UPDATE grant; on DELETE no DELETE grant;
--   * DELETE is granted to fundzim_app only on an explicit allow-list of ephemeral tables;
--   * TRUNCATE, REFERENCES, TRIGGER are never granted to a runtime role;
--   * fail closed: a new object gets no runtime privilege until app.apply_runtime_grants() is re-run
--     (Stage 3: every goose migration ends with CALL app.apply_runtime_grants(); a test diffs the result
--     against the expected matrix in data-protection-architecture.md §6).
-- In this draft all objects are created by the loading superuser, so step 1 hands them to
-- fundzim_migrator: no runtime role owns anything (an owner could ALTER/DISABLE TRIGGER, bypass RLS).
-- =====================================================================================================

-- -----------------------------------------------------------------------------------------------------
-- 0. Roles added by this file and write gateways for the restricted roles (lead decisions C-3, C-10).
-- -----------------------------------------------------------------------------------------------------
-- fundzim_worker: the worker pool. Holds every fundzim_app privilege (role membership, INHERIT) plus the
-- worker-only routines (ledger.fold_deferred_balances, audit.verify_chain). The API pool (fundzim_app)
-- cannot run them. NOLOGIN placeholder like 0001.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_worker') THEN
    CREATE ROLE fundzim_worker NOLOGIN;
  END IF;
END $$;

-- Gateways (lead decision C-3): fundzim_kyc and fundzim_compliance get NO privilege on audit.* or app.outbox_events; they call
-- these SECURITY DEFINER functions (owned by fundzim_migrator, pinned search_path, fully qualified names),
-- which validate input and append inside the caller's transaction (atomic with the domain write). The
-- callers therefore cannot read other modules' audit events or outbox rows.
CREATE FUNCTION audit.restricted_input_ok(p_action text, p_metadata jsonb) RETURNS boolean
LANGUAGE sql IMMUTABLE SET search_path = pg_catalog, pg_temp AS $$
  SELECT p_action ~ '^(kyc|kyb|beneficiary|organisation|compliance|sanctions|screening|case|str|evidence|risk|payout\.hold)\.[a-z_]+(\.[a-z_*]+)*$'
     AND jsonb_typeof(p_metadata) = 'object'
     AND pg_catalog.octet_length(p_metadata::text) <= 16384
     -- never-embed rule (AUDIT §6): reject keys that would carry C3/C4 values
     AND NOT EXISTS (SELECT 1 FROM pg_catalog.jsonb_object_keys(p_metadata) k
                      WHERE k ~* '(id_number|passport_number|date_of_birth|document_image|document_text|selfie|liveness|password|otp_code|token|secret|private_key|account_number|legal_name)'
                         OR k ~* '(^|_)(dob|pan|cvv|pin|address)(_|$)')
$$;

CREATE FUNCTION audit.append_event(
  p_id uuid, p_occurred_at timestamptz, p_actor_type text, p_actor_id uuid, p_actor_role text,
  p_action text, p_target_type text, p_target_id uuid, p_outcome text,
  p_request_id text, p_correlation_id text, p_reason text, p_justification text,
  p_metadata jsonb, p_break_glass boolean DEFAULT false)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF p_id IS NULL OR p_occurred_at IS NULL OR p_occurred_at > pg_catalog.now() + interval '5 minutes'
     OR NOT audit.restricted_input_ok(p_action, coalesce(p_metadata, '{}'::jsonb)) THEN
    RAISE EXCEPTION 'audit.append_event: rejected input (action %, metadata keys or size)', p_action
      USING ERRCODE = 'invalid_parameter_value';
  END IF;
  INSERT INTO audit.audit_events (id, occurred_at, actor_type, actor_id, actor_role, action, target_type, target_id,
                                  outcome, request_id, correlation_id, reason, justification, metadata, break_glass)
  VALUES (p_id, p_occurred_at, p_actor_type, p_actor_id, p_actor_role, p_action, p_target_type, p_target_id,
          p_outcome, p_request_id, p_correlation_id, p_reason, p_justification, coalesce(p_metadata, '{}'::jsonb),
          coalesce(p_break_glass, false));
  RETURN p_id;
END $$;

CREATE FUNCTION audit.append_security_event(
  p_id uuid, p_occurred_at timestamptz, p_actor_type text, p_actor_id uuid, p_actor_role text,
  p_action text, p_target_type text, p_target_id uuid, p_outcome text,
  p_request_id text, p_correlation_id text, p_reason text, p_justification text,
  p_metadata jsonb, p_break_glass boolean DEFAULT false)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF p_id IS NULL OR p_occurred_at IS NULL OR p_occurred_at > pg_catalog.now() + interval '5 minutes'
     OR NOT audit.restricted_input_ok(p_action, coalesce(p_metadata, '{}'::jsonb)) THEN
    RAISE EXCEPTION 'audit.append_security_event: rejected input (action %, metadata keys or size)', p_action
      USING ERRCODE = 'invalid_parameter_value';
  END IF;
  INSERT INTO audit.security_audit_events (id, occurred_at, actor_type, actor_id, actor_role, action, target_type, target_id,
                                           outcome, request_id, correlation_id, reason, justification, metadata, break_glass)
  VALUES (p_id, p_occurred_at, p_actor_type, p_actor_id, p_actor_role, p_action, p_target_type, p_target_id,
          p_outcome, p_request_id, p_correlation_id, p_reason, p_justification, coalesce(p_metadata, '{}'::jsonb),
          coalesce(p_break_glass, false));
  RETURN p_id;
END $$;

-- Evidence metadata for objects the caller stored (kyc: private-kyc; compliance: compliance prefix).
CREATE FUNCTION audit.record_evidence(
  p_id uuid, p_evidence_type text, p_subject_type text, p_subject_id uuid, p_related_refs jsonb,
  p_collected_at timestamptz, p_collected_by_type text, p_collected_by_id uuid, p_source text,
  p_storage_ref text, p_content_sha256 bytea, p_size_bytes bigint, p_media_type text,
  p_classification text, p_retention_class text, p_supersedes_id uuid, p_audit_event_id uuid)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF p_classification IS DISTINCT FROM 'C3' AND p_classification IS DISTINCT FROM 'C2' THEN
    RAISE EXCEPTION 'audit.record_evidence: classification must be C2 or C3' USING ERRCODE = 'invalid_parameter_value';
  END IF;
  IF p_storage_ref IS NOT NULL AND p_storage_ref !~ '^(kyc|compliance|reports)/' THEN
    RAISE EXCEPTION 'audit.record_evidence: storage_ref outside the private kyc/compliance/reports prefixes' USING ERRCODE = 'invalid_parameter_value';
  END IF;
  INSERT INTO audit.evidence_records (id, evidence_type, subject_type, subject_id, related_refs, collected_at, collected_by_type,
                                      collected_by_id, source, storage_ref, content_sha256, size_bytes, media_type,
                                      classification, retention_class, supersedes_id, audit_event_id)
  VALUES (p_id, p_evidence_type, p_subject_type, p_subject_id, coalesce(p_related_refs, '{}'::jsonb), p_collected_at,
          p_collected_by_type, p_collected_by_id, p_source, p_storage_ref, p_content_sha256, p_size_bytes, p_media_type,
          p_classification, p_retention_class, p_supersedes_id, p_audit_event_id);
  RETURN p_id;
END $$;

-- Legal hold place/release (maker-checker enforced by audit.evidence_holds CHECK) + legal_hold flag, atomically.
CREATE FUNCTION audit.set_evidence_hold(
  p_id uuid, p_evidence_record_id uuid, p_action text, p_released_hold_id uuid, p_reason text, p_case_id uuid,
  p_requested_by uuid, p_approved_by uuid, p_occurred_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  INSERT INTO audit.evidence_holds (id, evidence_record_id, action, released_hold_id, reason, case_id, requested_by, approved_by, occurred_at)
  VALUES (p_id, p_evidence_record_id, p_action, p_released_hold_id, p_reason, p_case_id, p_requested_by, p_approved_by, p_occurred_at);
  UPDATE audit.evidence_records e
     SET legal_hold = EXISTS (SELECT 1 FROM audit.evidence_holds h
                               WHERE h.evidence_record_id = e.id AND h.action = 'HOLD'
                                 AND NOT EXISTS (SELECT 1 FROM audit.evidence_holds r WHERE r.released_hold_id = h.id))
   WHERE e.id = p_evidence_record_id;
  RETURN p_id;
END $$;

CREATE FUNCTION app.enqueue_outbox(
  p_id uuid, p_aggregate_type text, p_aggregate_id uuid, p_event_type text, p_event_version smallint,
  p_payload jsonb, p_correlation_id text, p_occurred_at timestamptz)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF p_event_type !~ '^(kyc|kyb|beneficiary|organisation|compliance|screening|sanctions|case|risk)\.[a-z_]+(\.[a-z_]+)*$'
     OR jsonb_typeof(p_payload) IS DISTINCT FROM 'object' OR pg_catalog.octet_length(p_payload::text) > 65536
     OR NOT audit.restricted_input_ok('kyc.outbox', p_payload) THEN
    RAISE EXCEPTION 'app.enqueue_outbox: rejected event (type %, payload shape, size or sensitive keys)', p_event_type
      USING ERRCODE = 'invalid_parameter_value';
  END IF;
  INSERT INTO app.outbox_events (id, aggregate_type, aggregate_id, event_type, event_version, payload, correlation_id, occurred_at)
  VALUES (p_id, p_aggregate_type, p_aggregate_id, p_event_type, coalesce(p_event_version, 1::smallint), p_payload,
          p_correlation_id, p_occurred_at);
  RETURN p_id;
END $$;

-- -----------------------------------------------------------------------------------------------------
-- 1. Ownership: every schema, relation, routine and standalone type belongs to fundzim_migrator.
-- -----------------------------------------------------------------------------------------------------
DO $$
DECLARE
  r record;
  schemas constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
BEGIN
  FOR r IN SELECT nspname FROM pg_namespace WHERE nspname = ANY (schemas) LOOP
    EXECUTE format('ALTER SCHEMA %I OWNER TO fundzim_migrator', r.nspname);
  END LOOP;

  FOR r IN
    SELECT c.oid::regclass AS rel, c.relkind
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = ANY (schemas)
       AND c.relkind IN ('r','p','v','m','S','f')
       -- sequences owned by a column (serial/identity) follow their table
       AND NOT (c.relkind = 'S' AND EXISTS (SELECT 1 FROM pg_depend d
                 WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype IN ('a','i')))
  LOOP
    EXECUTE format('ALTER %s %s OWNER TO fundzim_migrator',
                   CASE r.relkind WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW'
                                  WHEN 'S' THEN 'SEQUENCE' WHEN 'f' THEN 'FOREIGN TABLE' ELSE 'TABLE' END,
                   r.rel);
  END LOOP;

  FOR r IN
    SELECT p.oid::regprocedure AS fn
      FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
     WHERE n.nspname = ANY (schemas) AND p.prokind IN ('f','p')
  LOOP
    EXECUTE format('ALTER ROUTINE %s OWNER TO fundzim_migrator', r.fn);
  END LOOP;

  FOR r IN
    SELECT t.oid::regtype AS typ
      FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
     WHERE n.nspname = ANY (schemas) AND t.typtype IN ('e','d','r','m')
  LOOP
    EXECUTE format('ALTER TYPE %s OWNER TO fundzim_migrator', r.typ);
  END LOOP;
END $$;

-- -----------------------------------------------------------------------------------------------------
-- 2. Integrity-check trigger functions that must see rows regardless of the caller's RLS visibility run
--    as their owner (fundzim_migrator, owner of the tables => not subject to RLS). search_path pinned.
-- -----------------------------------------------------------------------------------------------------
ALTER FUNCTION compliance.cases_require_event() SECURITY DEFINER SET search_path = pg_catalog, pg_temp;
ALTER FUNCTION compliance.str_reports_guard()   SECURITY DEFINER SET search_path = pg_catalog, pg_temp;

-- -----------------------------------------------------------------------------------------------------
-- 3. The grant procedure. Idempotent: revokes every runtime privilege in the FundZim schemas, then
--    re-derives the matrix from the catalogue. Owned by fundzim_migrator; not executable by anyone else.
-- -----------------------------------------------------------------------------------------------------
CREATE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly';
  -- fundzim_app may DELETE only these ephemeral rows (expired sessions/challenges/keys, pruned inbox/outbox)
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  -- reference data changed only by migrations
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.permissions','ledger.ledger_posting_rules',
                                          'ledger.ledger_posting_rule_lines'];
  -- non-ro_* views approved for the reporting role (aggregates, no C3, no personal contact data)
  readonly_approved constant text[] := ARRAY['ledger.v_trial_balance','ledger.v_pool_integrity'];
  -- ledger tables whose rows are never updated, whatever their triggers say (journal history, account identity)
  ledger_no_update constant text[] := ARRAY['ledger.ledger_accounts','ledger.ledger_transactions','ledger.ledger_entries',
                                            'ledger.ledger_posting_rules','ledger.ledger_posting_rule_lines'];
  s text;
  r record;
  fq text;
  privs text;
BEGIN
  -- 3.0 reset
  FOREACH s IN ARRAY schemas LOOP
    IF to_regnamespace(s) IS NULL THEN CONTINUE; END IF;
    EXECUTE format('REVOKE ALL ON SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL TABLES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL SEQUENCES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
    EXECUTE format('REVOKE ALL ON ALL ROUTINES IN SCHEMA %I FROM PUBLIC, %s', s, runtime);
  END LOOP;

  -- 3.1 fundzim_app: app, queue, ledger, audit, risk, recon (+ one compliance view). Never kyc.
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
      -- reporting views (ro_*) are for fundzim_readonly only; other views are app read models
      IF r.tbl NOT LIKE 'ro\_%' THEN
        EXECUTE format('GRANT SELECT ON %s TO fundzim_app', r.rel);
      END IF;
      CONTINUE;
    END IF;
    IF fq = ANY (reference_only) THEN
      privs := 'SELECT';
    ELSIF r.sch = 'queue' THEN
      privs := 'SELECT, INSERT, UPDATE, DELETE';                     -- River job tables
    ELSIF r.sch = 'audit' THEN
      privs := 'SELECT, INSERT';                                     -- append-only; legal_hold column below
    ELSIF r.sch = 'ledger' THEN
      privs := 'SELECT, INSERT';
      IF NOT r.no_update AND NOT (fq = ANY (ledger_no_update)) THEN
        -- adjustments/periods workflows; ledger_balances keeps UPDATE per baseline §4 but it is inert: the
        -- 0006 guard trigger rejects direct updates (projection moves only via SECURITY DEFINER ledger routines)
        privs := privs || ', UPDATE';
      END IF;
    ELSE                                                             -- app, risk, recon
      privs := 'SELECT, INSERT';
      IF NOT r.no_update THEN privs := privs || ', UPDATE'; END IF;
      IF fq = ANY (app_delete_ok) AND NOT r.no_delete THEN privs := privs || ', DELETE'; END IF;
    END IF;
    EXECUTE format('GRANT %s ON %s TO fundzim_app', privs, r.rel);
  END LOOP;

  IF to_regclass('audit.evidence_records') IS NOT NULL AND EXISTS (
       SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'audit' AND table_name = 'evidence_records' AND column_name = 'legal_hold') THEN
    GRANT UPDATE (legal_hold) ON audit.evidence_records TO fundzim_app;
  END IF;

  GRANT USAGE ON SCHEMA compliance TO fundzim_app;
  GRANT SELECT ON compliance.v_payout_blocking_cases TO fundzim_app;   -- EC-13 (subject, case id, flag)
  GRANT SELECT ON compliance.v_screening_status TO fundzim_app;         -- EC-15 (latest result, no hit details)
  GRANT SELECT ON compliance.v_active_restrictions TO fundzim_app;      -- capability checks (no reasons)

  -- 3.2 fundzim_kyc: the kyc schema, plus what a kyc transaction must write atomically
  --     (audit events, evidence metadata, outbox) and what shared triggers read (status_transitions).
  GRANT USAGE ON SCHEMA kyc TO fundzim_kyc;
  GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA kyc TO fundzim_kyc;
  FOR r IN
    SELECT format('%I.%I', n.nspname, c.relname) AS rel, c.relkind,
           coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
      LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                            AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
     WHERE n.nspname = 'kyc' AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
     GROUP BY n.nspname, c.relname, c.relkind
  LOOP
    IF r.relkind IN ('v','m') THEN
      EXECUTE format('GRANT SELECT ON %s TO fundzim_kyc', r.rel);
    ELSIF r.no_update THEN
      EXECUTE format('GRANT SELECT, INSERT ON %s TO fundzim_kyc', r.rel);
    ELSE
      EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO fundzim_kyc', r.rel);   -- never DELETE
    END IF;
  END LOOP;

  -- 3.3 fundzim_compliance: the compliance schema (RLS below), plus holds/limits/alerts in risk that a
  --     compliance decision changes in the same transaction, plus audit/evidence/outbox.
  GRANT USAGE ON SCHEMA compliance TO fundzim_compliance;
  GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA compliance TO fundzim_compliance;
  FOR r IN
    SELECT format('%I.%I', n.nspname, c.relname) AS rel, c.relkind,
           coalesce(bool_or((t.tgtype & 16) <> 0), false) AS no_update
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
      LEFT JOIN pg_trigger t ON t.tgrelid = c.oid AND NOT t.tgisinternal
                            AND t.tgfoid = 'app.forbid_mutation()'::regprocedure
     WHERE n.nspname = 'compliance' AND c.relkind IN ('r','p','v','m') AND NOT c.relispartition
     GROUP BY n.nspname, c.relname, c.relkind
  LOOP
    IF r.relkind IN ('v','m') THEN
      EXECUTE format('GRANT SELECT ON %s TO fundzim_compliance', r.rel);
    ELSIF r.no_update THEN
      EXECUTE format('GRANT SELECT, INSERT ON %s TO fundzim_compliance', r.rel);
    ELSE
      EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %s TO fundzim_compliance', r.rel);   -- never DELETE
    END IF;
  END LOOP;
  GRANT USAGE ON SCHEMA risk TO fundzim_compliance;
  -- C-5: risk module code executing on the compliance transaction. Minimal: insert/read, and UPDATE only of
  -- the set-once release / decision / closure columns (column grants; the risk triggers still apply).
  GRANT SELECT ON risk.limits TO fundzim_compliance;
  GRANT SELECT, INSERT ON risk.holds TO fundzim_compliance;
  GRANT UPDATE (released_at, released_by_type, released_by, released_by_job, release_approved_by, release_reason_code,
                release_reason_text, release_ledger_transaction_id, release_request_id, version) ON risk.holds TO fundzim_compliance;
  GRANT SELECT, INSERT ON risk.hold_events TO fundzim_compliance;
  GRANT SELECT, INSERT ON risk.hold_release_requests TO fundzim_compliance;
  GRANT UPDATE (status, decided_by, decided_at, decision_note, checker_step_up_at, version)
    ON risk.hold_release_requests TO fundzim_compliance;
  GRANT SELECT ON risk.monitoring_alerts TO fundzim_compliance;
  GRANT UPDATE (status, disposition, disposition_reason, closed_by, second_reviewer_id, closed_at, case_id)
    ON risk.monitoring_alerts TO fundzim_compliance;

  -- 3.4 shared needs of the two restricted roles: only what shared triggers read. Audit, evidence and
  --     outbox writes go through the SECURITY DEFINER gateways (section 0); no direct audit/outbox privilege.
  GRANT USAGE ON SCHEMA app TO fundzim_kyc, fundzim_compliance;
  GRANT SELECT ON app.status_transitions TO fundzim_kyc, fundzim_compliance;   -- read by app.guard_transition (invoker)
  GRANT USAGE ON SCHEMA audit TO fundzim_kyc, fundzim_compliance;              -- to resolve the gateway functions

  -- 3.4b fundzim_worker inherits every fundzim_app privilege (membership); worker-only routines below.
  GRANT fundzim_app TO fundzim_worker;

  -- 3.5 routines: EXECUTE only on an explicit allow-list (reset above revoked everything, PUBLIC included).
  --     PostgreSQL checks EXECUTE when a CHECK constraint, default or query calls a routine, so the
  --     functions used in CHECK constraints of tables a role writes must be listed. Trigger functions need no
  --     EXECUTE grant to fire. Never listed: ledger.rebuild_balance (break-glass only, granted to a named
  --     break-glass login during an incident), app.apply_runtime_grants (migrator only).
  --     A routine missing from the database is skipped (allow-list may name routines of later files).
  FOR r IN
    SELECT * FROM (VALUES
      ('app.payment_status_rank(text)',                 'fundzim_app'),   -- CHECK on app.payment_intents
      ('app.payout_status_rank(text)',                  'fundzim_app'),   -- CHECK on app.payout_requests
      ('app.payout_policy_rank(text)',                  'fundzim_app'),   -- payout approval-policy guard
      ('app.eligibility_results_valid(jsonb,text,text)','fundzim_app'),   -- CHECK on payout_eligibility_decisions
      ('ledger.account_class_signature(text)',          'fundzim_app'),   -- CHECK on ledger accounts / rule lines
      ('ledger.fold_deferred_balances()',               'fundzim_worker'),-- worker-only job (C-10)
      ('audit.verify_chain(regclass)',                  'fundzim_worker'),-- scheduled chain verification job
      ('audit.append_event(uuid,timestamptz,text,uuid,text,text,text,uuid,text,text,text,text,text,jsonb,boolean)', 'fundzim_kyc'),
      ('audit.append_event(uuid,timestamptz,text,uuid,text,text,text,uuid,text,text,text,text,text,jsonb,boolean)', 'fundzim_compliance'),
      ('audit.append_security_event(uuid,timestamptz,text,uuid,text,text,text,uuid,text,text,text,text,text,jsonb,boolean)', 'fundzim_kyc'),
      ('audit.append_security_event(uuid,timestamptz,text,uuid,text,text,text,uuid,text,text,text,text,text,jsonb,boolean)', 'fundzim_compliance'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid,uuid)', 'fundzim_kyc'),
      ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamptz,text,uuid,text,text,bytea,bigint,text,text,text,uuid,uuid)', 'fundzim_compliance'),
      ('audit.set_evidence_hold(uuid,uuid,text,uuid,text,uuid,uuid,uuid,timestamptz)', 'fundzim_compliance'),
      ('app.enqueue_outbox(uuid,text,uuid,text,smallint,jsonb,text,timestamptz)', 'fundzim_kyc'),
      ('app.enqueue_outbox(uuid,text,uuid,text,smallint,jsonb,text,timestamptz)', 'fundzim_compliance')
    ) AS t(fn, grantee)
  LOOP
    IF to_regprocedure(r.fn) IS NOT NULL THEN
      EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO %I', r.fn, r.grantee);
    END IF;
  END LOOP;

  -- 3.6 fundzim_readonly: SELECT on approved reporting views (ro_* + readonly_approved) only; never kyc or compliance.
  IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
              WHERE n.nspname IN ('kyc','compliance') AND c.relname LIKE 'ro\_%') THEN
    RAISE EXCEPTION 'reporting views (ro_*) are not allowed in kyc or compliance';
  END IF;
  FOR r IN
    SELECT n.nspname AS sch, format('%I.%I', n.nspname, c.relname) AS rel
      FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname IN ('app','ledger','audit','risk','recon') AND c.relkind IN ('v','m')
       AND (c.relname LIKE 'ro\_%' OR format('%s.%s', n.nspname, c.relname) = ANY (readonly_approved))
  LOOP
    EXECUTE format('GRANT USAGE ON SCHEMA %I TO fundzim_readonly', r.sch);
    EXECUTE format('GRANT SELECT ON %s TO fundzim_readonly', r.rel);
  END LOOP;
END $$;
REVOKE ALL ON PROCEDURE app.apply_runtime_grants() FROM PUBLIC;
ALTER PROCEDURE app.apply_runtime_grants() OWNER TO fundzim_migrator;

CALL app.apply_runtime_grants();

-- -----------------------------------------------------------------------------------------------------
-- 4. Default privileges for objects fundzim_migrator creates later. Deliberately minimal (fail closed):
--    no runtime role receives table privileges by default except the River queue schema, whose tables are
--    created by the queue library's own migrations. Routines are never executable by PUBLIC by default.
-- -----------------------------------------------------------------------------------------------------
ALTER DEFAULT PRIVILEGES FOR ROLE fundzim_migrator REVOKE EXECUTE ON ROUTINES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE fundzim_migrator IN SCHEMA queue
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO fundzim_app;
ALTER DEFAULT PRIVILEGES FOR ROLE fundzim_migrator IN SCHEMA queue
  GRANT USAGE, SELECT ON SEQUENCES TO fundzim_app;

-- -----------------------------------------------------------------------------------------------------
-- 5. Row-level security for STR-restricted compliance data (ADR-022).
--    The compliance module sets the flag only inside a transaction, after checking str.prepare/str.approve:
--        SET LOCAL fundzim.str_access = 'on';
--    Limits (data-protection-architecture.md §6.4): any code holding a fundzim_compliance connection can set
--    the flag, so this protects against accidental exposure (list views, forgotten filters, ad hoc queries
--    through the compliance pool), not against a compromised compliance module. The table owner
--    (fundzim_migrator) is not subject to RLS (not FORCEd) so that v_payout_blocking_cases still sees
--    restricted cases: an STR subject's payouts stay blocked without the case being visible.
-- -----------------------------------------------------------------------------------------------------
ALTER TABLE compliance.compliance_cases ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_compliance_cases_read ON compliance.compliance_cases FOR SELECT TO fundzim_compliance
  USING (confidentiality = 'NORMAL' OR current_setting('fundzim.str_access', true) = 'on');
CREATE POLICY p_compliance_cases_insert ON compliance.compliance_cases FOR INSERT TO fundzim_compliance
  WITH CHECK (true);                                       -- anyone in compliance may raise an internal suspicion
CREATE POLICY p_compliance_cases_update ON compliance.compliance_cases FOR UPDATE TO fundzim_compliance
  USING (confidentiality = 'NORMAL' OR current_setting('fundzim.str_access', true) = 'on')
  WITH CHECK (true);                                       -- raising to RESTRICTED_STR is allowed; lowering is blocked by trigger

ALTER TABLE compliance.compliance_case_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_compliance_case_events_read ON compliance.compliance_case_events FOR SELECT TO fundzim_compliance
  USING (EXISTS (SELECT 1 FROM compliance.compliance_cases c WHERE c.id = case_id));   -- inherits case visibility
CREATE POLICY p_compliance_case_events_insert ON compliance.compliance_case_events FOR INSERT TO fundzim_compliance
  WITH CHECK (true);

ALTER TABLE compliance.compliance_case_links ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_compliance_case_links_read ON compliance.compliance_case_links FOR SELECT TO fundzim_compliance
  USING (EXISTS (SELECT 1 FROM compliance.compliance_cases c WHERE c.id = case_id));
CREATE POLICY p_compliance_case_links_insert ON compliance.compliance_case_links FOR INSERT TO fundzim_compliance
  WITH CHECK (true);

ALTER TABLE compliance.str_reports ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_str_reports_all ON compliance.str_reports FOR ALL TO fundzim_compliance
  USING (current_setting('fundzim.str_access', true) = 'on')
  WITH CHECK (current_setting('fundzim.str_access', true) = 'on');
