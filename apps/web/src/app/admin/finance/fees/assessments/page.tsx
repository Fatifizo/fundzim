import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { AssessmentsTable, FEES_BASE, FeeBanner } from "@/components/admin/finance/fees";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readAssessment, readFeePage } from "@/lib/fees/normalise";

export const metadata: Metadata = { title: "Fee assessments", robots: { index: false, follow: false, nocache: true } };

/** Fee assessments (`fee.view`): calculated obligations, not cash, revenue or settlement. */
export default async function FeeAssessmentsPage() {
  await requireStaff();
  const page = readFeePage((await serverGetOr404<unknown>("/api/v1/admin/fees/assessments?limit=200", `${FEES_BASE}/assessments`)).data, readAssessment);
  return (
    <AccountPage title="Fee assessments" intro="Fees assessed under an approved policy version. An assessment is not cash received, revenue or settlement.">
      <FeeBanner />
      <AssessmentsTable items={page.items} caption="Fee assessments" />
    </AccountPage>
  );
}
