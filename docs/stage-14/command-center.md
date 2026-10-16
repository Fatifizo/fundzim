# Stage 14 — Operations command center

Page `/admin/operations`; API `GET /api/v1/admin/operations/overview` (also `/metrics`, `/tasks`, `/alerts`).
Permission `ops.dashboard.view` (every staff role); each item additionally needs its own permission.

## Content

- **My tasks** (`Tasks`): approvals the caller can decide, support cases assigned to the caller (and those past an
  approved internal target), exceptions assigned, incidents owned, access-review items waiting, open incidents. Counts
  are computed on request from operations records and module snapshots.
- **Alerts** (see [platform-health.md](platform-health.md)) with a link to the incident declared for each.
- **Metrics** grouped by module. Each carries `source` (the table and condition, in words), `permission`, `nature` and,
  for capabilities, `status`.

## Metric rules

| Rule | Implementation |
|---|---|
| Every metric has a documented source | `opsview.Metric.Source` is required; `TestOperationsOverviewSourcesAndPermissions` fails on an empty source, nature or permission |
| Counts come from the owning module's tables | each module's `OpsSnapshot` runs `count(*)` over its own tables (not a capped list) |
| No synthetic value mixed with production | TEST payments, ledger, fees and payouts are `TEST_ONLY`; simulator data `SIMULATED`; there is no money total on the page |
| Disabled capability ≠ healthy | live payments, real payouts, production ledger posting, production fees and production screening are `BLOCKED` / `BLOCKED` |
| Unknown stays unknown | a metric with no value is "Not measured", never 0; a failed source is listed as unavailable |

Metric keys (authoritative list: the `OpsSnapshot` functions): identity (`staff.active`, `staff.suspended`,
`staff.without_mfa`, `staff.role_requests.pending`), campaigns (`campaigns.reviews.*`, `campaigns.reports.open`,
`campaigns.updates.pending`), kyc (`kyc.reviews.*`, `kyb.reviews.*`, `verification.escalated`), beneficiaries
(`beneficiaries.reviews.pending`), compliance (`compliance.cases.open`, `compliance.resolutions.pending`,
`compliance.restrictions.active`), risk (`risk.cases.*`, `risk.holds.active`, `risk.rules.active`, `risk.screening.*`),
payments (`payments.unknown`, `payments.refunds.pending_approval`, `payments.reconciliation.open`,
`payments.live_execution`), providers (`providers.<code>.health`, `providers.live_execution`,
`providers.webhooks.dead_lettered`), ledger (`ledger.adjustments.pending`, `ledger.reconciliation.open`,
`ledger.integrity`, `ledger.production_posting`), payouts (`payouts.awaiting_approval`, `payouts.approved_not_submitted`,
`payouts.in_flight`, `payouts.unknown`, `payouts.exceptions.open`, `payouts.destinations.pending`,
`payouts.live_execution`), fees (`fees.policy_versions.pending`, `fees.adjustments.pending`,
`fees.provider_schedules.pending`, `fees.reconciliation.open`, `fees.production`), operations (`support.*`,
`incidents.open`, `exceptions.in_triage`, `access_reviews.open`).

## Performance (measured, development stack)

From `TestOperationsPerf` (2026-10-16, in-process service calls, 412k audit events): overview p50 293 ms / p95 336 ms
(c=1) and p50 386 ms / p95 486 ms (c=8). The providers snapshot dominates (p50 215 ms): its derived health checks
whether provider credentials resolve in the secret manager. See [testing.md](testing.md).
