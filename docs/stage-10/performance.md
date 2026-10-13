# Stage 10 — Ledger performance

Every number below was measured in this session on 2026-10-13 with `tests/perf/ledger` (build tag `perf`). Nothing is
extrapolated. These are development-machine numbers for a TEST ledger, not production capacity figures.

## 1. Environment

| Item | Value |
|---|---|
| Host | 16 vCPU Intel Xeon Platinum 8160 @ 2.10 GHz, 30 GB RAM; Go client and PostgreSQL on the same host |
| Database | PostgreSQL 17.11 in the compose container `fundzim-postgres` (1 GiB memory limit), **default settings**: shared_buffers 128 MB, work_mem 4 MB, maintenance_work_mem 64 MB, fsync on, synchronous_commit on |
| Database used | scratch database `fundzim_perf`, created as `fundzim_migrator`, migrated with `fundzimctl`, dropped afterwards (the development ledger is append-only and was not used) |
| Client | `ledger.Service` on the `fundzim_ledger` role, pgx pool of 16 connections; GOMAXPROCS 16 |
| Dataset | TEST_ONLY synthetic `DONATION_CAPTURED` journals (2 lines each) over 1 000 campaign accounts and 1 provider clearing account; amounts 1.00–99.99 USD |
| Loading | bulk SQL inserts in batches of 20 000 journals per transaction **through every trigger and the deferred COMMIT checks** (not through the HTTP API); then each scenario was measured through the production code paths |
| Concurrent activity | During the load to 1 M journals, Go unit tests (race), web unit tests and the mock E2E suite ran on the same host, and two diagnostic SQL probes briefly contended for balance-row locks. Nothing else ran during the measurement phases |

## 2. Results (run of 15:42–16:40 UTC)

#### Setup

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| seed: PostOnce, 1000 new accounts | 8 | 1000 | 124.4 | 146.7 | 216.8 | 437.3 | 63 | 0 |

#### 10000 journals

