# FundZim AML/CFT and Fraud Risk Framework (Stage 1 design)

> **Status:** Stage 1 design. Stages 5, 11 and 13 implement it (identity, payout hook, risk engine). Nothing
> here states that FundZim is subject to, or compliant with, any AML law. FundZim's status under the Money
> Laundering and Proceeds of Crime Act is **LR-060** (→ LR-051). Legal figures below are **candidate** REGULATORY
> limits that apply **only if** counsel confirms FundZim (or the campaign owner) is in scope. They are never
> hard-coded.
>
> **Related:** [kyc-architecture.md](kyc-architecture.md), [kyb-architecture.md](kyb-architecture.md),
> [transaction-monitoring.md](transaction-monitoring.md), [sanctions-screening.md](sanctions-screening.md),
> [compliance-case-management.md](compliance-case-management.md),
> [payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md) (limits §4, EC-16/17/21),
> [campaign-approval-policy.md](campaign-approval-policy.md), [THREAT-MODEL.md](../THREAT-MODEL.md),
> [ADR-015](../adr/ADR-015-risk-based-identity-verification.md).

---

## 1. Objectives

FundZim faces two different but overlapping risks:

1. **Financial crime through the platform:**
   - money laundering;
   - terrorist financing;
   - proliferation financing;
   - sanctions evasion;
   - misuse of the platform for illegitimate fundraising.
2. **Fraud against donors and the platform:**
   - fake campaigns;
   - account takeover;
   - payout destination hijack;
   - card testing;
   - refund and chargeback abuse;
   - money mules.

The framework applies a **risk-based approach**. Controls get stronger as risk rises (customer, campaign,
transaction, geography, channel). Every control is configurable and sourced, and every override is approved
and audited.

Whatever the outcome of LR-060 (→ LR-051), FundZim runs this framework as **internal policy**:

- PSPs will expect it.
- The FIU can request information from FundZim as a company (MLPC Act s 6E(1)(f)).
- PVO campaign owners have their own duties under SI 98 of 2026 (R2-10, R2-38, HIGH).
- Fraud directly destroys donor trust.

## 2. Governance

| Function | Responsibility | Role |
|---|---|---|
| Risk appetite and policy approval | Approves this framework, risk tiers, prohibited categories and limit values | Founders / board, advised by COMPLIANCE lead and counsel |
| Compliance officer (MLRO-equivalent) | Owns the framework, approves REGULATORY limits, STR decisions, PEP approvals. The MLPC Act s 25 requires a management-level compliance officer for institutions in scope (R2-10, HIGH). Appointment is **PD-28**. | `COMPLIANCE` (named lead) |
| Risk operations | Tunes INTERNAL_RISK limits and monitoring rules; reviews alerts | `COMPLIANCE`, with `FINANCE` for payout-related limits |
| Provider liaison | Records PROVIDER limits from contracts and docs | `FINANCE` |
| Independent review | Periodic review of effectiveness. The MLPC Act s 25 expects an independent audit function for institutions in scope (R2-10). | External reviewer (Stage 19/20) |

## 3. Risk factors and scoring

### 3.1 Factors

| Dimension | Factors (examples) | Data source |
|---|---|---|
| **Customer (owner/organisation)** | Verification level; account age; PEP status ([kyc-architecture.md §7](kyc-architecture.md)); prior rejected, frozen or cancelled campaigns; chargeback and dispute history; device or identity linkage to other accounts; organisation type and structure complexity; nominee or layered ownership ([kyb-architecture.md §3.3](kyb-architecture.md)); adverse information from fraud reports | kyc, campaigns, cases, risk |
| **Beneficiary** | Type (`MINOR`, `INCAPACITATED_ADULT` etc.); payee type (institution preferred = lower risk); beneficiary is shared across campaigns | beneficiaries |
| **Campaign** | Category tier ([campaign-approval-policy.md §4](campaign-approval-policy.md)); goal size relative to category norms; evidence quality; story similarity to known fraud; political-adjacent content (LR-025); fundraising authority basis (`SECTION_8_AUTHORITY` expiring, `NOT_REQUIRED_OTHER`) | campaigns, review |
| **Transaction / donation** | Amount relative to campaign median; velocity; card vs mobile money; donor anonymity; donor = owner or beneficiary linkage (round-tripping); refund requests soon after donating; failed-then-succeeded card sequences | payments, risk |
| **Payout** | First payout; destination new or changed; time since donations settled; payout share of total raised; payout shortly after a spike; payee type | payouts |
| **Geography** | Donor country; card issuing country; IP and device country mismatch; FATF high-risk or monitored jurisdictions (§3.3) | payments (PSP-reported), request metadata |
| **Channel** | New device; VPN or proxy; SIM-swap signal (PCR); OTP failures | auth, risk |

