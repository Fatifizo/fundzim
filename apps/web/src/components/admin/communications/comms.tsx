import Link from "next/link";

import { Code, TableShell, When } from "@/components/admin/payments/common";
import { ShortId } from "@/components/admin/operations/ops";
import { Badge, type BadgeTone } from "@/components/ui/badge";
import { humanise } from "@/lib/payments/labels";
import type { Delivery } from "@/lib/notifications/types";

/** Staff communications building blocks (Stage 15). Destinations are masked; no message body is ever shown. */

export const deliveryHref = (id: string) => `/admin/communications/deliveries/${encodeURIComponent(id)}`;
export const templateHref = (id: string) => `/admin/communications/templates/${encodeURIComponent(id)}`;

export function DeliveryStatus({ status }: { status: string }) {
  const tone: BadgeTone = ["DELIVERED", "READ"].includes(status) ? "brand" : ["FAILED_PERMANENT", "UNKNOWN", "FAILED_RETRYABLE"].includes(status) ? "gold" : "neutral";
  return <Badge tone={tone}>{humanise(status)}</Badge>;
}

export function CommsBanner() {
  return (
    <p className="rounded-xl border border-gold-200 bg-gold-100 px-4 py-2 text-sm font-semibold text-gold-800">
      TEST environment: no production messaging provider is selected. Simulators send nothing; the development senders deliver to the local
      Mailpit only. Provider acceptance is not delivery.
    </p>
  );
}

export function DeliveriesTable({ items, caption }: { items: Delivery[]; caption: string }) {
  if (items.length === 0) return <p className="text-ink-700">No deliveries match.</p>;
  return (
    <TableShell caption={caption} head={["Created", "Template", "Channel", "Status", "Destination", "Attempts", "Decision"]} minWidthClass="min-w-[60rem]">
      {items.map((d) => (
        <tr key={d.id} className="border-b border-line align-top last:border-b-0">
          <td className="p-3">
            <Link href={deliveryHref(d.id)} className="underline underline-offset-4">
              <When at={d.created_at} />
            </Link>
          </td>
          <td className="p-3">
            <Code>{d.template_key}</Code>
            {d.test_data ? <div className="text-sm font-semibold">TEST data</div> : null}
          </td>
          <td className="p-3">{humanise(d.channel)}</td>
          <td className="p-3">
            <DeliveryStatus status={d.status} />
            {d.conflicting_evidence ? <div className="text-sm font-semibold">Contradictory provider reports</div> : null}
          </td>
          <td className="p-3">{d.destination_masked ?? "—"}</td>
          <td className="p-3">
            {d.attempt_count} / {d.max_attempts}
          </td>
          <td className="p-3 break-words text-sm">{d.suppression_reason ? humanise(d.suppression_reason) : humanise(d.decision)}</td>
        </tr>
      ))}
    </TableShell>
  );
}

export function Recipient({ id }: { id: string }) {
  return <ShortId value={id} />;
}
