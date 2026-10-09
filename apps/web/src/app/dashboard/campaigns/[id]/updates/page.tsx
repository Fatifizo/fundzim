import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { UpdatesManager } from "@/components/campaigns/updates-manager";
import { requireUser, serverGetOptional } from "@/lib/auth/session";
import { readUpdates } from "@/lib/campaigns/normalise";
import { loadOwnCampaign } from "@/lib/campaigns/server";

export const metadata: Metadata = { title: "Campaign updates", robots: { index: false, follow: false, nocache: true } };

export default async function CampaignUpdatesPage({ params }: PageProps<"/dashboard/campaigns/[id]/updates">) {
  const { id } = await params;
  const path = `/dashboard/campaigns/${id}/updates`;
  await requireUser(path);
  const campaign = await loadOwnCampaign(id, path);
  const updates = readUpdates(await serverGetOptional<unknown>(`/api/v1/campaigns/${campaign.id}/updates`, path, []));
  return (
    <AccountPage title="Updates" intro="Tell supporters how things are going. Updates appear on the public page.">
      <UpdatesManager campaignId={campaign.id} initial={updates} canPost={["ACTIVE", "PAUSED", "COMPLETED"].includes(String(campaign.status))} />
    </AccountPage>
  );
}
