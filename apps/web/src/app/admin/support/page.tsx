import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, NextPage, toOptions } from "@/components/admin/payments/common";
import { NoPermission, OpsBanner, Section, SupportTable } from "@/components/admin/operations/ops";
import { OpenSupportCaseForm } from "@/components/admin/operations/ops-forms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise, pick } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { items, nextCursor } from "@/lib/operations/normalise";
import { SUPPORT_CATEGORIES, SUPPORT_PRIORITIES, SUPPORT_STATUSES, type SupportCase } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Support cases", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/support";
const FILTER = ["OPEN_ANY", ...SUPPORT_STATUSES] as const;

/**
 * Support cases (`ops.support.view`; working them needs `ops.support.work`). Support staff see the case and its
 * references only — no KYC documents, bank details, ledger internals, risk evidence or compliance notes.
 */
export default async function SupportPage({ searchParams }: PageProps<"/admin/support">) {
  const me = await requireStaff();
  const p = await searchParams;
  const one = (v: string | string[] | undefined) => (typeof v === "string" ? v : undefined);
  const q = one(p.q)?.slice(0, 100);
  const filters = {
    status: pick(p.status, FILTER) ?? "OPEN_ANY",
    category: pick(p.category, SUPPORT_CATEGORIES),
    priority: pick(p.priority, SUPPORT_PRIORITIES),
    assigned_to: pick(p.assigned_to, ["me", "unassigned"] as const),
    q,
  };
  const cursor = one(p.cursor);
  const data = (await serverGetOr404<unknown>(`/api/v1/admin/support/cases${buildQuery({ ...filters, cursor, limit: "50" })}`, BASE)).data;
  return (
    <AccountPage title="Support cases" intro="Internal support cases. Due dates appear only when an approved internal target exists.">
      <OpsBanner />
      <FilterForm
        action={BASE}
        label="Filter support cases"
        fields={[
          { name: "status", label: "Status", value: filters.status, options: FILTER.map((s) => ({ value: s, label: s === "OPEN_ANY" ? "Any open" : humanise(s) })) },
          { name: "category", label: "Category", value: filters.category, options: toOptions(SUPPORT_CATEGORIES) },
          { name: "priority", label: "Priority", value: filters.priority, options: toOptions(SUPPORT_PRIORITIES) },
          { name: "assigned_to", label: "Assigned", value: filters.assigned_to, options: [{ value: "me", label: "To me" }, { value: "unassigned", label: "Unassigned" }] },
        ]}
      />
      <Section id="list" title="Cases">
        <SupportTable items={items<SupportCase>(data)} caption="Support cases" />
        <NextPage base={BASE} query={filters} next={nextCursor(data)} />
      </Section>
      <Section id="open" title="Open a case">
        {can(me.permissions, "ops.support.work") ? <OpenSupportCaseForm /> : <NoPermission action="Opening a support case" permission="ops.support.work" />}
      </Section>
    </AccountPage>
  );
}
