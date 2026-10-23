# Stage 19 — Test commands and observed results

Every result below was produced in this session (2026-10-23) on `stage-19/financial-concurrency`, on the development
host described in load-test-plan.md §1, with integration and real-stack runs under
`flock /tmp/claude-1000/fundzim-stack.lock`, after rebuilding the compose API and worker from the branch. Statuses:
PASS / FAIL / NOT RUN; "before fix" runs were made on purpose to reproduce a defect and are listed in
failure-analysis.md.

## 1. Stage 19 tests (focused runs)

| Test | Result | Key observation |
|---|---|---|
| `TestFinancialPayoutCompletionWindowVersusReconciliation` | FAIL before the fix (RESERVATION_MISMATCH; then 6 × RESERVATION_CONSUMED with only the link fix reverted) → **PASS** | reconciliation waited at the barrier; 0 references; 1 consumption event; 3 journals |
| `TestFinancialPayoutConnectionLostBetweenJournalAndConsumption` | PASS (before and after) | reservation ACTIVE after the fault; recovered once |
| `TestFinancialPayoutLedgerRefusalIsStillReported` | PASS | 0 CREATE_PAYOUT while the journal was refused; SEV1 LEDGER_MISMATCH `missing_journal=submitted`, `ledger_sync=FAILED`, `error_code=LEDGER_ACCOUNT_FROZEN`; rerun added nothing; settled once after unfreeze |
| `TestFinancialReservationInvariantsInTheDatabase` | PASS | 6 contradictory writes refused (check, unique, set-once, guard) |
| `TestFinancialPayoutConcurrentApprovals` | PASS | 12 calls: 2 accepted, 10 refused; 2 approvals by 2 checkers; 1 transition |
| `TestFinancialPayoutCancelRacesSubmission` | PASS | 3 rounds, CANCELLED each time, 0 CREATE_PAYOUT |
| `TestFinancialPayoutConcurrentReconciliationRunsRecordOnce` | PASS | 6 runs: 1 reference, 1 exception |
| `TestFinancialPayoutCrossTenantUnderConcurrency` | PASS | 50 requests by another user: all refused |
| `TestFinancialPaymentPollBudgetExhaustionAndLateSuccess` | FAIL before the fix (queried after the horizon) → **PASS** | status kept, 1 reference, 0 queries after exhaustion, late webhook resolved it, 1 success notice |
| `TestFinancialPaymentPollAttemptCap` | PASS | 520 calls → 500 queries |
| `TestFinancialPaymentConcurrentSweepsPollOnce` | PASS (also passed with the due check disabled: race not reproduced) | 8 sweeps: 1 query |
| `TestFinancialPaymentPollErrorClassesNeverFail` | FAIL (test assertion names) → **PASS** | RATE_LIMITED, DEFINITELY_NOT_SENT, OUTCOME_UNKNOWN recorded; UNMAPPED_STATUS reference; 1 SUCCEEDED; SUCCEEDED never re-queried |
| `TestFinancialLedgerConcurrentConflictingPayloads` | FAIL (test bug) → **PASS** | 60 posts, 1 journal, 30 conflicts |
| `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock` | FAIL (86/200, 5 deadlocks, 109 lock timeouts) → **PASS** after F-S19-01 | 200/200, 0, 0, 22.8 s |
| `TestFinancialLedgerConnectionLossAndAmbiguousRetry` | PASS | 57P01; 0 journals; 5 retries → 1 journal |
| `TestFinancialLedgerIntegrityDuringConcurrentPosting` | PASS | 144 integrity runs passed during 1 878 postings |
| `TestFinancialLedgerPoolExhaustionFailsCleanly` | PASS | deadline exceeded; 0 then 1 journal |
| `TestFinancialAuditVerificationInterrupted` | PASS | 4 interrupted runs recorded nothing; full run 1 330 046 rows INTACT in 12.8 s recorded once |
| `TestFinancialPayoutReconciliationInterrupted` | PASS | 4 interrupted runs (checked 0/0/52/336), final run: 1 reference, 1 exception |
| `TestFinancialWorkerCrashMidFlight` (opt-in) | PASS | worker SIGKILLed mid-batch, restarted; 6/6 payouts and 6/6 payments settled once; integrity passed |
| `TestLedgerPeriodCloseMakerCheckerAndPriorPeriodPostings` (KI-S18-24 fixture fix) | PASS | — |
| `TestFinancialLoad` (opt-in) | see benchmark-results.md | — |

Unit tests added: `TestPollJitterStaysWithinBounds`, `TestPollBudgetHorizonBindsBeforeAttemptCap` (438 polls in 72 h).

Additional Stage 19 tests added after the first load run: `TestFinancialPayoutConcurrentStatusQueries` — FAIL before
F-S19-12 (21 ok / 43 failed / 68 deadlocks / 1 stranded IN_FLIGHT) → **PASS** (64/64, 0 deadlocks, unique attempt
numbers); `TestFinancialReconciliationVolumeSample` (opt-in) — PASS (23 612 exceptions opened in 83 s, seen again in
46 s).

## 2. Full suites

| Suite | Command | Result |
|---|---|---|
| gofmt / vet | `gofmt -l apps internal migrations tests`; `go vet` (default, `integration`, `perf`) | clean |
| Go unit, race | `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` in `golang:1.27.3` (no cgo toolchain on the host) | **39 packages ok**, no data race |
| gosec | `gosec@v2.29.0 -quiet -exclude-dir=apps/web ./apps/api/... ./internal/... ./migrations/...` | exit 0, no issue |
| OpenAPI | `@redocly/cli@2.54.3 lint` | valid (2 pre-existing warnings) |
| Secrets | `scripts/check-secrets.sh` | no secrets in 1 525 files (gitleaks not installed; the CI job runs it) |
| Go integration (full) | `FUNDZIM_IT_API_URL=http://127.0.0.1:8080 go test -tags integration -count=1 -timeout 120m -v ./tests/integration/...` (after both load runs) | **221 passed, 7 failed, 15 skipped** (opt-in and environment-dependent). Failures analysed in failure-analysis.md rows 19–24: 1 environmental (notification backlog), 1 test-isolation defect (fixed), 1 application defect (F-S19-13, fixed), 3 cascading from F-S19-14 (fixed), 1 local volume (KI-S19-17) |
| Affected suites on the final code | `-run '^(TestLedger\|TestReconciliation\|TestFee\|TestFinancial)'` after the last fixes and a stack rebuild | **54 passed, 1 failed, 4 skipped** (opt-in). The failure is `TestReconciliationPaymentsEndToEnd` (KI-S19-17: local volume left by the load runs; not masked) |
| Previously failing, rerun | the 7 tests of the full run | 6 passed; `TestReconciliationPaymentsEndToEnd` fails locally as above |
| Web unit / mock E2E / real-stack E2E | not run locally: no web or API-contract behaviour changed except one response enum value | remote CI (§3) |

## 3. Remote CI

Recorded after push (§3 is completed in the final report with the observed run).
