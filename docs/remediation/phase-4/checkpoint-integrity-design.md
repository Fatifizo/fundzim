# Remediation phase 4 — checkpoint integrity design (Workstream B)

Code commit: `30eb045`. Migration: `20261027090000_reconciliation_sweep_evidence.sql`. ADR-052 Amendment 2 (ADR still
**Proposed**).

## 1. Checkpoint write-path map (B1, before the change)

| Path | What it writes | Role | Transaction |
|---|---|---|---|
| `reconsweep.SQLStore.Active` | INSERT of cycle 1 when no RUNNING row exists (`ON CONFLICT DO NOTHING`; partial unique index: one RUNNING per detector and scope) | payouts and fees: `fundzim_app`; ledger: `fundzim_ledger` | own statement |
| `SQLStore.Advance` | compare-and-swap UPDATE of cursor, counters and deferrals (`WHERE version = $cur AND status = 'RUNNING'`); on completion the row becomes COMPLETED and the next cycle is INSERTed with the carried deferrals | same | one transaction |
| `SQLStore.RecordAttempt` (Phase 3) | attempt columns only, no version step | same | own statement |
| Migrations | DDL only; no backfill of checkpoint rows | `fundzim_migrator` | — |
| Other modules, admin, HTTP | none: archtest table ownership keeps other modules' SQL away; no route writes checkpoints | — | — |

### Meaning and ordering

- **Cursor.** The last key of the last fully examined batch, in uuid order. The next batch is `id > cursor ORDER BY <table>.id
  LIMIT n` (F-R1-06).
- **When a record counts as examined.** When `examine` returned without error. Its findings are written inside `examine`
  (module tables, idempotent unique keys) *before* the batch's `Advance`.
- **Crash between steps.** Nothing commits for the batch, so the next run re-examines it; findings are deduplicated.
- **Deferrals.** Deferred records (grace period, provider lookup not verified) count as examined but are carried in
  `deferred_keys` until a later examination resolves them.
- **Moving dataset.** A record committed behind the cursor during a cycle, or changed after its examination, is examined by
  the next cycle. UUIDv7 keys are time-ordered but are generated before commit, so a late commit can land behind the cursor.
  That is why every cycle restarts from the nil cursor.

### What the database enforced before Phase 4

No rewind, no rewrite of identity or counters downwards, version + 1 per position change, COMPLETED immutable, no delete,
attempt-only updates allowed.

### What it did not enforce

That a position change corresponds to anything examined. `fundzim_app` (payouts, fees) and `fundzim_ledger` (ledger) could
move the cursor forward, raise `examined`, drop deferrals or complete a cycle.

## 2. Threat assumptions

