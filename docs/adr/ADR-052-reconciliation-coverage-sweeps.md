# ADR-052: Complete, resumable coverage of the internal reconciliations — durable keyset sweeps beside the recency windows

- **Status:** Proposed
- **Date:** 2026-10-25
- **Deciders:** Technical lead (remediation phase 1); the coverage *requirement* (freshness, volume) is owner decision D-06/D-05
- **Stage:** Remediation phase 1 (after Stage 20; not a product stage)
- **Amends:** ADR-043 §8 (payout reconciliation), ADR-044 (fee reconciliation), ADR-042 (ledger reconciliation), ADR-051 (the
  Stage 19 newest-first windows stay as the recency pass)
- **Addresses:** KI-S19-09 (HIGH, launch blocker B-05), KI-S11-15, F-R1-01 (new: ledger succeeded-payment window read the oldest
  5 000)

## Context

The three internal reconciliations (payouts, fees, ledger journals vs payment facts) were windowed scans every 15 minutes:
the newest 5 000 payouts (by creation), the newest 5 000 fee assessments, the newest 10 000 payment capture journals.
Remediation preflight found a fourth window that Stage 20 did not list: the ledger's succeeded-payment check read the
**oldest** 5 000 SUCCEEDED payments (`PaymentFactsInStatus … ORDER BY updated_at`), the same defect class as F-S19-13 — once
more than 5 000 succeeded payments exist, new ones are never checked for a missing ledger evaluation. None of these windows
guarantees that every record is ever examined, and a record that becomes inconsistent after leaving its window (for example a
later provider-side change to an old payout, or a payment fact that changes after its journal was compared) is never
re-detected. Stage 20 raised this to HIGH (B-05).

Constraints: each module owns its tables and reaches others only through services (design baseline §5; the ledger writes only
on the `fundzim_ledger` pool); external provider state can change without any local signal (no change feed can see it); the
payout check costs a provider status query per submitted payout; payout polling updates `payout_requests.updated_at`, so
timestamps are not a reliable change signal; no capacity or freshness target is approved (D-05, D-06).

## Options considered

| Option | Coverage of every record | Re-detects later changes | Notes |
|---|---|---|---|
| Larger windows | no | no | moves the cliff; cost grows with volume |
| Timestamp watermark (`updated_at > last`) | only for changes that bump the timestamp, and only with careful commit-order handling (a transaction committing late with an earlier timestamp is skipped) | not for provider-side or cross-module changes (no local timestamp moves) | polling bumps `updated_at` constantly; precision/commit-order hazards |
| Change tracking (triggers / outbox per change) | for local changes | not for provider-side changes | new write-path coupling into financial tables; still needs a sweep for external state |
| Version / sequence column per table | as above | as above | schema change on financial tables |
| **Durable keyset sweep over the primary key, re-run cycle after cycle** | **yes**: every record existing at a cycle's start is examined in that cycle | **yes**: every cycle re-reads every record in its current state, including external state | cost proportional to table size per cycle; bounded per run |

Only a periodic full re-verification can see provider-side drift, so a sweep is required whatever else is added; a sweep alone
also covers local changes. It is the narrowest design that gives the guarantee. The existing newest-first windows are kept as a
fast *recency pass* (detection latency after recent transitions); they guarantee nothing and are documented as such.

## Decisions

1. **Sweep engine (`internal/platform/reconsweep`, no tables).** A detector walks its scope in primary-key (UUID) order, a
   bounded batch at a time (`id > $cursor ORDER BY id LIMIT n`; the nil UUID before the first batch). After every record of a
   batch has been examined *and its findings recorded*, the checkpoint is advanced by compare-and-swap on its version. When a
   batch is short the cycle is COMPLETED and the next cycle opens at the start of the key range. At most one cycle completes
   per run. No timestamp ordering or precision is relied on — keys only.
