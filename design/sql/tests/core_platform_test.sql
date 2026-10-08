-- Invariant tests for 0002_platform, 0003_audit, 0005_storage and 0011_notifications (DESIGN DRAFTS).
-- Run by design/sql/validate/run.mjs; every case is its own transaction; "ok" cases commit and are
-- visible to later cases in this file.

-- ===================================================================================== money / registry
-- @case currencies_seeded_per_money_md expect=ok
DO $$ BEGIN
  IF (SELECT count(*) FROM app.currencies
       WHERE (code, numeric_code, minor_units, display_symbol) IN (('USD', 840, 2, 'US$'), ('ZWG', 924, 2, 'ZiG'))) <> 2 THEN
    RAISE EXCEPTION 'currency seed does not match MONEY.md §2';
  END IF;
  IF (SELECT minor_units_verified FROM app.currencies WHERE code = 'ZWG') THEN
    RAISE EXCEPTION 'ZWG must stay minor_units_verified = false until LR-043/PCR-018';
  END IF;
END $$;

-- @case no_float_numeric_or_money_type_columns_anywhere expect=ok
DO $$
DECLARE r record;
BEGIN
  -- Base tables only: views may expose exact SUM(bigint) results, which PostgreSQL types as numeric.
  FOR r IN SELECT c.table_schema, c.table_name, c.column_name, c.data_type FROM information_schema.columns c
            JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
            WHERE c.table_schema IN ('app', 'audit', 'ledger', 'kyc', 'risk', 'compliance', 'recon')
              AND (data_type IN ('real', 'double precision', 'money', 'numeric')
                   OR (column_name LIKE '%amount_minor' AND data_type <> 'bigint'))
  LOOP
    RAISE EXCEPTION 'money rule violated: %.%.% is %', r.table_schema, r.table_name, r.column_name, r.data_type;
  END LOOP;
END $$;

-- @case every_amount_column_has_a_currency_column expect=ok
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT c.table_schema, c.table_name, c.column_name FROM information_schema.columns c
            JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
            WHERE c.table_schema IN ('app', 'audit', 'ledger', 'kyc', 'risk', 'compliance', 'recon')
              AND c.column_name LIKE '%amount_minor'
              AND NOT EXISTS (SELECT 1 FROM information_schema.columns k
                               WHERE k.table_schema = c.table_schema AND k.table_name = c.table_name
                                 AND k.column_name LIKE '%currency' AND k.data_type = 'character')
  LOOP
    RAISE EXCEPTION 'amount without currency: %.%.%', r.table_schema, r.table_name, r.column_name;
  END LOOP;
END $$;

-- @case verified_currency_minor_units_are_frozen expect=error:minor_units of verified currency USD is immutable
UPDATE app.currencies SET minor_units = 3 WHERE code = 'USD';

-- @case currency_delete_rejected expect=error:currencies are never deleted
DELETE FROM app.currencies WHERE code = 'ZWG';

-- @case unknown_currency_code_format_rejected expect=error:ck_currencies_code_format
INSERT INTO app.currencies (code, numeric_code, minor_units, name, display_symbol) VALUES ('us$', 999, 2, 'Bad', 'X');

-- ===================================================================================== fixtures
-- @case fixtures_users expect=ok
INSERT INTO app.users (id, account_kind) VALUES
  ('00000000-0000-7000-8000-000000000001', 'STAFF'),
  ('00000000-0000-7000-8000-000000000002', 'STAFF'),
  ('00000000-0000-7000-8000-000000000003', 'USER');

-- ===================================================================================== idempotency
-- @case idempotency_key_first_use_ok expect=ok
INSERT INTO app.idempotency_keys (id, scope, key, request_hash, locked_until, expires_at)
VALUES ('00000000-0000-7000-8000-0000000000a1', 'payments.create:user:00000000-0000-7000-8000-000000000003',
        'k-2f1c9a0e-0001', sha256('POST /api/v1/donations {"amount_minor":"1000"}'::bytea),
        now() + interval '30 seconds', now() + interval '24 hours');

-- @case duplicate_idempotency_key_rejected expect=error:uq_idempotency_keys_scope_key
INSERT INTO app.idempotency_keys (id, scope, key, request_hash, expires_at)
VALUES ('00000000-0000-7000-8000-0000000000a2', 'payments.create:user:00000000-0000-7000-8000-000000000003',
        'k-2f1c9a0e-0001', sha256('other body'::bytea), now() + interval '24 hours');

