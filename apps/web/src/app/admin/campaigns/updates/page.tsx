import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { UpdateModeration } from "@/components/admin/update-moderation";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readModeration } from "@/lib/campaigns/normalise";

export const metadata: Metadata = { title: "Update moderation", robots: { index: false, follow: false, nocache: true } };

const PATH = "/admin/campaigns/updates";

export default async function UpdateModerationPage() {
  await requireStaff();
  const items = readModeration((await serverGetOr404<unknown>("/api/v1/admin/campaigns/updates/moderation", PATH)).data);
  return (
    <AccountPage title="Update moderation" intro="Campaign updates waiting for moderation.">
      <UpdateModeration items={items} />
    </AccountPage>
  );
}
