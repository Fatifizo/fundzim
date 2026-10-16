import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, TableShell, When } from "@/components/admin/payments/common";
import { RiskAction } from "@/components/admin/risk/risk-action";
import { EvidenceForm } from "@/components/admin/risk/risk-forms";
import { caseFacts, EvidenceTable, HistoryTable, HoldsTable, Id, RISK_BASE, RiskBanner } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { humanise } from "@/lib/payments/labels";
import { canEscalate, canPropose, caseIsOpen, caseTransitions, holdActions, short } from "@/lib/risk/labels";
import { readCaseDetail } from "@/lib/risk/normalise";
import { NoPermission } from "@/components/admin/operations/ops";
import { can } from "@/lib/operations/navigation";
import { RISK_FORM_PERMISSION, mayRisk } from "@/lib/risk/permissions";

export const metadata: Metadata = { title: "Risk case", robots: { index: false, follow: false, nocache: true } };

const HOLD_ACTIONS = ["activate", "revoke", "review", "request-release", "approve-release", "reject-release"] as const;

/** The investigation workspace: case, immutable evidence, assignments, signal reviews, holds and history. Viewing is logged. */
export default async function RiskCasePage({ params }: PageProps<"/admin/risk/cases/[id]">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const me = await requireStaff();
  const d = readCaseDetail((await serverGetOr404<unknown>(`/api/v1/admin/risk/cases/${id}`, `${RISK_BASE}/cases/${id}`)).data);
  if (!d) notFound();
  const c = d.case;
  const open = caseIsOpen(c.status);
  const reviewed = new Set(d.signal_reviews.map((r) => r.signal_id));
  return (
    <AccountPage title="Risk case" intro={<Link href={`${RISK_BASE}/cases`}>All risk cases</Link>}>
      <RiskBanner />
      <section aria-labelledby="case" className="space-y-3">
        <h2 id="case" className="font-display text-2xl font-semibold text-ink-900">
          Case {c.case_number}
        </h2>
        <Facts items={caseFacts(c)} />
        <p className="text-sm text-ink-700">Your view of this case is recorded in its access log.</p>
      </section>
      <Section id="actions" title="Actions">
        <div className="flex flex-wrap gap-3">
          <RiskAction target={{ kind: "case-assign", id: c.id }} available={(open) && mayRisk(me.permissions, "case-assign")} />
          <RiskAction target={{ kind: "case-transition", id: c.id, options: caseTransitions(c.status) }} available={(caseTransitions(c.status).length > 0) && mayRisk(me.permissions, "case-transition")} />
          <RiskAction target={{ kind: "case-escalate", id: c.id }} available={(canEscalate(c.status)) && mayRisk(me.permissions, "case-escalate")} />
          <RiskAction target={{ kind: "case-propose", id: c.id }} available={(canPropose(c.status)) && mayRisk(me.permissions, "case-propose")} />
          <RiskAction target={{ kind: "case-approve", id: c.id }} available={(c.status === "DECISION_PENDING") && mayRisk(me.permissions, "case-approve")} />
          <RiskAction target={{ kind: "case-return", id: c.id }} available={(c.status === "DECISION_PENDING") && mayRisk(me.permissions, "case-return")} />
          <RiskAction target={{ kind: "case-reopen", id: c.id }} available={(!open) && mayRisk(me.permissions, "case-reopen")} />
        </div>
      </Section>
      <Section id="evidence" title="Evidence" description="Evidence is immutable and content-hashed. A correction is a new record that supersedes the original.">
        <EvidenceTable
          items={d.evidence}
          caption="Case evidence"
          actions={(e) =>
            e.evidence_type === "SIGNAL" && e.reference_id ? (
              <RiskAction target={{ kind: "case-review-signal", id: c.id, signalId: e.reference_id }} available={(c.status !== "CLOSED" && !reviewed.has(e.reference_id)) && mayRisk(me.permissions, "case-review-signal")} />
            ) : null
          }
        />
      </Section>
      {c.status !== "CLOSED" ? (
        <Section id="add-evidence" title="Add a note or evidence reference">
          {can(me.permissions, RISK_FORM_PERMISSION.evidence) ? <EvidenceForm caseId={c.id} existing={d.evidence.filter((e) => !e.superseded_by).map((e) => ({ id: e.id, label: `${humanise(e.evidence_type)} ${short(e.id)}` }))} /> : <NoPermission action="Adding evidence" permission={RISK_FORM_PERMISSION.evidence} />}
        </Section>
      ) : null}
      <Section id="reviews" title="Signal reviews">
        {d.signal_reviews.length === 0 ? (
          <p className="text-ink-700">No signal reviewed yet.</p>
        ) : (
          <TableShell caption="Signal reviews" head={["Signal", "Classification", "Explanation", "Reviewed"]}>
            {d.signal_reviews.map((r) => (
              <tr key={r.id} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  <Id value={r.signal_id} />
                </th>
                <td className="p-3">{humanise(r.classification)}</td>
                <td className="p-3 text-sm">{r.explanation}</td>
                <td className="p-3 text-sm">
                  <When at={r.reviewed_at} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="holds" title="Holds">
        <HoldsTable
          items={d.holds}
          caption="Case holds"
          actions={(h) => HOLD_ACTIONS.map((a) => <RiskAction key={a} target={{ kind: `hold-${a}`, id: h.id }} available={holdActions(h.status).includes(a) && mayRisk(me.permissions, `hold-${a}`)} />)}
        />
      </Section>
      <Section id="assignments" title="Assignments">
        {d.assignments.length === 0 ? (
          <p className="text-ink-700">Never assigned.</p>
        ) : (
          <ul className="list-disc space-y-1 pl-5 text-ink-800">
            {d.assignments.map((a, i) => (
              <li key={`${a.assigned_to}-${i}`}>
                <Id value={a.assigned_to} /> — {a.reason} (<When at={a.assigned_at} />)
              </li>
            ))}
          </ul>
        )}
      </Section>
      <Section id="history" title="History">
        <HistoryTable items={d.events} caption="Case history" />
      </Section>
    </AccountPage>
  );
}
