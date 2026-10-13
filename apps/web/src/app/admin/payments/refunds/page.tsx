import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, NextPage, PAYMENTS_BASE, toOptions } from "@/components/admin/payments/common";
import { RefundExecutionDisabledNotice, RefundList } from "@/components/admin/payments/lists";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readNextCursor } from "@/lib/campaigns/normalise";
import { isCursor } from "@/lib/campaigns/paths";
import { buildQuery, cursorParam, pick } from "@/lib/payments/labels";
import { readRefunds } from "@/lib/payments/normalise";
import { REFUND_STATUSES } from "@/lib/payments/types";

export const metadata: Metadata = { title: "Refund requests", robots: { index: false, follow: false, nocache: true } };

const BASE = `${PAYMENTS_BASE}/refunds`;

/** Refund requests (`payment.view`); decisions need `refund.approve` and a step-up, and the requester can't decide. */
export default async function RefundRequestsPage({ searchParams }: PageProps<"/admin/payments/refunds">) {
  const me = await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, REFUND_STATUSES);
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payments/refund-requests${buildQuery({ status, cursor: cursorParam(p.cursor) })}`, BASE);
  const items = readRefunds(result.data);
  const next = readNextCursor(result.data, result.meta);
  return (
    <AccountPage title="Refund requests" intro="Refund requests under maker-checker: the person who requests a refund can't approve it.">
      <RefundExecutionDisabledNotice />
      <FilterForm action={BASE} label="Filter refund requests" fields={[{ name: "status", label: "Status", value: status, options: toOptions(REFUND_STATUSES) }]} />
      <RefundList items={items} meId={me.id} />
      <NextPage base={BASE} query={{ status }} next={next && isCursor(next) ? next : null} />
    </AccountPage>
  );
}
