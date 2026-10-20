/**
 * Discovery listing parameters (GET /api/v1/discovery/campaigns, docs/stage-16/search.md). Everything is validated here
 * before it reaches the API or a URL: unknown values are dropped (the listing then shows everything), never forwarded.
 */
import { first, isCategoryFilter, isCursor } from "@/lib/campaigns/paths";
import { cleanText } from "@/lib/campaigns/text";

export const DISCOVERY_SORTS = ["relevance", "newest", "oldest", "trending"] as const;
export type DiscoverySort = (typeof DISCOVERY_SORTS)[number];
export const STATUS_FILTERS = ["active", "paused", "ended"] as const;
export const PUBLISHED_FILTERS = ["7d", "30d", "90d", "365d"] as const;
export const ORGANISER_FILTERS = ["individual", "organisation"] as const;

export const SORT_LABELS: Record<DiscoverySort, string> = {
  relevance: "Best match",
  newest: "Newest",
  oldest: "Oldest",
  trending: "Popular right now",
};
export const STATUS_LABELS: Record<(typeof STATUS_FILTERS)[number], string> = { active: "Active", paused: "Paused", ended: "Ended" };
export const PUBLISHED_LABELS: Record<(typeof PUBLISHED_FILTERS)[number], string> = {
  "7d": "In the last 7 days",
  "30d": "In the last 30 days",
  "90d": "In the last 3 months",
  "365d": "In the last year",
};
export const ORGANISER_LABELS: Record<(typeof ORGANISER_FILTERS)[number], string> = { individual: "Individuals", organisation: "Organisations" };

const LOCATION_SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
/** Discovery cursors are base64url, bounded. */
const DISCOVERY_CURSOR = /^[A-Za-z0-9_-]{1,200}$/;

export interface DiscoveryQuery {
  q: string;
  category: string;
  province: string;
  town: string;
  status: string;
  published: string;
  organiser: string;
  /** "" = the default (relevance with a query, newest without). */
  sort: DiscoverySort | "";
  cursor: string;
}

export const EMPTY_QUERY: DiscoveryQuery = { q: "", category: "", province: "", town: "", status: "", published: "", organiser: "", sort: "", cursor: "" };

function oneOf<T extends string>(value: string | undefined, allowed: readonly T[]): T | "" {
  return value && (allowed as readonly string[]).includes(value) ? (value as T) : "";
}

function locationSlug(value: string | undefined): string {
  return value && value.length <= 40 && LOCATION_SLUG.test(value) ? value : "";
}

/** Reads and validates search parameters (q ≤ 100 characters of plain text, closed value sets, well-formed cursor). */
export function readDiscoveryQuery(p: Record<string, string | string[] | undefined>): DiscoveryQuery {
  const q = cleanText(first(p.q) ?? "")
    .replace(/[\n\t]/g, " ")
    .trim();
  const category = first(p.category);
  const cursor = first(p.cursor);
  return {
    q: [...q].slice(0, 100).join("").trim(),
    category: isCategoryFilter(category) && category.length <= 40 ? category : "",
    province: locationSlug(first(p.province)),
    town: locationSlug(first(p.town)),
    status: oneOf(first(p.status), STATUS_FILTERS),
    published: oneOf(first(p.published), PUBLISHED_FILTERS),
    organiser: oneOf(first(p.organiser), ORGANISER_FILTERS),
    sort: oneOf(first(p.sort), DISCOVERY_SORTS),
    cursor: isCursor(cursor) && DISCOVERY_CURSOR.test(cursor) ? cursor : "",
  };
}

/** Whether anything narrows the listing (a query or a filter). */
export function isFiltered(q: DiscoveryQuery, ignore: ReadonlyArray<keyof DiscoveryQuery> = []): boolean {
  return (["q", "category", "province", "town", "status", "published", "organiser"] as const).some((k) => !ignore.includes(k) && q[k] !== "");
}

/** API query string for a listing page. */
export function discoveryApiParams(q: DiscoveryQuery, limit: number): URLSearchParams {
  const params = new URLSearchParams();
  for (const k of ["q", "category", "province", "town", "status", "published", "organiser", "sort", "cursor"] as const) {
    if (q[k]) params.set(k, q[k]);
  }
  params.set("limit", String(limit));
  return params;
}

/**
 * Page URL for a listing. `fixed` names parameters carried by the path itself (a category or province page), which
 * are left out. Parameters are written in a fixed order so equal listings have equal URLs.
 */
export function discoveryHref(basePath: string, q: DiscoveryQuery, overrides: Partial<DiscoveryQuery> = {}, fixed: ReadonlyArray<keyof DiscoveryQuery> = []): string {
  const merged = { ...q, ...overrides };
  const params = new URLSearchParams();
  for (const k of ["q", "category", "province", "town", "status", "published", "organiser", "sort", "cursor"] as const) {
    if (merged[k] && !fixed.includes(k)) params.set(k, merged[k]);
  }
  return params.size ? `${basePath}?${params.toString()}` : basePath;
}
