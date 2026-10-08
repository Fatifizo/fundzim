-- FundZim migration: RBAC, security events, break-glass grants, staff conflict declarations (identity, Stage 4; two reviewers)
-- Derived from the Stage 2 design draft design/sql/0004_users_auth.sql (validated in design/sql/validate), adapted for Stage 4
-- (ADR-032). Runs as fundzim_migrator via `fundzimctl migrate up`. Forward-only in production (ADR-028); the
-- Down section exists for local development only.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';
-- -----------------------------------------------------------------------------------------------------
-- RBAC: roles, permissions, role_permissions (reference data, changed only by reviewed migrations).
-- roles.conflicting_role_codes encodes the role-level ("R") separation-of-duties conflicts of
-- operational-controls §2; role_assignments enforces them. Classification: C1.
-- Seed ids are md5-derived (deterministic across environments).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.roles (
  id                     uuid        NOT NULL,
  code                   text        NOT NULL,
  name                   text        NOT NULL,
  description            text        NOT NULL,
  conflicting_role_codes text[]      NOT NULL DEFAULT '{}',
  requires_mfa           boolean     NOT NULL DEFAULT true,
  created_at             timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_roles PRIMARY KEY (id),
  CONSTRAINT uq_roles_code UNIQUE (code),
  CONSTRAINT ck_roles_code CHECK (code ~ '^[A-Z][A-Z_]*$'),
  CONSTRAINT ck_roles_not_self_conflicting CHECK (NOT (code = ANY (conflicting_role_codes)))
);
CREATE TRIGGER trg_roles_no_delete BEFORE DELETE ON app.roles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_roles_immutable_cols BEFORE UPDATE ON app.roles
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'code', 'created_at');

CREATE TABLE app.permissions (
  id                 uuid        NOT NULL,
  code               text        NOT NULL,
  description        text        NOT NULL,
  is_sensitive       boolean     NOT NULL DEFAULT false,   -- requires justification + audit on use
  requires_step_up   boolean     NOT NULL DEFAULT false,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_permissions PRIMARY KEY (id),
  CONSTRAINT uq_permissions_code UNIQUE (code),
  CONSTRAINT ck_permissions_code CHECK (code ~ '^[a-z_]+(\.[a-z_]+)+$')
);
CREATE TRIGGER trg_permissions_no_delete BEFORE DELETE ON app.permissions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_permissions_immutable_cols BEFORE UPDATE ON app.permissions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_column_change('id', 'code', 'created_at');

CREATE TABLE app.role_permissions (
  id            uuid        NOT NULL,
  role_id       uuid        NOT NULL,
  permission_id uuid        NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_role_permissions PRIMARY KEY (id),
  CONSTRAINT fk_role_permissions_role_id FOREIGN KEY (role_id) REFERENCES app.roles (id),
  CONSTRAINT fk_role_permissions_permission_id FOREIGN KEY (permission_id) REFERENCES app.permissions (id),
  CONSTRAINT uq_role_permissions_role_permission UNIQUE (role_id, permission_id)
);
CREATE INDEX ix_role_permissions_permission_id ON app.role_permissions (permission_id);
CREATE TRIGGER trg_role_permissions_no_update BEFORE UPDATE ON app.role_permissions
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.roles (id, code, name, description, conflicting_role_codes) VALUES
  (md5('role:REVIEWER')::uuid,       'REVIEWER',       'Campaign reviewer',      'Trust & safety campaign review',               '{SECURITY_ADMIN}'),
  (md5('role:KYC_REVIEWER')::uuid,   'KYC_REVIEWER',   'KYC reviewer',           'Identity, KYB and beneficiary verification',   '{SECURITY_ADMIN,SUPER_ADMIN}'),
  (md5('role:SUPPORT')::uuid,        'SUPPORT',        'Support agent',          'Customer support with masked PII',             '{SECURITY_ADMIN}'),
  (md5('role:COMPLIANCE')::uuid,     'COMPLIANCE',     'Compliance officer',     'AML/CFT, cases, freezes, holds',               '{SECURITY_ADMIN,SUPER_ADMIN,BUSINESS_APPROVER}'),
  (md5('role:FINANCE')::uuid,        'FINANCE',        'Finance operator',       'Payout/refund approval, reconciliation, ledger adjustments (dual control)', '{SECURITY_ADMIN,SUPER_ADMIN,BUSINESS_APPROVER}'),
  (md5('role:ADMIN')::uuid,          'ADMIN',          'Platform administrator', 'Configuration requests, content moderation',   '{}'),
  (md5('role:SECURITY_ADMIN')::uuid, 'SECURITY_ADMIN', 'Security administrator', 'Access reviews, key rotation, incident tooling', '{REVIEWER,KYC_REVIEWER,SUPPORT,COMPLIANCE,FINANCE,BUSINESS_APPROVER}'),
  (md5('role:BUSINESS_APPROVER')::uuid, 'BUSINESS_APPROVER', 'Business approver', 'Approval-only: fee changes, write-offs above threshold, review-policy and business-owned limit changes (baseline I-4)', '{FINANCE,COMPLIANCE,SECURITY_ADMIN}'),
  (md5('role:SUPER_ADMIN')::uuid,    'SUPER_ADMIN',    'Role administrator',     'Role grants (maker-checker); no sensitive permissions by default', '{KYC_REVIEWER,COMPLIANCE,FINANCE}');

