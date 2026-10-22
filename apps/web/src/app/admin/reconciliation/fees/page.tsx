import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, When } from "@/components/admin/payments/common";
import { ExceptionTable, ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readFeeView } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Fee reconciliation", robots: { index: false, follow: false, nocache: true } };

/** Statement fees against FundZim's assessments, and the fee module's own recompute. An unknown fee is never zero. */
export default async function ReconciliationFeesPage() {
  await requireStaff();
  const v = readFeeView((await serverGetOr404<unknown>("/api/v1/admin/reconciliation/fees", "/admin/reconciliation/fees")).data);
  return (
    <AccountPage title="Fee reconciliation" intro="TEST fees only.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/fees" />
      <Section id="status" title="Evidence and revenue">
        <Facts items={[["Provider fee evidence", v.evidence], ["Revenue recognition", v.revenue]]} />
      </Section>
      <Section id="lines" title="Matched lines by fee status" description="Matched: the statement fee equals FundZim's assessment. Not reported: the statement gave no fee. No expectation: FundZim's fee is unknown.">
        <Facts items={v.matches.length === 0 ? [["Matches", "0"]] : v.matches.map((m): [string, string] => [humanise(m.key), String(m.value)])} />
      </Section>
      <Section id="recompute" title="Fee module recompute (latest run)">
        {v.recompute ? (
          <Facts items={[["Checked", String(v.recompute.checked)], ["Matched", String(v.recompute.matched)], ["Mismatches", String(v.recompute.mismatches)],
            ["Finished", <When key="f" at={v.recompute.finishedAt} />]]} />
        ) : (
          <p className="text-ink-700">The fee module has not run its reconciliation yet.</p>
        )}
      </Section>
      <Section id="mismatches" title="Fee mismatches">
        <ExceptionTable items={v.mismatches} caption="Fee mismatches" />
      </Section>
    </AccountPage>
  );
}
