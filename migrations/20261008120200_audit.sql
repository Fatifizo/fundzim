-- FundZim migration: audit events and security audit events (hash-chained, append-only); evidence tables follow in Stage 5 (I-18)
-- Derived from the Stage 2 design draft design/sql/0003_audit.sql (validated in design/sql/validate).
-- Runs as fundzim_migrator via `fundzimctl migrate up`. Forward-only in production (ADR-028); the Down
-- section exists for local development only and is never relied on in production.
-- +goose Up
-- +goose StatementBegin
-- goose runs each migration in its own transaction; SET LOCAL scopes the timeouts to it.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
-- -----------------------------------------------------------------------------------------------------
-- Hash chain (AUDIT.md §4.3). Each row stores
--   hash = SHA-256( canonical_json(row without "hash", NULL-valued keys removed) )
-- where the canonical JSON includes prev_hash and seq. seq is assigned under a per-table transaction
-- advisory lock, so seq order == chain order == commit order of audit writers (writers are serialised
-- per chain). The canonical form is PostgreSQL's jsonb text rendering (keys in jsonb order), produced with
-- TimeZone = UTC and bytea_output = hex pinned on the function, so it does not depend on session settings.
-- NULL-valued keys are stripped so adding a nullable column later does not change old rows' canonical form.
-- chain_version records the canonicalisation version; a change of algorithm starts a new version.
-- Stage 3 note: the Go verifier calls audit.verify_chain() rather than re-implementing jsonb rendering.
-- -----------------------------------------------------------------------------------------------------
CREATE FUNCTION audit.chain_append() RETURNS trigger LANGUAGE plpgsql
  SET timezone = 'UTC' SET bytea_output = 'hex' AS $$
DECLARE
  v_seq  bigint;
  v_hash bytea;
BEGIN
  PERFORM pg_advisory_xact_lock(hashtextextended(TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME, 0));
  EXECUTE format('SELECT seq, hash FROM %I.%I ORDER BY seq DESC LIMIT 1', TG_TABLE_SCHEMA, TG_TABLE_NAME)
     INTO v_seq, v_hash;
  NEW.seq       := coalesce(v_seq, 0) + 1;      -- caller-supplied values are ignored
  NEW.prev_hash := v_hash;                      -- NULL only for the genesis row
  NEW.hash      := NULL;
  NEW.hash      := sha256(convert_to(jsonb_strip_nulls(to_jsonb(NEW) - 'hash')::text, 'UTF8'));
  RETURN NEW;
END $$;

-- Recomputes a chain and returns every problem found (empty result = intact). Detects modified rows
-- (hash mismatch), deleted or reordered rows (seq gap / prev_hash mismatch).
CREATE FUNCTION audit.verify_chain(p_table regclass)
  RETURNS TABLE (seq bigint, problem text) LANGUAGE plpgsql
  SET timezone = 'UTC' SET bytea_output = 'hex' AS $$
DECLARE
  r          record;
  prev_seq   bigint := 0;
  prev_hash  bytea  := NULL;
BEGIN
  FOR r IN EXECUTE format(
    'SELECT t.seq AS s, t.prev_hash AS ph, t.hash AS h, jsonb_strip_nulls(to_jsonb(t) - ''hash'') AS c
       FROM %s t ORDER BY t.seq', p_table)
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
END $$;

-- -----------------------------------------------------------------------------------------------------
-- audit.audit_events (AUDIT.md §3). Append-only, hash-chained. Classification: C2 (never C3/C4 content;
-- evidence is referenced via metadata.evidence_record_ids). Retention: AUDIT (period pending LR-012).
-- actor_type/outcome keep the lower-case vocabulary of AUDIT.md §3 (they are an audit vocabulary, not a
-- domain state machine). 'vendor' is added for verification-vendor callbacks (audit-evidence-model §4.2).
-- No foreign keys to app.users: audit rows must be insertable for system/provider actors and must outlive
-- any anonymisation of the referenced account.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE audit.audit_events (
  id             uuid        NOT NULL,
  seq            bigint      NOT NULL,                    -- assigned by audit.chain_append()
  occurred_at    timestamptz NOT NULL,
  recorded_at    timestamptz NOT NULL DEFAULT now(),
  actor_type     text        NOT NULL,
  actor_id       uuid,
  actor_role     text,
  on_behalf_of   uuid,
  action         text        NOT NULL,
  target_type    text        NOT NULL,
  target_id      uuid,
  outcome        text        NOT NULL,
  request_id     text,
  correlation_id text,
  ip             inet,                                    -- C2; where lawful (PRIVACY.md)
  user_agent     text,                                    -- C2; truncated
  reason         text,                                    -- machine reason code
  justification  text,                                    -- staff text; required for J actions (enforced in Go catalogue)
  metadata       jsonb       NOT NULL DEFAULT '{}',
  break_glass    boolean     NOT NULL DEFAULT false,
  chain_version  smallint    NOT NULL DEFAULT 1,
  prev_hash      bytea,
  hash           bytea       NOT NULL,
  CONSTRAINT pk_audit_events PRIMARY KEY (id),
  CONSTRAINT uq_audit_events_seq UNIQUE (seq),
  CONSTRAINT ck_audit_events_actor_type CHECK (actor_type IN ('user', 'staff', 'system', 'provider', 'vendor')),
  CONSTRAINT ck_audit_events_actor_id CHECK (actor_type NOT IN ('user', 'staff') OR actor_id IS NOT NULL),
  CONSTRAINT ck_audit_events_outcome CHECK (outcome IN ('success', 'denied', 'failed')),
  CONSTRAINT ck_audit_events_action CHECK (action ~ '^[a-z_]+(\.[a-z_*]+)+$'),
  CONSTRAINT ck_audit_events_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),
  CONSTRAINT ck_audit_events_user_agent_length CHECK (user_agent IS NULL OR length(user_agent) <= 512),
  CONSTRAINT ck_audit_events_break_glass_justified CHECK (NOT break_glass OR justification IS NOT NULL),
  CONSTRAINT ck_audit_events_hash_length CHECK (octet_length(hash) = 32),
  CONSTRAINT ck_audit_events_prev_hash CHECK ((seq = 1) = (prev_hash IS NULL))
);
CREATE INDEX ix_audit_events_target ON audit.audit_events (target_type, target_id, occurred_at);
CREATE INDEX ix_audit_events_actor ON audit.audit_events (actor_id, occurred_at);
CREATE INDEX ix_audit_events_action ON audit.audit_events (action, occurred_at);
CREATE INDEX ix_audit_events_correlation_id ON audit.audit_events (correlation_id) WHERE correlation_id IS NOT NULL;

