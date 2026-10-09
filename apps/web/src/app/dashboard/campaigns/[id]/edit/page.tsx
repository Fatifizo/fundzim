import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { CampaignEditForm } from "@/components/campaigns/edit-form";
import { Alert } from "@/components/ui/alert";
import { requireUser } from "@/lib/auth/session";
import { EDITABLE_STATUSES } from "@/lib/campaigns/labels";
import { loadCategories, loadCurrencies, loadOwnCampaign } from "@/lib/campaigns/server";

export const metadata: Metadata = { title: "Edit campaign", robots: { index: false, follow: false, nocache: true } };

export default async function EditCampaignPage({ params }: PageProps<"/dashboard/campaigns/[id]/edit">) {
  const { id } = await params;
  const path = `/dashboard/campaigns/${id}/edit`;
  await requireUser(path);
  const [campaign, categories, currencies] = await Promise.all([loadOwnCampaign(id, path), loadCategories(path), loadCurrencies(path)]);
  const editable = EDITABLE_STATUSES.has(String(campaign.status));
  return (
    <AccountPage title="Campaign details" intro="Category, title, summary, story and goal.">
      {editable ? (
        <CampaignEditForm campaign={campaign} categories={categories} currencies={currencies} />
      ) : (
        <Alert tone="info" title="These details can't be changed now">
          A campaign can be edited while it is a draft, when changes are requested, or while it is published or paused.
        </Alert>
      )}
    </AccountPage>
  );
}
