/**
 * Defensive readers for discovery responses: unknown shapes become empty values, never exceptions; strings are kept as
 * plain text (React escapes them). Highlights must rebuild the card text exactly, otherwise they are ignored (a server
 * bug can then never make the page show text that is not the campaign's).
 */
import { readPublicLocation, readPublicSummary, readTotal } from "@/lib/campaigns/normalise";
import { readPublicCategories } from "@/lib/campaigns/normalise";
import { isSlug } from "@/lib/campaigns/paths";

import type {
  CampaignAnalytics,
  CategoryFacet,
  DiscoveryCard,
  DiscoveryPage,
  DiscoveryReason,
  DiscoverySitemap,
  LocationFacet,
  LocationsResult,
  Recommendations,
  Segment,
  TrendingResult,
} from "./types";

type Rec = Record<string, unknown>;
const isRec = (v: unknown): v is Rec => typeof v === "object" && v !== null && !Array.isArray(v);
const str = (v: unknown, fallback = ""): string => (typeof v === "string" ? v : fallback);
const strOrNull = (v: unknown): string | null => (typeof v === "string" && v !== "" ? v : null);
const num = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) && Number.isInteger(v) && v >= 0 ? v : 0);
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : isRec(v) && Array.isArray(v.items) ? v.items : []);

const REASONS = new Set(["FEATURED", "POPULAR_NOW", "RECENTLY_PUBLISHED", "MORE_IN_CATEGORY", "SAME_PROVINCE", "RELATED_TO_SEARCH"]);

function readSegments(v: unknown, text: string): Segment[] | null {
  if (!Array.isArray(v)) return null;
  const segs = v.filter(isRec).map((s) => ({ text: str(s.text), match: s.match === true }));
  return segs.map((s) => s.text).join("") === text ? segs : null;
}

function readReason(v: unknown): DiscoveryReason | null {
  return isRec(v) && typeof v.code === "string" && REASONS.has(v.code) && typeof v.text === "string" ? { code: v.code, text: v.text } : null;
}

export function readDiscoveryCard(c: Rec): DiscoveryCard {
  const base = readPublicSummary(c);
  const h = isRec(c.highlights) ? c.highlights : null;
  const title = h ? readSegments(h.title, base.title) : null;
  const summary = h ? readSegments(h.summary, base.summary) : null;
  return { ...base, location: readPublicLocation(c.location), highlights: title && summary ? { title, summary } : null, reason: readReason(c.reason) };
}

export function readDiscoveryCards(v: unknown): DiscoveryCard[] {
  return list(v)
    .filter(isRec)
    .filter((c) => isSlug(c.slug))
    .map(readDiscoveryCard);
}

export function readDiscoveryPage(v: unknown): DiscoveryPage {
  const d = isRec(v) ? v : {};
  return {
    items: readDiscoveryCards(d.items),
    nextCursor: strOrNull(d.next_cursor),
    total: readTotal(d),
    match: str(d.match, "ALL"),
    query: str(d.query),
    sort: str(d.sort),
  };
}

function readFacet(v: Rec): LocationFacet {
  return { code: str(v.code), kind: str(v.kind), slug: str(v.slug), name: str(v.name), province_code: strOrNull(v.province_code), count: num(v.discoverable_campaigns) };
}

export function readLocations(v: unknown): LocationsResult {
  const d = isRec(v) ? v : {};
  const facets = (x: unknown) => (Array.isArray(x) ? x.filter(isRec).filter((f) => isSlug(f.slug) && typeof f.name === "string").map(readFacet) : []);
  return { provinces: facets(d.provinces), towns: facets(d.towns), unspecified: num(d.unspecified_campaigns), note: str(d.note) };
}

export function readCategoryFacets(v: unknown): CategoryFacet[] {
  const raw = list(v).filter(isRec);
  const cats = readPublicCategories(raw);
  return cats.map((c) => {
    const r = raw.find((x) => x.code === c.code);
    return { ...c, discoverable: r ? num(r.discoverable_campaigns) : 0 };
  });
}

export function readTrending(v: unknown): TrendingResult {
  const d = isRec(v) ? v : {};
  return {
    items: readDiscoveryCards(d.items),
    rule: str(d.rule),
    policyVersion: typeof d.policy_version === "number" ? d.policy_version : null,
    windowDays: num(d.window_days),
  };
}

export function readRecommendations(v: unknown): Recommendations {
  const d = isRec(v) ? v : {};
  // anything claiming personalisation is not shown at all (FundZim never personalises; ADR-048)
  return { items: d.personalised === false ? readDiscoveryCards(d.items) : [], personalised: false, rules: str(d.rules) };
}

export function readDiscoverySitemap(v: unknown): DiscoverySitemap {
  const d = isRec(v) ? v : {};
  const entries = (x: unknown) =>
    Array.isArray(x) ? x.filter(isRec).filter((e) => isSlug(e.slug)).map((e) => ({ slug: e.slug as string, updated_at: strOrNull(e.updated_at) })) : [];
  return { campaigns: entries(d.campaigns), categories: entries(d.categories), provinces: entries(d.provinces) };
}

function counts(v: unknown): Record<string, number> {
  const out: Record<string, number> = {};
  if (isRec(v)) for (const [k, x] of Object.entries(v)) if (/^[A-Z_a-z]{1,40}$/.test(k)) out[k] = num(x);
  return out;
}

export function readCampaignAnalytics(v: unknown): CampaignAnalytics {
  const d = isRec(v) ? v : {};
  const defs: Record<string, string> = {};
  if (isRec(d.definitions)) for (const [k, x] of Object.entries(d.definitions)) if (typeof x === "string") defs[k] = x;
  return {
    from: str(d.from),
    to: str(d.to),
    days: num(d.days),
    nature: str(d.nature),
    totals: counts(d.totals),
    sharesByChannel: counts(d.shares_by_channel),
    referralsByChannel: counts(d.referral_visits_by_channel),
    trafficSources: counts(d.traffic_sources),
    impressionsBySurface: counts(d.impressions_by_surface),
    clicksBySurface: counts(d.clicks_by_surface),
    clickThroughBp: typeof d.click_through_bp === "number" && Number.isInteger(d.click_through_bp) ? d.click_through_bp : null,
    daily: Array.isArray(d.daily)
      ? d.daily.filter(isRec).map((p) => ({ day: str(p.day), views: num(p.views), unique_visitors: num(p.unique_visitors), shares: num(p.shares) }))
      : [],
    definitions: defs,
  };
}
