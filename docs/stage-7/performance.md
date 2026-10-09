# Stage 7 public experience: performance measurements

Stream P (contract §1). Everything below was measured on 2026-10-09 against a scratch database seeded with synthetic
data. Nothing here is a production capacity figure: the database ran in the shared local compose stack on a
developer VM while other agents were working, so absolute numbers are noisy (load average 9 to 13 on 16 vCPUs during
the runs). Compare rows within a table rather than reading single numbers as targets.

## 1. Environment

| Item | Value |
|---|---|
| Host CPU | Intel Xeon Platinum 8160 @ 2.10 GHz, 16 vCPUs (`/proc/cpuinfo`) |
| Host RAM | 31.8 GB (`/proc/meminfo` MemTotal 31 812 364 kB) |
| Kernel | Linux 6.17.0-41-generic |
| PostgreSQL | 17.11 (image `postgres:17.11-alpine3.24`), container memory limit 1 GiB, no CPU limit |
| Postgres settings | shared_buffers 128 MB, work_mem 4 MB, effective_cache_size 4 GB, random_page_cost 4, max_parallel_workers_per_gather 2, jit on (all defaults of the compose stack) |
| Go | go1.27.2 linux/amd64; harness GOMAXPROCS 16 |
| API pools | `DATABASE_MAX_CONNS=20` (fundzim_app), KYC pool 8, compliance pool 4 (as wired in `internal/app`) |
| API config change in the harness only | `RateLimit.Enabled = false` (the global per-IP limiter); everything else is `.env` |
| Load | other agents' builds and tests were running; load average 10.22 / 12.91 / 9.66 at the start of run 2 |

### Dataset (`tests/perf/seed.sql`)

| Table | Rows |
|---|---:|
| app.campaigns | 50 000 (26 854 live PUBLIC; 10% UNLISTED) |
| app.campaign_versions | 47 390 (approved snapshots + ~15% earlier versions) |
| app.campaign_status_history | 220 352 (one per campaign version) |
| app.campaign_reviews | 41 455 (3 547 open) |
| app.campaign_media / app.stored_objects | 57 923 / 115 846 (covers + 25% gallery images; original + derivative, all CLEAN/APPROVED) |
| app.campaign_updates | 21 064 (one campaign with 300) |
| app.campaign_reports | 1 342 (942 OPEN, 400 DISMISSED) |
| app.users / kyc.verification_profiles | 20 007 / 20 000 (60% IDENTITY_VERIFIED) |
| app.beneficiaries | 14 395 (SELF, APPROVED) |
| app.organisations | 500 |
| Database size after `VACUUM FULL` of the updated tables | 477 MB |

Status mix: ACTIVE 45%, PAUSED 3%, COMPLETED 12% (60% live), DRAFT 14%, SUBMITTED 4%, UNDER_REVIEW 3%,
CHANGES_REQUESTED 2%, APPROVED 2%, SUSPENDED 2%, REJECTED 3%, CANCELLED 3%, ARCHIVED 7%. All 11 categories (Medical
largest: 6 082 live; Animal welfare smallest: 764). Titles and summaries are about 65% English and 35% Shona,
generated from word lists (names, places, hospitals, schools, causes).

Every row goes through the real triggers: status machines walked one step per UPDATE, one history/event row per
aggregate version (deferred checks), the campaigns/versions composite FKs, media object purpose checks and the
reviewer not-self trigger. `session_replication_role` is not used. Not seeded: KYB rows (`kyc.kyb_organisations`;
organisation trust lookups therefore hit "no row"), `app.campaign_beneficiaries` links (not read by public paths).
The seed takes about 12 minutes; the database is about 1.6 GB before the `VACUUM FULL` (row versions left by the
step-by-step lifecycle walk).

Note: the whole database (477 MB) is larger than shared_buffers (128 MB), so many plans show `shared read` (served from
the OS page cache, not disk).

## 2. Query plans

`tests/perf/explain.sql` PREPAREs each statement with the exact SQL of the service and runs
`EXPLAIN (ANALYZE, BUFFERS)` twice; the table shows the second (warm) run. "Custom" is the plan Postgres builds for the
actual parameter values; "generic" is the parameter-independent plan it may cache after five executions of a pgx
prepared statement. In a separate check (`plan_cache_mode = auto`, six cheap executions then rare and common queries)
Postgres kept choosing custom plans (`pg_prepared_statements.generic_plans = 0`), so the generic column is a risk
indicator, not what the API was observed to use.

### 2.1 Execution time, as migrated (baseline) and with the two proposed indexes (§4 R1)

