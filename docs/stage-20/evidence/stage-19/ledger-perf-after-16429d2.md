## Ledger performance

started 2026-10-23T19:13:57Z UTC; GOMAXPROCS=16; NumCPU=16; PostgreSQL 17.11 (compose fundzim-postgres, default settings, 1 GiB memory limit); pool 16; scratch database fundzim_perf

### Setup

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| seed: PostOnce, 1000 new accounts | 8 | 1000 | 121.8 | 141.5 | 238.7 | 481.8 | 64 | 0 |

### 1000 journals

dataset: 2000 journals, 4000 entries, 1000 campaign accounts + 1 clearing account; database size 32 MB. Bulk load of this step: 1.867s (536 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 8ms (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 14.7 | 21.7 | 24.3 | 25.8 | 64 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 48.1 | 59.1 | 62.3 | 71.9 | 164 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 96.2 | 112.4 | 129.9 | 291.4 | 164 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 0.6 | 1.8 | 10.5 | 12.4 | 9047 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 5.0 | 7.5 | 11.8 | 11.9 | 1463 | 0 |
| Journal register, first page | 8 | 300 | 2.0 | 3.1 | 4.3 | 5.5 | 3596 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 0.0 | 0.0 | 0.0 | 0.0 | 2639010 | 300 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 28.5 | 35.0 | 35.0 | 35.0 | 34 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 185.8 | 185.8 | 185.8 | 185.8 | 5 | 0 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 15.6 | 15.6 | 15.6 | 15.6 | 64 | 0 |

### 10000 journals

dataset: 16100 journals, 32200 entries, 1000 campaign accounts + 1 clearing account; database size 58 MB. Bulk load of this step: 19.153s (522 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 16ms (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 15.6 | 21.7 | 23.9 | 28.0 | 61 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 46.7 | 55.1 | 60.9 | 88.6 | 170 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 95.5 | 108.5 | 123.3 | 141.8 | 166 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 0.6 | 1.5 | 3.5 | 4.7 | 10540 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 13.5 | 19.8 | 20.4 | 23.3 | 524 | 0 |
| Journal register, first page | 8 | 300 | 1.9 | 2.8 | 3.5 | 4.0 | 3963 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 2.0 | 3.0 | 3.7 | 4.2 | 3651 | 0 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 139.2 | 145.2 | 145.2 | 145.2 | 7 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 325.6 | 325.6 | 325.6 | 325.6 | 3 | 0 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 15.3 | 15.3 | 15.3 | 15.3 | 65 | 0 |

### 100000 journals

dataset: 111200 journals, 222400 entries, 1000 campaign accounts + 1 clearing account; database size 169 MB. Bulk load of this step: 3m12.316s (520 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 558ms (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 16.0 | 22.4 | 25.1 | 57.9 | 59 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 51.0 | 60.4 | 65.1 | 84.0 | 155 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 100.9 | 116.0 | 121.2 | 139.1 | 158 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 2.9 | 4.1 | 33.6 | 33.7 | 2319 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 35.1 | 45.7 | 556.0 | 558.8 | 162 | 0 |
| Journal register, first page | 8 | 300 | 1.9 | 3.1 | 4.7 | 5.2 | 3763 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 2.1 | 3.6 | 4.6 | 4.9 | 3340 | 0 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 274.6 | 399.0 | 399.0 | 399.0 | 3 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 1830.7 | 1830.7 | 1830.7 | 1830.7 | 1 | 0 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 55.8 | 55.8 | 55.8 | 55.8 | 18 | 0 |