2. **Guarantees.** (a) Every record existing when a cycle starts is examined in that cycle or carried as a deferral until it is.
   (b) A change committed at time *t* is examined by the end of the first cycle that starts after *t* (cycle start times are the
   database's transaction time). (c) The checkpoint never passes an unexamined record: a crash, error, deadline or lost race
   leaves it at the last fully examined batch, and the batch is examined again (findings are deduplicated by their owners, so
   repetition is harmless). (d) Concurrent runs are safe without a lock held across provider calls: both may examine a batch,
   only one advances (the loser stops with CHECKPOINT_CONFLICT). (e) The coverage watermark — the start of the last completed
   cycle — is persisted and shown in the operations portal; no completed cycle = coverage NOT established (status UNKNOWN).
3. **Deferrals instead of silent skips.** A record the detector may not judge yet (inside its grace period: payouts changed in
   the last 5 minutes, payments succeeded in the last 5 minutes or UNKNOWN for less than an hour) is carried in the checkpoint
   (key + first deferral time), retried at the start of every run and across cycles, and counted; the oldest deferral age is
   visible. More than `MaxDeferred` deferrals → the checkpoint is **not** advanced (fail closed, DEFERRAL_LIMIT).
4. **Checkpoints are owned by each module, guarded in the database.** `app.payout_reconciliation_sweeps` (payouts),
   `app.fee_reconciliation_sweeps` (fees), `ledger.ledger_reconciliation_sweeps` (ledger, written only by `fundzim_ledger`).
   One row per cycle; a guard trigger enforces one RUNNING cycle per detector and scope (partial unique index), immutable
   identity columns, a cursor that never moves backwards, counters that never decrease, version + 1 per update, immutable
   COMPLETED cycles, no delete or truncate; status changes go through `app.guard_transition`. The SQL lives in one platform
   type (`reconsweep.SQLStore`) parameterised with the owning module's table name (validated against
   `^(app|ledger)\.[a-z_]+_reconciliation_sweeps$`); archtest lists each table under its owner.
5. **Detectors and scopes.** `PAYOUTS` (every payout past PAYOUT_REQUESTED; the examination is the Stage 19 `reconcileFresh`:
   re-read, settle a pending ledger sync, judge), `FEE_ASSESSMENTS` (every assessment; MISSING_FEE is judged per whole
   source/calculation group, keyed by the group's first assessment in both passes, and an OPEN MISSING_FEE on any member
   counts), `PAYMENT_JOURNALS` (every capture journal of the reconciled source system; duplicates counted across the whole
   ledger, not within a batch), `PAYMENT_FACTS` (every SUCCEEDED or UNKNOWN payment, read through the new
   `PaymentFacts.PaymentIDsAfter`). The production scope is `default`; a run over another journal source (synthetic fixtures)
   uses `<scope>.<source>`; integration tests use their own scopes and never move the worker's checkpoints.
6. **Run budgets are engineering defaults, not capacity targets.** Per run: payouts 200/batch, 2 000 records, 3 min; fees
   500/5 000/3 min; each ledger detector 1 000/10 000/3 min; 10 000 deferrals. Cycle duration = scope size ÷ per-run budget ×
   15 minutes. A freshness requirement is the owner's (D-06); the measured costs are in the phase report.
7. **Recency windows read the newest records.** The payment-facts window is now newest-first (F-R1-01), matching the binding
   Stage 19 rule; window sizes are unchanged and overridable for tests.
8. **Changed discrepancy under an open payout exception.** A payout with an open RECONCILIATION_MISMATCH exception no longer
   suppresses *new* mismatch types: a type not referenced since the exception opened is recorded as an immutable reference
   (evidence on the open exception; no second exception, no event); a repeated type is not recorded again. Ledger and fee
   references already deduplicate per type/kind. Nothing closes or suppresses an exception automatically.
9. **Evidence.** Each run's report carries the sweep report (scope, cycle, cursor from/to, examined, out of scope, deferrals
   new/retried/resolved/carried, oldest deferral, exceptions, batches, checkpoint advanced, cycle completed, stop reason) and
   the coverage; the payout and ledger run audit events include it; fee runs carry it in their result; checkpoint rows hold the
   per-cycle counters and the last run id. A run whose sweep fails still records its audit event (non-cancelled context).

## Consequences

- Every record is re-verified once per cycle; detection of a later change is bounded by one cycle plus the 15-minute schedule.
- Cost per cycle is proportional to the table (the payout sweep makes one provider status query per submitted payout);
  measured on the development host in the phase report, not a production claim.
- One checkpoint row per detector per completed cycle accumulates (never deleted: operational evidence; a retention period is
  not invented — LR-012).
- Stage 17 statement reconciliation remains the settlement control; settlement reconciliation stays NOT_PERFORMED.
- Not addressed: Stage 17's 93-day statement horizon, risk reconciliation's 30-day window (not a financial detector), batched
  exception writes (KI-S19-16).

