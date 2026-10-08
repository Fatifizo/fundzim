-- Invariant tests for 0004_users_auth and 0009_organisations (DESIGN DRAFTS).
-- Users: U1 ...0011, U2 ...0012 (personal). Staff: S1 ...0021 .. S4 ...0024.

-- @case fixtures_users expect=ok
INSERT INTO app.users (id, account_kind) VALUES
  ('00000000-0000-7000-8000-000000000011', 'USER'),
  ('00000000-0000-7000-8000-000000000012', 'USER'),
  ('00000000-0000-7000-8000-000000000021', 'STAFF'),
  ('00000000-0000-7000-8000-000000000022', 'STAFF'),
  ('00000000-0000-7000-8000-000000000023', 'STAFF'),
  ('00000000-0000-7000-8000-000000000024', 'STAFF');

-- ===================================================================================== users
-- @case unknown_account_kind_rejected expect=error:ck_users_account_kind
INSERT INTO app.users (id, account_kind) VALUES ('00000000-0000-7000-8000-000000000019', 'ADMIN');

-- @case user_must_start_active expect=error:illegal user_account transition
INSERT INTO app.users (id, account_kind, status, closed_at) VALUES ('00000000-0000-7000-8000-000000000018', 'USER', 'CLOSED', now());

-- @case user_illegal_transition_rejected expect=error:illegal user_account transition
UPDATE app.users SET status = 'CLOSED', closed_at = now() WHERE id = '00000000-0000-7000-8000-000000000011';

-- @case account_kind_immutable expect=error:is immutable
UPDATE app.users SET account_kind = 'STAFF' WHERE id = '00000000-0000-7000-8000-000000000011';

-- @case user_delete_rejected expect=error:append-only table
DELETE FROM app.users WHERE id = '00000000-0000-7000-8000-000000000012';

-- @case user_suspend_ok_and_version_bumped expect=ok
UPDATE app.users SET status = 'SUSPENDED', suspended_at = now() WHERE id = '00000000-0000-7000-8000-000000000012';
DO $$ BEGIN
  IF (SELECT version FROM app.users WHERE id = '00000000-0000-7000-8000-000000000012') <> 2 THEN RAISE EXCEPTION 'version not bumped'; END IF;
END $$;
UPDATE app.users SET status = 'ACTIVE', suspended_at = NULL WHERE id = '00000000-0000-7000-8000-000000000012';

-- @case staff_link_only_on_staff_accounts expect=error:ck_users_staff_link
UPDATE app.users SET staff_personal_user_id = '00000000-0000-7000-8000-000000000012' WHERE id = '00000000-0000-7000-8000-000000000011';

-- ===================================================================================== contacts
-- @case verified_phone_ok expect=ok
INSERT INTO app.user_phone_numbers (id, user_id, e164, is_primary, verified_at)
VALUES ('00000000-0000-7000-8000-000000000101', '00000000-0000-7000-8000-000000000011', '+263771234567', true, now());

-- @case duplicate_verified_phone_rejected expect=error:uq_user_phone_numbers_verified_e164
INSERT INTO app.user_phone_numbers (id, user_id, e164, verified_at)
VALUES ('00000000-0000-7000-8000-000000000102', '00000000-0000-7000-8000-000000000012', '+263771234567', now());

-- @case unverified_duplicate_phone_allowed expect=ok
INSERT INTO app.user_phone_numbers (id, user_id, e164)
VALUES ('00000000-0000-7000-8000-000000000103', '00000000-0000-7000-8000-000000000012', '+263771234567');

-- @case verifying_a_claimed_phone_rejected expect=error:uq_user_phone_numbers_verified_e164
UPDATE app.user_phone_numbers SET verified_at = now() WHERE id = '00000000-0000-7000-8000-000000000103';

-- @case phone_not_e164_rejected expect=error:ck_user_phone_numbers_e164
INSERT INTO app.user_phone_numbers (id, user_id, e164) VALUES ('00000000-0000-7000-8000-000000000104', '00000000-0000-7000-8000-000000000012', '0771234567');

-- @case unverified_primary_phone_rejected expect=error:ck_user_phone_numbers_primary_verified
INSERT INTO app.user_phone_numbers (id, user_id, e164, is_primary) VALUES ('00000000-0000-7000-8000-000000000105', '00000000-0000-7000-8000-000000000012', '+263772222222', true);

-- @case verified_email_ok expect=ok
INSERT INTO app.user_emails (id, user_id, email, email_normalized, is_primary, verified_at)
VALUES ('00000000-0000-7000-8000-000000000111', '00000000-0000-7000-8000-000000000011', 'Tendai@Example.org', 'tendai@example.org', true, now());

