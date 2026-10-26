# Remediation phase 3 — tests executed and results

Every result below was executed in this phase. Logs are in `evidence/` (with `SHA256SUMS`).

**Commits:**
- `01ea615`: the corrections;
- `9ed4974`: test-fixture amount fix;
- `f85192d`: security alert scan race fix, the final code commit;
- later commits are documentation only.

**Environment:**
- Go 1.27.3; race tests in the `golang:1.27.3` container;
- disposable compose projects `fundzim-r3`, `fundzim-r4` (aborted, see §3) and `fundzim-r5`, each with fresh volumes and its own
  image tags (`fundzim/api:<project>`), `APP_ENV=development`, `*_EXECUTION_ENVIRONMENT=LOCAL_SIMULATOR`, and no production
  secret reference in `.env`;
- each was removed with `-v` afterwards (0 volumes left);
- the development project was stopped without `-v` during the runs and restarted with `--no-build`. Its image IDs were
  identical before and after, and `fundzim-migrate` reported `no pending migrations`. The new migration was never applied to
  the shared development database.

## 1. Results

| Check | Commit / environment | Result | Evidence |
|---|---|---|---|
| F-R2-01 unit: `TestMissingFindingsReportsEveryMissingCategory` (8 cases: complete, one, two, several missing; open finding plus a newly missing type; duplicate component; NOT_CONFIGURED; inconsistent group), `TestDedupeKeysSeparateComparisons`, `TestLegacyCheckFromStoredContent` | host, `01ea615` | pass. **Mutation:** removing the category from the key failed the one-, two- and several-missing cases | session output |
| F-R2-02 unit: `TestJudgeProviderOnlyComparesAuthoritativeAnswers` (18 observations: consistent, amount and status mismatch, NOT_FOUND ×2, timeout, connection loss, 5xx, malformed, incomplete, rate limited, retryable, auth/config, unsupported, invalid, definite failure, unclassified) | host, `01ea615` | pass | session output |
| Engine: 10 existing reconsweep tests + `TestSweepFailingChecksNeverStopTheWalk`, `TestSweepSlowChecksRespectTheTimeBudget`, `TestSweepRecordsEveryAttempt`, `TestHealthClassification` (11 states) | host, `01ea615` | 14/14 pass. `TestSweepTimeBudget` expectation updated from 300 to 250 records: the budget now holds inside a batch, and the assertion stays exact. **Mutations:** removing the retry cap failed `…FailingChecks…`; removing the in-batch time check failed `…TimeBudget` and `…SlowChecks…` | session output |
| F-R2-03 alert rules: `TestSecurityFindingsReconciliationSweepHealth` | host, `01ea615` | pass | session output |
| Go unit tests with `-race`, `./apps/api/... ./internal/... ./migrations/...` | container, `01ea615` | **40 packages ok, 0 FAIL** | `go-race-01ea615.log.txt` |
| Go `-race`, `internal/operations` (the only package changed afterwards) | container, `f85192d` | ok | session output |
| `go vet` (incl. integration and perf tags), gofmt | host | clean | — |
| OpenAPI lint (Redocly 2.54.3) | host | valid; 2 warnings, the same 2 as on `be9cfb6` | — |
| Targeted integration: `TestReconciliationCorrections*` (3) + `TestSecurityAlerts*` (2) | `fundzim-r5`, `f85192d` | **5/5 pass** | `targeted-fundzim-r5-f85192d-results.txt` |
| Proof of the scan-race regression test: `TestSecurityAlertsOutOfOrderScanTimes` on the pre-fix code (worktree at `9ed4974` + the new test only) | `fundzim-r5` | **fails as expected** with `ck_ops_security_alerts_counts`, the CI error | `integration-fundzim-r5-f85192d.log.txt` |
| Full integration suite (`FUNDZIM_IT_PERF=1`) | `fundzim-r5`, `f85192d` | **247 pass, 0 fail, 7 skip** (26.6 min) | `integration-fundzim-r5-f85192d{.log,-results}.txt` |
| Historical coverage at volume: `TestReconciliationCoverageVolume` (opt-in `FUNDZIM_IT_R1_VOLUME=1`) | `fundzim-r5`, `f85192d` | pass (35 s) | `coverage-volume-f85192d.log.txt` |
| Destructive migration round trip (Down to 0, Up; includes `20261026120000`) | `fundzim-r5` database | pass | log |
| Remote CI | `01ea615` | run 40300772129: **failure**, 7/8 (Integration: 2 failures, §2) | — |
| Remote CI | `9ed4974` | run 40305203774: cancelled (superseded by the next push) | — |
| Remote CI | `f85192d` | run 40305574851: **failure**, 7/8. Integration: 246 pass, 1 fail (KI-R1-07, §2), 7 skip; Go job (race, gosec, govulncheck) success | `rc-manifest-f85192d-rejected.json` |
| Remote CI | final head (documentation) | see the final report | — |
| Release-candidate manifest | `f85192d` | **REJECTED**: the Integration check failed (the gate works) | `rc-manifest-f85192d-rejected.json` |

**Skipped (opt-in, the same 7 as Phases 1 and 2):** `TestFinancialLoad`, `TestFinancialReconciliationVolumeSample`,
`TestFinancialWorkerCrashMidFlight`, `TestDistributedLoginLimitAcrossReplicas`, `TestFrontendReachesBackend`,
`TestRateLimitFallbackWhenValkeyContainerStopped`, `TestReconciliationCoverageVolume`. The volume test was run separately (above).
No skip is counted as a pass. **Not run:** web tests (no web code changed; CI `Web` job success on `f85192d`) and the ledger
volume tiers (scratch DB; ledger detector code unchanged apart from the PAYMENT_FACTS independence).

