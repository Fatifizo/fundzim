// In-memory mock of the Stage 16 discovery API (api/openapi/fundzim-v1.yaml, tag "Discovery (Stage 16)") for the E2E
// suite, built over the campaign mock's data (passed in by campaigns.mjs). Shapes follow internal/discovery. It
// enforces what the UI must cope with: eligibility (only listed public campaigns; restricted owners excluded), closed
// filter values (422), maker-checker featured placements (the requester never decides; permissions; step-up), bounded
// ranking weights, referral codes that carry only campaign + channel, aggregate analytics for owners only (404
// otherwise). NOT a security reference.
import { randomBytes, randomUUID } from "node:crypto";

export const LOCATIONS = [
  { code: "PROVINCE_BULAWAYO", kind: "PROVINCE", slug: "bulawayo", name: "Bulawayo", province_code: null, display_order: 10 },
  { code: "PROVINCE_HARARE", kind: "PROVINCE", slug: "harare", name: "Harare", province_code: null, display_order: 20 },
  { code: "PROVINCE_MANICALAND", kind: "PROVINCE", slug: "manicaland", name: "Manicaland", province_code: null, display_order: 30 },
  { code: "PROVINCE_MIDLANDS", kind: "PROVINCE", slug: "midlands", name: "Midlands", province_code: null, display_order: 100 },
  { code: "TOWN_BULAWAYO", kind: "TOWN", slug: "bulawayo", name: "Bulawayo", province_code: "PROVINCE_BULAWAYO", display_order: 10 },
  { code: "TOWN_HARARE", kind: "TOWN", slug: "harare", name: "Harare", province_code: "PROVINCE_HARARE", display_order: 20 },
  { code: "TOWN_MUTARE", kind: "TOWN", slug: "mutare", name: "Mutare", province_code: "PROVINCE_MANICALAND", display_order: 30 },
  { code: "TOWN_GWERU", kind: "TOWN", slug: "gweru", name: "Gweru", province_code: "PROVINCE_MIDLANDS", display_order: 100 },
];

export const ROLE_PERMISSIONS = {
  ADMIN: ["discovery.view", "discovery.feature.request", "discovery.feature.remove", "discovery.ranking.request", "discovery.operate"],
  REVIEWER: ["discovery.view", "discovery.feature.request", "discovery.feature.remove"],
  COMPLIANCE: ["discovery.view", "discovery.feature.approve", "discovery.feature.remove", "discovery.ranking.approve", "discovery.operate"],
};

const SORTS = new Set(["relevance", "newest", "oldest", "trending"]);
const STATUS = { active: ["ACTIVE"], paused: ["PAUSED"], ended: ["COMPLETED"] };
const PUBLISHED = { "7d": 7, "30d": 30, "90d": 90, "365d": 365 };
const SHARE_CHANNELS = new Set(["WHATSAPP", "FACEBOOK", "X", "LINKEDIN", "EMAIL", "COPY_LINK", "WEB_SHARE"]);
const SOURCES = new Set(["DIRECT", "SEARCH", "CATEGORY", "FEATURED", "TRENDING", "RECOMMENDED", "INTERNAL", "UNKNOWN"]);
const SURFACES = new Set(["SEARCH", "CATEGORY", "FEATURED", "TRENDING", "RECOMMENDED"]);
const DEFAULT_WEIGHTS = { view_weight: 10, share_weight: 25, freshness_half_life_hours: 168, window_days: 7, min_unique_visitors: 3 };
const WEIGHT_BOUNDS = { view_weight: [0, 100], share_weight: [0, 100], freshness_half_life_hours: [6, 2160], window_days: [1, 30], min_unique_visitors: [0, 1000] };
const REASON_TEXT = { FEATURED: "Featured by FundZim", POPULAR_NOW: "Popular right now", RECENTLY_PUBLISHED: "Recently published", MORE_IN_CATEGORY: "More in this category", SAME_PROVINCE: "In the same province", RELATED_TO_SEARCH: "Related to your search" };
const BOT = /bot|crawl|spider|preview|whatsapp|facebookexternalhit|curl|python|headless/i;
const now = () => new Date().toISOString();
const day = () => now().slice(0, 10);

