# Payment Provider Due-Diligence Checklist

> **Stage 1 deliverable.** This checklist is applied to every shortlisted provider before it can be selected
> (Stage 9, by ADR). A provider is selectable only when every **gate** item passes, every **required** item
> passes or has an approved, documented exception, and the evidence is filed. "Pass" means FundZim has the
> evidence listed and the named reviewer has accepted it. **It is not a certification of the provider.**
> FundZim never claims a provider is "licensed", "compliant" or "certified" on the strength of this checklist.
> It records only the evidence received.

Related: [provider-questions.md](provider-questions.md) (PCR register) ·
[provider-capability-matrix.md](provider-capability-matrix.md) · [provider-comparison.md](provider-comparison.md) ·
[../compliance/financial-responsibility-matrix.md](../compliance/financial-responsibility-matrix.md) ·
[../compliance/open-legal-questions.md](../compliance/open-legal-questions.md)

---

## 1. How to run it

1. Create a **due-diligence case** per provider. Evidence goes into the evidence store as `evidence_records`
   ([../compliance/audit-evidence-model.md](../compliance/audit-evidence-model.md)), classified C2 or C3. Each item
   below references its evidence by ID.
2. Each item has:
   - a **level**:
     - **GATE**: failure ends the evaluation unless the operating model changes by ADR;
     - **REQ**: failure needs a written exception approved by the named approver;
     - **REC**: recommended; a failure is recorded as a risk.
   - a **reviewer** (role), from the functions in [../compliance/operational-controls.md](../compliance/operational-controls.md).
   - a **status**: `NOT_STARTED` → `REQUESTED` → `RECEIVED` → `PASS` / `FAIL` / `EXCEPTION`.
3. Legal or regulatory items are **inputs to counsel**. A PASS on them requires counsel's written view,
   recorded against the relevant `LR-xxx` item.
4. Final sign-off is **maker-checker**: the evaluator completes the checklist, and a second approver (the
   technical lead plus COMPLIANCE for gates) signs. The decision is recorded in the Stage 9 provider-selection ADR.
5. Re-run the checklist **annually** and on any material change: contract amendment, ownership change,
   licence change, or a major incident.

---

## 2. Checklist

### A. Licensing and corporate standing

| # | Item | Level | Evidence required | Pass criteria | Reviewer | PCR / LR |
|---|---|---|---|---|---|---|
| A1 | RBZ licence or authorisation covering the services used (collection, holding, settlement, disbursement) | GATE | A copy of the licence or authorisation letter (category, number, date, conditions); counsel's review | Counsel confirms the authorisation covers the services FundZim will rely on, in FundZim's operating model | COMPLIANCE + counsel | PCR-001; LR-004 |
| A2 | Licence currency and conditions | REQ | Expiry and renewal dates; any conditions or restrictions; confirmation of no pending sanctions or suspensions | Valid through the planned pilot and launch; conditions compatible | COMPLIANCE | PCR-001 |
| A3 | Corporate identity | REQ | Certificate of incorporation, registration documents, registered address, directors | Matches the contracting entity and the licence holder | COMPLIANCE | — |
| A4 | Ownership and beneficial owners | REQ | Ownership structure down to beneficial owners | Screened (sanctions/PEP per [../compliance/sanctions-screening.md](../compliance/sanctions-screening.md)); no unresolved hits | COMPLIANCE | LR-009 |
| A5 | Sponsoring or settlement bank | REQ | Name of the bank(s); a description of the arrangement | Bank identified; arrangement consistent with A1 | FINANCE + COMPLIANCE | PCR-001 |
| A6 | Financial standing | REC | Latest audited financial statements or equivalent | No going-concern qualification | FINANCE | — |
| A7 | Regulatory and litigation history | REC | Declaration of material regulatory actions or litigation | Nothing material and unresolved | COMPLIANCE | — |

### B. Contract terms

