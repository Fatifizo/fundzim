-- Invariant tests for 0008_risk.sql, 0010_kyc.sql, 0013_compliance.sql (DESIGN DRAFT; PGlite only).
-- Each case runs in its own transaction; expect=ok cases commit and are visible to later cases.
-- All identifiers, hashes and "ciphertext" values below are synthetic test bytes.
-- Users: ...a1 maker, ...a2 checker, ...a3 third staff, ...a4 subject user, ...a5 second subject user.

-- @case setup_fixtures expect=ok
INSERT INTO app.users (id, account_kind) VALUES
  ('00000000-0000-0000-0000-0000000000a1', 'STAFF'), ('00000000-0000-0000-0000-0000000000a2', 'STAFF'),
  ('00000000-0000-0000-0000-0000000000a3', 'STAFF'), ('00000000-0000-0000-0000-0000000000a4', 'USER'),
  ('00000000-0000-0000-0000-0000000000a5', 'USER');
INSERT INTO audit.audit_events (id, occurred_at, actor_type, action, target_type, outcome)
VALUES ('00000000-0000-0000-0000-0000000000f1', now(), 'system', 'kyc.submission.created', 'kyc_case', 'success');
INSERT INTO audit.evidence_records (id, evidence_type, subject_type, subject_id, collected_at, collected_by_type, source, storage_ref,
                                    content_sha256, classification, retention_class, audit_event_id)
VALUES ('00000000-0000-0000-0000-0000000000e1', 'REVIEW_NOTE', 'user', '00000000-0000-0000-0000-0000000000a4', now(), 'system',
        'staff_note', 'compliance/00000000-0000-0000-0000-0000000000e1', sha256('synthetic note 1'::bytea), 'C3', 'CASE',
        '00000000-0000-0000-0000-0000000000f1'),
       ('00000000-0000-0000-0000-0000000000e2', 'REVIEW_NOTE', 'user', '00000000-0000-0000-0000-0000000000a4', now(), 'system',
        'staff_note', 'compliance/00000000-0000-0000-0000-0000000000e2', sha256('synthetic note 2'::bytea), 'C3', 'CASE',
        '00000000-0000-0000-0000-0000000000f1');

-- ===================================== risk.holds ===================================================
-- @case hold_place_with_event expect=ok
INSERT INTO risk.holds (id, hold_type, scope_type, scope_id, reason_code, origin_type, placed_by_type, placed_by, review_by)
VALUES ('00000000-0000-0000-0000-000000000101', 'ACCOUNT_HOLD', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'SUSPECTED_TAKEOVER', 'STAFF_DECISION', 'STAFF', '00000000-0000-0000-0000-0000000000a1', now() + interval '7 days');
INSERT INTO risk.hold_events (id, hold_id, hold_version, event_type, actor_type, actor_id)
VALUES ('00000000-0000-0000-0000-000000000111', '00000000-0000-0000-0000-000000000101', 1, 'PLACED', 'STAFF', '00000000-0000-0000-0000-0000000000a1');

-- @case hold_without_event_rejected expect=error:has no hold_events row
INSERT INTO risk.holds (id, hold_type, scope_type, scope_id, reason_code, origin_type, placed_by_type, placed_by, review_by)
VALUES ('00000000-0000-0000-0000-000000000102', 'ACCOUNT_HOLD', 'USER', '00000000-0000-0000-0000-0000000000a5',
        'SUSPECTED_TAKEOVER', 'STAFF_DECISION', 'STAFF', '00000000-0000-0000-0000-0000000000a1', now() + interval '7 days');

-- @case hold_type_scope_mismatch_rejected expect=error:ck_holds_type_scope
INSERT INTO risk.holds (id, hold_type, scope_type, scope_id, reason_code, origin_type, placed_by_type, placed_by, review_by)
VALUES ('00000000-0000-0000-0000-000000000103', 'ACCOUNT_HOLD', 'CAMPAIGN', gen_random_uuid(),
        'X_REASON', 'STAFF_DECISION', 'STAFF', '00000000-0000-0000-0000-0000000000a1', now() + interval '7 days');

-- @case compliance_hold_without_case_rejected expect=error:ck_holds_compliance_case
INSERT INTO risk.holds (id, hold_type, scope_type, scope_id, reason_code, origin_type, placed_by_type, placed_by, review_by)
VALUES ('00000000-0000-0000-0000-000000000104', 'COMPLIANCE_HOLD', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'SANCTIONS_POTENTIAL_MATCH', 'STAFF_DECISION', 'STAFF', '00000000-0000-0000-0000-0000000000a1', now() + interval '7 days');

-- @case hold_content_change_rejected expect=error:only release fields and a first case link may change
UPDATE risk.holds SET reason_code = 'OTHER_REASON', version = version + 1 WHERE id = '00000000-0000-0000-0000-000000000101';

-- @case hold_release_once expect=ok
UPDATE risk.holds SET released_at = now(), released_by_type = 'STAFF', released_by = '00000000-0000-0000-0000-0000000000a2',
       release_reason_code = 'ACCOUNT_RECOVERED', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000101';
INSERT INTO risk.hold_events (id, hold_id, hold_version, event_type, actor_type, actor_id)
VALUES ('00000000-0000-0000-0000-000000000112', '00000000-0000-0000-0000-000000000101', 2, 'RELEASED', 'STAFF', '00000000-0000-0000-0000-0000000000a2');

-- @case hold_release_fields_change_rejected expect=error:release fields are set once and never cleared or changed
UPDATE risk.holds SET release_reason_code = 'DIFFERENT', version = 3 WHERE id = '00000000-0000-0000-0000-000000000101';

-- @case hold_release_cleared_rejected expect=error:release fields are set once and never cleared or changed
UPDATE risk.holds SET released_at = NULL, released_by_type = NULL, released_by = NULL, release_reason_code = NULL, version = 3
 WHERE id = '00000000-0000-0000-0000-000000000101';

-- @case hold_delete_rejected expect=error:append-only table: DELETE on risk.holds
DELETE FROM risk.holds WHERE id = '00000000-0000-0000-0000-000000000101';

-- @case hold_truncate_rejected expect=error:append-only table: TRUNCATE on risk.holds
TRUNCATE risk.holds CASCADE;

-- @case hold_events_update_rejected expect=error:append-only table: UPDATE on risk.hold_events
UPDATE risk.hold_events SET reason_code = 'X' WHERE hold_id = '00000000-0000-0000-0000-000000000101';

-- @case compliance_hold_place expect=ok
INSERT INTO risk.holds (id, hold_type, scope_type, scope_id, reason_code, origin_type, case_id, placed_by_type, placed_by, review_by, confidential)
VALUES ('00000000-0000-0000-0000-000000000105', 'COMPLIANCE_HOLD', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'SANCTIONS_POTENTIAL_MATCH', 'CASE', '00000000-0000-0000-0000-000000000c01', 'STAFF', '00000000-0000-0000-0000-0000000000a1',
        now() + interval '7 days', true);
