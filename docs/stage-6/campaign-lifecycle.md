# Stage 6 — Campaign lifecycle

> ADR-036. Machine `campaign` in `app.status_transitions` (migration `20261009171000`); the Go list
> `campaigns.Edges` is identical (`TestEdgesMatchMigration`). Brief names: `PENDING_REVIEW` = `SUBMITTED`,
> `PUBLISHED` = `ACTIVE`. `FROZEN` is reserved for the ledger stages and has no edges.

## 1. States

| State | Public | Meaning |
|---|---|---|
| `DRAFT` | no | owner edits the working copy |
| `SUBMITTED` | no | queued for review (a snapshot version is pinned) |
| `UNDER_REVIEW` | no | an assigned reviewer started the review |
| `CHANGES_REQUESTED` | no | reviewer asked for changes; owner edits and resubmits |
| `APPROVED` | no (owner preview) | passed review; not yet published |
| `ACTIVE` | yes | published |
| `PAUSED` | yes, marked paused | owner paused it |
| `SUSPENDED` | no | staff or compliance suspended it |
| `REJECTED` | no | review failed |
| `COMPLETED` | yes, read-only | ended |
| `CANCELLED` | no | ended before completion |
| `ARCHIVED` | no | terminal; all history kept |

`visibility` is the owner's choice: `PUBLIC` (listed and searchable) or `UNLISTED` (link only). Exposure additionally
requires `ACTIVE`, `PAUSED` or `COMPLETED`.

## 2. Transitions — who may take each edge

| Edge | Actor | Conditions |
|---|---|---|
| `'' → DRAFT` | owner (personal) or ORG_ADMIN | CREATE_DRAFT evaluation |
| `DRAFT → SUBMITTED`, `CHANGES_REQUESTED → SUBMITTED` | owner / ORG_ADMIN | SUBMIT_FOR_REVIEW evaluation (persisted); snapshot + review opened; idempotent if already SUBMITTED |
| `SUBMITTED → DRAFT` | owner (withdraw) | only while no reviewer started; the queued review is cancelled |
| `DRAFT → CANCELLED`, `CHANGES_REQUESTED → CANCELLED`, `APPROVED → CANCELLED` | owner | — |
| `SUBMITTED → UNDER_REVIEW` | assigned reviewer (`campaign.review`) | start-review |
| `UNDER_REVIEW → CHANGES_REQUESTED` | assigned reviewer | reason code + note |
| `UNDER_REVIEW → APPROVED` | assigned reviewer (`campaign.decide`, step-up); HIGH tier: plus a second approver with `campaign.decide.high` | APPROVE evaluation against the reviewed snapshot |
| `UNDER_REVIEW → REJECTED` | assigned reviewer (`campaign.decide`) | reason code + note |
| `REJECTED → DRAFT` | owner (revise) | below the policy's resubmission limit |
| `REJECTED → UNDER_REVIEW` | staff (`campaign.decide`, reopen) | new review assigned to the reopener |
| `APPROVED → ACTIVE` | owner (publish) or staff (`campaign.publish`) | PUBLISH evaluation inside the locked transaction |
| `ACTIVE → PAUSED` | owner | policy `owner_pause_allowed` |
| `PAUSED → ACTIVE` | owner (resume) | REACTIVATE evaluation |
| `APPROVED/ACTIVE/PAUSED → SUSPENDED` | staff (`campaign.suspend`) or system (compliance restriction) | reason code |
| `SUSPENDED → ACTIVE` (was published) / `SUSPENDED → APPROVED` (never published) | staff (`campaign.unsuspend`) | REACTIVATE evaluation; **never automatic** |
| `ACTIVE/PAUSED → COMPLETED` | owner (`ORGANISER_COMPLETED`, `OTHER`) or staff (`ADMIN_COMPLETED`) | — |
| `ACTIVE/PAUSED/SUSPENDED/APPROVED → CANCELLED` | staff (`campaign.suspend`) | reason code |
| `COMPLETED/CANCELLED/REJECTED → ARCHIVED` | owner or staff | no restoration path |

Every transition locks the campaign row (`FOR UPDATE`), checks the edge, updates with the optimistic version, writes
a `campaign_status_history` row (actor, reason, correlation id), an audit event and, where relevant, an outbox event —
in one transaction. Staff transitions require a reason code (database CHECK).

## 3. Versions and re-review of live edits

Submission snapshots title, summary, story, category, goal, beneficiary, disclosure and approved media into
`campaign_versions` (with a SHA-256 of the canonical content). Approval pins `approved_version_id`. **The public page
renders only the approved snapshot.**

Editing an `ACTIVE`/`PAUSED` campaign (content, goal, category, beneficiary or media) updates the working copy and opens
a `RE_REVIEW` (a newer edit supersedes an open one). The campaign stays live with its previous approved version;
approval swaps in the new snapshot; rejection or a change request discards it. Material change types are recorded
(`TITLE`, `SUMMARY`, `STORY`, `CATEGORY`, `GOAL_CHANGE`, `GOAL_INCREASE` above the policy ratio, `BENEFICIARY`,
`MEDIA`). A beneficiary is never silently replaced on a live campaign.

## 4. Completion, cancellation, archiving

- Completion reasons: `ORGANISER_COMPLETED`, `OTHER` (owner), `ADMIN_COMPLETED` (staff). `GOAL_REACHED` is refused:
  no donation totals exist, so nothing may imply money was collected. `EXPIRED` is reserved (no end dates yet).
- Cancelling a live campaign has no financial effect in Stage 6 because no funds can exist; fund resolution on
  cancellation is LEGAL_REVIEW_REQUIRED (LR-019) for the payment stages.
- Archiving keeps every row: campaigns, versions, reviews, history, evaluations and media/updates are never deleted
  (no-delete triggers). There is no unarchive.