-- @case duplicate_verified_email_rejected expect=error:uq_user_emails_verified_email
INSERT INTO app.user_emails (id, user_id, email, email_normalized, verified_at)
VALUES ('00000000-0000-7000-8000-000000000112', '00000000-0000-7000-8000-000000000012', 'tendai@example.org', 'tendai@example.org', now());

-- @case non_normalized_email_rejected expect=error:ck_user_emails_normalized
INSERT INTO app.user_emails (id, user_id, email, email_normalized)
VALUES ('00000000-0000-7000-8000-000000000113', '00000000-0000-7000-8000-000000000012', 'X@Example.org', 'X@Example.org');

-- @case deleted_phone_frees_number_for_new_owner expect=ok
UPDATE app.user_phone_numbers SET deleted_at = now(), is_primary = false WHERE id = '00000000-0000-7000-8000-000000000101';
UPDATE app.user_phone_numbers SET verified_at = now() WHERE id = '00000000-0000-7000-8000-000000000103';

-- ===================================================================================== credentials
-- @case non_argon2id_password_rejected expect=error:ck_password_credentials_argon2id
INSERT INTO app.password_credentials (id, user_id, password_hash)
VALUES ('00000000-0000-7000-8000-000000000121', '00000000-0000-7000-8000-000000000011', 'plaintext-password-123');

-- @case argon2id_password_ok expect=ok
INSERT INTO app.password_credentials (id, user_id, password_hash)
VALUES ('00000000-0000-7000-8000-000000000122', '00000000-0000-7000-8000-000000000011',
        '$argon2id$v=19$m=65536,t=3,p=2$c29tZXNhbHQ$RdescudvJCsgt3ub+b+dWRWJTmaaJObG');

-- @case password_hash_immutable expect=error:only must_change, compromised_at, superseded_at may change
UPDATE app.password_credentials SET password_hash = '$argon2id$v=19$m=65536,t=3,p=2$b3RoZXI$abc' WHERE id = '00000000-0000-7000-8000-000000000122';

-- @case two_current_passwords_rejected expect=error:uq_password_credentials_current
INSERT INTO app.password_credentials (id, user_id, password_hash)
VALUES ('00000000-0000-7000-8000-000000000123', '00000000-0000-7000-8000-000000000011',
        '$argon2id$v=19$m=65536,t=3,p=2$c2FsdDI$RdescudvJCsgt3ub+b+dWRWJTmaaJObG');

-- @case totp_without_encrypted_secret_rejected expect=error:ck_mfa_methods_totp
INSERT INTO app.mfa_methods (id, user_id, method_type, label)
VALUES ('00000000-0000-7000-8000-000000000131', '00000000-0000-7000-8000-000000000021', 'TOTP', 'phone');

-- @case staff_mfa_methods_ok expect=ok
INSERT INTO app.mfa_methods (id, user_id, method_type, label, webauthn_credential_id, webauthn_public_key, webauthn_sign_count, confirmed_at)
VALUES ('00000000-0000-7000-8000-000000000132', '00000000-0000-7000-8000-000000000021', 'WEBAUTHN', 'security key', '\x0102', '\xa501', 0, now());
INSERT INTO app.mfa_methods (id, user_id, method_type, label, totp_secret_ciphertext, totp_secret_key_id, confirmed_at)
VALUES ('00000000-0000-7000-8000-000000000133', '00000000-0000-7000-8000-000000000022', 'TOTP', 'authenticator', '\x00ff00ff', 'mfa-secret-v1', now());

-- ===================================================================================== OTP
-- @case otp_challenge_ok expect=ok
INSERT INTO app.otp_challenges (id, purpose, channel, destination_hmac, code_hmac, hmac_key_id, flow_id, expires_at)
VALUES ('00000000-0000-7000-8000-000000000141', 'LOGIN', 'SMS', sha256('+263771234567'::bytea), sha256('123456|141'::bytea), 'otp-v1',
        '00000000-0000-7000-8000-0000000001f1', now() + interval '5 minutes');

-- @case otp_ttl_over_five_minutes_rejected expect=error:ck_otp_challenges_ttl
INSERT INTO app.otp_challenges (id, purpose, channel, destination_hmac, code_hmac, hmac_key_id, flow_id, expires_at)
VALUES ('00000000-0000-7000-8000-000000000142', 'VERIFY_EMAIL', 'EMAIL', sha256('a@example.org'::bytea), sha256('1'::bytea), 'otp-v1',
        '00000000-0000-7000-8000-0000000001f2', now() + interval '10 minutes');

