-- Privilege, ownership and row-level-security tests for 0018_grants.sql (DESIGN DRAFT; PGlite only).
-- Fixtures are inserted as the loading superuser; each assertion switches role with SET LOCAL ROLE so the
-- privilege check is exactly the one the runtime role would get. Permission is checked before triggers,
-- so "WHERE false" statements prove a privilege exists (ok) or is missing (permission denied).
-- Synthetic data only.

-- @case setup_fixtures expect=ok
INSERT INTO app.users (id, account_kind) VALUES
  ('00000000-0000-0000-0000-0000000000b1', 'STAFF'), ('00000000-0000-0000-0000-0000000000b2', 'STAFF'),
  ('00000000-0000-0000-0000-0000000000b4', 'USER'),  ('00000000-0000-0000-0000-0000000000b5', 'USER');
-- one NORMAL and one RESTRICTED_STR case, both blocking payouts
INSERT INTO compliance.compliance_cases (id, case_number, case_type, severity, status, source, opened_by_type, opened_by,
                                         team, sla_due_at, sla_policy_ref, blocks_payouts, confidentiality, suspicion_formed_at)
VALUES ('00000000-0000-0000-0000-000000000d01', 'CMP-2026-000101', 'AML_MONITORING', 'S2', 'OPEN', 'STAFF', 'STAFF',
        '00000000-0000-0000-0000-0000000000b1', 'COMPLIANCE', now() + interval '3 days', 'PD-28-draft', true, 'NORMAL', NULL),
       ('00000000-0000-0000-0000-000000000d02', 'CMP-2026-000102', 'AML_MONITORING', 'S1', 'OPEN', 'STAFF', 'STAFF',
        '00000000-0000-0000-0000-0000000000b1', 'COMPLIANCE', now() + interval '1 day', 'PD-28-draft', true, 'RESTRICTED_STR', TIMESTAMPTZ '2026-10-01 00:00:00+00');
