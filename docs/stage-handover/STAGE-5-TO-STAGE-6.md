# Stage 5 → Stage 6 Handover: Campaign Engine

> Stage 5 (KYC, KYB, beneficiary and payout-destination verification, secure documents, review engine,
> compliance cases and the risk foundation) is implemented on branch `stage-5/verification` and **awaits
> acceptance**. Do not start Stage 6 until the owner accepts Stage 5 and asks for Stage 6. Stages 1–4 acceptance
> is also still unrecorded, and CI has never run (KI-S5-01).

Stage 5 docs: [implementation](../stage-5/implementation.md) · [KYC](../stage-5/kyc-workflows.md) ·
[KYB](../stage-5/kyb-workflows.md) · [beneficiaries](../stage-5/beneficiary-verification.md) ·
[payout destinations](../stage-5/payout-destination-verification.md) · [documents](../stage-5/document-security.md) ·
[compliance & risk](../stage-5/compliance-review.md) · [security review](../stage-5/security-review.md) ·
[testing](../stage-5/testing.md) · [known issues](../stage-5/known-issues.md) ·
[interface contracts](../stage-5/interface-contracts.md). ADRs: 034, 035.

## 1. What Stage 6 can use

| Need | Use | Notes |
|---|---|---|
| A user's verification level | `kyc.Service.Level(ctx, userID)` → level, status, risk level, expiry; `kyc.AtLeast(level, min)` | A level counts only with status `ACTIVE`. `app.users.kyc_level/kyc_status` is an eventually consistent mirror for display, **not** for gating |
| An organisation's verification | `kyc.Service.OrganisationLevel(ctx, orgID)` | `ORG_KYB_VERIFIED` is the only organisation level granted |
| Who the money is for | `beneficiaries.Service` (`Get`, `View`) and `Beneficiary.Status()` | Require an APPROVED beneficiary (and an approved representative authority for organisations) before a campaign can be submitted for a third party |
| Representative authority | `kyc.representative_authorities` (written on KYB approval; permissions include `org.campaign.create`, `org.campaign.submit`) | Add a kyc Go method to read it — do not query the table from another module |
| Where money would go | `payouts.Service` destinations | `eligible_for_payout` is always false until Stage 11 builds the eligibility engine |
| Evidence documents | `storage.Service` (upload/scan/open) with a new purpose and owner module | Campaign media is **public** media — never put it in PRIVATE_KYC |
| Audit / outbox / evidence | `audit.Record`, `outbox.Write`, `audit.RecordEvidence` (app pool) | Restricted pools must use the gateways |
| Risk signals | publish campaign events; add signal types and rules as a **new model version** (`risk-model-v2`), with limits as `risk.limits` rows | Never hard-code thresholds; never label a score as fraud |
| Compliance | `kyc.case_escalated`-style events → `compliance.open_case` | Add new trigger event types to the compliance consumer list |
| Policies | versioned verification policy (`kyc.ActivePolicy`) | New rules = new policy version via maker-checker |

## 2. Decisions Stage 6 must take first

1. **`create_draft` gate / BASIC_VERIFIED (KI-S5-17).** BASIC_VERIFIED is never granted because there is no
   age attestation and no device-risk input. Either add age-attestation capture (LR-021 open) and decide the
   device/IP-risk input, then grant BASIC_VERIFIED, **or** change the gate by ADR. Do not grant it from email
   and phone alone.
2. **PVO fundraising authority** records (`kyc.fundraising_authorities`, Stage 1 design) are not built
   (KI-S5-16). Campaigns "for others" by PVOs depend on them (LR-015, LR-046).
3. **Minors and medical campaigns:** publication rules are LEGAL_REVIEW_REQUIRED (LR-070, LR-058, PD-39).
   MINOR beneficiaries are verified with four eyes, but nothing yet decides what may be shown publicly.
4. **Screening:** no provider (KI-S5-04). Decide whether campaign submission needs screening, or proceeds on
   `APPROVE_WITH_CONDITIONS`.

## 3. Constraints carried forward

- Never treat a verified representative as a verified organisation, or a verified owner as a verified
  beneficiary.
- Never use format validation, the dev mock or a risk score as proof of anything.
- No support/admin access to KYC data; reviewers only when assigned, with step-up; every access audited.
- Gate on `kyc.Service.Level`, not the users mirror.
- Maker-checker for policy, limits and compliance overrides.
- No floating-point money. Campaign goals are `amount_minor` + currency.

## 4. Open items to schedule

KI-S5-02/03 (staff ↔ personal link and DB trigger), KI-S5-08 (real-stack admin E2E), KI-S5-10 (reveal
scoping), KI-S5-11/12/13 (small API consistency fixes), KI-S5-21 (enforce compliance resolutions on campaigns).
The OpenAPI Stage 2 design operations on shared paths were aligned in Stage 5; `info.version` still reads
`1.0.0-draft.stage4` and should be bumped when the contract is next released.
