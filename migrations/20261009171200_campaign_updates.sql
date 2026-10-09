-- Stage 6 stream U: campaign updates (owner news posts on a live campaign; interface-contracts §1, §2, §6, §9;
-- campaign-schema §9). Owner module: campaigns (package internal/campaigns/updates). Schema: app.
--
-- Tables:
--   * app.campaign_updates       — the update (plain text title/body, optional media references), lifecycle
--                                  machine campaign_update. Never hard-deleted: owner delete is status DELETED.
--   * app.campaign_update_events — append-only history, exactly one row per campaign_updates version (deferred
--                                  constraint, same pattern as campaign_status_history).
-- Self-moderation: a moderator who approves or hides an update must not be the author, the campaign's owner or
-- creator, a member of the owning organisation, or linked to any of them (app.actor_identities, ADR-037 §4).
-- media_ids reference app.campaign_media ids of the same campaign; they are checked against the APPROVED media at
-- publication time in Go (the campaign_media table belongs to stream M and is created by its own migration).
-- Classification: C1 while unpublished, C0 once PUBLISHED; moderator notes and hidden_note C2 (staff only).
-- Retention: account/campaign lifetime. Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

-- Lifecycle (machine campaign_update). The Go edge list in internal/campaigns/updates/updates.go must stay
-- identical (unit test parses this file). DELETED is terminal; HIDDEN is staff-only and leaves only to DELETED.
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('campaign_update', '',                   'DRAFT'),
  ('campaign_update', '',                   'PENDING_MODERATION'),
  ('campaign_update', '',                   'PUBLISHED'),
  ('campaign_update', 'DRAFT',              'PENDING_MODERATION'),
  ('campaign_update', 'DRAFT',              'PUBLISHED'),
  ('campaign_update', 'DRAFT',              'DELETED'),
  ('campaign_update', 'PENDING_MODERATION', 'PUBLISHED'),
  ('campaign_update', 'PENDING_MODERATION', 'HIDDEN'),
  ('campaign_update', 'PENDING_MODERATION', 'DELETED'),
  ('campaign_update', 'PUBLISHED',          'HIDDEN'),
  ('campaign_update', 'PUBLISHED',          'DELETED'),
  ('campaign_update', 'HIDDEN',             'DELETED');

