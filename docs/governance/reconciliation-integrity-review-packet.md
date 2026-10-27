# Reconciliation and checkpoint-integrity review packet (Phases 1, 3, 4, 6)

**Review status: NOT REVIEWED.** No independent reviewer exists (D-01a). The "author pre-review" notes in §6 were written
by Claude Code, the author. They are inputs for the reviewer, **not** a review.

**Code under review:** commits `6a65d70..f7d1cf7` (Phases 1–4, branch `remediation/checkpoint-integrity`) and
`dfa91b6..b9496c3` (Phase 6, branch `remediation/integrity-status`; §D6). The reconciliation-relevant code is
`97b8a70..f7d1cf7` plus `019b245`, `b9496c3`. Design records: ADR-052 (Proposed) with Amendments 1–3,
`docs/remediation/phase-{1,3,4,6}/`. Overall scope and claims: `review-scope-phases-1-6.md`.

## D1. Checkpoint advancement

| Look at | Where | Question for the reviewer |
|---|---|---|
| Write paths | `internal/platform/reconsweep/sqlstore.go`: `Active` (l. 69), `Advance` (l. 89), `RecordAttempt` (l. 137), `RecordIntegrity` (l. 278) | Is there any other statement anywhere that writes `*_reconciliation_sweeps` or `*_sweep_batches`? (`grep -rn reconciliation_sweep internal`; archtest table ownership) |
| Guard (monotonic) | `migrations/20261027090000…sql` l. 97 (current `app.reconciliation_sweep_guard`); history in `20261025090000` l. 19 and `20261026120000` l. 41 | Does the attempt-only branch (`to_jsonb(NEW) - attempt_cols = to_jsonb(OLD) - attempt_cols`) let anything except the attempt and integrity columns change? |
| Evidence requirement | `20261027090000` l. 128 `reconciliation_sweep_evidence_check()` and l. 171–175 (DEFERRABLE INITIALLY DEFERRED constraint triggers) | Can a session avoid the check? (`SET CONSTRAINTS … IMMEDIATE` still runs it; disabling triggers needs table ownership (`fundzim_migrator`); `session_replication_role = replica` needs superuser.) Is `e IS NULL` the right "no row" test? |
| Evidence fields and identity | `*_sweep_batches`: `sweep_id`, `seq` (`UNIQUE (sweep_id, seq)`), `from_key`, `to_key`, `keys uuid[]`, `deferred_added`, `deferrals_resolved`, `completing`, `run_id`, `examined_at`; l. 26–73 | Do these suffice to reconstruct the claimed walk? Are array limits (100 000) safe? |
| Deferred records | evidence check: set equation `(OLD.deferred − resolved) ∪ added`; `reconsweep/sweep.go` retry rotation | Can a deferral be dropped without being resolved by an examination? |
| New cycle start | l. 156 `reconciliation_sweep_cycle_start_check()` | Does `Active`'s `max(cycle)+1` insert ever need to carry deferrals? |
| Transaction boundaries | `Advance`: UPDATE (CAS on `version`) → evidence INSERT → optional next-cycle INSERT, one transaction | Is the evidence written only by the winner of the CAS? |
| Concurrency, interruption, replay | tests: `TestSweepConcurrentRunsNeverSkipOrDuplicate`, `TestVerifyEngineEvidenceIsConsistent`, `TestReconciliationIntegrityKilledBeforeCheckpointAdvance` | Is a killed advance fully rolled back (no orphan evidence row)? |
| Immutability of evidence | `forbid_mutation` triggers l. 47–73; runtime grants SELECT and INSERT only (`apply_runtime_grants`) | Confirm with `\dp app.*_sweep_batches` as the migrator |
| Role privileges | payouts and fees: `fundzim_app` SELECT, INSERT, UPDATE on `app.*_sweeps` (DELETE refused by trigger); ledger: `fundzim_ledger` on `ledger.*_sweeps` | Can one role hold fewer privileges? (see D3) |

## D2. Integrity verification

| Look at | Where | Question |
|---|---|---|
| Independent source | `payouts.Service.sweepScope`, `fees.Service.sweepScope`, the ledger journal source in `ledger/reconcile.go`, `payments.PaymentFactStampsBetween` | Set queries without ORDER BY or LIMIT; modification time maintained by the database (triggers `set_updated_at`; journals immutable). Does every scope change bump the time? |
| Rules | `reconsweep/integrity.go` `verifyCycle` | Are the problem codes RECORD_SKIPPED, COMPLETED_EARLY, CHAIN_BROKEN, CURSOR_BEYOND_EVIDENCE, EXAMINED_MISMATCH, KEY_OUT_OF_RANGE, DUPLICATE_KEY, DEFERRALS_MISMATCH, BATCH_MISSING and COMPLETION_NOT_EVIDENCED correct and complete? Is the 5-minute margin acceptable? |
| Never advances | `Verify` writes only `RecordIntegrity` (attempt-only UPDATE) | confirm |
| Visibility | `Coverage.Health` → INTEGRITY_FAILURE; `*.reconciliation.sweep_health*` metric; `operations/security.go` RECONCILIATION_SWEEP_INTEGRITY (CRITICAL) | in-app only, no human delivery (D-10) |
| Errors, partial results, timeouts | `Verify` returns early on a source error and does **not** record a status; INCOMPLETE on budget | see author notes A-1 and A-2 |
| Bound and cost | `docs/remediation/phase-4/performance.md` | 0.3 s at 100 000 journals; quadratic in batches per cycle |