| Query | Baseline custom ms | Baseline generic ms | With R1 indexes custom ms | With R1 indexes generic ms |
|---|---:|---:|---:|---:|
| S1 search, no q | 1.7 | 20.0 | 1.7 | 19.5 |
| S1b search, sort=created | 1.5 | 16.9 | 1.4 | 17.5 |
| S2 search, q=harare | 49.8 | 91.5 | 53.5 | 94.4 |
| S2b search, q=pangolins (rare) | 51.4 | 553.9 | 1.5 | 489.6 |
| S2c search, q=for (very common) | 3.2 | 22.0 | 3.1 | 21.6 |
| S3 search, category=medical | 1.5 | 22.2 | 2.4 | 25.8 |
| S3b search, category=animal-welfare | 5.3 | 31.3 | 5.8 | 27.9 |
| S4 search, category=education and q=fees | 3.1 | 30.7 | 3.2 | 21.9 |
| S5 search, keyset cursor 20 000 rows deep | 0.6 | 43.6 | 0.6 | 36.2 |
| S6 capped count, no filter | 4.7 | 46.6 | 1.0 | 20.8 |
| S6b capped count, q=harare | 47.2 | 490.4 | 3.5 | 451.0 |
| S6c capped count, category=animal-welfare | 79.6 | 58.4 | 9.1 | 31.1 |
| S6d capped count, q=pangolins | 46.6 | 473.4 | 0.6 | 449.3 |
| F1 featured staff picks | 0.5 | 0.6 | 0.5 | 0.8 |
| F2 featured recent fill | 1.1 | 2.1 | 1.0 | 1.0 |
| **C1 categories with live counts** | **1 020.3** | **908.0** | **77.9** | **71.1** |
| C2 one category by slug | 87.5 | 91.2 | 9.4 | 8.6 |
| M1 sitemap campaigns | 79.7 | 74.6 | 82.1 | 81.8 |
| D1 detail: slug lookup | 0.08 | 0.08 | 0.06 | 0.07 |
| D2 detail: public-code lookup | 0.06 | 0.06 | 0.04 | 0.06 |
| D3 detail: approved version | 0.04 | 0.05 | 0.04 | 0.05 |
| D4 detail: media of the version | 0.09 | 0.10 | 0.10 | 0.08 |
| U1 public updates (300-update campaign) | 0.06 | 0.08 | 0.07 | 0.07 |
| Q1 review queue (any) | 61.1 | 12.8 | 19.1 | 13.9 |
| Q2 review queue (unassigned, MEDICAL) | 10.7 | 7.1 | 10.0 | 6.9 |
| R1 report triage queue (OPEN) | 0.4 | 0.4 | 0.4 | 0.4 |

### 2.2 Plans (trimmed; baseline, warm, custom unless stated)

**C1 categories with live counts: one full join per category (11 times)**

```
Sort (actual time=914.901..914.905 rows=11 loops=1)
  ->  Seq Scan on campaign_categories k (rows=11)
        SubPlan 1
          ->  Aggregate (actual time=79.136..79.137 rows=1 loops=11)
                ->  Hash Join (actual time=31.279..78.791 rows=2441 loops=11)
                      ->  Seq Scan on campaigns c (actual time=0.010..41.519 rows=26854 loops=11)
                            Filter: ((visibility = 'PUBLIC') AND (status = ANY ('{ACTIVE,PAUSED,COMPLETED}')))
                            Rows Removed by Filter: 23146
                      ->  Hash
                            ->  Seq Scan on campaign_versions v (actual time=0.055..29.759 rows=4308 loops=11)
                                  Filter: (category_code = k.code)
                                  Rows Removed by Filter: 43082
Buffers: shared hit=48129 read=136180
Execution Time: 968.450 ms   (first run; 1 020 ms warm)
```

With the R1 indexes the same SQL becomes two index-only scans per category (merge join), 77.9 ms. With the R2 rewrite
(one grouped pass) it is 38 to 44 ms:

```
Sort  ->  Hash Left Join (k.code = n.category_code)
  ->  HashAggregate (Group Key: v.category_code)
        ->  Hash Join (c.approved_version_id = v.id) (rows=26854)
              ->  Index Only Scan using ix_campaigns_public_live_version on campaigns c (rows=26854, Heap Fetches: 0)
              ->  Index Only Scan using ix_campaign_versions_category on campaign_versions v (rows=47390, Heap Fetches: 0)
Execution Time: 39.273 ms
```

**S2b search, rare term: GIN finds 103 rows, then a sequential scan of campaigns to join them**

```
Limit (actual time=50.659..51.180 rows=20)
  ->  Sort (top-N heapsort)
        ->  Hash Join (c.approved_version_id = v.id) (rows=45)
              ->  Seq Scan on campaigns c (actual time=0.020..45.983 rows=26854 loops=1)
              ->  Hash
                    ->  Bitmap Heap Scan on campaign_versions v (rows=103)
                          ->  Bitmap Index Scan on ix_campaign_versions_search (rows=103)
Execution Time: 51.429 ms   (1.5 ms with ix_campaigns_public_live_version: nested loop on the new index)
```

The capped counts with q (S6b, S6d) have the same shape (about 47 ms each, 0.6 to 3.5 ms with the index).

**S2 search, q=harare: walks the published index and recomputes `to_tsvector` per candidate**

```
Limit (actual time=1.968..69.425 rows=20)
  ->  Nested Loop
        ->  Index Scan using ix_campaigns_public_published on campaigns c (rows=1391)
        ->  Memoize -> Index Scan using pk_campaign_versions on campaign_versions v (loops=1391)
              Filter: (to_tsvector('simple', ((title || ' ') || summary)) @@ '''harare'''::tsquery)
Execution Time: 49.8 ms warm
```

Not fixed by R1 (53.5 ms). A stored tsvector column (R3) gave 4.7 ms on the same data (bitmap scan of the new GIN
index, nested loop into ix_campaigns_public_live_version).

**S2b generic plan (the risk case): no GIN, `to_tsvector` evaluated for every live campaign**

```
Limit (actual time=587.627..600.883 rows=20)
  ->  Gather Merge (Workers Launched: 1)
        ->  Nested Loop
              ->  Parallel Index Scan using ix_campaigns_public_published on campaigns c (rows=7802 loops=2)
              ->  Index Scan using pk_campaign_versions on campaign_versions v (loops=15604)
                    Filter: ((($1 = '') OR ...) AND (($2 = '') OR (to_tsvector(...) @@ websearch_to_tsquery('simple', $2))))
Execution Time: 601.095 ms
```