-- @case same_key_in_other_scope_ok expect=ok
INSERT INTO app.idempotency_keys (id, scope, key, request_hash, expires_at)
VALUES ('00000000-0000-7000-8000-0000000000a3', 'payouts.request:user:00000000-0000-7000-8000-000000000003',
        'k-2f1c9a0e-0001', sha256('x'::bytea), now() + interval '24 hours');

-- @case idempotency_complete_ok expect=ok
UPDATE app.idempotency_keys
   SET status = 'COMPLETED', completed_at = now(), response_status_code = 201,
       response_body = '{"data":{"id":"x"}}', resource_type = 'donation', locked_until = NULL
 WHERE id = '00000000-0000-7000-8000-0000000000a1';

-- @case completed_idempotency_record_frozen expect=error:completed idempotency record is immutable
UPDATE app.idempotency_keys SET response_status_code = 500 WHERE id = '00000000-0000-7000-8000-0000000000a1';

-- @case idempotency_request_hash_immutable expect=error:idempotency key identity is immutable
UPDATE app.idempotency_keys SET request_hash = sha256('tampered'::bytea) WHERE id = '00000000-0000-7000-8000-0000000000a3';

-- @case idempotency_completed_without_response_rejected expect=error:ck_idempotency_keys_completed
INSERT INTO app.idempotency_keys (id, scope, key, request_hash, status, expires_at)
VALUES ('00000000-0000-7000-8000-0000000000a4', 's:x', 'k-00000000', sha256('y'::bytea), 'COMPLETED', now() + interval '1 hour');

-- ===================================================================================== outbox / inbox
-- @case outbox_insert_ok expect=ok
INSERT INTO app.outbox_events (id, aggregate_type, aggregate_id, event_type, payload, occurred_at)
VALUES ('00000000-0000-7000-8000-0000000000b1', 'campaign', '00000000-0000-7000-8000-0000000000c1',
        'campaign.status_changed', '{"from":"DRAFT","to":"SUBMITTED"}', now());

-- @case outbox_payload_immutable expect=error:only available_at, dispatched_at, attempts, last_error, dead_lettered_at may change
UPDATE app.outbox_events SET payload = '{"from":"DRAFT","to":"ACTIVE"}' WHERE id = '00000000-0000-7000-8000-0000000000b1';

-- @case outbox_undispatched_delete_rejected expect=error:undispatched outbox event
DELETE FROM app.outbox_events WHERE id = '00000000-0000-7000-8000-0000000000b1';

-- @case outbox_dispatch_bookkeeping_ok expect=ok
UPDATE app.outbox_events SET attempts = attempts + 1, dispatched_at = now() WHERE id = '00000000-0000-7000-8000-0000000000b1';

-- @case outbox_dispatched_and_dead_lettered_rejected expect=error:ck_outbox_events_terminal
UPDATE app.outbox_events SET dead_lettered_at = now() WHERE id = '00000000-0000-7000-8000-0000000000b1';

-- @case inbox_first_delivery_ok expect=ok
INSERT INTO app.inbox_events (id, consumer, event_id, event_type)
VALUES ('00000000-0000-7000-8000-0000000000d1', 'notifications.campaign_status', '00000000-0000-7000-8000-0000000000b1', 'campaign.status_changed');

-- @case inbox_duplicate_delivery_rejected expect=error:uq_inbox_events_consumer_event_id
INSERT INTO app.inbox_events (id, consumer, event_id, event_type)
VALUES ('00000000-0000-7000-8000-0000000000d2', 'notifications.campaign_status', '00000000-0000-7000-8000-0000000000b1', 'campaign.status_changed');

-- ===================================================================================== feature flags
-- @case policy_flag_seeded_off expect=ok
DO $$ BEGIN
  IF (SELECT enabled FROM app.feature_flags WHERE key = 'campaign.individual_for_others.enabled') THEN
    RAISE EXCEPTION 'individual-for-others must default to disabled (LR-046..048, PD-27)';
  END IF;
END $$;

-- @case flag_change_without_change_row_rejected expect=error:has no feature_flag_changes row
UPDATE app.feature_flags SET enabled = true WHERE key = 'campaign.individual_for_others.enabled';