INSERT INTO compliance.compliance_case_events (id, case_id, case_version, event_type, to_status, actor_type, actor_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000d01', 1, 'OPENED', 'OPEN', 'STAFF', '00000000-0000-0000-0000-0000000000b1'),
       (gen_random_uuid(), '00000000-0000-0000-0000-000000000d02', 1, 'OPENED', 'OPEN', 'STAFF', '00000000-0000-0000-0000-0000000000b1');
INSERT INTO compliance.compliance_case_links (id, case_id, subject_type, subject_id, role, linked_by_type, linked_by)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000d01', 'USER', '00000000-0000-0000-0000-0000000000b5', 'PRIMARY_SUBJECT', 'STAFF', '00000000-0000-0000-0000-0000000000b1'),
       (gen_random_uuid(), '00000000-0000-0000-0000-000000000d02', 'USER', '00000000-0000-0000-0000-0000000000b4', 'PRIMARY_SUBJECT', 'STAFF', '00000000-0000-0000-0000-0000000000b1');

-- =========================== ownership ==============================================================
-- @case no_object_owned_by_runtime_or_superuser expect=ok
DO $$
DECLARE bad text;
BEGIN
  SELECT string_agg(n.nspname || '.' || c.relname || ':' || pg_get_userbyid(c.relowner), ', ') INTO bad
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
   WHERE n.nspname IN ('app','queue','ledger','audit','kyc','risk','compliance','recon')
     AND c.relkind IN ('r','p','v','m','S') AND pg_get_userbyid(c.relowner) <> 'fundzim_migrator';
  IF bad IS NOT NULL THEN RAISE EXCEPTION 'objects not owned by fundzim_migrator: %', bad; END IF;
  SELECT string_agg(nspname, ', ') INTO bad FROM pg_namespace
   WHERE nspname IN ('app','queue','ledger','audit','kyc','risk','compliance','recon') AND pg_get_userbyid(nspowner) <> 'fundzim_migrator';
  IF bad IS NOT NULL THEN RAISE EXCEPTION 'schemas not owned by fundzim_migrator: %', bad; END IF;
END $$;

-- @case no_truncate_or_trigger_privilege_for_runtime_roles expect=ok
DO $$
DECLARE bad text;
BEGIN
  SELECT string_agg(table_schema || '.' || table_name || ':' || grantee || ':' || privilege_type, ', ') INTO bad
    FROM information_schema.table_privileges
   WHERE grantee IN ('fundzim_app','fundzim_kyc','fundzim_compliance','fundzim_readonly')
     AND privilege_type IN ('TRUNCATE','TRIGGER','REFERENCES');
  IF bad IS NOT NULL THEN RAISE EXCEPTION 'forbidden privileges: %', bad; END IF;
END $$;

-- @case readonly_has_no_privilege_in_kyc_or_compliance expect=ok
DO $$
BEGIN
  IF has_schema_privilege('fundzim_readonly', 'kyc', 'USAGE') OR has_schema_privilege('fundzim_readonly', 'compliance', 'USAGE')
     OR has_schema_privilege('fundzim_app', 'kyc', 'USAGE') THEN
    RAISE EXCEPTION 'kyc/compliance schema reachable by a role that must not reach it';
  END IF;
END $$;

-- =========================== fundzim_app ============================================================
-- @case app_cannot_read_kyc_identities expect=error:permission denied for schema kyc
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM kyc.identities;

-- @case app_cannot_read_kyc_documents expect=error:permission denied for schema kyc
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM kyc.kyc_documents;

-- @case app_cannot_update_ledger_entries expect=error:permission denied for table ledger_entries
SET LOCAL ROLE fundzim_app;
UPDATE ledger.ledger_entries SET id = id WHERE false;

-- @case app_cannot_delete_ledger_entries expect=error:permission denied for table ledger_entries
SET LOCAL ROLE fundzim_app;
DELETE FROM ledger.ledger_entries WHERE false;

-- @case app_cannot_truncate_ledger_entries expect=error:permission denied for table ledger_entries
SET LOCAL ROLE fundzim_app;
TRUNCATE ledger.ledger_entries;

-- @case app_cannot_update_ledger_transactions expect=error:permission denied for table ledger_transactions
SET LOCAL ROLE fundzim_app;
UPDATE ledger.ledger_transactions SET id = id WHERE false;

-- @case app_can_read_and_insert_ledger_entries_privilege expect=ok
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM ledger.ledger_entries;
DO $$ BEGIN
  IF NOT has_table_privilege('ledger.ledger_entries', 'INSERT') THEN RAISE EXCEPTION 'app lacks INSERT on ledger_entries'; END IF;
END $$;

-- @case app_can_update_ledger_balances expect=ok
SET LOCAL ROLE fundzim_app;
UPDATE ledger.ledger_balances SET version = version WHERE false;

-- @case app_cannot_update_audit_events expect=error:permission denied for table audit_events
SET LOCAL ROLE fundzim_app;
UPDATE audit.audit_events SET id = id WHERE false;

-- @case app_cannot_delete_audit_events expect=error:permission denied for table audit_events
SET LOCAL ROLE fundzim_app;
DELETE FROM audit.audit_events WHERE false;

-- @case app_has_insert_select_on_audit_events expect=ok
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM audit.audit_events;
DO $$ BEGIN
  IF NOT has_table_privilege('audit.audit_events', 'INSERT') THEN RAISE EXCEPTION 'app lacks INSERT on audit_events'; END IF;
END $$;

-- @case app_can_update_evidence_legal_hold_only expect=ok
SET LOCAL ROLE fundzim_app;
UPDATE audit.evidence_records SET legal_hold = legal_hold WHERE false;

-- @case app_cannot_update_other_evidence_columns expect=error:permission denied for table evidence_records
SET LOCAL ROLE fundzim_app;
UPDATE audit.evidence_records SET id = id WHERE false;

-- @case app_cannot_read_compliance_cases expect=error:permission denied for table compliance_cases
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM compliance.compliance_cases;

-- @case app_cannot_read_compliance_case_links expect=error:permission denied for table compliance_case_links
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM compliance.compliance_case_links;

-- @case app_view_shows_blocking_subjects_including_restricted expect=ok
SET LOCAL ROLE fundzim_app;
DO $$ BEGIN
  IF (SELECT count(*) FROM compliance.v_payout_blocking_cases
       WHERE subject_type = 'USER' AND subject_id IN ('00000000-0000-0000-0000-0000000000b4','00000000-0000-0000-0000-0000000000b5')) <> 2 THEN
    RAISE EXCEPTION 'EC-13 view must list both blocked subjects (incl. the STR-restricted one)';
  END IF;
END $$;

-- @case app_view_exposes_no_case_details expect=ok
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'compliance' AND table_name = 'v_payout_blocking_cases'
              AND column_name NOT IN ('subject_type','subject_id','case_id','blocks_payouts')) THEN
    RAISE EXCEPTION 'v_payout_blocking_cases exposes more than subject + case id + flag';
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'compliance' AND table_name = 'v_screening_status'
              AND column_name NOT IN ('subject_type','subject_id','latest_result','screened_at','valid_until','list_version','passes')) THEN
    RAISE EXCEPTION 'v_screening_status exposes hit details';
  END IF;
