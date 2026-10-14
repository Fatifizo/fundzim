import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { PAYOUTS_BASE, PayoutBanner, PayoutsTable } from "@/components/admin/finance/payouts";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readPage, readStaffPayout } from "@/lib/payouts/normalise";

export const metadata: Metadata = { title: "Payout approvals", robots: { index: false, follow: false, nocache: true } };

/** Payouts waiting for FINANCE approval (maker-checker; DUAL needs two distinct approvers). */
export default async function PayoutApprovalsPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/payout-requests?status=PENDING_REVIEW", `${PAYOUTS_BASE}/approvals`);
  const page = readPage(result.data, readStaffPayout);
  return (
    <AccountPage title="Payout approvals" intro="Open a request to review its 22 eligibility checks and decide. You can't approve a payout you requested, own or are linked to.">
      <PayoutBanner />
      <PayoutsTable items={page.items} caption="Payouts awaiting approval" empty="Nothing is waiting for approval" />
    </AccountPage>
  );
}
