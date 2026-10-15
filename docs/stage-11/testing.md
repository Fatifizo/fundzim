# Stage 11 — Testing

All results below were produced in this session on 2026-10-14 (local stack). Remote CI results are in §5.

## 1. Unit tests

- `internal/payouts`: `TestEvaluateFullyEligible`, `TestEvaluateEachBlockingCondition` (41 cases: every check's failure,
  review and deferral path with its outcome and reason category), `TestEvaluateTiersFailClosed`,
  `TestEvaluateAcknowledgedReviewsAtSubmission`, `TestReasonCategoriesNeverRevealComplianceOrRisk`,
  `TestOwnerViewHidesInternals`, `TestThrottleUsesThePayoutRequestPolicyPerUser` (+ the Stage 5 destination test).
- `internal/ledger`: payouts-sourced journal with non-SIMULATED evidence refused in a TEST ledger.
- `internal/platform/config`, `internal/platform/ratelimit`, `internal/archtest` (payouts allowed imports and table ownership).
- `go test -race -count=1 ./apps/api/... ./internal/... ./migrations/...` in `golang:1.27.2`: **34 packages ok**.

## 2. Integration tests (`tests/integration/payouts_test.go`, real stack)

| Test | What it proves |
|---|---|
| `TestPayoutHappyPathReserveApproveSubmitComplete` | TEST_ONLY preview (production BLOCKED); request → reservation (payable/pending exact) → DUAL (unconfigured limit) → same approver refused twice → second approver → pre-submit → submit → simulator completion; reservation CONSUMED; journals reserved/submitted/completed only; owner view hides approvers; every payout journal TEST/SIMULATED/payouts; ledger integrity passes |
| `TestPayoutHundredConcurrentRequestsReserveOnce` | 100 concurrent requests ⇒ 1 accepted, 99 PAYOUT_IN_PROGRESS, one reservation; cancel releases; over-allocation refused; HTTP rate limit (429) |
| `TestPayoutUnknownOutcomeKeepsReservationAndIsNeverResubmitted` | timeout ⇒ UNKNOWN, reservation BLOCKED, exception; dispatcher finds nothing; DB refuses a second CREATE_PAYOUT and the release; cancel refused; "not found" within the finality window stays UNKNOWN, after it FAILED; funds back |
| `TestPayoutSimulatorScenarios` | all 13 scenarios (results in [payout-simulator.md](payout-simulator.md)) incl. WORKER_RESTART ⇒ UNKNOWN without resend |
| `TestPayoutApprovalSeparationOfDutiesAndInvalidation` | linked FINANCE account refused (HTTP and SQL); destination verifier refused (SQL); COMPLIANCE 403; APPROVED without approvals refused; destination suspension ⇒ new round; amount immutable; re-approval; staff cancel releases |
| `TestPayoutEligibilityRefusalsAndPreSubmitRecheck` | insufficient funds refused with a decision record; another user 404 (request and preview); ZWG, unknown destination, float amount refused; campaign paused after approval ⇒ back to review + COMPLIANCE_HOLD exception; owner cancel; step-up required |
| `TestPayoutDatabaseGuards` | PRODUCTION, non-test payout, SUBMITTED without a PRE_SUBMIT decision, illegal transition, deletes, history edits, consuming a reservation, a second in-flight payout, app-role ledger writes, payouts journal with real evidence in TEST — all refused |
| `TestPayoutFailureInjection` | ledger unreachable ⇒ refused, nothing written; ledger lock contention ⇒ PAYOUT_REQUESTED without reservation, recovered by the job; declared provider outage ⇒ submission deferred, then completes after resolution |
| `TestPayoutReconciliationAndExceptionWorkflow` | provider/state mismatch ⇒ SEV1 reference + exception; rerun no duplicate; investigate/resolve over HTTP; detection fields immutable; payout unchanged; staff views 200, SUPPORT 403 |

Run (after rebuilding the containers so the compose worker carries the payout handlers):
`flock /tmp/claude-1000/fundzim-stack.lock go test -tags integration -run TestPayout ./tests/integration/` — **all 9 passed**
(`TestPayoutsPerf` skips unless `FUNDZIM_IT_PERF=1`). The full integration suite was run as well; its only failure was a
race in this file's RETURNED assertion (the status commits before the derived journal), fixed by waiting for the journal
(ADR-043 §4) and re-run green.

The compose worker shares the development database and also submits, polls and delivers simulator webhooks; the tests are
written to accept either process acting (they wait for states, not for which process won).

## 3. Web

- Typecheck and lint clean; vitest **51 files, 495 tests passed** (payout normalisers, money, components).
- Mock-API Playwright: **169 passed, 1 skipped** (the existing mobile-only skip): new `e2e/staff-payouts.spec.ts` (dual
  approval with TOTP step-up, conflict attestation required, same approver refused, exceptions and reconciliation, TEST_ONLY
  and production BLOCKED labels, axe, CSP; staff without payout.view see nothing) and `e2e/owner-withdrawals.spec.ts`
  (request with password step-up → under review → cancel; another user cannot see it).
