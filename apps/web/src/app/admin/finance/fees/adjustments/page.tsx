import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FeeAction } from "@/components/admin/finance/fee-action";
import { AdjustmentsTable, FEES_BASE, FeeBanner } from "@/components/admin/finance/fees";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readAdjustment, readFeePage } from "@/lib/fees/normalise";

export const metadata: Metadata = { title: "Fee adjustments", robots: { index: false, follow: false, nocache: true } };

/** Fee adjustments (`fee.view`; deciding needs `fee.adjust.approve` and step-up; never the requester). */
export default async function FeeAdjustmentsPage() {
  await requireStaff();
  const page = readFeePage((await serverGetOr404<unknown>("/api/v1/admin/fees/adjustments?limit=200", `${FEES_BASE}/adjustments`)).data, readAdjustment);
  return (
    <AccountPage title="Fee adjustments" intro="Corrections are new records; an assessed fee is never edited. Manual adjustments need a different person's approval and never post.">
      <FeeBanner />
      <AdjustmentsTable
        items={page.items}
        caption="Fee adjustments"
        actions={(a) => (
          <>
            <FeeAction target={{ kind: "adjustment-approve", id: a.id }} available={a.status === "PENDING_APPROVAL"} />
            <FeeAction target={{ kind: "adjustment-reject", id: a.id }} available={a.status === "PENDING_APPROVAL"} />
          </>
        )}
      />
    </AccountPage>
  );
}