### 3.2 Scoring model (configurable, explainable)

- The Stage 11/13 risk engine produces `risk_score` (integer 0–1000) and `risk_rating` (`LOW` / `STANDARD`
  / `HIGH`) per **subject**: user, organisation, campaign, payout. It is recomputed on events.
- The model is a **rule-weighted sum**, not an opaque ML model, at launch. Each rule has a code, weight,
  explanation text and version. Scores are stored with the list of contributing rule codes, so every
  decision is explainable to a reviewer and auditable.
- Rating thresholds (`risk.rating.high_min`, etc.) are INTERNAL_RISK limit records.
- **Score effects** are configured per decision point:

  | Decision point | Effects |
  |---|---|
  | Onboarding | KYC level required |
  | Campaign submission | Review tier, escalate |
  | Donation acceptance | Block card BIN, step-up, cap amount, require login |
  | Payout | Approval tier (AUTO / SINGLE / DUAL), cooling-off, hold |

- **Automated decisions are limited.** The engine may raise friction: require review, hold a payout, or
  decline a donation attempt that matches card-testing patterns. It may not finally reject a user or
  campaign without human review. This reflects CDPA s 25, the right not to be subject to solely automated
  decisions with significant effects (R2-23, HIGH). **LR-071.**

### 3.3 Geography: high-risk jurisdictions

- FATF list status as researched (R2-13):
  - Zimbabwe was reported removed from the FATF grey list in March 2022 (The Herald, secondary, MEDIUM).
  - Zimbabwe is not on the FATF "Jurisdictions under Increased Monitoring – 19 June 2026" list, as
    reproduced by the Securities Commission of The Bahamas (HIGH-MEDIUM).
  - The list must be re-checked after each FATF plenary (an October 2026 plenary may post-date this
    research).
- MLPC Act s 26A requires institutions in scope to apply EDD and countermeasures for countries the FATF calls
  for, as advised by FIU directive (R2-04, HIGH). The FIU high-risk-jurisdiction directive dated
  17 Feb 2026 was **not read** (research checklist U8).
- **Design:**
  - A `jurisdiction_risk` reference table holds `country_code`, `list_source` (FATF black list, FATF
    increased monitoring, FIU directive, internal), `effective_from`, `effective_to`, `source_ref`,
    `approved_by` and `review_by`.
  - It is maintained by COMPLIANCE with maker-checker. It is never edited in place: a change creates a new
    row.
  - Donations whose card issuer or donor country is on an active list raise the transaction risk. For PVO
    campaigns, they also feed the PVO's foreign-funding aggregates ([kyb-architecture.md §6](kyb-architecture.md)).

## 4. Prohibited and restricted activity

The authoritative list is LR-025 / [campaign-approval-policy.md](campaign-approval-policy.md)
`PROHIBITED_ACTIVITY`. Framework-level additions:

- campaigns for or by **designated (sanctioned) persons** — absolute block ([sanctions-screening.md](sanctions-screening.md));
- campaigns whose purpose is political-party or candidate support — prohibited pending LR-025. PVOs are
  barred from partisan conduct (PVO Act s 20A(g), s 23(4); R2-35, HIGH);
- virtual-asset (crypto) donations — **not supported**. Accepting them would likely bring VASP registration
  questions under S.I. 99 of 2026 (R2-15, HIGH for existence);
- cash collection — **not supported**. FundZim never handles cash, which keeps it outside cash-handling
  risks. Whether any cash transaction reporting duty still exists for non-cash flows is **LR-073** (→ LR-052).

## 5. Limits model

