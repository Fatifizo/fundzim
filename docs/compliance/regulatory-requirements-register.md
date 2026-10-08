# FundZim — Regulatory Requirements Register

**Stage 1 deliverable.** Verification date for every row: **2026-10-08**. **Not legal advice.**

This register lists each regulatory requirement identified in Stage 1 research that may affect FundZim. It
gives the source, how confident we are, who owns it, the proposed technical control, and the evidence that
would close it. The narrative context is in [regulatory-landscape.md](regulatory-landscape.md). Open legal
questions (`LR-xxx`) are in [open-legal-questions.md](open-legal-questions.md). Provider questions (`PCR-xxx`)
are in [../payments/provider-questions.md](../payments/provider-questions.md).

## 1. Classification semantics

| Classification | Meaning in this register |
|---|---|
| `CONFIRMED` | A primary source we read verifies that the requirement **exists**, and on the face of the text it reaches an activity FundZim plans under **every** operating model considered (for example, processing personal data). It does **not** mean counsel has confirmed applicability or interpretation; no counsel has reviewed any row yet. |
| `POTENTIALLY_APPLICABLE` | The requirement exists (primary or reputable source). Whether it binds FundZim depends on facts not yet fixed: the operating model, FundZim's legal status (e.g. "financial institution"), entity structure, the campaign owner type, or features chosen. |
| `LEGAL_REVIEW_REQUIRED` | Applicability **or** content needs legal interpretation before design can rely on it, or the source is unverified, disputed or possibly superseded. Every such row links to an `LR` item. |
| `PROVIDER_CONFIRMATION_REQUIRED` | The obligation binds FundZim's payment provider or bank. FundZim needs the provider's confirmation, evidence or contract terms to rely on it. |
| `NOT_APPLICABLE_WITH_REASON` | Out of scope for the planned product, with the reason stated. This is a scope statement, not a legal conclusion that the regime could never apply. |

**Confidence:**
- **HIGH:** the primary official text was read.
- **MEDIUM:** a reputable secondary source, or a primary source that may be superseded.
- **LOW:** weak, indirect or snippet-only evidence.

Where a row says "HIGH (text); MEDIUM (currency)", the wording was verified but we could not confirm it is
the latest version.

**Responsible roles** use the Stage 1 operational functions
([operational-controls.md](operational-controls.md)). External counsel advises; a FundZim role owns each
item.

**Proposed technical controls** are designs for later stages. None is implemented in Stage 1.

## 2. Summary index

