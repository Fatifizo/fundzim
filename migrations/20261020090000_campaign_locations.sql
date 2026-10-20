-- Stage 16: coarse campaign location (docs/stage-16/location-discovery.md, ADR-048). Owner module: campaigns. Schema: app.
--   app.campaign_locations   reference list of Zimbabwe provinces and towns (changed only by migrations)
--   app.campaigns.location_code, app.campaign_versions.location_code
-- A campaign's location is where the cause is, chosen by the owner from this list (a province, or a town that implies
-- its province). It is reviewed content like the title: a change to a live campaign is a material edit ('LOCATION')
-- and stays off the public page until a re-review approves it. It is never an address, never the beneficiary's or the
-- organiser's location and never the visitor's location. Optional: NULL means "not specified".
-- Seed source: the ten provinces of Zimbabwe and their capitals as listed on
-- https://en.wikipedia.org/wiki/Provinces_of_Zimbabwe (accessed 2026-10-19). Other towns are added by migration.
-- Runs as fundzim_migrator. Forward-only in production (ADR-028).
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

CREATE TABLE app.campaign_locations (
  code           text    NOT NULL,
  kind           text    NOT NULL,
  slug           text    NOT NULL,
  name           text    NOT NULL,
  province_code  text,
  display_order  integer NOT NULL,
  active         boolean NOT NULL DEFAULT true,
  source         text    NOT NULL,
  CONSTRAINT pk_campaign_locations PRIMARY KEY (code),
  CONSTRAINT uq_campaign_locations_kind_slug UNIQUE (kind, slug),
  CONSTRAINT fk_campaign_locations_province FOREIGN KEY (province_code) REFERENCES app.campaign_locations (code),
  CONSTRAINT ck_campaign_locations_code CHECK (code ~ '^(PROVINCE|TOWN)_[A-Z][A-Z_]{1,40}$' AND starts_with(code, kind || '_')),
  CONSTRAINT ck_campaign_locations_kind CHECK (kind IN ('PROVINCE', 'TOWN')),
  CONSTRAINT ck_campaign_locations_slug CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 40),
  CONSTRAINT ck_campaign_locations_name CHECK (length(name) BETWEEN 2 AND 60),
  -- a town belongs to exactly one province; a province to none
  CONSTRAINT ck_campaign_locations_parent CHECK ((kind = 'TOWN') = (province_code IS NOT NULL) AND (province_code IS NULL OR starts_with(province_code, 'PROVINCE_'))),
  CONSTRAINT ck_campaign_locations_source CHECK (length(source) BETWEEN 10 AND 300)
);
CREATE TRIGGER trg_campaign_locations_no_delete BEFORE DELETE ON app.campaign_locations
  FOR EACH ROW EXECUTE FUNCTION app.forbid_mutation();

INSERT INTO app.campaign_locations (code, kind, slug, name, province_code, display_order, source)
SELECT l.code, l.kind, l.slug, l.name, l.prov, l.ord, 'https://en.wikipedia.org/wiki/Provinces_of_Zimbabwe (accessed 2026-10-19)'
FROM (VALUES
  ('PROVINCE_BULAWAYO',            'PROVINCE', 'bulawayo',            'Bulawayo',            NULL::text, 10),
  ('PROVINCE_HARARE',              'PROVINCE', 'harare',              'Harare',              NULL, 20),
  ('PROVINCE_MANICALAND',          'PROVINCE', 'manicaland',          'Manicaland',          NULL, 30),
  ('PROVINCE_MASHONALAND_CENTRAL', 'PROVINCE', 'mashonaland-central', 'Mashonaland Central', NULL, 40),
  ('PROVINCE_MASHONALAND_EAST',    'PROVINCE', 'mashonaland-east',    'Mashonaland East',    NULL, 50),
  ('PROVINCE_MASHONALAND_WEST',    'PROVINCE', 'mashonaland-west',    'Mashonaland West',    NULL, 60),
  ('PROVINCE_MASVINGO',            'PROVINCE', 'masvingo',            'Masvingo',            NULL, 70),
  ('PROVINCE_MATABELELAND_NORTH',  'PROVINCE', 'matabeleland-north',  'Matabeleland North',  NULL, 80),
  ('PROVINCE_MATABELELAND_SOUTH',  'PROVINCE', 'matabeleland-south',  'Matabeleland South',  NULL, 90),
  ('PROVINCE_MIDLANDS',            'PROVINCE', 'midlands',            'Midlands',            NULL, 100)
) AS l(code, kind, slug, name, prov, ord);

