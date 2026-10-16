import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { TableShell, When } from "@/components/admin/payments/common";
import { NoPermission, OpsBanner, Section, ShortId } from "@/components/admin/operations/ops";
import { SLADecision } from "@/components/admin/operations/ops-actions";
import { ProposeSLAForm } from "@/components/admin/operations/ops-forms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { can } from "@/lib/operations/navigation";
import { formatDuration, items, obj } from "@/lib/operations/normalise";
import type { SLATarget } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Service targets", robots: { index: false, follow: false, nocache: true } };

/**
 * Internal service targets (Stage 14 §56–58). None is configured by FundZim: each is proposed (`ops.sla.request`) and
 * approved by a different person (`ops.sla.approve`). They are internal operational targets, never contractual or
 * regulatory deadlines.
 */
export default async function SLAPage() {
  const me = await requireStaff();
  const data = (await serverGetOr404<unknown>("/api/v1/admin/operations/sla-targets", "/admin/operations/sla")).data;
  const list = items<SLATarget>(data);
  const notice = obj(data).notice;
  const approve = can(me.permissions, "ops.sla.approve");
  return (
    <AccountPage title="Service targets" intro={typeof notice === "string" ? notice : "Internal operational targets only."}>
      <OpsBanner />
      <Section id="targets" title="Active and proposed targets">
        {list.length === 0 ? (
          <p className="text-ink-700">No targets are configured. Due dates are not shown anywhere until a target is approved.</p>
        ) : (
          <TableShell caption="Service targets" head={["Metric", "Target", "Status", "Proposed", "Reason", "Decision"]} minWidthClass="min-w-[56rem]">
            {list.map((t) => (
              <tr key={t.id} className="border-b border-line align-top last:border-b-0">
                <td className="p-3 font-semibold">{humanise(t.metric_key)}</td>
                <td className="p-3">{formatDuration(t.target_seconds)}</td>
                <td className="p-3">{humanise(t.status)}</td>
                <td className="p-3">
                  <ShortId value={t.proposed_by} /> <When at={t.proposed_at} />
                </td>
                <td className="p-3 break-words">{t.reason}</td>
                <td className="p-3">
                  {t.status === "PROPOSED" ? (
                    approve && t.proposed_by !== me.id ? <SLADecision targetId={t.id} /> : t.proposed_by === me.id ? "Waiting for a different person" : "Waiting for an approver"
                  ) : t.decided_at ? (
                    <>
                      <ShortId value={t.decided_by} /> <When at={t.decided_at} />
                    </>
                  ) : (
                    "—"
                  )}
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="propose" title="Propose a target">
        {can(me.permissions, "ops.sla.request") ? <ProposeSLAForm /> : <NoPermission action="Proposing a target" permission="ops.sla.request" />}
      </Section>
    </AccountPage>
  );
}
