# design/sql — Stage 2 SQL design drafts (NON-EXECUTABLE)

These files are **design artifacts, not migrations**:

- they are never run against a real, shared or production database;
- goose does not read this directory; executable migrations start in Stage 3 under `/migrations`;
- they exist so the schema design can be checked mechanically: they load in order, the constraints and
  triggers behave as specified, and the invariant tests pass.

Load order and ownership: [docs/stage-2/design-baseline.md §10](../../docs/stage-2/design-baseline.md).
Per-domain explanations: `docs/database/*.md`.

## Validate

```bash
cd design/sql/validate
npm ci
npm run validate
```

The harness loads `NNNN_*.sql` in order into an **in-memory** PGlite (PostgreSQL compiled to WASM) and runs
every case in `tests/*.sql`. A case looks like:

```sql
-- @case journal_must_balance expect=error:unbalanced
INSERT INTO ...;
```

Each case runs in its own transaction, so deferred constraint triggers fire at `COMMIT`.

Limitations: PGlite is single-connection, so it cannot test true concurrency (two sessions racing for the same
row). Concurrency is specified in the docs and must be tested against real PostgreSQL in Stage 3+
([docs/testing/test-architecture.md](../../docs/testing/test-architecture.md)). Grants are checked using
`SET ROLE`.
