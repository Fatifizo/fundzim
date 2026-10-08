# ADR-004: PostgreSQL as the authoritative database

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (schema design Stage 2, implementation Stage 3+)

## Context

FundZim needs:

- ACID transactions spanning payments, ledger, fees and outbox;
- strong constraints (foreign keys, unique, check, deferred triggers) to enforce financial invariants in the
  database as well as in code;
- row-level locking for concurrency;
- mature backup and point-in-time recovery.

We also need durable queues for idempotency keys, webhook inbox, outbox and background jobs. A second datastore
for these would split the source of truth.

## Decision

**PostgreSQL is the sole authoritative store.** This includes:

- domain data;
- the ledger;
- audit events;
- idempotency keys;
- `webhook_inbox`;
- the transactional outbox;
- the background job queue (Postgres-backed with `FOR UPDATE SKIP LOCKED`; library such as River chosen in
  Stage 2/3).

Standards (detailed in [DATABASE.md](../DATABASE.md)):

- **Keys:** UUIDv7 primary keys, never exposed sequential integers.
- **Timestamps:** `timestamptz` stored in UTC (ADR-011).
- **Money:** `BIGINT amount_minor` + `CHAR(3) currency` referencing `currencies` (ADR-005).
- **Constraints:** foreign keys, `UNIQUE` and `CHECK` constraints for every critical invariant. Examples:
  `amount_minor > 0` on ledger entries, `UNIQUE(provider, provider_transaction_id)`, and
  `UNIQUE(ledger_transactions.idempotency_key)`.
- **Ledger and audit tables:** append-only. The application role has no UPDATE, DELETE or TRUNCATE on them, and triggers
  block mutation.
- **Separate DB roles:**
  - `fundzim_migrator`, the sole owner of all schemas and objects;
  - application role;
  - KYC role (`fundzim_kyc`), with DML only on the `kyc` schema's tables;
  - read-only reporting role.
- **Migrations:** forward-only plain SQL, reviewed. Tool chosen in Stage 2/3 (goose, golang-migrate or Atlas).

Redis may be used for rate limiting, caching and ephemeral coordination only (see ARCHITECTURE.md). Losing
Redis must never lose or corrupt financial data.

## Consequences

### Positive
- One transactional source of truth, with financial invariants enforced at both the application and database
  layers.
- A boring, proven, well-understood operational model with PITR and logical replication available.

### Negative / costs
- Postgres-backed queues have lower throughput ceilings than dedicated brokers. This is acceptable at the
  expected scale and can be revisited by ADR if a need is demonstrated.
- Database-level invariants (triggers) add migration complexity and need dedicated tests.

### Follow-up work
- Stage 2: schema design.
- Stage 3: role setup, migration tooling, a constraint test suite.
- Stage 18: backup encryption and restore drills.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| MySQL/MariaDB | Weaker support for deferred constraints and transactional DDL, which matters for ledger invariants and safe migrations. |
| Document store (MongoDB) | Weaker relational integrity. Multi-document financial transactions are possible but not idiomatic. |
| Redis/Kafka for jobs and events | Splits the source of truth and adds infrastructure without a demonstrated need. Prohibited for authoritative financial data. |
| Dedicated ledger database (e.g. TigerBeetle) | Attractive later. Today it adds a second source of truth and an operational burden. Revisit by ADR after Stage 10 if volume demands it. |

## Security implications
- Least-privilege roles.
- TLS for all connections.
- Encryption at rest (managed disk/volume encryption, plus application-level encryption for C3 identity fields
  per ADR-009).
- Parameterised queries only.
- Backups encrypted and restore-tested.
- The reporting role cannot read the `kyc` schema.

## Financial implications
The database is the last line of defence for financial integrity. Uniqueness constraints stop duplicate
postings even if application idempotency fails, and a deferred trigger rejects unbalanced ledger transactions
at commit.

## Related
ADR-001, ADR-005, ADR-006, ADR-009, ADR-011, [DATABASE.md](../DATABASE.md), [LEDGER.md](../LEDGER.md).
