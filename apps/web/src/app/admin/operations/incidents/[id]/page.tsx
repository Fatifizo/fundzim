import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, When } from "@/components/admin/payments/common";
import { HistoryList, OpsBanner, Section, SevBadge, ShortId } from "@/components/admin/operations/ops";
import { IncidentActions } from "@/components/admin/operations/ops-actions";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { list, obj } from "@/lib/operations/normalise";
import type { HistoryEntry, Incident } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Incident", robots: { index: false, follow: false, nocache: true } };

/** One incident with its timeline (`ops.incident.view`). */
export default async function IncidentPage({ params }: PageProps<"/admin/operations/incidents/[id]">) {
  const me = await requireStaff();
  const { id } = await params;
  const data = (await serverGetOr404<unknown>(`/api/v1/admin/operations/incidents/${encodeURIComponent(id)}`, `/admin/operations/incidents/${encodeURIComponent(id)}`)).data;
  const x = obj(obj(data).incident) as unknown as Incident;
  return (
    <AccountPage title={`INC-${x.incident_number}: ${x.title}`}>
      <OpsBanner />
      <Section id="facts" title="Incident">
        <Facts
          items={[
            ["Status", humanise(x.status)],
            ["Severity", <SevBadge key="s" severity={x.severity} />],
            ["Affected module", humanise(x.affected_module)],
            ["Owner", x.owner_id ? <ShortId key="o" value={x.owner_id} /> : "No owner"],
            ["Started", <When key="st" at={x.started_at} />],
            ["Detected by", x.alert_key ? `Alert ${x.alert_key}` : humanise(x.detected_by)],
            ["Resolution", x.resolution ?? "—"],
            ["Post-incident notes", x.post_incident_notes ?? "—"],
          ]}
        />
      </Section>
      <Section id="actions" title="Manage">
        <IncidentActions incidentId={x.id} status={x.status} version={x.version} canManage={can(me.permissions, "ops.incident.manage")} />
      </Section>
      <Section id="timeline" title="Timeline">
        <HistoryList items={list<HistoryEntry>(data, "timeline")} caption="Incident timeline" />
      </Section>
    </AccountPage>
  );
}
