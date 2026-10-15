import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FeeTestBanner } from "@/components/fees/common";
import { DonationQuotePreview } from "@/components/fees/quote-preview";
import { requireUser } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";

export const metadata: Metadata = { title: "Donation fee preview", robots: { index: false, follow: false, nocache: true } };

/**
 * Development preview of the donor fee disclosure (signed in only). Public checkout stays disabled: this asks the
 * API for a fee quote and shows its breakdown; it never offers to pay.
 */
export default async function DonationPreviewPage({ searchParams }: PageProps<"/dashboard/donation-preview">) {
  await requireUser("/dashboard/donation-preview");
  const sp = await searchParams;
  const raw = Array.isArray(sp.campaign) ? sp.campaign[0] : sp.campaign;
  const campaign = typeof raw === "string" && isUuid(raw) ? raw : undefined;
  return (
    <AccountPage title="Donation fee preview" intro="Development preview: what a donor would be shown before paying. Donations are not yet available.">
      <FeeTestBanner />
      <DonationQuotePreview campaignId={campaign} />
    </AccountPage>
  );
}
