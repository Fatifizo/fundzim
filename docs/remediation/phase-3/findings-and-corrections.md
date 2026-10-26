# Remediation phase 3 — root causes and corrections (F-R2-01, F-R2-02, F-R2-03)

Code commit: `01ea615`. Design record: ADR-052 Amendment 1 (ADR still **Proposed**).

## F-R2-01 — fee reconciliation reported only the first missing fee type

### How the detector works (investigated before the change)

- **Groups.** `sourceGroup` returns every assessment of one (source type, source id, calculation id), in id order. `group[0]`
  carries the group's completeness judgement in both the recency pass and the sweep.
- **Expected types.** The assessable components of the calculation (`app.fee_components`; basis ≠ NOT_CONFIGURED). They are
  the calculation's own output (Stage 12 policy versions). No accounting rule is invented here, and the expected set is not
  changed.
- **Actual assessments.** `app.fee_assessments` of the group; unique per (source, category) in the database.
- **Missing detection.** A component category with no assessment and no OPEN MISSING_FEE finding on any group member
  (`have`).
- **Deduplication.** `recordFinding` inserts with `ON CONFLICT (dedupe_key) WHERE status = 'OPEN' DO NOTHING`, using the key
  `kind:assessment:journal`.
- **Resolution.** A person resolves a reference (`ResolveRecon`; fee, assessment and ledger untouched). If the condition
  persists, the next run records it again.

### Root cause

The dedupe key had no **check discriminator**. Every MISSING_FEE finding of a group has the same kind, the same assessment
(`group[0]`) and no journal, so every missing category produced the same key `MISSING_FEE:<g0>:`. The first insert won and every
other category hit `ON CONFLICT … DO NOTHING`. On later runs the first category was filtered by `have`, but the others still
collided with its open key. They stayed invisible until that first finding was resolved.

The same defect class existed for **AMOUNT_MISMATCH**, which is wider than the Phase 2 description:
- the calculation-component comparison and the provider-report comparison share the key `AMOUNT_MISMATCH:<a>:`;
- the journal fee-lines comparison and the posted-versus-assessed comparison share `AMOUNT_MISMATCH:<a>:<journal>`.

An open finding of one comparison hid the other. Every other kind has one comparison per (assessment, journal), so it had no
collision.

### Correction

- `finding.check` names the comparison:
  - `category:<CATEGORY>` for MISSING_FEE;
  - `calculation_component`, `provider_report`, `journal_fee_lines` or `posted_vs_assessed` for AMOUNT_MISMATCH.
- `dedupeKey` = legacy key + `:` + check. The keys of all other kinds are **unchanged**, so their open references keep
  deduplicating.
- **References from before the fix.** An OPEN reference stored under the legacy key is taken to represent a finding only if
  `legacyCheck` derives the *same* check from its stored content (MISSING_FEE: `expected.category`; AMOUNT_MISMATCH: its
  detail text). Content that matches no comparison represents nothing, so a distinct discrepancy is never hidden. At worst one
  finding is recorded once more under its new key, a bounded duplicate rather than a suppression.
- **No data migration.** No existing reference is rewritten. The shared development database's references are untouched.
- `missingFindings` is now pure. It reports each category once, even when a calculation lists it twice.
- The detector still creates no assessment, journal or adjustment to compensate, and the ledger is untouched.

Files: `internal/fees/reconcile.go`; tests `internal/fees/reconcile_test.go` and
`tests/integration/reconciliation_corrections_test.go` (`TestReconciliationCorrectionsFeeEveryMissingType`).

## F-R2-02 — a failed provider-status lookup counted as a completed reconciliation check

### Trace (before the change)

The provider lookup happened in `reconcileOne`, for a payout with `SubmittedAt` when `ExecEnv = LOCAL_SIMULATOR`:
- `adapterFor` error → silently skipped;
- `GetPayout` class ACCEPTED → compared;
- class NOT_FOUND → compared (a mismatch only for COMPLETED or REVERSED);
- any other class (INDETERMINATE for timeouts, connection loss, 5xx and malformed bodies; RATE_LIMITED; AUTH_CONFIG_ERROR;
  NOT_SUPPORTED; INVALID_REQUEST; DEFINITE_FAILURE; RETRYABLE_BEFORE_SEND) → silently skipped.

`reconcileFresh` returned the other mismatches. The sweep's examine returned `Outcome{Exceptions: n}`, so the payout counted as
**examined**. The batch advanced the checkpoint, the cycle completed, and the coverage watermark claimed the payout was covered
although its provider view was never compared. Nothing changed the payout itself: reconciliation never writes payout state
except the ledger catch-up `syncLedger`, which is unchanged.

### Root cause

There was no representation of "a required comparison could not be made". Every non-error result of examine meant "judged".

### Correction (the smallest correct extension)

- **Classification.** `judgeProvider` (pure) compares only authoritative answers: ACCEPTED with a status, and NOT_FOUND. Every
  other class, an ACCEPTED answer without any status (`INCOMPLETE_RESPONSE`) and an adapter error (`ADAPTER_UNAVAILABLE`) return
  `verified = false` with the cause, and **no mismatch is derived from them**.
- **What happens to an unverified payout.** The internal comparisons (reservation, journals, attempts) are authoritative and
  are still recorded. They are idempotent under an open exception. Then:
  - the sweep returns `Outcome{Deferred, Reason: PROVIDER_UNVERIFIED}`; the checkpoint carries the payout with its
    first-deferral time and retries it in later runs;
  - the run report adds `provider_unverified`, a count by cause and a sample of up to 20 payout ids, all written into the
    `payout.reconciliation.run` audit event;
  - the recency pass reports unverified payouts the same way.
