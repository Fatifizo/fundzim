-- Query plans for the Stage 7 public read paths, mirroring the SQL in internal/campaigns/{public.go,publicx.go},
-- internal/campaigns/views.go (review queue) and internal/campaigns/updates/updates.go (public updates).
-- Mirrors the code after migration 20261010090100 (search_tsv, grouped category counts); the baseline SQL
-- is quoted in docs/stage-7/performance.md. Each statement is PREPAREd like pgx does (extended protocol, statement cache) and EXPLAINed twice:
-- with a custom plan (first executions) and with the generic plan Postgres may switch to after five executions.
--
--   docker compose exec -T fundzim-postgres psql -X -U fundzim_app -d fundzim_perf -v mode=force_custom_plan < tests/perf/explain.sql
--   docker compose exec -T fundzim-postgres psql -X -U fundzim_app -d fundzim_perf -v mode=force_generic_plan < tests/perf/explain.sql
\set ON_ERROR_STOP 1
SET plan_cache_mode = :mode;
SET statement_timeout = '30s';

\set card 'SELECT c.id, c.slug, v.title, v.summary, v.category_code, v.goal_amount_minor, v.goal_currency, c.status, (SELECT u.m::text FROM unnest(v.media_ids) WITH ORDINALITY AS u(m, o) JOIN app.campaign_media cm ON cm.id = u.m AND cm.status = ''APPROVED'' AND cm.kind = ''COVER'' ORDER BY u.o LIMIT 1), c.published_at, c.created_at, c.owner_user_id, c.owner_organisation_id, v.beneficiary_id'
\set where ' FROM app.campaigns c JOIN app.campaign_versions v ON v.id = c.approved_version_id WHERE c.status IN (''ACTIVE'',''PAUSED'',''COMPLETED'') AND c.visibility = ''PUBLIC'' AND ($1 = '''' OR v.category_code = $1 OR v.category_code = (SELECT code FROM app.campaign_categories WHERE slug = $1)) AND ($2 = '''' OR v.search_tsv @@ websearch_to_tsquery(''simple'', $2))'

PREPARE search(text, text, timestamptz, uuid, int) AS :card, c.published_at :where
  AND ($3::timestamptz IS NULL OR (c.published_at, c.id) < ($3, $4::uuid)) ORDER BY c.published_at DESC, c.id DESC LIMIT $5;
PREPARE search_created(text, text, timestamptz, uuid, int) AS :card, c.created_at :where
  AND ($3::timestamptz IS NULL OR (c.created_at, c.id) < ($3, $4::uuid)) ORDER BY c.created_at DESC, c.id DESC LIMIT $5;
PREPARE search_count(text, text) AS SELECT count(*) FROM (SELECT 1 :where LIMIT 1001) x;

-- deep cursor: ~20 000 rows into the default order
SELECT published_at AS deep_ts, id AS deep_id FROM app.campaigns WHERE status IN ('ACTIVE','PAUSED','COMPLETED') AND visibility = 'PUBLIC'
  ORDER BY published_at DESC, id DESC OFFSET 20000 LIMIT 1 \gset

\echo '=== S1 search: no q, no category'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search('', '', NULL, 'ffffffff-ffff-ffff-ffff-ffffffffffff', 20);
\echo '=== S1b search: sort=created'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search_created('', '', NULL, 'ffffffff-ffff-ffff-ffff-ffffffffffff', 20);
\echo '=== S2 search: q=harare'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search('', 'harare', NULL, 'ffffffff-ffff-ffff-ffff-ffffffffffff', 20);
\echo '=== S2b search: q=pangolins (rare)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search('', 'pangolins', NULL, 'ffffffff-ffff-ffff-ffff-ffffffffffff', 20);
\echo '=== S2c search: q=for (very common)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search('', 'for', NULL, 'ffffffff-ffff-ffff-ffff-ffffffffffff', 20);
\echo '=== S3 search: category=medical (slug)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search('medical', '', NULL, 'ffffffff-ffff-ffff-ffff-ffffffffffff', 20);
\echo '=== S3b search: category=animal-welfare (small category)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search('animal-welfare', '', NULL, 'ffffffff-ffff-ffff-ffff-ffffffffffff', 20);
\echo '=== S4 search: category=education & q=fees'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search('education', 'fees', NULL, 'ffffffff-ffff-ffff-ffff-ffffffffffff', 20);
\echo '=== S5 search: deep keyset cursor'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search('', '', :'deep_ts', :'deep_id', 20);
\echo '=== S6 capped count: no filter'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search_count('', '');
\echo '=== S6b capped count: q=harare'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search_count('', 'harare');
\echo '=== S6c capped count: category=animal-welfare'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search_count('animal-welfare', '');
\echo '=== S6d capped count: q=pangolins'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE search_count('', 'pangolins');

PREPARE featured_picks(int) AS :card, f.position::text FROM app.campaign_featured f
  JOIN app.campaigns c ON c.id = f.campaign_id AND f.removed_at IS NULL JOIN app.campaign_versions v ON v.id = c.approved_version_id
  WHERE c.status IN ('ACTIVE','PAUSED','COMPLETED') AND c.visibility = 'PUBLIC' ORDER BY f.position, f.added_at LIMIT $1;
PREPARE featured_recent(uuid[], int) AS :card FROM app.campaigns c JOIN app.campaign_versions v ON v.id = c.approved_version_id
  WHERE c.status IN ('ACTIVE','PAUSED') AND c.visibility = 'PUBLIC' AND NOT (c.id = ANY ($1::uuid[])) ORDER BY c.published_at DESC, c.id DESC LIMIT $2;
