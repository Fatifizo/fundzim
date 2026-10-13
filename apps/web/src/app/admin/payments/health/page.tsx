import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { LiveExecutionPanel, PAYMENTS_BASE, SimulatedBadge, StatusBadge, TableShell } from "@/components/admin/payments/common";
import { ExecutionEnvironmentNotice, HealthSummary } from "@/components/admin/payments/provider-health";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readHealthView } from "@/lib/payments/normalise";

export const metadata: Metadata = { title: "Payment provider health", robots: { index: false, follow: false, nocache: true } };

/** Provider health (`payment.view`): derived from observations only, plus the secret manager's reachability. */
export default async function ProviderHealthPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/payments/health", `${PAYMENTS_BASE}/health`);
  const view = readHealthView(result.data);
  return (
    <AccountPage title="Provider health" intro="Health is derived from recent provider calls, webhooks, circuit state and declared outages. No observation means UNKNOWN.">
      <ExecutionEnvironmentNotice env={view.execution_environment} />
      <LiveExecutionPanel live={view.live_execution} sandboxEnabled={view.execution_environment === "LOCAL_SIMULATOR"} />
      <p className="text-ink-800">
        Secret manager: <StatusBadge status={view.secret_manager} />
      </p>
      {view.items.length === 0 ? (
        <EmptyState title="No providers configured" />
      ) : (
        <TableShell caption="Provider health" head={["Provider", "Usable here", "Health"]} minWidthClass="min-w-[48rem]">
          {view.items.map((it) => (
            <tr key={it.id} className="border-b border-line align-top last:border-b-0">
              <th scope="row" className="p-3 font-semibold">
                <Link href={`${PAYMENTS_BASE}/providers/${encodeURIComponent(it.id)}`}>{it.provider.display_name}</Link>
                <span className="block">
                  <SimulatedBadge simulated={it.provider.simulated} />
                </span>
              </th>
              <td className="p-3">{it.usable ? "Yes" : "No"}</td>
              <td className="p-3">
                <HealthSummary health={it.health} />
              </td>
            </tr>
          ))}
        </TableShell>
      )}
    </AccountPage>
  );
}
