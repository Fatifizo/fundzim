import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount, LedgerEnvironmentBanner } from "@/components/admin/finance/common";
import { Code, StatusBadge, TableShell } from "@/components/admin/payments/common";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { first } from "@/lib/campaigns/paths";
import { readAccount, readPage } from "@/lib/ledger/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Ledger accounts", robots: { index: false, follow: false, nocache: true } };

const STATUSES = new Set(["ACTIVE", "FROZEN", "CLOSED"]);

/** Accounts with exact current balances in normal-balance terms (`ledger.view`). */
export default async function LedgerAccountsPage({ searchParams }: PageProps<"/admin/finance/accounts">) {
  await requireStaff();
  const sp = await searchParams;
  const status = first(sp.status);
  const q = status && STATUSES.has(status) ? `?status=${status}&limit=100` : "?limit=100";
  const result = await serverGetOr404<unknown>(`/api/v1/admin/ledger/accounts${q}`, `${FINANCE_BASE}/accounts`);
  const page = readPage(result.data, readAccount);
  const env = page.items[0]?.environment ?? "TEST";
  return (
    <AccountPage title="Ledger accounts" intro="Balances are derived from posted journal lines; nothing updates a balance directly.">
      <LedgerEnvironmentBanner environment={env} />
      {page.items.length === 0 ? (
        <EmptyState title="No accounts yet" />
      ) : (
        <TableShell caption="Ledger accounts" head={["Account", "Type", "Currency", "Status", "Balance", "Entries"]} minWidthClass="min-w-[56rem]">
          {page.items.map((a) => (
            <tr key={a.id} className="border-b border-line align-top last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                <Link href={`${FINANCE_BASE}/accounts/${encodeURIComponent(a.id)}`} className="text-brand-700 underline">
                  <Code>{a.code}</Code>
                </Link>
              </th>
              <td className="p-3">
                {humanise(a.type)}
                <span className="block text-sm text-ink-600">{humanise(a.kind)}</span>
              </td>
              <td className="p-3">{a.currency}</td>
              <td className="p-3">
                <StatusBadge status={a.status} />
              </td>
              <td className="p-3">
                <LedgerAmount minor={a.balance_minor} currency={a.currency} />
              </td>
              <td className="p-3">{a.entry_count}</td>
            </tr>
          ))}
        </TableShell>
      )}
    </AccountPage>
  );
}
