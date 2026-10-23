import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { Code, Facts, When } from "@/components/admin/payments/common";
import { HistoryList, OpsBanner, Section } from "@/components/admin/operations/ops";
import { AlertActions, AlertSeverity } from "@/components/admin/operations/security";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { list, obj } from "@/lib/operations/normalise";
import type { HistoryEntry, SecurityAlert } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Security alert", robots: { index: false, follow: false, nocache: true } };

/** One security alert with its evidence and history (`security.alert.view`; acting needs `security.alert.manage`). */
export default async function SecurityAlertPage({ params }: PageProps<"/admin/security/alerts/[id]">) {
  const me = await requireStaff();
  const { id } = await params;
  const path = `/admin/security/alerts/${encodeURIComponent(id)}`;
  const data = (await serverGetOr404<unknown>(`/api/v1${path}`, path)).data;
  const a = obj(obj(data).alert) as unknown as SecurityAlert;
  return (
    <AccountPage title={a.title}>
      <OpsBanner />
      <Section id="facts" title="Alert">
        <Facts
          items={[
            ["Severity", <AlertSeverity key="s" severity={a.severity} />],
            ["Status", humanise(a.status)],
            ["Condition", a.condition_active ? "Present at the last scan" : "Cleared"],
            ["Rule", <Code key="k">{a.alert_key}</Code>],
            ["Module", humanise(a.source_module)],
            ["Component", <Code key="c">{a.component}</Code>],
            ["Source", a.source],
            ["Seen", a.occurrences === 1 ? "Once" : `${a.occurrences} times`],
            ["First seen", <When key="f" at={a.first_seen_at} />],
            ["Last seen", <When key="l" at={a.last_seen_at} />],
            [
              "Incident",
              a.incident_id ? (
                <Link key="i" href={`/admin/operations/incidents/${encodeURIComponent(a.incident_id)}`} className="underline underline-offset-4">
                  Open the incident
                </Link>
              ) : (
                "Not escalated"
              ),
            ],
            ["Resolution", a.resolution_note ?? "—"],
          ]}
        />
      </Section>
      <Section id="evidence" title="Evidence">
        <Facts items={Object.entries(a.evidence ?? {}).map(([k, v]): [string, string] => [humanise(k), String(v)])} />
      </Section>
      <Section id="actions" title="Act on this alert">
        <AlertActions alert={a} canManage={can(me.permissions, "security.alert.manage")} />
      </Section>
      <Section id="history" title="History">
        <HistoryList items={list<HistoryEntry>(data, "history")} caption="Alert history" />
      </Section>
    </AccountPage>
  );
}