| # | Item | Level | Evidence required | Pass criteria | Reviewer | PCR / LR |
|---|---|---|---|---|---|---|
| B1 | Donation crowdfunding on behalf of third-party beneficiaries is permitted | GATE | Contract clause or written confirmation; review of the prohibited-activities list | Explicitly permitted, or confirmed not prohibited in writing; no conflicting warranties (cf. the third-party-beneficiary warranty [P12]) | Counsel | PCR-002, PCR-032, PCR-056 |
| B2 | Holding of funds and safeguarding | GATE | Contract terms describing who holds funds, where (trust or escrow account and bank), segregation and insolvency treatment | Funds held by the licensed party (or its bank trust account), **not** in a FundZim-controlled account (Model A per [ADR-013](../adr/ADR-013-regulatory-operating-model.md)) | Counsel + FINANCE | PCR-003; LR-001, LR-003 |
| B3 | Disbursement on FundZim instruction | GATE (Model A) / REQ (Model C) | Contract terms and API: who can instruct disbursement, to whom, with what controls | FundZim can instruct payouts to verified beneficiaries without taking custody, or a split or sub-merchant structure achieves the same | Counsel + FINANCE | PCR-003, PCR-013 |
| B4 | Settlement terms | REQ | Schedules, thresholds, cut-offs, settlement fees, holds and reserves | Documented and compatible with the payout policy ([../payments/payout-eligibility-and-controls.md](payout-eligibility-and-controls.md)) | FINANCE | PCR-007 |
| B5 | Chargeback, reversal and dispute liability | REQ | Liability allocation, reserves and offset rights, dispute timelines and fees | Allocation understood and reflected in the reserve policy (PD) and [../compliance/financial-responsibility-matrix.md](../compliance/financial-responsibility-matrix.md) | Counsel + FINANCE | PCR-005, PCR-011, PCR-012; LR-020 |
| B6 | Refund terms | REQ | Refund mechanisms, windows and fees | Refunds possible on every rail FundZim will offer, or a documented manual alternative | FINANCE | PCR-006 |
| B7 | Fees | REQ | A complete fee schedule in the contract (collection, payout, settlement, refunds, chargebacks, FX if any, minimums) | Matches the published fees or improves on them; no undisclosed fees | FINANCE | PCR-016 |
| B8 | Termination and exit | REQ | Termination rights and notice; treatment of held funds at termination; data export | Held funds remain payable to beneficiaries or refundable; history exportable in a usable format | Counsel + FINANCE | PCR-022 |
| B9 | Data processing terms | REQ | A data processing agreement: roles, sub-processors, data location, breach notification, deletion | Meets the requirements in [../compliance/data-protection-assessment.md](../compliance/data-protection-assessment.md) | COMPLIANCE (DPO) + counsel | PCR-019; LR-011, LR-033 |
| B10 | Change control | REC | Notice period for API, fee and term changes | At least a reasonable notice period is defined | Technical lead | — |
| B11 | Taxes | REQ | Which taxes and levies the provider deducts or invoices on which legs | Understood and reflected in the ledger design | FINANCE + tax adviser | PCR-024; LR-016 |

### C. Technical integration

