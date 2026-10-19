import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, TableShell, When } from "@/components/admin/payments/common";
import { HealthBadge } from "@/components/admin/operations/ops";
import { CommsBanner } from "@/components/admin/communications/comms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { formatDuration, obj } from "@/lib/operations/normalise";
import type { CommsOverview } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Communications", robots: { index: false, follow: false, nocache: true } };

const n = (v: number | null | undefined) => (v === null || v === undefined ? "Not measured" : v.toLocaleString("en-GB"));

/** Communications dashboard (`communications.view`): every figure is a query over the engine's records, measured now. */
export default async function CommunicationsPage() {
  await requireStaff();
  const o = obj((await serverGetOr404<unknown>("/api/v1/admin/communications/overview", "/admin/communications")).data) as unknown as CommsOverview;
  const rate = o.error_rate_last_1h === null ? "Not measured (no attempts in the last hour)" : `${(o.error_rate_last_1h * 100).toFixed(1)} %`;
  return (
    <AccountPage title="Communications" intro={o.notice}>
      <CommsBanner />
      <Section id="gates" title="Gates">
        <Facts items={[["Live messaging", o.live_messaging_enabled ? "Enabled" : "Disabled (no provider selected)"], ["Marketing", o.marketing_enabled ? "Enabled" : "Disabled"],
          ["Generated", <When key="g" at={o.generated_at} />]]} />
      </Section>
      <Section id="queues" title="Queues and failures">
        <Facts items={[
          ["Waiting to send", n(o.queue_backlog)], ["Oldest waiting", o.oldest_queued_age_seconds === null ? "—" : formatDuration(o.oldest_queued_age_seconds)],
          ["Waiting to retry", n(o.retry_backlog)], ["Oldest retry", o.oldest_retry_age_seconds === null ? "—" : formatDuration(o.oldest_retry_age_seconds)],
          ["Dead letters to review", n(o.dead_letters_open)], ["Failed attempts in the last hour", `${n(o.failed_attempts_last_1h)} of ${n(o.attempts_last_1h)}`],
          ["Error rate (last hour)", rate], ["Send latency p50 / p95 (24 h)", o.latency_p50_ms_24h === null ? "Not measured" : `${o.latency_p50_ms_24h} ms / ${o.latency_p95_ms_24h} ms`],
          ["Suppressed by consent (24 h)", n(o.consent_restricted_24h)], ["Refused provider callbacks (24 h)", n(o.webhook_rejections_24h)],
          ["Contradictory provider reports", n(o.conflicting_evidence)],
        ]} />
      </Section>
      <Section id="status" title="External deliveries by status">
        <Facts items={Object.entries(o.deliveries_by_status ?? {}).map(([k, v]) => [humanise(k), n(v)] as [string, string])} />
      </Section>
      <Section id="workers" title="Delivery workers">
        {(o.workers ?? []).length === 0 ? (
          <p className="text-ink-700">No delivery worker has reported (status unknown).</p>
        ) : (
          <TableShell caption="Delivery workers" head={["Worker", "Last sweep", "Status"]}>
            {o.workers.map((w) => (
              <tr key={w.worker_id} className="border-b border-line last:border-b-0">
                <td className="p-3 break-all">{w.worker_id}</td>
                <td className="p-3">
                  <When at={w.last_seen_at} />
                </td>
                <td className="p-3">
                  <HealthBadge status={w.status} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