INSERT INTO risk.hold_events (id, hold_id, hold_version, event_type, actor_type, actor_id)
VALUES ('00000000-0000-0000-0000-000000000113', '00000000-0000-0000-0000-000000000105', 1, 'PLACED', 'STAFF', '00000000-0000-0000-0000-0000000000a1');

-- @case compliance_hold_release_without_request_rejected expect=error:release requires an APPROVED hold_release_request
UPDATE risk.holds SET released_at = now(), released_by_type = 'STAFF', released_by = '00000000-0000-0000-0000-0000000000a2',
       release_reason_code = 'FALSE_POSITIVE', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000105';

-- @case compliance_hold_release_by_placer_as_checker_rejected expect=error:release requires an APPROVED hold_release_request
UPDATE risk.holds SET released_at = now(), released_by_type = 'STAFF', released_by = '00000000-0000-0000-0000-0000000000a2',
       release_approved_by = '00000000-0000-0000-0000-0000000000a1', release_reason_code = 'FALSE_POSITIVE', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000105';

-- @case hold_release_request_open expect=ok
INSERT INTO risk.hold_release_requests (id, hold_id, status, requested_by, reason_code, justification, payload_sha256, expires_at)
VALUES ('00000000-0000-0000-0000-000000000121', '00000000-0000-0000-0000-000000000105', 'PENDING', '00000000-0000-0000-0000-0000000000a2',
        'FALSE_POSITIVE', 'synthetic test release', decode(repeat('ab', 32), 'hex'), now() + interval '3 days');

-- @case hold_release_request_self_approval_rejected expect=error:ck_hrr_maker_checker
UPDATE risk.hold_release_requests SET status = 'APPROVED', decided_by = '00000000-0000-0000-0000-0000000000a2', decided_at = now(),
       checker_step_up_at = now(), version = 2 WHERE id = '00000000-0000-0000-0000-000000000121';

-- @case hold_release_request_approval_by_placer_rejected expect=error:whoever placed the hold cannot approve
UPDATE risk.hold_release_requests SET status = 'APPROVED', decided_by = '00000000-0000-0000-0000-0000000000a1', decided_at = now(),
       checker_step_up_at = now(), version = 2 WHERE id = '00000000-0000-0000-0000-000000000121';

-- @case compliance_hold_release_maker_checker_ok expect=ok
UPDATE risk.hold_release_requests SET status = 'APPROVED', decided_by = '00000000-0000-0000-0000-0000000000a3', decided_at = now(),
       checker_step_up_at = now(), version = 2 WHERE id = '00000000-0000-0000-0000-000000000121';
UPDATE risk.holds SET released_at = now(), released_by_type = 'STAFF', released_by = '00000000-0000-0000-0000-0000000000a2',
       release_approved_by = '00000000-0000-0000-0000-0000000000a3', release_reason_code = 'FALSE_POSITIVE',
       release_request_id = '00000000-0000-0000-0000-000000000121', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000105';
