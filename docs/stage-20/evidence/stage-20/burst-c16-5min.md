## Stage 19 financial load (TestFinancialLoad)

started 2026-10-25T08:48:17Z UTC; host NumCPU=16 GOMAXPROCS=16; go1.27.3; PostgreSQL 17.11 (compose, max_connections 100, 1 GiB limit); in-process API pools (DATABASE_MAX_CONNS="20"); compose worker running alongside; simulator provider; tier duration 5m0s

| tier | c | duration | payout lifecycles (n / err) | payout p50 / p95 / p99 s | donations (n / err) | donation p50 / p95 / p99 ms | posting rounds (n / err) | posting p50 / p95 / p99 ms | recon runs (max s) | integrity runs (max s) | deadlocks | rollbacks / commits | max lock waiters | max active | max idle-in-tx | max outbox undispatched | max River jobs ready |
|---|---:|---|---|---|---|---|---|---|---|---|---:|---|---:|---:|---:|---:|---:|
| B/D | 16 | 5m0s | 880 / 0 | 5.39 / 5.63 / 5.72 | 880 / 0 | 63.1 / 165.8 / 441.7 | 880 / 0 | 57.4 / 165.2 / 952.3 | 23 (4.74) | 7 (0.15) | 0 | 2266 / 755849 | 0 | 1 | 0 | 70 | 2361 |

A posting round = one capture plus one freeze/unfreeze pair (3 journals). Deadlocks, rollbacks and commits are database-wide deltas over the tier (pg_stat_database; the compose worker's own work is included). Samples every second.

Correctness over this run's 880 payouts and 880 donations: payouts with more than one accepted CREATE_PAYOUT 0; payouts consumed more than once 0; COMPLETED payouts not CONSUMED 0; payments SUCCEEDED more than once 0; duplicate success notices 0; payout reconciliation references 0; ledger integrity after the run: passed=true.
