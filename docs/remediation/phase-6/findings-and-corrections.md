# Remediation phase 6 — findings and corrections

Author: Claude Code. **Nothing here has been independently reviewed** (D-01a: no reviewer exists). A correction described
as FIXED is fixed on branch `remediation/integrity-status` only. It is not merged, deployed or accepted.

## 1. Findings carried in (governance gate, Phase 5)

### F-R5-01 — a failed verification left a stale VERIFIED (LOW–MEDIUM) → FIXED on the branch, NOT REVIEWED

**Root cause.**
- `reconsweep.Verify` returned on a read error (`IntegrityCycles`, `Evidence` or `Source`) before `RecordIntegrity`.
- `RecordIntegrity` overwrote one status, and its `integrity_checked_at` was the time of recording.
- Nothing kept the last success apart from the latest outcome.
- A slow check finishing after a newer one overwrote it.

**Correction** (`integrity-status-design.md`):
- Every outcome is recorded:
  - a read failure as ERROR;
  - an ended context as INCOMPLETE/INTERRUPTED;
  - the budget as INCOMPLETE/TIME_BUDGET.

  Each carries a sanitised code, and the record is written with a context that is not cancelled.
- New columns, carried across cycles:
  - last success;
  - start of the unverified period;
  - consecutive failures;
  - latest failure, which survives recovery.
- Results are ordered by the start time of the check.
- An integrity state is derived, with its own metric and alert.

**Evidence.**
- Unit:
  - `TestVerifyRecordsSuccessThenFailure`
  - `…AFirstAttemptFailure`
  - `…TimeoutsAndInterruptions`
  - `…KeepsProblemsFoundBeforeAFailure`
  - `TestVerifyConcurrent`
  - `TestIntegrityState`
- Integration: `TestReconciliationIntegrityStatusFailureAndRecovery`, `…CarriesAndOrders`. See `test-evidence.md`.

### F-R5-02 — INCOMPLETE and UNVERIFIABLE did not reach health (LOW) → FIXED on the branch, NOT REVIEWED

**Correction.**
- The integrity state ages the reference time (the last success, else the first failure). It becomes STALE after
  `IntegrityStaleAfter`: 1 h, an **engineering default, not an approved SLO** (D-06, D-10).
- Health becomes INTEGRITY_UNVERIFIED.
- The integrity metric is DEGRADED for LATEST_FAILED and STALE, and is never HEALTHY.
- RECONCILIATION_SWEEP_INTEGRITY_UNVERIFIED (HIGH, in-app) is raised for STALE even when the sweep's health shows another
  state.
- Future timestamps beyond a 5-minute skew are STALE.
- A completed cycle that was never verified is STALE.

**Evidence.** `TestIntegrityState` (16 cases), `TestHealthClassification` (2 new cases),
`TestSecurityFindingsReconciliationIntegrityUnverified`, and the web test for the state display.

### F-R5-03 — deployment ordering hazard (MEDIUM, deployment) → ANALYSED AND TESTED; procedural control remains

**Correction.**
- `migration-compatibility.md`:
  - the code × schema matrix;
  - the safest rollout order;
  - the rollback;
  - the residual mixed-fleet risk.
- `TestReconciliationMigrationCompatibility` runs the earlier code's statements on a scratch database at each schema level.
  - Phase 3 code is refused on the Phase 4/6 schemas.
  - Phase 4 code works on the Phase 6 schema.
  - Phase 6 code fails closed on the older schemas.
  - The Down and the second Up round-trip.

**Not fixable in code.** Old code on the Phase 4 schema stops advancing **by design** (an advance without evidence is
refused). The control stays procedural: deploy code and migrations together, migration first, with the services stopped.
`20261026120000` and `20261027090000` were **not edited** (goose stores no checksum; an applied migration is never edited).

### F-R5-04 (storage estimate) — unchanged; reviewed in §3

### F-R5-05 (CI install slowdown) — watched; see `test-evidence.md` §4 for this phase's CI timings

## 2. New findings in this phase

| Id | Severity | Finding | Status |
|---|---|---|---|
| F-R6-01 | LOW | The first version of `20261028090000` Down failed: "cannot ALTER TABLE … pending trigger events". The ERROR → UNVERIFIABLE rewrite queued the deferred evidence trigger in the same transaction | **FIXED** in `b9496c3` (`SET CONSTRAINTS ALL IMMEDIATE`). Found by the compatibility test on `fundzim-r9`. The migration had been applied only to disposable databases, so no retained database ever held the earlier text |
| F-R6-02 | LOW (test) | `TestReconciliationIntegrityKilledBeforeCheckpointAdvance` (Phase 4) depended on data volume. On a fresh database with ≤ 20 payouts, the resumed run completes the cycle and the running row belongs to the next cycle | **FIXED** in `b9496c3`. The advance is now checked on the killed run's own row; the assertion is unchanged in substance |
| F-R6-03 | LOW | Phase 4 `RecordIntegrity` stamped the recording time, so concurrent checks could record out of order | **FIXED** with F-R5-01 (start-time ordering) |
| F-R6-04 | LOW (deployment) | A mixed Phase 4/Phase 6 fleet can let a Phase 4 VERIFIED win the ordering. It is read as at worst STALE, never a false VERIFIED | **OPEN — procedural** (stop all services for the migration, `migration-compatibility.md` §3) |
| F-R6-05 | LOW (documentation) | The storage projection counted evidence keys only, not checkpoint rows. Each completed cycle keeps one row with an `integrity_detail` of at most 16 KB | **ASSESSED** in §3 |
| F-R6-06 | INFO | Detectors with completed but never-verified cycles show INTEGRITY_UNVERIFIED after deployment until their first verification (≤ 15 min) | **ACCEPTED as accurate** (design §8.2). The operator should expect it |

## 3. Storage estimate review (F-R5-04, F-R6-05)

- **Evidence.** The Phase 5 projection (`docs/governance/owner-decision-register.md`) stands. Phase 6 adds no evidence
  rows: at most ≈26–51 MB/day across all detectors at today's budget ceilings (≈10–20 B per key, 96 runs a day).
- **Checkpoint rows (not previously counted).**
  - One row per completed cycle per detector and scope. A detector whose table is smaller than its per-run budget can
    complete a cycle every run: at most 96 rows/day × 4 production detectors.
  - Phase 6 adds 7 small columns to each row (< 100 B). The jsonb `integrity_detail` is bounded at 16 KB by CHECK; its
    measured size is in `test-evidence.md` §3.
  - Upper bound: 4 × 96 × (16 KB + ≈1 KB) ≈ **6.5 MB/day**. The measured typical size is far lower.
- **Retention.** No retention period is chosen here. LR-012 remains with the owner and counsel; nothing is deleted.
