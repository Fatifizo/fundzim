# FundZim — Database Standards

| | |
|---|---|
| Status | Stage 0 baseline; schema design in Stage 2, first migrations in Stage 3 |
| Database | PostgreSQL (authoritative store for everything, including the ledger, idempotency, webhook inbox, outbox and jobs) — ADR-004 |
| Related | [ARCHITECTURE.md](ARCHITECTURE.md), [MONEY.md](MONEY.md), [LEDGER.md](LEDGER.md), [AUDIT.md](AUDIT.md), [SECURITY.md](SECURITY.md), [DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md) |

These rules apply to every table and every migration. A migration that breaks them needs an ADR, not a code
comment.

## 1. Principles

1. **The database enforces critical invariants itself.** Application checks are the first line of defence,
   and constraints, triggers and grants are the last. A bug in Go code must not be able to create an
   imbalanced journal, a negative amount, an unknown currency, a duplicate payment or an illegal campaign
   state.
2. **PostgreSQL is the only source of truth.** Redis and caches are disposable.
3. **Financial and audit history is append-only.**
4. **Least privilege for every database role.**
5. **Boring and explicit**: plain SQL migrations, explicit constraints, no ORM magic that hides queries.
   A thin query layer such as sqlc or pgx with hand-written SQL is preferred; the final choice is made in
   Stage 2/3.

## 2. Identifiers

- Primary keys are **UUIDv7** (`uuid` type), generated in the application by `internal/platform/ids`, or by a
  DB function if one is adopted. They are time-ordered for index locality and not sequential or enumerable.
- **Never expose sequential integers** externally. Internal surrogate integers are discouraged; if one is
  needed for performance, it never leaves the database.
- UUIDv7 embeds a millisecond creation timestamp. This is acceptable for our entities. Where creation time is
  itself sensitive, use a random UUIDv4 and document why.
- Public campaign URLs use `slug` + a short random suffix (`/c/{slug}-{shortid}`). The short ID is random and
  unique (constraint), and it is not an authorisation mechanism.
- **Authorisation never depends on ID secrecy.** Every access is checked (IDOR defence).
- External references keep their own columns and constraints, e.g. `provider` +
  `provider_transaction_id`, with `UNIQUE (provider, provider_transaction_id)`.

## 3. Time

- All timestamp columns are `timestamptz`. Never `timestamp without time zone`.
- The database session time zone is `UTC` (set on the role and connection). Application code works in UTC.
- Timestamps are produced by the injected clock (`platform/clock`) for business events, so tests are
  deterministic. `now()` defaults are allowed for `created_at` and `updated_at` bookkeeping.
- Distinguish **`occurred_at`** (when it happened in the world, e.g. provider payment time) from
  **`recorded_at` / `created_at`** (when FundZim stored it) and **`posted_at`** (ledger posting time).
  Never overwrite one with another.
- Calendar-date business concepts (campaign end date in Harare) are stored as `timestamptz` instants computed
  from an explicit time zone, or as `date` + `time_zone text`. Never infer the zone from the server.
- Financial correctness never depends on server local time or wall-clock ordering across machines. Ordering
  uses IDs and DB sequencing within transactions.

## 4. Standard columns

| Column | Rule |
|---|---|
| `id uuid PRIMARY KEY` | UUIDv7 |
| `created_at timestamptz NOT NULL DEFAULT now()` | Required on every table. Immutable: a trigger or grant prevents updates |
| `updated_at timestamptz NOT NULL DEFAULT now()` | Only on mutable tables; maintained by trigger. **Absent** on append-only tables |
| `version integer NOT NULL DEFAULT 1` | Optimistic concurrency on mutable aggregates (campaigns, payout requests) |
| `created_by` / `actor_id` | Where an actor matters; FK to users/staff or a typed actor reference (system actors have IDs too) |

## 5. Money columns

See [MONEY.md](MONEY.md). The database form is always:

```sql
amount_minor BIGINT  NOT NULL,
currency     CHAR(3) NOT NULL REFERENCES currencies(code),
```

- **No `numeric`, `real`, `double precision` or `money` type** for monetary amounts. (`numeric` may appear only
  for non-monetary ratios if ever justified by ADR. Fee rates use integer basis points.)