-- Representative permission set (SECURITY §5, operational-controls §1, baseline §12). The full matrix is Stage 4/14.
-- ADMIN and SUPER_ADMIN do not hold kyc.status.view (baseline I-5, identity-data-protection.md).
INSERT INTO app.permissions (id, code, description, is_sensitive, requires_step_up)
SELECT md5('permission:' || p.code)::uuid, p.code, p.description, p.sensitive, p.step_up
FROM (VALUES
  ('campaign.view',                        'View campaigns and the review queue',                false, false),
  ('campaign.review',                      'Claim and work campaign reviews',                    false, false),
  ('campaign.decide',                      'Approve, reject or request changes',                 true,  false),
  ('campaign.suspend',                     'Suspend a campaign',                                 true,  true),
  ('campaign.unsuspend',                   'Unsuspend a campaign',                               true,  true),
  ('campaign.freeze',                      'Freeze a campaign and its payouts',                  true,  true),
  ('campaign.unfreeze.request',            'Request unfreeze (maker)',                           true,  true),
  ('campaign.unfreeze.approve',            'Approve unfreeze (checker)',                         true,  true),
  ('content.moderate',                     'Moderate public content',                            false, false),
  ('case.create',                          'Open a compliance/risk case',                        false, false),
  ('case.manage',                          'Manage compliance cases',                            true,  false),
  ('kyc.status.view',                      'View KYC level and status',                          false, false),
  ('kyc.case.review',                      'Review KYC/KYB cases',                               true,  false),
  ('kyc.document.view',                    'View KYC documents (justified, case-bound)',         true,  true),
  ('kyc.identity_number.reveal',           'Reveal a full identity number (justified)',          true,  true),
  ('kyc.decision.record',                  'Record a KYC decision',                              true,  true),
  ('beneficiary.verification.decide',      'Decide beneficiary verification',                    true,  true),
  ('org.verification.decide',              'Decide organisation verification',                   true,  true),
  ('payout.hold',                          'Place or release a payout hold',                     true,  true),
  ('payout.approve',                       'Approve a payout (checker)',                         true,  true),
  ('payout.reject',                        'Reject a payout',                                    true,  true),
  ('payout.destination.override.approve',  'Approve a staff payout-destination override',        true,  true),
  ('refund.request',                       'Request a refund (maker)',                           true,  false),
  ('refund.approve',                       'Approve a refund (checker)',                         true,  true),
  ('ledger.adjustment.create',             'Create a ledger adjustment (maker)',                 true,  true),
  ('ledger.adjustment.approve',            'Approve a ledger adjustment (checker)',              true,  true),
  ('reconciliation.run',                   'Run reconciliation',                                 false, false),
  ('reconciliation.resolve',               'Resolve reconciliation discrepancies',               true,  true),
  ('fee.config.request',                   'Request a fee configuration change',                 true,  true),
  ('compliance.override.request',          'Request a compliance override (maker)',              true,  true),
  ('compliance.override.approve',          'Approve a compliance override (checker)',            true,  true),
  ('str.prepare',                          'Prepare suspicious-transaction reports (LR-008)',    true,  true),
  ('evidence.view',                        'View evidence records',                              true,  false),
  ('evidence.hold',                        'Place/release evidence legal holds',                 true,  true),
  ('donor.identity.unmask',                'Unmask an anonymous donor (justified)',              true,  true),
  ('user.view',                            'View user accounts (masked PII)',                    false, false),
  ('account.recovery.assist',              'Assist account recovery (step-up gated)',            true,  true),
  ('audit.read',                           'Read the audit log (scoped)',                        true,  false),
  ('security_audit.read',                  'Read security audit events',                         true,  false),
  ('access.review.run',                    'Run access reviews',                                 false, false),
  ('session.revoke',                       'Revoke staff or user sessions',                      true,  true),
  ('staff.suspend',                        'Suspend a staff account',                            true,  true),
  ('killswitch.activate',                  'Activate a kill switch (security scopes)',           true,  true),
  ('secret.rotation.trigger',              'Trigger secret/key rotation',                        true,  true),
  ('config.change.request',                'Request a configuration change',                     true,  false),
  ('role.assign.request',                  'Request a role grant or revocation (maker)',         true,  true),
  ('role.assign.approve',                  'Approve a role grant or revocation (checker)',       true,  true),
  ('fee.config.approve',                   'Approve a fee schedule change (checker)',            true,  true),
  ('recovery.write_off.approve',           'Approve a write-off above threshold (checker)',      true,  true),
  ('refund.bulk.request',                  'Request a bulk refund batch (maker, I-24)',          true,  true),
  ('refund.bulk.approve',                  'Approve a bulk refund batch above the limit (I-24)', true,  true),
  ('campaign.review_policy.request',       'Propose a campaign review policy version (maker)',   true,  true),
  ('campaign.review_policy.approve',       'Approve a campaign review policy version (checker)', true,  true),
  ('limit.change.request',                 'Request a limit/threshold change (maker)',           true,  true),
  ('limit.change.approve',                 'Approve a business-owned limit change (checker)',    true,  true),
  ('account.suspend',                      'Suspend a personal (USER) account (Stage 4)',        true,  true),
  ('account.reactivate',                   'Reactivate a suspended personal account (Stage 4)',  true,  true),
  ('staff.invite',                         'Invite a new staff account (Stage 4)',               true,  true)
) AS p(code, description, sensitive, step_up);

