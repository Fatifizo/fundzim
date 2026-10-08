# Database Migration Strategy

**Stage 2 — design only.** Decision record: [ADR-028](../adr/ADR-028-migration-strategy.md). This document is
the operating manual Stage 3 implements. The ordered list of migrations per roadmap stage is
[migration-plan.md](migration-plan.md). Standards: [DATABASE.md §13](../DATABASE.md),
[design-principles.md](design-principles.md).

The SQL in `design/sql/` is **input** to migrations, never a migration: goose does not read that directory,
and nothing there is run against a real database.

---

## 1. Tooling

- **goose**, SQL migrations embedded in the Go binary (`embed.FS`), run by `fundzimctl migrate up` in a
  dedicated pipeline step as **`fundzim_migrator`** (owner of every schema and object). Never at application
  start-up, never as `fundzim_app`.
- Directory: `/migrations`. One file per migration:

  ```
  migrations/20261110090000_create_platform_currencies.sql
  migrations/20261110090100_create_audit_events.sql
  ```

  Naming: `YYYYMMDDHHMMSS_<snake_case_description>.sql` (UTC timestamp of authoring). Ordering is by timestamp.
  **Out-of-order application is disallowed in production** (`goose` with `allowMissing = false`); a branch that
  lands with an older timestamp than an applied migration is re-stamped before merge (CI check: new files
  must sort after the latest file on `main`).
- **River's own migrations** (schema `queue`) are applied by the same `fundzimctl migrate` step using River's
  migrator at a **pinned River version**, recorded in a goose migration comment and in `go.mod`. Upgrading River
  is a reviewed change with its own migration step; River tables are never edited by hand.

## 2. File anatomy

```sql
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '3s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE app.currencies ( ... );
-- comments carry the data classification per column group and the owning module
-- +goose StatementEnd

-- +goose Down
-- Local development only. Never relied upon in production (see §4).
DROP TABLE app.currencies;
```

- Each migration runs **in one transaction** by default. DDL in PostgreSQL is transactional, so a failed
  migration leaves no partial state.
- `-- +goose NO TRANSACTION` is used **only** for statements that cannot run in a transaction
  (`CREATE INDEX CONCURRENTLY`, `DROP INDEX CONCURRENTLY`, `ALTER TYPE … ADD VALUE` if ever used,
  `REINDEX CONCURRENTLY`), and only **alone in their own file**. Such a file sets
  `SET lock_timeout`/`statement_timeout` at session level and must be idempotent
  (`CREATE INDEX CONCURRENTLY IF NOT EXISTS`), because a failed concurrent build leaves an `INVALID` index that
  the retry must drop and recreate.
- `lock_timeout` and `statement_timeout` are set in **every** migration (ADR-028 §5). A migration that cannot
  get its lock within `lock_timeout` fails fast instead of queueing behind long transactions and blocking all
  traffic behind it; the pipeline retries with backoff (max 3) and then stops for a human.

## 3. What goes into one migration

- One logical change per file (a table with its constraints, indexes, triggers, seed rows and grants; or one
  column change).
- **Grants travel with the table**: the migration that creates a table also grants the runtime role exactly the
  privileges listed in `design/sql/0018_grants.sql` (for example INSERT/SELECT only on append-only tables) and
  attaches the append-only triggers. A table never exists in production without its triggers and grants.
- **State-machine edges** (`app.status_transitions`) are inserted in the migration that creates the guarded
  table; changing an edge is a new migration reviewed together with the Go state machine change (parity test).
- **Seeds**: only reference data (currencies, markets, roles, permissions, role matrix, categories, org roles,
  default policy flags). Deterministic `md5('<kind>:<code>')::uuid` ids. No personal data, no test fixtures.
- Data classification of every new column is stated in a comment (DATA-CLASSIFICATION §5), and the same change
  updates the relevant `docs/database/*.md`.

## 4. Forward-only in production

- Production only ever moves **forward**. `Down` sections may exist for local development (resetting a dev
  database) and CI round-trip checks, but no production runbook relies on them.
- A bad migration is fixed by a **new forward migration** (restore a dropped default, re-add a constraint,
  etc.). If a migration destroyed data, recovery is PITR (DATABASE §14), not a down migration.
- Down migrations that would drop financial, audit or KYC tables are written as `RAISE EXCEPTION` stubs so they
  cannot be run by accident against anything but a disposable database.

## 5. Expand → migrate → contract

Every breaking change ships in at least three releases:

| Phase | Schema | Code |
|---|---|---|
| Expand | Add the new column/table, nullable or with a safe default; add new constraints `NOT VALID` | Old code keeps working; new code writes both |
| Migrate | Backfill in batches (job, not migration, for large tables); `VALIDATE CONSTRAINT` | Reads switch to the new shape |
| Contract | Drop the old column/table, set `NOT NULL` (after a validated `CHECK (col IS NOT NULL)`) | Old shape no longer referenced |

Rules:

- Never rename a column or table in place on a live system; add, dual-write, switch, drop.
- `ADD COLUMN … DEFAULT <constant>` is metadata-only on PostgreSQL 11+; volatile defaults rewrite the table and
  are forbidden on large tables.
