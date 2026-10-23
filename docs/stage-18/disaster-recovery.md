# Stage 18 — Disaster recovery and high availability

## 1. Failure domains and recovery order

| Failure domain | Data dependency | Recovery |
|---|---|---|
| Web | none (stateless) | restart / reschedule |
| API replicas | PostgreSQL, Valkey (optional), Garage, secret manager | restart; replicas share nothing in memory (rate limits in Valkey with in-process fallback) |
| Worker | PostgreSQL (River queue), Garage, secret manager | restart; River resumes jobs; outbox is durable |
| PostgreSQL | authoritative for everything | restore from backup (PITR in production) |
| Garage | private documents, statements, media | restore snapshot + blocks; verify objects |
| Secret manager | keys and provider credentials | its own snapshot + unseal keys |
| Valkey | none (cache) | restart empty |

Recovery order: secrets → PostgreSQL → object storage → API → worker → web → verification (backup-recovery.md §4).
Recovery access: named administrators through the management network; backup key custody outside the platform.

No secondary infrastructure exists (none is invented here). The production DR design (proposed): a second region or
provider zone with PostgreSQL replicas and WAL archive, replicated object storage, a secret manager with its own DR,
and infrastructure as code to recreate the application tier.

## 2. Recovery objectives

| Objective | Engineering proposal | Business-approved | Measured (development) |
|---|---|---|---|
| RPO, PostgreSQL | ≤ 5 min with WAL archiving; ≤ 24 h with nightly dumps only | **not approved** (owner) | dump-based: the age of the last dump |
| RPO, object storage | ≤ 24 h (daily snapshot) — must be at least as fresh as the database | not approved | — |
| RTO, full restore | ≤ 4 h | not approved | 173 s for the development data set (restore drill) |
| RTO, single service | ≤ 5 min | not approved | 0.3–0.6 s after restart (exercise below) |

These are proposals, not service commitments (KI-S18-12).

## 3. Exercises (2026-10-23, `scripts/dr/exercise.sh`, local compose stack)

| Scenario | Behaviour during the failure | Recovery |
|---|---|---|
| API container killed | API not answering; web `/healthz` 200; web-proxied API 503 | 0.33 s after restart. **Docker does not apply a restart policy to `docker kill`** — production needs a supervisor with health-based restarts (KI-S18-19). The first exercise run was invalid for this reason and was discarded |
| PostgreSQL stopped | API ready 503, liveness 200; worker ready 503; public read 500 → **fixed to 503** (F-S18-18) | API ready 0.59 s and worker 0.04 s after PostgreSQL started (pools reconnect; no process restart) |
| Valkey stopped | API ready 200 (Valkey not required); login still answers 401 (rate limits fall back in process) | 0.04 s |
| Garage stopped | API ready 503 (buckets are critical checks); uploads and downloads fail closed | 0.59 s after start |
| Worker killed | queued outbox events stay queued (149 eligible-later events unchanged) | worker ready 0.3 s after restart; jobs resume |
| Secret manager stopped | running API unaffected (ready 200; cached provider secrets) | dev OpenBao lost its contents (404) → re-seeded in 3.3 s; a real store needs its own restore |
| *Rerun on the final build* | public read during the PostgreSQL stop now **503** (fix confirmed); API ready 0.33 s and worker 0.03 s after PostgreSQL started; API 0.32 s after restart; Garage 0.58 s; Valkey 0.03 s; dev OpenBao re-seeded in 3.8 s | — |
| Invalid configuration release | the API process exits 1 with validation errors (never starts half-configured) | roll back the configuration |
| Failed migration | a migration that fails half-way leaves no object and no version row; the one before it stays applied (`TestSecurityFailedMigrationLeavesNothing`, own version table); Downs are tested by the CI round trip on a populated database | fix and re-run |
| Corrupt backup / partial restore | refused (backup-recovery.md §5) | use the previous set |

## 4. High availability

**Not claimed.** The development stack is single-node everywhere (one PostgreSQL, one Garage node with replication 1,
one OpenBao dev server, one Valkey). What the code supports: stateless API replicas (a second replica is in compose
under the `scale-test` profile; distributed rate limits are tested), several workers (River coordinates with
PostgreSQL leases; jobs are idempotent), queue isolation per workload, idempotent consumers. Production HA needs
managed PostgreSQL with failover, multi-node object storage, an HA secret manager and a load balancer (Stage 20).

## 5. Worker resilience

| Worker | Idempotency | Retry limits | Coordination / crash recovery | Dead letters | Queue |
|---|---|---|---|---|---|
| Payments (poll, sweeper) | IN_FLIGHT before calls; attempt rows; UNKNOWN never failed | backoff to 10 min; UNKNOWN horizon → reconciliation | River leases; in-flight sweeper | outbox dead letters | default |
| Payouts | attempts guard; retry budget for DEFINITELY_NOT_SENT / RATE_LIMITED; UNKNOWN never resubmitted | `PAYOUTS_SUBMIT_RETRY_BUDGET` | DB guards | SUBMISSION_RETRIES_EXHAUSTED exception | default |
| Reconciliation | hash-first imports, guarded runs, stale-run reaper | job retries | heartbeats | exceptions | reconciliation |
| Notifications | exactly-once creation, recorded delivery | bounded retries | sweeps | notification dead letters | notifications |
| Discovery | ordered by snapshot time, idempotent | — | — | — | discovery |
| Risk | deterministic rules | — | — | — | default |
| Audit verification, security scan (Stage 18) | append-only results; advisory lock | job retries | maintenance queue, unique per period | alerts | maintenance |

Health: worker `/readyz` (database, River), heartbeats, the Stage 14 "worker stopped" alert, now persisted as a
security/operations alert. Backpressure: per-queue worker counts; outbox relay batches. Payments in PENDING are polled
indefinitely (KI-S18-22, a Stage 19 input).