| Threat | In scope | Control |
|---|---|---|
| A defect in the engine or detector that skips records (wrong keyset bound, ordering bug like F-R1-06, early completion) | yes | detective: independent re-read (RECORD_SKIPPED, COMPLETED_EARLY) |
| A defect or stray statement that moves a checkpoint without examination (any code path or role, including the module's own) | yes | **preventive**: constraint trigger refuses it at commit |
| Evidence damage (missing, duplicated, out-of-order rows; deferrals edited) | yes | preventive (append-only, unique `(sweep_id, seq)`, deferral equation) and detective (chain, count and deferral checks) |
| A deliberate forger with the module's role who writes both a checkpoint move **and** evidence that lists real keys never examined | **no** | Not detectable: the evidence states what was examined, and nothing can prove that an examination happened. A forger listing keys that do not exist or that are out of range is detected (KEY_OUT_OF_RANGE, RECORD_SKIPPED). Mitigation is role separation and review (B-07/B-13), not this control |
| Triggers disabled by a superuser or the migrator | partially | the verifier still finds chain, count and skip breaks; disabling triggers is outside the runtime roles' privileges |

## 3. Preventive layer (database, migration 20261027090000)

- **Evidence tables.** `app.payout_reconciliation_sweep_batches`, `app.fee_reconciliation_sweep_batches` and
  `ledger.ledger_reconciliation_sweep_batches` hold one row per advance: `sweep_id`, `seq` (= batches after the advance),
  `from_key`, `to_key`, `keys uuid[]` (examined, deferred ones included), `deferred_added`, `deferrals_resolved`,
  `completing`, `run_id`, and `examined_at` (database time).
  - Append-only: a `forbid_mutation` trigger on UPDATE, DELETE and TRUNCATE.
  - `UNIQUE (sweep_id, seq)`.
  - Bounded array sizes.
  - Runtime grants: SELECT and INSERT only.
- **`trg_*_reconciliation_sweeps_evidence`** is a DEFERRABLE INITIALLY DEFERRED constraint trigger on UPDATE of the checkpoint
  tables. For every update that changes the position (a version step), at commit, the evidence row
  `(sweep_id = NEW.id, seq = NEW.batches)` must exist with:
  - `from_key = OLD.cursor_key`;
  - `to_key = NEW.cursor_key`;
  - `NEW.examined = OLD.examined + cardinality(keys)`;
  - `NEW.batches = OLD.batches + 1`;
  - `NEW.deferred_keys` equal, as a set, to `(OLD.deferred_keys − deferrals_resolved) ∪ deferred_added`.

  Otherwise the commit fails with `check_violation`. Attempt-only and integrity-only updates (no version step) are not
  position changes.
- **`trg_*_reconciliation_sweeps_cycle_start`** (AFTER INSERT): a new cycle starts with no cursor, 0 examined, 0 batches and
  version 0, and carries exactly the previous cycle's final deferrals (none for cycle 1).
- **`Advance` writes the evidence in the same transaction as the update** (`SQLStore.Advance`, after it won the
  compare-and-swap). A losing concurrent run writes nothing.

## 4. Detective layer (`reconsweep.Verify`)

**When.** After every sweep run, in the payouts, fees and ledger reconciliations (both ledger detectors; PAYMENT_FACTS only
when the facts provider implements `PaymentFactStamps`, which the composition root's provider does). It verifies the
RUNNING cycle and the last COMPLETED one.

**Independent source.** Each module's `Source` is a set query over the detector's scope with no ordering and no LIMIT. It
returns every in-scope key with its last-modification time. It is not the keyset read the walk uses.

| Detector | Scope | Last-modification time |
|---|---|---|
| PAYOUTS | `payout_requests` with `status <> 'PAYOUT_REQUESTED'` (a state never re-entered) | `updated_at`, set by a database trigger on every update |
| FEE_ASSESSMENTS | all `fee_assessments` | `updated_at`, trigger-maintained |
| PAYMENT_JOURNALS | `ledger_transactions` of the source and `DONATION_CAPTURED` | `posted_at` (immutable rows) |
| PAYMENT_FACTS | `payment_intents` in SUCCEEDED or UNKNOWN | `updated_at`, trigger-maintained |

Every scope change of a record also changes its modification time. That is required for the "unchanged since" rule below and
holds for all four.

**Checks.**

| Problem code | Condition |
|---|---|
| BATCH_MISSING | evidence rows are not numbered consecutively up to `batches` |
| CHAIN_BROKEN | a batch starts somewhere other than where the previous one ended |
| CURSOR_BEYOND_EVIDENCE | the cursor is not where the evidence ends |
| EXAMINED_MISMATCH | `examined` ≠ the number of evidenced keys |
| KEY_OUT_OF_RANGE | keys outside `(from, to]`, not ascending, or a batch not ending at its last key |
| DUPLICATE_KEY | a key evidenced twice in one cycle |
| DEFERRALS_MISMATCH | initial deferrals plus added minus resolved ≠ the carried set |
| RECORD_SKIPPED | an in-scope key in a batch range, last modified earlier than `examined_at − margin`, absent from that batch's keys |
| COMPLETED_EARLY | for a completed cycle, an in-scope key beyond its final cursor last modified earlier than `completed_at − margin` |
| COMPLETION_NOT_EVIDENCED | a COMPLETED cycle whose last evidence row is not marked `completing` |

**Moving dataset.** Records modified within the margin before, or at any time after, their batch's examination are counted
as *late changes*, not flagged. The next cycle examines them.

**Margin.** An engineering default of 5 minutes. It covers a transaction that inserted a record before a batch's keyset read
but committed after it. A skip of a record modified within 5 minutes before its batch is not flagged. If the skip is
systematic, older records reveal it.

**Statuses.**

| Status | Meaning |
|---|---|
| VERIFIED | every check passed on both cycles |
| INCONSISTENT | at least one problem |
| INCOMPLETE | the time budget, default 1 minute per detector per run, ended before every batch was re-read. **Never reported as verified** |
| UNVERIFIABLE | a cycle has batches from before the evidence existed |

**Visibility.**
- `integrity_status`, `integrity_checked_at` and `integrity_detail` (bounded JSON) on the RUNNING checkpoint, as an
  attempt-only update;
- in `Coverage`;
- `Health` becomes **INTEGRITY_FAILURE**, the highest precedence; the metric status is UNAVAILABLE;
- the operations scan raises **RECONCILIATION_SWEEP_INTEGRITY (CRITICAL)** as an in-app alert;
- run reports and audit metadata carry the report.

No alert leaves FundZim (no destination exists, D-10).

**Never.** It never moves a checkpoint, re-examines a record, creates a financial finding, writes a financial table or sends
a provider call. Repair is a human decision: investigate, then let the next cycle re-examine.

## 5. Recovery semantics (B3) — where each case is proven

| Case | Outcome | Evidence |
|---|---|---|
| Killed before a batch begins | nothing written; the next run starts at the cursor | `TestSweepCrashAfterCheckpoint` (engine), `TestVerifyEngineEvidenceIsConsistent` |
| Killed after records are read, before results are committed | the examine error aborts the batch: no advance, no evidence | `TestSweepFailureBeforeCheckpointNeverSkips`, `TestVerifyEngineEvidenceIsConsistent` |
| Killed after findings are written, before the advance | the advance dies with the connection; checkpoint and evidence unchanged; failed run recorded; the next run re-examines (findings deduplicated); integrity VERIFIED | **`TestReconciliationIntegrityKilledBeforeCheckpointAdvance`** (real DB, row-lock barrier and backend termination) |
| Killed after the advance | the next run resumes after the cursor | `TestSweepCrashAfterCheckpoint` |
| Provider lookup timeout, malformed or incomplete | deferred, not reconciled; deferral sets match the evidence; VERIFIED | Phase 3 `…PayoutLookupFailure`; `TestReconciliationIntegrityRealSweepsVerify` |
| Deferred payout revisited later | resolved through `deferrals_resolved`; the chain stays consistent | engine tests; Phase 3 integration |
| New in-scope record during a sweep | above the cursor: examined in the same cycle; behind it: a late change, examined next cycle | `TestVerifyLateChangesAreNotSkips`; `…RealSweepsVerify` |
| Batch with no findings, or several distinct findings for one record | evidence is independent of findings | all of the above; Phase 3 F-R2-01 tests |
| Moving a checkpoint beyond the examined scope | refused by the database; a self-consistent forgery is caught as RECORD_SKIPPED | `…DatabaseRefusesUnsupportedMoves`, `…DetectsAForgedConsistentAdvance`, `TestVerifyDetectsUnsupportedCheckpointMoves` |
| Missing, duplicated or inconsistent evidence | refused (append-only, unique); detected (chain, count, duplicate, deferral) | same |
| Concurrent workers | one compare-and-swap winner per version writes one evidence row; VERIFIED | `TestVerifyEngineEvidenceIsConsistent`, `…RealSweepsVerify` |

## 6. Retention and storage

The evidence keeps every examined key, 16 bytes per key plus row overhead, for every cycle. The volume is in
`performance.md` §2. **No retention period is set (LR-012)**: rows are append-only and nothing deletes them. A retention
decision is needed before production volumes (owner and counsel).

## 7. Limits (stated, not hidden)

- The deliberate-forger case (§2) is undetectable.
- Verification re-reads the running and last completed cycles on every run. Its cost grows with cycle size: a quadratic
  number of batch re-reads across a cycle. At large volumes it reports INCOMPLETE within its 1-minute budget. Measurements
  and the safe alternative (incremental verification with a persisted verified-through marker) are in `performance.md`.
- **Margin blind spot (§4).** A record modified within the margin before its batch is not flagged.
- Cycles running when the migration is applied are UNVERIFIABLE until they complete.
