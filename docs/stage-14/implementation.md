# Stage 14 — Administration & Operations Portal: implementation

Date: 2026-10-16. Branch `stage-14/operations`. Decision record: [ADR-046](../adr/ADR-046-operations-portal.md).
TEST environment only: live donations, real payouts, production ledger posting and production screening stay BLOCKED.

## Documents

| Topic | Document |
|---|---|
| Prerequisites, governance, Stage 13 remediation | [prerequisite-assessment.md](prerequisite-assessment.md) |
| Architecture, boundaries, lifecycles | [operations-architecture.md](operations-architecture.md) |
| Command center and metrics | [command-center.md](command-center.md) |
| Staff management and access governance | [staff-management.md](staff-management.md) |
| Approval center | [approval-center.md](approval-center.md) |
| Campaign / verification / risk / financial operations | [campaign-operations.md](campaign-operations.md), [verification-operations.md](verification-operations.md), [risk-operations.md](risk-operations.md), [financial-operations.md](financial-operations.md) |
| Support cases | [support-operations.md](support-operations.md) |
| Exceptions | [exception-management.md](exception-management.md) |
| Health and alerts | [platform-health.md](platform-health.md) |
| Incidents | [incident-management.md](incident-management.md) |
| Audit center | [audit-center.md](audit-center.md) |
| Reports and SLA targets | [reporting.md](reporting.md) |
| Security review | [security-review.md](security-review.md) |
| Tests and measurements | [testing.md](testing.md) |
| Known issues | [known-issues.md](known-issues.md) |
| Stage 15 preparation | [../stage-handover/STAGE-14-TO-STAGE-15.md](../stage-handover/STAGE-14-TO-STAGE-15.md) |

## What was built

**Backend.** `internal/operations` (service, HTTP, 34 routes; plus 4 staff routes in `auth`), `internal/platform/opsview`, an `OpsSnapshot` in
identity (auth), campaigns, kyc, beneficiaries, compliance, risk, payments, providers (psp), ledger, payouts and fees
(plus `OpsSearch` in campaigns, kyc, risk, payments, payouts), staff directory / detail / role inventory / session
revocation in `auth`, audit search and export reader in `audit`, outbox and job statistics, effective `permissions` on
`/me` and the session for staff. Migrations `20261017090000_risk_fault_isolation` and `20261017090100_operations`
(nine `app.ops_*` tables, five state machines, eleven permissions with role mappings). Risk rule fault isolation and the
payout `RISK_EVALUATION_INCOMPLETE` indicator. OpenAPI: 30 paths and 63 schemas (tag "Operations (Stage 14)").

**Frontend.** A permission-aware admin shell (identity, search, task indicator, breadcrumbs, grouped navigation; one
navigation landmark that follows the content on phones), and pages `/admin/operations` (+ `campaigns`, `verification`,
`payments`, `ledger`, `payouts`, `exceptions`, `exceptions/{module}/{ref}`, `health`, `incidents`, `incidents/{id}`,
`sla`), `/admin/approvals`, `/admin/support` (+ `{id}`), `/admin/staff` (+ `{id}`, `roles`, `access-reviews`,
`access-reviews/{id}`), `/admin/audit`, `/admin/reports`, `/admin/search`; `/admin` redirects to the command center.
Risk pages offer only the actions and forms the caller's roles include (KI-S13-21).

## Acceptance criteria (brief §77) — engineering self-assessment

| # | Criterion | Result | Evidence |
|---|---|---|---|
| 1–3 | Prerequisites, governance, blockers tracked | MET | prerequisite-assessment.md; register row 14 NOT_RECORDED |
| 4 | Risk fault isolation | MET | `TestRiskRuleFaultIsolation` |
| 5–7 | Unified architecture, shell, command center | MET | ADR-046; E2E `staff-operations.spec.ts` |
| 8–10 | Real sources, labelled synthetic data, accurate statuses | MET | `TestOperationsOverviewSourcesAndPermissions`; BLOCKED gates; UNAVAILABLE sources |
| 11–13 | RBAC enforced; permission-aware navigation and actions | MET | integration tests call the API directly; vitest navigation tests; E2E 404 and 403 checks |
| 14–17 | Staff directory, access management, privileged controls, access reviews | MET | `TestOperationsStaffGovernance` |
| 18–20 | Approval queue, execution by domain services, maker-checker | MET | `TestOperationsApprovalCenterRoutesToDomain` |
| 21–29 | Campaign, verification, risk, compliance-hold, payment, provider, ledger (read-only), payout, fee operations | MET (views over module snapshots; decisions in the existing module pages) | module pages; overview test validates every snapshot against the real schema |
| 30–31 | Support cases and escalations | MET | `TestOperationsSupportCases`; E2E |
| 32–33 | Global search and its authorization | MET | `TestOperationsReportsSearchAndSLA` |
| 34 | Exception center | MET | `TestOperationsExceptionTriage` |
| 35–37 | Health, alerts, incidents | MET | `TestOperationsIncidentsAndAlerts`, overview test |
| 38–40 | Audit center, immutability, controlled exports | MET | `TestOperationsAuditCenter` |
| 41–43 | Reporting, SLA foundations, task indicators | MET | `TestOperationsReportsSearchAndSLA`; vitest indicator test |
| 44 | API contracts | MET | Redocly lint (2 pre-existing warnings); route/contract test in `internal/app` |
| 45 | Migrations | MET | both migrations round-tripped down/up on the development database |
| 46–47 | Responsive, accessibility | MET (automated) | E2E on desktop and Pixel 7 with axe (no serious violations); no manual screen-reader audit |
| 48–53 | Security, integration, concurrency, failure-injection, backend, frontend tests | see [testing.md](testing.md) | |
| 54 | OpenAPI validation | MET | Redocly |
| 55 | Remote CI | see the completion report | |
| 56–59 | No real payments, payouts, production posting or fabricated provider status | MET | unchanged gates; derived provider health only |
| 60–61 | Documentation, Stage 15 handover | MET | this folder; STAGE-14-TO-STAGE-15.md |

## Deviations from the brief and the roadmap

- Same hostname and session as the app (the roadmap's separate staff portal hostname is not built).
- No masked-PII user search: search is exact ids and numbers only (privacy by design, KI-S14-07).
- KYC review, payout approval and investigation stay on their module pages; the portal links to them.
- No break-glass UI; no saved views or bulk actions; exports only in the audit center.
- No internal SLA target is configured (none is known; KI-S14-04).