END $$;

-- @case app_can_read_screening_status expect=ok
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM compliance.v_screening_status;

-- @case app_can_read_active_restrictions_without_reasons expect=ok
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM compliance.v_active_restrictions;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'compliance' AND table_name = 'v_active_restrictions'
              AND column_name NOT IN ('subject_type','subject_id','capability','expires_at')) THEN
    RAISE EXCEPTION 'v_active_restrictions exposes more than subject/capability/expiry';
  END IF;
END $$;

-- @case app_cannot_read_restrictions_table expect=error:permission denied for table compliance_restrictions
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM compliance.compliance_restrictions;

-- @case app_cannot_read_screening_hits expect=error:permission denied for table screening_hits
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM compliance.screening_hits;

-- @case app_cannot_read_screening_callback_inbox expect=error:permission denied for table screening_callback_inbox
SET LOCAL ROLE fundzim_app;
SELECT count(*) FROM compliance.screening_callback_inbox;

-- @case app_can_update_holds expect=ok
SET LOCAL ROLE fundzim_app;
UPDATE risk.holds SET version = version WHERE false;

-- @case app_cannot_delete_holds expect=error:permission denied for table holds
SET LOCAL ROLE fundzim_app;
DELETE FROM risk.holds WHERE false;

-- @case app_cannot_update_append_only_risk_signals expect=error:permission denied for table risk_signals
SET LOCAL ROLE fundzim_app;
UPDATE risk.risk_signals SET facts = facts WHERE false;

-- @case app_cannot_update_hold_events expect=error:permission denied for table hold_events
SET LOCAL ROLE fundzim_app;
UPDATE risk.hold_events SET reason_code = reason_code WHERE false;

-- @case app_cannot_change_transition_registry expect=error:permission denied for table status_transitions
SET LOCAL ROLE fundzim_app;
INSERT INTO app.status_transitions VALUES ('compliance_case', 'CLOSED', 'OPEN');

-- @case app_cannot_execute_rebuild_balance expect=error:permission denied for function rebuild_balance
SET LOCAL ROLE fundzim_app;
SELECT ledger.rebuild_balance(gen_random_uuid());