INSERT INTO risk.hold_events (id, hold_id, hold_version, event_type, actor_type, actor_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000105', 2, 'RELEASED', 'STAFF', '00000000-0000-0000-0000-0000000000a2');
-- re-place a fresh compliance hold on the same subject for the restriction tests below
INSERT INTO risk.holds (id, hold_type, scope_type, scope_id, reason_code, origin_type, case_id, placed_by_type, placed_by, review_by, confidential)
VALUES ('00000000-0000-0000-0000-000000000107', 'COMPLIANCE_HOLD', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'SANCTIONS_POTENTIAL_MATCH', 'CASE', '00000000-0000-0000-0000-000000000c01', 'STAFF', '00000000-0000-0000-0000-0000000000a1',
        now() + interval '7 days', true);
INSERT INTO risk.hold_events (id, hold_id, hold_version, event_type, actor_type, actor_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000107', 1, 'PLACED', 'STAFF', '00000000-0000-0000-0000-0000000000a1');

-- @case freeze_without_ledger_journal_rejected expect=error:ck_holds_freeze_ledger
INSERT INTO risk.holds (id, hold_type, scope_type, scope_id, reason_code, origin_type, case_id, placed_by_type, placed_by, review_by)
VALUES ('00000000-0000-0000-0000-000000000106', 'CAMPAIGN_FREEZE', 'CAMPAIGN', gen_random_uuid(), 'FRAUD_SUSPECTED', 'CASE',
        '00000000-0000-0000-0000-000000000c01', 'STAFF', '00000000-0000-0000-0000-0000000000a1', now() + interval '7 days');

-- ===================================== risk.limits ==================================================
-- @case limit_propose expect=ok
INSERT INTO risk.limits (id, limit_key, version, limit_type, scope_type, value_kind, value_seconds, source_type, source_ref,
                         approval_owner_role, status, made_by, effective_from, review_by)
VALUES ('00000000-0000-0000-0000-000000000201', 'compliance.restriction.max_duration', 1, 'INTERNAL_RISK', 'GLOBAL', 'DURATION',
        864000, 'INTERNAL_DECISION', 'DEC-TEST-0001', 'COMPLIANCE', 'PROPOSED', '00000000-0000-0000-0000-0000000000a1',
        now() - interval '1 day', current_date + 180);
INSERT INTO risk.limit_change_requests (id, limit_id, action, status, requested_by, justification, payload_sha256, expires_at)
VALUES ('00000000-0000-0000-0000-000000000211', '00000000-0000-0000-0000-000000000201', 'ACTIVATE', 'PENDING',
        '00000000-0000-0000-0000-0000000000a1', 'synthetic test proposal', decode(repeat('ab', 32), 'hex'), now() + interval '7 days');

-- @case limit_self_approval_rejected expect=error:ck_lcr_maker_checker
UPDATE risk.limit_change_requests SET status = 'APPROVED', decided_by = '00000000-0000-0000-0000-0000000000a1', decided_at = now(),
       checker_step_up_at = now(), version = 2
 WHERE id = '00000000-0000-0000-0000-000000000211';

-- @case limit_approval_without_request_rejected expect=error:approval must match a decided ACTIVATE request
UPDATE risk.limits SET status = 'APPROVED', approved_by = '00000000-0000-0000-0000-0000000000a2', approved_at = now(),
       approval_request_id = '00000000-0000-0000-0000-000000000211'
 WHERE id = '00000000-0000-0000-0000-000000000201';

-- @case limit_approve_by_checker expect=ok
UPDATE risk.limit_change_requests SET status = 'APPROVED', decided_by = '00000000-0000-0000-0000-0000000000a2', decided_at = now(),
       checker_step_up_at = now(), version = 2
 WHERE id = '00000000-0000-0000-0000-000000000211';
UPDATE risk.limits SET status = 'APPROVED', approved_by = '00000000-0000-0000-0000-0000000000a2', approved_at = now(),
       approval_request_id = '00000000-0000-0000-0000-000000000211'
 WHERE id = '00000000-0000-0000-0000-000000000201';

-- @case approved_limit_value_change_rejected expect=error:is immutable in status APPROVED
UPDATE risk.limits SET value_seconds = 1 WHERE id = '00000000-0000-0000-0000-000000000201';

-- @case decided_request_change_rejected expect=error:is final
UPDATE risk.limit_change_requests SET decision_note = 'edited later', version = 3 WHERE id = '00000000-0000-0000-0000-000000000211';

-- @case limit_delete_rejected expect=error:append-only table: DELETE on risk.limits
DELETE FROM risk.limits WHERE id = '00000000-0000-0000-0000-000000000201';

-- @case money_limit_without_currency_rejected expect=error:ck_limits_value_shape
INSERT INTO risk.limits (id, limit_key, version, limit_type, scope_type, value_kind, value_minor, source_type, source_ref,
                         approval_owner_role, status, made_by, effective_from, review_by)
VALUES (gen_random_uuid(), 'payout.auto_approve_max', 1, 'INTERNAL_RISK', 'GLOBAL', 'MONEY', 100, 'INTERNAL_DECISION',
        'DEC-TEST-0002', 'FINANCE', 'PROPOSED', '00000000-0000-0000-0000-0000000000a1', now(), current_date + 30);

-- @case regulatory_limit_without_source_rejected expect=error:ck_limits_source_regulatory
INSERT INTO risk.limits (id, limit_key, version, limit_type, scope_type, value_kind, value_count, source_type, source_ref,
                         approval_owner_role, status, made_by, effective_from, review_by)
VALUES (gen_random_uuid(), 'pvo.s8_max_days', 1, 'REGULATORY', 'GLOBAL', 'COUNT', 1, 'STATUTE', 'DEC-1',
        'COMPLIANCE', 'PROPOSED', '00000000-0000-0000-0000-0000000000a1', now(), current_date + 30);

-- @case limit_inserted_as_approved_rejected expect=error:must be inserted as PROPOSED
INSERT INTO risk.limits (id, limit_key, version, limit_type, scope_type, value_kind, value_count, source_type, source_ref,
                         approval_owner_role, status, made_by, approved_by, approved_at, approval_request_id, effective_from, review_by)
VALUES (gen_random_uuid(), 'tm.donor.count', 1, 'INTERNAL_RISK', 'GLOBAL', 'COUNT', 5, 'INTERNAL_DECISION', 'DEC-TEST-0003',
        'COMPLIANCE', 'APPROVED', '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000a2', now(),
        '00000000-0000-0000-0000-000000000211', now(), current_date + 30);

-- ===================================== compliance cases ==============================================
-- @case case_open_with_event expect=ok
INSERT INTO compliance.compliance_cases (id, case_number, case_type, severity, status, source, opened_by_type, opened_by_job,
                                         team, sla_due_at, sla_policy_ref, blocks_payouts)
VALUES ('00000000-0000-0000-0000-000000000c01', 'CMP-2026-000001', 'SANCTIONS', 'S1', 'OPEN', 'SCREENING', 'SYSTEM', 'screening.rescreen',
        'COMPLIANCE', now() + interval '1 day', 'PD-28-draft', true);
INSERT INTO compliance.compliance_case_events (id, case_id, case_version, event_type, to_status, actor_type, actor_job)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000c01', 1, 'OPENED', 'OPEN', 'SYSTEM', 'screening.rescreen');
INSERT INTO compliance.compliance_case_links (id, case_id, subject_type, subject_id, role, linked_by_type)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000c01', 'USER', '00000000-0000-0000-0000-0000000000a4', 'PRIMARY_SUBJECT', 'SYSTEM');

-- @case case_without_event_rejected expect=error:has no compliance_case_events row
INSERT INTO compliance.compliance_cases (id, case_number, case_type, severity, status, source, opened_by_type, opened_by,
                                         team, sla_due_at, sla_policy_ref)
VALUES (gen_random_uuid(), 'CMP-2026-000002', 'AML_MONITORING', 'S2', 'OPEN', 'STAFF', 'STAFF', '00000000-0000-0000-0000-0000000000a1',
        'COMPLIANCE', now() + interval '3 days', 'PD-28-draft');

-- @case sanctions_case_must_block_payouts expect=error:ck_compliance_cases_sanctions_block
INSERT INTO compliance.compliance_cases (id, case_number, case_type, severity, status, source, opened_by_type, opened_by_job,
                                         team, sla_due_at, sla_policy_ref, blocks_payouts)
VALUES (gen_random_uuid(), 'CMP-2026-000003', 'SANCTIONS', 'S1', 'OPEN', 'SCREENING', 'SYSTEM', 'x', 'COMPLIANCE',
        now() + interval '1 day', 'PD-28-draft', false);

-- @case illegal_case_transition_rejected expect=error:illegal compliance_case transition: "OPEN" -> "CLOSED"
UPDATE compliance.compliance_cases SET status = 'CLOSED', closed_at = now(), closed_by = '00000000-0000-0000-0000-0000000000a1',
       closure_memo_evidence_id = '00000000-0000-0000-0000-0000000000e1', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000c01';

-- @case case_triage expect=ok
UPDATE compliance.compliance_cases SET status = 'IN_PROGRESS', assigned_to = '00000000-0000-0000-0000-0000000000a1',
       assigned_at = now(), version = 2 WHERE id = '00000000-0000-0000-0000-000000000c01';
INSERT INTO compliance.compliance_case_events (id, case_id, case_version, event_type, from_status, to_status, actor_type, actor_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000c01', 2, 'STATUS_CHANGED', 'OPEN', 'IN_PROGRESS', 'STAFF',
        '00000000-0000-0000-0000-0000000000a1');

-- @case checker_decision_skipping_pending_approval_rejected expect=error:checker decisions go through PENDING_APPROVAL
UPDATE compliance.compliance_cases SET status = 'DECIDED', decision = 'CONFIRMED_MATCH', decision_reason_code = 'LIST_MATCH',
       decided_by = '00000000-0000-0000-0000-0000000000a1', second_approver_id = '00000000-0000-0000-0000-0000000000a2',
       decided_at = now(), version = 3 WHERE id = '00000000-0000-0000-0000-000000000c01';

-- @case confirmed_match_without_checker_rejected expect=error:ck_compliance_cases_checker_required
UPDATE compliance.compliance_cases SET status = 'DECIDED', decision = 'CONFIRMED_MATCH', decision_reason_code = 'LIST_MATCH',
       decided_by = '00000000-0000-0000-0000-0000000000a1', decided_at = now(), version = 3
 WHERE id = '00000000-0000-0000-0000-000000000c01';

-- @case case_self_check_rejected expect=error:ck_compliance_cases_maker_checker
UPDATE compliance.compliance_cases SET status = 'PENDING_APPROVAL', decision = 'CONFIRMED_MATCH', decision_reason_code = 'LIST_MATCH',
       decided_by = '00000000-0000-0000-0000-0000000000a1', second_approver_id = '00000000-0000-0000-0000-0000000000a1', version = 3
 WHERE id = '00000000-0000-0000-0000-000000000c01';

-- @case case_raise_to_restricted expect=ok
UPDATE compliance.compliance_cases SET confidentiality = 'RESTRICTED_STR', suspicion_formed_at = now(), version = 3
 WHERE id = '00000000-0000-0000-0000-000000000c01';
INSERT INTO compliance.compliance_case_events (id, case_id, case_version, event_type, actor_type, actor_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000c01', 3, 'CONFIDENTIALITY_RAISED', 'STAFF', '00000000-0000-0000-0000-0000000000a1');

-- @case case_lower_restricted_rejected expect=error:RESTRICTED_STR confidentiality cannot be lowered
UPDATE compliance.compliance_cases SET confidentiality = 'NORMAL', version = 4 WHERE id = '00000000-0000-0000-0000-000000000c01';

-- @case case_events_append_only expect=error:append-only table: UPDATE on compliance.compliance_case_events
UPDATE compliance.compliance_case_events SET reason_code = 'X' WHERE case_id = '00000000-0000-0000-0000-000000000c01';

-- @case str_report_maker_checker_rejected expect=error:ck_str_reports_maker_checker
INSERT INTO compliance.str_reports (id, case_id, status, suspicion_formed_at, deadline_at, deadline_limit_id, prepared_by)
SELECT '00000000-0000-0000-0000-000000000701', c.id, 'DRAFT', c.suspicion_formed_at, c.suspicion_formed_at + interval '3 days',
       gen_random_uuid(), '00000000-0000-0000-0000-0000000000a1'
  FROM compliance.compliance_cases c WHERE c.id = '00000000-0000-0000-0000-000000000c01';
UPDATE compliance.str_reports SET status = 'PENDING_APPROVAL', decision = 'DO_NOT_FILE', draft_evidence_id = '00000000-0000-0000-0000-0000000000e1',
       rationale_evidence_id = '00000000-0000-0000-0000-0000000000e2', version = 2 WHERE id = '00000000-0000-0000-0000-000000000701';
UPDATE compliance.str_reports SET status = 'APPROVED_NOT_TO_FILE', approved_by = '00000000-0000-0000-0000-0000000000a1',
       approved_at = now(), version = 3 WHERE id = '00000000-0000-0000-0000-000000000701';

-- @case str_report_on_normal_case_rejected expect=error:requires a RESTRICTED_STR case
INSERT INTO compliance.str_reports (id, case_id, status, suspicion_formed_at, deadline_at, deadline_limit_id, prepared_by)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000c01', 'DRAFT', now() - interval '1 year', now(), gen_random_uuid(),
        '00000000-0000-0000-0000-0000000000a1');

-- ===================================== compliance restrictions =======================================
-- @case restriction_without_expiry_rejected expect=error:null value in column "expires_at"
INSERT INTO compliance.compliance_restrictions (id, case_id, subject_type, subject_id, capability, status, reason_code, requested_by, starts_at)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000c01', 'USER', '00000000-0000-0000-0000-0000000000a4', 'DONATE',
        'PENDING_APPROVAL', 'AML_CONCERN', '00000000-0000-0000-0000-0000000000a1', now());

-- @case restriction_self_approval_rejected expect=error:ck_compliance_restrictions_maker_checker
INSERT INTO compliance.compliance_restrictions (id, case_id, subject_type, subject_id, capability, status, reason_code, requested_by,
                                                starts_at, expires_at)
VALUES ('00000000-0000-0000-0000-000000000302', '00000000-0000-0000-0000-000000000c01', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'DONATE', 'PENDING_APPROVAL', 'AML_CONCERN', '00000000-0000-0000-0000-0000000000a1', now(), now() + interval '1 day');
UPDATE compliance.compliance_restrictions SET status = 'ACTIVE', approved_by = '00000000-0000-0000-0000-0000000000a1', approved_at = now(),
       max_duration_limit_id = '00000000-0000-0000-0000-000000000201', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000302';

-- @case restriction_over_max_duration_rejected expect=error:duration exceeds the approved maximum
INSERT INTO compliance.compliance_restrictions (id, case_id, subject_type, subject_id, capability, status, reason_code, requested_by,
                                                starts_at, expires_at)
VALUES ('00000000-0000-0000-0000-000000000303', '00000000-0000-0000-0000-000000000c01', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'DONATE', 'PENDING_APPROVAL', 'AML_CONCERN', '00000000-0000-0000-0000-0000000000a1', now(), now() + interval '11 days');
UPDATE compliance.compliance_restrictions SET status = 'ACTIVE', approved_by = '00000000-0000-0000-0000-0000000000a2', approved_at = now(),
       max_duration_limit_id = '00000000-0000-0000-0000-000000000201', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000303';

-- @case restriction_without_approved_limit_rejected expect=error:no approved effective limit
INSERT INTO compliance.compliance_restrictions (id, case_id, subject_type, subject_id, capability, status, reason_code, requested_by,
                                                starts_at, expires_at)
VALUES ('00000000-0000-0000-0000-000000000304', '00000000-0000-0000-0000-000000000c01', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'DONATE', 'PENDING_APPROVAL', 'AML_CONCERN', '00000000-0000-0000-0000-0000000000a1', now(), now() + interval '1 day');
UPDATE compliance.compliance_restrictions SET status = 'ACTIVE', approved_by = '00000000-0000-0000-0000-0000000000a2', approved_at = now(),
       max_duration_limit_id = gen_random_uuid(), version = 2
 WHERE id = '00000000-0000-0000-0000-000000000304';

-- @case restriction_receive_payout_without_hold_rejected expect=error:needs an active COMPLIANCE_HOLD
INSERT INTO compliance.compliance_restrictions (id, case_id, subject_type, subject_id, capability, status, reason_code, requested_by,
                                                starts_at, expires_at, hold_id)
VALUES ('00000000-0000-0000-0000-000000000305', '00000000-0000-0000-0000-000000000c01', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'RECEIVE_PAYOUT', 'PENDING_APPROVAL', 'AML_CONCERN', '00000000-0000-0000-0000-0000000000a1', now(), now() + interval '1 day',
        gen_random_uuid());
UPDATE compliance.compliance_restrictions SET status = 'ACTIVE', approved_by = '00000000-0000-0000-0000-0000000000a2', approved_at = now(),
       max_duration_limit_id = '00000000-0000-0000-0000-000000000201', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000305';

-- @case restriction_receive_payout_with_hold expect=ok
INSERT INTO compliance.compliance_restrictions (id, case_id, subject_type, subject_id, capability, status, reason_code, requested_by,
                                                starts_at, expires_at, hold_id)
VALUES ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000c01', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'RECEIVE_PAYOUT', 'PENDING_APPROVAL', 'AML_CONCERN', '00000000-0000-0000-0000-0000000000a1', now(), now() + interval '9 days',
        '00000000-0000-0000-0000-000000000107');
