-- FundZim migration: compliance cases (Stage 5, work stream C) — compliance.compliance_cases,
-- compliance_case_events, compliance_case_links, compliance_case_notes, compliance_case_triggers.
-- Derived from the Stage 2 design draft design/sql/0013_compliance.sql (cases, events, links) and the RLS of
-- design/sql/0018_grants.sql §5, with the ADR-034 §5 state machine:
--   OPEN -> ASSIGNED -> IN_REVIEW <-> AWAITING_INFORMATION; IN_REVIEW -> ESCALATED -> IN_REVIEW;
--   IN_REVIEW | ESCALATED -> RESOLVED (resolution_status PROPOSED | APPROVED) -> CLOSED;
--   RESOLVED | CLOSED -> IN_REVIEW (reopen, recorded).
-- Draft tables for screening, STR reports and restrictions belong to later stages and are not created; the
-- RESTRICTED_STR confidentiality flag and its row-level security are (ADR-035 §4). SLA columns are omitted
-- (PD-28 SLA policy is an open business decision); there is no payout-blocking view yet (EC-13 is payouts).
--
-- Owner module: compliance. Runtime access: role fundzim_compliance only (app.apply_runtime_grants v3);
-- fundzim_app has no privilege on this schema. Audit and outbox writes go through the ADR-035 gateways.
-- Classification: C3 (case metadata, notes ciphertext) / C2 (timeline, links). Notes are AES-256-GCM
-- ciphertext (COMPLIANCE_FIELD_ENCRYPTION_LOCAL_KEY, AAD = note id), always staff-only.
--
-- Maker-checker: resolutions RESTRICT, SUSPEND, OFFBOARD and CONFIRMED_FRAUD are PROPOSED by the decider
-- and APPROVED by a different staff member (CHECK); neither the assignee, the decider nor the approver may be
-- a linked USER subject of the case or that subject's staff alias (trigger; organisation membership of the
-- staff member's personal account is checked by the service).
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('compliance_case', '',                     'OPEN'),
  ('compliance_case', 'OPEN',                 'ASSIGNED'),
  ('compliance_case', 'ASSIGNED',             'IN_REVIEW'),
  ('compliance_case', 'IN_REVIEW',            'AWAITING_INFORMATION'),
  ('compliance_case', 'AWAITING_INFORMATION', 'IN_REVIEW'),
  ('compliance_case', 'IN_REVIEW',            'ESCALATED'),
  ('compliance_case', 'ESCALATED',            'IN_REVIEW'),
  ('compliance_case', 'IN_REVIEW',            'RESOLVED'),
  ('compliance_case', 'ESCALATED',            'RESOLVED'),
  ('compliance_case', 'RESOLVED',             'CLOSED'),
  ('compliance_case', 'RESOLVED',             'IN_REVIEW'),
  ('compliance_case', 'CLOSED',               'IN_REVIEW');

