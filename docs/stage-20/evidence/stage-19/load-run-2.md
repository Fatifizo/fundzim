## Stage 19 financial load (TestFinancialLoad)

started 2026-10-23T20:16:33Z UTC; host NumCPU=16 GOMAXPROCS=16; go1.27.3; PostgreSQL 17.11 (compose, max_connections 100, 1 GiB limit); in-process API pools (DATABASE_MAX_CONNS="20"); compose worker running alongside; simulator provider; tier duration 3m0s

container memory before: `fundzim-fundzim-worker-1=33.12MiB / 512MiB fundzim-fundzim-api-1=76.26MiB / 512MiB fundzim-fundzim-web-1=168.1MiB / 768MiB fundzim-fundzim-postgres-1=533.7MiB / 1GiB fundzim-fundzim-redis-1=4.652MiB / 256MiB fundzim-fundzim-clamav-1=954MiB / 3GiB fundzim-fundzim-secrets-1=30.57MiB / 256MiB fundzim-fundzim-mail-1=50.48MiB / 128MiB fundzim-fundzim-storage-1=8.434MiB / 512MiB`

container memory after: `fundzim-fundzim-worker-1=32.29MiB / 512MiB fundzim-fundzim-api-1=13.06MiB / 512MiB fundzim-fundzim-web-1=168.1MiB / 768MiB fundzim-fundzim-postgres-1=1007MiB / 1GiB fundzim-fundzim-redis-1=4.457MiB / 256MiB fundzim-fundzim-clamav-1=952.4MiB / 3GiB fundzim-fundzim-secrets-1=30.35MiB / 256MiB fundzim-fundzim-mail-1=31.31MiB / 128MiB fundzim-fundzim-storage-1=7.16MiB / 512MiB`

| tier | c | duration | payout lifecycles (n / err) | payout p50 / p95 / p99 s | donations (n / err) | donation p50 / p95 / p99 ms | posting rounds (n / err) | posting p50 / p95 / p99 ms | recon runs (max s) | integrity runs (max s) | deadlocks | rollbacks / commits | max lock waiters | max active | max idle-in-tx | max outbox undispatched | max River jobs ready |
|---|---:|---|---|---|---|---|---|---|---|---|---:|---|---:|---:|---:|---:|---:|
| B/D | 8 | 3m0s | 438 / 0 | 3.07 / 3.73 / 4.07 | 438 / 0 | 84.7 / 127.1 / 305.4 | 438 / 0 | 106.6 / 180.6 / 560.3 | 7 (18.95) | 2 (1.11) | 0 | 256 / 521041 | 0 | 1 | 0 | 235 | 592 |
| B/D | 16 | 3m0s | 536 / 0 | 5.39 / 5.57 / 5.71 | 536 / 0 | 69.3 / 198.3 / 349.9 | 536 / 0 | 53.0 / 175.1 / 474.6 | 7 (16.57) | 2 (1.26) | 0 | 1112 / 654694 | 0 | 1 | 0 | 268 | 1977 |
| E | 16 | 30m0s | 5167 / 0 | 5.43 / 5.64 / 5.83 | 5167 / 0 | 66.3 / 125.3 / 186.0 | 5167 / 0 | 52.1 / 118.5 / 170.1 | 66 (18.68) | 22 (2.79) | 0 | 17892 / 6520421 | 0 | 1 | 0 | 199 | 15025 |

A posting round = one capture plus one freeze/unfreeze pair (3 journals). Deadlocks, rollbacks and commits are database-wide deltas over the tier (pg_stat_database; the compose worker's own work is included). Samples every second.

Correctness over this run's 6141 payouts and 6141 donations: payouts with more than one accepted CREATE_PAYOUT 0; payouts consumed more than once 0; COMPLETED payouts not CONSUMED 0; payments SUCCEEDED more than once 0; duplicate success notices 0; payout reconciliation references 0; ledger integrity after the run: passed=true.
