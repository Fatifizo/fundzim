import type { Metadata } from "next";

import { CampaignGrid } from "@/components/campaigns/campaign-card";
import { ButtonLink } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Container } from "@/components/ui/container";
import { EmptyState } from "@/components/ui/empty-state";
import { ErrorState } from "@/components/ui/error-state";
import { fetchFeaturedCards } from "@/lib/discovery/server";
import { pageMetadata } from "@/lib/seo/metadata";

export function generateMetadata(): Metadata {
  return pageMetadata({
    title: "Featured campaigns",
    description: "Campaigns the FundZim team is featuring for a set period. Being featured is not an endorsement, and nobody can pay for it.",
    path: "/campaigns/featured",
  });
}

/** Featured placements only (no fill), with what featuring means and does not mean. */
export default async function FeaturedPage() {
  const featured = await fetchFeaturedCards(12, false);
  return (
    <Container className="py-10 sm:py-14">
      <div className="space-y-6">
        <div>
          <h1 className="font-display text-3xl font-bold tracking-tight text-ink-900 sm:text-4xl">Featured campaigns</h1>
          <p className="mt-2 max-w-3xl text-lg text-ink-600">A changing selection that shows the range of causes on FundZim.</p>
        </div>
        <Card className="space-y-2">
          <h2 className="font-semibold text-ink-900">What featured means</h2>
          <ul className="list-disc space-y-1 pl-5 text-ink-700">
            <li>A team member proposes a campaign for a set period, and a second team member must approve it.</li>
            <li>Only active campaigns without restrictions or open reviews are shown. A featured campaign that stops qualifying disappears at once.</li>
            <li>Being featured is not an endorsement, a verification or a promise about the campaign, and nobody can pay to be featured.</li>
          </ul>
        </Card>
        {!featured.ok ? (
          <ErrorState title="Featured campaigns could not be loaded" description="Please try again in a moment." action={<ButtonLink href="/campaigns/featured" variant="outline">Try again</ButtonLink>} />
        ) : featured.data.length === 0 ? (
          <EmptyState title="Nothing is featured right now" description="Browse every published campaign instead." action={<ButtonLink href="/campaigns" variant="outline">Browse campaigns</ButtonLink>} />
        ) : (
          <CampaignGrid campaigns={featured.data} label="Featured campaigns" headingLevel={2} priorityFirst surface="FEATURED" />
        )}
      </div>
    </Container>
  );
}