**S1 search, no q (good)**

```
Limit (actual time=0.834..1.373 rows=20)
  ->  Nested Loop
        ->  Index Scan using ix_campaigns_public_published on campaigns c (rows=20)
        ->  Memoize -> Index Scan using pk_campaign_versions on campaign_versions v (loops=20)
        SubPlan 1 (cover) -> Index Scan using pk_campaign_media on campaign_media cm (loops=20)
Execution Time: 1.650 ms
```

**S5 deep keyset cursor (good):** `Index Scan using ix_campaigns_public_published ... Index Cond: (ROW(published_at, id) < ROW(...))`, 20 rows, 0.6 ms.

**S3b small category:** walks ix_campaigns_public_published and filters `v.category_code` (833 index rows for 20
hits), 5.3 ms. `ix_campaigns_public_category` (on `c.category_code`) is never used, because search filters the
approved version's category; `pg_stat_user_indexes.idx_scan = 0` for it after both API runs.

**S6 capped count, no filter:** `Seq Scan on campaigns` stopping at 1 001 rows plus PK index-only lookups, 4.7 ms.

**M1 sitemap:** `Seq Scan on campaigns` (26 854 rows) then `Sort Method: external merge Disk: 2416kB` on
published_at, 80 ms. The partial index exists, but the planner prefers a sequential scan to fetch all rows.

**D1 to D4 detail, U1 updates, R1 reports, F1/F2 featured:** single index scans (`uq_campaigns_slug`,
`uq_campaigns_public_code`, `pk_campaign_versions`, `pk_campaign_media`, `ix_campaign_updates_public`,
`ix_campaign_reports_queue`, `ix_campaigns_public_published`), all under 1.1 ms.

**Q1 review queue:** bitmap scan of `uq_campaign_reviews_open` (3 547 open reviews), sort on (created_at, id), with
a parallel worker whose start-up takes most of the 61 ms (`Gather Merge actual time=47.899`). 13 ms with the
generic plan, which does not go parallel. Staff-only and not a hot path.

## 3. API latency

Harness: `tests/perf/api_perf_test.go` (build tag `perf`). The API runs in-process (`app.NewDeps` +
`app.PublicHandler` behind `httptest`, real loopback HTTP), latency measured at the client, response bodies read in
full. n = 200 per warm cell (40 for the sitemap) after a warm-up of max(2 × concurrency, 20) requests. Every response
was 200 (errors column 0 in all cells).

"Cold" means: fundzim_perf's pages evicted from shared_buffers with `pg_buffercache_evict` (Postgres was **not**
restarted, because it is shared with other agents; the OS page cache stays warm), then a brand-new `app.Deps`
(new pools, so new Postgres backends with empty catalog and plan caches), then the first request and a second one.

### 3.1 Run 1: schema as migrated (baseline)

Cold:

| Endpoint | 1st request ms | 2nd request ms |
|---|---:|---:|
| search: default (no q) | 93.8 | 31.8 |
| search: sort=created | 80.8 | 30.0 |
| search: q=harare | 217.1 | 109.5 |
| search: q=kidney dialysis | 179.1 | 107.4 |
| search: q=rubatsiro (Shona) | 147.8 | 41.4 |
| search: q=pangolins (rare) | 181.2 | 115.3 |
| search: q=for (very common) | 168.1 | 118.0 |
| search: category=medical | 124.4 | 64.4 |
| search: category=animal-welfare | 188.1 | 119.3 |
| search: category=education&q=fees | 167.8 | 75.6 |
| search: deep cursor (~20k) | 81.6 | 30.0 |
| search: rotating q | 178.9 | 117.5 |
| featured | 63.5 | 9.7 |
| categories (live counts) | 828.6 | 750.3 |
| category by slug | 90.7 | 68.1 |
| detail by slug | 96.4 | 12.6 |
| detail, non-canonical slug | 61.8 | 17.3 |
| updates (1 to 5 per campaign) | 20.1 | 3.9 |
| updates (300, first page) | 18.2 | 4.0 |
| sitemap | 965.8 | 904.1 |

Warm (ms):

