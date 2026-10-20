import "server-only";

import { headers } from "next/headers";
import { connection } from "next/server";
import { cache } from "react";

import { serverGetOr404 } from "@/lib/auth/session";
import { getServerApi } from "@/lib/api/server";
import { fetchCategories } from "@/lib/campaigns/public";
import { withCategoryName } from "@/lib/campaigns/normalise";
import { clientIpFromHeaders } from "@/lib/net/client-ip";

import {
  readCampaignAnalytics,
  readCategoryFacets,
  readDiscoveryPage,
  readDiscoverySitemap,
  readLocations,
  readRecommendations,
  readTrending,
} from "./normalise";
import { discoveryApiParams, type DiscoveryQuery } from "./query";
import type { CampaignAnalytics, CategoryFacet, DiscoveryCard, DiscoveryPage, DiscoverySitemap, LocationsResult, Recommendations, TrendingResult } from "./types";

/**
 * Discovery reads for Server Components (no session; the same for everyone). Fetched with `cache: 'no-store'` (ADR-038:
 * no server-side copy of campaign state). Only the observed client address goes along, for the API's rate limits.
 */
async function get(path: string, timeoutMs = 8_000) {
  await connection();
  const ip = clientIpFromHeaders(await headers());
  return getServerApi({ timeoutMs, maxRetries: 1 }).get<unknown>(path, { headers: ip ? { "X-Forwarded-For": ip } : {}, cache: "no-store" });
}

export type Loaded<T> = { ok: true; data: T } | { ok: false };

async function named<T extends { items: DiscoveryCard[] }>(x: T): Promise<T> {
  const categories = await fetchCategories();
  return { ...x, items: x.items.map((c) => withCategoryName(c, categories)) };
}

/** One listing page. `surface` picks the endpoint whose impressions it counts (search vs directory). Errors throw. */
export async function fetchDiscoveryPage(q: DiscoveryQuery, limit: number, surface: "search" | "campaigns" = "campaigns"): Promise<DiscoveryPage> {
  const params = discoveryApiParams(q, limit);
  const result = await get(`/api/v1/discovery/${surface}?${params.toString()}`);
  return named(readDiscoveryPage(result.data));
}

export const fetchLocations = cache(async (): Promise<Loaded<LocationsResult>> => {
  try {
    return { ok: true, data: readLocations((await get("/api/v1/discovery/locations", 5_000)).data) };
  } catch {
    return { ok: false };
  }
});

export const fetchCategoryFacets = cache(async (): Promise<Loaded<CategoryFacet[]>> => {
  try {
    return { ok: true, data: readCategoryFacets((await get("/api/v1/discovery/categories", 5_000)).data) };
  } catch {
    return { ok: false };
  }
});

export async function fetchTrending(limit = 12, category = ""): Promise<Loaded<TrendingResult>> {
  try {
    const params = new URLSearchParams({ limit: String(limit) });
    if (category) params.set("category", category);
    return { ok: true, data: await named(readTrending((await get(`/api/v1/discovery/trending?${params.toString()}`, 5_000)).data)) };
  } catch {
    return { ok: false };
  }
}

export async function fetchFeaturedCards(limit = 12, fill = false): Promise<Loaded<DiscoveryCard[]>> {
  try {
    const result = await get(`/api/v1/discovery/featured?limit=${limit}&fill=${fill ? "true" : "false"}`, 5_000);
    return { ok: true, data: (await named(readDiscoveryPage(result.data))).items };
  } catch {
    return { ok: false };
  }
}

/** Recommendations are secondary: failures render nothing. */
export async function fetchRecommendations(campaignSlug: string, limit = 4): Promise<Recommendations | null> {
  try {
    const params = new URLSearchParams({ campaign: campaignSlug, limit: String(limit) });
    return await named(readRecommendations((await get(`/api/v1/discovery/recommendations?${params.toString()}`, 4_000)).data));
  } catch {
    return null;
  }
}

export async function fetchDiscoverySitemap(): Promise<DiscoverySitemap | null> {
  try {
    return readDiscoverySitemap((await get("/api/v1/discovery/sitemap", 10_000)).data);
  } catch {
    return null;
  }
}

/** Owner analytics (session-bound): the API's 404 for anyone who is not the owner or an organisation member renders this
 * app's 404 page; other errors throw (the page's error boundary). */
export async function loadCampaignAnalytics(id: string, days: number, currentPath: string): Promise<CampaignAnalytics> {
  return readCampaignAnalytics((await serverGetOr404<unknown>(`/api/v1/campaigns/${id}/analytics?days=${days}`, currentPath)).data);
}