## D3. Threat model and residual limitation

**Known limitation.** A role that can INSERT evidence and UPDATE checkpoints (today the same runtime role) can write evidence
listing **real** in-scope keys that were never examined, together with a matching checkpoint move. The database accepts it
(the chain is consistent) and the verifier cannot distinguish it from a real examination. Detection shares the trust boundary
with processing. This limitation is **not eliminated** by any control below.

| Mitigation | State | Effect |
|---|---|---|
| Least-privilege roles | partly: runtime roles cannot DELETE, alter or disable triggers; ledger checkpoints are only `fundzim_ledger` | narrows who can forge; does not separate processing from evidence |
| Separate evidence-writer role (e.g. a SECURITY DEFINER function that is the only path for evidence and checkpoint writes, callable by the sweep role, and a verifier role with SELECT only) | **not implemented**; feasible as a new migration | stops ad-hoc SQL writes; a compromised application process could still call the function with false keys |
| Append-only evidence | implemented (triggers, grants) | evidence cannot be rewritten after the fact |
| Independent verification | implemented | catches defects and inconsistent forgeries, not consistent ones |
| Audit monitoring | run reports in audit events (payouts, ledger); no audit of direct SQL by runtime roles | partial |
| Restricted migration and admin privileges | the migrator and superuser are separate credentials in `.env`; production custody not designed (B-11) | depends on infrastructure |
| Deployment and code-version evidence | `/api/v1/version` commit when built with `GIT_COMMIT`; rc-manifest | ties a run to code only for evidenced builds |
| External review of trigger SQL and grants | **not done** | this packet |

## D4. Migration review

| | `20261026120000_reconciliation_sweep_attempts.sql` | `20261027090000_reconciliation_sweep_evidence.sql` |
|---|---|---|
| Commit | `01ea615` (only commit; never edited) | `30eb045` (only commit; never edited) |
| Changes | adds `last_attempt_at`, `last_attempt_outcome`, `last_attempt_error`, `failed_attempts` (default 0) and a CHECK to the 3 checkpoint tables; replaces the guard function (adds the attempt-only branch); `apply_runtime_grants()` | 3 evidence tables (append-only triggers, unique index); `integrity_checked_at`, `integrity_status`, `integrity_detail` and a CHECK on the 3 checkpoint tables; guard replaced (integrity columns added to the attempt-only set); 2 new trigger functions; 3 deferred constraint triggers; 3 AFTER INSERT triggers; `apply_runtime_grants()` |
| Additive / data-preserving | yes / yes (no existing row changed) | yes / yes |
| Locks | `lock_timeout 5s`, `statement_timeout 60s`; ADD COLUMN with constant default (metadata only) | same; CREATE TABLE and TRIGGER take short exclusive locks on the 3 checkpoint tables |
| Down | restores the previous guard, drops the 4 columns: loses attempt history (operational, not financial) | **drops the 3 evidence tables**: destroys integrity evidence. Development only (`fundzimctl` refuses Down unless `APP_ENV` is development or test, `main.go:119`). **Never run in a shared or production database** |
| Tested | disposable `fundzim-r3/r5` (up, full suite, Down→0→Up) | disposable `fundzim-r6/r7/r8` and scratch `fundzim_perf_r6` (up, full suite, volume, Down→0→Up) |
| Applied to the shared development database | **no** (verified 2026-10-27) | **no** (verified 2026-10-27) |

`20261028090000` (Phase 6) is described in §D6. All three pending migrations go to a shared database together, with their
code.

**Prerequisites before applying to the shared development database** (owner authorisation required):
1. **Deploy code and migrations together.**
   - `20261027090000` refuses every checkpoint advance that lacks evidence. The development stack's current images
     (pre-Phase-3) write no evidence, so every sweep advance would fail at commit and every sweep would go FAILING.
   - Conversely, Phase 3/4 code against the old schema fails on the new columns.
   - So: rebuild the development images from the integrated commit (`GIT_COMMIT=$(git rev-parse HEAD) docker compose build`),
     then `up`. The migrate step runs before the API and worker.
