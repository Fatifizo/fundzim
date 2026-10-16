import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { TableShell, When } from "@/components/admin/payments/common";
import { AlertsList, HealthBadge, OpsBanner, Section } from "@/components/admin/operations/ops";
import { DeclareFromAlert } from "@/components/admin/operations/ops-actions";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { can } from "@/lib/operations/navigation";
import { humanise } from "@/lib/payments/labels";
import { items, list, obj } from "@/lib/operations/normalise";
import type { Alert, HealthComponent } from "@/lib/operations/types";

export const metadata: Metadata = { title: "Platform health", robots: { index: false, follow: false, nocache: true } };

const BASE = "/admin/operations/health";

/**
 * Platform health (`ops.dashboard.view`): each component checked or read when the page loads, with its evidence
 * source. Nothing is reported HEALTHY without evidence; a disabled capability is BLOCKED, never HEALTHY.
 */
export default async function HealthPage() {
  const me = await requireStaff();
  const [h, a] = await Promise.all([
    serverGetOr404<unknown>("/api/v1/admin/operations/health", BASE).then((r) => r.data),
    serverGetOr404<unknown>("/api/v1/admin/operations/alerts", BASE).then((r) => r.data),
  ]);
  const ho = obj(h);
  const comps = list<HealthComponent>(h, "components");
  const alerts = items<Alert>(a);
  const manage = can(me.permissions, "ops.incident.manage");
  return (
    <AccountPage title="Platform health" intro={<>Overall: <HealthBadge status={typeof ho.overall === "string" ? ho.overall : "UNKNOWN"} /> (measured <When at={typeof ho.generated_at === "string" ? ho.generated_at : null} />)</>}>
      <OpsBanner notice={typeof ho.notice === "string" ? ho.notice : undefined} />
      <Section id="components" title="Components">
        <TableShell caption="Platform components" head={["Component", "Status", "Detail", "Evidence source", "Observed"]} minWidthClass="min-w-[52rem]">
          {comps.map((c) => (
            <tr key={c.name} className="border-b border-line align-top last:border-b-0">
              <td className="p-3 font-semibold">{humanise(c.name)}</td>
              <td className="p-3">
                <HealthBadge status={c.status} />
              </td>
              <td className="p-3 break-words">{c.detail}</td>
              <td className="p-3 break-words text-sm">{c.source}</td>
              <td className="p-3">
                <When at={c.observed_at} />
              </td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="alerts" title="Alerts">
        <AlertsList alerts={alerts} canDeclare={manage} declare={(al) => <DeclareFromAlert alertKey={al.key} title={al.title} />} />
      </Section>
    </AccountPage>
  );
}
