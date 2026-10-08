# FundZim — Licensing Assessment

**Stage 1 deliverable.** Status: analysis for counsel. Verified on **2026-10-08**. **Not legal advice.**

This document breaks FundZim's planned business into discrete activities. For each activity it asks which
licence, registration, recognition or authority *might* be needed, and who would need it, under each
operating model:
- **Model A:** PSP-mediated.
- **Model B:** platform-controlled settlement account.
- **Model C:** direct beneficiary settlement.

The models are defined in [operating-model-decision.md](operating-model-decision.md).

**Every cell is `LEGAL_REVIEW_REQUIRED`.** The table shows *potential* exposure, judged from the sources in
[regulatory-landscape.md](regulatory-landscape.md) and the [requirements register](regulatory-requirements-register.md).
It does **not** conclude that any licence is, or is not, required. FundZim holds no licence, registration
or approval of any kind as of this date.

---

## 1. Instruments that could require a licence, registration or authority

| Code | Instrument | Holder | Source | Requirement rows |
|---|---|---|---|---|
| L-RBZ-PS | RBZ authorisation to operate a retail payment system or issue instruments (2017 Guidelines para 3.1); recognition of a payment system (NPS Act s3; S.I. 80/2020 s3) | Operator / issuer / money transmission provider | [S04](regulatory-landscape.md#24-sources-consulted), [S01](regulatory-landscape.md#24-sources-consulted), [S02](regulatory-landscape.md#24-sources-consulted) | REQ-002, REQ-003, REQ-007 |
| L-RBZ-EM | E-money issuance with trust account under RBZ-approved trustees (2017 Guidelines paras 16–18) | E-money issuer | [S04](regulatory-landscape.md#24-sources-consulted) | REQ-008 |
| L-RBZ-REM | Registration and licence as a remittance service provider (2017 Guidelines para 13.1) | Remittance provider | [S04](regulatory-landscape.md#24-sources-consulted) | REQ-011 |
| L-NPS-S18 | Status as participant, introduced person, duly appointed agent, or Ministerial exemption (NPS Act s18) | Anyone accepting money for payment to third parties as a regular business | [S01](regulatory-landscape.md#24-sources-consulted) | REQ-001 |
| L-FX | Authorised Dealer / ADLA status (FXD 2/2025 para 1.1.2.2) | Anyone buying or selling foreign currency | [S09](regulatory-landscape.md#24-sources-consulted) | REQ-016 |
| L-PVO-REG | PVO registration (PVO Act s6(2); S.I. 97/2026) | Bodies with PVO objects collecting from the public | [S34](regulatory-landscape.md#24-sources-consulted), [S41](regulatory-landscape.md#24-sources-consulted) | REQ-023 |
| L-PVO-S8 | Temporary authority to collect contributions (PVO Act s8; S.I. 97/2026 s16) | "Any person or group of persons" collecting for PVO objects without registration | [S34](regulatory-landscape.md#24-sources-consulted), [S41](regulatory-landscape.md#24-sources-consulted) | REQ-024 |
| L-DP | Data-controller licence and DPO notification (S.I. 155/2024) | Anyone determining purposes and means of processing | [S36](regulatory-landscape.md#24-sources-consulted) | REQ-045, REQ-046 |
| L-FIU | goAML registration / accountable-institution obligations (MLPCA) | Financial institutions and DNFBPs | [S21](regulatory-landscape.md#24-sources-consulted), [S25](regulatory-landscape.md#24-sources-consulted) | REQ-030–REQ-037 |
| L-VAT | VAT registration (compulsory above US$25,000 of taxable supplies in 12 months) and FDMS fiscalisation | Suppliers of taxable services | [S17](regulatory-landscape.md#24-sources-consulted), [S12](regulatory-landscape.md#24-sources-consulted) | REQ-061, REQ-062 |
| L-CO | Company incorporation; beneficial-ownership register and filings (COBE Act s72) | FundZim operating company | [S33](regulatory-landscape.md#24-sources-consulted) | REQ-041 |

## 2. Activity × model matrix

Legend for each cell:
- **Party exposed:** who would need the instrument (FZ = FundZim, PSP = payment provider, OWN = campaign
  owner, BEN = beneficiary).
- **Exposure:** Low / Medium / High. This is FundZim's *working* estimate, to be replaced by counsel's view.

| # | Activity | Model A: PSP-mediated | Model B: platform account | Model C: direct beneficiary settlement | Open questions |
|---|---|---|---|---|---|
| 1 | **Campaign hosting**: publishing stories and taking pledges of support (no money) | No payment licence apparent. L-DP for FZ (High certainty that this applies). L-PVO-S8 or L-PVO-REG may be needed by OWN depending on cause. FZ may be exposed under PVO s23 ("instructs another person to collect"). | Same | Same | LR-046, LR-047, LR-057 |
| 2 | **Payment initiation**: creating a payment intent and redirecting or pushing to the PSP checkout | PSP holds L-RBZ-PS. FZ acts as merchant/platform (Low). Outsourcing characterisation is on the PSP's side (REQ-010). | Same, but FZ is the merchant of record for all funds (Medium) | PSP or OWN/BEN sub-merchant is merchant of record. FZ is the technical platform (Low). | LR-004, LR-038; PCR |
| 3 | **Collection**: accepting donor money | PSP accepts as participant or introduced person (L-NPS-S18 sits with the PSP). FZ: Low. | FZ accepts into its own account. Exposure under L-NPS-S18, L-RBZ-PS and L-FIU (High). | PSP accepts on behalf of the beneficiary sub-merchant. FZ: Low. | LR-036, LR-037, LR-051 |
| 4 | **Holding funds** between collection and payout | PSP holds in its trust or escrow arrangement (L-RBZ-EM / S.I. 80 s4 obligations on the PSP). FZ ledger is a record only (Low, **if** no spendable balances). | FZ holds. L-RBZ-EM-like trust and safeguarding duties, MLPCA s2(1)(j)/(k), and banking-partner requirements (High). | Funds settle to the beneficiary. No pooled holding (Low), but no holdback either. | LR-001, LR-003, LR-039 |
| 5 | **Settlement**: provider to destination | PSP settles per its approved scheme. Whether settlement on a platform's instruction to many beneficiaries is within its approval is PCR. | Bank settles to the FZ account (FZ exposure as in row 3) | PSP settles directly to BEN (split or sub-merchant). Scheme approval is PCR. | LR-038 |
| 6 | **Disbursement**: payout to beneficiary | PSP disburses on FZ instruction. FZ instructs only (Medium, pending s18 and FI analysis). | FZ disburses from its own account. L-NPS-S18, L-FIU (High). | No separate disbursement; settlement is direct. | LR-036, LR-051, LR-038 |
| 7 | **FX conversion** | Not performed (ADR-018). Any conversion is by an AD/ADLA (bank or PSP). | Not performed | Not performed | LR-044 |
| 8 | **Cross-border inflows** (international donors) | PSP or acquirer must be licensed for the flow. Remittance characterisation is open (L-RBZ-REM). | FZ receives foreign funds. Remittance or exchange-control exposure (High). | As A, at sub-merchant level | LR-005, LR-044 |
| 9 | **Platform fee collection** | PSP deducts or splits at source, or remits to FZ. L-VAT for FZ once over the threshold. | FZ deducts from funds it holds (custody issue as in row 4); L-VAT | Split at source to FZ's account; L-VAT | LR-016, LR-045; PCR |
| 10 | **Refunds and chargebacks** | PSP executes; FZ instructs or approves | FZ executes from its own account (FX approval needed for outbound refunds to foreign cards) | PSP or BEN account debited; recovery depends on the BEN | LR-018, LR-020, LR-044 |
| 11 | **KYC/KYB processing** | L-DP (FZ). Biometric notification if liveness is used. Vendor DPA. Cross-border notification if the vendor is offshore. | Same, plus FZ's own AML programme as a likely FI | Same; PSP sub-merchant onboarding may duplicate KYC | LR-057, LR-058, LR-011 |
| 12 | **Data controller** (all personal data) | L-DP (FZ): applies on the face of S.I. 155/2024. Licence tier by data-subject count. | Same | Same | LR-010, LR-057 |
| 13 | **Charitable collections** | OWN needs L-PVO-REG or L-PVO-S8 (or an exclusion) for "for others" charitable campaigns. FZ exposure under s23 is open. | Same; and FZ itself "collects", so its exposure is higher | Same | LR-046–LR-050 |
| 14 | **AML reporting** | FI status of FZ is open (Medium). Build to the accountable standard anyway. | FI status likely (High) | Open (Medium–Low) | LR-051, LR-052 |
| 15 | **Corporate existence** | L-CO: FZ must be incorporated (entity location decision PD-36), with BO register | Same | Same | LR-054 |

## 3. Reading the matrix

1. **Model A minimises FundZim's payment-licensing exposure, but does not remove it.** Rows 2, 6 and 14
   remain open: whether *instructing* payouts makes FundZim a financial institution under MLPCA s2(1), and
   whether the PSP's RBZ approval covers platform-instructed disbursement.
2. **Model B concentrates exposure on FundZim** in rows 3, 4, 6, 8, 10 and 14. It would likely need an RBZ
   authorisation or recognition route (or a s18 exemption), trust and safeguarding arrangements, and full
   accountable-institution status. Under S.I. 17/2025 an applicant reportedly must partner with a local
   authorised financial institution and pay turnover-based fees (MEDIUM, REQ-006). This is why Model B is
   not selected ([operating-model-decision.md](operating-model-decision.md)).
3. **Model C shifts merchant status to beneficiaries.** It suits verified organisations (PVOs, schools,
   hospitals) whose bank accounts can be onboarded as PSP sub-merchants. It is impractical for individuals,
   and offers no platform holdback for refunds or chargebacks.
4. **Charitable-collection authority (row 13) is model-independent.** It may restrict *which campaigns can
   exist*, whoever moves the money. This is the most significant unresolved item.
5. **Data-controller licensing (row 12) is the one licence FundZim almost certainly needs itself.** It is
   confirmed on the face of S.I. 155/2024 and required before any real personal data is processed.

## 4. FundZim's licence and registration status (truthful snapshot, 2026-10-08)

| Item | Status |
|---|---|
| Operating company incorporated | Not confirmed in this repository (founders to supply) |
| POTRAZ data-controller licence | Not held |
| DPO appointed and notified | No |
| PVO registration / s8 authority (for any FundZim-run campaign) | Not applicable yet (no campaigns) |
| VAT registration | Not held (no revenue) |
| goAML / FIU registration | Not held; obligation undetermined (LR-051) |
| RBZ authorisation, recognition, comfort letter or sandbox | None held or applied for (LR-041) |
| PSP contract | None (provider selection pending; see [../payments/provider-comparison.md](../payments/provider-comparison.md)) |

## 5. Questions to put to counsel (in priority order)

1. **Charitable collections (PVO Act):**
   - Do PVO Act s6(3) and s23 apply to individuals raising for themselves, individuals raising for named
     others, and FundZim as platform?
   - Is a "fiscal sponsor" model (campaigns run under a registered PVO's authority) or a per-campaign s8
     authority workable?
   - (LR-046–LR-048)
2. **Payment-systems law under Model A:**
   - Is FundZim outside NPS Act s18, S.I. 80/2020 s3, and the 2017 Guidelines para 3.1, given that the PSP
     accepts, holds and disburses?
   - What contractual terms must the PSP give? (LR-036, LR-038, LR-039)
3. **AML status:** Is FundZim a "financial institution" under MLPCA s2(1)(d)/(j)/(k) under Model A? If so,
   what is the registration route? (LR-051, LR-052)
4. **Data-protection licensing:** What licence tier and timing apply? Is FundZim the controller for
   beneficiary and medical data uploaded by owners? (LR-057)
5. **RBZ engagement:** Should FundZim seek RBZ comfort or sandbox entry before the pilot? (LR-041)
6. **Cross-border:** Is a card-based diaspora donation a merchant payment or a remittance? (LR-044)
7. **Tax:** VAT on fees under agency vs principal treatment, entity location, and IMTT disclosure.
   (LR-045, LR-059)

## 6. Decision impact

- **Stage 2 (schema)** must model:
  - `fundraising_authority`;
  - provider-held funds as records (not balances);
  - per-currency isolation;
  - evidence records for every licence-dependent gate.
- **Stage 9 (PSP contracting)** cannot close until rows 2–6 and 8–10 have counsel views and the provider
  confirmations in [../payments/provider-questions.md](../payments/provider-questions.md).
- **Stage 20 (pilot certification)** requires:
  - the POTRAZ licence and a DPO;
  - counsel sign-off on rows 3, 4, 6, 13 and 14;
  - an executed PSP contract.