-- @case second_live_otp_for_same_destination_rejected expect=error:uq_otp_challenges_live
INSERT INTO app.otp_challenges (id, purpose, channel, destination_hmac, code_hmac, hmac_key_id, flow_id, expires_at)
VALUES ('00000000-0000-7000-8000-000000000143', 'LOGIN', 'SMS', sha256('+263771234567'::bytea), sha256('654321|143'::bytea), 'otp-v1',
        '00000000-0000-7000-8000-0000000001f1', now() + interval '5 minutes');

-- @case otp_unknown_purpose_rejected expect=error:ck_otp_challenges_purpose
INSERT INTO app.otp_challenges (id, purpose, channel, destination_hmac, code_hmac, hmac_key_id, flow_id, expires_at)
VALUES ('00000000-0000-7000-8000-000000000144', 'anything', 'SMS', sha256('+263770000000'::bytea), sha256('1'::bytea), 'otp-v1',
        '00000000-0000-7000-8000-0000000001f3', now() + interval '5 minutes');

-- @case otp_payout_purpose_requires_user expect=error:ck_otp_challenges_user_bound
INSERT INTO app.otp_challenges (id, purpose, channel, destination_hmac, code_hmac, hmac_key_id, flow_id, expires_at)
VALUES ('00000000-0000-7000-8000-000000000145', 'PAYOUT_DESTINATION_CHANGE', 'SMS', sha256('+263770000001'::bytea), sha256('1'::bytea), 'otp-v1',
        '00000000-0000-7000-8000-0000000001f4', now() + interval '5 minutes');

-- @case otp_sixth_attempt_rejected expect=error:ck_otp_challenges_attempts
UPDATE app.otp_challenges SET attempts = 6 WHERE id = '00000000-0000-7000-8000-000000000141';

-- @case otp_attempts_cannot_decrease expect=error:otp attempts cannot decrease
UPDATE app.otp_challenges SET attempts = 2 WHERE id = '00000000-0000-7000-8000-000000000141';
UPDATE app.otp_challenges SET attempts = 1 WHERE id = '00000000-0000-7000-8000-000000000141';

-- @case otp_code_hash_immutable expect=error:otp challenge is immutable
UPDATE app.otp_challenges SET code_hmac = sha256('000000'::bytea) WHERE id = '00000000-0000-7000-8000-000000000141';

-- @case otp_consume_ok expect=ok
UPDATE app.otp_challenges SET attempts = 1, consumed_at = now() WHERE id = '00000000-0000-7000-8000-000000000141';

-- @case otp_replay_after_consume_rejected expect=error:already consumed or invalidated
UPDATE app.otp_challenges SET attempts = 2 WHERE id = '00000000-0000-7000-8000-000000000141';

-- ===================================================================================== sessions
-- @case user_session_ok expect=ok
INSERT INTO app.sessions (id, user_id, kind, token_hash, auth_method, idle_expires_at, absolute_expires_at, ip, user_agent)
VALUES ('00000000-0000-7000-8000-000000000151', '00000000-0000-7000-8000-000000000011', 'USER', sha256('token-1'::bytea), 'PHONE_OTP',
        now() + interval '7 days', now() + interval '30 days', '196.4.1.1', 'Mozilla/5.0');

-- @case duplicate_session_token_hash_rejected expect=error:uq_sessions_token_hash
INSERT INTO app.sessions (id, user_id, kind, token_hash, auth_method, idle_expires_at, absolute_expires_at)
VALUES ('00000000-0000-7000-8000-000000000152', '00000000-0000-7000-8000-000000000012', 'USER', sha256('token-1'::bytea), 'PHONE_OTP',
        now() + interval '7 days', now() + interval '30 days');

-- @case session_kind_must_match_account_kind expect=error:fk_sessions_user_id_kind
INSERT INTO app.sessions (id, user_id, kind, token_hash, auth_method, idle_expires_at, absolute_expires_at)
VALUES ('00000000-0000-7000-8000-000000000153', '00000000-0000-7000-8000-000000000021', 'USER', sha256('token-3'::bytea), 'PASSWORD',
        now() + interval '15 minutes', now() + interval '12 hours');

-- @case staff_session_without_mfa_rejected expect=error:ck_sessions_staff_mfa
INSERT INTO app.sessions (id, user_id, kind, token_hash, auth_method, idle_expires_at, absolute_expires_at)
VALUES ('00000000-0000-7000-8000-000000000154', '00000000-0000-7000-8000-000000000021', 'STAFF', sha256('token-4'::bytea), 'PASSWORD',
        now() + interval '15 minutes', now() + interval '12 hours');

-- @case staff_session_with_mfa_ok expect=ok
INSERT INTO app.sessions (id, user_id, kind, token_hash, auth_method, mfa_verified_at, idle_expires_at, absolute_expires_at)
VALUES ('00000000-0000-7000-8000-000000000155', '00000000-0000-7000-8000-000000000021', 'STAFF', sha256('token-5'::bytea), 'PASSWORD',
        now(), now() + interval '15 minutes', now() + interval '12 hours');

