# Remediation phase 4 — findings and corrections

Code commit: `30eb045`.

## KI-R1-07 — `TestPayoutApprovalSeparationOfDutiesAndInvalidation` raced the payout worker

### Mechanism (from the code)

- **The worker.** It runs `SubmitDue` every few seconds and selects every APPROVED payout. `SubmitApproved` evaluates
  eligibility (`gather`, no locks). Then, in one transaction, it locks the payout row (`FOR UPDATE`), checks that status,
  approval round and version are unchanged, records the pre-submission decision in `app.payout_eligibility_decisions`
  (`writeDecision`), and moves the payout to SUBMITTED.
- **The test.** It approves the payout, then suspends its destination (`SetSuspended`, which calls `invalidateApprovals`: an
  APPROVED payout returns to REVIEW, round + 1), reinstates the destination, re-approves, asserts APPROVED, and cancels
  "before submission". The payout is APPROVED from the first approval until the cancellation.
- **The race.** It is scheduling order between two independent processes, with no synchronisation barrier: neither a fixture
  leak nor a stale queue entry. Any worker pass inside that window submits the payout first. The suspension then correctly
  leaves a submitted payout alone, which is the CI observation in run 40305574851: "after destination suspension: SUBMITTED
  round 1". Later steps (re-approval check, cancellation) have the same exposure.
- **Business behaviour is correct.** A payout whose submission committed before the suspension is not taken back, and
  approvals are invalidated only for payouts still pending or approved. The defect was in the test: it relied on the worker
  not running.

### Reproduction (kept as a regression test)

`TestPayoutSubmissionCommittedBeforeDestinationSuspension` plays the worker inside the window: approve, then
`SubmitApproved`, then suspend the destination. The payout ends SUBMITTED, PROCESSING or COMPLETED in approval round 1, and no
APPROVALS_INVALIDATED event exists. That is the exact failing interleaving, made deterministic, with the correct outcome
asserted.

### Correction (`tests/integration/payout_worker_barrier_test.go`, `payouts_test.go`)

**The barrier.** The test holds a SHARE lock on `app.payout_eligibility_decisions` (`holdEligibilityDecisions`) from just
before the first approval until after the cancellation.
- No eligibility decision can be inserted while it is held.
- So no submission can reach its move to SUBMITTED: `writeDecision` precedes the move in the same transaction.
- Approval, suspension, reinstatement, the amount-change attempt and cancellation write no decision and are not blocked.

**Removing a blocked attempt.** An attempt that blocks at the decision insert holds the payout row the test's next action
needs. `decisionsBarrier.do` therefore runs each action on the test goroutine while a watcher terminates exactly the backends
that are:
- direct waiters of the test's own lock (`pg_blocking_pids`);
- running that insert (`query ILIKE 'INSERT INTO app.payout_eligibility_decisions%'`).

This is the Stage 19 technique: no sleep is the synchronisation, and nothing outside the test's lock is touched. The killed
transaction rolls back, so nothing of the attempt is committed, and the worker later re-evaluates the payout in its current
state. Every interception is counted and logged.

**Unchanged.** Every original assertion is kept: linked-account and verifier self-approval refused in Go and in the
database; compliance cannot approve; no APPROVED without approvals; invalidation to REVIEW in round 2; immutable amount;
re-approval; cancellation; balances; ledger integrity.

**Added.** At the end, the payout has no SUBMITTED event and no CREATE_PAYOUT attempt. The test now proves the
financial-safety invariant directly.

**Not used.** No sleep as synchronisation, no paused or disabled worker (the worker keeps running and keeps submitting
everything else), no skip, no relaxed state check, and no dependency on execution order or earlier database contents. The
superuser connection is the Stage 19 fault-injection path.

### Evidence

The repeat runs and suite results are in `test-evidence.md` §2. KI-R1-07 is **closed only if** every repeat run and the CI
run pass.

## F-R4-01 (from Phase 3: "checkpoint forward-move design limit") — a module role could advance a checkpoint past unexamined records

**Root cause.** The checkpoint guard of `20261025090000` checks only monotonic properties: no rewind, version + 1, counters
never decreasing. Nothing tied a position change to records actually examined, and the module's runtime roles hold UPDATE on
their checkpoint tables. The complete write-path map is in `checkpoint-integrity-design.md` §1.

**Correction.** Migration `20261027090000` and `reconsweep`:
- every advance writes append-only batch evidence in the same transaction;
- deferred constraint triggers refuse any position change without matching evidence, and any new cycle that does not start
  empty with the previous cycle's deferrals;
- `reconsweep.Verify` re-reads each scope independently and records an integrity status that becomes INTEGRITY_FAILURE and a
  CRITICAL in-app alert.

Design, threats, limits and recovery cases: `checkpoint-integrity-design.md`.

**Status.** FIXED on the branch, pending review and integration. The deliberate-forger limit remains: it is documented and
out of reach of any evidence-based control.

## Not changed

Payout, approval, maker-checker, reservation, ledger, provider and polling behaviour; the Phase 3 corrections; every
financial gate. No production code path was changed for KI-R1-07 (test only).