INSERT INTO app.role_permissions (id, role_id, permission_id)
SELECT md5('role_permission:' || rp.role_code || ':' || rp.perm_code)::uuid,
       md5('role:' || rp.role_code)::uuid, md5('permission:' || rp.perm_code)::uuid
FROM (VALUES
  ('REVIEWER', 'campaign.view'), ('REVIEWER', 'campaign.review'), ('REVIEWER', 'campaign.decide'),
  ('REVIEWER', 'campaign.suspend'), ('REVIEWER', 'campaign.unsuspend'), ('REVIEWER', 'case.create'),
  ('REVIEWER', 'kyc.status.view'),
  ('KYC_REVIEWER', 'kyc.status.view'), ('KYC_REVIEWER', 'kyc.case.review'), ('KYC_REVIEWER', 'kyc.document.view'),
  ('KYC_REVIEWER', 'kyc.decision.record'), ('KYC_REVIEWER', 'beneficiary.verification.decide'),
  ('KYC_REVIEWER', 'org.verification.decide'),
  ('SUPPORT', 'campaign.view'), ('SUPPORT', 'user.view'), ('SUPPORT', 'refund.request'), ('SUPPORT', 'case.create'),
  ('SUPPORT', 'account.recovery.assist'), ('SUPPORT', 'kyc.status.view'),
  ('COMPLIANCE', 'campaign.view'), ('COMPLIANCE', 'campaign.decide'), ('COMPLIANCE', 'campaign.suspend'),
  ('COMPLIANCE', 'campaign.unsuspend'), ('COMPLIANCE', 'campaign.freeze'), ('COMPLIANCE', 'campaign.unfreeze.request'),
  ('COMPLIANCE', 'campaign.unfreeze.approve'), ('COMPLIANCE', 'case.create'), ('COMPLIANCE', 'case.manage'),
  ('COMPLIANCE', 'payout.hold'), ('COMPLIANCE', 'kyc.status.view'), ('COMPLIANCE', 'kyc.document.view'),
  ('COMPLIANCE', 'kyc.identity_number.reveal'), ('COMPLIANCE', 'kyc.decision.record'),
  ('COMPLIANCE', 'compliance.override.request'), ('COMPLIANCE', 'compliance.override.approve'),
  ('COMPLIANCE', 'str.prepare'), ('COMPLIANCE', 'evidence.view'), ('COMPLIANCE', 'evidence.hold'),
  ('COMPLIANCE', 'refund.request'), ('COMPLIANCE', 'refund.bulk.request'), ('COMPLIANCE', 'audit.read'), ('COMPLIANCE', 'donor.identity.unmask'),
  ('FINANCE', 'campaign.view'), ('FINANCE', 'campaign.freeze'), ('FINANCE', 'kyc.status.view'),
  ('FINANCE', 'payout.approve'), ('FINANCE', 'payout.reject'), ('FINANCE', 'payout.destination.override.approve'),
  ('FINANCE', 'refund.approve'), ('FINANCE', 'ledger.adjustment.create'), ('FINANCE', 'ledger.adjustment.approve'),
  ('FINANCE', 'reconciliation.run'), ('FINANCE', 'reconciliation.resolve'), ('FINANCE', 'fee.config.request'),
  ('FINANCE', 'audit.read'),
  ('ADMIN', 'campaign.view'), ('ADMIN', 'content.moderate'), ('ADMIN', 'config.change.request'),
  ('SECURITY_ADMIN', 'access.review.run'), ('SECURITY_ADMIN', 'session.revoke'), ('SECURITY_ADMIN', 'staff.suspend'),
  ('SECURITY_ADMIN', 'killswitch.activate'), ('SECURITY_ADMIN', 'secret.rotation.trigger'),
  ('SECURITY_ADMIN', 'security_audit.read'),
  ('SUPER_ADMIN', 'role.assign.request'), ('SUPER_ADMIN', 'role.assign.approve'), ('SUPER_ADMIN', 'audit.read'),
  ('SUPER_ADMIN', 'security_audit.read'), ('SUPER_ADMIN', 'campaign.view'),
  ('BUSINESS_APPROVER', 'fee.config.approve'), ('BUSINESS_APPROVER', 'recovery.write_off.approve'), ('BUSINESS_APPROVER', 'refund.bulk.approve'),
  ('BUSINESS_APPROVER', 'campaign.review_policy.approve'), ('BUSINESS_APPROVER', 'limit.change.approve'),
  ('COMPLIANCE', 'account.suspend'), ('COMPLIANCE', 'account.reactivate'), ('SUPER_ADMIN', 'staff.invite'), ('SECURITY_ADMIN', 'staff.invite'),
  ('COMPLIANCE', 'campaign.review_policy.request'), ('COMPLIANCE', 'limit.change.request'), ('FINANCE', 'limit.change.request')
) AS rp(role_code, perm_code);
-- Note: campaign.unfreeze.approve is held by COMPLIANCE, but the requester can never approve their own
-- request (campaign_status_history CHECK in 0014 and the service SoD check).

