# Stage 17 — Reconciliation operations

## 1. Staff UI (`/admin/reconciliation`, FINANCE; COMPLIANCE reads)

| Page | Shows | Actions |
|---|---|---|
| Overview | production gates (BLOCKED), active rule set (or "none — matching stopped"), open exceptions by severity and type, approvals waiting, unknown-outcome evidence, oldest open age, sources with freshness (NEVER / FRESH / STALE / UNKNOWN), imports, lines per currency and status, settlement batches, workers, recent runs | — |
| Imports and runs (`/batches`) | imports (hash, status, counts, origin), runs (scope, kind, rule set and matcher version, counts) | upload a TEST statement, generate one from the simulator, queue a run, replay a run |
| Import detail (`/batches/[id]`) | evidence (hash, evidence record, period, balances, account hint), validation report, lines with matches and explanations, exceptions on the lines | — |
| Exceptions (`/exceptions`, `/exceptions/[id]`) | queue (filters), detail with line, comparison detail, history, resolutions with proposal hash | assign, start, wait for evidence, resume, comment / evidence reference, propose a resolution, approve / reject / cancel, close, reopen |
| Settlements | batches: provider header vs FundZim's expectation, lines settled | — |
| Payouts | in-flight and UNKNOWN payouts, attempts (refused-before-processing counted), reservation, halts, payout exceptions, the rules reconciliation follows | — |
| Ledger, Fees | trial balance per currency, integrity, journal checks; fee statuses, mismatches, fee recompute | — |
| Reports | bounded reports | CSV export (step-up, audited) |
| Matching rules | rule sets with hash and decisions | propose, approve, reject, withdraw, retire |

Every page carries the TEST evidence banner; money is shown from integer minor units without floating point; currencies
are never added together. The pages work at phone width (E2E on Pixel 7).

## 2. Reports

`daily_summary`, `settlement_summary`, `unmatched_lines`, `payout_exceptions`, `fee_discrepancies`, `ledger_integrity`,
`exception_ageing` — last 30 days by default, at most 366 days, at most 5 000 rows (`truncated`), nature TEST_ONLY.
CSV export (`reconciliation.report.export`, step-up): a `nature` column first; every cell starting with `=`, `+`, `-`,
`@`, tab or CR is prefixed with an apostrophe (CSV injection); attachment with `nosniff`; audited
(`reconciliation.report.exported`). The Stage 14 `financial_reconciliation` report now includes the reconciliation
metrics.

## 3. Health and alerts

| Signal | Where | Alert |
|---|---|---|
| worker heartbeats (parse, match, ageing) | overview, ops snapshot | DEGRADED after a failure or 30 min without success; UNKNOWN when never run |
| rule set missing | ops snapshot `reconciliation.rule_set` NOT_CONFIGURED | — |
| import validation failures | `reconciliation.imports.failed` (7 days) | in-app `RECON_IMPORT_FAILED` to importers |
| open / CRITICAL exceptions, unknown outcomes, unmatched lines, batch discrepancies | ops snapshot metrics | in-app `RECON_EXCEPTION_OPENED` (HIGH and CRITICAL only), `RECON_UNKNOWN_OUTCOME_ESCALATED` |
| ledger imbalance | ledger integrity (its own alerts) and the ledger page | stop and report |
| duplicate payout evidence | DUPLICATE_TRANSACTION / PAYOUT_MISMATCH (CRITICAL) | exception notice |
| settlement discrepancy | SETTLEMENT_SHORT / OVER | exception notice |
| bank mismatch / donor funds alarm | UNMATCHED_BANK_ENTRY / POSSIBLE_DONOR_FUNDS_IN_OPERATING_ACCOUNT (CRITICAL) | exception notice |
| backlog / staleness | STALE_RECONCILIATION, open lines | exception notice |
| production gates | `reconciliation.production` BLOCKED | — |

Thresholds (30 min heartbeat, 7-day failure window) are engineering defaults, not SLAs.

## 4. Notifications (Stage 15 engine; staff, in-app only)

| Event | Template | Recipients |
|---|---|---|
| `reconciliation.exception_opened` (HIGH, CRITICAL) | RECON_EXCEPTION_OPENED | holders of `reconciliation.investigate` |
| `reconciliation.exception_assigned` | RECON_EXCEPTION_ASSIGNED | the assignee (still an investigator; never for a self-assignment) |
| `reconciliation.import_failed` | RECON_IMPORT_FAILED | holders of `reconciliation.import` |
| `reconciliation.resolution_requested` | RECON_RESOLUTION_AWAITING_APPROVAL | holders of `reconciliation.resolve` except the maker |
| `reconciliation.unknown_outcome_escalated` | RECON_UNKNOWN_OUTCOME_ESCALATED (high priority) | holders of `reconciliation.resolve` |

Templates are migration baseline (`STAGE_17_BASELINE`), placeholder-only, synthetic-labelled; no external channel is used
(`LiveMessagingEnabled = false`).

## 5. Runbook (TEST)

1. Approve a rule set (two FINANCE officers). 2. Import statements (or generate them from the simulator). 3. Wait for the
worker (≤ 1 min) or queue a run. 4. Work the queue: assign, investigate, propose, approve. 5. Close resolved cases after
checking. Never edit `recon.*`, payment, payout or ledger rows by hand; an imbalance or a negative balance is a stop-and-
report event.
