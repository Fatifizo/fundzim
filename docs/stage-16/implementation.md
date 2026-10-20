# Stage 16 — Discovery, Sharing & WhatsApp Optimisation: implementation

Date: 2026-10-19. Branch `stage-16/discovery` (from `stage-15/notifications` at `558825d`). Decision record:
[ADR-048](../adr/ADR-048-discovery-sharing-and-referrals.md). Non-production engineering: no live donations, no real
payouts, no live messaging, no fabricated donation activity, totals or verification; referral attribution is blocked in
production until LR-023.

## Documents

| Topic | Document |
|---|---|
| Prerequisites, Stage 15 verification, governance, legal dependencies | [prerequisite-assessment.md](prerequisite-assessment.md) |
| Architecture, eligibility levels, data model, surfaces | [discovery-architecture.md](discovery-architecture.md) |
| Search, indexing, ranking | [search.md](search.md), [search-indexing.md](search-indexing.md), [ranking.md](ranking.md) |
| Trending, recommendations | [trending.md](trending.md), [recommendations.md](recommendations.md) |
| Categories, locations | [categories.md](categories.md), [location-discovery.md](location-discovery.md) |
| Sharing, previews, SEO | [whatsapp-sharing.md](whatsapp-sharing.md), [social-sharing.md](social-sharing.md), [seo.md](seo.md) |
| Referrals, analytics | [referrals.md](referrals.md), [analytics.md](analytics.md) |
| Mobile and low-bandwidth performance, budgets, measurements | [mobile-performance.md](mobile-performance.md) |
| Security and privacy review | [security-review.md](security-review.md) |
| Tests and results | [testing.md](testing.md) |
| Known issues | [known-issues.md](known-issues.md) |
| Stage 17 preparation | [../stage-handover/STAGE-16-TO-STAGE-17.md](../stage-handover/STAGE-16-TO-STAGE-17.md) |

## What was built

**Backend.** New module `internal/discovery` (search with normalisation, highlights, typo fallback, filters, four sorts and
keyset cursors; outbox indexing ordered by snapshot time and periodic reconciliation; featured placements with
maker-checker; trending formula and maker-checker ranking policies; rule-based recommendations; referral codes;
in-memory engagement recorder with batched writes; owner and staff analytics; sitemap source; operations snapshot; 26 HTTP
routes). Composition in `internal/app/discovery.go` (routes with rate limits and idempotency, consumer
`discovery.index` on the River queue `discovery`, jobs `discovery.reconcile` 10 min, `discovery.trending` 15 min,
`discovery.maintenance` 5 min, operations source). Campaigns: optional reviewed `location_code` (create, edit, snapshot,
owner and public views; material edit `LOCATION`), `campaigns.visibility_changed` event, batched serve-time eligibility
(`PublicCards`, `Discoverable`, `DiscoverySnapshot`, `IDsBySlugs`), public page hidden at SUSPENDED/OFFBOARDED
restrictions before the asynchronous suspension, Stage 7 listing and sitemap filtered through eligibility, Stage 7 staff
picks removed. Rate-limit policies `discovery.search.ip` (120/min), `discovery.event.ip` (240/min),
`discovery.referral.ip` (30/h). Archtest: module `discovery` (platform, audit, campaigns) and its nine tables;
`app.campaign_locations` owned by campaigns.

**Database.** `20261020090000_campaign_locations` (reference list of 10 provinces and 10 provincial capitals with source;
campaign and version `location_code`; material type `LOCATION`) and `20261020090100_discovery` (`pg_trgm`; nine
`app.discovery_*` tables; state machines `discovery_featured` (6 edges) and `discovery_ranking_policy` (5); maker-checker
CHECKs and an `actor_identities` trigger; immutability triggers; seven `discovery.*` permissions; kill switch
`discovery.referrals.enabled`; runtime grants: DELETE only on de-duplication keys, location list read-only). Both
round-trip down/up.

**API.** OpenAPI: 24 new paths and 30 schemas (tag "Discovery (Stage 16)"), location fields on campaign inputs and views,
`/public/campaigns/featured` selection `FEATURED | RECENTLY_PUBLISHED`; staff-pick paths and schemas removed. Redocly:
valid (2 pre-existing warnings).

**Frontend.** `/campaigns` (search, filters behind a disclosure, featured and trending strips, category and province
links), `/campaigns/search` and `/campaigns/category/[slug]` (308 aliases), `/campaigns/trending`, `/campaigns/featured`,
`/campaigns/location/[province]`, discovery-backed `/categories/[slug]`, campaign page (place, recommendations, view
tracking with source and referral, breadcrumbs, preview image for coverless campaigns, poster link),
`/campaigns/[slug]/poster` (printable QR poster), `/og/campaigns/[slug]` (generated preview PNG), owner location editor,
`/dashboard/campaigns/[id]/analytics`, `/admin/discovery`, `/admin/discovery/featured`, `/admin/discovery/ranking`; cards
with reason, place and highlights; result links without background prefetch; share controls with LinkedIn, referral
codes and synchronous share windows; analytics client on the discovery pipeline (DNT/GPC honoured, no storage); sitemap
from the discovery source; `noindex, follow` listing variants. Retired: the staff-pick manager and its API calls.

