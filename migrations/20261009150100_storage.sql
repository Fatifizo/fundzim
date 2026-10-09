-- FundZim migration: storage module — app.stored_objects and app.upload_sessions (Stage 5, work stream S).
-- Derived from the Stage 2 design draft design/sql/0005_storage.sql with the ADR-034 §3 scan vocabulary and the
-- ADR-035 §5 upload model (uploads stream THROUGH the API; there are no presigned browser uploads, so the
-- draft's declared-hash/presigned columns are gone and upload_sessions tracks an in-flight API upload).
-- Owner module: storage. Classification: C1 metadata (C3 objects themselves live in the private buckets; for
-- PRIVATE_KYC this row holds only technical metadata — no filename, no subject: the kyc module keeps the C3
-- linkage in kyc.*). Retention: per retention_class (LR-012).
--
-- Keys are server-generated: quarantine/<id> while unscanned, objects/<id> after promotion (enforced by CHECK).
-- Client filenames are never stored (there is no filename column).
--
-- Encryption at rest: private-bucket objects are written with S3 SSE-C (AES-256, performed by the object
-- store) using a per-object key derived by HMAC-SHA-256 from an application key; encryption_key_id records
-- which application key ("sse-c/hmac-sha256/local/<fingerprint>"). Garage v2 implements SSE-C; it has no
-- SSE-KMS. The application key is a LOCAL key (env) until KMS (Stage 18) — see internal/storage/doc.go.
--
-- Scan state machine `stored_object_scan` (ADR-034 §3; the column is scan_status, so the guard is a dedicated
-- trigger that reads the same app.status_transitions registry as app.guard_transition):
--   '' -> UPLOADED -> QUARANTINED -> SCANNING -> CLEAN | REJECTED | FAILED_SCAN ; FAILED_SCAN -> SCANNING
--   UPLOADED -> REJECTED (content failed upload validation: empty, too large, type not allowed / mismatch)
--   any state -> DELETED (soft; the row stays as proof)
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('stored_object_scan', '',            'UPLOADED'),
  ('stored_object_scan', 'UPLOADED',    'QUARANTINED'),
  ('stored_object_scan', 'UPLOADED',    'REJECTED'),
  ('stored_object_scan', 'UPLOADED',    'DELETED'),
  ('stored_object_scan', 'QUARANTINED', 'SCANNING'),
  ('stored_object_scan', 'QUARANTINED', 'DELETED'),
  ('stored_object_scan', 'SCANNING',    'CLEAN'),
  ('stored_object_scan', 'SCANNING',    'REJECTED'),
  ('stored_object_scan', 'SCANNING',    'FAILED_SCAN'),
  ('stored_object_scan', 'SCANNING',    'DELETED'),
  ('stored_object_scan', 'FAILED_SCAN', 'SCANNING'),
  ('stored_object_scan', 'FAILED_SCAN', 'DELETED'),
  ('stored_object_scan', 'CLEAN',       'DELETED'),
  ('stored_object_scan', 'REJECTED',    'DELETED'),
  ('upload_session',     '',            'PENDING'),
  ('upload_session',     'PENDING',     'COMPLETED'),
  ('upload_session',     'PENDING',     'ABORTED'),
  ('upload_session',     'PENDING',     'EXPIRED');

-- -----------------------------------------------------------------------------------------------------
-- app.stored_objects — one row per uploaded object (metadata only). Never deleted: DELETED is a soft state.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.stored_objects (
  id                    uuid        NOT NULL,
  bucket_class          text        NOT NULL,
  purpose               text        NOT NULL,
  owner_module          text        NOT NULL,               -- module that owns the business record referencing it
  quarantine_key        text        NOT NULL,               -- quarantine/<id>
  promoted_key          text,                               -- objects/<id>, set when CLEAN
  declared_content_type text        NOT NULL,               -- client claim (untrusted), kept for forensics
  sniffed_content_type  text,                               -- magic-byte detection (authoritative)
  content_sha256        bytea,                              -- set once when the upload completes
  size_bytes            bigint,                             -- set once when the upload completes
  scan_status           text        NOT NULL,
  scan_attempts         integer     NOT NULL DEFAULT 0,     -- also the fencing token of the current scan
  scan_started_at       timestamptz,                        -- lease start of the current SCANNING attempt
  next_scan_at          timestamptz,                        -- FAILED_SCAN: earliest retry
  last_scan_error       text,                               -- code of the last scanner failure
  scan_engine           text,                               -- e.g. 'ClamAV 1.5.3/27790' or 'dev-scanner (NOT malware protection)'
  scan_signature        text,                               -- malware signature name when REJECTED by the scanner
  scanned_at            timestamptz,
  promoted_at           timestamptz,
  reject_reason         text,                               -- code; set once when REJECTED
  classification        text        NOT NULL,
  retention_class       text        NOT NULL,
  encryption_key_id     text,                               -- SSE-C application key id (private buckets)
  uploaded_by_user_id   uuid,
  deleted_at            timestamptz,                        -- soft delete; bytes removed by the delete path
  deleted_by_user_id    uuid,
  delete_reason         text,                               -- code
  purged_at             timestamptz,                        -- bytes removed from the store
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_stored_objects PRIMARY KEY (id),
  CONSTRAINT uq_stored_objects_id_bucket_class UNIQUE (id, bucket_class),   -- target of bucket-restricted FKs
  CONSTRAINT uq_stored_objects_quarantine_key UNIQUE (bucket_class, quarantine_key),
  CONSTRAINT uq_stored_objects_promoted_key UNIQUE (bucket_class, promoted_key),
  CONSTRAINT fk_stored_objects_uploaded_by_user_id FOREIGN KEY (uploaded_by_user_id) REFERENCES app.users (id),
  CONSTRAINT fk_stored_objects_deleted_by_user_id FOREIGN KEY (deleted_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_stored_objects_bucket_class CHECK (bucket_class IN ('PUBLIC_MEDIA', 'PRIVATE_KYC', 'PRIVATE_EVIDENCE')),
  CONSTRAINT ck_stored_objects_purpose CHECK (
       (bucket_class = 'PUBLIC_MEDIA'     AND purpose IN ('CAMPAIGN_MEDIA', 'CAMPAIGN_UPDATE_MEDIA', 'PROFILE_AVATAR', 'ORGANISATION_LOGO'))
    OR (bucket_class = 'PRIVATE_KYC'      AND purpose IN ('KYC_DOCUMENT', 'KYC_SELFIE', 'KYB_DOCUMENT', 'BENEFICIARY_EVIDENCE',
                                                          'PAYOUT_DESTINATION_EVIDENCE'))
    OR (bucket_class = 'PRIVATE_EVIDENCE' AND purpose IN ('CAMPAIGN_SUPPORTING_DOCUMENT', 'DISPUTE_EVIDENCE', 'COMPLIANCE_EVIDENCE',
                                                          'RECONCILIATION_REPORT'))),
  CONSTRAINT ck_stored_objects_owner_module CHECK (owner_module IN ('users', 'organisations', 'campaigns', 'kyc', 'beneficiaries',
                                                                    'compliance', 'payments', 'payouts', 'reconciliation', 'audit')),
  CONSTRAINT ck_stored_objects_quarantine_key CHECK (quarantine_key = 'quarantine/' || id::text),
  CONSTRAINT ck_stored_objects_promoted_key CHECK (promoted_key IS NULL OR promoted_key = 'objects/' || id::text),
  CONSTRAINT ck_stored_objects_declared_type CHECK (length(declared_content_type) BETWEEN 1 AND 255),
  CONSTRAINT ck_stored_objects_sha256 CHECK (content_sha256 IS NULL OR octet_length(content_sha256) = 32),
  CONSTRAINT ck_stored_objects_size CHECK (size_bytes IS NULL OR size_bytes > 0),
  CONSTRAINT ck_stored_objects_scan_status CHECK (scan_status IN ('UPLOADED', 'QUARANTINED', 'SCANNING', 'CLEAN', 'REJECTED',
                                                                  'FAILED_SCAN', 'DELETED')),
  -- a complete upload (anything that reached QUARANTINED) has its hash, size and sniffed type
  CONSTRAINT ck_stored_objects_complete CHECK (scan_status NOT IN ('QUARANTINED', 'SCANNING', 'CLEAN', 'FAILED_SCAN')
                                               OR (content_sha256 IS NOT NULL AND size_bytes IS NOT NULL
                                                   AND sniffed_content_type IS NOT NULL)),
  CONSTRAINT ck_stored_objects_scanning CHECK (scan_status <> 'SCANNING' OR (scan_started_at IS NOT NULL AND scan_attempts > 0)),
  CONSTRAINT ck_stored_objects_failed CHECK (scan_status <> 'FAILED_SCAN' OR (last_scan_error IS NOT NULL AND next_scan_at IS NOT NULL)),
  CONSTRAINT ck_stored_objects_clean CHECK (scan_status <> 'CLEAN'
                                            OR (promoted_key IS NOT NULL AND scan_engine IS NOT NULL AND scanned_at IS NOT NULL)),
  CONSTRAINT ck_stored_objects_promotion CHECK ((promoted_key IS NULL) = (promoted_at IS NULL)
                                                AND (promoted_key IS NULL OR scan_status IN ('CLEAN', 'DELETED'))),
  CONSTRAINT ck_stored_objects_rejected CHECK (scan_status <> 'REJECTED' OR reject_reason IS NOT NULL),
  CONSTRAINT ck_stored_objects_reject_reason CHECK (reject_reason IS NULL OR reject_reason IN (
    'MALWARE_DETECTED', 'EMPTY_FILE', 'FILE_TOO_LARGE', 'UNSUPPORTED_FILE_TYPE', 'FILE_TYPE_MISMATCH', 'MALFORMED_UPLOAD')),
  CONSTRAINT ck_stored_objects_signature CHECK (scan_signature IS NULL OR (reject_reason = 'MALWARE_DETECTED' AND length(scan_signature) <= 255)),
  CONSTRAINT ck_stored_objects_scan_error CHECK (last_scan_error IS NULL OR last_scan_error IN (
    'SCANNER_UNAVAILABLE', 'SCANNER_TIMEOUT', 'SCANNER_ERROR', 'SCAN_INTERRUPTED', 'STORAGE_ERROR')),
  CONSTRAINT ck_stored_objects_attempts CHECK (scan_attempts >= 0),
  CONSTRAINT ck_stored_objects_content_type CHECK (
    sniffed_content_type IS NULL
    OR (bucket_class = 'PUBLIC_MEDIA' AND sniffed_content_type IN ('image/jpeg', 'image/png'))
    OR (bucket_class IN ('PRIVATE_KYC', 'PRIVATE_EVIDENCE') AND sniffed_content_type IN ('image/jpeg', 'image/png', 'application/pdf'))),
  CONSTRAINT ck_stored_objects_classification CHECK (
       (bucket_class = 'PUBLIC_MEDIA'     AND classification IN ('C0', 'C1'))
    OR (bucket_class = 'PRIVATE_KYC'      AND classification = 'C3')
    OR (bucket_class = 'PRIVATE_EVIDENCE' AND classification IN ('C2', 'C3'))),
  CONSTRAINT ck_stored_objects_retention_class CHECK (retention_class IN ('KYC', 'FINANCIAL', 'AUDIT', 'CASE', 'CONSENT', 'OPERATIONAL')),
  CONSTRAINT ck_stored_objects_private_encrypted CHECK (bucket_class = 'PUBLIC_MEDIA' OR encryption_key_id IS NOT NULL),
  CONSTRAINT ck_stored_objects_key_id CHECK (encryption_key_id IS NULL OR encryption_key_id ~ '^[a-z0-9-]+(/[a-z0-9-]+){1,4}$'),
  CONSTRAINT ck_stored_objects_deleted CHECK ((scan_status = 'DELETED') = (deleted_at IS NOT NULL)
                                              AND (deleted_at IS NULL OR delete_reason IS NOT NULL)),
  CONSTRAINT ck_stored_objects_delete_reason CHECK (delete_reason IS NULL OR delete_reason IN (
    'OWNER_DELETED', 'UPLOAD_EXPIRED', 'UPLOAD_FAILED', 'RETENTION_EXPIRED', 'STAFF_DELETED')),
  CONSTRAINT ck_stored_objects_purged CHECK (purged_at IS NULL OR deleted_at IS NOT NULL)
);
CREATE INDEX ix_stored_objects_scan_queue ON app.stored_objects (created_at) WHERE scan_status = 'QUARANTINED';
CREATE INDEX ix_stored_objects_retry ON app.stored_objects (next_scan_at) WHERE scan_status = 'FAILED_SCAN';
CREATE INDEX ix_stored_objects_scanning ON app.stored_objects (scan_started_at) WHERE scan_status = 'SCANNING';
CREATE INDEX ix_stored_objects_uploaded_by ON app.stored_objects (uploaded_by_user_id) WHERE uploaded_by_user_id IS NOT NULL;

-- Transition guard for scan_status against the shared registry (same semantics as app.guard_transition).
CREATE FUNCTION app.stored_objects_guard_scan_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  f text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    f := '';
  ELSE
    IF NEW.scan_status = OLD.scan_status THEN
      RETURN NEW;
    END IF;
    f := OLD.scan_status;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM app.status_transitions t
                  WHERE t.machine = 'stored_object_scan' AND t.from_status = f AND t.to_status = NEW.scan_status) THEN
    RAISE EXCEPTION 'illegal stored_object_scan transition: "%" -> "%"', f, NEW.scan_status USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_stored_objects_guard_scan_status BEFORE INSERT OR UPDATE OF scan_status ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.stored_objects_guard_scan_status();
CREATE TRIGGER trg_stored_objects_immutable_cols BEFORE UPDATE ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'bucket_class', 'purpose', 'owner_module', 'quarantine_key',
    'declared_content_type', 'classification', 'retention_class', 'encryption_key_id', 'uploaded_by_user_id', 'created_at');
