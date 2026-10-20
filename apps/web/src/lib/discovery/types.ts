/**
 * Wire types of the Stage 16 discovery API (api/openapi/fundzim-v1.yaml, tag "Discovery (Stage 16)"). Read defensively
 * by ./normalise.ts: a missing optional field never breaks a page.
 */
import type { ListTotal, PublicCampaignSummary, PublicCategory, PublicLocation } from "@/lib/campaigns/types";

export type { PublicLocation };

export interface Segment {
  text: string;
  match: boolean;
}

export interface DiscoveryReason {
  code: string;
  text: string;
}

export interface DiscoveryCard extends PublicCampaignSummary {
  location: PublicLocation | null;
  highlights: { title: Segment[]; summary: Segment[] } | null;
  reason: DiscoveryReason | null;
}

export interface DiscoveryPage {
  items: DiscoveryCard[];
  nextCursor: string | null;
  total: ListTotal | null;
  /** EXACT | FUZZY | NONE | ALL */
  match: string;
  query: string;
  sort: string;
}

export interface LocationFacet {
  code: string;
  kind: string;
  slug: string;
  name: string;
  province_code: string | null;
  count: number;
}

export interface LocationsResult {
  provinces: LocationFacet[];
  towns: LocationFacet[];
  unspecified: number;
  note: string;
}

export interface CategoryFacet extends PublicCategory {
  discoverable: number;
}

export interface TrendingResult {
  items: DiscoveryCard[];
  rule: string;
  policyVersion: number | null;
  windowDays: number;
}

export interface Recommendations {
  items: DiscoveryCard[];
  personalised: false;
  rules: string;
}

export interface DiscoverySitemap {
  campaigns: Array<{ slug: string; updated_at: string | null }>;
  categories: Array<{ slug: string; updated_at: string | null }>;
  provinces: Array<{ slug: string; updated_at: string | null }>;
}

export interface CampaignAnalytics {
  from: string;
  to: string;
  days: number;
  nature: string;
  totals: Record<string, number>;
  sharesByChannel: Record<string, number>;
  referralsByChannel: Record<string, number>;
  trafficSources: Record<string, number>;
  impressionsBySurface: Record<string, number>;
  clicksBySurface: Record<string, number>;
  clickThroughBp: number | null;
  daily: Array<{ day: string; views: number; unique_visitors: number; shares: number }>;
  definitions: Record<string, string>;
}
