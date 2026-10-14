# Stage 11 — Approval workflow (maker-checker)

- **Maker** = the owner (or ORG_ADMIN) who requests; **checkers** = FINANCE staff with `payout.approve` (approve) or
  `payout.reject` (reject). COMPLIANCE never approves payouts (design-baseline I-20).
- **Tier:** DUAL (two distinct FINANCE approvers) unless `payout.dual_approval_min` is configured and not reached, there is
  no risk indicator and it is not an organisation's first payout; then SINGLE. No AUTO tier (PD-24). The tier can only be
  raised (database).
- **Every decision** records actor, round, decision, reason (5–2000 chars), the step-up time, a conflict attestation
  (required) and, when `payout.approval_validity` is configured, an expiry. Audit `payout.approval.granted` /
  `payout.status_changed`; event `APPROVAL_GRANTED` / `APPROVAL_REJECTED`.
- **Step-up:** `payout.approve`, `payout.reject` and `payout.operate` require a fresh factor (route policy); owners need a
  fresh step-up to request or cancel a payout and to create, change or retire a payout destination.

## Separation of duties

| Refused approver | Where |
|---|---|
| The requester, the campaign owner, or any account linked to either (staff ↔ personal links, `app.actor_identities`) | Go and database (`app.payout_approvals_guard`) |
| Whoever last changed or verified the payout destination | Go and database |
| Any member of the owning organisation | Go |
| The same approver twice in a round | database (UNIQUE) → `ALREADY_DECIDED` |
| Approval outside PENDING_REVIEW or in an old round | database |

Tested in `TestPayoutApprovalSeparationOfDutiesAndInvalidation` (linked FINANCE account refused over HTTP and direct SQL;
destination verifier refused by SQL; COMPLIANCE gets 403; APPROVED without approvals refused by the database; destination
suspension starts a new round; amount immutable after approval).

## Expiration

Approvals expire only if `payout.approval_validity` is configured (no regulatory time limit is invented). An expired or
missing approval fails EC-22 at submission and sends the payout back to review (APPROVAL_EXPIRED exception).
