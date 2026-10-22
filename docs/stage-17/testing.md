# Stage 17 — Testing

Every result below was produced in this session (2026-10-22) on branch `stage-17/reconciliation`, against the compose
stack where a stack is needed, under `flock /tmp/claude-1000/fundzim-stack.lock`. No test was skipped, disabled or weakened
to pass; skips listed are pre-existing environment skips. Remote CI: see the completion report (recorded only once
observed).

## 1. Results

| Suite | Command | Result |
|---|---|---|
| Go unit (race) | `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` (inside `golang:1.27.2`: the host has no C compiler for `-race`) | 38 packages ok, no data races |
| gofmt / vet | `gofmt -l apps internal migrations tests`; `go vet` (default, `-tags integration`, `-tags perf`) | clean |
| Architecture | `internal/archtest` | ok — `reconciliation` registered (imports platform, audit, storage, payments, payouts, fees, ledger, psp; nothing imports it), owns the 11 `recon` tables |
| Reconciliation unit | `internal/reconciliation/reconciliation_test.go` | parser (CSV/JSON agreement, 20 rejection cases, currency alias, settlement arithmetic), matcher (confidence, never by amount, differences, payouts and ledger, duplicates and fees, missing settlement, determinism, bank), batch expectation, rule bounds and hash, CSV safety, proposal hash, exception edges = migration |
| Go integration (full) | `go test -tags integration -count=1 ./tests/integration/...` | 192 passed, 4 failed, 13 skipped (pre-existing environment skips). Rerun of the 4: `TestLedgerPeriodCloseMakerCheckerAndPriorPeriodPostings` and `TestPaymentsErrorCategoriesAndAuditEvents` pass (timing in the shared database); `TestIdentityAuditChainsIntact` and `TestComplianceMakerCheckerAndSubjectConflicts` fail on their 30 s timeout for `audit.verify_chain` over the development audit table (1 187 470 rows; 1.07 M are `payments.provider.response_received` written by the sandbox poller since Stage 9 at ~120 000 a day). Verified directly: 0 chain problems in 36.6 s. Environmental (KI-S17-12); CI starts from a fresh database |
| Reconciliation integration | `-run TestReconciliation` (9 tests) | all pass (after the persistence batching change, rerun: ok 115 s) |
| Migrations | up, down ×2, up on the development database; grants checked | clean; `fundzim_app` may UPDATE `recon.items` (status column only, trigger-guarded) but not `exception_events` or `sources`, and may DELETE nothing |
| Web unit | `npm --prefix apps/web test` | 58 files, 572 tests passed |
| Web lint / typecheck / build | `npm run lint`, `typecheck`, `build` | clean |
| Mock E2E (full) | `npm --prefix apps/web run test:e2e` | 217 passed, 1 skipped (pre-existing), desktop and Pixel 7 |
| Real-stack E2E (full) | `npm --prefix apps/web run test:e2e:real` | 64 passed, 0 failed — includes the new reconciliation spec and, for the first time, Stage 16's `discovery.spec.ts` (3 tests), which had never been wired into a project |
| OpenAPI | Redocly lint | valid (2 pre-existing warnings); route-coverage test passes |
| Performance | `TestReconciliationPerf` on scratch `fundzim_perf` | §4 |
| Vulnerabilities | `govulncheck@v1.8.0` | the first CI run on f812009 failed this step: GO-2026-6809 (crypto/x509, reachable from the SMTP sender) published for go1.27.2, fixed in go1.27.3. `go.mod` → `go 1.27.3`, API image → `golang:1.27.3-alpine3.24`; locally: 0 reachable vulnerabilities, Go unit suite passes on 1.27.3 |
| Secrets | `scripts/check-secrets.sh` | no secrets found in 1 431 files; gitleaks is not installed on this machine (NOT RUN locally; CI runs its secret scanning job) |

## 2. Integration tests (`tests/integration/reconciliation_test.go`)

