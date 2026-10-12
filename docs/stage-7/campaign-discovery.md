# Stage 7 — Campaign discovery

Search, browse, categories and featured campaigns. API: `internal/campaigns/public.go` (`Search`) and `publicx.go`
(`Featured`, `PublicCategories`, `Sitemap`). Contract: [interface-contracts.md](interface-contracts.md) §3.
Measurements: [performance.md](performance.md).

"Live" below means status `ACTIVE`, `PAUSED` or `COMPLETED` with visibility `PUBLIC`; `UNLISTED` campaigns are
reachable by link but never listed, counted, featured or put in the sitemap.

## 1. Search and browse — `GET /public/campaigns`

| Parameter | Rule |
|---|---|
| `q` | Optional. Cleaned with the Stage 6 text rules; control characters refused (422 `VALIDATION_FAILED`); markup is not refused here but treated as plain text to match; at most 100 characters after whitespace is collapsed |
| `category` | Category code **or** slug, at most 40 characters; unknown values match nothing |
| `sort` | `published` (default) or `created`; anything else 422 |
| `cursor` | Keyset cursor `<RFC 3339 timestamp>|<uuid>` from the previous page's `next_cursor`; malformed → 422 |
| `limit` | 1–50, default 20 |

- Matching: Postgres full-text search with the `simple` configuration — `websearch_to_tsquery('simple', q)` against
  the stored `search_tsv` (`to_tsvector('simple', title || ' ' || summary)`) of the **approved** version. No
  stemming and no language-specific handling of Shona or Ndebele (KI-S7-08).
- Order: newest first by `published_at` (or `created_at`), ties by id; no relevance ranking, no popularity.
- Total: `{value, capped}` — exact up to 1 000; above that `{value: 1000, capped: true}` (the web shows
  "1000+ campaigns").
- Bounds: each search runs in its own transaction with `SET LOCAL statement_timeout = '2s'` and
  `SET LOCAL plan_cache_mode = force_custom_plan` (a cached generic plan ignored the GIN index; performance.md R4).
  A timeout returns **503 `SEARCH_TIMEOUT`** ("Search took too long. Try a simpler search.").
- Cards: title, summary, slug, category, cover, goal, status, `organiser {type, display_name}` and `trust`. Display
  names, KYC levels and beneficiary statuses for a page of cards are looked up in batches (performance.md R5).
- Cache: `public, no-cache` + strong ETag.

## 2. Categories

- `GET /public/categories` — active categories in display order, each with `live_campaigns` (exact count of live
  PUBLIC campaigns, computed in one grouped query: performance.md R2). `GET /public/categories/{slug}` returns one,
  404 `CATEGORY_NOT_FOUND` if unknown or inactive. `GET /campaign-categories` is kept as a compatibility alias.
- Cache: `public, max-age=300` (configuration rather than campaign state; a count may lag by up to five minutes).
- Web: `/categories` (cards with counts) and `/categories/[slug]` (the category's campaigns with the same listing UI).
  A `CATEGORY_VIEW` event is counted on the category page.

## 3. Featured — `GET /public/campaigns/featured?limit=` (1–12, default 6)

1. **Staff picks** (`app.campaign_featured`, active rows) whose campaign is live and PUBLIC, ordered by `position`
   (1–100) then time added.
2. If fewer than `limit`, **recently published** `ACTIVE` or `PAUSED` PUBLIC campaigns that are not already picks,
   newest first.

Each item has `selection: STAFF_PICK | RECENTLY_PUBLISHED`. Staff manage picks at `/admin/campaigns/featured`:
`POST /admin/campaigns/{campaign_id}/feature` (`{position, note}`; note 3–500 characters) and
`DELETE /admin/campaigns/{campaign_id}/feature` (`{reason}`), permission `campaign.feature` (ADMIN, COMPLIANCE;
sensitive; step-up). Only `ACTIVE`/`PAUSED` PUBLIC campaigns can be picked (409 `NOT_FEATURABLE`; a second active pick
is 409 `ALREADY_FEATURED`). Picks and removals are audited and never deleted. The home page explains the selection
and says being featured is not an endorsement or ranking.

## 4. Canonical URLs and short codes (ADR-038 §6)

A campaign's slug ends with its 10-character public code and is fixed at first publication. `GET
/public/campaigns/{slug}` with a non-matching title part but a matching trailing code returns the campaign with its
`canonical_slug`; the web app redirects permanently. `GET /public/campaign-codes/{public_code}` returns
`{canonical_slug}` for `/c/[code]` short links (404 if not public). Redirect targets are always built by
`campaignPath()` from the API's slug — never from user input — so there is no open redirect.

## 5. Sitemap data — `GET /public/sitemap`

Live PUBLIC campaigns (newest first, capped at 50 000) and active categories, each with `updated_at`. Cache
`public, max-age=300`. The web's `/sitemap.xml` lists them only when indexing is allowed (see [seo.md](seo.md)).

## 6. What discovery does not do

No ranking by money raised or popularity (none exists), no personalisation, no "trending", no paid placement, no
query logging (search events record only whether a category filter was used). Advanced discovery and language
support are later work (ROADMAP Stage 16).
