import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, When } from "@/components/admin/payments/common";
import { HistoryList, OpsBanner, Section, SevBadge, ShortId } from "@/components/admin/operations/ops";
import { SupportActions } from "@/components/admin/operations/ops-actions";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { list, obj } from "@/lib/operations/normalise";
import type { HistoryEntry, SupportCase } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Support case", robots: { index: false, follow: false, nocache: true } };

/** One support case with its history (`ops.support.view`). References are ids only; sensitive records stay in their modules. */
export default async function SupportCasePage({ params }: PageProps<"/admin/support/[id]">) {
  const me = await requireStaff();
  const { id } = await params;
  const data = (await serverGetOr404<unknown>(`/api/v1/admin/support/cases/${encodeURIComponent(id)}`, `/admin/support/${encodeURIComponent(id)}`)).data;
  const c = obj(obj(data).case) as unknown as SupportCase;
  return (
    <AccountPage title={`Support case #${c.case_number}`} intro={c.subject}>
      <OpsBanner />
      <Section id="facts" title="Case">
        <Facts
          items={[
            ["Status", humanise(c.status)],
            ["Priority", <SevBadge key="p" severity={c.priority} />],
            ["Category", humanise(c.category)],
            ["Assigned to", c.assigned_to ? <ShortId key="a" value={c.assigned_to} /> : "Unassigned"],
            ["Escalated to", c.escalated_to ? `${humanise(c.escalated_to)}${c.escalation_reference ? ` (${c.escalation_reference})` : ""}` : "—"],
            ["Requester account", <ShortId key="r" value={c.requester_user_id} />],
            ["Related campaign", <ShortId key="c" value={c.related_campaign_id} />],
            ["Related record", c.related_reference_type ? <span key="x">{humanise(c.related_reference_type)} <ShortId value={c.related_reference_id} /></span> : "—"],
            ["Due (internal target)", c.due_at ? <When key="d" at={c.due_at} /> : "No internal target"],
            ["Opened", <When key="o" at={c.created_at} />],
            ["Resolution", c.resolution ?? "—"],
          ]}
        />
      </Section>
      <Section id="actions" title="Work the case">
        <SupportActions caseId={c.id} status={c.status} version={c.version} canWork={can(me.permissions, "ops.support.work")} />
      </Section>
      <Section id="history" title="History">
        <HistoryList items={list<HistoryEntry>(data, "history")} caption="Support case history" />
      </Section>
    </AccountPage>
  );
}