-- @case routine_execute_is_allow_listed expect=ok
DO $$
DECLARE bad text;
BEGIN
  WITH expected(fn, rolname) AS (VALUES
    ('app.payment_status_rank(text)','fundzim_app'), ('app.payout_status_rank(text)','fundzim_app'),
    ('app.payout_policy_rank(text)','fundzim_app'), ('app.eligibility_results_valid(jsonb,text,text)','fundzim_app'),
    ('ledger.account_class_signature(text)','fundzim_app'),
    ('app.payment_status_rank(text)','fundzim_worker'), ('app.payout_status_rank(text)','fundzim_worker'),
    ('app.payout_policy_rank(text)','fundzim_worker'), ('app.eligibility_results_valid(jsonb,text,text)','fundzim_worker'),
    ('ledger.account_class_signature(text)','fundzim_worker'),
    ('ledger.fold_deferred_balances()','fundzim_worker'), ('audit.verify_chain(regclass)','fundzim_worker'),
    ('audit.append_event(uuid,timestamp with time zone,text,uuid,text,text,text,uuid,text,text,text,text,text,jsonb,boolean)','fundzim_kyc'),
    ('audit.append_event(uuid,timestamp with time zone,text,uuid,text,text,text,uuid,text,text,text,text,text,jsonb,boolean)','fundzim_compliance'),
    ('audit.append_security_event(uuid,timestamp with time zone,text,uuid,text,text,text,uuid,text,text,text,text,text,jsonb,boolean)','fundzim_kyc'),
    ('audit.append_security_event(uuid,timestamp with time zone,text,uuid,text,text,text,uuid,text,text,text,text,text,jsonb,boolean)','fundzim_compliance'),
    ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamp with time zone,text,uuid,text,text,bytea,bigint,text,text,text,uuid,uuid)','fundzim_kyc'),
    ('audit.record_evidence(uuid,text,text,uuid,jsonb,timestamp with time zone,text,uuid,text,text,bytea,bigint,text,text,text,uuid,uuid)','fundzim_compliance'),
    ('audit.set_evidence_hold(uuid,uuid,text,uuid,text,uuid,uuid,uuid,timestamp with time zone)','fundzim_compliance'),
    ('app.enqueue_outbox(uuid,text,uuid,text,smallint,jsonb,text,timestamp with time zone)','fundzim_kyc'),
    ('app.enqueue_outbox(uuid,text,uuid,text,smallint,jsonb,text,timestamp with time zone)','fundzim_compliance'))
  SELECT string_agg(p.oid::regprocedure::text || ':' || r.rolname, ', ') INTO bad
    FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
    CROSS JOIN (VALUES ('fundzim_app'),('fundzim_worker'),('fundzim_kyc'),('fundzim_compliance'),('fundzim_readonly')) AS r(rolname)
   WHERE n.nspname IN ('app','queue','ledger','audit','kyc','risk','compliance','recon')
     AND p.prorettype <> 'trigger'::regtype
     AND has_function_privilege(r.rolname, p.oid, 'EXECUTE')
     AND NOT EXISTS (SELECT 1 FROM expected e WHERE e.fn = p.oid::regprocedure::text AND e.rolname = r.rolname);
  IF bad IS NOT NULL THEN RAISE EXCEPTION 'routines executable outside the allow-list: %', bad; END IF;
  IF EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
              WHERE n.nspname IN ('app','queue','ledger','audit','kyc','risk','compliance','recon')
                AND p.proacl IS NULL) THEN
    RAISE EXCEPTION 'a routine still has default (PUBLIC EXECUTE) privileges';
  END IF;
END $$;

-- @case app_cannot_execute_worker_routine expect=error:permission denied for function fold_deferred_balances
SET LOCAL ROLE fundzim_app;
SELECT ledger.fold_deferred_balances();

-- @case worker_inherits_app_and_runs_worker_routines expect=ok
SET LOCAL ROLE fundzim_worker;
SELECT ledger.fold_deferred_balances();
SELECT count(*) FROM ledger.ledger_entries;
DO $$ BEGIN
  IF has_schema_privilege('fundzim_worker', 'kyc', 'USAGE') THEN RAISE EXCEPTION 'worker reaches kyc'; END IF;
END $$;

-- @case kyc_cannot_select_audit_events expect=error:permission denied for table audit_events
SET LOCAL ROLE fundzim_kyc;
SELECT count(*) FROM audit.audit_events;

