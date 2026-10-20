# Stage 16 — Mobile-first, low-bandwidth performance

## 1. Budgets (engineering targets, not commitments)

| Metric | Budget | Test condition |
|---|---|---|
| Largest Contentful Paint | ≤ 2 500 ms (p75 field; lab p95 here) | lab mobile profile §3 |
| Interaction to Next Paint | ≤ 200 ms (lab INP proxy) | lab mobile profile |
| Cumulative Layout Shift | ≤ 0.1 | lab mobile profile |
| JavaScript per discovery page | ≤ 200 KB compressed on a cold load | lab, transfer size |
| Transfer on a warm reload | ≤ 60 KB (static assets cached; data revalidated by ETag) | lab |
| Image payload above the fold | first card cover ≤ 100 KB (320/640 px JPEG variants via `srcset`) | Stage 7 media pipeline |
| Discovery API (search, directory) | p95 ≤ 300 ms at concurrency 8 on 100 000 campaigns | `TestDiscoveryPerf` |
| Event ingestion | p95 ≤ 50 ms at concurrency 16 (no synchronous database write) | `TestDiscoveryPerf` |
| Statement timeout | 2 s hard limit (503 `SEARCH_TIMEOUT`) | code |

## 2. What the pages do for slow networks

- Server-rendered HTML; the search form is a plain GET form (works before and without JavaScript); filters live in a
  native `<details>` disclosure.
- Results stream behind fixed-size skeletons (no layout shift); the first card's cover is the only eager image
  (`priority`); all others lazy with explicit width and height and responsive `srcset`/`sizes` (Stage 7 variants).
- Card and strip links are **not prefetched** (`prefetch={false}`): a viewport of prefetches costs more than it saves on
  1.6 Mbps.
- Keyset pagination of 12 cards per page; no infinite scroll, no background polling on public pages.
- Public data is `public, no-cache` + ETag: revalidation costs one round trip and usually a 304.
- Analytics: one small `keepalive` POST per view or share, never retried, nothing when DNT/GPC is on.
- Referral codes requested only on a share click, with an 800 ms cap.
- Recommendations render after the main content (Suspense) and fail silently.
- Offline: the Stage 7 service worker serves the static offline page for navigations; an on-page notice announces when
  the browser goes offline; nothing is queued for later replay (no unsafe offline operations).
- Generated preview images are produced only when a crawler asks (not on page loads).

## 3. Lab method

`apps/web/scripts/cwv.mjs` (Stage 7 tool; Stage 16 adds the search-results, trending and province pages) against the
real compose stack at http://localhost:3000 (web image built from this branch, Go API, PostgreSQL, worker; development
dataset). Mobile: 360×800, DPR 2, touch, CPU 4× throttled, 1.6 Mbps down / 750 kbps up / 150 ms latency. Desktop:
1350×940 unthrottled. 5 runs per page, profile and mode (cold: new context; warm: reload). p50 / p95 nearest rank.
LCP and CLS from `PerformanceObserver`, INP proxy from Event Timing (heading tap, menu taps, Tab). These are lab
numbers, not field data (KI-S16-14).

## 4. Lab results (2026-10-19, real compose stack, development dataset)

Measured after the final web build. 5 runs each; p50 / p95.

| Profile | Page | Load | LCP ms | CLS | INP-proxy ms | TTFB ms | Transfer KB | JS KB (decoded) | Requests |
|---|---|---|---|---|---|---|---|---|---|
| mobile | / | cold | 644 / 712 | 0 / 0 | 48 / 56 | 40 / 68 | 172 | 145 (476) | 18 |
| mobile | / | warm | 344 / 396 | 0 / 0 | 32 / 40 | 42 / 50 | 19 | 0 (476) | 18 |
| mobile | /campaigns | cold | 628 / 648 | 0 / 0 | 48 / 48 | 31 / 53 | 175 | 146 (478) | 15 |
| mobile | /campaigns | warm | 444 / 464 | 0 / 0 | 40 / 40 | 31 / 39 | 21 | 0 (478) | 15 |
| mobile | /categories/education | cold | 620 / 640 | 0 / 0 | 48 / 48 | 36 / 48 | 176 | 146 (478) | 18 |
| mobile | /categories/education | warm | 364 / 432 | 0 / 0 | 32 / 32 | 34 / 44 | 22 | 0 (478) | 18 |
| mobile | campaign detail | cold | 600 / 664 | 0 / 0 | 48 / 56 | 47 / 61 | 183 | 152 (493) | 20 |
| mobile | campaign detail | warm | 360 / 420 | 0 / 0 | 32 / 40 | 44 / 44 | 23 | 0 (493) | 20 |
| mobile | /campaigns?q=school | cold | 572 / 592 | 0 / 0 | 48 / 56 | 29 / 36 | 174 | 146 (478) | 16 |
| mobile | /campaigns?q=school | warm | 288 / 364 | 0 / 0 | 32 / 32 | 30 / 34 | 20 | 0 (478) | 16 |
| mobile | /campaigns/trending | cold | 628 / 640 | 0 / 0 | 48 / 56 | 35 / 41 | 166 | 145 (476) | 16 |
| mobile | /campaigns/trending | warm | 340 / 376 | 0 / 0 | 32 / 32 | 32 / 36 | 24 | 0 (476) | 28 |
| mobile | /campaigns/location/harare | cold | 620 / 632 | 0 / 0 | 48 / 64 | 33 / 41 | 173 | 146 (478) | 18 |
| mobile | /campaigns/location/harare | warm | 384 / 404 | 0 / 0 | 32 / 40 | 30 / 37 | 19 | 0 (478) | 18 |
| desktop | all seven pages | cold / warm | 160–248 (p95 ≤ 248) | 0 | ≤ 32 | ≤ 61 | 22–190 | 145–152 | 22–34 |