Every limit and threshold in FundZim is a **limit record**. This structure is shared with
[payout-eligibility-and-controls.md §4](../payments/payout-eligibility-and-controls.md); this section is the
authoritative definition of its governance fields.

```sql
-- risk.limits (append-only versions; the effective row is the latest APPROVED version in its window)
id uuid PK, limit_key text NOT NULL,                 -- e.g. 'payout.dual_approval_min', 'donor.unverified.max_per_donation'
limit_type text NOT NULL CHECK (limit_type IN ('REGULATORY','PROVIDER','INTERNAL_RISK')),
scope_type text NOT NULL,                             -- 'global'|'currency'|'provider'|'category'|'org_type'|'risk_rating'|...
scope_value text,                                     -- e.g. 'USD', 'paynow', 'MEDICAL'
currency char(3) REFERENCES currencies(code),         -- required when the value is money
value_minor bigint,                                   -- money limits: integer minor units, never float
value_int bigint, value_bp integer,                   -- counts / durations (seconds) / basis points
source_type text NOT NULL CHECK (source_type IN ('STATUTE','REGULATION','DIRECTIVE','GUIDELINE','PROVIDER_DOC','PROVIDER_CONTRACT','INTERNAL_DECISION')),
source_ref text NOT NULL,                             -- citation (instrument + section + URL + accessed date) or contract clause or decision id
legal_review_ref text,                                -- LR-xxx where applicability is unconfirmed
approval_owner_role text NOT NULL,                    -- COMPLIANCE for REGULATORY, FINANCE for PROVIDER, COMPLIANCE/FINANCE for INTERNAL_RISK
approval_state text NOT NULL CHECK (approval_state IN ('PROPOSED','APPROVED','REJECTED','RETIRED')),
proposed_by uuid NOT NULL, approved_by uuid CHECK (approved_by IS NULL OR approved_by <> proposed_by),
effective_from timestamptz, effective_to timestamptz, review_by date NOT NULL,
supersedes_id uuid REFERENCES risk.limits(id), created_at timestamptz NOT NULL
```

Rules:

1. **Three types, never mixed.**
   - **REGULATORY**: imposed by law or a regulator. It needs a legal source citation, and a `legal_review_ref`
     until counsel confirms that it applies.
   - **PROVIDER**: imposed by a PSP, a rail or a bank. It needs a provider document or contract reference.
   - **INTERNAL_RISK**: FundZim's own risk appetite. It needs an internal decision id.

   The same `limit_key` may have records of more than one type. The engine uses the **most restrictive**
   effective value.
2. **Approval and review.**
   - Approval is maker-checker (`approved_by ≠ proposed_by`), per [operational-controls.md §3](operational-controls.md).
   - Every record has a `review_by` date. A scheduled job raises a case 30 days before expiry.
   - A record past `review_by` stays effective but is flagged. The flag disables `AUTO` payout approval
     (EC-21 behaviour).
3. **Fail closed.** If a limit referenced on a payout or withdrawal path has no approved effective record,
   the path routes to manual review. It never treats the limit as "unlimited" (brief §8; EC-21).
4. **Never in code.** Code references `limit_key`s only. Seed data may create `PROPOSED` records with
   citations; only approval makes them effective.
5. **Currency-specific.** Money limits exist per currency (USD and ZWG separately). There is no implied
   conversion between them ([MONEY.md](../MONEY.md), ADR-018).

### 5.1 Candidate REGULATORY figures found in research (NOT hard-coded)

> **Applies IF FundZim (or, where stated, the campaign owner) is in scope — `LEGAL_REVIEW_REQUIRED`.**
> These figures are seeded as `PROPOSED` limit records with the citation. COMPLIANCE approves each one only
> after counsel confirms applicability (LR-060 (→ LR-051), LR-068 (→ LR-046–LR-048), LR-073 (→ LR-052)). All sources were accessed 2026-10-08.

