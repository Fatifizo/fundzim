# Stage 20 — Capacity and performance certification

**Area result: BLOCKED — no owner-approved capacity, latency or volume target exists**, so no workload can be certified.
This document records what was measured, on which environment, and the decisions needed. A development host is not
production hardware; record counts processed in a test are not a capacity claim.

## 1. Measured baselines (all on the 16-vCPU development host; PostgreSQL container limited to 1 GiB)

| Path | Measurement | Source (environment) |
|---|---|---|
| Authentication (Argon2id 64 MiB) | 6.6 logins/s at 1 worker, 18.4/s at 8 (bounded by 4 concurrent hashes by design) | Stage 18 security-testing.md §6 (dev stack) |
| Session-authenticated read (`GET /me`) | p95 4.6 ms, 3 496 req/s at 10 workers | Stage 18 (dev stack) |
| Campaign browsing / search | Stage 7 and 16 perf documents (not re-measured since) | docs/stage-7, docs/stage-16 |
| Donation intent + attempt creation | p50 63 ms, p95 166 ms at 16 load workers | Stage 20 burst (**disposable stack**), `evidence/stage-20/burst-c16-5min.md` |
| Payment status processing | covered inside the payout/donation lifecycles; poll budget bounds traffic (ADR-051) | Stage 19/20 |
| Ledger posting (2-line journal) | 59–65 journals/s at 1 writer; **155–182 /s from 8 writers on, flat at 16**; p50 46–51 ms (8 writers) and 95–101 ms (16) | `evidence/stage-19/ledger-perf-*.md` (scratch databases `fundzim_perf_s19a/b`) |
| Payout lifecycle (request → 2 approvals → submit → simulator completion → consumption) | 880 lifecycles in 5 min at 16 workers, p50 5.39 s, p99 5.72 s; 0 errors, 0 deadlocks | Stage 20 burst (disposable stack) |
| Reconciliation | payout run max 4.7 s during the burst (clean data) vs 17–19 s on the contaminated dev DB; Stage 17 run with 23 612 discrepancies: 83 s (first), 46 s (second) | Stage 20 burst; Stage 19 volume sample (dev DB) |
| Notification delivery | ceiling ~19.7 jobs/s with 2 notification workers; backlog recovers at that rate | operations-readiness.md §2 (disposable stack) |
| Audit write | 452 events/s at 1 writer, 553/s at 8 writers | Stage 18 §6 (dev stack) |
| Audit verification | full chain 1 330 046 rows in 12.8 s on 8 connections; incremental after a checkpoint in ms | Stage 19 (dev DB) |

Reproducibility: the Stage 20 burst on clean data reproduced the Stage 19 post-fix lifecycle figures (p50 5.39 s both).

## 2. The audit-chain lock (brief §7)

**Implementation (verified).** `audit.chain_append()` (migration `20261008120200_audit.sql`) takes
`pg_advisory_xact_lock(hash(<chain table>))`, then reads the chain head and assigns `seq = head + 1`, `prev_hash =
head.hash`. The lock is transaction-scoped: it is held from the audit insert **until the transaction commits**. There
are two chains (`audit.audit_events`, `audit.security_audit_events`); 81 Go files write audit events, including every
ledger posting (`ledger.journal.posted`), payment transition, payout event and staff action.

| Question | Answer (evidence) |
|---|---|
| Does it serialise unrelated financial operations? | **Yes.** Any two transactions that write to the same chain serialise from their audit insert to commit, whatever they touch otherwise (a ledger posting and an unrelated staff login, for example). A ledger posting also holds its balance-row locks while waiting for the chain lock, so hot accounts convoy behind it (KI-S19-01). |
| Is it required by the current integrity design? | **Yes.** It makes `seq` order equal commit order, so the chain has no gaps or forks and range/incremental verification (ADR-050) can prove completeness. Removing it would allow concurrent writers to fork the chain. |
| Does contention increase materially with writers? | **Yes.** Ledger posting throughput rises from ~63/s (1 writer) to ~170/s (8) and then stays flat at 16 while p50 latency doubles (≈ 47 → ≈ 98 ms): saturation. Audit writes alone: 452/s → 553/s from 1 to 8 writers. |
| Is verification reliable during concurrent posting? | **Yes** in every test: 144 integrity runs during 1 878 concurrent postings (Stage 19) and the isolated Stage 20 run; interrupted verifications record nothing. |
| Could an optimisation weaken integrity? | Removing or narrowing the lock, writing audit asynchronously outside the business transaction, or batching without a sealing protocol would weaken either completeness (events lost on crash) or ordering. Candidate designs that keep integrity — per-domain chains with a periodic cross-chain Merkle seal, or a single sequencer with batched appends sealed by checkpoints — need an ADR, a migration strategy for 1.3 M+ existing rows and new tamper/ordering tests. **None was implemented** (brief §7). |
| What target is required? | **Owner decision** (§3). Without it the measured ~155–182 journals/s on the dev host cannot be judged sufficient or insufficient. |

## 3. Capacity decision document (owner decisions required)

| Decision | Why it is needed | Evidence that will be used |
|---|---|---|
| Expected peak donations per minute and per day, per currency (pilot and first year) | ledger posting and audit chain ceiling; notification sizing; reconciliation windows (KI-S19-09) | ledger harness, burst harness on production-like hardware |
| Expected payouts per day and concurrent reviewers | payout lifecycle, approval throughput, reconciliation window size | burst harness |
| Acceptable latency for donation creation and status display (p95) | user experience under load | burst harness |
| Notification delivery target (e.g. 95 % within N minutes) | worker sizing (F-S20-04) | notification burst-and-drain |
| Reconciliation freshness target | incremental design (KI-S19-09) | coverage tests |
| Production hardware / managed database class | all figures above are dev-host figures | repeat every harness there |

Until these are approved and met by measurements on production-like infrastructure, capacity remains BLOCKED and
**blocks launch**.