UPDATE compliance.compliance_restrictions SET status = 'ACTIVE', approved_by = '00000000-0000-0000-0000-0000000000a2', approved_at = now(),
       max_duration_limit_id = '00000000-0000-0000-0000-000000000201', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000301';

-- @case restriction_expiry_change_rejected expect=error:scope and expiry are immutable
UPDATE compliance.compliance_restrictions SET expires_at = expires_at + interval '1 day', version = 3
 WHERE id = '00000000-0000-0000-0000-000000000301';

-- ===================================== screening ===================================================
-- @case screening_request_and_potential_match expect=ok
INSERT INTO compliance.screening_requests (id, subject_type, subject_id, trigger, list_kinds, list_versions, list_versions_sha256,
                                           status, requested_by_type)
VALUES ('00000000-0000-0000-0000-000000000401', 'USER', '00000000-0000-0000-0000-0000000000a4', 'LIST_UPDATE', '{SANCTIONS,PEP}',
        '{"UN_CONSOLIDATED":"v-test-1"}', decode(repeat('01', 32), 'hex'), 'PENDING', 'SYSTEM');
INSERT INTO compliance.sanctions_screening_results (id, request_id, subject_type, subject_id, result, list_versions, screened_at,
                                                    valid_until, freshness_limit_id, evidence_record_id)
