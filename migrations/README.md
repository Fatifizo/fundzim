# migrations — executable PostgreSQL migrations

[goose](https://github.com/pressly/goose) v3 SQL migrations, embedded into the Go binaries by `embed.go`
(`migrations.FS`, `migrations.ExpectedVersion()`) and applied by `fundzimctl migrate` as the `fundzim_migrator`
role ([ADR-028](../docs/adr/ADR-028-migration-strategy.md)). The API reports not-ready while the database is
behind the embedded version.

| File | Content |
|---|---|
| `20261008120000_foundation.sql` | Role placeholders, schemas (`app queue ledger audit kyc risk compliance recon`), shared trigger functions, `app.status_transitions` + `app.guard_transition` |
| `20261008120100_platform.sql` | Helper triggers; `app.currencies` (USD, ZWG), `app.markets` (ZW), `app.idempotency_keys`, `app.outbox_events`, `app.inbox_events`, `app.feature_flags`, `app.feature_flag_changes` |
| `20261008120200_audit.sql` | `audit.audit_events`, `audit.security_audit_events` (hash-chained, append-only) |
| `20261008120300_runtime_grants.sql` | `app.apply_runtime_grants()` — derived least-privilege grants for the runtime roles |

```bash
make migrate-up | make migrate-status | make migrate-down      # down: development/test only
# without make:
set -a; . ./.env; set +a; go run ./apps/api/cmd/fundzimctl migrate up    # status | version | down
```

Rules (full guide: [docs/development/migrations.md](../docs/development/migrations.md)): file names
`YYYYMMDDHHMMSS_<description>.sql`; only comments before `-- +goose Up`; `SET LOCAL` lock/statement timeouts
inside the Up section; `StatementBegin`/`StatementEnd` around PL/pgSQL; every later migration that adds tables
ends with `CALL app.apply_runtime_grants();`; forward-only in production; staged foreign keys per design-baseline
I-16; never edit a merged migration. `design/sql/` holds design drafts, **not** migrations.
