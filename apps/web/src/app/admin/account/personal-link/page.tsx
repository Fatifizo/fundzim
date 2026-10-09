import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { PersonalLinkRequest } from "@/components/admin/personal-link";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readStaffLink } from "@/lib/campaigns/normalise";

export const metadata: Metadata = { title: "Personal account link", robots: { index: false, follow: false, nocache: true } };

const PATH = "/admin/account/personal-link";

export default async function PersonalLinkPage() {
  await requireStaff();
  const status = readStaffLink((await serverGetOr404<unknown>("/api/v1/admin/me/personal-account-link", PATH)).data);
  return (
    <AccountPage
      title="Link your personal account"
      intro="If you also use FundZim as a member of the public, link that personal account so you are never asked to review your own campaigns or verifications."
    >
      <PersonalLinkRequest initial={status} />
    </AccountPage>
  );
}
