# Integrity-status design (F-R5-01, F-R5-02)

Status: **implemented on branch `remediation/integrity-status`; NOT REVIEWED** (author: Claude Code; no independent
reviewer exists, D-01a). ADR-052 Amendment 3. Migration `20261028090000_reconciliation_integrity_status.sql`.

## 1. The defects

| Id | Before | Consequence |
|---|---|---|
| F-R5-01 | `reconsweep.Verify` returned early on a read error (the cycles, the evidence or the source) without recording anything. `RecordIntegrity` stamped `integrity_checked_at = now()` (time of recording, not of the check) and kept no history | After one success, every later failed verification left **VERIFIED** as the visible status, with a timestamp that did not age into a signal. An older, slow check finishing last could overwrite a newer outcome |
| F-R5-02 | `Coverage.Health` looked only at `INCONSISTENT`. INCOMPLETE (time budget) and UNVERIFIABLE never changed health; there was no age check | A verifier that never completes at volume, keeps timing out, or cannot read raised no signal |

## 2. What is recorded (per running checkpoint; attempt-only columns)

| Column | Meaning | Written |
|---|---|---|
| `integrity_checked_at` | **start** of the latest recorded check (any outcome) | every outcome; only if not older than the recorded one |
| `integrity_status` | latest outcome: VERIFIED, INCONSISTENT, INCOMPLETE, UNVERIFIABLE, **ERROR** (new) | every outcome |
| `integrity_detail` | bounded report (cycles, problems, elapsed) | every outcome |
| `integrity_error` *(new)* | sanitised reason code of a latest outcome that is not VERIFIED (or of INCONSISTENT found before a failure) | every outcome; NULL on success |
| `integrity_verified_at` *(new)* | start of the last **VERIFIED** check | only on VERIFIED; a failure never clears it |
| `integrity_unverified_since` *(new)* | start of the first non-VERIFIED check since the last VERIFIED | set on the first failure; cleared on VERIFIED |
| `integrity_failures` *(new)* | consecutive non-VERIFIED checks | +1 per failure; 0 on VERIFIED |
| `integrity_last_failure_at`, `integrity_last_failure` *(new)* | the latest non-VERIFIED check and `STATUS[:CODE]` | on failure; **kept** after a later success |

The row's position, counters, deferrals, status and version never change through these columns. The guard's attempt-only
set includes them, and the deferred evidence trigger ignores updates whose version did not change. The next cycle's
INSERT carries all of them from the completed row, so a cycle boundary erases neither a failure nor the last success.

Reason codes (fixed vocabulary; a CHECK refuses anything but `^[A-Z_]{1,64}$`, so a database message cannot be
stored):

| Code | Status | Cause |
|---|---|---|
| CYCLES_READ_FAILED | ERROR | the checkpoint rows could not be read |
| EVIDENCE_READ_FAILED | ERROR | a cycle's evidence could not be read |
| SOURCE_READ_FAILED | ERROR | the module's independent `Source` read failed (the dependency is unavailable) |
| INTERRUPTED | INCOMPLETE | the run's context ended (job time-out, shutdown, cancellation) |
| TIME_BUDGET | INCOMPLETE | the verifier's own re-read budget ended first |

When problems were already found before a read failed, the outcome stays **INCONSISTENT** with the code. What was found is
never downgraded. The outcome is written with `context.WithoutCancel`, so an interrupted run still records it. The error is
still returned to the caller, which joins it into the run's error as before.

## 3. Ordering and concurrency

The UPDATE applies only `WHERE integrity_checked_at IS NULL OR integrity_checked_at <= <this check's start>`:

- A slow check that started earlier and finishes later is discarded.
- The newest-started check wins regardless of completion order.
- The rule is decided in one statement on one row, so no lock is held across the re-read.

The first two properties are tested on the real database (`TestReconciliationIntegrityStatus*`: an injected older
VERIFIED is ignored; eight concurrent checks, half failing, leave the outcome of the newest start).

## 4. Integrity state (pure, `Coverage.integrityState`)

| State | Condition |
|---|---|
| INCONSISTENT | latest status INCONSISTENT |
| NOT_RUN | never checked, and no cycle has completed and the running cycle is younger than `IntegrityStaleAfter` (a new detector) |
| STALE | never checked although a cycle completed or the running cycle is older than the threshold; **or** the reference time is older than `IntegrityStaleAfter`; **or** a timestamp is more than 5 min in the future |
| VERIFIED | latest status VERIFIED within the threshold |
| LATEST_FAILED | latest status not VERIFIED, the last success (or the first failure) within the threshold |

The reference time is chosen in this order:
1. `integrity_verified_at`;
2. if there was never a success, `integrity_unverified_since`;
3. for rows written before these columns existed, `integrity_checked_at`.

Repeated time-outs or errors therefore turn into STALE once the last success is older than the threshold, however often
checks are attempted. A first-ever failure turns STALE once the first failure is older than the threshold.

`IntegrityStaleAfter` = **1 h: an engineering default, not an approved service level** (4 × the 15-minute reconcile run).
The freshness target is owner decision D-06, alert routing D-10.

## 5. Health, metric, alert

