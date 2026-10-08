# Performance and Capacity Design

> **Status:** Stage 2 design. **These are design scenarios, not claims.** Nothing has been measured, because no
> code exists yet. Every number below is either an explicit **assumption** (marked A-n) or arithmetic derived
> from assumptions. Stage 19 replaces the assumptions with measurements and documents real capacity limits
> (ROADMAP Stage 19). Values marked **[default]** are tunable starting points.

Related: [ARCHITECTURE.md §11](../ARCHITECTURE.md) (scalability path) · [LEDGER.md §9](../LEDGER.md)
(concurrency) · [DATABASE.md §11–§12](../DATABASE.md) · [background-processing.md](background-processing.md) ·
[observability.md](observability.md) · [ledger-posting-model.md](ledger-posting-model.md) ·
[docs/database/schema-overview.md](../database/schema-overview.md) ·
[test-architecture.md §3.12](../testing/test-architecture.md) (k6 scenarios)

---

## 1. Scope

Four design tiers by **registered users**: 1k, 10k, 100k and 1M. For each tier this document derives
expected load, identifies the first bottleneck, and fixes the design response together with the
**measurable trigger** for adopting it. The architecture adopts nothing speculatively (ARCHITECTURE §11:
"each step taken only when measurements justify it"). It does make sure that no step requires a redesign.

## 2. Assumptions

| ID | Assumption | 1k | 10k | 100k | 1M | Basis |
|---|---|---|---|---|---|---|
| A1 | Monthly active ratio (MAU / registered) | 40 % | 30 % | 25 % | 20 % | Typical decay as a platform grows. Unverified. |
| A2 | Share of registered users who ever create a campaign | 5 % | 4 % | 3 % | 2 % | Donation platforms are donor-heavy |
| A3 | Share of campaign creators with an **active** campaign at any time | 40 % | 40 % | 40 % | 40 % | Campaign duration of weeks |
| A4 | Average completed donations per active campaign per day | 2 | 2 | 2 | 2 | Long tail. Most campaigns receive few donations. |
| A5 | Checkout completion rate (completed / initiated) | 70 % | 70 % | 70 % | 70 % | Mobile-money prompts expire or are declined |
| A6 | Share of a day's donations in the peak hour | 15 % | 15 % | 15 % | 15 % | Evening peak |
| A7 | Mean checkout duration (initiate → authoritative outcome) | 120 s | 120 s | 120 s | 120 s | USSD/push approval on mobile money. Cards are faster. |
| A8 | **Viral campaign:** donations to one campaign in its peak hour | 200 | 1 000 | 5 000 | 20 000 | WhatsApp group sharing (funerals, medical emergencies) |
| A9 | Burst factor inside the viral hour (peak 10 min vs hour average) | ×3 | ×3 | ×3 | ×3 | Shares land in large groups at once |
| A10 | Campaign page → donation conversion | 5 % | 5 % | 5 % | 5 % | |
| A11 | Webhooks per checkout initiation | 1.7 | 1.7 | 1.7 | 1.7 | 2 per completed payment (pending + final), 1 per abandoned/failed: 0.7×2 + 0.3×1 |
| A12 | Status polls per initiation (provider calls) | 3 | 3 | 3 | 3 | Backstop polling (background-processing §6.2) |
| A13 | Ledger entries per donation over its lifecycle | 10 | 10 | 10 | 10 | T1 (3 lines) + T2 (2) + T3 (2) + T4 (2–3) ([settlement-and-custody-model.md §5](../ledger/settlement-and-custody-model.md)) |
| A14 | `payment_events` rows per initiation | 4 | 4 | 4 | 4 | CREATED, PENDING, outcome, plus occasional extra |
| A15 | Audit events per donation / per MAU per month | 6 / 5 | 6 / 5 | 6 / 5 | 6 / 5 | |
| A16 | Provider webhook retry storm after a provider outage | ×10 of normal peak rate for 15 min | same | same | same | TESTING §3.10 |
| A17 | Staff users | 5 | 15 | 50 | 200 | |

## 3. Derived load (arithmetic shown)

### 3.1 Steady state