VALUES ('00000000-0000-0000-0000-000000000411', '00000000-0000-0000-0000-000000000401', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'POTENTIAL_MATCH', '{"UN_CONSOLIDATED":"v-test-1"}', now(), now() + interval '30 days', gen_random_uuid(),
        '00000000-0000-0000-0000-0000000000e1');

-- @case duplicate_screening_request_rejected expect=error:uq_screening_requests_idem
INSERT INTO compliance.screening_requests (id, subject_type, subject_id, trigger, list_kinds, list_versions, list_versions_sha256,
                                           status, requested_by_type)
VALUES (gen_random_uuid(), 'USER', '00000000-0000-0000-0000-0000000000a4', 'EVENT', '{SANCTIONS}',
        '{"UN_CONSOLIDATED":"v-test-1"}', decode(repeat('01', 32), 'hex'), 'PENDING', 'SYSTEM');

-- @case clear_potential_match_single_reviewer_rejected expect=error:resolving a potential match requires a second reviewer
INSERT INTO compliance.sanctions_screening_results (id, request_id, subject_type, subject_id, result, list_versions, screened_at,
                                                    valid_until, freshness_limit_id, supersedes_result_id, decided_by, reason_code, evidence_record_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000401', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'CLEARED_BY_REVIEW', '{"UN_CONSOLIDATED":"v-test-1"}', now(), now() + interval '30 days', gen_random_uuid(),
        '00000000-0000-0000-0000-000000000411', '00000000-0000-0000-0000-0000000000a1', 'DOB_MISMATCH', '00000000-0000-0000-0000-0000000000e2');

-- @case confirmed_match_without_second_rejected expect=error:resolving a potential match requires a second reviewer
INSERT INTO compliance.sanctions_screening_results (id, request_id, subject_type, subject_id, result, list_versions, screened_at,
                                                    valid_until, freshness_limit_id, supersedes_result_id, decided_by, reason_code, evidence_record_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000401', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'CONFIRMED_MATCH', '{"UN_CONSOLIDATED":"v-test-1"}', now(), now() + interval '30 days', gen_random_uuid(),
        '00000000-0000-0000-0000-000000000411', '00000000-0000-0000-0000-0000000000a1', 'LIST_MATCH', '00000000-0000-0000-0000-0000000000e2');

-- @case screening_result_update_rejected expect=error:append-only table: UPDATE on compliance.sanctions_screening_results
UPDATE compliance.sanctions_screening_results SET result = 'CLEAR' WHERE id = '00000000-0000-0000-0000-000000000411';

-- ===================================== kyc =========================================================
-- @case kyc_profile_identity_setup expect=ok
INSERT INTO kyc.verification_profiles (id, user_id, policy_version)
VALUES ('00000000-0000-0000-0000-000000000501', '00000000-0000-0000-0000-0000000000a4', 'gate-v1'),
       ('00000000-0000-0000-0000-000000000502', '00000000-0000-0000-0000-0000000000a5', 'gate-v1');
INSERT INTO kyc.profile_events (id, profile_id, aggregate_version, to_level, to_status, reason_code, actor_type, actor_job)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000501', 1, 'UNVERIFIED', 'ACTIVE', 'PROFILE_CREATED', 'SYSTEM', 'kyc.onboard'),
       (gen_random_uuid(), '00000000-0000-0000-0000-000000000502', 1, 'UNVERIFIED', 'ACTIVE', 'PROFILE_CREATED', 'SYSTEM', 'kyc.onboard');
INSERT INTO kyc.identities (id, subject_kind, profile_id, legal_name_ciphertext, legal_name_key_id, id_type, id_number_ciphertext,
                            id_number_key_id, id_number_bidx, bidx_key_version, subject_data_key_ref, source)
VALUES ('00000000-0000-0000-0000-000000000511', 'ACCOUNT_HOLDER', '00000000-0000-0000-0000-000000000501',
        decode(repeat('cd', 40), 'hex'), 'dek-test-1', 'ZW_NATIONAL_ID', decode(repeat('ef', 40), 'hex'), 'dek-test-1',
        decode(repeat('aa', 32), 'hex'), 1, 'dek-test-1', 'USER_ENTRY');

-- @case duplicate_active_identity_bidx_rejected expect=error:uq_identities_active_id_bidx
INSERT INTO kyc.identities (id, subject_kind, profile_id, legal_name_ciphertext, legal_name_key_id, id_type, id_number_ciphertext,
                            id_number_key_id, id_number_bidx, bidx_key_version, subject_data_key_ref, source)
VALUES (gen_random_uuid(), 'ACCOUNT_HOLDER', '00000000-0000-0000-0000-000000000502',
        decode(repeat('cd', 40), 'hex'), 'dek-test-2', 'ZW_NATIONAL_ID', decode(repeat('ef', 40), 'hex'), 'dek-test-2',
        decode(repeat('aa', 32), 'hex'), 1, 'dek-test-2', 'USER_ENTRY');

-- @case same_bidx_as_kyb_person_allowed expect=ok
INSERT INTO kyc.identities (id, subject_kind, legal_name_ciphertext, legal_name_key_id, id_type, id_number_ciphertext,
                            id_number_key_id, id_number_bidx, bidx_key_version, subject_data_key_ref, source)
VALUES (gen_random_uuid(), 'KYB_PERSON', decode(repeat('cd', 40), 'hex'), 'dek-test-3', 'ZW_NATIONAL_ID',
        decode(repeat('ef', 40), 'hex'), 'dek-test-3', decode(repeat('aa', 32), 'hex'), 1, 'dek-test-3', 'OWNER_DECLARATION');

-- @case plaintext_sized_id_number_rejected expect=error:ck_identities_id_number_shape
INSERT INTO kyc.identities (id, subject_kind, legal_name_ciphertext, legal_name_key_id, id_type, id_number_ciphertext,
                            id_number_key_id, id_number_bidx, bidx_key_version, subject_data_key_ref, source)
