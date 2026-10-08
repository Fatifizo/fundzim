# FundZim — Compliance Framework & Assumptions Register

> **Status:** Stage 0 framework, updated at Stage 1. **Nothing in this document is legal advice or a legal
> conclusion.**
> Every regulatory statement below is an *assumption* or *open question* to be confirmed by qualified
> Zimbabwean counsel (and, where relevant, counsel in donor jurisdictions). Items needing a determination are
> tagged `LEGAL_REVIEW_REQUIRED` and tracked in the Compliance Assumptions Register, which moved in Stage 1
> to [compliance/open-legal-questions.md](compliance/open-legal-questions.md) (see §5).
>
> FundZim does **not** claim to be licensed, approved, registered, PCI DSS compliant, or compliant with any
> data-protection, AML/CFT or charity law. Those are outcomes to be earned and evidenced, not asserted.

Related: [PRODUCT.md](PRODUCT.md) · [PAYMENTS.md](PAYMENTS.md) · [LEDGER.md](LEDGER.md) ·
[PRIVACY.md](PRIVACY.md) · [DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md) · [SECURITY.md](SECURITY.md) ·
[ROADMAP.md](ROADMAP.md). Stage 1 documents are indexed in §6.

---

## 1. Purpose

FundZim orchestrates real people's money across mobile money, bank and card rails, for Zimbabwean
beneficiaries and donors inside and outside Zimbabwe. That places it adjacent to (and possibly inside) several
regulated domains: payment systems, exchange control, AML/CFT, data protection, charitable fundraising and tax.

This document:

1. Defines the **regulatory boundary** FundZim is designing around.
2. Describes the **compliance framework** — the controls the architecture must be able to support regardless
   of the final legal answers.
3. Points to the **authoritative Compliance Assumptions Register** (`LR-xxx`), which lives in
   [compliance/open-legal-questions.md](compliance/open-legal-questions.md) from Stage 1 onward. Other documents
   reference these IDs rather than restating assumptions.

The engineering rule is simple: **when a feature depends on an open `LR` item, the feature is designed so the
answer can be applied as configuration or policy, and the feature does not go to production until the item is
closed.** Open items must never be silently converted into facts in code, copy, or documentation.

---

## 2. Regulatory boundary

> **Stage 1 update.** The regulatory boundary below was tested against Stage 1 research. Findings, sources
> and confidence levels are in [compliance/regulatory-landscape.md](compliance/regulatory-landscape.md) and
> [compliance/regulatory-requirements-register.md](compliance/regulatory-requirements-register.md)
> (`REQ-xxx`). The preferred operating model (PSP-mediated, Model A) is recorded in
> [ADR-013](adr/ADR-013-regulatory-operating-model.md) and
> [compliance/operating-model-decision.md](compliance/operating-model-decision.md). Where §3 below says
> "Instruments to review", the Stage 1 findings for that instrument are in the landscape document. The most
> significant new finding concerns the Private Voluntary Organisations Act as amended in 2025 (who may collect
> contributions from the public), tracked as LR-046 – LR-050.

### 2.1 Preferred money flow

```
DONOR
  │  pays using EcoCash / OneMoney / InnBucks / O'Mari / ZimSwitch / bank / card (target rails)
  ▼
LICENSED PAYMENT SERVICE PROVIDER (PSP)           ← holds/moves funds under its own licence
  │  settlement per PSP agreement & approved flow
  ▼
APPROVED SETTLEMENT / PAYOUT FLOW                 ← structure LEGAL_REVIEW_REQUIRED (LR-001, LR-004)
  │
  ▼
BENEFICIARY (campaign owner / verified organisation / verified beneficiary account)
```

FundZim sits **beside** this flow as the orchestrator and system of record, not (by assumption) as a holder of
funds.

### 2.2 What FundZim does (by design)

| Function | Notes |
|---|---|
| Campaign management | Creation, review, publication, lifecycle, suspension, freezing. |
| Transaction orchestration | Initiates payments with PSPs via the provider abstraction; never captures card data. |
| Payment initiation | Hosted/tokenised PSP flows; FundZim stores references and states, not instruments. |
| Transaction records | Payment intents, provider references, webhook inbox, status history. |
| Ledger / accounting | Double-entry record of FundZim's *view* of orchestrated funds (see LR-002). |
| Risk management | Risk scoring, velocity limits, holds, investigations. |
| KYC workflow | Collecting and reviewing verification evidence, possibly via a vendor (vendor not chosen). |
| Payout orchestration | Requesting payouts through PSP/bank under maker-checker controls. |
| Reconciliation | Matching internal records to PSP/bank statements. |
| Platform fee calculation | Fee rules, fee records; collection mechanism depends on LR-001/LR-016. |
| Reporting | Operational, financial, and (if required) regulatory reporting. |

