# Stage 10 — Balances, snapshots and the trial balance

## 1. Semantics

`balance_minor` is expressed in the account's **normal balance**: debit-normal accounts (assets, expenses,
suspense) = debits − credits; credit-normal accounts (liabilities, revenue, equity) = credits − debits. Balances
are per account and currency. They are accounting records, not spendable funds: no balance in Stage 10 is
withdrawable, and every value in a TEST ledger is synthetic (the UI labels it TEST_ONLY).

## 2. Projection

| Mode | Accounts | Maintenance |
|---|---|---|
| SYNC | obligation and recoverable (non-negative) | updated in the posting transaction under a row lock; CHECK `balance_minor >= 0` when non-negative |
| DEFERRED | hot platform/provider accounts (clearing, settled, revenue, expense, suspense, bank, equity) | folded by `ledger.fold_deferred_balances()` (maintenance job, every 5 s) |

`v_balances_current` = projection + unfolded entries, so reads are exact regardless of fold lag.
`v_balance_drift` lists accounts whose projection differs from a full recompute; integrity check L10 fails if it
is non-empty. Repair is `ledger.rebuild_balance(account)`, executable by the table owner only (a migration or DBA
action after investigation), never by the application.

## 3. Negative balances

Obligation and recoverable accounts may never be negative (L13): Go refuses (`LEDGER_INSUFFICIENT_FUNDS`), the
SYNC projection CHECK refuses, and the integrity checker reports. Clearing, settled and suspense accounts may move
in either direction by nature (a provider fee before capture settles, a discrepancy); a negative value there is a
reconciliation signal, not an error.

## 4. Trial balance and snapshots

- The trial balance runs in one REPEATABLE READ, read-only snapshot and returns, per currency, every account's
  net debit or credit, total debits = total credits and a `balanced` flag. It covers the whole ledger, or the
  movements of one accounting period (`period_id`).
- There is **no "balance as of an arbitrary timestamp"** API in Stage 10 (KI-S10-09); historical balances can be
  recomputed from entries (`posted_at`), and closed periods are snapshotted.
- Period close records a SYSTEM_SNAPSHOT evidence record with the period's trial balance (SHA-256 of the content
  plus the content) linked to the `ledger.period.closed` audit event.

## 5. Periods

One period per calendar month and currency (`ledger_periods`), created ahead by the maintenance job. OPEN →
CLOSING (request, `ledger.period.close`, step-up; the period must have ended) → CLOSED (approve by a different
person; the trial balance must balance) or back to OPEN (abort). A posting whose `occurred_at` falls in a CLOSED
period is refused (`LEDGER_PERIOD_CLOSED`) unless its rule allows prior-period posting, in which case it lands in the
current OPEN period. Tested: `TestLedgerPeriodCloseMakerCheckerAndPriorPeriodPostings`.
