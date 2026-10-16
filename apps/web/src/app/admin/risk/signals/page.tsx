import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, toOptions } from "@/components/admin/payments/common";
import { RISK_BASE, RiskBanner, SignalsTable } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, pick } from "@/lib/payments/labels";
import { readRiskPage, readSignal } from "@/lib/risk/normalise";
import { RISK_CATEGORIES, SIGNAL_TYPES, SUBJECT_TYPES } from "@/lib/risk/types";

export const metadata: Metadata = { title: "Risk signals", robots: { index: false, follow: false, nocache: true } };

const BASE = `${RISK_BASE}/signals`;

/** Normalised risk signals (`risk.operations.view`), newest first. Facts are ids, codes and amounts only. */
export default async function RiskSignalsPage({ searchParams }: PageProps<"/admin/risk/signals">) {
  await requireStaff();
  const p = await searchParams;
  const filters = { category: pick(p.category, RISK_CATEGORIES), signal_type: pick(p.signal_type, SIGNAL_TYPES), subject_type: pick(p.subject_type, SUBJECT_TYPES) };
  const page = readRiskPage((await serverGetOr404<unknown>(`/api/v1/admin/risk/signals${buildQuery({ ...filters, limit: "100" })}`, BASE)).data, readSignal);
  return (
    <AccountPage title="Risk signals" intro="Observed and derived facts recorded from domain events. A signal is not a finding; a false positive stays recorded and labelled.">
      <RiskBanner />
      <FilterForm
        action={BASE}
        label="Filter signals"
        fields={[
          { name: "category", label: "Category", value: filters.category, options: toOptions(RISK_CATEGORIES) },
          { name: "signal_type", label: "Signal type", value: filters.signal_type, options: toOptions(SIGNAL_TYPES) },
          { name: "subject_type", label: "Subject type", value: filters.subject_type, options: toOptions(SUBJECT_TYPES) },
        ]}
      />
      <SignalsTable items={page.items} caption="Risk signals" />
    </AccountPage>
  );
}
