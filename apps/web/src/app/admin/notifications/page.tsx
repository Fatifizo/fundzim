import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { NextPage } from "@/components/admin/payments/common";
import { Inbox } from "@/components/notifications/inbox";
import { requireStaff, serverGetResult } from "@/lib/auth/session";
import { buildQuery, pick } from "@/lib/payments/labels";
import { items, nextCursor } from "@/lib/operations/normalise";
import type { InboxItem } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "My notifications", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/notifications";

/** The staff member's own in-app notifications (assignments, approvals waiting, incidents, escalations). In-app only. */
export default async function StaffNotificationsPage({ searchParams }: PageProps<"/admin/notifications">) {
  await requireStaff();
  const p = await searchParams;
  const unread = pick(p.unread, ["true"] as const);
  const cursor = typeof p.cursor === "string" ? p.cursor : undefined;
  const data = (await serverGetResult<unknown>(`/api/v1/notifications${buildQuery({ unread, cursor, limit: "25" })}`, BASE)).data;
  return (
    <AccountPage title="My notifications" intro="Work routed to you. Staff notifications are shown in the app only.">
      <Inbox items={items<InboxItem>(data)} />
      <NextPage base={BASE} query={{ unread }} next={nextCursor(data)} label="Older notifications" />
    </AccountPage>
  );
}
