# Stage 18 — Database security (PostgreSQL)

| Area | Implementation | Evidence |
|---|---|---|
| Roles | migrator (owns objects, DDL, not superuser), app, worker (member of app + verification routines), kyc, compliance, ledger (only ledger writer), readonly (ledger views), **backup (Stage 18: `pg_read_all_data`, BYPASSRLS, `default_transaction_read_only`)**; no application connects as superuser | `deploy/docker/postgres/init/*.sh`; `TestSecurityBackupRoleIsReadOnly` |
| Grants | derived from the catalogue by `app.apply_runtime_grants()` (revoke all, then grant: SELECT/INSERT, UPDATE only where no `forbid_mutation` trigger exists, DELETE only on an allow-list of operational tables); routines by explicit allow-list | `TestSecurityRuntimeRolesCannotRewriteFinancialHistory` (no runtime role may DELETE/TRUNCATE ledger, audit or recon tables; only `fundzim_ledger` writes the ledger; posted journals not updatable) |
| Financial schemas | ledger append-only with triggers; recon immutable lines; audit append-only hash chains | Stage 10/17 tests; Stage 18 tamper test |
| Reconciliation cannot modify the ledger | `fundzim_app` has no ledger privilege; reconciliation calls `ledger.Service` approved adjustments only | grants test, archtest |
| Row-level security | compliance cases, events, links, notes, triggers (policies to `fundzim_compliance`); other schemas rely on grants and module isolation | migration `20261009150600` |
| Connections | TLS `verify-full` required outside development; pools per role with limits (API 10–20, worker 10, KYC 8, compliance 4, ledger 2–4); role-level `statement_timeout`, `lock_timeout`, `idle_in_transaction_session_timeout` | config tests; DR exercise (pools reconnect within 0.6 s of the database returning) |
| Audit visibility | every module writes audit events in its own transaction; KYC/compliance/ledger through SECURITY DEFINER gateways | Stage 3–10 tests |
| Backup access | dedicated read-only role, password separate from every runtime role | Stage 18 |
| Administrative access | superuser only for initialisation and break-glass; migrations as `fundzim_migrator`; `migrate down` refused outside development/test | `fundzimctl` |
| Extensions | only `pg_trgm` (trusted extension, discovery search) | migration `20261020090100` |
| Migration safety | goose transactional migrations; Up/Down round trip in CI on a populated throwaway database; Downs keep referenced records (Stage 17 lesson); Stage 18 Downs use `IF EXISTS` and drop only their own tables | CI destructive round trip; local down/up cycles |
| Configuration | `shm_size` 256 MB, SCRAM-SHA-256 host auth | compose |

Production gaps (EXTERNALLY_PENDING): managed PostgreSQL with TLS certificates, PITR (WAL archiving), replicas and
failover, pgAudit or equivalent for administrative sessions, encryption at rest of the volume, network isolation.
