import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FeeAction } from "@/components/admin/finance/fee-action";
import { ScheduleForm } from "@/components/admin/finance/fee-forms";
import { FEES_BASE, FeeBanner, SchedulesTable } from "@/components/admin/finance/fees";
import { Section } from "@/components/admin/payments/common";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { readFeePage, readSchedule } from "@/lib/fees/normalise";

export const metadata: Metadata = { title: "Provider fee evidence", robots: { index: false, follow: false, nocache: true } };

interface ProviderRow {
  id?: unknown;
  simulated?: unknown;
}

/** Provider fee evidence. No provider is selected (ADR-040): evidence is SYNTHETIC (sandbox) or UNVERIFIED, never VERIFIED. */
export default async function ProviderSchedulesPage() {
  await requireStaff();
  const path = `${FEES_BASE}/provider-schedules`;
  const page = readFeePage((await serverGetOr404<unknown>("/api/v1/admin/fees/provider-schedules", path)).data, readSchedule);
  const providers = await serverGetOptional<unknown>("/api/v1/admin/payments/providers", path, { items: [] });
  const rows: ProviderRow[] = Array.isArray(providers) ? providers : Array.isArray((providers as { items?: unknown })?.items) ? (providers as { items: ProviderRow[] }).items : [];
  const sandbox = rows.find((r) => r.simulated === true && typeof r.id === "string");
  return (
    <AccountPage title="Provider fee evidence" intro="Provider fees are UNKNOWN unless evidence is recorded. Estimates from this evidence are labelled ESTIMATED.">
      <FeeBanner />
      <SchedulesTable
        items={page.items}
        caption="Provider fee evidence"
        actions={(s) => (
          <>
            <FeeAction target={{ kind: "schedule-approve", id: s.id }} available={s.status === "PENDING_APPROVAL"} />
            <FeeAction target={{ kind: "schedule-retire", id: s.id }} available={s.status !== "RETIRED"} />
          </>
        )}
      />
      <Section id="record" title="Record provider fee evidence" description="VERIFIED is not available: no payment provider is selected and no contractual evidence exists.">
        <ScheduleForm sandboxProviderId={typeof sandbox?.id === "string" ? sandbox.id : null} />
      </Section>
    </AccountPage>
  );
}
