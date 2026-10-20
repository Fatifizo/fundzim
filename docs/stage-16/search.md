# Stage 16 — Search

Code: `internal/discovery/search.go`. Contract: `GET /api/v1/discovery/campaigns` and `/discovery/search` (OpenAPI tag
"Discovery (Stage 16)").

## 1. Engine choice

PostgreSQL full-text search over the discovery projection, with `pg_trgm` for typo tolerance. No Elasticsearch or other
cluster: at the measured sizes (§8) the queries stay far inside the 2 s budget, and a second store would add an
eventual-consistency surface that the serve-time eligibility rule already makes harmless but not free. Revisit only with
measured need (KI-S16-09).

## 2. Searchable fields

| Weight | Fields | Source |
|---|---|---|
| A | title | approved version |
| B | summary | approved version |
| C | category name, town and province names | categories, locations |
| D | organiser display name (individual) or organisation name | users / organisations |

Not indexed: the story (long, and may contain personal detail better found on the page), beneficiary names (disclosed
only on the page when consented, never searchable), KYC, documents, risk or compliance data, goals or money.
Configuration `simple` (no stemming: English, Shona and Ndebele words are matched as written).

## 3. Normalisation (`NormalizeQuery`)

UTF-8 required (`INVALID_ENCODING`), at most 100 characters (`INVALID_LENGTH`), NFC, lower case. Split on anything that is
not a letter, digit or combining mark — punctuation, symbols, tsquery operators (`& | ! : * ( ) <->`), quotes and markup
become separators. Single letters dropped (digits kept), duplicates removed, at most 8 terms of at most 40 characters.
The `tsquery` is built from these terms only (`a & b & c:*`; the last term is a prefix match when ≥ 3 characters), so no
query syntax can be injected; every value reaches SQL as a bound parameter.

## 4. Typo tolerance

When a query has terms, the first page has no full-text match, and no cursor is given, the same filters are re-run with
`word_similarity(query, lower(title)) ≥ 0.4` (GIN trigram index), ordered by similarity then newest — one page, no
cursor, `match: "FUZZY"`, and the web app says "No exact matches … campaigns with similar titles".

## 5. Highlighting

`Highlight(text, terms)` returns plain-text segments; a segment is marked when it is a word starting with a query term
(the same prefix rule as the search). Concatenating the segments gives the text back exactly; the web app drops
highlights that do not (defence in depth) and renders marks as `<mark>` with React-escaped text. Unmarked segments
are bare text nodes (wrapping them in elements made screen readers read "HelpTariro"; found by an accessibility test).

## 6. Filters and sorts

| Parameter | Values | Notes |
|---|---|---|
| `q` | text | §3 |
| `category` | slug or code | unknown values match nothing |
| `province`, `town` | location slugs | unknown values match nothing; campaigns without a location appear only unfiltered |
| `status` | `active`, `paused`, `ended` | default: all three live statuses |
| `published` | `7d`, `30d`, `90d`, `365d` | publication date |
| `organiser` | `individual`, `organisation` | campaign type |
| `sort` | `relevance` (default with a query), `newest` (default without), `oldest`, `trending` | ties: newest, then id |

Not offered, deliberately: funding progress, "most supported", "ending soon" (no real donations exist; campaigns have no
end date) and verification filters (trust indicators are computed live per card and are not a search key; offering a
"verified" filter would invite reading it as an endorsement).

## 7. Pagination and bounds

Keyset cursors (base64url of `sort | key… | id`, bound to their sort; a cursor of another sort is refused): newest and
oldest on `(published_at, id)`, relevance on `(rank::real, published_at, id)`, trending on `(score, published_at, id)`.
Page size 1–48 (default 12). `SET LOCAL statement_timeout = '2s'` and `plan_cache_mode = force_custom_plan` (the Stage 7
finding: generic plans ignore the GIN index). Total count capped at 1 000 (`capped: true`). Relevance is ranked within the 2 000 newest matches
(`RelevanceWindow`): ranking every match of a very common word cost 758 ms p95 at 54 000 campaigns; a match older
than the window is still reachable by the newest/oldest sorts and by more specific queries (KI-S16-16). The
recommendations' related-to-search rule uses the same window. A page can contain fewer
items than the limit when candidates stopped being eligible since they were indexed; the cursor still advances.
Rate limit `discovery.search.ip` 120/min per client address (generous for shared carrier addresses).

## 8. Performance

Measured on a 100 000-campaign scratch database — see [mobile-performance.md](mobile-performance.md) §5 and
[testing.md](testing.md).

## 9. What search never does

Never stores the query text (searches are counted by sort, has-query, has-filters and outcome only); never returns a
campaign that is not discoverable now; never ranks by money; never personalises.