| Candidate `limit_key` | Figure as stated in source | Who it binds per the source | Source | Confidence | Gate |
|---|---|---|---|---|---|
| `cdd.occasional_txn_min` | Occasional transaction of **USD 5,000 or more** (including linked transactions) triggers identification and verification | Financial institutions and DNFBPs | MLPC Act s 15(1)(b) (R2-03) | HIGH | LR-060 (→ LR-051), LR-061 |
| `cdd.wire_transfer_min` | Domestic or international wire transfer of **USD 1,000 or more** | Financial institutions and DNFBPs | MLPC Act s 15(1)(c), s 27(1) (R2-03, R2-09) | HIGH | LR-060 (→ LR-051) |
| `ctr.nonbank.threshold` | Cash transaction reports at **ZiG 70,000 / USD 5,000**. Non-bank FIs and DNFBPs file CTRs monthly by the 10th, including nil returns, via goAML. | Non-bank FIs and DNFBPs | FIU AML/CFT Directive 01/04/2024 (R2-12) | HIGH (scanned PDF read visually) | LR-060 (→ LR-051), LR-073 (→ LR-052) (FundZim handles no cash) |
| `str.deadline` | STR "promptly, but not later than **three working days**" after forming suspicion | FIs, DNFBPs and staff | MLPC Act s 30(1) (R2-06) | HIGH | LR-060 (→ LR-051) |
| `sanctions.freeze_deadline` | Freeze "immediately", meaning "without delay but not later than **24 hours**" | FIs and DNFBPs | S.I. 76 of 2014 (R2-16a) | HIGH | LR-060 (→ LR-051), LR-066 |
| `kyb.bo_threshold.*` | COBE more than 20%; MLPC 25%; PVO 25% of votes | See [kyb-architecture.md §3.3](kyb-architecture.md) | R2-05, R2-16, R2-17 | HIGH / MEDIUM-HIGH | LR-067 (→ LR-054) |
| `pvo.foreign_high_risk_min` (owner-side) | Single or cumulative annual receipts **above USD 5,000** from a high-risk jurisdiction trigger high-risk monitoring and a foreign funding disclosure to the FIU within 30 days | PVOs (campaign owners) | S.I. 98 of 2026 s 10 (R2-38) | HIGH | LR-069 (→ LR-049) (data sharing) |
| `pvo.single_foreign_donation_edd_min` (owner-side) | Single foreign donation **above USD 10,000**; aggregate from one high-risk jurisdiction **above USD 25,000 a year** | Medium- and high-risk PVOs | S.I. 98 of 2026, Second Schedule (R2-38) | HIGH | LR-069 (→ LR-049) |
| `pvo.beneficiary_record_min` (owner-side) | Beneficiary identity records for anyone receiving **more than USD 500** in one transaction or **USD 2,000 a year** | PVOs | S.I. 98 of 2026, Second Schedule (R2-38) | HIGH | LR-069 (→ LR-049) |
| `pvo.kyd_kyb_txn_min` (owner-side) | Mandatory KYB/KYD for transactions **above USD 50,000** | High-risk PVOs | S.I. 98 of 2026 s 7 (R2-38) | HIGH | LR-069 (→ LR-049) |
| `pvo.s8_max_days` | Temporary authority up to **90 days**, extendable once by up to **90 days** | Persons collecting contributions | PVO Act s 8 (R2-34) | HIGH (text) / MEDIUM (Act 1/2025 validity) | LR-068 (→ LR-046–LR-048) |

Notes:

- **Owner-side** figures are not FundZim obligations as stated. FundZim uses them to give PVO owners
  aggregates and alerts so that the owners can meet their own duties ([kyb-architecture.md §6](kyb-architecture.md)).
- The SI 98 internal inconsistency on red-flag reporting (3 working days in s 29(7) vs "within 72 hours" in
  the Second Schedule, R2-38) is noted for counsel. Configuration would use the **stricter** value if it
  applied.
- Penalty context, for risk appetite only:
  - FIU civil penalties "not to exceed … US$250 000" per infringement, with personal liability possible
    (FIU Directive PFIU21/10/2024; R2-12; HIGH for pages read).
  - MLPC Act offences up to USD 100,000 and/or 3 years' imprisonment (R2-03, R2-06).

### 5.2 PROVIDER and INTERNAL_RISK limits (placeholders)

