# ADR-048: Discovery — search projection with serve-time eligibility, governed featured placements, explainable trending, rule-based recommendations, privacy-preserving referrals and engagement analytics, coarse campaign location

- **Status:** Proposed
- **Date:** 2026-10-19
- **Deciders:** Technical lead (Stage 16)
- **Stage:** 16
- **Amends:** ADR-038 §2 (featured selection) and §4 (analytics foundation); design-baseline §3 (module graph: new module
  `discovery`) and §5 (table ownership)

## Context

Stage 7 built a public directory over the campaigns tables: search over `campaign_versions.search_tsv`, staff picks
added and removed by one person, and daily counters fed by `POST /public/events`. Stage 16 must add relevance ranking,
typo tolerance, filters (including place), trending, recommendations, referral attribution, richer analytics, governed
promotion and an indexing pipeline — without ever letting a stale index, a ranking signal or a promotion expose a
campaign that is not public, and without building profiles of visitors.

Constraints: campaigns is the authority on what is public (ADR-036); compliance restrictions suspend campaigns
**asynchronously** through an outbox consumer (ADR-036 §6), so for a short window a restricted campaign is still
`ACTIVE`; risk holds (ADR-045) gate publication but not visibility; referral attribution and tracking consent is open
legal question LR-023 and the lawful basis for analytics is LR-035; there is no real money.

## Decision

1. **New module `internal/discovery`** owning only `app.discovery_*` tables (index, engagement keys, daily aggregates,
   search and abuse counters, referral tokens, featured placements, ranking policies, trending scores). It imports
   `platform`, `audit` and `campaigns` (public service only; archtest). Linked identities and feature flags arrive
   through interfaces implemented in `internal/app`.

2. **Candidates from a projection, cards from the authority.** `app.discovery_campaign_index` holds public,
   approved-version fields (title, summary, category, coarse location, organiser display name) and a weighted
   `tsvector` plus a trigram column. It is used only to find and order candidates. Every card served by any discovery
   surface is rendered by `campaigns.PublicCards`, which re-checks eligibility **at serve time**:
   - *discoverable* (search, categories, locations, sitemap): live status, `PUBLIC` visibility, an approved version, and
     **no compliance restriction of any level** on the campaign, owner, organisation or current beneficiary;
   - *promotable* (featured, trending, recommendations): discoverable, `ACTIVE`, and no `CAMPAIGN_HOLD`,
     `DONATION_HOLD` or `ACCOUNT_REVIEW_HOLD` on the campaign, owner or organisation.

   A stale or wrong projection row can therefore hide a campaign for a while but can never expose one. Registry errors
   fail closed (`503 DISCOVERY_UNAVAILABLE`, `no-store`).

3. **The public page closes the restriction window too.** `campaigns.PublicBySlug` (page, images, updates, reports,
   short links) now hides a campaign whose subjects carry a `SUSPENDED` or `OFFBOARDED` restriction even before the
   suspension consumer has run; a lookup error is `503`. The Stage 7 listing and sitemap endpoints also filter through
   the same eligibility check.

4. **Index maintenance.** The outbox consumer `discovery.index` (River queue `discovery`, never the default queue)
   reindexes on every campaign lifecycle event that can change what is public (including the new
   `campaigns.visibility_changed`) and on compliance restriction events for every campaign of the subject. A periodic
   reconciliation pass compares the projection with campaigns' eligibility and repairs drift, and retries rows left
   dirty by failures. Reindexing is idempotent and **ordered by snapshot time** (database clock read before the
   snapshot; an older snapshot never overwrites a newer one) — no lock is held while campaigns is read.

5. **Search.** PostgreSQL full-text search (`simple` configuration; title A, summary B, category and place C, organiser
   D; `ts_rank_cd`) with a trigram word-similarity fallback for typos (`pg_trgm`, a trusted extension; one page, marked
   `match: FUZZY`). Queries are normalised to letters and digits (no operator can be injected), bounded (100
   characters, 8 terms, 48 per page, 2 s statement timeout, count capped at 1 000) and paginated by keyset cursors
   bound to their sort. No external search cluster.