-- -----------------------------------------------------------------------------------------------------
-- app.role_assignment_requests — maker-checker for role grants and revocations (SECURITY §5.2,
-- operational-controls §3: 24 h expiry). Classification: C1. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.role_assignment_requests (
  id                 uuid        NOT NULL,
  action             text        NOT NULL,
  target_user_id     uuid        NOT NULL,
  target_account_kind text       NOT NULL DEFAULT 'STAFF',
  role_id            uuid        NOT NULL,
  requested_by       uuid        NOT NULL,
  justification      text        NOT NULL,
  status             text        NOT NULL DEFAULT 'PENDING',
  decided_by         uuid,
  decided_at         timestamptz,
  decision_reason    text,
  checker_step_up_at timestamptz,                          -- approver's fresh MFA (step-up) time
  expires_at         timestamptz NOT NULL,
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_role_assignment_requests PRIMARY KEY (id),
  CONSTRAINT uq_role_assignment_requests_approval UNIQUE (id, target_user_id, role_id, requested_by, decided_by),
  CONSTRAINT fk_role_assignment_requests_target FOREIGN KEY (target_user_id, target_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_role_assignment_requests_role_id FOREIGN KEY (role_id) REFERENCES app.roles (id),
  CONSTRAINT fk_role_assignment_requests_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id),
  CONSTRAINT fk_role_assignment_requests_decided_by FOREIGN KEY (decided_by) REFERENCES app.users (id),
  CONSTRAINT ck_role_assignment_requests_target_kind CHECK (target_account_kind = 'STAFF'),
  CONSTRAINT ck_role_assignment_requests_action CHECK (action IN ('GRANT', 'REVOKE')),
  CONSTRAINT ck_role_assignment_requests_status CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'EXPIRED', 'WITHDRAWN')),
  CONSTRAINT ck_role_assignment_requests_no_self_request CHECK (requested_by <> target_user_id),
  CONSTRAINT ck_role_assignment_requests_no_self_approval CHECK (decided_by IS NULL OR (decided_by <> requested_by AND decided_by <> target_user_id)),
  CONSTRAINT ck_role_assignment_requests_decided CHECK (
    (status IN ('APPROVED', 'REJECTED')) = (decided_by IS NOT NULL AND decided_at IS NOT NULL)),
  CONSTRAINT ck_role_assignment_requests_approved CHECK (
    status <> 'APPROVED' OR (checker_step_up_at IS NOT NULL AND decided_at <= expires_at)),
  CONSTRAINT ck_role_assignment_requests_justification CHECK (length(justification) >= 10),
  CONSTRAINT ck_role_assignment_requests_expiry CHECK (expires_at > created_at)
);
CREATE UNIQUE INDEX uq_role_assignment_requests_pending ON app.role_assignment_requests (target_user_id, role_id, action)
  WHERE status = 'PENDING';
