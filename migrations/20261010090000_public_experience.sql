-- Stage 7: public experience (ADR-038). Owner module: campaigns. Schema: app.
--   app.campaign_reports       public reports about campaigns (triage queue; never auto-suspends)
--   app.campaign_featured      staff picks (transparent featured selection; audited)
--   app.public_event_counters  first-party aggregate counters (no IP, no user id, no query text)
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('campaign_report', '',          'OPEN'),
  ('campaign_report', 'OPEN',      'DISMISSED'),
  ('campaign_report', 'OPEN',      'ESCALATED');

-- Reports. Reporter identity: a signed-in reporter's user id, otherwise an HMAC of the client IP (dedupe and
-- rate limiting only; never shown). An optional contact email is stored as a blind index plus ciphertext for
-- staff follow-up and is never returned by list APIs or shown to the campaign owner. Classification: C2.
CREATE TABLE app.campaign_reports (
  id                 uuid        NOT NULL,
  campaign_id        uuid        NOT NULL REFERENCES app.campaigns (id),
  reason             text        NOT NULL,
  details            text,
  reporter_user_id   uuid        REFERENCES app.users (id),
  reporter_key       bytea       NOT NULL,           -- HMAC(user id | client IP); dedupe key
  report_day         date        NOT NULL,
  contact_email_bidx bytea,
  contact_email_ct   bytea,
  status             text        NOT NULL,
  triaged_by         uuid        REFERENCES app.users (id),
  triaged_at         timestamptz,
  triage_reason_code text,
  triage_note        text,
  version            integer     NOT NULL DEFAULT 1,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_campaign_reports PRIMARY KEY (id),
  CONSTRAINT uq_campaign_reports_dedupe UNIQUE (campaign_id, reporter_key, report_day),
  CONSTRAINT ck_campaign_reports_reason CHECK (reason IN ('MISLEADING_INFORMATION', 'IMPERSONATION', 'SUSPICIOUS_ACTIVITY',
             'INAPPROPRIATE_CONTENT', 'PRIVACY_CONCERN', 'OTHER')),
  CONSTRAINT ck_campaign_reports_details CHECK (details IS NULL OR (length(details) BETWEEN 1 AND 2000 AND details !~ '<\s*/?\s*[A-Za-z!?]')),
  CONSTRAINT ck_campaign_reports_other CHECK (reason <> 'OTHER' OR length(details) >= 10),
  CONSTRAINT ck_campaign_reports_contact CHECK ((contact_email_bidx IS NULL) = (contact_email_ct IS NULL)),
  CONSTRAINT ck_campaign_reports_status CHECK (status IN ('OPEN', 'DISMISSED', 'ESCALATED')),
  CONSTRAINT ck_campaign_reports_triaged CHECK ((status = 'OPEN') = (triaged_at IS NULL)
             AND (status = 'OPEN' OR (triaged_by IS NOT NULL AND triage_reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'
                  AND length(triage_note) BETWEEN 3 AND 5000)))
);
CREATE INDEX ix_campaign_reports_queue ON app.campaign_reports (status, created_at, id);
CREATE INDEX ix_campaign_reports_campaign ON app.campaign_reports (campaign_id, created_at);
CREATE TRIGGER trg_campaign_reports_guard_status BEFORE INSERT OR UPDATE OF status ON app.campaign_reports
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('campaign_report');
CREATE TRIGGER trg_campaign_reports_bump_version BEFORE UPDATE ON app.campaign_reports
  FOR EACH ROW EXECUTE FUNCTION app.bump_version();
CREATE TRIGGER trg_campaign_reports_set_updated_at BEFORE UPDATE ON app.campaign_reports
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
-- the report itself is immutable; only triage fields change, once
CREATE TRIGGER trg_campaign_reports_cols BEFORE UPDATE ON app.campaign_reports
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'triaged_by', 'triaged_at', 'triage_reason_code', 'triage_note',
                                                             'version', 'updated_at');
CREATE TRIGGER trg_campaign_reports_no_delete BEFORE DELETE ON app.campaign_reports
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- Staff picks for the featured list (ADR-038 §2). One active pick per campaign; removal keeps history.
CREATE TABLE app.campaign_featured (
  id             uuid        NOT NULL,
  campaign_id    uuid        NOT NULL REFERENCES app.campaigns (id),
  position       integer     NOT NULL,
  note           text        NOT NULL,
  added_by       uuid        NOT NULL REFERENCES app.users (id),
  added_at       timestamptz NOT NULL,
  removed_by     uuid        REFERENCES app.users (id),
  removed_at     timestamptz,
  removal_reason text,
  CONSTRAINT pk_campaign_featured PRIMARY KEY (id),
  CONSTRAINT ck_campaign_featured_position CHECK (position BETWEEN 1 AND 100),
  CONSTRAINT ck_campaign_featured_note CHECK (length(note) BETWEEN 3 AND 500),
  CONSTRAINT ck_campaign_featured_removed CHECK ((removed_at IS NULL) = (removed_by IS NULL)
             AND (removed_at IS NULL OR length(removal_reason) >= 3))
);
CREATE UNIQUE INDEX uq_campaign_featured_active ON app.campaign_featured (campaign_id) WHERE removed_at IS NULL;
CREATE TRIGGER trg_campaign_featured_cols BEFORE UPDATE ON app.campaign_featured
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('removed_by', 'removed_at', 'removal_reason');
CREATE TRIGGER trg_campaign_featured_no_delete BEFORE DELETE ON app.campaign_featured
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- First-party aggregate counters (ADR-038 §4). Nothing identifies a visitor; search text is never stored.
CREATE TABLE app.public_event_counters (
  day        date    NOT NULL,
  event_type text    NOT NULL,
  subject    text    NOT NULL DEFAULT '',   -- campaign slug or category code ('' when none)
  channel    text    NOT NULL DEFAULT '',   -- share channel ('' when none)
  count      bigint  NOT NULL,
  CONSTRAINT pk_public_event_counters PRIMARY KEY (day, event_type, subject, channel),
  CONSTRAINT ck_public_event_counters_type CHECK (event_type IN ('CAMPAIGN_PAGE_VIEW', 'CAMPAIGN_SHARE_CLICK', 'CAMPAIGN_SEARCH', 'CATEGORY_VIEW')),
  CONSTRAINT ck_public_event_counters_channel CHECK (channel IN ('', 'WHATSAPP', 'FACEBOOK', 'X', 'EMAIL', 'COPY_LINK', 'WEB_SHARE',
             'WITH_CATEGORY', 'WITHOUT_CATEGORY')),
  CONSTRAINT ck_public_event_counters_subject CHECK (subject ~ '^[a-zA-Z0-9_-]{0,96}$'),
  CONSTRAINT ck_public_event_counters_count CHECK (count > 0)
);

INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up)
VALUES (md5('permission:campaign.feature')::uuid, 'campaign.feature', 'Add or remove campaigns from the featured staff picks', true, true);
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':campaign.feature')::uuid, md5('role:' || rp.role_code)::uuid,
       md5('permission:campaign.feature')::uuid
FROM (VALUES ('ADMIN'), ('COMPLIANCE')) AS rp(role_code)
ON CONFLICT DO NOTHING;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id = md5('permission:campaign.feature')::uuid;
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code = 'campaign.feature';
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP TABLE IF EXISTS app.public_event_counters, app.campaign_featured, app.campaign_reports;
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine = 'campaign_report';
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
