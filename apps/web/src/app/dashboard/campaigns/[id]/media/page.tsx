import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { MediaManager } from "@/components/campaigns/media-manager";
import { requireUser } from "@/lib/auth/session";
import { EDITABLE_STATUSES } from "@/lib/campaigns/labels";
import { loadOwnCampaign, loadOwnMedia } from "@/lib/campaigns/server";

export const metadata: Metadata = { title: "Campaign photos", robots: { index: false, follow: false, nocache: true } };

export default async function CampaignMediaPage({ params }: PageProps<"/dashboard/campaigns/[id]/media">) {
  const { id } = await params;
  const path = `/dashboard/campaigns/${id}/media`;
  await requireUser(path);
  const campaign = await loadOwnCampaign(id, path);
  const media = await loadOwnMedia(campaign.id, path);
  return (
    <AccountPage title="Photos" intro="A cover photo is required. Photos are checked and processed before anyone can see them.">
      <MediaManager campaignId={campaign.id} initial={media} editable={EDITABLE_STATUSES.has(String(campaign.status))} />
    </AccountPage>
  );
}
