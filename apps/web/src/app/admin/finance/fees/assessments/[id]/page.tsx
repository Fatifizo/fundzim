import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { LedgerAmount } from "@/components/admin/finance/common";
import { AdjustmentForm } from "@/components/admin/finance/fee-forms";
import { AdjustmentsTable, BasisBadge, FEES_BASE, FeeAmount, FeeBanner } from "@/components/admin/finance/fees";
import { Code, Facts, Section, TableShell, When } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { responsibilityLabel } from "@/lib/fees/labels";
import { readAssessmentDetail } from "@/lib/fees/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Fee assessment", robots: { index: false, follow: false, nocache: true } };

/** One fee assessment: its journals, adjustments, history, and a manual adjustment request (maker-checker). */
export default async function FeeAssessmentPage({ params }: PageProps<"/admin/finance/fees/assessments/[id]">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  await requireStaff();
  const d = readAssessmentDetail((await serverGetOr404<unknown>(`/api/v1/admin/fees/assessments/${id}`, `${FEES_BASE}/assessments/${id}`)).data);
  if (!d) notFound();
  const a = d.assessment;
  return (
    <AccountPage title="Fee assessment" intro={<Link href={`${FEES_BASE}/assessments`}>All fee assessments</Link>}>
      <FeeBanner />
      <section aria-labelledby="assessment" className="space-y-3">
        <h2 id="assessment" className="font-display text-2xl font-semibold text-ink-900">
          {humanise(a.category)}: <FeeAmount minor={a.fee_minor} currency={a.currency} /> <BasisBadge basis={a.basis} />
        </h2>
        <Facts
          items={[
            ["Source", `${humanise(a.source_type)} ${a.source_id}`],
            ["On", <LedgerAmount key="b" minor={a.basis_minor} currency={a.currency} />],
            ["After adjustments", <FeeAmount key="n" minor={a.net_fee_minor} currency={a.currency} />],
            ["Who bears it", responsibilityLabel(a.responsibility)],
            ["Accounting", humanise(a.accounting_status)],
            ["Policy version", a.policy_version_id ? <Code key="p">{a.policy_version_id}</Code> : "—"],
            ["Assessed", <When key="w" at={a.assessed_at} />],
            ["Label", a.synthetic ? "TEST_ONLY synthetic donation" : "TEST payment (never posted)"],
          ]}
        />
      </section>
      <Section id="journals" title="Ledger journals">
        {d.journals.length === 0 ? (
          <p className="text-ink-700">No journal carries this fee (test payments are never posted).</p>
        ) : (
          <TableShell caption="Fee journals" head={["Purpose", "Rule", "Fee", "Journal key"]} minWidthClass="min-w-[48rem]">
            {d.journals.map((j) => (
              <tr key={j.id} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  {humanise(j.purpose)}
                </th>
                <td className="p-3 text-sm">{j.posting_rule}</td>
                <td className="p-3">
                  <LedgerAmount minor={j.fee_amount_minor} currency={j.currency} />
                </td>
                <td className="p-3 text-sm break-all">{j.journal_key}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="adjustments" title="Adjustments">
        <AdjustmentsTable items={d.adjustments} caption="Adjustments of this fee" />
      </Section>
      <Section id="request" title="Request a manual adjustment" description="Refunds and chargebacks are computed from the policy. A manual correction needs a different person's approval and never posts.">
        {a.fee_minor === null ? <p className="text-ink-700">This fee is UNKNOWN and cannot be adjusted.</p> : <AdjustmentForm assessmentId={a.id} currency={a.currency} />}
      </Section>
      <Section id="history" title="History">
        <ol className="space-y-1 text-ink-800">
          {d.events.map((e) => (
            <li key={e.id}>
              <When at={e.occurred_at} /> — {humanise(e.event_type)} ({e.actor_type.toLowerCase()})
            </li>
          ))}
        </ol>
      </Section>
    </AccountPage>
  );
}
