-- Stage 7 performance dataset (stream P). Synthetic data only; for the scratch database fundzim_perf ONLY.
-- Never run against the shared development database `fundzim` or any real environment.
--
-- Run as the migrator (table owner), after `fundzimctl migrate up` on fundzim_perf:
--   docker compose exec -T fundzim-postgres psql -v ON_ERROR_STOP=1 -U fundzim_migrator -d fundzim_perf < tests/perf/seed.sql
-- (the local socket trusts the container's own users; otherwise pass the migrator URL from .env + tests/perf/env.sh)
--
-- Every row goes through the real triggers and CHECKs: status machines are walked step by step
-- (app.guard_transition), one history/event row per aggregate version (deferred require_* triggers), the
-- campaigns <-> campaign_versions composite FKs, campaign-media object purpose checks and reviewer not-self
-- checks. session_replication_role is NOT used.
--
-- Sizes (defaults below): 20 000 personal users, 6 staff, 500 organisations, 50 000 campaigns (~60% live:
-- ACTIVE/PAUSED/COMPLETED; the rest DRAFT/SUBMITTED/UNDER_REVIEW/CHANGES_REQUESTED/APPROVED/REJECTED/
-- SUSPENDED/CANCELLED/ARCHIVED), ~10% UNLISTED, all 11 categories, English and Shona titles/summaries.
\set ON_ERROR_STOP 1
\timing on
SET statement_timeout = 0;
SET lock_timeout = '10s';
SET client_min_messages = warning;

BEGIN;
SELECT setseed(0.20261009);

-- guard: refuse to run anywhere but the scratch database
DO $$ BEGIN
  IF current_database() <> 'fundzim_perf' THEN
    RAISE EXCEPTION 'seed.sql may only run against fundzim_perf (current: %)', current_database();
  END IF;
  IF EXISTS (SELECT 1 FROM app.campaigns) THEN
    RAISE EXCEPTION 'fundzim_perf already holds campaigns; drop and recreate the database first';
  END IF;
END $$;

-- ---- helpers (session-local) ------------------------------------------------------------------------------
CREATE FUNCTION pg_temp.uuid7(ts timestamptz) RETURNS uuid LANGUAGE sql VOLATILE AS $$
  SELECT (lpad(to_hex((extract(epoch FROM ts) * 1000)::bigint), 12, '0')
          || '7' || substr(md5(random()::text), 1, 3)
          || to_hex(8 + floor(random() * 4)::int) || substr(md5(random()::text), 1, 15))::uuid
$$;
CREATE FUNCTION pg_temp.pick(a text[]) RETURNS text LANGUAGE sql VOLATILE AS $$
  SELECT a[1 + floor(random() * cardinality(a))::int]
$$;
CREATE FUNCTION pg_temp.code(n int) RETURNS text LANGUAGE sql IMMUTABLE AS $$
  SELECT string_agg(substr('0123456789ABCDEFGHJKMNPQRSTVWXYZ', 1 + (get_byte(decode(md5('perf-code:' || n), 'hex'), i) % 32), 1), '' ORDER BY i)
  FROM generate_series(0, 9) i
$$;

CREATE TEMP TABLE w AS SELECT
  ARRAY['Tendai','Rutendo','Tatenda','Farai','Chipo','Nyasha','Tinashe','Kudzai','Tafadzwa','Rumbidzai','Simba','Tapiwa','Fadzai',
        'Munyaradzi','Nokuthula','Sipho','Thandiwe','Busisiwe','Blessing','Precious','Memory','Takudzwa','Anesu','Ruvimbo','Vimbai',
        'Shamiso','Tanaka','Panashe','Ropafadzo','Kundai','Chiedza','Tsitsi','Gift','Lovemore','Learnmore','Givemore','Tawanda',
        'Kudakwashe','Nomsa','Themba','Mandla','Sibusiso','Rudo','Chenai','Ngoni','Tonderai','Fungai','Tariro','Nyarai','Mufaro']::text[] AS first,
  ARRAY['Moyo','Ncube','Dube','Sibanda','Chikwanha','Mutasa','Mlambo','Nyathi','Gumbo','Chigumba','Mapfumo','Makoni','Banda','Phiri',
        'Mhlanga','Mpofu','Zhou','Shumba','Marufu','Chirwa','Mukanya','Hove','Madziva','Chiweshe','Mazvita','Matongo','Ndlovu',
        'Khumalo','Tshuma','Mudzingwa','Chaumba','Mandaza','Kanengoni','Mashingaidze','Chinembiri','Mukonoweshuro']::text[] AS last,
  ARRAY['Harare','Bulawayo','Mutare','Gweru','Masvingo','Chinhoyi','Kwekwe','Kadoma','Marondera','Bindura','Chitungwiza','Epworth',
        'Hwange','Victoria Falls','Kariba','Rusape','Chipinge','Zvishavane','Beitbridge','Gokwe','Mutoko','Murehwa','Nyanga',
        'Chiredzi','Plumtree','Lupane','Binga','Mbare','Highfield','Glen View','Budiriro','Mabvuku','Dzivarasekwa','Norton',
        'Ruwa','Shurugwi','Chegutu','Karoi','Mount Darwin','Mvurwi','Checheche','Chimanimani','Tsholotsho','Gwanda']::text[] AS place,
  ARRAY['kidney dialysis','heart surgery','cancer treatment','a hip replacement','cataract surgery','chemotherapy','insulin and diabetes care',
        'a wheelchair and physiotherapy','burn treatment','maternity care','HIV medication and nutrition','sickle cell treatment',
        'a prosthetic leg','eye surgery','spinal surgery','TB treatment']::text[] AS med,
  ARRAY['Parirenyatwa Hospital','Sally Mugabe Hospital','Mpilo Hospital','United Bulawayo Hospitals','Avenues Clinic','Chitungwiza Hospital',
        'Mutare Provincial Hospital','Gweru Provincial Hospital','Karanda Mission Hospital','Howard Hospital']::text[] AS hospital,
  ARRAY['Prince Edward School','Mabelreign Girls High','Goromonzi High','Mzilikazi High','Chaplin High','Hartzell High','Mutare Boys High',
        'Kutama College','St Ignatius College','Nyanga High','the University of Zimbabwe','NUST','Midlands State University',
        'Great Zimbabwe University','Harare Polytechnic','a primary school']::text[] AS school,
  ARRAY['floods','a house fire','a storm','a road accident','drought','a hailstorm','a collapsed roof','cyclone damage']::text[] AS event,
  ARRAY['tuckshop','tailoring shop','poultry project','market stall','welding workshop','hair salon','bakery','piggery','carpentry shop',
        'grinding mill','solar phone-charging stall','fish pond']::text[] AS biz,
  ARRAY['dogs','donkeys','cats','cattle','goats','pangolins','rhinos','orphaned elephants','horses']::text[] AS animal,
  ARRAY['Rubatsiro','Batsirai','Tsigirai','Rutsigiro','Ruyamuro','Yamurai']::text[] AS sh_help,
  ARRAY['rwekurapa','rwechikoro','rwemhuri','rwekuvaka','rwemvura','rwechipatara','rwevana','rwekudya','rwenharaunda']::text[] AS sh_kind,
  ARRAY['mwana','vana','chikoro','mvura','tsime','kereke','mhuri','musha','varwere','chipatara','dzidzo','nzara','mafashama','zvipfuyo',
        'bhizimisi','mukoma','hanzvadzi','ambuya','sekuru','nharaunda','kubatsira','kuchengeta','kudya','mbatya','mishonga','mari','fizi',
        'mabhuku','yunifomu','denga','imba','zvirimwa','mbeu','fetiraiza','gomba','bhiriji','rugare','tariro','rudo','kutenda']::text[] AS sh_word,
  ARRAY['urgent','community','family','children','school','water','clinic','hope','support','together','village','mother','father',
        'grandmother','orphans','widow','farmers','youth','girls','learners','patients','neighbours','church','borehole','fees','rent',
        'medicine','transport','food','shelter','books','uniforms','solar','roof','toilets','garden','seeds','livestock','repair','rebuild']::text[] AS en_word,
  ARRAY['Every dollar goes directly to the hospital account through the payment provider.',
        'We have already raised part of the cost from family and the church.',
        'The family has no medical aid and cannot cover the balance.',
        'Receipts and updates will be posted here as the work progresses.',
        'The community committee meets every month to account for the funds.',
        'Thank you to everyone who has shared this campaign on WhatsApp.',
        'The school has confirmed the fees statement and the bank details.',
        'Rains destroyed the old structure last season and nobody has been able to rebuild it.',
        'Tichatumira mifananidzo nemashoko ese pano kuti munzwe kuti mari yashandiswa sei.',
        'Mhuri yakatambura zvikuru mushure menjodzi iyi uye inoda rubatsiro rwenyu.',
        'Vana vanofamba makiromita akawanda zuva rega rega kuenda kuchikoro.',
        'Tinotenda zvikuru nerubatsiro rwenyu rwese, zvisinei nekuti rudiki sei.',
        'Doctors have recommended treatment within the next three months.',
        'The borehole will serve more than two hundred households and the local clinic.',
        'We will publish a full breakdown of costs once the project is complete.',
        'The quotation from the contractor is attached to the campaign review documents.',
        'Our goal covers materials, transport and labour only.',
        'Mari ichashandiswa kutenga mishonga, kubhadhara chipatara uye kufambisa murwere.',
        'Hurumende yakavimbisa rubatsiro asi hapana chakauya kusvika parizvino.',
        'Please keep the family in your thoughts and prayers.']::text[] AS sentence;

-- ---- staff and personal users ---------------------------------------------------------------------------
CREATE TEMP TABLE staff AS
SELECT n, pg_temp.uuid7(now() - interval '800 days' + n * interval '1 hour') AS id FROM generate_series(1, 6) n;
INSERT INTO app.users (id, account_kind, status, created_at, updated_at)
SELECT id, 'STAFF', 'ACTIVE', now() - interval '800 days', now() - interval '800 days' FROM staff;
INSERT INTO app.user_profiles (id, user_id, display_name)
SELECT pg_temp.uuid7(now() - interval '800 days'), id, 'Perf Staff ' || n FROM staff;

CREATE TEMP TABLE u AS
SELECT n, pg_temp.uuid7(ts) AS id, ts, pg_temp.pick(w.first) || ' ' || pg_temp.pick(w.last) AS name, random() < 0.6 AS verified
FROM generate_series(1, 20000) n, w, LATERAL (SELECT now() - (random() * 760 + 30) * interval '1 day' AS ts) t;
ALTER TABLE u ADD PRIMARY KEY (n);
INSERT INTO app.users (id, account_kind, status, kyc_level, created_at, updated_at)
SELECT id, 'USER', 'ACTIVE', CASE WHEN verified THEN 'IDENTITY_VERIFIED' ELSE 'BASIC_VERIFIED' END, ts, ts FROM u;
INSERT INTO app.user_profiles (id, user_id, display_name, created_at, updated_at)
SELECT pg_temp.uuid7(ts), id, name, ts, ts FROM u;

-- KYC profiles (60% identity verified, the rest basic) with one profile event each (deferred check)
CREATE TEMP TABLE kp AS SELECT u.n, u.id AS user_id, pg_temp.uuid7(u.ts) AS id, u.ts, u.verified FROM u;
INSERT INTO kyc.verification_profiles (id, user_id, level, status, risk_level, pep_status, level_granted_at, level_expires_at,
  status_changed_at, policy_version, created_at, updated_at)
SELECT id, user_id, CASE WHEN verified THEN 'IDENTITY_VERIFIED' ELSE 'BASIC_VERIFIED' END, 'ACTIVE', 'STANDARD', 'NONE', ts + interval '1 day',
  ts + interval '3 years', ts, 'kyc-v1', ts, ts FROM kp;
INSERT INTO kyc.profile_events (id, profile_id, aggregate_version, from_level, to_level, from_status, to_status, reason_code, actor_type, occurred_at)
SELECT pg_temp.uuid7(ts), id, 1, NULL, CASE WHEN verified THEN 'IDENTITY_VERIFIED' ELSE 'BASIC_VERIFIED' END, NULL, 'ACTIVE', 'PERF_SEED', 'SYSTEM', ts
FROM kp;

-- ---- organisations (each with an ORG_ADMIN member, deferred require_admin) -------------------------------
CREATE TEMP TABLE o AS
SELECT g.n, pg_temp.uuid7(now() - interval '700 days') AS id, (SELECT u.id FROM u WHERE u.n = 19000 + g.n) AS admin_id,
  pg_temp.pick(ARRAY['Hope','Tariro','Rudo','Ubuntu','Simukai','Batanai','Kubatana','Vimbai','Ziviso','Mutsa']) || ' '
  || pg_temp.pick(ARRAY['Trust','Foundation','Community Trust','Ministries','Care Centre','Children''s Home','Mission','Welfare Society'])
  || ' ' || pg_temp.pick(w.place) AS name
FROM generate_series(1, 500) g(n), w;
INSERT INTO app.organisations (id, display_name, slug, org_type, status, created_by_user_id, created_at, updated_at)
SELECT id, name, 'perf-org-' || n, pg_temp.pick(ARRAY['TRUST','PVO','FAITH_BASED','COMMUNITY_BASED','SCHOOL','HEALTH_INSTITUTION','COMPANY']),
  'ACTIVE', admin_id, now() - interval '700 days', now() - interval '700 days' FROM o;
INSERT INTO app.organisation_members (id, organisation_id, user_id, organisation_role_id, status)
SELECT pg_temp.uuid7(now() - interval '700 days'), id, admin_id, md5('org_role:ORG_ADMIN')::uuid, 'ACTIVE' FROM o;

-- ---- campaign plan -------------------------------------------------------------------------------------
-- target status distribution (cumulative): live 60% = ACTIVE 45 + PAUSED 3 + COMPLETED 12
CREATE TEMP TABLE p AS
WITH base AS (
  SELECT n, random() AS rs, random() AS rc, random() AS rv, random() AS ro, random() AS rl, random() AS rg,
         (SELECT array_agg(code ORDER BY display_order) FROM app.campaign_categories) AS cats
  FROM generate_series(1, 50000) n
)
SELECT n,
  CASE WHEN rs < 0.45 THEN 'ACTIVE' WHEN rs < 0.48 THEN 'PAUSED' WHEN rs < 0.60 THEN 'COMPLETED'
       WHEN rs < 0.74 THEN 'DRAFT' WHEN rs < 0.78 THEN 'SUBMITTED' WHEN rs < 0.81 THEN 'UNDER_REVIEW'
       WHEN rs < 0.83 THEN 'CHANGES_REQUESTED' WHEN rs < 0.85 THEN 'APPROVED' WHEN rs < 0.87 THEN 'SUSPENDED'
       WHEN rs < 0.90 THEN 'REJECTED' WHEN rs < 0.93 THEN 'CANCELLED' ELSE 'ARCHIVED' END AS target,
  -- skewed category spread across all 11 (medical/education/funeral most common)
  cats[CASE WHEN rc < 0.22 THEN 1 WHEN rc < 0.38 THEN 2 WHEN rc < 0.48 THEN 3 WHEN rc < 0.60 THEN 4 WHEN rc < 0.66 THEN 5
            WHEN rc < 0.76 THEN 6 WHEN rc < 0.82 THEN 7 WHEN rc < 0.87 THEN 8 WHEN rc < 0.93 THEN 9 WHEN rc < 0.96 THEN 10 ELSE 11 END] AS category,
  CASE WHEN rv < 0.10 THEN 'UNLISTED' ELSE 'PUBLIC' END AS visibility,
  ro < 0.08 AS org_owned, rl < 0.35 AS shona, rg < 0.15 AS zwg
FROM base;
ALTER TABLE p ADD PRIMARY KEY (n);
UPDATE p SET org_owned = true WHERE category IN ('CHARITY', 'RELIGIOUS');  -- these categories require an organisation

ALTER TABLE p ADD COLUMN path text[], ADD COLUMN id uuid, ADD COLUMN owner_user uuid, ADD COLUMN owner_org uuid, ADD COLUMN created_by uuid,
  ADD COLUMN tier text, ADD COLUMN title text, ADD COLUMN summary text, ADD COLUMN story text, ADD COLUMN goal bigint, ADD COLUMN currency char(3),
  ADD COLUMN code text, ADD COLUMN slug text, ADD COLUMN created_at timestamptz, ADD COLUMN submitted_at timestamptz,
  ADD COLUMN approved_at timestamptz, ADD COLUMN published_at timestamptz, ADD COLUMN ended_at timestamptz,
  ADD COLUMN v1 uuid, ADD COLUMN v2 uuid, ADD COLUMN cover uuid, ADD COLUMN gallery uuid, ADD COLUMN ben uuid, ADD COLUMN reviewer uuid,
  ADD COLUMN approver2 uuid, ADD COLUMN basis text;

UPDATE p SET path = CASE target
  WHEN 'DRAFT'             THEN ARRAY[]::text[]
  WHEN 'CANCELLED'         THEN ARRAY['CANCELLED']
  WHEN 'SUBMITTED'         THEN ARRAY['SUBMITTED']
  WHEN 'UNDER_REVIEW'      THEN ARRAY['SUBMITTED','UNDER_REVIEW']
  WHEN 'CHANGES_REQUESTED' THEN ARRAY['SUBMITTED','UNDER_REVIEW','CHANGES_REQUESTED']
  WHEN 'REJECTED'          THEN ARRAY['SUBMITTED','UNDER_REVIEW','REJECTED']
  WHEN 'APPROVED'          THEN ARRAY['SUBMITTED','UNDER_REVIEW','APPROVED']
  WHEN 'ACTIVE'            THEN ARRAY['SUBMITTED','UNDER_REVIEW','APPROVED','ACTIVE']
  WHEN 'PAUSED'            THEN ARRAY['SUBMITTED','UNDER_REVIEW','APPROVED','ACTIVE','PAUSED']
  WHEN 'COMPLETED'         THEN ARRAY['SUBMITTED','UNDER_REVIEW','APPROVED','ACTIVE','COMPLETED']
  WHEN 'SUSPENDED'         THEN ARRAY['SUBMITTED','UNDER_REVIEW','APPROVED','ACTIVE','SUSPENDED']
  WHEN 'ARCHIVED'          THEN ARRAY['SUBMITTED','UNDER_REVIEW','APPROVED','ACTIVE','COMPLETED','ARCHIVED'] END;

-- owners: personal users 1..18000 (power-law-ish: some organisers run several campaigns), orgs 1..500
UPDATE p SET owner_user = u.id
FROM (SELECT n AS pn, 1 + floor(power(random(), 1.6) * 18000)::int AS un FROM p) x JOIN u ON u.n = x.un
WHERE p.n = x.pn AND NOT p.org_owned;
UPDATE p SET owner_org = o.id, created_by = o.admin_id FROM o WHERE p.org_owned AND o.n = 1 + (p.n % 500);
UPDATE p SET created_by = owner_user WHERE owner_user IS NOT NULL;
UPDATE p SET tier = k.default_risk_tier FROM app.campaign_categories k WHERE k.code = p.category;
UPDATE p SET basis = CASE WHEN org_owned THEN 'ORGANISATION_OWN_CAUSE' WHEN random() < 0.5 THEN 'SELF_FUNDRAISING' ELSE 'INDIVIDUAL_FOR_OTHERS' END;

-- timeline (created within the last ~2 years; each step after the previous)
UPDATE p SET created_at = now() - (random() * 720 + 10) * interval '1 day';
UPDATE p SET submitted_at = created_at + (random() * 5 + 0.1) * interval '1 day' WHERE 'SUBMITTED' = ANY (path);
UPDATE p SET approved_at = submitted_at + (random() * 3 + 0.1) * interval '1 day' WHERE 'APPROVED' = ANY (path);
UPDATE p SET published_at = approved_at + (random() * 2) * interval '1 day' WHERE 'ACTIVE' = ANY (path);
UPDATE p SET ended_at = LEAST(now() - interval '1 hour', published_at + (random() * 120 + 5) * interval '1 day') WHERE cardinality(path) >= 5;
UPDATE p SET ended_at = LEAST(now() - interval '1 hour', created_at + (random() * 20 + 1) * interval '1 day') WHERE target = 'CANCELLED';
UPDATE p SET id = pg_temp.uuid7(created_at);

-- content (English ~65%, Shona ~35%; varied words)
UPDATE p SET title = CASE WHEN NOT shona THEN CASE category
    WHEN 'MEDICAL'          THEN format('Help %s %s get %s at %s', pg_temp.pick(w.first), pg_temp.pick(w.last), pg_temp.pick(w.med), pg_temp.pick(w.hospital))
    WHEN 'EDUCATION'        THEN format('%s fees for %s %s at %s', pg_temp.pick(ARRAY['School','Tuition','Exam','Boarding']), pg_temp.pick(w.first), pg_temp.pick(w.last), pg_temp.pick(w.school))
    WHEN 'EMERGENCY'        THEN format('Emergency help for the %s family after %s in %s', pg_temp.pick(w.last), pg_temp.pick(w.event), pg_temp.pick(w.place))
    WHEN 'FUNERAL'          THEN format('Funeral costs for %s %s %s of %s', pg_temp.pick(ARRAY['Mbuya','Sekuru','Baba','Amai','our brother','our sister']), pg_temp.pick(w.first), pg_temp.pick(w.last), pg_temp.pick(w.place))
    WHEN 'DISASTER_RELIEF'  THEN format('%s relief for families in %s', pg_temp.pick(ARRAY['Flood','Drought','Cyclone','Storm','Fire']), pg_temp.pick(w.place))
    WHEN 'COMMUNITY'        THEN format('%s for %s %s', pg_temp.pick(ARRAY['A borehole','A new classroom block','Solar lights','A footbridge','Clinic repairs','A community garden']), pg_temp.pick(w.place), pg_temp.pick(ARRAY['village','ward','community','primary school']))
    WHEN 'CHARITY'          THEN format('Feed %s children at a care centre in %s', 20 + floor(random() * 480)::int, pg_temp.pick(w.place))
    WHEN 'RELIGIOUS'        THEN format('%s for %s church in %s', pg_temp.pick(ARRAY['A new roof','Chairs and a sound system','Building completion','Toilets']), pg_temp.pick(ARRAY['St Mary''s','Methodist','Apostolic','Anglican','Baptist','Seventh-day Adventist','Pentecostal']), pg_temp.pick(w.place))
    WHEN 'BUSINESS_SUPPORT' THEN format('Help %s restart a %s in %s', pg_temp.pick(w.first), pg_temp.pick(w.biz), pg_temp.pick(w.place))
    WHEN 'ANIMAL_WELFARE'   THEN format('Veterinary care for rescued %s in %s', pg_temp.pick(w.animal), pg_temp.pick(w.place))
    ELSE format('Support %s %s with %s in %s', pg_temp.pick(w.first), pg_temp.pick(w.last), pg_temp.pick(w.en_word), pg_temp.pick(w.place)) END
  ELSE CASE category
    WHEN 'MEDICAL'          THEN format('%s rwekurapa %s %s kuchipatara che%s', pg_temp.pick(w.sh_help), pg_temp.pick(w.first), pg_temp.pick(w.last), pg_temp.pick(w.place))
    WHEN 'EDUCATION'        THEN format('Mari yechikoro ya%s %s ku%s', pg_temp.pick(w.first), pg_temp.pick(w.last), pg_temp.pick(w.place))
    WHEN 'FUNERAL'          THEN format('Mari yemariro a%s %s ku%s', pg_temp.pick(ARRAY['Sekuru','Mbuya','Baba','Amai']), pg_temp.pick(w.last), pg_temp.pick(w.place))
    WHEN 'COMMUNITY'        THEN format('Tsime remvura renharaunda ye%s', pg_temp.pick(w.place))
    WHEN 'RELIGIOUS'        THEN format('Kuvaka kereke ye%s ku%s', pg_temp.pick(ARRAY['Methodist','Apostolic','Anglican','Katorike','Baptist']), pg_temp.pick(w.place))
    ELSE format('%s %s mhuri ya%s ku%s', pg_temp.pick(w.sh_help), pg_temp.pick(w.sh_kind), pg_temp.pick(w.last), pg_temp.pick(w.place)) END
  END
FROM w;
UPDATE p SET title = left(title, 120) WHERE length(title) > 120;
UPDATE p SET summary = left(format('%s %s %s, %s, %s.', title || '.', pg_temp.pick(w.sentence),
    initcap(CASE WHEN shona THEN pg_temp.pick(w.sh_word) ELSE pg_temp.pick(w.en_word) END),
    CASE WHEN shona THEN pg_temp.pick(w.sh_word) ELSE pg_temp.pick(w.en_word) END,
    CASE WHEN shona THEN pg_temp.pick(w.sh_word) ELSE pg_temp.pick(w.en_word) END), 300)
FROM w;
UPDATE p SET story = summary || E'\n\n' || (SELECT string_agg(pg_temp.pick(w.sentence) || ' ' || pg_temp.pick(w.en_word) || ' ' || pg_temp.pick(w.sh_word) || '.', ' ')
  FROM generate_series(1, 4 + (p.n % 9)), w);
UPDATE p SET currency = CASE WHEN zwg THEN 'ZWG' ELSE 'USD' END,
  goal = CASE WHEN zwg THEN (5000 + floor(random() * 2000000)::bigint) * 100 ELSE (100 + floor(power(random(), 2) * 49900)::bigint) * 100 END;
UPDATE p SET code = pg_temp.code(n);
UPDATE p SET slug = regexp_replace(left(trim(both '-' FROM regexp_replace(lower(title), '[^a-z0-9]+', '-', 'g')), 80), '-+$', '') || '-' || lower(code);

-- versions (one SUBMISSION; ~15% of reviewed campaigns also have an earlier version and a RESUBMISSION), media ids
UPDATE p SET v2 = pg_temp.uuid7(submitted_at) WHERE submitted_at IS NOT NULL;
UPDATE p SET v1 = pg_temp.uuid7(submitted_at - interval '2 days') WHERE v2 IS NOT NULL AND random() < 0.15 AND 'UNDER_REVIEW' = ANY (path);
UPDATE p SET cover = pg_temp.uuid7(created_at + interval '1 hour') WHERE cardinality(path) > 0 OR random() < 0.5;
UPDATE p SET gallery = pg_temp.uuid7(created_at + interval '2 hours') WHERE cover IS NOT NULL AND random() < 0.25;
UPDATE p SET reviewer = s1.id, approver2 = s2.id FROM staff s1, staff s2 WHERE s1.n = 1 + (p.n % 3) AND s2.n = 4 + (p.n % 3);

-- ---- SELF beneficiaries for personal campaigns that were submitted (APPROVED via the verification machine) ---
CREATE TEMP TABLE b AS
SELECT owner_user AS user_id, pg_temp.uuid7(min(created_at)) AS id, min(created_at) AS ts
FROM p WHERE owner_user IS NOT NULL AND submitted_at IS NOT NULL GROUP BY owner_user;
INSERT INTO app.beneficiaries (id, owner_user_id, created_by, beneficiary_type, display_name, beneficiary_user_id, relationship_type,
  authority_basis, status, policy_version, created_at, updated_at)
SELECT b.id, b.user_id, b.user_id, 'SELF', split_part(u.name, ' ', 1), b.user_id, 'SELF', 'NOT_REQUIRED', 'DRAFT', 'beneficiary-v1', b.ts, b.ts
FROM b JOIN u ON u.id = b.user_id;
UPDATE app.beneficiaries x SET status = 'SUBMITTED', submitted_at = b.ts + interval '1 hour' FROM b WHERE x.id = b.id;
UPDATE app.beneficiaries x SET status = 'UNDER_REVIEW', assigned_to = (SELECT id FROM staff WHERE n = 1), assigned_at = b.ts + interval '2 hours' FROM b WHERE x.id = b.id;
UPDATE app.beneficiaries x SET status = 'APPROVED', decided_at = b.ts + interval '3 hours' FROM b WHERE x.id = b.id;
INSERT INTO app.beneficiary_events (id, beneficiary_id, beneficiary_version, event_type, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT pg_temp.uuid7(b.ts), b.id, s.v, s.ev, s.f, s.t, s.at, CASE WHEN s.at = 'USER' THEN b.user_id ELSE (SELECT id FROM staff WHERE n = 1) END,
  CASE WHEN s.at = 'STAFF' THEN 'PERF_SEED' END, b.ts + s.v * interval '1 hour'
FROM b, (VALUES (1, 'CREATED', NULL, 'DRAFT', 'USER'), (2, 'SUBMITTED', 'DRAFT', 'SUBMITTED', 'USER'),
                (3, 'ASSIGNED', 'SUBMITTED', 'UNDER_REVIEW', 'STAFF'), (4, 'APPROVED', 'UNDER_REVIEW', 'APPROVED', 'STAFF')) s(v, ev, f, t, at);
UPDATE p SET ben = b.id FROM b WHERE b.user_id = p.owner_user AND p.submitted_at IS NOT NULL;

-- ---- campaigns (inserted as DRAFT, then walked through the machine) --------------------------------------
INSERT INTO app.campaigns (id, public_code, slug, owner_user_id, owner_organisation_id, created_by, category_code, title, summary, story,
  goal_amount_minor, goal_currency, status, visibility, risk_tier, policy_version, fundraising_basis, submitted_at, created_at, updated_at)
SELECT id, code, slug, owner_user, owner_org, created_by, category, title, summary, story, goal, currency, 'DRAFT', visibility, tier,
  'campaign-v1', basis, submitted_at, created_at, created_at
FROM p;
INSERT INTO app.campaign_goals (id, campaign_id, goal_version, amount_minor, currency, set_by, created_at)
SELECT pg_temp.uuid7(created_at), id, 1, goal, currency, created_by, created_at FROM p;

-- media objects: original + processed derivative per image, both CLEAN, purpose CAMPAIGN_MEDIA
CREATE TEMP TABLE m AS
SELECT p.id AS campaign_id, x.media_id, x.kind, x.pos, p.created_by, p.created_at AS ts, p.title,
  pg_temp.uuid7(p.created_at) AS orig_id, pg_temp.uuid7(p.created_at) AS proc_id
FROM p CROSS JOIN LATERAL (VALUES (p.cover, 'COVER', 0), (p.gallery, 'GALLERY', 1)) x(media_id, kind, pos)
WHERE x.media_id IS NOT NULL;
CREATE TEMP TABLE so AS
SELECT orig_id AS id, created_by, ts, 'image/jpeg' AS declared FROM m UNION ALL SELECT proc_id, created_by, ts, 'image/jpeg' FROM m;
INSERT INTO app.stored_objects (id, bucket_class, purpose, owner_module, quarantine_key, declared_content_type, sniffed_content_type,
  content_sha256, size_bytes, scan_status, classification, retention_class, uploaded_by_user_id, created_at, updated_at)
SELECT id, 'PUBLIC_MEDIA', 'CAMPAIGN_MEDIA', 'campaigns', 'quarantine/' || id, declared, 'image/jpeg', sha256(id::text::bytea),
  50000 + floor(random() * 2000000)::bigint, 'UPLOADED', 'C0', 'OPERATIONAL', created_by, ts, ts FROM so;
UPDATE app.stored_objects x SET scan_status = 'QUARANTINED' FROM so WHERE x.id = so.id;
UPDATE app.stored_objects x SET scan_status = 'SCANNING', scan_attempts = 1, scan_started_at = so.ts + interval '1 minute' FROM so WHERE x.id = so.id;
UPDATE app.stored_objects x SET scan_status = 'CLEAN', promoted_key = 'objects/' || so.id, promoted_at = so.ts + interval '2 minutes',
  scan_engine = 'perf-seed (synthetic; no scan)', scanned_at = so.ts + interval '2 minutes' FROM so WHERE x.id = so.id;

INSERT INTO app.campaign_media (id, campaign_id, stored_object_id, kind, position, alt_text, status, created_by, created_at, updated_at)
SELECT media_id, campaign_id, orig_id, kind, pos, left('Photo: ' || title, 250), 'UPLOADED', created_by, ts, ts FROM m;
UPDATE app.campaign_media x SET status = 'QUARANTINED' FROM m WHERE x.id = m.media_id;
UPDATE app.campaign_media x SET status = 'SCANNING' FROM m WHERE x.id = m.media_id;
UPDATE app.campaign_media x SET status = 'APPROVED', processed_object_id = m.proc_id, content_type = 'image/jpeg',
  width = 1600, height = 900 + (floor(random() * 3) * 150)::int, approved_at = m.ts + interval '3 minutes' FROM m WHERE x.id = m.media_id;
INSERT INTO app.campaign_media_events (id, media_id, media_version, event_type, from_status, to_status, actor_type, actor_id, occurred_at)
SELECT pg_temp.uuid7(m.ts), m.media_id, s.v, s.ev, s.f, s.t, s.at, CASE WHEN s.at = 'USER' THEN m.created_by END, m.ts + s.v * interval '1 minute'
FROM m, (VALUES (1, 'UPLOADED', NULL, 'UPLOADED', 'USER'), (2, 'QUARANTINED', 'UPLOADED', 'QUARANTINED', 'SYSTEM'),
                (3, 'SCAN_STARTED', 'QUARANTINED', 'SCANNING', 'SYSTEM'), (4, 'APPROVED', 'SCANNING', 'APPROVED', 'SYSTEM')) s(v, ev, f, t, at);

-- versions (append-only snapshots)
INSERT INTO app.campaign_versions (id, campaign_id, version_number, change_kind, title, summary, story, category_code, goal_amount_minor,
  goal_currency, beneficiary_id, beneficiary_disclosure, media_ids, content_sha256, created_by, created_at)
SELECT v1, id, 1, 'SUBMISSION', title, summary, story, category, goal, currency, ben, 'NONE', array_remove(ARRAY[cover, gallery], NULL),
  sha256(convert_to(title || summary || story || 'v1', 'UTF8')), created_by, submitted_at - interval '2 days'
FROM p WHERE v1 IS NOT NULL;
INSERT INTO app.campaign_versions (id, campaign_id, version_number, change_kind, title, summary, story, category_code, goal_amount_minor,
  goal_currency, beneficiary_id, beneficiary_disclosure, media_ids, content_sha256, created_by, created_at)
SELECT v2, id, CASE WHEN v1 IS NULL THEN 1 ELSE 2 END, CASE WHEN v1 IS NULL THEN 'SUBMISSION' ELSE 'RESUBMISSION' END, title, summary, story,
  category, goal, currency, ben, CASE WHEN ben IS NOT NULL AND n % 3 = 0 THEN 'DISPLAY_NAME' ELSE 'NONE' END,
  array_remove(ARRAY[cover, gallery], NULL), sha256(convert_to(title || summary || story, 'UTF8')), created_by, submitted_at
FROM p WHERE v2 IS NOT NULL;

-- walk the lifecycle: one UPDATE per step (each bumps campaigns.version; history rows are added below)
DO $$
DECLARE k int;
BEGIN
  FOR k IN 1..6 LOOP
    UPDATE app.campaigns c SET
      status              = p.path[k],
      pending_version_id  = CASE p.path[k] WHEN 'SUBMITTED' THEN p.v2 WHEN 'APPROVED' THEN NULL ELSE c.pending_version_id END,
      approved_version_id = CASE WHEN p.path[k] = 'APPROVED' THEN p.v2 ELSE c.approved_version_id END,
      approved_at         = CASE WHEN p.path[k] = 'APPROVED' THEN p.approved_at ELSE c.approved_at END,
      published_at        = CASE WHEN p.path[k] = 'ACTIVE' THEN p.published_at ELSE c.published_at END,
      paused_at           = CASE WHEN p.path[k] = 'PAUSED' THEN p.ended_at END,
      suspended_at        = CASE WHEN p.path[k] = 'SUSPENDED' THEN p.ended_at END,
      completed_at        = CASE WHEN p.path[k] = 'COMPLETED' THEN p.ended_at ELSE c.completed_at END,
      completion_reason   = CASE WHEN p.path[k] = 'COMPLETED' THEN (ARRAY['GOAL_REACHED','ORGANISER_COMPLETED','EXPIRED'])[1 + p.n % 3] ELSE c.completion_reason END,
      cancelled_at        = CASE WHEN p.path[k] = 'CANCELLED' THEN p.ended_at ELSE c.cancelled_at END,
      archived_at         = CASE WHEN p.path[k] = 'ARCHIVED' THEN p.ended_at + interval '30 days' END
    FROM p WHERE c.id = p.id AND cardinality(p.path) >= k;
  END LOOP;
END $$;

-- history: exactly one row per campaign version (version 1 = created as DRAFT; version k+1 = step k)
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, event_type, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT pg_temp.uuid7(p.created_at), p.id, 1, 'CREATED', NULL, 'DRAFT', 'USER', p.created_by, NULL, p.created_at FROM p;
INSERT INTO app.campaign_status_history (id, campaign_id, campaign_version, event_type, from_status, to_status, actor_type, actor_id, reason_code, occurred_at)
SELECT pg_temp.uuid7(t.at), p.id, s.k + 1, 'STATUS_CHANGED', COALESCE(p.path[s.k - 1], 'DRAFT'), p.path[s.k], t.actor_type,
  CASE t.actor_type WHEN 'STAFF' THEN p.reviewer WHEN 'USER' THEN p.created_by END,
  CASE WHEN t.actor_type = 'STAFF' THEN 'PERF_' || p.path[s.k] END, t.at
FROM p CROSS JOIN LATERAL generate_subscripts(p.path, 1) s(k)
CROSS JOIN LATERAL (SELECT
  CASE WHEN p.path[s.k] IN ('UNDER_REVIEW','APPROVED','REJECTED','CHANGES_REQUESTED','SUSPENDED') THEN 'STAFF'
       WHEN p.path[s.k] = 'ARCHIVED' THEN 'SYSTEM' ELSE 'USER' END AS actor_type,
  CASE p.path[s.k] WHEN 'SUBMITTED' THEN p.submitted_at WHEN 'UNDER_REVIEW' THEN p.submitted_at + interval '1 hour'
    WHEN 'APPROVED' THEN p.approved_at WHEN 'ACTIVE' THEN p.published_at WHEN 'ARCHIVED' THEN p.ended_at + interval '30 days'
    WHEN 'CHANGES_REQUESTED' THEN p.submitted_at + interval '1 day' WHEN 'REJECTED' THEN p.submitted_at + interval '1 day'
    ELSE p.ended_at END AS at) t;

-- reviews: open (QUEUED for SUBMITTED, IN_REVIEW for UNDER_REVIEW) or DECIDED with the matching outcome
CREATE TEMP TABLE r AS
SELECT pg_temp.uuid7(p.submitted_at) AS id, p.id AS campaign_id, p.v2 AS version_id, p.tier, p.tier = 'HIGH' AS second, p.target,
  p.reviewer, p.approver2, p.submitted_at AS ts,
  CASE WHEN 'APPROVED' = ANY (p.path) THEN 'APPROVE' WHEN p.target = 'REJECTED' THEN 'REJECT'
       WHEN p.target = 'CHANGES_REQUESTED' THEN 'REQUEST_CHANGES' END AS outcome
FROM p WHERE p.submitted_at IS NOT NULL;
INSERT INTO app.campaign_reviews (id, campaign_id, campaign_version_id, policy_version, risk_tier, requires_second_approval, kind, status, created_at, updated_at)
SELECT id, campaign_id, version_id, 'campaign-v1', tier, second, 'SUBMISSION', 'QUEUED', ts, ts FROM r;
UPDATE app.campaign_reviews x SET status = 'ASSIGNED', assigned_to = r.reviewer, assigned_at = r.ts + interval '30 minutes'
FROM r WHERE x.id = r.id AND r.target <> 'SUBMITTED';
UPDATE app.campaign_reviews x SET status = 'IN_REVIEW', started_at = r.ts + interval '1 hour' FROM r WHERE x.id = r.id AND r.target <> 'SUBMITTED';
UPDATE app.campaign_reviews x SET status = 'DECIDED', outcome = r.outcome, decided_by = r.reviewer, decided_at = r.ts + interval '1 day',
  second_approver = CASE WHEN r.second AND r.outcome = 'APPROVE' THEN r.approver2 END,
  reason_code = 'PERF_' || r.outcome, note = 'Synthetic review decision (performance dataset).'
FROM r WHERE x.id = r.id AND r.outcome IS NOT NULL;

-- ---- owner updates (published), incl. one campaign with a long history ------------------------------------
CREATE TEMP TABLE up AS
SELECT pg_temp.uuid7(p.published_at) AS id, p.id AS campaign_id, p.created_by, p.shona,
  LEAST(now() - interval '1 minute', p.published_at + (g * 3 + random() * 2) * interval '1 day') AS ts, g
FROM p CROSS JOIN LATERAL generate_series(1, 1 + (p.n % 5)) g
WHERE p.published_at IS NOT NULL AND p.n % 10 < 3;
INSERT INTO up
SELECT pg_temp.uuid7(p.published_at), p.id, p.created_by, p.shona, p.published_at + g * interval '6 hours', g
FROM (SELECT * FROM p WHERE target = 'ACTIVE' AND visibility = 'PUBLIC' ORDER BY n LIMIT 1) p, generate_series(1, 300) g;
INSERT INTO app.campaign_updates (id, campaign_id, author_id, title, body, status, submitted_at, published_at, created_at, updated_at)
SELECT up.id, up.campaign_id, up.created_by,
  CASE WHEN up.shona THEN 'Mashoko matsva ' || up.g ELSE 'Progress update ' || up.g END,
  pg_temp.pick(w.sentence) || ' ' || pg_temp.pick(w.sentence) || ' ' || pg_temp.pick(w.sentence),
  'PUBLISHED', up.ts, up.ts, up.ts, up.ts
FROM up, w;
INSERT INTO app.campaign_update_events (id, update_id, campaign_id, update_version, event_type, from_status, to_status, actor_type, actor_id, occurred_at)
SELECT pg_temp.uuid7(ts), id, campaign_id, 1, 'PUBLISHED', NULL, 'PUBLISHED', 'USER', created_by, ts FROM up;

-- ---- staff picks (6 live public campaigns) and public reports ---------------------------------------------
INSERT INTO app.campaign_featured (id, campaign_id, position, note, added_by, added_at)
SELECT pg_temp.uuid7(now()), id, row_number() OVER (ORDER BY n), 'Staff pick (performance dataset)', (SELECT id FROM staff WHERE n = 4), now() - interval '1 day'
FROM (SELECT * FROM p WHERE target = 'ACTIVE' AND visibility = 'PUBLIC' AND n % 997 = 0 ORDER BY n LIMIT 6) x;

INSERT INTO app.campaign_reports (id, campaign_id, reason, details, reporter_key, report_day, status, created_at, updated_at)
SELECT pg_temp.uuid7(ts), id, reason, CASE WHEN reason = 'OTHER' THEN 'Synthetic report details for performance testing.' END,
  sha256(('perf-reporter:' || g)::bytea), ts::date, 'OPEN', ts, ts
FROM (SELECT p.id, g, now() - random() * interval '60 days' AS ts,
        (ARRAY['MISLEADING_INFORMATION','IMPERSONATION','SUSPICIOUS_ACTIVITY','INAPPROPRIATE_CONTENT','PRIVACY_CONCERN','OTHER'])[1 + g % 6] AS reason
      FROM generate_series(1, 2000) g JOIN p ON p.n = 1 + (g * 37) % 50000
      WHERE p.published_at IS NOT NULL) x;
UPDATE app.campaign_reports SET status = 'DISMISSED', triaged_by = (SELECT id FROM staff WHERE n = 2), triaged_at = created_at + interval '1 day',
  triage_reason_code = 'PERF_DISMISSED', triage_note = 'Synthetic triage.'
WHERE id IN (SELECT id FROM app.campaign_reports ORDER BY id LIMIT 400);

COMMIT;  -- deferred require_history / require_event / require_admin checks run here

ANALYZE;

SELECT status, visibility, count(*) FROM app.campaigns GROUP BY 1, 2 ORDER BY 1, 2;
SELECT category_code, count(*) FILTER (WHERE status IN ('ACTIVE','PAUSED','COMPLETED') AND visibility = 'PUBLIC') AS live_public, count(*)
FROM app.campaigns GROUP BY 1 ORDER BY 1;
SELECT 'users' t, count(*) FROM app.users UNION ALL SELECT 'campaign_versions', count(*) FROM app.campaign_versions
UNION ALL SELECT 'campaign_status_history', count(*) FROM app.campaign_status_history UNION ALL SELECT 'campaign_reviews', count(*) FROM app.campaign_reviews
UNION ALL SELECT 'campaign_media', count(*) FROM app.campaign_media UNION ALL SELECT 'stored_objects', count(*) FROM app.stored_objects
UNION ALL SELECT 'campaign_updates', count(*) FROM app.campaign_updates UNION ALL SELECT 'campaign_reports', count(*) FROM app.campaign_reports
UNION ALL SELECT 'beneficiaries', count(*) FROM app.beneficiaries UNION ALL SELECT 'organisations', count(*) FROM app.organisations
UNION ALL SELECT 'kyc.verification_profiles', count(*) FROM kyc.verification_profiles;
SELECT pg_size_pretty(pg_database_size('fundzim_perf')) AS db_size;