- Real-stack Playwright: **49 passed**, including the new payout overview test in `e2e-real/staff-ledger.spec.ts`
  (COMPLIANCE sees LOCAL_SIMULATOR, production BLOCKED, settlement NOT_PERFORMED; a reviewer gets 403/404).
- Flake fixes after the first CI run (mock E2E failed intermittently under `CI=1`): `signInStaff` waits for the MFA page to
  hydrate before submitting (a pre-hydration click submitted the form natively); and the payout-exception and Stage 10
  ledger action components (account freeze/unfreeze/close, period close, reconciliation investigate/resolve) now stay
  mounted with their button hidden once the action no longer applies, so the confirmation notice survives the
  `router.refresh()` that changes the record's status (previously it could unmount before being shown or announced, a
  real accessibility defect). Verified with `CI=1 … --repeat-each=8` on the payout, withdrawal and ledger specs (96/96)
  and the full mock suite (169 passed, 1 skipped). The payout exception step also asserts the persisted outcome.
- The destructive migration round trip (down → up) failed in CI on the Stage 11 Down; fixed (the Down keeps the
  sandbox capability v2 row that payment intents may reference, and re-adds the Stage 10 ledger source CHECKs `NOT VALID`)
  and verified locally on a copy of the development database.

## 4. Performance (measured; nothing extrapolated)

`TestPayoutsPerf` (FUNDZIM_IT_PERF=1), in-process service calls against the development stack:

measured 2026-10-14T07:47:43Z UTC; GOMAXPROCS=16; development stack (compose PostgreSQL 17.11, default settings); in-process service calls; dataset after the run: 848 payout requests, 5397 payout events, 913 eligibility decisions, 1730 payout ledger journals.

| scenario | c | n | p50 ms | p95 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|
| request (eligibility + reserve) then cancel (release) | 1 | 400 | 208.2 | 228.9 | 446.4 | 5 | 0 |
|   of which: withdrawal request incl. eligibility and reservation | 1 | 400 | 177.2 | 194.4 | 417.1 | — | 0 |
|   of which: cancellation incl. reservation release | 1 | 400 | 30.5 | 40.3 | 47.4 | — | 0 |
| request (eligibility + reserve) then cancel (release) | 8 | 400 | 216.2 | 299.8 | 767.6 | 34 | 0 |
|   of which: withdrawal request incl. eligibility and reservation | 8 | 400 | 178.1 | 234.5 | 667.3 | — | 0 |
|   of which: cancellation incl. reservation release | 8 | 400 | 37.2 | 65.2 | 271.4 | — | 0 |
| eligibility evaluation (owner preview with destination) | 8 | 300 | 124.5 | 141.1 | 196.3 | 62 | 0 |
| approval decision (FINANCE, recorded with step-up time) | 8 | 8 | 24.4 | 38.5 | 38.5 | 207 | 0 |
| approval decision completing DUAL (→ APPROVED) | 8 | 8 | 29.2 | 61.7 | 61.7 | 129 | 0 |
| payout status retrieval (owner view) | 8 | 500 | 0.7 | 1.6 | 3.4 | 9507 | 0 |
| payout detail (staff: events, decisions, attempts, journals) | 8 | 300 | 5.2 | 8.2 | 17.7 | 1373 | 0 |
| staff payout list, first page | 8 | 300 | 4.3 | 6.9 | 11.2 | 1677 | 0 |
| exception queue | 8 | 300 | 1.6 | 2.8 | 5.9 | 4623 | 0 |
| reconciliation references (latest 200) | 8 | 300 | 0.4 | 0.7 | 2.2 | 16943 | 0 |
| reconciliation run (all payouts past the grace period) | 1 | 3 | 1285.5 | 1288.1 | 1288.1 | 1 | 0 |

Notes: a request includes the 22-check gather (KYC, campaign, beneficiary, destination, provider health, ledger balances,
limits, integrity) and the ledger reservation; eligibility evaluation alone is p50 ≈ 125 ms, dominated by the number of
lookups (KI-S11-14). Reads are single-digit milliseconds. The reconciliation run time grows with the number of payouts
(it also queries the simulator for each submitted payout).

## 5. Remote CI

- Run 38533629949 (`9d677d2`): **failed** — Integration (146 passed, 0 failed; the destructive migration round trip
  failed on the Stage 11 Down) and Web end-to-end (intermittent notice/sign-in flakes). Both fixed as described in §3.
- Run 38568202988 (`2494c4b`): **all 8 jobs succeeded** (OpenAPI and SQL design drafts, container images, web, Go,
  web end-to-end, integration, real-stack end-to-end, secret scanning). Integration annotation: passed 146, failed 0,
  skipped 3 (`TestDistributedLoginLimitAcrossReplicas`, `TestFrontendReachesBackend`,
  `TestRateLimitFallbackWhenValkeyContainerStopped` — pre-existing, none of them payout tests). Inspected through the
  GitHub API (jobs and check-run annotations) on 2026-10-14.
