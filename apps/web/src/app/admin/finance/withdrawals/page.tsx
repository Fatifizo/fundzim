import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { PayoutBanner, PayoutsTable, WITHDRAWALS_BASE } from "@/components/admin/finance/payouts";
import { FilterForm, NextPage, toOptions } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { first, isCursor } from "@/lib/campaigns/paths";
import { pick } from "@/lib/payments/labels";
import { readPage, readStaffPayout } from "@/lib/payouts/normalise";
import { PAYOUT_STATUSES } from "@/lib/payouts/types";

export const metadata: Metadata = { title: "Withdrawal requests", robots: { index: false, follow: false, nocache: true } };

/** Withdrawal (payout) requests, newest first (`payout.view`; keyset pages). */
export default async function WithdrawalsPage({ searchParams }: PageProps<"/admin/finance/withdrawals">) {
  await requireStaff();
  const sp = await searchParams;
  const status = pick(sp.status, PAYOUT_STATUSES);
  const recon = first(sp.needs_reconciliation) === "true" ? "true" : undefined;
  const cursor = first(sp.cursor);
  const q = new URLSearchParams();
  if (status) q.set("status", status);
  if (recon) q.set("needs_reconciliation", recon);
  if (cursor && isCursor(cursor)) q.set("cursor", cursor);
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payout-requests?${q.toString()}`, WITHDRAWALS_BASE);
  const page = readPage(result.data, readStaffPayout);
  return (
    <AccountPage title="Withdrawal requests" intro="Payout requests from campaign owners. Funds are reserved in the ledger at request time and released if the payout is rejected, cancelled or fails.">
      <PayoutBanner />
      <FilterForm
        action={WITHDRAWALS_BASE}
        label="Filter withdrawal requests"
        fields={[
          { name: "status", label: "Status", value: status, options: toOptions(PAYOUT_STATUSES) },
          { name: "needs_reconciliation", label: "Reconciliation", value: recon, options: [{ value: "true", label: "Needs reconciliation" }] },
        ]}
      />
      <PayoutsTable items={page.items} caption="Withdrawal requests" />
      <NextPage base={WITHDRAWALS_BASE} query={{ status, needs_reconciliation: recon }} next={page.next_cursor} />
    </AccountPage>
  );
}
