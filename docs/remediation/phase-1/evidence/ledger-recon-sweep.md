## Ledger reconciliation sweep volume (ADR-052)

started 2026-10-25T15:14:33Z UTC; GOMAXPROCS=16; NumCPU=16; PostgreSQL 17.11; pool 8; scratch database; facts injected in memory

### 20000 capture journals (20003 in the ledger's synthetic source, 20003 injected payment facts)

| measure | value |
|---|---|
| load (PostOnce, 8 workers) | 5m16.49s |
| one full cycle of both sweeps | 3 runs, 19.357s wall, slowest run 9.8s |
| records examined in that cycle (journals + facts) | 40006 (2067 records/s) |
| planted old AMOUNT_MISMATCH, cross-batch DUPLICATE_POSTING, oldest NOT_EVALUATED | found, once each |
| next cycle under deadlines of 1/8 … 8/8 of the slowest run | 7 interrupted runs, completed in 35.612s, examined 20003 = 20003 journals |

### 100000 capture journals (100003 in the ledger's synthetic source, 100003 injected payment facts)

| measure | value |
|---|---|
| load (PostOnce, 8 workers) | 22m11.518s |
| one full cycle of both sweeps | 11 runs, 1m46.259s wall, slowest run 10.932s |
| records examined in that cycle (journals + facts) | 200006 (1882 records/s) |
| planted old AMOUNT_MISMATCH, cross-batch DUPLICATE_POSTING, oldest NOT_EVALUATED | found, once each |
| next cycle under deadlines of 1/8 … 8/8 of the slowest run | 13 interrupted runs, completed in 1m24.721s, examined 100003 = 100003 journals |

