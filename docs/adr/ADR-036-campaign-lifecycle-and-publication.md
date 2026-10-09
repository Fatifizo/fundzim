# ADR-036: Campaign lifecycle, versions and controlled publication

- **Status:** Accepted
- **Date:** 2026-10-09
- **Deciders:** Technical lead (Stage 6)
- **Stage:** 6

## Context

PRODUCT §6 and the Stage 2 campaign design (`docs/database/campaign-schema.md`, `design/sql/0014_campaigns.sql`)
define the lifecycle `DRAFT → SUBMITTED → UNDER_REVIEW → APPROVED → ACTIVE`, with `SUSPENDED`, `FROZEN`,
`COMPLETED`, `CANCELLED` and `REJECTED`. The Stage 6 brief asks for `PENDING_REVIEW`, `CHANGES_REQUESTED`,
`PUBLISHED`, `PAUSED` and `ARCHIVED`, and says to adapt to Stage 2 terminology. `FROZEN` moves funds
(payable → held) and needs the ledger and risk holds, which do not exist yet.

## Decision

1. **Vocabulary (machine `campaign`).** Stage 2 names are kept; the brief's extra states are added:

   | State | Brief name | Public | Notes |
   |---|---|---|---|
   | `DRAFT` | DRAFT | no | owner edits |
   | `SUBMITTED` | PENDING_REVIEW | no | in the review queue |
   | `UNDER_REVIEW` | UNDER_REVIEW | no | an assigned reviewer started the review |
   | `CHANGES_REQUESTED` | CHANGES_REQUESTED | no | **new**: replaces Stage 2's `UNDER_REVIEW → DRAFT`, so the request stays visible |
   | `APPROVED` | APPROVED | no (owner preview) | passed review, not yet published |
   | `ACTIVE` | PUBLISHED | yes | live; the API action is called *publish* |
   | `PAUSED` | PAUSED | yes, marked paused | **new**: owner pause, where policy allows |
   | `SUSPENDED` | SUSPENDED | no (notice only) | staff or compliance |
   | `REJECTED` | REJECTED | no | |
   | `COMPLETED` | COMPLETED | yes, read-only | |
   | `CANCELLED` | CANCELLED | no | |
   | `ARCHIVED` | ARCHIVED | no | **new**: terminal; history kept, nothing deleted |

   `FROZEN` is **reserved** for the ledger stages and has no edges in Stage 6.

2. **Edges** (`app.status_transitions`, guarded by `app.guard_transition('campaign')`, Go list identical and
   compared by a unit test): `'' → DRAFT`; `DRAFT → SUBMITTED | CANCELLED`; `SUBMITTED → UNDER_REVIEW | DRAFT`
   (withdraw); `UNDER_REVIEW → APPROVED | REJECTED | CHANGES_REQUESTED`; `CHANGES_REQUESTED → SUBMITTED |
   CANCELLED`; `APPROVED → ACTIVE | CANCELLED | SUSPENDED`; `ACTIVE → PAUSED | SUSPENDED | COMPLETED | CANCELLED`;
   `PAUSED → ACTIVE | SUSPENDED | COMPLETED | CANCELLED`; `SUSPENDED → ACTIVE | APPROVED | CANCELLED` (back to `APPROVED` only when it was never published);
   `REJECTED → DRAFT | UNDER_REVIEW` (owner revise, limited; staff reopen); `COMPLETED → ARCHIVED`;
   `CANCELLED → ARCHIVED`; `REJECTED → ARCHIVED`. `ARCHIVED` is terminal (no restoration path).
   Who may take each edge is in `docs/stage-6/campaign-lifecycle.md`; the database records the actor in
   `campaign_status_history` (one row per version, deferred constraint, as Stage 2 designed).

3. **Approved versions.** Submission snapshots the content, goal, beneficiary and media into
   `campaign_versions`. Approval pins `approved_version_id`. The public page renders **only the approved
   version**, never the working copy. A material edit of an `ACTIVE` campaign (beneficiary, goal increase over
   the policy ratio, category, story rewrite) creates a pending version and opens a re-review; the public page
   keeps the previous approved version until the new one is approved.

4. **Publication is a backend action** (owner *publish*, or staff publish) that re-runs the PUBLISH
   eligibility evaluation inside the transaction that moves `APPROVED → ACTIVE`, with the campaign row locked.
   A compliance restriction, KYC change or media problem found at that moment refuses publication.

5. **No financial state.** Campaigns have no raised, balance or total columns (as in Stage 2). Completion
   reasons never imply money was collected. Public totals are not shown until the ledger exists. Archiving
   and cancellation never delete rows.

6. **Restriction-driven suspension.** A compliance restriction of level `SUSPENDED` or `OFFBOARDED` on the
   owner, the owning organisation, the beneficiary or the campaign suspends its `APPROVED`, `ACTIVE` and
   `PAUSED` campaigns (system actor, reason `COMPLIANCE_RESTRICTION`). Lifting the restriction never
   reactivates a campaign: reactivation is a staff action that re-runs the REACTIVATE evaluation.

## Consequences

- PRODUCT §6 gains three states; the deviation is documented here and in the Stage 6 docs.
- FROZEN, refunds and fund resolution on cancellation (LR-019) remain future work; in Stage 6 no campaign
  can hold funds, so cancelling an active campaign has no financial effect.

## Related

ADR-010, ADR-013, ADR-021, ADR-034, ADR-037; PRODUCT §6; campaign-approval-policy.md; campaign-schema.md.
