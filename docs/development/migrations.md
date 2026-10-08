# Migration Development Guide

> How to write, run and review FundZim's executable PostgreSQL migrations. Policy decisions come from
> [ADR-028](../adr/ADR-028-migration-strategy.md), [migration-strategy.md](../database/migration-strategy.md)
> and the per-stage table split in [migration-plan.md](../database/migration-plan.md). This guide covers the
> mechanics as implemented in Stage 3.

Related: [DATABASE.md](../DATABASE.md) · [configuration.md](configuration.md) · [seed-data.md](seed-data.md) ·
[`migrations/`](../../migrations/README.md) · [design-baseline.md §12](../stage-2/design-baseline.md) (I-16, I-18)

---

## 1. Tooling

| Item | Implementation |
|---|---|
| Tool | [goose](https://github.com/pressly/goose) `v3.28.0` (`github.com/pressly/goose/v3`), used as a library — no goose CLI is installed or needed |
| Location | `migrations/*.sql` at the repository root |
| Embedding | `migrations/embed.go`: `//go:embed *.sql` into `migrations.FS`. `migrations.ExpectedVersion()` returns the highest embedded version |
| Runner | `fundzimctl migrate …` (`apps/api/cmd/fundzimctl`), goose `Provider` with the Postgres dialect over `database/sql` + the pgx stdlib driver |
| Role | Always `fundzim_migrator` via `DATABASE_MIGRATION_URL`. The migrator owns the `fundzim` database and every FundZim object; it is not a superuser and has no `CREATEROLE` locally |
| Version table | goose's `public.goose_db_version`. The runtime roles may only `SELECT` it (for readiness) |
| Readiness | The API's `migrations` readiness check fails while the database version is below `ExpectedVersion()` — so a binary never reports ready on an older schema |

Every Stage 3 binary that is built embeds the migrations, so the API image and `fundzimctl` always agree on
the expected version.

## 2. Commands

| Task | With make | Without make |
|---|---|---|
| Apply all pending | `make migrate-up` | `set -a; . ./.env; set +a; go run ./apps/api/cmd/fundzimctl migrate up` |
| Status (each file, applied time) | `make migrate-status` | `… fundzimctl migrate status` |
| Applied vs embedded version | — | `… fundzimctl migrate version` (exits 1 if the DB is behind) |
| Roll back the most recent migration | `make migrate-down` | `… fundzimctl migrate down` |
| In containers | `docker compose up fundzim-migrate` | (same) — runs `fundzimctl migrate up` once and exits |

`docker compose up -d --build` always runs `fundzim-migrate` before starting `fundzim-api`.

`migrate down` is **refused unless `APP_ENV` is `development` or `test`** (enforced in `fundzimctl`). Production
is forward-only (§6). The overall timeout for one `fundzimctl migrate` run is 10 minutes.

## 3. Writing a migration

### 3.1 File naming

`YYYYMMDDHHMMSS_<snake_case_description>.sql`, UTC timestamp, e.g. `20261015093000_create_users.sql`.
The numeric prefix is the goose version. Never rename, renumber or edit a migration after it has been merged
(it may already be applied somewhere); write a new one instead.

### 3.2 Template

```sql
-- FundZim migration: <what and why>
-- Derived from design/sql/<draft>.sql (if any). Runs as fundzim_migrator via `fundzimctl migrate up`.
-- Forward-only in production (ADR-028); the Down section exists for local development only.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '60s';

CREATE TABLE app.example (...);
-- triggers, indexes, seed reference data ...

CALL app.apply_runtime_grants();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS app.example;
-- +goose StatementEnd
```

Rules:

1. **Nothing before `-- +goose Up` except comments and blank lines.** goose rejects any SQL statement that
   appears before the Up annotation ("failed to parse migration: unexpected state 0"). Put session settings
   **inside** the Up section.
2. **Timeouts in every file.** Use `SET LOCAL lock_timeout` and `SET LOCAL statement_timeout` as the first
   statements of the Up section. goose runs each migration in a transaction, so `SET LOCAL` is scoped to that
   migration (a plain `SET` would persist on the pooled connection). A migration that cannot get its locks in
   time must fail, not queue behind application traffic.
3. **`StatementBegin` / `StatementEnd`** wrap any block that contains semicolons inside one statement
   (PL/pgSQL functions, `DO` blocks, procedures). The Stage 3 files wrap the entire Up and Down sections this
   way; the block is sent to PostgreSQL as one multi-statement batch.
4. **One logical change per migration**, plain SQL, named constraints per [DATABASE.md](../DATABASE.md)
   (`pk_`, `uq_`, `ck_`, `fk_`, `ix_`, `trg_`).
5. **`-- +goose NO TRANSACTION`** only when PostgreSQL forbids a transaction (e.g. `CREATE INDEX
   CONCURRENTLY`). Such a file contains exactly that statement and must be safe to re-run.
6. **Every migration after `20261008120300_runtime_grants.sql` that creates or alters tables, views or
   routines ends its Up section with `CALL app.apply_runtime_grants();`** (§4).
7. **Down sections** exist so developers can iterate locally. They drop what the Up created, in reverse
   order. They never drop roles (cluster-wide, shared with infrastructure) and are never relied on in a
   deployed environment.
8. **No secrets, no personal data, no environment-specific values** in a migration. Reference data only (§7).

### 3.3 Staged foreign keys (I-16)

The `design/sql` drafts show the **end state**. When a column references a table that a later stage creates,
the earlier migration creates the column **nullable and without the FK** (comment it, as
`app.feature_flag_changes.requested_by/approved_by` are). The later stage adds the constraint in three steps:

```sql
ALTER TABLE app.feature_flag_changes
  ADD CONSTRAINT fk_feature_flag_changes_approved_by FOREIGN KEY (approved_by) REFERENCES app.users (id) NOT VALID;
ALTER TABLE app.feature_flag_changes VALIDATE CONSTRAINT fk_feature_flag_changes_approved_by;
-- then, only if the end state requires it:  ALTER TABLE … ALTER COLUMN … SET NOT NULL;
```

The authoritative list of staged FKs is in [migration-plan.md](../database/migration-plan.md). Audit tables
deliberately have **no** FK to `app.users` (audit rows must outlive account anonymisation and accept
system/provider actors).

## 4. Grants: `app.apply_runtime_grants()`

Runtime privileges are **derived**, not hand-written per table. The procedure (created in
`20261008120300_runtime_grants.sql`, owned by the migrator, not executable by `PUBLIC`):

1. revokes everything from `PUBLIC` and the runtime roles in the eight FundZim schemas;
2. grants `fundzim_app` `USAGE` on `app`, `queue`, `ledger`, `audit`, `risk`, `recon` (never `kyc`;
   `compliance` only through approved views later) and sequence usage;
3. per table: reference data (`app.status_transitions`, `app.currencies`, `app.markets`, and later
   `app.permissions`, ledger posting rules) → `SELECT`; `audit` and `ledger` → `SELECT, INSERT` only;
   `queue` → full DML; `app`/`risk`/`recon` → `SELECT, INSERT`, plus `UPDATE` unless the table carries an
   `app.forbid_mutation` UPDATE trigger, plus `DELETE` only for the ephemeral allow-list
   (`app.sessions`, `app.otp_challenges`, `app.idempotency_keys`, `app.upload_sessions`, `app.inbox_events`,
   `app.outbox_events`) and only if no `forbid_mutation` DELETE trigger exists; views → `SELECT` unless named
   `ro_*`;
4. gives `fundzim_kyc` and `fundzim_compliance` `USAGE` on `app` and `SELECT` on `app.status_transitions`
   (their own schemas get grants when their tables exist);
5. verifies that `fundzim_worker` is a member of `fundzim_app` and fails otherwise (role membership is infrastructure: the local init script and production provisioning grant it, because the migrator has no admin rights on roles), then grants `EXECUTE` on an explicit routine allow-list
   (Stage 3: `audit.verify_chain(regclass)` → `fundzim_worker` only);
6. lets `fundzim_app` read `public.goose_db_version`.

New tables therefore get **no** runtime privilege until the migration calls the procedure (fail closed).
When a new table needs a different rule (e.g. a new ephemeral table that may be deleted, a new worker-only
routine), change the procedure in a new migration with `CREATE OR REPLACE PROCEDURE` and review it with the
same care as a security control. The integration tests (`tests/integration`) assert the important outcomes as
the real roles.

Also in that migration: default privileges revoke `EXECUTE` on new routines from `PUBLIC`, and grant
`fundzim_app` DML on future tables in the `queue` schema (for the job-queue library's own tables).

## 5. What exists after Stage 3

| Version | File | Content |
|---|---|---|
| 20261008120000 | `foundation.sql` | Role placeholders (`NOLOGIN`, only if missing), `REVOKE ALL ON SCHEMA public FROM PUBLIC`, schemas `app queue ledger audit kyc risk compliance recon`, `app.forbid_mutation`, `app.set_updated_at`, `app.keep_created_at`, `app.status_transitions` + `app.guard_transition` |
| 20261008120100 | `platform.sql` | Helper triggers (`forbid_column_change`, `allow_only_column_changes`, `set_once_columns`, `bump_version`); `app.currencies` (+ USD, ZWG), `app.markets` (+ ZW), `app.idempotency_keys`, `app.outbox_events`, `app.inbox_events`, `app.feature_flags` + `app.feature_flag_changes` (+ one policy flag) |
| 20261008120200 | `audit.sql` | `audit.audit_events`, `audit.security_audit_events` (hash-chained, append-only), `audit.chain_append()`, `audit.verify_chain()` |
| 20261008120300 | `runtime_grants.sql` | `app.apply_runtime_grants()` and its first call; default privileges |

No River/queue tables exist yet (the `queue` schema is empty). No users, auth, storage or evidence tables
(I-18).

## 6. Environments and safety

| Environment | Up | Down | Notes |
|---|---|---|---|
| development | `fundzimctl migrate up` / compose `fundzim-migrate` | allowed (`make migrate-down`) | Throw-away data only |
| test / CI | yes | allowed; the CI integration job runs a full down-to-0 / up round trip (`FUNDZIM_IT_DESTRUCTIVE=1`) | Throwaway database |
| staging / production | yes, forward-only | **refused by `fundzimctl`** | A bad migration is fixed by a new forward migration. Expand → migrate → contract for breaking changes ([migration-strategy.md](../database/migration-strategy.md)) |

Never run `design/sql/*.sql` against any database: those files are **non-executable design drafts**, validated
only in-memory (`make db-validate`). Executable SQL lives only in `migrations/`. When porting a draft, copy the
relevant part into a new migration, adapt it to the stage's table scope (I-18) and staged FKs (I-16), and keep
the draft unchanged unless the design itself changes.

## 7. Reference data vs seed data

Reference data that every environment needs (currencies, markets, policy flags, later roles and permissions)
is inserted **by migrations**, with deterministic IDs where rows are referenced (the policy flag uses
`md5('feature_flag:<key>')::uuid`). Development-only sample data never goes into a migration — see
[seed-data.md](seed-data.md).

## 8. Review checklist (migrations are a protected path)

- [ ] File name/timestamp correct; no edits to merged migrations.
- [ ] Only comments before `-- +goose Up`; `SET LOCAL` timeouts present.
- [ ] Table scope matches [migration-plan.md](../database/migration-plan.md) for this stage; only tables owned
      by the stage's modules ([design-baseline.md §5](../stage-2/design-baseline.md)).
- [ ] Money as `amount_minor bigint` + `currency char(3)`; `timestamptz`; UUID keys; named constraints.
- [ ] Append-only tables have `app.forbid_mutation` UPDATE/DELETE and TRUNCATE triggers; state machines use
      `app.guard_transition` with edges inserted into `app.status_transitions`.
- [ ] Staged FKs follow I-16 (`NOT VALID` → `VALIDATE` → `SET NOT NULL`).
- [ ] Ends with `CALL app.apply_runtime_grants();`; any change to the procedure reviewed as a security change.
- [ ] Down section drops exactly what Up created; never drops roles.
- [ ] Integration tests cover the new invariants as the runtime roles (append-only, grants, guards).
- [ ] `make migrate-up`, `make migrate-down`, `make migrate-up` run cleanly on a local database, and
      `make test-integration` passes.
- [ ] Two reviewers ([DEVELOPMENT.md §5](../DEVELOPMENT.md)).
