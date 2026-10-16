import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Section } from "@/components/admin/payments/common";
import { CreateRuleForm } from "@/components/admin/risk/risk-forms";
import { RISK_BASE, RiskBanner, RulesTable } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readRiskPage, readRuleSummary } from "@/lib/risk/normalise";

export const metadata: Metadata = { title: "Risk rules", robots: { index: false, follow: false, nocache: true } };

/** Risk rules (`risk.operations.view`; creating needs `risk.rule.request` and step-up). */
export default async function RiskRulesPage() {
  await requireStaff();
  const page = readRiskPage((await serverGetOr404<unknown>("/api/v1/admin/risk/rules", `${RISK_BASE}/rules`)).data, readRuleSummary);
  return (
    <AccountPage title="Risk rules" intro="A rule is a declarative definition, never code. Its versions pass maker-checker approval before they apply.">
      <RiskBanner />
      <RulesTable items={page.items} caption="Risk rules" />
      <Section id="create" title="Create a risk rule" description="Creating a rule changes nothing: its draft must be submitted, approved by a different person and activated.">
        <CreateRuleForm />
      </Section>
    </AccountPage>
  );
}
