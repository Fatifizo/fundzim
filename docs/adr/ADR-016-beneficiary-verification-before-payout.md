# ADR-016: Beneficiary verification (and fundraising authority) before payout

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead; compliance officer; counsel for LR-013, LR-015, LR-024
- **Stage:** 1 (design). Implemented in Stages 5, 6 and 11.

## Context

The person running a campaign is often not the person who benefits: a parent raising money for a child, a
school for a student, an organiser for a hospital patient, a family for a funeral. Stage 0 modelled only the
campaign owner's KYC. Paying out to an owner who has no real link to the stated beneficiary is the core
crowdfunding fraud pattern ([THREAT-MODEL.md](../THREAT-MODEL.md)).

Stage 1 research also found that the Private Voluntary Organisations Act, as amended by Act 1 of 2025
(Veritas consolidation, accessed 2026-10-08; validity of the amendment is disputed by Veritas and no ruling
was found), provides that no person shall collect contributions from the public except in terms of the Act.
It makes it an offence to collect, or instruct another person to collect, contributions for charitable
objects other than on behalf of a registered PVO, for an excluded body, or under a s.8 temporary authority
from the Registrar (up to 90 days, extendable once). S.I. 97 of 2026 attaches conditions to such authorities.
Whether this captures individuals raising for themselves, individuals raising for others, or FundZim as a
platform is **LEGAL_REVIEW_REQUIRED** (LR-013) and is a **potential launch blocker** for "for others"
campaigns. See [regulatory-landscape.md](../compliance/regulatory-landscape.md).

## Decision

1. **Beneficiary is a first-class entity, separate from campaign owner.**
   `beneficiary_type ∈ {SELF, INDIVIDUAL_OTHER, MINOR, DECEASED_ESTATE_OR_FAMILY, INSTITUTION, ORGANISATION,
   COMMUNITY_GROUP}`;
   `beneficiary_verification ∈ {NOT_STARTED, DECLARED, EVIDENCE_SUBMITTED, VERIFIED, REJECTED}`.
2. **No payout before the beneficiary is VERIFIED.** Verification means the relationship and the authority
   to receive funds for the beneficiary are evidenced per [beneficiary-verification.md](../compliance/beneficiary-verification.md)
   (e.g. guardianship for a minor; invoice or institution confirmation for an institution). Exceptions require
   a time-boxed compliance override with maker-checker ([ADR-017](ADR-017-payout-approval-segregation-of-duties.md)).
3. **Payout destination types are explicit:** the owner's verified account (beneficiary SELF, or owner acting
   with evidenced authority), the beneficiary's own verified account, or a **verified institution payee**
   (hospital, school, funeral parlour). Paying a third party who is not the verified owner is **LR-024** and
   is disabled by configuration until counsel confirms.
4. **Fundraising authority is campaign evidence.** Each campaign records `fundraising_authority` as one of:
   - `SELF_FUNDRAISING` — the owner raises for their own needs (whether this needs no authority is
     **UNCONFIRMED**, LR-013);
   - `REGISTERED_PVO` — with registration number and evidence;
   - `EXCLUDED_BODY` — with the claimed basis and evidence;
   - `SECTION_8_AUTHORITY` — with authority number, validity window and conditions.

   Rules: the campaign end date cannot exceed the authority's validity; payout requires a valid authority
   where one is required; authority expiry suspends new donations and payouts and opens a review. Which
   campaign types require which authority is configuration, defaulting to **requiring evidence** for every
   charitable "for others" campaign until counsel advises (fail closed).
5. Beneficiary and authority evidence are evidence records ([ADR-019](ADR-019-regulatory-evidence-management.md));
   documents about minors and health are C3 and never public.

## Consequences

### Positive
- Directly mitigates impersonation, fake-beneficiary and payout-diversion fraud.
- Makes the PVO question explicit in the data model instead of hidden in policy text.
- Supports institution-direct payouts, which reduce misuse risk for medical and school campaigns.

### Negative / costs
- More friction for "for others" campaigns; some may be unlaunchable until LR-013 is answered.
- Reviewers need evidence standards per beneficiary type and authority type.
- Minors' data requires verified parental/guardian consent (Cyber and Data Protection Act regulations; LR-015).

### Follow-up work
- Counsel: LR-013 (PVO applicability, platform role), LR-015 (consent incl. minors and deceased), LR-024.
- Stage 2: `beneficiaries`, `campaign_beneficiaries`, `fundraising_authorities`, `payees` tables.
- Stage 6: campaign review checks ([campaign-approval-policy.md](../compliance/campaign-approval-policy.md)).
- Stage 11: eligibility check wiring ([payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)).

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Treat owner = beneficiary always | Misrepresents common real cases and hides fraud. |
| Verify beneficiaries only above an amount | Fraud is not amount-bound; small medical campaigns are common targets. Risk tiers can relax evidence, not skip verification. |
| Ignore PVO rules until launch | Would build a product that may be unlawful to operate for its main use case. |

## Security implications
Beneficiary and authority documents are C3 RESTRICTED in the private bucket; minors' identities are never
published. Destination or beneficiary changes trigger re-verification, payout holds and cooling-off.

## Financial implications
Adds blocking checks to payout eligibility; no change to ledger accounts. Institution payouts use the same
payout lifecycle with a different verified payee.

## Related
[beneficiary-verification.md](../compliance/beneficiary-verification.md),
[campaign-approval-policy.md](../compliance/campaign-approval-policy.md),
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md),
[data-protection-assessment.md](../compliance/data-protection-assessment.md), ADR-013, ADR-015, ADR-017,
ADR-019. LEGAL_REVIEW_REQUIRED: LR-013, LR-014, LR-015, LR-024, LR-025.
