import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, toOptions } from "@/components/admin/payments/common";
import { ApprovalsTable, OpsBanner, Section, SourcesNotice } from "@/components/admin/operations/ops";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, pick } from "@/lib/payments/labels";
import { items, sources } from "@/lib/operations/normalise";
import { APPROVAL_MODULES, type Approval } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Approval center", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/approvals";

/**
 * Approval center (`ops.dashboard.view`): every pending maker-checker decision the caller may see, oldest first. It
 * is not a second approval authority — each item links to its owning module, which takes and re-checks the decision.
 */
export default async function ApprovalsPage({ searchParams }: PageProps<"/admin/approvals">) {
  await requireStaff();
  const p = await searchParams;
  const filters = { module: pick(p.module, APPROVAL_MODULES), decidable: pick(p.decidable, ["true"] as const) };
  const data = (await serverGetOr404<unknown>(`/api/v1/admin/approvals${buildQuery(filters)}`, BASE)).data;
  const list = items<Approval>(data);
  return (
    <AccountPage title="Approval center" intro="Pending decisions across modules. You never decide your own request; the owning module enforces it again.">
      <OpsBanner />
      <SourcesNotice sources={sources(data)} />
      <FilterForm
        action={BASE}
        label="Filter approvals"
        fields={[
          { name: "module", label: "Module", value: filters.module, options: toOptions(APPROVAL_MODULES) },
          { name: "decidable", label: "Show", value: filters.decidable, options: [{ value: "true", label: "Only what I can decide" }] },
        ]}
      />
      <Section id="queue" title={`Pending (${list.length})`}>
        <ApprovalsTable items={list} caption="Pending approvals" />
      </Section>
    </AccountPage>
  );
}
