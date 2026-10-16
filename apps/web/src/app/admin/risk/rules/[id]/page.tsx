import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, StatusBadge } from "@/components/admin/payments/common";
import { RiskAction, type RiskActionTarget } from "@/components/admin/risk/risk-action";
import { RuleVersionForm, SimulateForm } from "@/components/admin/risk/risk-forms";
import { HistoryTable, Id, RISK_BASE, RiskBanner } from "@/components/admin/risk/risk";
import { Badge } from "@/components/ui/badge";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { humanise } from "@/lib/payments/labels";
import { durationText, ruleActions } from "@/lib/risk/labels";
import { readHistory, readRiskPage, readRule } from "@/lib/risk/normalise";

export const metadata: Metadata = { title: "Risk rule", robots: { index: false, follow: false, nocache: true } };

const ACTIONS = ["submit", "approve", "reject", "activate", "suspend", "reactivate", "retire"] as const;

/** One risk rule with its versions, their lifecycle actions (maker-checker, step-up), simulation and history. */
export default async function RiskRulePage({ params }: PageProps<"/admin/risk/rules/[id]">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  await requireStaff();
  const path = `${RISK_BASE}/rules/${id}`;
  const r = readRule((await serverGetOr404<unknown>(`/api/v1/admin/risk/rules/${id}`, path)).data);
  if (!r) notFound();
  const histories = await Promise.all(
    r.versions.map(async (v) => readRiskPage(await serverGetOptional<unknown>(`/api/v1/admin/risk/rule-versions/${v.id}/events`, path, null), readHistory).items),
  );
  const draft = r.versions.find((v) => v.status === "DRAFT");
  const openDraft = r.versions.some((v) => v.status === "DRAFT" || v.status === "PENDING_APPROVAL");
  return (
    <AccountPage title="Risk rule" intro={<Link href={`${RISK_BASE}/rules`}>All risk rules</Link>}>
      <RiskBanner />
      <section aria-labelledby="rule" className="space-y-3">
        <h2 id="rule" className="font-display text-2xl font-semibold text-ink-900">
          {r.name} <Badge tone="gold">TEST</Badge>
        </h2>
        <Facts items={[["Code", r.code], ["Subject", humanise(r.subject_type)], ["Category", humanise(r.category)], ["Version in force", r.active_version_id ? `v${r.versions.find((v) => v.id === r.active_version_id)?.version_no ?? "?"}` : "None"]]} />
        {r.description ? <p className="text-ink-800">{r.description}</p> : null}
      </section>
      <Section id="versions" title="Versions">
        {r.versions.length === 0 ? <p className="text-ink-700">No versions yet.</p> : null}
        <ol className="space-y-4">
          {r.versions.map((v, i) => (
            <li key={v.id} className="space-y-3 rounded-card border border-line bg-surface p-4">
              <section aria-label={`Version ${v.version_no}`} className="space-y-3">
                <h3 className="flex flex-wrap items-center gap-2 font-display text-xl font-semibold text-ink-900">
                  Version {v.version_no} <StatusBadge status={v.status} /> {v.id === r.active_version_id ? <Badge tone="brand">In force</Badge> : null}
                </h3>
                <Facts
                  items={[
                    ["Contribution", `+${v.contribution}`],
                    ["Severity", humanise(v.severity)],
                    ["Recommended action", humanise(v.recommended_action)],
                    ["Automated hold", v.hold_type ? `${humanise(v.hold_type)} for ${durationText(v.hold_duration_seconds)}` : "None"],
                    ["Case priority", v.case_priority],
                    ["Definition hash", v.definition_hash ? <Id key="h" value={v.definition_hash} /> : "Not frozen (draft)"],
                    ["Approval", v.approved_by ? `Approved: ${v.approval_reason ?? ""}` : v.rejected_by ? `Rejected: ${v.rejection_reason ?? ""}` : "—"],
                    ["Suspension", v.suspension_reason ?? "—"],
                  ]}
                />
                <details className="rounded-card border border-line p-3">
                  <summary className="cursor-pointer font-semibold text-ink-900">Definition</summary>
                  <pre className="mt-2 overflow-x-auto text-sm text-ink-900">{v.definition}</pre>
                </details>
                <div className="flex flex-wrap gap-3">
                  {ACTIONS.map((a) => {
                    const target: RiskActionTarget = a === "approve" ? { kind: "rule-approve", id: v.id, definitionHash: v.definition_hash } : { kind: `rule-${a}`, id: v.id };
                    return <RiskAction key={a} target={target} available={ruleActions(v.status).includes(a)} />;
                  })}
                </div>
                <details className="rounded-card border border-line p-3">
                  <summary className="cursor-pointer font-semibold text-ink-900">Simulate against a subject</summary>
                  <div className="mt-3">
                    <SimulateForm versionId={v.id} />
                  </div>
                </details>
                <HistoryTable items={histories[i] ?? []} caption={`Version ${v.version_no} history`} />
              </section>
            </li>
          ))}
        </ol>
      </Section>
      {draft ? (
        <Section id="edit-draft" title="Edit the draft" description="A draft is editable until it is submitted; submitting freezes and hashes it.">
          <RuleVersionForm ruleId={r.id} draft={draft} />
        </Section>
      ) : (
        <Section id="draft" title="Draft a new version" description="A new version is a draft until it is submitted, approved by a different person and activated.">
          {openDraft ? <p className="text-ink-700">A pending version already exists. Decide it first.</p> : <RuleVersionForm ruleId={r.id} />}
        </Section>
      )}
    </AccountPage>
  );
}