CREATE TABLE app.campaign_updates (
  id                  uuid        NOT NULL,
  campaign_id         uuid        NOT NULL,
  author_id           uuid        NOT NULL,
  title               text        NOT NULL,
  body                text        NOT NULL,
  media_ids           uuid[]      NOT NULL DEFAULT '{}',
  status              text        NOT NULL,
  moderation_reasons  text[]      NOT NULL DEFAULT '{}',   -- why it was pre-moderated (codes only)
  submitted_at        timestamptz,                         -- owner asked to publish
  published_at        timestamptz,
  approved_by         uuid,                                -- moderator who approved a pre-moderated update
  approved_at         timestamptz,
  hidden_by           uuid,
  hidden_at           timestamptz,
  hidden_reason       text,
  hidden_note         text,
  deleted_by          uuid,
  deleted_at          timestamptz,
  version             integer     NOT NULL DEFAULT 1,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_updates PRIMARY KEY (id),
  CONSTRAINT fk_campaign_updates_campaign FOREIGN KEY (campaign_id) REFERENCES app.campaigns (id),
  CONSTRAINT fk_campaign_updates_author FOREIGN KEY (author_id) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_updates_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_updates_hidden_by FOREIGN KEY (hidden_by) REFERENCES app.users (id),
  CONSTRAINT fk_campaign_updates_deleted_by FOREIGN KEY (deleted_by) REFERENCES app.users (id),
  CONSTRAINT ck_campaign_updates_status CHECK (status IN ('DRAFT', 'PENDING_MODERATION', 'PUBLISHED', 'HIDDEN', 'DELETED')),
  -- coarse bounds; the APPROVED campaign policy (content.update_title / update_body) is enforced in Go
  CONSTRAINT ck_campaign_updates_title CHECK (length(title) BETWEEN 1 AND 200),
  CONSTRAINT ck_campaign_updates_body CHECK (length(body) BETWEEN 1 AND 20000),
  -- plain text only (defence in depth for interface-contracts §9): no tags, no C0 controls other than \n in the body
  CONSTRAINT ck_campaign_updates_plain CHECK (title !~ '<[[:space:]]*[/!?A-Za-z]' AND body !~ '<[[:space:]]*[/!?A-Za-z]'),
  CONSTRAINT ck_campaign_updates_controls CHECK (title !~ '[\x01-\x1f\x7f]' AND body !~ '[\x01-\x09\x0b-\x1f\x7f]'),
  CONSTRAINT ck_campaign_updates_media CHECK (cardinality(media_ids) <= 10 AND array_position(media_ids, NULL) IS NULL),
  CONSTRAINT ck_campaign_updates_moderation CHECK (moderation_reasons <@ ARRAY['CATEGORY_POLICY', 'TIER_POLICY', 'OWNER_RESTRICTED']::text[]),
  CONSTRAINT ck_campaign_updates_submitted CHECK (status = 'DRAFT' OR submitted_at IS NOT NULL OR status = 'DELETED'),
  CONSTRAINT ck_campaign_updates_pending CHECK (status <> 'PENDING_MODERATION' OR cardinality(moderation_reasons) > 0),
  CONSTRAINT ck_campaign_updates_published CHECK (status <> 'PUBLISHED' OR published_at IS NOT NULL),
  CONSTRAINT ck_campaign_updates_approved CHECK ((approved_by IS NULL) = (approved_at IS NULL)
             AND (approved_by IS NULL OR published_at IS NOT NULL)),
  CONSTRAINT ck_campaign_updates_hidden CHECK ((hidden_at IS NULL) = (hidden_by IS NULL)
             AND (hidden_at IS NULL) = (hidden_reason IS NULL)
             AND (status <> 'HIDDEN' OR hidden_at IS NOT NULL)
             AND (hidden_reason IS NULL OR hidden_reason ~ '^[A-Z][A-Z0-9_]{2,63}$')
             AND (hidden_note IS NULL OR length(hidden_note) BETWEEN 3 AND 5000)),
  CONSTRAINT ck_campaign_updates_deleted CHECK ((status = 'DELETED') = (deleted_at IS NOT NULL)
             AND (deleted_at IS NULL) = (deleted_by IS NULL))
);
CREATE INDEX ix_campaign_updates_campaign ON app.campaign_updates (campaign_id, created_at DESC, id DESC) WHERE status <> 'DELETED';
CREATE INDEX ix_campaign_updates_public ON app.campaign_updates (campaign_id, published_at DESC, id DESC) WHERE status = 'PUBLISHED';
CREATE INDEX ix_campaign_updates_moderation ON app.campaign_updates (id) WHERE status = 'PENDING_MODERATION';

CREATE TRIGGER trg_campaign_updates_guard_status BEFORE INSERT OR UPDATE OF status ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('campaign_update');
CREATE TRIGGER trg_campaign_updates_bump_version BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaign_updates_set_updated_at BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_campaign_updates_keep_created_at BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_campaign_updates_immutable_cols BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'campaign_id', 'author_id', 'created_at');
CREATE TRIGGER trg_campaign_updates_no_delete BEFORE DELETE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaign_updates_no_truncate BEFORE TRUNCATE ON app.campaign_updates
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Content is editable only while DRAFT or PENDING_MODERATION; publication, approval and hiding facts are set once.
CREATE FUNCTION app.campaign_updates_rules() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.status NOT IN ('DRAFT', 'PENDING_MODERATION')
     AND (NEW.title, NEW.body, NEW.media_ids) IS DISTINCT FROM (OLD.title, OLD.body, OLD.media_ids) THEN
    RAISE EXCEPTION 'campaign update %: content cannot change in status %', OLD.id, OLD.status USING ERRCODE = 'restrict_violation';
  END IF;
  IF (OLD.published_at IS NOT NULL AND NEW.published_at IS DISTINCT FROM OLD.published_at)
     OR (OLD.approved_by IS NOT NULL AND (NEW.approved_by, NEW.approved_at) IS DISTINCT FROM (OLD.approved_by, OLD.approved_at))
     OR (OLD.hidden_at IS NOT NULL AND (NEW.hidden_by, NEW.hidden_at, NEW.hidden_reason, NEW.hidden_note)
                                    IS DISTINCT FROM (OLD.hidden_by, OLD.hidden_at, OLD.hidden_reason, OLD.hidden_note)) THEN
    RAISE EXCEPTION 'campaign update %: publication, approval and hiding are recorded once', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_campaign_updates_rules BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.campaign_updates_rules();

