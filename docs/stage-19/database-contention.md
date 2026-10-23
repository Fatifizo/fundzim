# Stage 19 — Database isolation, locking, deadlocks and retry semantics

PostgreSQL 17.11, single node. Role-level settings (`pg_db_role_setting`, observed): `fundzim_app`, `fundzim_worker`,
`fundzim_ledger`: `lock_timeout = 3s`, `idle_in_transaction_session_timeout = 30s`, `statement_timeout` 15 s / 60 s /
30 s; `deadlock_timeout = 1s` (server). Pools: API app pool `DATABASE_MAX_CONNS` (20), ledger pool 8, KYC 8,
compliance 4; worker pool `WORKER_DATABASE_MAX_CONNS` (10), `WORKER_CONCURRENCY` 4 per queue. Isolation is READ
COMMITTED everywhere except read-only REPEATABLE READ snapshots for ledger integrity (`integrity.go`) and balance
reports (`balances.go`).

## 1. Locks used by financial paths

| Path | Locks, in order |
|---|---|
| Ledger posting (`ledger.Post`) | (1) account rows created/read in code order (`INSERT … ON CONFLICT DO NOTHING`); (2) **Stage 19:** `FOR UPDATE` on every SYNC `ledger_balances` row of the journal, ascending account id; (3) entries in ascending account id — `FOR SHARE` on each `ledger_accounts` row (status vs freeze) and the projection UPDATE (already held); (4) audit chain advisory lock (`audit.RecordGateway`, global, serialises audit writers); (5) outbox insert. DEFERRED (hot, platform-wide) projection rows are never locked by postings; the fold job updates them under its own single-runner lock |
| Payment apply (webhook, poll, sync) | payment row `FOR UPDATE` (`lockPay`) or `FOR UPDATE SKIP LOCKED` (routine poll, sweeps); events, outbox, audit in the same transaction; provider calls never inside a transaction (calls committed IN_FLIGHT first) |
| Payout status change (`applyStatus`) | payout row `FOR UPDATE`; reservation row only for BLOCKED↔ACTIVE; journals afterwards on the ledger pool (`syncLedger`) |
| Payout attempt insert (`startAttempt`) | **Stage 19:** payout row `FOR UPDATE` first, then the attempt number (`max + 1`, now serialised per payout) and, for GET_STATUS, the "no other status query in flight" check; trigger `payout_attempts_guard` takes the same payout row and reads the reservation's submit link (no CREATE_PAYOUT without the in-transit journal; UNKNOWN never resubmitted) |
| Payout provider answer (`recordObservation`) | **Stage 19:** payout row first, then the attempt row update, events, status |
| Reservation link (`syncLedger.link`) | reservation row (UPDATE … WHERE link IS NULL) |
| Payout reconciliation recording | **Stage 19:** payout row `FOR UPDATE` while writing references and the exception |
| Reconciliation (Stage 17) | row locks (`FOR UPDATE`) on `recon.exceptions` (by dedupe key) and `recon` resolutions while deciding (ADR-049) |
| Migrations | goose PostgreSQL session lock (Stage 18) |

## 2. Lock-order analysis

