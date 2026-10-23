# Stage 19 — Load, contention and endurance test plan

## 1. Environment (recorded with every result)

Local development host: 16 vCPU Intel Xeon Platinum 8160, 30 GiB RAM, SSD-backed root volume, Ubuntu 25.10, Docker.
Compose PostgreSQL 17.11 (1 GiB memory limit, default settings, `max_connections` 100), API in-process in the test
binary (app pool `DATABASE_MAX_CONNS`, ledger pool 8), compose API and worker running alongside on the same database
(worker pool 10, `WORKER_CONCURRENCY` 4). Providers: the sandbox simulator only (no network provider). These numbers
describe this host and this configuration; they are **not production capacity**, and no service-level objective
exists to compare them with (none is approved; none is invented).

## 2. Tiers

| Tier | What | Harness | Data |
|---|---|---|---|
| A — baseline | one worker of the mixed workload | `TestFinancialLoad` (c = 1) | local stack, new TEST records |
| B — concurrent financial activity | 4 and 8 workers: payout lifecycles, donations, ledger postings, reconciliation and integrity runs in parallel | `TestFinancialLoad` (c = 4, 8) | same |
| C — record volume | 1 000, 10 000 and 100 000 journals, with posting latency at 1/8/16 writers, reads, trial balance, integrity and reconciliation at each size, before and after the Stage 19 lock-order change | `tests/perf/ledger` (`TestLedgerPerf`, `FUNDZIM_PERF_SCALES=1000,10000,100000`) | scratch databases `fundzim_perf_s19a` (before, code at `801babe`) and `fundzim_perf_s19b` (after) — the development ledger is append-only and is never loaded |
| D — contention and burst | 16 workers; hot-campaign opposite-direction postings (200 at 16-way concurrency, one campaign) | `TestFinancialLoad` (c = 16), `TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock` | local stack |
| E — endurance | the mixed workload at the highest tier for a bounded period, sampled every second | `TestFinancialLoad` with `FUNDZIM_IT_S19_ENDURANCE` | local stack |

## 3. Workload (per worker, repeated until the tier's duration ends)

1. Ledger: one `DONATION_CAPTURED` capture to a fresh synthetic owner, then a `CAMPAIGN_FROZEN` and a
   `CAMPAIGN_UNFROZEN` journal on the worker's campaign (the opposite-order pattern).
2. A donation through the simulator (intent and attempt through the payments service; success webhook after 1 s,
   delivered and applied by the stack).
3. A full payout lifecycle on the worker's campaign: request (eligibility, reservation), two FINANCE approvals,
   submission (pre-submission re-check, in-transit journal, provider instruction), simulator completion (webhook or
   poll), completion journal and reservation consumption.

In parallel: a payout reconciliation run every 10 s and a ledger integrity run every 30 s. Every simulated provider
event is a sandbox fixture; every journal is TEST_ONLY; nothing is real money.

## 4. Metrics

Per tier: payout lifecycle, donation-creation and posting-round latencies (p50/p95/p99) and counts, errors,
reconciliation and integrity run counts and maximum durations, database-wide deadlocks, rollbacks and commits
(`pg_stat_database` delta), and per-second samples of lock waiters, active and idle-in-transaction sessions,
undispatched outbox events (relay backlog) and ready River jobs (consumer backlog). Optional container memory before and after
(`FUNDZIM_IT_S19_STATS_CMD`). After the run, over every record the run created: payouts with more than one accepted
CREATE_PAYOUT, payouts consumed more than once, COMPLETED payouts not CONSUMED, payments SUCCEEDED more than once,
duplicate success notices, payout reconciliation references, and a final ledger integrity run. Any non-zero
duplicate count fails the test.

## 5. Not measured (and why)

- Production capacity, user counts, devices or transactions per second: no production environment, no approved
  target.
- Provider latency and rate limits: no provider is selected.
- Multi-node behaviour, failover, replication lag: single node only (Stage 18 deployment architecture is a proposal).
- 10⁶-record payment and payout tables: building them through the real domain model (eligibility, approvals,
  simulator) takes far longer than this stage allows; generating rows that bypass the domain model is refused by the
  brief (§13.3). Ledger volume to 10⁵ journals (and 10⁶ in Stage 10) is measured instead.
