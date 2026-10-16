import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FilterForm, toOptions } from "@/components/admin/payments/common";
import { EvaluationsTable, RISK_BASE, RiskBanner } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { buildQuery, pick } from "@/lib/payments/labels";
import { readEvaluation, readRiskPage } from "@/lib/risk/normalise";
import { CLASSIFICATIONS, SUBJECT_TYPES } from "@/lib/risk/types";

export const metadata: Metadata = { title: "Risk evaluations", robots: { index: false, follow: false, nocache: true } };

const BASE = `${RISK_BASE}/evaluations`;

/** Rule engine evaluations (`risk.operations.view`), newest first. */
export default async function RiskEvaluationsPage({ searchParams }: PageProps<"/admin/risk/evaluations">) {
  await requireStaff();
  const p = await searchParams;
  const filters = { classification: pick(p.classification, CLASSIFICATIONS), subject_type: pick(p.subject_type, SUBJECT_TYPES) };
  const page = readRiskPage((await serverGetOr404<unknown>(`/api/v1/admin/risk/evaluations${buildQuery({ ...filters, limit: "100" })}`, BASE)).data, readEvaluation);
  return (
    <AccountPage
      title="Risk evaluations"
      intro="Every evaluation is deterministic and explained: the score is the capped sum of the fired rules' contributions. A risk score is a prioritisation aid from configured rules over recorded signals. It is not proof of fraud or of any wrongdoing."
    >
      <RiskBanner />
      <FilterForm
        action={BASE}
        label="Filter evaluations"
        fields={[
          { name: "classification", label: "Classification", value: filters.classification, options: toOptions(CLASSIFICATIONS) },
          { name: "subject_type", label: "Subject type", value: filters.subject_type, options: toOptions(SUBJECT_TYPES) },
        ]}
      />
      <EvaluationsTable items={page.items} caption="Risk evaluations" />
    </AccountPage>
  );
}
