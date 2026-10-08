import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { SessionsList } from "@/components/account/sessions-list";
import { requireUser, serverGet } from "@/lib/auth/session";
import type { SessionItem } from "@/lib/auth/types";

export const metadata: Metadata = { title: "Sessions", robots: { index: false, follow: false } };

export default async function SessionsPage() {
  await requireUser("/settings/sessions");
  const sessions = await serverGet<SessionItem[]>("/api/v1/me/sessions", "/settings/sessions");
  return (
    <AccountPage title="Sessions" intro="Devices and browsers where your account is signed in.">
      <SessionsList sessions={sessions} />
    </AccountPage>
  );
}
