# Reconciliation coverage — investigation, model and design (KI-S19-09)

Decision record: [ADR-052](../../adr/ADR-052-reconciliation-coverage-sweeps.md). This document is the investigation and the
coverage model behind it.

## 1. Every reconciliation path found (before this phase)

| Path | Code | Trigger | Selection before | Exceptions and dedupe | Coverage before |
|---|---|---|---|---|---|
| Payout reconciliation | `internal/payouts/reconcile.go` `Reconcile` | River `payouts.reconcile` every 15 min (`RunOnStart: false`, 10 min timeout); staff `payout.operate` | `status <> 'PAYOUT_REQUESTED' AND updated_at < now − 5 min ORDER BY created_at DESC LIMIT 5000` | immutable `app.payout_reconciliation_references`; one unresolved RECONCILIATION_MISMATCH exception per payout (partial unique index); while open, **every** new mismatch suppressed | newest 5 000 by creation; never older |
| Fee reconciliation | `internal/fees/reconcile.go` | River `fees.reconcile` (15 min); staff `POST /admin/fees/reconciliation/run` | `ORDER BY assessed_at DESC LIMIT 5000`; MISSING_FEE grouped within that list only | `app.fee_reconciliation_references`, OPEN unique per `dedupe_key = kind:assessment:tx` | newest 5 000; a source group split by the window judged on a partial group |
| Ledger: journals vs payment facts | `internal/ledger/reconcile.go` §1 | River ledger reconcile (15 min); staff | newest 10 000 `DONATION_CAPTURED` payment journals by `posted_at` (Stage 19 F-S19-13 fix) | `ledger.ledger_reconciliation_references`, unique OPEN per (type, source_type, source_id) | newest 10 000; duplicates counted only within the window |
| Ledger: succeeded payments evaluated/posted | `internal/ledger/reconcile.go` §2 via `payments.PaymentFactsInStatus` | same run | `status = 'SUCCEEDED' AND updated_at < now − 5 min ORDER BY updated_at, id LIMIT 5000` — **oldest first** | as above | **oldest 5 000**: once more exist, no new payment is ever checked (**F-R1-01**, same class as F-S19-13, not listed by Stage 20) |
| Ledger: long-UNKNOWN payments | same, `PaymentFactsInStatus('UNKNOWN', now − 1 h, 1000)` | same run | oldest 1 000 | as above | oldest 1 000 |
| Ledger: suspense balances | same §3 | same run | every SUSPENSE account with a non-zero balance | as above | complete (no window) |
| Stage 17 statement reconciliation | `internal/reconciliation` | per scope | statement periods within 93 days of the newest import; scopes with open lines re-run | maker-checker resolutions | covers statement periods; out of scope here (settlement control, unchanged) |
| Payments reconciliation references | `internal/payments` (UNKNOWN horizon, POLL_BUDGET_EXHAUSTED) | polling / horizon jobs | per payment, event-driven | `app.payment_reconciliation_references` | per payment; not windowed (unchanged) |
| Risk reconciliation | `internal/risk/reconcile.go` | 15 min | evidence added in the last 30 days, newest 5 000 | risk cases | not a financial detector; unchanged (noted) |

Different services implement different rules and scopes (confirmed): payouts re-read and settle a pending ledger sync first
(ADR-051), fees recompute calculations, the ledger compares facts read through an interface. They share no code; this phase
gives them one coverage engine and leaves each detector's judgement unchanged.

## 2. Root causes

1. Windowed full scans with a fixed `LIMIT` and no persisted position: what is outside the window is never examined.
2. Windows over *creation* order (payouts, fees) or *posting* order (journals): a change to an old record does not bring it into
   the window; provider-side changes have no local signal at all.
3. One window ordered oldest-first (succeeded payments) — after 5 000 records, new ones are never reached (F-R1-01).
4. Grouping and duplicate detection computed within the window (fee MISSING_FEE, ledger DUPLICATE_POSTING) — wrong at the edge.
5. Payout exceptions suppressed every new mismatch while open, so a changed discrepancy disappeared silently.

