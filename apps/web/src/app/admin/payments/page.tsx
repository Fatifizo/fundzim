import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { LiveExecutionPanel, PAYMENTS_BASE, Section, StatusBadge, TableShell } from "@/components/admin/payments/common";
import { RefundExecutionDisabledNotice } from "@/components/admin/payments/lists";
import { Badge } from "@/components/ui/badge";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readOverview } from "@/lib/payments/normalise";

export const metadata: Metadata = { title: "Payments", robots: { index: false, follow: false, nocache: true } };

function Count({ label, value, href }: { label: string; value: number; href?: string }) {
  return (
    <div className="rounded-card border border-line bg-surface p-4">
      <dt className="text-sm font-semibold text-ink-600">{label}</dt>
      <dd className="font-display text-3xl font-bold text-ink-900">
        {href ? (
          <Link href={href} className="text-brand-700 underline">
            {value}
          </Link>
        ) : (
          value
        )}
      </dd>
    </div>
  );
}

/** Payment operations overview (`payment.view`; 404 otherwise). Counts only — never sums of money. */
export default async function PaymentsOverviewPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/payments/overview", PAYMENTS_BASE);
  const o = readOverview(result.data);
  return (
    <AccountPage title="Payments" intro="Payment operations at a glance. Counts only: this page never adds up money.">
      <LiveExecutionPanel live={o.live_execution} sandboxEnabled={o.sandbox_enabled} />
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <Count label="Unknown outcome" value={o.unknown_count} href={`${PAYMENTS_BASE}/unknown`} />
        <Count label="Needs reconciliation" value={o.needs_reconciliation_count} href={`${PAYMENTS_BASE}/attempts?needs_reconciliation=true`} />
        <Count label="Open refund requests" value={o.open_refund_requests} href={`${PAYMENTS_BASE}/refunds`} />
        <Count label="Webhook rejections (24 h)" value={o.rejections_24h} href={`${PAYMENTS_BASE}/webhooks#rejections-title`} />
      </dl>
      <RefundExecutionDisabledNotice />
      <Section id="attempts-by-status" title="Payments by status">
        <TableShell caption="Payments by status" head={["Status", "Count"]} minWidthClass="min-w-[16rem]">
          {o.attempts_by_status.map((s) => (
            <tr key={s.status} className="border-b border-line last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                <Link href={`${PAYMENTS_BASE}/attempts?status=${encodeURIComponent(s.status)}`} className="text-brand-700 underline">
                  {humanise(s.status)}
                </Link>
              </th>
              <td className="p-3">{s.count}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="inbox-by-status" title="Webhook inbox by status">
        <TableShell caption="Webhook inbox by status" head={["Status", "Count"]} minWidthClass="min-w-[16rem]">
          {o.inbox_by_status.map((s) => (
            <tr key={s.status} className="border-b border-line last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                <Link href={`${PAYMENTS_BASE}/webhooks?status=${encodeURIComponent(s.status)}`} className="text-brand-700 underline">
                  {humanise(s.status)}
                </Link>
              </th>
              <td className="p-3">{s.count}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="providers-title" title="Providers">
        <TableShell caption="Provider status and circuit" head={["Provider", "Status", "Circuit"]} minWidthClass="min-w-[24rem]">
          {o.providers.map((p) => (
            <tr key={p.code} className="border-b border-line last:border-b-0">
              <th scope="row" className="p-3 font-semibold">
                {p.display_name}
                {p.simulated ? (
                  <span className="mt-1 block">
                    <Badge tone="gold" className="uppercase">Simulated — development only</Badge>
                  </span>
                ) : null}
              </th>
              <td className="p-3">
                <StatusBadge status={p.status} />
              </td>
              <td className="p-3">
                <StatusBadge status={p.circuit} />
              </td>
            </tr>
          ))}
        </TableShell>
        <Link href={`${PAYMENTS_BASE}/providers`} className="font-semibold text-brand-700 underline">
          Provider details
        </Link>
      </Section>
    </AccountPage>
  );
}
