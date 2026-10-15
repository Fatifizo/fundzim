import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FEES_BASE, FeeBanner } from "@/components/admin/finance/fees";
import { Facts, Section, When } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readFeeOverview } from "@/lib/fees/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Fees", robots: { index: false, follow: false, nocache: true } };

/** Fee overview (`fee.view`): TEST environment, production fees BLOCKED, counts and the latest reconciliation. */
export default async function FeesOverviewPage() {
  await requireStaff();
  const o = readFeeOverview((await serverGetOr404<unknown>("/api/v1/admin/fees/overview", FEES_BASE)).data);
  const r = o.latest_reconciliation;
  return (
    <AccountPage title="Fees" intro="Fee policies, provider fee evidence, quotes, assessments, adjustments and fee reconciliation.">
      <FeeBanner />
      <Section id="gates" title="Environment">
        <Facts items={[["Fee environment", o.fee_environment], ["Production fees", o.production_fees], ["Active quotes", String(o.active_quotes)], ["Assessments", String(o.assessments)], ["Adjustments awaiting approval", String(o.pending_adjustments)], ["Open reconciliation items", String(o.open_reconciliation_items)]]} />
      </Section>
      <Section id="versions" title="Policy versions by status">
        <Facts items={Object.entries(o.policy_versions_by_status).map(([k, n]) => [humanise(k), String(n)] as [string, string])} />
      </Section>
      <Section id="evidence" title="Provider fee evidence by status">
        <Facts items={Object.entries(o.provider_schedules_by_status).map(([k, n]) => [humanise(k), String(n)] as [string, string])} />
      </Section>
      <Section id="recon" title="Latest fee reconciliation">
        {r ? (
          <Facts items={[["Started", <When key="s" at={r.started_at} />], ["Checked", String(r.checked)], ["Matched", String(r.matched)], ["Mismatches", String(r.mismatches)], ["Settlement reconciliation", r.settlement_reconciliation]]} />
        ) : (
          <p className="text-ink-700">No reconciliation has run yet.</p>
        )}
      </Section>
      {o.notes.length ? (
        <Section id="notes" title="Open dependencies">
          <ul className="list-disc space-y-1 pl-5 text-ink-800">
            {o.notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </Section>
      ) : null}
    </AccountPage>
  );
}
