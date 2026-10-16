# Stage 14 — Platform health and alerts

Page `/admin/operations/health`; API `GET /api/v1/admin/operations/health` and `/alerts` (`ops.dashboard.view`).
Everything is checked or read **when the request is made**; nothing is cached or invented.

| Component | Check | Status rules |
|---|---|---|
| `api` | the response itself | HEALTHY |
| `database`, `kyc_database`, `compliance_database`, `ledger_database` | `db.Ping` on each pool (2 s timeout) | HEALTHY / UNAVAILABLE |
| `cache` | Redis ping | HEALTHY / UNAVAILABLE |
| `object_storage_<class>` | storage `Check` per bucket class | HEALTHY / UNAVAILABLE |
| `outbox` | `outbox.ReadStats` over `app.outbox_events` | DEGRADED when anything is dead-lettered or the oldest pending event is older than 5 min (`OutboxBacklogAfter`) |
| `workers` | `jobs.ReadStats` over the River tables | UNKNOWN without any completed job; UNAVAILABLE when none completed in 5 min (`WorkerStaleAfter`); DEGRADED with jobs discarded in 24 h |
| `application_errors` | — | always UNKNOWN: error rates go to Prometheus and are not stored for queries (NOT_AVAILABLE) |

Overall = UNAVAILABLE if any component is; otherwise DEGRADED if any is DEGRADED or UNKNOWN. Because application error
rates are not measurable, the overall status is never reported HEALTHY in this environment — deliberately.
Thresholds are **engineering defaults**, not service commitments.

Provider health is not probed here: it is the providers module's derived health (observations only; no observation =
UNKNOWN; the sandbox is SIMULATED; the live gate is BLOCKED). A disabled integration is BLOCKED or NOT_CONFIGURED, never
HEALTHY.

## Alerts

Derived from the health report and module snapshots on each request: `WORKER_STOPPED` (SEV1), `JOBS_DISCARDED` (SEV3),
`OUTBOX_BACKLOG` (SEV2), `DEPENDENCY_<name>_UNAVAILABLE` (SEV1), `LEDGER_INTEGRITY_FAILURE` (SEV1),
`RISK_EVALUATION_FAILURE` (SEV2), `RISK_P1_UNASSIGNED`, `PROVIDER_WEBHOOKS_DEAD_LETTERED` (SEV2),
`SCREENING_UNAVAILABLE` (SEV3). Each alert links to the open incident declared for its key, if any. An alert never
changes data and is never sent outside the application (Stage 15 owns delivery).
