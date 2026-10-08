# FundZim — Regulatory Landscape (Zimbabwe)

**Stage 1 deliverable.** Status: research map, verified on **2026-10-08**. **Not legal advice.**

This document maps the Zimbabwean rules that may apply to FundZim. For each area it records what the
sources **say**, separately from FundZim's **interpretation**. Interpretations are working hypotheses for
qualified Zimbabwean counsel to confirm or reject. None is a legal conclusion, and none of them, alone
or together, amounts to permission to operate.

Related documents:
- [regulatory-requirements-register.md](regulatory-requirements-register.md): the requirement-by-requirement register (`REQ-xxx`).
- [licensing-assessment.md](licensing-assessment.md): an activity × operating-model analysis.
- [open-legal-questions.md](open-legal-questions.md): the authoritative `LR-xxx` register.
- [data-protection-assessment.md](data-protection-assessment.md): the CDPA and SI 155/2024 deep-dive.
- [operating-model-decision.md](operating-model-decision.md): Models A/B/C ([ADR-013](../adr/ADR-013-regulatory-operating-model.md)).

---

## 1. How to read this document

| Label | Meaning |
|---|---|
| **Source says** | A faithful paraphrase or short quotation of an opened source. The source ID (`Sxx`) resolves to the table in §24. |
| **Interpretation** | FundZim's working hypothesis about how the source affects the platform. Always `LEGAL_REVIEW_REQUIRED`. |
| **Confidence** | **HIGH**: primary official text was read. **MEDIUM**: reputable secondary source, or a primary text that may be superseded. **LOW**: weak, indirect, or search-snippet-only evidence. |
| `LR-xxx` | Open legal question in [open-legal-questions.md](open-legal-questions.md). |
| `REQ-xxx` | Requirement row in [regulatory-requirements-register.md](regulatory-requirements-register.md). |

The register defines the classification vocabulary (CONFIRMED, POTENTIALLY_APPLICABLE,
LEGAL_REVIEW_REQUIRED, PROVIDER_CONFIRMATION_REQUIRED, NOT_APPLICABLE_WITH_REASON). In short, **CONFIRMED**
means a primary source verified that the requirement exists. It does **not** mean counsel has confirmed that
it applies to FundZim; no counsel has reviewed any item yet.

## 2. Research access limitations (read before relying on anything)

The research was desk-based and automated. These gaps lower confidence and must be closed by counsel or by
direct enquiry:

