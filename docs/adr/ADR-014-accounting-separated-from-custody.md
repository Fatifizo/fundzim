# ADR-014: Separation of financial accounting from custody (settlement-aware ledger)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead; accounting treatment to be confirmed by a qualified accountant (LR-002)
- **Stage:** 1 (design). Implemented in Stage 10; reconciliation in Stage 17.

## Context

[ADR-006](ADR-006-double-entry-ledger.md) requires a double-entry ledger. Stage 0's chart of accounts
([LEDGER.md](../LEDGER.md) §3.2) posted a captured donation straight into `asset:psp_receivable:{p}` and
`liability:campaign_payable:{c}`, so a captured-but-unsettled donation was immediately "available".

Under the Model A operating model ([ADR-013](ADR-013-regulatory-operating-model.md)) the PSP, not FundZim,
holds the money. That creates two needs Stage 0 did not cover:

1. **Distinct financial states.** Initiated, authorised, captured, settled, eligible (available), reserved,
   paid out, refunded and reversed are different states and must never collapse into one "campaign balance".
   Paying out captured-but-unsettled funds would expose the platform to settlement failures and reversals.
2. **Custody honesty.** FundZim's ledger must stay auditable while recording money it does not hold, and must
   not imply ownership (LR-001, LR-002) or create spendable stored value (LR-003).

## Decision

1. **The ledger is FundZim's record of funds orchestrated through, and held by, licensed providers.** It is a
   system of record for claims and obligations, not a statement of FundZim-owned cash. `asset:psp_*` accounts
   are control/memorandum accounts; whether they appear on FundZim's balance sheet is LR-002, and the ledger
   must support both presentations (fund-accounting view and balance-sheet view).
2. **Initiated and authorised states post nothing.** They live on the payment record only. The first posting
   happens at authoritative capture (`SUCCEEDED`).
3. **Settlement-aware chart of accounts** (per currency), superseding Stage 0 LEDGER §3.2 naming:

   | Change vs Stage 0 | Account |
   |---|---|
   | **Renamed** `asset:psp_receivable:{p}` → | `asset:psp_clearing:{p}` — captured, not yet settled (claim on PSP) |
   | **New** | `asset:psp_settled:{p}` — settled per PSP statement, held by the PSP for disbursement |
   | **New** | `asset:fundzim_operating_bank` — FundZim's own bank account; **only** platform fee income lands here |
   | **New** | `liability:campaign_unsettled:{c}` — net donations captured, not yet settled |
   | Kept (meaning narrowed) | `liability:campaign_payable:{c}` — settled **and released** = available for payout |
   | **New** | `liability:campaign_reserve:{c}` — rolling reserve / holdback, not available |
   | Kept | `liability:campaign_payout_pending:{c}` — reserved for a payout request |
   | **New** | `liability:payout_in_transit:{p}` — submitted to provider, outcome not final (incl. UNKNOWN) |
   | Kept | `liability:campaign_held:{c}`, `liability:refund_payable`, `revenue:platform_fees`, `expense:psp_processing_fees`, `expense:chargeback_losses`, `asset:chargeback_recoverable` |
   | **New** | `asset:refund_recoverable:{c}`, `expense:refund_losses` — refund shortfalls kept separate from chargebacks |
   | **Renamed** `suspense:unmatched:{p}` → | `suspense:settlement_discrepancy:{p}` |
   | **Replaced** `asset:settlement_bank:{account}` → | `asset:fundzim_operating_bank` for Model A; a donor-funds settlement/trust account is reintroduced only by a Model B ADR |
   | Kept, **not used in Model A** | `asset:psp_payout_float:{p}` — only if a provider requires prefunding (LR-001/LR-003) |
   | Kept | `equity:platform` |

   The authoritative chart and worked journals are in
   [settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md).
4. **Funds become available only after settlement is matched.** Capture credits `campaign_unsettled`;
   a reconciliation-confirmed settlement moves the provider claim from `psp_clearing` to `psp_settled`; a
   separate release journal moves the campaign's net amount from `campaign_unsettled` to `campaign_payable`
   (optionally splitting into `campaign_reserve`). Payout eligibility reads only `campaign_payable`.
5. **No balance is spendable stored value.** Ledger balances cannot be transferred between users or
   campaigns, spent on the platform, or withdrawn except through the payout lifecycle to a verified
   destination.
6. **Three-way match is the audit basis:** payment record ↔ provider report/statement ↔ ledger. Differences
   post to `suspense:settlement_discrepancy:{p}` and open a reconciliation case; they are never absorbed by
   editing postings.

## Consequences

### Positive
- Payouts cannot draw on unsettled money: structurally, not only by a code check.
- The ledger reflects provider custody honestly and can be audited against provider statements.
- Supports Model C and a future Model B through account deltas rather than redesign.

### Negative / costs
- More journals per donation (capture, PSP fee, settlement, release) and dependence on timely provider
  settlement reports (PCR-007 — settlement report format and timing).
- Owners see funds as "processing" until settlement, which may be T+1 to T+3 or longer depending on the
  provider (published timelines vary; see [provider-capability-matrix.md](../payments/provider-capability-matrix.md)).

### Follow-up work
- Lead updates [LEDGER.md](../LEDGER.md) §3.2 and §6 to point at the new chart.
- Stage 2: tables for settlement batches, settlement lines and match results.
- Stage 10/17: implement postings and reconciliation.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep Stage 0's capture-is-available model | Allows payouts against unsettled or later-reversed funds. |
| Track settlement only on payment rows, not the ledger | Loses the double-entry audit trail for the most important custody boundary. |
| Model provider-held funds as FundZim cash | Misstates custody; contradicts ADR-013 and LR-001. |

## Security implications
- The release journal is a privileged system action driven only by reconciliation results; it cannot be
  triggered from any user-facing endpoint.
- Manual ledger adjustments remain maker-checker ([ADR-017](ADR-017-payout-approval-segregation-of-duties.md)).

## Financial implications
Every journal balances per currency (verified in the worked examples). Available balance = `campaign_payable`
only. Discrepancies are visible in suspense accounts that must trend to zero and are monitored.

## Related
[settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md), [LEDGER.md](../LEDGER.md),
[funds-flow-architecture.md](../payments/funds-flow-architecture.md), ADR-006, ADR-010, ADR-013, ADR-018.
LEGAL_REVIEW_REQUIRED: LR-001, LR-002, LR-003.
