# KI-S18-25 — Payout completion versus reservation consumption

Status: **FIXED** (Stage 19, ADR-051). Severity before the fix: MEDIUM (false SEV1 alarms; misleading event history; no
financial misstatement).

## 1. Original failure

CI run 39965210148 (Stage 18, `7fd002e`): `TestPayoutReconciliationAndExceptionWorkflow` found a
`RESERVATION_MISMATCH` (plus the missing completion journal) for a payout that had just completed. The Stage 18 test
was changed to wait for settlement; reconciliation was left unchanged and the issue handed to Stage 19.

## 2. Trace (actual implementation)

| Step | Function | Transaction / pool |
|---|---|---|
| provider evidence (webhook, poll, sync answer, RECON) | `applyStatus` inside the webhook / poll / dispatch transaction | app pool, tx A: payout → COMPLETED, `payout_events`, outbox. Commits. |
| completion journal | `syncLedger` → `ledger.PostOnce(payout:{id}:completed)` | ledger pool (`fundzim_ledger`), tx B — a different role and pool by design (ADR-042: only the ledger writes ledger tables), so A and B can never be one transaction |
| reservation consumption | `syncLedger.link` → `UPDATE payout_reservations SET ledger_consume_txn_id, status = 'CONSUMED' WHERE … IS NULL` + `RESERVATION_CONSUMED` event | app pool, tx C |
| crash recovery | `Recover` job (30 s): selects payouts whose state requires a journal link that is NULL and calls `syncLedger` | as above |
| reconciliation | `Reconcile` (15 min job, staff): lists payouts changed before now − grace (5 min; the ITs set 1 ns), evaluates each **from the list's snapshot** | app pool reads + ledger reads |

Answers to brief §5.1: the payout state (A) and the reservation (C) change in **separate transactions**, possibly in
different processes (a webhook in the API, Recover in the worker), with the ledger journal (B) between them on another
pool. Locks: A locks the payout row; C locks the reservation row; B locks ledger rows. Isolation: READ COMMITTED
everywhere. Reconciliation did **not** read a consistent view: the candidate list was read once at the start of the run
and each payout was evaluated from that snapshot, while ledger journals were read later. So reconciliation could observe
A without C (and with or without B). A crash after A leaves the payout COMPLETED with an ACTIVE reservation until
Recover runs — never permanently, because Recover re-drives it, but the state is visible. Retries cannot consume twice:
the journal key is deterministic (L6) and the link is `WHERE ledger_consume_txn_id IS NULL` with set-once triggers.
The state machine does not name the intermediate state; it is implicit in "status requires a link that is NULL".

## 3. Reproduction (deterministic)

`TestFinancialPayoutCompletionWindowVersusReconciliation` holds `FOR UPDATE` on the test payout's reservation row
(`rowLock`), drives completion, and waits until the completer — in this process or the compose stack — is blocked on
that row while the payout is COMPLETED and the completion journal exists (`pg_blocking_pids`, no sleeps). Reconciliation
then runs inside the window.

Before the fix (log `prefix-payout.log`, this session):

```
reconciliation finished inside the window: {… NewMismatches:1 ByType:map[RESERVATION_MISMATCH:1] …}
the completion window was reported as a mismatch: [RESERVATION_MISMATCH] (KI-S18-25)
--- FAIL: TestFinancialPayoutCompletionWindowVersusReconciliation
```

A second defect surfaced in the same window: four concurrent Recover runs plus reconciliation all called `link`; each
inserted its event even when its UPDATE matched no row (the link was already set by another). With the reconciliation
fix in place but the link fix reverted (`prefix-linkdedupe.log`):

```
RESERVATION_CONSUMED recorded 6 times
--- FAIL: TestFinancialPayoutCompletionWindowVersusReconciliation
```

One consumption, six "consumed" history events: a misleading financial history (brief §17).

## 4. Fix (narrowest change preserving the invariant)

Option A (one transaction) is impossible without giving the app role ledger privileges (ADR-042). Option B/D is what
the code already is: the payout status is the durable intent and Recover the recovery mechanism. The defect was in the
detector and in the event recording:

