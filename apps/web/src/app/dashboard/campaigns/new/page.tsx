import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { CampaignWizard } from "@/components/campaigns/wizard";
import { requireUser, serverGetOptional } from "@/lib/auth/session";
import { first } from "@/lib/campaigns/paths";
import { parseStep, resumeStep } from "@/lib/campaigns/progress";
import { loadCategories, loadCurrencies, loadOwnCampaign, loadOwnMedia } from "@/lib/campaigns/server";
import type { Beneficiary, KycStatus, MyOrganisation } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Start a campaign", robots: { index: false, follow: false, nocache: true } };

const PATH = "/dashboard/campaigns/new";

export default async function NewCampaignPage({ searchParams }: PageProps<"/dashboard/campaigns/new">) {
  const params = await searchParams;
  const id = first(params.id);
  const stepParam = parseStep(first(params.step));
  const current = id ? `${PATH}?id=${encodeURIComponent(id)}${stepParam ? `&step=${stepParam}` : ""}` : PATH;
  await requireUser(current);
  const [categories, currencies, beneficiaries, organisations, kyc, campaign] = await Promise.all([
    loadCategories(current),
    loadCurrencies(current),
    serverGetOptional<Beneficiary[]>("/api/v1/beneficiaries", current, []),
    serverGetOptional<MyOrganisation[]>("/api/v1/me/organisations", current, []),
    serverGetOptional<KycStatus | null>("/api/v1/kyc/status", current, null),
    id ? loadOwnCampaign(id, current) : Promise.resolve(null),
  ]);
  const media = campaign ? await loadOwnMedia(campaign.id, current) : [];
  const step = campaign ? (stepParam && stepParam >= 1 ? stepParam : resumeStep(campaign, media)) : stepParam && stepParam <= 4 ? stepParam : 1;
  return (
    <AccountPage
      title={campaign ? "Continue your campaign" : "Start a campaign"}
      intro={
        <>
          Eight short steps. Your draft is private until a reviewer approves it and you publish it.{" "}
          <Link href="/dashboard/campaigns" className="text-base font-semibold text-brand-700 underline">
            Your campaigns
          </Link>
        </>
      }
    >
      <CampaignWizard
        key={campaign?.id ?? "new"}
        categories={categories}
        currencies={currencies}
        beneficiaries={Array.isArray(beneficiaries) ? beneficiaries : []}
        organisations={Array.isArray(organisations) ? organisations : []}
        initialCampaign={campaign}
        initialMedia={media}
        initialStep={step}
        verificationLevel={kyc?.level ?? null}
      />
    </AccountPage>
  );
}
