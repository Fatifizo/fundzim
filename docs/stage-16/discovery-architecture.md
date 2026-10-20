# Stage 16 — Discovery architecture

Decision record: [ADR-048](../adr/ADR-048-discovery-sharing-and-referrals.md).

## 1. Shape

```
browser ── Next.js (server components, no-store) ──► /api/v1/discovery/*  ─┐
                                                                           │  internal/discovery (owns app.discovery_*)
                                                   candidates (projection) ├─► app.discovery_campaign_index
                                                   cards + eligibility ────┼─► campaigns.PublicCards ─► campaigns tables
                                                                           │        ├─► compliance.Restrictions (batched)
                                                                           │        └─► risk.BlockingHolds (batched, promotion)
outbox (campaigns.*, compliance.restriction_*) ─► consumer discovery.index (River queue "discovery") ─► Reindex
periodic jobs (queue "discovery"): reconcile 10 min · trending 15 min · maintenance 5 min (end placements, purge keys)
API process: engagement Recorder (in memory, bounded) ─► batched writes every 2 s
```

`internal/discovery` is one package (search, indexing, trending, featured, recommendations, referrals, events,
analytics, HTTP, operations snapshot) — the brief's sub-packages map to files, not packages, following the repository
convention of one package per module. It imports `platform`, `audit` and `campaigns` only (archtest).

## 2. Authority and ownership

| Concern | Owner | Discovery's role |
|---|---|---|
| Whether a campaign may be public | campaigns (ADR-036) | asks `campaigns.PublicCards` / `Discoverable` for every result |
| Restrictions | compliance | read through campaigns (batched) |
| Holds | risk | read through campaigns (batched; promotion only) |
| Campaign text, media, organiser, trust indicators | campaigns | copies public fields into the projection for candidate generation only |
| Categories | campaigns (`app.campaign_categories`) | reads; counts discoverable rows |
| Locations | campaigns (`app.campaign_locations`, campaign `location_code`) | reads; filters; counts |
| Ranking, featured placements, referral codes, engagement aggregates | discovery | owns |

## 3. Eligibility levels

| Level | Conditions | Used by |
|---|---|---|
| Public page | live status (ACTIVE, PAUSED, COMPLETED) and no SUSPENDED/OFFBOARDED restriction on campaign, owner, organisation or beneficiary (Stage 16 addition; ADR-036 rule otherwise unchanged) | `/public/campaigns/{slug}`, images, updates, reports, short links, preview image |
| Discoverable | page rules + `PUBLIC` visibility + approved version + **no restriction of any level** | search, directory, categories, locations, sitemap, analytics events, referral codes |
| Promotable | discoverable + `ACTIVE` + no `CAMPAIGN_HOLD`, `DONATION_HOLD`, `ACCOUNT_REVIEW_HOLD` on campaign, owner or organisation | featured, trending, recommendations, featured requests and approvals |

Checked at: query (projection `discoverable` flag), indexing (snapshot), **serve time** (every card), trending
computation (`rank_eligible`), featured request and approval, sitemap (batched re-check), events (ignored for
non-discoverable campaigns), referral issuance. A registry error anywhere fails closed (503, never an unfiltered list).

## 4. Data model (migration `20261020090100_discovery`)

| Table | Content | Mutability |
|---|---|---|
| `discovery_campaign_index` | one row per campaign ever indexed: public fields, weighted `search_doc`, `title_norm` (trigram), `discoverable`, `snapshot_at`, dirty/error markers | upserted; never deleted (a removed campaign is cleared and marked not discoverable); CHECK keeps removed rows empty |
| `discovery_engagement_keys` | (day, campaign, kind, 32-byte keyed hash, hits) | upsert; deleted after 2 days (the only DELETE grant added) |
| `discovery_campaign_daily` | aggregates (day, campaign, metric, channel, count) | upsert increments |
| `discovery_search_daily` | searches by sort, has-query, has-filters, outcome (never text) | upsert increments |
| `discovery_abuse_daily` | bot-filtered, visitor cap, rate limited, invalid referral, ingest dropped, not discoverable | upsert increments |
| `discovery_referral_tokens` | code, campaign, channel, expiry, revocation | only revocation columns change; no delete |
| `discovery_featured_placements` | request, window, decision, removal | guarded state machine; request immutable; maker-checker CHECK + trigger; no delete |
| `discovery_ranking_policies` | versioned weights (closed key set), hash, decision | guarded state machine; weights immutable; maker-checker CHECK + trigger; one ACTIVE; no delete |
| `discovery_trending_scores` | latest score, components, policy version, `rank_eligible` | upsert; no delete |

Campaigns-owned additions (migration `20261020090000_campaign_locations`): `app.campaign_locations` (reference list,
read-only for runtime roles), `campaigns.location_code`, `campaign_versions.location_code` (append-only snapshots;
`content_sha256` includes the location only when set, so Stage 6 snapshots keep their hash form), material change
type `LOCATION`.

Permissions: `discovery.view`, `discovery.feature.request`, `discovery.feature.approve`, `discovery.feature.remove`,
`discovery.ranking.request`, `discovery.ranking.approve`, `discovery.operate` (all but `view` require step-up). Roles:
ADMIN (view, request, remove, ranking request, operate), REVIEWER (view, request, remove), COMPLIANCE (view, approve,
remove, ranking approve, operate). Nobody holds both sides of a maker-checker pair through these grants, and the
database refuses a checker linked to the maker anyway.

## 5. Surfaces

| Route | Notes |
|---|---|
| `GET /discovery/campaigns`, `/discovery/search` | same contract; the second counts impressions as search |
| `GET /discovery/categories`, `/discovery/locations` | counts from the projection |
| `GET /discovery/trending`, `/discovery/featured`, `/discovery/recommendations` | promotable only; each item has a reason |
| `GET /discovery/sitemap` | discoverable only, re-checked in batches of 1 000 |
| `POST /discovery/events`, `/discovery/referrals` | rate limited; private, no-store |
| `GET /public/campaigns/featured` | Stage 7 path, now served by discovery |
| `GET /campaigns/{campaign_id}/analytics` | owner or organisation members |
| `/admin/discovery/*` | overview, placements, ranking, referral revocation, reconciliation |

Web: `/campaigns` (directory with search, filters, featured and trending strips, category and province links),
`/campaigns/search` → `/campaigns`, `/campaigns/category/[slug]` → `/categories/[slug]`, `/campaigns/trending`,
`/campaigns/featured`, `/campaigns/location/[province]`, `/categories/[slug]` (discovery-backed), `/campaigns/[slug]`
(location, recommendations, view tracking, breadcrumbs, preview image), `/og/campaigns/[slug]` (generated preview),
`/dashboard/campaigns/[id]/analytics`, `/admin/discovery`, `/admin/discovery/featured`, `/admin/discovery/ranking`.

## 6. Featured placements

`REQUESTED → APPROVED | REJECTED | CANCELLED`, `APPROVED → REMOVED | ENDED` (`app.guard_transition('discovery_featured')`).
One open placement per campaign (partial unique index). Window ≤ 31 days, start within 90 days. Shown only inside the
window and while promotable; the maintenance job moves expired placements to `ENDED`. Up to 12 are shown, by position;
recently published promotable campaigns fill the rest (`selection: RECENTLY_PUBLISHED`).
