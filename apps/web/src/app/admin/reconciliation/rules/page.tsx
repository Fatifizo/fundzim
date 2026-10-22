import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { ShortId } from "@/components/admin/operations/ops";
import { Code, Facts, Section, StatusBadge, When } from "@/components/admin/payments/common";
import { RuleSetActions, RuleSetForm } from "@/components/admin/reconciliation/recon-admin";
import { ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readRuleSets } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Matching rules", robots: { index: false, follow: false, nocache: true } };

/**
 * Matching rule sets: the tunable parameters of the matcher, proposed by one person and approved by another, hashed and
 * immutable. None is seeded: they are assumptions about settlement timing that no provider has confirmed. Without an
 * active set, matching stops.
 */
export default async function ReconciliationRulesPage() {
  await requireStaff();
  const list = readRuleSets((await serverGetOr404<unknown>("/api/v1/admin/reconciliation/rule-sets", "/admin/reconciliation/rules")).data);
  const active = list.find((r) => r.status === "ACTIVE") ?? null;
  return (
    <AccountPage title="Matching rules" intro="Amount and currency are always checked and never used to find a match. A reference is always required.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/rules" />
      <Section id="sets" title="Rule sets">
        {list.length === 0 ? <p className="text-ink-700">No rule set exists yet. Matching is stopped until one is approved.</p> : null}
        {list.map((r) => (
          <div key={r.id} className="space-y-3 rounded-card border border-line bg-surface p-4">
            <Facts items={[
              ["Version", String(r.version)],
              ["Status", <StatusBadge key="s" status={r.status} />],
              ["Date window", `${r.dateWindowDays} days`],
              ["Settlement horizon", `${r.settlementHorizonDays} days`],
              ["Stale after", `${r.staleAfterHours} hours`],
              ["Escalate after", r.escalateAfterHours == null ? "never (not configured)" : `${r.escalateAfterHours} hours`],
              ["Why", r.reason],
              ["Proposed", <span key="p"><When at={r.proposedAt} /> by <ShortId value={r.proposedBy} /></span>],
              ["Decision", r.decidedBy ? <span key="d"><ShortId value={r.decidedBy} /> — {r.decisionNote}</span> : "—"],
              ["In force from", r.effectiveFrom ? <When key="e" at={r.effectiveFrom} /> : "—"],
              ["Configuration hash", <Code key="h">{r.sha256}</Code>],
            ]} />
            <RuleSetActions rs={r} />
          </div>
        ))}
      </Section>
      <Section id="propose" title="Propose a rule set">
        <RuleSetForm current={active} />
      </Section>
    </AccountPage>
  );
}
