import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount, LedgerEnvironmentBanner, TestOnlyBadge } from "@/components/admin/finance/common";
import { NextPage, TableShell, When } from "@/components/admin/payments/common";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { first, isCursor } from "@/lib/campaigns/paths";
import { readJournal, readPage } from "@/lib/ledger/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Journals", robots: { index: false, follow: false, nocache: true } };

/** Journal register, newest first (`ledger.view`; keyset pages of 50). */
export default async function JournalsPage({ searchParams }: PageProps<"/admin/finance/journals">) {
  await requireStaff();
  const sp = await searchParams;
  const cursor = first(sp.cursor);
  const q = cursor && isCursor(cursor) ? `?cursor=${encodeURIComponent(cursor)}` : "";
  const result = await serverGetOr404<unknown>(`/api/v1/admin/ledger/journals${q}`, `${FINANCE_BASE}/journals`);
  const page = readPage(result.data, readJournal);
  return (
    <AccountPage title="Journals" intro="Posted journals are immutable. Corrections are separate reversal journals, approved by a second person.">
      <LedgerEnvironmentBanner environment={page.items[0]?.environment ?? "TEST"} />
      {page.items.length === 0 ? (
        <EmptyState title="No journals yet" />
      ) : (
        <TableShell caption="Journal register" head={["Posted", "Rule", "Total", "Lines", "Source", "Evidence", "Reversal"]} minWidthClass="min-w-[64rem]">
          {page.items.map((j) => (
            <tr key={j.id} className="border-b border-line align-top last:border-b-0">
              <td className="p-3">
                <When at={j.posted_at} />
              </td>
              <th scope="row" className="p-3 font-normal">
                <Link href={`${FINANCE_BASE}/journals/${encodeURIComponent(j.id)}`} className="text-brand-700 underline">
                  {j.posting_rule}
                </Link>{" "}
                <TestOnlyBadge testOnly={j.test_only} />
              </th>
              <td className="p-3">
                <LedgerAmount minor={j.total_minor} currency={j.currency} />
              </td>
              <td className="p-3">{j.line_count}</td>
              <td className="p-3 text-sm">
                {j.source_system} · {j.source_type}
              </td>
              <td className="p-3 text-sm">{humanise(j.evidence_class)}</td>
              <td className="p-3 text-sm">{j.reverses_transaction_id ? "Reversal" : j.reversed_by_transaction_id ? "Reversed" : "—"}</td>
            </tr>
          ))}
        </TableShell>
      )}
      <NextPage base={`${FINANCE_BASE}/journals`} query={{}} next={page.next_cursor} />
    </AccountPage>
  );
}
