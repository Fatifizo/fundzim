# ADR-013: Regulatory operating model — PSP-mediated crowdfunding (Model A) for the MVP

- **Status:** Accepted (provisional — subject to LR-001/LR-004 and provider confirmation)
- **Date:** 2026-10-08
- **Deciders:** Technical lead (architecture); business owner and Zimbabwean counsel must confirm before Stage 20
- **Stage:** 1 (decision). Shapes Stages 2, 8, 9, 10, 11, 17 and 20.

## Context

FundZim must decide **who touches the money** between a donor and a beneficiary. Three models were evaluated
in [operating-model-decision.md](../compliance/operating-model-decision.md):

| Model | Summary |
|---|---|
| **A — PSP-mediated** | A suitably authorised payment service provider (PSP) collects, holds/safeguards, settles and disburses under contract. FundZim provides campaigns, orchestration, instructions, ledger records, risk, KYC workflow, reconciliation and reporting. |
| **B — Platform-controlled settlement account** | Donations land in an account FundZim controls (its merchant settlement account or a trust account) and FundZim pays beneficiaries from it. |
| **C — Direct beneficiary settlement** | Each verified beneficiary is onboarded with the PSP (sub-merchant / split beneficiary); collections settle directly to them, with the platform fee split off. |

Facts from Stage 1 research (sources, dates and confidence in
[regulatory-landscape.md](../compliance/regulatory-landscape.md); all interpretations are
**LEGAL_REVIEW_REQUIRED**):

- National Payment Systems Act [Chapter 24:23] s.18 restricts accepting payments on behalf of third parties
  as a regular feature of a business, with exceptions (participants in a recognised payment system, persons
  they introduce, duly appointed agents of the payee, ministerial exemption). Veritas consolidation, accessed
  2026-10-08, HIGH confidence on the text.
- RBZ Guidelines for Retail Payment Systems and Instruments (effective 1 July 2017) require authorisation to
  operate payment systems or issue payment instruments; e-money must be fully backed by a trust/escrow
  account and client funds segregated. Holding balances or offering campaign "wallets" resembles e-money
  issuance (LR-003).
- The Money Laundering and Proceeds of Crime Act defines a "financial institution" by activities such as
  transferring money or value and safekeeping or managing funds for others. Holding donor funds would increase
  the chance that FundZim falls within it (LR-007, LR-008).
- Provider research ([provider-capability-matrix.md](../payments/provider-capability-matrix.md)) found **no**
  provider with verified RBZ licensing evidence and no provider covering every need. Gateways commonly settle
  to the **merchant's** bank account; if FundZim were the merchant receiving all funds, that would be Model B
  in substance. Split settlement exists at Pesepay but requires each beneficiary to be an approved Pesepay
  merchant (Model C shape). Payout APIs found are narrow (e.g. EcoCash-only).
- The PVO Act as amended in 2025 restricts collecting contributions from the public for charitable objects
  to registered PVOs, excluded bodies or holders of a s.8 temporary authority, and also penalises a person who
  "instructs another person to collect" (LR-013; see [ADR-016](ADR-016-beneficiary-verification-before-payout.md)).
  This affects every model and is not solved by the choice of funds flow.

## Decision

1. **The MVP operating model is Model A (PSP-mediated), provisionally.** FundZim will not receive donor funds
   into any account it owns or controls. The PSP performs collection, holding/safeguarding, settlement and
   disbursement; FundZim instructs and records.
2. **A Model A provider must offer, by contract, one of:** (a) funds held by the PSP (or its trust/settlement
   arrangement) and disbursed to verified beneficiaries on FundZim's instruction; (b) sub-merchant /
   marketplace accounts per campaign or beneficiary; or (c) split settlement that routes the beneficiary share
   away from FundZim. Which providers offer this is **PROVIDER_CONFIRMATION_REQUIRED**
   ([provider-questions.md](../payments/provider-questions.md)).
3. **Only platform fee income** may settle to FundZim's own bank account (`asset:fundzim_operating_bank`,
   [ADR-014](ADR-014-accounting-separated-from-custody.md)).
4. **Model C is a permitted variant**, initially for KYB-verified organisations, where a provider confirms
   per-beneficiary onboarding or split settlement. It uses the same ledger and provider abstraction.
