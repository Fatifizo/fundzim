import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { inputClasses } from "@/components/forms/text-field";
import { TableShell, When } from "@/components/admin/payments/common";
import { NatureBadge, OpsBanner, Section } from "@/components/admin/operations/ops";
import { buttonClasses } from "@/components/ui/button";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery } from "@/lib/payments/labels";
import { formatDuration, items } from "@/lib/operations/normalise";
import type { Report } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Operational reports", robots: { index: false, follow: false, nocache: true } };

const DATE = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Operational reports (`ops.dashboard.view`; each report also needs its source permission). Each states its source,
 * environment, freshness and limitations. Counts and durations only: no money totals, no combined currencies.
 */
export default async function ReportsPage({ searchParams }: PageProps<"/admin/reports">) {
  await requireStaff();
  const p = await searchParams;
  const from = typeof p.from === "string" && DATE.test(p.from) ? p.from : undefined;
  const to = typeof p.to === "string" && DATE.test(p.to) ? p.to : undefined;
  const q = buildQuery({ from: from ? `${from}T00:00:00Z` : undefined, to: to ? `${to}T23:59:59Z` : undefined });
  const reports = items<Report>((await serverGetOr404<unknown>(`/api/v1/admin/reports${q}`, "/admin/reports")).data);
  return (
    <AccountPage title="Operational reports" intro="Default range: the last 30 days. TEST environment figures are never business performance.">
      <OpsBanner />
      <form method="get" action="/admin/reports" aria-label="Report range" className="grid gap-4 rounded-card border border-line bg-surface p-4 sm:grid-cols-3 sm:items-end">
        <div className="space-y-1">
          <label htmlFor="r-from" className="block font-semibold text-ink-900">
            From
          </label>
          <input id="r-from" name="from" type="date" defaultValue={from} className={inputClasses} />
        </div>
        <div className="space-y-1">
          <label htmlFor="r-to" className="block font-semibold text-ink-900">
            To
          </label>
          <input id="r-to" name="to" type="date" defaultValue={to} className={inputClasses} />
        </div>
        <div>
          <button type="submit" className={buttonClasses("primary")}>
            Show reports
          </button>
        </div>
      </form>
      {reports.length === 0 ? <p className="text-ink-700">No reports are available to your roles.</p> : null}
      {reports.map((r) => (
        <Section key={r.key} id={`report-${r.key}`} title={r.title}>
          <p className="text-sm text-ink-700">
            Source: {r.source}. Environment: {r.environment}. {r.freshness}. Generated <When at={r.generated_at} />.
          </p>
          {r.limitations.length > 0 ? (
            <ul className="list-disc pl-6 text-sm text-ink-700">
              {r.limitations.map((l) => (
                <li key={l}>{l}</li>
              ))}
            </ul>
          ) : null}
          <TableShell caption={r.title} head={["Group", "Measure", "Value", "Nature"]}>
            {r.rows.map((row, i) => (
              <tr key={`${row.group}-${row.label}-${i}`} className="border-b border-line align-top last:border-b-0">
                <td className="p-3">{row.group}</td>
                <td className="p-3">{row.label}</td>
                <td className="p-3">{row.value === null ? "Not measured" : row.unit === "seconds" ? formatDuration(row.value) : row.value.toLocaleString("en-GB")}</td>
                <td className="p-3">
                  <NatureBadge nature={row.nature} />
                </td>
              </tr>
            ))}
          </TableShell>
        </Section>
      ))}
    </AccountPage>
  );
}