-- @case flag_change_without_approver_rejected expect=error:requires an approver
INSERT INTO app.feature_flag_changes (id, feature_flag_id, flag_version, old_enabled, new_enabled, requested_by, reason, occurred_at)
SELECT '00000000-0000-7000-8000-0000000000e1', id, version + 1, enabled, true, '00000000-0000-7000-8000-000000000001', 'test enable', now()
  FROM app.feature_flags WHERE key = 'campaign.individual_for_others.enabled';
UPDATE app.feature_flags SET enabled = true WHERE key = 'campaign.individual_for_others.enabled';

-- @case flag_self_approval_rejected expect=error:ck_feature_flag_changes_maker_checker
INSERT INTO app.feature_flag_changes (id, feature_flag_id, flag_version, old_enabled, new_enabled, requested_by, approved_by, reason, occurred_at)
SELECT '00000000-0000-7000-8000-0000000000e2', id, version + 1, enabled, true,
       '00000000-0000-7000-8000-000000000001', '00000000-0000-7000-8000-000000000001', 'test enable', now()
  FROM app.feature_flags WHERE key = 'campaign.individual_for_others.enabled';

-- @case flag_change_with_checker_ok expect=ok
INSERT INTO app.feature_flag_changes (id, feature_flag_id, flag_version, old_enabled, new_enabled, requested_by, approved_by, reason, occurred_at)
SELECT '00000000-0000-7000-8000-0000000000e3', id, version + 1, enabled, true,
       '00000000-0000-7000-8000-000000000001', '00000000-0000-7000-8000-000000000002', 'counsel position recorded (test)', now()
  FROM app.feature_flags WHERE key = 'campaign.individual_for_others.enabled';
UPDATE app.feature_flags SET enabled = true WHERE key = 'campaign.individual_for_others.enabled';

-- @case flag_change_history_append_only expect=error:append-only table
DELETE FROM app.feature_flag_changes WHERE id = '00000000-0000-7000-8000-0000000000e3';

-- ===================================================================================== audit chain
-- @case audit_events_insert_chain_ok expect=ok
INSERT INTO audit.audit_events (id, occurred_at, actor_type, actor_id, action, target_type, target_id, outcome, metadata)
VALUES ('00000000-0000-7000-8000-0000000001a1', now(), 'user', '00000000-0000-7000-8000-000000000003', 'user.created', 'user',
        '00000000-0000-7000-8000-000000000003', 'success', '{}');
INSERT INTO audit.audit_events (id, occurred_at, actor_type, action, target_type, target_id, outcome, metadata)
VALUES ('00000000-0000-7000-8000-0000000001a2', now(), 'system', 'campaign.state.changed', 'campaign',
        '00000000-0000-7000-8000-0000000000c1', 'success', '{"from":"DRAFT","to":"SUBMITTED"}');
-- caller-supplied seq / hash are ignored by the chain trigger
INSERT INTO audit.audit_events (id, seq, occurred_at, actor_type, actor_id, actor_role, action, target_type, outcome, justification, prev_hash, hash)
VALUES ('00000000-0000-7000-8000-0000000001a3', 999, now(), 'staff', '00000000-0000-7000-8000-000000000001', 'FINANCE',
        'payout.approval.denied_self', 'payout', 'denied', 'attempted self approval', '\x00', '\x00');

-- @case audit_chain_links_and_verifies expect=ok
DO $$ BEGIN
  IF (SELECT array_agg(seq ORDER BY seq) FROM audit.audit_events) <> ARRAY[1, 2, 3]::bigint[] THEN
    RAISE EXCEPTION 'seq not contiguous';
  END IF;
  IF (SELECT prev_hash FROM audit.audit_events WHERE seq = 3) <> (SELECT hash FROM audit.audit_events WHERE seq = 2) THEN
    RAISE EXCEPTION 'prev_hash does not link';
  END IF;
  IF EXISTS (SELECT 1 FROM audit.verify_chain('audit.audit_events')) THEN
    RAISE EXCEPTION 'intact chain reported problems';
  END IF;
END $$;

-- @case audit_update_rejected expect=error:append-only table
UPDATE audit.audit_events SET reason = 'edited' WHERE seq = 2;

-- @case audit_delete_rejected expect=error:append-only table
DELETE FROM audit.audit_events WHERE seq = 3;

