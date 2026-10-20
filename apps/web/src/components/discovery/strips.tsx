import Link from "next/link";

import { CampaignGrid } from "@/components/campaigns/campaign-card";
import { fetchFeaturedCards, fetchRecommendations, fetchTrending } from "@/lib/discovery/server";

/**
 * Featured placements (approved by a second staff member, inside their window, promotable now). Renders nothing when
 * there are none or the API is unavailable: the directory below still works.
 */
export async function FeaturedStrip({ limit = 3 }: { limit?: number }) {
  const featured = await fetchFeaturedCards(limit, false);
  if (!featured.ok || featured.data.length === 0) return null;
  return (
    <section aria-labelledby="featured-strip" className="space-y-4">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 id="featured-strip" className="font-display text-2xl font-semibold text-ink-900">
          Featured
        </h2>
        <Link href="/campaigns/featured" prefetch={false} className="inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
          All featured campaigns
        </Link>
      </div>
      <p className="text-ink-600">Chosen by the FundZim team for a set period. Being featured is not an endorsement, and nobody can pay for it.</p>
      <CampaignGrid campaigns={featured.data} label="Featured campaigns" surface="FEATURED" />
    </section>
  );
}

/** Popular right now: the trending rule (explained on /campaigns/trending). Renders nothing without results. */
export async function TrendingStrip({ limit = 3, category = "" }: { limit?: number; category?: string }) {
  const trending = await fetchTrending(limit, category);
  if (!trending.ok || trending.data.items.length === 0) return null;
  return (
    <section aria-labelledby="trending-strip" className="space-y-4">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 id="trending-strip" className="font-display text-2xl font-semibold text-ink-900">
          Popular right now
        </h2>
        <Link href="/campaigns/trending" prefetch={false} className="inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
          How this list is made
        </Link>
      </div>
      <CampaignGrid campaigns={trending.data.items} label="Popular campaigns" surface="TRENDING" />
    </section>
  );
}

/** "More campaigns" on a campaign page: rule-based, the same for everyone; each card says why it is shown. */
export async function RecommendationsSection({ slug }: { slug: string }) {
  const rec = await fetchRecommendations(slug, 3);
  if (!rec || rec.items.length === 0) return null;
  return (
    <section aria-labelledby="more-campaigns" className="space-y-4">
      <h2 id="more-campaigns" className="font-display text-2xl font-semibold text-ink-900">
        More campaigns
      </h2>
      <p className="text-sm text-ink-600">{rec.rules}</p>
      <CampaignGrid campaigns={rec.items} label="More campaigns" surface="RECOMMENDED" />
    </section>
  );
}