INSERT INTO app.campaign_locations (code, kind, slug, name, province_code, display_order, source)
SELECT l.code, 'TOWN', l.slug, l.name, l.prov, l.ord, 'https://en.wikipedia.org/wiki/Provinces_of_Zimbabwe (accessed 2026-10-19): provincial capital'
FROM (VALUES
  ('TOWN_BULAWAYO',  'bulawayo',  'Bulawayo',  'PROVINCE_BULAWAYO',            10),
  ('TOWN_HARARE',    'harare',    'Harare',    'PROVINCE_HARARE',              20),
  ('TOWN_MUTARE',    'mutare',    'Mutare',    'PROVINCE_MANICALAND',          30),
  ('TOWN_BINDURA',   'bindura',   'Bindura',   'PROVINCE_MASHONALAND_CENTRAL', 40),
  ('TOWN_MARONDERA', 'marondera', 'Marondera', 'PROVINCE_MASHONALAND_EAST',    50),
  ('TOWN_CHINHOYI',  'chinhoyi',  'Chinhoyi',  'PROVINCE_MASHONALAND_WEST',    60),
  ('TOWN_MASVINGO',  'masvingo',  'Masvingo',  'PROVINCE_MASVINGO',            70),
  ('TOWN_LUPANE',    'lupane',    'Lupane',    'PROVINCE_MATABELELAND_NORTH',  80),
  ('TOWN_GWANDA',    'gwanda',    'Gwanda',    'PROVINCE_MATABELELAND_SOUTH',  90),
  ('TOWN_GWERU',     'gweru',     'Gweru',     'PROVINCE_MIDLANDS',            100)
) AS l(code, slug, name, prov, ord);

ALTER TABLE app.campaigns ADD COLUMN location_code text;
ALTER TABLE app.campaigns ADD CONSTRAINT fk_campaigns_location FOREIGN KEY (location_code) REFERENCES app.campaign_locations (code);
-- campaign_versions is append-only evidence: a new nullable column rewrites no snapshot and fires no row trigger;
-- content_sha256 covers the location only for versions that have one (owner.go snapshot)
ALTER TABLE app.campaign_versions ADD COLUMN location_code text;
ALTER TABLE app.campaign_versions ADD CONSTRAINT fk_campaign_versions_location FOREIGN KEY (location_code) REFERENCES app.campaign_locations (code);
ALTER TABLE app.campaign_versions DROP CONSTRAINT ck_campaign_versions_material;
ALTER TABLE app.campaign_versions ADD CONSTRAINT ck_campaign_versions_material CHECK (
  material_change_types <@ ARRAY['BENEFICIARY', 'GOAL_INCREASE', 'GOAL_CHANGE', 'CATEGORY', 'STORY', 'TITLE', 'SUMMARY', 'MEDIA', 'LOCATION']::text[]
  AND ((change_kind = 'MATERIAL_EDIT') = (cardinality(material_change_types) > 0)));

CALL app.apply_runtime_grants();
-- reference data: the runtime roles read it, only migrations change it
REVOKE INSERT, UPDATE, DELETE ON app.campaign_locations FROM fundzim_app;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app.campaign_versions DROP CONSTRAINT ck_campaign_versions_material;
ALTER TABLE app.campaign_versions ADD CONSTRAINT ck_campaign_versions_material CHECK (
  material_change_types <@ ARRAY['BENEFICIARY', 'GOAL_INCREASE', 'GOAL_CHANGE', 'CATEGORY', 'STORY', 'TITLE', 'SUMMARY', 'MEDIA']::text[]
  AND ((change_kind = 'MATERIAL_EDIT') = (cardinality(material_change_types) > 0))) NOT VALID;
ALTER TABLE app.campaign_versions DROP COLUMN location_code;
ALTER TABLE app.campaigns DROP COLUMN location_code;
DROP TABLE app.campaign_locations;
-- +goose StatementEnd