-- @case audit_truncate_rejected expect=error:append-only table
TRUNCATE audit.audit_events CASCADE;

-- Simulates an owner-level attacker who disables the guard trigger and edits a row: the verifier must
-- report it. The case raises TAMPER_DETECTED (and rolls back) only if detection works.
-- @case audit_tamper_detected_by_verifier expect=error:TAMPER_DETECTED
ALTER TABLE audit.audit_events DISABLE TRIGGER trg_audit_events_no_mutation;
UPDATE audit.audit_events SET outcome = 'success' WHERE seq = 3;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM audit.verify_chain('audit.audit_events') v WHERE v.seq = 3 AND v.problem LIKE 'hash mismatch%') THEN
    RAISE EXCEPTION 'TAMPER_DETECTED';
  END IF;
END $$;

-- @case audit_deleted_row_detected_by_verifier expect=error:TAMPER_DETECTED
ALTER TABLE audit.audit_events DISABLE TRIGGER trg_audit_events_no_mutation;
DELETE FROM audit.audit_events WHERE seq = 2;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM audit.verify_chain('audit.audit_events') v WHERE v.seq = 3) THEN
    RAISE EXCEPTION 'TAMPER_DETECTED';
  END IF;
END $$;

-- @case audit_break_glass_requires_justification expect=error:ck_audit_events_break_glass_justified
INSERT INTO audit.audit_events (id, occurred_at, actor_type, actor_id, action, target_type, outcome, break_glass)
VALUES ('00000000-0000-7000-8000-0000000001a4', now(), 'staff', '00000000-0000-7000-8000-000000000001', 'breakglass.started', 'system', 'success', true);

-- @case security_audit_is_a_separate_chain expect=ok
INSERT INTO audit.security_audit_events (id, occurred_at, actor_type, actor_id, actor_role, action, target_type, target_id, outcome, justification)
VALUES ('00000000-0000-7000-8000-0000000001b1', now(), 'staff', '00000000-0000-7000-8000-000000000002', 'SUPER_ADMIN',
        'role.grant.approved', 'user', '00000000-0000-7000-8000-000000000001', 'success', 'quarterly staffing');
DO $$ BEGIN
  IF (SELECT seq FROM audit.security_audit_events WHERE id = '00000000-0000-7000-8000-0000000001b1') <> 1 THEN
    RAISE EXCEPTION 'security chain must start at seq 1';
  END IF;
  IF EXISTS (SELECT 1 FROM audit.verify_chain('audit.security_audit_events')) THEN
    RAISE EXCEPTION 'security chain broken';
  END IF;
END $$;

-- @case security_audit_update_rejected expect=error:append-only table
UPDATE audit.security_audit_events SET outcome = 'denied';

-- ===================================================================================== evidence
-- @case evidence_record_ok expect=ok
INSERT INTO audit.audit_events (id, occurred_at, actor_type, actor_id, action, target_type, target_id, outcome)
VALUES ('00000000-0000-7000-8000-0000000001c1', now(), 'user', '00000000-0000-7000-8000-000000000003', 'campaign.submitted', 'campaign',
        '00000000-0000-7000-8000-0000000000c1', 'success');
INSERT INTO audit.evidence_records (id, evidence_type, subject_type, subject_id, collected_at, collected_by_type, collected_by_id,
                                    source, storage_ref, content_sha256, size_bytes, media_type, classification, retention_class, audit_event_id)
VALUES ('00000000-0000-7000-8000-0000000002a1', 'SUPPORTING_DOCUMENT', 'campaign', '00000000-0000-7000-8000-0000000000c1', now(),
        'user', '00000000-0000-7000-8000-000000000003', 'upload', 'compliance/0f6f3b4e-5d0c-4a2e-9b1f-2a3b4c5d6e7f',
        sha256('pdf bytes'::bytea), 9, 'application/pdf', 'C3', 'CASE', '00000000-0000-7000-8000-0000000001c1');

-- @case evidence_update_of_other_column_rejected expect=error:only legal_hold may change
UPDATE audit.evidence_records SET content_sha256 = sha256('forged'::bytea) WHERE id = '00000000-0000-7000-8000-0000000002a1';

-- @case evidence_legal_hold_without_hold_row_rejected expect=error:legal_hold must match evidence_holds
UPDATE audit.evidence_records SET legal_hold = true WHERE id = '00000000-0000-7000-8000-0000000002a1';