-- @case raw_token_never_fits_token_hash expect=error:ck_sessions_token_hash
INSERT INTO app.sessions (id, user_id, kind, token_hash, auth_method, idle_expires_at, absolute_expires_at)
VALUES ('00000000-0000-7000-8000-000000000156', '00000000-0000-7000-8000-000000000011', 'USER', convert_to('raw-session-token', 'UTF8'), 'PHONE_OTP',
        now() + interval '7 days', now() + interval '30 days');

-- @case session_revoke_ok expect=ok
UPDATE app.sessions SET revoked_at = now(), revoked_reason = 'LOGOUT' WHERE id = '00000000-0000-7000-8000-000000000151';

-- @case revoked_session_cannot_be_revived expect=error:revoked session
UPDATE app.sessions SET revoked_at = NULL, revoked_reason = NULL WHERE id = '00000000-0000-7000-8000-000000000151';

-- @case session_absolute_expiry_cannot_be_extended expect=error:is immutable
UPDATE app.sessions SET absolute_expires_at = absolute_expires_at + interval '30 days' WHERE id = '00000000-0000-7000-8000-000000000155';

-- ===================================================================================== RBAC
-- @case roles_and_least_privilege_seeded expect=ok
DO $$ BEGIN
  IF (SELECT count(*) FROM app.roles) <> 9 THEN RAISE EXCEPTION 'expected 9 staff roles (8 + BUSINESS_APPROVER)'; END IF;
  IF EXISTS (SELECT 1 FROM app.role_permissions rp JOIN app.roles r ON r.id = rp.role_id JOIN app.permissions p ON p.id = rp.permission_id
              WHERE r.code IN ('ADMIN', 'SUPER_ADMIN') AND p.code = 'kyc.status.view') THEN
    RAISE EXCEPTION 'ADMIN/SUPER_ADMIN must not see KYC status by default (baseline I-5)';
  END IF;
  IF EXISTS (SELECT 1 FROM app.role_permissions rp JOIN app.roles r ON r.id = rp.role_id JOIN app.permissions p ON p.id = rp.permission_id
              WHERE r.code = 'BUSINESS_APPROVER' AND p.code NOT LIKE '%.approve') THEN
    RAISE EXCEPTION 'BUSINESS_APPROVER holds approval-only permissions (baseline I-4)';
  END IF;
  IF EXISTS (SELECT 1 FROM app.role_permissions rp JOIN app.roles r ON r.id = rp.role_id JOIN app.permissions p ON p.id = rp.permission_id
              WHERE p.code = 'payout.approve' AND r.code <> 'FINANCE') THEN
    RAISE EXCEPTION 'payout.approve is FINANCE only (baseline I-20)';
  END IF;
  IF (SELECT count(*) FROM app.permissions WHERE code IN ('security_audit.read', 'recovery.write_off.approve', 'refund.bulk.request', 'refund.bulk.approve')) <> 4
     OR EXISTS (SELECT 1 FROM app.permissions WHERE code = 'ledger.write_off.approve') THEN
    RAISE EXCEPTION 'permission names must follow baseline I-23/I-24';
  END IF;
  IF (SELECT count(*) FROM app.organisation_roles) <> 2 THEN RAISE EXCEPTION 'MVP org roles are ORG_ADMIN and ORG_MEMBER (I-1)'; END IF;
  IF EXISTS (SELECT 1 FROM app.role_permissions rp JOIN app.roles r ON r.id = rp.role_id JOIN app.permissions p ON p.id = rp.permission_id
              WHERE r.code IN ('ADMIN', 'SUPER_ADMIN')
                AND p.code IN ('kyc.document.view', 'kyc.identity_number.reveal', 'ledger.adjustment.create', 'payout.approve')) THEN
    RAISE EXCEPTION 'ADMIN/SUPER_ADMIN must not hold sensitive permissions by default (SECURITY §5.2)';
  END IF;
  IF EXISTS (SELECT 1 FROM app.role_permissions rp JOIN app.roles r ON r.id = rp.role_id JOIN app.permissions p ON p.id = rp.permission_id
              WHERE r.code = 'SECURITY_ADMIN' AND (p.code LIKE 'kyc.%' OR p.code LIKE 'payout.%' OR p.code LIKE 'ledger.%' OR p.code LIKE 'refund.%')) THEN
    RAISE EXCEPTION 'SECURITY_ADMIN must have no KYC or financial permissions';
  END IF;
END $$;

