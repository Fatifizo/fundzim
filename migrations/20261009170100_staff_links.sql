-- FundZim migration: staff <-> personal account links (Stage 6, ADR-037 §4; stream R).
--   * app.staff_link_requests — a staff member's request to link the personal account that owns a verified login
--     email. Only the SHA-256 hash of the single-use emailed token is stored (issued by the worker at send time,
--     like app.auth_tokens); each request expires and is consumed at most once.
--   * app.actor_identities(uuid) — the actor plus any linked account (staff -> personal and personal -> staff).
--     Self-decision triggers (KYC here, campaigns in 20261009171000) use it so linked identities are refused by
--     the database, not only in Go.
--   * app.users.staff_personal_user_id can be set once (to a personal account) and never changed or removed by
--     a runtime role (no unlink in Stage 6: unlinking would weaken the conflict checks).
--   * kyc.kyc_decisions_not_self() now refuses decisions where any identity of the decider or second approver is
--     the subject, a member of the organisation, or the KYB case's creator / submitter.
--   * Runtime grants v4: v3 plus EXECUTE on app.actor_identities(uuid) for fundzim_app, fundzim_worker, fundzim_kyc.
-- Owner module: auth (staff_link_requests); users (app.users column guard). Runs as fundzim_migrator.
-- Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- the actor and every account linked to it (one hop in each direction; links are 1:1 by uq_users_staff_personal_user_id)
CREATE FUNCTION app.actor_identities(actor uuid) RETURNS uuid[] LANGUAGE sql STABLE SECURITY DEFINER
  SET search_path = pg_catalog, pg_temp AS $$
  SELECT CASE WHEN actor IS NULL THEN ARRAY[]::uuid[] ELSE
    ARRAY[actor]
    || coalesce((SELECT array_agg(u.staff_personal_user_id) FROM app.users u WHERE u.id = actor AND u.staff_personal_user_id IS NOT NULL), ARRAY[]::uuid[])
    || coalesce((SELECT array_agg(s.id) FROM app.users s WHERE s.staff_personal_user_id = actor), ARRAY[]::uuid[])
  END
$$;
REVOKE ALL ON FUNCTION app.actor_identities(uuid) FROM PUBLIC;

-- the link is set once, to an active-or-not personal (USER) account, and never changed or cleared by a runtime
-- role; the migrator (development fixtures, a future maker-checker procedure) is the only exception
CREATE FUNCTION app.users_staff_link_guard() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, pg_temp AS $$
BEGIN
  IF NEW.staff_personal_user_id IS NOT DISTINCT FROM OLD.staff_personal_user_id THEN
    RETURN NEW;
  END IF;
  IF OLD.staff_personal_user_id IS NOT NULL AND session_user <> 'fundzim_migrator' THEN
    RAISE EXCEPTION 'app.users %: a staff <-> personal account link cannot be changed or removed', OLD.id
      USING ERRCODE = 'restrict_violation', CONSTRAINT = 'ck_users_staff_link_permanent';
  END IF;
  IF NEW.staff_personal_user_id IS NOT NULL AND NOT EXISTS (
       SELECT 1 FROM app.users p WHERE p.id = NEW.staff_personal_user_id AND p.account_kind = 'USER') THEN
    RAISE EXCEPTION 'app.users %: a staff account can only be linked to a personal account', OLD.id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'ck_users_staff_link_personal';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION app.users_staff_link_guard() FROM PUBLIC;
CREATE TRIGGER trg_users_staff_link_guard BEFORE UPDATE OF staff_personal_user_id ON app.users
  FOR EACH ROW EXECUTE FUNCTION app.users_staff_link_guard();

