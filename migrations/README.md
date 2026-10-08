# migrations — PostgreSQL schema migrations

**Status: none yet.** The schema is designed in Stage 2 and the migration tool (candidates: goose,
golang-migrate, Atlas) is selected in Stage 2/3 by ADR.

Standards (full detail in [docs/DATABASE.md](../docs/DATABASE.md)): plain SQL, one change per migration,
reviewed like code, forward-only in production, run by the dedicated migrator role (never the app role),
and every financial invariant also enforced by constraints/triggers here.
