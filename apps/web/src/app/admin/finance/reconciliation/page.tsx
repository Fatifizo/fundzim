import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount } from "@/components/admin/finance/common";
import { LedgerAction } from "@/components/admin/finance/ledger-action";
import { Code, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readException, readPage } from "@/lib/ledger/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Ledger reconciliation", robots: { index: false, follow: false, nocache: true } };

function age(seconds: number): string {
  if (seconds < 3600) return `${Math.floor(seconds / 60)} min`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} h`;
  return `${Math.floor(seconds / 86400)} d`;
}

/** Reconciliation exceptions (`ledger.view`; workflow `ledger.reconciliation.operate` + step-up). */
export default async function LedgerReconciliationPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/ledger/reconciliation?limit=100", `${FINANCE_BASE}/reconciliation`);
  const page = readPage(result.data, readException);
  return (
    <AccountPage
      title="Ledger reconciliation"
      intro="Payments, provider evidence and ledger postings compared. Settlement reconciliation is not performed: no provider settlement evidence exists yet."
    >
      {page.items.length === 0 ? (
        <EmptyState title="No reconciliation exceptions" />
      ) : (
        <TableShell caption="Reconciliation exceptions" head={["Exception", "Source", "Expected / actual", "Status", "Age", "Actions"]} minWidthClass="min-w-[64rem]">
          {page.items.map((e) => (
            <tr key={e.id} className="border-b border-line align-top last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                {humanise(e.exception_type)}
                {e.transaction_id ? (
                  <span className="block text-sm">
                    <Link href={`${FINANCE_BASE}/journals/${encodeURIComponent(e.transaction_id)}`} className="text-brand-700 underline">
                      Journal
                    </Link>
                  </span>
                ) : null}
              </th>
              <td className="p-3 text-sm">
                {humanise(e.source_type)} <Code>{e.source_id}</Code>
              </td>
              <td className="p-3 text-sm">
                {e.expected_amount_minor && e.currency ? <LedgerAmount minor={e.expected_amount_minor} currency={e.currency} /> : "—"} /{" "}
                {e.actual_amount_minor && e.currency ? <LedgerAmount minor={e.actual_amount_minor} currency={e.currency} /> : "—"}
              </td>
              <td className="p-3">
                <StatusBadge status={e.status} />
                {e.resolution_note ? <span className="block text-sm">{e.resolution_note}</span> : null}
              </td>
              <td className="p-3">
                {age(e.age_seconds)}
                <span className="block text-sm">
                  <When at={e.detected_at} />
                </span>
              </td>
              <td className="space-y-2 p-3">
                {/* always mounted: the confirmation notice must survive the refresh that changes the status */}
                <LedgerAction target={{ kind: "recon", id: e.id, action: "investigate" }} available={e.status === "OPEN"} />
                <LedgerAction target={{ kind: "recon", id: e.id, action: "resolve" }} available={e.status !== "RESOLVED"} />
              </td>
            </tr>
          ))}
        </TableShell>
      )}
    </AccountPage>
  );
}
