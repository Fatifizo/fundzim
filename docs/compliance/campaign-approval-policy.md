# FundZim Campaign Approval Policy (Stage 1 design)

Status: **Stage 1 design — provisional.** It defines how a campaign is reviewed before publication and
re-reviewed afterwards. The review engine is built in Stage 6 (Campaign Engine), with risk inputs added in
Stage 13 and the staff tools in Stage 14 ([ROADMAP.md](../ROADMAP.md)).

Related: [PRODUCT.md](../PRODUCT.md) §5–§7, [kyc-architecture.md](kyc-architecture.md),
[kyb-architecture.md](kyb-architecture.md), [beneficiary-verification.md](beneficiary-verification.md),
[aml-risk-framework.md](aml-risk-framework.md), [sanctions-screening.md](sanctions-screening.md),
[compliance-case-management.md](compliance-case-management.md),
[operational-controls.md](operational-controls.md), [audit-evidence-model.md](audit-evidence-model.md),
[donor-protection-policy.md](donor-protection-policy.md),
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md).

---

## 1. Core rule: verified identity ≠ approval

A campaign owner's `IDENTITY_VERIFIED` status is a **precondition** for submission, never a reason to approve.
Identity verification answers "is this person who they say they are?". Campaign review answers different
questions:

- is the cause **permitted** (LR-025)?
- is the **story plausible** and is the beneficiary real?
- is the owner **entitled** to raise funds for this beneficiary (relationship and consent — LR-015)?
- can the funds be **paid out lawfully** to the intended payee (LR-024)?
- are there **fraud or AML** indicators?

No configuration may set a category's policy to "auto-approve on verified identity". The policy validator
rejects any policy in which every check is `AUTOMATIC` with no human review for an `ELEVATED` or `HIGH` tier.
A `STANDARD`-tier policy may allow **automated pre-checks plus sampled human review** only after `PD-31` is
decided and only for owners with a clean history. In MVP, every campaign gets a human review.

---

## 2. Review pipeline

```mermaid
sequenceDiagram
    autonumber
    participant O as Campaign owner
    participant API as FundZim API (campaigns)
    participant CHK as Automated checks
    participant Q as Review queue
    participant R as Reviewer
    participant C as Compliance
    O->>API: Submit campaign (DRAFT → SUBMITTED)
    API->>API: Gate: owner IDENTITY_VERIFIED, status ACTIVE; required fields; policy version pinned
    API->>CHK: Run automated checks (sanctions, duplicates, image reuse, prohibited terms, risk score)
    CHK-->>API: Check results (pass / flag / block) as review_check rows
    API->>Q: Enqueue with risk tier + flags + SLA (PD-31)
    R->>Q: Claim (SUBMITTED → UNDER_REVIEW); conflict-of-interest check
    R->>R: Work the checklist for the category and tier
    alt All required checks pass
        R->>API: Approve (→ APPROVED), reason code
    else Missing or unclear evidence
        R->>API: Request changes (→ DRAFT), reasons to owner
    else Prohibited cause or clear fraud
        R->>API: Reject (→ REJECTED), reason code
    else AML/sanctions/fraud concern or HIGH tier sign-off
        R->>C: Escalate (stays UNDER_REVIEW, case opened)
        C->>API: Approve / reject / request changes (with justification)
    end
    API->>O: Notify outcome
```

The decision is recorded as a `campaign_review_decision` row and an audit event (`campaign.state.changed`,
justification required), with references to the evidence used ([audit-evidence-model.md](audit-evidence-model.md)).

---

## 3. Check catalogue

Checks are configuration data. Each check has a code, a type, the failure effect, and the evidence it
consumes. A policy (§5) selects which checks apply to a category and tier.

