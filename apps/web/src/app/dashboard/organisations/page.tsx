import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { OrganisationsList } from "@/components/verification/organisations";
import { requireUser, serverGet } from "@/lib/auth/session";
import type { MyOrganisation } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Organisations", robots: { index: false, follow: false, nocache: true } };

export default async function OrganisationsPage() {
  await requireUser("/dashboard/organisations");
  const organisations = await serverGet<MyOrganisation[]>("/api/v1/me/organisations", "/dashboard/organisations");
  return (
    <AccountPage title="Organisations" intro="Organisations you belong to. Administrators can verify an organisation so it can raise funds.">
      <OrganisationsList initial={organisations} />
    </AccountPage>
  );
}
