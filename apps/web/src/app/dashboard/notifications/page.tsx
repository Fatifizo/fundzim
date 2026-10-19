import type { Metadata } from "next";
import Link from "next/link";

import { AccountContainer, AccountPage } from "@/components/account/account-shell";
import { FilterForm, NextPage, toOptions } from "@/components/admin/payments/common";
import { Inbox } from "@/components/notifications/inbox";
import { requireUser, serverGetResult } from "@/lib/auth/session";
import { buildQuery, pick } from "@/lib/payments/labels";
import { items, nextCursor } from "@/lib/operations/normalise";
import { CATEGORIES, type InboxItem } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Notifications", robots: { index: false, follow: false, nocache: true } };

const BASE = "/dashboard/notifications";

/** The signed-in person's in-app notifications (their own only: the API scopes every query to the caller). */
export default async function NotificationsPage({ searchParams }: PageProps<"/dashboard/notifications">) {
  await requireUser(BASE);
  const p = await searchParams;
  const filters = { unread: pick(p.unread, ["true"] as const), category: pick(p.category, CATEGORIES) };
  const cursor = typeof p.cursor === "string" ? p.cursor : undefined;
  const data = (await serverGetResult<unknown>(`/api/v1/notifications${buildQuery({ ...filters, cursor, limit: "25" })}`, BASE)).data;
  return (
    <AccountContainer>
      <AccountPage title="Notifications" intro={<>Messages about your account, campaigns, verification and TEST payments. <Link href={`${BASE}/preferences`} className="underline underline-offset-4">Choose how you are notified</Link>.</>}>
        <FilterForm
          action={BASE}
          label="Filter notifications"
          fields={[
            { name: "unread", label: "Show", value: filters.unread, options: [{ value: "true", label: "Unread only" }] },
            { name: "category", label: "Category", value: filters.category, options: toOptions(CATEGORIES.filter((c) => !["RISK", "COMPLIANCE", "ADMINISTRATIVE"].includes(c))) },
          ]}
        />
        <Inbox items={items<InboxItem>(data)} />
        <NextPage base={BASE} query={filters} next={nextCursor(data)} label="Older notifications" />
      </AccountPage>
    </AccountContainer>
  );
}