## Amendment 1 — remediation phase 3 (2026-10-26; status of the ADR unchanged: Proposed)

Findings F-R2-01 … F-R2-03 of the phase 2 review (`docs/remediation/phase-2/reconciliation-review.md`) change four rules. Details,
root causes and evidence: `docs/remediation/phase-3/`.

1. **External checks that cannot be made defer the record (F-R2-02).** A detector reports `Outcome{Deferred, Reason}` when a
   required external comparison could not be made (payouts: the provider status lookup timed out, lost its connection, was
   rate limited, refused, unsupported, malformed or incomplete; `PROVIDER_UNVERIFIED`). Such a record is not reconciled, is not
   counted as covered, and nothing is inferred from the failure. Internal comparisons found meanwhile are still recorded.
2. **Deferrals never stop the walk (F-R2-02).**
   - A run retries at most half its record budget of deferrals, and stops retrying at the time budget. Untried deferrals keep
     their place at the front of the list; retried ones that stay deferred move to the back (round-robin; each keeps its first
     deferral time).
   - The time budget is also checked before each record of a batch, after at least one record. The checkpoint then advances to
     the last examined key only, so the "never past an unexamined record" guarantee is unchanged.
   - The deferral limit still fails closed (`DEFERRAL_LIMIT`).
3. **Every run's outcome is recorded (F-R2-03).** `Store.RecordAttempt` writes `last_attempt_at`, `last_attempt_outcome`,
   `last_attempt_error` and `failed_attempts` (consecutive) on the RUNNING checkpoint (migration `20261026120000`). The guard
   accepts an update that changes only these columns without a version step; every other rule of the guard is unchanged.
4. **Health is a first-class signal (F-R2-03).** `Coverage.Health` classifies the current cycle from the checkpoint rows alone:
   - states: NOT_STARTED, PROGRESSING, PROGRESSING_SLOWLY, STALLED, BLOCKED_ON_DEFERRALS, FAILING;
   - last cycle: NONE, COMPLETED_CLEAN, COMPLETED_WITH_DISCREPANCIES.

   Its thresholds are **engineering defaults**, not approved targets (D-06, D-10): stall after 1 h without progress, slow after
   a 24 h cycle, a deferral older than 24 h, failing after 3 consecutive failed runs. Each module publishes a
   `*.reconciliation.sweep_health*` operations metric, and the Stage 18 alert scan raises RECONCILIATION_SWEEP_FAILING,
   RECONCILIATION_SWEEP_STALLED and RECONCILIATION_SWEEP_BLOCKED as in-app alerts. No alert leaves FundZim (no delivery
   channel exists).

Also: the ledger's PAYMENT_FACTS sweep runs even when the PAYMENT_JOURNALS sweep failed in the same run (both errors are
returned), and fee findings carry a check discriminator in their dedupe key (F-R2-01; outside the engine).

## Amendment 2 — remediation phase 4 (2026-10-26; status of the ADR unchanged: Proposed)

Checkpoint integrity (`docs/remediation/phase-4/checkpoint-integrity-design.md`). Before this amendment, the guard prevented
moving a checkpoint backwards but not forwards past unexamined records.

1. **Evidence per advance.** `Store.Advance` takes the batch's `Evidence` and stores it in the same transaction: range, the
   examined keys, deferrals added and resolved. Evidence is stored in append-only `*_reconciliation_sweep_batches` tables
   (migration `20261027090000`).
2. **Preventive.** Deferred constraint triggers refuse, at commit, any checkpoint position change without the matching
   evidence row (from = old cursor, to = new cursor, examined + its key count, batches + 1, deferral set equation). They also
   refuse a new cycle that does not start empty with the previous cycle's deferrals. This applies to every role.
3. **Detective.** `reconsweep.Verify` runs after every sweep run. It re-reads the scope through the module's independent
   `Source` (a set query with modification times) and checks the running and last completed cycles. The outcome is recorded
   on the checkpoint as VERIFIED, INCONSISTENT, INCOMPLETE or UNVERIFIABLE. INCONSISTENT makes the health INTEGRITY_FAILURE
   and raises a CRITICAL in-app alert. The verifier never moves a checkpoint or changes money.
4. **Limit.** Evidence that lists real keys which were never examined cannot be distinguished from a real examination.
   Evidence retention is not decided (LR-012).
