import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, NextPage, toOptions } from "@/components/admin/payments/common";
import { CommsBanner, DeliveriesTable } from "@/components/admin/communications/comms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, pick } from "@/lib/payments/labels";
import { items, nextCursor } from "@/lib/operations/normalise";
import { CHANNELS, DELIVERY_STATUSES, type Delivery } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Deliveries", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/communications/deliveries";
const UUID = /^[0-9a-f-]{36}$/i;

/** Deliveries (`communications.view`): routing decision, state and evidence per channel; masked destinations only. */
export default async function DeliveriesPage({ searchParams }: PageProps<"/admin/communications/deliveries">) {
  await requireStaff();
  const p = await searchParams;
  const rec = typeof p.recipient_user_id === "string" && UUID.test(p.recipient_user_id) ? p.recipient_user_id : undefined;
  const filters = { status: pick(p.status, DELIVERY_STATUSES), channel: pick(p.channel, CHANNELS), recipient_user_id: rec };
  const cursor = typeof p.cursor === "string" ? p.cursor : undefined;
  const data = (await serverGetOr404<unknown>(`/api/v1/admin/communications/deliveries${buildQuery({ ...filters, cursor, limit: "50" })}`, BASE)).data;
  return (
    <AccountPage title="Deliveries" intro="One row per channel of each notification, with the routing decision and what the provider reported.">
      <CommsBanner />
      <FilterForm action={BASE} label="Filter deliveries" fields={[
        { name: "status", label: "Status", value: filters.status, options: toOptions(DELIVERY_STATUSES) },
        { name: "channel", label: "Channel", value: filters.channel, options: toOptions(CHANNELS) },
      ]} />
      <DeliveriesTable items={items<Delivery>(data)} caption="Deliveries" />
      <NextPage base={BASE} query={filters} next={nextCursor(data)} label="Older deliveries" />
    </AccountPage>
  );
}
