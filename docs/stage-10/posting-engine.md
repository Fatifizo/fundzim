# Stage 10 — Posting engine

## 1. API

```go
func (s *Service) Post(ctx context.Context, tx pgx.Tx, p ledger.Posting) (ledger.Result, error) // caller's transaction
func (s *Service) PostOnce(ctx context.Context, p ledger.Posting) (ledger.Result, error)          // own transaction, retries
```

Both run on the `fundzim_ledger` pool. There is no public or unauthenticated posting endpoint; the only HTTP paths
that create journals are maker-checker approval (`POST /admin/ledger/adjustments/{id}/approve`) and TEST-only
synthetic fixtures (`POST /admin/ledger/synthetic-fixtures`, ADMIN, `LEDGER_SYNTHETIC_FIXTURES_ENABLED`).

## 2. Steps of `Post` (one database transaction)

1. **Validate in Go** (`validate`): rule exists (current version); production disabled; evidence postable for the
   environment; source system and `source_type` allowed by the rule; idempotency key format; currency postable
   (verified minor units); occurred_at, actor, description; reversal and approval requirements; ≥ 2 lines; every
   line a known class with a valid owner and amount > 0; debits = credits (checked, int64 overflow-safe).
2. **Idempotency**: look up the key; an equivalent stored journal returns `Replayed: true`; a different one is
   `LEDGER_IDEMPOTENCY_CONFLICT` ([idempotency.md](idempotency.md)).
3. **Accounts**: create missing accounts (`INSERT … ON CONFLICT DO NOTHING`, audited), refuse FROZEN (unless
   adjustment-backed) and CLOSED accounts.
4. **Lock and check**: lock, in ascending account id, the SYNC projection rows of non-negative accounts that this
   journal decreases (`SELECT … FOR UPDATE`), and refuse `LEDGER_INSUFFICIENT_FUNDS` if the balance does not cover
   it. The CHECK on `ledger_balances` is the backstop.
5. **Insert** the header, then the entries sorted by account id (consistent lock order across writers).
6. **Record** the source reference (`decision = POSTED`), the audit event (`ledger.journal.posted` or
   `ledger.adjustment.posted`) and the outbox event (`ledger.journal_posted` / `ledger.journal_reversed`).
7. **COMMIT** runs the deferred constraint trigger: L1 balance, L2 line count, L12 rule conformance, L7 reversal
   mirror, L14 currency. Any failure aborts the whole transaction — header, lines, projection, audit and outbox.

Database triggers re-check what Go checked (environment, currency, account status `FOR SHARE`, period, rule lines,
amount sign, append-only), so a direct SQL write as `fundzim_ledger` is held to the same rules
(`TestLedgerDatabaseInvariantsDirectSQL`).

## 3. `PostOnce`

Retries the whole transaction (bounded) when a concurrent writer won the idempotency-key race (the retry then
returns the winner's journal) or on deadlock / serialization failure (SQLSTATE 40P01 / 40001). A final failure is
audited as `ledger.journal.rejected` with the error code.

## 4. Concurrency

- Duplicate submissions: the UNIQUE key decides; 100 concurrent identical postings yield exactly one journal and 99
  replays (`TestLedgerHundredConcurrentDuplicatePostingsYieldOneJournal`).
- Decreases of one non-negative account serialise on its projection row lock, so two concurrent debits can never
  overdraw it. Redis is not used for ledger locking.
- Account status changes take the account row lock; postings read it `FOR SHARE`.
- The trial balance reads in one REPEATABLE READ snapshot and stays balanced during concurrent posting
  (`TestLedgerTrialBalanceStaysBalancedDuringPosting`).

## 5. Failure behaviour

Tested in `TestLedgerFailureInjection`: rollback after insert leaves nothing; a connection killed mid-transaction
leaves nothing; lock timeout returns an error and leaves nothing; an outbox gateway refusal aborts the posting; a
deliberately drifted projection is detected by the integrity checker (L10) and repaired only by the owner-only
`ledger.rebuild_balance` function (not by the application).
