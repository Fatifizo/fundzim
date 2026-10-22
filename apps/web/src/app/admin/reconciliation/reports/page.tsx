import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, Section, TableShell } from "@/components/admin/payments/common";
import { ExportButton } from "@/components/admin/reconciliation/recon-admin";
import { ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readReport, REPORT_KINDS } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Reconciliation reports", robots: { index: false, follow: false, nocache: true } };

/** Bounded reports (5,000 rows; last 30 days by default). Every export is audited and needs a fresh confirmation. */
export default async function ReconciliationReportsPage({ searchParams }: PageProps<"/admin/reconciliation/reports">) {
  await requireStaff();
  const sp = await searchParams;
  const kind = typeof sp.kind === "string" && REPORT_KINDS.some((k) => k.kind === sp.kind) ? sp.kind : "daily_summary";
  const r = readReport((await serverGetOr404<unknown>(`/api/v1/admin/reconciliation/reports?kind=${kind}`, "/admin/reconciliation/reports")).data);
  const title = REPORT_KINDS.find((k) => k.kind === kind)?.label ?? humanise(kind);
  return (
    <AccountPage title="Reconciliation reports" intro="TEST evidence only: never business performance. Currencies are reported separately.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/reports" />
      <FilterForm action="/admin/reconciliation/reports" label="Choose a report" fields={[{ name: "kind", label: "Report", value: kind,
        options: REPORT_KINDS.map((k) => ({ value: k.kind, label: k.label })) }]} />
      <Section id="report" title={title} description={r.truncated ? "Only the first 5,000 rows are shown." : undefined}>
        {r.rows.length === 0 ? (
          <p className="text-ink-700">No rows for the last 30 days.</p>
        ) : (
          <TableShell caption={title} head={r.columns.map(humanise)} minWidthClass="min-w-[56rem]">
            {r.rows.map((row, i) => (
              <tr key={i} className="border-b border-line last:border-0">
                {row.map((c, j) => (
                  <td key={j} className="p-3 break-all">
                    {c}
                  </td>
                ))}
              </tr>
            ))}
          </TableShell>
        )}
        <ExportButton kind={kind} />
      </Section>
    </AccountPage>
  );
}
