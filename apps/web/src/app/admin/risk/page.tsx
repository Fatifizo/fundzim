import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, When } from "@/components/admin/payments/common";
import { CountsFacts, RISK_BASE, RiskBanner } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readRiskOverview } from "@/lib/risk/normalise";

export const metadata: Metadata = { title: "Risk operations", robots: { index: false, follow: false, nocache: true } };

/** Risk operations overview (`risk.operations.view`): counts and pending approvals. TEST data only. */
export default async function RiskOverviewPage() {
  await requireStaff();
  const o = readRiskOverview((await serverGetOr404<unknown>("/api/v1/admin/risk/overview", RISK_BASE)).data);
  return (
    <AccountPage title="Risk operations" intro="Signals, explainable rule evaluations, cases, holds, TEST screening and monitoring.">
      <RiskBanner />
      <Section id="gates" title="Environment">
        <Facts
          items={[
            ["Environment", o.environment],
            ["Production screening", o.screening_production],
            ["Accuracy metrics", o.accuracy_metrics],
            ["Active rules", String(o.active_rules)],
            ["Overdue cases", String(o.overdue_cases)],
            ["Generated", <When key="g" at={o.generated_at} />],
          ]}
        />
      </Section>
      <Section id="pending" title="Waiting for a second person">
        <Facts
          items={[
            ["Rule versions awaiting approval", String(o.pending_rule_approvals)],
            ["Holds awaiting activation", String(o.pending_hold_activations)],
            ["Hold releases awaiting approval", String(o.pending_hold_releases)],
            ["Case outcomes awaiting approval", String(o.pending_case_decisions)],
          ]}
        />
      </Section>
      <Section id="cases" title="Open cases by priority">
        <CountsFacts items={o.open_cases_by_priority} empty="No open cases." />
      </Section>
      <Section id="case-status" title="Open cases by status">
        <CountsFacts items={o.open_cases_by_status} empty="No open cases." />
      </Section>
      <Section id="holds" title="Open holds by type">
        <CountsFacts items={o.open_holds_by_type} empty="No open holds." />
      </Section>
      <Section id="signals" title="Signals in the last 24 hours">
        <CountsFacts items={o.signals_last_24h_by_category} empty="No signals recorded." />
      </Section>
      <Section id="evaluations" title="Evaluations in the last 7 days">
        <CountsFacts items={o.evaluations_last_7d_by_classification} empty="No evaluations." />
      </Section>
      <Section id="screening" title="Screening requests by status (TEST)">
        <CountsFacts items={o.screenings_by_status} empty="No screening requests." />
      </Section>
      <Section id="monitoring" title="Monitoring events in the last 24 hours">
        <CountsFacts items={o.monitoring_last_24h_by_type} empty="No monitoring events." />
      </Section>
    </AccountPage>
  );
}
