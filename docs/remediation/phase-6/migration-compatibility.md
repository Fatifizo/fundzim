# Migration and code compatibility (F-R5-03)

Status: analysis and test on branch `remediation/integrity-status`. **NOT REVIEWED.** No migration was applied to the shared
development database; it remains at `20261025090000`. Nothing here authorises a deployment.

## 1. Versions involved

| Label | Code commit (branch) | Schema it needs (highest migration) |
|---|---|---|
| C1 | `07e5965` (phase 1; **the development stack runs this**) | S1 = `20261025090000` |
| C3 | `1e167b1` (phase 3) | S3 = `20261026120000` (run-outcome columns) |
| C4 | `f7d1cf7` = `dfa91b6` code (phase 4) | S4 = `20261027090000` (evidence tables, constraint triggers, integrity columns) |
| C6 | `019b245` (phase 6) | S6 = `20261028090000` (integrity-status columns, ERROR) |

Goose stores no checksum. **An applied migration is never edited** (CLAUDE.md). Every change is a new migration, so
`20261026120000` and `20261027090000` are unchanged in this phase.

## 2. Matrix: code × schema

Rows marked *tested* are executed by `TestReconciliationMigrationCompatibility`: a scratch database migrated step by step,
running the statements of each code level copied verbatim from its commit. The other rows are derived by reading the code.

| Code \ schema | S1 | S3 | S4 | S6 |
|---|---|---|---|---|
| **C1/C3** (advance without evidence) | works | works (*tested* for C3) | **every advance refused at commit** (23514, *tested*). Sweeps stop advancing and record failed runs; health becomes FAILING/STALLED and raises the in-app alerts. Nothing is corrupted or skipped | same as S4 (*tested*) |
| **C4** | fails (no evidence tables) | fails (no evidence tables) | works (*tested*) | **works** (*tested*: Coverage read and RecordIntegrity). A VERIFIED written by C4 has no `integrity_verified_at`; C6 reads it as verified at its `checked_at` (*tested*) |
| **C6** | fails: evidence and columns missing | fails (`42703`, *tested*) | partly fails. Advances work, but cycle completion, `RecordIntegrity` and `Coverage` fail (`42703`, *tested* for Coverage). Coverage and integrity metrics show UNAVAILABLE. Readiness reports not ready (the `migrations` critical check: schema behind the binary) | works (*tested*) |

**Old code on the new schema.**
- C4 on S6 is safe: the Phase 6 migration only adds nullable or defaulted columns, widens a CHECK and extends the guard's
  attempt-only set.
- C1/C3 on S4 or later fails closed: sweeps stop and alert. This is F-R5-03, inherited from Phase 4. Phase 6 does not
  change it.

**New code on the old schema** fails closed: errors, UNAVAILABLE metrics, not ready. It never records a false VERIFIED and
never advances without evidence.

## 3. Safest rollout order (proposal; requires authorisation, D-01/D-01a, integration plan)

1. Preconditions, all outside Claude Code's authority:
   - independent review of Phases 3–6;
   - protection applied on `main`;
   - a verified backup of the target database (`scripts/backup/`, restore drill);
   - owner authorisation.
2. Build C6 images from the reviewed commit (`GIT_COMMIT=$(git rev-parse HEAD)`).
3. Stop the worker and API of the target project. This prevents C1 from running against S4, the F-R5-03 window.
4. Run `fundzimctl migrate up`. The compose `fundzim-migrate` job does this before the API and worker start, from the same
   image. It applies `20261026120000`, `20261027090000` and `20261028090000` in one run, each in its own transaction.
5. Start the API and worker from the C6 images. Readiness must report the `migrations` check as passing.
6. Watch the first reconcile runs (15 min). Expect INTEGRITY_UNVERIFIED until the first verification of each detector
   (design §8.2), then VERIFIED. No sweep may show FAILING.

**Do not** run C1 or C3 against S4/S6 (they stop advancing). **Do not** run C6 before the migration.

On compose, `depends_on: fundzim-migrate: service_completed_successfully` already enforces the order for a single
`docker compose up`. The risk is a manual or partial rollout that restarts only some services.

## 4. Rollback

- **Production is forward-only** (ADR-028; `fundzimctl migrate down` is refused outside development and test). The
  rollback for a defect in C6 is a fix forward. Running C4 on S6 is a valid fallback (§2), so C4 images can be redeployed
  **without** a schema rollback.
- **Development or test only** — `20261028090000` Down (*tested*):
  - restores the Phase 4 guard first;
  - rewrites a running cycle's ERROR to UNVERIFIABLE, so C4 can keep recording;
  - drops the new columns;
  - restores the Phase 4 CHECK as **NOT VALID**. A *completed* cycle that ended with ERROR is immutable and keeps it
    (history is not rewritten), while new writes are checked.
- The test also proves that, after the Down:
  - C4 works;
  - writing ERROR is refused;
  - C6 cannot read;
  - a second Up succeeds.

## 5. The shared development database

- It is at `20261025090000` and runs C1 images.
- Applying `20261026120000` to it was never authorised.
- Applying `20261027090000` and `20261028090000` to it is **not authorised**, and not done in this phase.

Integration testing uses disposable projects whose `fundzim-migrate` applies everything to their own fresh volumes.

## 6. Residual risks

- A mixed C4/C6 fleet (rolling restart) is transient but possible. C4 stamps `checked_at = now()` (time of recording),
  which can be later than a concurrent C6 check's start, so it can win the ordering. Its VERIFIED does not set
  `integrity_verified_at`, and C6 then reads the age from the older success. The effect is at worst STALE (fail-safe),
  never a false VERIFIED. Avoid it with step 3.
- The scratch-database test runs only on demand (`FUNDZIM_IT_MIGRATION_COMPAT=1`); CI does not run it.
