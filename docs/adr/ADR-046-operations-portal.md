# ADR-046: Operations portal — an orchestration module over domain services, its own operational records, no new authority

- **Status:** Proposed (Stage 14, 2026-10-16)
- **Deciders:** Technical lead (Stage 14); owner acceptance pending
- **Implements:** ROADMAP Stage 14; [operational-controls.md](../compliance/operational-controls.md)
- **Related:** ADR-017 (maker-checker), ADR-032/033 (identity, RBAC), ADR-045 (risk); [stage-14/operations-architecture.md](../stage-14/operations-architecture.md)

## Context

Stages 4–13 each built their own staff API and pages (verification, campaigns, compliance, payments, ledger, payouts,
fees, risk). Staff need one place to see queues, approvals, exceptions and health, plus capabilities no module owns:
support cases, operational incidents, staff directory and access reviews, audit search and export, operational reports,
in-app task indicators. The temptation is a "super admin" module that reads every table and changes statuses directly —
which would bypass maker-checker, step-up, state machines and table ownership.

## Decision

1. **New module `internal/operations`** (imports `platform`, `audit` only). It owns only its operational records:
   `app.ops_support_cases` (+ events), `app.ops_incidents` (+ events), `app.ops_exception_assignments` (+ events),
   `app.ops_access_reviews` (+ items) and `app.ops_sla_targets` (maker-checker on the row itself: PROPOSED → ACTIVE |
   REJECTED, the previous ACTIVE target SUPERSEDED). It never reads or writes another module's tables. A triage record
   stores the permission the owning module requires to see the exception (`view_permission`, immutable), so triage
   stays visible only to holders after the module stops reporting it.
2. **Aggregation through snapshots.** Every module exposes `OpsSnapshot(ctx) (opsview.Snapshot, error)` (and optionally
   `OpsSearch`), built from its own tables in its own package (shared value types in `internal/platform/opsview`); the
   composition root (`internal/app/operations.go`) registers them as `operations.Source`s. Sources are read
   concurrently, each bounded by a timeout (3 s) and panic recovery; a source that fails is shown as UNAVAILABLE and its
   figures are missing (never 0) while the rest of the page renders. Every metric names its source, the permission
   needed to see it and its data nature (REAL, SIMULATED, TEST_ONLY, UNKNOWN, NOT_AVAILABLE, BLOCKED).
3. **No competing authority.** The approval center and the exception center are read models with links to the owning
   module's decision endpoints. A decision is always executed by the owning module's existing endpoint and service
   (which re-checks permission, step-up, maker-checker, linked identities, current state and idempotency). The
   operations module never changes a domain status column. Exception *assignment and triage notes* are operations
   records; *resolution* stays with the domain (e.g. a payout exception is resolved by payouts).
4. **Permissions** (new): `ops.dashboard.view` (every staff role), `ops.support.view`, `ops.support.work`,
   `ops.exception.work`, `ops.incident.view`, `ops.incident.manage`, `staff.view`, `access.review.certify`,
   `audit.export`, `ops.sla.request`, `ops.sla.approve`; existing `audit.read`, `access.review.run`, `session.revoke`,
   `staff.invite`, `staff.suspend`, `role.assign.request|approve` are reused. Every mutation requires step-up. No
   permission grants another permission or bypasses a domain check; there is no override permission.
5. **Permission-aware UI.** The staff self view (`GET /api/v1/me`) returns the caller's effective permissions; the admin
   shell and actions use them to hide what the caller cannot do. The API remains the authority (every route keeps its
   policy; tests call the API directly).
6. **Staff management stays in `auth`.** The staff directory, session revocation and role inventory are `auth` services
   (it owns roles, assignments and sessions; `users` owns the accounts). Access reviews are operations records whose
   revocation recommendations become ordinary role REVOKE requests (dual control).
7. **Audit center in `audit`.** Search and export read `audit.audit_events` through the `audit` package (append-only;
   exports are themselves audited, bounded, permissioned and exclude IP/user agent unless the caller holds
   `security_audit.read`).
8. **Risk fault isolation (KI-S13-11).** A rule version the engine cannot run no longer blocks signal recording: the
   signal and an evaluation of the remaining rules are stored, the failure is recorded (`risk.risk_rule_failures`), the
   evaluation is marked degraded, a monitoring event and an operations exception surface it, and payout eligibility
   treats a degraded latest evaluation as REVIEW (fail closed for money).

## Consequences

- One portal without a privileged backdoor: everything it can do, the owning module could already do (or now does).
- Adding a module to the command center means writing one adapter; the module's rules stay where they are.
- Global search is a fan-out over module search functions with per-source permission checks (bounded, paginated,
  audited), not a cross-schema index.
