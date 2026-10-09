-- Stage 6 work stream M: campaign media (interface-contracts §1, §2, §4, §6; ADR-036). Owner module: campaigns
-- (package internal/campaigns/media). Schema: app.
--
-- Tables: campaign_media (one row per uploaded image; soft-removed, never deleted) and campaign_media_events
-- (append-only timeline, one row per media version, like beneficiary_events / payout_destination_events).
--
-- Pipeline: the original upload is a PUBLIC_MEDIA stored object (purpose CAMPAIGN_MEDIA) in quarantine. After the
-- malware scan is CLEAN the worker decodes it with the Go standard library (dimension limits checked BEFORE
-- decoding: decompression-bomb protection), re-encodes it (dropping EXIF/GPS, comments and every ancillary chunk)
-- and stores the derivative as a NEW PUBLIC_MEDIA object, which goes through the same quarantine and scan. Only the
-- derivative (processed_object_id) is ever served; the original is never served.
--
-- Machine `campaign_media` (Go copy: internal/campaigns/media/media.go Transitions; a unit test compares them):
--   '' -> UPLOADED -> QUARANTINED -> SCANNING -> APPROVED
--   UPLOADED | QUARANTINED | SCANNING -> REJECTED   (scan rejected, decode/limits failed, derivative rejected)
--   any non-removed state -> REMOVED                (owner delete or staff moderation; soft, the row stays)
-- A private object can never become campaign media: both object references are composite foreign keys to
-- app.stored_objects (id, bucket_class) pinned to PUBLIC_MEDIA, and a trigger requires purpose CAMPAIGN_MEDIA and
-- owner module campaigns. At most one non-removed COVER per campaign (unique partial index); "at least one" is a
-- submission/publication gate (MediaInfo.Readiness), not a row constraint.
-- Classification: C1 (C0 once in a published version). Retention: OPERATIONAL (LR-012).
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('campaign_media', '',            'UPLOADED'),
  ('campaign_media', 'UPLOADED',    'QUARANTINED'),
  ('campaign_media', 'UPLOADED',    'REJECTED'),
  ('campaign_media', 'UPLOADED',    'REMOVED'),
  ('campaign_media', 'QUARANTINED', 'SCANNING'),
  ('campaign_media', 'QUARANTINED', 'REJECTED'),
  ('campaign_media', 'QUARANTINED', 'REMOVED'),
  ('campaign_media', 'SCANNING',    'APPROVED'),
  ('campaign_media', 'SCANNING',    'REJECTED'),
  ('campaign_media', 'SCANNING',    'REMOVED'),
  ('campaign_media', 'APPROVED',    'REMOVED'),
  ('campaign_media', 'REJECTED',    'REMOVED');

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_media
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_media (
  id                      uuid        NOT NULL,
  campaign_id             uuid        NOT NULL REFERENCES app.campaigns (id),
  stored_object_id        uuid        NOT NULL,              -- the uploaded original (never served)
  stored_object_bucket    text        NOT NULL DEFAULT 'PUBLIC_MEDIA',
  processed_object_id     uuid,                              -- the re-encoded derivative (the only bytes ever served)
  processed_object_bucket text        NOT NULL DEFAULT 'PUBLIC_MEDIA',
  kind                    text        NOT NULL,
  position                integer     NOT NULL DEFAULT 0,
  alt_text                text        NOT NULL,
  content_type            text,                              -- of the derivative
  width                   integer,
  height                  integer,
  status                  text        NOT NULL,
  rejected_reason         text,
  approved_at             timestamptz,
  removed_by              uuid        REFERENCES app.users (id),
  removed_at              timestamptz,
  removed_reason          text,
  created_by              uuid        NOT NULL REFERENCES app.users (id),
  version                 integer     NOT NULL DEFAULT 1,
  created_at              timestamptz NOT NULL DEFAULT now(),
  updated_at              timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_media PRIMARY KEY (id),
  CONSTRAINT uq_campaign_media_stored_object UNIQUE (stored_object_id),
  CONSTRAINT uq_campaign_media_processed_object UNIQUE (processed_object_id),
  CONSTRAINT fk_campaign_media_stored_object FOREIGN KEY (stored_object_id, stored_object_bucket)
    REFERENCES app.stored_objects (id, bucket_class),
  CONSTRAINT fk_campaign_media_processed_object FOREIGN KEY (processed_object_id, processed_object_bucket)
    REFERENCES app.stored_objects (id, bucket_class),
  CONSTRAINT ck_campaign_media_stored_bucket CHECK (stored_object_bucket = 'PUBLIC_MEDIA'),
  CONSTRAINT ck_campaign_media_processed_bucket CHECK (processed_object_bucket = 'PUBLIC_MEDIA'),
  CONSTRAINT ck_campaign_media_distinct_objects CHECK (processed_object_id IS DISTINCT FROM stored_object_id),
  CONSTRAINT ck_campaign_media_kind CHECK (kind IN ('COVER', 'GALLERY')),
  CONSTRAINT ck_campaign_media_position CHECK (position BETWEEN 0 AND 1000 AND (kind = 'GALLERY' OR position = 0)),
  -- plain text only (the Go layer normalises and refuses HTML; this is the database backstop)
  CONSTRAINT ck_campaign_media_alt_text CHECK (length(alt_text) BETWEEN 3 AND 250 AND alt_text !~ '<\s*/?\s*[A-Za-z!?]'
    AND alt_text !~ '[[:cntrl:]]'),
  CONSTRAINT ck_campaign_media_content_type CHECK (content_type IS NULL OR content_type IN ('image/jpeg', 'image/png')),
  CONSTRAINT ck_campaign_media_dimensions CHECK ((width IS NULL) = (height IS NULL)
    AND (width IS NULL OR (width BETWEEN 1 AND 8000 AND height BETWEEN 1 AND 8000))),
  CONSTRAINT ck_campaign_media_status CHECK (status IN ('UPLOADED', 'QUARANTINED', 'SCANNING', 'APPROVED', 'REJECTED', 'REMOVED')),
  CONSTRAINT ck_campaign_media_approved CHECK (status <> 'APPROVED'
    OR (processed_object_id IS NOT NULL AND width IS NOT NULL AND content_type IS NOT NULL AND approved_at IS NOT NULL)),
  CONSTRAINT ck_campaign_media_rejected CHECK ((status = 'REJECTED') <= (rejected_reason IS NOT NULL)),
  CONSTRAINT ck_campaign_media_rejected_reason CHECK (rejected_reason IS NULL OR rejected_reason IN (
    'MALWARE_DETECTED', 'UPLOAD_REJECTED', 'IMAGE_TYPE_MISMATCH', 'IMAGE_UNSUPPORTED', 'IMAGE_DECODE_FAILED', 'IMAGE_TOO_LARGE',
    'IMAGE_ACTIVE_CONTENT', 'IMAGE_TRAILING_DATA', 'DERIVATIVE_TOO_LARGE', 'DERIVATIVE_REJECTED', 'OBJECT_DELETED')),
  CONSTRAINT ck_campaign_media_removed CHECK ((status = 'REMOVED') = (removed_at IS NOT NULL)
    AND (removed_at IS NULL OR (removed_by IS NOT NULL AND removed_reason ~ '^[A-Z][A-Z0-9_]{2,63}$'))),
  CONSTRAINT ck_campaign_media_version CHECK (version >= 1)
);
CREATE UNIQUE INDEX uq_campaign_media_one_cover ON app.campaign_media (campaign_id) WHERE kind = 'COVER' AND status <> 'REMOVED';
CREATE INDEX ix_campaign_media_campaign ON app.campaign_media (campaign_id, kind, position, created_at);
CREATE INDEX ix_campaign_media_in_progress ON app.campaign_media (updated_at) WHERE status IN ('UPLOADED', 'QUARANTINED', 'SCANNING');