-- @case evidence_created_on_hold_rejected expect=error:created without legal_hold
INSERT INTO audit.evidence_records (id, evidence_type, subject_type, subject_id, collected_at, collected_by_type, source,
                                    related_refs, content_sha256, classification, retention_class, legal_hold, audit_event_id)
VALUES ('00000000-0000-7000-8000-0000000002a2', 'SYSTEM_SNAPSHOT', 'campaign', '00000000-0000-7000-8000-0000000000c1', now(), 'system',
        'system_snapshot', '{"campaign_version_id":"x"}', sha256('s'::bytea), 'C2', 'CASE', true, '00000000-0000-7000-8000-0000000001c1');

-- @case evidence_hold_self_approval_rejected expect=error:ck_evidence_holds_maker_checker
INSERT INTO audit.evidence_holds (id, evidence_record_id, action, reason, requested_by, approved_by, occurred_at)
VALUES ('00000000-0000-7000-8000-0000000002b1', '00000000-0000-7000-8000-0000000002a1', 'HOLD', 'regulator request',
        '00000000-0000-7000-8000-000000000001', '00000000-0000-7000-8000-000000000001', now());

-- @case evidence_hold_placed_ok expect=ok
INSERT INTO audit.evidence_holds (id, evidence_record_id, action, reason, requested_by, approved_by, occurred_at)
VALUES ('00000000-0000-7000-8000-0000000002b2', '00000000-0000-7000-8000-0000000002a1', 'HOLD', 'regulator request',
        '00000000-0000-7000-8000-000000000001', '00000000-0000-7000-8000-000000000002', now());
UPDATE audit.evidence_records SET legal_hold = true WHERE id = '00000000-0000-7000-8000-0000000002a1';

-- @case evidence_delete_rejected expect=error:append-only table
DELETE FROM audit.evidence_records WHERE id = '00000000-0000-7000-8000-0000000002a1';

-- @case evidence_clearing_hold_while_active_rejected expect=error:legal_hold must match evidence_holds
UPDATE audit.evidence_records SET legal_hold = false WHERE id = '00000000-0000-7000-8000-0000000002a1';

-- @case evidence_hold_released_ok expect=ok
INSERT INTO audit.evidence_holds (id, evidence_record_id, action, released_hold_id, reason, requested_by, approved_by, occurred_at)
VALUES ('00000000-0000-7000-8000-0000000002b3', '00000000-0000-7000-8000-0000000002a1', 'RELEASE', '00000000-0000-7000-8000-0000000002b2',
        'investigation closed', '00000000-0000-7000-8000-000000000002', '00000000-0000-7000-8000-000000000001', now());
UPDATE audit.evidence_records SET legal_hold = false WHERE id = '00000000-0000-7000-8000-0000000002a1';

-- @case evidence_hold_released_twice_rejected expect=error:uq_evidence_holds_released_hold_id
INSERT INTO audit.evidence_holds (id, evidence_record_id, action, released_hold_id, reason, requested_by, approved_by, occurred_at)
VALUES ('00000000-0000-7000-8000-0000000002b4', '00000000-0000-7000-8000-0000000002a1', 'RELEASE', '00000000-0000-7000-8000-0000000002b2',
        'again', '00000000-0000-7000-8000-000000000002', '00000000-0000-7000-8000-000000000001', now());

-- @case evidence_holds_append_only expect=error:append-only table
UPDATE audit.evidence_holds SET reason = 'edited';

-- ===================================================================================== storage
-- @case stored_object_quarantined_ok expect=ok
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                classification, retention_class, original_filename, uploaded_by_user_id)
VALUES ('00000000-0000-7000-8000-0000000003a1', 'PUBLIC_MEDIA', 'campaigns', 'quarantine/6d1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f',
        sha256('jpeg'::bytea), 2048, 'image/jpeg', 'C1', 'OPERATIONAL', 'IMG_0001.jpg', '00000000-0000-7000-8000-000000000003');

-- @case stored_object_cannot_start_clean expect=error:stored objects start QUARANTINED
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                scan_status, scanned_at, scan_engine, sniffed_content_type, classification, retention_class)
VALUES ('00000000-0000-7000-8000-0000000003a2', 'PUBLIC_MEDIA', 'campaigns', 'quarantine/7d1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f',
        sha256('jpeg2'::bytea), 2048, 'image/jpeg', 'CLEAN', now(), 'clamav', 'image/jpeg', 'C1', 'OPERATIONAL');