VALUES (gen_random_uuid(), 'KYB_PERSON', decode(repeat('cd', 40), 'hex'), 'k', 'ZW_NATIONAL_ID',
        convert_to('63-123456A42', 'UTF8'), 'k', decode(repeat('bb', 32), 'hex'), 1, 'k', 'STAFF_ENTRY');

-- @case identity_content_change_rejected expect=error:content is immutable
UPDATE kyc.identities SET id_number_key_id = 'dek-other' WHERE id = '00000000-0000-0000-0000-000000000511';

-- @case profile_change_without_event_rejected expect=error:has no matching profile_events row
UPDATE kyc.verification_profiles SET status = 'PENDING_REVIEW', paused_scope = '{WITHDRAW}', version = 2
 WHERE id = '00000000-0000-0000-0000-000000000501';

-- @case profile_illegal_status_transition_rejected expect=error:illegal kyc_verification_status transition
UPDATE kyc.verification_profiles SET status = 'REJECTED', version = 2 WHERE id = '00000000-0000-0000-0000-000000000501';

-- @case kyc_case_and_check expect=ok
INSERT INTO kyc.kyc_cases (id, profile_id, origin, target_level, status)
VALUES ('00000000-0000-0000-0000-000000000521', '00000000-0000-0000-0000-000000000501', 'VERIFICATION_ATTEMPT', 'IDENTITY_VERIFIED', 'OPEN');
INSERT INTO kyc.kyc_checks (id, kyc_case_id, profile_id, check_code, result, rule_version, performed_by_type, reason_code)
VALUES ('00000000-0000-0000-0000-000000000531', '00000000-0000-0000-0000-000000000521', '00000000-0000-0000-0000-000000000501',
        'ID_DOCUMENT', 'REVIEW', 'chk-v1', 'SYSTEM', 'MRZ_UNREADABLE');

-- @case kyc_checks_update_rejected expect=error:append-only table: UPDATE on kyc.kyc_checks
UPDATE kyc.kyc_checks SET result = 'PASS' WHERE id = '00000000-0000-0000-0000-000000000531';

-- @case kyc_checks_delete_rejected expect=error:append-only table: DELETE on kyc.kyc_checks
DELETE FROM kyc.kyc_checks WHERE id = '00000000-0000-0000-0000-000000000531';

-- @case kyc_case_illegal_transition_rejected expect=error:illegal kyc_case transition: "OPEN" -> "AWAITING_SUBMISSION"
UPDATE kyc.kyc_cases SET status = 'AWAITING_SUBMISSION', version = 2 WHERE id = '00000000-0000-0000-0000-000000000521';

-- @case kyc_decision_second_equals_first_rejected expect=error:ck_kyc_decisions_second_distinct
INSERT INTO kyc.kyc_decisions (id, kyc_case_id, outcome, resulting_status, reason_code, checklist_evidence_id, decided_by,
                               second_approval_required, second_approver_id, policy_version)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000521', 'REJECT', 'REJECTED', 'FRAUD_FORGED_DOCUMENT',
        '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000a1', true, '00000000-0000-0000-0000-0000000000a1', 'gate-v1');

-- @case kyc_fraud_reject_without_second_rejected expect=error:ck_kyc_decisions_fraud_second
INSERT INTO kyc.kyc_decisions (id, kyc_case_id, outcome, resulting_status, reason_code, checklist_evidence_id, decided_by,
                               second_approval_required, policy_version)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000521', 'REJECT', 'REJECTED', 'FRAUD_FORGED_DOCUMENT',
        '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000a1', false, 'gate-v1');

-- @case kyc_decision_on_own_verification_rejected expect=error:cannot decide their own verification
INSERT INTO kyc.kyc_decisions (id, kyc_case_id, outcome, granted_level, resulting_status, reason_code, checklist_evidence_id, decided_by,
                               second_approval_required, policy_version)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000521', 'APPROVE', 'IDENTITY_VERIFIED', 'ACTIVE', 'ALL_CHECKS_PASSED',
        '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000a4', false, 'gate-v1');

-- @case consent_given_then_withdrawn_as_new_row expect=ok
INSERT INTO kyc.consents (id, consent_type, action, subject_user_id, grantor_user_id, grantor_capacity, method, text_version, occurred_at)
VALUES ('00000000-0000-0000-0000-000000000541', 'BIOMETRIC', 'GIVEN', '00000000-0000-0000-0000-0000000000a4',
        '00000000-0000-0000-0000-0000000000a4', 'SELF', 'OTP_ECONSENT', 'bio-consent-v1', now() - interval '1 hour');
INSERT INTO kyc.consents (id, consent_type, action, withdraws_consent_id, subject_user_id, grantor_user_id, grantor_capacity, method,
                          text_version, occurred_at)
VALUES (gen_random_uuid(), 'BIOMETRIC', 'WITHDRAWN', '00000000-0000-0000-0000-000000000541', '00000000-0000-0000-0000-0000000000a4',
        '00000000-0000-0000-0000-0000000000a4', 'SELF', 'IN_APP_ATTESTATION', 'bio-consent-v1', now());

-- @case consent_update_rejected expect=error:append-only table: UPDATE on kyc.consents
UPDATE kyc.consents SET text_version = 'edited' WHERE id = '00000000-0000-0000-0000-000000000541';

-- @case biometric_document_with_withdrawn_consent_rejected expect=error:requires a current BIOMETRIC consent
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                classification, retention_class, encryption_key_id)
VALUES ('00000000-0000-0000-0000-000000000551', 'PRIVATE_KYC', 'kyc', 'quarantine/00000000-0000-0000-0000-000000000551',
        decode(repeat('11', 32), 'hex'), 1024, 'image/jpeg', 'C3', 'KYC', 'kms-test-kyc');
INSERT INTO kyc.kyc_documents (id, profile_id, kyc_case_id, document_type, stored_object_id, evidence_record_id, content_sha256,
                               media_type, size_bytes, is_biometric, biometric_consent_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000501', '00000000-0000-0000-0000-000000000521', 'SELFIE_LIVENESS',
        '00000000-0000-0000-0000-000000000551', '00000000-0000-0000-0000-0000000000e1', decode(repeat('11', 32), 'hex'),
        'image/jpeg', 1024, true, '00000000-0000-0000-0000-000000000541');

-- @case kyc_document_on_public_media_object_rejected expect=error:fk_kyc_documents_stored_object
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                classification, retention_class)
VALUES ('00000000-0000-0000-0000-000000000552', 'PUBLIC_MEDIA', 'campaigns', 'quarantine/00000000-0000-0000-0000-000000000552',
        decode(repeat('12', 32), 'hex'), 2048, 'image/jpeg', 'C1', 'OPERATIONAL');
INSERT INTO kyc.kyc_documents (id, profile_id, kyc_case_id, document_type, side, stored_object_id, evidence_record_id, content_sha256,
                               media_type, size_bytes)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000501', '00000000-0000-0000-0000-000000000521', 'ZW_NATIONAL_ID', 'FRONT',
        '00000000-0000-0000-0000-000000000552', '00000000-0000-0000-0000-0000000000e1', decode(repeat('12', 32), 'hex'),
        'image/jpeg', 2048);

