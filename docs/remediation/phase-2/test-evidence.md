# Remediation phase 2 — tests executed and results (phase I)

Every result below was executed in this phase and its output captured in `evidence/`. Code commit under test: **`efa7b62`**.
The later Phase 2 commits change documentation only; their CI is reported in the final report.

## 1. Results

| Check | Where | Commit | Result | Evidence |
|---|---|---|---|---|
| Go unit tests with `-race` (`./apps/api/... ./internal/... ./migrations/...`) | `golang:1.27.3` container | `efa7b62` | **40 packages ok, 0 FAIL** (16 without tests) | `go-race-efa7b62.log.txt` |
| deploytest including the new `TestWorkflowRequiredChecks` and the image-tag assertion | host | `efa7b62` | pass; **mutation check**: renaming a job and restoring a `:local` tag each made the test fail, then the files were restored | session output (summarised in the final report) |
| reconsweep unit tests | host (review agent) | `c8fb8c6` (no reconciliation code changed since) | 10/10 pass | reconciliation-review.md |
| Web unit tests / lint / typecheck | host, Node 24.21.0 | `efa7b62` | **578/578 tests (61 files)**; lint exit 0; typecheck exit 0 | `web-efa7b62.log.txt` |
| Integration suite (`-tags integration`, `FUNDZIM_IT_API_URL`, `FUNDZIM_IT_PERF=1`) | **disposable compose project `fundzim-r2`**, fresh volumes, images built from `efa7b62` (`/api/v1/version` reported that commit) | `efa7b62` | **243 pass, 0 fail, 7 skip** (25 min) | `integration-fundzim-r2-efa7b62.log.txt`, `…-results.txt` |
| Destructive migration round trip (`TestMigrationsAppliedAndRepeatable`, Down to 0, then Up) | `fundzim-r2` database after stopping its API and worker | `efa7b62` | pass | same log |
| Release-candidate manifest | clean worktrees | `c8fb8c6`, `efa7b62`, `a0a687d` | `ELIGIBLE_FOR_REVIEW` (runs 40279994161 and 40290159925); `REJECTED` for `a0a687d` (no CI run); `REJECTED` for a dirty tree | `rc-manifest-*.json` |
| Remote CI | GitHub Actions | `efa7b62` | **run 40290159925: success, 8/8 jobs** | rc-manifest-efa7b62.json (`ci.checks`) |
| Remote CI | GitHub Actions | `c8fb8c6` | run 40279994161: success, 8/8 | rc-manifest-c8fb8c6.json |
| Secret scan (`scripts/check-secrets.sh`) | host | before each commit | exit 0 | — |

**Skipped integration tests.** All 7 are opt-in by design, the same set as Phase 1:
- `TestFinancialLoad` and `TestFinancialReconciliationVolumeSample` (`FUNDZIM_IT_S19_LOAD`);
- `TestFinancialWorkerCrashMidFlight` (kill and start commands);
- `TestDistributedLoginLimitAcrossReplicas` (scale-test profile);
- `TestFrontendReachesBackend` (`FUNDZIM_IT_WEB_URL`);
- `TestRateLimitFallbackWhenValkeyContainerStopped` (`FUNDZIM_IT_STOP_VALKEY`);
- `TestReconciliationCoverageVolume` (`FUNDZIM_IT_R1_VOLUME`).

They were not run. No reconciliation or financial code changed in Phase 2, and Phase 1 ran the volume tests on `a0a687d` and
`8baee4e`.

**Not run in Phase 2:**
- the ledger volume tiers (scratch DB; Phase 1, unchanged code);
- local real-stack Playwright (covered by CI job `Real-stack E2E`, success on `efa7b62`);
- local container scanning (CI job `Container images`: Trivy and syft, success on `efa7b62`).

## 2. Financial regressions on `efa7b62` (all pass, `fundzim-r2`)

| Regression | Tests |
|---|---|
| KI-S18-25 (payout / reservation; the ledger follows the payout state) | `TestFinancialPayoutCompletionWindowVersusReconciliation`, `…ConcurrentReconciliationRunsRecordOnce`, `…ConnectionLostBetweenJournalAndConsumption`, `…LedgerRefusalIsStillReported`, `…ReconciliationInterrupted`, `TestFinancialReservationInvariantsInTheDatabase` |
| KI-S18-22 (bounded polling) | `TestFinancialPaymentPollBudgetExhaustionAndLateSuccess`, `…PollAttemptCap`, `…ConcurrentSweepsPollOnce`, `…PollErrorClassesNeverFail` |
| F-S19-01 (ledger lock order) | `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock`, `…IntegrityDuringConcurrentPosting`, `…ConcurrentConflictingPayloads`, `…ConnectionLossAndAmbiguousRetry`, `…PoolExhaustionFailsCleanly` |
| F-S19-12 (payout attempts) | `TestFinancialPayoutConcurrentStatusQueries`, `…ConcurrentApprovals`, `…CancelRacesSubmission`, `…CrossTenantUnderConcurrency` |
| Reconciliation coverage (historical, interruption, concurrency, guards) | `TestReconciliationCoveragePayoutOutsideWindow`, `…FeeOutsideWindow`, `…LedgerSweeps`, `…ConcurrentAndInterrupted`, `…CheckpointGuards` |
| Families | `TestFinancial*` 20 pass / 3 opt-in skips; `TestPayout*` 12/12; `TestLedger*` 13/13; `TestFee*` 13/13 |

## 3. Development environment around the test run

| Time (UTC) | Step | Observed |
|---|---|---|
| 08:09:52 | before | dev images `fundzim/api:fundzim` = `sha256:adfb403d…`, `fundzim/web:fundzim` = `sha256:9c4e1ccd…` |
| 08:09:52 | dev project stopped with `down` **without** `-v` | volumes kept |
| 08:11:41 | `fundzim-r2` built and started | its own image `fundzim/api:fundzim-r2` = `sha256:239b804d…`; the dev image was still `sha256:adfb403d…` |
| 08:37:08 | `fundzim-r2` removed with `-v` | dev images **unchanged** (`adfb403d…`, `9c4e1ccd…`) |
| 08:37:30 | dev project restarted with `--no-build` | `fundzim-migrate`: `no pending migrations`; `/api/v1/ready`: ready; api, worker and web running on the `:fundzim` images |

The dev worker's interrupted payout cycle resumes from its saved checkpoint after the restart, as the sweep design intends.
That resumption was not separately measured.