-- Nobody moderates content that concerns them (ADR-037 §4): the approver or hider must not be (or be linked to)
-- the author, the campaign's personal owner or creator, or a member of the owning organisation. SECURITY DEFINER
-- so the check sees links and memberships whatever the caller's grants.
CREATE FUNCTION app.campaign_updates_not_self() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER
  SET search_path = pg_catalog, app AS $$
DECLARE
  c app.campaigns%ROWTYPE;
  actor uuid;
  ident uuid;
BEGIN
  IF NEW.approved_by IS NOT DISTINCT FROM OLD.approved_by AND NEW.hidden_by IS NOT DISTINCT FROM OLD.hidden_by THEN
    RETURN NEW;
  END IF;
  SELECT * INTO c FROM app.campaigns WHERE id = NEW.campaign_id;
  FOREACH actor IN ARRAY ARRAY[NEW.approved_by, NEW.hidden_by] LOOP
    CONTINUE WHEN actor IS NULL;
    FOREACH ident IN ARRAY app.actor_identities(actor) LOOP
      IF ident = NEW.author_id OR ident = c.owner_user_id OR ident = c.created_by
         OR (c.owner_organisation_id IS NOT NULL AND EXISTS (SELECT 1 FROM app.organisation_members m
               WHERE m.organisation_id = c.owner_organisation_id AND m.user_id = ident AND m.removed_at IS NULL)) THEN
        RAISE EXCEPTION 'campaign update %: % may not moderate content that concerns them', NEW.id, actor USING ERRCODE = 'check_violation';
      END IF;
    END LOOP;
  END LOOP;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION app.campaign_updates_not_self() FROM PUBLIC;
CREATE TRIGGER trg_campaign_updates_not_self BEFORE UPDATE ON app.campaign_updates
  FOR EACH ROW EXECUTE FUNCTION app.campaign_updates_not_self();

-- ---------------------------------------------------------------------------------------------------------
-- app.campaign_update_events — append-only history, one row per campaign_updates version.
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE app.campaign_update_events (
  id               uuid        NOT NULL,
  update_id        uuid        NOT NULL REFERENCES app.campaign_updates (id),
  campaign_id      uuid        NOT NULL REFERENCES app.campaigns (id),
  update_version   integer     NOT NULL,
  event_type       text        NOT NULL,
  from_status      text,
  to_status        text        NOT NULL,
  actor_type       text        NOT NULL,
  actor_id         uuid        REFERENCES app.users (id),
  reason_code      text,
  note             text,
  correlation_id   text,
  occurred_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_update_events PRIMARY KEY (id),
  CONSTRAINT uq_campaign_update_events_version UNIQUE (update_id, update_version),
  CONSTRAINT ck_campaign_update_events_type CHECK (event_type IN ('CREATED', 'EDITED', 'SUBMITTED', 'PUBLISHED', 'APPROVED', 'HIDDEN', 'DELETED')),
  CONSTRAINT ck_campaign_update_events_actor CHECK ((actor_type IN ('USER', 'STAFF') AND actor_id IS NOT NULL)
             OR (actor_type = 'SYSTEM' AND actor_id IS NULL)),
  CONSTRAINT ck_campaign_update_events_staff_reason CHECK (actor_type <> 'STAFF' OR reason_code IS NOT NULL),
  CONSTRAINT ck_campaign_update_events_reason CHECK (reason_code IS NULL OR reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_campaign_update_events_note CHECK (note IS NULL OR length(note) BETWEEN 3 AND 5000)
);
CREATE INDEX ix_campaign_update_events_update ON app.campaign_update_events (update_id, update_version);
CREATE TRIGGER trg_campaign_update_events_no_mutation BEFORE UPDATE OR DELETE ON app.campaign_update_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_campaign_update_events_no_truncate BEFORE TRUNCATE ON app.campaign_update_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION app.campaign_updates_require_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v integer; st text;
BEGIN
  SELECT version, status INTO v, st FROM app.campaign_updates WHERE id = NEW.id;
  IF NOT EXISTS (SELECT 1 FROM app.campaign_update_events e WHERE e.update_id = NEW.id AND e.update_version = v AND e.to_status = st) THEN
    RAISE EXCEPTION 'campaign update % version % has no matching campaign_update_events row', NEW.id, v USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_campaign_updates_require_event AFTER INSERT OR UPDATE ON app.campaign_updates
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION app.campaign_updates_require_event();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.campaign_update_events, app.campaign_updates CASCADE;
DROP FUNCTION IF EXISTS app.campaign_updates_rules(), app.campaign_updates_not_self(), app.campaign_updates_require_event();
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine = 'campaign_update';
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
