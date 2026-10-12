import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FeaturedManager } from "@/components/admin/featured-manager";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readFeaturedPicks } from "@/lib/campaigns/normalise";
import { first, isUuid } from "@/lib/campaigns/paths";

export const metadata: Metadata = { title: "Featured campaigns", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/campaigns/featured";

/** Staff picks for the home page (`campaign.feature`; 404 for anyone else). `?campaign_id=` pre-fills the add form. */
export default async function FeaturedCampaignsPage({ searchParams }: PageProps<"/admin/campaigns/featured">) {
  await requireStaff();
  const idRaw = first((await searchParams).campaign_id);
  const result = await serverGetOr404<unknown>("/api/v1/admin/campaigns/featured", BASE);
  return (
    <AccountPage title="Featured campaigns" intro="Staff picks appear first in the home page's featured list; recently published campaigns fill the rest.">
      <FeaturedManager items={readFeaturedPicks(result.data)} initialCampaignId={isUuid(idRaw) ? idRaw : ""} />
    </AccountPage>
  );
}
