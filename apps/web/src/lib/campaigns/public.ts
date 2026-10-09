import "server-only";

import { headers } from "next/headers";
import { connection } from "next/server";
import { cache } from "react";

import { isApiError } from "@/lib/api/errors";
import { getServerApi } from "@/lib/api/server";
import type { ApiMeta } from "@/lib/api/types";
import { clientIpFromHeaders } from "@/lib/net/client-ip";

import { readCategories, readNextCursor, readPublicCampaign, readPublicSummaries, readUpdates, withCategoryName } from "./normalise";
import { isSlug } from "./paths";
import type { CampaignUpdate, Category, PublicCampaign, PublicCampaignSummary } from "./types";

/**
 * Public (no session) reads for campaign pages. No cookies are forwarded: these responses are the same for
 * everyone. Only the observed client address goes along, for the API's public rate limits.
 */
async function publicHeaders(): Promise<Record<string, string>> {
  await connection();
  const ip = clientIpFromHeaders(await headers());
  return ip ? { "X-Forwarded-For": ip } : {};
}

/** Active categories (public); an empty list when unavailable (names then fall back to the code). */
export const fetchCategories = cache(async (): Promise<Category[]> => {
  try {
    return readCategories((await getServerApi({ timeoutMs: 5_000, maxRetries: 1 }).get<unknown>("/api/v1/campaign-categories", { headers: await publicHeaders() })).data);
  } catch {
    return [];
  }
});

/** The published campaign for a slug, or null when the API says it is not public (404). Other errors throw. */
export const fetchPublicCampaign = cache(async (slug: string): Promise<PublicCampaign | null> => {
  if (!isSlug(slug)) return null;
  try {
    const result = await getServerApi({ timeoutMs: 8_000, maxRetries: 1 }).get<unknown>(`/api/v1/public/campaigns/${slug}`, { headers: await publicHeaders() });
    const campaign = withCategoryName(readPublicCampaign(result.data), await fetchCategories());
    return campaign.slug === slug ? campaign : { ...campaign, slug };
  } catch (error) {
    if (isApiError(error) && error.status === 404) return null;
    throw error;
  }
});

export async function fetchPublicUpdates(slug: string): Promise<CampaignUpdate[]> {
  try {
    const result = await getServerApi({ timeoutMs: 5_000, maxRetries: 1 }).get<unknown>(`/api/v1/public/campaigns/${slug}/updates`, { headers: await publicHeaders() });
    return readUpdates(result.data);
  } catch {
    return []; // Updates are secondary: the page still renders without them.
  }
}

export async function fetchPublicList(query: string): Promise<{ items: PublicCampaignSummary[]; meta: ApiMeta }> {
  const result = await getServerApi({ timeoutMs: 8_000, maxRetries: 1 }).get<unknown>(`/api/v1/public/campaigns${query ? `?${query}` : ""}`, { headers: await publicHeaders() });
  const categories = await fetchCategories();
  const nextCursor = readNextCursor(result.data, result.meta);
  return { items: readPublicSummaries(result.data).map((c) => withCategoryName(c, categories)), meta: { ...result.meta, ...(nextCursor ? { next_cursor: nextCursor } : {}) } };
}

/**
 * Canonical site origin for absolute Open Graph URLs, from the optional server-side PUBLIC_SITE_URL (an
 * origin only). Without it, URL-based metadata is left out rather than guessed from request headers.
 */
export function siteOrigin(env: Record<string, string | undefined> = process.env): URL | null {
  const raw = env.PUBLIC_SITE_URL?.trim();
  if (!raw) return null;
  try {
    const url = new URL(raw);
    if ((url.protocol !== "https:" && url.protocol !== "http:") || url.username || url.password || (url.pathname !== "/" && url.pathname !== "") || url.search || url.hash) return null;
    return new URL(url.origin);
  } catch {
    return null;
  }
}