1. **Reconciliation re-reads and settles before judging** (`reconcileFresh`, `ledgerSyncPending`). The run's list only
   proposes candidates; each payout is re-read; if its state requires a journal/reservation link that is NULL, the run
   calls the same idempotent `syncLedger` as Recover, re-reads, and evaluates the state as it is afterwards. If a
   transition is in flight in another process, reconciliation's own link waits at the reservation row lock and finds
   the link set — it never reports the in-flight transition. A payout changed after the run's cutoff is skipped (the
   next run checks it past its grace period).
2. **A failed recovery is evidence, not noise.** If `syncLedger` fails (ledger refusal), the payout is reported with the
   mismatches of its real state and `detail.ledger_sync = FAILED`, `detail.error_code` (e.g. `LEDGER_ACCOUNT_FROZEN`).
3. **`link` records an event only when its UPDATE linked the row** (`RowsAffected() == 1`); a concurrent sync's event is
   the record.
4. **One recorder per payout** while writing references: `SELECT … FOR UPDATE` on the payout row before the
   "already under investigation" check, so concurrent runs (job + staff) cannot both record references for the same
   payout (by construction; not reproduced without it in 2 runs, `prefix-reconlock.log`).
5. Dispatch no longer misclassifies the database's "no in-transit journal" refusal as DUPLICATE_SUBMISSION_ATTEMPT: the
   provider is not called, LEDGER_POSTING_FAILED (opened by `syncLedger`) is the record, and DispatchDue resumes once
   Recover has posted the journal.

No schema change, no new state, no change to the grace period, the alerting, the severities or the reservation CHECKs.
Nothing is classified as transient by assumption: the recovery is run and its result observed.

## 5. Regression tests (all run in this session after the fix: PASS)

| Brief §5.4 case | Test |
|---|---|
| 1, 2 completion and consumption concurrent; reconciliation during the transition | `TestFinancialPayoutCompletionWindowVersusReconciliation` (reconciliation waits at the barrier; 0 references) |
| 3, 4 crash / connection loss between the commits | `TestFinancialPayoutConnectionLostBetweenJournalAndConsumption`: the completer's backend is terminated (`pg_terminate_backend`) while it waits at the consumption write; the payout stays COMPLETED with the reservation ACTIVE (observed), Recover consumes it once; 1 completion journal; reconciliation then reports nothing |
| 5 concurrent completion | window test: the compose stack and this process both complete/sync the same payout |
| 6 duplicate result delivery | window test (concurrent syncs) + Stage 11 duplicate-callback scenario (suffix 05) |
| 7 duplicate consumption requests | four concurrent Recover runs + reconciliation + completer: one RESERVATION_CONSUMED, one LEDGER_POSTED, 3 journals |
| 8 retry after an ambiguous commit | connection-loss test (the retry is Recover; deterministic keys) |
| 9 genuinely inconsistent final state | `TestFinancialPayoutLedgerRefusalIsStillReported` (ledger refuses; SEV1 LEDGER_MISMATCH with the refusal); `TestPayoutReconciliationAndExceptionWorkflow` (provider says failed: PROVIDER_STATUS_MISMATCH SEV1) |
| 10 completed payout with missing evidence | provider record changed / absent → PROVIDER_STATUS_MISMATCH (Stage 11 test, rerun) |
| 11 released reservation with an invalid payout state | `TestFinancialReservationInvariantsInTheDatabase` (RELEASED → CONSUMED and → ACTIVE refused) |
| 12 two payouts, one reservation | same test (second reservation for a payout, moving a reservation, sharing a reserve journal: refused) |
| concurrent reconciliation runs | `TestFinancialPayoutConcurrentReconciliationRunsRecordOnce` (6 runs: 1 reference, 1 exception) |

The ledger-refusal test also showed a safety property worth recording: while the in-transit journal is refused, the
payout stays SUBMITTED and **the provider is never instructed** (0 CREATE_PAYOUT attempts; the attempts guard trigger
refuses the attempt without the journal).

## 6. Remaining limitations

- The intermediate state still exists for milliseconds (or until Recover runs after a crash); it is now named,
  documented and never reported. Making it atomic would need a cross-pool transaction (not available by design).
- If the ledger is unavailable for longer than the grace period, reconciliation reports the payout (SEV1 with
  `ledger_sync = FAILED`). That is intended: a journal the state requires and the ledger cannot post is actionable.
- Reconciliation now performs the same idempotent ledger catch-up as Recover. It never changes a payout status, a
  provider record, an approval or a severity; ADR-051 records the decision.