-- @case kyc_cannot_insert_outbox_directly expect=error:permission denied for table outbox_events
SET LOCAL ROLE fundzim_kyc;
INSERT INTO app.outbox_events (id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
VALUES (gen_random_uuid(), 'kyc_profile', gen_random_uuid(), 'kyc.level.granted', '{}', now());

-- @case kyc_gateways_append_atomically expect=ok
SET LOCAL ROLE fundzim_kyc;
SELECT audit.append_event('00000000-0000-0000-0000-000000000f11', now(), 'staff', '00000000-0000-0000-0000-0000000000b1', 'KYC_REVIEWER',
       'kyc.document.viewed', 'evidence_record', gen_random_uuid(), 'success', 'req-1', 'corr-1', 'CASE_REVIEW', 'synthetic justification',
       '{"case_id": "00000000-0000-0000-0000-000000000d01"}');
SELECT audit.append_security_event(gen_random_uuid(), now(), 'staff', '00000000-0000-0000-0000-0000000000b1', 'KYC_REVIEWER',
       'kyc.document.viewed', 'evidence_record', gen_random_uuid(), 'success', 'req-1', 'corr-1', 'CASE_REVIEW', 'synthetic justification', '{}');
SELECT audit.record_evidence(gen_random_uuid(), 'ID_DOCUMENT', 'user', '00000000-0000-0000-0000-0000000000b4', '{}', now(), 'user',
       '00000000-0000-0000-0000-0000000000b4', 'upload', 'kyc/0f6f3b4e-5d0c-4a2e-9b1f-2a3b4c5d6e7f', sha256('synthetic'::bytea), 9,
       'image/jpeg', 'C3', 'KYC', NULL, '00000000-0000-0000-0000-000000000f11');
SELECT app.enqueue_outbox(gen_random_uuid(), 'kyc_profile', gen_random_uuid(), 'kyc.level.granted', 1::smallint,
       '{"level": "IDENTITY_VERIFIED"}', 'corr-1', now());

-- @case gateway_rejects_sensitive_metadata expect=error:audit.append_event: rejected input
SET LOCAL ROLE fundzim_kyc;
SELECT audit.append_event(gen_random_uuid(), now(), 'staff', '00000000-0000-0000-0000-0000000000b1', 'KYC_REVIEWER',
       'kyc.identity_number.revealed', 'identity', gen_random_uuid(), 'success', NULL, NULL, NULL, 'x', '{"id_number": "63-0000000X00"}');

-- @case gateway_rejects_foreign_action expect=error:audit.append_event: rejected input
SET LOCAL ROLE fundzim_kyc;
SELECT audit.append_event(gen_random_uuid(), now(), 'system', NULL, NULL, 'ledger.transaction.posted', 'ledger_transaction',
       gen_random_uuid(), 'success', NULL, NULL, NULL, NULL, '{}');

-- @case gateway_rejects_public_storage_ref expect=error:storage_ref outside the private
SET LOCAL ROLE fundzim_compliance;
SELECT audit.record_evidence(gen_random_uuid(), 'REVIEW_NOTE', 'user', '00000000-0000-0000-0000-0000000000b4', '{}', now(), 'system',
       NULL, 'staff_note', 'objects/0f6f3b4e-5d0c-4a2e-9b1f-2a3b4c5d6e7f', sha256('x'::bytea), 1, 'application/pdf', 'C3', 'CASE', NULL,
       '00000000-0000-0000-0000-000000000f11');

-- @case compliance_cannot_rewrite_hold_scope expect=error:permission denied for table holds
SET LOCAL ROLE fundzim_compliance;
UPDATE risk.holds SET reason_code = reason_code WHERE false;

-- @case compliance_can_record_hold_release_columns expect=ok
SET LOCAL ROLE fundzim_compliance;
UPDATE risk.holds SET released_at = released_at, version = version WHERE false;

-- @case app_cannot_run_grant_procedure expect=error:permission denied for procedure apply_runtime_grants
SET LOCAL ROLE fundzim_app;
CALL app.apply_runtime_grants();

-- =========================== fundzim_kyc ============================================================
-- @case kyc_cannot_read_ledger expect=error:permission denied for schema ledger
SET LOCAL ROLE fundzim_kyc;
SELECT count(*) FROM ledger.ledger_entries;

-- @case kyc_cannot_read_compliance expect=error:permission denied for schema compliance
SET LOCAL ROLE fundzim_kyc;
SELECT count(*) FROM compliance.compliance_cases;

-- @case kyc_cannot_read_risk expect=error:permission denied for schema risk
SET LOCAL ROLE fundzim_kyc;
SELECT count(*) FROM risk.holds;

-- @case kyc_cannot_read_app_users expect=error:permission denied for table users
SET LOCAL ROLE fundzim_kyc;
SELECT count(*) FROM app.users;

-- @case kyc_writes_profile_with_fk_and_guard expect=ok
SET LOCAL ROLE fundzim_kyc;
INSERT INTO kyc.verification_profiles (id, user_id, policy_version)
VALUES ('00000000-0000-0000-0000-000000000e01', '00000000-0000-0000-0000-0000000000b4', 'gate-v1');
INSERT INTO kyc.profile_events (id, profile_id, aggregate_version, to_level, to_status, reason_code, actor_type, actor_job)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000e01', 1, 'UNVERIFIED', 'ACTIVE', 'PROFILE_CREATED', 'SYSTEM', 'kyc.onboard');

-- @case kyc_cannot_update_kyc_checks expect=error:permission denied for table kyc_checks
SET LOCAL ROLE fundzim_kyc;
UPDATE kyc.kyc_checks SET result = result WHERE false;

-- @case kyc_cannot_delete_profiles expect=error:permission denied for table verification_profiles
SET LOCAL ROLE fundzim_kyc;
DELETE FROM kyc.verification_profiles WHERE false;

-- @case kyc_cannot_alter_its_tables expect=error:must be owner of table identities
SET LOCAL ROLE fundzim_kyc;
ALTER TABLE kyc.identities DISABLE TRIGGER trg_identities_guard;

-- =========================== fundzim_readonly =======================================================
-- @case readonly_cannot_read_kyc expect=error:permission denied for schema kyc
SET LOCAL ROLE fundzim_readonly;
SELECT count(*) FROM kyc.identities;

-- @case readonly_cannot_read_compliance expect=error:permission denied for schema compliance
SET LOCAL ROLE fundzim_readonly;
SELECT count(*) FROM compliance.compliance_cases;

-- @case readonly_cannot_read_base_tables expect=error:permission denied for table holds
SET LOCAL ROLE fundzim_readonly;
SELECT count(*) FROM risk.holds;

-- @case readonly_can_read_reporting_view expect=ok
SET LOCAL ROLE fundzim_readonly;
SELECT * FROM risk.ro_active_hold_counts;
SELECT count(*) FROM ledger.v_trial_balance;

-- @case readonly_cannot_read_ledger_entries expect=error:permission denied for table ledger_entries
SET LOCAL ROLE fundzim_readonly;
SELECT count(*) FROM ledger.ledger_entries;

-- @case readonly_cannot_read_users expect=error:permission denied for schema app
SET LOCAL ROLE fundzim_readonly;
SELECT count(*) FROM app.users;

-- =========================== fundzim_compliance + RLS ===============================================
-- @case compliance_restricted_rows_invisible_without_flag expect=ok
SET LOCAL ROLE fundzim_compliance;
DO $$ BEGIN
  IF (SELECT count(*) FROM compliance.compliance_cases) <> 1 THEN
    RAISE EXCEPTION 'RESTRICTED_STR case visible without fundzim.str_access';
  END IF;
  IF (SELECT count(*) FROM compliance.compliance_case_events) <> 1 OR (SELECT count(*) FROM compliance.compliance_case_links) <> 1 THEN
    RAISE EXCEPTION 'events/links of a RESTRICTED_STR case visible without fundzim.str_access';
  END IF;
END $$;

-- @case compliance_restricted_rows_visible_with_flag expect=ok
SET LOCAL ROLE fundzim_compliance;
SET LOCAL fundzim.str_access = 'on';
DO $$ BEGIN
  IF (SELECT count(*) FROM compliance.compliance_cases) <> 2 THEN
    RAISE EXCEPTION 'RESTRICTED_STR case not visible with fundzim.str_access';
  END IF;
END $$;

-- @case compliance_flag_is_transaction_scoped expect=ok
SET LOCAL ROLE fundzim_compliance;
DO $$ BEGIN
  IF (SELECT count(*) FROM compliance.compliance_cases) <> 1 THEN
    RAISE EXCEPTION 'SET LOCAL flag leaked into the next transaction';
  END IF;
END $$;

-- @case compliance_cannot_update_restricted_case_without_flag expect=ok
SET LOCAL ROLE fundzim_compliance;
DO $$ DECLARE n integer; BEGIN
  UPDATE compliance.compliance_cases SET severity = 'S2', version = version + 1 WHERE id = '00000000-0000-0000-0000-000000000d02';
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n <> 0 THEN RAISE EXCEPTION 'restricted case updated without fundzim.str_access'; END IF;
END $$;

-- @case compliance_str_report_insert_without_flag_rejected expect=error:new row violates row-level security policy
SET LOCAL ROLE fundzim_compliance;
INSERT INTO compliance.str_reports (id, case_id, status, suspicion_formed_at, deadline_at, deadline_limit_id, prepared_by)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000d02', 'DRAFT', TIMESTAMPTZ '2026-10-01 00:00:00+00', TIMESTAMPTZ '2026-10-04 00:00:00+00',
        gen_random_uuid(), '00000000-0000-0000-0000-0000000000b1');