-- @case promotion_before_clean_rejected expect=error:ck_stored_objects_promotion
UPDATE app.stored_objects SET promoted_key = 'objects/6d1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f', promoted_at = now()
 WHERE id = '00000000-0000-7000-8000-0000000003a1';

-- @case svg_or_pdf_not_allowed_as_public_media expect=error:ck_stored_objects_content_type
UPDATE app.stored_objects SET scan_status = 'CLEAN', scanned_at = now(), scan_engine = 'clamav', sniffed_content_type = 'application/pdf'
 WHERE id = '00000000-0000-7000-8000-0000000003a1';

-- @case scan_clean_and_promote_ok expect=ok
UPDATE app.stored_objects SET scan_status = 'CLEAN', scanned_at = now(), scan_engine = 'clamav', sniffed_content_type = 'image/jpeg',
       promoted_key = 'objects/6d1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f', promoted_at = now()
 WHERE id = '00000000-0000-7000-8000-0000000003a1';

-- @case clean_object_cannot_be_requarantined expect=error:illegal stored_object scan transition
UPDATE app.stored_objects SET scan_status = 'QUARANTINED' WHERE id = '00000000-0000-7000-8000-0000000003a1';

-- @case object_hash_immutable expect=error:is immutable
UPDATE app.stored_objects SET content_sha256 = sha256('other'::bytea) WHERE id = '00000000-0000-7000-8000-0000000003a1';

-- @case kyc_object_with_filename_rejected expect=error:ck_stored_objects_kyc_no_filename
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                classification, retention_class, encryption_key_id, original_filename)
VALUES ('00000000-0000-7000-8000-0000000003a3', 'PRIVATE_KYC', 'kyc', 'quarantine/8d1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f',
        sha256('id'::bytea), 4096, 'image/jpeg', 'C3', 'KYC', 'kms-key-private-kyc', 'passport_JOHN_DOE_AB123456.jpg');

-- @case kyc_object_must_be_c3_and_encrypted expect=error:ck_stored_objects_classification
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                classification, retention_class, encryption_key_id)
VALUES ('00000000-0000-7000-8000-0000000003a4', 'PRIVATE_KYC', 'kyc', 'quarantine/9d1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f',
        sha256('id2'::bytea), 4096, 'image/jpeg', 'C2', 'KYC', 'kms-key-private-kyc');

-- @case user_derived_object_key_rejected expect=error:ck_stored_objects_quarantine_key
INSERT INTO app.stored_objects (id, bucket_class, owner_module, quarantine_key, content_sha256, size_bytes, declared_content_type,
                                classification, retention_class)
VALUES ('00000000-0000-7000-8000-0000000003a5', 'PUBLIC_MEDIA', 'campaigns', 'quarantine/../../campaigns/my-photo.jpg',
        sha256('x'::bytea), 10, 'image/jpeg', 'C1', 'OPERATIONAL');

-- @case stored_object_delete_rejected expect=error:append-only table
DELETE FROM app.stored_objects WHERE id = '00000000-0000-7000-8000-0000000003a1';

-- @case upload_session_wrong_purpose_for_bucket_rejected expect=error:ck_upload_sessions_purpose
INSERT INTO app.upload_sessions (id, bucket_class, purpose, user_id, quarantine_key, declared_content_type, declared_size_bytes,
                                 declared_sha256, expires_at)
VALUES ('00000000-0000-7000-8000-0000000003b1', 'PUBLIC_MEDIA', 'KYC_DOCUMENT', '00000000-0000-7000-8000-000000000003',
        'quarantine/ad1f0c1e-3b2a-4c5d-8e9f-0a1b2c3d4e5f', 'image/jpeg', 100, sha256('a'::bytea), now() + interval '10 minutes');

-- ===================================================================================== notifications
-- @case notification_template_and_job_ok expect=ok
INSERT INTO app.notification_templates (id, template_key, channel, locale, version, category, is_mandatory, body_template,
                                        allowed_variables, status, created_by, approved_by, activated_at)
