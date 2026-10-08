# FundZim Privacy Architecture

Status: Stage 0 design. Legal bases, retention periods, cross-border rules and notification duties are
**not decided** — every such point is marked LEGAL_REVIEW_REQUIRED and tracked in the Compliance
Assumptions Register ([COMPLIANCE.md](COMPLIANCE.md)).
Related: [DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md), [SECURITY.md](SECURITY.md), [AUDIT.md](AUDIT.md).

> This document describes privacy engineering intent. It does not claim compliance with the Cyber and Data
> Protection Act [Chapter 12:07] or any other law. Applicability and obligations — including any
> registration/licensing with POTRAZ as data protection authority — must be confirmed by qualified
> Zimbabwean counsel (LEGAL_REVIEW_REQUIRED, LR-010).

---

## 1. Principles

1. **Minimise.** Collect a field only when a specific feature, legal obligation or security control needs
   it, and only at the point it is needed (progressive collection: donors need far less than payout
   recipients).
2. **Separate.** C3 identity data is physically and logically separated (ADR-009).
3. **Purpose-bind.** Data collected for KYC is used for verification, compliance and fraud prevention —
   not marketing, analytics or model training.
4. **Default private.** Donor public display, campaign discoverability of sensitive categories and referral
   tracking default to the most private reasonable option.
5. **Accountable access.** Staff access to personal data is permissioned, justified where sensitive, and
   audited.
6. **Explain.** Users are told in plain language (English first; Shona and Ndebele translations a product
   decision) what is collected, why, who sees it and how long it is kept.

## 2. Data subjects

| Subject | Typical data | Notes |
|---|---|---|
| Donor (registered or guest) | Contact, donation records, display preference | May be outside Zimbabwe |
| Campaign owner | Account, KYC, payout destination | Must reach IDENTITY_VERIFIED to publish |
| Beneficiary | Name, relationship, story details, sometimes medical/death information | Often **not** the account holder; may be a minor or deceased |
| Organisation representatives, directors, trustees, beneficial owners | Identity data | Organisation verification |
| Reporters (people who report campaigns) | Contact, report text | Identity protected from the reported party |
| Staff | Account, activity in audit log | |

## 3. Lawful basis and consent — LEGAL_REVIEW_REQUIRED (LR-035)

The applicable lawful bases under Zimbabwean law (and, for diaspora donors, possibly foreign law) are to be
confirmed. Working design assumptions, **not conclusions**:

| Processing | Assumed basis (to be confirmed) |
|---|---|
| Account and donation processing | Performance of the service the user requested |
| KYC, AML/CFT record-keeping, sanctions screening | Legal obligation (scope depends on AML regime applicability — LR-007, LR-008) |
| Fraud prevention, security logging | Legitimate interest / legal obligation |
| Publishing campaign content about a beneficiary | Consent of the beneficiary or lawful representative |
| Health information in medical campaigns | Explicit consent (sensitive data — LEGAL_REVIEW_REQUIRED (LR-014)) |
| Marketing messages | Opt-in consent, separately revocable |

Engineering consequences regardless of final basis:

- consent records are stored as data (who, what text/version, when, how withdrawn) — not as a checkbox
  boolean;
- consent withdrawal must be technically possible and propagate (e.g. unpublishing beneficiary details);
- marketing consent is never bundled with terms acceptance.

## 4. Data minimisation by flow

| Flow | Collected | Explicitly not collected |
|---|---|---|
| Guest donation | Amount, currency, payment method choice, contact for receipt (email or phone), optional display name/message | ID numbers, address, DOB, card data (PSP-hosted), mobile-money PIN |
| Registered donor | Above + account contact | KYC unless risk/legal threshold triggers (thresholds LEGAL_REVIEW_REQUIRED (LR-007)) |
| Draft campaign | Owner contact (BASIC_VERIFIED), story, beneficiary info | — |
| Publish | IDENTITY_VERIFIED data via `kyc` module | — |
| Payout | Payout destination + ownership verification | Data not required by the PSP/payout rail |

