import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, Section, toOptions } from "@/components/admin/payments/common";
import { RiskAction } from "@/components/admin/risk/risk-action";
import { CreateHoldForm } from "@/components/admin/risk/risk-forms";
import { HoldsTable, RISK_BASE, RiskBanner } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, humanise, pick } from "@/lib/payments/labels";
import { holdActions } from "@/lib/risk/labels";
import { readHold, readRiskPage } from "@/lib/risk/normalise";
import { HOLD_STATUSES, HOLD_TYPES } from "@/lib/risk/types";
import { NoPermission } from "@/components/admin/operations/ops";
import { can } from "@/lib/operations/navigation";
import { RISK_FORM_PERMISSION, mayRisk } from "@/lib/risk/permissions";

export const metadata: Metadata = { title: "Risk holds", robots: { index: false, follow: false, nocache: true } };

const BASE = `${RISK_BASE}/holds`;
const STATUS_FILTER = ["OPEN", ...HOLD_STATUSES] as const;
const ACTIONS = ["activate", "revoke", "review", "request-release", "approve-release", "reject-release"] as const;

/** The central hold registry (`risk.operations.view`; placing needs `risk.hold.create`, releasing `risk.hold.release`). */
export default async function RiskHoldsPage({ searchParams }: PageProps<"/admin/risk/holds">) {
  const me = await requireStaff();
  const p = await searchParams;
  const filters = { status: pick(p.status, STATUS_FILTER) ?? "OPEN", hold_type: pick(p.hold_type, HOLD_TYPES) };
  const page = readRiskPage((await serverGetOr404<unknown>(`/api/v1/admin/risk/holds${buildQuery(filters)}`, BASE)).data, readHold);
  return (
    <AccountPage title="Risk holds" intro="A hold stops one kind of action on one subject until two people release it. It never moves money or applies a legal restriction.">
      <RiskBanner />
      <FilterForm
        action={BASE}
        label="Filter holds"
        fields={[
          { name: "status", label: "Status", value: filters.status, options: STATUS_FILTER.map((s) => ({ value: s, label: s === "OPEN" ? "Any open" : humanise(s) })) },
          { name: "hold_type", label: "Hold type", value: filters.hold_type, options: toOptions(HOLD_TYPES) },
        ]}
      />
      <HoldsTable
        items={page.items}
        caption="Risk holds"
        actions={(h) => ACTIONS.map((a) => <RiskAction key={a} target={{ kind: `hold-${a}`, id: h.id }} available={holdActions(h.status).includes(a) && mayRisk(me.permissions, `hold-${a}`)} />)}
      />
      <Section id="create" title="Place a hold" description="Campaign, donation and account-review holds need a second person to activate them. Never on yourself or a linked account.">
        {can(me.permissions, RISK_FORM_PERMISSION.createHold) ? <CreateHoldForm /> : <NoPermission action="Creating a hold" permission={RISK_FORM_PERMISSION.createHold} />}
      </Section>
    </AccountPage>
  );
}
