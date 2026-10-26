# Remediation phase 4 — tests executed and results

Every result below was executed in this phase. Logs are in `evidence/` (with `SHA256SUMS`).

**Commits.**

| Commit | Content |
|---|---|
| `30eb045` | code: checkpoint integrity, KI-R1-07 barrier |
| `e4f8c24` | tests |
| `f7d1cf7` | tests; the **final code commit** |

Production code is identical in `30eb045`, `e4f8c24` and `f7d1cf7`: the later commits change only `tests/`.

**Environment.**
- Disposable compose projects `fundzim-r6`, `fundzim-r7` and `fundzim-r8`.
- Each was checked for isolation before running:
  - project name;
  - its own image tags `fundzim/{api,web}:<project>`;
  - fresh volumes, 0 before;
  - `APP_ENV=development`;
  - `LOCAL_SIMULATOR` only;
  - 0 production secret references.
- Each was removed with `-v` afterwards (0 volumes left).
- The development project was stopped without `-v` and restarted with `--no-build`. Its image IDs were identical before and
  after every run, `fundzim-migrate` reported `no pending migrations`, and its database's highest migration stayed
  **`20261025090000`** (read-only query after each run). Neither `20261026120000` nor `20261027090000` was applied to the
  shared development database.
- The ledger volume harness ran on the scratch database `fundzim_perf_r6` inside `fundzim-r6`.

## 1. Results on the final code commit `f7d1cf7` (`fundzim-r8`)

| Check | Result | Evidence |
|---|---|---|
| **KI-R1-07 repeatability:** `-count=20 -run '^TestPayoutApprovalSeparationOfDutiesAndInvalidation$'` against the live compose worker (it submits APPROVED payouts every 2 s) | **20/20 pass**. In these 20 runs the worker made no submission attempt inside a barrier window (0 intercepted), so the repeats show the outcome is stable but do not themselves exercise the interception. The deterministic test below does | `ki-r1-07-repeat-fundzim-r8-f7d1cf7.log.txt` |
| Targeted: `TestReconciliationIntegrity*` (4), `TestReconciliationCorrections*` (3), `TestPayoutSubmissionCommittedBeforeDestinationSuspension`, `TestPayoutDecisionsBarrierStopsASubmissionInTheWindow`, `TestReconciliationCoverageCheckpointGuards` | **10/10 pass**. The barrier test logged an actual interception ("terminated a submission attempt blocked at the decision insert") | `targeted-fundzim-r8-f7d1cf7-results.txt`, log |
| Full integration suite (`FUNDZIM_IT_PERF=1`) | **253 pass, 0 fail, 7 skip** (27.5 min) | `integration-fundzim-r8-f7d1cf7{.log,-results}.txt` |
| `TestReconciliationCoverageVolume` (opt-in, Phase 1 historical coverage) | pass | `coverage-volume-f7d1cf7.log.txt` |
| Destructive migration round trip (Down to 0 and Up, including `20261026120000` and `20261027090000`) | pass | log |

**Skipped (opt-in, the same 7 as Phases 1–3):** `TestFinancialLoad`, `TestFinancialReconciliationVolumeSample`,
`TestFinancialWorkerCrashMidFlight`, `TestDistributedLoginLimitAcrossReplicas`, `TestFrontendReachesBackend`,
`TestRateLimitFallbackWhenValkeyContainerStopped`, `TestReconciliationCoverageVolume`. The last was run separately (above).
A skip is never counted as a pass.

## 2. Results on `30eb045` (production code identical to `f7d1cf7`)

| Check | Result | Evidence |
|---|---|---|
| Go unit tests with `-race`, `./apps/api/... ./internal/... ./migrations/...` | **40 packages ok, 0 FAIL** | `go-race-30eb045.log.txt` |
| New unit tests (host) | pass: `TestVerify*` (6), `TestSweep*` (13), `TestHealthClassification`, `TestSecurityFindingsReconciliationSweepIntegrity`. **Mutations:** disabling the RECORD_SKIPPED rule failed `TestVerifyDetectsAWalkThatSkipsARecord`; disabling the COMPLETED_EARLY rule failed `TestVerifyDetectsACycleCompletedEarly` | session output |
| Ledger volume harness, 20 000 and 100 000 journals (`fundzim_perf_r6`) | **pass**: planted findings found once; every run's journal integrity VERIFIED or INCOMPLETE; standalone verification VERIFIED (69 ms / 297 ms); VERIFIED after the interrupted cycle. Figures in performance.md | `ledger-recon-sweep-30eb045.md`, `perf-ledger-30eb045.log.txt` |
| `go vet` (default, `integration`, `perf`, the payments perf overlay), gofmt, secrets scan | clean | — |
| OpenAPI lint | not re-run in Phase 4 (only a description text changed); CI `contracts` job | CI |

