# Stage 17 — Financial adjustment requests

## 1. Model

A financial adjustment from reconciliation is a resolution of type LEDGER_ADJUSTMENT. It is never a direct ledger write.

| Resolution state | Ledger adjustment state | Meaning |
|---|---|---|
| PENDING_REVIEW | PENDING_APPROVAL | maker proposed; lines checked by the ledger at request (rule, accounts, currency) |
| APPROVED (transient) | PENDING_APPROVAL → POSTED | checker approved; `ledger.ApproveAdjustment` posts the journal |
| EXECUTED | POSTED | journal posted; exception RESOLVED |
| REJECTED | REJECTED | checker rejected |
| CANCELLED | WITHDRAWN | maker cancelled |
| EXPIRED | EXPIRED (ledger's own 24 h) | nobody decided |

The prompt's suggested DRAFT state is not modelled: a proposal is immutable from the moment it is filed (it is hashed),
and a maker composes it in the form before filing.

## 2. Accounting rules

- Only named rules that the ledger marks `requires_approval`: `SETTLEMENT_DISCREPANCY_RESOLVED`, `ADJUSTMENT`,
  `WRITE_OFF` (reconciliation refuses any other; the ledger validates the lines against the rule's allowed accounts).
- Two to twenty lines; integer minor units; one currency — the exception's currency; debits = credits (ledger).
- Reason code `RECON_DISCREPANCY`, linked subject `reconciliation_exception:<id>`, justification and evidence references.
- The resulting journal is a new journal; nothing is edited; reversal of an adjustment is another adjustment.

## 3. Production safety

`ProductionAdjustmentsEnabled = false`: a LEDGER_ADJUSTMENT proposal is refused unless the ledger is TEST
(`PRODUCTION_ADJUSTMENTS_DISABLED`), on top of the ledger's own `ProductionPostingEnabled = false`. Enabling requires:
approved accounting rules (KI-S10-02), legal review (LR-001/002/003/076), provider dependencies resolved (ADR-040),
owner authorisation recorded and operational controls certified — and an ADR.

## 4. Tested

`TestReconciliationBankStatementsAndLedgerAdjustment`: maker proposes Dr `suspense:settlement_discrepancy` / Cr
`asset:psp_settled` (35 minor units, SETTLEMENT_DISCREPANCY_RESOLVED); the checker approves; the ledger adjustment is
POSTED with a resulting journal approved by the checker; the trial balance of every currency stays balanced.