| Code | Check | Type | Evidence / input | Failure effect |
|---|---|---|---|---|
| `OWNER_IDENTITY` | Owner is `IDENTITY_VERIFIED` and status `ACTIVE` (not `SUSPENDED`/`REJECTED`) | AUTOMATIC (gate) | KYC level ([kyc-architecture.md](kyc-architecture.md)) | BLOCK submission |
| `OWNER_HISTORY` | Prior rejected, frozen or cancelled campaigns; open cases; chargeback history | AUTOMATIC → flag | Case and campaign history | FLAG → reviewer must acknowledge |
| `CATEGORY_PERMITTED` | Category is enabled and the stated purpose fits it | MANUAL | Story, category | REQUEST_CHANGES (recategorise) or REJECT |
| `PROHIBITED_ACTIVITY` | Purpose is not on the prohibited list (investment, lending, political financing, illegal activity, sanctioned parties, hate/violence, policy exclusions) — LR-025 | AUTOMATIC keyword pre-screen + MANUAL | Story, title, images | REJECT; ESCALATE if possible criminal purpose |
| `BENEFICIARY_DECLARED` | Beneficiary type and identity declared ([beneficiary-verification.md](beneficiary-verification.md)) | AUTOMATIC (completeness) | Beneficiary record | BLOCK submission |
| `BENEFICIARY_RELATIONSHIP` | Owner's relationship to the beneficiary and authority to raise funds (consent, guardianship for minors, institution confirmation) — LR-015 | MANUAL | Relationship declaration, consent evidence | REQUEST_CHANGES or REJECT |
| `SUPPORTING_DOCS` | Category-specific supporting documents present and plausible (§4) | MANUAL | Uploaded documents (C3 where they contain health/ID data) | REQUEST_CHANGES |
| `DOC_AUTHENTICITY` | Documents not obviously forged; institution letterhead and contacts verifiable; spot-check by calling the institution for `HIGH` tier | MANUAL | Documents, independent contact | ESCALATE |
| `IMAGE_REUSE` | Images not found in other campaigns (perceptual hash) or flagged sources | AUTOMATIC → flag | Media hashes | FLAG |
| `DUPLICATE_CAMPAIGN` | No near-duplicate active campaign for the same beneficiary or story | AUTOMATIC → flag | Text similarity, beneficiary identifiers (blind index) | FLAG; REJECT if duplicative without reason |
| `SANCTIONS_SCREEN` | Owner, beneficiary, organisation and its directors/beneficial owners screened ([sanctions-screening.md](sanctions-screening.md)) — LR-009 | AUTOMATIC | Screening result | Potential match → ESCALATE (BLOCK until cleared) |
| `RISK_SCORE` | Rule-based risk score within tier limit ([aml-risk-framework.md](aml-risk-framework.md)) | AUTOMATIC | Signals (account age, device, velocity, category) | Above limit → ESCALATE |
| `KYB_COMPLETE` | Organisation-run campaigns: organisation at required KYB level, and the submitter is an authorised representative ([kyb-architecture.md](kyb-architecture.md)) — LR-013 | AUTOMATIC (gate) + MANUAL | KYB record | BLOCK submission |
| `FUNDRAISING_AUTHORITY` | The campaign has a valid `fundraising_authority` record ([kyb-architecture.md](kyb-architecture.md)): self-fundraising, registered PVO (registration number), excluded body (basis) or s8 temporary authority (number and validity window), with evidence. The campaign end date does not exceed the authority's validity. Campaigns where an individual raises **for someone else** are blocked while the policy flag `campaign.individual_for_others.enabled` is `false` (default until LR-046 – LR-048 and PD-27 are decided; LR-068 is a duplicate). PVO Act basis unconfirmed — **LEGAL_REVIEW_REQUIRED** (LR-046, LR-047, LR-048, LR-050) | AUTOMATIC (gate) + MANUAL (evidence) | Fundraising-authority record, beneficiary type | BLOCK submission (missing, expired or flag-disabled); REQUEST_CHANGES (evidence unclear) |
| `PAYMENT_ELIGIBILITY` | Goal currency enabled; at least one rail available for the campaign's currency; intended payee type supported (owner, beneficiary or institution — LR-024) | AUTOMATIC | Provider capability config ([provider-capability-matrix.md](../payments/provider-capability-matrix.md)) | BLOCK (cannot publish a campaign that cannot receive or pay out) |
| `PRIVACY_REVIEW` | Story does not expose unnecessary health detail, ID numbers, children's identifying details or third-party personal data — LR-014, LR-015 | MANUAL | Story, images | REQUEST_CHANGES (redact) |
| `GOAL_PLAUSIBLE` | Goal amount is proportionate to the stated need (e.g. matches the quotation) | MANUAL | Story, documents | REQUEST_CHANGES |
| `CONTACTABILITY` | Owner reachable via verified phone; `HIGH` tier: reviewer call | MANUAL | Call log (evidence record) | REQUEST_CHANGES |

Check results are stored per review (`review_check_result`: check code, result `PASS | FLAG | FAIL | N/A`,
performed_by system/staff, evidence ids, notes). A reviewer cannot approve while any applicable check is
`FAIL` or an unacknowledged `FLAG`.

---

