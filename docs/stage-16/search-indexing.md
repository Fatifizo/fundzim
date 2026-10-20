# Stage 16 — Search indexing

Code: `internal/discovery/index.go`, `internal/campaigns/discovery.go` (`DiscoverySnapshot`, `Discoverable`).

## 1. Triggers

| Trigger | Events | Action |
|---|---|---|
| Campaign lifecycle | `campaigns.published`, `approved` (incl. re-review approval), `paused`, `resumed`, `suspended`, `reactivated`, `completed`, `cancelled`, `archived`, `visibility_changed` (new in Stage 16), `beneficiary_changed` | reindex the campaign |
| Compliance | `compliance.restriction_applied`, `compliance.restriction_lifted` | reindex every campaign of the subject (owner, organisation, beneficiary, campaign) |
| Reconciliation | periodic (10 min), staff "reconcile now" (≤ 2 000 campaigns) | compare projection with eligibility for every published campaign; reindex differences; retry dirty rows |

Consumer `discovery.index` runs on the River queue `discovery` (`outbox.Relay.ConsumerQueues`), so indexing never delays
payments, scans or notifications. Delivery is at least once; reindexing is idempotent.

## 2. Reindex

1. Read the database clock (`snapshot_at`).
2. `campaigns.DiscoverySnapshot`: loads the campaign, checks eligibility (restrictions; a registry error is returned),
   reads the approved version, organiser name, category and location names.
3. Not discoverable → the row is upserted as `discoverable = false` with every text field cleared (a CHECK enforces it).
   Discoverable → upsert with a weighted `tsvector` and `title_norm`.
4. The upsert applies only when `snapshot_at` is newer than the stored one: an older snapshot never overwrites a newer
   one. Every state change emits an event whose reindex starts after the change committed, so the newest snapshot
   reflects the latest state. No connection is held while campaigns reads (an earlier advisory-lock design exhausted
   the pool under 20 concurrent reindexes — found by `TestDiscoveryConcurrency`).
5. Failure → `dirty_since`, `last_error` (`ELIGIBILITY_UNAVAILABLE`, `TIMEOUT`, `INDEX_FAILED`), `attempts` + 1; the row
   keeps its previous state (serve-time checks still apply) and the outbox and the reconciliation retry it.

Unknown campaign ids create no row. Drafts are never indexed (only campaigns that have been published are walked).

## 3. Consistency guarantees

| Situation | Effect |
|---|---|
| Campaign suspended or restricted, consumer not yet run | absent from every surface at once (serve-time check); row cleared when the event is processed |
| Restriction applied (RESTRICTED level, campaign stays ACTIVE) | absent from discovery at once; page stays (ADR-036) |
| Restriction lifted / campaign reactivated | back after the reindex (event or reconciliation) |
| Projection row wrong (bug, manual edit) | cannot expose anything; reconciliation repairs it (`TestDiscoveryEndToEnd` edits a row of a suspended campaign to `discoverable = true`: search does not show it; one pass repairs it) |
| Content edited and re-approved | `campaigns.approved` → reindex; until then the projection holds the previous approved text — the same text the page shows |
| Worker down | backlog grows (operations metric `discovery.index.backlog`, DEGRADED after 5 min or 5 failed attempts); serving is unaffected |

Known limitation: reconciliation compares the discoverable flag only, not content; content drift is repaired by the
next campaign event (KI-S16-05).

## 4. Observability

Operations portal metrics `discovery.index.backlog`, `discovery.index.discoverable`; exception
`SEARCH_INDEX_FAILING` (SEV3) per row with ≥ 5 failed attempts; staff overview `/admin/discovery` (index status,
backlog, oldest waiting, failing, last indexed).
