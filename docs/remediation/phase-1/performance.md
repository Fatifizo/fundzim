# Remediation phase 1 — performance and resource measurement (phase G)

These measurements come from one development host only:
- 16 CPUs, GOMAXPROCS 16;
- PostgreSQL 17.11 in the disposable compose project `fundzim-r1`;
- ledger figures from the scratch database `fundzim_perf_r1b`.

**No capacity target exists, and none is invented here.** The figures are inputs for the owner's coverage freshness decision
(D-06) and volume decision (D-05). Anything below labelled *arithmetic* is the configured budgets applied to the measured
rates. It is not a measurement and not a capacity claim. The raw reports are in `evidence/ledger-recon-sweep.md` and
`it-coverage-volume*.log.txt` / `coverage-sweep-volume-2.md` in `evidence/`.

## 1. Run budgets (engineering defaults, not targets)

| Detector | Batch | Records per run | Time per run | Max deferrals | Schedule | Job timeout |
|---|---|---|---|---|---|---|
| PAYOUTS | 200 | 2 000 | 3 min | 10 000 | every 15 min | 10 min |
| FEE_ASSESSMENTS | 500 | 5 000 | 3 min | 10 000 | every 15 min | 10 min |
| PAYMENT_JOURNALS | 1 000 | 10 000 | 3 min | 10 000 | every 15 min | 10 min (one ledger job runs both ledger sweeps after the recency pass) |
| PAYMENT_FACTS | 1 000 | 10 000 | 3 min | 10 000 | (same job) | (same) |

A run stops at whichever budget it reaches first, and it always completes at least one batch (`TestSweepBudgetAlwaysMakesProgress`).
The job timeout must stay above the run budget plus the recency pass. A hard deadline shorter than one batch makes no progress
(KI-R1-09).

## 2. Ledger sweeps at volume (scratch database, `TestLedgerReconciliationSweepVolume`)

Run on `8baee4e` with the test patch later committed in `a0a687d`. Payment facts were injected in memory (KI-R1-04), so the
per-journal payments lookup of a real deployment is not included.

| Measure | 20 000 journals | 100 000 journals |
|---|---|---|
| load (`PostOnce`, 8 workers; not part of the sweep) | 5 m 16 s | 22 m 12 s |
| one full cycle of both sweeps, runs back to back | 3 runs, 19.4 s | 11 runs, 1 m 46 s |
| slowest run (≤ 10 000 records per detector) | 9.8 s | 10.9 s |
| records examined (journals + facts) | 40 006 (2 067 /s) | 200 006 (1 882 /s) |
| planted old AMOUNT_MISMATCH, cross-batch DUPLICATE_POSTING, oldest NOT_EVALUATED | found once each | found once each |
| next cycle under deadlines from 1/8 to 8/8 of the slowest run | 7 interrupted runs; completed in 35.6 s; examined = 20 003 = all | 13 interrupted runs; completed in 1 m 25 s; examined = 100 003 = all |

Examination costs about 1 ms per record, which means about 1 s per 1 000-record batch. The rate stays nearly flat from
20 000 to 100 000 journals (2 067/s against 1 882/s).

### F-R1-06 — keyset read plan

The runs above used the first version of the keyset reads, `SELECT id::text … ORDER BY id`. There, ORDER BY resolved to the
**text output column**. Plans on the same database at 100 000 journals:

| Statement | Plan | Execution time per 1 000-key batch |
|---|---|---|
| before (`a0a687d^`) | parallel sequential scan of all rows + sort on `(id)::text` | 49.2 ms |
| after (`a0a687d`, `ORDER BY t.id`) | index scan on `pk_ledger_transactions`, `id > cursor`, stops at 1 000 | 3.3 ms |

The full sort grows with the table, so the old cost is O(n) per batch and O(n²) per cycle. At 100 000 journals it was about
5 % of a batch's time, because per-record examination dominates. Ordering was verified identical to uuid order on all existing
data (0 of 100 003 journals, 4 058 development journals, 853 payouts and 41 payments differ), so no record was skipped. The
sweep figures in the table were not re-measured after the fix: the 30-minute load would be repeated for a 5 % change. The
volume IT now fails if a keyset read sorts on the text key.

### Arithmetic for D-06

With the default budget of 10 000 records per run and one run every 15 minutes, a ledger cycle over *N* journals takes
⌈N / 10 000⌉ runs. For example, 100 000 journals take 10 runs, which is 2.5 h of schedule, plus the same again for payment facts
in the same jobs. The runs themselves take about 10 s each on this host. Raising the per-run budget or the frequency shortens
the cycle, at a proportional database cost.

## 3. Payouts and fees on the disposable stack (`TestReconciliationCoverageVolume`)

The disposable stack holds only what the integration suite creates: about 850 payouts and about 475 fee assessments.
`TestFinancialLoad` was **not** run in this phase, so these figures are small-volume only.

| Measure (run on `8baee4e`) | Payouts | Fee assessments |
|---|---|---|
| in scope | 853 (33 submitted → one simulator status query each) | 475 |
| runs for one cycle | 1 | 1 |
| examined (runs) / completed checkpoint | 853 / 853 | 475 |
| cycle wall time, including the recency pass | 3.9 s | 4.0 s |
| sessions waiting on a lock, sampled after each run | 0 | 0 |
| test-process heap allocated in the cycle | 32 MiB total (6.8 MiB live after) | — |

The rerun on `a0a687d` with the production statements is in `evidence/coverage-sweep-volume-2.md`. That run came straight after a fresh suite, so the volume was smaller (45 payouts, 75 assessments). It shows index range scans on the uuid key and no text-key sort.

**Arithmetic.** A payout cycle over *N* payouts takes ⌈N / 2 000⌉ runs, one every 15 min. Each submitted payout costs one
provider status query, made through the simulator here. A real provider's latency and rate limits are unknown (PCR register),
and the time budget will cap a run first. Payouts are therefore the detector to size first once a provider and D-05 volumes
exist.

## 4. Resources

- **Database:** one keyset read per batch (now an index range scan), plus each detector's existing per-record queries. One
  checkpoint row update per batch, in a short transaction. No lock is held across batches or provider calls. No lock waits
  were observed.
- **Memory:** bounded by one batch of keys plus the deferral list (at most 10 000 uuids per checkpoint).
- **Storage:** one checkpoint row per detector per completed cycle (KI-R1-02).

## 5. Not measured

- Payout and fee sweeps at load volume (`TestFinancialLoad` not run).
- Real payment facts in the ledger harness.
- Production-like hardware.
- Concurrency with a full production workload.

These are inputs to a capacity workstream once D-05 exists.
