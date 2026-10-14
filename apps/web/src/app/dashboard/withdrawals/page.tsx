import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { OwnerPayoutList, TestFundsBanner } from "@/components/payouts/common";
import { requireUser, serverGetOptional } from "@/lib/auth/session";
import { readOwnerPayout, readPage } from "@/lib/payouts/normalise";

export const metadata: Metadata = { title: "Withdrawals", robots: { index: false, follow: false, nocache: true } };

const PATH = "/dashboard/withdrawals";

/** The organiser's withdrawal requests across their (and their organisations') campaigns. */
export default async function WithdrawalsPage() {
  await requireUser(PATH);
  const page = readPage(await serverGetOptional<unknown>("/api/v1/payout-requests", PATH, { items: [] }), readOwnerPayout);
  return (
    <AccountPage title="Withdrawals" intro="Request withdrawals from a campaign's page. Every request is reviewed by a finance officer.">
      <TestFundsBanner />
      <OwnerPayoutList items={page.items} />
    </AccountPage>
  );
}
