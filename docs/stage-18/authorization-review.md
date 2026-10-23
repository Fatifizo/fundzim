# Stage 18 — Authorization, tenant isolation and privileged access

## 1. Model

Every route declares Public, Authenticated, User, Staff or Permission(code); the router panics on a route without a
policy and the default authorizer denies anything non-public (`internal/platform/httpx/router.go`;
`TestRouteWithoutPolicyPanics`, `TestNonPublicRoutesDeniedWithoutAuthorizer`). Permissions = active role assignments ∪
active break-glass grants, reloaded per request. Staff-only routes answer 404 to non-staff; staff without the
permission get 403. The UI's navigation follows `/me` permissions as presentation only (every page re-fetches through a
permissioned route and renders 404 on refusal).

## 2. Role-to-permission review (separation of duties)

| Duty | Who can make | Who can check | Enforcement |
|---|---|---|---|
| Role grant/revoke | SUPER_ADMIN (`role.assign.request`) | another SUPER_ADMIN (`role.assign.approve`, step-up), never the maker or the target | Go + DB (24 h expiry) |
| Payout approval | FINANCE | another FINANCE, never linked identities | Go + DB (`app.actor_identities`) |
| Ledger adjustment | FINANCE | another FINANCE | ledger maker-checker |
| Refund request | FINANCE | another FINANCE (cannot execute) | payments |
| Reconciliation rule set / resolution | FINANCE | another FINANCE | Go + CHECK + trigger |
| Fee policy | FINANCE | BUSINESS_APPROVER | fees |
| Risk rules / hold release | risk staff | a different checker | risk |
| Security alerts (Stage 18) | — (raised by the worker) | SECURITY_ADMIN or ADMIN acknowledge/escalate/resolve (`security.alert.manage`, step-up) | operations |

FINANCE holds payouts, ledger, fees and reconciliation duties; separation is enforced per decision (maker ≠ checker,
linked accounts excluded), not by splitting the role. A distinct reconciliation-checker role remains an owner decision.
Admin roles do not imply KYC document or ledger-adjustment rights; support roles get no KYC, bank, ledger, risk-evidence
or compliance-note access; no superuser and no override permission exist.

## 3. Horizontal and vertical escalation tests

Horizontal (another user's resource): sessions IDOR (`TestIdentitySessionsRotationLogoutAndIDOR`), organisation
isolation (`TestIdentityOrganisationIsolation`), campaigns, payments, payouts, notifications and discovery ITs (owner
checks, 404 for foreign ids). Vertical (more privilege): users cannot discover admin routes (404), staff without the
permission get 403 (RBAC IT, alert IT: SUPPORT 403, user 404), step-up required for sensitive permissions, staff
password-only step-up refused, and the Stage 18 password-change bypass closed. Database level: KYC and compliance
schemas are reachable only by their pools; the app role has no ledger privilege; RLS policies on compliance tables;
`TestReferenceDataAndPrivilegeBoundaries`, `TestSecurityRuntimeRolesCannotRewriteFinancialHistory`.

Tenant isolation summary (brief §21):

| Area | Evidence |
|---|---|
| Users, sessions | identity ITs |
| Organisations | `TestIdentityOrganisationIsolation` |
| Campaigns, updates, media | campaign ITs (owner checks, moderation) |
| Donations, payments | `TestPayments*` (donor-scoped intents, 404 on foreign ids) |
| Payouts, destinations | `TestPayout*` |
| Ledger | no public endpoint; staff only; `fundzim_ledger` pool |
| Reconciliation batches and evidence | staff only (`reconciliation.*`), `TestReconciliation*` (support/reviewer 403/404) |
| Notifications | `TestNotifications*` (recipient-scoped) |
| Analytics | aggregate only (no user ids) |
| Administrative operations | operations ITs; alerts IT |

No cross-tenant financial data leak was found.

## 4. Privileged access management

Named staff identities only (invitation flow, MFA enrolment), least privilege by role, separate operational and
financial roles, every privileged action audited, access reviews (Stage 14, dual-controlled revocations), session
revocation by SECURITY_ADMIN. Emergency access: `app.break_glass_grants` — at most one hour, requester ≠ approver ≠
grantee, never `role.*` or `ledger.adjustment.*`, append-only apart from revocation; it cannot bypass any immutable
financial control (those are database constraints, not permissions). Gaps: no workflow API and no audit tagging of
uses (F-S18-04); the Stage 18 security scan raises `BREAK_GLASS_ACTIVE` (HIGH) whenever a grant is active.

## 5. Service accounts

`fundzim_app` (API), `fundzim_worker` (member of app + verification routines), `fundzim_kyc`, `fundzim_compliance`,
`fundzim_ledger` (only ledger writer), `fundzim_migrator` (DDL, not superuser), `fundzim_readonly` (ledger views),
`fundzim_backup` (Stage 18: read-only, BYPASSRLS, backups only). No application process connects as a superuser.
