import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { LedgerAmount } from "@/components/admin/finance/common";
import { PayoutAction } from "@/components/admin/finance/payout-action";
import { PAYOUTS_BASE, PayoutBanner, withdrawalHref } from "@/components/admin/finance/payouts";
import { TableShell, When } from "@/components/admin/payments/common";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readPage, readRecon } from "@/lib/payouts/normalise";

export const metadata: Metadata = { title: "Payout reconciliation", robots: { index: false, follow: false, nocache: true } };

/** Mismatches found by payout reconciliation runs (immutable). */
export default async function PayoutReconciliationPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/payouts/reconciliation?limit=100", `${PAYOUTS_BASE}/reconciliation`);
  const page = readPage(result.data, readRecon);
  return (
    <AccountPage
      title="Payout reconciliation"
      intro="Requests, reservations, ledger journals and the provider's status compared. Settlement of payouts against provider statements is not performed: no statements exist yet."
    >
      <PayoutBanner />
      <PayoutAction target={{ kind: "reconcile" }} />
      {page.items.length === 0 ? (
        <EmptyState title="No mismatches recorded" />
      ) : (
        <TableShell caption="Payout reconciliation mismatches" head={["Mismatch", "Payout", "Expected / actual", "Detected"]} minWidthClass="min-w-[56rem]">
          {page.items.map((r) => (
            <tr key={r.id} className="border-b border-line align-top last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                {humanise(r.mismatch_type)}
              </th>
              <td className="p-3 text-sm">
                {r.payout_request_id ? (
                  <Link href={withdrawalHref(r.payout_request_id)} className="text-brand-700 underline">
                    Withdrawal request
                  </Link>
                ) : (
                  "—"
                )}
              </td>
              <td className="p-3 text-sm">
                {r.expected_amount_minor && r.currency ? <LedgerAmount minor={r.expected_amount_minor} currency={r.currency} /> : "—"} /{" "}
                {r.actual_amount_minor && r.currency ? <LedgerAmount minor={r.actual_amount_minor} currency={r.currency} /> : "—"}
              </td>
              <td className="p-3 text-sm">
                <When at={r.detected_at} />
              </td>
            </tr>
          ))}
        </TableShell>
      )}
    </AccountPage>
  );
}
