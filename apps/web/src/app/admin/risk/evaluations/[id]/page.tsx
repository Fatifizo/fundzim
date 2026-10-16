import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, TableShell, When } from "@/components/admin/payments/common";
import { caseHref, ClassBadge, Explanation, Id, OutcomesTable, RISK_BASE, RiskBanner } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { humanise } from "@/lib/payments/labels";
import { readEvaluation } from "@/lib/risk/normalise";

export const metadata: Metadata = { title: "Risk evaluation", robots: { index: false, follow: false, nocache: true } };

/** One evaluation: score, explanation, per-rule results and the automated actions taken. */
export default async function RiskEvaluationPage({ params }: PageProps<"/admin/risk/evaluations/[id]">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  await requireStaff();
  const e = readEvaluation((await serverGetOr404<unknown>(`/api/v1/admin/risk/evaluations/${id}`, `${RISK_BASE}/evaluations/${id}`)).data);
  if (!e) notFound();
  return (
    <AccountPage title="Risk evaluation" intro={<Link href={`${RISK_BASE}/evaluations`}>All evaluations</Link>}>
      <RiskBanner />
      <p className="rounded-card border border-line bg-surface p-4 text-ink-800">{e.notice}</p>
      <Section id="score" title="Score">
        <Facts
          items={[
            ["Score", `${e.score} / 100`],
            ["Classification", <ClassBadge key="c" classification={e.classification} />],
            ["Bands", humanise(e.band_source)],
            ["Confidence", String(e.confidence)],
            ["Recommended action", humanise(e.recommended_action)],
            ["Subject", <span key="s">{humanise(e.subject_type)} <Id value={e.subject_id} /></span>],
            ["Engine", e.engine_version],
            ["Inputs hash", <Id key="h" value={e.inputs_hash} />],
            ["Evaluated", <When key="w" at={e.evaluated_at} />],
          ]}
        />
      </Section>
      <Section id="explanation" title="Why">
        <Explanation factors={e.explanation} />
      </Section>
      <Section id="results" title="Rule results">
        <OutcomesTable items={e.results} caption="Rule results" />
      </Section>
      <Section id="actions" title="Automated actions">
        {e.actions.length === 0 ? (
          <p className="text-ink-700">No action recorded.</p>
        ) : (
          <TableShell caption="Automated actions" head={["Action", "Case", "Hold"]}>
            {e.actions.map((a) => (
              <tr key={a.action_type} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-semibold">
                  {humanise(a.action_type)}
                </th>
                <td className="p-3">
                  {a.case_id ? (
                    <Link href={caseHref(a.case_id)} className="text-brand-700 underline">
                      Open case
                    </Link>
                  ) : (
                    "—"
                  )}
                </td>
                <td className="p-3">
                  <Id value={a.hold_id} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