| Quantity | Formula | 1k | 10k | 100k | 1M |
|---|---|---|---|---|---|
| MAU | R × A1 | 400 | 3 000 | 25 000 | 200 000 |
| Campaign creators | R × A2 | 50 | 400 | 3 000 | 20 000 |
| Active campaigns | creators × A3 | 20 | 160 | 1 200 | 8 000 |
| Donations / day | active × A4 | 40 | 320 | 2 400 | 16 000 |
| Initiations / day | donations ÷ A5 | 57 | 457 | 3 429 | 22 857 |
| Peak-hour donations | donations/day × A6 | 6 | 48 | 360 | 2 400 |
| Peak-hour donations / s | ÷ 3 600 | 0.002 | 0.013 | 0.10 | 0.67 |
| Peak-hour initiations / s | ÷ A5 | 0.002 | 0.019 | 0.14 | 0.95 |
| Concurrent checkouts at peak (Little's law L = λW) | initiations/s × A7 | ≈ 0.3 | ≈ 2.3 | ≈ 17 | ≈ 114 |
| Webhooks / s at peak | initiations/s × A11 | 0.004 | 0.03 | 0.24 | 1.6 |
| Provider status polls / s at peak | initiations/s × A12 | 0.007 | 0.06 | 0.43 | 2.9 |

### 3.2 Viral spike (one campaign, on top of steady state)

| Quantity | Formula | 1k | 10k | 100k | 1M |
|---|---|---|---|---|---|
| Viral donations / s (hour average) | A8 ÷ 3 600 | 0.056 | 0.28 | 1.39 | 5.56 |
| Viral donations / s (burst) | × A9 | 0.17 | 0.83 | 4.2 | 16.7 |
| Viral initiations / s (burst) | ÷ A5 | 0.24 | 1.19 | 5.95 | 23.8 |
| Concurrent checkouts (burst) | × A7 (120 s) | 29 | 143 | 714 | 2 857 |
| Webhooks / s (burst) | initiations × A11 | 0.4 | 2.0 | 10.1 | 40.5 |
| Webhooks / s (outage retry storm, A16) | 10 × (steady peak + viral burst) | 4 | 20 | 103 | 421 |
| Status polls / s (burst) | initiations × A12 | 0.7 | 3.6 | 17.9 | 71 |
| Campaign page views / s (burst) | donations/s ÷ A10 | 3.3 | 17 | 83 | 333 |
| Ledger projection updates / s on **that** campaign's `campaign_unsettled` row | donations/s (T1) + T2 at capture | 0.33 | 1.7 | 8.3 | 33 |
| Updates / s on the **global** `revenue:platform_fees` (per currency) and `psp_clearing:{p}` rows | all donations platform-wide × postings touching the row | ≈ 0.2–0.4 | ≈ 0.9–1.7 | ≈ 4.4–8.6 | ≈ 17–35 |

### 3.3 Data growth per year (for partitioning decisions)

| Table | Formula | 1k | 10k | 100k | 1M |
|---|---|---|---|---|---|
| `ledger.ledger_entries` | donations/day × A13 × 365 | 146 k | 1.17 M | 8.8 M | 58 M |
| `ledger.ledger_transactions` | ≈ 4 journals per donation | 58 k | 467 k | 3.5 M | 23 M |
| `app.payment_events` | initiations/day × A14 × 365 | 83 k | 667 k | 5.0 M | 33 M |
| `app.provider_webhook_inbox` | initiations/day × A11 × 365 | 35 k | 284 k | 2.1 M | 14 M |
| `audit.audit_events` | (donations × 6 + MAU × 5 / 30) per day × 365 | 112 k | 883 k | 6.8 M | 47 M |
| `app.security_events` | MAU × 4 logins/month × 3 events × 12 | 58 k | 432 k | 3.6 M | 29 M |
| `app.outbox_events` | ≈ 5 per donation (prunable after retention, LR-012) | 73 k | 584 k | 4.4 M | 29 M |

At 1M users, the largest table grows by about 60 M rows a year, or roughly 15–25 GB a year including indexes
(estimating ~250–400 B per entry row with indexes). That is comfortably within one PostgreSQL primary.
Partitioning is a maintenance tool here (vacuum, retention), not a throughput necessity (§14).

## 4. Latency budgets (per endpoint p95, server-side) [default]

TESTING §3.10 requires per-endpoint p95 targets to be set in Stage 2. They are measured at the API (excluding
the network to the client), on production-like data volumes, at the tier's steady peak.

| Endpoint / operation | p95 | p99 | Notes |
|---|---|---|---|
| `GET /api/v1/campaigns/{id}` (public, cache miss) | 150 ms | 400 ms | A cache hit at the CDN/Next.js layer costs no API time |
| `GET /api/v1/campaigns` (list/search, cursor) | 250 ms | 600 ms | |
| `POST /api/v1/donations`, **excluding** the provider call | 300 ms | 700 ms | DB work: idempotency + intent + attempt + audit + outbox |
| `POST /api/v1/donations`, end to end | provider p95 + 300 ms | — | Provider HTTP timeout bounded at 15 s [default] → UNKNOWN beyond |
| `GET /api/v1/donations/{id}` (status polling by the confirmation page) | 100 ms | 250 ms | Called every 3–5 s per waiting donor |
| `POST /api/v1/webhooks/{provider}` (ack) | 200 ms | 500 ms | Verify + inbox insert + job insert |
| Webhook received → payment state applied (`fundzim_webhook_processing_lag_seconds`) | 5 s | 30 s | Queue `webhooks` |
| Ledger posting transaction (T1+T2) inside the apply job | 50 ms | 150 ms | Includes deferred trigger |
| Staff list endpoints (payout queue, review queue) | 500 ms | 1 500 ms | |
| `/readyz` | 50 ms | 200 ms | Cached 2 s |

Web performance budgets (LCP, JS size) are in [FRONTEND.md](../FRONTEND.md).

## 5. Per-tier bottlenecks and design response

| Tier | First expected bottleneck | Design response (adopt when trigger is met) | Trigger (measured) |
|---|---|---|---|
| **1k** | None technical. The risk is operational: low volume hides bugs. | 1 api + 1 worker + 1 Postgres (managed, 2 vCPU/4–8 GB). No Redis needed (`REDIS_REQUIRED=false`). Invariant jobs nightly. | — |
| **10k** | A viral campaign: ~143 concurrent checkouts and ~17 page views/s | CDN caching of public campaign pages and images (§10). The confirmation-page poll endpoint is cheap (indexed PK read). Same single instances, with 2 api replicas for availability. | p95 of the public campaign API > budget, or origin request rate > 20/s |
| **100k** | (a) Public read traffic at ~83 views/s bursts; (b) DB connections with 2–3 api and 2 worker replicas (~130 connections); (c) provider poll rate ~18/s against provider rate limits | (a) CDN + Next.js cached components with short `cacheLife` (§10); campaign summary cache in Redis (optional). (b) Pool sizing per §7; raise `max_connections` to 200 or introduce PgBouncer. (c) Per-provider poll rate limiter and **first-poll delay per provider capability** (start at 2 min where webhooks are reliable). | Origin p95 breach; pool acquire wait p95 > 50 ms; provider 429s |
| **1M** | (a) Hot ledger projection rows: viral campaign `campaign_unsettled` ≈ 33 updates/s and global `revenue:platform_fees`/`psp_clearing:{p}` ≈ 17–35/s; (b) webhook storms ≈ 420/s; (c) reporting queries competing with OLTP; (d) table maintenance on 50 M-row/yr tables | (a) §6 options, in order. (b) Ingest is cheap and the queue absorbs the rest: scale `webhooks` workers and api replicas. (c) Read replica for reporting and the `fundzim_readonly` role (§11). (d) Monthly partitioning of append-only history tables (§14). | §6 thresholds; webhook ack p95 > 200 ms; replica need when reporting queries > 5 % of primary CPU; partition when a table > 100 M rows or > 50 GB, or autovacuum on it > 1 h |

**Horizontal scaling unit:** api and worker are stateless (sessions and jobs are in Postgres), so both scale
by replica count. The database primary is the single scaling constraint. The design keeps the primary for
**writes and financial decisions only**. Reads that tolerate staleness go to CDN/cache/replica, and financial
decisions **never** read a replica or cache (ARCHITECTURE §11).

## 6. Hot campaign contention and ledger posting cost

### 6.1 Where the hot rows are

Each captured donation posts T1 (Dr `psp_clearing:{p}`, Cr `campaign_unsettled:{c}`, Cr `revenue:platform_fees`)
and usually T2 (Dr `campaign_unsettled:{c}`, Cr `psp_clearing:{p}`). With a synchronously maintained
`ledger.ledger_balances` projection, every donation row-locks:

| Projection row | Scope | Contention source |
|---|---|---|
| `liability:campaign_unsettled:{c}` (per currency) | One campaign | **Viral campaign** |
| `asset:psp_clearing:{p}` (per currency) | Platform-wide per provider | **All donations to that provider** |
| `revenue:platform_fees` (per currency) | Platform-wide | **All donations** |

The two global rows are the **real** hotspot at scale. They receive every donation, viral or not. LEDGER §9
only discusses the campaign row. This is a finding of this document.

### 6.2 Capacity of one hot row

A row lock is held from the projection `UPDATE` to `COMMIT`. With the projection updates issued as the **last
statements before COMMIT** (lock order: ascending `account_id`, LEDGER §9), the hold time is roughly the
deferred balance trigger plus commit fsync, about **3–8 ms** (assumption A-L1, to be measured in Stage 10/19).
Serialised throughput per row is then ≈ 1 / 0.008 s ≈ **125/s** (pessimistic) to ≈ 330/s (optimistic).

Utilisation at the 1M tier burst: campaign row ρ ≈ 33 × 0.008 ≈ **0.26**; global rows ρ ≈ 35 × 0.008 ≈
**0.28**. Queueing delay at ρ ≈ 0.3 is small (an M/D/1 mean wait of ≈ ρ/(2(1−ρ)) × service ≈ 2 ms). **Option 1
is therefore sufficient for every tier in these scenarios.** The risk is that assumption A-L1 is wrong, for
example if a slow trigger or a provider call is mistakenly placed inside the transaction.

### 6.3 Options (LEDGER §9) and recommendation

| Option | What changes | Cost | Invariants |
|---|---|---|---|
| **1. Short transactions** (default) | Posting tx contains only: lock intent row → insert journal + entries → update projections (last, ascending account id) → events/audit/outbox → COMMIT. No provider calls (BP-3), no reads of unrelated tables after the projection update. | None | Unchanged |
| **2. Batched projection for high-fan-in accounts** | Entries commit synchronously as now. For accounts flagged `projection_mode = BATCHED`, the projection is **not** updated in the posting tx. A `ledger.projection_apply` job (single consumer per account, unique per account) folds new entries (`id > last_entry_id`) into the projection every ≤ 1 s. | Small staleness of the projection for those accounts only | L1–L7 unchanged (entries are authoritative). L10 verification compares against entries up to `last_entry_id`. **Only allowed for accounts never used in a sufficiency check** (no L8 decision reads them): `psp_clearing`, `psp_settled`, `revenue:*`, `expense:*`, `suspense:*`. **Never** for `campaign_payable`, `campaign_payout_pending` or `campaign_held`. |
| **3. Sub-accounts** | `campaign_unsettled:{c}:{k}` for k ∈ 0..N−1, chosen by hash of `payment_id`. Reads sum the N rows. | More rows, summed reads, more complex release logic | Unchanged. Releases and payout sufficiency read the sum under locks on all N rows (in order). |

**Recommendation:**

1. Stage 10 ships **Option 1** for all accounts, with projection-update-last ordering and the
   `fundzim_ledger_projection_lock_wait_seconds{account_class}` metric.
2. Stage 10 includes the `projection_mode` column (default `SYNC`) in `ledger_accounts` design, so Option 2
   needs no schema change. **Proposal for [ledger-posting-model.md](ledger-posting-model.md) / ADR-023.**
3. Adopt **Option 2 for global system accounts** (`revenue:platform_fees`, `psp_clearing:{p}`) when
   lock-wait p99 on that account class > **20 ms** for 15 min, or sustained > **50 postings/s** on one row in a
   load test. This is likely the first contention fix needed, before any campaign-level change.
4. Adopt **Option 3 for a campaign** only if lock-wait p99 on `campaign_unsettled` > **50 ms** during a real or
   rehearsed viral event, or a single campaign's sustained rate exceeds **100 donations/s**, which is far beyond
   the 1M scenario (33/s). `campaign_payable` is never split without an ADR, because it carries the L8
   decision.

### 6.4 Deferred trigger cost

The illustrative L1/L2 trigger (LEDGER §4.1) is a **per-row** deferred constraint trigger that re-aggregates the
whole transaction for every entry. That is O(n²) per journal with n = entries. With n = 2–4 for donation
journals it costs a few index lookups and is negligible. It becomes material for **bulk journals**
(settlement batches with hundreds of lines, `ledger_posting_batches`), where it would be O(n²). The design
response, to be decided in [ledger-posting-model.md](ledger-posting-model.md): check **once per transaction**
(a deferred trigger on `ledger_transactions` insert, or a statement-level `AFTER INSERT … REFERENCING NEW TABLE`
trigger that checks only the distinct `transaction_id`s in the statement), with the same guarantee and the tests
in LEDGER §4.1. Settlement postings should be **one journal per settled payment** (as the idempotency keys
`settlement:{batch}:{payment}` already imply), not one giant journal, which keeps n small anyway.

### 6.5 Displayed raised totals under viral load

The public "raised" figure is derived from ledger data (LEDGER §8). Under viral load:

- the public figure comes from a **campaign summary read model** (projection fed by the
  `payment.succeeded` outbox event, owned by `campaigns`). It is cached at the CDN/Next.js layer for ≤ 30 s and
  labelled "updated hh:mm" ([FRONTEND.md](../FRONTEND.md)). **It is display only. It never feeds a decision.**
- payout sufficiency, release and refund decisions read `ledger_balances` (SYNC accounts) under row locks on
  the primary, never the cached figure.

## 7. Database connections and pooling

### 7.1 Per-process pools (pgx v5 `pgxpool`) [default]

| Process | `fundzim_app` / `fundzim_worker` | `fundzim_kyc` | `fundzim_compliance` | `fundzim_readonly` | Session-mode conns |
|---|---|---|---|---|---|
| api | 20 `fundzim_app` (`DATABASE_MAX_CONNS`) | 5 | 5 | — | — |
| worker | 25 `fundzim_worker` (`WORKER_DB_MAX_CONNS`, baseline §12 I-15) | 3 | 3 | 5 (reports) | 1 (River LISTEN notifier) |
| fundzimctl / migrations | 1 (`fundzim_migrator`) | — | — | — | — |

Pool settings: `MinConns 2`, `MaxConnLifetime 30 min` (+ jitter), `MaxConnIdleTime 5 min`,
`HealthCheckPeriod 30 s`. Each connection sets role-level defaults (`ALTER ROLE … SET`), so they survive any
pooler: `statement_timeout` (`DATABASE_STATEMENT_TIMEOUT` = 15 s for app; 5 min for readonly),
`lock_timeout` 3 s, `idle_in_transaction_session_timeout` 30 s. Financial transactions additionally use
`SET LOCAL lock_timeout`.

### 7.2 Totals per tier

| Tier | Replicas (api / worker) | Connections ≈ (api 30 each, worker 37 each, + 10 ops) | Recommendation |
|---|---|---|---|
| 1k–10k | 1–2 / 1 | 77–107 | Direct connections, `max_connections` 150 |
| 100k | 3 / 2 | 174 | Direct, `max_connections` 250 (memory permitting), **or** PgBouncer |
| 1M | 4–6 / 3 | 241–301 | **PgBouncer in transaction mode** for the api `app` pools. Worker and kyc/compliance pools stay direct (session) or use a session-mode pool. |

### 7.3 PgBouncer transaction-mode caveats

| Concern | Effect in transaction mode | Design rule |
|---|---|---|
| Prepared statements (pgx caches statements by default) | Statements prepared on one server connection are missing on another | Use PgBouncer ≥ 1.21 with `max_prepared_statements` > 0 (protocol-level prepared statement support), **or** set pgx `DefaultQueryExecMode = QueryExecModeCacheDescribe` / `Exec` for pools behind PgBouncer. Decide in Stage 18 with a test. |
| `LISTEN/NOTIFY` (River's notifier, any outbox kick by NOTIFY) | LISTEN does not survive across transactions | Workers connect **directly** (or via session-mode pool), or River runs in `PollOnly` mode with `FetchPollInterval` 1 s. The api only inserts jobs, which works in transaction mode. |
| Session `SET` (search_path, timeouts, RLS session setting for `fundzim_compliance`) | Leaks or is lost across clients | Only `SET LOCAL` inside transactions. Role-level `ALTER ROLE … SET` for defaults. The compliance RLS setting must be `SET LOCAL` (ADR-022 dependency). |
| Session advisory locks | Not held reliably | Forbidden. Use transaction-scoped `pg_advisory_xact_lock` only, and row locks. |
| River leader election and maintenance | Requires stable sessions for some operations | Worker pools are never behind transaction-mode pooling |

## 8. Key indexes per hot table (starting points)

The schema documents ([schema-overview.md](../database/schema-overview.md), [payment-schema.md](../database/payment-schema.md),
[ledger-schema.md](../database/ledger-schema.md), [payout-schema.md](../database/payout-schema.md),
[reconciliation-schema.md](../database/reconciliation-schema.md)) are authoritative for DDL. This table lists the
**access paths** the load scenarios rely on. Every index must be justified by one of these queries (DATABASE §11:
no speculative indexes).

| Table | Index | Serves |
|---|---|---|
| `app.campaigns` | `uq_campaigns_public_code`; `ix_campaigns_listing (status, visibility, published_at DESC, id DESC) WHERE status='ACTIVE' AND visibility='PUBLIC'` | Public page by code; discovery listing with cursor |
| `app.donations` | `ix_donations_campaign_public (campaign_id, created_at DESC, id DESC) WHERE <succeeded and not anonymous-hidden>` | Recent donors list on the campaign page |
| `app.payment_intents` | `pk`; `ix_payment_intents_campaign (campaign_id, created_at DESC)`; `ix_payment_intents_poll (next_poll_at) WHERE status IN ('PENDING','REQUIRES_ACTION','AUTHORISED','UNKNOWN')`; `ix_payment_intents_needs_recon (provider, created_at) WHERE needs_reconciliation` | Owner views; sweepers; reconciliation queue |
| `app.payment_provider_references` | `uq_payment_provider_references (provider, reference_kind, reference)` | Webhook/poll lookup |
| `app.payment_events` | `ix_payment_events_intent (payment_intent_id, recorded_at)` | History |
| `app.payment_attempts` | `ix_payment_attempts_intent (payment_intent_id, attempt_no DESC)` | Crash rule (background-processing §9.1) |
| `app.provider_webhook_inbox` | `uq_provider_webhook_inbox_event (provider_id, provider_event_id)`; `ix_provider_webhook_inbox_work (next_attempt_at, received_at) WHERE status IN ('RECEIVED','FAILED_RETRYING')`; `ix_provider_webhook_inbox_parked` | Dedupe; backlog age metric; parked re-dispatch |
| `app.outbox_events` | `ix_outbox_events_undispatched (available_at, id) WHERE dispatched_at IS NULL AND dead_lettered_at IS NULL` | Dispatcher |
| `app.inbox_events` | `uq_inbox_events_consumer_event_id (consumer, event_id)` | Consumer dedupe |
| `app.idempotency_keys` | `uq_idempotency_keys_scope_key (scope, key)`; `ix_idempotency_keys_expires_at` | Request path; expiry job |
| `ledger.ledger_transactions` | `uq_ledger_transactions_idempotency_key`; `uq_…_reverses (reverses_transaction_id) WHERE NOT NULL`; `ix_ledger_transactions_source (source_type, source_id)` | L6/L7; lookup by payment/payout |
| `ledger.ledger_entries` | `ix_ledger_entries_account (account_id, id)`; `ix_ledger_entries_transaction (transaction_id)` | Statements and recompute (L10); balance trigger |
| `ledger.ledger_balances` | `pk (account_id)` | Locked projection reads |
| `app.payout_requests` | `uq_payout_requests_one_inflight (campaign_id, currency) WHERE status IN (non-terminal)`; `ix_payout_requests_queue (status, created_at) WHERE status IN ('PENDING_REVIEW','APPROVED')`; `ix_payout_requests_poll (next_poll_at) WHERE status IN ('SUBMITTED','PROCESSING','UNKNOWN')` | Duplicate prevention; staff queue; sweepers |
| `recon.reconciliation_items` | `uq_reconciliation_items (import_id, line_no)`; `ix_reconciliation_items_ref (source_id, provider_reference)` | Idempotent import; matching |
| `audit.audit_events` | `ix_audit_events_subject (subject_type, subject_id, occurred_at)`; `ix_audit_events_actor (actor_id, occurred_at)` | Investigation; per-staff KYC access alert |
| `app.sessions` | `ix_sessions_user (user_id)`; `ix_sessions_expires (expires_at)` | Revocation; pruning |
| `queue.river_job` | Library-managed | — |

Indexes on populated tables are created `CONCURRENTLY` in `-- +goose NO TRANSACTION` migrations
([migration-strategy.md](../database/migration-strategy.md)).

## 9. Pagination

Cursor pagination only (ARCHITECTURE §6): the cursor encodes the last row's `(sort_key, id)` (UUIDv7 ids are
time-ordered, so `(created_at, id)` or simply `id` is a stable tiebreaker). Queries use the row-value
comparison `WHERE (created_at, id) < ($1, $2) ORDER BY created_at DESC, id DESC LIMIT $n+1`, served by a
matching index. Cursors are opaque (base64url of a versioned, **HMAC-signed** struct) so clients cannot craft
arbitrary scans. `limit` defaults to 20 with a maximum of 100. There is no `COUNT(*)` for totals on large
tables: the API returns `has_more` only. Detailed rules are in [docs/api/](../api/).

## 10. Caching

| What | Where | TTL [default] | Invalidation | Rule |
|---|---|---|---|---|
| Public campaign page HTML/RSC | CDN + Next.js cached components (`'use cache'` + `cacheLife`, tag `campaign:{id}`; Next 16 `cacheComponents` is enabled in `apps/web/next.config.ts`) | 60 s, stale-while-revalidate 5 min | `revalidateTag('campaign:{id}')` on campaign content/status change (outbox consumer → internal revalidate endpoint, Stage 7) | Read the Next 16 docs in `apps/web/node_modules/next/dist/docs/` before implementing |
| Displayed raised total / donor count | Inside the cached page + summary API | ≤ 30 s | Time-based | Labelled "updated hh:mm". **Display only.** |
| Campaign summary API (`GET /api/v1/campaigns/{id}`) | Redis (optional) or in-process LRU | 15 s | Time-based | Cache miss → Postgres. Redis loss is harmless. |
| Images (variants) | CDN from `public-media` | 1 year, immutable (content-addressed key) | New key on change | |
| Reference data (currencies, categories, fee schedule **for display**) | In-process | 5 min | Restart / time | Fee **calculation** for posting reads the versioned schedule from the DB in-tx |
| **Never cached** | — | — | — | Ledger balances used for decisions, payment/payout status for decisions, idempotency records, session validity (sessions are checked in Postgres; a short in-process positive cache ≤ 5 s is allowed, but revocation must take effect within 5 s), authz decisions, KYC anything |

The donation **status** endpoint (`GET /api/v1/donations/{id}`) is not cached at the CDN. It is a cheap PK
read on the primary. The UI never shows "confirmed" from a cached response (Stage 7 acceptance).

## 11. Read/write patterns and reporting

| Pattern | Volume share | Path |
|---|---|---|
| Public campaign reads | Dominant (≈ 95 % of requests in a viral burst) | CDN → Next.js cache → API → primary (replica at 1M for non-financial public reads, with ≤ 5 s lag tolerance) |
| Donation create | Low rate, latency-sensitive | Primary |
| Webhook ingest + apply | Bursty | Primary (short txs) |
| Staff queues | Low | Primary |
| Reporting (trial balance, campaign statements, recon reports, exports) | Heavy scans | `fundzim_readonly` role on **approved views** (DATABASE §8), on a read replica from the 100k–1M tier. `statement_timeout` 5 min. Invariant verification stays on the primary snapshot until replica lag is recorded with each run. |

## 12. Webhook throughput

The ingest path does about 3 statements in one short tx (inbox insert `ON CONFLICT DO NOTHING`, River job
insert, commit) plus signature verification (HMAC: microseconds; RSA verify: < 1 ms). The **assumed** cost is
≈ 5–15 ms of DB time per webhook. One api replica with a 20-connection pool can therefore sustain on the order
of 20 / 0.015 ≈ 1 300 inserts/s before pool saturation, which is ×3 above the 1M-tier retry storm (≈ 420/s).
This must be verified by the k6 `webhook_burst` scenario. Processing drains through the `webhooks` queue:
8 workers × (1 / ~50 ms) ≈ 160 applies/s per worker replica, so a 15-minute storm of 420/s (≈ 380 k events)
drains in about 40 minutes with one worker replica, or 13 minutes with three. That is acceptable, because
payment state is not lost meanwhile (inbox rows are durable) and polling covers urgency. Alert ALR-W01 fires
if the backlog age exceeds 2 minutes.

## 13. Media delivery

- Campaign images are re-encoded into fixed variants (proposal: 320, 640, 1 280 px wide, WebP plus JPEG
  fallback), with keys content-addressed `{object_id}/{variant}-{hash}.webp` and immutable caching. OG preview
  variant 1 200×630 for WhatsApp.
- Served only from the CDN in front of `public-media`. The API never streams public images.
- Upload/processing load: `media` queue, 2 workers per replica. Image decode memory is bounded (decompression
  bomb guard: max pixels 40 MP [default]).
- KYC documents are never on the CDN. Staff view them through ≤ 5 min presigned GETs (ADR-009).

## 14. Partitioning outlook

| Table | Partition key | When | Caveats |
|---|---|---|---|
| `audit.audit_events`, `audit.security_audit_events` | `RANGE (occurred_at)` monthly | > 100 M rows or retention needs (LR-012) | The PK must include `occurred_at`. The hash chain spans partitions (chain by sequence, not by partition). Append-only triggers per partition. Detaching or dropping partitions only under an approved retention policy and legal-hold check. |
| `ledger.ledger_entries` | `RANGE (posted_at)` monthly | > 100 M rows (≈ year 2 at the 1M tier) | The PK becomes `(id, posted_at)`. FKs **to** entries are not allowed (none are planned). The L1 trigger is unaffected. **`ledger_transactions` is not partitioned**: `UNIQUE(idempotency_key)` must stay global (L6), and a partitioned table can only enforce uniqueness that includes the partition key. |
| `app.payment_events`, `app.payout_events` | `RANGE (recorded_at)` monthly | > 100 M rows | Same PK caveat. These are history tables with no inbound FKs. |
| `app.provider_webhook_inbox` | `RANGE (received_at)` monthly | > 50 M rows | `UNIQUE(provider_id, provider_event_id)` would need to include `received_at`, which **breaks dedupe across months**. Keep dedupe in a slim non-partitioned `(provider, provider_event_id)` table, or do not partition. Decide only when needed. |
| `app.security_events` | `RANGE (occurred_at)` monthly | Early (high volume, shorter retention) | Retention via partition drop once LR-012 sets the period |

Partitioning is introduced through expand/contract migrations ([migration-strategy.md](../database/migration-strategy.md))
and needs an ADR when it touches `ledger` or `audit`.

## 15. Load-test scenarios (handed to test-architecture)

| Scenario | Shape | Pass criteria |
|---|---|---|
| `campaign_page_viral` | Ramp to the tier's burst page-view rate (§3.2) for 10 min via the Next.js SSR path, cache warm and cold | §4 budgets; origin rate within the CDN offload plan |
| `donation_spike_one_campaign` | Viral initiations/s on one campaign against the sandbox provider with realistic delays | Donation create p95; zero invariant failures; projection lock wait p99 < 20 ms |
| `webhook_burst` | 10× steady + viral rate for 15 min, 5 % duplicates, 2 % out of order | Ack p95 < 200 ms; backlog drains; exactly-once effects; invariants pass |
| `payout_queue_staff` | Staff listing under 10k pending items | p95 < 500 ms |
| `soak_24h` | Steady-state tier mix | No leak or growth in pool wait; job backlog flat |

Details and the CI cadence: [test-architecture.md §3.12](../testing/test-architecture.md).

## 16. Concerns

| # | Concern | Handling |
|---|---|---|
| PCC-1 | Global hot rows (`revenue:platform_fees`, `psp_clearing:{p}`) are not covered by LEDGER §9 | Recommendation §6.3, with a proposal of `projection_mode` for ADR-023 / [ledger-posting-model.md](ledger-posting-model.md) |
| PCC-2 | Per-row O(n²) balance trigger for bulk journals | §6.4. Decide the trigger form in [ledger-posting-model.md](ledger-posting-model.md). |
| PCC-3 | Partitioning `provider_webhook_inbox` conflicts with global dedupe | §14. Do not partition without a dedupe side table. |
| PCC-4 | Provider rate limits on status polling are unknown | PCR items. Per-provider poll rate limiter and first-poll delay are configuration. |
| PCC-5 | Every number here is an assumption | Stage 19 must replace §2–§3 with measured values, and Stage 20 capacity limits must cite measurements |
