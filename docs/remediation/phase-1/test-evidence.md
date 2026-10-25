# Remediation phase 1 — tests and evidence

Every result below was produced in this session on the commit named in [README.md](README.md) §Evidence; logs are preserved in
`evidence/` with `SHA256SUMS`. Database tests ran only on the disposable compose project `fundzim-r1` (fresh volumes) or a
scratch `fundzim_perf_r1` database inside it — never on the development database.

## 1. The brief's 25 cases → tests

Unit = `internal/platform/reconsweep/sweep_test.go` (deterministic in-memory store with the SQL store's semantics; fault and
interleaving injection). IT = `tests/integration/reconciliation_coverage_test.go` (real PostgreSQL, real services, real
simulator). Scratch = `tests/perf/ledger/recon_sweep_perf_test.go` (scratch database). Volume =
`tests/integration/reconciliation_coverage_volume_test.go` (opt-in, after a load run).

| # | Case | Covered by |
|---|---|---|
| 1 | more records than the batch limit | Unit `TestSweepCoversEveryRecordAcrossRuns` (12 345 records, batch 100, 1 000/run); Scratch (20 000 / 100 000 journals); IT walks with batch 10–50 |
| 2 | records older than the initial window | IT payouts/fees/ledger with a recency window of **1**; Scratch planted findings in the oldest records |
| 3 | discrepancy introduced in an old record after a successful run | Unit `TestSweepReexaminesChangedRecordsNextCycle`; IT `…PayoutOutsideWindow` (provider status changed after a clean cycle), `…FeeOutsideWindow` (calculation tampered after a clean cycle), `…LedgerSweeps` (payment fact changed after its pair was reconciled) |
| 4 | record updated while a run is active | Unit `TestSweepRecordsCommittedDuringACycle` (changed behind the cursor → next cycle) |
| 5 | new record committed near a checkpoint boundary | Unit `TestSweepRecordsCommittedDuringACycle` (key right after the checkpoint key; key below the cursor) |
| 6 | identical timestamps | not applicable by construction: the walk orders by primary key only; no timestamp is compared (ADR-052 §1) |
| 7 | crash before a batch completes | Unit `TestSweepFailureBeforeCheckpointNeverSkips` (examine error mid-batch); IT `…ConcurrentAndInterrupted` (deadlines expiring mid-batch); Scratch (random 50–449 ms deadlines) |
| 8 | crash after processing, before checkpoint advancement | Unit `TestSweepFailureBeforeCheckpointNeverSkips` (checkpoint write fails) |
| 9 | crash after checkpoint advancement | Unit `TestSweepCrashAfterCheckpoint` |
| 10 | database failure during a batch | Unit (examine error, key query error, checkpoint write error); IT deadlines (real cancelled queries) |
| 11 | worker restart | Unit (fresh `Run` after failures resumes from the store); IT/Scratch (each run is a new call reading the persisted checkpoint) |
| 12 | two workers on overlapping work | Unit `TestSweepConcurrentRunsNeverSkipOrDuplicate` (forced interleaving: both examine, one advances, the other stops CHECKPOINT_CONFLICT); IT `…ConcurrentAndInterrupted` (two services, separate connection pools, same scope) |
| 13 | duplicate execution of one batch | Unit (same) — the finding is recorded once; IT (same finding, one reference, one exception) |
| 14 | persistent discrepancy across runs | Unit `…ReexaminesChanged…`; IT payouts (two more cycles add nothing), fees (third cycle adds nothing) |
| 15 | discrepancy resolved through the approved workflow | IT `…PayoutOutsideWindow`: cause corrected at the provider, exception resolved via `ResolveException` (PROVIDER_CONFIRMED) → later cycles record nothing |
| 16 | new discrepancy on a previously reconciled record | IT payouts: AMOUNT_MISMATCH added while the PROVIDER_STATUS_MISMATCH exception is open → one new reference, no second exception (`open_exception_evidence`); ledger: amount fact changed on a reconciled pair → AMOUNT_MISMATCH |
| 17 | genuine ledger inconsistency | Scratch: cross-batch DUPLICATE_POSTING, AMOUNT_MISMATCH; IT ledger: AMOUNT_MISMATCH, MISSING_POSTING, UNKNOWN_OUTCOME; (F-R1-02: L6 itself detected the planted duplicate on the disposable database) |
| 18 | valid payout/reservation transition | IT payouts: completed + consumed payouts produce no finding over many cycles; the Stage 19 completion-window tests rerun (§2) |
| 19 | invalid payout/reservation final state | existing `TestPayoutReconciliationAndExceptionWorkflow` and Stage 19 `TestFinancialPayoutReconciliation*` rerun (§2); the sweep uses the same `reconcileOne` |
| 20 | duplicate financial event | Scratch DUPLICATE_POSTING; Stage 19 load run (§3) asserts zero duplicate effects |
| 21 | large historical dataset | Scratch 20 000 and 100 000 journals; Volume after the load run |
| 22 | empty scope | Unit `TestSweepEmptyScope` |
| 23 | record changes from eligible to ineligible during processing | Unit `TestSweepDeferralsAreCarriedUntilExamined` (`gone` → OutOfScope); IT payment fact sweep treats statuses outside SUCCEEDED/UNKNOWN as out of scope |
| 24 | run exceeding its execution budget | Unit `TestSweepTimeBudget`, `TestSweepBudgetAlwaysMakesProgress` |
| 25 | resumption after a partially completed historical sweep | Unit `TestSweepCoversEveryRecordAcrossRuns` (resume assertions); IT/Scratch (multi-run cycles) |

