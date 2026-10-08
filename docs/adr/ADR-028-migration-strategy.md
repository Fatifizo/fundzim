# ADR-028: Database migration strategy (goose, forward-only)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2

## Context

DATABASE §13 required plain SQL migrations, forward-only in production, and support for non-transactional
steps (`CREATE INDEX CONCURRENTLY`). The tool choice was deferred to Stage 2/3 between goose, golang-migrate
and atlas.

## Decision

1. **goose**, with SQL migrations embedded in the Go binary and run by `fundzimctl migrate` as
   `fundzim_migrator` in a dedicated pipeline step, never at application start-up.
2. File naming is `YYYYMMDDHHMMSS_<snake_desc>.sql`. Ordering is by timestamp, and out-of-order application is
   disallowed in production.
3. Each migration runs in a transaction unless annotated `-- +goose NO TRANSACTION`, which is used only for
   `CONCURRENTLY` operations and only in a migration of its own.
4. Forward-only in production. `Down` sections may exist for local development and are never relied on.
   Breaking changes follow expand → migrate → contract.
5. `lock_timeout` and `statement_timeout` are set in every migration. A migration touching `ledger`, `audit`,
   `kyc`, `compliance`, payments or payouts needs a second reviewer, and production runs need recorded
   approval.
6. Schema version tracking uses goose's version table. `/readyz` reports not-ready if the database is behind
   the binary.
7. The Stage 2 SQL drafts in `design/sql` are inputs to these migrations, not migrations themselves.

## Consequences

### Positive
- Simple, SQL-first, embeddable, supports non-transactional steps.

### Negative / costs
- No declarative diffing (atlas offers it). We rely on review plus migration tests from an empty database.

### Follow-up work
- Stage 3 wires goose into `fundzimctl` and CI (apply from empty and verify grants).

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| golang-migrate | Weaker per-file control of non-transactional steps; less convenient embedding |
| atlas | Declarative power we don't need yet; adds a schema-as-code layer to learn and review |
| ORM auto-migrate | Forbidden by DATABASE §1 (no hidden queries/DDL) |

## Security implications

Only the migrator role can run DDL, and its credentials exist only in the pipeline.

## Financial implications

Data fixes on financial tables are new postings or events, never `UPDATE` of history (DATABASE §13).

## Related

ADR-004, ADR-022; [migration-strategy.md](../database/migration-strategy.md),
[migration-plan.md](../database/migration-plan.md).
