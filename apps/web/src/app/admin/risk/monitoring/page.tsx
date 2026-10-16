import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Section, TableShell } from "@/components/admin/payments/common";
import { ReconRunPanel } from "@/components/admin/risk/risk-forms";
import { MonitoringTable, RISK_BASE, RiskBanner } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { readMonitoring, readQuality, readRiskPage } from "@/lib/risk/normalise";
import { NoPermission } from "@/components/admin/operations/ops";
import { can } from "@/lib/operations/navigation";
import { RISK_FORM_PERMISSION } from "@/lib/risk/permissions";

export const metadata: Metadata = { title: "Risk monitoring", robots: { index: false, follow: false, nocache: true } };

/** Monitoring events, rule quality counts and reconciliation (`risk.operations.view`; running needs `risk.case.work`). */
export default async function RiskMonitoringPage() {
  const me = await requireStaff();
  const path = `${RISK_BASE}/monitoring`;
  const events = readRiskPage((await serverGetOr404<unknown>("/api/v1/admin/risk/monitoring?limit=100", path)).data, readMonitoring);
  const q = readQuality(await serverGetOptional<unknown>("/api/v1/admin/risk/quality?days=30", path, null));
  return (
    <AccountPage title="Risk monitoring" intro="Alerts, expiries, rejected callbacks and reconciliation findings, with rule quality counts.">
      <RiskBanner />
      <MonitoringTable items={events.items} caption="Monitoring events" />
      <Section id="quality" title={`Rule quality (last ${q.window_days} days)`} description={<>Accuracy: {q.accuracy}. {q.notice}</>}>
        {q.rules.length === 0 ? (
          <p className="text-ink-700">No rule fired in this period.</p>
        ) : (
          <TableShell caption="Rule quality counts" head={["Rule", "Fired", "Cases decided", "False positive", "Risk confirmed", "No further action", "Signals reviewed as false positive"]} minWidthClass="min-w-[60rem]">
            {q.rules.map((r) => (
              <tr key={r.rule_version_id + r.rule_code} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-semibold">
                  {r.rule_code}
                </th>
                <td className="p-3">{r.fired}</td>
                <td className="p-3">{r.cases_resolved}</td>
                <td className="p-3">{r.false_positive_outcomes}</td>
                <td className="p-3">{r.risk_confirmed_outcomes}</td>
                <td className="p-3">{r.no_further_action_outcomes}</td>
                <td className="p-3">{r.signal_false_positive_reviews}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="reconciliation" title="Risk reconciliation" description="Read-only consistency checks. Findings become monitoring events; nothing is changed.">
        {can(me.permissions, RISK_FORM_PERMISSION.reconciliation) ? <ReconRunPanel /> : <NoPermission action="Running risk reconciliation" permission={RISK_FORM_PERMISSION.reconciliation} />}
      </Section>
    </AccountPage>
  );
}