Deferrals (grace period) — Unit `TestSweepDeferralsAreCarriedUntilExamined` (carried across batches, runs, cycles; fail closed
at the limit); IT ledger (`hot` payment deferred, judged on the run after it left its grace period).

Checkpoint guards as runtime roles — IT `TestReconciliationCoverageCheckpointGuards` (second RUNNING cycle, cursor backwards,
cursor reset, counter decrease, version skip/kept, scope change, delete, change/reopen of a completed cycle: all refused; grants
between `fundzim_app` and `fundzim_ledger`).

## 2. Results

All runs below were executed in this session. Logs are in `evidence/` (hashes in `evidence/SHA256SUMS`).

| Run | Commit | Environment | Result | Log |
|---|---|---|---|---|
| Unit suite, `-race` (golang:1.27.3 container), gofmt, vet | `97b8a70` | — | **40 packages ok**, gofmt and vet clean | `unit-race.log.txt` |
| `reconsweep` unit tests, `-race -count=3` | `97b8a70` | — | pass (all 10 tests, 3 times) | in `unit-race.log.txt` |
| Coverage ITs, first attempt | working tree before `97b8a70` | `fundzim-r1` | 5/5 pass, **but** `…ConcurrentAndInterrupted` logged 0 interrupted runs (deadline too long) → test changed to graded deadlines and asserts interrupted > 0 | `it-coverage-1.log.txt` |
| Coverage ITs, second attempt | working tree before `97b8a70` | `fundzim-r1` | **FAIL** `…ConcurrentAndInterrupted`: `422 PAYOUT_NOT_ELIGIBLE (PAYOUTS_UNAVAILABLE)` — F-R1-02: the ledger IT's planted duplicate journal broke L6 for good, EC-19 refused every payout. Fixture removed, `fundzim-r1` recreated from empty volumes | `it-coverage-2.log.txt` |
| Full integration suite, first run | `97b8a70` (pre-cleanup) | `fundzim-r1` (fresh) | **233 pass, 10 fail, 6 skip**. Root failure: `TestPayoutApprovalSeparationOfDutiesAndInvalidation` races the compose worker (KI-R1-07). It left the shared fixture destination SUSPENDED, and the other 9 failures were EC-12 `DESTINATION_HOLD` refusals in later payout-requesting tests. Fix: `t.Cleanup` reinstates the destination; `fundzim-r1` recreated | `it-full-1-first-run.log.txt` |
| Full integration suite, final | `97b8a70` | `fundzim-r1` (fresh) | **242 pass, 1 fail, 7 skip** (skips are opt-in or environment tests: `TestFinancialLoad`, `TestFinancialReconciliationVolumeSample`, `TestFinancialWorkerCrashMidFlight`, `TestReconciliationCoverageVolume`, `TestDistributedLoginLimitAcrossReplicas`, `TestFrontendReachesBackend`, `TestRateLimitFallbackWhenValkeyContainerStopped`). The failure was `TestDiscoveryEndToEnd`: token `zqfgbbzhsrjpg` had equal letters 5 and 6 (KI-R1-08). `…ConcurrentAndInterrupted`: 849 payouts, 6 interrupted runs, cycle complete | `it-full-2.log.txt` |
| `TestDiscoveryEndToEnd -count=5` | `8baee4e` | `fundzim-r1` | 5/5 pass | `it-discovery-rerun.log.txt` |
| Ledger sweep volume (scratch), first attempt | `8baee4e` | `fundzim_perf_r1` | **timed out (2 h)**. 20 003 journals loaded, then every interrupted run used a fixed 50–449 ms deadline, shorter than one 1 000-journal batch, so no run ever advanced the checkpoint. This is safe (no record skipped) but makes no progress. Recorded as KI-R1-09; the test now scales its deadlines to the measured run time | `perf-ledger-sweep-1-timeout.log.txt` |
| Ledger sweep volume (scratch), rerun | `8baee4e` + test patch (committed afterwards) | `fundzim_perf_r1b` (fresh) | **pass** — 20 000 and 100 000 journals: planted findings found once each; interrupted cycles (7 and 13 interrupted runs) examined every journal; figures in [performance.md](performance.md) | `perf-ledger-sweep-2.log.txt`, `ledger-recon-sweep.md` |
| Payout/fee coverage volume | `8baee4e` | `fundzim-r1` after the full suite | **pass** (853 payouts, 475 fee assessments; one cycle each, examined = in scope). The EXPLAIN output revealed **F-R1-06** (text-key sort) | `it-coverage-volume.log.txt` (report in the log) |
| Unit suite, `-race`, gofmt, vet | `a0a687d` (F-R1-06 fix) | — | **40 packages ok**, exit 0 | `unit-race-2.log.txt` |
| **Full integration suite, final candidate** | `a0a687d` | `fundzim-r1` recreated: fresh volumes, images rebuilt | **233 pass, 0 fail, 17 skip** (opt-in variables not set) | `it-full-3.log.txt` |
| The 10 opt-in tests that run with `FUNDZIM_IT_PERF=1` / `FUNDZIM_IT_API_URL` | `a0a687d` | same stack | **10/10 pass**. Together with the line above: **243 pass, 0 fail, 7 skip** (the same 7 opt-in/environment tests as the `97b8a70` run) | `it-full-3-optin.log.txt` |
| Payout/fee coverage volume, production statements | `a0a687d` | same stack, run right after the suite (45 payouts, 75 assessments) | **pass**; keyset reads use the uuid key (index range scans; the payment_intents read is a seq scan on 41 rows); no text-key sort | `it-coverage-volume-2.log.txt`, `coverage-sweep-volume-2.md` |

