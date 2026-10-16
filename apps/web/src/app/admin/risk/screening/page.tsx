import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, FilterForm, Section } from "@/components/admin/payments/common";
import { ScreeningRequestForm } from "@/components/admin/risk/risk-forms";
import { RISK_BASE, RiskBanner, ScreeningsTable } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, pick } from "@/lib/payments/labels";
import { screeningStatusLabel } from "@/lib/risk/labels";
import { readCapabilities, readRiskPage, readScreening } from "@/lib/risk/normalise";
import { SCREENING_STATUSES } from "@/lib/risk/types";

export const metadata: Metadata = { title: "Screening (TEST)", robots: { index: false, follow: false, nocache: true } };

const BASE = `${RISK_BASE}/screening`;

/** The provider-neutral screening foundation (`risk.operations.view`): TEST simulator only, synthetic references. */
export default async function RiskScreeningPage({ searchParams }: PageProps<"/admin/risk/screening">) {
  await requireStaff();
  const p = await searchParams;
  const status = pick(p.status, SCREENING_STATUSES);
  const page = readRiskPage((await serverGetOr404<unknown>(`/api/v1/admin/risk/screening${buildQuery({ status })}`, BASE)).data, readScreening);
  const caps = readCapabilities(await serverGetOptional<unknown>("/api/v1/admin/risk/screening/capabilities", BASE, null));
  return (
    <AccountPage title="Screening (TEST)" intro="Synthetic TEST screening through the simulator. A provider result is not an analyst decision, and an analyst decision is not a compliance decision.">
      <RiskBanner />
      <Section id="capabilities" title="Provider">
        <Facts
          items={[
            ["Production screening", caps.production_screening],
            ["Screening provider selected", caps.provider_selected ? "Yes" : "No — no provider is selected"],
            ["Available", caps.providers.length ? caps.providers.map((c) => `${c.code} (${c.environment} simulator)`).join(", ") : "None"],
          ]}
        />
        <p className="text-ink-700">Production screening BLOCKED, no provider selected, TEST simulator only. Every result is labelled TEST_ONLY.</p>
      </Section>
      <FilterForm action={BASE} label="Filter screening requests" fields={[{ name: "status", label: "Status", value: status, options: SCREENING_STATUSES.map((s) => ({ value: s, label: screeningStatusLabel(s) })) }]} />
      <ScreeningsTable items={page.items} caption="Screening requests" />
      <Section id="request" title="Request a TEST screening" description="Only a synthetic reference is sent to the simulator: no name, date of birth or document.">
        <ScreeningRequestForm />
      </Section>
    </AccountPage>
  );
}