- Every money column has a sign `CHECK`, e.g. `CHECK (amount_minor > 0)` for ledger entries and payment
  amounts. Signed amounts are allowed only where the semantics are documented.
- Every amount column sits next to a `currency` column. A table with two amounts in potentially different
  currencies has two currency columns.
- Aggregation queries **always** `GROUP BY currency`. A `SUM(amount_minor)` across currencies is a bug, and
  code review rejects it.
- `currencies` reference table: `code CHAR(3) PK`, `numeric_code SMALLINT`, `minor_units SMALLINT`, `name`,
  `display_symbol text` (e.g. `US$`, `ZiG`), `enabled boolean` (full definition in [MONEY.md](MONEY.md) §2). Seeded with `USD (840, 2)` and `ZWG (924, 2)`.

## 6. Constraints

- **Foreign keys everywhere** relationships exist, with `ON DELETE RESTRICT` by default. `CASCADE` only for
  pure child rows with no independent meaning and never for anything financial, audit or KYC.
- **NOT NULL by default.** Nullable columns must have a reason.
- **CHECK constraints** for enumerations (`state IN (...)`), positive amounts, ranges, and cross-column rules
  (e.g. `ended_at IS NULL OR ended_at >= started_at`). Use `text` + `CHECK` (or a lookup table) rather than
  Postgres `ENUM` types, which are awkward to evolve.
- **UNIQUE constraints** back every idempotency and external-reference guarantee:
  - `idempotency_keys (scope, key)`
  - `webhook_inbox (provider, provider_event_id)`
  - `payments (provider, provider_transaction_id)` where not null
  - `ledger_transactions (idempotency_key)`
  - `payout_requests (idempotency_key)`
  - Partial unique indexes for "at most one active X", e.g. one default payout destination per owner and
    currency.
- **Deferred constraint triggers** for invariants spanning rows. The canonical one: every
  `ledger_transaction` balances per currency at COMMIT (see [LEDGER.md](LEDGER.md)).
- State-machine columns are guarded by a `CHECK` on the allowed values plus a trigger that rejects illegal
  transitions for the critical machines (campaigns, payments, payouts), so an illegal transition is
  impossible even via direct SQL. The application state machine remains the primary implementation and must
  agree with the trigger (tested in both directions).

## 7. Schemas and data separation

| Schema | Contents | Accessed by |
|---|---|---|
| `public` / `app` (final name in Stage 2) | Core domain tables (users, organisations, campaigns, payments, payouts, fees, outbox, jobs, idempotency) | API/worker app role |
| `ledger` | `ledger_accounts`, `ledger_transactions`, `ledger_entries`, balance projections | App role, via the `ledger` module only: SELECT + INSERT on `ledger_accounts`, `ledger_transactions`, `ledger_entries`; additionally UPDATE on the `ledger_balances` projection (same-transaction projection updates, LEDGER.md §8) |
| `audit` | `audit_events` | App role: INSERT + SELECT (SELECT is needed to read `prev_hash` for the hash chain); staff viewing goes through the audit module's authorised viewer |
| `kyc` | Verification cases, identity attributes (encrypted), document metadata (objects live in `private-kyc` bucket), organisation (KYB) records, beneficiary evidence, consents, and `fundraising_authorities` (PVO registration, excluded-body basis or s8 temporary authority per campaign/organisation; Stage 1, [kyb-architecture.md](compliance/kyb-architecture.md)) | `fundzim_kyc` role only, used solely by the `kyc` module |
| `risk` | Limits (append-only versions, [aml-risk-framework.md](compliance/aml-risk-framework.md)), monitoring rules, alerts, scores, linkage edges ([transaction-monitoring.md](compliance/transaction-monitoring.md)). No C3 data. Added in Stage 1. | App role via the `risk` module only; limit changes are maker-checker |
| `compliance` | Compliance cases, holds, screening list sources, screening requests and hits ([compliance-case-management.md](compliance/compliance-case-management.md), [sanctions-screening.md](compliance/sanctions-screening.md)). Restricted-STR cases carry a confidentiality level. Added in Stage 1. | App role via the `compliance` module only; restricted-case rows readable only with the specific permission |

