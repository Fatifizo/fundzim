# Stage 14 — Approval center

Page `/admin/approvals`; API `GET /api/v1/admin/approvals?module=&decidable=true` (`ops.dashboard.view`).

The approval center is a **read model**, not an approval authority. Each pending maker-checker item comes from its
module's snapshot: identity role requests, campaign second approvals, KYC/KYB second decisions, beneficiary second
approvals, compliance restricting resolutions, risk rule versions / hold activations / hold releases / case decisions,
refund requests, ledger adjustments, payout approvals, fee policy versions / adjustments / provider schedules.

| Field | Meaning |
|---|---|
| `required_permission` | the permission the owning module requires for the decision |
| `can_decide` | the caller holds that permission and is not one of the item's makers |
| `blocked_reason` | `MAKER` (the caller made or requested it, or is its subject where the module records that) or `PERMISSION` |
| `href` | the owning module's page where the decision is taken |
| `age_seconds` | time since the request |

Items are listed if the caller holds the required permission or is a maker. The page has no approve/reject button:
"Review and decide in <module>" opens the module, whose route re-checks permission, step-up, state, version, linked
identities and idempotency. `TestOperationsApprovalCenterRoutesToDomain` shows the checker sees the item as decidable,
the maker as `MAKER`, SUPPORT not at all, four concurrent decisions through the risk service succeed exactly once, and
the item leaves the queue afterwards.
