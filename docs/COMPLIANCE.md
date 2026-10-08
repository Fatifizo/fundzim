# FundZim — Compliance Framework & Assumptions Register

> **Status:** Stage 0 draft. **Nothing in this document is legal advice or a legal conclusion.**
> Every regulatory statement below is an *assumption* or *open question* to be confirmed by qualified
> Zimbabwean counsel (and, where relevant, counsel in donor jurisdictions). Items needing a determination are
> tagged `LEGAL_REVIEW_REQUIRED` and tracked in the [Compliance Assumptions Register](#5-compliance-assumptions-register).
>
> FundZim does **not** claim to be licensed, approved, registered, PCI DSS compliant, or compliant with any
> data-protection, AML/CFT or charity law. Those are outcomes to be earned and evidenced, not asserted.

Related: [PRODUCT.md](PRODUCT.md) · [PAYMENTS.md](PAYMENTS.md) · [LEDGER.md](LEDGER.md) ·
[PRIVACY.md](PRIVACY.md) · [DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md) · [SECURITY.md](SECURITY.md) ·
[ROADMAP.md](ROADMAP.md) (Stage 1 is dedicated to resolving this register).

---

## 1. Purpose

FundZim orchestrates real people's money across mobile money, bank and card rails, for Zimbabwean
beneficiaries and donors inside and outside Zimbabwe. That places it adjacent to (and possibly inside) several
regulated domains: payment systems, exchange control, AML/CFT, data protection, charitable fundraising and tax.

This document:

1. Defines the **regulatory boundary** FundZim is designing around.
2. Describes the **compliance framework** — the controls the architecture must be able to support regardless
   of the final legal answers.
3. Holds the **authoritative Compliance Assumptions Register** (`LR-xxx`). Other documents reference these IDs
   rather than restating assumptions.

The engineering rule is simple: **when a feature depends on an open `LR` item, the feature is designed so the
answer can be applied as configuration or policy, and the feature does not go to production until the item is
closed.** Open items must never be silently converted into facts in code, copy, or documentation.

---

## 2. Regulatory boundary

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

**Status values:** `OPEN` (unresolved), `IN_REVIEW` (with counsel), `RESOLVED` (determination documented,
with reference), `SUPERSEDED`. All items are `OPEN` at Stage 0. Owner abbreviations: **Counsel** = qualified
Zimbabwean legal counsel; **Founders** = FundZim business owners; **Finance** = finance lead/accountant;
**DPO** = Data Protection Officer (once appointed); **Eng** = technical lead.

Every item is `LEGAL_REVIEW_REQUIRED` unless marked as a pure business decision.

| ID | Area | Assumption / question | Why it matters | Architectural impact / what is blocked | Owner | Status |
|---|---|---|---|---|---|---|
| LR-001 | Custody of funds | **Assumption:** FundZim will not hold customer funds; funds are held and moved by licensed PSPs/banks. **Question:** Can FundZim lawfully hold funds even transiently (e.g. a collection account), and if so under what licence/trust structure? | Holding funds may require a licence and safeguarding. | Determines settlement design, payout flow, ledger account structure, fee collection. Shapes PSP selection (Stage 1 shortlist, Stage 9 integration) and payout design (Stage 11); blocks live acceptance (Stage 20). | Counsel, Founders | OPEN |
| LR-002 | Ledger accounting treatment | **Question:** Are amounts orchestrated for campaigns FundZim balance-sheet items (assets/liabilities) or off-balance-sheet records? How should the ledger chart of accounts map to statutory accounts? | Wrong treatment misstates financials and may imply custody. | Ledger account naming/types (Stage 10). Ledger is designed to support either view; naming avoids implying custody. | Finance, Counsel | OPEN |
| LR-003 | Stored value / e-money | **Assumption:** A per-user or per-campaign balance that cannot be spent, transferred or topped up, and is only paid out via the approved flow, is not stored value. **Question:** Confirm. | Stored value/e-money is a regulated activity. | No wallets, no P2P, no balance spending. Any feature resembling a wallet is blocked. | Counsel | OPEN |
| LR-004 | PSP licensing & role | **Question:** Does FundZim's orchestration role (initiating payments, instructing payouts) require registration/licensing under the National Payment Systems Act or RBZ directives (e.g. as a payment facilitator, aggregator, or third-party payment processor)? Which licences must partner PSPs hold for each rail? | Operating without a required licence may be a serious offence (to be confirmed by counsel). | PSP selection criteria (Stage 8/9); payout instruction design. Shapes PSP contracting (Stage 9); blocks live acceptance (Stage 20). | Counsel | OPEN |
| LR-005 | Exchange control — international donations | **Question:** May foreign/diaspora donors pay in USD (cards, international transfers) for Zimbabwean beneficiaries? What documentation, limits, surrender or reporting rules apply to receipt and payout? | Cross-border inflows are central to the vision. | Provider routing by donor country/currency; donor data collected at payment; payout currency rules. Shapes international rails in Stage 9; blocks live international acceptance (Stage 20). | Counsel | OPEN |
| LR-006 | ZiG (ZWG) handling & settlement | **Question:** Rules for accepting, settling and paying out in ZiG vs USD; whether a campaign may receive both; whether payouts must be in the currency received; minor-unit usage by PSPs. | Mis-handled currency creates legal and financial errors. | Multi-currency acceptance policy (MVP default: goal currency only); currency enable/disable configuration. | Counsel, Finance | OPEN |
| LR-007 | AML thresholds & KYC tiers | **Question:** Required CDD per level; thresholds that trigger IDENTITY/PAYOUT verification, enhanced due diligence, and donor identification; per-transaction and cumulative limits for UNVERIFIED users. | Determines onboarding friction and AML exposure. | KYC gate configuration (Stage 5), risk limits (Stage 13). Default gates in §3.2 are placeholders. | Counsel | OPEN |
| LR-008 | FIU reporting obligations | **Question:** Is FundZim an accountable/reporting institution? Obligations for suspicious transaction reports, cash/threshold reports, record keeping, compliance officer appointment, staff training, tipping-off rules. | Non-reporting may be an offence (to be confirmed by counsel); tipping-off rules shape UX. | Case management and reporting tooling (Stage 13/14); support-visible statuses. | Counsel, Founders | OPEN |
| LR-009 | Sanctions & PEP screening | **Question:** Which sanctions lists apply (domestic, UN, and PSP/bank-imposed such as OFAC/UK/EU)? Who must be screened (owners, beneficiaries, directors, donors) and when? PEP definition and EDD. | Sanctions breaches can terminate PSP relationships. | Screening integration points (Stage 5, 11, 13); vendor selection. | Counsel | OPEN |
| LR-010 | Data protection registration & DPO | **Question:** Registration/licensing requirements as data controller with POTRAZ (Data Protection Authority) under the Cyber and Data Protection Act; DPO appointment requirements; breach notification obligations and timelines. | Processing without required registration is non-compliant. | Pre-launch gate (Stage 20); incident response plan (Stage 18). | Counsel, Founders | OPEN |
| LR-011 | Cross-border transfer & hosting location | **Question:** May personal data (incl. KYC) be hosted outside Zimbabwe? Conditions for transfers to cloud providers and vendors (KYC, SMS, email, error tracking)? Any data localisation for financial data? | Determines cloud region, vendor eligibility, backup location. | Production topology decision (Stage 18/20); vendor contracts. Blocks production hosting choice. | Counsel, DPO | OPEN |
| LR-012 | Retention periods | **Question:** Minimum and maximum retention for KYC records, financial/transaction records, ledger, audit logs, compliance cases, and marketing data; start events (e.g. account closure, transaction date). | Conflicting duties: keep for AML vs minimise for privacy. | Retention policy table designed from Stage 3; deletion/anonymisation jobs in Stages 17/18. No retention numbers in code until resolved. | Counsel, DPO | OPEN |
| LR-013 | Charitable / PVO / organisation fundraising | **Question:** Must organisations (charities, PVOs, trusts, churches, community groups, sports clubs) be registered to raise public funds? Does FundZim have verification duties? Do individuals raising for third parties or for "community" causes trigger these rules? | May restrict who can create organisation or community campaigns. | Organisation types and verification rules (Stage 5/6); campaign category restrictions. | Counsel | OPEN |
| LR-014 | Medical fundraising & health data | **Question:** Lawful basis for publishing health information about beneficiaries; consent requirements; verification of medical claims; restrictions on medical claims advertising. | Health data is sensitive; false claims harm donors. | Beneficiary consent capture, sensitive-content flags, review checklist (Stage 6). | Counsel, DPO | OPEN |
| LR-015 | Beneficiary consent incl. minors & incapacitated persons | **Question:** Consent and authority requirements when raising for another person, a minor, a deceased person (funerals), or someone lacking capacity; who may receive payouts on their behalf. | Protects beneficiaries; prevents exploitation and misappropriation. | Beneficiary model, guardian/representative verification, payout destination rules (Stage 5, 6, 11). | Counsel | OPEN |
| LR-016 | Tax on platform fees & transactions | **Question:** VAT and income tax treatment of platform fees; any transaction-level taxes (e.g. intermediated money transfer tax) on donations/payouts and who withholds; tax registration requirements. | Fee pricing and invoicing depend on it. | Fee model and fee ledger accounts (Stage 12); invoice/receipt formats. | Counsel, Finance | OPEN |
| LR-017 | Donation receipts & tax deductibility | **Assumption:** FundZim issues payment confirmations only, never tax-deductible receipts. **Question:** Can/should verified organisations issue deductible receipts via FundZim; donor-jurisdiction rules? | Misrepresenting deductibility harms donors. | Receipt templates and copy (Stage 7, 15). | Counsel | OPEN |
| LR-018 | Consumer protection & refunds | **Question:** Donor refund rights (cooling-off, misrepresentation, campaign fraud); disclosure obligations; complaint handling requirements. | Determines refund policy and UX disclosures. | Refund workflow and time windows (Stage 8, 11); terms of service. | Counsel, Founders | OPEN |
| LR-019 | Cancelled / frozen campaign funds | **Question:** What happens to funds of cancelled, rejected-after-donation, or fraud-confirmed campaigns: refund to donors, payout to a verified beneficiary, transfer to charity, or hold? Unclaimed funds rules. | Determines refund automation and ledger flows. | Campaign termination flows (Stage 6, 11); ledger reversal templates (Stage 10). | Counsel, Founders | OPEN |
| LR-020 | Chargebacks & dispute liability | **Question:** Who bears chargeback loss after payout (FundZim, PSP, campaign owner)? Reserve/rolling-hold requirements imposed by PSPs/schemes; recovery rights against campaign owners. | Card disputes can arrive weeks after payout. | Payout holds/reserves (Stage 11), DISPUTED payment state, ledger accounts for losses (Stage 10). | Counsel, Finance | OPEN |
| LR-021 | Age of users | **Question:** Minimum age to create an account, donate, create a campaign, or receive payouts; parental consent rules; processing of children's data. | Contract capacity and child data protection. | Age capture/attestation at sign-up (Stage 4), KYC DOB checks (Stage 5). | Counsel | OPEN |
| LR-022 | Terms of service & platform liability | **Question:** Required terms (user agreement, campaign rules, prohibited causes, privacy notice, cookie notice), platform liability for campaign content and misuse, governing law. | Contractual basis for every restrictive action. | Acceptance versioning and re-consent flows (Stage 4); prohibited-content policy (Stage 6). | Counsel, Founders | OPEN |
| LR-023 | WhatsApp, SMS & marketing consent | **Question:** Consent requirements for marketing messages, transactional SMS/WhatsApp, use of WhatsApp Business messaging policies; referral attribution and tracking consent. | Unconsented marketing breaches data protection and platform policies. | Consent records and preference centre (Stage 15/16); referral tracking design. | Counsel, DPO | OPEN |
| LR-024 | Payout to third parties & nominee accounts | **Question:** May payouts go to an account not in the verified owner's name (e.g. hospital, school, funeral parlour, family member)? Required evidence? | Common Zimbabwean use case; also a laundering vector. | Payout destination verification rules (Stage 11). | Counsel | OPEN |
| LR-025 | Prohibited & restricted causes | **Question:** Causes that are illegal or require licences to fundraise for (e.g. political campaigns, legal defence, lotteries/raffles, religious tithes, cross-border remittance-like use). | Political and remittance-like use carry specific legal risk. | Category allow-list and review checklist (Stage 6). | Counsel, Founders | OPEN |
| LR-026 | Donor identity disclosure | **Question:** Whether "anonymous" donations are permissible above thresholds; obligations to disclose donor identity to campaign owners, authorities, or recipients of political/organisation funds. | Interacts with AML and data protection. | Anonymity rules (public vs owner vs compliance) per [PRIVACY.md](PRIVACY.md); thresholds configurable. | Counsel | OPEN |
| LR-027 | Electronic transactions & e-signatures | **Question:** Validity of click-wrap acceptance, electronic records as evidence, electronic signature of organisation authorisations. | Evidentiary value of FundZim's records. | Acceptance record design (Stage 4); audit evidence format. | Counsel | OPEN |
| LR-028 | Platform fee model | **Business decision (plus tax review via LR-016):** fee deducted from donation vs donor-covers-fees vs optional tip; fee disclosure requirements. | Affects ledger postings and donor UX. | Fee engine (Stage 12); ledger supports both deduction and add-on. | Founders, Finance | OPEN |
| LR-029 | Licence of FundZim source code | **Business decision:** repository licence (proprietary vs open). No LICENSE file exists until decided. | IP ownership and contributor terms. | Repository LICENSE; contributor agreements. | Founders | OPEN |
| LR-030 | Payout approval thresholds & auto-approval limits | **Question:** Above what amounts (per payout, per period, per currency) must payouts be manually approved, held or subjected to enhanced checks? Are there regulatory or PSP-imposed limits on auto-approval? | Sets the maker-checker boundary for payouts and the risk/hold hook. | Payout policy configuration (Stage 11); risk engine (Stage 13). Values are configuration with an audit trail, never code constants. | Counsel, Finance, Founders | OPEN |
| LR-031 | SMS sender ID & SMS provider terms | **Question:** Is registration of an alphanumeric sender ID required with operators/POTRAZ? What do SMS provider terms and operator rules require for OTP and transactional traffic (content, opt-out, throttling)? | OTP delivery reliability and anti-spoofing; contractual exposure. | SMS adapter and sender configuration (Stage 15); Stage 4 uses the `log` fake only. | Counsel, Eng | OPEN |
| LR-032 | Regulator & law-enforcement data access requests | **Question:** Which authorities may compel disclosure of user, KYC or transaction data, under what process (warrant, court order, FIU request), with what notification or non-disclosure duties? | Ad hoc disclosure is a privacy and tipping-off risk; refusal may be unlawful. | Documented request-handling process; audited, permissioned export path (Stage 14); no ad hoc data pulls. | Counsel, DPO | OPEN |
| LR-033 | Vendor contracts & data processing terms | **Question:** What contractual terms (data processing agreements, sub-processors, breach notice, audit rights, data location, deletion on exit) are required with KYC vendors, PSPs, SMS/email providers and hosting? | Vendors process C2/C3 data on FundZim's behalf. | Vendor selection gates in Stages 5, 9, 15 and 18; vendor register. | Counsel, DPO, Eng | OPEN |
| LR-034 | Data subject rights & erasure conflicts | **Question:** Which rights (access, correction, deletion, objection) apply, with what response timelines, and how do they interact with AML/financial record-keeping and backups? | Determines the deletion/anonymisation design and DSR workflow. | DSR tooling (Stages 17/18, operated through the Stage 14 portal); anonymisation and crypto-shredding design (Stages 3, 5, 18). | Counsel, DPO | OPEN |
| LR-035 | Lawful basis & consent for processing | **Question:** Lawful basis for each processing purpose (account, KYC, fraud prevention incl. device/IP signals, analytics, marketing); form and records of consent where consent is the basis. | Processing without a valid basis is unlawful; consent UX depends on it. | Consent records (Stage 4); risk signals limited to lawful ones (Stage 13); analytics choices (Stage 7/16). | Counsel, DPO | OPEN |

### 5.1 Register maintenance rules

- New `LR` IDs are appended; IDs are never reused or renumbered.
- Resolving an item requires: a written determination (or reference to counsel's memo stored outside the
  repository if privileged), the date, the reviewer, and the resulting engineering changes (ADR if
  architectural).
- Code that depends on an `LR` item references its ID in a comment near the policy configuration
  (`// policy: LR-007`), so resolution changes are traceable.
- Stage completion reports must list every `LR` item touched or still blocking that stage.
