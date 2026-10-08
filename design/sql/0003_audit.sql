-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0003: audit events, security audit events, evidence records and evidence holds.
-- Owner module: audit. Schema: audit.
-- Docs: docs/database/audit-notification-schema.md, docs/AUDIT.md, docs/compliance/audit-evidence-model.md.
-- =====================================================================================================

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

-- -----------------------------------------------------------------------------------------------------
-- audit.evidence_records (audit-evidence-model §2, ADR-019). Metadata + hash of material that supported
-- a decision; the object lives in a private bucket (KYC objects via kyc, other evidence via storage in
-- the PRIVATE_EVIDENCE class). Append-only except legal_hold, a cached flag that must equal "this record
-- has at least one HOLD row without a RELEASE row" (checked by trigger).
-- Classification: C2 metadata (the referenced object may be C3; related_refs never holds C3 values).
-- Retention: per retention_class (periods pending LR-012; unset period = retain).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE audit.evidence_records (
  id                uuid        NOT NULL,
  evidence_type     text        NOT NULL,                -- controlled list (Go catalogue), e.g. 'ID_DOCUMENT'
  subject_type      text        NOT NULL,                -- 'user'|'organisation'|'campaign'|'beneficiary'|'payment'|...
  subject_id        uuid        NOT NULL,
  related_refs      jsonb       NOT NULL DEFAULT '{}',   -- ids only, e.g. {"case_id": ..., "provider_reference": ...}
  collected_at      timestamptz NOT NULL,
  collected_by_type text        NOT NULL,
  collected_by_id   uuid,
  source            text        NOT NULL,
  storage_ref       text,                                -- private bucket object key (random); NULL if a DB row is the evidence
  content_sha256    bytea       NOT NULL,
  size_bytes        bigint,
  media_type        text,
  classification    text        NOT NULL,
  retention_class   text        NOT NULL,
  legal_hold        boolean     NOT NULL DEFAULT false,
  supersedes_id     uuid,
  audit_event_id    uuid        NOT NULL,                -- the event that created this record (same transaction)
  created_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_evidence_records PRIMARY KEY (id),
  CONSTRAINT fk_evidence_records_supersedes_id FOREIGN KEY (supersedes_id) REFERENCES audit.evidence_records (id),
  CONSTRAINT fk_evidence_records_audit_event_id FOREIGN KEY (audit_event_id) REFERENCES audit.audit_events (id),
  CONSTRAINT uq_evidence_records_supersedes_id UNIQUE (supersedes_id),
  CONSTRAINT ck_evidence_records_evidence_type CHECK (evidence_type ~ '^[A-Z][A-Z0-9_]*$'),
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

-- -----------------------------------------------------------------------------------------------------
-- audit.evidence_holds — append-only legal-hold history; HOLD and RELEASE are both maker-checker.
-- A RELEASE names the HOLD it releases (same evidence record, enforced by composite FK), at most once.
-- Classification: C2. Retention: CASE.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE audit.evidence_holds (
  id                 uuid        NOT NULL,
  evidence_record_id uuid        NOT NULL,
  action             text        NOT NULL,
  released_hold_id   uuid,
  reason             text        NOT NULL,
  case_id            uuid,                               -- compliance case id (no FK: compliance schema)
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

-- A RELEASE may only name a HOLD row (not another RELEASE).
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

-- evidence_records: only legal_hold may change, and only to the value implied by evidence_holds.
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

-- A new evidence record never starts on hold (holds are always evidence_holds rows).
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
