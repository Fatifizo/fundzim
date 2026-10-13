import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount, LedgerEnvironmentBanner } from "@/components/admin/finance/common";
import { Code, Section, StatusBadge, TableShell } from "@/components/admin/payments/common";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { first } from "@/lib/campaigns/paths";
import { readTrialBalance } from "@/lib/ledger/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Trial balance", robots: { index: false, follow: false, nocache: true } };

/** Trial balance per currency (never summed across currencies), from one consistent snapshot (`ledger.view`). */
export default async function TrialBalancePage({ searchParams }: PageProps<"/admin/finance/trial-balance">) {
  await requireStaff();
  const sp = await searchParams;
  const ccy = first(sp.currency);
  const q = ccy && /^[A-Z]{3}$/.test(ccy) ? `?currency=${ccy}` : "";
  const result = await serverGetOr404<unknown>(`/api/v1/admin/ledger/trial-balance${q}`, `${FINANCE_BASE}/trial-balance`);
  const tb = readTrialBalance(result.data);
  return (
    <AccountPage title="Trial balance" intro="Every account's net debit or credit; the totals must be equal in each currency.">
      <LedgerEnvironmentBanner environment={tb.environment} />
      {tb.currencies.length === 0 ? <EmptyState title="No journals posted yet" /> : null}
      {tb.currencies.map((c) => (
        <Section key={c.currency} id={`tb-${c.currency}`} title={`${c.currency}`}>
          <p className="flex flex-wrap items-center gap-2">
            {c.balanced ? <StatusBadge status="PASSED" /> : <StatusBadge status="FAILED" />}
            <span>
              Debits <LedgerAmount minor={c.total_debit_minor} currency={c.currency} /> · Credits{" "}
              <LedgerAmount minor={c.total_credit_minor} currency={c.currency} /> · Difference{" "}
              <LedgerAmount minor={c.difference_minor} currency={c.currency} />
            </span>
          </p>
          <TableShell caption={`Trial balance ${c.currency}`} head={["Account", "Type", "Debit", "Credit"]} minWidthClass="min-w-[48rem]">
            {c.rows.map((r) => (
              <tr key={r.account_id} className="border-b border-line last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  <Link href={`${FINANCE_BASE}/accounts/${encodeURIComponent(r.account_id)}`} className="text-brand-700 underline">
                    <Code>{r.code}</Code>
                  </Link>
                </th>
                <td className="p-3">{humanise(r.type)}</td>
                <td className="p-3">{r.debit_minor !== "0" ? <LedgerAmount minor={r.debit_minor} currency={c.currency} /> : ""}</td>
                <td className="p-3">{r.credit_minor !== "0" ? <LedgerAmount minor={r.credit_minor} currency={c.currency} /> : ""}</td>
              </tr>
            ))}
          </TableShell>
        </Section>
      ))}
    </AccountPage>
  );
}