### 2.3 What FundZim explicitly does NOT do (unless a future, documented legal determination says otherwise)

- **Does not hold customer funds** in FundZim-owned accounts (LR-001).
- **Does not operate a stored-value wallet** or let users keep spendable balances on the platform (LR-003).
  Ledger "payable" balances are records of amounts due to beneficiaries via the approved payout flow, not
  wallets — no peer-to-peer transfers, no spending, no top-ups.
- **Does not act as a licensed payment service provider**, bank, money transfer operator or bureau de change
  (LR-004).
- **Does not perform currency conversion** between USD and ZiG (ZWG) or any other currency (LR-005, LR-006).
- **Does not offer investment, equity, debt, lending, interest-bearing or profit-sharing crowdfunding.** Only
  donation-based, reward-free campaigns are in scope. Anything else requires separate regulatory analysis.
- **Does not store raw card data** (PAN/CVV). Card flows are hosted/tokenised by PSPs. No PCI DSS compliance
  is claimed; the architectural goal is to minimise cardholder-data scope.
- **Does not make tax-deductibility claims** on behalf of campaigns or donors (LR-017).
- **Does not issue legal or financial advice** to campaign owners or donors.

---

## 3. Compliance framework

The controls below are architectural requirements now, even where thresholds and exact obligations are open.
Each is built to be **policy-driven** (thresholds, lists, retention periods in configuration or reference
tables, versioned and audited), so legal answers can be applied without redesign.

### 3.1 AML/CFT

Instruments to review (not conclusions): Money Laundering and Proceeds of Crime Act; Financial Intelligence
Unit (FIU) guidance and directives; RBZ AML/CFT guidelines applicable to payment system participants.
Whether FundZim is itself a designated/accountable institution, or relies on PSPs' obligations, is open
(LR-008).

Architectural requirements regardless of outcome:

- **Risk-based approach** — every user, organisation, campaign, payment and payout can carry a risk score
  and risk flags (`internal/risk`, Stage 13).
- **Transaction monitoring hooks** — velocity, structuring/smurfing patterns (many small donations from one
  source), unusual cross-border patterns, rapid in-out (donation then immediate payout request), round-trip
  (donor ≈ beneficiary), repeated failed payments, payout destination churn.
- **Holds and freezes** — payout holds at user/campaign/destination level and the `FROZEN` campaign state
  (see [PRODUCT.md](PRODUCT.md)) are first-class, auditable controls.
- **Case management** — investigations with notes (classified C3 RESTRICTED), assignments, outcomes, and
  evidence links; access restricted to COMPLIANCE.
- **No tipping-off** — UI and notifications must be able to suppress or neutralise messaging when an account
  is under investigation. Support staff see a generic status, not the investigation (LR-008).

### 3.2 KYC / CDD

Verification levels (defined in [PRODUCT.md](PRODUCT.md) §7):

| Level | Evidence (indicative) | Default gate (configurable; thresholds LR-007) |
|---|---|---|
| `UNVERIFIED` | none | May donate, subject to risk limits. |
| `BASIC_VERIFIED` | email + phone OTP | May create draft campaigns. |
| `IDENTITY_VERIFIED` | National ID or passport, liveness/manual review, DOB, legal name | May submit/publish campaigns. |
| `PAYOUT_VERIFIED` | payout account ownership verified, name match to verified identity | May withdraw. |

- **Organisations**: name, type, registration information, directors/trustees, beneficial owners where
  applicable, authorised representative and proof of authority, payout account in the organisation's name,
  supporting documents (LR-013 governs what is required for charitable/PVO fundraising).
- **Enhanced due diligence** triggers (to be confirmed, LR-007/LR-009): PEP match, high cumulative amounts,
  high-risk jurisdictions, adverse media, organisation fundraising.
- **Donor due diligence**: donors are not KYC'd by default. Whether and at what thresholds donor
  identification is required (especially cross-border) is open (LR-007, LR-005).
- **Re-verification**: on change of legal name, payout destination, or expiry of identity documents.
- KYC data storage separation per ADR-009 and [SECURITY.md](SECURITY.md); verification vendor not chosen.

### 3.3 Sanctions & PEP screening

