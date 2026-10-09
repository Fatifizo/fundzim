import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { BeneficiaryPicker } from "@/components/campaigns/beneficiary-picker";
import { requireUser, serverGetOptional } from "@/lib/auth/session";
import { loadOwnCampaign } from "@/lib/campaigns/server";
import type { Beneficiary } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Campaign beneficiary", robots: { index: false, follow: false, nocache: true } };

const CHANGEABLE = new Set(["DRAFT", "CHANGES_REQUESTED", "ACTIVE", "PAUSED"]);

export default async function CampaignBeneficiaryPage({ params }: PageProps<"/dashboard/campaigns/[id]/beneficiaries">) {
  const { id } = await params;
  const path = `/dashboard/campaigns/${id}/beneficiaries`;
  await requireUser(path);
  const [campaign, beneficiaries] = await Promise.all([loadOwnCampaign(id, path), serverGetOptional<Beneficiary[]>("/api/v1/beneficiaries", path, [])]);
  return (
    <AccountPage title="Beneficiary" intro="Who the funds are for, and what the public page shows about them.">
      <BeneficiaryPicker
        campaignId={campaign.id}
        organisationId={campaign.organisation_id}
        beneficiaries={Array.isArray(beneficiaries) ? beneficiaries : []}
        current={campaign.beneficiary}
        editable={CHANGEABLE.has(String(campaign.status))}
      />
    </AccountPage>
  );
}
