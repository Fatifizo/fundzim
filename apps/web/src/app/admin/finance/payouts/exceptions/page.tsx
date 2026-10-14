import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { PayoutAction } from "@/components/admin/finance/payout-action";
import { PAYOUTS_BASE, PayoutBanner, withdrawalHref } from "@/components/admin/finance/payouts";
import { StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readException, readPage } from "@/lib/payouts/normalise";

export const metadata: Metadata = { title: "Payout exceptions", robots: { index: false, follow: false, nocache: true } };

/** Exception queue: open first, by severity, oldest first. Resolving never moves money. */
export default async function PayoutExceptionsPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/payouts/exceptions?include_resolved=true", `${PAYOUTS_BASE}/exceptions`);
  const page = readPage(result.data, readException);
  return (
    <AccountPage title="Payout exceptions" intro="Unknown outcomes, holds, ledger and reconciliation problems that need a person.">
      <PayoutBanner />
      {page.items.length === 0 ? (
        <EmptyState title="No payout exceptions" />
      ) : (
        <TableShell caption="Payout exceptions" head={["Exception", "Severity", "Status", "Detected", "Actions"]} minWidthClass="min-w-[60rem]">
          {page.items.map((e) => (
            <tr key={e.id} className="border-b border-line align-top last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                {humanise(e.exception_type)}
                <span className="block text-sm">
                  {humanise(e.source)}
                  {e.payout_request_id ? (
                    <>
                      {" · "}
                      <Link href={withdrawalHref(e.payout_request_id)} className="text-brand-700 underline">
                        Withdrawal request
                      </Link>
                    </>
                  ) : null}
                </span>
              </th>
              <td className="p-3">
                <Badge tone={e.severity === "SEV1" ? "gold" : "neutral"}>{e.severity}</Badge>
              </td>
              <td className="p-3">
                <StatusBadge status={e.status} />
                {e.resolution_code ? <span className="block text-sm">{humanise(e.resolution_code)}</span> : null}
              </td>
              <td className="p-3 text-sm">
                <When at={e.detected_at} />
              </td>
              <td className="space-y-2 p-3">
                {/* always mounted: the confirmation notice must survive the refresh that changes the status */}
                <PayoutAction target={{ kind: "investigate", id: e.id }} available={e.status === "OPEN"} />
                <PayoutAction target={{ kind: "resolve", id: e.id }} available={e.status !== "RESOLVED"} />
              </td>
            </tr>
          ))}
        </TableShell>
      )}
    </AccountPage>
  );
}
