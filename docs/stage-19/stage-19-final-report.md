# Stage 19 — Final report: financial concurrency, load, failure and integrity validation

Date: 2026-10-23/24. Branch `stage-19/financial-concurrency`. Execution mode: controlled non-production engineering.
Production financial operations remain **BLOCKED**.

## A. Executive summary

**Recommendation: PASS WITH CONDITIONS** (engineering). Stage 19 reproduced both handed-over defects deterministically
and fixed them, then found and fixed twelve more through adversarial concurrency, fault injection and load. Two are
HIGH: ledger postings deadlocked when funds moved in opposite directions between the same accounts, and concurrent
payout status queries collided and deadlocked. No run, before or after a fix, misstated money: every failure rolled
back, and no duplicate journal, duplicate provider instruction, double consumption, double success or negative balance
was ever observed. After the fixes, two load runs with a 30-minute endurance phase each (11 763 payouts and 11 763
donations in total) produced zero duplicate financial effects, zero unexplained reconciliation findings and passing
ledger integrity; the second run had zero deadlocks.

- **Implemented:** reconciliation that settles before it judges (KI-S18-25); writer-only history events; ledger lock
  order; payout attempt lock order; bounded, jittered, per-environment payment polling (KI-S18-22); provider-health
  indexes; newest-first ledger reconciliation; reliable FAILED recording for Stage 17 runs; one OpenAPI enum fix;
  KI-S18-24 fixture fix; 25 new tests (deterministic barriers, faults, load, volume).
- **Passed:** every Stage 19 test; race-enabled unit tests (39 packages); gosec; the full integration suite apart from
  the cases below; both load runs' correctness checks.
- **Failed and still failing:** `TestReconciliationPaymentsEndToEnd` **on the development database only**. The load
  runs left about 11 800 sandbox payments and payouts without statement lines in its window (KI-S19-17), so its run
  needs 46–83 s against a 30 s deadline. The deadline was not raised.
- **Blocked or not done:** failover chaos (single node), staging invariant monitors (no staging), alert delivery (no
  paging), production capacity (no target), 10⁵–10⁶ payment and payout volumes.
- **Production:** remains blocked.

## B. Repository provenance

| Item | Value |
|---|---|
| Starting commit | `801babef97c0a21891a48f30580f7bda32485755` (Stage 18 head; CI run 39984705098, 8/8 jobs) |
| Final code commit | `16429d2` (see §C for later documentation commits) |
| Branch | `stage-19/financial-concurrency`, created from `801babe`, pushed to `origin` |
| Prior stages | stacked branches `stage-1/…` … `stage-18/security-hardening`; Stage 17 head `f5e9d00`, Stage 18 head `801babe` |
| Required code present | yes (preflight.md §1) |
| Pull request | none created |
| Merge | none; `main` is still `a552de2` (Stage 0) |
| On main | no |

## C. CI evidence

Observed through the GitHub REST API (`/actions/runs/40031324294`, `/jobs`, `/artifacts`):

| Commit | Run | Jobs | Passed | Failed | Skipped | Artifacts |
|---|---|---|---|---|---|---|
| `16429d27ee1c35d102b459decda4e5a08e64ba90` | **40031324294** (2026-10-23 22:05:31 → 22:29:39 UTC) | 8 | **8**: Go (format, vet, unit tests incl. gosec and deploytest, vulnerabilities); Web (lint, types, unit tests, build, audit); Web end-to-end (Playwright); Real-stack E2E (compose + Playwright); Integration (real PostgreSQL, Valkey, Garage, ClamAV; incl. the destructive migration round trip over both Stage 19 migrations); Container images (build, SBOM, vulnerability scan, provenance); OpenAPI and SQL design drafts; Secret scanning (gitleaks, full history) | 0 | 0 | `supply-chain-evidence-16429d27…` (546 537 bytes) |

