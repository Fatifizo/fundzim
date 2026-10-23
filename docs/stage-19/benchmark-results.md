# Stage 19 — Benchmark and load results

Environment: load-test-plan.md §1 (one development host, compose PostgreSQL 17.11 with a 1 GiB limit, simulators).
Every number was measured in this session; none is extrapolated. These are baselines for this host, **not production
capacity**; no capacity target or SLO exists to compare them with. Single runs unless stated; run-to-run variation was
not established.

## 1. Tier C — ledger record volume, before and after the lock-order change (F-S19-01)

`TestLedgerPerf` (`tests/perf/ledger`), scales 1 000 / 10 000 / 100 000 journals, pool 16. "Before" = code at
`801babe` on scratch database `fundzim_perf_s19a`; "after" = this branch on `fundzim_perf_s19b`; both freshly migrated,
run one after the other. Full reports: scratchpad `s19/perf-a/ledger-perf.md`, `s19/perf-b/ledger-perf.md`.

| Scale (journals in table) | Writers | Before p50 / p95 ms | Before journals/s | After p50 / p95 ms | After journals/s |
|---|---:|---|---:|---|---:|
| 2 000 | 1 | 14.1 / 21.2 | 65 | 14.7 / 21.7 | 64 |
| 2 000 | 8 | 43.6 / 53.4 | 181 | 48.1 / 59.1 | 164 |
| 2 000 | 16 | 86.2 / 101.7 | 182 | 96.2 / 112.4 | 164 |
| 16 100 | 1 | 14.3 / 20.9 | 65 | 15.6 / 21.7 | 61 |
| 16 100 | 8 | 45.0 / 57.6 | 174 | 46.7 / 55.1 | 170 |
| 16 100 | 16 | 92.8 / 106.8 | 171 | 95.5 / 108.5 | 166 |
| 111 200 | 1 | 15.0 / 21.1 | 63 | 16.0 / 22.4 | 59 |
| 111 200 | 8 | 46.6 / 56.5 | 170 | 51.0 / 60.4 | 155 |
| 111 200 | 16 | 95.3 / 111.1 | 167 | 100.9 / 116.0 | 158 |

| Scale | Integrity checker (L1–L15) before / after | Trial balance p50 before / after | Bulk load before / after |
|---|---|---|---|
| 2 000 | 115 / 186 ms | 56 / 29 ms | 710 / 536 journals/s |
| 16 100 | 304 / 326 ms | 131 / 139 ms | 752 / 522 journals/s |
| 111 200 | 1 707 / 1 831 ms | 329 / 275 ms | 533 / 520 journals/s |