5. **Model B is not selected.** It must not be adopted — including "temporarily", or by accepting a provider
   whose only settlement option is FundZim's merchant account — without a superseding ADR, written legal
   sign-off (LR-001, LR-003, LR-004) and a safeguarding design (segregated/trust account, bank partner,
   reconciliation, audit).
6. **Fallback:** if no provider can support Model A or C, **launch is blocked**. The fallback is not Model B.
7. Model A does **not** transfer FundZim's own obligations to the PSP (AML programme, KYC of campaign owners
   and beneficiaries, data protection, consumer disclosures, PVO-related controls). Allocation is fixed in
   [financial-responsibility-matrix.md](../compliance/financial-responsibility-matrix.md) and by contract.

## Consequences

### Positive
- Minimises FundZim's exposure to NPS Act s.18, e-money and safeguarding requirements, and to "financial
  institution" status, pending counsel.
- No FundZim-held donor balances: the ledger records claims and obligations, not spendable stored value.
- Keeps the provider abstraction ([ADR-007](ADR-007-payment-provider-abstraction.md)) as the single place
  where provider differences live.

### Negative / costs
- Strong dependence on provider capabilities that are currently unverified; launch may be delayed or blocked.
- Reconciliation is harder: FundZim must evidence balances held by a third party (statement ingestion,
  three-way match — [settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md) §7).
- Rail coverage may be narrower than "all rails at launch" if the Model A provider lacks some rails.
- Platform-fee collection depends on the provider remitting FundZim's share.

### Follow-up work
- Counsel: LR-001, LR-003, LR-004, LR-007, LR-008, LR-013 ([open-legal-questions.md](../compliance/open-legal-questions.md)).
- Provider due diligence ([provider-due-diligence-checklist.md](../payments/provider-due-diligence-checklist.md)).
- Stage 2: data model for provider-held balances, settlement batches and per-provider accounts.
- Stage 9: contract a provider that satisfies Decision 2; sandbox only.
- Stage 20: confirm the model is permitted before any live money.

## Alternatives considered

| Alternative | Why not selected now |
|---|---|
| Model B — FundZim settlement or trust account | Appears to fall within NPS Act s.18 restrictions and to resemble e-money/stored value; would likely require authorisation, safeguarding (trust account, trustees, reconciliation), a banking partner and increased AML status. Not permitted without legal confirmation. |
| Model C for everyone at launch | Requires every beneficiary (including individuals raising medical or funeral funds) to be onboarded as a PSP merchant — heavy friction, unverified provider support, and per-beneficiary KYB at the PSP. Kept as a variant for organisations. |
| Hybrid A + B ("hold briefly, then pay") | Has Model B's legal profile while appearing to be A. Rejected explicitly. |

## Security implications
- FundZim never holds bank or wallet credentials capable of moving donor funds; provider API credentials
  that can instruct payouts are C4 SECRET, scoped per environment and protected by maker-checker controls on
  payout approval ([ADR-017](ADR-017-payout-approval-segregation-of-duties.md)).
- Payout instruction is the highest-value attack target (account takeover, destination hijack, insider
  abuse); controls are in [payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md).

## Financial implications
- The ledger is a record of funds held by the PSP on behalf of campaigns, not a statement of FundZim-owned
  cash ([ADR-014](ADR-014-accounting-separated-from-custody.md)); accounting treatment is LR-002.
- Platform fees are revenue; their cash arrives only through provider remittance.
- Reconciliation against provider statements (Stage 17) is mandatory before live payouts (Gate B).

## Related
[operating-model-decision.md](../compliance/operating-model-decision.md),
[financial-responsibility-matrix.md](../compliance/financial-responsibility-matrix.md),
[funds-flow-architecture.md](../payments/funds-flow-architecture.md),
[provider-comparison.md](../payments/provider-comparison.md),
[licensing-assessment.md](../compliance/licensing-assessment.md),
ADR-006, ADR-007, ADR-014, ADR-016, ADR-017. LEGAL_REVIEW_REQUIRED: LR-001, LR-002, LR-003, LR-004, LR-007,
LR-008, LR-013.
