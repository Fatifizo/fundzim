# Remediation phase 3 — complete-coverage guarantees after the corrections (section 7)

Reviewed on `01ea615`: `internal/platform/reconsweep`, the four detectors, the checkpoint tables, the guard trigger and
`app.apply_runtime_grants()`.

| Property | Status | Evidence |
|---|---|---|
| Required records are eventually examined | holds | Keyset walk in uuid order; a cycle re-reads every record. Records whose external check fails are deferred, never passed, and retried round-robin (`TestSweepFailingChecksNeverStopTheWalk`, integration `…PayoutLookupFailure`) |
| The checkpoint advances only after examination and recording | holds | Compare-and-swap after the batch. A time-budget stop inside a batch advances to the last *examined* key (`TestSweepTimeBudget`, `TestSweepSlowChecksRespectTheTimeBudget`) |
| A deferred record is never lost | holds | Carried in `deferred_keys` with its first time, also into the next cycle. Retries are bounded but rotate, so untried ones keep their place. The deferral limit fails closed (`DEFERRAL_LIMIT`, now BLOCKED_ON_DEFERRALS) |
| Previously examined records are re-examined when state changes | holds | Every cycle restarts from the nil cursor (`TestSweepReexaminesChangedRecordsNextCycle`) |
| Concurrent workers cannot corrupt progress | holds | One compare-and-swap winner per version; findings deduplicated by unique indexes. `RecordAttempt` changes no position and needs no version step (concurrency tests in both packages) |
| Test scopes cannot move production checkpoints | holds by convention | Every test sets its own scope (`it.*`, `perf.*`). A service without a scope falls back to `default` (CLAUDE.md rule; no code guard) |
| Duplicate findings stay controlled | holds | Open-uniqueness indexes. F-R2-01 makes keys more specific, never less (unit and integration repeat and concurrency cases) |
| Genuine discrepancies are not hidden by existing exceptions | holds after F-R2-01 | Distinct comparisons have distinct keys. Payout F-R1-03 rule (a new type under an open exception is recorded) unchanged |
| Recovery after interruption is safe | holds | No commit on cancel. A cancelled provider call is a run error, not a provider outcome (`reconcileOne` returns `ctx.Err()`) |

## The checkpoint guard — precise guarantee and remaining risk

**The database refuses:**
- deleting or truncating a checkpoint;
- any change to a COMPLETED cycle;
- changing identity columns;
- a cursor moving backwards or to NULL;
- decreasing counters;
- a version step other than +1 for any update that touches more than the attempt columns;
- a second RUNNING cycle per detector and scope;
- a status transition other than →RUNNING→COMPLETED;
- an attempt record outside the CHECK vocabulary or longer than 300 characters.

**The database cannot refuse, by design:**
- **A forward jump of the cursor**, dropping deferred keys, or completing a cycle early, done by a role with UPDATE on the
  table. The database cannot know which records were examined.
- **Resetting `failed_attempts`** or rewriting the attempt columns. This new, attempt-only path carries the same trust as
  above.

**Who holds UPDATE** (`apply_runtime_grants`):
- `app.payout_reconciliation_sweeps` and `app.fee_reconciliation_sweeps`: the runtime roles that receive `app` table
  privileges (fundzim_app and its peers). DELETE is refused by trigger.
- `ledger.ledger_reconciliation_sweeps`: only `fundzim_ledger` (Stage 10 rule).

**Write paths in code:** only `reconsweep.SQLStore.Advance` (compare-and-swap after examination) and
`SQLStore.RecordAttempt` (attempt columns only). archtest keeps other modules' SQL out of these tables (table ownership).

**Remaining risk.** A defect, or code acting with an app or ledger role's credentials, could move a checkpoint forward past
unexamined records, or hide failed runs. Such a move is not detected automatically. A possible detective control, not
implemented, is a periodic comparison of `examined` against the number of in-scope keys up to the cursor. Accepting this risk
or adding that control is a tech-lead and owner decision. It is recorded as an open condition, not fixed here, because no
defect demonstrated that a redesign is necessary.
