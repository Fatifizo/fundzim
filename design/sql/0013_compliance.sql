-- =====================================================================================================
-- FundZim Stage 2 SQL DESIGN DRAFT — NOT A MIGRATION. Never run against a real database.
-- Validated only in an in-memory PGlite instance (design/sql/validate).
-- File 0013: schema `compliance` (owner module: compliance). Baseline §5.14, ADR-022.
-- Explained in docs/database/compliance-schema.md.
--
-- Runtime access: role `fundzim_compliance` only (0018). fundzim_app sees exactly one narrow view,
-- compliance.v_payout_blocking_cases (EC-13). Rows of RESTRICTED_STR cases (and their events, links and STR
-- reports) are hidden by row-level security unless the compliance module, after its own permission check
-- (str.prepare / str.approve), sets `SET LOCAL fundzim.str_access = 'on'` in the transaction (0018).
-- STR content, case notes and screening details are evidence objects (audit.evidence_records, C3), never
-- free text in these rows.
-- Cross-schema FKs: only app.users and audit.evidence_records. Hold ids and limit ids (risk) and subject
-- ids are plain uuids validated by trigger where needed (baseline §10).
-- =====================================================================================================

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_cases (compliance-case-management §2–§4). Machine 'compliance_case'.
-- -----------------------------------------------------------------------------------------------------
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('compliance_case', '',                 'OPEN'),
  ('compliance_case', 'OPEN',             'IN_PROGRESS'),
  ('compliance_case', 'IN_PROGRESS',      'AWAITING_INFO'),
  ('compliance_case', 'AWAITING_INFO',    'IN_PROGRESS'),
  ('compliance_case', 'IN_PROGRESS',      'PENDING_APPROVAL'),
  ('compliance_case', 'PENDING_APPROVAL', 'IN_PROGRESS'),
  ('compliance_case', 'PENDING_APPROVAL', 'DECIDED'),
  ('compliance_case', 'IN_PROGRESS',      'DECIDED'),
  ('compliance_case', 'DECIDED',          'CLOSED'),
  ('compliance_case', 'CLOSED',           'REOPENED'),
  ('compliance_case', 'REOPENED',         'IN_PROGRESS');

