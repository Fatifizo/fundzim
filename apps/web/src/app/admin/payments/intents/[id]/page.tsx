import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Amount, AttemptsTable, Code, EventTimeline, Facts, PAYMENTS_BASE, Section, SimulatedBadge, StatusBadge, When } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { readIntentDetail } from "@/lib/payments/normalise";

export const metadata: Metadata = { title: "Donation intent", robots: { index: false, follow: false, nocache: true } };

/** One donation intent with its attempts and events (`payment.view`). */
export default async function DonationIntentPage({ params }: PageProps<"/admin/payments/intents/[id]">) {
  await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const result = await serverGetOr404<unknown>(`/api/v1/admin/payments/intents/${id}`, `${PAYMENTS_BASE}/intents/${id}`);
  const intent = readIntentDetail(result.data);
  if (!intent) notFound();
  return (
    <AccountPage
      title="Donation intent"
      intro={
        <Link href={`${PAYMENTS_BASE}/intents`} className="text-base font-semibold text-brand-700 underline">
          Back to donation intents
        </Link>
      }
    >
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge status={intent.status} />
        <SimulatedBadge simulated={intent.simulated} />
      </div>
      <Facts
        items={[
          ["Donation intent id", <Code key="id">{intent.id}</Code>],
          ["Amount", <Amount key="a" money={intent.amount} />],
          ["Campaign id", <Code key="c">{intent.campaign_id || "—"}</Code>],
          ["Donor user id", <Code key="d">{intent.donor_user_id ?? "—"}</Code>],
          ["Anonymous to the public", intent.is_anonymous ? "Yes" : "No"],
          ["Expires", <When key="e" at={intent.expires_at} />],
          ["Created", <When key="cr" at={intent.created_at} />],
          ["Updated", <When key="u" at={intent.updated_at} />],
        ]}
      />
      <Section id="intent-attempts" title="Payment attempts">
        <AttemptsTable items={intent.attempts} caption="Payment attempts of this donation intent" empty="No payment attempts" />
      </Section>
      <Section id="intent-events" title="Event timeline" description="Append-only. Events that were not applied are shown with the reason.">
        <EventTimeline events={intent.events} />
      </Section>
    </AccountPage>
  );
}