CREATE TRIGGER trg_role_assignment_requests_guard_status BEFORE INSERT OR UPDATE OF status ON app.role_assignment_requests
  FOR EACH ROW EXECUTE FUNCTION app.guard_transition('role_assignment_request');
CREATE TRIGGER trg_role_assignment_requests_set_updated_at BEFORE UPDATE ON app.role_assignment_requests
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_role_assignment_requests_cols BEFORE UPDATE ON app.role_assignment_requests
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('status', 'decided_by', 'decided_at', 'decision_reason', 'checker_step_up_at');
CREATE TRIGGER trg_role_assignment_requests_no_delete BEFORE DELETE ON app.role_assignment_requests
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.status_transitions (machine, from_status, to_status) VALUES
  ('role_assignment_request', '',        'PENDING'),
  ('role_assignment_request', 'PENDING', 'APPROVED'),
  ('role_assignment_request', 'PENDING', 'REJECTED'),
  ('role_assignment_request', 'PENDING', 'EXPIRED'),
  ('role_assignment_request', 'PENDING', 'WITHDRAWN');

-- -----------------------------------------------------------------------------------------------------
-- app.role_assignments — active staff roles. Created only from an APPROVED GRANT request whose maker and
-- checker are copied here (composite FK); revocation sets revoked_* (REVOKE request). Staff accounts only.
-- Role-level SoD conflicts (roles.conflicting_role_codes) are rejected. Classification: C1. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.role_assignments (
  id                   uuid        NOT NULL,
  user_id              uuid        NOT NULL,
  user_account_kind    text        NOT NULL DEFAULT 'STAFF',
  role_id              uuid        NOT NULL,
  request_id           uuid        NOT NULL,
  granted_by           uuid        NOT NULL,                -- maker
  approved_by          uuid        NOT NULL,                -- checker
  valid_from           timestamptz NOT NULL DEFAULT now(),
  expires_at           timestamptz,                         -- optional time-boxed grant
  revoked_at           timestamptz,
  revoked_by           uuid,
  revoke_request_id    uuid,
  revoke_reason        text,
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_role_assignments PRIMARY KEY (id),
  CONSTRAINT uq_role_assignments_request_id UNIQUE (request_id),
  CONSTRAINT fk_role_assignments_user FOREIGN KEY (user_id, user_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_role_assignments_role_id FOREIGN KEY (role_id) REFERENCES app.roles (id),
  CONSTRAINT fk_role_assignments_request FOREIGN KEY (request_id, user_id, role_id, granted_by, approved_by)
    REFERENCES app.role_assignment_requests (id, target_user_id, role_id, requested_by, decided_by),
  CONSTRAINT fk_role_assignments_revoked_by FOREIGN KEY (revoked_by) REFERENCES app.users (id),
  CONSTRAINT fk_role_assignments_revoke_request_id FOREIGN KEY (revoke_request_id) REFERENCES app.role_assignment_requests (id),
  CONSTRAINT ck_role_assignments_user_kind CHECK (user_account_kind = 'STAFF'),
  CONSTRAINT ck_role_assignments_no_self_grant CHECK (granted_by <> user_id),
  CONSTRAINT ck_role_assignments_no_self_approval CHECK (approved_by <> user_id AND approved_by <> granted_by),
  CONSTRAINT ck_role_assignments_revoked CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)),
  CONSTRAINT ck_role_assignments_expiry CHECK (expires_at IS NULL OR expires_at > valid_from)
);
CREATE UNIQUE INDEX uq_role_assignments_active ON app.role_assignments (user_id, role_id) WHERE revoked_at IS NULL;
CREATE INDEX ix_role_assignments_role_id ON app.role_assignments (role_id) WHERE revoked_at IS NULL;
CREATE TRIGGER trg_role_assignments_set_updated_at BEFORE UPDATE ON app.role_assignments
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();
CREATE TRIGGER trg_role_assignments_cols BEFORE UPDATE ON app.role_assignments
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('revoked_at', 'revoked_by', 'revoke_request_id', 'revoke_reason');
CREATE TRIGGER trg_role_assignments_no_delete BEFORE DELETE ON app.role_assignments
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CREATE FUNCTION app.role_assignments_check_grant() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  r app.role_assignment_requests%ROWTYPE;
  v_code text;
  v_conflict text;
