-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate). Executable migrations start in
-- Stage 3 under /migrations (goose), derived from these drafts.
-- File 0005: storage module — stored_objects, upload_sessions. Schema: app.
-- Docs: docs/database/design-principles.md §storage, docs/SECURITY.md §18, ADR-008, ADR-009.
-- Objects themselves live in object storage; this is metadata only. Keys are server-generated random
-- UUID paths, never derived from user input. Upload flow: upload_session (presigned PUT to quarantine/ with
-- a declared SHA-256 that the store verifies) → stored_object QUARANTINED → scan → CLEAN (promoted) |
-- INFECTED | FAILED. KYC objects use the same pipeline in the PRIVATE_KYC bucket class, but their row here
-- holds only technical metadata (no filename, no subject): the kyc module keeps the C3 linkage in kyc.*.
-- =====================================================================================================

CREATE TABLE app.stored_objects (
  id                   uuid        NOT NULL,
  bucket_class         text        NOT NULL,
  owner_module         text        NOT NULL,               -- module that owns the business record referencing it
  quarantine_key       text        NOT NULL,
  promoted_key         text,                               -- set when CLEAN and copied out of quarantine
  content_sha256       bytea       NOT NULL,               -- declared at upload, verified by the store and the scanner
  size_bytes           bigint      NOT NULL,
  declared_content_type text       NOT NULL,
  sniffed_content_type text,                               -- magic-byte detection by the scanner
  scan_status          text        NOT NULL DEFAULT 'QUARANTINED',
  scan_engine          text,                               -- e.g. 'clamav <version>/<db version>'
  scanned_at           timestamptz,
  promoted_at          timestamptz,
  classification       text        NOT NULL,
  retention_class      text        NOT NULL,
  encryption_key_id    text,                               -- SSE-KMS key id for private buckets
  original_filename    text,                               -- display metadata only (escaped); never for PRIVATE_KYC
  image_width          integer,
  image_height         integer,
  uploaded_by_user_id  uuid,
  deleted_at           timestamptz,                        -- soft delete (hidden); object still stored
  purged_at            timestamptz,                        -- object removed / key destroyed by the retention job
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_stored_objects PRIMARY KEY (id),
  CONSTRAINT uq_stored_objects_id_bucket_class UNIQUE (id, bucket_class),   -- target of bucket-restricted FKs
  CONSTRAINT uq_stored_objects_quarantine_key UNIQUE (bucket_class, quarantine_key),
  CONSTRAINT uq_stored_objects_promoted_key UNIQUE (bucket_class, promoted_key),
  CONSTRAINT fk_stored_objects_uploaded_by_user_id FOREIGN KEY (uploaded_by_user_id) REFERENCES app.users (id),
  CONSTRAINT ck_stored_objects_bucket_class CHECK (bucket_class IN ('PUBLIC_MEDIA', 'PRIVATE_KYC', 'PRIVATE_EVIDENCE')),
  CONSTRAINT ck_stored_objects_owner_module CHECK (owner_module IN ('users', 'organisations', 'campaigns', 'kyc', 'beneficiaries',
                                                                    'compliance', 'payments', 'payouts', 'reconciliation', 'audit')),
  CONSTRAINT ck_stored_objects_quarantine_key CHECK (quarantine_key ~ '^quarantine/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
  CONSTRAINT ck_stored_objects_promoted_key CHECK (promoted_key IS NULL OR promoted_key ~ '^objects/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
  CONSTRAINT ck_stored_objects_sha256 CHECK (octet_length(content_sha256) = 32),
  CONSTRAINT ck_stored_objects_size CHECK (size_bytes > 0),
  CONSTRAINT ck_stored_objects_scan_status CHECK (scan_status IN ('QUARANTINED', 'CLEAN', 'INFECTED', 'FAILED')),
  CONSTRAINT ck_stored_objects_scanned CHECK (scan_status = 'QUARANTINED' OR (scanned_at IS NOT NULL AND scan_engine IS NOT NULL)),
  CONSTRAINT ck_stored_objects_promotion CHECK ((promoted_key IS NULL) = (promoted_at IS NULL)
                                                AND (promoted_key IS NULL OR scan_status = 'CLEAN')),
  CONSTRAINT ck_stored_objects_clean_sniffed CHECK (scan_status <> 'CLEAN' OR sniffed_content_type IS NOT NULL),
  CONSTRAINT ck_stored_objects_content_type CHECK (
    sniffed_content_type IS NULL
    OR (bucket_class = 'PUBLIC_MEDIA' AND sniffed_content_type IN ('image/jpeg', 'image/png', 'image/webp'))
    OR (bucket_class IN ('PRIVATE_KYC', 'PRIVATE_EVIDENCE') AND sniffed_content_type IN ('image/jpeg', 'image/png', 'application/pdf'))
    OR (bucket_class = 'PRIVATE_EVIDENCE' AND sniffed_content_type IN ('text/csv', 'application/json'))),   -- provider reports
  CONSTRAINT ck_stored_objects_classification CHECK (
       (bucket_class = 'PUBLIC_MEDIA'     AND classification IN ('C0', 'C1'))
    OR (bucket_class = 'PRIVATE_KYC'      AND classification = 'C3')
    OR (bucket_class = 'PRIVATE_EVIDENCE' AND classification IN ('C2', 'C3'))),
  CONSTRAINT ck_stored_objects_retention_class CHECK (retention_class IN ('KYC', 'FINANCIAL', 'AUDIT', 'CASE', 'CONSENT', 'OPERATIONAL')),
  CONSTRAINT ck_stored_objects_private_encrypted CHECK (bucket_class = 'PUBLIC_MEDIA' OR encryption_key_id IS NOT NULL),
  CONSTRAINT ck_stored_objects_kyc_no_filename CHECK (bucket_class <> 'PRIVATE_KYC' OR original_filename IS NULL),
  CONSTRAINT ck_stored_objects_filename CHECK (original_filename IS NULL OR length(original_filename) <= 255),
  CONSTRAINT ck_stored_objects_dimensions CHECK ((image_width IS NULL OR image_width > 0) AND (image_height IS NULL OR image_height > 0)),
  CONSTRAINT ck_stored_objects_purged CHECK (purged_at IS NULL OR deleted_at IS NOT NULL)
);
CREATE INDEX ix_stored_objects_scan_queue ON app.stored_objects (created_at) WHERE scan_status = 'QUARANTINED';
CREATE INDEX ix_stored_objects_uploaded_by ON app.stored_objects (uploaded_by_user_id) WHERE uploaded_by_user_id IS NOT NULL;

-- Scan state machine (column is scan_status, so the generic status guard is not used):
--   QUARANTINED -> CLEAN | INFECTED | FAILED ; FAILED -> QUARANTINED (rescan). CLEAN and INFECTED are final.
-- Identity columns are immutable.
CREATE FUNCTION app.stored_objects_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.scan_status <> 'QUARANTINED' THEN
      RAISE EXCEPTION 'stored objects start QUARANTINED' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;
  IF NEW.scan_status <> OLD.scan_status AND NOT (
       (OLD.scan_status = 'QUARANTINED' AND NEW.scan_status IN ('CLEAN', 'INFECTED', 'FAILED'))
    OR (OLD.scan_status = 'FAILED' AND NEW.scan_status = 'QUARANTINED')) THEN
    RAISE EXCEPTION 'illegal stored_object scan transition: "%" -> "%"', OLD.scan_status, NEW.scan_status
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_stored_objects_guard BEFORE INSERT OR UPDATE ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.stored_objects_guard();
CREATE TRIGGER trg_stored_objects_immutable_cols BEFORE UPDATE ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('bucket_class', 'owner_module', 'quarantine_key', 'content_sha256',
                                                         'size_bytes', 'classification', 'uploaded_by_user_id', 'created_at');
CREATE TRIGGER trg_stored_objects_set_updated_at BEFORE UPDATE ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_stored_objects_no_delete BEFORE DELETE ON app.stored_objects
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();   -- rows stay as proof of what existed (purged_at)

-- app.upload_sessions — a presigned upload grant. Classification: C1. Retention: OPERATIONAL.
CREATE TABLE app.upload_sessions (
  id                    uuid        NOT NULL,
  bucket_class          text        NOT NULL,
  purpose               text        NOT NULL,
  user_id               uuid,                              -- uploader (user or staff)
  quarantine_key        text        NOT NULL,
  declared_content_type text        NOT NULL,
  declared_size_bytes   bigint      NOT NULL,
  declared_sha256       bytea       NOT NULL,              -- enforced by the store on PUT (checksum header)
  status                text        NOT NULL DEFAULT 'PENDING',
  expires_at            timestamptz NOT NULL,              -- presigned URL expiry
  completed_at          timestamptz,
  stored_object_id      uuid,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_upload_sessions PRIMARY KEY (id),
  CONSTRAINT uq_upload_sessions_quarantine_key UNIQUE (bucket_class, quarantine_key),
  CONSTRAINT uq_upload_sessions_stored_object_id UNIQUE (stored_object_id),
  CONSTRAINT fk_upload_sessions_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT fk_upload_sessions_stored_object FOREIGN KEY (stored_object_id, bucket_class) REFERENCES app.stored_objects (id, bucket_class),
  CONSTRAINT ck_upload_sessions_bucket_class CHECK (bucket_class IN ('PUBLIC_MEDIA', 'PRIVATE_KYC', 'PRIVATE_EVIDENCE')),
  CONSTRAINT ck_upload_sessions_purpose CHECK (
       (bucket_class = 'PUBLIC_MEDIA'     AND purpose IN ('CAMPAIGN_MEDIA', 'CAMPAIGN_UPDATE_MEDIA', 'PROFILE_AVATAR', 'ORGANISATION_LOGO'))
    OR (bucket_class = 'PRIVATE_KYC'      AND purpose IN ('KYC_DOCUMENT', 'KYC_SELFIE', 'KYB_DOCUMENT', 'BENEFICIARY_EVIDENCE'))
    OR (bucket_class = 'PRIVATE_EVIDENCE' AND purpose IN ('CAMPAIGN_SUPPORTING_DOCUMENT', 'DISPUTE_EVIDENCE', 'COMPLIANCE_EVIDENCE',
                                                          'RECONCILIATION_REPORT'))),
  CONSTRAINT ck_upload_sessions_quarantine_key CHECK (quarantine_key ~ '^quarantine/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
  CONSTRAINT ck_upload_sessions_size CHECK (declared_size_bytes > 0),   -- per-purpose maxima are configuration
  CONSTRAINT ck_upload_sessions_sha256 CHECK (octet_length(declared_sha256) = 32),
  CONSTRAINT ck_upload_sessions_status CHECK (status IN ('PENDING', 'COMPLETED', 'EXPIRED', 'ABORTED')),
  CONSTRAINT ck_upload_sessions_completed CHECK ((status = 'COMPLETED') = (stored_object_id IS NOT NULL AND completed_at IS NOT NULL)),
  CONSTRAINT ck_upload_sessions_expiry CHECK (expires_at > created_at)
);
CREATE INDEX ix_upload_sessions_pending_expiry ON app.upload_sessions (expires_at) WHERE status = 'PENDING';
CREATE TRIGGER trg_upload_sessions_cols BEFORE UPDATE ON app.upload_sessions
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'completed_at', 'stored_object_id');
CREATE FUNCTION app.upload_sessions_final() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status <> 'PENDING' THEN
    RAISE EXCEPTION 'upload session % is final (%)', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_upload_sessions_final BEFORE UPDATE ON app.upload_sessions
  FOR EACH ROW EXECUTE FUNCTION app.upload_sessions_final();

-- Deferred FK from 0004.
ALTER TABLE app.user_profiles ADD CONSTRAINT fk_user_profiles_avatar_object
  FOREIGN KEY (avatar_object_id, avatar_bucket_class) REFERENCES app.stored_objects (id, bucket_class);