CREATE TRIGGER trg_stored_objects_set_once_cols BEFORE UPDATE ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.set_once_columns('content_sha256', 'size_bytes', 'sniffed_content_type', 'promoted_key',
    'promoted_at', 'reject_reason', 'scan_signature', 'deleted_at', 'deleted_by_user_id', 'delete_reason', 'purged_at');
CREATE TRIGGER trg_stored_objects_set_updated_at BEFORE UPDATE ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_stored_objects_no_delete BEFORE DELETE ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();   -- rows stay as proof of what existed
CREATE TRIGGER trg_stored_objects_no_truncate BEFORE TRUNCATE ON app.stored_objects
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.upload_sessions — one in-flight API upload (the bytes stream through the API into the quarantine key).
-- A PENDING session past expires_at is an interrupted upload: the cleanup job marks it EXPIRED, deletes any
-- partial object and moves the stored object UPLOADED -> DELETED (UPLOAD_EXPIRED). Classification: C1.
-- Retention: OPERATIONAL (prunable; the stored_objects row is the durable record).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.upload_sessions (
  id                uuid        NOT NULL,
  stored_object_id  uuid        NOT NULL,
  user_id           uuid,
  max_bytes         bigint      NOT NULL,
  status            text        NOT NULL,
  expires_at        timestamptz NOT NULL,
  finished_at       timestamptz,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_upload_sessions PRIMARY KEY (id),
  CONSTRAINT uq_upload_sessions_stored_object_id UNIQUE (stored_object_id),
  CONSTRAINT fk_upload_sessions_stored_object_id FOREIGN KEY (stored_object_id) REFERENCES app.stored_objects (id),
  CONSTRAINT fk_upload_sessions_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_upload_sessions_status CHECK (status IN ('PENDING', 'COMPLETED', 'ABORTED', 'EXPIRED')),
  CONSTRAINT ck_upload_sessions_max_bytes CHECK (max_bytes > 0),   -- per-purpose maxima are configuration
  CONSTRAINT ck_upload_sessions_finished CHECK ((status = 'PENDING') = (finished_at IS NULL)),
  CONSTRAINT ck_upload_sessions_expiry CHECK (expires_at > created_at)
);
CREATE INDEX ix_upload_sessions_pending_expiry ON app.upload_sessions (expires_at) WHERE status = 'PENDING';
CREATE TRIGGER trg_upload_sessions_guard_status BEFORE INSERT OR UPDATE OF status ON app.upload_sessions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('upload_session');
CREATE TRIGGER trg_upload_sessions_cols BEFORE UPDATE ON app.upload_sessions
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'finished_at');
CREATE TRIGGER trg_upload_sessions_set_updated_at BEFORE UPDATE ON app.upload_sessions
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.upload_sessions, app.stored_objects;
DROP FUNCTION IF EXISTS app.stored_objects_guard_scan_status();
-- status_transitions is append-only; the migrator (table owner) lifts the guard only for this development rollback.
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine IN ('stored_object_scan', 'upload_session');
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