BEGIN
  SELECT * INTO r FROM app.role_assignment_requests WHERE id = NEW.request_id;
  IF r.status <> 'APPROVED' OR r.action <> 'GRANT' THEN
    RAISE EXCEPTION 'role assignment requires an APPROVED GRANT request (got % %)', r.action, r.status
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  SELECT code INTO v_code FROM app.roles WHERE id = NEW.role_id;
  SELECT ro.code INTO v_conflict
    FROM app.role_assignments ra JOIN app.roles ro ON ro.id = ra.role_id
   WHERE ra.user_id = NEW.user_id AND ra.revoked_at IS NULL
     AND (ro.code = ANY ((SELECT conflicting_role_codes FROM app.roles WHERE id = NEW.role_id)::text[])
          OR v_code = ANY (ro.conflicting_role_codes))
   LIMIT 1;
  IF v_conflict IS NOT NULL THEN
    RAISE EXCEPTION 'separation of duties: role % conflicts with active role %', v_code, v_conflict
      USING ERRCODE = 'integrity_constraint_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_role_assignments_check_grant BEFORE INSERT ON app.role_assignments
  FOR EACH ROW EXECUTE FUNCTION app.role_assignments_check_grant();

-- -----------------------------------------------------------------------------------------------------
-- app.security_events — authentication telemetry (high volume, append-only, shorter retention). Not the
-- audit log: security-relevant *decisions* go to audit.security_audit_events. Retention by dropping
-- monthly partitions (Stage 17/18), never by row DELETE. Classification: C2 (IP, user agent).
-- Retention: OPERATIONAL (period pending LR-012).
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.security_events (
  id               uuid        NOT NULL,
  user_id          uuid,                                   -- NULL when the account is unknown
  session_id       uuid,                                   -- no FK: sessions are purged
  event_type       text        NOT NULL,
  destination_hmac bytea,                                  -- for OTP/rate-limit events on unknown accounts
  ip               inet,
  user_agent       text,
  details          jsonb       NOT NULL DEFAULT '{}',      -- reason codes, counters; never secrets
  occurred_at      timestamptz NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_security_events PRIMARY KEY (id),
  CONSTRAINT fk_security_events_user_id FOREIGN KEY (user_id) REFERENCES app.users (id),
  CONSTRAINT ck_security_events_event_type CHECK (event_type IN (
    'LOGIN_SUCCEEDED', 'LOGIN_FAILED', 'OTP_SENT', 'OTP_VERIFY_FAILED', 'OTP_MAX_ATTEMPTS', 'OTP_RESEND_LIMITED',
    'SESSION_CREATED', 'SESSION_ROTATED', 'SESSION_REVOKED', 'SESSION_EXPIRED', 'STEP_UP_SUCCEEDED', 'STEP_UP_FAILED',
    'MFA_ENROLLED', 'MFA_REMOVED', 'MFA_FAILED', 'RECOVERY_CODE_USED', 'PASSWORD_CHANGED', 'PASSWORD_FAILED',
    'RATE_LIMITED', 'SUSPICIOUS_LOGIN', 'CONTACT_CHANGED', 'REGISTRATION_ATTEMPT', 'PASSWORD_RESET_REQUESTED',
    'EMAIL_VERIFICATION_FAILED', 'PASSWORD_RESET_FAILED', 'LOGIN_BLOCKED')),
  CONSTRAINT ck_security_events_details_object CHECK (jsonb_typeof(details) = 'object'),
  CONSTRAINT ck_security_events_user_agent CHECK (user_agent IS NULL OR length(user_agent) <= 512)
);
CREATE INDEX ix_security_events_user_occurred ON app.security_events (user_id, occurred_at) WHERE user_id IS NOT NULL;
CREATE INDEX ix_security_events_type_occurred ON app.security_events (event_type, occurred_at);
CREATE TRIGGER trg_security_events_no_mutation BEFORE UPDATE OR DELETE ON app.security_events
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_security_events_no_truncate BEFORE TRUNCATE ON app.security_events
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();

