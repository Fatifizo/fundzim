import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { PayoutDestinationsManager } from "@/components/verification/payout-destinations";
import { requireUser, serverGet, serverGetOptional } from "@/lib/auth/session";
import type { Beneficiary, MyOrganisation, PayoutDestination } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Payout accounts", robots: { index: false, follow: false, nocache: true } };

const PATH = "/dashboard/verification/payout-destinations";

export default async function PayoutDestinationsPage() {
  await requireUser(PATH);
  const [destinations, beneficiaries, organisations] = await Promise.all([
    serverGet<PayoutDestination[]>("/api/v1/payout-destinations", PATH),
    serverGetOptional<Beneficiary[]>("/api/v1/beneficiaries", PATH, []),
    serverGetOptional<MyOrganisation[]>("/api/v1/me/organisations", PATH, []),
  ]);
  return (
    <AccountPage title="Payout accounts" intro="Bank accounts and mobile money wallets where funds would be sent. Only the last digits are ever shown.">
      <PayoutDestinationsManager initial={destinations} beneficiaries={beneficiaries} organisations={organisations} />
    </AccountPage>
  );
}
