# Financial Responsibility Matrix

**Stage 1. Status: DESIGN ASSUMPTION under the provisional Model A** ([operating-model-decision.md](operating-model-decision.md),
[ADR-013](../adr/ADR-013-regulatory-operating-model.md)). Allocations to parties other than FundZim are
**assumptions to be fixed by contract**:
- **PCR** = must be confirmed by the provider in writing ([provider-questions.md](../payments/provider-questions.md));
- **LR** = depends on a legal question ([open-legal-questions.md](open-legal-questions.md)).

Nothing here is legal advice.

> **A PSP does not automatically assume FundZim's compliance obligations.** The PSP's own regulatory
> obligations (e.g. as a recognised/authorised provider — R1-04, R1-06) sit alongside FundZim's. FundZim's own
> obligations stay FundZim's unless a contract and the law both say otherwise. These include customer
> verification of campaign owners and beneficiaries, transaction monitoring on its platform, record-keeping,
> data protection as a controller, possible AML reporting (LR-008) and consumer-facing disclosures. Any
> reliance on the PSP's KYC or monitoring must be documented, contractually agreed, and confirmed lawful
> (LR-007, LR-008).

Related: [funds-flow-architecture.md](../payments/funds-flow-architecture.md),
[settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md),
[operational-controls.md](operational-controls.md).

---

## 1. Legend

| Code | Meaning |
|---|---|
| **R** | Responsible: performs the activity |
| **A** | Accountable: answerable for the outcome (one per row where possible) |
| **C** | Consulted / provides input |
| **I** | Informed |
| **—** | No role |
| **⚑** | Assumed under Model A; to be fixed by contract (PCR) and/or legal advice (LR) |

Parties: **FZ** = FundZim · **PSP** = payment service provider · **BANK** = the PSP's settlement/trust bank and
the beneficiary's bank · **DON** = donor · **OWN** = campaign owner · **BEN** = beneficiary (may be the owner;
see [beneficiary-verification.md](beneficiary-verification.md)).

## 2. Matrix