| Limitation | Effect |
|---|---|
| `rbz.co.zw` HTML pages sit behind a bot-check and could not be opened. One RBZ PDF did load ([S04](#24-sources-consulted)). The April 2025 RBZ FX guideline was read from an embassy mirror ([S09](#24-sources-consulted)). | No direct access to the RBZ circulars, NPS Department notices, the cyber resilience guideline, the QR guideline or any list of licensed PSPs. |
| `zimlii.org` returned HTTP 403, and `old.zimlii.org` refused connections. | Consolidated statute versions after Veritas consolidations were not checked. For example, the NPS Act consolidation is believed to be "up to date as at 31 December 2016", but this was not opened. |
| `fatf-gafi.org` returned 403. | FATF status was taken from a secondary source and a regulator's reproduction of the FATF list. |
| `potraz.gov.zw` redirected, and the redirect was not followed. | POTRAZ notices (inspection schedule, cross-border and consent guidelines) were seen only via secondary sources. |
| Several news and law-firm sites returned 403 or 429. | Some secondary claims are search-snippet only. Those are marked LOW and are not used as findings. |
| Veritas texts are faithful reproductions, but Veritas disclaims legal responsibility. | Rated HIGH here. They must be checked against the Government Gazette before anyone relies on them. |

The unverified items from research are carried into the register as `LEGAL_REVIEW_REQUIRED` rows marked
**UNVERIFIED** (§6 of the register), so they are not lost.

## 3. The shape of the problem

FundZim is a donation platform. Donors pay, licensed providers move money, and beneficiaries receive it.
Four bodies of law dominate:

1. **Payment-system law (RBZ):** who may accept money for others, who may operate a payment system, and
   who may hold balances. This determines the operating model.
2. **Charitable-collection law (PVO Act, as amended 2025):** who may collect "contributions from the
   public". This is the **highest-impact finding** of Stage 1 and may restrict the product itself, not just
   its plumbing.
3. **AML/CFT law (MLPC Act, sanctions SIs):** whether FundZim is an accountable institution, and what its
   partners will require of it in any case.
4. **Data protection (CDPA and SI 155/2024):** licensing as a data controller, sensitive health and child
   data, and cross-border transfer.

Tax (IMTT and VAT), exchange control, consumer protection, and the ZiG currency regime shape the economics
and the user experience.

## 4. National payment systems and RBZ oversight

### 4.1 National Payment Systems Act [Chapter 24:23]

**Source says ([S01](#24-sources-consulted), HIGH for the text, MEDIUM for currency):**
- The Act (Act 21/2001; S.I. 262/2006) governs the *recognition* of systems that clear payment instructions
  **between financial institutions**.
- Section 3(3)(a) limits participants in a recognised system to financial institutions and the RBZ.
- Section 3(3)(d)(v) requires system rules to set criteria by which a participant may "introduce any
  person to provide payment services".
- The Act does not define "payment service provider".
- No amending Act after 2006 was found; this is an absence of evidence (LOW).

**Section 18 (HIGH):**
- No person other than a participant, or "a person introduced by a participant", shall "as a regular
  feature of his business, accept money or a payment instruction from any other person for the purpose of
  making a payment on behalf of that other person to a third person to whom the payment is due".
- Exemptions include "a person who is acting as the duly appointed agent of the person to whom the payment
  is due" (s18(3)(c)) and Ministerial exemptions by Gazette notice (s18(4)).
- The penalty is a fine expressed in pre-2009 dollars, or up to 1 year's imprisonment.

**Interpretation (`LEGAL_REVIEW_REQUIRED`, LR-036, LR-037):**
- **What s18 does to the operating model.** It is the provision closest to "collecting on behalf of third
  parties".
  - If FundZim itself accepted donor money to pass to beneficiaries (Model B), it would appear to fall
    within s18(1) unless an exemption applied.
  - Where a licensed PSP accepts and disburses funds (Model A), the activity sits with a participant, or a
    person introduced by one, which is the most natural fit.
  - Two questions are open:
    - whether a donation is a payment "due" to the beneficiary (a gift is arguably not a debt "due");
    - whether FundZim could rely on agency appointments by beneficiaries.
- **Who licenses non-bank PSPs.** In practice, licensing of non-bank PSPs comes from the RBZ guidelines and
  the Banking Act regulations (§4.2–4.3), not the NPS Act itself.

### 4.2 Banking (Money Transmission, Mobile Banking and Mobile Money Interoperability) Regulations, 2020 (S.I. 80 of 2020)

**Source says ([S02](#24-sources-consulted), HIGH for the text, MEDIUM for currency):**
- **Definitions.** A "money transmission provider" is "any person who owns a payment system that
  facilitates the transmission of monies from one person to another". A mobile money wallet holds funds "in
  the mobile money provider's trust bank account".
- **Recognition (s3).** Money transmission and mobile banking providers must obtain recognition of their
  payment system under NPS Act s3(1).
- **Operating requirements (s4):**
  - connection to the national switch;
  - a bank account designated exclusively for mobile banking services;
  - "no money is transmitted or is retained on the payment system without a corresponding correct bank
    balance";
  - periodic returns "kept for a period of 7 years";
  - audit reports;
  - read-only RBZ access;
  - "policies and procedures for sound internal controls and risk management practices including data
    protection and cyber security";
  - transaction screening and limits.
- **Charges (s5).** Charges may not be levied or changed without prior RBZ approval.
- **2025 amendment (secondary, [S03](#24-sources-consulted), MEDIUM).** S.I. 17 of 2025 reportedly set a
  USD 5,000 application fee and an annual fee of 2% of gross turnover, capped at USD 50,000. It also
  reportedly requires a non-bank applicant to partner with a local authorised financial institution. The
  gazetted text was not read.

**Interpretation (LR-038):**
- **Ledger and wallet risk.** A FundZim-built ledger or wallet that moves value between users risks
  characterisation as a money-transmission payment system.
- **Keeping out of scope.** A model where checkout, holding and disbursement run on the PSP's recognised
  system, and FundZim's ledger is a record rather than a value-transfer mechanism, keeps FundZim further
  from s3. This is the reason for [ADR-014](../adr/ADR-014-accounting-separated-from-custody.md).
- **Fee pricing.** The rule on charge approval may constrain how a PSP prices fees passed through to
  FundZim. This needs provider confirmation (PCR).

### 4.3 RBZ Guidelines for Retail Payment Systems and Instruments (effective 1 July 2017)

**Source says ([S04](#24-sources-consulted), HIGH for the text, MEDIUM for currency):**
- **Authorisation (para 3.1).** "No operator can operate a system or an issuer can issue and manage retail
  payment instruments unless it has been authorized by the Reserve Bank."
- **Definitions.** An acquirer is "the payment service provider (PSP) processing payments on behalf of a
  merchant". A merchant is any person that accepts retail payment instruments or e-money.
- **Operators (para 6.1).** Operators need minimum initial capital of USD 1 million.
- **Records (para 9.2).** "Ten years unless a higher minimum period is prescribed in terms of AML/CFT or
  electronic communications legislation."
- **Outsourcing (para 10.1).** Requires RBZ authorisation.
- **Remittances (para 13.1).** Remittance service providers must be registered and licensed.
- **User funds (paras 16.3–16.4, 17, 18.1(c)).** User funds must be segregated from working capital.
  E-money must be fully backed by escrow or trust deposits, administered by RBZ-approved trustees under a
  trust deed, with weekly and monthly reconciliation.
- **Prohibitions (para 20.1).** No interest may be paid on e-money balances, and user funds may not be lent.

**Interpretation (LR-003, LR-038, LR-039):**
- **FundZim as merchant.** In these guidelines' vocabulary FundZim would be a **merchant** using an
  authorised acquirer or PSP.
- **Balances and wallets.** Holding donor balances or offering campaign "wallets" would make FundZim look
  like an e-money issuer or operator, with capital, trust-account and trustee obligations. FundZim's design
  therefore never holds balances; "campaign balances" are ledger records of funds held by the PSP
  ([ledger/settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md)).
- **What the PSP contract should confirm.** The PSP holds donor funds in its trust or escrow arrangement,
  and disbursement on FundZim's instruction is within the PSP's RBZ-approved services (para 20.1(e)).
  Both are provider-confirmation and legal-review items.

### 4.4 National switch and QR payments

**Source says:**
- The RBZ designated ZimSwitch as the national payment switch on 9 July 2020 and directed providers to
  connect ([S05](#24-sources-consulted), MEDIUM).
- A March 2026 RBZ circular (NPSD 03/2026) reportedly operationalised a national QR Code Payment Guideline
  requiring routing via the national switch and EMVCo alignment. This is search-snippet only, LOW, and
  **UNVERIFIED**.

**Interpretation:** Any "scan to donate" QR feature must be issued by the PSP and conform to the RBZ QR
guideline once that guideline is verified (LR-040).

### 4.5 Crowdfunding, aggregation and payment facilitation

**Source says:**
- No RBZ instrument defining "payment aggregator", "payment facilitator", "third-party payment processor",
  "sub-merchant" or "crowdfunding" was found.
- A 2022 news report says the RBZ "issued crowdfunding licences to local firms", without naming the
  framework ([S06](#24-sources-consulted), LOW).
- A 2026 SECZ sandbox admission concerned *investment* crowdfunding. This is search-snippet only and was
  not opened, so it is not relied on.

**Interpretation (LR-041):**
- **No bespoke regime found.** There is no known regime for donation crowdfunding, so the analysis falls
  back on NPS Act s18, S.I. 80/2020 and the 2017 Guidelines.
- **Possible RBZ engagement.** Counsel should advise whether FundZim should seek RBZ comfort (a
  no-objection letter, or admission to the RBZ fintech sandbox). FundZim must not assume that silence
  equals permission.
- **SECZ.** SECZ involvement appears irrelevant for donation-only crowdfunding, because FundZim offers no
  equity, rewards or lending. This should still be confirmed.

### 4.6 Merchant onboarding and PSP arrangements

**Source says:** The 2017 Guidelines treat merchants as users of authorised acquirers ([S04](#24-sources-consulted)).
The RBZ texts read set no specific merchant-onboarding rule for platforms.

**Interpretation:** Merchant onboarding obligations (CDD on FundZim and, possibly, on each campaign owner or
beneficiary) will come primarily from the **PSP's own AML programme** and contract (§9.3). The research
found no statutory sub-merchant regime. For provider-side detail see
[../payments/provider-due-diligence-checklist.md](../payments/provider-due-diligence-checklist.md) and
[../payments/provider-questions.md](../payments/provider-questions.md).

## 5. Collection, settlement, safeguarding and disbursement

**Source says:**
- S.I. 80/2020 s4(3)–(5) requires a dedicated bank account and no platform value without a matching bank
  balance ([S02](#24-sources-consulted)).
- The 2017 Guidelines require segregation of user funds, and require e-money to be fully backed in an
  escrow or trust account at a deposit-taking institution under RBZ-approved trustees
  ([S04](#24-sources-consulted)).

**Interpretation (LR-001, LR-038, LR-039):**
- **Where donor funds sit.** Under Model A, donor funds sit in the PSP's trust or escrow arrangement and are
  disbursed by the PSP on FundZim's instruction. They should **never** pass through a FundZim operating
  account.
- **Platform fees.** These should be deducted at source by the PSP (split) or remitted by the PSP to
  FundZim. They should not be collected gross by FundZim.
- **Approvals and contract terms.** Two points are open:
  - whether split settlement, or disbursement on a third-party platform's instruction, needs specific RBZ
    approval of the PSP's scheme;
  - what the PSP contract must state: trust holding, instruction authority, refunds and chargebacks, and
    settlement currency equal to collection currency.
  Both are tracked as LR-038 and PCR items.

Ledger treatment: [../ledger/settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md).
Flow detail: [../payments/funds-flow-architecture.md](../payments/funds-flow-architecture.md).

## 6. Currency: ZiG (ZWG) and the multi-currency regime

**Source says:**
- **ZiG ([S07](#24-sources-consulted), HIGH).** S.I. 60 of 2024 (5 April 2024) inserted s44D into the RBZ
  Act. ZiG notes and coins are "legal tender in all transactions, alongside any other currency acceptable
  as legal tender as prescribed under section 44A". ZWL balances were converted to ZiG.
- **Minor unit.** S.I. 60/2024 does **not** define a minor unit (cents). Coins were to be "prescribed at a
  later date".
- **Lapse dispute ([S08](#24-sources-consulted), MEDIUM).** A legal commentator argued the Presidential
  Powers regulations lapsed. The RBZ responded in October 2024 that "there is no gap at law".
- **Multi-currency until 2030 ([S09](#24-sources-consulted), HIGH for what the RBZ document says, MEDIUM
  for the underlying SIs).** The RBZ FX guideline (FXD 2/2025, April 2025) states that multi-currency use
  was legalised by S.I. 85 of 2020. It also states that through S.I. 218 of 2023, "the United States
  dollar, other denominated currencies and local currency remain legal tender till 31 December 2030".
- **2026 policy statement ([S10](#24-sources-consulted), MEDIUM).** In February 2026 the RBZ Governor said
  2030 "will no longer be the deadline", and that foreign currency accounts and USD contracts will be
  preserved. No instrument amending the statutory date was cited.

**Interpretation (LR-006, LR-042, LR-043):**
- **USD now.** Accepting USD and paying out in USD appears lawful under the current regime.
- **After 2030.** The legal position after 31 December 2030 is uncertain: the statutory sunset appears to
  stand, while policy has moved away from it.
- **Engineering consequences:**
  - Currency must be configurable per campaign and per rail ([../payments/currency-and-fx-policy.md](../payments/currency-and-fx-policy.md)).
  - ZWG's minor unit must not be hard-coded as settled. The currency registry entry
    (`ZWG`, minor_units = 2) remains **provisional** pending LR-043 and provider confirmation
    ([../MONEY.md](../MONEY.md)).

## 7. Exchange control and cross-border donations

**Source says ([S09](#24-sources-consulted), HIGH):**
- **Who may deal (para 1.1.2.2).** Foreign currency dealing is "only limited to Authorised Dealers and
  Authorised Dealers with Limited Authority (ADLAs)".
- **Inbound funds.** Individual foreign currency accounts (FCAs) may be funded from "donations" and
  "diaspora remittances", which are "free funds". FCAs for NGOs can be opened without prior RBZ reference.
  Individuals, NGOs and international organisations may use FCA balances "without any restriction".
- **Remittances (para 5.4).** A resident "can receive diaspora remittances in foreign currency, in cash,
  mobile wallet or bank account". The surrender requirement applies to export proceeds (para 3.2.3.1).
  Outward person-to-person remittances are limited to US$5,000 per transaction and US$50,000 per year.
- **Outbound donations (para 3.1.13.1).** "All corporate monetary donations, gifts and other miscellaneous
  payments require prior Reserve Bank approval."
- **Cash withdrawals.** FCA cash withdrawals are capped at USD 1,000 per day for individuals and USD 10,000
  per day for corporates.
- **Corroboration ([S11](#24-sources-consulted), MEDIUM).** The RBZ stated in 2024 that diaspora
  remittances are free funds and recipients are not forced to take ZiG.

**Interpretation (LR-005, LR-044):**
- **No conversion by FundZim.** FundZim must never buy, sell or convert currency. Any conversion is
  performed by an Authorised Dealer or ADLA (bank or PSP) and recorded as an external fact
  ([ADR-018](../adr/ADR-018-currency-isolation-and-fx.md)).
- **Inbound donations.** Foreign-currency donations to resident individuals and NGOs appear permitted
  without prior approval, subject to KYC.
- **Refunds and recovery abroad.** Refunds to foreign donors, and any outbound movement such as recovery
  of mistaken payments abroad, may need Authorised Dealer processing or RBZ approval.
- **Open question.** It is unresolved whether a cross-border card donation is a merchant card payment or a
  "remittance" requiring a licensed remittance operator (2017 Guidelines para 13.1).

## 8. Charitable collections: PVO Act [Chapter 17:05] as amended (2025) and 2026 regulations

> **Highest-impact Stage 1 finding.** This area may restrict *who can run a campaign* on FundZim, not just
> how money moves. It is a candidate **launch blocker** for individual "fundraising for others" campaigns.
> See LR-046 to LR-050.

**Source says ([S34](#24-sources-consulted); HIGH for the text, MEDIUM on validity):**
- **What a PVO is (s2(1)).** A "private voluntary organisation" includes any body or association whose
  objects include providing for the "material, mental, physical or social needs of persons or families",
  "the rendering of charity to persons or families in distress", uplifting living standards, and so on,
  and "(h) the collection of contributions for any of the foregoing".
- **Exclusions (s2(1)).** These include State and local-authority institutions, "any religious body in
  respect of activities confined to religious work", educational trusts approved by the Minister, bodies
  benefiting only their own members, registered health institutions, and others.
- **"Contributions" (s2).** Money or property "not transferred in fulfilment of a legally enforceable
  obligation" and conferring no right to consideration.
- **Registration (s6(2)).** A body with PVO objects that "collects contributions from the public" or seeks
  financial assistance must register.
- **General prohibition (s6(3)).** "No person shall collect contributions from the public except in terms
  of this Act." The penalty (s6(5)(b)) is a fine up to level 12 or up to 1 year's imprisonment.
- **Offence of unauthorised collection (s23(1)).** It is an offence when a person "collects or attempts to
  collect or instructs another person to collect" any contribution in furtherance of PVO objects, unless
  the collection is on behalf of and with the authority of a registered PVO, for an excluded body, or
  "authorised under section eight".
- **Temporary authority (s8).** The Registrar may grant "any person or group of persons" written temporary
  authority to collect. It lasts up to 90 days and may be extended once by up to 90 days.
- **Ministerial orders (s26).** The Minister may order unlawfully collected contributions to be frozen or
  returned. Unreturnable funds go to the Guardian's Fund.
- **PVO duties (s20A).** PVOs must endeavour to ascertain donor identity and sources, refuse illegitimate
  donations, and use formal regulated channels "at every point from source to destination". They must not
  act in a politically partisan manner.
- **Validity dispute ([S34](#24-sources-consulted) caveat; MEDIUM).** Veritas publicly questions whether the
  gazetted Amendment Act (Act 1 of 2025) matches what Parliament passed. No court ruling was found.

**S.I. 97 of 2026 (PVO Board and General) Regulations ([S41](#24-sources-consulted), HIGH, Gazette 5 June 2026):**
- Temporary-authority conditions (s16) may include spending on the stated purpose and disposing within 60
  days after expiry, paying all money into a bank account disclosed to the Registrar, and keeping
  receipts, books and donation lists open to inspection.
- The application form distinguishes "One person (self)", "Group of persons" and "PVO".
- Membership organisations must register if they solicit non-members (s12(2)).
- Registration fees are USD 150 for a local PVO and USD 250 for an international PVO.

**S.I. 98 of 2026 (Risk-Based Supervision and Protection from Terrorist Financing Abuse) ([S42](#24-sources-consulted), HIGH):**
- **Risk categories** for PVOs. Foreign-funding disclosure goes to the FIU within 30 days where funds from a
  "High-Risk Jurisdiction" exceed USD 5,000.
- **EDD and notification thresholds:** a single foreign donation above USD 10,000, or more than USD 25,000
  a year from one high-risk jurisdiction.
- **Beneficiary identity records** for anyone receiving more than USD 500 in one transaction or USD 2,000 a
  year.
- **TF red flags** reportable within 72 hours, including "sudden, unexplained spikes in funding" and donor
  identity "obscured through multiple layers of intermediaries".
- **STRs** are due within 3 working days. The SI is internally inconsistent: it says 3 working days in
  s29(7) and 72 hours in the Schedule.
- **Records:** 5 years for transactions and 7 years for accounting.

**FIU materials ([S23](#24-sources-consulted)/[S24](#24-sources-consulted), [S30](#24-sources-consulted)):**
- The FIU's PVO guidance says PVOs should "Receive All Funds Through Banking Channels".
- The FIU's 2024 NPO risk assessment rates the sector low overall. Faith-based organisations score highest
  for TF exposure (0.20).

**Interpretation (`LEGAL_REVIEW_REQUIRED`; LR-013, LR-046–LR-050):**
- **Individuals raising for others.** An individual raising money from the public for *another person's*
  medical bills or relief appears to fall within s6(3) and s23(1). That person would need a registered PVO,
  a s8 temporary authority, or an exclusion.
- **Self-raising.** Raising for *oneself* is less clear, because objects (a)–(d) refer to "persons or
  families" and charity. However, s6(3) is broadly drafted.
- **FundZim's own exposure.** FundZim may itself be exposed as a person who "instructs another person to
  collect". Ministerial s26 freezing orders could reach campaign funds.
- **Organisations.** Charities and community groups would generally need PVO registration. Churches are
  excluded only for activities "confined to religious work". Schools depend on the exclusion that applies
  to them.
- **Campaign length vs authority length.** The 90-day (+90) temporary-authority limit conflicts with
  open-ended campaigns.
- **Provisional design response (pending counsel; recorded in the Stage 2 handover):**
  - **Evidence on every campaign.** Each campaign carries a `fundraising_authority` record. Its basis is one
    of:
    - `SELF_FUNDRAISING` (no authority needed; this is **UNCONFIRMED**);
    - `REGISTERED_PVO` (with registration number);
    - `EXCLUDED_BODY` (with the basis);
    - `SECTION_8_AUTHORITY` (with authority number and validity window).
  - **Validity limits.** The campaign end date cannot exceed the authority's validity.
  - **Payout gate.** Payouts require valid authority evidence where one is required.
  - **Where these controls live.** Campaign approval enforces them
    ([campaign-approval-policy.md](campaign-approval-policy.md)), and the payout eligibility checks verify
    them ([../payments/payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)).
- **Data PVOs will ask for.** PVO campaign owners will need donor identity, source, and country data to meet
  their s20A and SI 98 duties. Sharing that data with them needs a CDPA basis (LR-049).

## 9. AML/CFT, CDD, beneficial ownership and suspicious transactions

### 9.1 Money Laundering and Proceeds of Crime Act [Chapter 9:24] (MLPCA)

**Source says ([S21](#24-sources-consulted), HIGH; FIU consolidation including Act 7/2025):**
- **"Financial institution" (s2(1)).** "Any person who conducts as a business one or more of the following
  activities for or on behalf of a customer". These include (d) "the transfer of money or value", (j)
  "safekeeping and administration of cash … on behalf of other persons", and (k) "investing, administering
  or managing funds or money on behalf of other persons". The Minister may declare further financial
  institutions by SI (s2(3)).
- **DNFBPs (s13).** The list (gaming, estate agents, precious metals and stones dealers, professionals,
  trust and company service providers, motor dealers, and others designated under s101) does not include
  crowdfunding platforms. No s101 designation covering crowdfunding was found.
- **CDD (s15).** Required when establishing a business relationship, for occasional transactions of
  USD 5,000 or more, for wire transfers of USD 1,000 or more, where there are doubts about identity
  documents, and on any suspicion. Beneficial owners must be verified (s15(3)), before the relationship
  starts (s16).
- **Reliance (s18).** Third-party reliance is allowed, but the relying firm keeps "ultimate
  responsibility".
- **Remote onboarding (s19).** Non-face-to-face customers need measures "no less effective" than in person.
- **High risk and PEPs (s20).** EDD for high-risk customers. PEPs, including "any close associate, spouse
  or family member", need senior-management approval and source-of-wealth and source-of-funds checks.
- **Failure to complete CDD (s22).** The relationship must not proceed, and a report is made to the FIU.
- **Records (s24).** At least 5 years after the relationship ends, or from the transaction. Some
  cross-border wire data is kept 10 years (s27(8)).
- **STRs (s30).** Due "promptly, but not later than three working days after forming the suspicion".
- **Tipping-off (s31(2)).** Prohibited. No disclosure that an STR "will be, is being or has been submitted".
- **Internal programme (s25).** A risk-based programme with a management-level compliance officer.
- **FIU information powers (s6E).** The FIU may obtain information from companies and from PVOs
  "registered or required to be registered", whether or not they are accountable institutions.

**FIU directives:**
- **Thresholds ([S25](#24-sources-consulted), HIGH).** Directive 01/04/2024 sets CTR, EFT and IFT
  thresholds of ZiG 70,000 or USD 5,000. Non-bank financial institutions and DNFBPs file CTRs monthly
  through goAML.
- **Civil penalties ([S26](#24-sources-consulted), HIGH for the pages read).** Directive PFIU21/10/2024
  sets a civil-penalty regime with fixed penalties "not to exceed … US$250 000". Directors and employees
  can be personally liable.

**Interpretation (LR-007, LR-008, LR-051, LR-052):**
- **Whether FundZim is a financial institution.** It is not a listed DNFBP. Whether it is a "financial
  institution" turns on whether it conducts transfer, safekeeping or management of funds "as a business …
  for or on behalf of a customer".
  - Model A (the PSP holds and moves funds; FundZim instructs) strengthens the argument that FundZim is
    *not* one, but does not settle it.
  - Model B makes accountable-institution status likely.
- **Building to the standard anyway.** Either way:
  - FundZim's PSPs will impose equivalent CDD.
  - The FIU can demand information.
  - FundZim should build its controls (KYC tiers, screening, STR escalation, tipping-off-safe messaging,
    5-year-minimum records) as if accountable. That costs little and keeps options open
    ([aml-risk-framework.md](aml-risk-framework.md)).
- **Tipping-off vs data access requests.** Tipping-off rules constrain support scripts, campaign-suspension
  notices and data-subject access responses (LR-088).

### 9.2 Sanctions and targeted financial sanctions

**Source says ([S32](#24-sources-consulted), HIGH):**
- S.I. 76 of 2014 designates the FIU as the national implementing agency.
- "Immediately" means "without delay but not later than 24 hours".
- Every financial institution and DNFBP "shall review the UN Consolidated List and the Zimbabwean List
  prior to conducting any transaction". On a match it must block the funds and file an STR.
- Dealing with a designated person's funds carries a fine up to USD 20,000 or twice the property value,
  whichever is greater.
- S.I. 98/2026 makes any attempted transaction with a UN-listed party a mandatory report for PVOs
  ([S42](#24-sources-consulted)).

**Interpretation (LR-009, LR-053):** FundZim should screen donors where identifiable, campaign owners,
beneficiaries, organisation officers and beneficial owners at onboarding, at payout, and on list updates.
That holds whether or not it is formally obliged, because its PSPs and PVO clients are obliged. Whether a
published domestic "Zimbabwean List" exists, and where to find it, is unverified.
See [sanctions-screening.md](sanctions-screening.md).

### 9.3 Beneficial ownership

**Source says:**
- **COBE Act ([S33](#24-sources-consulted), MEDIUM-HIGH; consolidation to 2020).** The threshold is "more
  than twenty per centum" of shares or votes, or other significant control. Changes must be filed within 7
  days. The Registrar-held information is "public information" and is accessible to financial institutions
  and DNFBPs. Records are kept at least 5 years after dissolution.
- **MLPCA ([S21](#24-sources-consulted)).** Control is deemed at 25% or more of shares or voting rights.
- **PVO Act ([S34](#24-sources-consulted)).** "Beneficial owner" and "controller", including control
  through "the size of that person's contributions". A significant voice includes 25% or more of governing
  body votes. Material changes must be notified within one month.

**Interpretation:** KYB must capture beneficial ownership using the strictest applicable test for the
entity type: more than 20% for companies, and 25% plus controllers for PVOs. Where possible, the captured
ownership should be checked against Registrar records. See [kyb-architecture.md](kyb-architecture.md)
(LR-054).

### 9.4 FATF and ESAAMLG context

**Source says:**
- Zimbabwe was removed from the FATF grey list in March 2022 ([S27](#24-sources-consulted), MEDIUM;
  secondary).
- Zimbabwe does not appear on the FATF "Jurisdictions under Increased Monitoring" list of 19 June 2026
  ([S28](#24-sources-consulted), HIGH-MEDIUM; a regulator's reproduction).
- ESAAMLG's April 2024 follow-up report rates Recommendation 8 (NPOs) "partially compliant", with Zimbabwe
  remaining in enhanced follow-up ([S29](#24-sources-consulted), [S46](#24-sources-consulted), HIGH).
- The 2016 mutual evaluation found a weak TF framework for NPOs ([S45](#24-sources-consulted), HIGH).

**Interpretation:** NPO and TF scrutiny is a live regulatory priority, so church and NGO campaigns may
attract attention. FATF status must be re-checked after each plenary. It is a configuration input to the
high-risk-jurisdiction list (LR-055).

### 9.5 Virtual assets

**Source says ([S31](#24-sources-consulted), HIGH that it exists):** S.I. 99 of 2026 (VASP registration,
10 June 2026) requires VASP registration and travel-rule compliance.

**Interpretation:** Crypto donations are **out of scope**. Accepting them would likely require a registered
VASP and a new ADR.

## 10. Consumer protection

**Source says ([S40](#24-sources-consulted), HIGH; Consumer Protection Act [Chapter 14:44], gazetted 10 December 2019):**
- **"Transaction" (s2).** Requires the supply of goods or services "in exchange for consideration".
- **Scope (s3(3)).** The Act applies whether or not the supplier is resident and whether it "operates on a
  profit basis or otherwise".
- **Online disclosures (s52).** Suppliers transacting electronically must disclose identity and contact
  details, "the full price … including … any other fees or costs", "the return, exchange and refund
  policy", and "the security procedures and privacy policy". The consumer must be able to review and
  correct before ordering, and payment systems must be "sufficiently secure".
- **Cooling-off (s53).** A consumer may cancel "without reason and without penalty any electronic
  transaction … within seven days", with a full refund within 14 days.
- **Unsolicited communications (s54).** Unsubscribe options are required.
- **Complaints (ss4, 33).** The Consumer Protection Commission receives complaints, and consumers may be
  heard before the Commission or a court.

**Interpretation (LR-018, LR-056):**
- **Donations vs fees.** A pure donation arguably lacks consideration and so is not a "transaction".
  Platform fees, and any donor "tip", probably are.
- **Design to s52 regardless.** FundZim should design its checkout to s52 in any case: full fee disclosure
  before confirmation, review-and-correct, and published refund and privacy policies.
- **Cooling-off exposure.** If s53 applies to fees or donations, a 7-day cancellation right interacts with
  payout timing and reserves ([donor-protection-policy.md](donor-protection-policy.md)).
- **Commission status.** Whether the Consumer Protection Commission is operational, and through what
  channel, is unverified.

## 11. Personal data protection

Summarised here. The full analysis is in [data-protection-assessment.md](data-protection-assessment.md).

**Source says:**
- **POTRAZ.** The CDPA (Act 5/2021, [S35](#24-sources-consulted), HIGH) designates POTRAZ as the Data
  Protection Authority.
- **Licensing ([S36](#24-sources-consulted), HIGH; S.I. 155 of 2024, Gazette supplement 13 September
  2024).** Any person determining the purposes and means of processing must hold a data-controller
  licence. Licences are tiered by data-subject count and renewed annually.
- **DPO.** One must be appointed and notified to POTRAZ.
- **Breaches.** Must be notified to POTRAZ within 24 hours. High-risk breaches must be notified to data
  subjects within 72 hours.
- **Sensitive data.** Health, genetic and biometric data require written consent.
- **Children.** A child is anyone under 18, and their data requires verified parental or guardian consent.
- **Cross-border transfers.** Permitted only to adequate jurisdictions, or under s29 exceptions, with
  notification to POTRAZ.
- **Enforcement ([S37](#24-sources-consulted), MEDIUM).** POTRAZ inspections were reported to begin from
  1 September 2026.

**Interpretation:**
- **Controller licence.** FundZim will be a data controller and must be licensed *before* processing.
- **Campaign content.** Medical-appeal and child campaigns involve sensitive data, so consent must come
  from the right person.
- **KYC biometrics.** Liveness or face-match KYC is biometric processing.
- **Offshore processing.** Any offshore hosting or vendor processing is a cross-border transfer (LR-010,
  LR-011, LR-014, LR-057, LR-058).

## 12. Cybersecurity

**Source says:**
- S.I. 80/2020 s4(9) requires money transmission and mobile banking providers to maintain "policies and
  procedures for sound internal controls and risk management practices including data protection and
  cyber security" ([S02](#24-sources-consulted), HIGH).
- An RBZ cyber security framework circular (NPS/02/2021) and a 2025 RBZ Cybersecurity and Resilience
  Guideline reportedly bind PSPs and banks. Both are search-snippet only, LOW, and **UNVERIFIED**.
- CDPA s35 inserted computer crimes (hacking, unlawful acquisition of data, identity-related offences) into
  the Criminal Law Code, with extraterritorial reach ([S35](#24-sources-consulted), HIGH).
- SI 155 s16 requires controllers to implement security measures ([S36](#24-sources-consulted), HIGH).

**Interpretation:** These bind the PSP directly. Expect the PSP to pass equivalent obligations to FundZim by
contract: API security, incident notification, and vendor due diligence. FundZim's own baseline is in
[../SECURITY.md](../SECURITY.md) (LR-040).

## 13. Record retention

**Verified periods (each binds the named class of person; applicability to FundZim is `LEGAL_REVIEW_REQUIRED`, LR-012):**

| Source | Applies to | Records | Period |
|---|---|---|---|
| RBZ 2017 Guidelines para 9.2 ([S04](#24-sources-consulted)) | Payment system providers and participants | Payment records | Minimum 10 years, unless AML/CFT law prescribes longer |
| S.I. 80/2020 s4(6) ([S02](#24-sources-consulted)) | Money transmission and mobile banking providers | Periodic returns | 7 years |
| MLPCA s24(2) ([S21](#24-sources-consulted)) | Financial institutions and DNFBPs | CDD, transaction records, unusual-transaction findings, STR copies | At least 5 years (from relationship end or transaction) |
| MLPCA s27(8) ([S21](#24-sources-consulted)) | Intermediary institutions | Cross-border wire information (technical limits) | At least 10 years |
| COBE Act s72(9) ([S33](#24-sources-consulted)) | Companies | Beneficial-ownership records | 5 years after dissolution |
| S.I. 98/2026 s28 ([S42](#24-sources-consulted)) | PVOs | Transactions; accounting records | 5 years; 7 years |
| CDPA s13 ([S35](#24-sources-consulted)) | Data controllers | Personal data generally | "no longer than is necessary" |

**Interpretation:**
- **Conservative design default.** Until counsel rules, the design default is to retain financial and
  transaction records for **at least 10 years** (the longest period found, and the one a PSP is likely to
  impose contractually), and KYC records for at least 5 years after the relationship ends. These defaults
  are configuration under LR-012. They are **not** FundZim's legal conclusion.
- **Lawful basis for keeping data.** Retention beyond the CDPA minimisation default relies on the legal
  obligation basis (CDPA s10(3)).
- **Tax retention.** Tax and accounting retention periods were not researched.

## 14. Complaints and disputes

**Source says:** The Consumer Protection Act gives consumers the right to be heard before the Commission or a
court and prohibits retaliation (s33). It provides for conciliation and arbitration via consumer protection
organisations and officers (ss55–57) ([S40](#24-sources-consulted), HIGH). No payment-specific complaint
timeline was found in the RBZ texts read.

**Interpretation:** FundZim needs a documented complaints process with timestamps and outcomes. Statutory
response timelines remain unknown (LR-086). Card disputes follow scheme and provider rules (PCR); see
[../payments/refund-and-dispute-architecture.md](../payments/refund-and-dispute-architecture.md).

## 15. Taxation and reporting

**Source says:**
- **IMTT on ZiG ([S12](#24-sources-consulted), HIGH).** The Finance Act 2025 (Act 7 of 2025) sets IMTT at
  1.5% per unit of local currency transacted, from 1 January 2026, with a flat local-currency equivalent of
  US$10,150 for single transactions of US$500,000 or more.
- **IMTT on USD ([S13](#24-sources-consulted), MEDIUM; secondary).** The 2% rate on USD transactions
  reportedly remains.
- **Who collects IMTT ([S15](#24-sources-consulted), MEDIUM).** Financial institutions, including mobile
  money operators, withhold and remit it. No donation or charity exemption is listed.
- **VAT rate ([S12](#24-sources-consulted), [S16](#24-sources-consulted), HIGH).** 15.5% from 1 January
  2026.
- **VAT registration ([S17](#24-sources-consulted), HIGH).** Compulsory above "US$25,000.00 or ZiG
  equivalent" of taxable supplies in 12 months.
- **Fiscal invoices ([S12](#24-sources-consulted), HIGH).** The Finance Act 2025 adds TIN and FDMS
  QR-code requirements to tax invoices.
- **Digital services withholding tax ([S12](#24-sources-consulted), HIGH for the mechanism;
  [S18](#24-sources-consulted), MEDIUM for the 15.5% rate).** Applies from 1 January 2026 and is withheld by
  financial intermediaries on amounts remitted outside Zimbabwe to non-resident electronic-commerce
  operators.
- **Donations received.** No authoritative source was found on whether gifts to individuals are taxable.
  PVOs can obtain income-tax exemption only with ZIMRA approval ([S19](#24-sources-consulted), MEDIUM).

**Interpretation (LR-016, LR-017, LR-045, LR-059):**
- **IMTT on both legs.** IMTT may apply on *both* the collection leg and the payout leg, materially reducing
  what beneficiaries receive. Donors and owners need disclosure.
- **VAT on fees.** FundZim's platform fees are prima facie standard-rated supplies once it is registered.
  Whether gross donations count as FundZim's supplies depends on agency vs principal structuring, which
  links to Model A.
- **Entity location.** A Zimbabwe-resident operating entity avoids DSWT on its own fees. An offshore
  contracting entity would not.
- **Receipts.** FundZim issues payment confirmations, not tax receipts (LR-017).

## 16. Children

**Source says:** The Children's Amendment Act 2023 defines a child as a person under 18
([S43](#24-sources-consulted), HIGH), and the CDPA uses the same definition ([S35](#24-sources-consulted)).
The POTRAZ children's guideline ([S38](#24-sources-consulted), HIGH-MEDIUM) states that children "do not
have the capacity to contract". It also requires upfront age verification, verified guardianship
documents, and prior POTRAZ authorisation for transfers of minors' data to inadequate jurisdictions. The
guidelines "do not have the force of law" ([S39](#24-sources-consulted), MEDIUM).

**Interpretation (LR-021, LR-015, LR-058):**
- **Minors and accounts.** Minors should not hold accounts, run campaigns, or receive payouts directly.
- **Campaigns for minors.** These need a verified parent or guardian as owner, with written consent for
  health data, and minimal identifying content.

## 17. Electronic transactions

**Source says:** The Electronic Transactions and Electronic Commerce Bill had not been presented to
Parliament as of the 28 October 2025 State of the Nation Address ([S44](#24-sources-consulted), HIGH). The
online-transaction rules in force are CPA ss52–54 and the CDPA.

**Interpretation (LR-027):** The validity of click-wrap acceptance and of electronic consent for "written
consent" under CDPA ss11–12 is unconfirmed. FundZim's acceptance records are designed to be strong evidence
anyway: versioned terms, timestamps, actor, and IP/device where lawful
([audit-evidence-model.md](audit-evidence-model.md)).

## 18. Not applicable, with reasons

| Area | Why it is not applicable to the MVP |
|---|---|
| Securities and investment crowdfunding (SECZ) | FundZim offers no equity, securities, returns or lending ([../PRODUCT.md](../PRODUCT.md) §2). Re-assess if the product scope ever changes. |
| Lending and credit regulation | No loans or credit. |
| Virtual-asset (VASP) registration | No crypto acceptance (§9.5). |
| Deposit-taking / banking licence | FundZim takes no deposits, under every model it considers. Model B is not selected. |

These are scope statements, **not** legal conclusions that the regimes could never apply. Counsel should
confirm the SECZ point (LR-041).

## 19. Cross-cutting conclusions for architecture (all provisional)

1. **No FundZim-held funds.** Prefer a model where a licensed PSP holds and moves all funds: NPS Act s18,
   S.I. 80/2020, the 2017 Guidelines and MLPCA s2(1)(d)/(j)/(k) all point the same way. → [ADR-013](../adr/ADR-013-regulatory-operating-model.md).
2. **The ledger is a record, not a value store.** No wallets, no spendable balances, and no P2P transfers. →
   [ADR-014](../adr/ADR-014-accounting-separated-from-custody.md).
3. **Fundraising authority is a first-class campaign attribute,** gated before publication and before
   payout (PVO Act).
4. **Build AML controls to the accountable-institution standard** even before status is determined: KYC
   tiers, screening, STR escalation, tipping-off-safe UX, and records retained at least 5 years.
5. **Currency isolation; FundZim never converts** (exchange control). → [ADR-018](../adr/ADR-018-currency-isolation-and-fx.md).
6. **POTRAZ licence and DPO before processing real personal data;** treat health, child and biometric data as
   requiring written consent.
7. **Disclose IMTT and fees** to donors and owners. Design the checkout to CPA s52.

## 20. Monitoring triggers

These are events that should prompt a review of this document:
- An RBZ circular on aggregators, crowdfunding, QR or cyber resilience.
- Any NPS Act amendment.
- A court ruling on the PVO Amendment Act or SI 155/2024.
- Guidance from the PVO Registrar on online crowdfunding or individual fundraising.
- An FATF plenary outcome.
- A Finance Act or mid-term budget changing IMTT, VAT or DSWT.
- Any instrument changing the 31 December 2030 multi-currency date.
- New POTRAZ guidelines.
- A section 101 DNFBP designation or s2(3) MLPCA declaration.

The owner is the compliance lead; until appointed, the founders. Review this document at least quarterly
until launch.

## 21. Research checklist (open, not yet verified)

All of these are carried as UNVERIFIED rows in the register (§6). The most material items:
- **Payments law:**
  - the current NPS Act text and any amendment after 2016;
  - the gazetted text of S.I. 17/2025;
  - whether the 2017 Guidelines have been superseded;
  - any RBZ aggregator or crowdfunding rule;
  - s18(4) exemption notices.
- **Currency and exchange control:**
  - the S.I. 85/2020 and S.I. 218/2023 texts;
  - the Act giving parliamentary force to RBZ Act s44D;
  - the ZWG minor unit (ISO 4217 exponent);
  - whether cross-border donations need a remittance operator.
- **Tax:**
  - the full IMTT exemption list;
  - ZIMRA PN 05/2026 (DSWT);
  - 2026 mid-term budget changes;
  - income tax on gifts to individuals.
- **AML and sanctions:**
  - the FATF primary source;
  - S.I. 110/2021 and S.I. 164/2023;
  - the domestic sanctions list;
  - the FIU high-risk jurisdictions directive (17 February 2026);
  - the dollar values of standard-scale fine "levels".
- **PVO Act:**
  - the Registrar's treatment of individual and self-fundraising;
  - the commencement date of Act 1/2025;
  - court rulings on the validity of the PVO Amendment Act and SI 155.
- **Data protection:** POTRAZ notices and its consent and cross-border guidelines.
- **Other:**
  - Consumer Protection Commission operations;
  - Children's Act rules on publishing a child's identity;
  - municipal or police collection permits;
  - Deeds Registries Act s70A.

## 22. What this document does not do

It does not determine that FundZim is permitted to operate. It does not determine that any licence is or is
not required, and it does not replace counsel. A Stage 1 PASS is a documentation milestone only.

## 23. Change log

| Date | Change |
|---|---|
| 2026-10-08 | Initial Stage 1 research map |

## 24. Sources consulted

All sources were accessed on **2026-10-08**. Pages that could not be opened are not listed as sources (§2).

| ID | Source | URL | Source date | Confidence |
|---|---|---|---|---|
| S01 | National Payment Systems Act [Ch. 24:23] (Veritas consolidation, Act 21/2001; S.I. 262/2006) | https://www.veritaszim.net/sites/veritas_d/files/National%20Payment%20Systems%20Act.pdf | Consolidated to 2006 | HIGH (text); MEDIUM (currency) |
| S02 | S.I. 80 of 2020, Banking (Money Transmission, Mobile Banking and Mobile Money Interoperability) Regulations (Gazette Extraordinary No. 26) | https://archive.gazettes.africa/archive/zw/2020/zw-government-gazette-dated-2020-03-27-no-26.pdf | 27 Mar 2020 | HIGH (text); MEDIUM (currency) |
| S03 | Afriwise / Muvingi & Mugadza: S.I. 17 of 2025 licensing summary (secondary) | https://www.afriwise.com/blog/understanding-zimbabwes-new-licensing-regulations-for-money-transmission-mobile-banking-and-money-interoperability | 18 Mar 2025 | MEDIUM |
| S04 | RBZ Guidelines for Retail Payment Systems and Instruments | https://www.rbz.co.zw/documents/nps/payment-systems-guidelines-august-2017.pdf | Effective 1 Jul 2017 | HIGH (text); MEDIUM (currency) |
| S05 | Insiderzim: RBZ designates ZimSwitch national payment switch | https://insiderzim.com/reserve-bank-of-zimbabwe-designates-zimswitch-national-payment-switch/amp/ | 9 Jul 2020 | MEDIUM |
| S06 | Business Times: RBZ issues crowdfunding licences | https://businesstimes.co.zw/rbz-issues-crowdfunding-licences-to-local-firms/ | 16 Jun 2022 | LOW |
| S07 | S.I. 60 of 2024, Presidential Powers (Temporary Measures) (Amendment of RBZ Act and Issue of Zimbabwe Gold Notes and Coins) Regulations | https://www.veritaszim.net/sites/veritas_d/files/SI%202024-060%20Presidential%20Powers%20(Temporary%20Measures)%20(Zimbabwe%20Gold%20Notes%20and%20Coins)%20Regulations,%202024.pdf | 5 Apr 2024 | HIGH |
| S08 | ZimLive: ZiG remains legal tender, says RBZ | https://www.zimlive.com/zig-remains-legal-tender-says-rbz-amid-legitimacy-storm/ | 29 Oct 2024 | MEDIUM |
| S09 | RBZ Foreign Exchange Transactions Guideline (FXD 2/2025), via Zimbabwe Embassy mirror | https://zimembassydc.org/wp-content/uploads/2025/08/GUIDELINES-TO-AUTHORISED-DEALERS-AND-THEIR-CLIENTS-ON-FOREIGN-EXCHANGE-TRANSACTIONS-25-Apr-25-.pdf | Apr 2025 | HIGH |
| S10 | NewZimbabwe: RBZ abandons 2030 mono-currency deadline | https://www.newzimbabwe.com/reserve-bank-of-zimbabwe-abandons-2030-mono-currency-deadline/ | 28 Feb 2026 | MEDIUM |
| S11 | Zimsphere: RBZ refutes plans to convert diaspora remittances to ZiG | https://www.zimsphere.co.zw/2024/08/rbz-refutes-plans-to-convert-diaspora-remittances-to-zig.html?m=1 | 17 Aug 2024 | MEDIUM |
| S12 | Finance Act, 2025 (Act No. 7 of 2025) | https://www.veritaszim.net/sites/veritas_d/files/Finance%20Act%2C%20Act%20No.%207%20of%202025.pdf | Gazetted Dec 2025 (secondary sources: 29 Dec 2025) | HIGH |
| S13 | 263Chat via allAfrica: USD IMTT 2% unchanged | https://allafrica.com/stories/202511280037.html | 27 Nov 2025 | MEDIUM |
| S14 | Muvingi & Mugadza: Finance (No. 2) Act 2024 tax amendments | https://www.mmmlawfirm.co.zw/key-amendments-to-zimbabwes-tax-laws-under-the-finance-act-2024/ | 2024/25 | MEDIUM |
| S15 | Financial Gazette (Baker Tilly): Unpacking IMTT | https://fingaz.co.zw/2026/05/06/unpacking-intermediated-money-transfer-tax/ | 6 May 2026 | MEDIUM |
| S16 | ZIMRA Public Notice 07 of 2026 (VAT rate 15.5%) | https://www.zimra.co.zw/public-notices?download=4441%3Apublic-notice-07-of-2026-change-of-vat-rate-on-submission-of-return-category-a&start=20 | Feb 2026 | HIGH |
| S17 | ZIMRA VAT registration page | https://www.zimra.co.zw/domestic-taxes/vat/vat-registration | Threshold effective 1 Jan 2024 | HIGH |
| S18 | KPMG TaxNewsFlash: Zimbabwe digital services withholding | https://kpmg.com/us/en/taxnewsflash/news/2026/01/tnf-zimbabwe-digital-services-withholding-in-lieu-of-nonresident-vat-collection.html | 22 Jan 2026 | MEDIUM |
| S19 | Financial Gazette: Taxation of PVOs in Zimbabwe | https://fingaz.co.zw/2026/07/25/taxation-of-private-voluntary-organisations-in-zimbabwe/ | 25 Jul 2026 | MEDIUM |
| S21 | Money Laundering and Proceeds of Crime Act [Ch. 9:24] (FIU consolidation to Act 7/2025) | https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf | Current to Act 7/2025 | HIGH |
| S22 | FIU guidelines page | https://www.fiu.co.zw/index.php/guidelines/ | Undated | HIGH (existence) |
| S23 | FIU PVOs page | https://www.fiu.co.zw/index.php/pvos/ | Undated (items June 2026) | HIGH (existence) |
| S24 | FIU Guidelines for PVOs | https://www.fiu.co.zw/wp-content/uploads/2025/05/PVOs.pdf | Undated | MEDIUM |
| S25 | FIU AML/CFT Directive 01/04/2024 (CTR/EFT/IFT thresholds) | https://www.fiu.co.zw/wp-content/uploads/2025/05/DIRECTIVE-01-04-2024-REVISED-THRESHOLDS-FOR-CTRs-EFTs-AND-IFTs.pdf | 7 Apr 2024 | HIGH |
| S26 | FIU AML/CFT/CPF Directive PFIU21/10/2024 (civil penalties) | https://www.fiu.co.zw/wp-content/uploads/2025/05/AML_CFT_CPF-DIRECTIVE-NO.-PFIU21102024.pdf | 21 Oct 2024 | HIGH (pp. 1–3) |
| S27 | The Herald: FATF grey list exit | https://www.heraldonline.co.zw/fatf-grey-list-exit-huge-step-for-zim-2/ | 9 Mar 2022 | MEDIUM |
| S28 | Securities Commission of The Bahamas: FATF increased-monitoring list, June 2026 | https://scb.gov.bs/wp-content/uploads/2026/06/Financial-Action-Task-Force-Public-Statement-on-list-of-Jurisdictions-under-Increased-Monitoring-June-2026.pdf | 25 Jun 2026 | HIGH-MEDIUM |
| S29 | ESAAMLG: Zimbabwe 10th Enhanced Follow-Up Report, April 2024 | https://www.esaamlg.org/reports/Zimbabwe_FUR-April%202024.pdf | Apr 2024 | HIGH |
| S30 | FIU: 2024 Zimbabwe NPO Risk Assessment | https://www.fiu.co.zw/wp-content/uploads/2025/05/2024-Zimbabwe-NPO-Risk-Assessment.pdf | 2024 | HIGH |
| S31 | S.I. 99 of 2026, MLPC (VASP Registration) Regulations | https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-99of2026MoneyLaundering.pdf | 10 Jun 2026 | HIGH (existence) |
| S32 | S.I. 76 of 2014, Suppression of Foreign and International Terrorism (UNSCR application) Regulations | https://www.fiu.co.zw/wp-content/uploads/2025/05/S.I.-76-of-2014-Suppression-of-Foreign-International-Terrorism.pdf | 2014 | HIGH |
| S33 | Companies and Other Business Entities Act [Ch. 24:31] (Veritas consolidation to Act 8/2020) | https://www.veritaszim.net/sites/veritas_d/files/Companies%20&%20Other%20Business%20Entities%20Act%20Cap%2024,31.pdf | Consolidated to 2020 | MEDIUM-HIGH |
| S34 | Private Voluntary Organisations Act [Ch. 17:05] (Veritas consolidation to 11 Apr 2025, incl. Act 1/2025) | https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf | 11 Apr 2025 | HIGH (text); MEDIUM (validity) |
| S35 | Cyber and Data Protection Act [Ch. 12:07] (Act 5/2021; GN 492/2022) | https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf | 3 Dec 2021; republished 11 Mar 2022 | HIGH |
| S36 | S.I. 155 of 2024, Cyber and Data Protection (Licensing of Data Controllers and Appointment of DPOs) Regulations | https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf | 13 Sep 2024 | HIGH |
| S37 | Veritas Bill Watch 27-2026 (POTRAZ inspections) | https://www.veritaszim.net/node/8052 | 28 Jul 2026 | MEDIUM |
| S38 | POTRAZ guideline: Processing of Children's Personal Information (Veritas) | https://www.veritaszim.net/sites/veritas_d/files/02%20Processing%20of%20Children%27s%20Personal%20Information.pdf | Undated | HIGH-MEDIUM |
| S39 | Veritas Bill Watch 29-2026 (status of POTRAZ guidelines) | https://www.veritaszim.net/node/8054 | 4 Aug 2026 | MEDIUM |
| S40 | Consumer Protection Act [Ch. 14:44] (No. 5 of 2019) | https://commons.laws.africa/akn/zw/act/2019/5/media/publication/zw-act-2019-5-publication-document.pdf | 10 Dec 2019 | HIGH |
| S41 | S.I. 97 of 2026, PVO (Board and General) Regulations | https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-97of2026-PVO-Board-General.pdf | 5 Jun 2026 | HIGH |
| S42 | S.I. 98 of 2026, PVO (Risk-Based Supervision and Protection from TF Abuse) Regulations | https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-98of2026-PVO-Risk-Based-Supervision.pdf | 5 Jun 2026 | HIGH |
| S43 | Children's Amendment Act, 2023 (No. 8 of 2023) | https://commons.laws.africa/akn/zw/act/2023/8/media/publication/zw-act-2023-8-publication-document.pdf | 2023 | HIGH |
| S44 | Veritas: State of the Nation Address, 28 October 2025 | https://www.veritaszim.net/node/7687 | 28 Oct 2025 | HIGH |
| S45 | ESAAMLG: Zimbabwe 2nd Round Mutual Evaluation Report | https://www.esaamlg.org/reports/2ND-ROUND-MUTUAL-EVALUATION-REPORT-OF-THE-REPUBLIC-OF-ZIMBABWE(1).pdf | Sep 2016 | HIGH |
| S46 | ESAAMLG member page: Zimbabwe | https://www.esaamlg.org/index.php/Countries/readmore_members/Zimbabwe | Posted 7 Aug 2024 | HIGH |
| S47 | NZ Department of Internal Affairs: jurisdictions under increased monitoring, June 2026 | https://www.dia.govt.nz/AML-CFT-Jurisdictions-under-increased-monitoring-June-2026 | 24 Jun 2026 | MEDIUM |

(S20 is intentionally unused. Provider-side sources are in [../payments/provider-capability-matrix.md](../payments/provider-capability-matrix.md).)
