import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { TableShell } from "@/components/admin/payments/common";
import { HealthBadge } from "@/components/admin/operations/ops";
import { CommsBanner } from "@/components/admin/communications/comms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { items } from "@/lib/operations/normalise";
import type { Provider } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Messaging providers", robots: { index: false, follow: false, nocache: true } };

/** Provider readiness (`communications.view`). No provider is selected: readiness is at most CONFIGURED_TEST; health comes from attempts only. */
export default async function ProvidersPage() {
  await requireStaff();
  const list = items<Provider>((await serverGetOr404<unknown>("/api/v1/admin/communications/providers", "/admin/communications/providers")).data);
  return (
    <AccountPage title="Messaging providers" intro="Production providers are NOT_SELECTED. A simulator is TEST_ONLY and sends nothing.">
      <CommsBanner />
      <TableShell caption="Messaging providers" head={["Provider", "Channel", "Readiness", "Active here", "Health (24 h)", "Known limitations"]} minWidthClass="min-w-[60rem]">
        {list.map((p) => (
          <tr key={p.provider_code} className="border-b border-line align-top last:border-b-0">
            <td className="p-3 font-semibold">{p.display_name}{p.simulated ? <div className="text-sm">Simulated</div> : null}</td>
            <td className="p-3">{humanise(p.channel)}</td>
            <td className="p-3">{humanise(p.readiness)}</td>
            <td className="p-3">{p.active_in_this_process ? "Yes" : "No"}</td>
            <td className="p-3"><HealthBadge status={p.health} /><div className="text-sm">{p.failed_attempts_24h} failed of {p.attempts_24h}</div></td>
            <td className="p-3 break-words text-sm">{p.known_limitations}</td>
          </tr>
        ))}
      </TableShell>
    </AccountPage>
  );
}