-- @case role_request_self_request_rejected expect=error:ck_role_assignment_requests_no_self_request
INSERT INTO app.role_assignment_requests (id, action, target_user_id, role_id, requested_by, justification, expires_at)
VALUES ('00000000-0000-7000-8000-000000000201', 'GRANT', '00000000-0000-7000-8000-000000000022', md5('role:FINANCE')::uuid,
        '00000000-0000-7000-8000-000000000022', 'self grant attempt', now() + interval '24 hours');

-- @case role_request_for_personal_account_rejected expect=error:fk_role_assignment_requests_target
INSERT INTO app.role_assignment_requests (id, action, target_user_id, role_id, requested_by, justification, expires_at)
VALUES ('00000000-0000-7000-8000-000000000202', 'GRANT', '00000000-0000-7000-8000-000000000011', md5('role:SUPPORT')::uuid,
        '00000000-0000-7000-8000-000000000022', 'support role for a user', now() + interval '24 hours');

-- @case role_request_ok expect=ok
INSERT INTO app.role_assignment_requests (id, action, target_user_id, role_id, requested_by, justification, expires_at)
VALUES ('00000000-0000-7000-8000-000000000203', 'GRANT', '00000000-0000-7000-8000-000000000021', md5('role:FINANCE')::uuid,
        '00000000-0000-7000-8000-000000000022', 'joins finance operations team', now() + interval '24 hours');

-- @case role_request_self_approval_rejected expect=error:ck_role_assignment_requests_no_self_approval
UPDATE app.role_assignment_requests SET status = 'APPROVED', decided_by = '00000000-0000-7000-8000-000000000022',
       decided_at = now(), checker_step_up_at = now()
 WHERE id = '00000000-0000-7000-8000-000000000203';

-- @case role_request_approval_by_target_rejected expect=error:ck_role_assignment_requests_no_self_approval
UPDATE app.role_assignment_requests SET status = 'APPROVED', decided_by = '00000000-0000-7000-8000-000000000021',
       decided_at = now(), checker_step_up_at = now()
 WHERE id = '00000000-0000-7000-8000-000000000203';

-- @case pending_role_request_fixture expect=ok
INSERT INTO app.role_assignment_requests (id, action, target_user_id, role_id, requested_by, justification, expires_at)
VALUES ('00000000-0000-7000-8000-000000000204', 'GRANT', '00000000-0000-7000-8000-000000000024', md5('role:SUPPORT')::uuid,
        '00000000-0000-7000-8000-000000000022', 'support rota coverage', now() + interval '24 hours');

-- @case role_assignment_from_pending_request_rejected expect=error:requires an APPROVED GRANT request
INSERT INTO app.role_assignments (id, user_id, role_id, request_id, granted_by, approved_by)
VALUES ('00000000-0000-7000-8000-000000000305', '00000000-0000-7000-8000-000000000024', md5('role:SUPPORT')::uuid,
        '00000000-0000-7000-8000-000000000204', '00000000-0000-7000-8000-000000000022', '00000000-0000-7000-8000-000000000023');

-- @case role_request_approval_requires_step_up expect=error:ck_role_assignment_requests_approved
UPDATE app.role_assignment_requests SET status = 'APPROVED', decided_by = '00000000-0000-7000-8000-000000000023', decided_at = now()
 WHERE id = '00000000-0000-7000-8000-000000000203';

-- @case role_grant_maker_checker_ok expect=ok
UPDATE app.role_assignment_requests SET status = 'APPROVED', decided_by = '00000000-0000-7000-8000-000000000023',
       decided_at = now(), checker_step_up_at = now()
 WHERE id = '00000000-0000-7000-8000-000000000203';
INSERT INTO app.role_assignments (id, user_id, role_id, request_id, granted_by, approved_by)
VALUES ('00000000-0000-7000-8000-000000000301', '00000000-0000-7000-8000-000000000021', md5('role:FINANCE')::uuid,
        '00000000-0000-7000-8000-000000000203', '00000000-0000-7000-8000-000000000022', '00000000-0000-7000-8000-000000000023');

-- @case role_assignment_self_approved_rejected expect=error:ck_role_assignments_no_self_approval
INSERT INTO app.role_assignments (id, user_id, role_id, request_id, granted_by, approved_by)
VALUES ('00000000-0000-7000-8000-000000000302', '00000000-0000-7000-8000-000000000021', md5('role:FINANCE')::uuid,
        '00000000-0000-7000-8000-000000000203', '00000000-0000-7000-8000-000000000022', '00000000-0000-7000-8000-000000000021');

-- @case role_assignment_not_matching_request_rejected expect=error:fk_role_assignments_request
UPDATE app.role_assignment_requests SET status = 'APPROVED', decided_by = '00000000-0000-7000-8000-000000000023',
       decided_at = now(), checker_step_up_at = now()
 WHERE id = '00000000-0000-7000-8000-000000000204';