Reading: posting throughput saturates at ~155–182 journals/s from 8 writers on, independent of table size (the global
audit chain lock serialises writers — KI-S19-01). The lock-order change costs 3–9 % throughput at 8–16 writers and
1–5 ms p50 in this single comparison; it removes the deadlocks of §3. (The 2 000-journal "page after the 10 000th
journal" row of the harness reports 300 errors in both runs because that page does not exist yet: a harness artefact.)

## 2. Tiers A, B, D, E — mixed financial workload (`TestFinancialLoad`), first run (before F-S19-10 and F-S19-12)

Started 2026-10-23 19:25:32 UTC; 3-minute tiers, then a 30-minute endurance run at 16 workers; in-process API with
`DATABASE_MAX_CONNS` 20, compose worker alongside; report: scratchpad `s19/load/stage19-financial-load.md`.

| Tier | c | Duration | Payout lifecycles (n / err) | Payout p50 / p95 / p99 s | Donations (n / err) | Donation p50 / p95 / p99 ms | Posting rounds (n / err) | Posting p50 / p95 / p99 ms | Recon runs (max s) | Integrity runs (max s) | Deadlocks (DB-wide) | Max lock waiters | Max River ready |
|---|---:|---|---|---|---|---|---|---|---|---|---:|---:|---:|
| A | 1 | 3 min | 49 / 0 | 3.09 / 3.91 / 4.16 | 49 / 0 | 459 / 492 / 587 | 49 / 0 | 50 / 61 / 113 | 11 (6.7) | 3 (0.19) | 0 | 0 | 9 |
| B | 4 | 3 min | 184 / 0 | 3.49 / 3.97 / 4.13 | 184 / 0 | 487 / 635 / 719 | 184 / 0 | 52 / 79 / 240 | 11 (6.9) | 3 (0.29) | 0 | 0 | 42 |
| B | 8 | 3 min | 338 / 0 | 3.58 / 4.32 / 4.69 | 338 / 0 | 528 / 959 / 1 041 | 338 / 0 | 50 / 79 / 376 | 10 (8.3) | 3 (0.36) | 0 | 0 | 67 |
| D | 16 | 3 min | 450 / 0 | 5.49 / 7.02 / 8.23 | 450 / 0 | 686 / 1 455 / 1 685 | 450 / 0 | 57 / 133 / 544 | 9 (11.3) | 3 (0.53) | 4 | 0 | 732 |
| E | 16 | 30 min | 4 601 / 0 | 5.39 / 6.30 / 10.32 | 4 601 / 0 | 662 / 1 115 / 1 383 | 4 601 / 0 | 56 / 85 / 115 | 69 (18.8) | 23 (1.40) | 98 | 0 | 7 737 |

A posting round = one capture plus a freeze/unfreeze pair (3 journals). Deadlocks, rollbacks and commits are
database-wide (`pg_stat_database`; the compose worker's work included); lock waiters, active and idle-in-transaction
sessions sampled every second (max active 1, max idle-in-transaction 0 in every tier). Undispatched outbox events peaked
at 157–204 per tier (the relay keeps up).

**Correctness over all 5 622 payouts and 5 622 donations of the run:** payouts with more than one accepted
CREATE_PAYOUT 0; payouts consumed more than once 0; COMPLETED payouts not CONSUMED 0; payments SUCCEEDED more than once
0; duplicate success notices 0; payout reconciliation references 0; ledger integrity after the run passed.

**Endurance observations (30 min, 16 workers):** no workload error; payout and donation latencies flat from the 3-minute
c = 16 tier to the 30-minute run (p50 5.49 → 5.39 s; 686 → 662 ms) — no gradual degradation; container memory: compose
API 12.1 → 14.4 MiB, worker 32.4 → 33.4 MiB, PostgreSQL 656 → 780 MiB (page cache within its 1 GiB limit); payout
reconciliation run time grew with the table (6.7 → 18.8 s max) — KI-S19-09; River backlog: the *notifications* queue
(`outbox.deliver` avg 174 s, max 390 s; KI-S19-13) while the financial default queue stayed at avg 2.0 s (max 9.1 s);
**98 deadlocks**, all `payout_attempts` insert vs payout-row lock (PostgreSQL log) — F-S19-12, retried by `WithTx`,
none surfaced as a workload error.

## 3. Contention and burst (tier D)

| Scenario | Before | After |
|---|---|---|
| 200 opposite-direction postings on one campaign, 16 concurrent (`TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock`) | 86 posted, 5 deadlocks, 109 lock timeouts, 290 s | 200 posted, 0, 0, 22.8 s |
| 64 concurrent status queries of one payout (`TestFinancialPayoutConcurrentStatusQueries`) | 21 ok, 43 failed (39 duplicate attempt numbers, 2 deadlocks, 2 lock timeouts), 68 DB deadlocks, 1 stranded IN_FLIGHT | 64 ok, 0 failed, 0 deadlocks |

## 4. Provider health derivation (F-S19-10), 1.27 M `provider_health_events` rows

`EXPLAIN ANALYZE` on the development database, before and after migration `20261024090100`:

| Query (psp) | Before | After |
|---|---|---|
| health window (`ProviderHealth`) | 93.1 ms, parallel seq scan | 2.1 ms, `ix_provider_health_events_window` |
| latest circuit event per operation and currency (`Circuit`) | 79.3 ms, seq scan | 0.07 ms, `ix_provider_health_events_circuit` |
| distinct currencies per operation (`ProviderCircuit`) | 198.2 ms, seq scan (all call events) | 0.04 ms (circuit events only, same result) |
| latest outage (`outageOpen`) | seq scan + sort (not timed) | 0.06 ms, `ix_provider_health_events_outage` |

Per payout eligibility check that is ~0.6 s → ~2 ms of health derivation, twice per payout (request and pre-submit).

## 5. Second mixed-workload run (after F-S19-10 and F-S19-12)

Started 2026-10-23 20:16:33 UTC, same harness, tiers c = 8 and 16 (3 min) and a 30-minute endurance run at 16 workers,
compose API and worker rebuilt from the branch; report: scratchpad `s19/load2/stage19-financial-load.md`.

| Tier | c | Duration | Payout lifecycles (n / err) | Payout p50 / p95 / p99 s | Donations (n / err) | Donation p50 / p95 / p99 ms | Posting rounds (n / err) | Posting p50 / p95 / p99 ms | Recon runs (max s) | Integrity runs (max s) | Deadlocks (DB-wide) | Max lock waiters | Max River ready |
|---|---:|---|---|---|---|---|---|---|---|---|---:|---:|---:|
| B | 8 | 3 min | 438 / 0 | 3.07 / 3.73 / 4.07 | 438 / 0 | 85 / 127 / 305 | 438 / 0 | 107 / 181 / 560 | 7 (19.0) | 2 (1.11) | 0 | 0 | 592 |
| D | 16 | 3 min | 536 / 0 | 5.39 / 5.57 / 5.71 | 536 / 0 | 69 / 198 / 350 | 536 / 0 | 53 / 175 / 475 | 7 (16.6) | 2 (1.26) | 0 | 0 | 1 977 |
| E | 16 | 30 min | 5 167 / 0 | 5.43 / 5.64 / 5.83 | 5 167 / 0 | 66 / 125 / 186 | 5 167 / 0 | 52 / 119 / 170 | 66 (18.7) | 22 (2.79) | **0** | 0 | 15 025 |

**Correctness over all 6 141 payouts and 6 141 donations:** every duplicate counter 0, no reconciliation reference,
ledger integrity passed.

Comparison with §2 (same harness, same host): deadlocks 4 / 98 → **0 / 0** (c = 16 / endurance); payout lifecycles per
3-minute tier +30 % at c = 8 (338 → 438) and +19 % at c = 16 (450 → 536), endurance +12 % (4 601 → 5 167); payout p99
at endurance 10.32 → 5.83 s; donation creation p50 662 → 66 ms (donation routing derives provider health too, F-S19-10).
The reconciliation-run maximum is ~17–19 s in both runs (KI-S19-09).

**Capacity limit observed:** the compose worker (`WORKER_CONCURRENCY` 4) is the bottleneck at 16 sustained workers.
During the endurance run the notifications queue backlog grew to ~15 000 ready jobs (`outbox.deliver` avg 263 s, max
546 s from creation to completion; 13 561 still waiting when the run ended), and the financial default queue's average
job latency rose to 8.5 s (max 19.1 s) from 2.0 s in the first run, because more financial work flowed through the
same worker. No job failed and no financial effect was duplicated or lost; notification delivery was late (KI-S19-13).
Container memory: worker 33 → 32 MiB, PostgreSQL 534 → 1 007 MiB (page cache at its 1 GiB limit; 628 MiB a few minutes
later).
