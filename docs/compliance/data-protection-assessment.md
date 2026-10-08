# FundZim — Data Protection Assessment (Zimbabwe)

**Stage 1 deliverable.** Verified on **2026-10-08**. **Not legal advice.** This document is a design-stage
assessment, not a formal Data Protection Impact Assessment (DPIA). Formal DPIAs are produced per feature
from Stage 4 onward, with sign-off from the Data Protection Officer (DPO) once one is appointed.

It applies the Zimbabwean data-protection rules found in Stage 1 research to FundZim's planned processing
and defines the controls later stages must implement. It builds on the Stage 0 documents
[../PRIVACY.md](../PRIVACY.md) and [../DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md), which remain in
force. Where this document is more specific, it governs for the Zimbabwean legal context. KYC-specific
technical protections are in [../security/identity-data-protection.md](../security/identity-data-protection.md).

Source IDs (`Sxx`) refer to [regulatory-landscape.md §24](regulatory-landscape.md#24-sources-consulted).

---

## 1. Legal framework (what the sources say)

| Instrument | Key points relied on | Source, confidence |
|---|---|---|
| Cyber and Data Protection Act [Chapter 12:07] (CDPA), Act 5/2021 | POTRAZ is the Data Protection Authority. "Sensitive data" includes health information, genetic information, and "gender, age, marital status or family status". Child = under 18. **Lawful bases (s10).** Consent, or a basis without consent: legal obligation, vital interests, public interest, legitimate interests. **Sensitive data (s11).** Written consent required. **Genetic, biometric and health data (s12).** Written consent required; health data otherwise only under a health-care professional's responsibility. **Principles (s13).** Including retention "no longer than is necessary". **Rights (s14).** Informed, access, object, correction, deletion of false or misleading data. **Processors (s18(5)).** A written contract is required. **Breaches (s19).** Notify POTRAZ within 24 hours. **Children and incapacitated persons (ss26–27).** Their rights are exercised by a parent, guardian or court-designated person. **Automated decisions (s25).** No solely automated decision with significant effect, absent consent or a legal basis. **Transfers (ss28–29).** Adequacy required, or a s29 exception. **Penalties (s33).** | [S35](regulatory-landscape.md#24-sources-consulted), HIGH |
| S.I. 155 of 2024 (Licensing of Data Controllers and Appointment of DPOs) | **Licence (s3–4).** Required for anyone processing personal information who determines purposes and means; apply on Form DP1. **Validity (s5).** 12 months; renew at least 3 months before expiry. **Tiers (s6).** By number of data subjects: 50–1,000; 1,001–100,000; 100,001–500,000; over 500,000. **Fees.** USD 50 / 300 / 500 / 2,500. **Notifications to POTRAZ.** Intended transfers outside Zimbabwe (s10(2)(c)), and processing involving biometric or genetic data (s10(2)(d)). **Children (s10(5)).** Verified parental consent, DPIAs, no automated decisions. **DPO (s12–14).** Appointed and notified on Form DP2; certified. **Security (s16).** Measures required. **Breaches (s17).** Report to POTRAZ within 24 hours (Form DP3); tell affected data subjects within 72 hours if high risk; keep a breach register; respond to POTRAZ in 14 days; investigation report in 21 days. | [S36](regulatory-landscape.md#24-sources-consulted), HIGH |
| POTRAZ guideline: Processing of Children's Personal Information | Verify guardianship (birth certificates, adoption, custody or guardianship orders). Do not process children's data "even if the information has been made public by the child". Use upfront age verification. Obtain prior POTRAZ authorisation for transfers of minors' data to countries without adequate protection, and for high-risk processing. | [S38](regulatory-landscape.md#24-sources-consulted), HIGH-MEDIUM. Guidelines "do not have the force of law" ([S39](regulatory-landscape.md#24-sources-consulted), MEDIUM). FundZim follows them as good practice. |
| POTRAZ enforcement | Inspections reported from 1 September 2026, prioritised by sector | [S37](regulatory-landscape.md#24-sources-consulted), MEDIUM |
| Consumer Protection Act s48, s52, s54 | Confidentiality of consumer information. Online privacy and security disclosures. Unsubscribe rules for unsolicited communications. | [S40](regulatory-landscape.md#24-sources-consulted), HIGH |
| MLPCA s24, s31; PVO Act s20A; S.I. 98/2026 | Retention duties that override minimisation for regulated records. Tipping-off limits what may be disclosed. PVO owners' duty to identify donors. | [S21](regulatory-landscape.md#24-sources-consulted), [S34](regulatory-landscape.md#24-sources-consulted), [S42](regulatory-landscape.md#24-sources-consulted), HIGH |

**Disputes to note:**
- Veritas has argued that S.I. 155/2024 is ultra vires. No court ruling was found (RC-20).
- The validity of the PVO Amendment Act is disputed (LR-050).

Until counsel advises otherwise, FundZim treats both as in force.

## 2. FundZim's role: controller and processor (LEGAL_REVIEW_REQUIRED, LR-057)

| Party | Processing | Provisional characterisation | What must be confirmed |
|---|---|---|---|
| **FundZim** | Accounts, donor records, campaign content, KYC/KYB, risk and fraud, payouts, notifications, audit | **Controller.** It determines purposes and means. A licence is required before processing (REQ-045). | Licence tier and timing. Whether FundZim is controller, joint controller or processor for beneficiary data that campaign owners upload. |
| **Campaign owner (individual)** | Writes the story and uploads beneficiary details and photos | Possibly a controller for the beneficiary data they choose to publish. Personal or household exemption (S.I. 155 s8) unlikely to apply to public fundraising. | Whether owners need their own licences. FundZim's duties when an owner publishes third-party data. |
| **Campaign owner (organisation: PVO, school, church)** | As above, plus receiving donor data for its own PVO duties | Independent controller for donor data received from FundZim. Likely needs its own POTRAZ licence. | Basis for FundZim → PVO sharing (LR-049). |
| **Payment service provider** | Donor payment data, payout destination data | Independent controller for its regulated processing (AML and payments law). Possibly a processor for some FundZim-instructed operations. | Allocation in the contract (LR-033, PCR). |
| **KYC/KYB vendor** (not selected) | ID documents, selfies/biometrics, verification results | **Processor** for FundZim. Written contract needed (s18(5)). Cross-border transfer if offshore. | Vendor location, biometric processing, sub-processors (LR-058, LR-011). |
| **Hosting / cloud provider** (not selected) | All stored data | **Processor.** Cross-border transfer if offshore. | Hosting region (PD-38, LR-011). |
| **SMS / email / WhatsApp providers** | Phone numbers, emails, message content | **Processors** (transactional). Marketing has its own consent rules (CPA s54, LR-023). | Contracts, location. |
| **Screening vendor** (sanctions/PEP, not selected) | Names, DOBs, nationality | **Processor** | Contracts, location. |
| **Error tracking / analytics** | Pseudonymous technical data | Processor. Minimised and scrubbed ([../OBSERVABILITY.md](../OBSERVABILITY.md)). | Location. |

**Control:** a **vendor and processing register** listing for each party: role, purposes, data categories,
classification (C0–C4), destination country, transfer basis (adequacy or s29 exception), DPA reference,
POTRAZ notification reference, and review date. No vendor receives C2+ data before its row is complete and
approved by the DPO (REQ-052).

## 3. Data subjects and categories

| Data subject | Categories | Class | Notes |
|---|---|---|---|
| Donor (identified) | Name, email/phone, amount, currency, time, provider reference, donor country (from provider) | C2 | Anonymity options (§6) |
| Donor (card) | **No card data at FundZim.** The provider holds PAN/CVV; FundZim keeps the provider token or reference only. | — | [../PAYMENTS.md](../PAYMENTS.md) §16 |
| Campaign owner | Account data, DOB, ID document and number, selfie/liveness (if used), address where required, payout account | C2 / C3 | KYC levels per [kyc-architecture.md](kyc-architecture.md) |
| Organisation officers, BOs, controllers | Identity data, ownership percentages, roles | C3 | [kyb-architecture.md](kyb-architecture.md) |
| Beneficiary | Name, relationship, payout account, evidence of need (may include **health** data), photos | C2 / C3; health data **sensitive** | [beneficiary-verification.md](beneficiary-verification.md) |
| Minor beneficiary | As above plus guardian link and guardianship evidence | C3; **child data** | §8 |
| Staff | Account, role, access logs, vetting results (LR-089) | C2 / C3 | |

## 4. Lawful basis per purpose (provisional; LR-035)

| Purpose | Proposed basis (CDPA s10) | Sensitive / child data rule |
|---|---|---|
| Account creation and login | Necessary to provide the service (contract), plus consent at signup | — |
| KYC/KYB identity verification | Legal obligation where FundZim or its PSP is bound (MLPCA, if applicable); otherwise legitimate interests in fraud prevention | Biometrics need **written consent** (s12) and POTRAZ notification (REQ-049) |
| Publishing campaign stories | Consent of the person the story is about (beneficiary or lawful representative) | Health data: **written consent of the data subject** (s11–12). Child data: verified parental or guardian consent (S.I. 155 s10(5)). |
| Processing donations | Contract with the donor (payment instruction) | — |
| Fraud prevention and transaction monitoring | Legitimate interests and legal obligation | Human review for significant adverse decisions (s25; LR-071) |
| Sanctions screening | Legal obligation (if FI/DNFBP) or legitimate interests | — |
| Regulatory reporting (FIU, POTRAZ, Registrar) | Legal obligation | Tipping-off constraints |
| Sharing donor data with PVO owners | **Open**: legal obligation of the PVO, or consent at donation time (LR-049) | — |
| Transactional notifications (SMS/email/WhatsApp) | Contract | — |
| Marketing and re-engagement | Consent (opt-in), with unsubscribe in every message (CPA s54) | Never targeted at minors |
| Analytics | Legitimate interests with minimisation, or consent where required | No sensitive data in analytics |

## 5. Topic-by-topic controls

Each control lists the stage that implements it.

### 5.1 Personal information (general)

- **Classify every field.** Each field in the data model carries a C0–C4 class
  ([../DATA-CLASSIFICATION.md](../DATA-CLASSIFICATION.md)). Stage 2 schema reviews reject unclassified
  columns.
- **Minimise at collection.** Collect only fields with a documented purpose. Optional fields stay optional.
- **Notices at collection.** Required for both direct and indirect collection (CDPA ss15–16), with
  versioned privacy notices and acceptance evidence
  ([audit-evidence-model.md](audit-evidence-model.md)).
- **Licence before real data.** No real personal data is processed in any environment until the POTRAZ
  licence is held (REQ-045). Non-production environments use synthetic data only
  ([../TESTING.md](../TESTING.md)).

### 5.2 KYC documents

- **Restricted storage.** KYC documents are **C3 RESTRICTED**. They live only in the `kyc` schema and the
  `private-kyc` bucket. Access goes through the `kyc` module, with justification, short-lived presigned URLs
  and an audit event for every view ([ADR-009](../adr/ADR-009-kyc-storage-separation.md),
  [../security/identity-data-protection.md](../security/identity-data-protection.md)).
- **Encrypted identity numbers.** National ID and passport numbers are encrypted at application level,
  with a blind index for duplicate detection.
- **Biometrics.** Liveness or face-match is optional per KYC configuration. Enabling it requires explicit
  written consent capture, a completed DPIA, POTRAZ notification (REQ-049) and a vendor DPA (LR-058,
  LR-063 (→ LR-058)).
- **Retention.** Under the `KYC` retention class; see §10.

### 5.3 Donor information

- Donor contact data (C2) is visible only to the donor, FINANCE, COMPLIANCE and SUPPORT (masked).
- It is **never** shown to campaign owners by default. PD-07 keeps owner access to donor contact details
  off.
- Donor country and amount are captured from provider data where available, for monitoring and for
  PVO-owner reporting (REQ-026). The sharing basis is open (LR-049).

### 5.4 Anonymous donations

Display options from Stage 0 [../PRIVACY.md](../PRIVACY.md) §5: public, amount hidden, or anonymous.
"Anonymous" hides the donor from the public and the campaign owner, but **not** from compliance or finance.

Two open questions limit anonymity:
- PVO Act s20A requires PVOs to endeavour to identify donors and to check the good faith of anonymous ones.
- Whether anonymity is permissible above thresholds (LR-026).

**Control:** anonymity is a per-owner-type policy. For PVO campaigns above a configurable threshold,
anonymous donors may still be disclosed to the PVO **only if** LR-049 resolves that a basis exists. Until
then, they are not disclosed, and the PVO receives aggregate data only.

### 5.5 Campaign stories

- Stories are C0 once published, so they must not contain C3 data.
- **Upload scanning.** The editor and uploads are screened for ID numbers, phone numbers, account
  numbers, medical record numbers and minors' identifying details. Matches are flagged for the owner and
  the reviewer before publication ([campaign-approval-policy.md](campaign-approval-policy.md)).
- **EXIF removal.** EXIF and location metadata are stripped from all images
  ([ADR-008](../adr/ADR-008-s3-object-storage.md)).
- **Search indexing.** Sensitive categories (medical, minors) default to `noindex`, per Stage 0
  [../SECURITY.md](../SECURITY.md).

### 5.6 Medical information voluntarily submitted

- **Hidden by default.** Medical detail (diagnosis, hospital, treatment documents) is **not published by
  default** (PD-39). The public page shows the category and a summary the data subject approved.
  Supporting medical documents are private review evidence (C3), never published.
- **Written consent from the right person.** Consent comes from the **data subject** (the patient), or,
  for a child or a person lacking capacity, from their parent, guardian or court-designated representative
  (CDPA ss26–27). The organiser's own consent is insufficient unless they are that person.
- **Consent artefact.** The consent record stores: who consented, in what capacity, what exactly will be
  published, the method, a timestamp and the evidence ID. Whether click-through or OTP consent counts as
  "written" is open (LR-058/LR-063 (→ LR-058)), so the provisional design captures a signed-document upload **or** an
  OTP-confirmed attestation, configurable per counsel's answer.
- **Withdrawal.** When consent is withdrawn, the medical content is unpublished promptly, and the campaign
  is re-reviewed.

### 5.7 Minors

- **Adults only.** Minors cannot hold accounts, own campaigns or receive payouts (REQ-066).
- **Campaigns for minors.** These require beneficiary type `MINOR` with:
  - a verified guardian as owner;
  - guardianship evidence (birth certificate, adoption, custody or guardianship order);
  - written consent for any health data;
  - a completed DPIA template for that campaign class ([beneficiary-verification.md](beneficiary-verification.md)).
- **Public page limits.** Defaults are a first name or initials only, no school name, no faces unless the
  guardian's consent is verified (PD-39), and no location detail finer than district.
- **No automated decisions** using minors' data (S.I. 155 s10(5)).
- **Hosting offshore.** If minors' data is stored or replicated offshore, the prior POTRAZ authorisation
  question (LR-058) must be answered before minors' campaigns are enabled.
- **Donor age.** Donors attest that they are 18 or older at checkout. Card and mobile-money rails carry
  their own age controls (LR-021).

### 5.8 Consent management

- **Consent records are append-only.** Each records subject, purpose, scope, version, method, actor
  (self or representative), timestamp and withdrawal.
- **Separate consents.** Each purpose is consented to separately: no bundling of marketing with service
  terms.
- **Withdrawal.** Available at any time, free of charge (CDPA s11(2)), with effects applied promptly.

### 5.9 Public visibility

Each data element has a visibility rule. Defaults are the most protective option:

| Element | Default |
|---|---|
| Owner name | Public, display name only |
| Beneficiary name | Public; initials for minors |
| Medical detail | Hidden; summary only |
| Donor identity | Owner's choice of three modes |
| Amounts raised | Public per currency |
| Payout details | Never public |
| Verification status | Badge only, no documents |

### 5.10 Cross-border processing

- **Assess every destination.** For any destination outside Zimbabwe, record an adequacy assessment or a
  s29 exception (contract necessity or unambiguous consent) in the vendor register, and notify POTRAZ of
  the intended transfer (S.I. 155 s10(2)(c)) **before** the transfer starts.
- **International donors.** Their data is mostly collected *into* Zimbabwe. Their own countries' laws may
  also apply (Stage 0 [../PRIVACY.md](../PRIVACY.md) §13).
- **Hosting region.** Undecided (PD-38). The design must not preclude in-country hosting of the `kyc`
  schema and the `private-kyc` bucket.

### 5.11 Retention

Retention classes and provisional defaults (configuration, not code; LR-012):

| Class | Provisional default | Basis |
|---|---|---|
| FINANCIAL (payments, ledger, payouts, refunds) | ≥ 10 years | Longest verified period: RBZ 2017 Guidelines para 9.2 for providers/participants, likely imposed contractually ([S04](regulatory-landscape.md#24-sources-consulted)) |
| KYC (identity records, verification results, documents) | ≥ 5 years after the relationship ends | MLPCA s24(2)(a) for FIs ([S21](regulatory-landscape.md#24-sources-consulted)) |
| AML case and STR records | ≥ 5 years | MLPCA s24(2)(c)–(d) |
| AUDIT | Aligned to the longest class an event evidences | Evidential |
| Campaign content after closure | Unpublished/archived after a business-defined period. Financial references kept per FINANCIAL. | Minimisation (s13) |
| Marketing consent and preferences | Until withdrawal, plus proof-of-consent period | CDPA |
| Application logs | Operational decision; short, scrubbed ([../OBSERVABILITY.md](../OBSERVABILITY.md)) | Minimisation |

These periods come from the verified sources cited. Whether each source **binds FundZim** is open (LR-012,
LR-051). Where several apply, the longest governs, and the legal obligation is the basis for keeping data
beyond minimisation (CDPA s10(3)).

### 5.12 Erasure requests

- The CDPA s14 deletion right covers **false or misleading data**. FundZim honours broader deletion
  requests where no retention duty applies (LR-034).
- **Under a retention duty:** restrict processing, unpublish, and pseudonymise where possible; delete when
  the duty expires (crypto-shredding of per-subject keys for KYC; Stage 0 [../PRIVACY.md](../PRIVACY.md)
  §8).
- **Ledger and audit records** are never deleted. Personal fields linked to them are pseudonymised by
  reference where design permits ([../AUDIT.md](../AUDIT.md)).
- **Tipping-off.** Responses to access requests from subjects under investigation are reviewed by
  compliance for tipping-off risk (MLPCA s31). Any withholding is recorded with its legal basis.

### 5.13 Security incidents

- **Clocks.** The 24-hour POTRAZ notification clock starts at **awareness**. The 72-hour data-subject
  notification applies to high-risk breaches (S.I. 155 s17).
- **Playbook and register.** See [incident-response-workflows.md](incident-response-workflows.md). Form
  DP3 is pre-drafted. A breach register is kept, with follow-ups to the 14-day POTRAZ information request
  and the 21-day investigation report.
- **Drills.** At least one breach-notification drill before the pilot ([regulatory-readiness-checklist.md](regulatory-readiness-checklist.md)).

### 5.14 Access logging

- Every view, export or download of C3 data (KYC documents, identity numbers, payout account numbers,
  medical evidence, unmasked donor identities) generates an audit event recording actor, purpose or
  justification, target, time and correlation ID ([../AUDIT.md](../AUDIT.md),
  [audit-evidence-model.md](audit-evidence-model.md)).
- Bulk exports require maker-checker ([operational-controls.md](operational-controls.md)).
- Access logs are reviewed periodically. Anomalies (volume, time of day, unusual targets) alert the
  security administrator.

## 6. Licensing and DPO readiness

| Item | Requirement | Status (2026-10-08) |
|---|---|---|
| POTRAZ data-controller licence (Form DP1, tier by data-subject count) | Before processing real personal data | Not held |
| DPO appointed, certified, notified (Form DP2) | Before processing | Not appointed |
| Transfer notifications | Before any offshore processing | N/A (no vendors yet) |
| Biometric processing notification | Before enabling liveness | N/A |
| Licence renewal calendar | At least 3 months before expiry | N/A |

Tier planning: launch volumes are likely to fall in Tier 2 (1,001–100,000 data subjects). FundZim's
data-subject counter (distinct donors + owners + beneficiaries + staff) is monitored monthly so that tier
changes are caught (REQ-045).

## 7. Risk summary

| ID | Risk | Likelihood (initial) | Impact | Key controls |
|---|---|---|---|---|
| DP-R1 | Publishing health data without the data subject's written consent | High without controls | High (offence; harm to beneficiary) | §5.6 consent artefact; reviewer check; hidden-by-default |
| DP-R2 | Children's identities exposed in public campaigns | Medium | High | §5.7 defaults; guardian verification |
| DP-R3 | Processing before the POTRAZ licence | Medium (timeline pressure) | High (offence) | Pre-processing gate |
| DP-R4 | Offshore vendor without a transfer basis or notification | Medium | Medium–High | Vendor register gate |
| DP-R5 | Biometric KYC without consent or notification | Medium | High | Feature gate (§5.2) |
| DP-R6 | Retention conflict (deleting regulated records, or over-retaining) | Medium | Medium | Retention classes; legal-hold flags |
| DP-R7 | Tipping-off through access responses or support messages | Low–Medium | High | Compliance review of access requests in open cases |
| DP-R8 | Over-sharing donor data with PVO owners | Medium | Medium | Basis required (LR-049); aggregate-only default |
| DP-R9 | Breach notification missed (24-hour clock) | Medium | High | Playbook, drills, on-call |

## 8. Open questions

LR-010, LR-011, LR-012, LR-014, LR-015, LR-021, LR-023, LR-026, LR-027, LR-033, LR-034, LR-035, LR-049,
LR-057, LR-058 (see also LR-063 (→ LR-058), LR-069 (→ LR-049), LR-070, LR-071). Business decisions: PD-07, PD-38, PD-39. All are in
[open-legal-questions.md](open-legal-questions.md).

## 9. Stage hand-off

- **Stage 2:**
  - Classification metadata per column.
  - Consent, retention-class, legal-hold and vendor-register entities.
  - Visibility-rule configuration.
  - Beneficiary and guardian model.
- **Stage 4:** Consent capture, notices, age attestation.
- **Stage 5:** KYC storage controls, biometric gate.
- **Stage 6/7:** Story screening, medical and minor defaults, `noindex`.
- **Stage 14:** Access-request tooling, access-log review.
- **Stage 17/18:** Retention and deletion jobs, breach drills.