| Surface | Behaviour |
|---|---|
| `Health.State` | STALE → **INTEGRITY_UNVERIFIED** (after INTEGRITY_FAILURE, NOT_STARTED, BLOCKED, FAILING, STALLED and the deferral age; before PROGRESSING_SLOWLY). Metric status DEGRADED |
| `Health` fields (JSON) | `integrity_state`, `integrity_checked_at`, `integrity_verified_at`, `integrity_seconds_since_verified`, `integrity_error`, `integrity_failures`, `integrity_last_failure_at`, `integrity_last_failure` |
| New operations metric | `payouts.reconciliation.integrity_age_seconds`, `fees.…`, `ledger.….<detector>`. Value = seconds since the last **successful** verification (null if none, shown as "Not measured"); state = integrity state; status: INCONSISTENT → UNAVAILABLE, STALE/LATEST_FAILED → DEGRADED, otherwise UNKNOWN. **Never HEALTHY** (no approved freshness target). A failed source makes it UNAVAILABLE with no value |
| Portal (`MetricsGrid`) | shows "State: <state>" beside the status badge, for every metric that has a state |
| Alert scan | `*.reconciliation.integrity_age_seconds*` with state STALE → **RECONCILIATION_SWEEP_INTEGRITY_UNVERIFIED** (HIGH, in-app). It comes from the integrity metric, so a sweep whose health shows BLOCKED or FAILING still raises it. LATEST_FAILED alone does not alert (one failed check is visible as DEGRADED; persistence alerts). INCONSISTENT keeps RECONCILIATION_SWEEP_INTEGRITY (CRITICAL) |
| Recovery | a later VERIFIED makes the state VERIFIED; the scan's existing CONDITION_CLEARED records the alert's clearance; people still acknowledge and resolve it. The last failure stays on the row and in the alert's history |

## 6. What a failure can and cannot do

The verifier reads only: the checkpoint rows, the evidence and the module's `Source`. It writes only the integrity
columns. A failed verification:

- **cannot advance a checkpoint**: an attempt-only update, the guard, and the version unchanged;
- **cannot alter a payout, ledger entry or attempt, or trigger a payment instruction**: no such call exists in `reconsweep`.
  It is tested by fingerprinting the test's payouts, their attempts and their ledger transactions before and after five
  verifications.

## 7. Cases and where they are tested

| Brief case | Unit (`internal/platform/reconsweep`) | Integration (real DB) |
|---|---|---|
| success then failure | `TestVerifyRecordsSuccessThenFailure`, `TestIntegrityState` | `TestReconciliationIntegrityStatusFailureAndRecovery` |
| success then timeout | `TestVerifyRecordsTimeoutsAndInterruptions`, `TestIntegrityState` | idem (interrupted) |
| first-attempt failure | `TestVerifyRecordsAFirstAttemptFailure`, `TestIntegrityState` | `…CarriesAndOrders` (failure on cycle 1) |
| interrupted / cancelled | `TestVerifyRecordsTimeoutsAndInterruptions` (cancel and deadline) | `…FailureAndRecovery` |
| concurrent checks | `TestVerifyConcurrent` | `…CarriesAndOrders` (8 concurrent, ordering by start) |
| UI/API representation | `TestIntegrityState` (metric), web `operations-components.test.tsx` | — |
| alert and recovery | `TestSecurityFindingsReconciliationIntegrityUnverified` | — |
| normal check | `TestIntegrityState` "verified" | `…FailureAndRecovery` |
| exceeding expected duration | `TestIntegrityState` "verified long ago" | — |
| timed-out check / repeated timeouts | `TestVerifyRecordsTimeoutsAndInterruptions` (3× TIME_BUDGET), `TestIntegrityState` "repeated time-outs" | — |
| recovery after a later success | `TestVerifyRecordsSuccessThenFailure`, `TestIntegrityState` "recovered" | `…FailureAndRecovery` (last failure kept) |
| missing or invalid timestamps | `TestIntegrityState` (never verified, future timestamps, skew tolerance, pre-Phase-6 rows) | — |
| unavailable dependency | `TestVerifyRecordsSuccessThenFailure` (cycles, evidence, source) | `…FailureAndRecovery` (failing source) |
| blocked by deferrals | `TestIntegrityState` "blocked by deferrals" (health BLOCKED, integrity STALE), security test (both alerts) | — |
| problems found before a failure | `TestVerifyKeepsProblemsFoundBeforeAFailure` | — |
| DB refuses error texts and incoherent history | — | `…CarriesAndOrders` |

## 8. Behaviour changes to review

1. A sweep that has completed a cycle but was **never** verified is now INTEGRITY_UNVERIFIED (it was PROGRESSING).
   Three fixtures of `TestHealthClassification` that had no verification now record a recent one, so they keep testing
   what they tested. Two new cases assert the new state. No assertion was removed or weakened.
2. Immediately after the Phase 4 and Phase 6 migrations are deployed, detectors with completed cycles show
   INTEGRITY_UNVERIFIED until the first verification (≤ 15 min). This is accurate: nothing has been verified yet.
3. `integrity_checked_at` now means the start of the check (it was the time of recording).
