# Stage 14 — Exception management

Page `/admin/operations/exceptions` (list, `ops.dashboard.view`) and
`/admin/operations/exceptions/{module}/{ref}` (triage, `ops.exception.work` — FINANCE, COMPLIANCE, ADMIN,
SECURITY_ADMIN).

- **Source of truth:** each module's snapshot lists its open exceptions (payment UNKNOWN, payout exceptions, ledger and
  fee reconciliation items, webhook dead letters, screening errors, failing rules, escalated reviews…). The exception
  center shows those the caller may see (the module's permission) with their triage state (`UNTRIAGED` when none).
- **Triage** (`POST …/triage`): the first touch opens a triage record (only for an exception the module currently
  reports — `OPS_EXCEPTION_NOT_OPEN` otherwise) and stores the module's view permission on it. Assignment (the
  assignee needs `ops.exception.work` and the module's permission) acknowledges it; statuses follow the
  `ops_exception` machine; notes are append-only.
- **Resolution stays with the module:** RESOLVED is refused while the module still reports the exception
  (`OPS_EXCEPTION_DOMAIN_OPEN`); WAITING_EXTERNAL records that someone else must act. After the module stops reporting
  it, triage can be resolved and closed; the record stays visible only to holders of the stored permission.
- **History** (`GET …/history`): `ops.exception.work` plus the stored permission, no step-up for reading.

Covered by `TestOperationsExceptionTriage` (TEST screening in ERROR → triage → domain-open refusal → waiting external →
history; SUPPORT cannot read it; unknown reference refused).
