# ADR-017: Payout approval and segregation of duties

- **Status:** Accepted
- **Date:** 2026-10-08
- **Deciders:** Technical lead; business owner (approval thresholds, LR-030)
- **Stage:** 1 (design). Implemented in Stages 4 (roles), 11 (payouts) and 14 (admin portal).

## Context

Stage 0 established least privilege, maker-checker for sensitive actions and staff MFA
([SECURITY.md](../SECURITY.md) §5). Stage 1 must turn this into an operational model: which functions exist,
which combinations are incompatible, which actions need two people, and how compliance can stop money
without rewriting history. Insider abuse and account takeover of staff are top threats for payouts and ledger
adjustments ([THREAT-MODEL.md](../THREAT-MODEL.md)). Two functions in the Stage 1 brief had no Stage 0 role:
KYC reviewer and security administrator.

## Decision

1. **Functions are permission bundles mapped to roles.** Stage 0 roles stay. Two roles are added:
   - **`KYC_REVIEWER`** — reviews KYC/KYB/beneficiary cases and records decisions; may view KYC documents only
     with a case-bound justification; no payouts, refunds, ledger or campaign decisions.
   - **`SECURITY_ADMIN`** — access reviews, session revocation, staff suspension, security kill switches,
     secret-rotation triggers; **no** access to KYC data or financial data, and cannot approve any money
     movement.

   The full mapping is in [operational-controls.md](../compliance/operational-controls.md) §1.
2. **Every payout passes automated eligibility** ([payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md)).
   Approval tier is `AUTO`, `SINGLE` or `DUAL`, chosen by configured thresholds and risk flags (LR-030).
   **Pilot default: no `AUTO` approval** (PD-24).
3. **Maker-checker** (approver ≠ requester/initiator, and for DUAL two distinct approvers) applies to: payouts
   above threshold or risk-flagged; all refunds; ledger adjustments and reversals; write-offs; staff changes to
   payout destinations; limit and threshold changes; fee configuration; compliance overrides and exemptions
   (mandatory expiry; never for sanctions blocks); campaign unfreeze; review policy changes; role grants.
   Approvals require step-up MFA, expire, and are never auto-approved on expiry.
4. **Separation of duties** is enforced twice: role-assignment rejects incompatible role combinations, and
   action-time checks reject object-level conflicts (own request, own campaign, declared relationship, donor
   above a configured amount, recent destination change by the same person).
5. **Holds preserve history.** Compliance stops money by creating hold records and posting ledger moves
   (e.g. `campaign_payable → campaign_held`), never by editing or deleting payments, payouts or postings.
   A hold can stop a payout up to `APPROVED`; after `SUBMITTED` only a provider-supported cancel applies.
6. A staff identity is bound to one verified person; a person cannot complete both steps of a maker-checker
   action through two accounts. Staff accounts are separate from personal FundZim accounts.

## Consequences

### Positive
- No single insider can move money out, change where it goes, or alter the ledger alone.
- Security administration cannot be used as a path to money or KYC data.
- Compliance has strong, reversible tools that leave a full audit trail.

### Negative / costs
- Requires at least two FINANCE staff (and two COMPLIANCE, two SUPER_ADMIN) to operate — a staffing
  constraint for a small team (PD).
- Slower payouts while `AUTO` is disabled for the pilot.

### Follow-up work
- Business owner: approval thresholds and tiers (LR-030), staffing (PD), background vetting (LR-089).
- Stage 4: role and permission model; Stage 14: approval queues and evidence views.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| ADMIN/SUPER_ADMIN can do everything | Violates least privilege; one compromised account moves money. |
| Maker-checker for every payout regardless of size | Operationally heavy long-term; tiers allow it for the pilot via configuration instead. |
| Fold KYC review into COMPLIANCE only | Concentrates KYC data access and investigative power; separate role narrows access. |

## Security implications
Step-up MFA on every approval; approval objects carry hashes of what was approved so a changed request
invalidates approval; all decisions are audit events with evidence links. Break-glass is time-boxed,
justified, alerted and reviewed.

## Financial implications
Ledger adjustments are always new balanced transactions created by one FINANCE user and approved by another.
Holds are ledger moves, so a frozen amount is structurally unavailable.

## Related
[operational-controls.md](../compliance/operational-controls.md),
[payout-eligibility-and-controls.md](../payments/payout-eligibility-and-controls.md),
[payout-lifecycle.md](../payments/payout-lifecycle.md), [SECURITY.md](../SECURITY.md) §5, ADR-015, ADR-016.
LEGAL_REVIEW_REQUIRED: LR-030, LR-089.
