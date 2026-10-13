import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Code, Facts, PAYMENTS_BASE, Section, SimulatedBadge, StatusBadge, When } from "@/components/admin/payments/common";
import { PaymentAction } from "@/components/admin/payments/payment-action";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { humanise } from "@/lib/payments/labels";
import { readInboxDetail } from "@/lib/payments/normalise";
import { REQUEUEABLE_INBOX_STATUSES } from "@/lib/payments/types";

export const metadata: Metadata = { title: "Webhook", robots: { index: false, follow: false, nocache: true } };

function Json({ value, label }: { value: Record<string, unknown> | null; label: string }) {
  if (!value) return <p className="text-ink-700">None.</p>;
  return (
    <div role="region" aria-label={label} tabIndex={0} className="max-h-96 overflow-auto rounded-card border border-line bg-surface-muted p-4">
      <pre className="font-mono text-sm whitespace-pre-wrap break-all text-ink-900">{JSON.stringify(value, null, 2)}</pre>
    </div>
  );
}

/** One inbox item (`payment.view`): redacted payload, hash, verification facts and processing state. */
export default async function WebhookPage({ params }: PageProps<"/admin/payments/webhooks/[id]">) {
  await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payments/webhooks/${id}`, `${PAYMENTS_BASE}/webhooks/${id}`);
  const w = readInboxDetail(result.data);
  if (!w) notFound();
  return (
    <AccountPage
      title="Webhook"
      intro={
        <Link href={`${PAYMENTS_BASE}/webhooks`} className="text-base font-semibold text-brand-700 underline">
          Back to webhooks
        </Link>
      }
    >
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge status={w.status} />
        <SimulatedBadge simulated={w.simulated} />
      </div>
      <PaymentAction target={{ kind: "requeue", inboxId: w.id }} available={REQUEUEABLE_INBOX_STATUSES.has(w.status)} />
      <Section id="processing-title" title="Processing">
        <Facts
          items={[
            ["Inbox id", <Code key="id">{w.id}</Code>],
            ["Provider", w.provider.display_name],
            ["Provider event id", <Code key="pe">{w.provider_event_id || "—"}</Code>],
            ["Event class", humanise(w.event_class)],
            ["Raw event type", <Code key="rt">{w.event_type_raw || "—"}</Code>],
            ["Our reference", <Code key="our">{w.our_reference ?? "—"}</Code>],
            ["Provider reference", <Code key="pr">{w.provider_reference ?? "—"}</Code>],
            ["Processing attempts", String(w.attempts)],
            ["Last error code", w.last_error_code ?? "—"],
            ["Dispatched to", w.dispatched_to ?? "—"],
            ["Next attempt", <When key="na" at={w.next_attempt_at} />],
            ["Parked subject", <Code key="ps">{w.parked_subject_id ?? "—"}</Code>],
            ["Parked until status", w.parked_until_status ? humanise(w.parked_until_status) : "—"],
            ["Park deadline", <When key="pd" at={w.park_deadline_at} />],
            ["Received", <When key="r" at={w.received_at} />],
            ["Processed", <When key="p" at={w.processed_at} />],
          ]}
        />
      </Section>
      <Section id="verification-title" title="Verification">
        <Facts
          items={[
            ["Verification method", w.verification_method ? humanise(w.verification_method) : "—"],
            ["Signature key slot", w.signature_key_slot ?? "—"],
            ["Raw body SHA-256", <Code key="sha">{w.raw_body_sha256 ?? "—"}</Code>],
          ]}
        />
      </Section>
      <Section id="payload-title" title="Payload (redacted)">
        <Json value={w.payload_redacted} label="Redacted payload" />
      </Section>
      <Section id="headers-title" title="Headers (subset)">
        <Json value={w.headers_subset} label="Header subset" />
      </Section>
    </AccountPage>
  );
}