-- @case kyb_setup expect=ok
INSERT INTO app.organisations (id, display_name, slug, org_type, created_by_user_id)
VALUES ('00000000-0000-0000-0000-000000000601', 'Synthetic Test Trust', 'synthetic-test-trust', 'TRUST', '00000000-0000-0000-0000-0000000000a5');
INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000601', '00000000-0000-0000-0000-0000000000a5', md5('org_role:ORG_ADMIN')::uuid);
INSERT INTO kyc.kyb_organisations (id, organisation_id, legal_name, org_type, policy_version)
VALUES ('00000000-0000-0000-0000-000000000611', '00000000-0000-0000-0000-000000000601', 'Synthetic Test Trust', 'TRUST', 'kyb-v1');
INSERT INTO kyc.profile_events (id, kyb_organisation_id, aggregate_version, to_level, to_status, reason_code, actor_type, actor_job)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000611', 1, 'ORG_UNVERIFIED', 'ACTIVE', 'PROFILE_CREATED', 'SYSTEM', 'kyc.onboard');

-- @case s8_authority_over_180_days_rejected expect=error:ck_fundraising_authorities_s8
INSERT INTO kyc.fundraising_authorities (id, kyb_organisation_id, authority_basis, reference_number, purpose, valid_from, valid_to, status)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000611', 'SECTION_8_AUTHORITY', 'S8-TEST-1', 'test purpose',
        DATE '2026-01-01', DATE '2026-01-01' + 181, 'PENDING_VERIFICATION');

-- @case s8_authority_without_expiry_rejected expect=error:ck_fundraising_authorities_s8
INSERT INTO kyc.fundraising_authorities (id, kyb_organisation_id, authority_basis, reference_number, purpose, valid_from, status)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000611', 'SECTION_8_AUTHORITY', 'S8-TEST-2', 'test purpose',
        DATE '2026-01-01', 'PENDING_VERIFICATION');

-- @case s8_authority_at_outer_bound_allowed expect=ok
INSERT INTO kyc.fundraising_authorities (id, kyb_organisation_id, authority_basis, reference_number, purpose, valid_from, valid_to, status)
VALUES ('00000000-0000-0000-0000-000000000621', '00000000-0000-0000-0000-000000000611', 'SECTION_8_AUTHORITY', 'S8-TEST-3', 'test purpose',
        DATE '2026-01-01', DATE '2026-01-01' + 180, 'PENDING_VERIFICATION');

-- @case authority_two_subjects_rejected expect=error:ck_fundraising_authorities_subject
INSERT INTO kyc.fundraising_authorities (id, kyb_organisation_id, user_id, authority_basis, reference_number, purpose, valid_from, status)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000611', '00000000-0000-0000-0000-0000000000a4', 'REGISTERED_PVO', 'PVO-TEST-1',
        'test', DATE '2026-01-01', 'PENDING_VERIFICATION');

-- @case authority_content_edit_rejected expect=error:content is immutable
UPDATE kyc.fundraising_authorities SET valid_to = valid_to + 10 WHERE id = '00000000-0000-0000-0000-000000000621';

-- @case org_person_ownership_bp_out_of_range_rejected expect=error:ck_organisation_persons_bp
INSERT INTO kyc.organisation_persons (id, kyb_organisation_id, user_id, roles, ownership_bp, valid_from)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000611', '00000000-0000-0000-0000-0000000000a5', '{BENEFICIAL_OWNER}', 10001, current_date);

-- @case org_person_unknown_role_rejected expect=error:ck_organisation_persons_roles
INSERT INTO kyc.organisation_persons (id, kyb_organisation_id, user_id, roles, valid_from)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000611', '00000000-0000-0000-0000-0000000000a5', '{SHAREHOLDER_FRIEND}', current_date);

-- ===================================== gate policies & callback inboxes =============================
-- @case gate_policy_below_stage0_floor_rejected expect=error:ck_gate_policies_floor
INSERT INTO kyc.gate_policies (id, policy_version, subject_kind, action, min_level, status, made_by, change_reason, decision_ref,
                               effective_from, review_by)
VALUES (gen_random_uuid(), 'gate-v2', 'INDIVIDUAL', 'WITHDRAW', 'IDENTITY_VERIFIED', 'PROPOSED', '00000000-0000-0000-0000-0000000000a1',
        'test', 'DEC-TEST-0010', now(), current_date + 90);

-- @case gate_policy_propose expect=ok
INSERT INTO kyc.gate_policies (id, policy_version, subject_kind, action, min_level, status, made_by, change_reason, decision_ref,
                               effective_from, review_by)
VALUES ('00000000-0000-0000-0000-000000000801', 'gate-v2', 'INDIVIDUAL', 'PUBLISH_CAMPAIGN', 'PAYOUT_VERIFIED', 'PROPOSED',
        '00000000-0000-0000-0000-0000000000a1', 'raise publish gate (test)', 'DEC-TEST-0011', now(), current_date + 90);

-- @case gate_policy_self_approval_rejected expect=error:ck_gate_policies_maker_checker
UPDATE kyc.gate_policies SET status = 'APPROVED', approved_by = '00000000-0000-0000-0000-0000000000a1', approved_at = now()
 WHERE id = '00000000-0000-0000-0000-000000000801';

-- @case gate_policy_approve_then_edit_rejected expect=error:immutable once decided
UPDATE kyc.gate_policies SET status = 'APPROVED', approved_by = '00000000-0000-0000-0000-0000000000a2', approved_at = now()
 WHERE id = '00000000-0000-0000-0000-000000000801';
UPDATE kyc.gate_policies SET min_level = 'IDENTITY_VERIFIED' WHERE id = '00000000-0000-0000-0000-000000000801';

-- @case vendor_callback_store expect=ok
INSERT INTO kyc.vendor_callback_inbox (id, vendor_code, vendor_event_id, event_type, signature_verified, verification_method,
                                       signing_key_ref, raw_payload_ciphertext, raw_payload_key_id, raw_payload_sha256)
VALUES ('00000000-0000-0000-0000-000000000811', 'vendor-test', 'evt-1', 'session.completed', true, 'HMAC', 'whsec-v1',
        decode(repeat('ab', 40), 'hex'), 'dek-test', decode(repeat('cd', 32), 'hex'));

-- @case vendor_callback_duplicate_rejected expect=error:uq_vendor_callback_inbox_event
INSERT INTO kyc.vendor_callback_inbox (id, vendor_code, vendor_event_id, event_type, signature_verified, verification_method,
                                       signing_key_ref, raw_payload_ciphertext, raw_payload_key_id, raw_payload_sha256)
VALUES (gen_random_uuid(), 'vendor-test', 'evt-1', 'session.completed', true, 'HMAC', 'whsec-v1',
        decode(repeat('ab', 40), 'hex'), 'dek-test', decode(repeat('cd', 32), 'hex'));