## 2. Failures before the final passing runs (all explained)

| Where | Failure | Root cause | Resolution |
|---|---|---|---|
| `fundzim-r3` (targeted and full) and CI 40300772129, `01ea615` | `TestReconciliationCorrectionsPayoutLookupFailure`: "timed out waiting for completed" | Test fixture: amounts 5 001 … 5 005 select the simulator's scenarios by their last two digits (01 PENDING, 02 FAILURE, …), so those payouts never complete. The code under test was never reached | `9ed4974`: amounts ending in 00 (SUCCESS). Passes on `fundzim-r5` |
| CI 40300772129, `01ea615` | `TestSecurityAlertsLifecycle`: concurrent scan violates `ck_ops_security_alerts_counts` | **Latent Stage 18 defect.** `SecurityScan` takes its observation time before serialising on the advisory lock, so concurrent scans can write in the opposite order and set `last_seen_at < first_seen_at`. The test's scanner reads only its own test source, so the Phase 3 metrics were not involved. Timing exposed it in CI; it passed locally on `fundzim-r3` | `f85192d`: `last_seen_at = greatest(…)`, evidence replaced only by a newer observation. The deterministic regression test fails on the pre-fix code and passes after |
| CI 40305574851, `f85192d` | `TestPayoutApprovalSeparationOfDutiesAndInvalidation`: "after destination suspension: SUBMITTED round 1" | **KI-R1-07** (pre-existing, Phase 1): the worker on the CI runner submits the approved payout before the test suspends the destination. Not related to Phase 3; passed in both full disposable runs | Not changed. A deterministic fix needs a lock barrier against the worker's submit path (a redesign of a Stage 11 test); recorded as an open known issue |

## 3. Aborted run

`fundzim-r4` (meant for `9ed4974`) was stopped before its tests ran. The `SecurityScan` fix had been edited into the working
tree after the run started, so its images and tests would have mixed commits. The project was removed with `-v`, and
`fundzim-r5` reran everything from `f85192d`.

## 4. Financial regressions (all pass in the `fundzim-r5` full suite, `f85192d`)

- KI-S18-25: `TestFinancialPayoutCompletionWindowVersusReconciliation`, `…ConcurrentReconciliationRunsRecordOnce`,
  `…ConnectionLostBetweenJournalAndConsumption`, `…LedgerRefusalIsStillReported`, `…ReconciliationInterrupted`,
  `TestFinancialReservationInvariantsInTheDatabase`.
- KI-S18-22: `TestFinancialPaymentPollBudgetExhaustionAndLateSuccess`, `…PollAttemptCap`, `…ConcurrentSweepsPollOnce`,
  `…PollErrorClassesNeverFail`.
- F-S19-01: `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock`, `…IntegrityDuringConcurrentPosting`,
  `…ConcurrentConflictingPayloads`, `…ConnectionLossAndAmbiguousRetry`, `…PoolExhaustionFailsCleanly`.
- F-S19-12: `TestFinancialPayoutConcurrentStatusQueries`, `…ConcurrentApprovals`, `…CancelRacesSubmission`,
  `…CrossTenantUnderConcurrency`.
- Families: ledger integrity and immutability (`TestLedger*`); payment idempotency (`TestPayments*`); payout idempotency,
  reservations and maker-checker (`TestPayout*`); fees (`TestFee*`); audit integrity (`TestSecurity*`, audit chain); tenant
  isolation (cross-tenant cases); reconciliation exceptions (`TestReconciliation*`); state transitions (DB-guarded machine
  tests).
- Financial gate contract (`internal/archtest/financial_gates_test.go`): unit run, pass.

## 5. What the new integration tests prove (dataset: synthetic TEST records of the disposable database)

**`TestReconciliationCorrectionsFeeEveryMissingType`**: 5 synthetic donation groups and 7 added components. It checks exact
open MISSING_FEE categories and keys for:
- a complete group, and groups missing one, two and three types;
- repeat runs;
- an open finding for one type plus a newly missing type;
- two concurrent workers;
- one type fixed and resolved while the other remains;
- a resolved-but-persisting discrepancy recorded again exactly once;
- a pre-fix legacy-key open finding (no duplicate, no suppression);
- an AMOUNT_MISMATCH calculation-component finding next to a legacy provider-report finding;
- one-record recency window, so the groups are judged by the sweep across window boundaries;
- the run outcome recorded with 0 failed attempts.

**`TestReconciliationCorrectionsPayoutLookupFailure`**: 6 completed TEST payouts, batch size 2, so checkpoint boundaries fall
between payouts. Five lookups fail (timeout, connection loss, rate limit, malformed, incomplete) and one works. It checks:
- every cause is reported;
- the five are carried as deferrals and the sixth is not;
- every payout is examined;
- no exception opens from a failure;
- payout status, reservation and attempt count are unchanged throughout;
- no CREATE or CANCEL call (the fault adapter fails the test on any);
- repeated failures and a "restart" (a fresh service on the same scope) keep the deferrals and their first time;
- two concurrent workers keep each deferral once;
- health is PROGRESSING with 0 failed attempts and outstanding deferrals;
- after recovery, every deferral resolves, the one genuine amount inconsistency is recorded exactly once with one exception,
  and a further run adds nothing.

**`TestReconciliationCorrectionsUnknownPayoutLookupFailure`**: an UNKNOWN payout whose lookup keeps failing stays UNKNOWN,
keeps its BLOCKED reservation and gets no new CREATE_PAYOUT attempt. It is retried and carried.
