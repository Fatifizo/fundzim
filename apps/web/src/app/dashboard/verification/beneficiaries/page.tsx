import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { BeneficiariesManager } from "@/components/verification/beneficiaries";
import { requireUser, serverGet, serverGetOptional } from "@/lib/auth/session";
import type { Beneficiary, MyOrganisation } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Beneficiaries", robots: { index: false, follow: false, nocache: true } };

const PATH = "/dashboard/verification/beneficiaries";

export default async function BeneficiariesPage() {
  await requireUser(PATH);
  const [beneficiaries, organisations] = await Promise.all([
    serverGet<Beneficiary[]>("/api/v1/beneficiaries", PATH),
    serverGetOptional<MyOrganisation[]>("/api/v1/me/organisations", PATH, []),
  ]);
  return (
    <AccountPage
      title="Beneficiaries"
      intro="The person, group or organisation who will benefit from the funds. Each beneficiary is verified before campaigns for them can go live."
    >
      <BeneficiariesManager initial={beneficiaries} organisations={organisations} />
    </AccountPage>
  );
}
