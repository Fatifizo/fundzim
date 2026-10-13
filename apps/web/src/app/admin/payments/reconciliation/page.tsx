import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, NextPage, PAYMENTS_BASE, toOptions } from "@/components/admin/payments/common";
import { ReconList } from "@/components/admin/payments/lists";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readNextCursor } from "@/lib/campaigns/normalise";
import { isCursor } from "@/lib/campaigns/paths";
import { buildQuery, cursorParam, pick } from "@/lib/payments/labels";
import { readRecons } from "@/lib/payments/normalise";
import { RECON_STATUSES } from "@/lib/payments/types";

export const metadata: Metadata = { title: "Reconciliation", robots: { index: false, follow: false, nocache: true } };

const BASE = `${PAYMENTS_BASE}/reconciliation`;

/** Reconciliation references (`payment.view`). Staff can only record "awaiting settlement data" here. */
export default async function ReconciliationPage({ searchParams }: PageProps<"/admin/payments/reconciliation">) {
  await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, RECON_STATUSES);
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payments/reconciliation${buildQuery({ status, cursor: cursorParam(p.cursor) })}`, BASE);
  const items = readRecons(result.data);
  const next = readNextCursor(result.data, result.meta);
  return (
    <AccountPage title="Reconciliation" intro="Payments whose outcome or amount needs checking against the provider. A reference is resolved only by an authoritative provider status.">
      <FilterForm action={BASE} label="Filter reconciliation references" fields={[{ name: "status", label: "Status", value: status, options: toOptions(RECON_STATUSES) }]} />
      <ReconList items={items} />
      <NextPage base={BASE} query={{ status }} next={next && isCursor(next) ? next : null} />
    </AccountPage>
  );
}
