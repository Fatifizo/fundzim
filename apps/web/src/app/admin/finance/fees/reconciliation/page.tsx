import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { FeeAction } from "@/components/admin/finance/fee-action";
import { assessmentHref, FEES_BASE, FeeBanner } from "@/components/admin/finance/fees";
import { Facts, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readReconPage } from "@/lib/fees/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Fee reconciliation", robots: { index: false, follow: false, nocache: true } };

/** Fee reconciliation items (open first). Resolving records a note; it never changes a fee or the ledger. */
export default async function FeeReconciliationPage() {
  await requireStaff();
  const page = readReconPage((await serverGetOr404<unknown>("/api/v1/admin/fees/reconciliation?limit=200", `${FEES_BASE}/reconciliation`)).data);
  const r = page.latest_run;
  return (
    <AccountPage title="Fee reconciliation" intro="Calculations, assessments, ledger journals and provider-reported fees compared. Settlement reconciliation: NOT_PERFORMED.">
      <FeeBanner />
      <Section id="run" title="Latest run">
        {r ? (
          <Facts items={[["Started", <When key="s" at={r.started_at} />], ["Checked", String(r.checked)], ["Matched", String(r.matched)], ["Mismatches", String(r.mismatches)], ["Settlement reconciliation", page.settlement_reconciliation]]} />
        ) : (
          <p className="text-ink-700">No reconciliation has run yet. Settlement reconciliation: {page.settlement_reconciliation}.</p>
        )}
        <FeeAction target={{ kind: "recon-run" }} />
      </Section>
      <Section id="items" title="Items">
        {page.items.length === 0 ? (
          <EmptyState title="No fee reconciliation items" />
        ) : (
          <TableShell caption="Fee reconciliation items" head={["Kind", "Severity", "Status", "Detected", "Detail", "Actions"]} minWidthClass="min-w-[60rem]">
            {page.items.map((x) => (
              <tr key={x.id} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  {humanise(x.kind)}
                  {x.assessment_id ? (
                    <Link href={assessmentHref(x.assessment_id)} className="block text-brand-700 underline">
                      Assessment
                    </Link>
                  ) : null}
                </th>
                <td className="p-3">
                  <Badge tone={x.severity === "SEV1" ? "gold" : "neutral"}>{x.severity}</Badge>
                </td>
                <td className="p-3">
                  <StatusBadge status={x.status} />
                  {x.resolution_note ? <span className="block text-sm">{x.resolution_note}</span> : null}
                </td>
                <td className="p-3 text-sm">
                  <When at={x.detected_at} />
                </td>
                <td className="p-3 text-sm">{x.detail}</td>
                <td className="p-3">
                  {/* always mounted: the confirmation notice must survive the refresh that changes the status */}
                  <FeeAction target={{ kind: "recon-resolve", id: x.id }} available={x.status === "OPEN"} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
