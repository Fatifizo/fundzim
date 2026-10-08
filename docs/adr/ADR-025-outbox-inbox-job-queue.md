# ADR-025: Transactional outbox, inbox and PostgreSQL job queue (River)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2 (design); implementation Stage 3+

## Context

ARCHITECTURE §7 requires a PostgreSQL-backed queue, a transactional outbox and a webhook inbox, and it
deferred the choice of library to Stage 2/3. Redis must never be authoritative for financial work.

## Decision

1. **Job queue: River** (Go, PostgreSQL, `FOR UPDATE SKIP LOCKED`, transactional enqueue with pgx) in schema
   `queue`. River's migrations are pinned to the library version and run by goose as part of our migrations.
2. **Outbox:** domain modules write `app.outbox_events` in the same transaction as their change. A dispatcher
   claims rows with `SKIP LOCKED`, enqueues River jobs or calls subscribers, and marks rows dispatched.
   Delivery is at least once.
3. **Consumer dedupe:** `app.inbox_events (consumer, event_id)` is unique. A consumer inserts the row in the
   same transaction as its effect.
4. **Webhook inbox:** `psp.provider_webhook_inbox` receives the verified raw event. The HTTP response is sent
   after the insert. Processing is a job routed to the owning module.
5. **Retries:** exponential backoff with jitter, a maximum number of attempts per job kind, then a dead-letter
   state (River `discarded`) with an alert. Nothing is silently dropped.
6. Jobs must be idempotent. Jobs that move money follow record intent → call provider → record result, never
   inside a DB transaction.

## Consequences

### Positive
- One authoritative store. Atomic "change + intent to act". Crash recovery through River's rescue of stuck
  jobs.

### Negative / costs
- A dependency on River's schema and its upgrade path.
- Queue load lands on the primary database (acceptable up to the scenarios in performance-capacity).

### Follow-up work
- Stage 3 pins the River version and adds the dispatcher.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Redis queues (asynq etc.) | Not authoritative; loss of Redis could lose financial work (ARCHITECTURE §8.2) |
| Kafka / RabbitMQ | New infrastructure without a demonstrated need (CLAUDE.md) |
| Hand-written `SKIP LOCKED` queue | Re-implements retries, scheduling and rescue that River already provides |
| graphile-worker / pgmq | Not Go-native (graphile) or less mature Go integration (pgmq) |

## Security implications

Job payloads carry IDs, not C3 data. The same redaction rules apply to job errors stored in the database.

## Financial implications

Guarantees that a committed financial change always has its follow-up work queued and that duplicated
deliveries have no double effect.

## Related

ADR-004, ADR-012; [background-processing.md](../architecture/background-processing.md),
[audit-notification-schema.md](../database/audit-notification-schema.md).