-- -----------------------------------------------------------------------------------------------------
-- app.staff_link_requests (owner: auth). Classification: C2 (email address of the person's own account).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.staff_link_requests (
  id               uuid        NOT NULL,
  staff_user_id    uuid        NOT NULL,
  staff_kind       text        NOT NULL DEFAULT 'STAFF',
  email_normalized text        NOT NULL,
  target_user_id   uuid,                    -- the eligible personal account found at request time (NULL: none, no email sent)
  token_hash       bytea,                   -- SHA-256 of the emailed token; set by the worker when it sends the email
  token_issued_at  timestamptz,
  requested_ip     inet,
  created_at       timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz NOT NULL,
  consumed_at      timestamptz,
  consumed_by      uuid,
  invalidated_at   timestamptz,
  CONSTRAINT pk_staff_link_requests PRIMARY KEY (id),
  CONSTRAINT uq_staff_link_requests_token_hash UNIQUE (token_hash),
  CONSTRAINT fk_staff_link_requests_staff FOREIGN KEY (staff_user_id, staff_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_staff_link_requests_target FOREIGN KEY (target_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_staff_link_requests_consumed_by FOREIGN KEY (consumed_by) REFERENCES app.users (id),
  CONSTRAINT ck_staff_link_requests_staff_kind CHECK (staff_kind = 'STAFF'),
  CONSTRAINT ck_staff_link_requests_email CHECK (email_normalized = lower(email_normalized) AND length(email_normalized) BETWEEN 3 AND 254),
  CONSTRAINT ck_staff_link_requests_token_hash CHECK (token_hash IS NULL OR octet_length(token_hash) = 32),
  CONSTRAINT ck_staff_link_requests_token_pair CHECK ((token_hash IS NULL) = (token_issued_at IS NULL)),
  CONSTRAINT ck_staff_link_requests_token_target CHECK (token_hash IS NULL OR target_user_id IS NOT NULL),
  CONSTRAINT ck_staff_link_requests_expiry CHECK (expires_at > created_at AND expires_at <= created_at + interval '7 days'),
  CONSTRAINT ck_staff_link_requests_final CHECK (consumed_at IS NULL OR invalidated_at IS NULL),
  CONSTRAINT ck_staff_link_requests_consumed CHECK ((consumed_at IS NULL) = (consumed_by IS NULL)
                                                   AND (consumed_by IS NULL OR (consumed_by = target_user_id AND token_hash IS NOT NULL))),
  CONSTRAINT ck_staff_link_requests_not_self CHECK (target_user_id IS NULL OR target_user_id <> staff_user_id)
);
COMMENT ON TABLE app.staff_link_requests IS 'C2. Staff -> personal account link requests (ADR-037 §4); token hash only, single use, expiring.';
CREATE UNIQUE INDEX uq_staff_link_requests_live ON app.staff_link_requests (staff_user_id)
  WHERE consumed_at IS NULL AND invalidated_at IS NULL;
CREATE INDEX ix_staff_link_requests_target ON app.staff_link_requests (target_user_id) WHERE target_user_id IS NOT NULL;
CREATE FUNCTION app.staff_link_requests_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.consumed_at IS NOT NULL OR OLD.invalidated_at IS NOT NULL THEN
    RAISE EXCEPTION 'staff link request % is already consumed or invalidated', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF (to_jsonb(OLD) - ARRAY['token_hash','token_issued_at','consumed_at','consumed_by','invalidated_at'])
     IS DISTINCT FROM (to_jsonb(NEW) - ARRAY['token_hash','token_issued_at','consumed_at','consumed_by','invalidated_at']) THEN
    RAISE EXCEPTION 'staff link request % is immutable except its token and final state', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_staff_link_requests_guard BEFORE UPDATE ON app.staff_link_requests
  FOR EACH ROW EXECUTE FUNCTION app.staff_link_requests_guard();
CREATE TRIGGER trg_staff_link_requests_no_delete BEFORE DELETE ON app.staff_link_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_staff_link_requests_no_truncate BEFORE TRUNCATE ON app.staff_link_requests
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- KYC/KYB self-decision guard, now over every identity of the decider and second approver (ADR-037 §4)
CREATE OR REPLACE FUNCTION kyc.kyc_decisions_not_self() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE subject_user uuid; org uuid; idents uuid[];
BEGIN
  idents := app.actor_identities(NEW.decided_by) || app.actor_identities(NEW.second_approver_id);
  IF NEW.kyc_case_id IS NOT NULL THEN
    SELECT p.user_id INTO subject_user FROM kyc.kyc_cases c JOIN kyc.verification_profiles p ON p.id = c.profile_id
     WHERE c.id = NEW.kyc_case_id;
    IF subject_user = ANY (idents) THEN
      RAISE EXCEPTION 'kyc decision: a reviewer cannot decide their own verification' USING ERRCODE = 'check_violation';
    END IF;
  ELSE
    SELECT o.organisation_id INTO org FROM kyc.kyb_cases c JOIN kyc.kyb_organisations o ON o.id = c.kyb_organisation_id
     WHERE c.id = NEW.kyb_case_id;
    IF EXISTS (SELECT 1 FROM app.organisation_members m WHERE m.organisation_id = org AND m.status <> 'REMOVED'
                 AND m.user_id = ANY (idents))
       OR EXISTS (SELECT 1 FROM kyc.kyb_cases c WHERE c.id = NEW.kyb_case_id
                    AND (c.submitted_by = ANY (idents) OR c.created_by = ANY (idents))) THEN
      RAISE EXCEPTION 'kyb decision: a member or representative of the organisation cannot decide its verification' USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION kyc.kyc_decisions_not_self() FROM PUBLIC;

-- -----------------------------------------------------------------------------------------------------
-- Runtime grants v4: v3 (20261009150000) plus EXECUTE on app.actor_identities(uuid).
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
      ('app.enqueue_outbox(uuid,text,uuid,text,jsonb,text,timestamptz)', 'fundzim_compliance'),
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
-- Development rollback only. Restores the Stage 5 KYC guard and runtime grants v3.
CREATE OR REPLACE FUNCTION kyc.kyc_decisions_not_self() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, pg_temp AS $$
DECLARE subject_user uuid; org uuid;
BEGIN
  IF NEW.kyc_case_id IS NOT NULL THEN
    SELECT p.user_id INTO subject_user FROM kyc.kyc_cases c JOIN kyc.verification_profiles p ON p.id = c.profile_id
     WHERE c.id = NEW.kyc_case_id;
    IF subject_user IN (NEW.decided_by, NEW.second_approver_id) THEN
      RAISE EXCEPTION 'kyc decision: a reviewer cannot decide their own verification' USING ERRCODE = 'check_violation';
    END IF;
  ELSE
    SELECT o.organisation_id INTO org FROM kyc.kyb_cases c JOIN kyc.kyb_organisations o ON o.id = c.kyb_organisation_id
     WHERE c.id = NEW.kyb_case_id;
    IF EXISTS (SELECT 1 FROM app.organisation_members m WHERE m.organisation_id = org AND m.status <> 'REMOVED'
                 AND m.user_id IN (NEW.decided_by, NEW.second_approver_id))
       OR EXISTS (SELECT 1 FROM kyc.kyb_cases c WHERE c.id = NEW.kyb_case_id
                    AND (c.submitted_by IN (NEW.decided_by, NEW.second_approver_id) OR c.created_by IN (NEW.decided_by, NEW.second_approver_id))) THEN
      RAISE EXCEPTION 'kyb decision: a member or representative of the organisation cannot decide its verification' USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION kyc.kyc_decisions_not_self() FROM PUBLIC;
DROP TABLE IF EXISTS app.staff_link_requests;
DROP FUNCTION IF EXISTS app.staff_link_requests_guard();
DROP TRIGGER IF EXISTS trg_users_staff_link_guard ON app.users;
DROP FUNCTION IF EXISTS app.users_staff_link_guard();
DROP FUNCTION IF EXISTS app.actor_identities(uuid);
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
