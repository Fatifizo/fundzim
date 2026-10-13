# Stage 9 — Legal review (payment provider integration)

Date: 2026-10-13. **This is not legal advice and records no legal opinion.** It lists the open items from
[open-legal-questions.md](../compliance/open-legal-questions.md) that block or constrain provider integration, with
their exact definitions copied from that register (Stage 1 research notes omitted), their current status and the
Stage 9 engineering consequence. No item was resolved in Stage 9. Nothing here states that FundZim is licensed, that
a PSP licence authorises FundZim's business model, or that any Zimbabwean legal requirement exists beyond what the
register already records.

**Overall:** every listed item is OPEN (or a DUPLICATE of an OPEN item). Production payment execution is **BLOCKED**.
Provider sandbox work may proceed only against a provider's own sandbox with test credentials and no real funds.

## Operating model, custody and licensing (merchant of record)

Stage 9 consequence: Blocks any real collection. Under Model A the PSP is assumed to hold funds; whether FundZim or the PSP is merchant of record for a donation, and whether FundZim's instruction role needs a licence, is unresolved. No adapter may be enabled outside a provider sandbox.

| ID | Title | Exact definition (register) | Status |
|---|---|---|---|
| LR-001 | Custody of funds | **Assumption:** FundZim will not hold customer funds; funds are held and moved by licensed PSPs/banks. **Question:** Can FundZim lawfully hold funds even transiently (e.g. a collection account), and if so under what licence/trust structure? | OPEN |
| LR-003 | Stored value / e-money | **Assumption:** A per-user or per-campaign balance that cannot be spent, transferred or topped up, and is only paid out via the approved flow, is not stored value. **Question:** Confirm. | OPEN |
| LR-004 | PSP licensing & role | **Question:** Does FundZim's orchestration role (initiating payments, instructing payouts) require registration/licensing under the National Payment Systems Act or RBZ directives (e.g. as a payment facilitator, aggregator, or third-party payment processor)? Which licences must partner PSPs hold for each rail? | OPEN |
| LR-036 | NPS Act s18 applicability | Does NPS Act s18 (accepting money or payment instructions for payment to a third person 'to whom the payment is due', as a regular feature of business) apply to FundZim under Models A, B and C? Is a donation a payment 'due' to the beneficiary? | OPEN |
| LR-037 | s18 agency and exemption routes | Can FundZim rely on appointment as the 'duly appointed agent' of each beneficiary (s18(3)(c)), or on a Ministerial exemption (s18(4))? Have s18(4) exemptions ever been granted? | OPEN |
| LR-038 | PSP scheme approvals and contract terms | Does the PSP's RBZ approval cover (a) holding funds collected for a platform's campaigns, (b) disbursement to beneficiaries on a third-party platform's instruction, (c) split settlement of platform fees, and (d) any outsourcing to FundZim? What must the contract state (trust holding, instruction authority, refunds, chargebacks, settlement currency)? | OPEN |
| LR-039 | Ledger vs payment system / e-money characterisation | Could FundZim's ledger, campaign 'balances' and payout instructions be characterised as owning a payment system that 'facilitates the transmission of monies' (S.I. 80/2020) or as issuing e-money (2017 Guidelines)? | OPEN |
| LR-041 | RBZ engagement and SECZ scope | Should FundZim seek an RBZ no-objection/comfort letter or fintech sandbox admission before piloting? Under what framework were the 2022 RBZ 'crowdfunding licences' issued? Confirm SECZ regulation does not reach donation-only crowdfunding. | OPEN |
| LR-051 | MLPCA financial-institution status under Model A | Is FundZim a 'financial institution' under MLPCA s2(1)(d), (j) or (k) when a PSP holds and moves funds and FundZim instructs payouts? | OPEN |
| LR-075 | FundZim as instructing party | Under Model A, does instructing a PSP to disburse PSP-held funds make FundZim (a) a person who "accepts money… for the purpose of making a payment" (NPS Act s18), (b) a money transmission provider (S.I. 80/2020), or (c) an operator/issuer under the 2017 Guidelines? Should FundZim get agency appointments, an RBZ no-objection letter or sandbox admission? | DUPLICATE → LR-036, LR-037, LR-039, LR-041 |
| LR-076 | Pool funding by FundZim | May FundZim move its own funds into a PSP-held pool (fee true-ups, chargeback shortfall funding, `*:funding` journals) without being treated as holding, commingling or safeguarding client funds, or as issuing e-money? | OPEN |

## KYC/KYB, AML and reporting

Stage 9 consequence: Donor KYC tiers and reporting duties are unknown; Stage 9 collects no new donor identity data and keeps the Stage 8 money limits.