-- the request approved SUPPORT; the assignment tries to grant COMPLIANCE instead
INSERT INTO app.role_assignments (id, user_id, role_id, request_id, granted_by, approved_by)
VALUES ('00000000-0000-7000-8000-000000000303', '00000000-0000-7000-8000-000000000024', md5('role:COMPLIANCE')::uuid,
        '00000000-0000-7000-8000-000000000204', '00000000-0000-7000-8000-000000000022', '00000000-0000-7000-8000-000000000023');

-- @case sod_conflicting_role_rejected expect=error:separation of duties
INSERT INTO app.role_assignment_requests (id, action, target_user_id, role_id, requested_by, justification, expires_at)
VALUES ('00000000-0000-7000-8000-000000000205', 'GRANT', '00000000-0000-7000-8000-000000000021', md5('role:SECURITY_ADMIN')::uuid,
        '00000000-0000-7000-8000-000000000022', 'security cover during leave', now() + interval '24 hours');
UPDATE app.role_assignment_requests SET status = 'APPROVED', decided_by = '00000000-0000-7000-8000-000000000023',
       decided_at = now(), checker_step_up_at = now()
 WHERE id = '00000000-0000-7000-8000-000000000205';
INSERT INTO app.role_assignments (id, user_id, role_id, request_id, granted_by, approved_by)
VALUES ('00000000-0000-7000-8000-000000000304', '00000000-0000-7000-8000-000000000021', md5('role:SECURITY_ADMIN')::uuid,
        '00000000-0000-7000-8000-000000000205', '00000000-0000-7000-8000-000000000022', '00000000-0000-7000-8000-000000000023');

-- @case role_assignment_grantor_columns_immutable expect=error:only revoked_at, revoked_by, revoke_request_id, revoke_reason may change
UPDATE app.role_assignments SET approved_by = '00000000-0000-7000-8000-000000000024' WHERE id = '00000000-0000-7000-8000-000000000301';

-- @case decided_role_request_cannot_be_reopened expect=error:illegal role_assignment_request transition
UPDATE app.role_assignment_requests SET status = 'PENDING' WHERE id = '00000000-0000-7000-8000-000000000203';

-- @case role_permission_matrix_not_editable expect=error:append-only table
UPDATE app.role_permissions SET permission_id = md5('permission:kyc.document.view')::uuid
 WHERE role_id = md5('role:ADMIN')::uuid AND permission_id = md5('permission:content.moderate')::uuid;

-- @case security_events_append_only expect=error:append-only table
INSERT INTO app.security_events (id, user_id, event_type, occurred_at)
VALUES ('00000000-0000-7000-8000-000000000401', '00000000-0000-7000-8000-000000000011', 'LOGIN_SUCCEEDED', now());
UPDATE app.security_events SET event_type = 'LOGIN_FAILED' WHERE id = '00000000-0000-7000-8000-000000000401';

-- ===================================================================================== organisations
-- @case organisation_without_admin_rejected expect=error:must have at least one active ORG_ADMIN
INSERT INTO app.organisations (id, display_name, slug, org_type, created_by_user_id)
VALUES ('00000000-0000-7000-8000-000000000501', 'Harare Community Trust', 'harare-community-trust', 'TRUST', '00000000-0000-7000-8000-000000000011');

-- @case organisation_with_admin_ok expect=ok
INSERT INTO app.organisations (id, display_name, slug, org_type, created_by_user_id)
VALUES ('00000000-0000-7000-8000-000000000502', 'Mutare Borehole Association', 'mutare-borehole-association', 'COMMUNITY_BASED',
        '00000000-0000-7000-8000-000000000011');
INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id)
VALUES ('00000000-0000-7000-8000-000000000511', '00000000-0000-7000-8000-000000000502', '00000000-0000-7000-8000-000000000011',
        md5('org_role:ORG_ADMIN')::uuid);
INSERT INTO app.organisation_verifications (id, organisation_id) VALUES ('00000000-0000-7000-8000-000000000521', '00000000-0000-7000-8000-000000000502');

-- @case staff_account_cannot_be_org_member expect=error:fk_organisation_members_user
INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id)
VALUES ('00000000-0000-7000-8000-000000000512', '00000000-0000-7000-8000-000000000502', '00000000-0000-7000-8000-000000000021',
        md5('org_role:ORG_MEMBER')::uuid);

-- @case duplicate_active_membership_rejected expect=error:uq_organisation_members_active
INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id)
VALUES ('00000000-0000-7000-8000-000000000513', '00000000-0000-7000-8000-000000000502', '00000000-0000-7000-8000-000000000011',
        md5('org_role:ORG_MEMBER')::uuid);