export function createDiscoveryMock({ send, err, sendData, campaigns, listed, card, findPublic, isMember, perms, restricted }) {
  const placements = []; // {id, campaignId, position, reason, starts_at, ends_at, status, requested_by, requested_at, decided_by, decision_note, ...}
  const policies = []; // ranking policy versions
  const referrals = new Map(); // code -> {campaignId, channel, revoked}
  const counts = new Map(); // `${campaignId}|${metric}|${channel}` -> n
  const visitors = new Set(); // `${day}|${campaignId}|${kind}|${ip}`
  const searches = new Map(); // outcome -> n
  let referralsEnabled = true;
  let eligibilityDown = false;

  const add = (campaignId, metric, channel = "", n = 1) => counts.set(`${campaignId}|${metric}|${channel}`, (counts.get(`${campaignId}|${metric}|${channel}`) ?? 0) + n);
  const sum = (campaignId, metric) => [...counts.entries()].filter(([k]) => k.startsWith(`${campaignId}|${metric}|`)).reduce((a, [, v]) => a + v, 0);
  const discoverable = () => listed().filter((c) => !restricted.has(c.ownerUserId) && !restricted.has(c.id));
  const promotable = () => discoverable().filter((c) => c.status === "ACTIVE");
  const locOf = (c) => LOCATIONS.find((l) => l.code === c.approved?.location_code) ?? null;
  const provinceOf = (c) => {
    const l = locOf(c);
    return l ? (l.kind === "PROVINCE" ? l : LOCATIONS.find((p) => p.code === l.province_code)) : null;
  };
  const publicLocation = (c) => {
    const l = locOf(c);
    if (!l) return null;
    const p = provinceOf(c);
    return { code: l.code, name: l.name, kind: l.kind, province_code: p.code, province_name: p.name, province_slug: p.slug, town_slug: l.kind === "TOWN" ? l.slug : null };
  };
  const score = (c) => {
    const v = sum(c.id, "VIEW_UNIQUE");
    if (v < DEFAULT_WEIGHTS.min_unique_visitors) return 0;
    return 10 * Math.log1p(v) + 25 * Math.log1p(sum(c.id, "SHARE_UNIQUE"));
  };
  const highlight = (text, terms) => {
    if (!terms.length) return null;
    const out = [];
    for (const part of text.split(/(\p{L}[\p{L}\p{N}]*|\p{N}+)/u)) {
      if (!part) continue;
      const m = terms.some((t) => part.toLowerCase().startsWith(t));
      if (out.length && out.at(-1).match === m) out.at(-1).text += part;
      else out.push({ text: part, match: m });
    }
    return out;
  };
  const dcard = (c, extra = {}) => ({ ...card(c), location: publicLocation(c), ...extra });
  const reason = (code) => ({ code, text: REASON_TEXT[code] });
  const featuredNow = () => placements.filter((p) => p.status === "APPROVED" && p.starts_at <= now() && p.ends_at > now()).sort((a, b) => a.position - b.position);

  function search(url, res, surface) {
    if (eligibilityDown) return err(res, 503, "DISCOVERY_UNAVAILABLE");
    const q = url.searchParams;
    const rawQ = q.get("q") ?? "";
    const details = [];
    if ([...rawQ].length > 100) details.push({ field: "q", code: "INVALID_LENGTH" });
    const sortIn = q.get("sort") ?? "";
    if (sortIn && !SORTS.has(sortIn)) details.push({ field: "sort", code: "INVALID_VALUE" });
    const status = q.get("status") ?? "";
    if (status && !STATUS[status]) details.push({ field: "status", code: "INVALID_VALUE" });
    const published = q.get("published") ?? "";
    if (published && !PUBLISHED[published]) details.push({ field: "published", code: "INVALID_VALUE" });
    const organiser = q.get("organiser") ?? "";
    if (organiser && !["individual", "organisation"].includes(organiser)) details.push({ field: "organiser", code: "INVALID_VALUE" });
    const cursor = q.get("cursor") ?? "";
    let offset = 0;
    if (cursor) {
      const m = /^o(\d+)$/.exec(cursor);
      if (!m) details.push({ field: "cursor", code: "INVALID_FORMAT" });
      else offset = Number(m[1]);
    }
    if (details.length) return err(res, 422, "VALIDATION_FAILED", { details });
    const terms = [...new Set(rawQ.toLowerCase().normalize("NFC").split(/[^\p{L}\p{N}]+/u).filter((t) => t.length > 1 || /\d/.test(t)))].slice(0, 8);
    const sort = sortIn === "relevance" && !terms.length ? "newest" : sortIn || (terms.length ? "relevance" : "newest");
    const cat = q.get("category") ?? "";
    const province = q.get("province") ?? "";
    const town = q.get("town") ?? "";
    let list = discoverable()
      .filter((c) => !cat || c.approved.category === cat || c.approved.category === cat.toUpperCase().replace(/-/g, "_"))
      .filter((c) => !province || provinceOf(c)?.slug === province)
      .filter((c) => !town || (locOf(c)?.kind === "TOWN" && locOf(c)?.slug === town))
      .filter((c) => !status || STATUS[status].includes(c.status))
      .filter((c) => !published || Date.now() - Date.parse(c.published_at) <= PUBLISHED[published] * 86400000)
      .filter((c) => !organiser || (organiser === "organisation") === !!c.orgId);
    const doc = (c) => `${c.approved.title} ${c.approved.summary} ${provinceOf(c)?.name ?? ""} ${locOf(c)?.name ?? ""}`.toLowerCase();
    let match = terms.length ? "EXACT" : "ALL";
    let matched = list.filter((c) => terms.every((t) => doc(c).split(/[^\p{L}\p{N}]+/u).some((w) => w.startsWith(t))));
    if (terms.length && matched.length === 0 && !cursor) {
      matched = list.filter((c) => terms.some((t) => t.length >= 4 && c.approved.title.toLowerCase().includes(t.slice(0, 3))));
      match = matched.length ? "FUZZY" : "NONE";
    }
    const order = {
      newest: (a, b) => (a.published_at < b.published_at ? 1 : -1),
      oldest: (a, b) => (a.published_at > b.published_at ? 1 : -1),
      relevance: (a, b) => (a.published_at < b.published_at ? 1 : -1),
      trending: (a, b) => score(b) - score(a) || (a.published_at < b.published_at ? 1 : -1),
    }[sort];
    matched.sort(order);
    const limit = Math.min(Math.max(parseInt(q.get("limit") ?? "12", 10) || 12, 1), 48);
    const page = matched.slice(offset, offset + limit);
    searches.set(page.length ? "RESULTS" : "NO_RESULTS", (searches.get(page.length ? "RESULTS" : "NO_RESULTS") ?? 0) + 1);
    for (const c of page) add(c.id, surface === "SEARCH" ? "SEARCH_IMPRESSION" : "LISTING_IMPRESSION", surface === "SEARCH" ? "SEARCH" : "");
    const items = page.map((c) => dcard(c, terms.length ? { highlights: { title: highlight(c.approved.title, terms), summary: highlight(c.approved.summary, terms) } } : {}));
    const next = match !== "FUZZY" && offset + limit < matched.length ? `o${offset + limit}` : null;
    return sendData(res, 200, { items, next_cursor: next, total: { value: Math.min(matched.length, 1000), capped: matched.length > 1000 }, match, query: terms.join(" "), sort });
  }

  function facets() {
    const by = (f) => {
      const m = new Map();
      for (const c of discoverable()) m.set(f(c) ?? "", (m.get(f(c) ?? "") ?? 0) + 1);
      return m;
    };
    return { prov: by((c) => provinceOf(c)?.code), loc: by((c) => locOf(c)?.code) };
  }

  const placementView = (p) => {
    const c = campaigns.get(p.campaignId);
    return { id: p.id, campaign_id: p.campaignId, campaign_slug: c?.slug ?? "", campaign_title: c?.title ?? "", position: p.position, reason: p.reason, starts_at: p.starts_at,
      ends_at: p.ends_at, status: p.status, requested_by: p.requested_by, requested_at: p.requested_at, decided_by: p.decided_by ?? null, decided_at: p.decided_at ?? null,
      decision_note: p.decision_note ?? null, removed_by: p.removed_by ?? null, removed_at: p.removed_at ?? null, removal_reason: p.removal_reason ?? null,
      live: p.status === "APPROVED" && p.starts_at <= now() && p.ends_at > now(), promotable: ["REQUESTED", "APPROVED"].includes(p.status) ? promotable().includes(c) : null, version: 1 };
  };
  const rankingView = () => {
    const active = policies.find((p) => p.status === "ACTIVE");
    return { in_force: active?.weights ?? DEFAULT_WEIGHTS, in_force_version: active?.policy_version ?? null, in_force_source: active ? "APPROVED_POLICY" : "ENGINEERING_DEFAULT",
      engineering_default: DEFAULT_WEIGHTS, policies: [...policies].sort((a, b) => b.policy_version - a.policy_version) };
  };

  async function handle({ req, res, method, path, url, user, session, body, stepUpTtlMs }) {
    const p = path.slice("/api/v1".length);
    const seg = p.split("/").filter(Boolean);
    if (p === "/campaign-locations" && method === "GET") return sendData(res, 200, LOCATIONS, { "Cache-Control": "public, max-age=300" }), true;

    if (seg[0] === "discovery" || p === "/public/campaigns/featured") {
      const what = p === "/public/campaigns/featured" ? "featured" : seg[1];
      if (method === "GET" && (what === "campaigns" || what === "search")) return search(url, res, what === "search" || url.searchParams.get("q") ? "SEARCH" : "DIRECTORY"), true;
      if (method === "GET" && what === "categories") {
        return sendData(res, 200, { items: [] }), true; // the web app reads categories from /public/categories; counts are optional
      }
      if (method === "GET" && what === "locations") {
        const f = facets();
        return sendData(res, 200, { country: "ZW", provinces: LOCATIONS.filter((l) => l.kind === "PROVINCE").map((l) => ({ ...l, discoverable_campaigns: f.prov.get(l.code) ?? 0 })),
          towns: LOCATIONS.filter((l) => l.kind === "TOWN").map((l) => ({ ...l, discoverable_campaigns: f.loc.get(l.code) ?? 0 })), unspecified_campaigns: f.prov.get("") ?? 0,
          note: "A campaign's location is where the cause is, chosen by the organiser from a list of provinces and towns. It is never an address." }), true;
      }
      if (method === "GET" && what === "trending") {
        const list = promotable().filter((c) => score(c) > 0).sort((a, b) => score(b) - score(a)).slice(0, 24);
        return sendData(res, 200, { items: list.map((c) => dcard(c, { reason: reason("POPULAR_NOW") })), next_cursor: null,
          rule: "Ranked by how many different people viewed and shared each campaign recently (automated traffic excluded, each person counted once a day), with newer campaigns given more weight. Money is never part of the ranking, and nobody can pay to trend.",
          policy_version: rankingView().in_force_version, window_days: 7 }), true;
      }
      if (method === "GET" && what === "featured") {
        const limit = Math.min(Math.max(parseInt(url.searchParams.get("limit") ?? "6", 10) || 6, 1), 12);
        const prom = promotable();
        const picks = featuredNow().map((x) => campaigns.get(x.campaignId)).filter((c) => prom.includes(c));
        const items = picks.map((c) => dcard(c, { reason: reason("FEATURED"), selection: "FEATURED" }));
        if (url.searchParams.get("fill") !== "false") {
          for (const c of prom.filter((x) => !picks.includes(x)).sort((a, b) => (a.published_at < b.published_at ? 1 : -1))) items.push(dcard(c, { reason: reason("RECENTLY_PUBLISHED"), selection: "RECENTLY_PUBLISHED" }));
        }
        return sendData(res, 200, { items: items.slice(0, limit), next_cursor: null }), true;
      }
      if (method === "GET" && what === "recommendations") {
        const viewed = findPublic(url.searchParams.get("campaign") ?? "");
        const prom = promotable().filter((c) => c !== viewed);
        const out = [];
        const take = (list, why) => list.forEach((c) => out.length < 4 && !out.some((o) => o.c === c) && out.push({ c, why }));
        if (viewed) take(prom.filter((c) => c.approved.category === viewed.approved.category), "MORE_IN_CATEGORY");
        if (viewed && provinceOf(viewed)) take(prom.filter((c) => provinceOf(c) === provinceOf(viewed)), "SAME_PROVINCE");
        take(featuredNow().map((x) => campaigns.get(x.campaignId)).filter((c) => prom.includes(c)), "FEATURED");
        take([...prom].sort((a, b) => (a.published_at < b.published_at ? 1 : -1)), "RECENTLY_PUBLISHED");
        return sendData(res, 200, { items: out.map(({ c, why }) => dcard(c, { reason: reason(why) })), personalised: false,
          rules: "Chosen by fixed rules, the same for everyone: same category, same province, featured, popular right now and recently published. Nothing about you is used." }), true;
      }
      if (method === "GET" && what === "sitemap") {
        const f = facets();
        return sendData(res, 200, { campaigns: discoverable().map((c) => ({ slug: c.slug, updated_at: c.updated_at })), categories: [],
          provinces: LOCATIONS.filter((l) => l.kind === "PROVINCE" && (f.prov.get(l.code) ?? 0) > 0).map((l) => ({ slug: l.slug, updated_at: now() })) }), true;
      }
      if (method === "POST" && what === "events") {
        const events = Array.isArray(body.events) ? body.events : [];
        if (events.length < 1 || events.length > 20) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "events", code: "INVALID_LENGTH" }] }), true;
        for (const e of events) {
          const ok = (e.type === "CAMPAIGN_VIEW" && (!e.source || SOURCES.has(e.source))) || (e.type === "SHARE_CLICK" && SHARE_CHANNELS.has(e.channel)) || (e.type === "RESULT_CLICK" && SURFACES.has(e.source));
          if (!ok || typeof e.slug !== "string" || Object.keys(e).some((k) => !["type", "slug", "channel", "source", "ref"].includes(k))) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "events", code: "INVALID_VALUE" }] }), true;
        }
        const ip = String(req.headers["x-forwarded-for"] ?? "unknown");
        const bot = BOT.test(String(req.headers["user-agent"] ?? ""));
        for (const e of events) {
          const c = findPublic(e.slug);
          if (!c) continue;
          if (bot) { add(c.id, "VIEW_BOT_EXCLUDED"); continue; }
          const kind = { CAMPAIGN_VIEW: "VIEW", SHARE_CLICK: "SHARE", RESULT_CLICK: "SEARCH_CLICK" }[e.type];
          const key = `${day()}|${c.id}|${kind}|${ip}`;
          if (kind === "VIEW") { add(c.id, "VIEW_RAW", e.source || "UNKNOWN"); if (!visitors.has(key)) add(c.id, "VIEW_UNIQUE"); }
          if (kind === "SHARE") { add(c.id, "SHARE_RAW", e.channel); if (!visitors.has(key)) add(c.id, "SHARE_UNIQUE"); }
          if (kind === "SEARCH_CLICK") add(c.id, "SEARCH_CLICK", e.source);
          visitors.add(key);
          const r = e.ref ? referrals.get(e.ref) : null;
          if (kind === "VIEW" && r && !r.revoked && r.campaignId === c.id && referralsEnabled) add(c.id, "REFERRAL_VISIT_RAW", r.channel);
        }
        res.writeHead(204, { "Cache-Control": "private, no-store" });
        res.end();
        return true;
      }
      if (method === "POST" && what === "referrals") {
        if (!SHARE_CHANNELS.has(body.channel)) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "channel", code: "INVALID_VALUE" }] }), true;
        if (!referralsEnabled) return err(res, 503, "REFERRALS_DISABLED"), true;
        const c = findPublic(String(body.slug ?? ""));
        if (!c || !discoverable().includes(c)) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
        const alphabet = "0123456789abcdefghjkmnpqrstvwxyz";
        const code = Array.from(randomBytes(12), (b) => alphabet[b % 32]).join("");
        referrals.set(code, { campaignId: c.id, channel: body.channel, revoked: false });
        return send(res, 201, { code, channel: body.channel, expires_at: new Date(Date.now() + 90 * 86400000).toISOString() }, { "Cache-Control": "private, no-store" }), true;
      }
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }

    // owner analytics
    if (seg[0] === "campaigns" && seg.length === 3 && seg[2] === "analytics" && method === "GET") {
      if (!user) return err(res, 401, "AUTHENTICATION_REQUIRED"), true;
      const c = campaigns.get(seg[1]);
      if (!c || user.account_kind === "STAFF" || !isMember(user, c)) return err(res, 404, "CAMPAIGN_NOT_FOUND"), true;
      const days = parseInt(url.searchParams.get("days") ?? "30", 10) || 30;
      const by = (metric) => Object.fromEntries([...counts.entries()].filter(([k]) => k.startsWith(`${c.id}|${metric}|`)).map(([k, v]) => [k.split("|")[2] || "NONE", v]));
      const totals = { views: sum(c.id, "VIEW_RAW"), unique_visitors: sum(c.id, "VIEW_UNIQUE"), bot_views_excluded: sum(c.id, "VIEW_BOT_EXCLUDED"), shares: sum(c.id, "SHARE_RAW"),
        referral_visits: sum(c.id, "REFERRAL_VISIT_RAW"), impressions: sum(c.id, "SEARCH_IMPRESSION") + sum(c.id, "LISTING_IMPRESSION"), clicks: sum(c.id, "SEARCH_CLICK") };
      return send(res, 200, { campaign_id: c.id, from: new Date(Date.now() - (days - 1) * 86400000).toISOString().slice(0, 10), to: day(), days, nature: "MEASURED_NON_PRODUCTION", totals,
        shares_by_channel: by("SHARE_RAW"), referral_visits_by_channel: by("REFERRAL_VISIT_RAW"), traffic_sources: by("VIEW_RAW"), impressions_by_surface: { SEARCH: sum(c.id, "SEARCH_IMPRESSION"), DIRECTORY: sum(c.id, "LISTING_IMPRESSION") },
        clicks_by_surface: by("SEARCH_CLICK"), click_through_bp: totals.impressions ? Math.floor((totals.clicks * 10000) / totals.impressions) : null,
        daily: [{ day: day(), views: totals.views, unique_visitors: totals.unique_visitors, shares: totals.shares }], donations: { available: false, message: "Donations are not yet available." },
        definitions: { views: "Page views by browsers that did not identify as automated.", donations: "Not available: FundZim does not accept donations." } }, { "Cache-Control": "private, no-store" }), true;
    }

    // staff
    if (seg[0] === "admin" && seg[1] === "discovery") {
      if (!user || user.account_kind !== "STAFF") return err(res, 404, "ROUTE_NOT_FOUND"), true;
      const ps = perms(user);
      const need = (perm, stepUp = false) => {
        if (!ps.has(perm)) return err(res, 403, "PERMISSION_DENIED"), false;
        if (stepUp && !(session && Date.now() - session.stepUpAt <= stepUpTtlMs)) return err(res, 403, "STEP_UP_REQUIRED"), false;
        return true;
      };
      const note = String(body?.note ?? "").trim();
      if (seg[2] === "overview" && method === "GET") {
        if (!need("discovery.view")) return true;
        const trending = promotable().filter((c) => score(c) > 0).sort((a, b) => score(b) - score(a)).slice(0, 10);
        return send(res, 200, { generated_at: now(), nature: "MEASURED_NON_PRODUCTION",
          index: { discoverable: discoverable().length, not_discoverable: 0, backlog: 0, oldest_backlog_at: null, failing: 0, last_indexed_at: now(), status: "HEALTHY", trending_computed_at: now(), trending_ranked: trending.length },
          search_7d_by_outcome: Object.fromEntries(searches), search_7d_by_sort: {}, engagement_7d: {}, referral_visits_7d_by_channel: {}, shares_7d_by_channel: {}, abuse_7d_by_kind: {},
          featured: { live: featuredNow().length, pending_requests: placements.filter((x) => x.status === "REQUESTED").length, scheduled: 0 },
          trending_top: trending.map((c) => ({ campaign_id: c.id, slug: c.slug, title: c.approved.title, score: score(c).toFixed(6), components: { unique_visitors: sum(c.id, "VIEW_UNIQUE"), unique_sharers: sum(c.id, "SHARE_UNIQUE"), age_hours: 1 } })),
          ranking: { source: rankingView().in_force_source, version: rankingView().in_force_version, pending: policies.filter((x) => x.status === "SUBMITTED").length, in_force: rankingView().in_force },
          referral_codes: { enabled: referralsEnabled, active: referrals.size, revoked: 0, expired: 0 },
          definitions: { views: "Page views by browsers that did not identify as automated." } }, { "Cache-Control": "private, no-store" }), true;
      }
      if (seg[2] === "featured") {
        if (seg.length === 3 && method === "GET") {
          if (!need("discovery.view")) return true;
          return send(res, 200, { items: placements.map(placementView), next_cursor: null }), true;
        }
        if (seg.length === 3 && method === "POST") {
          if (!need("discovery.feature.request", true)) return true;
          if (!req.headers["idempotency-key"]) return err(res, 400, "IDEMPOTENCY_KEY_REQUIRED"), true;
          const reasonText = String(body.reason ?? "").trim();
          if (!/^[0-9a-f-]{36}$/i.test(String(body.campaign_id)) || !(body.position >= 1 && body.position <= 100) || [...reasonText].length < 10) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
          const c = campaigns.get(body.campaign_id);
          if (!c || !promotable().includes(c)) return err(res, 409, "CAMPAIGN_NOT_PROMOTABLE"), true;
          if (placements.some((x) => x.campaignId === c.id && ["REQUESTED", "APPROVED"].includes(x.status))) return err(res, 409, "PLACEMENT_EXISTS"), true;
          const pl = { id: randomUUID(), campaignId: c.id, position: body.position, reason: reasonText, starts_at: body.starts_at ?? now(), ends_at: body.ends_at, status: "REQUESTED", requested_by: user.id, requested_at: now() };
          placements.push(pl);
          return send(res, 201, placementView(pl)), true;
        }
        if (seg.length === 5 && method === "POST") {
          const pl = placements.find((x) => x.id === seg[3]);
          const action = seg[4];
          const perm = { approve: "discovery.feature.approve", reject: "discovery.feature.approve", cancel: "discovery.feature.request", remove: "discovery.feature.remove" }[action];
          if (!perm) return err(res, 404, "ROUTE_NOT_FOUND"), true;
          if (!need(perm, true)) return true;
          if (!pl) return err(res, 404, "NOT_FOUND"), true;
          if ([...note].length < 3) return err(res, 422, "VALIDATION_FAILED", { details: [{ field: "reason", code: "INVALID_LENGTH" }] }), true;
          if (action === "remove") {
            if (pl.status !== "APPROVED") return err(res, 409, "INVALID_STATE"), true;
            Object.assign(pl, { status: "REMOVED", removed_by: user.id, removed_at: now(), removal_reason: note });
          } else {
            if (pl.status !== "REQUESTED") return err(res, 409, "INVALID_STATE"), true;
            if (action === "cancel") {
              if (pl.requested_by !== user.id) return err(res, 404, "NOT_FOUND"), true;
              pl.status = "CANCELLED";
            } else {
              if (pl.requested_by === user.id) return err(res, 403, "SELF_DECISION_FORBIDDEN"), true;
              Object.assign(pl, { status: action === "approve" ? "APPROVED" : "REJECTED", decided_by: user.id, decided_at: now(), decision_note: note });
            }
          }
          return send(res, 200, placementView(pl)), true;
        }
      }
      if (seg[2] === "ranking") {
        if (seg.length === 3 && method === "GET") {
          if (!need("discovery.view")) return true;
          return send(res, 200, rankingView()), true;
        }
        if (seg.length === 3 && method === "POST") {
          if (!need("discovery.ranking.request", true)) return true;
          const w = body.weights ?? {};
          const bad = Object.entries(WEIGHT_BOUNDS).filter(([k, [lo, hi]]) => !(Number.isInteger(w[k]) && w[k] >= lo && w[k] <= hi)).map(([k]) => ({ field: k, code: "OUT_OF_RANGE" }));
          if (bad.length || [...String(body.reason ?? "")].length < 10) return err(res, 422, "VALIDATION_FAILED", { details: bad }), true;
          const pol = { id: randomUUID(), policy_version: policies.length + 1, weights: w, content_sha256: randomBytes(32).toString("hex"), status: "SUBMITTED", reason: body.reason, proposed_by: user.id, proposed_at: now(), decided_by: null, decided_at: null, decision_note: null, retired_at: null };
          policies.push(pol);
          return send(res, 201, pol), true;
        }
        if (seg.length === 5 && method === "POST") {
          const pol = policies.find((x) => x.id === seg[3]);
          const action = seg[4];
          if (!need(action === "withdraw" ? "discovery.ranking.request" : "discovery.ranking.approve", true)) return true;
          if (!pol) return err(res, 404, "NOT_FOUND"), true;
          if (pol.status !== "SUBMITTED") return err(res, 409, "INVALID_STATE"), true;
          if (action !== "withdraw" && pol.proposed_by === user.id) return err(res, 403, "SELF_DECISION_FORBIDDEN"), true;
          if (action === "approve") policies.filter((x) => x.status === "ACTIVE").forEach((x) => Object.assign(x, { status: "RETIRED", retired_at: now() }));
          Object.assign(pol, { status: { approve: "ACTIVE", reject: "REJECTED", withdraw: "WITHDRAWN" }[action], decided_by: action === "withdraw" ? null : user.id, decision_note: note });
          return send(res, 200, pol), true;
        }
      }
      if (seg[2] === "referrals" && seg[4] === "revoke" && method === "POST") {
        if (!need("discovery.operate", true)) return true;
        const r = referrals.get(seg[3]);
        if (!r) return err(res, 404, "NOT_FOUND"), true;
        r.revoked = true;
        return send(res, 200, { code: seg[3], status: "REVOKED" }), true;
      }
      if (seg[2] === "index" && seg[3] === "reconcile" && method === "POST") {
        if (!need("discovery.operate", true)) return true;
        return send(res, 200, { checked: listed().length, reindexed: 0, failed: 0 }), true;
      }
      return err(res, 404, "ROUTE_NOT_FOUND"), true;
    }
    return false;
  }

  /** Test controls: seed an approved placement directly; switch referrals or eligibility; read counters. */
  function control(path, body, res) {
    if (path === "/api/v1/__mock/discovery/feature") {
      if (body.add) placements.push({ id: randomUUID(), campaignId: body.add, position: 1, reason: "Seeded placement for the E2E suite", starts_at: new Date(Date.now() - 60000).toISOString(), ends_at: new Date(Date.now() + 86400000).toISOString(), status: "APPROVED", requested_by: "seed", requested_at: now(), decided_by: "seed2", decision_note: "seeded" });
      if (body.remove) placements.filter((x) => x.campaignId === body.remove && x.status === "APPROVED").forEach((x) => (x.status = "REMOVED"));
      return send(res, 200, { ok: true }), true;
    }
    if (path === "/api/v1/__mock/discovery/settings") {
      if (typeof body.referrals === "boolean") referralsEnabled = body.referrals;
      if (typeof body.eligibilityDown === "boolean") eligibilityDown = body.eligibilityDown;
      return send(res, 200, { referralsEnabled, eligibilityDown }), true;
    }
    if (path === "/api/v1/__mock/discovery/counts") {
      const c = campaigns.get(body.campaign_id);
      return send(res, 200, { counts: c ? Object.fromEntries([...counts.entries()].filter(([k]) => k.startsWith(`${c.id}|`)).map(([k, v]) => [k.slice(c.id.length + 1), v])) : {}, referrals: [...referrals.entries()].filter(([, r]) => !c || r.campaignId === c.id).map(([code, r]) => ({ code, ...r })) }), true;
    }
    return false;
  }

  return { handle, control };
}