-- -----------------------------------------------------------------------------------------------------
-- app.break_glass_grants — time-boxed emergency grant of ONE named permission to a staff account
-- (SECURITY §5.5, operational-controls §5). Approver distinct from requester and grantee. The 1-hour bound
-- is the default design bound (SECURITY §5.5 "default <= 1 hour"); a different bound is a reviewed
-- migration, not a runtime setting. Never grants role administration or ledger write permissions.
-- Append-only except revocation (set once). Every use is tagged break_glass in audit events.
-- Classification: C1. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.break_glass_grants (
  id                 uuid        NOT NULL,
  staff_user_id      uuid        NOT NULL,
  staff_account_kind text        NOT NULL DEFAULT 'STAFF',
  permission_id      uuid        NOT NULL,
  justification      text        NOT NULL,
  incident_ref       text        NOT NULL,                -- incident / ticket reference
  requested_by       uuid        NOT NULL,                -- may be the grantee (on-call self-invocation)
  approved_by        uuid        NOT NULL,
  starts_at          timestamptz NOT NULL,
  expires_at         timestamptz NOT NULL,
  revoked_at         timestamptz,
  revoked_by         uuid,
  revoke_reason      text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_break_glass_grants PRIMARY KEY (id),
  CONSTRAINT fk_break_glass_grants_staff FOREIGN KEY (staff_user_id, staff_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT fk_break_glass_grants_permission_id FOREIGN KEY (permission_id) REFERENCES app.permissions (id),
  CONSTRAINT fk_break_glass_grants_requested_by FOREIGN KEY (requested_by) REFERENCES app.users (id),
  CONSTRAINT fk_break_glass_grants_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id),
  CONSTRAINT fk_break_glass_grants_revoked_by FOREIGN KEY (revoked_by) REFERENCES app.users (id),
  CONSTRAINT ck_break_glass_grants_staff_kind CHECK (staff_account_kind = 'STAFF'),
  CONSTRAINT ck_break_glass_grants_maker_checker CHECK (approved_by <> requested_by AND approved_by <> staff_user_id),
  CONSTRAINT ck_break_glass_grants_window CHECK (expires_at > starts_at AND expires_at - starts_at <= interval '1 hour'),
  CONSTRAINT ck_break_glass_grants_justification CHECK (length(justification) >= 10 AND length(incident_ref) >= 3),
  CONSTRAINT ck_break_glass_grants_revoked CHECK ((revoked_at IS NULL) = (revoked_by IS NULL)
                                                 AND (revoked_at IS NULL) = (revoke_reason IS NULL))
);
CREATE INDEX ix_break_glass_grants_active ON app.break_glass_grants (staff_user_id, expires_at) WHERE revoked_at IS NULL;
CREATE TRIGGER trg_break_glass_grants_cols BEFORE UPDATE ON app.break_glass_grants
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('revoked_at', 'revoked_by', 'revoke_reason');
CREATE TRIGGER trg_break_glass_grants_set_once BEFORE UPDATE ON app.break_glass_grants
  FOR EACH ROW EXECUTE FUNCTION app.set_once_columns('revoked_at', 'revoked_by', 'revoke_reason');
CREATE TRIGGER trg_break_glass_grants_no_delete BEFORE DELETE ON app.break_glass_grants
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();
CREATE TRIGGER trg_break_glass_grants_no_truncate BEFORE TRUNCATE ON app.break_glass_grants
  FOR EACH STATEMENT EXECUTE FUNCTION app.forbid_mutation();