### Financial regressions (phase H): pass in the full runs on `97b8a70` and again on `a0a687d`

| Stage 19 / 18 item | Tests rerun | Result |
|---|---|---|
| KI-S18-25 (payout/reservation race; ledger follows the payout state) | `TestFinancialPayoutCompletionWindowVersusReconciliation`, `…ConcurrentReconciliationRunsRecordOnce`, `…ConnectionLostBetweenJournalAndConsumption`, `…LedgerRefusalIsStillReported`, `…ReconciliationInterrupted`, `TestFinancialReservationInvariantsInTheDatabase`, `TestPayoutReconciliationAndExceptionWorkflow` | pass |
| KI-S18-22 (bounded payment polling) | `TestFinancialPaymentPollBudgetExhaustionAndLateSuccess`, `…PollAttemptCap`, `…ConcurrentSweepsPollOnce`, `…PollErrorClassesNeverFail` | pass |
| F-S19-01 (ledger lock order) | `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock`, `…IntegrityDuringConcurrentPosting`, `…ConcurrentConflictingPayloads`, `…ConnectionLossAndAmbiguousRetry`, `…PoolExhaustionFailsCleanly` | pass |
| F-S19-12 (payout attempt numbering) | `TestFinancialPayoutConcurrentStatusQueries`, `…ConcurrentApprovals`, `…CancelRacesSubmission`, `…CrossTenantUnderConcurrency` | pass |
| Other reconciliations | `TestLedgerReconciliationFindsExceptionsAndWorkflow`, `TestFeeReconciliationAndRevenueReport`, all `TestReconciliation*` (Stage 17) | pass |

**Not run:** `TestFinancialLoad`, `TestFinancialWorkerCrashMidFlight` and `TestFinancialReconciliationVolumeSample` are opt-in and were not run in this phase (load at volume is a phase-2 or capacity item; see performance.md). No result is claimed for them.

### CI

Remote CI on the branch: see [phase-1-final-report.md](phase-1-final-report.md) §14. It is reported only as observed.
