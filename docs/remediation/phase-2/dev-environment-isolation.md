# Remediation phase 2 — development environment safety and isolation (phase G)

## 1. Present state of the development project `fundzim` (read-only checks, 2026-10-26)

| Check | Result |
|---|---|
| Migration `20261025090000` present | yes. `public.goose_db_version`: 50 applied versions, newest `20261025090000`, applied 2026-10-25 16:20:58 UTC (F-R1-07). The repository holds 50 migration files; newest `20261025090000_reconciliation_coverage.sql` |
| Pending migrations | none. `fundzim-migrate` logged `no pending migrations` at the 2026-10-26 restart and again after the Phase 2 test run (test-evidence.md) |
| Readiness | `GET /api/v1/ready` → `{"status":"ready"}`; every container healthy |
| API and worker compatible with the schema | yes. They run image `sha256:adfb403d…` (Go sources and migrations identical from `a0a687d` to `c8fb8c6`, `git diff a0a687d c8fb8c6 -- apps/api internal migrations` empty), and that image's newest embedded migration equals the database's newest. The worker's sweeps write to the new tables without error (reconciliation-review.md §3) |
| Source revision of the running images | **known only indirectly.** The images were built on 2026-10-26 from a checkout at `07e5965`; the api layers were cache hits from the 2026-10-25 build at `a0a687d`, which has identical Go inputs. `/api/v1/version` reports `"commit":"unknown"` because compose passed no `GIT_COMMIT` |

## 2. Root cause of F-R1-07, and the fix

All compose projects built `fundzim/api:local` and `fundzim/web:local`. A disposable project (`-p fundzim-r1`) built from
another commit overwrote the tag, and the next `up` of the development project ran `fundzim-migrate` with it.

**Change (Phase 2, `efa7b62`).** FundZim's own images are tagged `fundzim/{api,web}:${COMPOSE_PROJECT_NAME}`. Compose sets
that variable from `-p`, from `COMPOSE_PROJECT_NAME`, or from the file's `name: fundzim`.

| Project | Images it builds and runs |
|---|---|
| development (default) | `fundzim/api:fundzim`, `fundzim/web:fundzim` |
| `docker compose -p fundzim-r2 …` | `fundzim/api:fundzim-r2`, `fundzim/web:fundzim-r2` |

A disposable build can no longer replace the development project's image. `internal/deploytest` fails if a FundZim service is
tagged any other way (mutation-tested). `scripts/dr/exercise.sh` now uses the active project's tag. Volumes were already per
project (`<project>_postgres-data` …), so they need no change.

**Proven in Phase 2.** The disposable project `fundzim-r2` was built from `efa7b62` and ran the full integration suite. The
development project's image IDs were recorded before and after: they were unchanged, and the development project restarted
with `--no-build` and `no pending migrations` (test-evidence.md §3).

**Residual (documented, not engineered away):**
- Host ports are shared. Two projects cannot run at once with default ports, and some integration tests fall back to
  default local endpoints. Disposable runs therefore still stop the development project first (`down` **without** `-v`), as
  the Stage 20 rule requires.
- An `up --build` of the development project still builds from whatever is checked out. Build from the intended commit and set
  `GIT_COMMIT=$(git rev-parse HEAD)` so `/api/v1/version` names it.
- Images built before this change keep the `:local` tag. On 2026-10-26 the existing development images were retagged
  `:fundzim` (same IDs), so the next `up` reuses them instead of rebuilding.

## 3. Rules (also in CLAUDE.md)

- Never run destructive or load tests on the development database. Use a disposable project, or a scratch `fundzim_perf*`
  database for ledger volume tiers.
- Reversing `20261025090000` on the development database needs an approved recovery plan. The owner decided to keep it.
- Before starting any project: `GIT_COMMIT=$(git rev-parse HEAD) docker compose -p <project> build` from the intended checkout.
