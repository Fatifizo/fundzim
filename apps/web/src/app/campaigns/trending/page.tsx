import type { Metadata } from "next";

import { CampaignGrid } from "@/components/campaigns/campaign-card";
import { ButtonLink } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Container } from "@/components/ui/container";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { fetchTrending } from "@/lib/discovery/server";
import { pageMetadata } from "@/lib/seo/metadata";

export function generateMetadata(): Metadata {
  return pageMetadata({
    title: "Popular campaigns right now",
    description: "Campaigns on FundZim that many different people viewed and shared recently. Money is never part of the ranking.",
    path: "/campaigns/trending",
  });
}

/** Trending campaigns with the rule that ranks them (shown in full: nothing about the list is hidden). */
export default async function TrendingPage() {
  const trending = await fetchTrending(24);
  return (
    <Container className="py-10 sm:py-14">
      <div className="space-y-6">
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">Popular right now</h1>
          <p className="mt-2 max-w-3xl text-lg text-ink-600">Campaigns that many different people viewed and shared recently.</p>
        </div>
        <Card className="space-y-2">
          <h2 className="font-semibold text-ink-900">How this list is made</h2>
          <p className="text-ink-700">{trending.ok && trending.data.rule ? trending.data.rule : "Ranked by recent views and shares from different people; money is never part of the ranking."}</p>
          {trending.ok && trending.data.windowDays > 0 ? (
            <p className="text-sm text-ink-600">
              Engagement counted over the last {trending.data.windowDays} days. {trending.data.policyVersion === null ? "Engineering default settings." : `Ranking settings version ${trending.data.policyVersion}.`}
            </p>
          ) : null}
        </Card>
        {!trending.ok ? (
          <ErrorState title="Popular campaigns could not be loaded" description="Please try again in a moment." action={<ButtonLink href="/campaigns/trending" variant="outline">Try again</ButtonLink>} />
        ) : trending.data.items.length === 0 ? (
          <EmptyState title="Nothing is trending yet" description="Campaigns appear here once enough different people have viewed them." action={<ButtonLink href="/campaigns" variant="outline">Browse every campaign</ButtonLink>} />
        ) : (
          <CampaignGrid campaigns={trending.data.items} label="Popular campaigns" headingLevel={2} priorityFirst surface="TRENDING" />
        )}
      </div>
    </Container>
  );
}
