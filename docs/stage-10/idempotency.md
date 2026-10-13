# Stage 10 — Idempotency

| Layer | Mechanism |
|---|---|
| Journal | `UNIQUE (idempotency_key)` on `ledger_transactions`. The key is deterministic per business event and rule (formats in `ledger_posting_rules.idempotency_key_format`, e.g. `payment:{payment_id}:capture`, `reversal:{original_transaction_id}`, `fixture:{run}:{step}`) |
| Replay | Same key + equivalent journal (same rule, currency, source and multiset of (account code, direction, amount)) ⇒ the existing journal is returned with `replayed: true`; nothing is written, audited or published again |
| Conflict | Same key + different journal ⇒ `LEDGER_IDEMPOTENCY_CONFLICT` (409), audited as `ledger.journal.rejected` |
| Race | Concurrent first writers: one wins the unique index; the loser's transaction aborts and `PostOnce` retries, then returns the winner's journal |
| Duplicate source event | L6 (integrity) flags two journals for one single-event source and rule; reconciliation flags DUPLICATE_POSTING |
| Reversal | `uq_ledger_transactions_reverses`: a journal can be reversed at most once; a reversal cannot be reversed |
| Adjustment approval | one reversal and one result journal per adjustment (`uq_ledger_transactions_adjustment_*`) |
| Source references | `uq_ledger_source_references_identity` (source system, type, id, event, rule); outbox redelivery inserts nothing |
| Reconciliation | one unresolved reference per (exception type, source type, source id) |
| HTTP | `POST /admin/ledger/adjustments` requires an `Idempotency-Key` header (platform idempotency middleware); other staff actions are state transitions guarded by the status machine |

Tested: `TestLedgerHundredConcurrentDuplicatePostingsYieldOneJournal` (100 concurrent identical postings ⇒ one
journal, balance counted once; then a conflicting body ⇒ conflict), `TestLedgerSyntheticFixturesBalancesAndTrialBalance`
(replaying a fixture run posts nothing new), `TestLedgerPaymentsConsumerNeverPostsTestPayments` (redelivered event
⇒ one source reference).
