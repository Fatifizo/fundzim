# Stage 17 — Financial reconciliation & settlement operations: implementation

Status: **complete; awaiting acceptance; TEST evidence only.** Decision record: [ADR-049](../adr/ADR-049-reconciliation-and-settlement-operations.md)
(implements ADR-030). Next stage: [STAGE-17-TO-STAGE-18](../stage-handover/STAGE-17-TO-STAGE-18.md).

## 1. Documents

| Topic | Document |
|---|---|
| Prerequisites, governance, Stage 11 retry review | [prerequisite-assessment.md](prerequisite-assessment.md) |
| Architecture, data model, workers, idempotency | [reconciliation-architecture.md](reconciliation-architecture.md) |
| Imports, canonical format, settlement batches, late evidence | [settlement-management.md](settlement-management.md) |
| Matching engine, rule sets, runs | [matching-engine.md](matching-engine.md) |
| Payments | [payment-reconciliation.md](payment-reconciliation.md) |
| Bank statements (Model A) | [bank-reconciliation.md](bank-reconciliation.md) |
| Ledger | [ledger-reconciliation.md](ledger-reconciliation.md) |
| Payouts (and KI-S17-01) | [payout-reconciliation.md](payout-reconciliation.md) |
| Fees and revenue | [fee-reconciliation.md](fee-reconciliation.md) |
| Unknown outcomes | [unknown-outcomes.md](unknown-outcomes.md) |
| Exceptions | [exceptions.md](exceptions.md) |
| Adjustments | [adjustments.md](adjustments.md) |
| Maker-checker approvals | [approvals.md](approvals.md) |
| Audit trail and evidence | [audit-evidence.md](audit-evidence.md) |
| Operations: UI, reports, health, alerts, notifications, runbook | [operations.md](operations.md) |
| Security review | [security-review.md](security-review.md) |
| Tests and results | [testing.md](testing.md) |
| Known issues | [known-issues.md](known-issues.md) |

## 2. Code map

| Area | Files |
|---|---|
| Module | `internal/reconciliation/`: `reconciliation.go` (service, gates, interfaces), `format.go` (canonical parser), `imports.go` (pipeline, sandbox statements), `match.go` (pure matcher), `runs.go` (runs, persistence, batches, lines), `rules.go` (rule sets), `exceptions.go` (lifecycle), `resolutions.go` (maker-checker, execution), `reports.go` (read models, reports, CSV), `ops.go` (ops snapshot, ageing), `http.go` (36 routes) |
| Other modules (their tables, their services) | `payments/reconfacts.go`, `payouts/reconfacts.go` (+ RATE_LIMITED in `orchestration.go`), `fees/reconfacts.go`, `ledger/payouts.go` (`JournalsByKeys`), `psp/sandbox/statement.go` (+ scenario 13), `storage/sniff.go` (CSV/JSON for `RECONCILIATION_REPORT`), `notifications/events.go` (five events), `operations/reports.go` |
| Wiring | `internal/app/reconciliation.go`, `app.go`, `routes.go`, `consumers.go`, `worker.go`, `operations.go`, `verification.go` (upload body limit); `platform/jobs` (queue `reconciliation`); `archtest` |
| Migrations | `20261022090000_reconciliation.sql`, `20261022090100_payout_rate_limited_retry.sql` |
| API | `api/openapi/fundzim-v1.yaml` (tag "Reconciliation (Stage 17)", 33 paths, 39 schemas; Stage 2 design paths REC-01… replaced) |
| Web | `src/lib/reconciliation/{admin,api,upload}.ts`, `src/components/admin/reconciliation/{recon-admin,recon-view}.tsx`, `src/app/admin/reconciliation/**` (12 pages), navigation group, error copy |
| Tests | `internal/reconciliation/reconciliation_test.go`, `tests/integration/reconciliation_test.go` (9 tests), `tests/perf/reconciliation_perf_test.go`, `apps/web/src/__tests__/reconciliation.test.tsx`, `apps/web/e2e/staff-reconciliation.spec.ts` (+ mock `e2e/mock-api/reconciliation.mjs`), `apps/web/e2e-real/staff-reconciliation.spec.ts` |

## 3. Prompt sections → delivery

