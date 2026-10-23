# Stage 19 — Authoritative financial invariants

These are the invariants Stage 19 tests under concurrency, retries, duplicates, ambiguous outcomes and failures. They
are taken from the implementation (Go, migrations, CHECKs, triggers) and the binding documents (docs/MONEY.md,
docs/LEDGER.md §4, docs/payments/transaction-lifecycle.md, docs/payments/payout-lifecycle.md, ADR-039/042/043/049);
Stage 19 adds no new money semantics. "DB" means enforced by the database (CHECK, trigger, unique index, grant,
`app.guard_transition`), "Go" by the owning service, "Recon" by a reconciliation detector.

## 1. Ledger (Stage 10)

| # | Invariant | Enforced | Stage 19 evidence |
|---|---|---|---|
| L1 | per journal and currency Σ debits = Σ credits | Go builder + DB deferred trigger | integrity runs during concurrent posting (`TestFinancialLedgerIntegrityDuringConcurrentPosting`: every run passed) |
| L2 | ≥ 2 entries | Go + DB | same |
| L5 | entries and journals never updated or deleted | DB grants + triggers | `TestSecurityRuntimeRolesCannotRewriteFinancialHistory` (Stage 18, rerun) |
| L6 | one business event posts at most once | `uq_ledger_transactions_idempotency_key` | `TestFinancialLedgerConcurrentConflictingPayloads` (60 concurrent posts, two payloads, one journal), `TestLedgerHundredConcurrentDuplicatePostingsYieldOneJournal` |
| L7 | an original is reversed at most once | partial unique index | `TestLedgerReversalsMakerCheckerAndNoDoubleReversal` (rerun) |
| L8 | campaign_payable never below zero | row lock in the posting transaction + `ck_ledger_balances_non_negative` | opposite-direction postings (`TestFinancialLedgerOppositeOrderPostingsDoNotDeadlock`), reservations (`TestPayoutHundredConcurrentRequestsReserveOnce`) |
| L10 | projections equal a full recompute | integrity checker | integrity during posting and after every Stage 19 payout test (`p.integrity`) |
| — | no posting visible unless committed | single transaction, deferred checks | `TestFinancialLedgerConnectionLossAndAmbiguousRetry` (connection killed before commit: 0 journals) |
| — | currencies never combined | per-currency accounts, composite FKs, `ck_*` | unchanged; Stage 19 workloads are USD only (ZWG paths are not exercised by the simulator) |
| — | test journals never real | `ledger_environment = TEST`, `ck_*_no_production_stage10`, `ProductionPostingEnabled = false` | `TestFinancialGateChecksSurviveMigrations`, `TestSecurityFinancialGatesInDatabase` |

**Lock order (Stage 19, ADR-051).** A posting locks the projection rows of every SYNC account it touches in ascending
account id before writing entries; entries are written in ascending account id; DEFERRED (platform-wide hot) rows are
never locked by postings. Consequence: postings never wait on each other in a cycle.

## 2. Payments (Stage 8/9)

| Invariant | Enforced |
|---|---|
| Only `payments.apply()` changes a payment status: allowed edge, precedence (rank), source rule — mirrored in SQL | Go + `app.guard_transition('payment')` + `ck_payment_events_*` |
| A browser return never confirms a payment; SUCCEEDED comes only from SYNC, WEBHOOK, POLL or RECON | Go `sourceAllowed` + DB CHECK |
| A timeout or ambiguous answer is UNKNOWN, never FAILED | Go (`ClassIndeterminate` → UNKNOWN) + DB (FAILED from INTERNAL only from CREATED) |
| A success is applied once per payment; the donor's success notice comes only from `payments.payment_succeeded` | precedence + edge rules; outbox event written once in the applying transaction |
| Webhooks are verified and stored once | `provider_webhook_inbox` dedupe on (provider, provider_event_id) |
| **Routine polling is bounded** (Stage 19): `PollHorizon` (72 h from creation) for CREATED/PENDING/REQUIRES_ACTION/AUTHORISED, `UnknownHorizon` (72 h from `unknown_since`) for UNKNOWN, `MaxStatusPolls` (500) per payment; exhaustion keeps the status and opens POLL_BUDGET_EXHAUSTED | Go (`payments.poll`) + reason CHECK |
| A due payment is queried once per due period, whatever the number of sweepers (Stage 19) | lease (`next_poll_at`) re-checked under `FOR UPDATE SKIP LOCKED` |
| A process routine-polls only payments of its own execution environment (Stage 19) | Go (`Sweep`) |
| Test payments are never posted to the ledger | `onSucceeded` posts only PRODUCTION payments; PRODUCTION refused |

