# Remediation phase 2 — review of the Phase 1 reconciliation implementation (phase D)

**Scope.** `internal/platform/reconsweep`, the sweep wiring in `internal/payouts/reconcile.go`, `internal/fees/reconcile.go`,
`internal/ledger/reconcile.go` and `internal/payments/facts.go`, migration `20261025090000`, and the tests. Reviewed at
`c8fb8c6`. Phase 2 changes no reconciliation code.

**Method.** A separate review agent reviewed the code read-only, without having written it. Every defect below was then
re-checked against the code by hand before being recorded. The unit tests ran on the reviewed code: reconsweep 10/10 pass
(review agent), and all 40 packages pass under `-race` on `efa7b62` (test-evidence.md).

**ADR-052 status: Proposed.** It is not an approved architectural decision. Approval belongs to the owner and tech lead, and
this review is one input to it.

## 1. Claims

| # | Property | Verdict | Evidence |
|---|---|---|---|
| 1 | Complete keyset coverage for PAYOUTS, FEE_ASSESSMENTS, PAYMENT_JOURNALS, PAYMENT_FACTS | **CONFIRMED** | Every read is `id > $cursor::uuid … ORDER BY <table>.id LIMIT n` (payouts `reconcile.go:142-145`, fees `sweepNext`, ledger `reconcile.go` ~300, payments `facts.go` `PaymentFactIDsAfter`). "Strictly after" in `reconsweep/sweep.go:213`; `After("")` is the nil uuid. There is no `::text` ordering (F-R1-06 fixed). No off-by-one: `complete := len(keys) < limit`, and a full final batch is followed by an empty batch that closes the cycle |
| 2 | A checkpoint advances only after examine **and** record; compare-and-swap | **CONFIRMED** | An examine error returns before `Advance` (`sweep.go:193-195, 219-221`). Each module records its findings inside the examine callback. CAS `WHERE id=$1 AND version=$2 AND status='RUNNING'` (`sqlstore.go:88-91`); the DB requires version + 1 |
| 3 | Checkpoints cannot be rewritten or deleted by an unintended path | **PARTIAL** (design limit) | Denied by trigger: DELETE and TRUNCATE, changes to a COMPLETED row or to identity columns, a cursor moving backwards or to NULL, decreasing counters, version steps other than +1, a second RUNNING row (partial unique index), and transitions other than →RUNNING→COMPLETED. **Not denied:** a role with UPDATE can jump the cursor *forward*, drop deferred keys, or complete a cycle early. The database cannot know which records were examined. Protection there rests on grants (the owning module's roles; ledger checkpoints only `fundzim_ledger`) and on code review. ADR-052 states this accurately ("never moves backwards"). The Phase 1 completion message said the database refuses "any attempt to move" a position, which overstated it and is corrected here |
| 4 | Interruption never skips records | **CONFIRMED** | No UPDATE commits on cancel; `examined` is persisted only through `Advance` (`sweep.go:245`); the budget is checked between batches only. Integration interruption tests and the ledger volume runs (Phase 1: 7 and 13 interrupted runs, all records examined) agree |
| 5 | Deferred records are re-examined, never lost | **PARTIAL** | Engine: deferrals are retried at the start of every run, keep their first `since`, and carry into the next cycle. Payouts and PAYMENT_FACTS defer. **Fees never defers:** an assessment inside the grace period skips its MISSING_POSTING check but counts as examined, so it is checked only in the next cycle. That is a latency, not a loss (the next cycle re-examines every assessment). Overflow fails closed but can stall (F-R2-03) |
| 6 | Re-evaluation when authoritative state changes | **CONFIRMED** | Each completed cycle opens a new one from the nil cursor (`sqlstore.go:100`); `TestSweepReexaminesChangedRecordsNextCycle` |
| 7 | Overlapping execution cannot corrupt progress | **CONFIRMED** | Exactly one CAS wins per version; the loser stops with CHECKPOINT_CONFLICT. Cycle close and next-cycle insert happen in one transaction under the unique index. Duplicate findings are absorbed by the open-uniqueness indexes |
| 8 | Test scopes cannot advance the `default` checkpoint | **CONFIRMED** | Every test caller sets a scope (`itSweep` / `ownSweep`); the perf harness uses `perf.*`; ledger non-payments sources derive `<scope>.<source>`. A service *without* a scope falls back to `default` (`sweep.go:293`). The CLAUDE.md rule covers this; no code guard exists |
| 9 | Deduplication never suppresses genuinely new discrepancies | **NOT CONFIRMED** | F-R1-03 is fixed for payouts (a new mismatch *type* is recorded under an open exception). Fees: F-R2-01 |

## 2. Findings (new in Phase 2)

| ID | Severity | Finding | Concrete failure | Introduced by | Proposed targeted correction |
|---|---|---|---|---|---|
| F-R2-01 | MEDIUM | Fee MISSING_FEE dedupe key omits the category: `kind:assessmentID:txID` (`fees/reconcile.go:474`), and `missingFees` keys every finding of a group to `group[0].ID` with an empty txID (`fees/reconcile.go:221`) | A source group missing two fee categories A and B produces two findings with the same key. B's insert hits `ON CONFLICT … DO NOTHING`, so only A is reported. B stays unreported until A's reference is resolved | Stage 12 key scheme. Phase 1 kept it when it moved group judgement to `group[0]` (F-R1-04) | Add the category to the MISSING_FEE dedupe key (`…:category`). Existing open references keep their old key, and the open-category lookup (`have`) already stops A from being reported twice. Add a test with two missing categories in one group. No migration needed |
| F-R2-02 | MEDIUM | A payout whose provider check could not run counts as examined: in `reconcileOne` (`payouts/reconcile.go:366-387`), an `adapterFor` error, or a `GetPayout` class other than Accepted / NotFound (timeout, unavailable), skips the provider comparison without a trace | During a provider outage the cursor passes submitted payouts whose provider-side amount or status was never compared. The coverage watermark reports them as covered; a mismatch is caught only if a later cycle's call succeeds. (Today only the simulator exists, `ExecLocalSimulator`) | pre-existing (Stage 11 / 19 windowed pass); Phase 1 now bases a coverage claim on it | Return a *deferred* outcome when the provider comparison was required but could not run, so the record stays in the deferral list and is retried. Report it as `provider_unverified` in the sweep report and metric. Under a long outage the deferral limit stops the run (fail closed) instead of silently covering. Test with a simulator timeout scenario |
| F-R2-03 | LOW (liveness) | A sweep can stall without anyone being alerted | (a) More than 10 000 deferrals stop every run before `Advance` (`sweep.go:234-237`), and that run's deferral resolutions are lost. (b) One record whose examine always errors blocks every record after it. (c) A PAYMENT_JOURNALS error also skips the PAYMENT_FACTS sweep for that run (`ledger/reconcile.go:316`). Each case is visible as an ERROR run and a growing coverage age, but no alert delivery exists (B-12) | Phase 1 (KI-R1-03 and KI-R1-09 cover part of it) | (c) Run PAYMENT_FACTS independently and report both errors. (b) After N consecutive failures on the same key, defer that key and open a SWEEP_RECORD_FAILING exception (never skip silently). (a) and (b) need an alert rule on coverage age and on consecutive ERROR runs once an alert destination exists (D-10) |

Minor observations (no action proposed now):
- Payout dedupe works per type. A different value of the same type while its exception is open is not re-recorded, and a SEV1
  type added under an open SEV2 exception does not raise the exception's severity.
- Ledger DUPLICATE_POSTING keeps one open exception per payment. A permanent duplicate reopens an exception after each
  resolution, which is correct but noisy.
- Deferral retries are bounded by `MaxDeferred`, not by `MaxRecords`.
- `deferred_since` and payout `detected_at` use the application clock; the checkpoint timestamps use the database clock.

## 3. Assessment

The coverage engine itself (claims 1, 2, 4, 6, 7) is sound. No defect was found that skips a record, advances a checkpoint past
an unexamined record, or produces a duplicate or incorrect *financial effect*. Reconciliation records findings only; it moves
no money and changes no ledger.

The two MEDIUM findings are **detection-quality** defects:
- F-R2-01: a second discrepancy in the same group is suppressed.
- F-R2-02: a record is treated as covered although its provider-side comparison never ran.

Neither stops integration planning. Both should be fixed (small, targeted changes with tests) **before** the reconciliation
blocker B-05 is closed, or explicitly accepted by the owner as conditions. Phase 2 does not fix them: the brief limits this
phase to documenting a defect and proposing a correction, not rewriting reconciliation.

Observation from the development database (read-only, 2026-10-26 08:04 UTC): after the restart, the development worker's
`default` sweeps ran on real development data.

| Detector | Cycles completed | Cycle in progress |
|---|---|---|
| FEE_ASSESSMENTS | 1 | — |
| PAYMENT_FACTS | 3 | — |
| PAYMENT_JOURNALS | 7 | — |
| PAYOUTS | — | cycle 1: 13 998 of 14 712 payouts examined, 2 exceptions, 70 batches, 0 deferrals |

This matches the measured budget arithmetic: 2 000 payouts per 15-minute run.