VALUES ('00000000-0000-7000-8000-0000000004a1', 'auth.otp', 'SMS', 'en-ZW', 1, 'SECURITY', true,
        'Your FundZim code is {{code}}. Never share it.', '{code}', 'ACTIVE',
        '00000000-0000-7000-8000-000000000001', '00000000-0000-7000-8000-000000000002', now());
INSERT INTO app.notification_jobs (id, template_id, channel, category, dedupe_key, recipient_user_id, destination_hmac)
VALUES ('00000000-0000-7000-8000-0000000004b1', '00000000-0000-7000-8000-0000000004a1', 'SMS', 'SECURITY',
        'otp:00000000-0000-7000-8000-0000000005a1:SMS', '00000000-0000-7000-8000-000000000003', sha256('+263771234567'::bytea));

-- @case notification_duplicate_dedupe_key_rejected expect=error:uq_notification_jobs_dedupe_key
INSERT INTO app.notification_jobs (id, template_id, channel, category, dedupe_key, recipient_user_id)
VALUES ('00000000-0000-7000-8000-0000000004b2', '00000000-0000-7000-8000-0000000004a1', 'SMS', 'SECURITY',
        'otp:00000000-0000-7000-8000-0000000005a1:SMS', '00000000-0000-7000-8000-000000000003');

-- @case notification_illegal_transition_rejected expect=error:illegal notification_job transition
UPDATE app.notification_jobs SET status = 'DELIVERED' WHERE id = '00000000-0000-7000-8000-0000000004b1';

-- @case otp_code_never_stored_in_notification_variables expect=error:ck_notification_jobs_no_secret_variables
INSERT INTO app.notification_jobs (id, template_id, channel, category, dedupe_key, recipient_user_id, variables)
VALUES ('00000000-0000-7000-8000-0000000004b3', '00000000-0000-7000-8000-0000000004a1', 'SMS', 'SECURITY',
        'otp:00000000-0000-7000-8000-0000000005a2:SMS', '00000000-0000-7000-8000-000000000003', '{"code":"123456"}');

-- @case otp_job_sent_ok expect=ok
UPDATE app.notification_jobs SET status = 'SENDING', attempts = 1 WHERE id = '00000000-0000-7000-8000-0000000004b1';
INSERT INTO app.notification_attempts (id, notification_job_id, attempt_number, provider_code, provider_message_id, outcome, started_at, finished_at)
VALUES ('00000000-0000-7000-8000-0000000004c1', '00000000-0000-7000-8000-0000000004b1', 1, 'log_sms', 'log-1', 'ACCEPTED', now(), now());
UPDATE app.notification_jobs SET status = 'SENT', sent_at = now() WHERE id = '00000000-0000-7000-8000-0000000004b1';

-- @case notification_attempts_append_only expect=error:append-only table
UPDATE app.notification_attempts SET outcome = 'REJECTED';

-- @case active_template_content_immutable expect=error:immutable; create a new version
UPDATE app.notification_templates SET body_template = 'Send your code to +263...' WHERE id = '00000000-0000-7000-8000-0000000004a1';

-- @case marketing_opt_in_requires_consent expect=error:ck_notification_preferences_marketing_consent
INSERT INTO app.notification_preferences (id, user_id, category, channel, enabled)
VALUES ('00000000-0000-7000-8000-0000000004d1', '00000000-0000-7000-8000-000000000003', 'MARKETING', 'EMAIL', true);

-- @case security_messages_not_preference_controlled expect=error:ck_notification_preferences_category
INSERT INTO app.notification_preferences (id, user_id, category, channel, enabled)
VALUES ('00000000-0000-7000-8000-0000000004d2', '00000000-0000-7000-8000-000000000003', 'SECURITY', 'SMS', false);

-- @case one_active_suppression_per_destination expect=error:uq_notification_suppressions_active
INSERT INTO app.notification_suppressions (id, channel, destination_hmac, hmac_key_id, reason, scope, source)
VALUES ('00000000-0000-7000-8000-0000000004e1', 'EMAIL', sha256('a@example.org'::bytea), 'k1', 'HARD_BOUNCE', 'ALL', 'provider_webhook'),
       ('00000000-0000-7000-8000-0000000004e2', 'EMAIL', sha256('a@example.org'::bytea), 'k1', 'COMPLAINT', 'ALL_NON_MANDATORY', 'provider_webhook');