2. Take a verified backup first (Stage 18 backup and restore drill).
3. Expect the running cycles to report **UNVERIFIABLE** until they complete (no evidence for their earlier batches).
4. Plan the evidence retention decision (LR-012) before the tables grow.
5. Never run Down there.

## D6. Phase 6 — integrity status (F-R5-01, F-R5-02) and compatibility (F-R5-03)

Design: `docs/remediation/phase-6/integrity-status-design.md`. Compatibility: `…/migration-compatibility.md`. Evidence:
`…/test-evidence.md`.

| Look at | Where | Question for the reviewer |
|---|---|---|
| Every verification outcome is recorded (ERROR, INCOMPLETE/INTERRUPTED, INCOMPLETE/TIME_BUDGET; fixed reason codes) | `reconsweep/integrity.go` `Verify` (`fail`) | Is there any return path that still skips `RecordIntegrity`? Is `context.WithoutCancel` right for the record? |
| Ordering by the check's start; last success apart from the latest outcome; latest failure kept after recovery | `reconsweep/sqlstore.go` `RecordIntegrity`, `Advance` (carry-over INSERT … SELECT) | Can a concurrent older check overwrite a newer outcome? Is the carry-over complete and safe at the cycle-start trigger? |
| Integrity state and thresholds | `reconsweep/health.go` `integrityState`, `IntegrityMetric` | Are NOT_RUN / STALE boundaries safe (never a false VERIFIED)? 1 h and 5 min are engineering defaults (D-06/D-10) |
| Alert | `operations/security.go` (`RECONCILIATION_SWEEP_INTEGRITY_UNVERIFIED`, HIGH) | Raised from the integrity metric even when sweep health shows another state |
| Migration `20261028090000` | `migrations/20261028090000_reconciliation_integrity_status.sql` | Additive? Guard attempt-only set extended correctly? Error-code and failure-history CHECKs sufficient? Down (development only): NOT VALID CHECK for immutable completed rows, `SET CONSTRAINTS ALL IMMEDIATE` |
| Not applied to the shared development database | read-only check 2026-10-27: at `20261025090000` | — |

What Phase 6 does **not** change:
- The sweep's own run outcome (`RecordAttempt`) still reflects the sweep. A verification failure is recorded in the
  integrity columns and joined into the run's error, not in `failed_attempts`. A-1's "attempt recorded as successful" part
  is addressed by the separate integrity status, not by changing the attempt.
- **A-4 and the D3 limitation stand.** The integrity columns are written by the same database role as the sweep, so a role
  holder could write VERIFIED by hand. Detecting that needs a separate verifier role or an append-only verification log.
  Neither exists.

## D5. Reviewer checklist and outcome

| Item | Status |
|---|---|
| D1 checkpoint advancement (trigger SQL, write paths, privileges) | NOT REVIEWED |
| D2 verifier rules, sources and visibility | NOT REVIEWED |
| D3 residual limitation and mitigations accepted or actioned | NOT REVIEWED |
| D4 migrations `20261026120000`, `20261027090000` | NOT REVIEWED |
| D6 Phase 6: integrity status and migration `20261028090000` | NOT REVIEWED |
| Author pre-review notes A-1 … A-4 dispositioned | NOT REVIEWED |
| **Overall** | **NOT REVIEWED (BLOCKED: no independent reviewer, D-01a)** |

The reviewer uses the record template in `independent-review-process.md` §5.

## 6. Author pre-review notes (Claude Code, the author: **not independent**)

| Id | Severity (author's view) | Observation | Suggested disposition |
|---|---|---|---|
| A-1 (F-R5-01) — **fixed on the branch in Phase 6, not reviewed** (§D6) | LOW–MEDIUM | When `Verify` fails (a source query error), the module returns the error but the run's attempt was already recorded as successful, and the checkpoint keeps its **previous** integrity status. Health can therefore show a stale VERIFIED; the failure is visible only as a job error | Record a verification failure on the checkpoint (e.g. status INCOMPLETE with the error in detail), and make Health treat an `integrity_checked_at` older than N runs as degraded |
| A-2 (F-R5-02) — **fixed on the branch in Phase 6, not reviewed** (§D6) | LOW | Health ignores INCOMPLETE and UNVERIFIABLE: a verifier that never finishes at volume raises no signal | Map persistent INCOMPLETE to DEGRADED (threshold: D-06/D-10) |
| A-3 (F-R5-03) — **analysed and tested in Phase 6; procedural control open** | MEDIUM (deployment) | Migration `20261027090000` with pre-Phase-3 code blocks every sweep advance | D4 prerequisite 1; record in the integration plan |
| A-4 — **unchanged by Phase 6; open** | INFO | Attempt-only and integrity-only updates can be written by the same role, which could reset `failed_attempts` or write VERIFIED | Covered by D3; a separate verifier role would close it |