Cross-schema foreign keys are allowed only from `kyc` → core (e.g. `user_id`) and from `ledger` → core
reference IDs where needed. Nothing in core references KYC attribute tables; core sees only a
verification **level/status** projection. Final grants for `risk` and `compliance` (including whether
`compliance` needs its own database role like `kyc`) are decided in Stage 2.

C3 fields such as national ID numbers and passport numbers are stored **encrypted at the application level**
(envelope encryption with KMS-managed keys), with an HMAC **blind index** column for duplicate detection.
Dates of birth and payout account numbers are also encrypted or partially masked, per
[DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md).

## 8. Database roles and grants

| Role | Purpose | Privileges |
|---|---|---|
| `fundzim_migrator` | Owns objects; runs migrations | DDL. Not used by running services. Credentials only in the migration pipeline |
| `fundzim_app` | API + worker runtime | DML on core tables; **SELECT/INSERT only** on `ledger_accounts`, `ledger_transactions`, `ledger_entries` and `audit_events` (no UPDATE/DELETE/TRUNCATE); UPDATE only on the `ledger_balances` projection; no access to `kyc` |
| `fundzim_kyc` | KYC module connection | DML only on `kyc.*` tables (does **not** own the schema, so it cannot disable triggers or alter tables); SELECT on minimal core tables |
| `fundzim_readonly` | Reporting / reconciliation analytics | SELECT on approved views only; no `kyc`; masked views for PII |
| Break-glass admin | Emergency DBA access | Time-boxed, individually named, audited, alerted; never shared |

- No service connects as a superuser.
- `REVOKE ALL ON SCHEMA public FROM PUBLIC` and explicit default privileges.
- Append-only enforcement uses **both** grants and triggers that raise (`BEFORE UPDATE OR DELETE` per row and
  `BEFORE TRUNCATE` per statement), so a
  mistaken grant alone does not open the table.
- Row-level security (RLS) may be added for defence in depth on multi-tenant tables (Stage 2 decision). It does
  not replace application authorisation.

## 9. Append-only tables and soft deletion

**Append-only (never UPDATE, DELETE or TRUNCATE):** `ledger_transactions`, `ledger_entries`, `audit_events`,
`payment_events` (provider-event history), `webhook_inbox` raw payload columns (processing-status columns may be
updated, or kept in a sibling table), campaign state transitions, payout state transitions, fee-schedule
versions.

**Mutable with history:** aggregates such as `campaigns`, `payments` and `payout_requests` keep a current-state
row (with `version`) **plus** an append-only transitions/events table. The current state must always be
derivable from, and consistent with, the event history.

**Soft deletion policy**

- Financial, audit and KYC records are **never hard-deleted** by application code. Retention and erasure
  follow [PRIVACY.md](PRIVACY.md). Erasure of personal data where legally required is done by
  **anonymisation/crypto-shredding** (destroying the per-record key) rather than deleting financial rows.
  Retention periods are **LEGAL_REVIEW_REQUIRED** (LR-012).
- User-facing content (drafts, media, updates) uses `deleted_at timestamptz NULL` soft deletion with
  partial indexes (`WHERE deleted_at IS NULL`). Hard purge happens through scheduled, audited retention jobs.
- No "is_deleted boolean" columns; use `deleted_at` (+ `deleted_by`).

## 10. Naming conventions

- `snake_case` for everything. Tables are **plural nouns** (`campaigns`, `ledger_entries`).
- Columns: `<entity>_id` for FKs; `*_at` for `timestamptz`; `*_on` for `date`; `is_*` / `has_*` for booleans;
  `amount_minor` + `currency` for money; `*_hash` for hashes; `*_ciphertext` + `*_key_id` for
  application-encrypted fields; `*_bidx` for blind indexes.
- Constraint and index names are explicit and stable: `pk_<table>`, `fk_<table>_<column>`,
  `uq_<table>_<cols>`, `ck_<table>_<rule>`, `ix_<table>_<cols>`, `trg_<table>_<purpose>`.
- State values are `UPPER_SNAKE` strings matching the API and documentation (`UNDER_REVIEW`).

## 11. Indexing

- Index every foreign key used in joins or `ON DELETE` checks.
- Index access paths actually used: e.g. `ledger_entries (account_id, created_at)` and
  `payments (campaign_id, state)`.