-- @case removing_last_org_admin_rejected expect=error:must have at least one active ORG_ADMIN
UPDATE app.organisation_members SET status = 'REMOVED', removed_at = now() WHERE id = '00000000-0000-7000-8000-000000000511';

-- @case org_member_role_change_with_second_admin_ok expect=ok
INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id)
VALUES ('00000000-0000-7000-8000-000000000514', '00000000-0000-7000-8000-000000000502', '00000000-0000-7000-8000-000000000012',
        md5('org_role:ORG_ADMIN')::uuid);
UPDATE app.organisation_members SET organisation_role_id = md5('org_role:ORG_MEMBER')::uuid WHERE id = '00000000-0000-7000-8000-000000000511';

-- @case org_role_permissions_outside_catalogue_rejected expect=error:ck_organisation_roles_permissions
INSERT INTO app.organisation_roles (id, code, name, permissions)
VALUES ('00000000-0000-7000-8000-000000000531', 'ORG_SUPER', 'Super', ARRAY['org.view', 'payout.approve']);

-- @case invitation_with_two_contacts_rejected expect=error:ck_organisation_invitations_one_contact
INSERT INTO app.organisation_invitations (id, organisation_id, organisation_role_id, invited_email_normalized, invited_phone_e164,
                                          token_hash, invited_by_user_id, expires_at)
VALUES ('00000000-0000-7000-8000-000000000541', '00000000-0000-7000-8000-000000000502', md5('org_role:ORG_MEMBER')::uuid,
        'rudo@example.org', '+263773333333', sha256('invite-token'::bytea), '00000000-0000-7000-8000-000000000011', now() + interval '7 days');

-- @case kyb_projection_forward_ok expect=ok
UPDATE app.organisation_verifications SET kyb_level = 'ORG_REGISTERED_VERIFIED', source_event_id = '00000000-0000-7000-8000-000000000551',
       source_occurred_at = now() WHERE organisation_id = '00000000-0000-7000-8000-000000000502';

-- @case kyb_projection_stale_event_rejected expect=error:stale KYB projection update
UPDATE app.organisation_verifications SET kyb_level = 'ORG_UNVERIFIED', source_event_id = '00000000-0000-7000-8000-000000000552',
       source_occurred_at = now() - interval '1 hour' WHERE organisation_id = '00000000-0000-7000-8000-000000000502';

-- ===================================================================================== break-glass / conflicts
-- @case break_glass_over_one_hour_rejected expect=error:ck_break_glass_grants_window
INSERT INTO app.break_glass_grants (id, staff_user_id, permission_id, justification, incident_ref, requested_by, approved_by, starts_at, expires_at)
VALUES ('00000000-0000-7000-8000-000000000601', '00000000-0000-7000-8000-000000000023', md5('permission:session.revoke')::uuid,
        'mass session revocation during ATO incident', 'INC-2026-001', '00000000-0000-7000-8000-000000000023',
        '00000000-0000-7000-8000-000000000024', now(), now() + interval '2 hours');

-- @case break_glass_self_approval_rejected expect=error:ck_break_glass_grants_maker_checker
INSERT INTO app.break_glass_grants (id, staff_user_id, permission_id, justification, incident_ref, requested_by, approved_by, starts_at, expires_at)
VALUES ('00000000-0000-7000-8000-000000000602', '00000000-0000-7000-8000-000000000023', md5('permission:session.revoke')::uuid,
        'mass session revocation during ATO incident', 'INC-2026-001', '00000000-0000-7000-8000-000000000024',
        '00000000-0000-7000-8000-000000000023', now(), now() + interval '1 hour');

-- @case break_glass_never_grants_role_admin expect=error:break-glass never grants role.assign.approve
INSERT INTO app.break_glass_grants (id, staff_user_id, permission_id, justification, incident_ref, requested_by, approved_by, starts_at, expires_at)
VALUES ('00000000-0000-7000-8000-000000000603', '00000000-0000-7000-8000-000000000023', md5('permission:role.assign.approve')::uuid,
        'need to grant myself a role quickly', 'INC-2026-001', '00000000-0000-7000-8000-000000000023',
        '00000000-0000-7000-8000-000000000024', now(), now() + interval '30 minutes');

-- @case break_glass_ok_and_revoked_once expect=ok
INSERT INTO app.break_glass_grants (id, staff_user_id, permission_id, justification, incident_ref, requested_by, approved_by, starts_at, expires_at)
VALUES ('00000000-0000-7000-8000-000000000604', '00000000-0000-7000-8000-000000000023', md5('permission:session.revoke')::uuid,
        'mass session revocation during ATO incident', 'INC-2026-001', '00000000-0000-7000-8000-000000000023',
        '00000000-0000-7000-8000-000000000024', now(), now() + interval '1 hour');
