-- Stage 14 (ADR-046): the operations module's own records — support cases, operational incidents, exception triage
-- (assignment and notes only; resolution authority stays with the owning module), staff access reviews and internal SLA
-- targets (maker-checker). Every lifecycle is database-guarded (app.guard_transition) and every history table is
-- append-only. Owner module: operations (schema app, tables app.ops_*). Runs as fundzim_migrator. Forward-only in
-- production (ADR-028); Down is for development.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

-- -----------------------------------------------------------------------------------------------------
-- 1. Support cases (machine 'ops_support_case'). Distinct from risk and compliance investigations: a support case
--    never holds risk evidence, compliance notes, KYC documents or account numbers.
-- -----------------------------------------------------------------------------------------------------
CREATE SEQUENCE app.ops_support_case_number_seq;
CREATE TABLE app.ops_support_cases (
  id                    uuid        NOT NULL,
  case_number           bigint      NOT NULL DEFAULT nextval('app.ops_support_case_number_seq'),
  subject               text        NOT NULL,
  category              text        NOT NULL,
  priority              text        NOT NULL,
  status                text        NOT NULL,
  requester_user_id     uuid,
  assigned_to           uuid,
  related_campaign_id   uuid,
  related_reference_type text,
  related_reference_id  text,
  escalated_to          text,
  escalation_reference  text,
  resolution            text,
  resolved_at           timestamptz,
  due_at                timestamptz,
  created_by            uuid        NOT NULL,
  version               integer     NOT NULL DEFAULT 1,
  created_at            timestamptz NOT NULL DEFAULT now(),
  updated_at            timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ops_support_cases PRIMARY KEY (id),
  CONSTRAINT uq_ops_support_cases_number UNIQUE (case_number),
  CONSTRAINT fk_ops_support_cases_requester FOREIGN KEY (requester_user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_support_cases_assignee FOREIGN KEY (assigned_to) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_support_cases_creator FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_support_cases_subject CHECK (char_length(btrim(subject)) BETWEEN 3 AND 200),
  CONSTRAINT ck_ops_support_cases_category CHECK (category IN ('GENERAL','ACCOUNT','CAMPAIGN','DONATION','VERIFICATION','WITHDRAWAL','TECHNICAL')),
  CONSTRAINT ck_ops_support_cases_priority CHECK (priority IN ('P1','P2','P3','P4')),
  CONSTRAINT ck_ops_support_cases_status CHECK (status IN ('OPEN','TRIAGE','IN_PROGRESS','WAITING_FOR_CUSTOMER','ESCALATED','RESOLVED','CLOSED','REOPENED')),
  CONSTRAINT ck_ops_support_cases_escalation CHECK ((status = 'ESCALATED') <= (escalated_to IS NOT NULL) AND (escalated_to IS NULL OR escalated_to IN
    ('CAMPAIGN_MODERATION','VERIFICATION','RISK_COMPLIANCE','PAYMENTS','PAYOUTS','FINANCIAL_OPERATIONS','TECHNICAL_OPERATIONS'))),
  CONSTRAINT ck_ops_support_cases_resolved CHECK (status NOT IN ('RESOLVED','CLOSED') OR (resolution IS NOT NULL AND resolved_at IS NOT NULL)),
  CONSTRAINT ck_ops_support_cases_resolution CHECK (resolution IS NULL OR char_length(resolution) <= 2000),
  CONSTRAINT ck_ops_support_cases_reference CHECK ((related_reference_type IS NULL) = (related_reference_id IS NULL)
    AND (related_reference_id IS NULL OR char_length(related_reference_id) <= 200)),
  CONSTRAINT ck_ops_support_cases_version CHECK (version >= 1)
);
CREATE INDEX ix_ops_support_cases_queue ON app.ops_support_cases (status, priority, created_at);
CREATE INDEX ix_ops_support_cases_assignee ON app.ops_support_cases (assigned_to) WHERE status NOT IN ('RESOLVED','CLOSED');
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ops_support_case', '',                     'OPEN'),
  ('ops_support_case', 'OPEN',                 'TRIAGE'),
  ('ops_support_case', 'REOPENED',             'TRIAGE'),
  ('ops_support_case', 'TRIAGE',               'IN_PROGRESS'),
  ('ops_support_case', 'IN_PROGRESS',          'WAITING_FOR_CUSTOMER'),
  ('ops_support_case', 'WAITING_FOR_CUSTOMER', 'IN_PROGRESS'),
  ('ops_support_case', 'TRIAGE',               'ESCALATED'),
  ('ops_support_case', 'IN_PROGRESS',          'ESCALATED'),
  ('ops_support_case', 'ESCALATED',            'IN_PROGRESS'),
  ('ops_support_case', 'TRIAGE',               'RESOLVED'),
  ('ops_support_case', 'IN_PROGRESS',          'RESOLVED'),
  ('ops_support_case', 'ESCALATED',            'RESOLVED'),
  ('ops_support_case', 'WAITING_FOR_CUSTOMER', 'RESOLVED'),
  ('ops_support_case', 'RESOLVED',             'CLOSED'),
  ('ops_support_case', 'RESOLVED',             'REOPENED'),
  ('ops_support_case', 'CLOSED',               'REOPENED');
CREATE TRIGGER trg_ops_support_cases_guard_status BEFORE INSERT OR UPDATE OF status ON app.ops_support_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ops_support_case');
CREATE TRIGGER trg_ops_support_cases_identity BEFORE UPDATE ON app.ops_support_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('case_number', 'created_by', 'created_at', 'requester_user_id');
CREATE TRIGGER trg_ops_support_cases_updated_at BEFORE UPDATE ON app.ops_support_cases FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_ops_support_cases_no_delete BEFORE DELETE ON app.ops_support_cases FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.ops_support_events (
  id          uuid        NOT NULL,
  case_id     uuid        NOT NULL,
  event_type  text        NOT NULL,
  actor_id    uuid        NOT NULL,
  from_status text,
  to_status   text,
  note        text,
  detail      jsonb       NOT NULL DEFAULT '{}',
  occurred_at timestamptz NOT NULL,
  CONSTRAINT pk_ops_support_events PRIMARY KEY (id),
  CONSTRAINT fk_ops_support_events_case FOREIGN KEY (case_id) REFERENCES app.ops_support_cases (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_support_events_type CHECK (event_type ~ '^[A-Z][A-Z_]{2,60}$'),
  CONSTRAINT ck_ops_support_events_note CHECK (note IS NULL OR char_length(note) <= 4000),
  CONSTRAINT ck_ops_support_events_detail CHECK (jsonb_typeof(detail) = 'object')
);
CREATE INDEX ix_ops_support_events_case ON app.ops_support_events (case_id, occurred_at, id);
CREATE TRIGGER trg_ops_support_events_no_mutation BEFORE UPDATE OR DELETE ON app.ops_support_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 2. Operational incidents (machine 'ops_incident'), with an append-only timeline.
-- -----------------------------------------------------------------------------------------------------
CREATE SEQUENCE app.ops_incident_number_seq;
CREATE TABLE app.ops_incidents (
  id                 uuid        NOT NULL,
  incident_number    bigint      NOT NULL DEFAULT nextval('app.ops_incident_number_seq'),
  title              text        NOT NULL,
  severity           text        NOT NULL,
  affected_module    text        NOT NULL,
  status             text        NOT NULL,
  owner_id           uuid,
  started_at         timestamptz NOT NULL,
  detected_by        text        NOT NULL,
  alert_key          text,
  resolution         text,
  resolved_at        timestamptz,
  post_incident_notes text,
  created_by         uuid        NOT NULL,
  version            integer     NOT NULL DEFAULT 1,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ops_incidents PRIMARY KEY (id),
  CONSTRAINT uq_ops_incidents_number UNIQUE (incident_number),
  CONSTRAINT fk_ops_incidents_owner FOREIGN KEY (owner_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_incidents_creator FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_incidents_title CHECK (char_length(btrim(title)) BETWEEN 3 AND 200),
  CONSTRAINT ck_ops_incidents_severity CHECK (severity IN ('SEV1','SEV2','SEV3','SEV4')),
  CONSTRAINT ck_ops_incidents_module CHECK (affected_module ~ '^[a-z][a-z_]{1,40}$'),
  CONSTRAINT ck_ops_incidents_status CHECK (status IN ('DETECTED','ACKNOWLEDGED','INVESTIGATING','MITIGATING','MONITORING','RESOLVED','CLOSED')),
  CONSTRAINT ck_ops_incidents_detected_by CHECK (detected_by IN ('STAFF','ALERT')),
  CONSTRAINT ck_ops_incidents_alert CHECK ((detected_by = 'ALERT') = (alert_key IS NOT NULL)),
  CONSTRAINT ck_ops_incidents_resolved CHECK (status NOT IN ('RESOLVED','CLOSED') OR (resolution IS NOT NULL AND resolved_at IS NOT NULL)),
  CONSTRAINT ck_ops_incidents_texts CHECK ((resolution IS NULL OR char_length(resolution) <= 4000) AND
    (post_incident_notes IS NULL OR char_length(post_incident_notes) <= 8000))
);
-- one open incident per alert (an alert that keeps firing links to it)
CREATE UNIQUE INDEX uq_ops_incidents_open_alert ON app.ops_incidents (alert_key) WHERE alert_key IS NOT NULL AND status NOT IN ('RESOLVED','CLOSED');
CREATE INDEX ix_ops_incidents_open ON app.ops_incidents (status, severity, started_at);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ops_incident', '',              'DETECTED'),
  ('ops_incident', 'DETECTED',      'ACKNOWLEDGED'),
  ('ops_incident', 'ACKNOWLEDGED',  'INVESTIGATING'),
  ('ops_incident', 'INVESTIGATING', 'MITIGATING'),
  ('ops_incident', 'INVESTIGATING', 'MONITORING'),
  ('ops_incident', 'MITIGATING',    'MONITORING'),
  ('ops_incident', 'MONITORING',    'INVESTIGATING'),
  ('ops_incident', 'MONITORING',    'RESOLVED'),
  ('ops_incident', 'MITIGATING',    'RESOLVED'),
  ('ops_incident', 'INVESTIGATING', 'RESOLVED'),
  ('ops_incident', 'RESOLVED',      'CLOSED');
CREATE TRIGGER trg_ops_incidents_guard_status BEFORE INSERT OR UPDATE OF status ON app.ops_incidents
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ops_incident');
CREATE TRIGGER trg_ops_incidents_identity BEFORE UPDATE ON app.ops_incidents
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('incident_number', 'created_by', 'created_at', 'detected_by', 'alert_key', 'started_at');
CREATE TRIGGER trg_ops_incidents_updated_at BEFORE UPDATE ON app.ops_incidents FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_ops_incidents_no_delete BEFORE DELETE ON app.ops_incidents FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.ops_incident_events (
  id          uuid        NOT NULL,
  incident_id uuid        NOT NULL,
  event_type  text        NOT NULL,
  actor_id    uuid,
  from_status text,
  to_status   text,
  note        text,
  detail      jsonb       NOT NULL DEFAULT '{}',
  occurred_at timestamptz NOT NULL,
  CONSTRAINT pk_ops_incident_events PRIMARY KEY (id),
  CONSTRAINT fk_ops_incident_events_incident FOREIGN KEY (incident_id) REFERENCES app.ops_incidents (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_incident_events_type CHECK (event_type ~ '^[A-Z][A-Z_]{2,60}$'),
  CONSTRAINT ck_ops_incident_events_note CHECK (note IS NULL OR char_length(note) <= 4000),
  CONSTRAINT ck_ops_incident_events_detail CHECK (jsonb_typeof(detail) = 'object')
);
CREATE INDEX ix_ops_incident_events_incident ON app.ops_incident_events (incident_id, occurred_at, id);
CREATE TRIGGER trg_ops_incident_events_no_mutation BEFORE UPDATE OR DELETE ON app.ops_incident_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 3. Exception triage (machine 'ops_exception'): the operations workflow around a domain exception, keyed by
--    (source module, source reference). It never resolves the domain record — the owning module does.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.ops_exception_assignments (
  id            uuid        NOT NULL,
  source_module text        NOT NULL,
  source_ref    text        NOT NULL,
  status        text        NOT NULL,
  assigned_to   uuid,
  severity      text        NOT NULL,
  category      text        NOT NULL,
  summary       text        NOT NULL,
  -- the permission the owning module requires to see the exception: triage records stay visible only to holders,
  -- also after the module stops reporting the exception
  view_permission text      NOT NULL,
  resolution    text,
  version       integer     NOT NULL DEFAULT 1,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ops_exception_assignments PRIMARY KEY (id),
  CONSTRAINT uq_ops_exception_assignments_source UNIQUE (source_module, source_ref),
  CONSTRAINT fk_ops_exception_assignments_assignee FOREIGN KEY (assigned_to) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_exception_assignments_module CHECK (source_module ~ '^[a-z][a-z_]{1,40}$'),
  CONSTRAINT ck_ops_exception_assignments_ref CHECK (char_length(source_ref) BETWEEN 1 AND 200),
  CONSTRAINT ck_ops_exception_assignments_status CHECK (status IN ('OPEN','ACKNOWLEDGED','INVESTIGATING','WAITING_EXTERNAL','RESOLVED','CLOSED','REOPENED')),
  CONSTRAINT ck_ops_exception_assignments_severity CHECK (severity IN ('SEV1','SEV2','SEV3','SEV4')),
  CONSTRAINT ck_ops_exception_assignments_permission CHECK (view_permission ~ '^[a-z_]+(\.[a-z_]+)+$'),
  CONSTRAINT ck_ops_exception_assignments_resolved CHECK (status NOT IN ('RESOLVED','CLOSED') OR resolution IS NOT NULL),
  CONSTRAINT ck_ops_exception_assignments_texts CHECK (char_length(summary) <= 300 AND (resolution IS NULL OR char_length(resolution) <= 2000))
);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ops_exception', '',                 'OPEN'),
  ('ops_exception', 'OPEN',             'ACKNOWLEDGED'),
  ('ops_exception', 'REOPENED',         'ACKNOWLEDGED'),
  ('ops_exception', 'ACKNOWLEDGED',     'INVESTIGATING'),
  ('ops_exception', 'INVESTIGATING',    'WAITING_EXTERNAL'),
  ('ops_exception', 'WAITING_EXTERNAL', 'INVESTIGATING'),
  ('ops_exception', 'ACKNOWLEDGED',     'RESOLVED'),
  ('ops_exception', 'INVESTIGATING',    'RESOLVED'),
  ('ops_exception', 'WAITING_EXTERNAL', 'RESOLVED'),
  ('ops_exception', 'RESOLVED',         'CLOSED'),
  ('ops_exception', 'RESOLVED',         'REOPENED'),
  ('ops_exception', 'CLOSED',           'REOPENED');
CREATE TRIGGER trg_ops_exception_assignments_guard_status BEFORE INSERT OR UPDATE OF status ON app.ops_exception_assignments
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ops_exception');
CREATE TRIGGER trg_ops_exception_assignments_identity BEFORE UPDATE ON app.ops_exception_assignments
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('source_module', 'source_ref', 'view_permission', 'created_at');
CREATE TRIGGER trg_ops_exception_assignments_updated_at BEFORE UPDATE ON app.ops_exception_assignments FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_ops_exception_assignments_no_delete BEFORE DELETE ON app.ops_exception_assignments FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.ops_exception_events (
  id            uuid        NOT NULL,
  assignment_id uuid        NOT NULL,
  event_type    text        NOT NULL,
  actor_id      uuid        NOT NULL,
  from_status   text,
  to_status     text,
  note          text,
  occurred_at   timestamptz NOT NULL,
  CONSTRAINT pk_ops_exception_events PRIMARY KEY (id),
  CONSTRAINT fk_ops_exception_events_assignment FOREIGN KEY (assignment_id) REFERENCES app.ops_exception_assignments (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_exception_events_type CHECK (event_type ~ '^[A-Z][A-Z_]{2,60}$'),
  CONSTRAINT ck_ops_exception_events_note CHECK (note IS NULL OR char_length(note) <= 4000)
);
CREATE INDEX ix_ops_exception_events_assignment ON app.ops_exception_events (assignment_id, occurred_at, id);
CREATE TRIGGER trg_ops_exception_events_no_mutation BEFORE UPDATE OR DELETE ON app.ops_exception_events FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 4. Staff access reviews: a review campaign over the role inventory, a reviewer who is not its creator, one item per
--    (staff member, role); a "revoke" recommendation becomes an ordinary dual-controlled role REVOKE request. No review
--    frequency is invented.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.ops_access_reviews (
  id           uuid        NOT NULL,
  title        text        NOT NULL,
  scope_role   text,
  status       text        NOT NULL,
  created_by   uuid        NOT NULL,
  reviewer_id  uuid        NOT NULL,
  due_at       timestamptz,
  completed_at timestamptz,
  version      integer     NOT NULL DEFAULT 1,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ops_access_reviews PRIMARY KEY (id),
  CONSTRAINT fk_ops_access_reviews_creator FOREIGN KEY (created_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_access_reviews_reviewer FOREIGN KEY (reviewer_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_access_reviews_title CHECK (char_length(btrim(title)) BETWEEN 3 AND 200),
  CONSTRAINT ck_ops_access_reviews_status CHECK (status IN ('OPEN','COMPLETED','CANCELLED')),
  CONSTRAINT ck_ops_access_reviews_separation CHECK (reviewer_id <> created_by),
  CONSTRAINT ck_ops_access_reviews_completed CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL))
);
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ops_access_review', '',     'OPEN'),
  ('ops_access_review', 'OPEN', 'COMPLETED'),
  ('ops_access_review', 'OPEN', 'CANCELLED');
CREATE TRIGGER trg_ops_access_reviews_guard_status BEFORE INSERT OR UPDATE OF status ON app.ops_access_reviews
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ops_access_review');
CREATE TRIGGER trg_ops_access_reviews_identity BEFORE UPDATE ON app.ops_access_reviews
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('created_by', 'reviewer_id', 'created_at', 'scope_role');
CREATE TRIGGER trg_ops_access_reviews_updated_at BEFORE UPDATE ON app.ops_access_reviews FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_ops_access_reviews_no_delete BEFORE DELETE ON app.ops_access_reviews FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE TABLE app.ops_access_review_items (
  id                uuid        NOT NULL,
  review_id         uuid        NOT NULL,
  staff_user_id     uuid        NOT NULL,
  role_code         text        NOT NULL,
  decision          text        NOT NULL DEFAULT 'PENDING',
  decided_by        uuid,
  decided_at        timestamptz,
  reason            text,
  revoke_request_id uuid,
  CONSTRAINT pk_ops_access_review_items PRIMARY KEY (id),
  CONSTRAINT fk_ops_access_review_items_review FOREIGN KEY (review_id) REFERENCES app.ops_access_reviews (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_access_review_items_staff FOREIGN KEY (staff_user_id) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_access_review_items_decider FOREIGN KEY (decided_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT uq_ops_access_review_items UNIQUE (review_id, staff_user_id, role_code),
  CONSTRAINT ck_ops_access_review_items_decision CHECK (decision IN ('PENDING','CERTIFIED','REVOKE_RECOMMENDED')),
  CONSTRAINT ck_ops_access_review_items_decided CHECK ((decision = 'PENDING') = (decided_by IS NULL) AND (decided_by IS NULL OR
    (decided_at IS NOT NULL AND reason IS NOT NULL AND decided_by <> staff_user_id))),
  CONSTRAINT ck_ops_access_review_items_reason CHECK (reason IS NULL OR char_length(reason) BETWEEN 5 AND 2000)
);
CREATE INDEX ix_ops_access_review_items_review ON app.ops_access_review_items (review_id, decision);
-- a decision is final (a later change is a new review)
CREATE FUNCTION app.ops_access_review_items_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.decision <> 'PENDING' AND (NEW.decision, NEW.decided_by, NEW.decided_at, NEW.reason) IS DISTINCT FROM
     (OLD.decision, OLD.decided_by, OLD.decided_at, OLD.reason) THEN
    RAISE EXCEPTION 'access review item % is already decided', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF (NEW.review_id, NEW.staff_user_id, NEW.role_code) IS DISTINCT FROM (OLD.review_id, OLD.staff_user_id, OLD.role_code) THEN
    RAISE EXCEPTION 'access review item % identity is immutable', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.decided_by IS NOT NULL AND NEW.staff_user_id = ANY (app.actor_identities(NEW.decided_by)) THEN
    RAISE EXCEPTION 'conflict of interest: % may not certify their own access', NEW.decided_by USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_ops_access_review_items_guard BEFORE UPDATE ON app.ops_access_review_items
  FOR EACH ROW EXECUTE FUNCTION app.ops_access_review_items_guard();
CREATE TRIGGER trg_ops_access_review_items_no_delete BEFORE DELETE ON app.ops_access_review_items FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 5. Internal SLA targets (operational targets only — never contractual or regulatory): versioned, maker-checker.
--    None is seeded: reports show NOT_CONFIGURED until two people approve a target.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.ops_sla_targets (
  id             uuid        NOT NULL,
  metric_key     text        NOT NULL,
  target_seconds integer     NOT NULL,
  status         text        NOT NULL,
  reason         text        NOT NULL,
  proposed_by    uuid        NOT NULL,
  proposed_at    timestamptz NOT NULL,
  decided_by     uuid,
  decided_at     timestamptz,
  decision_reason text,
  version        integer     NOT NULL DEFAULT 1,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_ops_sla_targets PRIMARY KEY (id),
  CONSTRAINT fk_ops_sla_targets_proposer FOREIGN KEY (proposed_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT fk_ops_sla_targets_decider FOREIGN KEY (decided_by) REFERENCES app.users (id) ON DELETE RESTRICT,
  CONSTRAINT ck_ops_sla_targets_metric CHECK (metric_key IN ('VERIFICATION_REVIEW_TURNAROUND','CAMPAIGN_MODERATION_TURNAROUND','SUPPORT_FIRST_RESPONSE',
    'SUPPORT_RESOLUTION','RISK_INVESTIGATION_AGE','APPROVAL_PROCESSING','EXCEPTION_RESOLUTION','INCIDENT_ACKNOWLEDGEMENT')),
  CONSTRAINT ck_ops_sla_targets_seconds CHECK (target_seconds BETWEEN 60 AND 31536000),
  CONSTRAINT ck_ops_sla_targets_status CHECK (status IN ('PROPOSED','ACTIVE','REJECTED','SUPERSEDED')),
  CONSTRAINT ck_ops_sla_targets_reason CHECK (char_length(btrim(reason)) BETWEEN 5 AND 2000),
  CONSTRAINT ck_ops_sla_targets_decided CHECK ((status = 'PROPOSED') = (decided_by IS NULL) AND (decided_by IS NULL OR
    (decided_at IS NOT NULL AND decision_reason IS NOT NULL AND decided_by <> proposed_by)))
);
CREATE UNIQUE INDEX uq_ops_sla_targets_active ON app.ops_sla_targets (metric_key) WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX uq_ops_sla_targets_proposed ON app.ops_sla_targets (metric_key) WHERE status = 'PROPOSED';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('ops_sla_target', '',         'PROPOSED'),
  ('ops_sla_target', 'PROPOSED', 'ACTIVE'),
  ('ops_sla_target', 'PROPOSED', 'REJECTED'),
  ('ops_sla_target', 'ACTIVE',   'SUPERSEDED');
CREATE FUNCTION app.ops_sla_targets_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE' AND (NEW.metric_key, NEW.target_seconds, NEW.proposed_by, NEW.proposed_at, NEW.reason) IS DISTINCT FROM
     (OLD.metric_key, OLD.target_seconds, OLD.proposed_by, OLD.proposed_at, OLD.reason) THEN
    RAISE EXCEPTION 'SLA target % is immutable; propose a new one', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.decided_by IS NOT NULL AND (TG_OP = 'INSERT' OR OLD.decided_by IS NULL) AND NEW.proposed_by = ANY (app.actor_identities(NEW.decided_by)) THEN
    RAISE EXCEPTION 'maker-checker: % proposed SLA target % and cannot decide it', NEW.decided_by, NEW.id USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_ops_sla_targets_guard BEFORE INSERT OR UPDATE ON app.ops_sla_targets FOR EACH ROW EXECUTE FUNCTION app.ops_sla_targets_guard();
CREATE TRIGGER trg_ops_sla_targets_guard_status BEFORE INSERT OR UPDATE OF status ON app.ops_sla_targets
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('ops_sla_target');
CREATE TRIGGER trg_ops_sla_targets_updated_at BEFORE UPDATE ON app.ops_sla_targets FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_ops_sla_targets_no_delete BEFORE DELETE ON app.ops_sla_targets FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- 6. Permissions (every mutation requires step-up; no permission overrides a domain control).
-- -----------------------------------------------------------------------------------------------------
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up) VALUES
  (md5('permission:ops.dashboard.view')::uuid, 'ops.dashboard.view', 'View the operations command center, approvals, exceptions, health and reports (each item still needs its own permission)', false, false),
  (md5('permission:ops.support.view')::uuid, 'ops.support.view', 'View support cases', true, false),
  (md5('permission:ops.support.work')::uuid, 'ops.support.work', 'Open, assign, update, escalate and resolve support cases', true, true),
  (md5('permission:ops.exception.work')::uuid, 'ops.exception.work', 'Triage operational exceptions (assignment and notes; resolution stays with the owning module)', true, true),
  (md5('permission:ops.incident.view')::uuid, 'ops.incident.view', 'View operational incidents', false, false),
  (md5('permission:ops.incident.manage')::uuid, 'ops.incident.manage', 'Declare and manage operational incidents', true, true),
  (md5('permission:staff.view')::uuid, 'staff.view', 'View the staff directory, role inventory and access reviews', true, false),
  (md5('permission:access.review.certify')::uuid, 'access.review.certify', 'Certify or recommend revocation in a staff access review', true, true),
  (md5('permission:audit.export')::uuid, 'audit.export', 'Export audit events (bounded; the export is itself audited)', true, true),
  (md5('permission:ops.sla.request')::uuid, 'ops.sla.request', 'Propose internal SLA targets (maker)', false, true),
  (md5('permission:ops.sla.approve')::uuid, 'ops.sla.approve', 'Approve or reject internal SLA targets (checker)', false, true)
ON CONFLICT DO NOTHING;
INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm)::uuid, md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm)::uuid
FROM (VALUES
  ('SUPER_ADMIN','ops.dashboard.view'), ('ADMIN','ops.dashboard.view'), ('SECURITY_ADMIN','ops.dashboard.view'), ('SUPPORT','ops.dashboard.view'),
  ('KYC_REVIEWER','ops.dashboard.view'), ('REVIEWER','ops.dashboard.view'), ('COMPLIANCE','ops.dashboard.view'), ('FINANCE','ops.dashboard.view'),
  ('BUSINESS_APPROVER','ops.dashboard.view'),
  ('SUPPORT','ops.support.view'), ('SUPPORT','ops.support.work'), ('COMPLIANCE','ops.support.view'), ('REVIEWER','ops.support.view'),
  ('KYC_REVIEWER','ops.support.view'), ('FINANCE','ops.support.view'),
  ('FINANCE','ops.exception.work'), ('COMPLIANCE','ops.exception.work'), ('ADMIN','ops.exception.work'), ('SECURITY_ADMIN','ops.exception.work'),
  ('SUPER_ADMIN','ops.incident.view'), ('ADMIN','ops.incident.view'), ('SECURITY_ADMIN','ops.incident.view'), ('FINANCE','ops.incident.view'),
  ('COMPLIANCE','ops.incident.view'), ('SUPPORT','ops.incident.view'),
  ('ADMIN','ops.incident.manage'), ('SECURITY_ADMIN','ops.incident.manage'),
  ('SUPER_ADMIN','staff.view'), ('SECURITY_ADMIN','staff.view'),
  ('SUPER_ADMIN','access.review.certify'), ('SECURITY_ADMIN','access.review.certify'),
  ('COMPLIANCE','audit.export'), ('SUPER_ADMIN','audit.export'),
  ('ADMIN','ops.sla.request'), ('COMPLIANCE','ops.sla.request'), ('BUSINESS_APPROVER','ops.sla.approve')
) AS rp(role_code, perm)
ON CONFLICT DO NOTHING;

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.role_permissions DISABLE TRIGGER USER;
DELETE FROM app.role_permissions WHERE permission_id IN (SELECT id FROM app.permissions WHERE code IN ('ops.dashboard.view','ops.support.view',
  'ops.support.work','ops.exception.work','ops.incident.view','ops.incident.manage','staff.view','access.review.certify','audit.export',
  'ops.sla.request','ops.sla.approve'));
ALTER TABLE app.role_permissions ENABLE TRIGGER USER;
ALTER TABLE app.permissions DISABLE TRIGGER USER;
DELETE FROM app.permissions WHERE code IN ('ops.dashboard.view','ops.support.view','ops.support.work','ops.exception.work','ops.incident.view',
  'ops.incident.manage','staff.view','access.review.certify','audit.export','ops.sla.request','ops.sla.approve');
ALTER TABLE app.permissions ENABLE TRIGGER USER;
DROP TABLE app.ops_sla_targets;
DROP FUNCTION app.ops_sla_targets_guard();
DROP TABLE app.ops_access_review_items;
DROP FUNCTION app.ops_access_review_items_guard();
DROP TABLE app.ops_access_reviews;
DROP TABLE app.ops_exception_events;
DROP TABLE app.ops_exception_assignments;
DROP TABLE app.ops_incident_events;
DROP TABLE app.ops_incidents;
DROP SEQUENCE app.ops_incident_number_seq;
DROP TABLE app.ops_support_events;
DROP TABLE app.ops_support_cases;
DROP SEQUENCE app.ops_support_case_number_seq;
ALTER TABLE app.status_transitions DISABLE TRIGGER USER;
DELETE FROM app.status_transitions WHERE machine IN ('ops_support_case','ops_incident','ops_exception','ops_access_review','ops_sla_target');
ALTER TABLE app.status_transitions ENABLE TRIGGER USER;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
