# Stage 14 — Campaign operations

Page `/admin/operations/campaigns` (`campaign.view`). Source: `campaigns.OpsSnapshot` (`internal/campaigns/ops.go`)
over `app.campaign_reviews`, `app.campaign_reports`, `app.campaign_updates` and `app.campaigns`.

- Metrics: reviews waiting, in review, escalated, awaiting a second approval; open public reports; updates awaiting
  moderation.
- Approvals: campaign decisions awaiting the second approver (maker = first approver) → `/admin/campaigns/review/{id}`.
- Exceptions: escalated reviews.
- Search: `campaigns.OpsSearch` reuses the existing admin campaign search (`AdminList` query) for `campaign.view`.

Decisions (approve, reject, request changes, suspend, feature) stay on the Stage 6/7 pages and routes; the operations
page links to them only for callers holding the matching permission.
