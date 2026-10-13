import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { Code, LiveExecutionPanel, PAYMENTS_BASE, SimulatedBadge, StatusBadge, TableShell } from "@/components/admin/payments/common";
import { ContractBasis, ExecutionEnvironmentNotice, HealthSummary } from "@/components/admin/payments/provider-health";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readProviders } from "@/lib/payments/normalise";
import type { CapabilityFlags } from "@/lib/payments/types";

export const metadata: Metadata = { title: "Payment providers", robots: { index: false, follow: false, nocache: true } };

const FLAG_LABELS: Record<keyof CapabilityFlags, string> = {
  refund_api: "Refund API",
  partial_refund: "Partial refund",
  idempotent_create: "Idempotent create",
  supports_cancel: "Cancel",
  signed_webhooks: "Signed webhooks",
  status_api: "Status API",
  separate_capture: "Separate capture",
  documented_expiry: "Documented expiry",
};

/**
 * Providers, accounts and capabilities (`payment.view`). Accounts show secret REFERENCE NAMES and whether they
 * resolve — never secret values (the API never sends them).
 */
export default async function ProvidersPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/payments/providers", `${PAYMENTS_BASE}/providers`);
  const view = readProviders(result.data);
  return (
    <AccountPage title="Payment providers" intro="Configured providers, their accounts and current capabilities.">
      <ExecutionEnvironmentNotice env={view.execution_environment} />
      <LiveExecutionPanel live={view.live_execution} sandboxEnabled={view.sandbox_enabled} />
      <p className="text-ink-700">
        Allow-listed providers in this deployment: {view.allowlist.length === 0 ? "none" : view.allowlist.map((c) => <Code key={c}>{c} </Code>)}
      </p>
      {view.items.length === 0 ? <EmptyState title="No providers configured" /> : null}
      {view.items.map((p) => (
        <section key={p.code} aria-labelledby={`provider-${p.code}`} className="space-y-4 rounded-card border border-line bg-surface p-5">
          <div className="flex flex-wrap items-center gap-2">
            <h2 id={`provider-${p.code}`} className="font-display text-2xl font-semibold text-ink-900">
              {p.id ? <Link href={`${PAYMENTS_BASE}/providers/${encodeURIComponent(p.id)}`}>{p.display_name}</Link> : p.display_name}
            </h2>
            <SimulatedBadge simulated={p.simulated} />
          </div>
          <dl className="grid gap-3 sm:grid-cols-3">
            <div>
              <dt className="text-sm font-semibold text-ink-600">Code</dt>
              <dd>
                <Code>{p.code}</Code>
              </dd>
            </div>
            <div>
              <dt className="text-sm font-semibold text-ink-600">Status</dt>
              <dd>
                <StatusBadge status={p.status} />
              </dd>
            </div>
            <div>
              <dt className="text-sm font-semibold text-ink-600">Circuit</dt>
              <dd>
                <StatusBadge status={p.circuit} />
              </dd>
            </div>
            <div>
              <dt className="text-sm font-semibold text-ink-600">Sandbox execution</dt>
              <dd>
                <StatusBadge status={p.sandbox_status} />
              </dd>
            </div>
            <div>
              <dt className="text-sm font-semibold text-ink-600">Selection</dt>
              <dd>{humanise(p.selection_status)}</dd>
            </div>
            <div>
              <dt className="text-sm font-semibold text-ink-600">Contract basis</dt>
              <dd>
                <ContractBasis contract={p.contract} />
              </dd>
            </div>
            <div>
              <dt className="text-sm font-semibold text-ink-600">Confirmation mode</dt>
              <dd>{humanise(p.confirmation_mode)}</dd>
            </div>
          </dl>
          <h3 className="font-semibold text-ink-900">Health</h3>
          <HealthSummary health={p.health} />
          <h3 className="font-semibold text-ink-900">Accounts</h3>
          {p.accounts.length === 0 ? (
            <p className="text-ink-700">No accounts.</p>
          ) : (
            <TableShell caption={`${p.display_name} accounts`} head={["Account", "Environment", "Status", "Secret references", "Secrets resolve", "Replay window"]} minWidthClass="min-w-[48rem]">
              {p.accounts.map((acc) => (
                <tr key={acc.id} className="border-b border-line align-top last:border-b-0">
                  <th scope="row" className="p-3 font-semibold">
                    {acc.label || "—"}
                  </th>
                  <td className="p-3">{humanise(acc.environment)}</td>
                  <td className="p-3">
                    <StatusBadge status={acc.status} />
                  </td>
                  <td className="p-3 text-sm">
                    API: <Code>{acc.api_credentials_secret_ref ?? "—"}</Code>
                    <span className="block">
                      Webhook: <Code>{acc.webhook_secret_ref ?? "—"}</Code>
                    </span>
                    <span className="block">
                      Previous webhook: <Code>{acc.webhook_secret_ref_previous ?? "—"}</Code>
                    </span>
                  </td>
                  <td className="p-3">{acc.secrets_resolvable ? <Badge tone="brand">Yes</Badge> : <Badge tone="gold">No</Badge>}</td>
                  <td className="p-3">{acc.webhook_replay_window_seconds === null ? "—" : `${acc.webhook_replay_window_seconds} s`}</td>
                </tr>
              ))}
            </TableShell>
          )}
          <h3 className="font-semibold text-ink-900">Capabilities</h3>
          {p.capabilities.length === 0 ? (
            <p className="text-ink-700">No capabilities.</p>
          ) : (
            <TableShell caption={`${p.display_name} capabilities`} head={["Method", "Currency", "Routable", "Custody", "Evidence", "Features", "Version"]} minWidthClass="min-w-[56rem]">
              {p.capabilities.map((c) => (
                <tr key={c.id} className="border-b border-line align-top last:border-b-0">
                  <th scope="row" className="p-3 font-semibold">
                    {humanise(c.method)}
                    <span className="block text-sm font-normal text-ink-600">{humanise(c.method_category)}</span>
                  </th>
                  <td className="p-3">{c.currency}</td>
                  <td className="p-3">{c.routable ? <Badge tone="brand">Routable</Badge> : <Badge tone="neutral">Not routable</Badge>}</td>
                  <td className="p-3">{humanise(c.custody_model)}</td>
                  <td className="p-3 text-sm">
                    {humanise(c.evidence_status)}
                    {c.pcr_ref ? <span className="block">{c.pcr_ref}</span> : null}
                    {c.evidence_source ? <span className="block break-words">{c.evidence_source}</span> : null}
                  </td>
                  <td className="p-3 text-sm">
                    {(Object.keys(FLAG_LABELS) as Array<keyof CapabilityFlags>).filter((k) => c.flags[k]).map((k) => FLAG_LABELS[k]).join(", ") || "None"}
                  </td>
                  <td className="p-3">{c.capability_version ?? "—"}</td>
                </tr>
              ))}
            </TableShell>
          )}
        </section>
      ))}
    </AccountPage>
  );
}