CREATE TABLE compliance.compliance_cases (
  id                     uuid        NOT NULL,
  case_number            text        NOT NULL,
  case_type              text        NOT NULL,
  severity               text        NOT NULL,
  status                 text        NOT NULL,
  confidentiality        text        NOT NULL DEFAULT 'NORMAL',
  source                 text        NOT NULL,
  opened_by_type         text        NOT NULL,
  opened_by              uuid        REFERENCES app.users(id),
  opened_by_job          text,
  opened_at              timestamptz NOT NULL DEFAULT now(),
  team                   text        NOT NULL,
  assigned_to            uuid        REFERENCES app.users(id),
  assigned_at            timestamptz,
  sla_due_at             timestamptz NOT NULL,
  sla_policy_ref         text        NOT NULL,              -- PD-28 policy version used to compute sla_due_at
  blocks_payouts         boolean     NOT NULL DEFAULT false, -- read through v_payout_blocking_cases (EC-13)
  suspicion_formed_at    timestamptz,                        -- STR clock start (RESTRICTED_STR)
  decision               text,
  decision_reason_code   text,
  decided_by             uuid        REFERENCES app.users(id),
  second_approver_id     uuid        REFERENCES app.users(id),
  decided_at             timestamptz,
  closed_at              timestamptz,
  closed_by              uuid        REFERENCES app.users(id),
  closure_memo_evidence_id uuid      REFERENCES audit.evidence_records(id),
  qa_sampled             boolean     NOT NULL DEFAULT false,
  qa_result              text,
  version                integer     NOT NULL DEFAULT 1,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_compliance_cases PRIMARY KEY (id),
  CONSTRAINT uq_compliance_cases_number UNIQUE (case_number),
  CONSTRAINT ck_compliance_cases_number CHECK (case_number ~ '^CMP-[0-9]{4}-[0-9]{6}$'),
  CONSTRAINT ck_compliance_cases_type CHECK (case_type IN ('KYC_REVIEW','BENEFICIARY_REVIEW','SANCTIONS','PEP_EDD','FRAUD_CAMPAIGN',
             'ACCOUNT_TAKEOVER','AML_MONITORING','PAYOUT_REVIEW','REFUND_ABUSE','DISPUTE_PATTERN','FUNDRAISING_AUTHORITY',
             'REGULATOR_REQUEST','DATA_SUBJECT_REQUEST_RESTRICTED')),
  CONSTRAINT ck_compliance_cases_severity CHECK (severity IN ('S1','S2','S3')),
  CONSTRAINT ck_compliance_cases_status CHECK (status IN ('OPEN','IN_PROGRESS','AWAITING_INFO','PENDING_APPROVAL','DECIDED','CLOSED','REOPENED')),
  CONSTRAINT ck_compliance_cases_confidentiality CHECK (confidentiality IN ('NORMAL','RESTRICTED_STR')),
  CONSTRAINT ck_compliance_cases_source CHECK (source IN ('ALERT','SCREENING','KYC_REVIEW','REPORT','STAFF','REGULATOR_REQUEST','PROVIDER')),
  CONSTRAINT ck_compliance_cases_opened_by CHECK (
       (opened_by_type = 'STAFF'  AND opened_by IS NOT NULL AND opened_by_job IS NULL)
    OR (opened_by_type = 'SYSTEM' AND opened_by IS NULL     AND opened_by_job IS NOT NULL)),
  CONSTRAINT ck_compliance_cases_team CHECK (team IN ('COMPLIANCE','KYC_REVIEWER','FINANCE')),
  CONSTRAINT ck_compliance_cases_assigned CHECK ((assigned_to IS NULL) = (assigned_at IS NULL)),
  CONSTRAINT ck_compliance_cases_in_progress_assigned CHECK (status IN ('OPEN','CLOSED') OR assigned_to IS NOT NULL),
  CONSTRAINT ck_compliance_cases_sla CHECK (sla_due_at > opened_at),
  -- sanctions potential matches always block payouts (sanctions-screening §6 step 1)
  CONSTRAINT ck_compliance_cases_sanctions_block CHECK (case_type <> 'SANCTIONS' OR blocks_payouts),
  CONSTRAINT ck_compliance_cases_str_clock CHECK (confidentiality <> 'RESTRICTED_STR' OR suspicion_formed_at IS NOT NULL),
  CONSTRAINT ck_compliance_cases_decision CHECK (decision IS NULL OR decision IN ('CLEARED','EDD_CONDITIONS','RESTRICTED','SUSPENDED',
             'FROZEN','UNFROZEN','OFFBOARDED','REJECTED_FRAUD','CONFIRMED_MATCH','FALSE_POSITIVE','STR_FILED','STR_NOT_FILED',
             'HOLD_RELEASED','REFUND_DISPOSITION','RECOVERY_WRITE_OFF','REGULATOR_RESPONDED','REFERRED')),
  CONSTRAINT ck_compliance_cases_decided CHECK ((status IN ('DECIDED','CLOSED')) <= (decision IS NOT NULL AND decision_reason_code IS NOT NULL
                                                 AND decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CONSTRAINT ck_compliance_cases_closed CHECK ((status = 'CLOSED') = (closed_at IS NOT NULL AND closed_by IS NOT NULL
                                                                      AND closure_memo_evidence_id IS NOT NULL)),
  -- decisions that always need a checker (compliance-case-management §4)
  CONSTRAINT ck_compliance_cases_checker_required CHECK (
       decision IS NULL
    OR decision NOT IN ('FROZEN','UNFROZEN','OFFBOARDED','REJECTED_FRAUD','CONFIRMED_MATCH','STR_FILED','STR_NOT_FILED',
                        'REFUND_DISPOSITION','RECOVERY_WRITE_OFF','REGULATOR_RESPONDED')
    OR second_approver_id IS NOT NULL),
  CONSTRAINT ck_compliance_cases_sanctions_fp CHECK (NOT (case_type = 'SANCTIONS' AND decision = 'FALSE_POSITIVE')
                                                     OR second_approver_id IS NOT NULL),
  CONSTRAINT ck_compliance_cases_maker_checker CHECK (second_approver_id IS NULL OR second_approver_id <> decided_by),
  CONSTRAINT ck_compliance_cases_qa CHECK (qa_result IS NULL OR (qa_sampled AND qa_result IN ('PASS','FINDING'))),
  CONSTRAINT ck_compliance_cases_version CHECK (version >= 1)
);
COMMENT ON TABLE compliance.compliance_cases IS 'C3. Compliance/fraud/risk cases; RESTRICTED_STR rows protected by RLS.';
CREATE INDEX ix_compliance_cases_queue ON compliance.compliance_cases (team, status, severity, sla_due_at)
  WHERE status NOT IN ('CLOSED');
CREATE INDEX ix_compliance_cases_blocking ON compliance.compliance_cases (id) WHERE blocks_payouts AND status <> 'CLOSED';

CREATE TRIGGER trg_compliance_cases_guard_status BEFORE INSERT OR UPDATE OF status ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('compliance_case');
CREATE TRIGGER trg_compliance_cases_updated_at BEFORE UPDATE ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_compliance_cases_created_at BEFORE UPDATE ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION app.keep_created_at();
CREATE TRIGGER trg_compliance_cases_no_delete BEFORE DELETE ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_cases_no_truncate BEFORE TRUNCATE ON compliance.compliance_cases
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION compliance.compliance_cases_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  -- identity of the case never changes
  IF changed && ARRAY['id','case_number','case_type','opened_by_type','opened_by','opened_by_job','opened_at','source'] THEN
    RAISE EXCEPTION 'compliance case %: identity fields are immutable (attempted: %)', OLD.case_number, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  -- confidentiality only ever rises (once STR-restricted, always restricted)
  IF OLD.confidentiality = 'RESTRICTED_STR' AND NEW.confidentiality <> 'RESTRICTED_STR' THEN
    RAISE EXCEPTION 'compliance case %: RESTRICTED_STR confidentiality cannot be lowered', OLD.case_number
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.suspicion_formed_at IS NOT NULL AND NEW.suspicion_formed_at IS DISTINCT FROM OLD.suspicion_formed_at THEN
    RAISE EXCEPTION 'compliance case %: suspicion_formed_at is set once (STR deadline clock)', OLD.case_number
      USING ERRCODE = 'restrict_violation';
  END IF;
  -- a decision that needs a checker cannot skip PENDING_APPROVAL
  IF NEW.status = 'DECIDED' AND OLD.status = 'IN_PROGRESS' AND NEW.second_approver_id IS NOT NULL THEN
    RAISE EXCEPTION 'compliance case %: checker decisions go through PENDING_APPROVAL', OLD.case_number
      USING ERRCODE = 'check_violation';
  END IF;
  -- decision fields are frozen once DECIDED, until the case is REOPENED (a reopened case is decided afresh)
  IF OLD.status IN ('DECIDED','CLOSED') AND NEW.status <> 'REOPENED'
     AND changed && ARRAY['decision','decision_reason_code','decided_by','second_approver_id','decided_at'] THEN
    RAISE EXCEPTION 'compliance case %: decision is final unless the case is reopened', OLD.case_number
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF changed <> '{}' AND changed <> ARRAY['updated_at'] AND NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'compliance case %: version must increase by 1 on every change', OLD.case_number
      USING ERRCODE = 'restrict_violation';
  END IF;
  -- reopening clears the previous decision (the history keeps it in compliance_case_events)
  IF NEW.status = 'REOPENED' THEN
    NEW.decision := NULL; NEW.decision_reason_code := NULL; NEW.decided_by := NULL;
    NEW.second_approver_id := NULL; NEW.decided_at := NULL; NEW.closed_at := NULL; NEW.closed_by := NULL;
    NEW.closure_memo_evidence_id := NULL;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_compliance_cases_guard BEFORE UPDATE ON compliance.compliance_cases
  FOR EACH ROW EXECUTE FUNCTION compliance.compliance_cases_guard();

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_case_events — append-only timeline. Every case version has exactly one event
-- (deferred constraint trigger). Payload: codes and ids only (no C3); notes are REVIEW_NOTE evidence.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.compliance_case_events (
  id                  uuid        NOT NULL,
  case_id             uuid        NOT NULL REFERENCES compliance.compliance_cases(id),
  case_version        integer,
  event_type          text        NOT NULL,
  from_status         text,
  to_status           text,
  actor_type          text        NOT NULL,
  actor_id            uuid        REFERENCES app.users(id),
  actor_job           text,
  reason_code         text,
  payload             jsonb       NOT NULL DEFAULT '{}',
  evidence_record_ids uuid[]      NOT NULL DEFAULT '{}',
  audit_event_id      uuid,
  occurred_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_compliance_case_events PRIMARY KEY (id),
  CONSTRAINT ck_compliance_case_events_type CHECK (event_type IN ('OPENED','TRIAGED','ASSIGNED','STATUS_CHANGED','SEVERITY_CHANGED',
             'CONFIDENTIALITY_RAISED','BLOCKING_CHANGED','DECISION_PROPOSED','DECISION_APPROVED','DECISION_REJECTED','DECIDED',
             'CLOSED','REOPENED','NOTE_ADDED','EVIDENCE_ADDED','HOLD_LINKED','INFO_REQUESTED','INFO_RECEIVED','QA_RECORDED','SLA_BREACHED')),
  CONSTRAINT ck_compliance_case_events_actor CHECK (
       (actor_type = 'STAFF'  AND actor_id IS NOT NULL)
    OR (actor_type = 'SYSTEM' AND actor_id IS NULL AND actor_job IS NOT NULL)),
  CONSTRAINT ck_compliance_case_events_status_pair CHECK ((event_type = 'STATUS_CHANGED') <= (to_status IS NOT NULL))
);
COMMENT ON TABLE compliance.compliance_case_events IS 'C2/C3. Append-only case timeline (no C3 in payload).';
CREATE UNIQUE INDEX uq_compliance_case_events_version ON compliance.compliance_case_events (case_id, case_version)
  WHERE case_version IS NOT NULL;
CREATE TRIGGER trg_compliance_case_events_no_mutation BEFORE UPDATE OR DELETE ON compliance.compliance_case_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_case_events_no_truncate BEFORE TRUNCATE ON compliance.compliance_case_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION compliance.cases_require_event() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE v integer;
BEGIN
  SELECT version INTO v FROM compliance.compliance_cases WHERE id = NEW.id;
  IF NOT EXISTS (SELECT 1 FROM compliance.compliance_case_events e WHERE e.case_id = NEW.id AND e.case_version = v) THEN
    RAISE EXCEPTION 'compliance case % version % has no compliance_case_events row', NEW.case_number, v
      USING ERRCODE = 'check_violation';
  END IF;
  RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER trg_compliance_cases_require_event AFTER INSERT OR UPDATE ON compliance.compliance_cases
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION compliance.cases_require_event();

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_case_links — subjects and related objects of a case (Stage 1 case_subjects +
-- case_links). Append-only; a mistaken link is corrected by an UNLINKED row (link_action).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.compliance_case_links (
  id            uuid        NOT NULL,
  case_id       uuid        NOT NULL REFERENCES compliance.compliance_cases(id),
  link_action   text        NOT NULL DEFAULT 'LINKED',
  unlinks_id    uuid        REFERENCES compliance.compliance_case_links(id),
  subject_type  text        NOT NULL,
  subject_id    uuid        NOT NULL,
  role          text        NOT NULL,
  linked_by_type text       NOT NULL,
  linked_by     uuid        REFERENCES app.users(id),
  linked_at     timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_compliance_case_links PRIMARY KEY (id),
  CONSTRAINT uq_compliance_case_links_unlink UNIQUE (unlinks_id),
  CONSTRAINT ck_compliance_case_links_action CHECK (link_action IN ('LINKED','UNLINKED')),
  CONSTRAINT ck_compliance_case_links_unlink CHECK ((link_action = 'UNLINKED') = (unlinks_id IS NOT NULL)),
  CONSTRAINT ck_compliance_case_links_subject CHECK (subject_type IN ('USER','ORGANISATION','CAMPAIGN','BENEFICIARY',
             'PAYOUT_DESTINATION','PAYMENT','PAYOUT','DONOR_GUEST','INSTITUTION_PAYEE',
             'ALERT','HOLD','DISPUTE','INCIDENT','REFUND','STR_REPORT','REGULATOR_REQUEST','SCREENING_REQUEST','KYC_CASE','KYB_CASE')),
  CONSTRAINT ck_compliance_case_links_role CHECK (role IN ('PRIMARY_SUBJECT','RELATED_SUBJECT','RELATED_OBJECT')),
  -- only parties can be subjects; objects (alerts, holds, disputes...) are RELATED_OBJECT
  CONSTRAINT ck_compliance_case_links_role_kind CHECK ((role = 'RELATED_OBJECT') = (subject_type IN ('PAYMENT','PAYOUT','ALERT','HOLD',
             'DISPUTE','INCIDENT','REFUND','STR_REPORT','REGULATOR_REQUEST','SCREENING_REQUEST','KYC_CASE','KYB_CASE'))),
  CONSTRAINT ck_compliance_case_links_by CHECK ((linked_by_type = 'STAFF') = (linked_by IS NOT NULL) AND linked_by_type IN ('STAFF','SYSTEM'))
);
COMMENT ON TABLE compliance.compliance_case_links IS 'C2. Append-only case subjects and related objects.';
CREATE INDEX ix_compliance_case_links_subject ON compliance.compliance_case_links (subject_type, subject_id);
CREATE INDEX ix_compliance_case_links_case ON compliance.compliance_case_links (case_id);
CREATE TRIGGER trg_compliance_case_links_no_mutation BEFORE UPDATE OR DELETE ON compliance.compliance_case_links
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_case_links_no_truncate BEFORE TRUNCATE ON compliance.compliance_case_links
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- Currently linked subjects (LINKED rows without a later UNLINKED row).
CREATE VIEW compliance.v_case_subjects_current AS
  SELECT l.case_id, l.subject_type, l.subject_id, l.role
    FROM compliance.compliance_case_links l
   WHERE l.link_action = 'LINKED' AND l.role <> 'RELATED_OBJECT'
     AND NOT EXISTS (SELECT 1 FROM compliance.compliance_case_links u WHERE u.unlinks_id = l.id);

-- -----------------------------------------------------------------------------------------------------
-- EC-13 view for fundzim_app (baseline §5.14): subject, case id and the flag only. No type, severity,
-- confidentiality, decision, reason or narrative, so restricted (STR) cases block payouts without being
-- described (tipping-off, LR-008/LR-072). The case id is an opaque reference the app role cannot
-- dereference (it has no privilege on compliance_cases); it lets payout_eligibility_decisions record which
-- case blocked, for the compliance module to explain to authorised staff.
-- Owned by fundzim_migrator (0018), the owner of the base tables, so RLS on compliance_cases does not
-- filter it: restricted cases DO block payouts. Not SECURITY DEFINER (no functions involved).
-- -----------------------------------------------------------------------------------------------------
CREATE VIEW compliance.v_payout_blocking_cases AS
  SELECT DISTINCT s.subject_type, s.subject_id, c.id AS case_id, true AS blocks_payouts
    FROM compliance.compliance_cases c
    JOIN compliance.v_case_subjects_current s ON s.case_id = c.id
   WHERE c.blocks_payouts AND c.status <> 'CLOSED'
     AND s.subject_type IN ('USER','ORGANISATION','CAMPAIGN','BENEFICIARY','PAYOUT_DESTINATION','INSTITUTION_PAYEE');
COMMENT ON VIEW compliance.v_payout_blocking_cases IS 'EC-13 read model for fundzim_app; exposes subject + flag only.';

-- -----------------------------------------------------------------------------------------------------
-- compliance.screening_requests — one per (subject, list versions) screening run; idempotent
-- (sanctions-screening §8). Lists and versions travel as data (see concern C-2: list-source registry).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.screening_requests (
  id                    uuid        NOT NULL,
  subject_type          text        NOT NULL,
  subject_id            uuid        NOT NULL,
  trigger               text        NOT NULL,
  list_kinds            text[]      NOT NULL,
  list_versions         jsonb       NOT NULL,             -- {"<list_source_code>": "<version_hash>", ...}
  list_versions_sha256  bytea       NOT NULL,             -- hash of canonical list_versions (idempotency)
  vendor_code           text,                              -- PD-29
  status                text        NOT NULL,
  requested_by_type     text        NOT NULL,
  requested_by          uuid        REFERENCES app.users(id),
  requested_at          timestamptz NOT NULL DEFAULT now(),
  completed_at          timestamptz,
  version               integer     NOT NULL DEFAULT 1,
  CONSTRAINT pk_screening_requests PRIMARY KEY (id),
  CONSTRAINT uq_screening_requests_idem UNIQUE (subject_type, subject_id, list_versions_sha256),
  CONSTRAINT ck_screening_requests_subject CHECK (subject_type IN ('USER','ORGANISATION','ORGANISATION_PERSON','BENEFICIARY',
             'INSTITUTION_PAYEE','PAYOUT_DESTINATION_HOLDER','DONOR')),
  CONSTRAINT ck_screening_requests_trigger CHECK (trigger IN ('ONBOARDING','LIST_UPDATE','PERIODIC','EVENT','PRE_PAYOUT','MANUAL')),
  CONSTRAINT ck_screening_requests_kinds CHECK (cardinality(list_kinds) >= 1 AND list_kinds <@ ARRAY['SANCTIONS','PEP','INTERNAL']),
  CONSTRAINT ck_screening_requests_versions CHECK (jsonb_typeof(list_versions) = 'object' AND list_versions <> '{}'::jsonb),
  CONSTRAINT ck_screening_requests_hash CHECK (octet_length(list_versions_sha256) = 32),
  CONSTRAINT ck_screening_requests_status CHECK (status IN ('PENDING','COMPLETED','DEFERRED','FAILED')),
  CONSTRAINT ck_screening_requests_completed CHECK ((status = 'COMPLETED') = (completed_at IS NOT NULL)),
  CONSTRAINT ck_screening_requests_by CHECK ((requested_by_type = 'STAFF') = (requested_by IS NOT NULL) AND requested_by_type IN ('STAFF','SYSTEM'))
);
COMMENT ON TABLE compliance.screening_requests IS 'C2. Screening runs per subject and list versions.';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('screening_request', '',         'PENDING'),
  ('screening_request', 'PENDING',  'COMPLETED'),
  ('screening_request', 'PENDING',  'DEFERRED'),           -- vendor unavailable: EC-15 -> DEFER, never pass
  ('screening_request', 'PENDING',  'FAILED'),
  ('screening_request', 'DEFERRED', 'PENDING');
CREATE TRIGGER trg_screening_requests_guard_status BEFORE INSERT OR UPDATE OF status ON compliance.screening_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('screening_request');
CREATE TRIGGER trg_screening_requests_no_delete BEFORE DELETE ON compliance.screening_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_screening_requests_no_truncate BEFORE TRUNCATE ON compliance.screening_requests
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION compliance.screening_requests_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT changed <@ ARRAY['status','completed_at','version'] OR NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'screening request %: only status/completion may change, with version + 1 (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_screening_requests_guard BEFORE UPDATE ON compliance.screening_requests
  FOR EACH ROW EXECUTE FUNCTION compliance.screening_requests_guard();

-- -----------------------------------------------------------------------------------------------------
-- compliance.sanctions_screening_results — append-only determinations. A vendor result is CLEAR / REVIEW
-- / POTENTIAL_MATCH; human review appends a CLEARED_BY_REVIEW or CONFIRMED_MATCH row superseding it.
-- EC-15 passes only on the latest row being CLEAR or CLEARED_BY_REVIEW with valid_until > now().
-- valid_until is computed from the limit `screening.freshness_seconds` (its version id is recorded).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.sanctions_screening_results (
  id                    uuid        NOT NULL,
  request_id            uuid        NOT NULL REFERENCES compliance.screening_requests(id),
  subject_type          text        NOT NULL,
  subject_id            uuid        NOT NULL,
  result                text        NOT NULL,
  strong_match          boolean     NOT NULL DEFAULT false,   -- exact name+DOB+ID: holds placed immediately
  list_versions         jsonb       NOT NULL,
  screened_at           timestamptz NOT NULL,
  valid_until           timestamptz NOT NULL,
  freshness_limit_id    uuid        NOT NULL,                  -- risk.limits version used (no FK; validated by service)
  supersedes_result_id  uuid        REFERENCES compliance.sanctions_screening_results(id),
  decided_by            uuid        REFERENCES app.users(id),
  second_reviewer_id    uuid        REFERENCES app.users(id),
  reason_code           text,
  evidence_record_id    uuid        NOT NULL REFERENCES audit.evidence_records(id),   -- vendor report / review note (C3)
  recorded_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_sanctions_screening_results PRIMARY KEY (id),
  CONSTRAINT uq_sanctions_screening_results_supersedes UNIQUE (supersedes_result_id),
  CONSTRAINT ck_ssr_result CHECK (result IN ('CLEAR','REVIEW','POTENTIAL_MATCH','CONFIRMED_MATCH','CLEARED_BY_REVIEW')),
  CONSTRAINT ck_ssr_freshness CHECK (valid_until > screened_at),
  CONSTRAINT ck_ssr_strong CHECK (NOT strong_match OR result = 'POTENTIAL_MATCH'),
  -- vendor/system results vs human determinations
  CONSTRAINT ck_ssr_review_shape CHECK (
       (result IN ('CLEAR','REVIEW','POTENTIAL_MATCH') AND supersedes_result_id IS NULL AND decided_by IS NULL AND second_reviewer_id IS NULL)
    OR (result IN ('CLEARED_BY_REVIEW','CONFIRMED_MATCH') AND supersedes_result_id IS NOT NULL AND decided_by IS NOT NULL
        AND reason_code IS NOT NULL)),
  CONSTRAINT ck_ssr_confirmed_second CHECK (result <> 'CONFIRMED_MATCH' OR second_reviewer_id IS NOT NULL),
  CONSTRAINT ck_ssr_second_distinct CHECK (second_reviewer_id IS NULL OR second_reviewer_id <> decided_by)
);
COMMENT ON TABLE compliance.sanctions_screening_results IS 'C3. Append-only screening determinations with list versions and freshness.';
CREATE INDEX ix_ssr_subject_latest ON compliance.sanctions_screening_results (subject_type, subject_id, recorded_at DESC);
CREATE TRIGGER trg_ssr_no_mutation BEFORE UPDATE OR DELETE ON compliance.sanctions_screening_results
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_ssr_no_truncate BEFORE TRUNCATE ON compliance.sanctions_screening_results
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION compliance.ssr_supersede_check() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prev compliance.sanctions_screening_results%ROWTYPE;
BEGIN
  IF NEW.supersedes_result_id IS NOT NULL THEN
    SELECT * INTO prev FROM compliance.sanctions_screening_results WHERE id = NEW.supersedes_result_id;
    IF prev.result NOT IN ('REVIEW','POTENTIAL_MATCH') OR prev.subject_id <> NEW.subject_id OR prev.subject_type <> NEW.subject_type THEN
      RAISE EXCEPTION 'screening review must supersede a REVIEW or POTENTIAL_MATCH result of the same subject' USING ERRCODE = 'check_violation';
    END IF;
    -- resolving a POTENTIAL_MATCH either way (false positive or confirmed) needs COMPLIANCE + a second
    -- reviewer (sanctions-screening §6)
    IF prev.result = 'POTENTIAL_MATCH' AND NEW.second_reviewer_id IS NULL THEN
      RAISE EXCEPTION 'resolving a potential match requires a second reviewer' USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM compliance.screening_requests r WHERE r.id = NEW.request_id
                    AND r.subject_type = NEW.subject_type AND r.subject_id = NEW.subject_id) THEN
    RAISE EXCEPTION 'screening result subject must match its request' USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_ssr_supersede BEFORE INSERT ON compliance.sanctions_screening_results
  FOR EACH ROW EXECUTE FUNCTION compliance.ssr_supersede_check();

-- -----------------------------------------------------------------------------------------------------
-- compliance.screening_hits — individual list-entry hits of a result. Disposition set once.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.screening_hits (
  id                  uuid        NOT NULL,
  result_id           uuid        NOT NULL REFERENCES compliance.sanctions_screening_results(id),
  list_source_code    text        NOT NULL,
  list_version_hash   text        NOT NULL,
  entry_ref           text        NOT NULL,
  score               integer     NOT NULL,
  matched_fields      text[]      NOT NULL,
  status              text        NOT NULL,
  decided_by          uuid        REFERENCES app.users(id),
  second_reviewer_id  uuid        REFERENCES app.users(id),
  decided_at          timestamptz,
  reason_code         text,
  created_at          timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_screening_hits PRIMARY KEY (id),
  CONSTRAINT uq_screening_hits_entry UNIQUE (result_id, list_source_code, entry_ref),
  CONSTRAINT ck_screening_hits_score CHECK (score BETWEEN 0 AND 1000),
  CONSTRAINT ck_screening_hits_fields CHECK (cardinality(matched_fields) >= 1 AND matched_fields <@ ARRAY['NAME','ALIAS','DOB',
             'NATIONALITY','ID_NUMBER','REGISTRATION_NUMBER','COUNTRY']),
  CONSTRAINT ck_screening_hits_status CHECK (status IN ('OPEN','CLEARED_BY_REVIEW','FALSE_POSITIVE','CONFIRMED_MATCH')),
  CONSTRAINT ck_screening_hits_decided CHECK ((status = 'OPEN') = (decided_by IS NULL AND decided_at IS NULL AND reason_code IS NULL)),
  CONSTRAINT ck_screening_hits_second CHECK (status NOT IN ('FALSE_POSITIVE','CONFIRMED_MATCH') OR second_reviewer_id IS NOT NULL),
  CONSTRAINT ck_screening_hits_distinct CHECK (second_reviewer_id IS NULL OR second_reviewer_id <> decided_by)
);
COMMENT ON TABLE compliance.screening_hits IS 'C3. List-entry hits; disposition set once.';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('screening_hit', '',     'OPEN'),
  ('screening_hit', 'OPEN', 'CLEARED_BY_REVIEW'),
  ('screening_hit', 'OPEN', 'FALSE_POSITIVE'),
  ('screening_hit', 'OPEN', 'CONFIRMED_MATCH');
CREATE TRIGGER trg_screening_hits_guard_status BEFORE INSERT OR UPDATE OF status ON compliance.screening_hits
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('screening_hit');
CREATE TRIGGER trg_screening_hits_no_delete BEFORE DELETE ON compliance.screening_hits
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_screening_hits_no_truncate BEFORE TRUNCATE ON compliance.screening_hits
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION compliance.screening_hits_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.status <> 'OPEN' OR NOT changed <@ ARRAY['status','decided_by','second_reviewer_id','decided_at','reason_code'] THEN
    RAISE EXCEPTION 'screening hit %: disposition is set once; hit content is immutable (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_screening_hits_guard BEFORE UPDATE ON compliance.screening_hits
  FOR EACH ROW EXECUTE FUNCTION compliance.screening_hits_guard();

-- -----------------------------------------------------------------------------------------------------
-- compliance.str_reports — suspicious-transaction report preparation (compliance-case-management §6).
-- Restricted: RLS (0018) hides every row unless fundzim.str_access = 'on'. Content is never stored here:
-- the draft, the rationale and the FIU acknowledgement are evidence objects (private bucket reports/).
-- Maker (prepared_by) and checker (approved_by) differ. The deadline is computed by the service from the
-- limit `str.deadline` and the working-day calendar (LR-060); it is stored, not derived here.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.str_reports (
  id                     uuid        NOT NULL,
  case_id                uuid        NOT NULL REFERENCES compliance.compliance_cases(id),
  status                 text        NOT NULL,
  suspicion_formed_at    timestamptz NOT NULL,
  deadline_at            timestamptz NOT NULL,
  deadline_limit_id      uuid        NOT NULL,              -- risk.limits version used (no FK)
  prepared_by            uuid        NOT NULL REFERENCES app.users(id),
  draft_evidence_id      uuid        REFERENCES audit.evidence_records(id),
  decision               text,
  rationale_evidence_id  uuid        REFERENCES audit.evidence_records(id),
  approved_by            uuid        REFERENCES app.users(id),
  approved_at            timestamptz,
  filing_channel         text,
  legal_basis_ref        text,                               -- LR-060 position / counsel reference
  filed_by               uuid        REFERENCES app.users(id),
  filed_at               timestamptz,
  fiu_ack_evidence_id    uuid        REFERENCES audit.evidence_records(id),
  version                integer     NOT NULL DEFAULT 1,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_str_reports PRIMARY KEY (id),
  CONSTRAINT ck_str_reports_status CHECK (status IN ('DRAFT','PENDING_APPROVAL','APPROVED_TO_FILE','APPROVED_NOT_TO_FILE','FILED')),
  CONSTRAINT ck_str_reports_deadline CHECK (deadline_at > suspicion_formed_at),
  CONSTRAINT ck_str_reports_decision CHECK (decision IS NULL OR decision IN ('FILE','DO_NOT_FILE')),
  CONSTRAINT ck_str_reports_proposal CHECK (status = 'DRAFT' OR (decision IS NOT NULL AND draft_evidence_id IS NOT NULL
                                                                 AND rationale_evidence_id IS NOT NULL)),
  CONSTRAINT ck_str_reports_approved CHECK ((status IN ('APPROVED_TO_FILE','APPROVED_NOT_TO_FILE','FILED'))
                                            = (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  CONSTRAINT ck_str_reports_decision_status CHECK ((status <> 'APPROVED_NOT_TO_FILE' OR decision = 'DO_NOT_FILE')
                                                   AND (status NOT IN ('APPROVED_TO_FILE','FILED') OR decision = 'FILE')),
  CONSTRAINT ck_str_reports_maker_checker CHECK (approved_by IS NULL OR approved_by <> prepared_by),
  CONSTRAINT ck_str_reports_channel CHECK (filing_channel IS NULL OR filing_channel IN ('GOAML_MANUAL','PSP_ESCALATION')),
  CONSTRAINT ck_str_reports_filed CHECK ((status = 'FILED') = (filed_at IS NOT NULL AND filed_by IS NOT NULL AND filing_channel IS NOT NULL
                                                               AND legal_basis_ref IS NOT NULL)),
  CONSTRAINT ck_str_reports_version CHECK (version >= 1)
);
COMMENT ON TABLE compliance.str_reports IS 'C3 RESTRICTED. STR preparation metadata only; content in evidence objects. RLS.';
CREATE UNIQUE INDEX uq_str_reports_open_per_case ON compliance.str_reports (case_id) WHERE status IN ('DRAFT','PENDING_APPROVAL');
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('str_report', '',                 'DRAFT'),
  ('str_report', 'DRAFT',            'PENDING_APPROVAL'),
  ('str_report', 'PENDING_APPROVAL', 'DRAFT'),
  ('str_report', 'PENDING_APPROVAL', 'APPROVED_TO_FILE'),
  ('str_report', 'PENDING_APPROVAL', 'APPROVED_NOT_TO_FILE'),
  ('str_report', 'APPROVED_TO_FILE', 'FILED');
CREATE TRIGGER trg_str_reports_guard_status BEFORE INSERT OR UPDATE OF status ON compliance.str_reports
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('str_report');
CREATE TRIGGER trg_str_reports_no_delete BEFORE DELETE ON compliance.str_reports
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_str_reports_no_truncate BEFORE TRUNCATE ON compliance.str_reports
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION compliance.str_reports_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NOT EXISTS (SELECT 1 FROM compliance.compliance_cases c WHERE c.id = NEW.case_id
                     AND c.confidentiality = 'RESTRICTED_STR' AND c.suspicion_formed_at = NEW.suspicion_formed_at) THEN
      RAISE EXCEPTION 'STR report requires a RESTRICTED_STR case with the same suspicion_formed_at' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF changed && ARRAY['id','case_id','suspicion_formed_at','deadline_at','deadline_limit_id','prepared_by','created_at'] THEN
    RAISE EXCEPTION 'STR report %: identity, clock and maker are immutable (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  IF OLD.status IN ('APPROVED_NOT_TO_FILE','FILED') THEN
    RAISE EXCEPTION 'STR report % is final', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  IF NEW.version <> OLD.version + 1 THEN
    RAISE EXCEPTION 'STR report %: version must increase by 1', OLD.id USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_str_reports_guard BEFORE INSERT OR UPDATE ON compliance.str_reports
  FOR EACH ROW EXECUTE FUNCTION compliance.str_reports_guard();

-- -----------------------------------------------------------------------------------------------------
-- compliance.compliance_restrictions — capability restrictions on a subject, with mandatory expiry and
-- maker-checker (baseline §5.14). The maximum duration is the approved limit
-- `compliance.restriction.max_duration` (DURATION); with no approved limit the restriction cannot be
-- activated (fail closed: use a hold instead, which has no expiry). A RECEIVE_PAYOUT restriction must name
-- the COMPLIANCE_HOLD placed in the same transaction (payout eligibility checks holds only, EC-12).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.compliance_restrictions (
  id                     uuid        NOT NULL,
  case_id                uuid        NOT NULL REFERENCES compliance.compliance_cases(id),
  subject_type           text        NOT NULL,
  subject_id             uuid        NOT NULL,
  capability             text        NOT NULL,
  status                 text        NOT NULL,
  reason_code            text        NOT NULL,
  requested_by           uuid        NOT NULL REFERENCES app.users(id),
  requested_at           timestamptz NOT NULL DEFAULT now(),
  approved_by            uuid        REFERENCES app.users(id),
  approved_at            timestamptz,
  starts_at              timestamptz NOT NULL,
  expires_at             timestamptz NOT NULL,
  max_duration_limit_id  uuid,                                -- risk.limits version that bounded expires_at
  hold_id                uuid,                                -- risk.holds row (COMPLIANCE_HOLD) for RECEIVE_PAYOUT
  lifted_by              uuid        REFERENCES app.users(id),
  lift_approved_by       uuid        REFERENCES app.users(id),
  lifted_at              timestamptz,
  lift_reason_code       text,
  version                integer     NOT NULL DEFAULT 1,
  CONSTRAINT pk_compliance_restrictions PRIMARY KEY (id),
  CONSTRAINT ck_compliance_restrictions_subject CHECK (subject_type IN ('USER','ORGANISATION','CAMPAIGN','BENEFICIARY','PAYOUT_DESTINATION')),
  CONSTRAINT ck_compliance_restrictions_capability CHECK (capability IN ('CREATE_CAMPAIGN','SUBMIT_CAMPAIGN','PUBLISH_CAMPAIGN',
             'RECEIVE_DONATIONS','DONATE','RECEIVE_PAYOUT','REQUEST_REFUND','CHANGE_PAYOUT_DESTINATION','REPRESENT_ORGANISATION')),
  CONSTRAINT ck_compliance_restrictions_capability_subject CHECK (
       (capability IN ('CREATE_CAMPAIGN','DONATE','REQUEST_REFUND','CHANGE_PAYOUT_DESTINATION','REPRESENT_ORGANISATION') AND subject_type = 'USER')
    OR (capability IN ('SUBMIT_CAMPAIGN','PUBLISH_CAMPAIGN','RECEIVE_DONATIONS') AND subject_type IN ('USER','ORGANISATION','CAMPAIGN'))
    OR (capability = 'RECEIVE_PAYOUT')),
  CONSTRAINT ck_compliance_restrictions_status CHECK (status IN ('PENDING_APPROVAL','ACTIVE','REJECTED','WITHDRAWN','LIFTED','EXPIRED')),
  CONSTRAINT ck_compliance_restrictions_reason CHECK (reason_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_compliance_restrictions_window CHECK (expires_at > starts_at),
  CONSTRAINT ck_compliance_restrictions_maker_checker CHECK (approved_by IS NULL OR approved_by <> requested_by),
  CONSTRAINT ck_compliance_restrictions_approved CHECK ((status IN ('ACTIVE','LIFTED','EXPIRED'))
                                                        = (approved_by IS NOT NULL AND approved_at IS NOT NULL
                                                           AND max_duration_limit_id IS NOT NULL)),
  CONSTRAINT ck_compliance_restrictions_hold CHECK (capability <> 'RECEIVE_PAYOUT' OR status IN ('PENDING_APPROVAL','REJECTED','WITHDRAWN')
                                                    OR hold_id IS NOT NULL),
  CONSTRAINT ck_compliance_restrictions_lifted CHECK ((status = 'LIFTED') = (lifted_at IS NOT NULL AND lifted_by IS NOT NULL
                                                      AND lift_approved_by IS NOT NULL AND lift_reason_code IS NOT NULL)),
  CONSTRAINT ck_compliance_restrictions_lift_checker CHECK (lift_approved_by IS NULL OR lift_approved_by <> lifted_by)
);
COMMENT ON TABLE compliance.compliance_restrictions IS 'C2. Capability restrictions with mandatory expiry and maker-checker.';
CREATE INDEX ix_compliance_restrictions_active ON compliance.compliance_restrictions (subject_type, subject_id, capability)
  WHERE status = 'ACTIVE';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('compliance_restriction', '',                 'PENDING_APPROVAL'),
  ('compliance_restriction', 'PENDING_APPROVAL', 'ACTIVE'),
  ('compliance_restriction', 'PENDING_APPROVAL', 'REJECTED'),
  ('compliance_restriction', 'PENDING_APPROVAL', 'WITHDRAWN'),
  ('compliance_restriction', 'ACTIVE',           'LIFTED'),
  ('compliance_restriction', 'ACTIVE',           'EXPIRED');
CREATE TRIGGER trg_compliance_restrictions_guard_status BEFORE INSERT OR UPDATE OF status ON compliance.compliance_restrictions
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('compliance_restriction');
CREATE TRIGGER trg_compliance_restrictions_no_delete BEFORE DELETE ON compliance.compliance_restrictions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_compliance_restrictions_no_truncate BEFORE TRUNCATE ON compliance.compliance_restrictions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION compliance.compliance_restrictions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  changed text[];
  lim_seconds bigint;
BEGIN
  IF TG_OP = 'UPDATE' THEN
    SELECT coalesce(array_agg(n.key), '{}') INTO changed
      FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
    IF changed && ARRAY['id','case_id','subject_type','subject_id','capability','reason_code','requested_by','requested_at',
                        'starts_at','expires_at'] THEN
      RAISE EXCEPTION 'restriction %: scope and expiry are immutable (attempted: %) — create a new restriction', OLD.id, changed
        USING ERRCODE = 'restrict_violation';
    END IF;
    IF NEW.version <> OLD.version + 1 THEN
      RAISE EXCEPTION 'restriction %: version must increase by 1', OLD.id USING ERRCODE = 'restrict_violation';
    END IF;
  END IF;
  IF NEW.status = 'ACTIVE' AND (TG_OP = 'INSERT' OR OLD.status <> 'ACTIVE') THEN
    -- the bounding limit must be an APPROVED, effective DURATION limit with the expected key
    SELECT l.value_seconds INTO lim_seconds FROM risk.limits l
     WHERE l.id = NEW.max_duration_limit_id AND l.limit_key = 'compliance.restriction.max_duration'
       AND l.status = 'APPROVED' AND l.value_kind = 'DURATION'
       AND l.effective_from <= now() AND (l.effective_to IS NULL OR l.effective_to > now());
    IF lim_seconds IS NULL THEN
      RAISE EXCEPTION 'restriction %: no approved effective limit compliance.restriction.max_duration (fail closed)', NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.expires_at - NEW.starts_at > make_interval(secs => lim_seconds) THEN
      RAISE EXCEPTION 'restriction %: duration exceeds the approved maximum', NEW.id USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.capability = 'RECEIVE_PAYOUT' AND NOT EXISTS (
         SELECT 1 FROM risk.holds h WHERE h.id = NEW.hold_id AND h.hold_type = 'COMPLIANCE_HOLD' AND h.released_at IS NULL
           AND h.case_id = NEW.case_id AND h.scope_id = NEW.subject_id AND h.scope_type = NEW.subject_type) THEN
      RAISE EXCEPTION 'restriction %: RECEIVE_PAYOUT needs an active COMPLIANCE_HOLD on the same subject and case', NEW.id
        USING ERRCODE = 'check_violation';
    END IF;
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_compliance_restrictions_guard BEFORE INSERT OR UPDATE ON compliance.compliance_restrictions
  FOR EACH ROW EXECUTE FUNCTION compliance.compliance_restrictions_guard();

-- -----------------------------------------------------------------------------------------------------
-- EC-15 view for fundzim_app (baseline §5.14): latest screening determination per subject. No hits, scores,
-- list entries or reviewer identities. `passes` is true only for CLEAR / CLEARED_BY_REVIEW still fresh.
-- -----------------------------------------------------------------------------------------------------
CREATE VIEW compliance.v_screening_status AS
  SELECT DISTINCT ON (r.subject_type, r.subject_id)
         r.subject_type, r.subject_id, r.result AS latest_result, r.screened_at, r.valid_until,
         r.list_versions AS list_version,
         (r.result IN ('CLEAR','CLEARED_BY_REVIEW') AND r.valid_until > now()) AS passes
    FROM compliance.sanctions_screening_results r
   ORDER BY r.subject_type, r.subject_id, r.recorded_at DESC, r.id DESC;
COMMENT ON VIEW compliance.v_screening_status IS 'EC-15 read model for fundzim_app; no hit details.';

-- -----------------------------------------------------------------------------------------------------
-- compliance.screening_callback_inbox — screening-vendor callbacks (list-update deltas, ongoing-monitoring
-- hits). Same pattern as kyc.vendor_callback_inbox: verified only, dedup, raw body encrypted (it can name
-- matched persons) and append-only; only processing state moves.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.screening_callback_inbox (
  id                     uuid        NOT NULL,
  vendor_code            text        NOT NULL,
  vendor_event_id        text        NOT NULL,
  event_type             text        NOT NULL,
  signature_verified     boolean     NOT NULL,
  verification_method    text        NOT NULL,
  signing_key_ref        text        NOT NULL,
  raw_payload_ciphertext bytea       NOT NULL,
  raw_payload_key_id     text        NOT NULL,
  raw_payload_sha256     bytea       NOT NULL,
  received_at            timestamptz NOT NULL DEFAULT now(),
  processing_status      text        NOT NULL DEFAULT 'RECEIVED',
  attempts               integer     NOT NULL DEFAULT 0,
  last_error_code        text,
  processed_at           timestamptz,
  screening_request_id   uuid        REFERENCES compliance.screening_requests(id),
  CONSTRAINT pk_screening_callback_inbox PRIMARY KEY (id),
  CONSTRAINT uq_screening_callback_inbox_event UNIQUE (vendor_code, vendor_event_id),
  CONSTRAINT ck_screening_callback_inbox_verified CHECK (signature_verified),
  CONSTRAINT ck_screening_callback_inbox_method CHECK (verification_method IN ('HMAC','ASYMMETRIC_SIGNATURE','AUTHENTICATED_STATUS_QUERY')),
  CONSTRAINT ck_screening_callback_inbox_hash CHECK (octet_length(raw_payload_sha256) = 32 AND octet_length(raw_payload_ciphertext) >= 28),
  CONSTRAINT ck_screening_callback_inbox_status CHECK (processing_status IN ('RECEIVED','PROCESSED','IGNORED_UNKNOWN_TYPE','FAILED')),
  CONSTRAINT ck_screening_callback_inbox_processed CHECK ((processing_status IN ('PROCESSED','IGNORED_UNKNOWN_TYPE')) = (processed_at IS NOT NULL)),
  CONSTRAINT ck_screening_callback_inbox_attempts CHECK (attempts >= 0)
);
COMMENT ON TABLE compliance.screening_callback_inbox IS 'C3. Verified, deduplicated screening-vendor callbacks; raw body encrypted and immutable.';
CREATE INDEX ix_screening_callback_inbox_pending ON compliance.screening_callback_inbox (received_at)
  WHERE processing_status IN ('RECEIVED','FAILED');
CREATE TRIGGER trg_screening_callback_inbox_no_delete BEFORE DELETE ON compliance.screening_callback_inbox
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_screening_callback_inbox_no_truncate BEFORE TRUNCATE ON compliance.screening_callback_inbox
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION compliance.screening_callback_inbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT changed <@ ARRAY['processing_status','attempts','last_error_code','processed_at','screening_request_id']
     OR OLD.processing_status IN ('PROCESSED','IGNORED_UNKNOWN_TYPE') THEN
    RAISE EXCEPTION 'screening callback %: raw callback is append-only; processing is final once done (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_screening_callback_inbox_guard BEFORE UPDATE ON compliance.screening_callback_inbox
  FOR EACH ROW EXECUTE FUNCTION compliance.screening_callback_inbox_guard();

-- -----------------------------------------------------------------------------------------------------
-- Restriction read model for fundzim_app (baseline §5.14): campaigns/payments check CREATE_CAMPAIGN /
-- DONATE / ... inside their own transaction. Active-now rows only; no case id, reason or requester.
-- (A row past expires_at is excluded even before the expiry job moves it to EXPIRED.)
-- -----------------------------------------------------------------------------------------------------
CREATE VIEW compliance.v_active_restrictions AS
  SELECT r.subject_type, r.subject_id, r.capability, r.expires_at
    FROM compliance.compliance_restrictions r
   WHERE r.status = 'ACTIVE' AND r.starts_at <= now() AND r.expires_at > now();
COMMENT ON VIEW compliance.v_active_restrictions IS 'Restriction read model for fundzim_app; no reasons.';

-- -----------------------------------------------------------------------------------------------------
-- compliance.screening_list_sources — registry of screened lists (sanctions-screening §2; lead decision
-- C-2). The mandatory basis is recorded with its reference (REGULATORY: LR id until counsel confirms;
-- PROVIDER: PCR id; INTERNAL_RISK: decision id). Staleness is checked against limit
-- `screening.list_staleness_max`; a stale list makes EC-15 fail closed. Registration is maker-checker.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.screening_list_sources (
  id                   uuid        NOT NULL,
  source_code          text        NOT NULL,                -- e.g. UN_CONSOLIDATED, ZW_DOMESTIC, FUNDZIM_INTERNAL
  name                 text        NOT NULL,
  authority            text        NOT NULL,
  list_type            text        NOT NULL,
  mandatory_basis      text        NOT NULL,
  basis_ref            text        NOT NULL,
  update_frequency     text        NOT NULL,                -- as published by the source (descriptive)
  status               text        NOT NULL DEFAULT 'PROPOSED',
  last_ingested_at     timestamptz,
  version_hash         text,
  created_by           uuid        NOT NULL REFERENCES app.users(id),
  approved_by          uuid        REFERENCES app.users(id),
  approved_at          timestamptz,
  version              integer     NOT NULL DEFAULT 1,
  created_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_screening_list_sources PRIMARY KEY (id),
  CONSTRAINT uq_screening_list_sources_code UNIQUE (source_code),
  CONSTRAINT ck_sls_code CHECK (source_code ~ '^[A-Z][A-Z0-9_]{2,63}$'),
  CONSTRAINT ck_sls_type CHECK (list_type IN ('SANCTIONS','PEP','INTERNAL')),
  CONSTRAINT ck_sls_basis CHECK (
       (mandatory_basis = 'REGULATORY'    AND basis_ref ~ '^LR-[0-9]{3}')
    OR (mandatory_basis = 'PROVIDER'      AND basis_ref ~ '^(PCR-[0-9]{3}|CONTRACT:.+)$')
    OR (mandatory_basis = 'INTERNAL_RISK' AND basis_ref ~ '^(DEC-[A-Za-z0-9-]+|PD-[0-9]{2})$')),
  CONSTRAINT ck_sls_status CHECK (status IN ('PROPOSED','ACTIVE','RETIRED')),
  CONSTRAINT ck_sls_maker_checker CHECK (approved_by IS NULL OR approved_by <> created_by),
  CONSTRAINT ck_sls_approved CHECK (status = 'PROPOSED' OR (approved_by IS NOT NULL AND approved_at IS NOT NULL)),
  CONSTRAINT ck_sls_ingest CHECK ((last_ingested_at IS NULL) = (version_hash IS NULL))
);
COMMENT ON TABLE compliance.screening_list_sources IS 'C1. Screened list registry with basis, freshness and version.';
INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('screening_list_source', '',         'PROPOSED'),
  ('screening_list_source', 'PROPOSED', 'ACTIVE'),
  ('screening_list_source', 'ACTIVE',   'RETIRED');
CREATE TRIGGER trg_screening_list_sources_guard_status BEFORE INSERT OR UPDATE OF status ON compliance.screening_list_sources
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('screening_list_source');
CREATE TRIGGER trg_screening_list_sources_no_delete BEFORE DELETE ON compliance.screening_list_sources
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_screening_list_sources_no_truncate BEFORE TRUNCATE ON compliance.screening_list_sources
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION compliance.screening_list_sources_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF NOT changed <@ ARRAY['status','approved_by','approved_at','last_ingested_at','version_hash','version']
     OR NEW.version <> OLD.version + 1
     OR (OLD.approved_by IS NOT NULL AND NEW.approved_by IS DISTINCT FROM OLD.approved_by)
     OR (NEW.last_ingested_at < OLD.last_ingested_at) THEN
    RAISE EXCEPTION 'list source %: basis is immutable, approval set once, ingestion only moves forward (attempted: %)',
      OLD.source_code, changed USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_screening_list_sources_guard BEFORE UPDATE ON compliance.screening_list_sources
  FOR EACH ROW EXECUTE FUNCTION compliance.screening_list_sources_guard();

-- -----------------------------------------------------------------------------------------------------
-- compliance.screening_suppressions — false-positive suppression for an exact (subject, list entry) pair
-- (sanctions-screening §6 step 4a, §9). Maker-checker. It lapses automatically if the list entry version or
-- the subject's identity fingerprint changes (the screening service compares both and sets lapsed_*,
-- once). Never applies to a CONFIRMED_MATCH. Append-only apart from the lapse.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE compliance.screening_suppressions (
  id                      uuid        NOT NULL,
  subject_type            text        NOT NULL,
  subject_id              uuid        NOT NULL,
  list_source_id          uuid        NOT NULL REFERENCES compliance.screening_list_sources(id),
  entry_ref               text        NOT NULL,
  entry_version_hash      text        NOT NULL,
  subject_fingerprint     bytea       NOT NULL,             -- HMAC of the identity data screened (no plaintext)
  decided_result_id       uuid        NOT NULL REFERENCES compliance.sanctions_screening_results(id),
  case_id                 uuid        REFERENCES compliance.compliance_cases(id),
  created_by              uuid        NOT NULL REFERENCES app.users(id),
  approved_by             uuid        NOT NULL REFERENCES app.users(id),
  created_at              timestamptz NOT NULL DEFAULT now(),
  lapsed_at               timestamptz,
  lapse_reason            text,
  CONSTRAINT pk_screening_suppressions PRIMARY KEY (id),
  CONSTRAINT ck_ssup_maker_checker CHECK (approved_by <> created_by),
  CONSTRAINT ck_ssup_fingerprint CHECK (octet_length(subject_fingerprint) = 32),
  CONSTRAINT ck_ssup_lapse CHECK ((lapsed_at IS NULL) = (lapse_reason IS NULL)),
  CONSTRAINT ck_ssup_lapse_reason CHECK (lapse_reason IS NULL OR lapse_reason IN ('ENTRY_CHANGED','SUBJECT_DATA_CHANGED','REVOKED_BY_REVIEW'))
);
COMMENT ON TABLE compliance.screening_suppressions IS 'C3. False-positive suppressions; lapse on entry or subject change.';
CREATE UNIQUE INDEX uq_screening_suppressions_active ON compliance.screening_suppressions (subject_type, subject_id, list_source_id, entry_ref)
  WHERE lapsed_at IS NULL;
CREATE TRIGGER trg_screening_suppressions_no_delete BEFORE DELETE ON compliance.screening_suppressions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_screening_suppressions_no_truncate BEFORE TRUNCATE ON compliance.screening_suppressions
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION compliance.screening_suppressions_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE changed text[];
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NOT EXISTS (SELECT 1 FROM compliance.sanctions_screening_results r
                    WHERE r.id = NEW.decided_result_id AND r.result = 'CLEARED_BY_REVIEW'
                      AND r.subject_type = NEW.subject_type AND r.subject_id = NEW.subject_id) THEN
      RAISE EXCEPTION 'suppression requires a CLEARED_BY_REVIEW determination of the same subject' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
  END IF;
  SELECT coalesce(array_agg(n.key), '{}') INTO changed
    FROM jsonb_each(to_jsonb(NEW)) n WHERE n.value IS DISTINCT FROM (to_jsonb(OLD) -> n.key);
  IF OLD.lapsed_at IS NOT NULL OR NOT changed <@ ARRAY['lapsed_at','lapse_reason'] THEN
    RAISE EXCEPTION 'suppression %: only the lapse may be recorded, once (attempted: %)', OLD.id, changed
      USING ERRCODE = 'restrict_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_screening_suppressions_guard BEFORE INSERT OR UPDATE ON compliance.screening_suppressions
  FOR EACH ROW EXECUTE FUNCTION compliance.screening_suppressions_guard();

-- Hits name a registered list source.
ALTER TABLE compliance.screening_hits ADD CONSTRAINT fk_screening_hits_list_source
  FOREIGN KEY (list_source_code) REFERENCES compliance.screening_list_sources (source_code);