| § | Requirement | Delivered | Notes |
|---|---|---|---|
| 2–3 | Audit, governance | prerequisite-assessment.md | Stage 16 CI observed; no PR, not merged, not accepted; a Stage 16 spec found unwired |
| 4 | Production restrictions | three new gates + TEST CHECKs; all earlier gates unchanged | |
| 5 | Stage 11 retry review | KI-S15-19 verified fixed with data; RATE_LIMITED gap fixed (KI-S17-01) | |
| 6–7 | Architecture, domain model | one module, 11 tables (packages not split further: the module is cohesive (about 5 800 lines of Go)) | suggested sub-packages adapted (ADR-049) |
| 8–9 | Source types, authority | three source kinds with authority ranks; internal state, acknowledgement, transaction, settlement and bank evidence distinguished | REFUND / REVERSAL / ADJUSTMENT / FEE / PAYOUT are line kinds, not sources |
| 10 | Financial states | line states UNMATCHED / MATCHED / EXCEPTION / CONFLICT / SUPERSEDED / IGNORED; exception states; batch and run states | PARTIALLY_MATCHED not used: a line is matched or not (partial refunds compare against the payment) |
| 11 | Batches | `recon.imports` (statement batch) + `recon.runs` + `recon.settlement_batches` | |
| 12–16 | Imports, validation, security, immutability, idempotency | canonical CSV/JSON, all-or-nothing validation, size/count limits, ClamAV, hash-first dedupe, evidence records, immutable lines | no provider schema invented |
| 17–19 | Settlement imports, validation, mathematics | batch headers, credits − debits − fees = net (parser + CHECK + trigger), lines add up | |
| 20 | Multi-currency | per-currency runs, CURRENCY_MISMATCH, no conversion, raw codes kept | FX evidence not modelled (FundZim never converts) |
| 21–26 | Payment reconciliation, unknown outcomes | matcher + maker-checker PROVIDER_OUTCOME_APPLIED through `apply` (RECON) | |
| 27–31 | Ledger | journal checks, trial balance, integrity view, adjustments through the ledger | no settlement postings (ADR-049 §8) |
| 32–35 | Bank | Model A bank checks, donor-funds alarm, account number never stored | balances not reconciled (KI-S17-04) |
| 36–40 | Payouts | matcher + watch queue + maker-checker outcome through `applyStatus`; never resubmitted | |
| 41–43 | Fees, revenue | statement fee vs assessment; fee recompute shown; revenue recognition BLOCKED | |
| 44–45 | Refunds, reversals | refund/chargeback/reversal lines vs payment status | refund transactions do not exist yet (KI-S17-08) |
| 46–49 | Matching engine | deterministic, explainable, versioned rule sets, reference-first | |
| 50–54 | Exceptions | 21 types, severities, lifecycle, assignment, resolution | |
| 55–58 | Maker-checker, adjustments | resolutions and rule sets; LEDGER_ADJUSTMENT through the ledger; production blocked | DRAFT state not modelled (adjustments.md) |
| 59–63 | Scheduling, workers, replay, cut-offs, late evidence | 3 jobs + consumer on queue `reconciliation`, heartbeats, reaper, replay runs, revisions and supersede | no bank schedule invented |
| 64–65 | Audit trail, evidence integrity | audit events for every step; hashes everywhere | |
| 66–72 | UI | 12 admin pages (overview, imports/runs, import detail, exceptions, exception detail, settlements, settlement detail, payouts, ledger, fees, reports, rules) | |
| 73–74 | Reports, provenance | 7 bounded reports, CSV export, TEST_ONLY nature | campaign statements / regulatory exports not built (KI-S17-09) |
| 75–77 | Health, alerts, notifications | ops snapshot, heartbeats, 5 staff notifications | |
| 78–79 | Migrations, API | 2 migrations (round trip verified), OpenAPI | |
| 80–81 | Security, privacy | security-review.md | |
| 82–90 | Tests | testing.md | |
| 91 | CI | integration and E2E jobs pick the new tests up automatically; `go vet -tags perf` already in CI | |
| 92 | Documentation | this folder, ADR-049, handover, README, CLAUDE.md, ARCHITECTURE, SECURITY, ROADMAP, register | |
| 94 | Stage 18 preparation | STAGE-17-TO-STAGE-18.md | |
