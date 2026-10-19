import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, TableShell } from "@/components/admin/payments/common";
import { CommsBanner } from "@/components/admin/communications/comms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { obj } from "@/lib/operations/normalise";
import type { CommsReport } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Communication reports", robots: { index: false, follow: false, nocache: true } };

/** Communication reports for the last 30 days (`communications.view`); figures come from the engine's records only. */
export default async function ReportsPage() {
  await requireStaff();
  const r = obj((await serverGetOr404<unknown>("/api/v1/admin/communications/reports", "/admin/communications/reports")).data) as unknown as CommsReport;
  const groups = [...new Set((r.rows ?? []).map((x) => x.group))];
  return (
    <AccountPage title="Communication reports" intro="Last 30 days. TEST environment: simulators and development senders only.">
      <CommsBanner />
      <Facts items={[["Send latency p50", r.latency_p50_ms === null ? "Not measured" : `${r.latency_p50_ms} ms`], ["Send latency p95", r.latency_p95_ms === null ? "Not measured" : `${r.latency_p95_ms} ms`]]} />
      <ul className="list-disc pl-6 text-sm text-ink-700">
        {(r.limitations ?? []).map((l) => <li key={l}>{l}</li>)}
      </ul>
      {groups.map((g) => (
        <Section key={g} id={`g-${g}`} title={humanise(g)}>
          <TableShell caption={humanise(g)} head={["Key", "Count", "Rate"]}>
            {r.rows.filter((x) => x.group === g).map((x) => (
              <tr key={x.key} className="border-b border-line last:border-b-0">
                <td className="p-3 break-all">{humanise(x.key)}</td>
                <td className="p-3">{x.count.toLocaleString("en-GB")}</td>
                <td className="p-3">{x.rate === undefined ? "—" : `${(x.rate * 100).toFixed(1)} %`}</td>
              </tr>
            ))}
          </TableShell>
        </Section>
      ))}
    </AccountPage>
  );
}
