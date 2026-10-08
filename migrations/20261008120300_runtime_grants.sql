-- FundZim migration: runtime grants (Stage 3 subset of design/sql/0018_grants.sql).
-- Privileges are DERIVED from the catalogue by app.apply_runtime_grants(), so later stages only add tables
-- and end their migration with `CALL app.apply_runtime_grants();` (fail closed: a new table has no runtime
-- privilege until then). Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
-- goose runs each migration in its own transaction; SET LOCAL scopes the timeouts to it.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
CREATE PROCEDURE app.apply_runtime_grants() LANGUAGE plpgsql AS $$
DECLARE
  schemas  constant text[] := ARRAY['app','queue','ledger','audit','kyc','risk','compliance','recon'];
  runtime  constant text   := 'fundzim_app, fundzim_worker, fundzim_kyc, fundzim_compliance, fundzim_readonly';
  -- fundzim_app may DELETE only these ephemeral rows (expired keys, pruned inbox/outbox, later sessions etc.)
  app_delete_ok constant text[] := ARRAY['app.sessions','app.otp_challenges','app.idempotency_keys','app.upload_sessions',
                                         'app.inbox_events','app.outbox_events'];
  -- reference data changed only by migrations
  reference_only constant text[] := ARRAY['app.status_transitions','app.currencies','app.markets','app.permissions',
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
REVOKE ALL ON PROCEDURE app.apply_runtime_grants() FROM PUBLIC;

-- Routines are never executable by PUBLIC by default; runtime roles get no default table privileges
-- (fail closed) except the queue schema, whose tables the job-queue library creates.
ALTER DEFAULT PRIVILEGES IN SCHEMA app, audit, ledger, kyc, risk, compliance, recon, queue REVOKE EXECUTE ON ROUTINES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES IN SCHEMA queue GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO fundzim_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA queue GRANT USAGE, SELECT ON SEQUENCES TO fundzim_app;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA queue REVOKE ALL ON TABLES FROM fundzim_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA queue REVOKE ALL ON SEQUENCES FROM fundzim_app;
DROP PROCEDURE IF EXISTS app.apply_runtime_grants();
-- +goose StatementEnd
