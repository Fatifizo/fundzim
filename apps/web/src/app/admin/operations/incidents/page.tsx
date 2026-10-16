import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm } from "@/components/admin/payments/common";
import { IncidentsTable, NoPermission, OpsBanner, Section } from "@/components/admin/operations/ops";
import { DeclareIncidentForm } from "@/components/admin/operations/ops-forms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise, pick } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { items } from "@/lib/operations/normalise";
import { INCIDENT_STATUSES, type Incident } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Incidents", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/operations/incidents";
const FILTER = ["OPEN_ANY", ...INCIDENT_STATUSES] as const;

/** Operational incidents (`ops.incident.view`; declaring and managing needs `ops.incident.manage`). */
export default async function IncidentsPage({ searchParams }: PageProps<"/admin/operations/incidents">) {
  const me = await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, FILTER);
  const list = items<Incident>((await serverGetOr404<unknown>(`/api/v1/admin/operations/incidents${buildQuery({ status })}`, BASE)).data);
  return (
    <AccountPage title="Incidents" intro="Operational incidents with an owner and a timeline. Incident records are internal; no notification leaves FundZim.">
      <OpsBanner />
      <FilterForm
        action={BASE}
        label="Filter incidents"
        fields={[{ name: "status", label: "Status", value: status, options: FILTER.map((s) => ({ value: s, label: s === "OPEN_ANY" ? "Any open" : humanise(s) })) }]}
      />
      <Section id="list" title={`Incidents (${list.length})`}>
        <IncidentsTable items={list} caption="Incidents" />
      </Section>
      <Section id="declare" title="Declare an incident">
        {can(me.permissions, "ops.incident.manage") ? <DeclareIncidentForm /> : <NoPermission action="Declaring an incident" permission="ops.incident.manage" />}
      </Section>
    </AccountPage>
  );
}