- Add CHECK and FK constraints as `NOT VALID`, then `VALIDATE CONSTRAINT` in a later migration (validation
  takes only a `SHARE UPDATE EXCLUSIVE` lock).
- New indexes on populated tables: `CREATE INDEX CONCURRENTLY` in a `NO TRANSACTION` file. New unique
  guarantees: build the unique index concurrently, then `ADD CONSTRAINT … UNIQUE USING INDEX`.
- Changing an enumeration CHECK: add the new value first (expand), deploy code, remove the old value last
  (contract) after confirming no rows use it.

## 6. Locking and zero-downtime rules

| Operation | Lock | Rule |
|---|---|---|
| `CREATE TABLE`, new seed rows | none on existing tables | Safe |
| `ADD COLUMN` (nullable / constant default) | `ACCESS EXCLUSIVE`, brief | Safe with `lock_timeout` |
| `ADD CONSTRAINT … NOT VALID` | `ACCESS EXCLUSIVE`, brief (FK also locks the referenced table `SHARE ROW EXCLUSIVE`) | Safe with `lock_timeout` |
| `VALIDATE CONSTRAINT` | `SHARE UPDATE EXCLUSIVE` | Safe; may run long |
| `CREATE INDEX` (non-concurrent) | `SHARE` (blocks writes) | Only on new/empty tables |
| `CREATE INDEX CONCURRENTLY` | `SHARE UPDATE EXCLUSIVE` | Required on populated tables |
| `ALTER COLUMN TYPE`, `SET NOT NULL` without a validated CHECK | rewrite / full scan under `ACCESS EXCLUSIVE` | Forbidden on populated hot tables; use expand/contract |
| `CREATE OR REPLACE FUNCTION` for a trigger | brief | Allowed; trigger functions for append-only/guards change only with second review |
| `DROP TRIGGER` / `DISABLE TRIGGER` on append-only or guarded tables | — | **Forbidden** in migrations except a reviewed replacement in the same transaction |

Deploy order: migrations run **before** the new binary rolls out, so every migration must be compatible with the
currently running binary (that is what expand/contract guarantees).

## 7. Schema version tracking and readiness

- goose keeps applied versions in **`goose_db_version`** (in schema `app`, owned by `fundzim_migrator`, SELECT
  granted to `fundzim_app`).
- Each binary embeds the highest migration version it was built with. `/readyz` reports **not ready** if
  `max(version_id) WHERE is_applied` is **behind** the binary's expected version (the binary needs schema that
  is not there). A database **ahead** of the binary is normal during expand/contract and is ready.
- `fundzimctl migrate status` prints applied and pending versions; CI fails if the embedded list and the files
  differ.

## 8. Data migrations on financial and audit tables

- **Never `UPDATE`/`DELETE` financial or audit history** in a migration. Append-only triggers would reject it,
  and disabling them is forbidden.
- Corrections and backfills of financial meaning are **new postings or events** created by the owning module's
  code path (for example a ledger adjustment under maker-checker, a `payment_events` row with source
  `MIGRATION`), never raw SQL in a migration.
- Backfilling a new nullable column on a mutable aggregate (for example `campaigns`) is allowed as a batched job
  that writes audit events; it is not part of the DDL migration.
- Reference-data changes (a new currency, a new permission, a new transition edge) are migrations because they
  are reference data, reviewed like code.

## 9. Review and approval

| Migration touches | Reviewers | Production approval |
|---|---|---|
| `ledger`, `audit`, `kyc`, `compliance`, payments tables, payouts tables, grants/roles, append-only or guard triggers | Author + **second reviewer** (DATABASE §13, ADR-028 §5) | Recorded approval in the pipeline (named approver ≠ author) |
| Anything else | Author + one reviewer | Pipeline approval |

The production pipeline step: build → apply to an ephemeral database from empty (all migrations) → run the
invariant test suite (the Stage 2 cases ported to real PostgreSQL) and grant tests as `fundzim_app`,
`fundzim_kyc`, `fundzim_compliance` → apply to staging → approval → apply to production as `fundzim_migrator`
with credentials only available to that step → deploy binary.

## 10. Testing migrations

- **Up from empty** on every CI run against the production PostgreSQL major version (testcontainers).
- **Schema assertions**: the resulting catalogue matches expectations (every table has a PK, `created_at`,
  classification comment; no `numeric`/float money; every append-only table has both triggers; grants as
  designed).
- **Down round-trip** (local/CI only) for non-financial migrations, to keep Down sections honest.
- **Invariant tests**: the `design/sql/tests/*.sql` cases become Go integration tests that run as the app role
  and expect the same failures.
- **Lock tests** for migrations on large tables: run against a seeded copy with a concurrent write load and
  assert no lock wait exceeds `lock_timeout`.

## 11. Local development

`fundzimctl migrate up|down|reset|status` against the Compose PostgreSQL. `reset` refuses to run unless the
database name ends in `_dev` or `_test` and the host is local.
