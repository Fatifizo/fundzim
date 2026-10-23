# Stage 18 — Security monitoring, alerting and audit

## 1. Audit coverage

Every module writes audit events in the same transaction as the change (business and security streams), including
authentication, sessions, MFA, privileged actions, role changes, financial approvals, payment/payout state changes,
ledger postings and adjustments, reconciliation decisions, security alert actions (Stage 18:
`operations.security_alert.{acknowledged,escalated,resolved}`) and configuration changes through maker-checker records.
Secret-manager access is audited by the secret manager (production: OpenBao audit device). Backup operations are
recorded in `app.ops_backup_runs`. Events never carry secrets, codes or identity values (metadata key checks).

**Stage 18 volume change:** a status poll whose answer repeats the previous one is no longer audited (it stays in
`app.payment_attempts`); it was 1.07 M of 1.19 M audit rows (~124 k a day from 864 stalled sandbox payments).

## 2. Audit integrity and performance (KI-S17-12 investigation)

Root cause: full verification recomputes SHA-256 over the jsonb rendering of every row (~30 µs each) in one pass, on a
table growing ~124 k rows a day from repeated poll results. Not missing indexes and not test sizing. Measurements on
1 314 358 rows: plpgsql single pass 40.1 s; set-based rewrite (window functions) 40.4 s — rejected.

Implemented (ADR-050): `audit.verify_chain_range` (contiguous range incl. the link to the previous row) run in parallel
— 13.2 s on 8 connections (worker: 12.5 s on 4); MAC-protected checkpoints under the worker-only key; incremental
verification from the latest checkpoint under the current key — 8 ms; worker jobs every 15 min (incremental) and daily
(full); every run in `audit.chain_verifications` (append-only). Tamper tests (inside a rolled-back transaction): a
modified row → `hash mismatch`; a removed row → `seq gap` + `prev_hash` mismatch. Integrity is **tamper evidence**: a
database superuser can disable triggers; anchoring checkpoint heads outside the database is pending (KI-S18-05).

## 3. Retention

`audit.retention_policies` classifies 20 record families. 17 financial, audit, identity, evidence and operational-record
families are LEGAL_REVIEW_REQUIRED (no period, NEVER_DELETE — CHECK-enforced; LR-012, LR-074). Three OPERATIONAL_ONLY
rows describe purges that already exist for non-records (dispatched outbox rows 7 days, idempotency keys 24 hours,
discovery dedupe hashes 2 days). A period can only be stored with an approval reference, approver and time; nothing
deletes records based on the registry. Legal holds on evidence: `audit.evidence_holds` (maker-checker).

## 4. Security telemetry and alert rules

The worker runs `operations.SecurityScan` every minute over module snapshots (each built by its module from its own
tables), the identity security signals (`auth.SecuritySignals`, counts only), the audit verification status
(`audit.OpsSnapshot`), the Stage 14 derived operational alerts and backup evidence. Thresholds are engineering
defaults, not service levels.

| Alert key | Severity | Condition |
|---|---|---|
| FINANCIAL_GATE_NOT_BLOCKED | CRITICAL | a `*.live_execution`, `ledger.production_posting` or `reconciliation.production` metric not reported BLOCKED |
| AUDIT_CHAIN_INTEGRITY_FAILURE | CRITICAL | latest verification found problems or an invalid checkpoint |
| AUDIT_CHAIN_VERIFICATION_STALE | MEDIUM | no verification in the last hour, or never |
| LEDGER_INTEGRITY_FAILURE (derived) | CRITICAL | ledger integrity check failed |
| RECONCILIATION_CRITICAL_EXCEPTIONS | HIGH | open CRITICAL reconciliation exceptions |
| CREDENTIAL_STUFFING_SUSPECTED | HIGH | ≥ 50 failed sign-ins / password checks in 15 min |
| ACCOUNT_TARGETED | MEDIUM | an account with ≥ 10 failed sign-ins in 15 min |
| SECOND_FACTOR_FAILURES | MEDIUM | ≥ 20 failed MFA/step-up/OTP checks in 15 min |
| RATE_LIMIT_ANOMALY | MEDIUM | ≥ 100 rate-limit refusals in 15 min |
| BREAK_GLASS_ACTIVE | HIGH | an active break-glass grant |
| MFA_REMOVED / RECOVERY_CODES_USED / PRIVILEGE_CHANGES | INFO / LOW / INFO | daily windows (deduplicated per UTC day) |
| MONITORING_SOURCE_UNAVAILABLE | MEDIUM | a snapshot source failed (never shown as zero) |
| BACKUP_EVIDENCE_MISSING / BACKUP_STALE / BACKUP_FAILED | MEDIUM / HIGH / HIGH | none recorded / older than 26 h / latest run failed |
| RESTORE_DRILL_MISSING / RESTORE_DRILL_STALE | MEDIUM | none / older than 35 days |
| Stage 14 derived alerts (worker stopped, outbox backlog, dead letters, webhook rejections, consent enforcement, …) | SEV1–SEV4 → CRITICAL–LOW | as Stage 14 |

Not covered (no source exists): per-process rate-limit metrics of the API (worker cannot read them), secret-manager
access failures (the OpenBao audit device is production infrastructure), host intrusion detection.

## 5. Alert records

`app.ops_security_alerts`: alert key, dedupe key, severity, source module, component, title, source, evidence (counts,
statuses, metric keys only; ≤ 4 KiB), first/last seen, occurrences, condition present/cleared, acknowledgement,
escalation (incident id), resolution. One live alert per condition (partial unique index + advisory lock); the scan
raises, repeats, clears and recurs; a person acknowledges (`security.alert.manage`, step-up), escalates (declares or
links an operations incident) and resolves (note required). The lifecycle OPEN → ACKNOWLEDGED → RESOLVED is
database-guarded; history in `app.ops_security_alert_events` (append-only). A condition still present after resolution
opens a new alert. API: `/api/v1/admin/security/alerts…`, `/api/v1/admin/security/backups`; UI: `/admin/security`.
Measured: one scan 77 ms p50 (alert latency ≤ 60 s + scan time).

Observed on the development stack: the first scan raised OUTBOX_BACKLOG, PROVIDER_WEBHOOKS_DEAD_LETTERED,
RECONCILIATION_CRITICAL_EXCEPTIONS, RISK_P1_UNASSIGNED (HIGH), NOTIFICATION_DEAD_LETTERS, SCREENING_UNAVAILABLE (MEDIUM)
— test data from earlier stages — and AUDIT_CHAIN_VERIFICATION_STALE, which cleared once the worker had verified. It
also raised MONITORING_SOURCE_UNAVAILABLE for the identity signals, which exposed a real query bug (F-S18-19).

No alert is sent outside FundZim (no provider is selected; Stage 15 rules). A paging channel is Stage 20 work.
