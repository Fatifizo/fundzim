import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, toOptions } from "@/components/admin/payments/common";
import { ExceptionsTable, OpsBanner, Section, SourcesNotice } from "@/components/admin/operations/ops";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise, pick } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { items, sources } from "@/lib/operations/normalise";
import { EXCEPTION_MODULES, SEVERITIES, TRIAGE_STATUSES, type ExceptionItem } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Exception center", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/operations/exceptions";

/**
 * Exception center (`ops.dashboard.view`; triage needs `ops.exception.work`): open exceptions reported by the modules,
 * with their triage. Triage tracks ownership only — the exception is fixed in its module, never here.
 */
export default async function ExceptionsPage({ searchParams }: PageProps<"/admin/operations/exceptions">) {
  const me = await requireStaff();
  const p = await searchParams;
  const filters = { module: pick(p.module, EXCEPTION_MODULES), severity: pick(p.severity, SEVERITIES), status: pick(p.status, TRIAGE_STATUSES),
    assigned_to: pick(p.assigned_to, ["me"] as const) };
  const data = (await serverGetOr404<unknown>(`/api/v1/admin/operations/exceptions${buildQuery(filters)}`, BASE)).data;
  const list = items<ExceptionItem>(data);
  return (
    <AccountPage title="Exception center" intro="Exceptions stay open until their module stops reporting them. Unknown payment and payout outcomes are never resolved by assumption.">
      <OpsBanner />
      <SourcesNotice sources={sources(data)} />
      <FilterForm
        action={BASE}
        label="Filter exceptions"
        fields={[
          { name: "module", label: "Module", value: filters.module, options: toOptions(EXCEPTION_MODULES) },
          { name: "severity", label: "Severity", value: filters.severity, options: toOptions(SEVERITIES) },
          { name: "status", label: "Triage", value: filters.status, options: TRIAGE_STATUSES.map((s) => ({ value: s, label: humanise(s) })) },
          { name: "assigned_to", label: "Assigned", value: filters.assigned_to, options: [{ value: "me", label: "To me" }] },
        ]}
      />
      <Section id="list" title={`Exceptions (${list.length})`}>
        <ExceptionsTable items={list} caption="Exceptions" canWork={can(me.permissions, "ops.exception.work")} />
      </Section>
    </AccountPage>
  );
}