CREATE SEQUENCE compliance.case_number_seq AS bigint MINVALUE 1 MAXVALUE 999999 NO CYCLE;

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_cases
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.compliance_cases (
  id                   uuid        NOT NULL,
  case_number          text        NOT NULL,
  case_type            text        NOT NULL,
  severity             text        NOT NULL,
  status               text        NOT NULL,
  confidentiality      text        NOT NULL DEFAULT 'NORMAL',
  source               text        NOT NULL,
  opening_reason_code  text        NOT NULL,
  opened_by_type       text        NOT NULL,
  opened_by            uuid,
  opened_by_job        text,
  opened_at            timestamptz NOT NULL,
  assigned_to          uuid,
  assigned_at          timestamptz,
  decision             text,
  decision_reason_code text,
  resolution_status    text,
  decided_by           uuid,
  decided_at           timestamptz,
  approved_by          uuid,
  approved_at          timestamptz,
  closed_by            uuid,
  closed_at            timestamptz,
  closure_reason_code  text,
  version              integer     NOT NULL DEFAULT 1,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_compliance_cases PRIMARY KEY (id),
  CONSTRAINT uq_compliance_cases_number UNIQUE (case_number),
  CONSTRAINT fk_compliance_cases_opened_by FOREIGN KEY (opened_by) REFERENCES app.users (id),
  CONSTRAINT fk_compliance_cases_assigned_to FOREIGN KEY (assigned_to) REFERENCES app.users (id),
  CONSTRAINT fk_compliance_cases_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id),
  CONSTRAINT fk_compliance_cases_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT fk_compliance_cases_closed_by FOREIGN KEY (closed_by) REFERENCES app.users (id),
  CONSTRAINT ck_compliance_cases_number CHECK (case_number ~ '^CMP-[0-9]{4}-[0-9]{6}$'),
  CONSTRAINT ck_compliance_cases_type CHECK (case_type IN ('KYC_REVIEW', 'KYB_REVIEW', 'BENEFICIARY_REVIEW',
             'PAYOUT_DESTINATION_REVIEW', 'RISK_REVIEW', 'SANCTIONS', 'PEP_EDD', 'FRAUD_CAMPAIGN', 'ACCOUNT_TAKEOVER',
             'AML_MONITORING', 'PAYOUT_REVIEW', 'FUNDRAISING_AUTHORITY', 'REGULATOR_REQUEST', 'OTHER')),
  CONSTRAINT ck_compliance_cases_severity CHECK (severity IN ('S1', 'S2', 'S3')),
  CONSTRAINT ck_compliance_cases_status CHECK (status IN ('OPEN', 'ASSIGNED', 'IN_REVIEW', 'AWAITING_INFORMATION', 'ESCALATED',
             'RESOLVED', 'CLOSED')),
  CONSTRAINT ck_compliance_cases_confidentiality CHECK (confidentiality IN ('NORMAL', 'RESTRICTED_STR')),
  CONSTRAINT ck_compliance_cases_source CHECK (source IN ('KYC_ESCALATION', 'BENEFICIARY_ESCALATION', 'RISK_ENGINE', 'STAFF',
             'ALERT', 'SCREENING', 'REPORT', 'REGULATOR_REQUEST', 'PROVIDER')),
  CONSTRAINT ck_compliance_cases_opening_reason CHECK (opening_reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_compliance_cases_opened_by CHECK (
       (opened_by_type = 'STAFF'  AND opened_by IS NOT NULL AND opened_by_job IS NULL)
    OR (opened_by_type = 'SYSTEM' AND opened_by IS NULL     AND opened_by_job ~ '^[a-z_]+(\.[a-z_]+)+$')),
  CONSTRAINT ck_compliance_cases_assigned_pair CHECK ((assigned_to IS NULL) = (assigned_at IS NULL)),
  -- OPEN means unassigned; every later state has an owner
  CONSTRAINT ck_compliance_cases_assigned CHECK ((status = 'OPEN') = (assigned_to IS NULL)),
  CONSTRAINT ck_compliance_cases_decision CHECK (decision IS NULL OR decision IN ('CLEARED', 'EDD_CONDITIONS', 'RESTRICT',
             'SUSPEND', 'OFFBOARD', 'CONFIRMED_FRAUD')),
  CONSTRAINT ck_compliance_cases_reason_codes CHECK (
        (decision_reason_code IS NULL OR decision_reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$')
    AND (closure_reason_code IS NULL OR closure_reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$')),
  CONSTRAINT ck_compliance_cases_resolution_status CHECK (resolution_status IS NULL OR resolution_status IN ('PROPOSED', 'APPROVED')),
  -- a resolution exists exactly in RESOLVED / CLOSED and is complete
  CONSTRAINT ck_compliance_cases_resolved CHECK ((status IN ('RESOLVED', 'CLOSED')) = (decision IS NOT NULL)),
  CONSTRAINT ck_compliance_cases_resolution_shape CHECK (
       (decision IS NULL AND decision_reason_code IS NULL AND resolution_status IS NULL AND decided_by IS NULL
        AND decided_at IS NULL AND approved_by IS NULL AND approved_at IS NULL)
    OR (decision IS NOT NULL AND decision_reason_code IS NOT NULL AND resolution_status IS NOT NULL AND decided_by IS NOT NULL
        AND decided_at IS NOT NULL)),
  CONSTRAINT ck_compliance_cases_approved_pair CHECK ((approved_by IS NULL) = (approved_at IS NULL)),
  -- maker-checker (compliance-case-management §4): these decisions take effect only with a different checker
  CONSTRAINT ck_compliance_cases_checker_required CHECK (
       resolution_status IS DISTINCT FROM 'APPROVED'
    OR decision NOT IN ('RESTRICT', 'SUSPEND', 'OFFBOARD', 'CONFIRMED_FRAUD')
    OR approved_by IS NOT NULL),
  CONSTRAINT ck_compliance_cases_proposed_only_checker CHECK (
       resolution_status IS DISTINCT FROM 'PROPOSED' OR decision IN ('RESTRICT', 'SUSPEND', 'OFFBOARD', 'CONFIRMED_FRAUD')),
  CONSTRAINT ck_compliance_cases_approver_after_proposal CHECK (approved_by IS NULL OR resolution_status = 'APPROVED'),
  CONSTRAINT ck_compliance_cases_maker_checker CHECK (approved_by IS NULL OR approved_by <> decided_by),
  CONSTRAINT ck_compliance_cases_closed CHECK ((status = 'CLOSED') = (closed_by IS NOT NULL AND closed_at IS NOT NULL
                                                                      AND closure_reason_code IS NOT NULL)),
  CONSTRAINT ck_compliance_cases_closed_approved CHECK (status <> 'CLOSED' OR resolution_status = 'APPROVED'),
  CONSTRAINT ck_compliance_cases_version CHECK (version >= 1)
);
COMMENT ON TABLE compliance.compliance_cases IS 'C3. Compliance cases (ADR-034 §5); RESTRICTED_STR rows protected by RLS.';
CREATE INDEX ix_compliance_cases_queue ON compliance.compliance_cases (status, severity, opened_at) WHERE status <> 'CLOSED';
CREATE INDEX ix_compliance_cases_assigned_to ON compliance.compliance_cases (assigned_to) WHERE assigned_to IS NOT NULL;

CREATE TRIGGER trg_compliance_cases_guard_status BEFORE INSERT OR UPDATE OF status ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('compliance_case');
CREATE TRIGGER trg_compliance_cases_set_updated_at BEFORE UPDATE ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_compliance_cases_keep_created_at BEFORE UPDATE ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_compliance_cases_no_delete BEFORE DELETE ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_cases_no_truncate BEFORE TRUNCATE ON compliance.compliance_cases
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION compliance.compliance_cases_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.version <> 1 OR NEW.decision IS NOT NULL THEN
      RAISE EXCEPTION 'compliance case: a new case starts at version 1 without a resolution' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  -- identity of the case never changes
  IF changed && ARRAY['id','case_number','case_type','source','opening_reason_code','opened_by_type','opened_by',
                      'opened_by_job','opened_at'] THEN
    RAISE EXCEPTION 'compliance case %: identity fields are immutable (attempted: %)', OLD.case_number, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  -- confidentiality only ever rises
  IF OLD.confidentiality = 'RESTRICTED_STR' AND NEW.confidentiality <> 'RESTRICTED_STR' THEN
    RAISE EXCEPTION 'compliance case %: RESTRICTED_STR confidentiality cannot be lowered', OLD.case_number
      USING ERRCODE = 'restrict_violation';
  END IF;
  -- a recorded resolution is frozen while the case stays RESOLVED/CLOSED; the only change is the checker's
  -- approval of a PROPOSED resolution (once). Reopening (-> IN_REVIEW) clears it; the timeline keeps it.
  IF OLD.decision IS NOT NULL AND NEW.status IN ('RESOLVED','CLOSED') THEN
    IF changed && ARRAY['decision','decision_reason_code','decided_by','decided_at'] THEN
      RAISE EXCEPTION 'compliance case %: the resolution is final unless the case is reopened', OLD.case_number
        USING ERRCODE = 'restrict_violation';
    END IF;
    IF changed && ARRAY['resolution_status','approved_by','approved_at'] AND OLD.resolution_status <> 'PROPOSED' THEN
      RAISE EXCEPTION 'compliance case %: the resolution is already approved', OLD.case_number USING ERRCODE = 'restrict_violation';
    END IF;
  END IF;
  IF changed <> '{}' AND NOT changed <@ ARRAY['updated_at'] AND NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'compliance case %: version must increase by 1 on every change', OLD.case_number
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_compliance_cases_guard BEFORE INSERT OR UPDATE ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION compliance.compliance_cases_guard();

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_case_events — append-only timeline. Every case version has exactly one event
-- (deferred constraint trigger); unversioned events (notes, source events) have case_version NULL.
-- Payload: codes and ids only (no C3, never note text).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.compliance_case_events (
  id             uuid        NOT NULL,
  case_id        uuid        NOT NULL,
  case_version   integer,
  event_type     text        NOT NULL,
  from_status    text,
  to_status      text,
  actor_type     text        NOT NULL,
  actor_id       uuid,
  actor_job      text,
  reason_code    text,
  payload        jsonb       NOT NULL DEFAULT '{}',
  audit_event_id uuid,
  occurred_at    timestamptz NOT NULL,
  CONSTRAINT pk_compliance_case_events PRIMARY KEY (id),
  CONSTRAINT fk_compliance_case_events_case_id FOREIGN KEY (case_id) REFERENCES compliance.compliance_cases (id),
  CONSTRAINT fk_compliance_case_events_actor_id FOREIGN KEY (actor_id) REFERENCES app.users (id),
  CONSTRAINT ck_compliance_case_events_type CHECK (event_type IN ('OPENED', 'ASSIGNED', 'STATUS_CHANGED', 'INFO_REQUESTED',
             'ESCALATED', 'RESOLUTION_PROPOSED', 'RESOLUTION_APPROVED', 'RESOLVED', 'CLOSED', 'REOPENED', 'NOTE_ADDED',
             'SUBJECT_LINKED', 'SOURCE_EVENT_LINKED', 'SEVERITY_CHANGED')),
  CONSTRAINT ck_compliance_case_events_actor CHECK (
       (actor_type = 'STAFF'  AND actor_id IS NOT NULL AND actor_job IS NULL)
    OR (actor_type = 'SYSTEM' AND actor_id IS NULL AND actor_job ~ '^[a-z_]+(\.[a-z_]+)+$')),
  CONSTRAINT ck_compliance_case_events_versioned CHECK (
       (event_type IN ('NOTE_ADDED', 'SOURCE_EVENT_LINKED', 'SUBJECT_LINKED')) = (case_version IS NULL)),
  CONSTRAINT ck_compliance_case_events_reason CHECK (reason_code IS NULL OR reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_compliance_case_events_payload CHECK (jsonb_typeof(payload) = 'object')
);
COMMENT ON TABLE compliance.compliance_case_events IS 'C2. Append-only case timeline (no C3 in payload).';
CREATE UNIQUE INDEX uq_compliance_case_events_version ON compliance.compliance_case_events (case_id, case_version)
  WHERE case_version IS NOT NULL;
CREATE INDEX ix_compliance_case_events_case ON compliance.compliance_case_events (case_id, occurred_at);
CREATE TRIGGER trg_compliance_case_events_no_mutation BEFORE UPDATE OR DELETE ON compliance.compliance_case_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_case_events_no_truncate BEFORE TRUNCATE ON compliance.compliance_case_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Every version has its event, checked at COMMIT. SECURITY DEFINER (owner fundzim_migrator is not subject to
-- RLS) so the check sees RESTRICTED_STR rows whatever the caller's flag (0018 §2).
CREATE FUNCTION compliance.cases_require_event() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE v integer; num text;
BEGIN
  SELECT c.version, c.case_number INTO v, num FROM compliance.compliance_cases c WHERE c.id = NEW.id;
  IF NOT EXISTS (SELECT 1 FROM compliance.compliance_case_events e WHERE e.case_id = NEW.id AND e.case_version = v) THEN
    RAISE EXCEPTION 'compliance case % version % has no compliance_case_events row', num, v USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_compliance_cases_require_event AFTER INSERT OR UPDATE ON compliance.compliance_cases
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION compliance.cases_require_event();

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_case_links — subjects and related objects of a case. Append-only; a mistaken link
-- is corrected by an UNLINKED row.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.compliance_case_links (
  id             uuid        NOT NULL,
  case_id        uuid        NOT NULL,
  link_action    text        NOT NULL DEFAULT 'LINKED',
  unlinks_id     uuid,
  subject_type   text        NOT NULL,
  subject_id     uuid        NOT NULL,
  role           text        NOT NULL,
  linked_by_type text        NOT NULL,
  linked_by      uuid,
  linked_at      timestamptz NOT NULL,
  CONSTRAINT pk_compliance_case_links PRIMARY KEY (id),
  CONSTRAINT fk_compliance_case_links_case_id FOREIGN KEY (case_id) REFERENCES compliance.compliance_cases (id),
  CONSTRAINT fk_compliance_case_links_unlinks_id FOREIGN KEY (unlinks_id) REFERENCES compliance.compliance_case_links (id),
  CONSTRAINT fk_compliance_case_links_linked_by FOREIGN KEY (linked_by) REFERENCES app.users (id),
  CONSTRAINT uq_compliance_case_links_unlink UNIQUE (unlinks_id),
  CONSTRAINT ck_compliance_case_links_action CHECK (link_action IN ('LINKED', 'UNLINKED')),
  CONSTRAINT ck_compliance_case_links_unlink CHECK ((link_action = 'UNLINKED') = (unlinks_id IS NOT NULL)),
  CONSTRAINT ck_compliance_case_links_subject CHECK (subject_type IN ('USER', 'ORGANISATION', 'BENEFICIARY', 'PAYOUT_DESTINATION',
             'CAMPAIGN', 'KYC_CASE', 'KYB_CASE', 'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT', 'PAYOUT')),
  CONSTRAINT ck_compliance_case_links_role CHECK (role IN ('PRIMARY_SUBJECT', 'RELATED_SUBJECT', 'RELATED_OBJECT')),
  -- only parties are subjects; cases, risk outputs and payments are related objects
  CONSTRAINT ck_compliance_case_links_role_kind CHECK ((role = 'RELATED_OBJECT') = (subject_type IN ('KYC_CASE', 'KYB_CASE',
             'RISK_DECISION', 'RISK_ASSESSMENT', 'PAYMENT', 'PAYOUT'))),
  CONSTRAINT ck_compliance_case_links_by CHECK (
       (linked_by_type = 'STAFF' AND linked_by IS NOT NULL) OR (linked_by_type = 'SYSTEM' AND linked_by IS NULL))
);
COMMENT ON TABLE compliance.compliance_case_links IS 'C2. Append-only case subjects and related objects.';
CREATE INDEX ix_compliance_case_links_subject ON compliance.compliance_case_links (subject_type, subject_id);
CREATE INDEX ix_compliance_case_links_case ON compliance.compliance_case_links (case_id);
CREATE TRIGGER trg_compliance_case_links_no_mutation BEFORE UPDATE OR DELETE ON compliance.compliance_case_links
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_case_links_no_truncate BEFORE TRUNCATE ON compliance.compliance_case_links
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Conflict guard (compliance-case-management §7, contract §7.3 SELF_DECISION_FORBIDDEN): the assignee, the
-- decider and the approver are never a currently linked USER party of the case, directly or through the
-- staff account's linked personal account (app.users.staff_personal_user_id). SECURITY DEFINER so it sees
-- every link and the staff link whatever the caller's RLS visibility and grants; read-only.
CREATE FUNCTION compliance.compliance_cases_actor_not_subject() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
DECLARE
  actor uuid;
  personal uuid;
BEGIN
  FOREACH actor IN ARRAY ARRAY[NEW.assigned_to, NEW.decided_by, NEW.approved_by] LOOP
    CONTINUE WHEN actor IS NULL;
    SELECT u.staff_personal_user_id INTO personal FROM app.users u WHERE u.id = actor;
    IF EXISTS (SELECT 1 FROM compliance.compliance_case_links l
                WHERE l.case_id = NEW.id AND l.link_action = 'LINKED' AND l.subject_type = 'USER'
                  AND l.subject_id IN (actor, personal)
                  AND NOT EXISTS (SELECT 1 FROM compliance.compliance_case_links u2 WHERE u2.unlinks_id = l.id)) THEN
      RAISE EXCEPTION 'compliance case %: a subject of the case cannot be its assignee, decider or approver', NEW.case_number
        USING ERRCODE = 'check_violation', CONSTRAINT = 'ck_compliance_cases_actor_not_subject';
    END IF;
  END LOOP;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_compliance_cases_actor_not_subject BEFORE UPDATE OF assigned_to, decided_by, approved_by
  ON compliance.compliance_cases FOR EACH ROW EXECUTE FUNCTION compliance.compliance_cases_actor_not_subject();

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_case_notes — append-only staff notes; the body is AES-256-GCM ciphertext
-- (nonce || ciphertext || tag, AAD = note id). Corrections are new notes. Never shown outside staff APIs.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.compliance_case_notes (
  id              uuid        NOT NULL,
  case_id         uuid        NOT NULL,
  author_id       uuid        NOT NULL,
  visibility      text        NOT NULL DEFAULT 'STAFF_ONLY',
  body_ciphertext bytea       NOT NULL,
  key_id          text        NOT NULL,
  created_at      timestamptz NOT NULL,
  CONSTRAINT pk_compliance_case_notes PRIMARY KEY (id),
  CONSTRAINT fk_compliance_case_notes_case_id FOREIGN KEY (case_id) REFERENCES compliance.compliance_cases (id),
  CONSTRAINT fk_compliance_case_notes_author_id FOREIGN KEY (author_id) REFERENCES app.users (id),
  CONSTRAINT ck_compliance_case_notes_visibility CHECK (visibility = 'STAFF_ONLY'),
  CONSTRAINT ck_compliance_case_notes_ciphertext CHECK (octet_length(body_ciphertext) BETWEEN 29 AND 65536),
  CONSTRAINT ck_compliance_case_notes_key_id CHECK (key_id ~ '^[a-z0-9-]{1,64}$')
);
COMMENT ON TABLE compliance.compliance_case_notes IS 'C3. Append-only encrypted staff notes.';
CREATE INDEX ix_compliance_case_notes_case ON compliance.compliance_case_notes (case_id, created_at);
CREATE TRIGGER trg_compliance_case_notes_no_mutation BEFORE UPDATE OR DELETE ON compliance.compliance_case_notes
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_case_notes_no_truncate BEFORE TRUNCATE ON compliance.compliance_case_notes
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_case_triggers — consumer-side dedupe for the events that open or link cases.
-- fundzim_compliance has no privilege on app.inbox_events, so the case consumers dedupe here, in the same
-- transaction as the case change: one row per (consumer, source_event_id). The row is inserted after the
-- case exists (INSERT ... ON CONFLICT also checks the new row against the SELECT policy); a conflict rolls
-- the whole transaction back.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.compliance_case_triggers (
  id              uuid        NOT NULL,
  consumer        text        NOT NULL,
  source_event_id uuid        NOT NULL,
  event_type      text        NOT NULL,
  case_id         uuid        NOT NULL,
  outcome         text        NOT NULL,
  received_at     timestamptz NOT NULL,
  CONSTRAINT pk_compliance_case_triggers PRIMARY KEY (id),
  CONSTRAINT uq_compliance_case_triggers_source UNIQUE (consumer, source_event_id),
  CONSTRAINT fk_compliance_case_triggers_case_id FOREIGN KEY (case_id) REFERENCES compliance.compliance_cases (id),
  CONSTRAINT ck_compliance_case_triggers_consumer CHECK (consumer ~ '^[a-z_]+(\.[a-z_]+)+$'),
  CONSTRAINT ck_compliance_case_triggers_event_type CHECK (event_type ~ '^[a-z_]+(\.[a-z_]+)+$'),
  CONSTRAINT ck_compliance_case_triggers_outcome CHECK (outcome IN ('OPENED', 'LINKED'))
);
COMMENT ON TABLE compliance.compliance_case_triggers IS 'C2. Append-only dedupe of case-opening events.';
CREATE INDEX ix_compliance_case_triggers_case ON compliance.compliance_case_triggers (case_id);
CREATE TRIGGER trg_compliance_case_triggers_no_mutation BEFORE UPDATE OR DELETE ON compliance.compliance_case_triggers
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_case_triggers_no_truncate BEFORE TRUNCATE ON compliance.compliance_case_triggers
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- Row-level security for STR-restricted cases (ADR-035 §4, draft 0018 §5). The module sets
--   SET LOCAL fundzim.str_access = 'on'
-- only inside a transaction, after its own permission check. Protects against accidental exposure (list
-- views, forgotten filters), not against a compromised module holding the compliance pool. The table owner
-- (fundzim_migrator) is not subject to RLS (not FORCEd), so integrity triggers see every row.
-- -----------------------------------------------------------------------------------------------------
ALTER TABLE compliance.compliance_cases ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_compliance_cases_read ON compliance.compliance_cases FOR SELECT TO fundzim_compliance
  USING (confidentiality = 'NORMAL' OR current_setting('fundzim.str_access', true) = 'on');
CREATE POLICY p_compliance_cases_insert ON compliance.compliance_cases FOR INSERT TO fundzim_compliance
  WITH CHECK (true);                                   -- anyone in compliance may raise an internal suspicion
CREATE POLICY p_compliance_cases_update ON compliance.compliance_cases FOR UPDATE TO fundzim_compliance
  USING (confidentiality = 'NORMAL' OR current_setting('fundzim.str_access', true) = 'on')
  WITH CHECK (true);                                   -- raising to RESTRICTED_STR is allowed; lowering is blocked by trigger

ALTER TABLE compliance.compliance_case_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_compliance_case_events_read ON compliance.compliance_case_events FOR SELECT TO fundzim_compliance
  USING (EXISTS (SELECT 1 FROM compliance.compliance_cases c WHERE c.id = case_id));   -- inherits case visibility
CREATE POLICY p_compliance_case_events_insert ON compliance.compliance_case_events FOR INSERT TO fundzim_compliance
  WITH CHECK (true);

ALTER TABLE compliance.compliance_case_links ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_compliance_case_links_read ON compliance.compliance_case_links FOR SELECT TO fundzim_compliance
  USING (EXISTS (SELECT 1 FROM compliance.compliance_cases c WHERE c.id = case_id));
CREATE POLICY p_compliance_case_links_insert ON compliance.compliance_case_links FOR INSERT TO fundzim_compliance
  WITH CHECK (true);

ALTER TABLE compliance.compliance_case_notes ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_compliance_case_notes_read ON compliance.compliance_case_notes FOR SELECT TO fundzim_compliance
  USING (EXISTS (SELECT 1 FROM compliance.compliance_cases c WHERE c.id = case_id));
CREATE POLICY p_compliance_case_notes_insert ON compliance.compliance_case_notes FOR INSERT TO fundzim_compliance
  WITH CHECK (true);

ALTER TABLE compliance.compliance_case_triggers ENABLE ROW LEVEL SECURITY;
CREATE POLICY p_compliance_case_triggers_read ON compliance.compliance_case_triggers FOR SELECT TO fundzim_compliance
  USING (EXISTS (SELECT 1 FROM compliance.compliance_cases c WHERE c.id = case_id));
CREATE POLICY p_compliance_case_triggers_insert ON compliance.compliance_case_triggers FOR INSERT TO fundzim_compliance
  WITH CHECK (true);

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Development rollback only. DROP removes the append-only tables (row/statement triggers do not fire on DROP).
DROP TABLE IF EXISTS compliance.compliance_case_triggers, compliance.compliance_case_notes, compliance.compliance_case_links,
  compliance.compliance_case_events, compliance.compliance_cases;
DROP SEQUENCE IF EXISTS compliance.case_number_seq;
DROP FUNCTION IF EXISTS compliance.compliance_cases_guard(), compliance.cases_require_event(),
  compliance.compliance_cases_actor_not_subject();
-- status_transitions is append-only; the migrator (table owner) lifts the guard only for this development rollback.
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine = 'compliance_case';
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
CALL app.apply_runtime_grants();
-- +goose StatementEnd
