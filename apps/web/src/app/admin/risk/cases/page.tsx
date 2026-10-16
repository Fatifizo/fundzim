import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, Section, toOptions } from "@/components/admin/payments/common";
import { OpenCaseForm } from "@/components/admin/risk/risk-forms";
import { CasesTable, RISK_BASE, RiskBanner } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise, pick } from "@/lib/payments/labels";
import { readCase, readRiskPage } from "@/lib/risk/normalise";
import { CASE_STATUSES, CASE_TYPES, PRIORITIES } from "@/lib/risk/types";

export const metadata: Metadata = { title: "Risk cases", robots: { index: false, follow: false, nocache: true } };

const BASE = `${RISK_BASE}/cases`;
const STATUS_FILTER = ["OPEN_ANY", ...CASE_STATUSES] as const;

/** Risk case queue (`risk.operations.view`), most urgent first. Opening a case needs `risk.case.work`. */
export default async function RiskCasesPage({ searchParams }: PageProps<"/admin/risk/cases">) {
  await requireStaff();
  const p = await searchParams;
  const filters = { status: pick(p.status, STATUS_FILTER) ?? "OPEN_ANY", priority: pick(p.priority, PRIORITIES), case_type: pick(p.case_type, CASE_TYPES),
    assigned_to: pick(p.assigned_to, ["me", "unassigned"] as const) };
  const page = readRiskPage((await serverGetOr404<unknown>(`/api/v1/admin/risk/cases${buildQuery({ ...filters, limit: "100" })}`, BASE)).data, readCase);
  return (
    <AccountPage title="Risk cases" intro="One open case per subject and case type; repeated events link to it. Due dates are internal targets, not regulatory deadlines.">
      <RiskBanner />
      <FilterForm
        action={BASE}
        label="Filter cases"
        fields={[
          { name: "status", label: "Status", value: filters.status, options: STATUS_FILTER.map((s) => ({ value: s, label: s === "OPEN_ANY" ? "Any open" : humanise(s) })) },
          { name: "priority", label: "Priority", value: filters.priority, options: toOptions(PRIORITIES) },
          { name: "case_type", label: "Case type", value: filters.case_type, options: toOptions(CASE_TYPES) },
          { name: "assigned_to", label: "Assigned", value: filters.assigned_to, options: [{ value: "me", label: "To me" }, { value: "unassigned", label: "Unassigned" }] },
        ]}
      />
      <CasesTable items={page.items} caption="Risk cases" />
      <Section id="open" title="Open a case" description="Never about yourself or an account linked to yours.">
        <OpenCaseForm />
      </Section>
    </AccountPage>
  );
}
