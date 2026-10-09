import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { LifecycleActions } from "@/components/campaigns/lifecycle-actions";
import { CampaignStatusBadge } from "@/components/campaigns/status";
import { VisibilityForm } from "@/components/campaigns/visibility-form";
import { Card } from "@/components/ui/card";
import { requireUser } from "@/lib/auth/session";
import { VISIBILITY_EDITABLE } from "@/lib/campaigns/labels";
import { loadOwnCampaign } from "@/lib/campaigns/server";

export const metadata: Metadata = { title: "Campaign settings", robots: { index: false, follow: false, nocache: true } };

export default async function CampaignSettingsPage({ params }: PageProps<"/dashboard/campaigns/[id]/settings">) {
  const { id } = await params;
  const path = `/dashboard/campaigns/${id}/settings`;
  await requireUser(path);
  const campaign = await loadOwnCampaign(id, path);
  const status = String(campaign.status);
  return (
    <AccountPage title="Settings" intro={<CampaignStatusBadge status={status} />}>
      <Card as="section" aria-labelledby="visibility-title">
        <h2 id="visibility-title" className="font-display text-xl font-semibold text-ink-900">
          Visibility
        </h2>
        <div className="mt-4">
          {VISIBILITY_EDITABLE.has(status) ? (
            <VisibilityForm campaignId={campaign.id} visibility={String(campaign.visibility)} version={campaign.version} />
          ) : (
            <p className="text-ink-700">Visibility can&apos;t be changed in the campaign&apos;s current status.</p>
          )}
        </div>
      </Card>
      <Card as="section" aria-labelledby="lifecycle-title">
        <h2 id="lifecycle-title" className="font-display text-xl font-semibold text-ink-900">
          Pause, complete, cancel or archive
        </h2>
        <p className="mt-2 text-ink-700">Cancelling and archiving cannot be undone. History is always kept.</p>
        <div className="mt-4">
          <LifecycleActions campaignId={campaign.id} status={status} organisationId={campaign.organisation_id} only={["pause", "resume", "complete", "cancel", "archive"]} />
        </div>
      </Card>
    </AccountPage>
  );
}