| Endpoint | c=1 p50 | c=1 p95 | c=1 p99 | c=8 p50 | c=8 p95 | c=8 p99 | c=32 p50 | c=32 p95 | c=32 p99 | c=32 req/s |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| search: default (no q) | 30.9 | 40.1 | 49.1 | 23.4 | 27.9 | 42.1 | 119.2 | 150.4 | 158.7 | 256 |
| search: sort=created | 30.9 | 40.1 | 48.3 | 24.2 | 26.6 | 27.7 | 125.0 | 150.9 | 155.0 | 251 |
| search: q=harare | 117.6 | 130.1 | 155.6 | 110.2 | 115.3 | 118.2 | 403.2 | 559.4 | 599.6 | 77 |
| search: q=kidney dialysis | 122.6 | 132.6 | 135.6 | 115.9 | 119.7 | 120.5 | 375.3 | 575.5 | 625.8 | 80 |
| search: q=rubatsiro | 47.5 | 68.4 | 73.4 | 43.3 | 60.1 | 62.5 | 198.2 | 292.9 | 327.9 | 152 |
| search: q=pangolins | 121.1 | 132.5 | 136.6 | 113.2 | 116.5 | 117.1 | 416.4 | 633.9 | 662.6 | 71 |
| search: q=for | 103.7 | 123.9 | 153.0 | 95.7 | 117.5 | 123.1 | 331.1 | 517.2 | 549.2 | 89 |
| search: category=medical | 74.7 | 94.3 | 98.0 | 64.4 | 103.4 | 112.7 | 231.7 | 339.2 | 397.5 | 131 |
| search: category=animal-welfare | 117.9 | 124.4 | 128.3 | 122.5 | 126.5 | 137.7 | 465.0 | 643.9 | 715.9 | 66 |
| search: category=education&q=fees | 73.8 | 82.8 | 85.8 | 70.4 | 189.9 | 286.3 | 298.8 | 452.1 | 478.2 | 102 |
| search: deep cursor | 32.0 | 40.7 | 53.6 | 37.0 | 74.3 | 89.8 | 122.5 | 157.4 | 175.6 | 245 |
| search: rotating q | 106.9 | 211.3 | 218.6 | 165.4 | 336.7 | 416.2 | 443.6 | 695.5 | 884.5 | 67 |
| featured | 6.8 | 9.1 | 10.1 | 11.4 | 17.6 | 20.3 | 27.2 | 51.5 | 53.2 | 1 034 |
| **categories (live counts)** | **922.2** | **1 235.3** | **1 265.7** | **969.9** | **1 123.1** | **1 214.4** | **3 768.0** | **4 785.9** | **5 090.3** | **8** |
| category by slug | 83.8 | 91.1 | 94.2 | 95.4 | 115.2 | 136.5 | 315.4 | 441.9 | 517.2 | 96 |
| detail by slug | 4.9 | 8.8 | 10.3 | 6.6 | 14.8 | 18.9 | 16.8 | 32.1 | 37.8 | 1 718 |
| detail, non-canonical slug | 5.9 | 9.6 | 10.9 | 9.9 | 19.6 | 27.7 | 24.1 | 50.7 | 55.4 | 1 154 |
| updates (1 to 5) | 2.3 | 3.2 | 3.8 | 3.0 | 5.8 | 6.5 | 7.8 | 12.4 | 14.3 | 3 637 |
| updates (300, first page) | 2.4 | 3.3 | 3.8 | 3.8 | 8.1 | 9.2 | 8.5 | 14.5 | 17.2 | 3 366 |
| **sitemap** (3.25 MB body) | **1 054.3** | 1 079.8 | 1 112.7 | 1 208.1 | 1 298.6 | 1 303.2 | 3 068.4 | 5 401.4 | 5 462.2 | 7 |

### 3.2 Run 2: with the two R1 indexes added to the scratch database (no code change)

Cold (selected rows; the full table is printed by the harness):

| Endpoint | 1st request ms | 2nd request ms |
|---|---:|---:|
| search: default (no q) | 77.0 | 20.1 |
| search: q=harare | 201.4 | 94.0 |
| search: q=pangolins | 64.4 | 24.2 |
| search: category=animal-welfare | 106.6 | 35.0 |
| featured | 70.9 | 15.7 |
| categories (live counts) | 62.1 | 44.6 |
| category by slug | 20.7 | 7.6 |
| detail by slug | 77.1 | 7.7 |
| updates (300, first page) | 22.4 | 2.5 |
| sitemap | 220.7 | 189.7 |

Warm (ms):

| Endpoint | c=1 p50 | c=1 p95 | c=1 p99 | c=8 p50 | c=8 p95 | c=8 p99 | c=32 p50 | c=32 p95 | c=32 p99 | c=32 req/s |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| search: default (no q) | 21.6 | 32.4 | 35.2 | 18.5 | 23.1 | 24.8 | 51.9 | 60.7 | 68.0 | 592 |
| search: sort=created | 23.1 | 31.4 | 33.1 | 17.9 | 20.8 | 24.2 | 54.3 | 68.7 | 79.2 | 563 |
| search: q=harare | 105.4 | 121.5 | 129.6 | 92.7 | 105.6 | 112.8 | 163.5 | 239.8 | 297.1 | 177 |
| search: q=kidney dialysis | 27.0 | 37.0 | 39.1 | 21.3 | 25.2 | 28.7 | 64.3 | 78.0 | 82.4 | 497 |
| search: q=rubatsiro | 39.3 | 49.1 | 51.0 | 31.7 | 38.3 | 40.3 | 80.1 | 97.8 | 118.3 | 386 |
| search: q=pangolins | 22.8 | 32.2 | 35.6 | 18.7 | 22.3 | 23.6 | 54.5 | 66.0 | 72.2 | 570 |
| search: q=for | 37.6 | 46.7 | 49.5 | 32.3 | 38.9 | 40.5 | 78.5 | 102.7 | 116.6 | 392 |
| search: category=medical | 33.8 | 42.1 | 44.5 | 27.8 | 35.2 | 37.0 | 71.6 | 93.7 | 111.1 | 426 |
| search: category=animal-welfare | 38.2 | 48.8 | 52.1 | 34.7 | 40.1 | 43.0 | 74.1 | 98.8 | 107.7 | 407 |
| search: category=education&q=fees | 56.9 | 68.4 | 72.3 | 48.5 | 54.2 | 56.5 | 102.5 | 136.5 | 148.0 | 304 |
| search: deep cursor | 21.6 | 29.8 | 34.2 | 18.9 | 23.9 | 26.0 | 59.3 | 67.8 | 71.9 | 526 |
| search: rotating q | 37.7 | 72.3 | 109.7 | 31.3 | 58.0 | 94.3 | 83.0 | 139.7 | 162.2 | 361 |
| featured | 6.3 | 10.1 | 11.9 | 5.0 | 6.5 | 7.1 | 15.7 | 20.6 | 21.0 | 1 924 |
| categories (live counts) | 43.1 | 51.3 | 53.1 | 44.3 | 68.1 | 77.6 | 86.5 | 147.7 | 174.2 | 320 |
| category by slug | 5.0 | 9.2 | 11.8 | 4.8 | 7.0 | 8.4 | 9.8 | 14.4 | 16.8 | 2 911 |
| detail by slug | 5.0 | 8.4 | 10.3 | 3.5 | 8.7 | 10.0 | 9.0 | 17.1 | 20.8 | 2 914 |
| detail, non-canonical slug | 5.3 | 9.4 | 12.1 | 3.9 | 6.4 | 9.2 | 10.6 | 19.4 | 22.8 | 2 654 |
| updates (1 to 5) | 2.2 | 3.9 | 4.7 | 1.7 | 3.0 | 3.3 | 5.2 | 8.1 | 8.8 | 5 616 |
| updates (300, first page) | 2.0 | 3.8 | 4.8 | 1.7 | 2.8 | 4.1 | 4.5 | 6.6 | 7.5 | 6 484 |
| sitemap (3.25 MB body) | 199.2 | 226.2 | 239.0 | 235.2 | 249.1 | 254.0 | 577.1 | 627.9 | 645.3 | 54 |

