-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0001: roles, schemas and shared trigger functions. Owner: platform.
-- =====================================================================================================

-- Roles. In real environments roles are created by infrastructure with LOGIN and secrets from the secret
-- manager; here they are NOLOGIN placeholders so grants can be designed and tested.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_migrator')   THEN CREATE ROLE fundzim_migrator   NOLOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_app')        THEN CREATE ROLE fundzim_app        NOLOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_kyc')        THEN CREATE ROLE fundzim_kyc        NOLOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_compliance') THEN CREATE ROLE fundzim_compliance NOLOGIN; END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'fundzim_readonly')   THEN CREATE ROLE fundzim_readonly   NOLOGIN; END IF;
END $$;

REVOKE ALL ON SCHEMA public FROM PUBLIC;

CREATE SCHEMA app;
CREATE SCHEMA queue;
CREATE SCHEMA ledger;
CREATE SCHEMA audit;
CREATE SCHEMA kyc;
CREATE SCHEMA risk;
CREATE SCHEMA compliance;
CREATE SCHEMA recon;

-- Append-only guard. Attach as:
--   CREATE TRIGGER trg_<t>_no_mutation BEFORE UPDATE OR DELETE ON <t> FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
--   CREATE TRIGGER trg_<t>_no_truncate BEFORE TRUNCATE ON <t> FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION app.forbid_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'append-only table: % on %.% is not allowed', TG_OP, TG_TABLE_SCHEMA, TG_TABLE_NAME
    USING ERRCODE = 'restrict_violation';
END $$;

-- updated_at maintenance for mutable tables.
CREATE FUNCTION app.set_updated_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  NEW.updated_at := now();
  RETURN NEW;
END $$;

-- created_at is immutable on mutable tables.
CREATE FUNCTION app.keep_created_at() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION 'created_at is immutable on %.%', TG_TABLE_SCHEMA, TG_TABLE_NAME USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;

-- State-machine transition registry. Each domain file inserts its allowed edges.
-- An INSERT must use an initial state listed with from_status = '' (the empty string means "no previous state").
CREATE TABLE app.status_transitions (
  machine     text NOT NULL,
  from_status text NOT NULL,
  to_status   text NOT NULL,
  CONSTRAINT pk_status_transitions PRIMARY KEY (machine, from_status, to_status),
  CONSTRAINT ck_status_transitions_distinct CHECK (from_status <> to_status OR from_status = '')
);

-- Transition guard. Attach as:
--   CREATE TRIGGER trg_<t>_guard_status BEFORE INSERT OR UPDATE OF status ON <t>
--     FOR EACH ROW EXECUTE FUNCTION app.guard_transition('<machine>');
-- Self-transitions (status unchanged) are allowed on UPDATE so other columns can change; machines that
-- need a recorded self-transition (e.g. PARTIALLY_REFUNDED -> PARTIALLY_REFUNDED) list it explicitly and
-- record it in their events table.
CREATE FUNCTION app.guard_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  m text := TG_ARGV[0];
  f text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    f := '';
  ELSE
    IF NEW.status = OLD.status THEN
      RETURN NEW;
    END IF;
    f := OLD.status;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM app.status_transitions t
                  WHERE t.machine = m AND t.from_status = f AND t.to_status = NEW.status) THEN
    RAISE EXCEPTION 'illegal % transition: "%" -> "%"', m, f, NEW.status USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;

-- The transition registry itself is reference data: only migrations change it.
CREATE TRIGGER trg_status_transitions_no_mutation BEFORE UPDATE OR DELETE ON app.status_transitions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
