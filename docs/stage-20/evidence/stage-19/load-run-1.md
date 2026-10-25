## Stage 19 financial load (TestFinancialLoad)

started 2026-10-23T19:25:32Z UTC; host NumCPU=16 GOMAXPROCS=16; go1.27.3; PostgreSQL 17.11 (compose, max_connections 100, 1 GiB limit); in-process API pools (DATABASE_MAX_CONNS="20"); compose worker running alongside; simulator provider; tier duration 3m0s

container memory before: `fundzim-fundzim-api-1=12.13MiB / 512MiB fundzim-fundzim-worker-1=32.43MiB / 512MiB fundzim-fundzim-web-1=167.9MiB / 768MiB fundzim-fundzim-postgres-1=655.6MiB / 1GiB fundzim-fundzim-redis-1=5.383MiB / 256MiB fundzim-fundzim-clamav-1=952.8MiB / 3GiB fundzim-fundzim-secrets-1=30.18MiB / 256MiB fundzim-fundzim-mail-1=49.76MiB / 128MiB fundzim-fundzim-storage-1=7.48MiB / 512MiB`

container memory after: `fundzim-fundzim-api-1=14.35MiB / 512MiB fundzim-fundzim-worker-1=33.42MiB / 512MiB fundzim-fundzim-web-1=168.1MiB / 768MiB fundzim-fundzim-postgres-1=779.5MiB / 1GiB fundzim-fundzim-redis-1=4.457MiB / 256MiB fundzim-fundzim-clamav-1=952.4MiB / 3GiB fundzim-fundzim-secrets-1=29.86MiB / 256MiB fundzim-fundzim-mail-1=31.75MiB / 128MiB fundzim-fundzim-storage-1=7.191MiB / 512MiB`

| tier | c | duration | payout lifecycles (n / err) | payout p50 / p95 / p99 s | donations (n / err) | donation p50 / p95 / p99 ms | posting rounds (n / err) | posting p50 / p95 / p99 ms | recon runs (max s) | integrity runs (max s) | deadlocks | rollbacks / commits | max lock waiters | max active | max idle-in-tx | max outbox undispatched | max River jobs ready |
|---|---:|---|---|---|---|---|---|---|---|---|---:|---|---:|---:|---:|---:|---:|
| A | 1 | 3m0s | 49 / 0 | 3.09 / 3.91 / 4.16 | 49 / 0 | 458.8 / 492.1 / 587.1 | 49 / 0 | 50.4 / 60.8 / 112.7 | 11 (6.70) | 3 (0.19) | 0 | 277 / 178132 | 0 | 1 | 0 | 157 | 9 |
| B/D | 4 | 3m0s | 184 / 0 | 3.49 / 3.97 / 4.13 | 184 / 0 | 486.5 / 634.5 / 719.0 | 184 / 0 | 52.2 / 78.8 / 239.8 | 11 (6.88) | 3 (0.29) | 0 | 280 / 265290 | 0 | 1 | 0 | 163 | 42 |
| B/D | 8 | 3m0s | 338 / 0 | 3.58 / 4.32 / 4.69 | 338 / 0 | 527.5 / 958.6 / 1040.6 | 338 / 0 | 49.6 / 79.4 / 376.4 | 10 (8.29) | 3 (0.36) | 0 | 279 / 357574 | 0 | 1 | 0 | 165 | 67 |
| B/D | 16 | 3m0s | 450 / 0 | 5.49 / 7.02 / 8.23 | 450 / 0 | 686.2 / 1454.7 / 1684.7 | 450 / 0 | 57.2 / 132.9 / 543.7 | 9 (11.26) | 3 (0.53) | 4 | 266 / 496088 | 0 | 1 | 0 | 201 | 732 |
| E | 16 | 30m0s | 4601 / 0 | 5.39 / 6.30 / 10.32 | 4601 / 0 | 661.5 / 1115.4 / 1383.1 | 4601 / 0 | 55.6 / 85.2 / 115.3 | 69 (18.76) | 23 (1.40) | 98 | 2592 / 5435815 | 0 | 1 | 0 | 204 | 7737 |

A posting round = one capture plus one freeze/unfreeze pair (3 journals). Deadlocks, rollbacks and commits are database-wide deltas over the tier (pg_stat_database; the compose worker's own work is included). Samples every second.

Correctness over this run's 5622 payouts and 5622 donations: payouts with more than one accepted CREATE_PAYOUT 0; payouts consumed more than once 0; COMPLETED payouts not CONSUMED 0; payments SUCCEEDED more than once 0; duplicate success notices 0; payout reconciliation references 0; ledger integrity after the run: passed=true.