- **What it never does.** No payout status, reservation or attempt changes. FAILED is never assumed. No CREATE or CANCEL call
  exists on this path (a status query only), so no instruction can be sent. No financial hold or exception is bypassed.
- **Liveness (engine, `internal/platform/reconsweep`).** Deferring alone would have made an outage stall the sweep:
  - deferral retries were not limited by the record or time budget;
  - the time budget was checked only between batches, and one 200-payout batch of 10-second lookup timeouts takes about
    33 minutes, longer than the 10-minute job timeout, so nothing would advance.

  Now:
  - retries are limited to half the record budget and the time budget, round-robin, so no deferral starves;
  - the time budget is checked before each record (after at least one), and the checkpoint advances to the last *examined*
    key only.

  Records past a slow or failing one therefore keep progressing, and the checkpoint still never passes an unexamined record.
  The deferral limit (10 000) still fails closed and is now visible as BLOCKED_ON_DEFERRALS (F-R2-03).
- **Unchanged.** The provider polling budget (72 h / 500), the provider-specific behaviour, the grace period and every
  database guard.
- **Out of scope (documented).** In `PAYOUTS_EXECUTION_ENVIRONMENT = DISABLED` no provider is reachable and the provider
  comparison is not required, as before. That environment has no submitted payouts to query.

Files: `internal/payouts/reconcile.go`, `internal/platform/reconsweep/sweep.go`; tests `internal/payouts/reconcile_test.go`,
`internal/platform/reconsweep/sweep_test.go` (`TestSweepFailingChecksNeverStopTheWalk`, `TestSweepSlowChecksRespectTheTimeBudget`,
`TestSweepTimeBudget`), `tests/integration/reconciliation_corrections_test.go` (`…PayoutLookupFailure`,
`…UnknownPayoutLookupFailure`).

## F-R2-03 — a stalled sweep raised no signal

### What existed

- **Recorded:** cycle start, the last advance (`last_advanced_at`), the last run id, deferrals with their times, and
  per-cycle counters.
- **Not recorded:** a run that failed or stopped without advancing. The only trace was the run's audit event (payouts,
  ledger) or run row (fees). The coverage metric reported the age of the last completed cycle, UNKNOWN or DEGRADED, but no
  rule turned it into an alert.
- In the ledger, an error in the PAYMENT_JOURNALS sweep also prevented the PAYMENT_FACTS sweep from running.

### Correction

- **Every run's outcome is recorded.** Migration `20261026120000` (additive) adds `last_attempt_at`, `last_attempt_outcome`,
  `last_attempt_error` (≤ 300 characters) and `failed_attempts` (consecutive) to the three sweep tables. `RecordAttempt` writes
  them after every run, also on error, with a non-cancelled context. The guard accepts an update that changes only these
  columns without a version step, so it never disturbs a concurrent run's compare-and-swap. Every other guard rule is
  unchanged.
- **`Coverage.Health`** classifies the current cycle:

  | State | Meaning |
  |---|---|
  | NOT_STARTED | no cycle has run: coverage is not established |
  | PROGRESSING | advancing |
  | PROGRESSING_SLOWLY | advancing, but the cycle is older than 24 h |
  | STALLED | no advance for more than 1 h |
  | BLOCKED_ON_DEFERRALS | the deferral limit stopped the walk, or a deferral is older than 24 h |
  | FAILING | 3 or more consecutive failed runs |

  It also reports the last cycle's outcome: NONE, COMPLETED_CLEAN, or COMPLETED_WITH_DISCREPANCIES when the module has open
  findings.

  The thresholds are **engineering defaults**, labelled as such in code, ADR and metric source. The production thresholds are
  owner decisions D-06 (coverage freshness) and D-10 (alerting).
- **Signals.**
  - Each module publishes `payouts.reconciliation.sweep_health`, `fees.reconciliation.sweep_health` and
    `ledger.reconciliation.sweep_health.{payment_journals,payment_facts}`. The value is seconds since progress, `state` is the
    health state, and the status is UNAVAILABLE for FAILING, DEGRADED for stalled, blocked or slow, otherwise UNKNOWN. It is
    never HEALTHY, because no target is approved.
  - The existing Stage 18 operations alert scan raises **RECONCILIATION_SWEEP_FAILING / _STALLED / _BLOCKED** (HIGH) as
    persisted in-app alerts (`app.ops_security_alerts`). They are deduplicated per detector, cleared when the condition
    clears, and acknowledged and resolved by people.
  - **No alert is delivered outside FundZim**: no notification destination exists, and none is invented (B-12, D-10).
- **Ledger.** PAYMENT_FACTS now runs even when PAYMENT_JOURNALS failed in the same run, and both errors are returned.
- **Not changed.** A record whose examination *errors* every time still stops its sweep at that record, failing closed with
  no skip. That is now visible as FAILING with the error text, but it is not auto-deferred: automatic deferral of erroring
  records would need its own design (some errors are outages of the whole database).

Files: `migrations/20261026120000_reconciliation_sweep_attempts.sql`, `internal/platform/reconsweep/{health.go,sqlstore.go,sweep.go}`,
`internal/{payouts,fees,ledger}/ops.go`, `internal/ledger/reconcile.go`, `internal/operations/security.go`,
`internal/platform/opsview/opsview.go`, `api/openapi/fundzim-v1.yaml` (`OpsMetricS14.state`); tests `TestHealthClassification`,
`TestSweepRecordsEveryAttempt`, `TestSecurityFindingsReconciliationSweepHealth`, and the attempt assertions in the integration
tests.
