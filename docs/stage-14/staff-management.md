# Stage 14 — Staff management

Pages `/admin/staff`, `/admin/staff/{id}`, `/admin/staff/roles`, `/admin/staff/access-reviews`,
`/admin/staff/access-reviews/{id}`. Services live in `internal/auth` (roles, assignments, sessions) and
`internal/operations` (access reviews). No parallel identity store: accounts stay in `app.users`.

| Capability | Route | Permission | Notes |
|---|---|---|---|
| Directory | `GET /api/v1/admin/staff?q=&status=` | `staff.view` | name, status, roles, MFA confirmed, active sessions, last activity; no email, phone or password data |
| Detail | `GET /api/v1/admin/staff/{user_id}` | `staff.view` | effective permissions, role history, pending role requests, recent security event *types*; each view is audited (`staff.viewed`, security stream) |
| Role inventory | `GET /api/v1/admin/staff/roles` | `staff.view` | each role's permissions and holder count |
| End sessions | `POST /api/v1/admin/staff/{user_id}/sessions/revoke` | `session.revoke` (step-up) | never one's own (`SELF_ACTION_FORBIDDEN`); reason required; audited `session.revoked_by_staff`; the target is signed out immediately |
| Role grant / revoke | existing `role-assignment-requests` routes | `role.assign.request` / `role.assign.approve` | dual control, unchanged; the staff detail page shows pending requests and the checker's approve/reject |
| Effective permissions | `GET /api/v1/me`, `GET /api/v1/auth/session` | own session | `permissions` for staff only; used to hide what the caller cannot do |

## Privileged access controls

- No staff member can grant themselves a role: grants are requests decided by a different holder of
  `role.assign.approve` (database CHECK and service rule, Stage 4).
- There is no superuser: `SUPER_ADMIN` holds identity governance (`staff.invite`, `staff.view`, `role.assign.*`,
  `access.review.certify`), audit (`audit.read`, `audit.export`, `security_audit.read`), `campaign.view` and the
  dashboard — no KYC documents, ledger adjustments, payout approval or risk decisions (`/admin/staff/roles`).
- `EffectivePermissions` returns nothing for a non-active or non-staff account.

## Access reviews

- Started by `access.review.run` (SECURITY_ADMIN) with a named reviewer who is not the starter (database CHECK).
- Items are the active role assignments in scope at start. The reviewer (`access.review.certify`) records CERTIFIED or
  REVOKE_RECOMMENDED with a reason; nobody decides their own access or a linked account's
  (`OPS_CONFLICT_OF_INTEREST`, database guard via `app.actor_identities`); a decision is final (database guard).
- REVOKE_RECOMMENDED files an ordinary REVOKE role request (`auth.RequestRoleRevocation`): a second person must approve
  it before anything is removed.
- No review frequency is configured or invented; reviews are started when needed.
