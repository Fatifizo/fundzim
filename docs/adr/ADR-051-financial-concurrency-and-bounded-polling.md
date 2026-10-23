# ADR-051: Financial concurrency — payout reconciliation settles before it judges, ledger postings lock every synchronous balance row in one order, bounded payment polling, deterministic interleaving tests

- **Status:** Proposed
- **Date:** 2026-10-23
- **Deciders:** Technical lead (Stage 19)
- **Stage:** 19
- **Amends:** ADR-042 (ledger posting engine: lock order), ADR-043 (payout reconciliation and ledger synchronisation),
  ADR-039 / Stage 9 payments (status polling budget, per-environment sweeping), Stage 9 provider health derivation
  (indexes).

## Context

Stage 18 handed over two known issues: KI-S18-25 (a payout reconciliation inside the window between a payout's
completion and its reservation consumption raised a transient SEV1 RESERVATION_MISMATCH) and KI-S18-22 (payments in
PENDING were polled forever). Stage 19 reproduced both deterministically and, while testing concurrency and load, found
further defects (docs/stage-19/known-issues.md), among them: concurrent ledger synchronisations recorded the same reservation consumption several times (6 events for
one consumption); ledger postings that move funds between the same two accounts in opposite directions deadlocked and
timed out (86 of 200 posted, 5 deadlocks, 109 lock timeouts); a worker polled payments of other execution
environments it could not query; and every payout eligibility check scanned the provider's whole call history. None misstated money: every failure rolled back. All are availability or evidence
defects in financial code paths. The payout status commits on the app pool and its journals on the ledger pool by
design (ADR-042: only the ledger writes ledger tables), so no single transaction can span them.

## Decisions

1. **Payout reconciliation settles before it judges.** The run's list only proposes candidates. Each payout is re-read;
   when its state requires a journal or reservation link that is not set (`ledgerSyncPending`, the Recover job's
   condition), reconciliation runs the same idempotent `syncLedger` (deterministic journal keys, links set only where
   NULL) and evaluates the state as it is afterwards. A transition still in flight elsewhere is waited for at its row
   lock, never reported. A recovery that fails is reported with the payout's real mismatches and the ledger's refusal
   (`ledger_sync = FAILED`, `error_code`). Nothing is classified as transient by assumption; the grace period,
   severities and detectors are unchanged. Reconciliation never changes a payout status, provider record or approval.
2. **History is written by the writer that changed the row.** A reservation link records its RESERVATION_* /
   LEDGER_POSTED event only when its own UPDATE set the link. Payout reconciliation locks the payout row while it
   records, so concurrent runs record one reference set. The database's "no in-transit journal" refusal of a provider
   instruction is not a duplicate submission: nothing is sent and LEDGER_POSTING_FAILED is the record.
3. **A ledger posting locks every SYNC projection row it touches, in ascending account id, before writing entries**
   (`lockAndCheck`; sufficiency is still checked only for decreased non-negative accounts). Locking only the decreased
   rows first let opposite-direction journals acquire locks in opposite orders. DEFERRED platform-wide hot rows remain
   unlocked by postings. Measured effect: 200/200 opposite-direction postings, 0 deadlocks, 0 lock timeouts, 22.8 s
   (before: 86/200, 5 deadlocks, 109 lock timeouts, 290 s).
4. **Payout attempts are created and recorded in one lock order: the payout row first.** `startAttempt` locks the
   payout before numbering the attempt (the attempts guard trigger locks it anyway) and skips a status query while
   another of the same payout is in flight; `recordObservation` locks the payout before updating its attempt. Before:
   concurrent status queries of one payout collided on attempt numbers and deadlocked (98 deadlocks in a 30-minute
   load run).
5. **Routine payment polling is bounded for every awaiting status.** `PollHorizon` (72 h from creation; CREATED,
   PENDING, REQUIRES_ACTION, AUTHORISED), the existing `UnknownHorizon` (72 h from `unknown_since`), and
   `MaxStatusPolls` (500 queries per payment, routine and staff). Exhaustion keeps the status — it is never evidence of
   failure — sets `needs_reconciliation`, opens one POLL_BUDGET_EXHAUSTED reference and stops polling; a later
   authoritative webhook or Stage 17 RECON evidence resolves it through `apply()`. Nothing is resubmitted. Routine polls
   are spread by ±20 % jitter, re-check under the row lock that the payment is still due (one query per due period
   whatever the number of sweepers), and a process sweeps only payments of its own execution environment. 72 h and 500
   are internal safety defaults, not provider values (PCR-010).
6. **Provider health derivation is independent of the call history's size.** Every payout eligibility check derives
   the provider's health; its queries scanned the whole append-only `provider_health_events` table (≈0.6 s at 1.26 M
   rows). Migration `20261024090100` adds a window index and partial indexes for circuit and outage events, and the
   circuit currency enumeration reads circuit events only — the same result, because a currency without a circuit
   event is CLOSED. Health semantics (window, thresholds, circuit and outage rules) are unchanged.
7. **Reconciliation windows read the newest records, and a failed run is always recorded.** Ledger reconciliation now
   scans the newest 10 000 payment journals (it read the oldest, so past 10 000 new journals were never compared), like
   the payout and fee reconciliations; a Stage 17 run whose context ended writes FAILED with a non-cancelled context
   instead of staying RUNNING until the reaper. Incremental (watermark) reconciliation remains open (KI-S19-09).
8. **Concurrency is tested with deterministic interleavings, not sleeps.** Integration tests hold a row lock on a
   record they created so that any writer — in-process or in the compose stack — blocks at an exact point, observe
   waiters through `pg_blocking_pids`, and inject connection loss with `pg_terminate_backend` only into backends blocked
   by their own lock. Load and worker-kill exercises are opt-in and run only against the local disposable stack;
   ledger volume tiers run against scratch `fundzim_perf*` databases.

## Consequences

- The KI-S18-25 window still exists (milliseconds, or until Recover after a crash) but is named, documented and
  settled by the detector instead of being reported; a ledger outage longer than the grace period is reported as
  intended.
- Ledger postings hold the locks they would take anyway, slightly earlier; the volume harness compares before and
  after (`docs/stage-19/benchmark-results.md`).
- Payments past their budget need an operator or later evidence; the operations portal counts them in
  `payments.reconciliation.open`.
- Migration `20261024090000_financial_concurrency.sql` adds one reason to a CHECK and `20261024090100_provider_health_indexes.sql`
  three indexes; no table, state or grant changes. On a large production table the indexes would need a concurrent
  build outside goose's transaction (no such procedure exists yet).

## Alternatives rejected

- Longer grace periods, retry-until-clean or suppressing RESERVATION_MISMATCH: they hide genuine stuck payouts.
- One transaction across the app and ledger pools: requires ledger privileges for the app role (ADR-042).
- A new "LEDGER_SYNC_PENDING" payout status: the condition is already derivable from the links, and a new status would
  need edges, notifications and UI for a millisecond state.
- Retrying ledger lock timeouts in `PostOnce`: unnecessary once the lock order is fixed, and it amplifies convoys.
- Marking budget-exhausted payments FAILED or EXPIRED: not authoritative evidence.