6. **Featured placements replace staff picks** (amends ADR-038 §2). A placement is requested by one staff member
   (`discovery.feature.request`, step-up), approved by another (`discovery.feature.approve`, step-up; never the requester
   or a linked account — Go **and** a database trigger), has a window of at most 31 days and is shown only while the
   campaign is promotable. Removal is immediate (`discovery.feature.remove`, reason). Stage 7 picks are not migrated
   (they carry no second approval); the table stays as history and the `campaign.feature` routes are removed. The public
   envelope keeps its shape; `selection` is `FEATURED | RECENTLY_PUBLISHED`.

7. **Trending is a published formula, not a model.** `score = (vw·ln(1+V) + sw·ln(1+S)) · (0.25 + 0.75·0.5^(age/h))`
   from deduplicated, bot-filtered unique visitors V and unique sharers S over a window; zero below a minimum sample.
   Money is never an input. Weights are versioned **ranking policies** (closed key set, bounded integers, hashed,
   immutable once submitted, maker-checker); none is seeded and an engineering default applies until one is approved.

8. **Recommendations are fixed rules**, the same for everyone (related to the search, same category, same province,
   featured, popular now, recently published), each with its reason; `personalised` is always `false`. No viewer data,
   KYC, screening or risk data is used.

9. **Engagement analytics are aggregate and minimised** (amends ADR-038 §4). The browser sends views (with a closed
   traffic-source value), share clicks and result clicks to `POST /discovery/events`; search and listing impressions are
   counted by the API. Events are buffered in memory and written in batches. De-duplication uses a keyed hash of
   (day, campaign, kind, network address, user-agent digest) that changes daily and cannot be linked across days or
   campaigns; keys are deleted after two days. Automated clients are counted separately and never as engagement; a
   visitor counts at most 20 raw events per campaign and kind per day. No cookie, IP, user id, user agent or query text
   is stored. Do Not Track and Global Privacy Control are honoured.

10. **Referral codes** are opaque 12-character codes naming only a campaign and a share channel (no person, device or
    address; no privileges; 90-day expiry; staff revocation). Visits carrying a valid code are counted per channel in
    aggregate; donations are never attributed. Because LR-023 is open, `discovery.ProductionReferralsEnabled = false`
    refuses referral issuance and counting in production, and the flag `discovery.referrals.enabled` is a kill switch in
    every environment.

11. **Coarse campaign location.** Campaigns get an optional `location_code` from a reference list of Zimbabwe's 10
    provinces and their capitals (migration-maintained, cited source). It is reviewed content: changing it on a live
    campaign is a material edit (`LOCATION`) and stays off the public page until re-review. It is never an address, the
    beneficiary's or organiser's location, or the visitor's location (LR-090).

12. **SEO.** Filtered, sorted and paginated listing URLs are `noindex, follow` with the canonical pointing at the
    unfiltered page; the sitemap lists only discoverable campaigns (re-checked), categories and provinces with at least
    one discoverable campaign; structured data adds `BreadcrumbList` only (never ratings, amounts, donate actions,
    verification or tax claims). Campaigns without a cover get a generated preview image (title, category, place;
    never money), `public, no-cache` with an ETag, 404 once not public.

## Consequences

- Two sources of campaign text exist (campaigns and the projection); the projection can lag. Correctness does not
  depend on it (serve-time checks), only completeness does (measured backlog in the operations portal).
- Every discovery page costs one batched eligibility check (restriction and, for promotion, hold registry reads).
- Analytics are approximate by design (daily unique estimate; buffer loss on crash ≤ one flush interval).
- Production use of referrals waits for LR-023; production use of analytics waits for LR-035 and LR-012 review.

## Related

ADR-025 (outbox), ADR-036, ADR-037, ADR-038, ADR-045, ADR-046, ADR-047; docs/stage-16/.