| # | Item | Level | Evidence required | Pass criteria | Reviewer | PCR |
|---|---|---|---|---|---|---|
| C1 | Sandbox or test environment | REQ | Sandbox access; test instruments per method and currency | Covers every method and currency FundZim will launch with, or the gap is documented | Technical lead | PCR-040, PCR-035 |
| C2 | Authentic status: webhook signing | GATE (with C3) | Signing documentation; secret rotation; tested verification in the sandbox | A signature over the raw body with a timestamp or nonce, **or** if unsigned, C3 must pass on its own | Technical lead + SECURITY_ADMIN | PCR-017 |
| C3 | Authenticated server-to-server status API | GATE (with C2) | API docs; sandbox test | FundZim can query the authoritative status for any payment, refund and payout by its own reference | Technical lead | PCR-010 |
| C4 | Status finality semantics | REQ | Written definitions of terminal states, "not found", expiry and error codes | Enough to apply the UNKNOWN-resolution rules in [transaction-lifecycle.md](transaction-lifecycle.md) | Technical lead + FINANCE | PCR-010 |
| C5 | Idempotency | REQ | Documented idempotency key or duplicate-reference rejection for payments, refunds and payouts | Retries with the same FundZim reference cannot create a duplicate charge, refund or payout; otherwise the adapter is set to poll-only | Technical lead | PCR-009, PCR-046, PCR-064 |
| C6 | Webhook delivery | REQ | Retry schedule and maximum; event catalogue; source IPs | Retries exist, or polling compensates (as with Pesepay's "no retry" [S10]) | Technical lead | PCR-017 |
| C7 | Reconciliation data | REQ | Settlement and transaction reports (API or SFTP), format and fields, timing; fee itemisation | Enough to drive the *settled* ledger state and Stage 17 reconciliation | FINANCE + technical lead | PCR-007, PCR-016 |
| C8 | Payout API | GATE (Model A, unless B3 is met by split) | API docs; name validation; status; returns | Matches [payout-lifecycle.md](payout-lifecycle.md) requirements, including the UNKNOWN handling | Technical lead + FINANCE | PCR-013, PCR-014, PCR-015 |
| C9 | Currency handling | REQ | Currency codes, minor units, per-transaction currency selection | Deterministic mapping to `USD`/`ZWG`; no implicit conversion | Technical lead | PCR-018, PCR-030, PCR-048 |
| C10 | Availability and SLA | REQ | SLA document; status page; maintenance windows | Defined uptime commitment and incident communication | Technical lead | PCR-008 |
| C11 | Incident notification | REQ | Contractual commitment to notify FundZim of incidents, including security incidents | A defined notification path and timeline | SECURITY_ADMIN | PCR-008, PCR-019 |
| C12 | API credentials and access | REQ | How keys are issued, scoped, rotated and revoked; separate test and live keys | Supports least privilege and rotation | SECURITY_ADMIN | — |
| C13 | Portal roles and audit | REQ | Merchant portal roles and permissions; dual approval; audit log | Portal refund and payout rights can be restricted to FINANCE with dual control, or disabled | FINANCE + SECURITY_ADMIN | PCR-021 |

### D. Financial controls

| # | Item | Level | Evidence required | Pass criteria | Reviewer | PCR |
|---|---|---|---|---|---|---|
| D1 | Reserves and holds imposed by the provider | REQ | Policy on rolling reserves and holds | Compatible with FundZim's reserve design ([../ledger/settlement-and-custody-model.md](../ledger/settlement-and-custody-model.md)) | FINANCE | PCR-007 |
| D2 | Platform fee mechanism | REQ | How FundZim's fee is collected (split share, invoice, deduction) | Fee reaches FundZim's own account without FundZim holding beneficiary funds | FINANCE + counsel | PCR-016; LR-001 |
| D3 | Statement accuracy test | REQ | Sandbox and pilot statements reconciled against FundZim records | Zero unexplained differences in test reconciliation | FINANCE | PCR-007 |
| D4 | Currency of settlement | REQ | Settlement account currency per rail | Settlement currency = transaction currency; no implicit FX ([currency-and-fx-policy.md](currency-and-fx-policy.md)) | FINANCE | PCR-004, PCR-018 |

### E. AML, sanctions and fraud

| # | Item | Level | Evidence required | Pass criteria | Reviewer | PCR / LR |
|---|---|---|---|---|---|---|
| E1 | Provider's own AML programme | REQ | Summary of the AML/CFT policy, CDD on merchants and sub-merchants, transaction monitoring | Documented; responsibilities split clearly with FundZim | COMPLIANCE | PCR-020; LR-007, LR-008 |
| E2 | Sanctions screening | REQ | Lists screened; screening points (onboarding, payout) | Documented; FundZim's own screening is not assumed to be replaced | COMPLIANCE | PCR-020; LR-009 |
| E3 | Information sharing and investigations | REQ | How fraud and AML information is shared; FundZim's reporting duties to the provider | Defined process consistent with [../compliance/compliance-case-management.md](../compliance/compliance-case-management.md) | COMPLIANCE | PCR-020 |
| E4 | Beneficiary onboarding requirements | REQ (Model C/split) | KYC/KYB the provider requires from each beneficiary or sub-merchant | Achievable for FundZim's beneficiary types ([../compliance/beneficiary-verification.md](../compliance/beneficiary-verification.md)) | COMPLIANCE | PCR-042, PCR-056 |

### F. Security

| # | Item | Level | Evidence required | Pass criteria | Reviewer | PCR |
|---|---|---|---|---|---|---|
| F1 | Card data scope | REQ (if cards are offered) | Confirmation that card data is captured only on hosted pages or fields; PCI DSS attestation evidence supplied by the provider | Raw card data never reaches FundZim. FundZim records only that evidence was received and **makes no claim** about the provider's status | SECURITY_ADMIN | PCR-023 |
| F2 | Security testing | REC | Summary of recent independent penetration tests; vulnerability management | Recent and material findings remediated | SECURITY_ADMIN | — |
| F3 | Transport and encryption | REQ | TLS versions; encryption of data at rest | TLS 1.2+ for all API and webhook traffic | SECURITY_ADMIN | — |
| F4 | Certifications and attestations | REC | Any certifications or attestations the provider holds, **with evidence documents** | Evidence on file. Claims without evidence are recorded as UNVERIFIED | SECURITY_ADMIN | — |
| F5 | Webhook endpoint hardening needs | REQ | Source IP list or mTLS options | Allows FundZim to apply defence in depth on webhook ingress | SECURITY_ADMIN | PCR-017 |

### G. Exit and portability

| # | Item | Level | Evidence required | Pass criteria | Reviewer | PCR |
|---|---|---|---|---|---|---|
| G1 | Data export | REQ | Export of the transaction, settlement and dispute history | Machine-readable; covers the retention needs (LR-012) | FINANCE | PCR-022 |
| G2 | Funds continuity on exit | GATE | Contractual treatment of held funds on termination or provider failure | Beneficiary and refund obligations remain payable | Counsel + FINANCE | PCR-022, PCR-003 |
| G3 | Multi-provider compatibility | REC | No exclusivity clause, or an acceptable one | FundZim can run a backup provider ([ADR-007](../adr/ADR-007-payment-provider-abstraction.md)) | Counsel | — |

---

## 3. Outcome record (one per provider)

```
provider:                <name, contracting entity>
case_id:                 <due-diligence case id>
evaluated_by / date:     <role, date>
approved_by / date:      <role(s), date>          # must differ from evaluator
gates:                   A1 _ B1 _ B2 _ B3 _ C2/C3 _ C8 _ G2 _
required_failed:         [ids + exception refs]
risks_accepted:          [ids + rationale + owner]
legal_opinion_refs:      [LR-xxx → evidence ids]
decision:                SELECTABLE | NOT_SELECTABLE | SELECTABLE_WITH_CONDITIONS
conditions:              [...]
next_review_due:         <date>
```

A `SELECTABLE` outcome is only an input to the Stage 9 provider-selection ADR. It authorises no live
processing; that requires the Stage 20 gate ([../ROADMAP.md](../ROADMAP.md)).

---

## 4. Status of the current candidates (2026-10-08)

| Provider | Checklist status | Note |
|---|---|---|
| Pesepay, Paynow, Payonify, Linkwa | `NOT_STARTED` | Due-diligence order per [provider-comparison.md §5.2](provider-comparison.md#52-leading-candidates-for-due-diligence). The project owner must confirm the identities of Payonify and Linkwa first. |
| Smile&Pay (ZB), ContiPay, EcoCash Open API | `NOT_STARTED` | Documentation access is needed first (PCR-070, PCR-080, PCR-090). |
| Paystack, Flutterwave, DPO | Not evaluated | Not viable on current evidence ([capability matrix §4](provider-capability-matrix.md#4-not-viable-on-current-evidence)). |