## 4. Risk tiers and category requirements

Tiers set the review depth, the required checks, escalation, and post-publication payout controls.
**Tier assignment is configuration**, decided by COMPLIANCE and approved by the business owner. The table is
the provisional starting point.

| Category | Default tier | Supporting documents (indicative) | Extra checks / notes |
|---|---|---|---|
| Medical | HIGH | Medical letter, quotation or invoice from the treating institution (C3; minimal health detail) | `DOC_AUTHENTICITY` with institution contact; `PRIVACY_REVIEW`; payout to the institution preferred where possible (LR-024) |
| Funeral | ELEVATED (fast-track) | Death notification or burial order, funeral parlour quotation | Fast-track SLA (`PD-31`) **with** a lower initial payout cap or payout to the funeral parlour; deceased beneficiary rules (LR-015) |
| Emergency | ELEVATED (fast-track) | Evidence of the event (report, photos) where available | Fast-track, payout caps until evidence is complete |
| Education | STANDARD | Fee statement or invoice from the school | Payout to the school preferred (LR-024); minors' data minimised (LR-015) |
| Community | ELEVATED | Project description, community leadership letter, quotation | Group authority (who may receive funds); KYB if run by an organisation |
| Charity | ELEVATED | Organisation registration (KYB) | `KYB_COMPLETE` and `FUNDRAISING_AUTHORITY` required; PVO/charity registration questions (LR-013, LR-046 – LR-050) |
| Religious / community organisation | ELEVATED | Organisation registration or constitution, authorised representative | `KYB_COMPLETE`; LR-013 |
| Sports | STANDARD | Team or club confirmation, travel or tournament invite | — |
| Personal causes | HIGH | Depends on purpose | Broad category = higher scrutiny; reviewer must justify approval |
| Other approved causes | HIGH | Reviewer-defined | Reviewer must classify; COMPLIANCE sign-off |

Tier effects:

| Tier | Human review | Second review / escalation | Post-publication controls |
|---|---|---|---|
| STANDARD | One reviewer | On flags only | Standard payout eligibility |
| ELEVATED | One reviewer, full checklist | COMPLIANCE on any flag | Payout caps until documents verified; reserve per `PD-33` |
| HIGH | One reviewer + COMPLIANCE sign-off (four eyes) | Always | First payout `PENDING_REVIEW` (manual); reserve per `PD-33`; tighter monitoring thresholds |

Any campaign can be upgraded (never silently downgraded) by risk signals; downgrades need COMPLIANCE approval
and are audited.

---

## 5. Policy configuration data model (conceptual; built in Stage 6)

```
campaign_review_policy                 -- versioned; immutable once published
  id, version, category_code, risk_tier,
  checks            : [{check_code, required: bool, mode: AUTOMATIC|MANUAL, failure_effect}],
  required_documents: [{doc_type, required: bool, classification: C2|C3}],
  escalation_rules  : [{condition, escalate_to_role}],
  sla_ref           : PD-31 config key,
  payout_controls   : {first_payout_manual: bool, payout_cap_ref: limit id, reserve_ref: PD-33 config key},
  effective_from, effective_to, approved_by (maker-checker), approval_reason

campaign_review
  id, campaign_id, campaign_version_id, policy_id+version (pinned at submission),
  risk_tier_at_submission, claimed_by, claimed_at, decided_by, decided_at,
  outcome APPROVE|REQUEST_CHANGES|REJECT|ESCALATE, reason_code, justification

review_check_result
  review_id, check_code, result PASS|FLAG|FAIL|N_A, performed_by (system|staff id),
  evidence_record_ids[], acknowledged_by, notes (no C3 content)
```

Rules:

- A campaign is reviewed against the policy version **pinned at submission**; policy changes do not
  retroactively change a decision, but a policy change may trigger a re-review campaign by campaign
  (bulk re-review is a COMPLIANCE action).
- Policy changes are maker-checker (`policy.change.requested` / `policy.change.approved`) — see
  [operational-controls.md](operational-controls.md).
- Limits (payout caps, reserves) follow the limits model: type, source, approval owner, effective dates,
  review date. Unconfigured payout limits fail closed to manual review.

---

## 6. Review outcomes