| Test | Proves |
|---|---|
| `RuleSetsAreMakerChecker` | bounds; self-approval refused (Go 403, database refuses a direct self-approved UPDATE and a config change); stale hash refused; second officer activates; previous set retired; audit; support and reviewer get 403 |
| `ImportsAreImmutableDeduplicatedAndValidated` | hash and evidence record; duplicate refused by hash; lines and imports immutable to the app role (UPDATE/DELETE refused); malformed file FAILED with report and no lines; markup refused by storage; support 403, users 403/404; audit; production import refused (code) and PRODUCTION source refused (CHECK) |
| `PaymentsEndToEnd` | exact match (NOT_POSTED_TEST_PAYMENT), AMOUNT_MISMATCH, MISSING_PAYMENT for an amount-only lookalike, DUPLICATE_TRANSACTION, UNKNOWN_PAYMENT_OUTCOME; statement changes nothing by itself; assign → start → propose; self-approval and stale hash refused; second officer approves → payment SUCCEEDED with exactly one RECON event and evidence row; retry executes nothing; replay duplicates nothing and bumps occurrences; simulator statement matches; future-clock run reports no matched payment missing |
| `SettlementBatchesAndExplanations` | arithmetic-broken report FAILED; FEE line → PROVIDER_ADJUSTMENT; batch in DISCREPANCY until explained; EXPLAINED_NO_ACTION → line IGNORED → batch re-evaluated; header amounts immutable |
| `PayoutsUnknownOutcomeNeverResubmitted` | completed payout matched with ledger POSTED; MISSING_PAYOUT (CRITICAL); UNKNOWN_PAYOUT_OUTCOME (CRITICAL); approved resolution completes it through payouts (one RECON event), reservation CONSUMED, completion journal, ledger integrity — attempt count unchanged throughout |
| `RateLimitedPayoutIsBudgeted` | KI-S17-01: exactly the budget of RATE_LIMITED attempts, no status polling, halted with the payout and reservation unchanged, no further attempt |
| `BankStatementsAndLedgerAdjustment` | account number not stored (hint + HMAC); remittance matched; donor-funds alarm (CRITICAL); unknown debit (CRITICAL); LEDGER_ADJUSTMENT: ledger request at proposal, posted by the checker's approval, approved by the checker; trial balance balanced |
| `SupersedeAndConflictingEvidence` | conflicting line → revision 2 CONFLICT + CONFLICTING_EVIDENCE; identical overlapping line → duplicate, not stored; re-issue supersedes (old import, lines SUPERSEDED; revision 3); second supersede refused |
| `Concurrency` | 6 concurrent identical uploads → 1 import; 4 concurrent parsers → lines once; 6 concurrent run requests → at most one active; concurrent executors → no line with two active matches; two checkers racing → exactly one execution |
| `ReportsAndOperations` | gates BLOCKED in the overview; every report kind; CSV export (attachment, `nature` column, no raw formula cell), audited; ops snapshot gate BLOCKED and natures; staleness opens one case with a future clock and closes after a fresh statement |

Failure injection covered: malformed and corrupt files, scan rejection path (unit), hash mismatch guard (code), duplicate
import, conflicting provider outcomes (CONFLICTING_EVIDENCE; payments' own contradiction log), approval race, provider
timeout (UNKNOWN payments/payouts), worker crash (stale-run reaper and PARSING sweeper — code paths; the run claim is a
guarded transition), missing statement (staleness). Not injected: a database outage mid-run (the run transaction rolls
back; the reaper fails the run after 10 min — reasoned, not exercised), ledger posting failure during a LEDGER_ADJUSTMENT
execution (resolution stays APPROVED with `last_error`, retried by the ageing job — code path, not exercised).

## 3. End-to-end

Mock (`apps/web/e2e/staff-reconciliation.spec.ts`, desktop and Pixel 7): dashboard with TEST banner and gates; upload with
step-up; duplicate refused; malformed file with validation report; exception assign → start → propose; self-approval
refused; second officer approves → resolved; settlements, payouts, ledger, fees views; rule proposal; report and CSV
download; axe (serious/critical) on the main pages; CSP; a reviewer has no reconciliation navigation and gets not-found.
Real stack (`apps/web/e2e-real/staff-reconciliation.spec.ts`): FINANCE imports a canonical statement through the UI
(step-up), the worker scans and parses it, a run opens MISSING_PAYMENT for its line, the exception page renders;
COMPLIANCE reads the dashboard; a REVIEWER gets 404. Two FINANCE officers were added to the real-stack staff fixture.

## 4. Performance (scratch `fundzim_perf`, 2026-10-22)

Harness substitutions (documented in the test): statement files stored by an in-memory storage double that records a
CLEAN stored object (no object store or ClamAV in the measurement); payment facts served by an in-memory double; file cap
raised to 64 MiB for the 100k-line file (production cap 10 MiB). Lines: 90 % exact matches, 5 % amount differences, 5 %
references naming nothing. Host: Xeon 8160, 16 vCPU; compose PostgreSQL 17, default settings.

Pure matcher:

| lines | ms | lines/s | heap MB after |
|---:|---:|---:|---:|
| 1 000 | 8.7 | 114 370 | 11.5 |
| 10 000 | 79.7 | 125 431 | 30.8 |
| 100 000 | 811.2 | 123 279 | 198.6 |

Import → parse → run (each step's run also re-decides the previous steps' open lines):

| lines | file MB | upload ms | parse + record ms | run ms | lines in run | matched | exceptions opened |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 1 000 | 0.1 | 113 | 191 | 768 | 1 000 | 900 | 100 |
| 10 000 | 1.5 | 31 | 1 479 | 7 103 | 10 100 | 9 000 | 1 000 |
| 100 000 | 15.1 | 168 | 15 779 | 70 999 | 101 100 | 90 000 | 10 000 |

Read models at the final volume (≈ 111 000 lines, 11 100 exceptions; 50 calls): open exception queue (200) p50 46.0 /
p95 53.2 ms; CRITICAL queue 5.2 / 8.3 ms; dashboard overview 186.3 / 197.3 ms; unmatched-lines report 49.6 / 53.9 ms;
ageing report 10.0 / 15.8 ms. `recon` schema size 197 MB.

**Defect found by this measurement and fixed:** the first run persisted each decision with its own statements (match
insert, line update): 100 000 lines took 207.5 s (1 000 lines: 2.0 s). Matches and line statuses are now written in
chunks of 5 000 through `unnest` arrays: 71.0 s (1 000 lines: 0.77 s). The remainder is dominated by the 10 000 exceptions
(each writes its row, history event, audit event and outbox event) — KI-S17-11.
