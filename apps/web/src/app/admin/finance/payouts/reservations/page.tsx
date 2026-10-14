import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount } from "@/components/admin/finance/common";
import { PAYOUTS_BASE, PayoutBanner, withdrawalHref } from "@/components/admin/finance/payouts";
import { StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readPage, readReservation } from "@/lib/payouts/normalise";

export const metadata: Metadata = { title: "Payout reservations", robots: { index: false, follow: false, nocache: true } };

/** Reservations: funds held in the ledger for each payout (BLOCKED while a provider outcome is unknown). */
export default async function ReservationsPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/payouts/reservations?limit=100", `${PAYOUTS_BASE}/reservations`);
  const page = readPage(result.data, readReservation);
  const journal = (id: string | null, label: string) =>
    id ? (
      <Link href={`${FINANCE_BASE}/journals/${encodeURIComponent(id)}`} className="block py-1 text-brand-700 underline">
        {label}
      </Link>
    ) : null;
  return (
    <AccountPage title="Payout reservations" intro="A reservation is never released while the provider outcome is unknown and is consumed only on authoritative completion.">
      <PayoutBanner />
      {page.items.length === 0 ? (
        <EmptyState title="No reservations" />
      ) : (
        <TableShell caption="Payout reservations" head={["Created", "Payout", "Amount", "Status", "Ledger journals"]} minWidthClass="min-w-[56rem]">
          {page.items.map((r) => (
            <tr key={r.id} className="border-b border-line align-top last:border-b-0">
              <td className="p-3">
                <When at={r.created_at} />
              </td>
              <th scope="row" className="p-3 font-normal">
                <Link href={withdrawalHref(r.payout_request_id)} className="text-brand-700 underline">
                  Withdrawal request
                </Link>
              </th>
              <td className="p-3">
                <LedgerAmount minor={r.amount_minor} currency={r.currency} />
              </td>
              <td className="p-3">
                <StatusBadge status={r.status} />
              </td>
              <td className="p-3 text-sm">
                {journal(r.ledger_reserve_transaction_id, "Reserved")}
                {journal(r.ledger_submit_transaction_id, "In transit")}
                {journal(r.ledger_consume_transaction_id, "Completed")}
                {journal(r.ledger_release_transaction_id, "Released")}
              </td>
            </tr>
          ))}
        </TableShell>
      )}
    </AccountPage>
  );
}
