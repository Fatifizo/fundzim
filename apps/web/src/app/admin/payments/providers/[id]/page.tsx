import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Code, Facts, LiveExecutionPanel, PAYMENTS_BASE, Section, SimulatedBadge, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { PaymentAction } from "@/components/admin/payments/payment-action";
import { ContractBasis, ExecutionEnvironmentNotice, HealthSummary } from "@/components/admin/payments/provider-health";
import { Badge } from "@/components/ui/badge";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { humanise } from "@/lib/payments/labels";
import { readProviderDetail, readProviders } from "@/lib/payments/normalise";

export const metadata: Metadata = { title: "Payment provider", robots: { index: false, follow: false, nocache: true } };

/**
 * One provider (`payment.view`): execution gates, derived health, contract basis, accounts (secret REFERENCE names
 * only), recent health events and normalised error counts. Staff controls (payment.operate + step-up) enable
 * SANDBOX execution, disable, or declare/resolve an outage. Nothing here enables production or changes a payment.
 */
export default async function ProviderPage({ params }: PageProps<"/admin/payments/providers/[id]">) {
  await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const path = `${PAYMENTS_BASE}/providers/${id}`;
  const [result, listRaw] = await Promise.all([
    serverGetOr404<unknown>(`/api/v1/admin/payments/providers/${id}`, path),
    serverGetOptional<unknown>("/api/v1/admin/payments/providers", path, null),
  ]);
  const p = readProviderDetail(result.data);
  if (!p) notFound();
  const gates = listRaw !== null ? readProviders(listRaw) : null;
  const categories = Object.entries(p.error_categories_24h);
  return (
    <AccountPage title={p.display_name} intro={<Link href={`${PAYMENTS_BASE}/providers`}>All providers</Link>}>
      <ExecutionEnvironmentNotice env={p.execution_environment} />
      {gates ? <LiveExecutionPanel live={gates.live_execution} sandboxEnabled={gates.sandbox_enabled} /> : null}
      <Section id="provider-state" title="State">
        <p className="flex flex-wrap gap-2">
          <SimulatedBadge simulated={p.simulated} />
          {p.usable ? <Badge tone="brand">Usable in this environment</Badge> : <Badge tone="neutral">Not usable in this environment</Badge>}
        </p>
        <Facts
          items={[
            ["Code", <Code key="c">{p.code}</Code>],
            ["Production status", <StatusBadge key="s" status={p.status} />],
            ["Sandbox execution", <StatusBadge key="ss" status={p.sandbox_status} />],
            ["Selection", `${humanise(p.selection_status)}${p.selection_adr ? ` (${p.selection_adr})` : ""}`],
            ["Allow-listed in this deployment", p.allowlisted ? "Yes" : "No"],
            ["Adapter registered", p.adapter_registered ? "Yes" : "No"],
            ["Contract basis", <ContractBasis key="cb" contract={p.contract} />],
            ["Confirmation mode", humanise(p.confirmation_mode)],
          ]}
        />
      </Section>
      <Section id="provider-health" title="Health">
        <HealthSummary health={p.health} />
      </Section>
      <Section
        id="provider-controls"
        title="Controls"
        description="Each control asks for a reason and a fresh MFA code, and is audited. None of them enables production execution or changes any payment."
      >
        <div className="flex flex-wrap gap-3">
          {p.sandbox_status !== "ENABLED" ? <PaymentAction target={{ kind: "provider", providerId: p.id, action: "enable-sandbox" }} /> : null}
          {p.sandbox_status === "ENABLED" || p.status === "ENABLED" ? <PaymentAction target={{ kind: "provider", providerId: p.id, action: "disable" }} /> : null}
          {p.health.outage_declared ? (
            <PaymentAction target={{ kind: "provider", providerId: p.id, action: "outage/resolve" }} />
          ) : (
            <PaymentAction target={{ kind: "provider", providerId: p.id, action: "outage" }} />
          )}
        </div>
      </Section>
      <Section id="provider-accounts" title="Accounts">
        {p.accounts.length === 0 ? (
          <p className="text-ink-700">No accounts.</p>
        ) : (
          <TableShell caption={`${p.display_name} accounts`} head={["Account", "Environment", "Status", "Secret references", "In scope", "Resolve"]} minWidthClass="min-w-[48rem]">
            {p.accounts.map((a) => (
              <tr key={a.id} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-semibold">
                  {a.label || "—"}
                </th>
                <td className="p-3">{humanise(a.environment)}</td>
                <td className="p-3">
                  <StatusBadge status={a.status} />
                </td>
                <td className="p-3 text-sm">
                  <Code>{a.api_credentials_secret_ref ?? "—"}</Code>
                  {a.webhook_secret_ref ? (
                    <span className="block">
                      <Code>{a.webhook_secret_ref}</Code>
                    </span>
                  ) : null}
                </td>
                <td className="p-3">{a.credential_scope_ok ? <Badge tone="brand">Yes</Badge> : <Badge tone="gold">No</Badge>}</td>
                <td className="p-3">{a.secrets_resolvable ? <Badge tone="brand">Yes</Badge> : <Badge tone="gold">No</Badge>}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="provider-errors" title="Provider errors (last 24 hours)" description="Provider outcomes normalised into FundZim's error categories.">
        {categories.every(([, n]) => n === 0) ? (
          <p className="text-ink-700">No provider errors recorded in the last 24 hours.</p>
        ) : (
          <TableShell caption="Error categories" head={["Category", "Calls"]} minWidthClass="min-w-[24rem]">
            {categories
              .filter(([, n]) => n > 0)
              .map(([c, n]) => (
                <tr key={c} className="border-b border-line last:border-b-0">
                  <th scope="row" className="p-3 font-semibold">
                    {humanise(c)}
                  </th>
                  <td className="p-3">{n}</td>
                </tr>
              ))}
          </TableShell>
        )}
      </Section>
      <Section id="provider-events" title="Recent health events">
        {p.recent_health_events.length === 0 ? (
          <p className="text-ink-700">No health events recorded.</p>
        ) : (
          <TableShell caption="Recent health events" head={["Observed", "Event", "Operation", "Error class", "Latency"]} minWidthClass="min-w-[44rem]">
            {p.recent_health_events.map((e) => (
              <tr key={e.id} className="border-b border-line last:border-b-0">
                <td className="p-3">
                  <When at={e.observed_at} />
                </td>
                <th scope="row" className="p-3 font-semibold">
                  {humanise(e.event_kind)}
                  {e.staff_declared ? <span className="block text-sm font-normal text-ink-600">Declared by staff</span> : null}
                </th>
                <td className="p-3">{humanise(e.operation)}</td>
                <td className="p-3">{e.error_class ? humanise(e.error_class) : "—"}</td>
                <td className="p-3">{e.latency_ms === null ? "—" : `${e.latency_ms} ms`}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