Optional fields are clearly optional. Free-text fields warn users not to include ID numbers or medical
records publicly.

## 5. Donor anonymity

- Donors choose: **public** (name + amount shown), **amount hidden**, or **anonymous**.
- Anonymous means hidden from the public **and** from the campaign owner (including in owner exports and
  notifications). It does **not** hide the donor from FundZim compliance, finance, the PSP, or authorities
  where legally required. This must be stated in the donation UI.
- Anonymous donor identity is C2; staff access is permissioned and audited.
- Thank-you messages from owners to anonymous donors (if offered) are relayed by FundZim without revealing
  contact details.
- Donor lists are never publicly enumerable via API (see THREAT-MODEL T-22, T-23).

## 6. Public campaign information and beneficiaries

Campaign stories routinely contain highly sensitive information (diagnoses, deaths, children,
financial hardship). Design requirements (Stage 6/7):

- **Beneficiary consent:** the owner records the beneficiary relationship and attests consent; for
  higher-risk categories (medical, minors) the review flow can require evidence of consent or of authority
  to act (parent/guardian, next of kin). Exact requirements LEGAL_REVIEW_REQUIRED (LR-015).
- **Minors:** stories about minors should avoid full names, schools and locations; the UI guides owners
  and reviewers enforce. Display rules for minors’ images LEGAL_REVIEW_REQUIRED (LR-015).
- **Supporting evidence** (medical letters, invoices, death certificates) is C3, submitted privately,
  never published, stored in `private-kyc`.
- **Image hygiene:** EXIF/GPS stripped on upload; re-encoded.
- **Search engine indexing** for sensitive categories may be opt-in (`noindex` by default) — product
  decision in Stage 16.
- **Unpublishing:** owners (and beneficiaries, via support) can request unpublication; public content is
  removed promptly while financial records remain under retention.
- **WhatsApp/OpenGraph previews** include only title, image and short summary — never beneficiary
  medical detail beyond what the owner put in the title.

## 7. Retention — LEGAL_REVIEW_REQUIRED (LR-012)

No statutory retention periods are asserted in this repository until counsel confirms them. The design
supports **per-category configurable retention**:

| Category | Retention driver | Design default until legal review |
|---|---|---|
| Ledger, payments, payouts, fees | Financial/accounting and possibly AML record-keeping | Retain; do not delete (LR-012) |
| KYC records and documents | AML/CFT record-keeping (if applicable) vs minimisation | Retain for account lifetime + legal period (LR-012); documents may be destroyed earlier than structured results if law permits |
| Audit events | Accountability, financial audit | Retain at least as long as related financial records (LR-012) |
| Account profile (no financial history) | Service provision | Delete/anonymise after closure + short grace period (LR-012, LR-034) |
| Unpublished drafts with no activity | Service provision | Delete after inactivity period (product + LR-012) |
| Application logs | Operations | Short operational window (e.g. 30–90 days, ops decision; not a legal claim) |
| Marketing data | Consent | Until consent withdrawn |
| Backups | Recovery | Rolling window; deletion requests take effect as backups age out (LR-012, LR-034) |

Retention jobs (Stage 17/18) run as audited system actions and produce reports.

## 8. Deletion vs anonymisation vs retention conflicts

A user’s right to deletion (scope LEGAL_REVIEW_REQUIRED (LR-034)) can conflict with financial and AML record-keeping.
Approach:

1. **Separate identity from records.** Financial and audit records reference internal IDs. On deletion,
   the user profile is anonymised (contact data removed, name replaced with a tombstone), while ledger and
   payment records keep the internal ID and amounts.
2. **Legal hold.** Records under an active investigation, dispute, chargeback or regulatory request are
   flagged with a legal hold that blocks deletion; holds are audited and reviewed.
3. **KYC.** KYC data is retained for the legally required period after the relationship ends (LR-012), then
   destroyed (documents deleted from `private-kyc`, KEK-wrapped data keys destroyed — crypto-shredding — so
   backup copies become unreadable).