| Pair | Before Stage 19 | Now |
|---|---|---|
| ledger posting vs ledger posting, same accounts, opposite directions (CAMPAIGN_FROZEN/UNFROZEN, RELEASE vs RESERVE_RELEASE, PAYOUT_RESERVED vs PAYOUT_RELEASED on one campaign) | **inversion**: only the decreased rows were pre-locked, the others were locked by the entry triggers → 5 deadlocks and 109 lock timeouts in 200 concurrent postings (`ledger2.log`) | all SYNC rows pre-locked in id order: 0 / 0 in 200 (`ledger3.log`) and under the mixed load (`benchmark-results.md`) |
| status-query attempt insert (payout row via the guard, then the unique attempt number) vs recording an answer (attempt row, then payout row) | **inversion** plus unnumbered concurrency: 64 concurrent status queries → 39 duplicate attempt numbers, 2 deadlocks surfaced (68 in the database, the rest retried by `WithTx`), 2 lock timeouts, 1 stranded IN_FLIGHT attempt; the 30-minute endurance run logged 98 deadlocks, every one of them this pair | payout row first in both; status queries of one payout are not concurrent: 64/64, 0 deadlocks |
| payout status (payout row) vs reservation link (reservation row) | different transactions; no transaction holds both in opposite orders (`applyStatus` takes payout then reservation; `link` takes only the reservation) | unchanged |
| reconciliation recording (payout row) vs `applyStatus` (payout row) | — | same single row; short transactions; no second lock taken while holding it besides inserts |
| ledger posting (balance rows → audit lock) vs other audit writers (audit lock only) | no cycle: audit writers outside the ledger never wait for ledger rows while holding the audit lock | unchanged; this is the main convoy source under load (a posting holds its balance rows while it waits for the global audit lock) |
| payment apply vs ledger | test payments are never posted; PRODUCTION posting runs in the apply transaction (payment row → ledger rows) and nothing locks ledger rows first and then a payment row | unchanged |

## 3. Retry classification

| Error | Safe to retry? | What the code does |
|---|---|---|
| Deadlock victim (40P01) | yes — the victim's transaction was rolled back | `db.WithTx` retries the whole function up to 3 times with jittered backoff; `ledger.PostOnce` adds 4 attempts around it |
| Serialization failure (40001) | yes — rolled back | same |
| Connection failure before the transaction started | yes | pool acquisition error returned; callers' outer loops (jobs) retry on their next run |
| Connection failure during execution (before COMMIT) | yes — PostgreSQL rolled back | error returned, not retried in place; the next run of the job (or Recover) retries; idempotent keys make it safe (`TestFinancialLedgerConnectionLossAndAmbiguousRetry`: 0 journals after the loss, 1 after the retry) |
| Connection failure **during/after COMMIT** (ambiguous) | only through an idempotent identity | never retried blindly: the next attempt is resolved by the deterministic key (ledger: `PostOnce` returns the existing journal; payouts: journal keys `payout:{id}:{step}`, links `WHERE … IS NULL`; payments: idempotency keys and `apply` precedence) |
| Unique violation on an idempotency key | yes — it means a concurrent writer won | ledger `errRetry`: the retry finds the winner's journal (same payload → same id; different payload → LEDGER_IDEMPOTENCY_CONFLICT) |
| Other constraint violation (CHECK, guard, FK) | no — a refusal by the last line of defence | mapped to a stable error; never retried; payouts open LEDGER_POSTING_FAILED |
| Lock timeout (55P03) | the statement and transaction were rolled back, so retrying is safe, but it signals contention | not retried in place (deliberately: retries amplify convoys); the calling job retries on its next run; Stage 19 removed the main source (opposite-order postings) |
| Pool acquisition timeout | yes | context deadline error; jobs retry next run |

Provider calls are never inside a database transaction and never retried by `WithTx`; their outcomes follow the
UNKNOWN rules (docs/payments/transaction-lifecycle.md).

## 4. Observed contention (this session)

| Run | Deadlocks | Lock timeouts | Notes |
|---|---|---|---|
| 200 opposite-direction postings, 16 concurrent, one campaign, before the fix | 5 (after retries) | 109 | 86 posted; 290 s |
| same, after the fix | 0 | 0 | 200 posted; 22.8 s |
| 64 concurrent payout status queries, before / after F-S19-12 | 68 / 0 | 2 / 0 | 43 of 64 failed before; 0 after |
| mixed load, first run (before F-S19-12) | c = 16: 4; endurance 30 min: 98 (all payout attempts) | — | no workload operation failed (`WithTx` retried them) |
| mixed load, second run (after) | see benchmark-results.md | | |

No production database setting was changed on the basis of these synthetic results.