CREATE TRIGGER trg_audit_events_chain BEFORE INSERT ON audit.audit_events
  FOR EACH ROW EXECUTE FUNCTION audit.chain_append();
CREATE TRIGGER trg_audit_events_no_mutation BEFORE UPDATE OR DELETE ON audit.audit_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_audit_events_no_truncate BEFORE TRUNCATE ON audit.audit_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- audit.security_audit_events — separate hash chain for security-relevant events (role grants,
-- break-glass, key rotation, MFA changes, KYC document views, session revocations by staff).
-- SECURITY_ADMIN reads this table without reading financial audit events. Same shape as audit_events
-- minus on_behalf_of. Classification: C2. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE audit.security_audit_events (
  id             uuid        NOT NULL,
  seq            bigint      NOT NULL,
  occurred_at    timestamptz NOT NULL,
  recorded_at    timestamptz NOT NULL DEFAULT now(),
  actor_type     text        NOT NULL,
  actor_id       uuid,
  actor_role     text,
  action         text        NOT NULL,
  target_type    text        NOT NULL,
  target_id      uuid,
  outcome        text        NOT NULL,
  request_id     text,
  correlation_id text,
  ip             inet,
  user_agent     text,
  reason         text,
  justification  text,
  metadata       jsonb       NOT NULL DEFAULT '{}',
  break_glass    boolean     NOT NULL DEFAULT false,
  chain_version  smallint    NOT NULL DEFAULT 1,
  prev_hash      bytea,
  hash           bytea       NOT NULL,
  CONSTRAINT pk_security_audit_events PRIMARY KEY (id),
  CONSTRAINT uq_security_audit_events_seq UNIQUE (seq),
  CONSTRAINT ck_security_audit_events_actor_type CHECK (actor_type IN ('user', 'staff', 'system', 'provider', 'vendor')),
  CONSTRAINT ck_security_audit_events_actor_id CHECK (actor_type NOT IN ('user', 'staff') OR actor_id IS NOT NULL),
  CONSTRAINT ck_security_audit_events_outcome CHECK (outcome IN ('success', 'denied', 'failed')),
  CONSTRAINT ck_security_audit_events_action CHECK (action ~ '^[a-z_]+(\.[a-z_*]+)+$'),
  CONSTRAINT ck_security_audit_events_metadata_object CHECK (jsonb_typeof(metadata) = 'object'),
  CONSTRAINT ck_security_audit_events_user_agent_length CHECK (user_agent IS NULL OR length(user_agent) <= 512),
  CONSTRAINT ck_security_audit_events_break_glass_justified CHECK (NOT break_glass OR justification IS NOT NULL),
  CONSTRAINT ck_security_audit_events_hash_length CHECK (octet_length(hash) = 32),
  CONSTRAINT ck_security_audit_events_prev_hash CHECK ((seq = 1) = (prev_hash IS NULL))
);
CREATE INDEX ix_security_audit_events_target ON audit.security_audit_events (target_type, target_id, occurred_at);
CREATE INDEX ix_security_audit_events_actor ON audit.security_audit_events (actor_id, occurred_at);
CREATE INDEX ix_security_audit_events_action ON audit.security_audit_events (action, occurred_at);

CREATE TRIGGER trg_security_audit_events_chain BEFORE INSERT ON audit.security_audit_events
  FOR EACH ROW EXECUTE FUNCTION audit.chain_append();
CREATE TRIGGER trg_security_audit_events_no_mutation BEFORE UPDATE OR DELETE ON audit.security_audit_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_security_audit_events_no_truncate BEFORE TRUNCATE ON audit.security_audit_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audit.security_audit_events, audit.audit_events;
DROP FUNCTION IF EXISTS audit.verify_chain(regclass), audit.chain_append();
-- +goose StatementEnd
