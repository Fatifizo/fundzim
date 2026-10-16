# Stage 14 — Operations architecture

Decision record: [ADR-046](../adr/ADR-046-operations-portal.md). This document describes what was built.

## Module and boundaries

| Piece | Location | Owns | May import |
|---|---|---|---|
| Operations service and HTTP | `internal/operations` | `app.ops_support_cases`, `app.ops_support_events`, `app.ops_incidents`, `app.ops_incident_events`, `app.ops_exception_assignments`, `app.ops_exception_events`, `app.ops_access_reviews`, `app.ops_access_review_items`, `app.ops_sla_targets` | `platform`, `audit` |
| Shared snapshot types | `internal/platform/opsview` | — | — |
| Per-module snapshots | `internal/<module>/ops.go` (identity in `internal/auth/staff.go`) | each module's own tables only | unchanged |
| Staff directory, role inventory, session revocation | `internal/auth/staff.go` | (auth tables) | unchanged |
| Audit search and export reader | `internal/audit/search.go` | (audit tables) | unchanged |
| Outbox and job statistics | `internal/platform/outbox/stats.go`, `internal/platform/jobs/stats.go` | read-only | — |
| Composition | `internal/app/operations.go` (`NewOperations`, `registerOperationsRoutes`) | — | — |

`internal/archtest` enforces the import list and table ownership (operations SQL references only `app.ops_*`).

## Data flow

1. A request reaches an operations route; the route policy requires a staff session and the route's permission
   (non-staff → 404; permissions flagged `requires_step_up` need a fresh MFA step-up for mutations).
2. The service builds a `Viewer` (the caller's effective permissions from `auth.EffectivePermissions`, and the caller's
   linked identities from `kyc.ActorIdentities`).
3. Read models call every registered `Source.Snapshot` concurrently (`collect`): each source has its own timeout (3 s
   default, `Service.SourceTimeout`) and panic recovery. A failing source is reported as `UNAVAILABLE` in `sources`;
   its metrics, approvals and exceptions are absent — never zero.
4. Items are filtered by the viewer's permissions (each metric, approval and exception names the permission its module
   requires). Approvals carry their makers (not serialised): a maker sees the item as `blocked_reason: MAKER`.
5. Operations records (support cases, incidents, triage, access reviews, SLA targets) are written in one transaction
   with their append-only event rows and an audit event; every state change passes `app.guard_transition`.

## What the portal never does

- It never writes another module's table, never changes a domain status column and never decides another module's
  approval: approvals and exception resolution link to the owning module's page and route.
- It never writes ledger tables (the ledger view is read-only metrics from `ledger.OpsSnapshot`).
- It never releases a reservation, a hold or a restriction; it never resolves an UNKNOWN payment or payout.
- It sends no email, SMS, push or WhatsApp message (task indicators are in-app only; delivery is Stage 15).
- It has no override permission and no superuser role.

## Lifecycles (Go maps identical to `app.status_transitions`)

| Record | Edges |
|---|---|
| Support case (`ops_support_case`) | OPEN→TRIAGE; REOPENED→TRIAGE; TRIAGE→IN_PROGRESS/ESCALATED/RESOLVED; IN_PROGRESS→WAITING_FOR_CUSTOMER/ESCALATED/RESOLVED; WAITING_FOR_CUSTOMER→IN_PROGRESS/RESOLVED; ESCALATED→IN_PROGRESS/RESOLVED; RESOLVED→CLOSED/REOPENED; CLOSED→REOPENED |
| Incident (`ops_incident`) | DETECTED→ACKNOWLEDGED→INVESTIGATING; INVESTIGATING→MITIGATING/MONITORING/RESOLVED; MITIGATING→MONITORING/RESOLVED; MONITORING→INVESTIGATING/RESOLVED; RESOLVED→CLOSED |
| Exception triage (`ops_exception`) | OPEN→ACKNOWLEDGED; REOPENED→ACKNOWLEDGED; ACKNOWLEDGED→INVESTIGATING/RESOLVED; INVESTIGATING→WAITING_EXTERNAL/RESOLVED; WAITING_EXTERNAL→INVESTIGATING/RESOLVED; RESOLVED→CLOSED/REOPENED; CLOSED→REOPENED |
| Access review (`ops_access_review`) | OPEN→COMPLETED/CANCELLED |
| SLA target (`ops_sla_target`) | PROPOSED→ACTIVE/REJECTED; ACTIVE→SUPERSEDED |

`TestLifecyclesAreClosed` (unit) and `operations-components.test.tsx` (web, reads the Go source) keep the copies equal.