Every page is inside the budgets (LCP p95 ≤ 712 ms vs 2 500; CLS 0; INP proxy ≤ 64 ms; JS ≈ 146 KB compressed; warm
reloads ≤ 24 KB). Caveats: lab profile, development dataset with mostly tiny synthetic covers (Stage 7 §8.3 measured
heavy images separately), localhost server. Field data does not exist (KI-S16-14).

**Defect found by this measurement:** the campaign page never reached network idle because the analytics endpoint
answered the browser's `keepalive` POST with a JSON body nobody reads, which kept the request open in Chromium. The
endpoint now answers `204 No Content` (as the Stage 7 counters did); fixed before these numbers were taken.

## 5. API measurements (100 000-campaign scratch database)

`TestDiscoveryPerf` (`tests/perf/discovery_perf_test.go`), 2026-10-19, against `fundzim_perf` seeded with
`tests/perf/seed.sql` scaled to 100 000 campaigns (69 013 ever published, 53 952 live PUBLIC). In-process API (same as
the integration tests), concurrency 8, 200 requests per scenario, warm caches; host Xeon 8160, 16 vCPU; compose PostgreSQL
17 with default settings. The seed has no campaign locations: province values were assigned in the projection only, for
the filter measurement. The engagement tables were empty, so trending ranked nothing (scores computed, none eligible).
The harness fails on any non-2xx response.

Index build (the real reconciliation pass, from an empty projection):

| step | campaigns checked | discoverable rows | reindexed | seconds | ms per campaign |
|---|---:|---:|---:|---:|---:|
| 1k | 1 000 | 791 | 1 000 | 5.6 | 5.6 |
| 10k | 10 000 | 7 817 | 9 000 | 44.1 | 4.9 |
| all | 69 013 | 53 952 | 59 013 | 276.4 | 4.7 |

Latency in ms (p50 / p95), end to end (candidates, serve-time eligibility, cards):

| scenario | 1k index | 10k index | full index (53 952) |
|---|---|---|---|
| directory, newest | 6.4 / 23.6 | — | 7.5 / 19.6 |
| search q=harare (common) | 9.4 / 13.3 | — | 103.5 / 130.1 |
| search q=kidney dialysis | — | — | 111.1 / 142.5 |
| search q=rubatsiro (common Shona word) | — | — | 217.1 / 276.1 |
| search q=pangolins (rare) | — | — | 20.9 / 26.8 |
| typo q=dialisys (trigram fallback) | — | — | 34.9 / 41.8 |
| q=school & category=education | — | — | 213.6 / 241.1 |
| filter province | — | — | 10.0 / 17.7 |
| filter status + published | — | — | 16.5 / 18.1 |
| sort=trending | — | — | 57.8 / 74.4 |
| sort=oldest | — | — | 7.4 / 14.6 |
| page 2 (keyset cursor) | — | — | 13.3 / 20.7 |
| rotating 20 queries | — | — | 169.5 / **309.6** |
| categories with counts | — | — | 0.3 / 1.7 |
| locations with counts | — | — | 0.4 / 1.4 |
| trending | — | — | 0.7 / 1.0 |
| featured | — | — | 6.1 / 8.0 |
| recommendations | — | — | 5.8 / 8.4 |
| trending computation (all rows) | — | — | 4 540 total |

(The full per-step table for 1k and 10k is in the test output; all 10k values were between the 1k and full values.)

Event ingestion: `POST /discovery/events`, concurrency 16, 4 000 requests of 2 events: p50 0.3 ms, p95 0.9 ms, max
9.8 ms, 0 errors; the final batched flush of 8 000 events took 231 ms.

Footprint at the full index: `discovery_campaign_index` 144 MB in total (GIN tsvector 13 MB, GIN trigram 21 MB).

**Defects found and fixed by these measurements.** (1) The first run was invalid: the discovery-specific rate limits
turned most requests into 429s (the harness now disables them and fails on any error). (2) Common-word relevance search
ranked every match: 660 / 758 ms for `rubatsiro`; ranking within the 2 000 newest matches (`RelevanceWindow`) brought it
to 217 / 276 ms. (3) Facet counts grouped every row on each request (≈ 98 ms p50); a 30 s in-process cache made them
sub-millisecond. Remaining: the mixed rotating-query p95 (310 ms) is just over the 300 ms budget at this size
(KI-S16-16); every scenario is far inside the 2 s statement timeout.

The scratch database was dropped after the runs; `tests/perf/` rebuilds it (see docs/stage-7/performance.md §6; replace
`50000` with `100000` in the seed for this size).