4. **Public content.** Published campaign content is removed from public view immediately on valid request
   (subject to fraud investigations), even if financial records remain.
5. **Audit log.** Never deleted within retention; personal fields (IP, user agent) may be pseudonymised
   later if permitted (see [AUDIT.md](AUDIT.md) §8).
6. **Users are told** what is deleted and what must be retained and why.

## 9. KYC records

- Collected only via the `kyc` module, only at the level needed (KYC levels in [PRODUCT.md](PRODUCT.md)).
- Stored in the `kyc` schema and `private-kyc` bucket with dedicated keys; identity numbers encrypted with
  HMAC blind index for duplicate detection.
- Staff view requires `kyc.document.view`, justification and is audited; reveal of full identity number
  is a separate audited action.
- Shared with a verification vendor only if one is selected, under contract, with data minimised to what
  the vendor needs. Vendor data retention and location LEGAL_REVIEW_REQUIRED (LR-011, LR-033).
- Not used for marketing, analytics or ML training.

## 10. Administrative access

- Role-based, least privilege (SECURITY §5). ADMIN/SUPER_ADMIN do not automatically see C3.
- Masked-by-default staff UI for C2/C3 (phone, account numbers, ID numbers); reveal is an explicit,
  audited action.
- Support staff can assist users without seeing KYC documents (they see KYC level/status only).
- Periodic access reviews (quarterly, Stage 14 onward): who holds which sensitive permissions, and anomalous
  access patterns from audit data.
- Production database access by humans is exceptional and audited (SECURITY §17).

## 11. Data subject requests — process design

Supported request types (exact rights and response timelines LEGAL_REVIEW_REQUIRED (LR-034)):

| Request | Mechanism |
|---|---|
| Access / copy of data | Self-service export of account, campaigns, donations; staff-assisted for KYC records |
| Correction | Self-service for profile; KYC corrections via re-verification |
| Deletion | §8 process with legal-hold and retention checks |
| Withdraw consent | Self-service toggles (marketing, public display); beneficiary consent withdrawal via support |
| Objection / restriction | Staff-handled case |

Requests are verified (authenticated session plus step-up; or identity verification for non-account
holders such as beneficiaries), tracked as cases with deadlines, and audited (`user.data_export.requested`,
`user.deletion.requested`, `user.anonymised`).

## 12. Breach notification — LEGAL_REVIEW_REQUIRED (LR-010)

- Notification duties (to the data protection authority, affected individuals, PSPs, RBZ or others) and
  timelines are to be confirmed by counsel.
- Engineering prerequisites: audit trail of data access; ability to determine affected subjects and data
  classes from logs and inventory ([DATA-CLASSIFICATION.md](DATA-CLASSIFICATION.md) §4); contact channels
  for affected users; incident process in [SECURITY.md](SECURITY.md) §22.

## 13. Cross-border transfer and hosting location — LEGAL_REVIEW_REQUIRED (LR-011)

- Hosting location (in Zimbabwe, in the region, or international cloud) is **undecided**. It depends on
  data-localisation and cross-border transfer rules, PSP and regulator expectations, cost and reliability.
- International donors’ data and vendors (SMS, email, KYC, error reporting, analytics) may involve transfers
  outside Zimbabwe. Each vendor is recorded in a processor register (Stage 1) with data categories,
  location and contract status.
- Architecture stays portable: container-first, S3-compatible storage, PostgreSQL — no provider-locked
  services in the critical path without an ADR.

## 14. Privacy by design checkpoints

| Stage | Privacy deliverable |
|---|---|
| 1 | Legal review of LR items above; processor register; privacy notice requirements |
| 2 | Schema classification annotations; retention fields and legal-hold design |
| 4 | Consent records; session/IP retention |
| 5 | KYC minimisation, vendor DPA, crypto-shredding design |
| 6/7 | Beneficiary consent, minors guidance, donor anonymity in UI and DTOs |
| 15 | Notification content review (no C3 in SMS/email) |
| 16 | Referral attribution and indexing defaults reviewed |
| 17/18 | Retention jobs, DSR tooling, breach runbook |
