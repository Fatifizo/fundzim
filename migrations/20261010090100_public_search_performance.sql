-- Stage 7: public listing and search performance (docs/stage-7/performance.md, measured on 50 000 campaigns).
--   * live PUBLIC campaigns by approved version (category counts, sitemap, search join)
--   * versions by category (category filter and counts)
--   * a stored search vector on campaign_versions with its GIN index, replacing the expression index.
--     campaign_versions is append-only evidence: adding a generated column rewrites no content (the
--     content_sha256 of each snapshot is unchanged) and fires no row trigger.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '600s';
CREATE INDEX ix_campaigns_public_live_version ON app.campaigns (approved_version_id)
  WHERE visibility = 'PUBLIC' AND status IN ('ACTIVE', 'PAUSED', 'COMPLETED');
CREATE INDEX ix_campaign_versions_category ON app.campaign_versions (category_code, id);
ALTER TABLE app.campaign_versions
  ADD COLUMN search_tsv tsvector GENERATED ALWAYS AS (to_tsvector('simple', title || ' ' || summary)) STORED;
CREATE INDEX ix_campaign_versions_search_tsv ON app.campaign_versions USING gin (search_tsv);
DROP INDEX IF EXISTS app.ix_campaign_versions_search;
-- never used by any measured plan (search filters on the approved version's category)
DROP INDEX IF EXISTS app.ix_campaigns_public_category;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS ix_campaigns_public_category ON app.campaigns (category_code, published_at DESC, id DESC)
  WHERE visibility = 'PUBLIC' AND status IN ('ACTIVE', 'PAUSED', 'COMPLETED');
CREATE INDEX IF NOT EXISTS ix_campaign_versions_search ON app.campaign_versions USING gin (to_tsvector('simple', title || ' ' || summary));
DROP INDEX IF EXISTS app.ix_campaign_versions_search_tsv;
ALTER TABLE app.campaign_versions DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS app.ix_campaign_versions_category;
DROP INDEX IF EXISTS app.ix_campaigns_public_live_version;
-- +goose StatementEnd