| # | Responsibility | FZ | PSP | BANK | DON | OWN | BEN | Notes |
|---|---|---|---|---|---|---|---|---|
| 1 | **Collection** (accepting the donor's payment) | C (initiates intent, shows checkout) | **R/A** ⚑ | R (card/wallet scheme & issuer side) | R (authorises payment) | — | — | FundZim never receives donor funds (Model A). PSP licensing evidence PCR; LR-004. |
| 2 | **Custody / safeguarding** of donor funds | I (records only) | **R/A** ⚑ | R (holds the PSP's trust/settlement account) ⚑ | — | — | — | Trust/segregation per R1-06 paras 16–17 would bind the PSP if it is an e-money issuer/operator. Contract must state where funds sit (PCR-003 — custody). LR-001. |
| 3 | **Settlement** (moving collected funds into the PSP pool / to accounts) | C (reconciles) | **R/A** ⚑ | R | — | — | — | Timelines provider-specific (e.g. Paynow T+1–T+3, Pesepay T+2 after threshold — R3). |
| 4 | **Payout execution** (disbursing to beneficiary) | **A** for the *instruction* (eligibility, approval, correct destination) | **R** for execution ⚑ | R (receiving bank/wallet) | — | C (requests, nominates destination) | I (receives) | FundZim is accountable that every instruction passed eligibility and maker-checker ([payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)). Executing is the PSP's. LR-075 (→ LR-036). |
| 5 | **Payment fees** (processing fees) | A (fee disclosure, ledger) | R (charges) | R (scheme/bank fees) | C/R (if donor covers fees, PD-01) | C/R (if campaign bears, PD-01) | — | Who bears what is PD-01 / LR-028. PSP fee changes may need RBZ approval (S.I. 80/2020 s5 — R1-04). |
| 6 | **Refund execution** | **A** (decision, approval, ledger) | **R** (executes, if supported) ⚑ | R | C (requests) | C (consulted where policy requires) | I | Refund API availability varies (Payonify full refunds only; others unconfirmed — R3). Manual path in [refund-and-reversal-flows.md](../payments/refund-and-reversal-flows.md) §3.6. |
| 7 | **Chargebacks** (card) | A (case handling, evidence, recovery) | R (scheme process, debits pool) ⚑ | R (issuer/acquirer) | R (initiates with issuer) | C (provides evidence) | C/R (recovery, LR-080) | Final loss allocation after payout is LR-020 / PD-34. |
| 8 | **Failed payouts** | **A** (detect, return funds to available, notify, retry as a new payout) | R (reports authoritative status) | R (rejects/returns) | — | I / C (corrects destination) | I | UNKNOWN is never treated as failed ([payout-lifecycle.md](../payments/payout-lifecycle.md) §5). |
| 9 | **Payment disputes** (non-card: wrong amount, unauthorised wallet debit, "I didn't donate") | **A** (intake, investigation, outcome) | R (rail-level investigation/reversal) ⚑ | C | R (raises) | C | I | Complaint routes and timelines LR-086. |
| 10 | **Transaction monitoring** | **R/A** for platform-level monitoring (campaign/donor/payout behaviour) | R/A for rail-level monitoring under its own obligations | R (own obligations) | — | — | — | Both monitor; neither replaces the other. Whether FundZim is a reporting institution is LR-008. [transaction-monitoring.md](transaction-monitoring.md). |
| 11 | **Customer verification** (KYC/KYB) | **R/A** for campaign owners, organisations, beneficiaries, payout destinations | R for its own merchant(s) and, where applicable, beneficiaries it onboards (e.g. split beneficiaries) ⚑ | R (account holders) | R (provides data if required) | R (provides data) | R (provides data/consent) | Reliance on PSP KYC only by documented agreement + LR-007. [kyc-architecture.md](kyc-architecture.md). |
| 12 | **Regulatory reporting** (e.g. suspicious transaction reports, data-protection notifications, tax) | **A** for its own obligations (LR-008, LR-010, LR-016) | A for its own | A for its own | — | — | — | Tipping-off rules may restrict what FundZim may tell users (LR-008). |
| 13 | **Reconciliation** | **R/A** (three-way match, discrepancy resolution) | R (provides statements/reports) ⚑ | C | — | — | — | [settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md) §7. Report formats PCR. |
| 14 | **Record retention** | **R/A** for platform, KYC, ledger, audit records | R/A for its own (e.g. 10 years under R1-06 para 9.2 for providers; 7 years for S.I. 80 returns — R1-04) | R/A own | — | — | — | FundZim periods pending LR-012. Conservative default: 10 years for transaction records (R1-22 interpretation, not a legal conclusion). |

## 3. Responsibilities FundZim cannot delegate

Whatever the contract says, these stay with FundZim because they rest on FundZim's own knowledge and
decisions:

1. Deciding whether a campaign may be published, and whether it is suspended or frozen.
2. Deciding whether a payout instruction is issued (eligibility, maker-checker, holds).
3. Verifying campaign owners, beneficiaries and payout-destination ownership, even if the PSP also verifies.
4. Monitoring campaign, donor and payout patterns on FundZim (the PSP sees payments, not campaigns).
5. Accurate donor-facing disclosures (fees, IMTT where applicable — LR-077 (→ LR-059), refund policy).
6. Protecting the personal data FundZim collects, as a data controller (LR-010).
7. Keeping its own ledger, audit and evidence records.

## 4. Responsibilities FundZim must not assume by accident

| Risk | How it happens | Guard |
|---|---|---|
| Becoming the custodian | Configuring the PSP to settle donations into FundZim's account (Model B in substance) | Routing refuses `custody_model = MERCHANT_SETTLEMENT` ([operating-model-decision.md](operating-model-decision.md) §9) |
| Becoming the payer of last resort | Funding chargeback/refund shortfalls by default | Pool funding only via approved `*:funding` journals; policy PD-34; LR-076 |
| Becoming the guarantor to donors | Marketing language such as "guaranteed" or "protected" | No guarantee claims unless PD-34 adopts one, with legal review |
| Becoming an issuer of stored value | Showing users a "balance" they can move, spend or transfer | No wallets ([settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md) §3.3); LR-003 |
| Becoming an FX dealer | Converting USD↔ZWG for campaigns | No FX; only authorised dealers convert (R1-13) — [currency-and-fx-policy.md](../payments/currency-and-fx-policy.md) |

## 5. Contract checklist derived from the matrix

The PSP agreement must address each ⚑ row. Items are tracked in
[provider-due-diligence-checklist.md](../payments/provider-due-diligence-checklist.md):

- custody and segregation of donor funds, and the bank holding them (rows 1–3);
- disbursement on FundZim's authenticated instruction; the instruction format, idempotency and status reporting (row 4);
- fee schedule and change notice (row 5);
- refund execution, timelines and fee treatment (row 6);
- chargeback process, notifications, evidence deadlines and debit timing (row 7);
- failed-payout reporting and return of funds (row 8);
- the PSP's own monitoring and KYC, and what FundZim may rely on (rows 10–11);
- the reporting each party owes the other; incident notification (row 12);
- statement and settlement report formats and delivery (row 13);
- retention, audit rights and data protection terms (row 14; LR-033).