dataset: 11000 journals, 22000 entries, 1000 campaign accounts + 1 clearing account; database size 35 MB. Bulk load of this step: 20.363s (491 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 16ms (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 15.5 | 22.6 | 25.8 | 27.5 | 61 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 51.9 | 64.7 | 69.7 | 75.8 | 152 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 128.9 | 155.5 | 202.7 | 321.9 | 123 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 0.6 | 2.2 | 7.3 | 9.3 | 8465 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 11.8 | 16.8 | 19.4 | 20.3 | 642 | 0 |
| Journal register, first page | 8 | 300 | 2.1 | 4.0 | 5.3 | 7.9 | 3338 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 2.2 | 3.7 | 4.3 | 5.2 | 3324 | 0 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 107.3 | 125.2 | 125.2 | 125.2 | 9 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 899.9 | 899.9 | 899.9 | 899.9 | 1 | 0 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 13.3 | 13.3 | 13.3 | 13.3 | 75 | 0 |

#### 100000 journals

dataset: 106100 journals, 212200 entries, 1000 campaign accounts + 1 clearing account; database size 146 MB. Bulk load of this step: 4m38.658s (359 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 104ms (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 17.6 | 25.6 | 28.8 | 32.1 | 54 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 69.9 | 84.3 | 90.5 | 122.0 | 113 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 135.0 | 159.1 | 168.6 | 208.8 | 118 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 1.0 | 1.8 | 4.5 | 6.8 | 6959 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 37.2 | 46.7 | 59.6 | 62.3 | 210 | 0 |
| Journal register, first page | 8 | 300 | 2.0 | 4.0 | 4.9 | 4.9 | 3465 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 2.1 | 4.2 | 5.1 | 6.4 | 3312 | 0 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 287.2 | 308.4 | 308.4 | 308.4 | 3 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 5471.4 | 5471.4 | 5471.4 | 5471.4 | 0 | 0 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 57.6 | 57.6 | 57.6 | 57.6 | 17 | 0 |

#### 1000000 journals

dataset: 1011200 journals, 2022400 entries, 1000 campaign accounts + 1 clearing account; database size 1080 MB. Bulk load of this step: 49m2.081s (340 journals/s incl. triggers and deferred checks); fold of DEFERRED balances: 7.712s (1 rows).

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| PostOnce (2-line capture, new journal) | 1 | 300 | 17.1 | 24.2 | 27.7 | 112.2 | 54 | 0 |
| PostOnce (2-line capture, new journal) | 8 | 1600 | 53.0 | 67.3 | 82.6 | 297.2 | 145 | 0 |
| PostOnce (2-line capture, new journal) | 16 | 3200 | 106.9 | 127.2 | 137.8 | 300.9 | 147 | 0 |
| Account balance (Account: exact current balance + totals) | 8 | 500 | 31.6 | 37.9 | 296.9 | 297.1 | 219 | 0 |
| Account activity (first page of journals touching an account) | 8 | 300 | 74.9 | 90.8 | 1005.1 | 1019.0 | 79 | 0 |
| Journal register, first page | 8 | 300 | 2.1 | 3.5 | 5.2 | 6.9 | 3517 | 0 |
| Journal register, page after the 10 000th journal (keyset) | 8 | 300 | 2.2 | 3.8 | 7.8 | 7.9 | 3102 | 0 |
| Trial balance (all USD accounts, REPEATABLE READ) | 1 | 5 | 3766.7 | 4830.5 | 4830.5 | 4830.5 | 0 | 0 |
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 1 | 69462.2 | 69462.2 | 69462.2 | 69462.2 | 0 | 1 |
| Reconciliation run (no payment facts; suspense scan) | 1 | 1 | 171.9 | 171.9 | 171.9 | 171.9 | 6 | 0 |


## 3. Integrity checker at one million journals

In the run above the integrity checker **failed with an error** at 1 M journals (69 s; the "errors 1" row). It did
not record a run and it did not find a violation: a single check query exceeded the `fundzim_ledger` role's 30 s
`statement_timeout`. Timing each check directly on the 1 M dataset (read-only snapshot, no timeout) found L15 (the
environment check) at 31.9 s, because the SECURITY DEFINER function `ledger.database_environment()` was evaluated
once per row. The other checks took L1 9.2 s, L2 5.1 s, L4 1.2 s, L6 2.2 s, L9 0.3 s, L10 4.9 s, L13 0.3 s, all
with 0 violations (L7 was not timed separately; the dataset has no reversals).

Two fixes: L15 reads the designation once (scalar subquery: 0.21 s at 1 M), and the checker's read-only snapshot
now has its own 15-minute statement timeout. Re-measured on the same 1 016 300-journal dataset
(`TestLedgerPerfIntegrityOnly`):

| scenario | c | n | p50 ms | p95 ms | p99 ms | max ms | ops/s | errors |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Integrity checker (L1–L15, one snapshot, recorded) | 1 | 3 | 31761.0 | 32873.1 | 32873.1 | 32873.1 | 0 | 0 |

All three runs recorded every invariant PASSED with 0 violations (2 032 600 entries checked by L4). The 10 k and
100 k integrity rows in §2 were measured before the L15 fix.

## 4. Observations

- **Posting latency stays flat with ledger size**: a single-writer `PostOnce` p50 was 15.5 / 17.6 / 17.1 ms at
  10 k / 100 k / 1 M journals.
- **Throughput does not grow beyond about 8 concurrent writers** (113–152 postings/s at c = 8 and c = 16). Every
  posting appends an audit event, and the audit hash chain serialises its writers with a transaction-scoped
  advisory lock (`audit.chain_append`) until commit (KI-S10-16). This is a throughput ceiling, not a correctness
  problem; the invariants were not weakened to raise it.
- The journal register (keyset pagination) stays at about 2 ms at every size, including a page 10 000 journals deep.
- Single-account reads degrade with the account's entry count: at 1 M journals (about 2 000 entries per campaign
  account) the exact balance read took p50 31.6 ms / p99 297 ms and the first activity page p50 74.9 ms / p99
  1 005 ms. The provider clearing account (1 M entries) was not measured as a single-account read.
- The trial balance is a full aggregation: p50 3.8 s at 1 M journals.
- Bulk loading through the triggers ran at 340–491 journals/s; most of each batch's time is the per-journal deferred
  check at COMMIT.
- The reconciliation figures cover only the suspense scan: no payment facts exist in a TEST ledger.

## 5. Reproduce

```bash
# as the postgres superuser: CREATE DATABASE fundzim_perf OWNER fundzim_migrator; GRANT CONNECT … TO fundzim_ledger;
DATABASE_MIGRATION_URL=<…/fundzim_perf…> go run ./apps/api/cmd/fundzimctl migrate up
FUNDZIM_PERF_LEDGER_URL=<fundzim_ledger …/fundzim_perf…> FUNDZIM_PERF_OUT=/tmp/ledgerperf \
  go test -tags perf -count=1 -timeout 240m -run TestLedgerPerf -v ./tests/perf/ledger/
# FUNDZIM_PERF_SCALES=10000 for a quick run; drop the database afterwards
```
