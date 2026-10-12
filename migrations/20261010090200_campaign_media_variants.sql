-- Stage 7 (public experience): resized JPEG width variants of approved campaign media, for srcset. Owner module:
-- campaigns (package internal/campaigns/media). Schema: app.
--
-- app.campaign_media_variants — one row per (media, width), append-only. Each variant is encoded from the decoded
-- pixels of the approved derivative (so it carries no metadata) and stored as its own PUBLIC_MEDIA object (purpose
-- CAMPAIGN_MEDIA, owner campaigns) through the storage pipeline, so it is quarantined and scanned like the derivative.
-- A row may exist before its object is CLEAN: serving checks the object and falls back to the full derivative.
-- Widths are 320, 640 and 1024 px; a width is never produced at or above the derivative's own width (no upscaling).
-- A variant that fails never blocks or rejects the media.
-- Classification: C1 (C0 once its media is in a published version). Retention: OPERATIONAL (LR-012).
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE app.campaign_media_variants (
  id             uuid        NOT NULL,
  media_id       uuid        NOT NULL REFERENCES app.campaign_media (id),
  width          integer     NOT NULL,
  height         integer     NOT NULL,
  object_id      uuid        NOT NULL,
  object_bucket  text        NOT NULL DEFAULT 'PUBLIC_MEDIA',
  created_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_media_variants PRIMARY KEY (id),
  CONSTRAINT uq_campaign_media_variants_width UNIQUE (media_id, width),
  CONSTRAINT uq_campaign_media_variants_object UNIQUE (object_id),
  -- a private (KYC/evidence) object can never be a variant: bucket-pinned composite foreign key
  CONSTRAINT fk_campaign_media_variants_object FOREIGN KEY (object_id, object_bucket) REFERENCES app.stored_objects (id, bucket_class),
  CONSTRAINT ck_campaign_media_variants_bucket CHECK (object_bucket = 'PUBLIC_MEDIA'),
  CONSTRAINT ck_campaign_media_variants_width CHECK (width IN (320, 640, 1024)),
  CONSTRAINT ck_campaign_media_variants_height CHECK (height BETWEEN 1 AND 8000)
);
CREATE INDEX ix_campaign_media_variants_media ON app.campaign_media_variants (media_id, width);

CREATE TRIGGER trg_campaign_media_variants_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_media_variants
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaign_media_variants_no_truncate BEFORE TRUNCATE ON app.campaign_media_variants
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- The variant object must be a campaign media object of the campaigns module, distinct from the media's original and
-- derivative, and only APPROVED media get variants; never wider than the derivative.
CREATE FUNCTION app.campaign_media_variants_rules() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  p text;
  o text;
  m app.campaign_media%ROWTYPE;
BEGIN
  SELECT purpose, owner_module INTO p, o FROM app.stored_objects WHERE id = NEW.object_id;
  IF p IS DISTINCT FROM 'CAMPAIGN_MEDIA' OR o IS DISTINCT FROM 'campaigns' THEN
    RAISE EXCEPTION 'campaign media variant: object % is not a CAMPAIGN_MEDIA object of the campaigns module', NEW.object_id
      USING ERRCODE = 'check_violation';
  END IF;
  SELECT * INTO m FROM app.campaign_media WHERE id = NEW.media_id;
  IF m.status IS DISTINCT FROM 'APPROVED' THEN
    RAISE EXCEPTION 'campaign media variant: media % is not APPROVED', NEW.media_id USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.object_id IN (m.stored_object_id, m.processed_object_id) THEN
    RAISE EXCEPTION 'campaign media variant: object % is the media''s own original or derivative', NEW.object_id
      USING ERRCODE = 'check_violation';
  END IF;
  IF NEW.width >= m.width THEN
    RAISE EXCEPTION 'campaign media variant: width % would not be smaller than the derivative (%)', NEW.width, m.width
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_media_variants_rules BEFORE INSERT ON app.campaign_media_variants
  FOR EACH ROW EXECUTE FUNCTION app.campaign_media_variants_rules();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.campaign_media_variants;
DROP FUNCTION IF EXISTS app.campaign_media_variants_rules();
-- +goose StatementEnd