The CI integration job runs every non-opt-in test on a fresh throwaway database, including
`TestReconciliationPaymentsEndToEnd` (which fails only on this session's development database, KI-S19-17) and every
`TestFinancial*` test; the opt-in load, worker-kill and volume tests do not run in CI. Later documentation-only commits
(this report) trigger further runs that are not part of the verified code evidence; the verified code head is `16429d2`.

## D. KI-S18-25

| Item | Content |
|---|---|
| Original failure | transient SEV1 RESERVATION_MISMATCH in CI run 39965210148 |
| Root cause | the payout status, the ledger journal (ledger pool) and the reservation link commit in three transactions by design; reconciliation judged payouts from a stale candidate list, inside that window |
| Invariant | financial-invariants.md §3: payout status → required journals and reservation state; the transitional state is exactly "a required link is NULL", and it is resolved only by `syncLedger` |
| Fix | `reconcileFresh`: re-read; when a sync is pending, run the idempotent `syncLedger` (the Recover path); evaluate afterwards; a refused sync is reported with `ledger_sync = FAILED` and its error code; a lock on the payout row while recording; link events written only by the writer that linked |
| Concurrency tests | the window test (row-lock barrier: reconciliation plus 4 concurrent Recovers plus the completer), 6 concurrent reconciliation runs, interrupted runs |
| Crash recovery | completer's backend terminated mid-transition: stranded, then recovered once |
| Reconciliation behaviour | 0 references in the window (failed before the fix); 1 reference and 1 exception for a genuine mismatch under 6 concurrent runs |
| Genuine inconsistencies | ledger refusal → SEV1 LEDGER_MISMATCH carrying LEDGER_ACCOUNT_FROZEN; provider disagreement → PROVIDER_STATUS_MISMATCH; contradictory reservation writes refused by the database |
| Limitations | the window still exists for milliseconds (named and settled, not removed); a ledger outage longer than the grace period is reported, which is intended |

## E. KI-S18-22

| Item | Content |
|---|---|
| Original behaviour | CREATED, PENDING, REQUIRES_ACTION and AUTHORISED polled forever every 10 min; no jitter; no due re-check; every environment swept |
| Policy | backoff 5 s doubling to 10 min, ±20 % jitter; `PollHorizon` 72 h from creation (awaiting statuses); `UnknownHorizon` 72 h from `unknown_since`; `MaxStatusPolls` 500 per payment |
| Max attempts / elapsed time | 500 queries (cap test: 520 calls → 500); 72 h; 438 polls fit in the horizon at the unjittered backoff |
| Unknown outcome | the status is kept; one POLL_BUDGET_EXHAUSTED reference with `needs_reconciliation`; polling stops; never FAILED, never resubmitted; a late webhook or RECON evidence resolves it |
| Worker restart | the budget is computed from stored facts; the lease and in-flight sweeper are unchanged |
| Duplicate effects | 0 in every polling test |
| Limitations | 72 h and 500 are internal defaults, not provider values (PCR-010); no Retry-After support |

## F. Financial correctness

| Area | Observed |
|---|---|
| Double-entry balance | integrity passed after every payout test and every load run; 144 runs passed during concurrent posting |
| Idempotency | 60 concurrent postings with two payloads → 1 journal and 30 conflicts; ambiguous retries resolved to the same journal |
| Duplicate events | 1 consumption event (6 before the fix); 1 SUCCEEDED and 1 success notice per payment under concurrency and load |
| Payment transitions | precedence and source rules held; errors never failed a payment |
| Payout transitions | 1 approval transition under 12 concurrent approvals; cancellation versus submission produced exactly one outcome; ≤ 1 accepted CREATE per payout across 11 763 payouts |
| Reservations | every contradictory write refused by the database; COMPLETED ⇒ CONSUMED for every load payout |
| Ledger immutability | unchanged; runtime roles cannot rewrite history (Stage 18 tests rerun) |
| Reconciliation | settles before it judges; no findings multiplied; genuine findings kept |
| Maker-checker | intact (concurrent approvals; full suite) |
| Audit integrity | intact; an interrupted verification records nothing; a full run took 12.8 s over 1.33 M rows |
| Gates and constraints | unchanged; gate tests pass; one CHECK gained a reason |

## G. Concurrency and failure evidence

| Scenario | Expected | Observed | Result | Evidence |
|---|---|---|---|---|
| Reconciliation inside the completion window | no false mismatch | 0 references (failed before the fix) | PASS | `TestFinancialPayoutCompletionWindowVersusReconciliation` |
| Concurrent syncs of one payout | one history event | 1 (6 before the fix) | PASS | same |
| Connection loss between journal and consumption | recovered once | as expected | PASS | `…ConnectionLostBetweenJournalAndConsumption` |
| Ledger refuses the payout journals | provider never called; SEV1 with the refusal | as expected | PASS | `…LedgerRefusalIsStillReported` |
| Contradictory reservation states | database refuses | 6/6 refused | PASS | `…ReservationInvariantsInTheDatabase` |
| Concurrent approvals | 2 approvals, 1 transition | as expected | PASS | `…ConcurrentApprovals` |
| Cancellation versus submission | exactly one outcome | cancel won in 3/3 rounds | PASS (one branch exercised) | `…CancelRacesSubmission` |
| 6 concurrent reconciliation runs | 1 reference | 1 | PASS | `…ConcurrentReconciliationRunsRecordOnce` |
| 64 concurrent payout status queries | no failure or deadlock | 64/64 (21/64 and 68 deadlocks before the fix) | PASS | `…ConcurrentStatusQueries` |
| Cross-tenant requests under concurrency | all refused | 50/50 | PASS | `…CrossTenantUnderConcurrency` |
| Poll horizon exhausted, then a late webhook | status kept, then resolved once | as expected (queried after the horizon before the fix) | PASS | `…PollBudgetExhaustionAndLateSuccess` |
| Poll cap | 500 | 500 | PASS | `…PollAttemptCap` |
| 8 concurrent sweeps | 1 query | 1 | PASS | `…ConcurrentSweepsPollOnce` |
| 429 / 503 / timeout / malformed answers | never FAILED | as expected | PASS | `…PollErrorClassesNeverFail` |
| Same key, two payloads, concurrently | 1 journal | 1 | PASS | `…ConcurrentConflictingPayloads` |
| Opposite-direction postings | no deadlock | 0 (5 deadlocks and 109 timeouts before the fix) | PASS | `…OppositeOrderPostingsDoNotDeadlock` |
| Posting connection killed before commit | nothing visible | 0 journals, then 1 | PASS | `…ConnectionLossAndAmbiguousRetry` |
| Integrity while posting | always passes | 144/144 | PASS | `…IntegrityDuringConcurrentPosting` |
| Pool exhausted | clean refusal | as expected | PASS | `…PoolExhaustionFailsCleanly` |
| Audit verification interrupted | nothing recorded | as expected | PASS | `…AuditVerificationInterrupted` |
| Reconciliation interrupted | recorded once | as expected | PASS | `…PayoutReconciliationInterrupted` |
| Worker SIGKILLed mid-batch | everything settles once | as expected | PASS | `…WorkerCrashMidFlight` (opt-in) |
| Load and endurance, 16 workers × 30 min | 0 duplicates | 0 in both runs; 0 deadlocks after the fixes | PASS | `TestFinancialLoad` |

The full fault matrix (F-01 … F-22) is in fault-injection.md.

## H. Load and performance

benchmark-results.md:

- **Environment:** one 16-vCPU development host; PostgreSQL in a container limited to 1 GiB.
- **Record volume:** ledger at 2 000, 16 100 and 111 200 journals. Posting saturates at 155–182 journals/s from 8
  writers on; the global audit chain lock is the cause (KI-S19-01). The lock-order fix costs 3–9 %.
- **Mixed load:** 3-minute tiers at c = 1, 4, 8 and 16, plus 30-minute endurance at c = 16, twice.

After the fixes, 3-minute tiers:

| c | Payout lifecycles | Payout p50 / p95 / p99 | Donation-creation p50 |
|---|---|---|---|
| 8 | 438 | 3.1 / 3.7 / 4.1 s | 85 ms |
| 16 | 536 | 5.4 / 5.6 / 5.7 s | 69 ms |

The 30-minute endurance run:

- **Volume:** 5 167 payout lifecycles, payout p99 5.8 s.
- **Errors and deadlocks:** 0 errors, 0 deadlocks (98 in the first run, before the fix), 0 lock waiters sampled.
- **Memory:** flat for the API and the worker. PostgreSQL's page cache reached its 1 GiB limit.
- **Queues:** the worker is the bottleneck. The notifications backlog reached ~15 000 jobs, with deliveries
  4–9 minutes late, and financial jobs averaged 8.5 s latency (KI-S19-13).

Contention before and after:

- **Provider health derivation:** 0.6 s → 2 ms per payout eligibility check.
- **Payout reconciliation runs:** up to 19 s under load (KI-S19-09).
- **Stage 17 reconciliation at volume:** 23 612 discrepancies in 83 s (KI-S19-16).

Nothing here is production capacity. No SLO or target exists.

## I. Security and governance

- **Authorization and tenancy:** regressions pass (security-regression.md), including 50 concurrent cross-tenant
  requests and concurrent maker-checker decisions.
- **Financial gates:** unchanged and tested.
- **Audit:** intact.
- **Test isolation:** barrier and fault tests touch only their own rows. Load runs polluted the shared development
  database's Stage 17 window (KI-S19-17); nothing was deleted.
- **Security findings:** none new; F-S18-03, F-S18-04, F-S18-05 (residual) and F-S18-12 are carried.
- **Formal acceptance:** Stages 0–19 **NOT_RECORDED**.

## J. Known issues and production blockers

known-issues.md:

- **Implementation defects:** F-S19-01 to F-S19-14, all fixed.
- **Open:**

| Category | Issues |
|---|---|
| Test or infrastructure limitation | KI-S19-05 to KI-S19-08, KI-S19-10, KI-S19-17 |
| Provider-selection dependency | KI-S19-04 and every PCR item |
| Operational readiness | KI-S19-01, -09, -13, -15, -16 |
| Implementation | KI-S19-02, -03, -12, -14 |
| Governance | KI-S19-11 and acceptance of Stages 0–19 |
| Legal / regulatory | carried LR items |
| Accounting | KI-S10-02 |
| Security | carried Stage 18 conditions |

No serious correctness defect remains open; the remaining items are limits and dependencies.

## K. Recommendation

**PASS WITH CONDITIONS.** The financial invariants are documented and were exercised under deterministic interleavings,
real connection loss, a real worker kill and sustained load. Every defect found was fixed at its root cause, with a
test that failed before the fix wherever reproduction was possible. No invariant failed, and remote CI is green on the
final code (run 40031324294, 8/8 jobs).

The conditions are:

- the one Stage 17 test that fails on this development database only, explained by environmental volume (it passed
  on CI's fresh database, run 40031324294, §C);
- owner decisions on capacity targets and poll budget values;
- incremental reconciliation and worker sizing before production volumes;
- every carried production blocker.

A green CI is necessary, not sufficient: production stays BLOCKED.

## L. Stage 20 handover

docs/stage-handover/STAGE-19-TO-STAGE-20.md covers verified prerequisites, unresolved issues, owner, legal,
accounting and provider decisions, infrastructure, security evidence, outstanding acceptances, tests to repeat and
explicit production blockers. Stage 20 has not been started.
