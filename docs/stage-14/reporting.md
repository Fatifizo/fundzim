# Stage 14 — Operational reporting and internal SLA targets

## Reports

Page `/admin/reports`; API `GET /api/v1/admin/reports?key=&from=&to=` (`ops.dashboard.view`; each report also needs
its source permission). Range: default last 30 days, at most 366, `to` after `from`.

Reports: `campaign_moderation_backlog`, `verification_processing`, `risk_investigation_backlog`, `payment_exceptions`,
`payout_exceptions`, `financial_reconciliation`, `support_case_volumes`, `approval_queue_age`, `operational_incidents`,
`staff_access_review`. Each states its `source`, `environment` (TEST), `freshness` and `limitations`; rows are counts or
durations with a nature label. There are no money totals and currencies are never combined. Settlement reconciliation
is reported as NOT_PERFORMED (no settlement evidence).

## Internal SLA targets

Page `/admin/operations/sla`; table `app.ops_sla_targets`. **None is configured**: FundZim has no contractual or
regulatory service levels to encode, and none is invented.

- Metrics a target may be set for: VERIFICATION_REVIEW_TURNAROUND, CAMPAIGN_MODERATION_TURNAROUND,
  SUPPORT_FIRST_RESPONSE, SUPPORT_RESOLUTION, RISK_INVESTIGATION_AGE, APPROVAL_PROCESSING, EXCEPTION_RESOLUTION,
  INCIDENT_ACKNOWLEDGEMENT; 60 s – 365 days.
- Maker-checker: proposed with `ops.sla.request` (ADMIN, COMPLIANCE), approved or rejected with `ops.sla.approve`
  (BUSINESS_APPROVER) by a different person (database guard); one PROPOSED and one ACTIVE target per metric; approving
  supersedes the previous ACTIVE target.
- Effect today: an ACTIVE `SUPPORT_RESOLUTION` target sets `due_at` on new support cases; the other targets are recorded
  for later reporting. Risk case due dates remain the Stage 13 engineering defaults (KI-S13-12).
