# ADR-015: Risk-based identity verification (levels, status overlay, KYB, limits model)

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead; compliance officer and counsel own thresholds and CDD content (LR-007)
- **Stage:** 1 (design). Implemented in Stage 4 (basic) and Stage 5 (KYC/KYB).

## Context

Stage 0 defined four individual verification levels in [PRODUCT.md](../PRODUCT.md) §7 and gates (donate,
draft, publish, withdraw). Stage 1 needs:

- statuses that can **block** a verified person (rejection, suspension) without erasing what was verified;
- an organisation (KYB) model with authorised representatives, directors/trustees and beneficial owners;
- a way to express thresholds without inventing legal numbers.

Research ([regulatory-landscape.md](../compliance/regulatory-landscape.md)) found that, **if** FundZim were a
"financial institution" under the Money Laundering and Proceeds of Crime Act, customer due diligence would be
triggered by business relationships, occasional transactions and wire transfers above stated amounts, remote
onboarding would need to be "no less effective" than face-to-face, and PEPs would need enhanced measures.
Whether FundZim is within scope is **LEGAL_REVIEW_REQUIRED** (LR-007, LR-008). Beneficial-ownership
thresholds differ between instruments (companies vs AML vs PVO law), so the threshold must be configurable.
No KYC vendor has been chosen.

## Decision

1. **Individuals have two independent fields:**
   - `verification_level ∈ {UNVERIFIED, BASIC_VERIFIED, IDENTITY_VERIFIED, PAYOUT_VERIFIED}` — the highest
     level achieved, monotonic except by explicit downgrade with reason;
   - `verification_status ∈ {ACTIVE, PENDING_REVIEW, REJECTED, SUSPENDED}` — an overlay.
     `REJECTED` and `SUSPENDED` block every level-gated action while preserving the level and its history.
2. **Gates stay as in Stage 0** (donate = UNVERIFIED within risk limits; create draft = BASIC_VERIFIED;
   submit/publish = IDENTITY_VERIFIED; withdraw = PAYOUT_VERIFIED) and are evaluated as
   `level ≥ required AND status = ACTIVE`.
3. **Organisations (KYB)** have `org_verification_level ∈ {ORG_UNVERIFIED, ORG_REGISTERED_VERIFIED,
   ORG_KYB_VERIFIED, ORG_PAYOUT_VERIFIED}` with the same status overlay. Every authorised representative must
   be an `IDENTITY_VERIFIED` individual. Directors/trustees and beneficial owners above the configured
   threshold are identified and screened.
4. **Verification is risk-based.** Risk inputs (category, amount raised, velocity, foreign funding, PEP or
   sanctions hits, fraud signals) can require a higher level or enhanced review earlier than the default gate.
   Verified identity never auto-approves a campaign ([campaign-approval-policy.md](../compliance/campaign-approval-policy.md)).
5. **Every threshold is a limit record**, never a constant in code or docs:
   `limit_type ∈ {REGULATORY, PROVIDER, INTERNAL_RISK}`, `source`, `approval_owner`, `effective_from`,
   `effective_to`, `review_by`, `currency`, `value_minor` (integer), `scope`. Documentation uses placeholders
   (`<TBD: LR-0xx>`). An unconfigured limit on a payout or withdrawal path **fails closed** to manual review.
6. **Re-verification triggers** (document expiry, name or payout-destination change, sanctions-list update,
   risk escalation, dormant account reactivation) move status to `PENDING_REVIEW` without lowering the level.
7. KYC data stays inside the `kyc` module boundary ([ADR-009](ADR-009-kyc-storage-separation.md),
   [identity-data-protection.md](../security/identity-data-protection.md)).

## Consequences

### Positive
- Suspension or rejection is reversible and auditable; no "level 0 reset" that destroys evidence.
- Thresholds can change with legal advice or provider terms without code changes, and each carries its
  source and approver.
- KYB reuses the individual KYC pipeline for representatives.

### Negative / costs
- Two fields per subject add authorisation-check complexity; every gate must check both.
- Until counsel sets regulatory limits, many paths fail closed to manual review (operational load).

### Follow-up work
- Counsel: CDD content per level, PEP rules, thresholds (LR-007), FIU status (LR-008), age rules (LR-021).
- Stage 2: tables `kyc_profiles`, `org_verifications`, `limits`, `verification_events`.
- Stage 5: vendor selection and implementation.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Single enum including REJECTED/SUSPENDED as levels | Loses the achieved level on suspension; makes reinstatement lossy. |
| Hard-coded thresholds | Invents legal facts; cannot change with advice; no provenance. |
| Full KYC for every donor | Disproportionate friction; not justified by any verified requirement; risk-based limits apply instead. |

## Security implications
KYC artefacts are C3 RESTRICTED; access is justified, case-bound and audited. Level and status changes are
audit events; downgrades and reinstatements by staff require reason and, for reinstatement after
`SUSPENDED`, maker-checker ([ADR-017](ADR-017-payout-approval-segregation-of-duties.md)).

## Financial implications
Payouts require `PAYOUT_VERIFIED` + `ACTIVE`; limit records gate amounts in integer minor units per currency.
No ledger change.

## Related
[kyc-architecture.md](../compliance/kyc-architecture.md), [kyb-architecture.md](../compliance/kyb-architecture.md),
[aml-risk-framework.md](../compliance/aml-risk-framework.md),
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md) §4,
[PRODUCT.md](../PRODUCT.md) §7, ADR-009, ADR-016. LEGAL_REVIEW_REQUIRED: LR-007, LR-008, LR-009, LR-021.