-- @case vendor_callback_unverified_rejected expect=error:ck_vendor_callback_inbox_verified
INSERT INTO kyc.vendor_callback_inbox (id, vendor_code, vendor_event_id, event_type, signature_verified, verification_method,
                                       signing_key_ref, raw_payload_ciphertext, raw_payload_key_id, raw_payload_sha256)
VALUES (gen_random_uuid(), 'vendor-test', 'evt-2', 'session.completed', false, 'HMAC', 'whsec-v1',
        decode(repeat('ab', 40), 'hex'), 'dek-test', decode(repeat('cd', 32), 'hex'));

-- @case vendor_callback_raw_edit_rejected expect=error:raw callback is append-only
UPDATE kyc.vendor_callback_inbox SET raw_payload_sha256 = decode(repeat('ee', 32), 'hex') WHERE id = '00000000-0000-0000-0000-000000000811';

-- @case screening_callback_duplicate_rejected expect=error:uq_screening_callback_inbox_event
INSERT INTO compliance.screening_callback_inbox (id, vendor_code, vendor_event_id, event_type, signature_verified, verification_method,
                                                 signing_key_ref, raw_payload_ciphertext, raw_payload_key_id, raw_payload_sha256)
VALUES (gen_random_uuid(), 'screen-test', 'd-1', 'list.delta', true, 'HMAC', 'whsec-s1', decode(repeat('ab', 40), 'hex'), 'dek', decode(repeat('cd', 32), 'hex')),
       (gen_random_uuid(), 'screen-test', 'd-1', 'list.delta', true, 'HMAC', 'whsec-s1', decode(repeat('ab', 40), 'hex'), 'dek', decode(repeat('cd', 32), 'hex'));

-- @case screening_status_view_latest_not_passing expect=ok
DO $$ BEGIN
  IF (SELECT passes FROM compliance.v_screening_status WHERE subject_id = '00000000-0000-0000-0000-0000000000a4') IS DISTINCT FROM false THEN
    RAISE EXCEPTION 'a POTENTIAL_MATCH must not pass EC-15';
  END IF;
END $$;

-- @case active_restriction_visible_in_read_model expect=ok
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM compliance.v_active_restrictions WHERE subject_id = '00000000-0000-0000-0000-0000000000a4'
                  AND capability = 'RECEIVE_PAYOUT') THEN
    RAISE EXCEPTION 'active RECEIVE_PAYOUT restriction missing from v_active_restrictions';
  END IF;
END $$;

-- ===================================== list sources & suppressions =================================
-- @case list_source_self_approval_rejected expect=error:ck_sls_maker_checker
INSERT INTO compliance.screening_list_sources (id, source_code, name, authority, list_type, mandatory_basis, basis_ref,
                                               update_frequency, status, created_by, approved_by, approved_at)
VALUES (gen_random_uuid(), 'UN_CONSOLIDATED', 'UN Security Council Consolidated List (test row)', 'UNSC', 'SANCTIONS', 'REGULATORY',
        'LR-060', 'on publication', 'PROPOSED', '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000a1', now());

-- @case list_source_regulatory_without_lr_rejected expect=error:ck_sls_basis
INSERT INTO compliance.screening_list_sources (id, source_code, name, authority, list_type, mandatory_basis, basis_ref,
                                               update_frequency, created_by)
VALUES (gen_random_uuid(), 'ZW_DOMESTIC', 'Zimbabwean list (test row)', 'FIU', 'SANCTIONS', 'REGULATORY', 'DEC-1', 'unknown',
        '00000000-0000-0000-0000-0000000000a1');

-- @case list_source_register_and_approve expect=ok
INSERT INTO compliance.screening_list_sources (id, source_code, name, authority, list_type, mandatory_basis, basis_ref,
                                               update_frequency, created_by)
VALUES ('00000000-0000-0000-0000-000000000901', 'UN_CONSOLIDATED', 'UN Security Council Consolidated List (test row)', 'UNSC',
        'SANCTIONS', 'REGULATORY', 'LR-065', 'on publication', '00000000-0000-0000-0000-0000000000a1');
UPDATE compliance.screening_list_sources SET status = 'ACTIVE', approved_by = '00000000-0000-0000-0000-0000000000a2', approved_at = now(),
       last_ingested_at = now(), version_hash = 'v-test-1', version = 2 WHERE id = '00000000-0000-0000-0000-000000000901';

-- @case list_source_basis_change_rejected expect=error:basis is immutable
UPDATE compliance.screening_list_sources SET basis_ref = 'LR-066', version = 3 WHERE id = '00000000-0000-0000-0000-000000000901';

-- @case hit_with_unregistered_list_rejected expect=error:fk_screening_hits_list_source
INSERT INTO compliance.screening_hits (id, result_id, list_source_code, list_version_hash, entry_ref, score, matched_fields, status)
VALUES (gen_random_uuid(), '00000000-0000-0000-0000-000000000411', 'UNKNOWN_LIST', 'v1', 'E-1', 900, '{NAME}', 'OPEN');

-- @case suppression_needs_cleared_determination expect=error:requires a CLEARED_BY_REVIEW determination
INSERT INTO compliance.screening_suppressions (id, subject_type, subject_id, list_source_id, entry_ref, entry_version_hash,
                                               subject_fingerprint, decided_result_id, created_by, approved_by)
VALUES (gen_random_uuid(), 'USER', '00000000-0000-0000-0000-0000000000a4', '00000000-0000-0000-0000-000000000901', 'E-1', 'v-test-1',
        decode(repeat('ab', 32), 'hex'), '00000000-0000-0000-0000-000000000411', '00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000a2');

-- @case suppression_self_approval_rejected expect=error:ck_ssup_maker_checker
INSERT INTO compliance.sanctions_screening_results (id, request_id, subject_type, subject_id, result, list_versions, screened_at,
                                                    valid_until, freshness_limit_id, supersedes_result_id, decided_by, second_reviewer_id,
                                                    reason_code, evidence_record_id)
VALUES ('00000000-0000-0000-0000-000000000412', '00000000-0000-0000-0000-000000000401', 'USER', '00000000-0000-0000-0000-0000000000a4',
        'CLEARED_BY_REVIEW', '{"UN_CONSOLIDATED":"v-test-1"}', now(), now() + interval '30 days', gen_random_uuid(),
        '00000000-0000-0000-0000-000000000411', '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000a2',
        'DOB_MISMATCH', '00000000-0000-0000-0000-0000000000e2');
INSERT INTO compliance.screening_suppressions (id, subject_type, subject_id, list_source_id, entry_ref, entry_version_hash,
                                               subject_fingerprint, decided_result_id, created_by, approved_by)
VALUES (gen_random_uuid(), 'USER', '00000000-0000-0000-0000-0000000000a4', '00000000-0000-0000-0000-000000000901', 'E-1', 'v-test-1',
        decode(repeat('ab', 32), 'hex'), '00000000-0000-0000-0000-000000000412', '00000000-0000-0000-0000-0000000000a1',
        '00000000-0000-0000-0000-0000000000a1');
