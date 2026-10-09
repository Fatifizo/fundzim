import "server-only";

import { notFound } from "next/navigation";

import { serverGetOptional, serverGetOr404 } from "@/lib/auth/session";

import { readCampaign, readCategories, readCurrencies, readMedia } from "./normalise";
import { isUuid } from "./paths";
import type { Campaign, CampaignMedia, Category, CurrencyInfo } from "./types";

/**
 * Server Component loaders for the owner's campaign pages. A malformed id is a 404 before any API call; the
 * API answers 404 (CAMPAIGN_NOT_FOUND) to anyone who is not the owner or an organisation member, and that
 * renders this app's 404 page (no hint that the campaign exists).
 */
export async function loadOwnCampaign(id: string, currentPath: string): Promise<Campaign> {
  if (!isUuid(id)) notFound();
  const result = await serverGetOr404<unknown>(`/api/v1/campaigns/${id}`, currentPath);
  return readCampaign(result.data);
}

export async function loadOwnMedia(id: string, currentPath: string): Promise<CampaignMedia[]> {
  return readMedia(await serverGetOptional<unknown>(`/api/v1/campaigns/${id}/media`, currentPath, []));
}

export async function loadCategories(currentPath: string): Promise<Category[]> {
  return readCategories(await serverGetOptional<unknown>("/api/v1/campaign-categories", currentPath, []));
}

/** Usable goal currencies (`GET /campaign-currencies`); anything marked unavailable is dropped as well. */
export async function loadCurrencies(currentPath: string): Promise<CurrencyInfo[]> {
  return readCurrencies(await serverGetOptional<unknown>("/api/v1/campaign-currencies", currentPath, []));
}
