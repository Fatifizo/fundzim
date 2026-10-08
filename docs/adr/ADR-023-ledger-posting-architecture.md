# ADR-023: Ledger posting architecture

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2 (design); implementation Stage 10

## Context

ADR-006 requires a double-entry ledger, and ADR-014 fixed the chart of accounts and money states. Stage 2 must
decide how postings are made and how PostgreSQL itself guarantees the invariants (L1–L10, SC-1 – SC-6), so that
a bug in Go cannot create an imbalanced or illegal journal.

## Decision

1. **Named posting rules only.** Each business event maps to a rule in `ledger.ledger_posting_rules`, for
   example `DONATION_CAPTURED`, `PAYOUT_RESERVED` or `CAMPAIGN_FROZEN`. Each rule lists, in
   `ledger_posting_rule_lines`, the account patterns it may debit and credit. A database trigger rejects
   entries whose accounts do not match the rule. This enforces SC-1 (a payout reservation may debit only
   `campaign_payable`) and SC-5 (only allow-listed rules touch `fundzim_operating_bank`). Free-form postings
   exist only as `ADJUSTMENT` / `REVERSAL` rules, which require an approved `ledger_adjustments` row
   (maker-checker).
2. **One journal = one currency.** `ledger_transactions.currency` is set; every entry and account must match
   it (SC-6, L2/L3).
3. **Balance is enforced at COMMIT** by a deferred constraint trigger (L1/L2). Idempotency uses
   `UNIQUE(idempotency_key)`. If a key is replayed with identical lines, `ledger.Post` returns the existing
   journal; if the lines differ, it fails with `ErrIdempotencyConflict` and raises an alert.
4. **The projection is the L8 backstop.** `ledger_balances` is updated in the same transaction as the entries
   and carries `CHECK (balance_minor >= 0)` for accounts flagged non-negative (campaign payable, payout
   pending, held, reserve). Decreasing postings lock projection rows in ascending account-id order.
5. **Corrections are reversals** (`reverses_transaction_id`, at most once) plus replacements. Nothing is
   updated or deleted. Grants and triggers enforce this.
6. **Periods.** A posting whose `occurred_at` falls in a `CLOSED` period is rejected unless its rule allows
   prior-period adjustment.
7. `ledger.Post` runs inside the caller's DB transaction. Provider calls never happen inside it.
8. **Projection modes.** Each account has `projection_mode` `SYNC` or `DEFERRED`, set by its kind:
   - Obligation and recoverable accounts are `SYNC`: updated under a row lock in the posting transaction.
   - Platform-wide hot accounts (revenue, PSP clearing/settled, suspense, bank, expense) are `DEFERRED`: a
     fold job adds their entries later, and `ledger.v_balances_current` gives exact balances.

   A CHECK makes every account that must be non-negative `SYNC`, so L8 never depends on a deferred projection.
9. **New invariants** (amending LEDGER §4):
   - **L11:** entries may only be added in the transaction that created their journal.
   - **L12:** a journal's entries match its posting rule's account patterns.
   - **L13:** every obligation and recoverable account stays non-negative. This is broader than the four
     accounts named in point 4.
   - **L14:** the single-currency journal rule.
   - **P-1:** the period rule.

   Definitions are in [ledger-invariants.md](../architecture/ledger-invariants.md). The balance check runs
   once per journal at commit (a deferred trigger on `ledger_transactions`).

## Consequences

### Positive
- Invariants hold even under direct SQL as the app role, and the rule registry documents every allowed money
  movement.

### Negative / costs
- Trigger cost on every entry. The deferred check runs once per journal (optimisation is designed; its
  validation is in Stage 10).
- New money movements require a migration that adds a rule, which is deliberate.

### Follow-up work
- Stage 10 implements this. The Stage 8 payments work uses a test-only `ledger.Post` stub (PAYMENTS §6).

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Application-only checks | Violates DATABASE §1: the database must be the last line of defence |
| A stored procedure as the only posting path (`SECURITY DEFINER`) | Hides logic from Go tests and makes idempotency comparison awkward. Triggers plus grants give the same guarantees. Kept as an option for Stage 10 if trigger cost is too high. |
| Event-sourced balances only (no projection) | L8 needs a lockable row to serialise concurrent decreases |

## Security implications

Ledger writes are limited to INSERT; adjustments are maker-checker and audited. A compromised app role still
cannot create an imbalanced journal or move money outside a rule.

## Financial implications

Defines how every balance is produced. Account semantics follow Model A (ADR-013/014): the ledger records
claims and obligations, not FundZim custody.

## Related

ADR-006, ADR-014, ADR-018, ADR-030; [ledger-posting-model.md](../architecture/ledger-posting-model.md),
[ledger-invariants.md](../architecture/ledger-invariants.md), [ledger-schema.md](../database/ledger-schema.md).
