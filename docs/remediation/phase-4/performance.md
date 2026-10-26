# Remediation phase 4 — performance of the checkpoint-integrity control (B5)

**Setup.**
- Development host: 16 CPUs, PostgreSQL 17.11 in the disposable compose project `fundzim-r6`.
- Scratch database `fundzim_perf_r6`, created and migrated to `20261027090000` inside that project.
- Harness `TestLedgerReconciliationSweepVolume` (`tests/perf/ledger`) at code commit `30eb045`. Payment facts are injected in
  memory (KI-R1-04), so only the PAYMENT_JOURNALS detector is integrity-verified in this harness.
- Raw report: `evidence/ledger-recon-sweep-30eb045.md`; log: `evidence/perf-ledger-30eb045.log.txt`.

**No capacity target exists and none is claimed.** These are measurements on one development host.

## 1. Results

| Measure | 20 000 journals | 100 000 journals | Phase 1 (`8baee4e`, no integrity control) |
|---|---|---|---|
| one full cycle of both sweeps (every run also verifies) | 3 runs, 19.7 s, slowest run 9.8 s | 11 runs, 1 m 40.7 s, slowest run 10.5 s | 19.4 s / 1 m 46 s |
| records examined per second | 2 031 | 1 985 | 2 067 / 1 882 |
| integrity status of every run in the cycle | VERIFIED or INCOMPLETE (the harness fails on anything else; it does not count which) | same | — |
| standalone verification of the completed and the running cycle | **69 ms**, 21 batches re-read, 8.2 MiB allocated | **297 ms**, 101 batches re-read, 39.0 MiB allocated | — |
| interrupted cycle (deadlines 1/8 … 8/8 of the slowest run) | 4 interrupted runs, completed in 17.4 s, all journals examined, **VERIFIED** afterwards | 14 interrupted runs, 1 m 20.9 s, all examined, **VERIFIED** | — |
| evidence stored (all scopes in the scratch database) | 42 rows, 760 kB | 275 rows, 4.6 MB (cumulative over both tiers) | — |
| range re-read plan | index range scan on `uq_ledger_transactions_id_currency`, 0.07 ms | same, 0.06 ms | — |

The plan samples the second-to-last evidence row, which in both tiers was a small tail batch of 3 keys, not a full 1 000-key
batch. A full batch reads one index range of 1 000 entries. The standalone verification times above include full batches.

**Cycle time.** No measurable overhead from the evidence writes and verification. The cycle times are within the run-to-run
variation of the Phase 1 measurement.

## 2. Locks and progress

- **Verification.** It reads only (SELECT, AccessShare): the checkpoint, its evidence, and the scope's range queries. It holds
  no lock that blocks a writer and never blocks reconciliation progress. It records its outcome with one attempt-only UPDATE
  of the RUNNING checkpoint row (no version step), which cannot conflict with an advance's compare-and-swap.
- **The evidence insert.** It runs in the advance's transaction. The constraint trigger is one indexed lookup
  (`uq_*_sweep_batches_seq`) and one set comparison of the deferral arrays, at commit.
- **Interruption.** A run cancelled inside verification records nothing (its error is returned). The next run verifies again.

## 3. Scaling limits (measured inputs, arithmetic, not measurements)

| Topic | Detail |
|---|---|
| Verification cost per run | proportional to the batches of the running cycle plus the last completed cycle. Across a cycle of *B* batches the re-reads add up to about *B²/2*. At 100 000 journals with 1 000-key batches (*B* ≈ 100) one verification took 0.3 s. At 1 000 000 journals (*B* ≈ 1 000) a verification would re-read about 1 000–2 000 ranges: a few seconds by linear extrapolation of the 0.3 ms per range measured here |
| The budget | each verification has a 1-minute budget. Beyond it the status is **INCOMPLETE**, never VERIFIED, and that is visible on the checkpoint and the health metric. **Safe alternative if INCOMPLETE becomes frequent:** incremental verification, i.e. verify each batch once after it is written and persist a verified-through sequence per cycle (an attempt-only column). Not implemented: the measured cost does not require it |
| Storage | about 16 bytes per examined key plus row overhead, every cycle. At 100 000 records and one cycle every ~2.5 h, about 40 MB per detector per day. **Retention is undecided (LR-012)**: nothing deletes evidence. This must be decided with D-05 volumes before production |
| Payouts and fees | not volume-measured in this phase. The Phase 1 small-volume test `TestReconciliationCoverageVolume` passed with verification active (`fundzim-r6`, `fundzim-r7`). Payout re-reads are cheap range scans; the payout sweep's cost stays dominated by provider status queries (Phase 1) |