Load differed between runs (other agents), so small differences (a few ms) are not meaningful; the large ones
(categories, sitemap, rare-term and category searches, concurrency 32) are.

### 3.3 Reading the numbers

- **Categories and sitemap** were the slowest endpoints by far (about 1 s per request, 4 to 5 s at concurrency 32).
  The sitemap calls the same category query and serialises 26 854 entries (3.25 MB). Both are `max-age=300`, so a
  shared cache absorbs most traffic, but every origin miss cost about one CPU-second of Postgres time.
- **Search** costs about 20 ms more than its SQL even with no q: S1 plus the count is about 6 ms of SQL, and the rest
  is mostly `enrichCards`, which runs 1 to 2 queries per card (`KYC.Level` on the 8-connection KYC pool, and
  `Beneficiaries.StatusOf`) plus `DisplayNames`, so about 40 sequential round trips for a page of 20. This is
  inferred from the query count, not profiled. Featured (6 cards) is 6 ms against about 1.5 ms of SQL, which fits.
- **Concurrency 32** roughly triples search latency in run 2 (52 ms p50 against 18 ms at concurrency 8) while
  throughput still rises. The app pool is 20 connections and the KYC pool 8, and the per-card trust queries share
  them. Detail, updates and featured scale well.
- **Detail and updates** are fast (p99 under 25 ms at concurrency 32). The non-canonical slug fallback adds about one
  indexed lookup.
- **Cold first requests** cost 60 to 220 ms more than warm ones: connection setup, catalog and plan caches, and
  shared_buffers misses. The OS cache stayed warm, so a real cold start from disk would be slower than this.

## 4. Recommendations

Status: R1 to R5 were applied by the lead (migration `20261010090100_public_search_performance.sql`, which also drops
`ix_campaigns_public_category`; grouped `PublicCategories`; `Search` on `v.search_tsv` with
`SET LOCAL plan_cache_mode = force_custom_plan`; batched `kyc.Levels`, `kyc.OrganisationLevels` and
`beneficiaries.StatusesOf` in `enrichCards`). R6 is deferred. Measurements after the change are in §7. The text below
is kept as written before the change.

**R1 (index, high value).** Add to a migration:

```sql
CREATE INDEX ix_campaigns_public_live_version ON app.campaigns (approved_version_id)
  WHERE visibility = 'PUBLIC' AND status IN ('ACTIVE', 'PAUSED', 'COMPLETED');
CREATE INDEX ix_campaign_versions_category ON app.campaign_versions (category_code, id);
```

Measured effect: categories 922 ms to 43 ms p50 (c=1) and 3.8 s to 87 ms (c=32); sitemap 1 054 ms to 199 ms;
q=pangolins 121 ms to 23 ms; category=animal-welfare 118 ms to 38 ms; capped counts with q from about 47 ms to
under 4 ms. Sizes on this dataset: 840 kB and 2.2 MB. Index-only scans need the visibility map, so autovacuum must
keep up on `app.campaigns`.

**R2 (query, categories).** Compute all counts in one pass instead of a correlated subquery per category
(`PublicCategories`, publicx.go):

```sql
SELECT k.code, k.slug, k.name, k.description, k.display_order, k.sensitive, k.updated_at, coalesce(n.live, 0)
FROM app.campaign_categories k
LEFT JOIN (SELECT v.category_code, count(*) AS live
           FROM app.campaigns c JOIN app.campaign_versions v ON v.id = c.approved_version_id
           WHERE c.status IN ('ACTIVE','PAUSED','COMPLETED') AND c.visibility = 'PUBLIC'
           GROUP BY v.category_code) n ON n.category_code = k.code
WHERE k.active AND ($1 = '' OR k.slug = $1)
ORDER BY k.display_order, k.code;
```

38 to 44 ms with R1 (against 78 ms for the current SQL with R1, and 1 020 ms without R1). Same result set.

**R3 (schema, common search terms).** Store the search vector so that filtering does not recompute
`to_tsvector` for every candidate row:

