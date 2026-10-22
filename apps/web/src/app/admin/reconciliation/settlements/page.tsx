import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { Section, StatusBadge, TableShell } from "@/components/admin/payments/common";
import { Money, ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readBatches } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Settlement batches", robots: { index: false, follow: false, nocache: true } };

/**
 * Settlement batches from TEST settlement reports: the provider's header (credits − debits − fees = net, validated at
 * import), FundZim's expectation of the net from its own records, and whether every line is settled.
 */
export default async function ReconciliationSettlementsPage() {
  await requireStaff();
  const list = readBatches((await serverGetOr404<unknown>("/api/v1/admin/reconciliation/settlements?limit=100", "/admin/reconciliation/settlements")).data);
  return (
    <AccountPage title="Settlement batches" intro="Provider settlement headers against FundZim's expectation. TEST reports only; no settlement is posted to the ledger in Stage 17.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/settlements" />
      <Section id="batches" title="Batches">
        {list.length === 0 ? (
          <p className="text-ink-700">No settlement batch has been imported yet.</p>
        ) : (
          <TableShell caption="Settlement batches" head={["Batch", "Date", "Status", "Credits", "Debits", "Fees", "Net (provider)", "Net (FundZim expects)", "Lines settled"]} minWidthClass="min-w-[64rem]">
            {list.map((b) => (
              <tr key={b.id} className="border-b border-line last:border-0">
                <td className="p-3">
                  <Link href={`/admin/reconciliation/settlements/${b.id}`} className="font-semibold text-brand-700 underline">
                    {b.ref}
                  </Link>
                </td>
                <td className="p-3">{b.date}</td>
                <td className="p-3">
                  <StatusBadge status={b.status} />
                </td>
                <td className="p-3">
                  <Money minor={b.credits} currency={b.currency} />
                </td>
                <td className="p-3">
                  <Money minor={b.debits} currency={b.currency} />
                </td>
                <td className="p-3">
                  <Money minor={b.fees} currency={b.currency} />
                </td>
                <td className="p-3">
                  <Money minor={b.net} currency={b.currency} />
                </td>
                <td className="p-3">
                  <Money minor={b.expectedNet} currency={b.currency} />
                </td>
                <td className="p-3">
                  {b.settled} of {b.lines}
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
