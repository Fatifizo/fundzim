# ADR-030: Financial reconciliation architecture

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead (Stage 2); pending project-owner acceptance
- **Stage:** 2 (design); implementation Stage 17 (settlement matching needed earlier for fund release — see below)

## Context

Under Model A the provider holds the money. FundZim's ledger is credible only if it is reconciled
continuously against provider statements (SC-3). Funds become available only after a settlement match
(ADR-014). Reconciliation therefore sits on the critical path of payouts, not just in back-office reporting.

## Decision

1. `reconciliation` is a first-class module with its own schema `recon`. It owns sources, imports, runs,
   normalised items, matches, discrepancies, resolutions, settlement batches and settlement items.
2. **Imports are immutable and deduplicated** by file or content hash and by external line id. Each import
   references an evidence record.
3. **Matching** links an item to a subject (payment intent, refund, payout or fee) by id plus the provider
   reference, and to the ledger transaction it confirms. Matches are recorded, never inferred at query time.
4. **Discrepancy types:**
   - missing on either side;
   - duplicate;
   - amount, currency, fee, payout or status mismatch;
   - settlement short or over.

   Each discrepancy has a severity, an owner queue and age-based escalation (SC-4).
5. **Postings:** reconciliation posts only through named ledger rules (`SETTLEMENT_MATCHED`,
   `SETTLEMENT_DISCREPANCY`, resolution adjustments). Any manual resolution that moves money is a
   maker-checker `ledger_adjustment` with evidence.
6. Payouts depend on reconciliation freshness (EC-20). A SEV1 discrepancy places a `PROVIDER_HOLD` for that
   provider and currency.
7. **Staging:** settlement matching (the minimum needed to release funds) is built with the ledger in Stage 10
   and payouts in Stage 11. Full reconciliation (all discrepancy types, bank statements, reporting) is built in
   Stage 17.

## Consequences

### Positive
- The ledger is auditable against external truth. No funds are released without a settlement match.

### Negative / costs
- Payout availability depends on provider statement timeliness (PD-15).

### Follow-up work
- ROADMAP note: a settlement-matching subset is needed before Stage 11 acceptance.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Reconciliation as an offline spreadsheet process | Not auditable; cannot gate payouts |
| Release funds at capture | Contradicts ADR-014 (captured ≠ settled ≠ available) |
| Reconciliation inside `payments` | Mixes concerns and needs payouts data (forbidden import) |

## Security implications

Statement files may contain personal data. They are stored as evidence in the private bucket, with access
audited.

## Financial implications

The core control for SC-2/SC-3. Discrepancies never release money to campaigns until explained.

## Related

ADR-014, ADR-023; [reconciliation-schema.md](../database/reconciliation-schema.md); settlement-and-custody-model §7.