## 3. Coverage model (brief §6.2)

1. **Record classes reconciled.** Payouts past PAYOUT_REQUESTED (`PAYOUTS`); fee assessments (`FEE_ASSESSMENTS`); payment
   capture journals of the reconciled source system (`PAYMENT_JOURNALS`); payments in SUCCEEDED or UNKNOWN (`PAYMENT_FACTS`);
   suspense accounts (already complete each run).
2. **Entering scope.** A record is in scope when its key exists in the detector's table with the scope predicate true; it enters
   by being inserted (any key) or by its status entering the predicate (payments becoming SUCCEEDED/UNKNOWN, payouts leaving
   PAYOUT_REQUESTED).
3. **Older records stay discoverable.** The walk is over the primary key from the nil UUID to the end, every cycle; age is
   irrelevant.
4. **Changed records re-evaluated.** Each cycle re-reads every record in its current state (and the provider's current
   state for payouts). A change committed at *t* is examined by the end of the first cycle starting after *t*; within the
   recency window it is usually seen within one 15-minute run.
5. **Incomplete or failed runs resume.** The checkpoint is the last key of the last fully examined batch; the next run starts
   after it. A failure before the checkpoint write repeats the batch; after it, nothing is repeated or lost.
6. **Concurrent updates.** A record updated while its batch is examined is examined in whichever state the read sees; the
   other state is examined in the next cycle. Payouts inside their 5-minute grace period are deferred (not judged in a
   transitional state); the Stage 19 settle-before-judge path is unchanged.
7. **Permanent omission is impossible** short of a record changing forever within its grace period, which is then visible as
   an outstanding deferral with its age; deferral overflow stops the checkpoint (fail closed).
8. **Duplicate findings.** Owners' dedupe is unchanged (unique OPEN indexes; payout recorder under the payout row lock); repeat
   examination of a batch (crash, conflict, recency pass + sweep in one run) records nothing new.
9. **Genuine discrepancies stay detectable.** Nothing closes or suppresses an exception automatically; a resolved exception
   whose cause persists is reported again by the next examination (existing semantics); a new mismatch type under an open payout
   exception is recorded as evidence.
10. **Proof of coverage.** Per completed cycle the database holds `started_at`, `completed_at`, `examined` (counted only by the
    winning checkpoint writes, so each key once), the final key and the deferrals carried. Tests compare `examined` with
    `count(*)` of the scope up to the final key. The operations portal shows the age of the coverage watermark per detector;
    no completed cycle = UNKNOWN.

No accounting-period cutoff, settlement policy or freshness target is invented: the cycle duration is a consequence of the
run budgets and table sizes, and the acceptable maximum is owner decision D-06.

## 4. Invariants (database-enforced unless stated)

- C1 one RUNNING cycle per (detector, scope) — partial unique index.
- C2 a checkpoint's cursor never decreases within a cycle and is never reset — guard trigger.
- C3 `examined`, `exceptions`, `batches` never decrease; `version` increases by exactly one per update — guard trigger.
- C4 a COMPLETED cycle is immutable and cannot be reopened — guard trigger + `app.guard_transition`.
- C5 checkpoints are never deleted or truncated — `app.forbid_mutation`.
- C6 the checkpoint is written only after its batch's findings are committed (engine order; unit-tested).
- C7 `fundzim_app` cannot read or write ledger checkpoints; `fundzim_ledger` cannot read app checkpoints — grants (tested).
- C8 a run over a non-payments journal source never writes the payments checkpoints (scope derivation; tested).

## 5. Concurrency

Allowed: the periodic job (River, unique per period) and a staff-triggered run may overlap; a second worker process may run the
same job kind. No lock is held across provider calls or batches: overlapping runs both examine, one wins the compare-and-swap,
the other stops (CHECKPOINT_CONFLICT). The payout recorder keeps its `FOR UPDATE` on the payout row; ledger and fee recorders
keep their unique-index dedupe. No distributed lock was introduced.

## 6. What the tests prove

See [test-evidence.md](test-evidence.md) for the brief's 25 cases mapped to tests and their results.