| `limit_key` (examples) | Type | Value | Owner |
|---|---|---|---|
| `donation.max_per_txn.{rail}` | PROVIDER | `<TBD: PCR — provider max per rail/currency>` | FINANCE |
| `payout.max_per_txn.{rail}` | PROVIDER | `<TBD: PCR>` | FINANCE |
| `donor.unverified.max_per_donation` / `.max_per_day` | INTERNAL_RISK | `<TBD: PD-26>` | COMPLIANCE |
| `donor.identity_required_min` | INTERNAL_RISK (REGULATORY if LR-060 (→ LR-051)/061 confirm) | `<TBD: PD-26 / LR-061>` | COMPLIANCE |
| `payout.auto_approve_max`, `payout.dual_approval_min` | INTERNAL_RISK | `<TBD: LR-030 / PD-24>` | FINANCE + COMPLIANCE |
| `payout.destination_cooling_off_seconds` | INTERNAL_RISK | `<TBD>` | COMPLIANCE |
| `screening.freshness_seconds` | INTERNAL_RISK | `<TBD: PD-29>` | COMPLIANCE |
| `risk.rating.high_min` | INTERNAL_RISK | `<TBD>` | COMPLIANCE |

## 6. Customer due diligence tiers (mapping)

| Tier | Applies when | Measures |
|---|---|---|
| **Simplified** (donors at low risk) | Donation ≤ unverified limits; LOW risk; not to a PVO campaign requiring donor identity | Payment instrument data from the PSP; device/IP risk; no ID |
| **Standard** | Owners and representatives (IDENTITY level); payees (PAYOUT level); donors above `donor.identity_required_min` | [kyc-architecture.md](kyc-architecture.md) levels; screening; purpose of relationship |
| **Enhanced (EDD)** | HIGH risk rating; PEP/RCA; high-risk jurisdiction exposure; complex ownership; large or unusual campaigns; category HIGH with weak evidence | Source-of-funds or source-of-need evidence; COMPLIANCE approval; `DUAL` payout tier; shorter rescreen interval; enhanced monitoring rules; periodic review |

If CDD cannot be completed, the relationship does not proceed. Under the MLPC Act s 22, institutions in
scope must not open or maintain a relationship where CDD cannot be completed, and must report to the FIU
(R2-03, HIGH). FundZim's design:

- blocks the gated capability;
- opens a case;
- leaves the STR decision to COMPLIANCE ([compliance-case-management.md §6](compliance-case-management.md)).

## 7. Manual review workflow (summary)

Alerts from screening, monitoring, KYC review and fraud reports become cases
([compliance-case-management.md](compliance-case-management.md)). The minimum flow:

1. **Triage.** COMPLIANCE or a delegated KYC_REVIEWER sets case type, severity and subject links. The SLA
   clock starts.
2. **Protective holds.** Where funds are at risk, COMPLIANCE places a hold or freeze. Holds are new
   records: `payout_holds`, or a campaign `FROZEN` transition with a ledger move from payable to held. They
   never edit or delete financial history ([payout-eligibility-and-controls.md §5](../payments/payout-eligibility-and-controls.md),
   [LEDGER.md §6.8](../LEDGER.md)).
3. **Investigate.** Gather evidence (evidence_records) and contact the user without tipping off (LR-072).
4. **Decide.** Clear, or apply restrictions (lower limits, EDD conditions). Or suspend, reject, freeze,
   refund (via the refund architecture), or offboard. Decisions with financial impact are maker-checker.
5. **Report.** Internal STR consideration. External filing only if LR-060 (→ LR-051) (or another duty) applies.
6. **Close.** Record the rationale. QA sample. Feed rule tuning ([transaction-monitoring.md §5](transaction-monitoring.md)).

## 8. Effectiveness metrics

Metrics are reported monthly to the COMPLIANCE lead and founders:

- alert volumes and true-positive rate per rule;
- time to triage and time to decision against SLA (PD-28);
- share of payouts in each approval tier;
- payouts held and average hold age;
- fraud losses (refund_losses, chargeback_losses per [LEDGER.md](../LEDGER.md) and the Stage 1 ledger
  model);
- confirmed fraudulent campaigns before vs after the first payout;
- screening potential matches and false-positive rate;
- limits past `review_by`.

## 9. New legal questions and decisions

Register: [open-legal-questions.md](open-legal-questions.md).

