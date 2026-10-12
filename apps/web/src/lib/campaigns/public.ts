import "server-only";

import { headers } from "next/headers";
import { connection } from "next/server";
import { cache } from "react";

import { isApiError } from "@/lib/api/errors";
import { getServerApi } from "@/lib/api/server";
import { clientIpFromHeaders } from "@/lib/net/client-ip";

import {
  readCanonicalSlug,
  readNextCursor,
  readPublicCampaign,
  readPublicCategories,
  readPublicCategory,
  readPublicSummaries,
  readPublicUpdates,
  readSitemap,
  readTotal,
  withCategoryName,
} from "./normalise";
import { isCategorySlug, isPublicCode, isSlug, normalisePublicCode } from "./paths";
import type { ListTotal, PublicCampaign, PublicCampaignSummary, PublicCategory, PublicUpdate, SitemapData } from "./types";

/**
 * Public (no session) reads for the public site (docs/stage-7/interface-contracts.md §3). No cookies are
 * forwarded: these responses are the same for everyone. Only the observed client address goes along, for the
 * API's public rate limits. Campaign data is fetched with `cache: 'no-store'` (the API client's default,
 * ADR-038 §1): there is no server-side copy of campaign state, so a suspended campaign disappears at once.
 */
async function publicHeaders(): Promise<Record<string, string>> {
  await connection();
  const ip = clientIpFromHeaders(await headers());
  return ip ? { "X-Forwarded-For": ip } : {};
}

async function get(path: string, timeoutMs = 8_000) {
  return getServerApi({ timeoutMs, maxRetries: 1 }).get<unknown>(path, { headers: await publicHeaders(), cache: "no-store" });
}

/** A load that may fail without breaking the whole page (home sections, category list). */
export type Loaded<T> = { ok: true; data: T } | { ok: false };

/** Active public categories with live counts; `{ok: false}` when the API is unavailable. */
export const fetchPublicCategories = cache(async (): Promise<Loaded<PublicCategory[]>> => {
  try {
    return { ok: true, data: readPublicCategories((await get("/api/v1/public/categories", 5_000)).data) };
  } catch {
    return { ok: false };
  }
});

/** Categories for labelling and filters: an empty list when unavailable (names then fall back to the code). */
export async function fetchCategories(): Promise<PublicCategory[]> {
  const result = await fetchPublicCategories();
  return result.ok ? result.data : [];
}

/** One category by slug, or null when unknown/inactive (404 CATEGORY_NOT_FOUND). Other errors throw. */
export const fetchCategory = cache(async (slug: string): Promise<PublicCategory | null> => {
  if (!isCategorySlug(slug)) return null;
  try {
    return readPublicCategory((await get(`/api/v1/public/categories/${slug}`, 5_000)).data);
  } catch (error) {
    if (isApiError(error) && error.status === 404) return null;
    throw error;
  }
});

/**
 * The published campaign for a slug, or null when the API says it is not public (404). A non-canonical slug
 * whose public code matches comes back with `canonical_slug` set: the page redirects. Other errors throw.
 */
export const fetchPublicCampaign = cache(async (slug: string): Promise<PublicCampaign | null> => {
  if (!isSlug(slug)) return null;
  try {
    const campaign = readPublicCampaign((await get(`/api/v1/public/campaigns/${slug}`)).data);
    const canonical = isSlug(campaign.canonical_slug) ? campaign.canonical_slug : isSlug(campaign.slug) ? campaign.slug : slug;
    return withCategoryName({ ...campaign, slug: canonical, canonical_slug: canonical }, await fetchCategories());
  } catch (error) {
    if (isApiError(error) && error.status === 404) return null;
    throw error;
  }
});

/** Canonical slug for a short-link public code, or null when it is not public. Other errors throw. */
export async function fetchSlugByCode(code: string): Promise<string | null> {
  if (!isPublicCode(code)) return null;
  try {
    return readCanonicalSlug((await get(`/api/v1/public/campaign-codes/${normalisePublicCode(code)}`, 5_000)).data);
  } catch (error) {
    if (isApiError(error) && error.status === 404) return null;
    throw error;
  }
}

export async function fetchPublicUpdates(slug: string): Promise<Loaded<PublicUpdate[]>> {
  try {
    return { ok: true, data: readPublicUpdates((await get(`/api/v1/public/campaigns/${slug}/updates`, 5_000)).data) };
  } catch {
    return { ok: false }; // Updates are secondary: the page still renders without them.
  }
}

export interface PublicListResult {
  items: PublicCampaignSummary[];
  nextCursor: string | null;
  total: ListTotal | null;
}

/** One page of `GET /public/campaigns` (query already validated by the caller). Errors throw. */
export async function fetchPublicList(query: URLSearchParams): Promise<PublicListResult> {
  const result = await get(`/api/v1/public/campaigns${query.size ? `?${query.toString()}` : ""}`);
  const categories = await fetchCategories();
  return {
    items: readPublicSummaries(result.data).map((c) => withCategoryName(c, categories)),
    nextCursor: readNextCursor(result.data, result.meta),
    total: readTotal(result.data, result.meta),
  };
}

/** Featured campaigns (staff picks, then recently published; ADR-038 §2). */
export async function fetchFeatured(limit = 6): Promise<Loaded<PublicCampaignSummary[]>> {
  try {
    const result = await get(`/api/v1/public/campaigns/featured?limit=${Math.min(Math.max(limit, 1), 12)}`, 5_000);
    const categories = await fetchCategories();
    return { ok: true, data: readPublicSummaries(result.data).map((c) => withCategoryName(c, categories)) };
  } catch {
    return { ok: false };
  }
}

/** Live PUBLIC campaigns and active categories for /sitemap.xml; null when the API is unavailable. */
export async function fetchSitemapData(): Promise<SitemapData | null> {
  try {
    return readSitemap((await get("/api/v1/public/sitemap", 10_000)).data);
  } catch {
    return null;
  }
}