```sql
ALTER TABLE app.campaign_versions
  ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', title || ' ' || summary)) STORED;
CREATE INDEX ix_campaign_versions_search_tsv ON app.campaign_versions USING gin (search_tsv);
-- then drop ix_campaign_versions_search, and in Search use: ($2 = '' OR v.search_tsv @@ websearch_to_tsquery('simple', $2))
```

On this data: q=harare 49.8 ms to 4.7 ms (custom plan); worst-case generic plans 554 ms to 161 ms (q=pangolins) and
490 ms to 62 ms (count, q=harare). The ALTER rewrote the table in 3.8 s here and does not fire the append-only row
trigger. Because it adds a column to an append-only evidence table, the lead should confirm it is acceptable (the
content hash is unaffected).

**R4 (search plan stability, one line).** In `Search`, next to `SET LOCAL statement_timeout = '2s'`, add
`SET LOCAL plan_cache_mode = force_custom_plan`. The `$1 = '' OR ...` / `$2 = '' OR ...` shape cannot use the GIN index
in a generic plan (S2b generic: 601 ms; counts about 490 ms, about a quarter of the timeout, and much worse under
load). Postgres did not switch to the generic plan in my checks, but nothing prevents it. Building the SQL per filter
combination would also work.

**R5 (code, N+1).** `enrichCards` issues `KYC.Level` and `Beneficiaries.StatusOf` once per card. Batched lookups
(`KYC.Levels(ctx, userIDs)` and `Beneficiaries.StatusesOf(ctx, ids)`, each `... WHERE id = ANY($1)`) would replace
about 40 round trips per search page with 2. Not measured (needs a code change); expected to remove most of the
roughly 15 to 20 ms gap between the search SQL and the endpoint latency, and to relieve the 8-connection KYC pool at
high concurrency.

**R6 (low priority).**
- `ix_campaigns_public_category` (on `campaigns.category_code`) is unused by the public paths
  (`idx_scan = 0`), because search filters on the approved version's category. Drop it, or keep it only if another
  path is planned to use it.
- Sitemap: an index-only path would avoid the 2.4 MB on-disk sort
  (`CREATE INDEX ... ON app.campaigns (published_at DESC) INCLUDE (slug, updated_at) WHERE visibility = 'PUBLIC' AND status IN (...)`).
  It is about 80 ms of the sitemap time after R1/R2, and the response is cached for 300 s.
- Review queue: `CREATE INDEX ON app.campaign_reviews (created_at, id) WHERE status IN ('QUEUED','ASSIGNED','IN_REVIEW')`
  would remove the sort and the parallel worker start-up (61 ms to an expected few ms). Staff-only.

## 5. Statement timeout (2 s search limit)

`TestSearchStatementTimeout`:

| Input | Status | Time ms |
|---|---|---:|
| q=`-zzzz` (negation only) | 200 | 181.8 |
| q=`-a -b -c -d -e -f -g -h -i -j -k` | 200 | 80.2 |
| q=`a or a or ... (20 terms)` | 200 | 189.3 |
| q=`"the family" or "school fees" or -harare` | 200 | 62.1 |
| q=100 × `x` | 200 | 5.3 |
| q=`for` | 200 | 115.0 |
| q=`a` | 200 | 177.1 |
| q=harare while another session holds `ACCESS EXCLUSIVE` on app.campaign_versions | **503 SEARCH_TIMEOUT** | 2 003.9 |

The timeout works: the search statement is cancelled at 2 s (SQLSTATE 57014) and mapped to 503 `SEARCH_TIMEOUT`.
No content-based input reached the limit on this data; the slowest valid query was about 190 ms end to end. The
closest realistic risk is the generic-plan shape (about 490 to 600 ms of SQL per query, single client), which would
approach the limit under concurrent load. R4 removes that risk.

## 6. Reproduce

```bash
# 1. scratch database (same roles and grants as deploy/docker/postgres/init/10-roles.sh)
sg docker -c "docker compose exec -T fundzim-postgres psql -v ON_ERROR_STOP=1 -U postgres -d postgres" < tests/perf/createdb.sql
# 2. migrations as fundzim_migrator
set -a; . ./.env; . tests/perf/env.sh; set +a
go run ./apps/api/cmd/fundzimctl migrate up
# 3. seed (about 12 min), then compact the updated tables
sg docker -c "docker compose exec -T fundzim-postgres psql -X -v ON_ERROR_STOP=1 -U fundzim_migrator -d fundzim_perf" < tests/perf/seed.sql
sg docker -c "docker compose exec -T fundzim-postgres psql -X -U fundzim_migrator -d fundzim_perf -c 'VACUUM (FULL, ANALYZE) app.campaigns, app.stored_objects, app.campaign_media, app.campaign_reviews, app.beneficiaries, app.campaign_reports' -c 'VACUUM ANALYZE'" < /dev/null
# 4. plans (explain.sql mirrors the code after migration 20261010090100; §2 baseline used the earlier SQL quoted in §2/§4)
for m in force_custom_plan force_generic_plan; do
  sg docker -c "docker compose exec -T fundzim-postgres psql -X -U fundzim_app -d fundzim_perf -v mode=$m" < tests/perf/explain.sql
done
# 5. API latency and timeout (eviction is optional and needs the superuser URL)
export FUNDZIM_PERF_SUPERUSER_URL="postgres://postgres:${POSTGRES_SUPERUSER_PASSWORD}@127.0.0.1:5432/fundzim_perf?sslmode=disable"
FUNDZIM_PERF_N=200 go test -tags perf -count=1 -timeout 90m -run TestPublicAPILatency -v ./tests/perf/
go test -tags perf -count=1 -run TestSearchStatementTimeout -v ./tests/perf/
# 6. clean up
sg docker -c "docker compose exec -T fundzim-postgres psql -U postgres -d postgres -c 'DROP DATABASE fundzim_perf'" < /dev/null
```