## 3. Payouts and reservations (Stage 11)

The payout state is the source of truth; the ledger and the reservation follow it (`syncLedger`). The reservation
status, its journal links and the payout status relate as follows (DB: `ck_payout_reservations_*`,
`app.guard_transition('payout_reservation')`, set-once link trigger; Recon: `reconcileOne` `wantRes`).

| Payout status | Reservation | Journals that must exist (deterministic keys `payout:{id}:…`) |
|---|---|---|
| PAYOUT_REQUESTED | none yet (created by `reserve`) | none |
| PENDING_REVIEW, APPROVED | ACTIVE | reserved |
| SUBMITTED, PROCESSING | ACTIVE | reserved, submitted (the provider is never called before `submitted` commits: attempts guard trigger) |
| UNKNOWN | BLOCKED (or ACTIVE) | reserved, submitted |
| COMPLETED | CONSUMED | reserved, submitted, completed |
| REVERSED | CONSUMED | reserved, submitted, completed, reversed |
| FAILED (after submission) | RELEASED | reserved, submitted, failed |
| REJECTED / CANCELLED | RELEASED or EXPIRED (or none if rejected before reservation) | reserved, released |

Database-level reservation rules (unchanged, re-tested in `TestFinancialReservationInvariantsInTheDatabase`): CONSUMED
requires consume and submit journals and no release; RELEASED/EXPIRED require a release journal and no consumption;
ACTIVE/BLOCKED have neither; one reservation per payout; a reserve journal belongs to one reservation; journal links and
facts are set once; transitions only ACTIVE↔BLOCKED, ACTIVE/BLOCKED→CONSUMED/RELEASED, ACTIVE→EXPIRED.

**Transitional state (KI-S18-25, ADR-051).** Because the journal is posted on the ledger pool, the payout status commits
first and the journal and reservation link follow in separate transactions. Between them the payout is, for example,
COMPLETED with its reservation still ACTIVE and its `ledger_consume_txn_id` NULL. This state is *legitimate and
transient*: it is exactly the condition `ledgerSyncPending` (and the Recover job's selection) describes, and it is
resolved only by `syncLedger` (idempotent keys, links set only where NULL). It is distinguished from a genuine
inconsistency by running that recovery: a state that recovery settles was transitional; a state recovery cannot
settle (ledger refusal) or that recovery does not address (e.g. reservation CONSUMED while the payout is SUBMITTED, a
provider status that differs, a journal with another amount) is a genuine mismatch and is reported.

Other payout invariants (unchanged): one in-progress payout per campaign and currency; at most one accepted
CREATE_PAYOUT and one provider reference per payout (attempts guard, DUPLICATE_PAYOUT_REFERENCE detector); an UNKNOWN
payout is never resubmitted; FAILED never by assumption; approvals are FINANCE maker-checker with linked-identity
exclusion (Go + DB); retries after DEFINITELY_NOT_SENT / RATE_LIMITED are budgeted (KI-S17-01).

## 4. Reconciliation

| Invariant | Where |
|---|---|
| A payout reconciliation evaluates the payout as it is now (re-read), after letting its ledger catch up (Stage 19) | `payouts.reconcileFresh` |
| One unresolved RECONCILIATION_MISMATCH per payout; concurrent runs record one reference set (Stage 19 recording lock) | `uq_payout_exceptions_open` + `FOR UPDATE` on the payout row while recording |
| A rerun over a payout under investigation records nothing new | existing check, re-tested |
| Payment references: one open reference per payment and reason | `ON CONFLICT (payment_intent_id, reason)` partial unique index |
| Stage 17 statement reconciliation: references find candidates, amounts only verify; nothing auto-resolved; UNKNOWN resolutions maker-checker | ADR-049 (unchanged; `TestReconciliationConcurrency` rerun) |
| Settlement is NOT_PERFORMED until real settlement evidence exists | ADR-043, ADR-049 |