| Outcome | Campaign transition | Required | Owner sees |
|---|---|---|---|
| APPROVE | `UNDER_REVIEW → APPROVED` | All applicable checks PASS or acknowledged FLAG; tier sign-offs complete | Approval; publish option (`PD-03`) |
| REQUEST_CHANGES | `UNDER_REVIEW → DRAFT` | Reason codes + specific instructions | What to fix; no internal risk signals revealed |
| REJECT | `UNDER_REVIEW → REJECTED` | Reason code from a controlled list; justification | Generic reason category; no detail that would tip off fraud or AML suspicion (LR-008) |
| ESCALATE | stays `UNDER_REVIEW`; compliance case opened | Escalation reason | "Your campaign needs additional review" |

Resubmissions after rejection are limited (configurable, [PRODUCT.md](../PRODUCT.md) §6.2). A third rejection
of the same owner within a period raises an account-level review.

SLAs per tier and category: `PD-31` (placeholder). The queue shows SLA breach risk; a breached SLA never
causes automatic approval.

---

## 7. After publication: re-review, suspension and freezing

### 7.1 Material edits (re-review triggers)

| Edit | Effect |
|---|---|
| Beneficiary changed (identity, type) | Re-review required; payouts held until beneficiary re-verified |
| Payout destination changed | Payout hold plus cooling-off ([payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)); destination re-verification |
| Goal increased by more than a configured ratio, or goal currency change | Re-review (goal currency change is not allowed after donations — ADR-010) |
| Category changed | Re-review against the new category's policy |
| Story substantially changed (similarity below threshold) | Re-review flag; the campaign stays `ACTIVE` unless a reviewer suspends it |
| New supporting documents | Reviewer verification |
| Organisation representative changed | KYB re-check |

Edits are versioned (`campaign_version_id`); public pages show the current approved version only. Edits
pending re-review are held as a pending version where the field allows.

### 7.2 Suspension and freezing criteria

| Action | Who | When | Effect |
|---|---|---|---|
| **SUSPEND** | REVIEWER or COMPLIANCE | Credible complaint, material edit pending, policy breach, missing documents | Donations paused, payouts paused; reversible |
| **FREEZE** | COMPLIANCE or FINANCE | Credible fraud, sanctions potential match, AML suspicion, chargeback cluster, legal/regulator request | Donations stopped; all payouts frozen; available balance moved to `campaign_held` ([LEDGER.md](../LEDGER.md) §6.8); payouts not yet `SUBMITTED` pulled back |
| **UNFREEZE** | COMPLIANCE + second approver | Investigation cleared | Inverse ledger move; payouts resume after eligibility re-check |

Every action needs a reason code and justification, and produces an audit event and evidence record.

---

## 8. Reviewer checklists

### 8.1 All campaigns

- [ ] Automated checks complete; every FLAG read and acknowledged with a note
- [ ] Category correct; purpose not prohibited (LR-025)
- [ ] Story is coherent; dates, places and amounts consistent with the documents
- [ ] Beneficiary declared; relationship and authority plausible; consent evidence where required
- [ ] Goal proportionate to documented need
- [ ] No unnecessary personal, health, ID or children's data in public content (redaction requested if needed)
- [ ] Images not reused or stock; no identifying images of minors without guardian consent (LR-015)
- [ ] Intended payee type supported and verifiable
- [ ] No conflict of interest (reviewer does not know the owner or beneficiary)

### 8.2 Medical (HIGH)

- [ ] Institution letter/quotation names the beneficiary and the treatment cost; institution contact verified independently
- [ ] Cost matches goal (allowing stated extras)
- [ ] Health detail in public story minimised (diagnosis category, not records)
- [ ] Payout to the institution considered; if to an individual, reason recorded
- [ ] COMPLIANCE sign-off recorded

### 8.3 Funeral / emergency (fast-track)

- [ ] Evidence of death or event (or a documented reason it is pending)
- [ ] Initial payout cap applied, or payee is the funeral parlour or service provider
- [ ] Follow-up evidence task scheduled before further payouts

### 8.4 Organisation-run (charity, religious, community)

- [ ] Organisation at required KYB level; submitter is an authorised representative
- [ ] Payout account is in the organisation's name
- [ ] Directors/trustees and beneficial owners screened
- [ ] Registration questions under LR-013 noted where unresolved (campaign may be blocked by policy)

---

## 9. Open items

- `PD-31` — review SLAs per tier (owned by [donor-protection-policy.md](donor-protection-policy.md) §12).
- `PD-33` — reserve/holdback configuration.
- LR-013, LR-014, LR-015, LR-024, LR-025 — legal inputs to the check catalogue.
- Tier assignment table approval: COMPLIANCE proposes, business owner approves, before Stage 6 acceptance.
