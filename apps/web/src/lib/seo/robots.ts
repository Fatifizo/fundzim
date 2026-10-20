/**
 * robots.txt and sitemap.xml content (pure: app/robots.ts and app/sitemap.ts pass the env and API data).
 *
 * Indexing is opt-in (`indexingAllowed`: PUBLIC_SITE_URL set AND SITE_INDEXING=allow). Otherwise every crawler
 * is asked to stay out, as in the development preview. When allowed, private areas are still disallowed and
 * also carry `noindex` on the pages themselves.
 */
import type { MetadataRoute } from "next";

import type { SitemapData } from "@/lib/campaigns/types";
import { absoluteUrl, campaignPath, categoryPath } from "@/lib/site/url";

/** Paths crawlers must never fetch (private, account and API surfaces). */
export const DISALLOWED_PATHS = [
  "/dashboard",
  "/admin",
  "/login",
  "/register",
  "/settings",
  "/staff",
  "/verify-email",
  "/reset-password",
  "/forgot-password",
  "/api",
] as const;

/** Static public pages listed in the sitemap. Draft legal pages (privacy, terms) stay out until final. */
export const STATIC_PUBLIC_PATHS = ["/", "/campaigns", "/campaigns/trending", "/campaigns/featured", "/categories", "/how-it-works", "/safety", "/about", "/contact"] as const;

export function buildRobots(origin: URL | null, allowed: boolean): MetadataRoute.Robots {
  if (!origin || !allowed) return { rules: { userAgent: "*", disallow: "/" } };
  return {
    rules: { userAgent: "*", allow: "/", disallow: [...DISALLOWED_PATHS] },
    sitemap: absoluteUrl("/sitemap.xml", origin)!,
  };
}

function lastModified(value: string | null): Date | undefined {
  if (!value) return undefined;
  const t = Date.parse(value);
  return Number.isNaN(t) ? undefined : new Date(t);
}

/**
 * Sitemap entries: static public pages, then discoverable campaigns, categories and provinces with at least one
 * discoverable campaign from GET /discovery/sitemap (every campaign re-checked by the API: never a draft, suspended,
 * restricted or unlisted one). Filtered, sorted and paginated listing URLs are never listed (they are noindex).
 * Without a configured origin there are no absolute URLs, so the sitemap is empty.
 * Campaign slugs are deduplicated; anything that is not a well-formed slug was already dropped by the reader.
 */
export function buildSitemap(origin: URL | null, data: SitemapData | null): MetadataRoute.Sitemap {
  if (!origin) return [];
  const entries: MetadataRoute.Sitemap = STATIC_PUBLIC_PATHS.map((path) => ({
    url: absoluteUrl(path, origin)!,
    changeFrequency: path === "/" || path === "/campaigns" ? "daily" : "monthly",
  }));
  if (!data) return entries;
  const seen = new Set<string>();
  for (const c of data.categories) {
    const url = absoluteUrl(categoryPath(c.slug), origin)!;
    if (seen.has(url)) continue;
    seen.add(url);
    entries.push({ url, lastModified: lastModified(c.updated_at), changeFrequency: "daily" });
  }
  for (const p of data.provinces ?? []) {
    const url = absoluteUrl(`/campaigns/location/${p.slug}`, origin)!;
    if (seen.has(url)) continue;
    seen.add(url);
    entries.push({ url, changeFrequency: "daily" });
  }
  for (const c of data.campaigns) {
    const url = absoluteUrl(campaignPath(c.slug), origin)!;
    if (seen.has(url)) continue;
    seen.add(url);
    entries.push({ url, lastModified: lastModified(c.updated_at), changeFrequency: "weekly" });
  }
  return entries;
}
