# ADR-006: Double-entry ledger requirement

- **Status:** Accepted
- **Date:** 2026-10-08
- **Stage:** 0 (design), implemented Stage 10

## Context

Mutable balance columns (`campaign.balance += donation`) lose history, are vulnerable to lost updates under
concurrency, and cannot be audited or reconciled against provider statements. FundZim must be able to answer
who, what, when, why, which provider and which ledger entries for every financial event, and must survive a
financial audit.

## Decision

FundZim will use a **double-entry ledger** in PostgreSQL. Only the `ledger` module writes it. The tables are:

- `ledger_accounts`: code, type `ASSET | LIABILITY | EQUITY | REVENUE | EXPENSE`, currency, owner reference
  (e.g. `campaign_id`), normal balance.
- `ledger_transactions`: the journal header. It holds:
  - type;
  - `idempotency_key UNIQUE`;
  - `occurred_at` and `posted_at`;
  - description;
  - source references (`payment_id`, provider, `provider_reference`);
  - `created_by`;
  - `reverses_transaction_id`.
- `ledger_entries`: transaction, account, `DEBIT | CREDIT`, `amount_minor > 0`, currency.

**Invariants**, enforced in Go **and** in the database (a deferred constraint trigger checks balance at commit):

1. For every transaction **and every currency**, sum(debits) = sum(credits).
2. Each entry's currency equals its account's currency.
3. A transaction has at least two entries, and every entry has `amount_minor > 0`.
4. **Append-only.** The application role has no UPDATE, DELETE or TRUNCATE grant on journal tables, and triggers block mutation (including TRUNCATE).
   Corrections are made only by reversing or adjusting transactions that reference the original.
5. Balances are **derived** from entries. Materialised balance projections are allowed only if they are updated
   in the same DB transaction as the postings and can be verified by a full recompute (nightly and on demand).
6. Postings are triggered only by **confirmed provider state** (verified webhook, authenticated status API or
   reconciliation), never by browser redirects or client claims.

**Custody caveat:** the ledger records FundZim's view of funds orchestrated through licensed PSPs. Account names
must not imply that FundZim holds customer funds; use names like "PSP settlement receivable" and "Campaign
payable to beneficiary". Whether these are FundZim balance-sheet items is **LEGAL_REVIEW_REQUIRED** (LR-001, LR-002) and needs
accounting review.

## Consequences

### Positive
- Full, immutable financial history, and `TOTAL DEBITS == TOTAL CREDITS` can be checked continuously.
- Natural support for fees, refunds, chargebacks, payout holds and reconciliation.
- Concurrency-safe: posting only inserts rows, so there are no read-modify-write races on balance columns.

### Negative / costs
- More complex than a balance column. Developers need training, and every financial feature needs posting rules
  plus tests.
- Balance queries need indexing and projections as volume grows.
- A wrong posting cannot be "fixed" in place. It needs a reversal, which is intentional.

### Follow-up work
- Stage 2: chart of accounts and posting rules for each flow.
- Stage 10: implementation.
- Stage 17: reconciliation.
- Stage 19: invariant and concurrency tests.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Mutable balance columns | Explicitly prohibited. No audit trail, and race-prone. |
| Single-entry transaction log | No balancing check, so errors go undetected. |
| External ledger SaaS | Adds a third party holding financial records (data residency and LEGAL_REVIEW_REQUIRED (LR-011)) and network dependency in the posting path. |
| Dedicated ledger DB (TigerBeetle) | See ADR-004. Possibly later. |

## Security implications
Append-only grants and triggers protect against tampering by compromised application code. The ledger is
classified C2 CONFIDENTIAL, and access to adjustments requires `ledger.adjustment.create` plus maker-checker
approval. ADMIN and SUPER_ADMIN do not get this permission implicitly.

## Financial implications
This is the core financial control. Any detected imbalance is a severity-1 incident: page immediately, halt the
affected postings and payouts, investigate. It is never silently corrected.

## Related
ADR-004, ADR-005, ADR-007, ADR-010, [LEDGER.md](../LEDGER.md), [PAYMENTS.md](../PAYMENTS.md),
[COMPLIANCE.md](../COMPLIANCE.md).
