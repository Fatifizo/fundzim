import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Code, Facts, Section, TableShell, When } from "@/components/admin/payments/common";
import { ShortId } from "@/components/admin/operations/ops";
import { CommsBanner, DeliveryStatus } from "@/components/admin/communications/comms";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { obj } from "@/lib/operations/normalise";
import type { DeliveryDetail } from "@/lib/notifications/types";

export const metadata: Metadata = { title: "Delivery", robots: { index: false, follow: false, nocache: true } };

/** One delivery with its attempts and provider evidence (`communications.view`). No message content is stored. */
export default async function DeliveryPage({ params }: PageProps<"/admin/communications/deliveries/[id]">) {
  await requireStaff();
  const { id } = await params;
  const d = obj((await serverGetOr404<unknown>(`/api/v1/admin/communications/deliveries/${encodeURIComponent(id)}`, `/admin/communications/deliveries/${encodeURIComponent(id)}`)).data) as unknown as DeliveryDetail;
  const x = d.delivery;
  return (
    <AccountPage title={`${humanise(x.channel)} delivery`} intro={<>Template <Code>{x.template_key}</Code> version {d.template_version_no}</>}>
      <CommsBanner />
      <Section id="facts" title="Delivery">
        <Facts items={[
          ["Status", <DeliveryStatus key="s" status={x.status} />], ["Decision", humanise(x.decision)], ["Suppression", x.suppression_reason ? humanise(x.suppression_reason) : "—"],
          ["Recipient account", <ShortId key="r" value={x.recipient_user_id} />], ["Destination", x.destination_masked ?? "—"], ["Provider", x.provider_code ?? "—"],
          ["Attempts", `${x.attempt_count} of ${x.max_attempts}`], ["Next attempt", x.next_attempt_at ? <When key="n" at={x.next_attempt_at} /> : "—"],
          ["Accepted by the provider", x.accepted_at ? <When key="a" at={x.accepted_at} /> : "—"], ["Delivered (provider evidence)", x.delivered_at ? <When key="d" at={x.delivered_at} /> : "—"],
          ["Failure", x.failure_code ?? "—"], ["TEST data", x.test_data ? "Yes" : "No"],
        ]} />
      </Section>
      <Section id="attempts" title="Attempts">
        {d.attempts.length === 0 ? <p className="text-ink-700">No attempt yet.</p> : (
          <TableShell caption="Attempts" head={["#", "Outcome", "Error", "Provider", "Latency", "Finished"]}>
            {d.attempts.map((a) => (
              <tr key={a.attempt_no} className="border-b border-line last:border-b-0">
                <td className="p-3">{a.attempt_no}</td>
                <td className="p-3">{humanise(a.outcome)}</td>
                <td className="p-3">{a.error_code ?? "—"}</td>
                <td className="p-3">{a.provider_code}</td>
                <td className="p-3">{a.latency_ms} ms</td>
                <td className="p-3"><When at={a.finished_at} /></td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="evidence" title="Provider evidence">
        {d.provider_evidence.length === 0 ? <p className="text-ink-700">No provider callback recorded.</p> : (
          <TableShell caption="Provider evidence" head={["Reported", "Occurred", "Received", "Applied"]}>
            {d.provider_evidence.map((e) => (
              <tr key={e.provider_event_id} className="border-b border-line last:border-b-0">
                <td className="p-3">{humanise(e.reported_status)}</td>
                <td className="p-3"><When at={e.occurred_at} /></td>
                <td className="p-3"><When at={e.received_at} /></td>
                <td className="p-3">{e.applied ? "Yes" : `No (${humanise(e.not_applied_reason)})`}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