`tests/perf/env.sh` rewrites every `DATABASE_*URL` from `/fundzim?` to `/fundzim_perf?`. The harness refuses to run
unless the app, KYC and compliance URLs point at fundzim_perf, and the seed refuses to run on any other database.

## 7. After R1 to R5

Same database, same seed, same harness and scripts. Steps: `fundzimctl migrate up` (applied
`20261010090100_public_search_performance.sql` in 4.2 s on this data), `VACUUM ANALYZE app.campaigns,
app.campaign_versions`, `tests/perf/explain.sql` (updated to the new SQL) in both plan modes, then
`FUNDZIM_PERF_N=200 go test -tags perf -run 'TestPublicAPILatency|TestSearchStatementTimeout' ./tests/perf/`.
Load was lower than in runs 1 and 2 (load average 1.53 / 2.63 / 4.79 at start, 4.20 at the end), so part of the
improvement in the c=8 and c=32 rows may be the quieter machine; the plan timings are a cleaner comparison.

### 7.1 Plans (warm, second run)

| Query | Baseline custom ms | Baseline generic ms | After custom ms | After generic ms |
|---|---:|---:|---:|---:|
| S1 search, no q | 1.7 | 20.0 | 1.4 | 19.5 |
| S2 search, q=harare | 49.8 | 91.5 | 6.5 | 39.4 |
| S2b search, q=pangolins | 51.4 | 553.9 | 1.2 | 141.4 |
| S2c search, q=for | 3.2 | 22.0 | 1.5 | 15.7 |
| S3b search, category=animal-welfare | 5.3 | 31.3 | 6.3 | 30.1 |
| S4 search, category=education and q=fees | 3.1 | 30.7 | 2.2 | 29.5 |
| S5 search, deep cursor | 0.6 | 43.6 | 0.6 | 37.6 |
| S6 capped count, no filter | 4.7 | 46.6 | 1.0 | 20.4 |
| S6b capped count, q=harare | 47.2 | 490.4 | 2.6 | 60.0 |
| S6c capped count, category=animal-welfare | 79.6 | 58.4 | 9.5 | 36.2 |
| S6d capped count, q=pangolins | 46.6 | 473.4 | 0.5 | 63.7 |
| C1 categories with live counts | 1 020.3 | 908.0 | 36.8 | 41.3 |
| C2 one category by slug | 87.5 | 91.2 | 37.2 | 37.0 |
| M1 sitemap campaigns | 79.7 | 74.6 | 79.0 | 75.5 |
| Q1 review queue (any) | 61.1 | 12.8 | 14.9 | 13.2 |

Search now runs with custom plans only (R4), so the generic column for S2/S6 is no longer reachable from the API; it
is shown to document the remaining worst case. Detail, updates, featured and report queries are unchanged (all under
1.1 ms).

S2 search, q=harare (after):

```
Limit (actual time=6.150..6.278 rows=20)
  ->  Sort (Sort Key: c.published_at DESC, c.id DESC)
        ->  Nested Loop (rows=403)
              ->  Bitmap Heap Scan on campaign_versions v (rows=723)
                    Recheck Cond: (search_tsv @@ '''harare'''::tsquery)
                    ->  Bitmap Index Scan on ix_campaign_versions_search_tsv (rows=723)
              ->  Index Scan using ix_campaigns_public_live_version on campaigns c (loops=723)
                    Index Cond: (approved_version_id = v.id)
Execution Time: 6.472 ms
```

C1 categories (after):

```
Sort  ->  Hash Left Join (k.code = n.category_code)
  ->  HashAggregate (Group Key: v.category_code)
        ->  Hash Join (c.approved_version_id = v.id) (rows=26854)
              ->  Index Only Scan using ix_campaigns_public_live_version on campaigns c (Heap Fetches: 0)
              ->  Index Only Scan using ix_campaign_versions_category on campaign_versions v (rows=47390, Heap Fetches: 0)
Execution Time: 36.803 ms
```

M1 sitemap (after, unchanged; R6 deferred): `Seq Scan on campaigns` (26 854 rows) then
`Sort Method: external merge Disk: 2416kB`, 79 ms.

Note on C2: one category by slug went from 87.5 ms (baseline) to 37.2 ms, but it is slower than the old correlated
query with R1 indexes (9.4 ms), because the grouped subquery counts every category before the slug filter. Pushing
the slug into the subquery (`AND ($1 = '' OR v.category_code = (SELECT code FROM app.campaign_categories WHERE slug = $1))`)
would restore that; low priority, since the response is `max-age=300`.

### 7.2 API latency (run 3)

Cold:

| Endpoint | 1st request ms (baseline) | 1st request ms (after) | 2nd request ms (after) |
|---|---:|---:|---:|
| search: default (no q) | 93.8 | 67.8 | 14.1 |
| search: q=harare | 217.1 | 77.5 | 15.0 |
| search: q=pangolins | 181.2 | 67.0 | 15.4 |
| search: category=animal-welfare | 188.1 | 91.6 | 26.9 |
| featured | 63.5 | 49.0 | 8.8 |
| categories (live counts) | 828.6 | 46.3 | 28.7 |
| category by slug | 90.7 | 53.1 | 28.9 |
| detail by slug | 96.4 | 81.2 | 8.7 |
| updates (300, first page) | 18.2 | 22.3 | 5.0 |
| sitemap | 965.8 | 188.5 | 178.2 |

