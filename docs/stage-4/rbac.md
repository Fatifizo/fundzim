# Stage 4 Role-Based Access Control, Staff Administration and Organisations

Code: `internal/platform/httpx/router.go` (policies), `internal/auth/middleware.go` (`Authorize`),
`internal/auth/admin.go`, `internal/organisations`. Schema: migration `20261008140300` (RBAC) and
`20261008140400` (organisations). Specs: SECURITY §5, `docs/compliance/operational-controls.md`, ADR-017.

## 1. Route policies (deny by default)

Every route is registered with exactly one policy; registering a route without one panics at start-up, and a
test asserts every route has a policy and appears in the OpenAPI contract.

| Policy | Allows | Otherwise |
|---|---|---|
| `Public` | everyone | — |
| `Authenticated` | any live session | `401 AUTHENTICATION_REQUIRED` |
| `User` | personal (USER) accounts | anonymous 401; staff `403 PERMISSION_DENIED` (staff accounts never act as users) |
| `Staff` | staff sessions (MFA guaranteed by DB CHECK) | **`404 ROUTE_NOT_FOUND`** (admin routes are not discoverable) |
| `Permission(code)` | staff holding `code`; plus a fresh step-up when `permissions.requires_step_up` | non-staff 404; missing permission `403 PERMISSION_DENIED`; stale step-up `403 STEP_UP_REQUIRED` |

If the step-up set cannot be loaded at start-up, every permission requires step-up (fail closed). A database
failure while resolving the session gives `503`, never "anonymous". Ownership (own sessions, own
organisation) is checked in handlers with queries scoped by the caller's ID, so guessing an ID returns 404.

The unit matrix is in `internal/auth/auth_test.go` (`TestAuthorizeMatrix`); integration tests exercise users on
admin routes, staff on user routes and anonymous callers.

## 2. Staff roles and permissions

Roles and permissions are reference data seeded by migration (`app.roles`, `app.permissions`,
`app.role_permissions`, read-only for the runtime roles). Stage 4 adds `account.suspend`, `account.reactivate`
(COMPLIANCE) and `staff.invite` (SUPER_ADMIN, SECURITY_ADMIN). SUPER_ADMIN holds role management, audit read
and `staff.invite` — **not** KYC, ledger, payout or account-suspension permissions (least privilege; tested:
a SUPER_ADMIN gets 403 on suspension).

Effective permissions = active role assignments (`revoked_at IS NULL`, within `valid_from`/`expires_at`)
∪ active break-glass grants. They are recomputed on every request.

## 3. Staff accounts

- **No public staff registration.** Staff accounts are created by `POST /admin/staff` (permission
  `staff.invite`, step-up, justification ≥ 10 characters) or the bootstrap ceremony (§5). The account has no
  password until the invitation is accepted.
- **Invitation acceptance** (`/staff/accept-invitation?token=…`): `staff-invitation/start` validates the link
  and returns a TOTP enrolment; `finish` consumes the token, checks the TOTP code, sets the password, marks the
  email verified and returns ten recovery codes — in one transaction. A staff account therefore never has a
  password without MFA, and staff login always requires TOTP.
- Staff need a distinct work address: one address is one account (`uq_user_emails_login`).

## 4. Role changes: maker-checker

| Step | Rule | Enforced by |
|---|---|---|
| Request (`POST /admin/role-assignment-requests`, `role.assign.request`, step-up) | `GRANT` or `REVOKE`, justification ≥ 10 chars, target must be a non-system STAFF account, GRANT needs the role not held, REVOKE needs it held; one pending identical request | handler + `ck_role_assignment_requests_no_self_request`, `uq_role_assignment_requests_pending` |
| Approve / reject (`role.assign.approve`, step-up) | Decider ≠ requester and ≠ target; request still PENDING and not expired (24 h; an expired request is marked EXPIRED) | handler + `ck_role_assignment_requests_no_self_approval`, status-transition guard |
| Grant | `role_assignments` row copies maker and checker through a composite FK to the APPROVED request; separation-of-duties role conflicts rejected by trigger | DB |
| Effect | Target's sessions revoked (`PRIVILEGE_CHANGE`) | handler |
| Audit | `rbac.role_request.created/approved/rejected/expired` in the security chain | `audit.Record` |

## 5. Super-admin bootstrap ceremony

There is **no hard-coded administrator** and no API path to the first SUPER_ADMIN.
`fundzimctl bootstrap-admins` (operator-run, application DB role):

1. Takes a transaction-scoped advisory lock and **refuses if any active SUPER_ADMIN assignment exists**.
2. Creates two distinct STAFF accounts (invited; they must set a password and enrol TOTP before signing in).
3. For each, inserts a GRANT request with maker = the seeded **system actor** (non-login, `is_system`) and
   checker = **the other new admin**, approves it, and inserts the assignment — so even the bootstrap grants
   live in the maker-checker tables and no account approves itself.
4. Writes `staff.invited` and `rbac.bootstrap.super_admin_granted` security audit events with the operator's
   justification.

Limitation (recorded in the audit metadata as `checker_step_up: ceremony_time`): the checkers have not yet
authenticated when the ceremony runs; the ceremony itself (operator with database access, two named people)
is the control. Production must run it under a documented procedure (Stage 18/20).

## 6. Account suspension

`POST /admin/users/{id}/suspend` (`account.suspend`), `…/reactivate` (`account.reactivate`),
`POST /admin/staff/{id}/suspend` (`staff.suspend`); all step-up, reason ≥ 10 characters. Self-suspension is
refused, the system actor is invisible, a target of the wrong kind is 404. Suspension revokes all sessions and
notifies the owner; a suspended account cannot sign in (`403 ACCOUNT_SUSPENDED`, only after a correct
password). Staff reactivation is deferred (should be maker-checker).

## 7. Organisations (membership foundation)

| Role | Permissions |
|---|---|
| `ORG_ADMIN` | `org.view`, `org.settings.manage`, `org.member.manage`, campaign/payout permissions reserved for later stages |
| `ORG_MEMBER` | `org.view`, `org.campaign.create`, `org.campaign.edit` |

- Personal (USER) accounts only; creating an organisation requires a **verified email**; the creator becomes
  ORG_ADMIN. Max 20 created per user (INTERNAL_RISK).
- **Isolation:** every query is scoped by organisation ID **and** the caller's active membership. Non-members
  get `404 ORGANISATION_NOT_FOUND` (existence not revealed); members without the needed permission get 403.
  Member/invitation IDs from another organisation cannot be used through this one (tested).
- **Invitations** are addressed to an email; only the signed-in owner of that **verified** sign-in address sees
  and accepts them. The email carries no token, so a forwarded email grants nothing. Pending invitations expire
  after 7 days; max 50 pending per organisation.
- **At least one ORG_ADMIN:** a deferred constraint trigger, checked immediately in the transaction
  (`SET CONSTRAINTS ALL IMMEDIATE`), returns `409 LAST_ORG_ADMIN` for demoting/removing the last admin.
- Organisation roles never grant platform (staff) permissions and vice versa. KYB verification, logos and
  organisation settings updates are later stages.