| ID | Title | Exact definition (register) | Status |
|---|---|---|---|
| LR-007 | AML thresholds & KYC tiers | **Question:** Required CDD per level; thresholds that trigger IDENTITY/PAYOUT verification, enhanced due diligence, and donor identification; per-transaction and cumulative limits for UNVERIFIED users. | OPEN |
| LR-008 | FIU reporting obligations | **Question:** Is FundZim an accountable/reporting institution? Obligations for suspicious transaction reports, cash/threshold reports, record keeping, compliance officer appointment, staff training, tipping-off rules. | OPEN |
| LR-052 | FIU registration, CTRs and PSP reliance | If FundZim is an FI: goAML registration, monthly (nil) CTRs for non-bank FIs, compliance officer appointment; may FundZim rely on PSP CDD under s18, and may the PSP rely on FundZim's? Do EFT/IFT reporting thresholds apply to a platform that makes no transfers itself? *(folded in from LR-073)* | OPEN |
| LR-054 | Beneficial ownership verification for KYB | Which BO test applies per organisation type (COBE >20%, MLPCA ≥25%, PVO 25% + controllers)? Can FundZim access Registrar BO records for verification? | OPEN |
| LR-061 | Who the CDD "customer" is | Who is FundZim's "customer" for CDD purposes: campaign owner, beneficiary, donor, or several of them? (Whether FundZim may rely on PSP CDD under MLPCA s18 is asked in LR-052.) | OPEN (narrower than LR-052; see LR-051) |

## Sanctions and PEP screening

Stage 9 consequence: No screening provider (KI-S5-04). Production stays BLOCKED.

| ID | Title | Exact definition (register) | Status |
|---|---|---|---|
| LR-009 | Sanctions & PEP screening | **Question:** Which sanctions lists apply (domestic, UN, and PSP/bank-imposed such as OFAC/UK/EU)? Who must be screened (owners, beneficiaries, directors, donors) and when? PEP definition and EDD. | OPEN |
| LR-053 | Sanctions lists and screening scope | Does a published domestic 'Zimbabwean List' exist and where? Must FundZim (if not an FI) screen under S.I. 76/2014? Which additional lists (OFAC/UK/EU) do PSPs require? Is post-payment screening of identified donors acceptable? *(folded in from LR-065)* | OPEN |
| LR-064 | Applying the PEP definition | How should FundZim apply the PEP definition (family members and close associates) and the "senior management approval" requirement (MLPCA s20) before it has a formal governance structure? Is a campaign benefiting a PEP's relative in scope? | OPEN (operational detail of LR-009) |

## Cross-border flows, currency and exchange control

Stage 9 consequence: International cards and cross-border rails stay UNSUPPORTED. ZWG stays refused (minor units unverified). No FX.

| ID | Title | Exact definition (register) | Status |
|---|---|---|---|
| LR-005 | Exchange control — international donations | **Question:** May foreign/diaspora donors pay in USD (cards, international transfers) for Zimbabwean beneficiaries? What documentation, limits, surrender or reporting rules apply to receipt and payout? | OPEN |
| LR-006 | ZiG (ZWG) handling & settlement | **Question:** Rules for accepting, settling and paying out in ZiG vs USD; whether a campaign may receive both; whether payouts must be in the currency received; minor-unit usage by PSPs. | OPEN |
| LR-042 | ZiG legal basis and post-2030 currency regime | Which Act gives parliamentary force to RBZ Act s44D after the S.I. 60/2024 lapse dispute? Does the statutory 31 Dec 2030 multi-currency date still stand after the Feb 2026 policy statement? | OPEN |
| LR-043 | ZWG minor unit | What minor-unit exponent applies to ZWG for accounting and for each provider's API (ISO 4217 entry; any RBZ prescription of coins/cents)? | OPEN |
| LR-044 | Cross-border donations and outbound flows | Is a foreign donor's card or transfer donation a merchant payment or a remittance requiring a licensed remittance operator? What approvals apply to refunds to foreign donors and any outbound recovery (FXD 2/2025 paras 3.1.13, 5.4)? | OPEN |

## Disclosures, consumer protection, refunds and chargebacks

Stage 9 consequence: Refunds remain requests only (no sandbox refund execution, see refunds section of implementation.md). Public checkout stays disabled.