## 3. Failures before the final runs (all explained; none hidden)

| Run | Failure | Root cause | Resolution |
|---|---|---|---|
| `fundzim-r6` (`30eb045`), 20-run repeat | 19/20 failed at setup: `uq_users_staff_personal_user_id` | The test links a staff account to the **shared** owner fixture and never unlinked it; a second run on the same database could not link again. A pre-existing non-idempotency, exposed only by repetition | `e4f8c24`: unlink in cleanup (the migrator's documented fixture exception) |
| `fundzim-r6` full suite | `TestReconciliationCoverageCheckpointGuards`: "position change to batch 0 without evidence" | The Phase 1 test's **fixture** advanced a checkpoint with a raw UPDATE, the very move Phase 4 now refuses by design | `e4f8c24`: the fixture's advance and completion are evidenced single statements. Every guard assertion is kept. "counter decreased" now decreases from the evidenced value (examined 1 → 0; before, 10 → 5). New assertion: an unevidenced advance is refused |
| `fundzim-r7` (`e4f8c24`), 20-run repeat | 15/20 failed with HTTP 429 at the owner's step-up | `auth.step_up` is throttled per user across test servers; the shared owner stepped up on every run | `f7d1cf7`: step up only when the read asks for it (`STEP_UP_REQUIRED`), the pattern `p.request` already uses; the 200 assertion is kept |
| `fundzim-r7` targeted and full | `TestPayoutDecisionsBarrierStopsASubmissionInTheWindow`: "the submission attempt never reached its decision insert" | **A defect in the barrier helper.** It observed blocked backends as `fundzim_migrator`, and `pg_stat_activity` hides other roles' query text (`<insufficient privilege>`), so no blocked insert was ever recognised. The earlier passing repeats therefore passed without any interception: the worker had not entered a window | `f7d1cf7`: the barrier observes through the superuser connection it already uses for termination. Interception is now proven (log above) |

## 4. Financial regressions (all pass in the `fundzim-r8` full suite, `f7d1cf7`)

| Area | Tests |
|---|---|
| KI-S18-25 | `TestFinancialPayoutCompletionWindowVersusReconciliation`, `…ConcurrentReconciliationRunsRecordOnce`, `…ConnectionLostBetweenJournalAndConsumption`, `…LedgerRefusalIsStillReported`, `…ReconciliationInterrupted`, `TestFinancialReservationInvariantsInTheDatabase` |
| KI-S18-22 | `TestFinancialPaymentPollBudgetExhaustionAndLateSuccess`, `…PollAttemptCap`, `…ConcurrentSweepsPollOnce`, `…PollErrorClassesNeverFail` |
| F-S19-01 | `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock` and the four other ledger concurrency tests |
| F-S19-12 | `TestFinancialPayoutConcurrentStatusQueries`, `…ConcurrentApprovals`, `…CancelRacesSubmission`, `…CrossTenantUnderConcurrency` |
| Phase 3 (F-R2-01/02/03, F-R3-01) | `TestReconciliationCorrections*` (3), `TestSecurityAlertsOutOfOrderScanTimes`, `TestSecurityAlertsLifecycle` |
| Phase 1 coverage | `TestReconciliationCoverage*` (5) and the volume test |
| Families | `TestLedger*`, `TestPayments*`, `TestPayout*`, `TestFee*`, `TestSecurity*` (audit integrity, grants), `TestOperations*`, cross-tenant cases |

## 5. Not run in Phase 4

- Web tests: no web code changed; the CI `Web` jobs cover them.
- Local real-stack Playwright: covered by CI `Real-stack E2E`.
- Payout and fee sweeps at load volume: as in Phases 1–3.
- The opt-in load and crash tests.

## 6. Remote CI

See the final report: run ids and results for the final code commit `f7d1cf7` and for the documentation head.