| ID | Question / decision | Blocks |
|---|---|---|
| **LR-071** | Automated decision-making (CDPA s 25): which risk-engine actions are "solely automated decisions" with significant effects? Examples: declining a donation, holding a payout, routing a campaign to review. What human-review and notice safeguards are required? | Automation scope in Stage 11/13 |
| **LR-073** (→ LR-052) | FundZim handles no cash. If it is a non-bank financial institution (LR-060 (→ LR-051)), does FIU Directive 01/04/2024 still require monthly (nil) CTR returns via goAML? Do EFT or IFT thresholds apply to a platform that does not itself make transfers? | Regulatory reporting design (Stage 17) |
| **PD-26** | Donor verification policy: unverified donor caps per currency, the threshold for requiring donor identity, and whether anonymous donations are allowed to PVO campaigns (see LR-026, LR-069 (→ LR-049)). Approver: COMPLIANCE lead + founders. | Donation flow (Stage 7), limits |
| **PD-28** | Appointment of a named compliance officer (MLRO-equivalent) and DPO, case SLAs and staffing for launch. Approver: founders. | Case management, readiness |

## 10. Sources cited (accessed 2026-10-08; research file R2)

| Ref | Source | URL | Confidence |
|---|---|---|---|
| R2-01–R2-10 | MLPC Act [Chapter 9:24], FIU-hosted consolidation | `https://www.fiu.co.zw/wp-content/uploads/2025/05/MLPCAct0924updated.pdf` | HIGH |
| R2-12 | FIU AML/CFT Directive 01/04/2024 (revised CTR/EFT/IFT thresholds) | `https://www.fiu.co.zw/wp-content/uploads/2025/05/DIRECTIVE-01-04-2024-REVISED-THRESHOLDS-FOR-CTRs-EFTs-AND-IFTs.pdf` | HIGH |
| R2-12 | FIU AML/CFT/CPF Directive PFIU21/10/2024 (civil penalties) | `https://www.fiu.co.zw/wp-content/uploads/2025/05/AML_CFT_CPF-DIRECTIVE-NO.-PFIU21102024.pdf` | HIGH (pages 1–3) |
| R2-13 | The Herald, 9 Mar 2022 (FATF grey list exit), secondary | `https://www.heraldonline.co.zw/fatf-grey-list-exit-huge-step-for-zim-2/` | MEDIUM |
| R2-13 | Securities Commission of The Bahamas reproduction of FATF "Jurisdictions under Increased Monitoring – 19 June 2026" | `https://scb.gov.bs/wp-content/uploads/2026/06/Financial-Action-Task-Force-Public-Statement-on-list-of-Jurisdictions-under-Increased-Monitoring-June-2026.pdf` | HIGH-MEDIUM |
| R2-14 | ESAAMLG 10th Enhanced Follow-Up Report, April 2024 | `https://www.esaamlg.org/reports/Zimbabwe_FUR-April%202024.pdf` | HIGH |
| R2-15 | S.I. 99 of 2026 (VASP Registration Regulations) | `https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-99of2026MoneyLaundering.pdf` | HIGH (existence) |
| R2-16a | S.I. 76 of 2014 (UNSCR 1267/1373 implementation) | `https://www.fiu.co.zw/wp-content/uploads/2025/05/S.I.-76-of-2014-Suppression-of-Foreign-International-Terrorism.pdf` | HIGH |
| R2-23 | CDPA s 25 (Veritas) | `https://www.veritaszim.net/sites/veritas_d/files/Cyber%20%26%20Data%20Protection%20Act%20Cap1207%20No%205%20of%202021%20gaz%202022-03-11.pdf` | HIGH |
| R2-34, R2-35 | PVO Act (Veritas consolidation to 11 Apr 2025) | `https://www.veritaszim.net/sites/veritas_d/files/Private%20Voluntary%20Organisations%20Act%20Cap%2017,05%20(Consolidated%20to%202025.04.11).pdf` | HIGH / MEDIUM (validity) |
| R2-38 | S.I. 98 of 2026 | `https://www.fiu.co.zw/wp-content/uploads/2025/05/SI-98of2026-PVO-Risk-Based-Supervision.pdf` | HIGH |