- Use partial indexes for queues and hot subsets (`WHERE state = 'PENDING'`, `WHERE processed_at IS NULL`).
- Create indexes `CONCURRENTLY` in migrations against populated tables (requires a non-transactional
  migration step; the tooling must support this).
- Do not add speculative indexes. Each index costs write throughput on hot financial tables.

## 12. Transactions, isolation and locking for financial operations

- **One business operation = one database transaction.** A ledger posting, its balance-projection update, the
  related payment state change, the audit event and the outbox message commit **together or not at all**.
- **Never call an external provider inside an open database transaction.** Pattern: record intent (commit) →
  call provider with our idempotency reference → record result (new transaction). Use the outbox for
  asynchronous calls.
- Default isolation is `READ COMMITTED`, plus **explicit row locks** for financial decisions:
  - Payout/withdrawal: `SELECT … FOR UPDATE` on the owner's balance-projection row(s) for that currency (or
    an advisory lock keyed by account + currency), then re-check available balance from ledger data
    **inside** the lock, then post.
  - Payment state transitions: lock the payment row (`FOR UPDATE`), apply precedence rules, write the
    transition.
- Use `SERIALIZABLE` where a decision reads a set of rows that concurrent transactions may change and no
  single row lock covers it. The application must then **retry on serialization failure (SQLSTATE 40001) and
  deadlock (40P01)** with bounded attempts. Retries are safe only because operations are idempotent.
- Job queue workers claim work with `FOR UPDATE SKIP LOCKED`.
- Lock ordering is documented per module to avoid deadlocks (e.g. always lock accounts in ascending
  `account_id` order).
- Set `statement_timeout` and `lock_timeout` on app connections. Long-running reports go to the read-only
  role and, later, a replica.
- Optimistic concurrency (`version` column, `UPDATE … WHERE id = $1 AND version = $2`) for user-edited
  aggregates such as campaign drafts.

## 13. Migrations

- Tooling: plain SQL migration files in `/migrations`. Tool chosen in Stage 2/3 (candidates: goose,
  golang-migrate, atlas) and recorded in an ADR.
- File naming: ordered (timestamp or sequence) + description, e.g. `20261101120000_create_currencies.sql`.
- **Forward-only in production.** "Down" migrations may exist for local development but are never relied on
  to undo production changes. Fixes are new forward migrations.
- **Expand → migrate → contract** for breaking changes: add new column/table, backfill, switch code, then
  remove the old column in a later release.
- Migrations are reviewed like code. Any migration touching `ledger`, `audit`, `kyc`, payments or payouts
  requires a second reviewer.
- Data backfills on financial tables are **new events or postings**, never `UPDATE`s of history.
- Migrations run under `fundzim_migrator` (the sole owner of all schemas and objects) in a dedicated step, never at app start-up by the app role.
- `/readyz` reports not-ready if the schema version is behind what the binary expects.
- Seed data: only reference data (currencies, categories, permissions) is seeded by migrations. Test fixtures
  live with tests, and no real personal data is ever seeded.

## 14. Backups and recovery

- Continuous WAL archiving with **point-in-time recovery (PITR)** plus regular base backups. The tool and
  hosting are decided at the production architecture stage (Stage 18).
- Backups are **encrypted** at rest with keys separate from the database host, and stored in a separate
  account/location with restricted, audited access.
- KYC data in backups inherits C3 handling. Crypto-shredded records stay unreadable in backups because their
  keys are destroyed.
- **Restore drills** are run on a schedule (at least quarterly once in production) and after major changes.
  A backup that has never been restored is not considered a backup.
- After any restore, run ledger invariant checks and reconciliation **before** resuming financial
  operations.
- Recovery Point and Recovery Time Objectives (RPO/RTO) are set in Stage 18. Retention of backups is
  **LEGAL_REVIEW_REQUIRED** (LR-012, LR-034) (interaction with erasure and record-keeping obligations).

## 15. Testing the database layer

- Integration tests run against real PostgreSQL (same major version as production), not SQLite or mocks.
- Every invariant listed here has a test that tries to violate it **directly in SQL as the app role** and
  expects failure: imbalanced journal, UPDATE on ledger or audit tables, negative amount, unknown currency,
  duplicate idempotency key, illegal state transition.
- Migration tests: apply all migrations from empty; schema matches expectations; app role grants verified.
