## Ledger reconciliation sweep volume (ADR-052)

started 2026-10-26T14:06:25Z UTC; GOMAXPROCS=16; NumCPU=16; PostgreSQL 17.11; pool 8; scratch database; facts injected in memory

### 20000 capture journals (20003 in the ledger's synthetic source, 20003 injected payment facts)

| measure | value |
|---|---|
| load (PostOnce, 8 workers) | 4m59.85s |
| one full cycle of both sweeps | 3 runs, 19.697s wall, slowest run 9.778s |
| records examined in that cycle (journals + facts) | 40006 (2031 records/s) |
| planted old AMOUNT_MISMATCH, cross-batch DUPLICATE_POSTING, oldest NOT_EVALUATED | found, once each |
| next cycle under deadlines of 1/8 … 8/8 of the slowest run | 4 interrupted runs, completed in 17.36s, examined 20003 = 20003 journals |
| checkpoint integrity (every run of the cycle; journals) | VERIFIED or INCOMPLETE in every run; VERIFIED after the interrupted cycle |
| standalone verification (completed + running cycle) | 69ms, 21 batches re-read, 8.2 MiB allocated |
| evidence (all scopes in this database) | 42 rows, 760 kB |

plan of one range re-read (1 000-key batch):

```
Index Scan using uq_ledger_transactions_id_currency on ledger_transactions t  (cost=0.29..8.32 rows=1 width=40) (actual time=0.027..0.032 rows=3 loops=1)
  Index Cond: ((id > '01a1788d-3f65-7820-bc38-7830dd3f052f'::uuid) AND (id <= '01a1788d-3f93-72a9-9f67-41361ad28a14'::uuid))
  Filter: ((source_system = 'synthetic_fixture'::text) AND (source_type = 'payment'::text) AND (posting_rule = 'DONATION_CAPTURED'::text))
  Buffers: shared hit=5
Planning:
  Buffers: shared hit=12
Planning Time: 0.318 ms
Execution Time: 0.071 ms
```

### 100000 capture journals (100003 in the ledger's synthetic source, 100003 injected payment facts)

| measure | value |
|---|---|
| load (PostOnce, 8 workers) | 20m16.887s |
| one full cycle of both sweeps | 11 runs, 1m40.739s wall, slowest run 10.497s |
| records examined in that cycle (journals + facts) | 200006 (1985 records/s) |
| planted old AMOUNT_MISMATCH, cross-batch DUPLICATE_POSTING, oldest NOT_EVALUATED | found, once each |
| next cycle under deadlines of 1/8 … 8/8 of the slowest run | 14 interrupted runs, completed in 1m20.932s, examined 100003 = 100003 journals |
| checkpoint integrity (every run of the cycle; journals) | VERIFIED or INCOMPLETE in every run; VERIFIED after the interrupted cycle |
| standalone verification (completed + running cycle) | 297ms, 101 batches re-read, 39.0 MiB allocated |
| evidence (all scopes in this database) | 275 rows, 4616 kB |

plan of one range re-read (1 000-key batch):

```
Index Scan using uq_ledger_transactions_id_currency on ledger_transactions t  (cost=0.42..8.45 rows=1 width=40) (actual time=0.027..0.031 rows=3 loops=1)
  Index Cond: ((id > '01a178a0-624d-7297-8f3d-ee08c92814f0'::uuid) AND (id <= '01a178a0-6281-74ba-93d5-75123e880e09'::uuid))
  Filter: ((source_system = 'synthetic_fixture'::text) AND (source_type = 'payment'::text) AND (posting_rule = 'DONATION_CAPTURED'::text))
  Buffers: shared hit=6
Planning:
  Buffers: shared hit=14
Planning Time: 0.288 ms
Execution Time: 0.064 ms
```

