# Stage 19 — Concurrency test plan and controlled interleavings

## 1. Principles

- **Deterministic interleavings, not sleeps.** A test that needs a writer to stop at an exact point holds `FOR UPDATE`
  (or `FOR NO KEY UPDATE`) on one row *the test itself created* in an open transaction (`rowLock`,
  `tests/integration/financial_barrier_test.go`). Any writer of that row — in the test process or in the compose API
  or worker — blocks there. The test observes blocked backends through `pg_blocking_pids` (transitively: PostgreSQL
  queues later waiters behind the first one's tuple lock) and filters its own backends by `application_name`
  (`namedPool`). Waits are condition polls with deadlines (`waitFor`), never fixed sleeps standing in for an
  assertion.
- **Fault injection is scoped.** `pg_terminate_backend` is used only on backends blocked by the test's own lock
  (local superuser from `.env`); nothing else on the stack is touched.
- **The compose worker is part of the system under test.** It completes, recovers and reconciles the same records.
  Assertions are written so that whichever process acts, the invariant holds (e.g. "exactly one RESERVATION_CONSUMED",
  "no query with a shifted timestamp").
- **The Go race detector is not a database race detector.** `go test -race` runs over every Go package; database races
  are exercised by the integration tests below with real PostgreSQL.
- **Repetition is not proof.** Each race is either forced deterministically, or its fix is argued by construction and
  the test asserts the invariant under real concurrency (and the report says which).

## 2. Inventory (new in Stage 19 — `tests/integration/financial_*_test.go`, prefix `TestFinancial`)

| # | Test | Interleaving | Invariant |
|---|---|---|---|
| 1 | `TestFinancialPayoutCompletionWindowVersusReconciliation` | completer blocked between completion journal and reservation link; reconciliation + 4 concurrent Recover inside the window | no mismatch reported; one consumption; one RESERVATION_CONSUMED; one LEDGER_POSTED; 3 journals |
| 2 | `TestFinancialPayoutConnectionLostBetweenJournalAndConsumption` | completer's backend terminated while it waits at the consumption write | stranded state visible, recovered once, no duplicate journal, no mismatch afterwards |
| 3 | `TestFinancialPayoutLedgerRefusalIsStillReported` | ledger refuses the payout's journals (campaign pending account frozen) | provider never instructed; SEV1 finding carries the refusal; rerun adds nothing; settles once after unfreeze |
| 4 | `TestFinancialReservationInvariantsInTheDatabase` | direct writes as the schema owner | every contradictory reservation state refused by the database |
| 5 | `TestFinancialPayoutConcurrentApprovals` | 12 concurrent approval calls by two FINANCE checkers | 2 approvals, 2 distinct checkers, 1 transition |
| 6 | `TestFinancialPayoutCancelRacesSubmission` | 4 cancellations vs 4 submissions, 3 rounds | exactly one outcome; a cancelled payout never reached the provider |
| 7 | `TestFinancialPayoutConcurrentReconciliationRunsRecordOnce` | 6 concurrent reconciliation runs over one genuine mismatch | one reference, one exception |
| 8 | `TestFinancialPayoutCrossTenantUnderConcurrency` | 50 concurrent requests by another verified user | all refused; nothing changed |
| 9 | `TestFinancialPaymentPollBudgetExhaustionAndLateSuccess` | payments clock moved past the horizon (only the payments service's clock) | status kept, one reference, no query, late webhook resolves once |
| 10 | `TestFinancialPaymentPollAttemptCap` | 520 explicit queries | exactly 500 queries |
| 11 | `TestFinancialPaymentConcurrentSweepsPollOnce` | 8 concurrent sweeps (+ compose worker) at the due moment | one query per due period |
| 12 | `TestFinancialPaymentPollErrorClassesNeverFail` | scripted 429 / 503 / timeout / malformed answers; 8 concurrent queries at the paid answer | never FAILED; rescheduled; one SUCCEEDED, one notice |
| 13 | `TestFinancialLedgerConcurrentConflictingPayloads` | 60 concurrent postings, one key, two payloads | one journal; losers are conflicts |
| 14 | `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock` | 200 concurrent opposite-direction journals on one campaign | 0 deadlocks, 0 timeouts, balances exact |
| 15 | `TestFinancialLedgerConnectionLossAndAmbiguousRetry` | posting blocked on its account row, backend terminated; then 5 retries of a committed posting | nothing visible after the loss; one journal after retries |
| 16 | `TestFinancialLedgerIntegrityDuringConcurrentPosting` | integrity checker in a loop while 6 writers post | every run passes; balance exact |
| 16b | `TestFinancialPayoutConcurrentStatusQueries` | 64 concurrent status queries of one PROCESSING payout (4 rounds of 16) | no failure, no deadlock, unique attempt numbers, no IN_FLIGHT left, status unchanged |
| 16c | `TestFinancialLedgerPoolExhaustionFailsCleanly`, `TestFinancialAuditVerificationInterrupted`, `TestFinancialPayoutReconciliationInterrupted` | pool held; verification and reconciliation cancelled at several points | nothing partial recorded; one finding after a complete run |
| 17 | `TestFinancialWorkerCrashMidFlight` (opt-in) | the real worker process SIGKILLed while dispatching payouts and applying donations, restarted | ≤ 1 accepted CREATE per payout, one consumption, one success per payment, no mismatch, integrity passes |
| 18 | `TestFinancialLoad` (opt-in) | tiers c = 1, 4, 8, 16 and an endurance run, mixed workload | zero duplicate effects across every record of the run (benchmark-results.md) |

## 3. Existing suites rerun as part of Stage 19 (unchanged)

Stage 8–18 concurrency and idempotency tests, among them: `TestPaymentsConcurrentAttemptsYieldOnePayment`,
`TestPaymentsWebhookOrderingAndDuplicates`, `TestPaymentsPostingRequestsExactlyOnce`, `TestPaymentsStage9Concurrency`,
`TestPaymentsCrashBetweenCallAndResultIsRecovered`, `TestPayoutHundredConcurrentRequestsReserveOnce`,
`TestPayoutUnknownOutcomeKeepsReservationAndIsNeverResubmitted` (incl. WORKER_RESTART), `TestPayoutSubmitRetryBudgetHaltsAndResumes`,
`TestLedgerHundredConcurrentDuplicatePostingsYieldOneJournal`, `TestLedgerReversalsMakerCheckerAndNoDoubleReversal`,
`TestLedgerAccountFreezeVersusConcurrentPostings`, `TestFeeConcurrency`, `TestRiskConcurrentSignalsAndReleases`,
`TestReconciliationConcurrency`, `TestOutboxDeliversExactlyOncePerSubscriber`, `TestWorkerCrashedJobIsRescued`,
`TestIdempotencyConcurrentRequestsRunHandlerOnce`, `TestIdempotencyKeyReusedWithDifferentBody`,
`TestSecurityConcurrentMigrationsSerialise` (results: test-results.md).

## 4. Delivery semantics

River and the outbox deliver **at least once**; no Stage 19 test claims exactly-once delivery. What is demonstrated is
**at most one logical financial effect** per business event through durable, idempotent processing: deterministic
ledger keys, `WHERE link IS NULL`, payment precedence and event dedupe, payout attempts guard, webhook inbox dedupe,
outbox inbox (consumer, event_id) uniqueness. Worker ownership (River leases, rescue of stuck jobs) is a scheduling
mechanism, not a correctness mechanism: a stale worker that finishes a job after losing it repeats an idempotent
operation (tests 1, 2, 17; `TestWorkerCrashedJobIsRescued`).