\echo '=== F1 featured: staff picks'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE featured_picks(12);
\echo '=== F2 featured: recently published fill'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE featured_recent('{}', 6);

PREPARE cats(text) AS SELECT k.code, k.slug, k.name, k.description, k.display_order, k.sensitive, k.updated_at, coalesce(n.live, 0)
  FROM app.campaign_categories k
  LEFT JOIN (SELECT v.category_code, count(*) AS live FROM app.campaigns c JOIN app.campaign_versions v ON v.id = c.approved_version_id
             WHERE c.status IN ('ACTIVE','PAUSED','COMPLETED') AND c.visibility = 'PUBLIC' GROUP BY v.category_code) n ON n.category_code = k.code
  WHERE k.active AND ($1 = '' OR k.slug = $1) ORDER BY k.display_order, k.code;
\echo '=== C1 categories with live counts (all)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE cats('');
\echo '=== C2 category by slug'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE cats('medical');

PREPARE sitemap AS SELECT slug, updated_at FROM app.campaigns WHERE status IN ('ACTIVE','PAUSED','COMPLETED')
  AND visibility = 'PUBLIC' ORDER BY published_at DESC LIMIT 50000;
\echo '=== M1 sitemap'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE sitemap;

SELECT slug AS some_slug, id AS some_id, approved_version_id AS some_ver, public_code AS some_code FROM app.campaigns
  WHERE status = 'ACTIVE' AND visibility = 'PUBLIC' ORDER BY md5(slug) LIMIT 1 \gset
PREPARE by_slug(text) AS SELECT id, status, category_code, visibility, owner_user_id, owner_organisation_id, created_by FROM app.campaigns WHERE slug = $1;
PREPARE by_code(text) AS SELECT id, status, category_code, visibility, owner_user_id, owner_organisation_id, created_by FROM app.campaigns WHERE public_code = $1;
PREPARE version_row(uuid) AS SELECT * FROM app.campaign_versions WHERE id = $1;
PREPARE public_media(uuid[]) AS SELECT cm.id::text, cm.kind, cm.alt_text FROM unnest($1::uuid[]) WITH ORDINALITY AS u(m, o)
  JOIN app.campaign_media cm ON cm.id = u.m AND cm.status = 'APPROVED' ORDER BY u.o;
\echo '=== D1 detail: resolve slug'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE by_slug(:'some_slug');
\echo '=== D2 detail: resolve public code (non-canonical slug fallback)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE by_code(:'some_code');
\echo '=== D3 detail: approved version'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE version_row(:'some_ver');
\echo '=== D4 detail: media of the approved version'
SELECT media_ids AS some_media FROM app.campaign_versions WHERE id = :'some_ver' \gset
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE public_media(:'some_media');

SELECT c.id AS heavy_id FROM app.campaigns c JOIN app.campaign_updates u ON u.campaign_id = c.id GROUP BY c.id ORDER BY count(*) DESC LIMIT 1 \gset
PREPARE updates_page(uuid, int) AS SELECT id, title, body, media_ids::text[], published_at FROM app.campaign_updates
  WHERE campaign_id = $1 AND status = 'PUBLISHED' ORDER BY published_at DESC, id DESC LIMIT $2;
\echo '=== U1 public updates (300-update campaign, first page)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE updates_page(:'heavy_id', 21);

PREPARE queue(text, uuid, text, timestamptz, uuid, int) AS SELECT c.id, r.id, c.title, c.category_code, c.status, r.status, r.kind, r.risk_tier,
  r.assigned_to, r.escalated, r.pending_outcome IS NOT NULL, r.created_at
  FROM app.campaign_reviews r JOIN app.campaigns c ON c.id = r.campaign_id
  WHERE r.status IN ('QUEUED','ASSIGNED','IN_REVIEW')
    AND ($1 = 'any' OR ($1 = 'me' AND r.assigned_to = $2::uuid) OR ($1 = 'unassigned' AND r.assigned_to IS NULL))
    AND ($3 = '' OR c.category_code = $3)
    AND ($4::timestamptz IS NULL OR (r.created_at, r.id) > ($4, $5::uuid))
  ORDER BY r.created_at, r.id LIMIT $6;
\echo '=== Q1 review queue (any)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE queue('any', '00000000-0000-0000-0000-000000000000', '', NULL, '00000000-0000-0000-0000-000000000000', 50);
\echo '=== Q2 review queue (unassigned, category MEDICAL)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE queue('unassigned', '00000000-0000-0000-0000-000000000000', 'MEDICAL', NULL, '00000000-0000-0000-0000-000000000000', 50);

PREPARE reports(text, timestamptz, uuid, int) AS SELECT r.id, r.campaign_id, c.title, c.slug, r.status, r.reason, r.details, r.contact_email_ct IS NOT NULL,
  r.reporter_user_id IS NOT NULL, r.created_at, r.triaged_at, r.triage_reason_code, r.triage_note, c.status
  FROM app.campaign_reports r JOIN app.campaigns c ON c.id = r.campaign_id
  WHERE r.status = $1 AND ($2::timestamptz IS NULL OR (r.created_at, r.id) > ($2, $3::uuid)) ORDER BY r.created_at, r.id LIMIT $4;
\echo '=== R1 report triage queue (OPEN)'
EXPLAIN (ANALYZE, BUFFERS, COSTS OFF) EXECUTE reports('OPEN', NULL, '00000000-0000-0000-0000-000000000000', 50);