Warm (ms; "base" = run 1):

| Endpoint | c=1 p50 base → after | c=1 p99 after | c=8 p50 after | c=8 p99 after | c=32 p50 base → after | c=32 p99 base → after | c=32 req/s after |
|---|---:|---:|---:|---:|---:|---:|---:|
| search: default (no q) | 30.9 → 11.0 | 17.6 | 9.3 | 13.2 | 119.2 → 23.6 | 158.7 → 33.1 | 1 316 |
| search: sort=created | 30.9 → 10.8 | 17.5 | 8.2 | 12.3 | 125.0 → 24.1 | 155.0 → 37.5 | 1 254 |
| search: q=harare | 117.6 → 16.9 | 25.6 | 13.3 | 18.8 | 403.2 → 31.3 | 599.6 → 52.0 | 954 |
| search: q=kidney dialysis | 122.6 → 12.5 | 19.6 | 9.3 | 14.2 | 375.3 → 25.2 | 625.8 → 38.9 | 1 219 |
| search: q=rubatsiro | 47.5 → 19.8 | 29.9 | 17.9 | 25.7 | 198.2 → 41.8 | 327.9 → 66.6 | 711 |
| search: q=pangolins | 121.1 → 10.1 | 16.1 | 7.4 | 10.0 | 416.4 → 18.7 | 662.6 → 29.9 | 1 601 |
| search: q=for | 103.7 → 18.0 | 30.8 | 17.3 | 31.5 | 331.1 → 33.9 | 549.2 → 72.4 | 801 |
| search: category=medical | 74.7 → 20.4 | 29.8 | 16.0 | 23.6 | 231.7 → 37.0 | 397.5 → 63.4 | 810 |
| search: category=animal-welfare | 117.9 → 24.8 | 38.2 | 21.4 | 30.8 | 465.0 → 45.4 | 715.9 → 79.5 | 657 |
| search: category=education&q=fees | 73.8 → 24.7 | 33.1 | 20.8 | 28.2 | 298.8 → 43.0 | 478.2 → 65.5 | 688 |
| search: deep cursor | 32.0 → 10.2 | 16.1 | 7.3 | 11.2 | 122.5 → 18.9 | 175.6 → 29.1 | 1 606 |
| search: rotating q | 106.9 → 18.3 | 31.0 | 14.6 | 22.5 | 443.6 → 34.7 | 884.5 → 71.3 | 842 |
| featured | 6.8 → 4.3 | 6.7 | 2.9 | 4.7 | 27.2 → 8.1 | 53.2 → 15.2 | 3 574 |
| categories (live counts) | 922.2 → 29.2 | 39.6 | 29.9 | 49.5 | 3 768.0 → 57.0 | 5 090.3 → 96.2 | 491 |
| category by slug | 83.8 → 29.1 | 41.7 | 28.7 | 48.9 | 315.4 → 58.4 | 517.2 → 130.5 | 471 |
| detail by slug | 4.9 → 4.5 | 9.2 | 3.1 | 9.6 | 16.8 → 9.5 | 37.8 → 18.8 | 2 880 |
| detail, non-canonical slug | 5.9 → 5.6 | 10.7 | 3.4 | 6.7 | 24.1 → 9.7 | 55.4 → 17.3 | 2 900 |
| updates (1 to 5) | 2.3 → 2.5 | 3.8 | 1.7 | 2.7 | 7.8 → 5.1 | 14.3 → 7.7 | 5 950 |
| updates (300, first page) | 2.4 → 2.5 | 4.5 | 1.6 | 3.1 | 8.5 → 4.5 | 17.2 → 7.4 | 6 381 |
| sitemap (3.25 MB body) | 1 054.3 → 184.8 | 224.4 | 274.9 | 336.1 | 3 068.4 → 481.8 | 5 462.2 → 552.6 | 60 |

Every response was 200 (errors 0 in every cell; cold requests also 200).

### 7.3 Statement timeout (after)

| Input | Status | Time ms |
|---|---|---:|
| q=`-zzzz` | 200 | 66.3 |
| q=`-a -b -c -d -e -f -g -h -i -j -k` | 200 | 23.8 |
| q=`a or a or ... (20 terms)` | 200 | 33.4 |
| q=`"the family" or "school fees" or -harare` | 200 | 24.7 |
| q=100 × `x` | 200 | 8.5 |
| q=`for` | 200 | 23.9 |
| q=`a` | 200 | 29.7 |
| q=harare under a held `ACCESS EXCLUSIVE` lock | **503 SEARCH_TIMEOUT** | 2 003.7 |

The 2 s timeout still maps to 503 `SEARCH_TIMEOUT`; the slowest content-based query is now 66 ms, about 3% of the
limit.

### 7.4 What remains

- Sitemap: 185 ms p50 sequentially, 482 ms at concurrency 32. It is dominated by the 26 854-row query with an on-disk
  sort (79 ms) plus building and hashing a 3.25 MB response. R6 (covering index) is still open; the response is cached
  for 300 s.
- Category by slug: 29 ms p50, see the note in §7.1.
- Review queue parallel start-up (R6), staff-only.

The scratch database `fundzim_perf` was dropped after these runs; `tests/perf/` keeps everything needed to rebuild it
(§6).
