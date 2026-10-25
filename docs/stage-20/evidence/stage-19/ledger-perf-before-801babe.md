## Ledger performance

started 2026-10-23T19:08:34Z UTC; GOMAXPROCS=16; NumCPU=16; PostgreSQL 17.11 (compose fundzim-postgres, default settings, 1 GiB memory limit); pool 16; scratch database fundzim_perf

### Setup

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| seed: PostOnce, 1000 new accounts | 8 | 1000 | 115.2 | 134.7 | 209.1 | 439.4 | 67 | 0 |

### 1000 journals

dataset: 2000 journals, 4000 entries, 1000 campaign accounts + 1 clearing account; database size 32 MB. Bulk load of this step: 1.408s (710 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 8ms (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 14.1 | 21.2 | 24.6 | 25.9 | 65 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 43.6 | 53.4 | 59.2 | 66.7 | 181 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 86.2 | 101.7 | 129.4 | 263.0 | 182 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 0.5 | 1.5 | 5.0 | 7.8 | 10950 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 5.1 | 8.3 | 12.5 | 12.8 | 1400 | 0 |
| Journal register, first page | 8 | 300 | 2.0 | 3.1 | 3.9 | 4.5 | 3706 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 0.0 | 0.0 | 0.0 | 0.0 | 2942734 | 300 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 55.9 | 61.0 | 61.0 | 61.0 | 18 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 115.1 | 115.1 | 115.1 | 115.1 | 9 | 0 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 9.1 | 9.1 | 9.1 | 9.1 | 109 | 0 |

### 10000 journals

dataset: 16100 journals, 32200 entries, 1000 campaign accounts + 1 clearing account; database size 59 MB. Bulk load of this step: 13.295s (752 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 14ms (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 14.3 | 20.9 | 23.8 | 39.0 | 65 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 45.0 | 57.6 | 68.5 | 95.4 | 174 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 92.8 | 106.8 | 115.7 | 138.0 | 171 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 0.6 | 2.2 | 3.5 | 6.9 | 8717 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 14.5 | 19.9 | 20.9 | 23.5 | 523 | 0 |
| Journal register, first page | 8 | 300 | 1.9 | 3.3 | 4.6 | 5.1 | 3719 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 2.0 | 2.8 | 4.4 | 5.1 | 3717 | 0 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 130.9 | 136.2 | 136.2 | 136.2 | 8 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 303.9 | 303.9 | 303.9 | 303.9 | 3 | 0 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 18.8 | 18.8 | 18.8 | 18.8 | 53 | 0 |

### 100000 journals

dataset: 111200 journals, 222400 entries, 1000 campaign accounts + 1 clearing account; database size 170 MB. Bulk load of this step: 3m7.507s (533 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 209ms (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 15.0 | 21.1 | 22.9 | 61.1 | 63 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 46.6 | 56.5 | 60.4 | 86.6 | 170 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 95.3 | 111.1 | 127.7 | 161.9 | 167 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 2.5 | 3.5 | 21.8 | 22.2 | 2749 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 35.6 | 40.6 | 104.0 | 107.2 | 213 | 0 |
| Journal register, first page | 8 | 300 | 1.9 | 3.0 | 4.8 | 5.5 | 3812 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 2.0 | 3.3 | 4.0 | 5.1 | 3571 | 0 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 328.5 | 419.6 | 419.6 | 419.6 | 3 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 1707.1 | 1707.1 | 1707.1 | 1707.1 | 1 | 0 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 59.8 | 59.8 | 59.8 | 59.8 | 17 | 0 |