## Acceptance criteria (brief §78) — engineering self-assessment

| # | Criterion | Result | Evidence |
|---|---|---|---|
| 1–4 | Prerequisites, Stage 15 verified, governance accurate, blockers tracked | MET | prerequisite-assessment.md (KI-S15-19 remediated; CI on 558825d green; register rows NOT_RECORDED) |
| 5 | Discovery architecture | MET | ADR-048; discovery-architecture.md |
| 6–7 | Directory works; eligibility enforced | MET | E2E directory specs; `TestDiscoveryEndToEnd` (restriction, hold, suspension, stale row) |
| 8–14 | Search, normalisation, filters, sorting, pagination, indexing, index consistency | MET | unit (`TestNormalizeQuery`, `TestTSQuery…`, `TestCursor…`, `TestSearchPlanValidation`), integration end-to-end and concurrency |
| 15–16 | Category discovery and pages | MET | E2E categories, discovery spec |
| 17–18 | Featured placements and governance | MET | integration (DB maker-checker), E2E maker-checker across two staff |
| 19–20 | Trending and manipulation controls | MET (address-pool inflation residual, KI-S16-03) | `TestScore…`, integration trending, visitor cap |
| 21–23 | Recommendations, explanations, privacy | MET | integration, vitest (personalised guard), E2E |
| 24–25 | Location discovery and privacy | MET (LR-090 open) | integration filters, E2E province pages |
| 26–28 | WhatsApp, social, native sharing | MET | vitest share-controls (window, referral, fallback), E2E sharing |
| 29–31 | Open Graph, preview images, crawler access | MET in tests; **real WhatsApp rendering not verified** (KI-S16-01) | E2E metadata, preview route tests |
| 32–37 | SEO metadata, canonicals, sitemaps, robots, structured data, slugs | MET | vitest SEO, E2E sitemap/robots/noindex/aliases |
| 38–41 | Referral tracking, token security, attribution, privacy | MET (production blocked, LR-023) | integration referral and revocation, unit codes, production gate test |
| 42–46 | Analytics, accuracy documented, pipeline, organiser and admin analytics | MET | integration analytics totals, E2E analytics and overview pages, analytics.md |
| 47–48 | Discovery administration, secure controls | MET | `TestDiscoveryGovernanceAndRBAC`, E2E staff |
| 49–53 | Mobile-first, low bandwidth, images, budgets, poor connectivity | MET (lab only, KI-S16-14) | mobile-performance.md |
| 54 | Accessibility tests pass | MET (automated axe; manual audit open, KI-S16-13) | vitest axe, Playwright axe desktop + Pixel 7, real-stack a11y |
| 55–57 | Discovery, search abuse, sharing abuse controls | MET | security-review.md; rate-limit test |
| 58 | Migrations | MET | round trip on the development database |
| 59 | Search performance measured | MET | mobile-performance.md §5 (1k / 10k / full index on 100 000 campaigns) |
| 60–61 | API contracts, frontend routes | MET | Redocly; `TestEveryRouteHasAPolicyAndIsInOpenAPI`; E2E |
| 62–70 | Unit, integration, security, concurrency, failure-injection, performance, E2E, SEO, OpenAPI | see [testing.md](testing.md) | |
| 71 | Remote CI | see the completion report | |
| 72–74 | No restricted leakage, no fabricated metrics, no real payments or payouts | MET | integration leakage tests; analytics never money; no financial code touched |
| 75–76 | Documentation, Stage 17 handover | MET | this folder; STAGE-16-TO-STAGE-17.md |

## Deviations from the brief and the roadmap

- Sub-packages suggested by the brief are files of one `discovery` package (repository convention).
- `discovery_category_metadata` and `discovery_index_jobs` were not needed (categories already carry descriptions; the
  index row itself carries the dirty/backlog state); search events are aggregate counters (`discovery_search_daily`),
  not per-event rows, so no query text can ever be stored.
- "Funding progress", "most supported" and "ending soon" are not offered (no real donations; no end dates);
  verification is not a search filter (trust indicators stay on cards).
- No WhatsApp Business integration (optional in the roadmap; LR-023, separate ADR); no automated messages.
- Roadmap "QR posters" delivered as a printable page; roadmap "WhatsApp preview rendering" not verified on real clients.
- `/campaigns/category/[slug]` and `/campaigns/search` are redirects to the existing canonical pages rather than new pages
  (no duplicate directories).