- Screen campaign owners, organisation directors/trustees/beneficial owners, beneficiaries and payout account
  holders at onboarding, at payout, and on list updates (rescreen).
- Donor screening scope (all donors vs. above thresholds vs. cross-border only) is open (LR-009).
- Lists to apply are open (LR-009): candidates to review include UN Security Council consolidated list,
  Zimbabwean domestic designations, and lists relevant to PSP/correspondent bank requirements (e.g. OFAC,
  UK, EU) — PSP contractual requirements may be stricter than local law.
- Matches create cases; no automatic rejection without review except confirmed exact matches per policy.
- Screening vendor not chosen; screening results and list versions are recorded for audit.

### 3.4 Suspicious transaction escalation path

```
Automated rule / staff observation / PSP notice / external report
        │
        ▼
Alert (risk module) ──► Triage by COMPLIANCE (not SUPPORT)
        │
        ├─► Dismiss with documented reason (audited)
        │
        └─► Case opened ──► Protective action as needed
                              (payout hold, campaign SUSPENDED/FROZEN)
                    │
                    ▼
            Compliance decision ──► External report, if required (LR-008)
                                    (format/channel/timing per FIU rules — to confirm)
```

- Who is FundZim's designated compliance officer/MLRO equivalent is an organisational decision (LR-008).
- Reports, decisions and the fact of a report are C3 RESTRICTED and never visible to the subject.

### 3.5 Record keeping

- Financial records (payments, ledger, payouts, refunds, reconciliation), KYC records, audit events and
  compliance cases must be retained for legally required periods — **periods are open (LR-012)**.
- Ledger and audit tables are append-only (ADR-006, [AUDIT.md](AUDIT.md)); data subject deletion requests
  cannot erase records under legal retention — they are restricted/anonymised where lawful
  ([PRIVACY.md](PRIVACY.md)).
- Retention is implemented as policy per data category (configurable), never hard-coded numbers in code.

### 3.6 Exchange control & currency

Instruments to review: Exchange Control Act and regulations; RBZ exchange control directives and monetary
policy statements; rules on USD and ZiG (ZWG) usage and settlement.

- International and diaspora donations, card acquiring in USD, and payouts to Zimbabwean beneficiaries in
  USD or ZiG may each have rules on who may receive, in which currency, through which channels, and with
  what documentation (LR-005, LR-006).
- Architecture: currency is explicit on every monetary value; USD and ZWG are never combined; no FX is
  performed by FundZim (ADR-010, [MONEY.md](MONEY.md)). Any surrender/conversion requirement would be executed
  by the licensed PSP/bank and recorded, not computed by FundZim.

### 3.7 Data protection

Instruments to review: Cyber and Data Protection Act [Chapter 12:07] and regulations; POTRAZ in its role as
Data Protection Authority; laws in donor jurisdictions (e.g. UK/EU GDPR, South African POPIA) where FundZim
targets donors there.

- Registration/licensing as data controller, appointment of a Data Protection Officer (LR-010).
- Cross-border transfer and hosting location (LR-011): affects cloud region selection, vendors (KYC,
  email/SMS, error tracking), and backups.
- Lawful basis and consent for processing, special categories (health information in medical campaigns,
  LR-014), children's data (LR-021).
- Data classification and privacy architecture: [DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md),
  [PRIVACY.md](PRIVACY.md).

### 3.8 Charitable / organisation fundraising

Instruments to review: Private Voluntary Organisations Act (as amended) and regulations; trust and
association rules; any rules on public fundraising appeals.

- Whether organisations must be registered (and under which regime) to raise funds publicly, and whether
  FundZim must verify that registration, is open (LR-013).
- Religious and community organisations may fall under different regimes (LR-013).
- Architecture supports organisation type, registration evidence, and type-specific verification rules.

### 3.9 Tax

Instruments to review: ZIMRA guidance on VAT and income tax for digital/platform services; withholding;
intermediated money transfer tax (or successor taxes) where applicable to transactions; donor-jurisdiction
tax rules.

- Platform fee VAT/tax treatment (LR-016); whether any transaction tax applies to donations or payouts and
  who collects it (LR-016).
- Receipts: FundZim issues **transaction confirmations**, not tax-deductible receipts, unless confirmed
  otherwise (LR-017).

### 3.10 Consumer protection, refunds, disputes

Instruments to review: Consumer Protection Act and regulations; PSP scheme rules (card chargebacks).