CREATE FUNCTION app.break_glass_grants_check_permission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  v_code text;
BEGIN
  SELECT code INTO v_code FROM app.permissions WHERE id = NEW.permission_id;
  IF v_code LIKE 'role.%' OR v_code LIKE 'ledger.adjustment.%' THEN
    RAISE EXCEPTION 'break-glass never grants %', v_code USING ERRCODE = 'check_violation';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER trg_break_glass_grants_check_permission BEFORE INSERT ON app.break_glass_grants
  FOR EACH ROW EXECUTE FUNCTION app.break_glass_grants_check_permission();

-- -----------------------------------------------------------------------------------------------------
-- app.staff_conflict_declarations — staff-declared relationships to subjects (operational-controls §2):
-- object-level SoD checks refuse actions on declared subjects. Append-only; withdrawal is set once.
-- Classification: C2. Retention: AUDIT.
-- -----------------------------------------------------------------------------------------------------
CREATE TABLE app.staff_conflict_declarations (
  id                 uuid        NOT NULL,
  staff_user_id      uuid        NOT NULL,
  staff_account_kind text        NOT NULL DEFAULT 'STAFF',
  subject_type       text        NOT NULL,
  subject_id         uuid        NOT NULL,
  relationship       text        NOT NULL,
  details            text,
  declared_at        timestamptz NOT NULL,
  withdrawn_at       timestamptz,
  withdrawn_reason   text,
  created_at         timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT pk_staff_conflict_declarations PRIMARY KEY (id),
  CONSTRAINT fk_staff_conflict_declarations_staff FOREIGN KEY (staff_user_id, staff_account_kind) REFERENCES app.users (id, account_kind),
  CONSTRAINT ck_staff_conflict_declarations_staff_kind CHECK (staff_account_kind = 'STAFF'),
  CONSTRAINT ck_staff_conflict_declarations_subject_type CHECK (subject_type IN ('user', 'organisation', 'campaign', 'beneficiary',
                                                                                 'institution_payee', 'payout_destination')),
  CONSTRAINT ck_staff_conflict_declarations_relationship CHECK (relationship IN ('FAMILY', 'FRIEND', 'BUSINESS', 'EMPLOYER',
                                                                                 'DONOR', 'BENEFICIARY', 'OTHER')),
  CONSTRAINT ck_staff_conflict_declarations_withdrawn CHECK ((withdrawn_at IS NULL) = (withdrawn_reason IS NULL))
);
CREATE UNIQUE INDEX uq_staff_conflict_declarations_active ON app.staff_conflict_declarations (staff_user_id, subject_type, subject_id)
  WHERE withdrawn_at IS NULL;
CREATE INDEX ix_staff_conflict_declarations_subject ON app.staff_conflict_declarations (subject_type, subject_id) WHERE withdrawn_at IS NULL;
CREATE TRIGGER trg_staff_conflict_declarations_cols BEFORE UPDATE ON app.staff_conflict_declarations
  FOR EACH ROW EXECUTE FUNCTION app.allow_only_column_changes('withdrawn_at', 'withdrawn_reason');
CREATE TRIGGER trg_staff_conflict_declarations_set_once BEFORE UPDATE ON app.staff_conflict_declarations
  FOR EACH ROW EXECUTE FUNCTION app.set_once_columns('withdrawn_at', 'withdrawn_reason');
CREATE TRIGGER trg_staff_conflict_declarations_no_delete BEFORE DELETE ON app.staff_conflict_declarations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.staff_conflict_declarations, app.break_glass_grants, app.security_events, app.role_assignments,
  app.role_assignment_requests, app.role_permissions, app.permissions, app.roles;
DROP FUNCTION IF EXISTS app.break_glass_grants_check_permission(), app.role_assignments_check_grant();
-- status_transitions is append-only; the migrator (table owner) lifts the guard only for this development rollback.
ALTER TABLE app.status_transitions DISABLE TRIGGER trg_status_transitions_no_mutation;
DELETE FROM app.status_transitions WHERE machine = 'role_assignment_request';
ALTER TABLE app.status_transitions ENABLE TRIGGER trg_status_transitions_no_mutation;
-- +goose StatementEnd