-- @case compliance_can_raise_internal_suspicion_case expect=ok
SET LOCAL ROLE fundzim_compliance;
INSERT INTO compliance.compliance_cases (id, case_number, case_type, severity, status, source, opened_by_type, opened_by,
                                         team, sla_due_at, sla_policy_ref, blocks_payouts, confidentiality, suspicion_formed_at)
VALUES ('00000000-0000-0000-0000-000000000d03', 'CMP-2026-000103', 'AML_MONITORING', 'S1', 'OPEN', 'STAFF', 'STAFF',
        '00000000-0000-0000-0000-0000000000b2', 'COMPLIANCE', now() + interval '1 day', 'PD-28-draft', true, 'RESTRICTED_STR', now());
INSERT INTO compliance.compliance_case_events (id, case_id, case_version, event_type, to_status, actor_type, actor_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000d03', 1, 'OPENED', 'OPEN', 'STAFF', '00000000-0000-0000-0000-0000000000b2');

-- @case compliance_cannot_read_kyc expect=error:permission denied for schema kyc
SET LOCAL ROLE fundzim_compliance;
SELECT count(*) FROM kyc.identities;

-- @case compliance_cannot_read_ledger expect=error:permission denied for schema ledger
SET LOCAL ROLE fundzim_compliance;
SELECT count(*) FROM ledger.ledger_entries;

-- @case compliance_cannot_delete_holds expect=error:permission denied for table holds
SET LOCAL ROLE fundzim_compliance;
DELETE FROM risk.holds WHERE false;

-- @case compliance_cannot_read_audit_events expect=error:permission denied for table audit_events
SET LOCAL ROLE fundzim_compliance;
SELECT count(*) FROM audit.audit_events;

-- @case compliance_cannot_change_limits expect=error:permission denied for table limits
SET LOCAL ROLE fundzim_compliance;
UPDATE risk.limits SET review_by = review_by WHERE false;