CREATE TRIGGER trg_campaign_media_guard_status BEFORE INSERT OR UPDATE OF status ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('campaign_media');
CREATE TRIGGER trg_campaign_media_bump_version BEFORE UPDATE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaign_media_set_updated_at BEFORE UPDATE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_media_immutable_cols BEFORE UPDATE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'campaign_id', 'stored_object_id', 'stored_object_bucket',
    'processed_object_bucket', 'kind', 'created_by', 'created_at');
CREATE TRIGGER trg_campaign_media_set_once_cols BEFORE UPDATE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.set_once_columns('processed_object_id', 'content_type', 'width', 'height', 'rejected_reason',
    'approved_at', 'removed_by', 'removed_at', 'removed_reason');
CREATE TRIGGER trg_campaign_media_no_delete BEFORE DELETE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaign_media_no_truncate BEFORE TRUNCATE ON app.campaign_media
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Rules the constraints cannot express: both objects must be campaign media objects of the campaigns module (a
-- KYC/evidence object is already impossible through the bucket-pinned composite FK; this also refuses avatars,
-- logos and update media); removed and rejected media are final apart from removal.
CREATE FUNCTION app.campaign_media_rules() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  oid uuid;
  check_ids uuid[];
  p text;
  m text;
BEGIN
  IF TG_OP = 'INSERT' THEN
    check_ids := ARRAY[NEW.stored_object_id, NEW.processed_object_id];
  ELSIF NEW.processed_object_id IS DISTINCT FROM OLD.processed_object_id THEN
    check_ids := ARRAY[NEW.processed_object_id];
  ELSE
    check_ids := ARRAY[]::uuid[];
  END IF;
  FOREACH oid IN ARRAY check_ids LOOP
    CONTINUE WHEN oid IS NULL;
    SELECT purpose, owner_module INTO p, m FROM app.stored_objects WHERE id = oid;
    IF p IS DISTINCT FROM 'CAMPAIGN_MEDIA' OR m IS DISTINCT FROM 'campaigns' THEN
      RAISE EXCEPTION 'campaign media %: object % is not a CAMPAIGN_MEDIA object of the campaigns module', NEW.id, oid
        USING ERRCODE = 'check_violation';
    END IF;
  END LOOP;
  IF TG_OP = 'UPDATE' AND OLD.status = 'REMOVED' THEN
    RAISE EXCEPTION 'campaign media %: removed media is final', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF TG_OP = 'UPDATE' AND OLD.status = 'REJECTED' AND NEW.status = 'REJECTED'
     AND (NEW.position, NEW.alt_text) IS DISTINCT FROM (OLD.position, OLD.alt_text) THEN
    RAISE EXCEPTION 'campaign media %: rejected media cannot be edited', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_media_rules BEFORE INSERT OR UPDATE ON app.campaign_media
  FOR EACH ROW EXECUTE FUNCTION app.campaign_media_rules();

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_media_events — append-only timeline; exactly one row per media version (deferred check).
-- Payloads carry ids and codes only.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_media_events (
  id               uuid        NOT NULL,
  media_id         uuid        NOT NULL REFERENCES app.campaign_media (id),
  media_version    integer     NOT NULL,
  event_type       text        NOT NULL,
  from_status      text,
  to_status        text        NOT NULL,
  actor_type       text        NOT NULL,
  actor_id         uuid        REFERENCES app.users (id),
  reason_code      text,
  note             text,
  correlation_id   text,
  payload          jsonb       NOT NULL DEFAULT '{}',
  occurred_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_media_events PRIMARY KEY (id),
  CONSTRAINT uq_campaign_media_events_version UNIQUE (media_id, media_version),
  CONSTRAINT ck_campaign_media_events_type CHECK (event_type ~ '^[A-Z][A-Z_]{2,39}$'),
  CONSTRAINT ck_campaign_media_events_actor CHECK ((actor_type IN ('USER', 'STAFF') AND actor_id IS NOT NULL)
    OR (actor_type = 'SYSTEM' AND actor_id IS NULL)),
  CONSTRAINT ck_campaign_media_events_staff_reason CHECK (actor_type <> 'STAFF' OR from_status IS NOT DISTINCT FROM to_status
    OR reason_code IS NOT NULL),
  CONSTRAINT ck_campaign_media_events_reason CHECK (reason_code IS NULL OR reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_campaign_media_events_note CHECK (note IS NULL OR length(note) <= 5000),
  CONSTRAINT ck_campaign_media_events_payload CHECK (jsonb_typeof(payload) = 'object')
);
CREATE TRIGGER trg_campaign_media_events_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_media_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION app.campaign_media_require_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v integer; st text;
BEGIN
  SELECT version, status INTO v, st FROM app.campaign_media WHERE id = NEW.id;
  IF NOT EXISTS (SELECT 1 FROM app.campaign_media_events e WHERE e.media_id = NEW.id AND e.media_version = v AND e.to_status = st) THEN
    RAISE EXCEPTION 'campaign media % version % has no matching campaign_media_events row', NEW.id, v USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_campaign_media_require_event AFTER INSERT OR UPDATE ON app.campaign_media
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.campaign_media_require_event();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.campaign_media_events, app.campaign_media;
DROP FUNCTION IF EXISTS app.campaign_media_rules(), app.campaign_media_require_event();
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine = 'campaign_media';
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