| ID | Area | Subject | Classification | Confidence |
|---|---|---|---|---|
| [REQ-001](#req-001) | Payments (RBZ) | NPS Act s18 — accepting money for payment to third parties | `LEGAL_REVIEW_REQUIRED` | HIGH (text); MEDIUM (currency of consolidation) |
| [REQ-002](#req-002) | Payments (RBZ) | Recognised payment systems; participants are financial institutions | `PROVIDER_CONFIRMATION_REQUIRED` | HIGH (text); MEDIUM (currency) |
| [REQ-003](#req-003) | Payments (RBZ) | S.I. 80/2020 s3 — recognition of money transmission providers | `POTENTIALLY_APPLICABLE` | HIGH (text); MEDIUM (currency; amended by S.I. 17/2025) |
| [REQ-004](#req-004) | Payments (RBZ) | S.I. 80/2020 s4 — operating requirements for money transmission providers | `PROVIDER_CONFIRMATION_REQUIRED` | HIGH (text); MEDIUM (currency) |
| [REQ-005](#req-005) | Payments (RBZ) | S.I. 80/2020 s5 — transaction charges require prior RBZ approval | `PROVIDER_CONFIRMATION_REQUIRED` | HIGH (text); MEDIUM (currency) |
| [REQ-006](#req-006) | Payments (RBZ) | S.I. 17/2025 — licence fees and partnering requirement (secondary source) | `LEGAL_REVIEW_REQUIRED` | MEDIUM (secondary) |
| [REQ-007](#req-007) | Payments (RBZ) | 2017 Retail Payment Guidelines para 3.1 — authorisation to operate a system or issue instruments | `POTENTIALLY_APPLICABLE` | HIGH (text); MEDIUM (may be superseded) |
| [REQ-008](#req-008) | Payments (RBZ) | 2017 Guidelines — e-money backing, trust accounts, segregation, no interest | `POTENTIALLY_APPLICABLE` | HIGH (text); MEDIUM (currency) |
| [REQ-009](#req-009) | Payments (RBZ) | 2017 Guidelines para 9.2 — 10-year record retention | `POTENTIALLY_APPLICABLE` | HIGH (text); MEDIUM (currency) |
| [REQ-010](#req-010) | Payments (RBZ) | 2017 Guidelines para 10.1 — outsourcing requires RBZ authorisation | `PROVIDER_CONFIRMATION_REQUIRED` | HIGH (text); MEDIUM (currency) |
| [REQ-011](#req-011) | Payments (RBZ) / Exchange control | Remittance service providers must be registered and licensed | `LEGAL_REVIEW_REQUIRED` | HIGH (text); interpretation LOW |
| [REQ-012](#req-012) | Payments (RBZ) | National switch connection; QR payment routing | `PROVIDER_CONFIRMATION_REQUIRED` | MEDIUM (switch); LOW (QR — unverified) |
| [REQ-013](#req-013) | Payments (RBZ) | No crowdfunding/aggregator-specific RBZ instrument found | `LEGAL_REVIEW_REQUIRED` | LOW |
| [REQ-014](#req-014) | Currency | ZiG is legal tender alongside other prescribed currencies | `CONFIRMED` | HIGH (S.I. text); MEDIUM (legal basis after lapse dispute) |
| [REQ-015](#req-015) | Currency | Multi-currency legal tender until 31 December 2030 | `CONFIRMED` | HIGH (RBZ document says this); MEDIUM (underlying SIs not read; 2026 policy shift secondary) |
| [REQ-016](#req-016) | Exchange control | Only Authorised Dealers / ADLAs may buy and sell foreign currency | `CONFIRMED` | HIGH |
| [REQ-017](#req-017) | Exchange control | FCAs may receive donations and diaspora remittances as free funds; NGO FCAs | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-018](#req-018) | Exchange control | Outbound donations and remittances restricted | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-019](#req-019) | Exchange control | FCA cash withdrawal limits | `NOT_APPLICABLE_WITH_REASON` | HIGH |
| [REQ-020](#req-020) | Currency | ZWG minor unit not defined by S.I. 60/2024 | `LEGAL_REVIEW_REQUIRED` | HIGH (absence in this SI); LOW (any other source) |
| [REQ-021](#req-021) | Charitable collections (PVO) | PVO Act s6(3) — no collection of contributions from the public except per the Act | `LEGAL_REVIEW_REQUIRED` | HIGH (text); MEDIUM (validity of Act 1/2025 disputed) |
| [REQ-022](#req-022) | Charitable collections (PVO) | PVO Act s23 — offence to collect or instruct another to collect without authority | `LEGAL_REVIEW_REQUIRED` | HIGH (text); MEDIUM (validity) |
| [REQ-023](#req-023) | Charitable collections (PVO) | PVO Act s6(2) — bodies with PVO objects collecting from the public must register | `POTENTIALLY_APPLICABLE` | HIGH (text); MEDIUM (validity) |
| [REQ-024](#req-024) | Charitable collections (PVO) | PVO Act s8 temporary authority and S.I. 97/2026 s16 conditions | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-025](#req-025) | Charitable collections (PVO) | PVO s20A — donor identification, refusal of illegitimate funds, formal channels, non-partisanship | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-026](#req-026) | Charitable collections (PVO) | S.I. 98/2026 — PVO foreign-funding disclosure, EDD and beneficiary-record thresholds | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-027](#req-027) | Charitable collections (PVO) / AML | S.I. 98/2026 — PVO STRs and TF red flags | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-028](#req-028) | Charitable collections (PVO) | PVO Act s26 — Ministerial freezing/return of unlawfully collected contributions | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-029](#req-029) | Charitable collections (PVO) | Validity and commencement of the PVO Amendment Act 2025 | `LEGAL_REVIEW_REQUIRED` | MEDIUM |
| [REQ-030](#req-030) | AML/CFT | MLPCA s2(1) — 'financial institution' definition | `LEGAL_REVIEW_REQUIRED` | HIGH |
| [REQ-031](#req-031) | AML/CFT | MLPCA s15–19 — CDD triggers, beneficial owners, reliance, non-face-to-face | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-032](#req-032) | AML/CFT | MLPCA s20 — EDD for high-risk customers and PEPs | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-033](#req-033) | AML/CFT | MLPCA s24 / s27(8) — record keeping | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-034](#req-034) | AML/CFT | MLPCA s30–31 — STRs within 3 working days; tipping-off prohibited | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-035](#req-035) | AML/CFT | MLPCA s25 — internal AML programme and compliance officer | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-036](#req-036) | AML/CFT | MLPCA s6E — FIU information requests to companies and PVOs | `CONFIRMED` | HIGH |
| [REQ-037](#req-037) | AML/CFT | FIU Directive 01/04/2024 — CTR/EFT/IFT thresholds; non-bank monthly CTRs via goAML | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-038](#req-038) | AML/CFT | MLPCA s27 — wire-transfer originator/beneficiary information | `PROVIDER_CONFIRMATION_REQUIRED` | HIGH |
| [REQ-039](#req-039) | AML/CFT | FIU Directive PFIU21/10/2024 — civil penalty regime | `POTENTIALLY_APPLICABLE` | HIGH (pp. 1–3 read) |
| [REQ-040](#req-040) | Sanctions | S.I. 76/2014 — screening against UN and Zimbabwean lists; freeze within 24 hours | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-041](#req-041) | Beneficial ownership | COBE Act s72–73 — company beneficial-ownership register | `POTENTIALLY_APPLICABLE` | MEDIUM-HIGH (consolidation to 2020) |
| [REQ-042](#req-042) | Beneficial ownership | Divergent BO thresholds across statutes | `POTENTIALLY_APPLICABLE` | HIGH (MLPCA, PVO); MEDIUM-HIGH (COBE) |
| [REQ-043](#req-043) | AML/CFT | FATF status and high-risk jurisdictions | `POTENTIALLY_APPLICABLE` | HIGH-MEDIUM |
| [REQ-044](#req-044) | AML/CFT | Virtual-asset service providers (S.I. 99/2026) | `NOT_APPLICABLE_WITH_REASON` | HIGH (existence) |
| [REQ-045](#req-045) | Data protection | S.I. 155/2024 — data-controller licence from POTRAZ | `CONFIRMED` | HIGH |
| [REQ-046](#req-046) | Data protection | DPO appointment and notification | `CONFIRMED` | HIGH |
| [REQ-047](#req-047) | Data protection | Sensitive data, health, genetic and biometric data — written consent | `CONFIRMED` | HIGH |
| [REQ-048](#req-048) | Data protection | Children's data — verified parental/guardian consent | `CONFIRMED` | HIGH (Act/SI); HIGH-MEDIUM (guideline) |
| [REQ-049](#req-049) | Data protection | Notification of biometric/genetic processing | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-050](#req-050) | Data protection | Cross-border transfer restrictions | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-051](#req-051) | Data protection | Breach notification — POTRAZ within 24 hours; data subjects within 72 hours (high risk) | `CONFIRMED` | HIGH |
| [REQ-052](#req-052) | Data protection | Written contracts with data processors | `CONFIRMED` | HIGH |
| [REQ-053](#req-053) | Data protection | Lawful basis, consent and processing principles | `CONFIRMED` | HIGH |
| [REQ-054](#req-054) | Data protection | Data subject rights and automated decisions | `CONFIRMED` | HIGH |
| [REQ-055](#req-055) | Data protection / Security | S.I. 155 s16 — security measures | `CONFIRMED` | HIGH |
| [REQ-056](#req-056) | Consumer protection | CPA s52 — e-commerce disclosures and review-before-order | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-057](#req-057) | Consumer protection | CPA s53 — 7-day cooling-off, refund within 14 days | `LEGAL_REVIEW_REQUIRED` | HIGH (text); applicability unresolved |
| [REQ-058](#req-058) | Consumer protection | CPA s54 — unsolicited electronic communications | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-059](#req-059) | Consumer protection | CPA complaints and redress | `POTENTIALLY_APPLICABLE` | HIGH (Act); Commission operational status unverified |
| [REQ-060](#req-060) | Tax | IMTT — 1.5% on ZiG from 1 Jan 2026; 2% on USD (secondary) | `PROVIDER_CONFIRMATION_REQUIRED` | HIGH (ZiG rate); MEDIUM (USD rate, scope, exemptions) |
| [REQ-061](#req-061) | Tax | VAT 15.5% from 1 Jan 2026; compulsory registration above US$25,000 | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-062](#req-062) | Tax | Fiscal tax invoices (TIN, FDMS code) | `POTENTIALLY_APPLICABLE` | HIGH |
| [REQ-063](#req-063) | Tax | Digital services withholding tax on non-resident e-commerce operators | `POTENTIALLY_APPLICABLE` | HIGH (mechanism); MEDIUM (rate) |
| [REQ-064](#req-064) | Tax | Income-tax treatment of donations received | `LEGAL_REVIEW_REQUIRED` | LOW–MEDIUM |
| [REQ-065](#req-065) | Cybersecurity | S.I. 80/2020 s4(9) — PSP internal controls incl. data protection and cyber security | `PROVIDER_CONFIRMATION_REQUIRED` | HIGH (S.I.); LOW (RBZ cyber documents) |
| [REQ-066](#req-066) | Children / capacity | Child = under 18; no capacity to contract | `CONFIRMED` | HIGH (definitions); MEDIUM (capacity statement via guideline) |
| [REQ-067](#req-067) | Electronic transactions | No electronic transactions statute in force | `LEGAL_REVIEW_REQUIRED` | HIGH (as at 28 Oct 2025) |
| [REQ-068](#req-068) | Scope | Securities / investment crowdfunding regulation | `NOT_APPLICABLE_WITH_REASON` | — |

**Counts:** `CONFIRMED` 14, `LEGAL_REVIEW_REQUIRED` 12, `NOT_APPLICABLE_WITH_REASON` 3, `POTENTIALLY_APPLICABLE` 31, `PROVIDER_CONFIRMATION_REQUIRED` 8 — total 68.

## 3. Priority view: launch-blocking rows

These rows block a live pilot (Stage 20) until resolved. The blocking legal questions are marked P0 in
[open-legal-questions.md](open-legal-questions.md):

- **Operating model:**
  - [REQ-001](#req-001) NPS Act s18
  - [REQ-008](#req-008) e-money/trust rules
  - [REQ-030](#req-030) MLPCA FI status
- **Who may fundraise:**
  - [REQ-021](#req-021) PVO s6(3)
  - [REQ-022](#req-022) PVO s23
  - [REQ-024](#req-024) s8 authority
  - [REQ-029](#req-029) validity of the Amendment Act
- **Data protection:**
  - [REQ-045](#req-045) POTRAZ licence
  - [REQ-046](#req-046) DPO
  - [REQ-047](#req-047) written consent for health data
  - [REQ-048](#req-048) children's data
  - [REQ-050](#req-050) cross-border transfer
  - [REQ-051](#req-051) breach notification
- **Currency:** [REQ-020](#req-020) ZWG minor unit, before ZiG is enabled.
- **Cross-border:** [REQ-011](#req-011) remittance characterisation, before international rails are enabled.
- **Consumer protection:** [REQ-057](#req-057) CPA s53 cooling-off, because it affects payout timing.

## 4. Requirements

### REQ-001

**NPS Act s18 — accepting money for payment to third parties** · Area: Payments (RBZ) · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** Only a participant in a recognised payment system, or a person introduced by one, may 'as a regular feature of his business, accept money or a payment instruction from any other person for the purpose of making a payment on behalf of that other person to a third person to whom the payment is due'. Exemptions: duly appointed agent of the payee (s18(3)(c)), Ministerial exemption by Gazette (s18(4)), others.
- **Applicability assessment (interpretation, not a legal conclusion):** Directly relevant to any model in which FundZim accepts donor money. Model A (licensed PSP accepts and disburses) is the natural fit; Model B appears to fall within s18(1) absent an exemption. Whether a donation is a payment 'due' is unresolved.
- **Source URL:** [S01](https://www.veritaszim.net/sites/veritas_d/files/National%20Payment%20Systems%20Act.pdf)
- **Source publication / effective date:** S01: Consolidated to S.I. 262/2006
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (currency of consolidation)
- **Responsible operational role:** Compliance officer (COMPLIANCE) with Counsel
- **Proposed technical control:** Model A only (ADR-013). No FundZim-owned collection account in any environment config; payout is an instruction to the PSP, never a FundZim bank transfer. Architecture test (Stage 3+) asserts no provider adapter targets a FundZim collection account.
- **Evidence required:** Counsel opinion on s18 applicability per model; PSP confirmation that it is a participant or introduced person; executed PSP contract.
- **Open questions:** LR-036, LR-037; PCR-001 (PSP recognition/introduction evidence)

### REQ-002

**Recognised payment systems; participants are financial institutions** · Area: Payments (RBZ) · Classification: `PROVIDER_CONFIRMATION_REQUIRED`

- **Description (source says):** RBZ recognises clearing systems whose participants are only financial institutions and the RBZ (s3(3)(a)); system rules set criteria for participants to introduce persons to provide payment services (s3(3)(d)(v)); financial institutions may not operate unrecognised clearing/settlement systems (s17).
- **Applicability assessment (interpretation, not a legal conclusion):** Binds FundZim's PSP/bank partners. FundZim's access to rails depends on its PSP being a participant or an introduced person.
- **Source URL:** [S01](https://www.veritaszim.net/sites/veritas_d/files/National%20Payment%20Systems%20Act.pdf)
- **Source publication / effective date:** S01: Consolidated to S.I. 262/2006
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (currency)
- **Responsible operational role:** Payments lead (Eng/FINANCE)
- **Proposed technical control:** Provider due diligence gate: no provider adapter may be enabled for live traffic without recorded evidence of the provider's recognition/introduction status (provider-due-diligence-checklist).
- **Evidence required:** RBZ recognition/authorisation letter or introducing bank's confirmation, held in the provider due-diligence file.
- **Open questions:** LR-004; PCR-001 (licensing evidence)

### REQ-003

**S.I. 80/2020 s3 — recognition of money transmission providers** · Area: Payments (RBZ) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** A 'money transmission provider' is 'any person who owns a payment system that facilitates the transmission of monies from one person to another'; such providers must obtain recognition of their payment system under NPS Act s3(1) and pay prescribed fees.
- **Applicability assessment (interpretation, not a legal conclusion):** Binds PSPs. Could reach FundZim if FundZim's own system moves value between users (wallets, P2P, internal transfers).
- **Source URL:** [S02](https://archive.gazettes.africa/archive/zw/2020/zw-government-gazette-dated-2020-03-27-no-26.pdf)
- **Source publication / effective date:** S02: 27 Mar 2020
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (currency; amended by S.I. 17/2025)
- **Responsible operational role:** Compliance officer (COMPLIANCE); Technical lead (Eng)
- **Proposed technical control:** No stored value, no P2P, no user-initiated internal transfers (CLAUDE.md, LR-003). Ledger balances are records of PSP-held funds, never spendable (ADR-014).
- **Evidence required:** Counsel view that FundZim does not 'own a payment system that facilitates the transmission of monies'; design review sign-off.
- **Open questions:** LR-003, LR-039

### REQ-004

**S.I. 80/2020 s4 — operating requirements for money transmission providers** · Area: Payments (RBZ) · Classification: `PROVIDER_CONFIRMATION_REQUIRED`

- **Description (source says):** Connection to the national switch; dedicated bank account; 'no money is transmitted or is retained on the payment system without a corresponding correct bank balance'; periodic returns incl. reconciliations kept 7 years; audits; RBZ read-only access; internal controls incl. data protection and cyber security; transaction screening and limits.
- **Applicability assessment (interpretation, not a legal conclusion):** Binds the PSP. FundZim relies on these controls (trust/dedicated account, reconciliations) for donor-fund safety under Model A.
- **Source URL:** [S02](https://archive.gazettes.africa/archive/zw/2020/zw-government-gazette-dated-2020-03-27-no-26.pdf)
- **Source publication / effective date:** S02: 27 Mar 2020
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (currency)
- **Responsible operational role:** Payments lead (Eng/FINANCE)
- **Proposed technical control:** Contract must commit the PSP to hold collected funds in its dedicated/trust account and to provide reconciliation/settlement reports FundZim ingests for three-way match (ledger/settlement-and-custody-model).
- **Evidence required:** PSP contract clauses; sample settlement and reconciliation reports; PSP audit report summary.
- **Open questions:** LR-038; PCR-007 (trust account, settlement reports)

### REQ-005

**S.I. 80/2020 s5 — transaction charges require prior RBZ approval** · Area: Payments (RBZ) · Classification: `PROVIDER_CONFIRMATION_REQUIRED`

- **Description (source says):** Providers may not levy or change transaction charges without prior RBZ approval.
- **Applicability assessment (interpretation, not a legal conclusion):** Binds the PSP; may constrain fee structures and any platform-fee split the PSP applies.
- **Source URL:** [S02](https://archive.gazettes.africa/archive/zw/2020/zw-government-gazette-dated-2020-03-27-no-26.pdf)
- **Source publication / effective date:** S02: 27 Mar 2020
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (currency)
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** Fee configuration records the provider's approved tariff reference; platform fee implemented as a separate, disclosed fee line (ledger), never hidden in PSP charges.
- **Evidence required:** PSP written confirmation that the fee/split structure is within its approved charges.
- **Open questions:** LR-038; PCR-001, PCR-016, PCR-034, PCR-042 (approved tariffs, split settlement approval)

### REQ-006

**S.I. 17/2025 — licence fees and partnering requirement (secondary source)** · Area: Payments (RBZ) · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** Reported: USD 5,000 application fee; annual fee 2% of gross turnover capped at USD 50,000; non-bank applicants must partner with a local authorised financial institution.
- **Applicability assessment (interpretation, not a legal conclusion):** Relevant only if FundZim itself ever needed a money-transmission licence (not the Model A plan). Gazetted text not read.
- **Source URL:** [S03](https://www.afriwise.com/blog/understanding-zimbabwes-new-licensing-regulations-for-money-transmission-mobile-banking-and-money-interoperability)
- **Source publication / effective date:** S03: 18 Mar 2025 (S.I. 17/2025 promulgated 28 Feb 2025 per source)
- **Verification date:** 2026-10-08
- **Confidence:** MEDIUM (secondary)
- **Responsible operational role:** Founders
- **Proposed technical control:** None in MVP; informs the cost case against Model B (operating-model-decision).
- **Evidence required:** Gazetted S.I. 17/2025 text.
- **Open questions:** LR-036; research checklist RC-02

### REQ-007

**2017 Retail Payment Guidelines para 3.1 — authorisation to operate a system or issue instruments** · Area: Payments (RBZ) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** 'No operator can operate a system or an issuer can issue and manage retail payment instruments unless it has been authorized by the Reserve Bank.' Operators need minimum initial capital of USD 1 million (para 6.1).
- **Applicability assessment (interpretation, not a legal conclusion):** FundZim avoids operating a system or issuing instruments by design (merchant/user of an authorised PSP). Applies if FundZim drifts into issuing stored value.
- **Source URL:** [S04](https://www.rbz.co.zw/documents/nps/payment-systems-guidelines-august-2017.pdf)
- **Source publication / effective date:** S04: Effective 1 Jul 2017
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (may be superseded)
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** Product rule: no instrument issuance (no FundZim cards, vouchers, wallets, balances). Change-control: any feature that creates a spendable balance requires ADR + counsel sign-off.
- **Evidence required:** Counsel confirmation; design review record.
- **Open questions:** LR-003, LR-039

### REQ-008

**2017 Guidelines — e-money backing, trust accounts, segregation, no interest** · Area: Payments (RBZ) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** User funds segregated from working capital (16.3); e-money fully backed by escrow/trust deposits (16.4); trust account administered by RBZ-approved trustees under a trust deed (17.1–17.3); weekly and monthly reconciliation (18.1(c)); no interest on e-money and no lending of user funds (20.1).
- **Applicability assessment (interpretation, not a legal conclusion):** Applies to e-money issuers and would apply to FundZim under Model B or any stored-value design. Under Model A the PSP's own arrangements must satisfy it.
- **Source URL:** [S04](https://www.rbz.co.zw/documents/nps/payment-systems-guidelines-august-2017.pdf)
- **Source publication / effective date:** S04: Effective 1 Jul 2017
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (currency)
- **Responsible operational role:** Compliance officer (COMPLIANCE); Finance lead (FINANCE)
- **Proposed technical control:** Model A; no FundZim-held funds; no interest/float income on beneficiary funds; PSP contract must confirm trust/escrow treatment.
- **Evidence required:** PSP trust-deed/escrow evidence; counsel confirmation.
- **Open questions:** LR-001, LR-003, LR-038

### REQ-009

**2017 Guidelines para 9.2 — 10-year record retention** · Area: Payments (RBZ) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Payment system providers and participants retain records for a 'minimum retention period… [of] ten years unless a higher minimum period is prescribed in terms of AML/CFT or electronic communications legislation'.
- **Applicability assessment (interpretation, not a legal conclusion):** Binds providers/participants; likely to be imposed on FundZim contractually by its PSP.
- **Source URL:** [S04](https://www.rbz.co.zw/documents/nps/payment-systems-guidelines-august-2017.pdf)
- **Source publication / effective date:** S04: Effective 1 Jul 2017
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (currency)
- **Responsible operational role:** Compliance officer (COMPLIANCE); Data Protection Officer
- **Proposed technical control:** Retention class FINANCIAL default ≥ 10 years (configuration; audit-evidence-model). Not hard-coded; LR-012 governs.
- **Evidence required:** Retention schedule approved by counsel/DPO; PSP contract retention clause.
- **Open questions:** LR-012

### REQ-010

**2017 Guidelines para 10.1 — outsourcing requires RBZ authorisation** · Area: Payments (RBZ) · Classification: `PROVIDER_CONFIRMATION_REQUIRED`

- **Description (source says):** Outsourcing by payment system providers requires specific RBZ authorisation.
- **Applicability assessment (interpretation, not a legal conclusion):** If the PSP treats FundZim's orchestration (payment initiation, payout instruction, KYC) as an outsourced function of its own regulated service, RBZ authorisation may be needed on the PSP side.
- **Source URL:** [S04](https://www.rbz.co.zw/documents/nps/payment-systems-guidelines-august-2017.pdf)
- **Source publication / effective date:** S04: Effective 1 Jul 2017
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (currency)
- **Responsible operational role:** Payments lead (Eng/FINANCE)
- **Proposed technical control:** Contract characterises roles explicitly (financial-responsibility-matrix); no reliance on FundZim KYC by the PSP unless the PSP confirms regulatory acceptability.
- **Evidence required:** PSP confirmation of how it characterises the FundZim relationship.
- **Open questions:** LR-004; PCR-003 (outsourcing characterisation)

### REQ-011

**Remittance service providers must be registered and licensed** · Area: Payments (RBZ) / Exchange control · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** 'All authorised or licensed remittances service providers, both domestic and international… need to be registered, licensed and are subject to regulation by the Reserve Bank' (para 13.1).
- **Applicability assessment (interpretation, not a legal conclusion):** Unclear whether cross-border donations (diaspora card or transfer donations) are remittances requiring a licensed remittance operator, or card payments to a merchant.
- **Source URL:** [S04](https://www.rbz.co.zw/documents/nps/payment-systems-guidelines-august-2017.pdf); [S09](https://zimembassydc.org/wp-content/uploads/2025/08/GUIDELINES-TO-AUTHORISED-DEALERS-AND-THEIR-CLIENTS-ON-FOREIGN-EXCHANGE-TRANSACTIONS-25-Apr-25-.pdf)
- **Source publication / effective date:** S04: Effective 1 Jul 2017; S09: Apr 2025 (FXD 2/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); interpretation LOW
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** International rails enabled only through a provider whose licensing covers the flow; rail config carries `cross_border_basis` evidence reference.
- **Evidence required:** Counsel opinion; provider confirmation of licence coverage for cross-border inflows.
- **Open questions:** LR-005, LR-044

### REQ-012

**National switch connection; QR payment routing** · Area: Payments (RBZ) · Classification: `PROVIDER_CONFIRMATION_REQUIRED`

- **Description (source says):** RBZ designated ZimSwitch as the national payment switch (2020); providers directed to connect. A 2026 RBZ QR guideline reportedly requires QR transactions to route via the national switch (UNVERIFIED).
- **Applicability assessment (interpretation, not a legal conclusion):** Binds providers. Relevant if FundZim offers QR 'donate' codes.
- **Source URL:** [S05](https://insiderzim.com/reserve-bank-of-zimbabwe-designates-zimswitch-national-payment-switch/amp/); [S02](https://archive.gazettes.africa/archive/zw/2020/zw-government-gazette-dated-2020-03-27-no-26.pdf)
- **Source publication / effective date:** S05: 9 Jul 2020; S02: 27 Mar 2020
- **Verification date:** 2026-10-08
- **Confidence:** MEDIUM (switch); LOW (QR — unverified)
- **Responsible operational role:** Payments lead (Eng/FINANCE)
- **Proposed technical control:** QR codes only provider-issued; no FundZim-generated payment QR until the QR guideline is verified.
- **Evidence required:** Text of NPSD Circular 03/2026 and QR guideline; provider QR documentation.
- **Open questions:** LR-040; research checklist RC-06

### REQ-013

**No crowdfunding/aggregator-specific RBZ instrument found** · Area: Payments (RBZ) · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** No RBZ instrument defining payment aggregator, facilitator, sub-merchant or crowdfunding was found; a 2022 report mentions RBZ 'crowdfunding licences' without naming the framework.
- **Applicability assessment (interpretation, not a legal conclusion):** Absence of evidence is not evidence of absence. FundZim must not treat silence as permission.
- **Source URL:** [S06](https://businesstimes.co.zw/rbz-issues-crowdfunding-licences-to-local-firms/)
- **Source publication / effective date:** S06: 16 Jun 2022
- **Verification date:** 2026-10-08
- **Confidence:** LOW
- **Responsible operational role:** Compliance officer (COMPLIANCE) with Counsel
- **Proposed technical control:** Pre-launch gate: written counsel view on whether RBZ comfort/no-objection or sandbox admission should be sought (regulatory-readiness-checklist).
- **Evidence required:** Counsel memo; any RBZ correspondence.
- **Open questions:** LR-041; research checklist RC-04

### REQ-014

**ZiG is legal tender alongside other prescribed currencies** · Area: Currency · Classification: `CONFIRMED`

- **Description (source says):** RBZ Act s44D (inserted by S.I. 60/2024): ZiG notes and coins are 'legal tender in all transactions, alongside any other currency acceptable as legal tender as prescribed under section 44A'. ZWL balances converted to ZiG.
- **Applicability assessment (interpretation, not a legal conclusion):** FundZim must be able to support ZWG and USD flows as separate currencies.
- **Source URL:** [S07](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-060%20Presidential%20Powers%20(Temporary%20Measures)%20(Zimbabwe%20Gold%20Notes%20and%20Coins)%20Regulations,%202024.pdf); [S08](https://www.zimlive.com/zig-remains-legal-tender-says-rbz-amid-legitimacy-storm/)
- **Source publication / effective date:** S07: 5 Apr 2024; S08: 29 Oct 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (S.I. text); MEDIUM (legal basis after lapse dispute)
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** Currency registry with USD and ZWG; per-currency ledger accounts; no implicit conversion (MONEY.md, ADR-010, ADR-018).
- **Evidence required:** Counsel confirmation of current parliamentary basis for s44D.
- **Open questions:** LR-006, LR-042

### REQ-015

**Multi-currency legal tender until 31 December 2030** · Area: Currency · Classification: `CONFIRMED`

- **Description (source says):** Per RBZ FXD 2/2025: multi-currency use legalised by S.I. 85/2020; per S.I. 218/2023 'the United States dollar, other denominated currencies and local currency remain legal tender till 31 December 2030'. In Feb 2026 the RBZ Governor said 2030 'will no longer be the deadline' (no amending instrument cited).
- **Applicability assessment (interpretation, not a legal conclusion):** USD donations and payouts appear lawful now; position after 2030 uncertain.
- **Source URL:** [S09](https://zimembassydc.org/wp-content/uploads/2025/08/GUIDELINES-TO-AUTHORISED-DEALERS-AND-THEIR-CLIENTS-ON-FOREIGN-EXCHANGE-TRANSACTIONS-25-Apr-25-.pdf); [S10](https://www.newzimbabwe.com/reserve-bank-of-zimbabwe-abandons-2030-mono-currency-deadline/)
- **Source publication / effective date:** S09: Apr 2025 (FXD 2/2025); S10: 28 Feb 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (RBZ document says this); MEDIUM (underlying SIs not read; 2026 policy shift secondary)
- **Responsible operational role:** Finance lead (FINANCE); Compliance officer (COMPLIANCE)
- **Proposed technical control:** Currencies are configuration (enable/disable per currency and per rail); no assumption that USD is permanent.
- **Evidence required:** Texts of S.I. 85/2020 and S.I. 218/2023; any 2026 amending instrument.
- **Open questions:** LR-042; research checklist RC-07

### REQ-016

**Only Authorised Dealers / ADLAs may buy and sell foreign currency** · Area: Exchange control · Classification: `CONFIRMED`

- **Description (source says):** 'Foreign currency dealing (to buy and sell foreign currency…), is only limited to Authorised Dealers and Authorised Dealers with Limited Authority (ADLAs)' (para 1.1.2.2).
- **Applicability assessment (interpretation, not a legal conclusion):** Applies on its face: FundZim is neither, so FundZim must never convert currencies.
- **Source URL:** [S09](https://zimembassydc.org/wp-content/uploads/2025/08/GUIDELINES-TO-AUTHORISED-DEALERS-AND-THEIR-CLIENTS-ON-FOREIGN-EXCHANGE-TRANSACTIONS-25-Apr-25-.pdf)
- **Source publication / effective date:** S09: Apr 2025 (FXD 2/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** No FX in MVP (ADR-018). Any conversion is performed and evidenced by an AD/ADLA and recorded as an external fact with rate source (currency-and-fx-policy).
- **Evidence required:** Design review; provider confirmation of who performs any conversion.
- **Open questions:** LR-044

### REQ-017

**FCAs may receive donations and diaspora remittances as free funds; NGO FCAs** · Area: Exchange control · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Individual FCAs may be funded from 'donations' and 'diaspora remittances' (free funds); NGO FCAs openable without prior RBZ reference and usable without restriction; deposits subject to KYC/CDD.
- **Applicability assessment (interpretation, not a legal conclusion):** Beneficiary-side: supports paying USD donations to beneficiaries' USD accounts/wallets without conversion.
- **Source URL:** [S09](https://zimembassydc.org/wp-content/uploads/2025/08/GUIDELINES-TO-AUTHORISED-DEALERS-AND-THEIR-CLIENTS-ON-FOREIGN-EXCHANGE-TRANSACTIONS-25-Apr-25-.pdf)
- **Source publication / effective date:** S09: Apr 2025 (FXD 2/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** Payout destination records currency capability; USD payouts only to USD-capable destinations.
- **Evidence required:** Counsel confirmation that campaign proceeds paid via a PSP are 'donations'/'free funds'.
- **Open questions:** LR-005, LR-044

### REQ-018

**Outbound donations and remittances restricted** · Area: Exchange control · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** 'All corporate monetary donations, gifts and other miscellaneous payments require prior Reserve Bank approval' (para 3.1.13.1); outward person-to-person remittances limited to US$5,000 per transaction and US$50,000 per year (para 5.4.2.1).
- **Applicability assessment (interpretation, not a legal conclusion):** Relevant to refunds to foreign donors, payouts to non-resident beneficiaries, and any cross-border recovery.
- **Source URL:** [S09](https://zimembassydc.org/wp-content/uploads/2025/08/GUIDELINES-TO-AUTHORISED-DEALERS-AND-THEIR-CLIENTS-ON-FOREIGN-EXCHANGE-TRANSACTIONS-25-Apr-25-.pdf)
- **Source publication / effective date:** S09: Apr 2025 (FXD 2/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Finance lead (FINANCE); Compliance officer (COMPLIANCE)
- **Proposed technical control:** MVP: payouts only to Zimbabwean destinations; refunds to foreign cards only via the provider's original-method refund path; no FundZim-initiated outbound transfers.
- **Evidence required:** Counsel and provider confirmation on refund characterisation.
- **Open questions:** LR-044; PCR-006 (cross-border refunds)

### REQ-019

**FCA cash withdrawal limits** · Area: Exchange control · Classification: `NOT_APPLICABLE_WITH_REASON`

- **Description (source says):** FCA cash withdrawals capped at USD 1,000/day (individuals) and USD 10,000/day (corporates).
- **Applicability assessment (interpretation, not a legal conclusion):** FundZim handles no cash and makes no cash payouts; limits bind banks and account holders.
- **Source URL:** [S09](https://zimembassydc.org/wp-content/uploads/2025/08/GUIDELINES-TO-AUTHORISED-DEALERS-AND-THEIR-CLIENTS-ON-FOREIGN-EXCHANGE-TRANSACTIONS-25-Apr-25-.pdf)
- **Source publication / effective date:** S09: Apr 2025 (FXD 2/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** None (informational for beneficiary communications).
- **Evidence required:** —
- **Open questions:** —

### REQ-020

**ZWG minor unit not defined by S.I. 60/2024** · Area: Currency · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** S.I. 60/2024 does not define a ZiG subdivision; coins were to be prescribed later.
- **Applicability assessment (interpretation, not a legal conclusion):** Engineering depends on a fixed minor-unit exponent per currency.
- **Source URL:** [S07](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-060%20Presidential%20Powers%20(Temporary%20Measures)%20(Zimbabwe%20Gold%20Notes%20and%20Coins)%20Regulations,%202024.pdf)
- **Source publication / effective date:** S07: 5 Apr 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (absence in this SI); LOW (any other source)
- **Responsible operational role:** Technical lead (Eng); Finance lead (FINANCE)
- **Proposed technical control:** Currency registry flags ZWG minor_units=2 as PROVISIONAL; startup check refuses to enable ZWG in production until the flag is cleared by a recorded decision.
- **Evidence required:** ISO 4217 maintenance agency entry for ZWG; provider API documentation.
- **Open questions:** LR-043; PCR-018 (ZiG minor units)

### REQ-021

**PVO Act s6(3) — no collection of contributions from the public except per the Act** · Area: Charitable collections (PVO) · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** 'No person shall collect contributions from the public except in terms of this Act.' Contravention: fine up to level 12 or up to 1 year (s6(5)(b)). 'Contributions' = money or property not transferred in fulfilment of a legally enforceable obligation, conferring no right to consideration.
- **Applicability assessment (interpretation, not a legal conclusion):** Potentially reaches every campaign (donations are 'contributions') and FundZim itself. Scope as to self-fundraising unresolved. P0 launch-blocking question.
- **Source URL:** [S34](https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf)
- **Source publication / effective date:** S34: Consolidated to 11 Apr 2025 (Act 1/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (validity of Act 1/2025 disputed)
- **Responsible operational role:** Compliance officer (COMPLIANCE) with Counsel; Founders
- **Proposed technical control:** Campaign `fundraising_authority` attribute mandatory at submission (SELF_FUNDRAISING [UNCONFIRMED] | REGISTERED_PVO | EXCLUDED_BODY | SECTION_8_AUTHORITY); campaign approval and payout eligibility verify it (campaign-approval-policy, payout-eligibility-and-controls).
- **Evidence required:** Counsel opinion; Registrar guidance; per-campaign authority evidence records.
- **Open questions:** LR-046, LR-047, LR-050

### REQ-022

**PVO Act s23 — offence to collect or instruct another to collect without authority** · Area: Charitable collections (PVO) · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** Offence where a person 'collects or attempts to collect or instructs another person to collect' contributions in furtherance of PVO objects, unless on behalf of and with authority of a registered PVO, for an excluded body, or 'authorised under section eight'.
- **Applicability assessment (interpretation, not a legal conclusion):** Individuals raising for others' medical bills/relief appear caught absent authority; FundZim may be exposed as a person who 'instructs another person to collect'.
- **Source URL:** [S34](https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf)
- **Source publication / effective date:** S34: Consolidated to 11 Apr 2025 (Act 1/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (validity)
- **Responsible operational role:** Compliance officer (COMPLIANCE) with Counsel
- **Proposed technical control:** As REQ-021; additionally, platform terms and campaign UX must not 'instruct' collection for unauthorised charitable objects; category rules restrict 'for others' charitable campaigns to authorised bases pending counsel.
- **Evidence required:** Counsel opinion on platform exposure.
- **Open questions:** LR-046, LR-047

### REQ-023

**PVO Act s6(2) — bodies with PVO objects collecting from the public must register** · Area: Charitable collections (PVO) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** A body with PVO objects that seeks financial assistance or 'collects contributions from the public' must register (90 days from commencement or 30 days after starting operations); applies even to Deeds-registered trusts. Exclusions in s2(1) include religious bodies for activities 'confined to religious work', approved educational trusts, member-only bodies, registered health institutions.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies to organisation campaign owners (charities, community groups, some churches/schools), not to FundZim as such.
- **Source URL:** [S34](https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf); [S41](https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-97of2026-PVO-Board-General.pdf)
- **Source publication / effective date:** S34: Consolidated to 11 Apr 2025 (Act 1/2025); S41: 5 Jun 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); MEDIUM (validity)
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** KYB requires PVO registration evidence or a documented exclusion basis for organisation owners (kyb-architecture).
- **Evidence required:** PVO registration certificate; exclusion basis documents.
- **Open questions:** LR-013, LR-050

### REQ-024

**PVO Act s8 temporary authority and S.I. 97/2026 s16 conditions** · Area: Charitable collections (PVO) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Registrar may grant 'any person or group of persons' temporary written authority to collect, up to 90 days, extendable once by up to 90 days. Conditions may include spending on stated purpose and disposal within 60 days after expiry; paying all money into a bank account disclosed to the Registrar; keeping receipts, books, donation lists open to inspection.
- **Applicability assessment (interpretation, not a legal conclusion):** If individual 'for others' campaigns require authority, the authority's term and conditions constrain campaign duration, payout destination and record-keeping.
- **Source URL:** [S34](https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf); [S41](https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-97of2026-PVO-Board-General.pdf)
- **Source publication / effective date:** S34: Consolidated to 11 Apr 2025 (Act 1/2025); S41: 5 Jun 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** SECTION_8_AUTHORITY records number and validity window; campaign end_date ≤ authority expiry; payout destination must equal the account disclosed to the Registrar where the authority so requires; donation list export for the owner.
- **Evidence required:** Copy of the authority; Registrar-disclosed bank account evidence.
- **Open questions:** LR-048

### REQ-025

**PVO s20A — donor identification, refusal of illegitimate funds, formal channels, non-partisanship** · Area: Charitable collections (PVO) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** PVOs must endeavour to ascertain donor identity and sources (anonymous donors: satisfy itself of good faith by other means); refuse and report illegitimate donations; use regulated formal channels 'at every point from source to destination'; not act in a politically partisan manner.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies to PVO campaign owners, who will need donor data from FundZim; FundZim's provider-only rails support 'formal channels'.
- **Source URL:** [S34](https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf)
- **Source publication / effective date:** S34: Consolidated to 11 Apr 2025 (Act 1/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE); Data Protection Officer
- **Proposed technical control:** Per-campaign donor-data export for PVO owners with documented CDPA basis; anonymous-donation rules configurable per owner type; political/partisan categories prohibited pending LR-025.
- **Evidence required:** Data-sharing terms with PVO owners; counsel view on anonymous donations to PVOs.
- **Open questions:** LR-049, LR-025, LR-026

### REQ-026

**S.I. 98/2026 — PVO foreign-funding disclosure, EDD and beneficiary-record thresholds** · Area: Charitable collections (PVO) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Funds above USD 5,000 from a 'High-Risk Jurisdiction' trigger high-risk monitoring and an FIU Foreign Funding Disclosure Form within 30 days; EDD/notification when a single foreign donation exceeds USD 10,000 or aggregate from one high-risk jurisdiction exceeds USD 25,000 a year; beneficiary identity records above USD 500 per transaction or USD 2,000 a year; records 5 years (transactions) / 7 years (accounting).
- **Applicability assessment (interpretation, not a legal conclusion):** Binds PVOs. FundZim must be able to provide PVO owners with donor-country, amount and identity data per campaign.
- **Source URL:** [S42](https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-98of2026-PVO-Risk-Based-Supervision.pdf)
- **Source publication / effective date:** S42: 5 Jun 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** Donor country captured from provider data where available; per-campaign aggregation reports by donor jurisdiction for PVO owners; thresholds held as REGULATORY limits in the limits model with source S.I. 98/2026 (not hard-coded).
- **Evidence required:** Report templates; limits-model entries with citation and approval owner.
- **Open questions:** LR-049, LR-055

### REQ-027

**S.I. 98/2026 — PVO STRs and TF red flags** · Area: Charitable collections (PVO) / AML · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** PVO STRs within 3 working days of forming a suspicion (s29(7)); TF red flags reportable 'within 72 hours, regardless of the monetary value' (Schedule) incl. obscured donor identity and 'sudden, unexplained spikes in funding'. The SI is internally inconsistent (3 working days vs 72 hours).
- **Applicability assessment (interpretation, not a legal conclusion):** Binds PVOs; FundZim's monitoring should not misread viral campaigns as abuse but must surface red flags to compliance.
- **Source URL:** [S42](https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-98of2026-PVO-Risk-Based-Supervision.pdf)
- **Source publication / effective date:** S42: 5 Jun 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** Transaction-monitoring rule for funding spikes with human review (transaction-monitoring); case-management SLA uses the stricter 72-hour window for TF flags pending counsel.
- **Evidence required:** Monitoring rule definitions; case SLA configuration.
- **Open questions:** LR-008, LR-055

### REQ-028

**PVO Act s26 — Ministerial freezing/return of unlawfully collected contributions** · Area: Charitable collections (PVO) · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** The Minister may order unlawfully collected contributions frozen or returned to contributors; unreturnable funds go to the Guardian's Fund.
- **Applicability assessment (interpretation, not a legal conclusion):** Could reach campaign funds held by the PSP for a campaign.
- **Source URL:** [S34](https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf)
- **Source publication / effective date:** S34: Consolidated to 11 Apr 2025 (Act 1/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE); Finance lead (FINANCE)
- **Proposed technical control:** Campaign FROZEN state and ledger hold (payable→held) executable on order; bulk refund-to-donors workflow; order recorded as evidence (incident-response-workflows: regulatory inquiry).
- **Evidence required:** Order copy; ledger and refund records.
- **Open questions:** LR-019, LR-047

### REQ-029

**Validity and commencement of the PVO Amendment Act 2025** · Area: Charitable collections (PVO) · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** Act 1 of 2025 is applied in consolidations (to 11 Apr 2025); Veritas questions whether the gazetted Act matches the Bill passed; no court ruling found; commencement date not confirmed.
- **Applicability assessment (interpretation, not a legal conclusion):** Determines whether the 2025 amendments (s6(2), s6(3) reach, s20A, s23) are in force as drafted.
- **Source URL:** [S34](https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf)
- **Source publication / effective date:** S34: Consolidated to 11 Apr 2025 (Act 1/2025)
- **Verification date:** 2026-10-08
- **Confidence:** MEDIUM
- **Responsible operational role:** Counsel
- **Proposed technical control:** Treat as in force (conservative) until counsel advises otherwise.
- **Evidence required:** Counsel memo; any court ruling.
- **Open questions:** LR-050

### REQ-030

**MLPCA s2(1) — 'financial institution' definition** · Area: AML/CFT · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** 'Any person who conducts as a business one or more of the following activities for or on behalf of a customer', incl. (d) transfer of money or value, (j) safekeeping/administration of cash on behalf of others, (k) investing, administering or managing funds on behalf of others. Minister may declare further FIs (s2(3)). Crowdfunding platforms are not listed DNFBPs (s13).
- **Applicability assessment (interpretation, not a legal conclusion):** Model A strengthens, but does not settle, the argument that FundZim is not an FI; Model B makes FI status likely.
- **Source URL:** [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf)
- **Source publication / effective date:** S21: Consolidated to Act 7/2025
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE) with Counsel
- **Proposed technical control:** Build AML controls to the accountable-institution standard regardless of status (aml-risk-framework).
- **Evidence required:** Counsel opinion; any FIU correspondence.
- **Open questions:** LR-008, LR-051

### REQ-031

**MLPCA s15–19 — CDD triggers, beneficial owners, reliance, non-face-to-face** · Area: AML/CFT · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Identify and verify on establishing a relationship, occasional transactions ≥ USD 5,000, wire transfers ≥ USD 1,000, doubt, or suspicion; verify beneficial owners; verify before the relationship; third-party reliance allowed but 'ultimate responsibility' remains; non-face-to-face measures 'no less effective'.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies if FundZim is an FI; PSPs will impose equivalent CDD in any case.
- **Source URL:** [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf)
- **Source publication / effective date:** S21: Consolidated to Act 7/2025
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** KYC levels and gates (kyc-architecture, ADR-015); thresholds as REGULATORY limits with source MLPCA s15 (limits model); remote ID verification with liveness subject to biometric rules (REQ-050).
- **Evidence required:** KYC policy approved by compliance; limits-model entries.
- **Open questions:** LR-007, LR-051, LR-052

### REQ-032

**MLPCA s20 — EDD for high-risk customers and PEPs** · Area: AML/CFT · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Risk-management systems to identify high-risk customers and PEPs (incl. 'any close associate, spouse or family member'); senior-management approval; source of wealth and funds; enhanced ongoing monitoring. EDD for FATF-called countries (s26A).
- **Applicability assessment (interpretation, not a legal conclusion):** If FI. Campaigns by/for PEPs and donors from FATF-listed countries.
- **Source URL:** [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf); [S28](https://scb.gov.bs/wp-content/uploads/2026/06/Financial-Action-Task-Force-Public-Statement-on-list-of-Jurisdictions-under-Increased-Monitoring-June-2026.pdf)
- **Source publication / effective date:** S21: Consolidated to Act 7/2025; S28: 25 Jun 2026 (FATF list of 19 Jun 2026)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** PEP screening at owner/beneficiary onboarding (sanctions-screening); EDD workflow with senior approval (compliance-case-management); high-risk jurisdiction list as configuration reviewed after each FATF plenary.
- **Evidence required:** Screening vendor contract; EDD case records.
- **Open questions:** LR-009, LR-055

### REQ-033

**MLPCA s24 / s27(8) — record keeping** · Area: AML/CFT · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** CDD and account records ≥ 5 years after relationship ends; transaction records ≥ 5 years from transaction; unusual-transaction findings and STR copies ≥ 5 years; certain cross-border wire information ≥ 10 years.
- **Applicability assessment (interpretation, not a legal conclusion):** If FI; also a sensible floor regardless.
- **Source URL:** [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf)
- **Source publication / effective date:** S21: Consolidated to Act 7/2025
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE); Data Protection Officer
- **Proposed technical control:** Retention classes KYC (≥5y after relationship end), FINANCIAL (≥10y default) as configuration under LR-012.
- **Evidence required:** Approved retention schedule.
- **Open questions:** LR-012

### REQ-034

**MLPCA s30–31 — STRs within 3 working days; tipping-off prohibited** · Area: AML/CFT · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Report suspicious property/transactions (incl. attempted) 'promptly, but not later than three working days after forming the suspicion'; no disclosure that an STR 'will be, is being or has been submitted'.
- **Applicability assessment (interpretation, not a legal conclusion):** If FI; tipping-off-safe UX is prudent regardless.
- **Source URL:** [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf)
- **Source publication / effective date:** S21: Consolidated to Act 7/2025
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** STR escalation path with SLA timers (compliance-case-management); neutral customer-facing reasons for holds/suspensions; support scripts never reference reports; DSR responses reviewed by compliance for tipping-off risk.
- **Evidence required:** Case-management configuration; support scripts.
- **Open questions:** LR-008, LR-088

### REQ-035

**MLPCA s25 — internal AML programme and compliance officer** · Area: AML/CFT · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Risk-based programme: policies, staff screening, training, technology-misuse controls, independent audit; management-level compliance officer.
- **Applicability assessment (interpretation, not a legal conclusion):** If FI; PSPs likely to require a named compliance contact contractually.
- **Source URL:** [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf)
- **Source publication / effective date:** S21: Consolidated to Act 7/2025
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Founders
- **Proposed technical control:** Named compliance officer before pilot; staff vetting (LR-089); training records (operational-controls).
- **Evidence required:** Appointment letter; training log.
- **Open questions:** LR-052, LR-089

### REQ-036

**MLPCA s6E — FIU information requests to companies and PVOs** · Area: AML/CFT · Classification: `CONFIRMED`

- **Description (source says):** The FIU may obtain information from financial institutions, DNFBPs, companies, trustees and PVOs registered or required to be registered.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies to FundZim as a company regardless of FI status (once incorporated).
- **Source URL:** [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf)
- **Source publication / effective date:** S21: Consolidated to Act 7/2025
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE); Data Protection Officer
- **Proposed technical control:** Documented, audited lawful-request handling process; permissioned export path; no ad hoc data pulls (incident-response-workflows: regulatory inquiry).
- **Evidence required:** Request log; export audit events.
- **Open questions:** LR-032

### REQ-037

**FIU Directive 01/04/2024 — CTR/EFT/IFT thresholds; non-bank monthly CTRs via goAML** · Area: AML/CFT · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Thresholds ZiG 70,000 / USD 5,000 (CTR, EFT), USD 5,000 (IFT) for banks; non-bank FIs and DNFBPs file CTRs monthly by the 10th, incl. nil returns, via goAML.
- **Applicability assessment (interpretation, not a legal conclusion):** If FI (non-bank). FundZim handles no cash; whether 'cash' CTRs could ever apply is doubtful but unconfirmed.
- **Source URL:** [S25](https://www.fiu.co.zw/wp-content/uploads/2025/05/DIRECTIVE-01-04-2024-REVISED-THRESHOLDS-FOR-CTRs-EFTs-AND-IFTs.pdf)
- **Source publication / effective date:** S25: 7 Apr 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** goAML registration readiness item; thresholds as REGULATORY limits with source.
- **Evidence required:** Counsel/FIU confirmation of filing obligation.
- **Open questions:** LR-052

### REQ-038

**MLPCA s27 — wire-transfer originator/beneficiary information** · Area: AML/CFT · Classification: `PROVIDER_CONFIRMATION_REQUIRED`

- **Description (source says):** Originator identification and information to travel with wires ≥ USD 1,000; name and reference for cross-border transfers below USD 1,000; beneficiary institution verification for cross-border ≥ USD 1,000.
- **Applicability assessment (interpretation, not a legal conclusion):** Binds the PSP/banks moving funds; FundZim must supply references and identity data the PSP needs.
- **Source URL:** [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf)
- **Source publication / effective date:** S21: Consolidated to Act 7/2025
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Payments lead (Eng/FINANCE)
- **Proposed technical control:** Payment/payout requests carry stable references (payment_id/payout_id) and required party data fields as documented by the provider.
- **Evidence required:** Provider data-field requirements.
- **Open questions:** PCR-020 (originator/beneficiary data requirements)

### REQ-039

**FIU Directive PFIU21/10/2024 — civil penalty regime** · Area: AML/CFT · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Civil-infringement regime; fixed civil penalty 'not to exceed … US$250 000' with daily penalties; directors and employees personally liable; 7 days for representations.
- **Applicability assessment (interpretation, not a legal conclusion):** Exposure if FundZim is an FI and non-compliant.
- **Source URL:** [S26](https://www.fiu.co.zw/wp-content/uploads/2025/05/AML_CFT_CPF-DIRECTIVE-NO.-PFIU21102024.pdf)
- **Source publication / effective date:** S26: 21 Oct 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (pp. 1–3 read)
- **Responsible operational role:** Founders
- **Proposed technical control:** Risk-register entry; drives the decision to build to accountable-institution standard.
- **Evidence required:** —
- **Open questions:** LR-051

### REQ-040

**S.I. 76/2014 — screening against UN and Zimbabwean lists; freeze within 24 hours** · Area: Sanctions · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** FIs and DNFBPs 'shall review the UN Consolidated List and the Zimbabwean List prior to conducting any transaction, undertaking any financial services or entering into any relationship'; on match block funds and file an STR; 'immediately' = not later than 24 hours. Dealing with designated persons' funds: fine up to USD 20,000 or twice the value.
- **Applicability assessment (interpretation, not a legal conclusion):** If FI/DNFBP; PSPs and PVO owners are bound, so FundZim screening is prudent regardless.
- **Source URL:** [S32](https://www.fiu.co.zw/wp-content/uploads/2025/05/S.I.-76-of-2014-Suppression-of-Foreign-International-Terrorism.pdf); [S42](https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-98of2026-PVO-Risk-Based-Supervision.pdf)
- **Source publication / effective date:** S32: 2014; S42: 5 Jun 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** Screening at onboarding, before payout, and on list updates; match → payout hold + campaign freeze + case within the 24-hour window (sanctions-screening).
- **Evidence required:** Screening logs; list-version records.
- **Open questions:** LR-009, LR-053

### REQ-041

**COBE Act s72–73 — company beneficial-ownership register** · Area: Beneficial ownership · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Companies keep a BO register in Zimbabwe and file with the Registrar, updating within 7 days; BO threshold 'more than twenty per centum'; Registrar information public and available to FIs/DNFBPs; records 5 years after dissolution.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies to FundZim's own operating company once incorporated in Zimbabwe; also a verification source for company campaign owners.
- **Source URL:** [S33](https://www.veritaszim.net/sites/veritas_d/files/Companies%20&%20Other%20Business%20Entities%20Act%20Cap%2024,31.pdf)
- **Source publication / effective date:** S33: Consolidated to Act 8/2020
- **Verification date:** 2026-10-08
- **Confidence:** MEDIUM-HIGH (consolidation to 2020)
- **Responsible operational role:** Founders
- **Proposed technical control:** Corporate housekeeping item (readiness checklist); KYB uses Registrar records where accessible.
- **Evidence required:** Filed BO return.
- **Open questions:** LR-054

### REQ-042

**Divergent BO thresholds across statutes** · Area: Beneficial ownership · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** COBE: > 20%; MLPCA: ≥ 25% shares/votes or control; PVO Act: 25% of governing-body votes plus 'controllers' incl. by size of contributions.
- **Applicability assessment (interpretation, not a legal conclusion):** KYB must apply the strictest test relevant to the entity type.
- **Source URL:** [S33](https://www.veritaszim.net/sites/veritas_d/files/Companies%20&%20Other%20Business%20Entities%20Act%20Cap%2024,31.pdf); [S21](https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf); [S34](https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf)
- **Source publication / effective date:** S33: Consolidated to Act 8/2020; S21: Consolidated to Act 7/2025; S34: Consolidated to 11 Apr 2025 (Act 1/2025)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (MLPCA, PVO); MEDIUM-HIGH (COBE)
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** KYB captures all owners/controllers ≥ the lowest applicable threshold per entity type (kyb-architecture); thresholds are REGULATORY limits with citation.
- **Evidence required:** KYB policy.
- **Open questions:** LR-054

### REQ-043

**FATF status and high-risk jurisdictions** · Area: AML/CFT · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Zimbabwe not on the FATF increased-monitoring list of 19 June 2026 (removed March 2022 per secondary source). PVO and MLPCA EDD rules reference FATF-listed jurisdictions.
- **Applicability assessment (interpretation, not a legal conclusion):** High-risk-jurisdiction list drives donor EDD and PVO reporting support.
- **Source URL:** [S28](https://scb.gov.bs/wp-content/uploads/2026/06/Financial-Action-Task-Force-Public-Statement-on-list-of-Jurisdictions-under-Increased-Monitoring-June-2026.pdf)
- **Source publication / effective date:** S28: 25 Jun 2026 (FATF list of 19 Jun 2026)
- **Verification date:** 2026-10-08
- **Confidence:** HIGH-MEDIUM
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** High-risk jurisdiction list as versioned configuration (source, effective date, review after each FATF plenary).
- **Evidence required:** List version history.
- **Open questions:** LR-055

### REQ-044

**Virtual-asset service providers (S.I. 99/2026)** · Area: AML/CFT · Classification: `NOT_APPLICABLE_WITH_REASON`

- **Description (source says):** VASP registration and travel-rule obligations.
- **Applicability assessment (interpretation, not a legal conclusion):** FundZim does not accept or transfer virtual assets; out of scope.
- **Source URL:** [S31](https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-99of2026MoneyLaundering.pdf)
- **Source publication / effective date:** S31: 10 Jun 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (existence)
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** Product rule: no crypto rails without ADR + counsel.
- **Evidence required:** —
- **Open questions:** —

### REQ-045

**S.I. 155/2024 — data-controller licence from POTRAZ** · Area: Data protection · Classification: `CONFIRMED`

- **Description (source says):** 'No person shall process personal information' for listed purposes 'unless they are licensed'; any person determining purposes and means must apply (Form DP1); 12-month validity, renewal ≥ 3 months before expiry; tiers by data-subject count (50–1,000; 1,001–100,000; 100,001–500,000; > 500,000) with fees USD 50/300/500/2,500; offences up to level 11 or 7 years.
- **Applicability assessment (interpretation, not a legal conclusion):** FundZim will determine purposes and means of processing donor, owner, beneficiary and KYC data. P0 pre-launch item.
- **Source URL:** [S36](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf)
- **Source publication / effective date:** S36: 13 Sep 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer; Founders
- **Proposed technical control:** Pre-processing gate: no real personal data processed (pilot or otherwise) until licence held; data-subject counter feeds tier monitoring.
- **Evidence required:** POTRAZ licence; renewal calendar.
- **Open questions:** LR-010, LR-057

### REQ-046

**DPO appointment and notification** · Area: Data protection · Classification: `CONFIRMED`

- **Description (source says):** CDPA s20(5) and S.I. 155/2024 s12–14: controller 'shall appoint a data protection officer and notify the Authority in writing' (Form DP2); changes within 14 days; Authority-approved certification course; duties incl. DPIA advice and contact point.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies on its face.
- **Source URL:** [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf); [S36](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf)
- **Source publication / effective date:** S35: 3 Dec 2021; republished 11 Mar 2022; S36: 13 Sep 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Founders
- **Proposed technical control:** DPO named before processing; DPO sign-off required on DPIAs (data-protection-assessment).
- **Evidence required:** Form DP2 notification; certification record.
- **Open questions:** LR-010

### REQ-047

**Sensitive data, health, genetic and biometric data — written consent** · Area: Data protection · Classification: `CONFIRMED`

- **Description (source says):** CDPA s11: no processing of sensitive data (incl. health information) 'unless the data subject has given consent in writing' (withdrawable); s12: genetic, biometric and health data processing 'prohibited unless the data subject has given consent in writing'; health data under a health-care professional's responsibility unless written consent.
- **Applicability assessment (interpretation, not a legal conclusion):** Medical campaigns, beneficiary health details, and any biometric KYC.
- **Source URL:** [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf)
- **Source publication / effective date:** S35: 3 Dec 2021; republished 11 Mar 2022
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer
- **Proposed technical control:** Consent records bound to the correct data subject (beneficiary or lawful representative), not the organiser; medical detail hidden by default; KYC liveness only with explicit written consent (data-protection-assessment §6).
- **Evidence required:** Consent artefacts; counsel view on electronic 'written' consent.
- **Open questions:** LR-014, LR-058, LR-027

### REQ-048

**Children's data — verified parental/guardian consent** · Area: Data protection · Classification: `CONFIRMED`

- **Description (source says):** Child = under 18 (CDPA s3; Children's Act as amended 2023). S.I. 155 s10(5): no processing 'without the consent of the parent or legal guardian', reasonable efforts to verify, DPIAs, privacy by design, no automated decisions affecting children. POTRAZ guideline (no force of law): verify guardianship documents, upfront age verification, prior POTRAZ authorisation for transfers of minors' data to inadequate jurisdictions.
- **Applicability assessment (interpretation, not a legal conclusion):** Campaigns for minors (school fees, sick children).
- **Source URL:** [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf); [S36](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf); [S38](https://www.veritaszim.net/sites/veritas_d/files/02%20Processing%20of%20Children%27s%20Personal%20Information.pdf); [S43](https://commons.laws.africa/akn/zw/act/2023/8/media/publication/zw-act-2023-8-publication-document.pdf)
- **Source publication / effective date:** S35: 3 Dec 2021; republished 11 Mar 2022; S36: 13 Sep 2024; S38: Undated; S43: 2023
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (Act/SI); HIGH-MEDIUM (guideline)
- **Responsible operational role:** Data Protection Officer
- **Proposed technical control:** Beneficiary type MINOR requires verified guardian owner, guardianship evidence and written consent; minimal identifying content; no automated decisions on minors' data (beneficiary-verification).
- **Evidence required:** Guardianship documents; consent; DPIA.
- **Open questions:** LR-015, LR-021, LR-058

### REQ-049

**Notification of biometric/genetic processing** · Area: Data protection · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** S.I. 155 s10(2)(d): controller notifies POTRAZ of 'any processing which involves biometric and genetic data'.
- **Applicability assessment (interpretation, not a legal conclusion):** If KYC uses liveness/face-match.
- **Source URL:** [S36](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf)
- **Source publication / effective date:** S36: 13 Sep 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer
- **Proposed technical control:** KYC vendor selection records whether biometrics are processed; notification is a pre-go-live gate for that feature.
- **Evidence required:** POTRAZ notification.
- **Open questions:** LR-058

### REQ-050

**Cross-border transfer restrictions** · Area: Data protection · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** CDPA s28: no transfer to a foreign third party unless adequate protection is ensured and transfer is for the controller's tasks; s29 exceptions incl. unambiguous consent and contract necessity; S.I. 155 s10(2)(c): notify POTRAZ of intended transfers outside Zimbabwe.
- **Applicability assessment (interpretation, not a legal conclusion):** Any offshore hosting, KYC vendor, email/SMS provider, error tracking, or foreign PSP.
- **Source URL:** [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf); [S36](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf)
- **Source publication / effective date:** S35: 3 Dec 2021; republished 11 Mar 2022; S36: 13 Sep 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer
- **Proposed technical control:** Vendor register with destination country, adequacy assessment and s29 basis; hosting region decision blocked on LR-011.
- **Evidence required:** Adequacy assessments; POTRAZ notifications.
- **Open questions:** LR-011, LR-057

### REQ-051

**Breach notification — POTRAZ within 24 hours; data subjects within 72 hours (high risk)** · Area: Data protection · Classification: `CONFIRMED`

- **Description (source says):** CDPA s19 and S.I. 155 s17: notify POTRAZ within 24 hours of becoming aware (Form DP3); inform affected data subjects within 72 hours where high risk; breach register; respond to POTRAZ within 14 days; investigation report within 21 days.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies on its face.
- **Source URL:** [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf); [S36](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf)
- **Source publication / effective date:** S35: 3 Dec 2021; republished 11 Mar 2022; S36: 13 Sep 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer; Security administrator (SECURITY_ADMIN)
- **Proposed technical control:** Incident playbook with 24-hour regulator clock (incident-response-workflows: data breach); breach register.
- **Evidence required:** Playbook; drill records; breach register.
- **Open questions:** LR-010

### REQ-052

**Written contracts with data processors** · Area: Data protection · Classification: `CONFIRMED`

- **Description (source says):** CDPA s18(5): a written contract with each data processor.
- **Applicability assessment (interpretation, not a legal conclusion):** KYC vendor, hosting, email/SMS, screening vendor, possibly PSP.
- **Source URL:** [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf)
- **Source publication / effective date:** S35: 3 Dec 2021; republished 11 Mar 2022
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer
- **Proposed technical control:** Vendor onboarding gate requires executed DPA (LR-033).
- **Evidence required:** Executed DPAs.
- **Open questions:** LR-033

### REQ-053

**Lawful basis, consent and processing principles** · Area: Data protection · Classification: `CONFIRMED`

- **Description (source says):** CDPA s10: processing with consent (competent person for a child) or without consent for legal obligation, vital interests, public interest or legitimate interests; s13: lawfulness, purpose limitation, minimisation, accuracy, retention 'no longer than is necessary'.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies on its face.
- **Source URL:** [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf)
- **Source publication / effective date:** S35: 3 Dec 2021; republished 11 Mar 2022
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer
- **Proposed technical control:** Processing register mapping each purpose to a basis (data-protection-assessment §4).
- **Evidence required:** Processing register.
- **Open questions:** LR-035

### REQ-054

**Data subject rights and automated decisions** · Area: Data protection · Classification: `CONFIRMED`

- **Description (source says):** CDPA s14 rights (informed, access, object, correction, deletion of false or misleading data); s25 and S.I. 155 s10(3): no solely automated decisions with legal or similarly significant effects without consent or legal basis.
- **Applicability assessment (interpretation, not a legal conclusion):** Applies on its face; fraud/risk scoring that blocks payouts may engage s25.
- **Source URL:** [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf); [S36](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf)
- **Source publication / effective date:** S35: 3 Dec 2021; republished 11 Mar 2022; S36: 13 Sep 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer
- **Proposed technical control:** Human review for adverse risk decisions on payouts/campaigns; DSR workflow (PRIVACY.md §11).
- **Evidence required:** Case records showing human decision.
- **Open questions:** LR-034

### REQ-055

**S.I. 155 s16 — security measures** · Area: Data protection / Security · Classification: `CONFIRMED`

- **Description (source says):** Controllers must implement security measures; breach of the requirement is an offence (up to level 11 or 7 years).
- **Applicability assessment (interpretation, not a legal conclusion):** Applies on its face.
- **Source URL:** [S36](https://www.veritaszim.net/sites/veritas_d/files/SI%202024-155%20Cyber%20and%20Data%20Protection%20(Licensing%20of%20Data%20Controllers%20and%20Appointment%20of%20Data%20Protection%20Officers)%20Regulations,%202024.pdf)
- **Source publication / effective date:** S36: 13 Sep 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Security administrator (SECURITY_ADMIN)
- **Proposed technical control:** SECURITY.md baseline; identity-data-protection controls for C3 data.
- **Evidence required:** Security review records; pen-test reports (Stage 18).
- **Open questions:** LR-010

### REQ-056

**CPA s52 — e-commerce disclosures and review-before-order** · Area: Consumer protection · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Supplier identity and contacts, registration number, 'the full price … including … any other fees or costs', payment terms, 'the return, exchange and refund policy', 'security procedures and privacy policy', review-and-correct before order, 'sufficiently secure' payment systems. Act applies to non-profit and non-resident suppliers.
- **Applicability assessment (interpretation, not a legal conclusion):** Likely applies to platform fees and any donor tip; uncertain for donations themselves (no consideration).
- **Source URL:** [S40](https://commons.laws.africa/akn/zw/act/2019/5/media/publication/zw-act-2019-5-publication-document.pdf)
- **Source publication / effective date:** S40: 10 Dec 2019
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Founders; Product
- **Proposed technical control:** Checkout shows all fees and the net amount before confirmation, links refund and privacy policies, and allows review/correct (FRONTEND.md; donor-protection-policy).
- **Evidence required:** UX review against s52 checklist.
- **Open questions:** LR-018, LR-056

### REQ-057

**CPA s53 — 7-day cooling-off, refund within 14 days** · Area: Consumer protection · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** 'A consumer is entitled to cancel without reason and without penalty any electronic transaction … within seven days'; full refund within 14 days of cancellation.
- **Applicability assessment (interpretation, not a legal conclusion):** If donations, tips or fees are 'electronic transactions', donors could cancel within 7 days — affects payout timing.
- **Source URL:** [S40](https://commons.laws.africa/akn/zw/act/2019/5/media/publication/zw-act-2019-5-publication-document.pdf)
- **Source publication / effective date:** S40: 10 Dec 2019
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (text); applicability unresolved
- **Responsible operational role:** Compliance officer (COMPLIANCE) with Counsel
- **Proposed technical control:** Configurable payout-availability delay (internal risk limit) that can be set ≥ 7 days if counsel so advises (payout-eligibility-and-controls).
- **Evidence required:** Counsel opinion.
- **Open questions:** LR-056

### REQ-058

**CPA s54 — unsolicited electronic communications** · Area: Consumer protection · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Unsubscribe option; disclose source of personal information on request; no sending after objection; offence.
- **Applicability assessment (interpretation, not a legal conclusion):** Donor re-engagement and marketing email/SMS/WhatsApp.
- **Source URL:** [S40](https://commons.laws.africa/akn/zw/act/2019/5/media/publication/zw-act-2019-5-publication-document.pdf)
- **Source publication / effective date:** S40: 10 Dec 2019
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Data Protection Officer
- **Proposed technical control:** Consent and preference centre; unsubscribe on every marketing message (Stage 15).
- **Evidence required:** Notification templates.
- **Open questions:** LR-023

### REQ-059

**CPA complaints and redress** · Area: Consumer protection · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Consumer Protection Commission receives complaints (s4); consumers may be heard before the Commission or a court; no retaliation (s33); conciliation/arbitration (ss55–57).
- **Applicability assessment (interpretation, not a legal conclusion):** If FundZim supplies services to consumers (fees).
- **Source URL:** [S40](https://commons.laws.africa/akn/zw/act/2019/5/media/publication/zw-act-2019-5-publication-document.pdf)
- **Source publication / effective date:** S40: 10 Dec 2019
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (Act); Commission operational status unverified
- **Responsible operational role:** Support lead (SUPPORT)
- **Proposed technical control:** Complaints register with timestamps, outcomes and escalation (donor-protection-policy).
- **Evidence required:** Complaints log.
- **Open questions:** LR-086

### REQ-060

**IMTT — 1.5% on ZiG from 1 Jan 2026; 2% on USD (secondary)** · Area: Tax · Classification: `PROVIDER_CONFIRMATION_REQUIRED`

- **Description (source says):** Finance Act 2025 s8: IMTT 1.5% on every unit of local currency transacted per transaction from 1 Jan 2026; flat local-currency equivalent of US$10,150 for single transactions ≥ US$500,000. USD rate reportedly remains 2%. Financial institutions incl. mobile money operators withhold; no donation/charity exemption found in the listed exemptions.
- **Applicability assessment (interpretation, not a legal conclusion):** May apply on both the donation and payout legs, withheld by the PSP/bank/MNO; affects net amounts and disclosures.
- **Source URL:** [S12](https://www.veritaszim.net/sites/veritas_d/files/Finance%20Act%2C%20Act%20No.%207%20of%202025.pdf); [S13](https://allafrica.com/stories/202511280037.html); [S15](https://fingaz.co.zw/2026/05/06/unpacking-intermediated-money-transfer-tax/)
- **Source publication / effective date:** S12: Act 7 of 2025; provisions effective 1 Jan 2026; S13: 27 Nov 2025; S15: 6 May 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (ZiG rate); MEDIUM (USD rate, scope, exemptions)
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** Ledger records provider-reported taxes as separate lines (never netted silently); donor and owner disclosures of possible IMTT on each leg.
- **Evidence required:** Provider statements showing IMTT treatment; tax adviser view.
- **Open questions:** LR-016, LR-059; PCR-024 (IMTT treatment on collection and payout)

### REQ-061

**VAT 15.5% from 1 Jan 2026; compulsory registration above US$25,000** · Area: Tax · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Finance Act 2025 s34 and ZIMRA PN 07/2026: VAT rate 15.5% from 1 Jan 2026; ZIMRA: registration compulsory where taxable supplies exceed 'US$25,000.00 or ZiG equivalent' in 12 months.
- **Applicability assessment (interpretation, not a legal conclusion):** FundZim's platform fees are prima facie taxable supplies; whether gross donations count depends on agency vs principal structuring.
- **Source URL:** [S12](https://www.veritaszim.net/sites/veritas_d/files/Finance%20Act%2C%20Act%20No.%207%20of%202025.pdf); [S16](https://www.zimra.co.zw/public-notices?download=4441%3Apublic-notice-07-of-2026-change-of-vat-rate-on-submission-of-return-category-a&start=20); [S17](https://www.zimra.co.zw/domestic-taxes/vat/vat-registration)
- **Source publication / effective date:** S12: Act 7 of 2025; provisions effective 1 Jan 2026; S16: Feb 2026; S17: Threshold effective 1 Jan 2024
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** Fee engine computes VAT as a configured rate with effective dates (no constant); fee revenue tracked per 12-month window for threshold monitoring.
- **Evidence required:** Tax adviser opinion; VAT registration.
- **Open questions:** LR-016, LR-045

### REQ-062

**Fiscal tax invoices (TIN, FDMS code)** · Area: Tax · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Finance Act 2025 s45 adds a TIN and an FDMS QR/authentication code to tax-invoice requirements.
- **Applicability assessment (interpretation, not a legal conclusion):** If FundZim is VAT-registered and issues fee invoices.
- **Source URL:** [S12](https://www.veritaszim.net/sites/veritas_d/files/Finance%20Act%2C%20Act%20No.%207%20of%202025.pdf)
- **Source publication / effective date:** S12: Act 7 of 2025; provisions effective 1 Jan 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** Invoice generation via fiscalisation integration (Stage 12); FundZim never issues donation 'tax receipts' (LR-017).
- **Evidence required:** Fiscal device/FDMS registration.
- **Open questions:** LR-045

### REQ-063

**Digital services withholding tax on non-resident e-commerce operators** · Area: Tax · Classification: `POTENTIALLY_APPLICABLE`

- **Description (source says):** Finance Act 2025 s44 (VAT Act s13A replaced from 1 Jan 2026): intermediaries withhold on amounts remitted outside Zimbabwe to non-resident electronic-commerce operators; rate 15.5% per KPMG citing ZIMRA PN 05/2026 (secondary).
- **Applicability assessment (interpretation, not a legal conclusion):** Applies if FundZim's contracting entity is offshore; also to FundZim's payments to offshore vendors.
- **Source URL:** [S12](https://www.veritaszim.net/sites/veritas_d/files/Finance%20Act%2C%20Act%20No.%207%20of%202025.pdf); [S18](https://kpmg.com/us/en/taxnewsflash/news/2026/01/tnf-zimbabwe-digital-services-withholding-in-lieu-of-nonresident-vat-collection.html)
- **Source publication / effective date:** S12: Act 7 of 2025; provisions effective 1 Jan 2026; S18: 22 Jan 2026
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (mechanism); MEDIUM (rate)
- **Responsible operational role:** Finance lead (FINANCE); Founders
- **Proposed technical control:** Entity-structure decision recorded before contracting (PD-36).
- **Evidence required:** Tax adviser opinion.
- **Open questions:** LR-045

### REQ-064

**Income-tax treatment of donations received** · Area: Tax · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** No authoritative source found for gifts to individuals; registered PVOs may obtain exemption only with ZIMRA approval (secondary).
- **Applicability assessment (interpretation, not a legal conclusion):** Beneficiary-side; affects what FundZim may say in disclosures, not its own obligations.
- **Source URL:** [S19](https://fingaz.co.zw/2026/07/25/taxation-of-private-voluntary-organisations-in-zimbabwe/)
- **Source publication / effective date:** S19: 25 Jul 2026
- **Verification date:** 2026-10-08
- **Confidence:** LOW–MEDIUM
- **Responsible operational role:** Finance lead (FINANCE)
- **Proposed technical control:** Copy rule: FundZim makes no statement about the tax treatment of donations received or given.
- **Evidence required:** Tax adviser opinion.
- **Open questions:** LR-017, LR-059

### REQ-065

**S.I. 80/2020 s4(9) — PSP internal controls incl. data protection and cyber security** · Area: Cybersecurity · Classification: `PROVIDER_CONFIRMATION_REQUIRED`

- **Description (source says):** Providers must maintain 'policies and procedures for sound internal controls and risk management practices including data protection and cyber security'; RBZ cyber framework (2021) and 2025 resilience guideline reported but UNVERIFIED.
- **Applicability assessment (interpretation, not a legal conclusion):** Binds the PSP; expect contractual pass-through (API security, incident notification, vendor DD).
- **Source URL:** [S02](https://archive.gazettes.africa/archive/zw/2020/zw-government-gazette-dated-2020-03-27-no-26.pdf)
- **Source publication / effective date:** S02: 27 Mar 2020
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (S.I.); LOW (RBZ cyber documents)
- **Responsible operational role:** Security administrator (SECURITY_ADMIN)
- **Proposed technical control:** SECURITY.md baseline; contract-ready security questionnaire answers (provider-due-diligence-checklist).
- **Evidence required:** PSP security requirements; RBZ guideline texts.
- **Open questions:** LR-040

### REQ-066

**Child = under 18; no capacity to contract** · Area: Children / capacity · Classification: `CONFIRMED`

- **Description (source says):** Children's Act as amended 2023 and CDPA s3: child is a person under 18; POTRAZ guideline: children 'do not have the capacity to contract'.
- **Applicability assessment (interpretation, not a legal conclusion):** Account holders, campaign owners and payout recipients must be adults.
- **Source URL:** [S43](https://commons.laws.africa/akn/zw/act/2023/8/media/publication/zw-act-2023-8-publication-document.pdf); [S35](https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf); [S38](https://www.veritaszim.net/sites/veritas_d/files/02%20Processing%20of%20Children%27s%20Personal%20Information.pdf)
- **Source publication / effective date:** S43: 2023; S35: 3 Dec 2021; republished 11 Mar 2022; S38: Undated
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (definitions); MEDIUM (capacity statement via guideline)
- **Responsible operational role:** Compliance officer (COMPLIANCE)
- **Proposed technical control:** DOB captured and verified at IDENTITY_VERIFIED; under-18 cannot reach BASIC_VERIFIED as owner or receive payouts; donor age attestation (LR-021).
- **Evidence required:** KYC DOB verification records.
- **Open questions:** LR-021

### REQ-067

**No electronic transactions statute in force** · Area: Electronic transactions · Classification: `LEGAL_REVIEW_REQUIRED`

- **Description (source says):** The Electronic Transactions and Electronic Commerce Bill had not been presented to Parliament as of 28 Oct 2025; CPA ss52–54 and the CDPA govern online transactions.
- **Applicability assessment (interpretation, not a legal conclusion):** Validity of click-wrap terms and electronic 'written consent' unconfirmed.
- **Source URL:** [S44](https://www.veritaszim.net/node/7687)
- **Source publication / effective date:** S44: 28 Oct 2025
- **Verification date:** 2026-10-08
- **Confidence:** HIGH (as at 28 Oct 2025)
- **Responsible operational role:** Compliance officer (COMPLIANCE) with Counsel
- **Proposed technical control:** Strong acceptance evidence: versioned terms, timestamp, actor, method, IP/device where lawful (audit-evidence-model).
- **Evidence required:** Counsel view.
- **Open questions:** LR-027

### REQ-068

**Securities / investment crowdfunding regulation** · Area: Scope · Classification: `NOT_APPLICABLE_WITH_REASON`

- **Description (source says):** Investment crowdfunding platforms engage securities regulation (a 2026 SECZ sandbox admission concerned investment crowdfunding — not verified, not relied on).
- **Applicability assessment (interpretation, not a legal conclusion):** FundZim is donation-only: no equity, securities, returns or lending (PRODUCT.md §2).
- **Source URL:** None (scope statement)
- **Source publication / effective date:** —
- **Verification date:** 2026-10-08
- **Confidence:** —
- **Responsible operational role:** Founders
- **Proposed technical control:** Product scope rule; any reward/equity/lending feature requires ADR + counsel.
- **Evidence required:** —
- **Open questions:** LR-041 (confirm)

## 5. How rows change

- **Adding rows.** New rows are appended with the next `REQ` number. IDs are never reused.
- **Changing a classification.** This needs a dated note, the evidence (counsel memo reference, provider
  letter, or newly read source) and the reviewer. A move to `CONFIRMED` or `NOT_APPLICABLE_WITH_REASON` on
  the strength of counsel's view records the memo reference.
- **When an `LR` resolves.** Every row that links to it is re-assessed in the same change.
- **Code references.** Code that implements a control cites the `REQ` ID near the policy configuration
  (for example, `// policy: REQ-024, LR-048`).
- **Re-verification.** Rows with confidence below HIGH, or with an "UNVERIFIED" note, are re-checked before
  the Stage 20 pilot certification.

## 6. Research checklist (UNVERIFIED items: all `LEGAL_REVIEW_REQUIRED`)

These items came up in research but could not be verified from an opened source. They are **not** used as
facts anywhere in FundZim's documentation. Each needs to be verified (or ruled out) by counsel or by
obtaining the primary text.

| ID | Item to verify | Related | Suggested source |
|---|---|---|---|
| RC-01 | Current consolidated NPS Act; any amendment or new NPS Bill since 2016 | REQ-001, REQ-002; LR-036 | zimlii.org; parlzim.gov.zw; RBZ NPS Department |
| RC-02 | Gazetted text of S.I. 17/2025 and any later amendment of S.I. 80/2020 | REQ-003–REQ-006 | Government Gazette 28 Feb 2025; veritaszim.net |
| RC-03 | Whether the 2017 Retail Payment Guidelines were superseded; RBZ recognition and oversight framework | REQ-007–REQ-010 | RBZ NPS Department |
| RC-04 | Any RBZ rule on aggregators, facilitators, sub-merchants or crowdfunding; the framework behind the 2022 "crowdfunding licences"; any s18(4) exemption notices | REQ-013; LR-041, LR-037 | RBZ; Gazette; counsel |
| RC-05 | Current penalty value for NPS Act s18 (fine expressed in pre-2009 dollars); dollar value of standard-scale fine "levels" 5–14 | REQ-001, REQ-021 | Criminal Law Code First Schedule as amended |
| RC-06 | NPSD Circular 03/2026 and the national QR Code Payment Guideline | REQ-012; LR-040 | rbz.co.zw circulars |
| RC-07 | Texts of S.I. 85/2020 and S.I. 218/2023; current Exchange Control Act s11(2a); any 2026 change to the 31 Dec 2030 date | REQ-015; LR-042 | veritaszim.net; Gazette |
| RC-08 | Which Act gave parliamentary force to RBZ Act s44D (ZiG) | REQ-014; LR-042 | Finance (No. 2) Act 2024 text |
| RC-09 | ZWG minor unit (ISO 4217 exponent); S.I. 75/2024 (coins) | REQ-020; LR-043 | ISO 4217 maintenance agency; Veritas |
| RC-10 | Whether cross-border donations need a licensed remittance operator rather than card-acquirer settlement; current export surrender rate | REQ-011; LR-044 | RBZ Exchange Control; counsel |
| RC-11 | Full IMTT exemption list (Thirtieth Schedule); USD IMTT rate text; 2026 mid-term budget changes | REQ-060; LR-059 | Consolidated Finance Act; ZIMRA |
| RC-12 | ZIMRA Public Notice 05/2026 (DSWT rate and procedure) | REQ-063; LR-045 | zimra.co.zw |
| RC-13 | Income-tax treatment of gifts to individuals, PVOs and trusts; tax and accounting record-retention periods | REQ-064; LR-059, LR-012 | Income Tax Act; ZIMRA |
| RC-14 | RBZ Cybersecurity and Resilience Guideline (2025); Circular NPS/02/2021 | REQ-065; LR-040 | rbz.co.zw |
| RC-15 | FATF primary source for the March 2022 delisting; status after the October 2026 plenary | REQ-043; LR-055 | fatf-gafi.org |
| RC-16 | Any s2(3) MLPCA declaration or s101 DNFBP designation covering crowdfunding or payment facilitators | REQ-030; LR-051 | Gazette SI index; FIU |
| RC-17 | Content of S.I. 110/2021 (TF), S.I. 164/2023 (PF), S.I. 56/2019; existence and location of a domestic "Zimbabwean List" | REQ-040; LR-053 | FIU legal-framework page |
| RC-18 | FIU high-risk jurisdictions directive (17 Feb 2026); FIU civil-penalty infringement table (pages after 3) | REQ-043, REQ-039 | fiu.co.zw |
| RC-19 | PVO Registrar's treatment of individuals raising for themselves or others; any guidance on online crowdfunding; commencement date of Act 1/2025 | REQ-021–REQ-024, REQ-029; LR-046–LR-050 | Registrar of PVOs; counsel |
| RC-20 | Court rulings on the validity of the PVO Amendment Act 2025 and of S.I. 155/2024 (Veritas challenges) | REQ-029, REQ-045; LR-050, LR-057 | ZimLII judgments; Veritas |
| RC-21 | POTRAZ Regulatory Notices 2026 (licensing deadline, inspections); POTRAZ consent and cross-border transfer guidelines | REQ-045–REQ-050; LR-057 | potraz.gov.zw |
| RC-22 | Consumer Protection Commission operational status, complaints channel and any CPA regulations | REQ-059; LR-086 | Ministry of Industry and Commerce |
| RC-23 | Children's Act provisions on publishing a child's identity or images in fundraising; Legal Age of Majority citation; Constitution ss57 and 81 | REQ-048, REQ-066; LR-015, LR-021 | Children's Act [Ch. 5:06]; Constitution |
| RC-24 | Any municipal or police public-collection permit regime | REQ-021; LR-046 | Harare City by-laws; ZRP |
| RC-25 | Deeds Registries Act s70A (trust registration and beneficial-ownership duties); COBE amendments after 2020 | REQ-041, REQ-042; LR-054 | veritaszim.net |
| RC-26 | Status of the Electronic Transactions and Electronic Commerce Bill after 28 Oct 2025 | REQ-067; LR-027 | parlzim.gov.zw |
| RC-27 | Whether CPA ss52–53 apply to donations, tips or platform fees (interpretive) | REQ-056, REQ-057; LR-056 | Counsel; Consumer Protection Commission |
| RC-28 | Whether RBZ FXD 2/2025 has been amended or replaced in 2025–2026 | REQ-015–REQ-018 | rbz.co.zw Exchange Control |