- Donor refund rights, cooling-off, and how refunds interact with already-paid-out funds (LR-018).
- Chargeback liability allocation between FundZim, PSP and campaign owner (LR-020).
- Disposition of funds on cancelled/frozen campaigns (LR-019).
- Clear, honest campaign representation obligations; fraud reporting flow for the public.

---

## 4. Compliance-driven product constraints (apply now)

1. No feature may imply FundZim holds funds ("your FundZim wallet", "balance", "top up") — use "amount
   raised", "available for payout via <provider>". Copy review is part of Stage 7.
2. No feature may combine USD and ZiG amounts or display a converted total without explicit, labelled,
   indicative-only treatment (ADR-010).
3. Every regulatory threshold (KYC gates, payout limits, screening triggers, retention) is configuration with
   an audit trail of changes, not a code constant.
4. Every restrictive action (hold, suspension, freeze, rejection) records actor, reason, and legal/policy
   basis reference.
5. Marketing claims ("safe", "verified", "guaranteed", "tax-deductible", "licensed") are prohibited until the
   underlying basis exists and is approved.

---

## 5. Compliance Assumptions Register

**Moved in Stage 1.** The authoritative register is now
[compliance/open-legal-questions.md](compliance/open-legal-questions.md). Items LR-001 – LR-035 moved there
with their IDs and substance unchanged (Stage 1 research notes were appended). New items LR-036 – LR-089 were
added in Stage 1; duplicates are marked `DUPLICATE` there, never deleted. The maintenance rules (append-only
IDs, written determinations, review cadence) are in that file's §6.

Every item is `LEGAL_REVIEW_REQUIRED` unless marked as a pure business decision. Nothing in the register is a
legal conclusion.

## 6. Stage 1 documents

| Document | Purpose |
|---|---|
| [regulatory-landscape.md](compliance/regulatory-landscape.md) | What the sources say, area by area, and how it may apply to FundZim |
| [regulatory-requirements-register.md](compliance/regulatory-requirements-register.md) | `REQ-xxx` requirements with source, confidence, owner, control and evidence |
| [licensing-assessment.md](compliance/licensing-assessment.md) | Activities × operating models → possible licences, registrations and authorities |
| [open-legal-questions.md](compliance/open-legal-questions.md) | Authoritative `LR-xxx` register |
| [operating-model-decision.md](compliance/operating-model-decision.md) | Models A, B and C evaluated; Model A recommended provisionally |
| [financial-responsibility-matrix.md](compliance/financial-responsibility-matrix.md) | Who is responsible for each financial function |
| [kyc-architecture.md](compliance/kyc-architecture.md) | Individual verification levels, status overlay, checks |
| [kyb-architecture.md](compliance/kyb-architecture.md) | Organisation verification, beneficial ownership, fundraising authority |
| [beneficiary-verification.md](compliance/beneficiary-verification.md) | Owner ≠ beneficiary model and payout preconditions |
| [aml-risk-framework.md](compliance/aml-risk-framework.md) | Risk-based approach and the limits model |
| [transaction-monitoring.md](compliance/transaction-monitoring.md) | Monitoring rules TM-01 – TM-19 and alert flow |
| [sanctions-screening.md](compliance/sanctions-screening.md) | Lists, screening points, matching and freeze handling |
| [compliance-case-management.md](compliance/compliance-case-management.md) | Cases, holds, STR workflow with tipping-off controls |
| [donor-protection-policy.md](compliance/donor-protection-policy.md) | Refunds, complaints, disputes: policy vs provider rule vs law |
| [campaign-approval-policy.md](compliance/campaign-approval-policy.md) | Pre-publication review checks and risk tiers |
| [data-protection-assessment.md](compliance/data-protection-assessment.md) | CDPA / S.I. 155 of 2024 controls and controller/processor roles |
| [audit-evidence-model.md](compliance/audit-evidence-model.md) | Audit events and evidence records |
| [operational-controls.md](compliance/operational-controls.md) | Separation of duties and maker-checker policy |
| [incident-response-workflows.md](compliance/incident-response-workflows.md) | Financial, security and regulatory incident playbooks |
| [regulatory-readiness-checklist.md](compliance/regulatory-readiness-checklist.md) | Readiness items with design / implementation / approval status |

Related Stage 1 documents outside this folder: [payments/](payments/) (funds flows, lifecycles, providers,
currency), [ledger/settlement-and-custody-model.md](ledger/settlement-and-custody-model.md) and
[security/identity-data-protection.md](security/identity-data-protection.md).