| ID | Title | Exact definition (register) | Status |
|---|---|---|---|
| LR-018 | Consumer protection & refunds | **Question:** Donor refund rights (cooling-off, misrepresentation, campaign fraud); disclosure obligations; complaint handling requirements. | OPEN |
| LR-019 | Cancelled / frozen campaign funds | **Question:** What happens to funds of cancelled, rejected-after-donation, or fraud-confirmed campaigns: refund to donors, payout to a verified beneficiary, transfer to charity, or hold? Unclaimed funds rules. | OPEN |
| LR-020 | Chargebacks & dispute liability | **Question:** Who bears chargeback loss after payout (FundZim, PSP, campaign owner)? Reserve/rolling-hold requirements imposed by PSPs/schemes; recovery rights against campaign owners. | OPEN |
| LR-022 | Terms of service & platform liability | **Question:** Required terms (user agreement, campaign rules, prohibited causes, privacy notice, cookie notice), platform liability for campaign content and misuse, governing law. | OPEN |
| LR-026 | Donor identity disclosure | **Question:** Whether "anonymous" donations are permissible above thresholds; obligations to disclose donor identity to campaign owners, authorities, or recipients of political/organisation funds. | OPEN |
| LR-056 | Consumer Protection Act scope | Do CPA s52 (disclosures) and s53 (7-day cooling-off, 14-day refund) apply to donations, optional tips, and platform fees? Is the Consumer Protection Commission operational? | OPEN |
| LR-080 | Beneficiary recovery (clawback) | Can FundZim contractually require campaign owners/beneficiaries to repay amounts refunded or charged back after payout, and how enforceable is that against individuals, organisations and third-party beneficiaries? What process is required before any recovery action? *(folded in from LR-085)* | OPEN |
| LR-081 | Refunds to another instrument | May a refund be paid to an instrument other than the one that paid (e.g. a manual refund to a different wallet or bank account), and what verification is required? | OPEN |
| LR-083 | Unclaimed / undeliverable refunds | What must happen to refunds that cannot be delivered (closed wallet, deceased donor, invalid account): holding period, unclaimed-funds rules, disposal? | OPEN |
| LR-086 | Complaints handling | Are there statutory complaint-handling timelines, record-keeping or escalation routes (e.g. a consumer or payments complaints body) for donor and owner complaints? | OPEN |

## Data protection, hosting and retention

Stage 9 consequence: Secret manager hosting location (LR-011) and provider data-processing terms (LR-033) are open. Evidence and inbox rows have no retention policy (LR-012).

| ID | Title | Exact definition (register) | Status |
|---|---|---|---|
| LR-010 | Data protection registration & DPO | **Question:** Registration/licensing requirements as data controller with POTRAZ (Data Protection Authority) under the Cyber and Data Protection Act; DPO appointment requirements; breach notification obligations and timelines. | OPEN |
| LR-011 | Cross-border transfer & hosting location | **Question:** May personal data (incl. KYC) be hosted outside Zimbabwe? Conditions for transfers to cloud providers and vendors (KYC, SMS, email, error tracking)? Any data localisation for financial data? | OPEN |
| LR-012 | Retention periods | **Question:** Minimum and maximum retention for KYC records, financial/transaction records, ledger, audit logs, compliance cases, and marketing data; start events (e.g. account closure, transaction date). | OPEN |
| LR-033 | Vendor contracts & data processing terms | **Question:** What contractual terms (data processing agreements, sub-processors, breach notice, audit rights, data location, deletion on exit) are required with KYC vendors, PSPs, SMS/email providers and hosting? | OPEN |
| LR-035 | Lawful basis & consent for processing | **Question:** Lawful basis for each processing purpose (account, KYC, fraud prevention incl. device/IP signals, analytics, marketing); form and records of consent where consent is the basis. | OPEN |
| LR-074 | Identity-data retention and deletion | (a) How to reconcile CDPA s13 minimisation and deletion rights with MLPCA s24 (if in scope), S.I. 98/2026 PVO retention and other mandates; (b) whether crypto-shredding (destroying per-subject keys, including for backups) counts as deletion; (c) retention terms required of KYC and screening vendors, including vendor-side deletion. | OPEN (complements LR-012, LR-034) |

## Tax and fees

Stage 9 consequence: No fees are calculated or charged in Stage 9.

| ID | Title | Exact definition (register) | Status |
|---|---|---|---|
| LR-016 | Tax on platform fees & transactions | **Question:** VAT and income tax treatment of platform fees; any transaction-level taxes (e.g. intermediated money transfer tax) on donations/payouts and who withholds; tax registration requirements. | OPEN |
| LR-028 | Platform fee model | **Business decision (plus tax review via LR-016):** fee deducted from donation vs donor-covers-fees vs optional tip; fee disclosure requirements. | OPEN |
| LR-059 | IMTT and income tax on donation flows | Is IMTT charged on both the collection and payout legs (and on fee remittance)? Any exemption for donations or registered PVOs? Are donations received by individuals taxable income? What must be disclosed to donors and beneficiaries about IMTT deductions? *(folded in from LR-077)* | OPEN |

## Required before any production execution

1. Counsel's written answers to LR-001, LR-003, LR-004 (and its canonical items LR-036/037/039/041), LR-005/LR-044, LR-008, LR-009/LR-053, LR-012, LR-018, LR-019, LR-020, LR-022, LR-035, LR-043.
2. The owner's acceptance of Stages 1–9 ([stage-acceptance-register.md](../governance/stage-acceptance-register.md)).
3. A provider selection ADR superseding [ADR-040](../adr/ADR-040-payment-provider-selection-deferred.md), with the provider's written answers to the PCR items ([provider-confirmation-register.md](provider-confirmation-register.md)).
4. A ledger (Stage 10) and a screening provider.
5. An ADR changing `psp.LiveExecutionEnabled` (CLAUDE.md rule).