UPDATE app.break_glass_grants SET revoked_at = now(), revoked_by = '00000000-0000-7000-8000-000000000024', revoke_reason = 'incident contained'
 WHERE id = '00000000-0000-7000-8000-000000000604';

-- @case break_glass_revocation_set_once expect=error:is set once
UPDATE app.break_glass_grants SET revoke_reason = 'rewritten' WHERE id = '00000000-0000-7000-8000-000000000604';

-- @case break_glass_window_immutable expect=error:only revoked_at, revoked_by, revoke_reason may change
UPDATE app.break_glass_grants SET expires_at = expires_at + interval '1 hour' WHERE id = '00000000-0000-7000-8000-000000000604';

-- @case conflict_declaration_ok expect=ok
INSERT INTO app.staff_conflict_declarations (id, staff_user_id, subject_type, subject_id, relationship, declared_at)
VALUES ('00000000-0000-7000-8000-000000000611', '00000000-0000-7000-8000-000000000021', 'user', '00000000-0000-7000-8000-000000000011', 'FAMILY', now());

-- @case duplicate_active_conflict_declaration_rejected expect=error:uq_staff_conflict_declarations_active
INSERT INTO app.staff_conflict_declarations (id, staff_user_id, subject_type, subject_id, relationship, declared_at)
VALUES ('00000000-0000-7000-8000-000000000612', '00000000-0000-7000-8000-000000000021', 'user', '00000000-0000-7000-8000-000000000011', 'FRIEND', now());

-- @case conflict_declaration_content_immutable expect=error:only withdrawn_at, withdrawn_reason may change
UPDATE app.staff_conflict_declarations SET relationship = 'OTHER' WHERE id = '00000000-0000-7000-8000-000000000611';

-- @case conflict_declaration_only_for_staff expect=error:fk_staff_conflict_declarations_staff
INSERT INTO app.staff_conflict_declarations (id, staff_user_id, subject_type, subject_id, relationship, declared_at)
VALUES ('00000000-0000-7000-8000-000000000613', '00000000-0000-7000-8000-000000000011', 'campaign', '00000000-0000-7000-8000-000000000099', 'OTHER', now());

-- @case staff_otp_identity_rejected expect=error:ck_authentication_identities_staff_no_otp
INSERT INTO app.user_phone_numbers (id, user_id, e164, verified_at)
VALUES ('00000000-0000-7000-8000-000000000621', '00000000-0000-7000-8000-000000000022', '+263779999999', now());
INSERT INTO app.authentication_identities (id, user_id, user_account_kind, identity_type, user_phone_number_id)
VALUES ('00000000-0000-7000-8000-000000000622', '00000000-0000-7000-8000-000000000022', 'STAFF', 'PHONE_OTP', '00000000-0000-7000-8000-000000000621');

-- @case staff_session_from_otp_rejected expect=error:ck_sessions_staff_first_factor
INSERT INTO app.sessions (id, user_id, kind, token_hash, auth_method, mfa_verified_at, idle_expires_at, absolute_expires_at)
VALUES ('00000000-0000-7000-8000-000000000623', '00000000-0000-7000-8000-000000000022', 'STAFF', sha256('token-staff-otp'::bytea), 'EMAIL_OTP',
        now(), now() + interval '15 minutes', now() + interval '12 hours');

-- @case staff_password_identity_ok expect=ok
INSERT INTO app.authentication_identities (id, user_id, user_account_kind, identity_type)
VALUES ('00000000-0000-7000-8000-000000000624', '00000000-0000-7000-8000-000000000022', 'STAFF', 'PASSWORD');

-- @case business_approver_conflicts_with_finance expect=error:separation of duties
INSERT INTO app.role_assignment_requests (id, action, target_user_id, role_id, requested_by, justification, expires_at)
VALUES ('00000000-0000-7000-8000-000000000625', 'GRANT', '00000000-0000-7000-8000-000000000021', md5('role:BUSINESS_APPROVER')::uuid,
        '00000000-0000-7000-8000-000000000022', 'approve fee schedule changes', now() + interval '24 hours');
UPDATE app.role_assignment_requests SET status = 'APPROVED', decided_by = '00000000-0000-7000-8000-000000000023',
       decided_at = now(), checker_step_up_at = now() WHERE id = '00000000-0000-7000-8000-000000000625';
INSERT INTO app.role_assignments (id, user_id, role_id, request_id, granted_by, approved_by)
VALUES ('00000000-0000-7000-8000-000000000626', '00000000-0000-7000-8000-000000000021', md5('role:BUSINESS_APPROVER')::uuid,
        '00000000-0000-7000-8000-000000000625', '00000000-0000-7000-8000-000000000022', '00000000-0000-7000-8000-000000000023');
