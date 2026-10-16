import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, TableShell, When } from "@/components/admin/payments/common";
import { RiskAction } from "@/components/admin/risk/risk-action";
import { caseHref, Id, RISK_BASE, RiskBanner, TestOnlyBadge } from "@/components/admin/risk/risk";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { humanise } from "@/lib/payments/labels";
import { screeningStatusLabel } from "@/lib/risk/labels";
import { readScreeningDetail } from "@/lib/risk/normalise";

export const metadata: Metadata = { title: "Screening request (TEST)", robots: { index: false, follow: false, nocache: true } };

/** One TEST screening request with every stored provider result; analyst review and retry. */
export default async function RiskScreeningRequestPage({ params }: PageProps<"/admin/risk/screening/[id]">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  await requireStaff();
  const d = readScreeningDetail((await serverGetOr404<unknown>(`/api/v1/admin/risk/screening/${id}`, `${RISK_BASE}/screening/${id}`)).data);
  if (!d) notFound();
  const s = d.request;
  const reviewable = s.status === "POTENTIAL_MATCH" || s.status === "REVIEW_REQUIRED";
  return (
    <AccountPage title="Screening request (TEST)" intro={<Link href={`${RISK_BASE}/screening`}>All screening requests</Link>}>
      <RiskBanner />
      <section aria-labelledby="request" className="space-y-3">
        <h2 id="request" className="flex flex-wrap items-center gap-2 font-display text-2xl font-semibold text-ink-900">
          {s.synthetic_reference} <TestOnlyBadge />
        </h2>
        <Facts
          items={[
            ["Status", screeningStatusLabel(s.status)],
            ["Subject", <span key="s">{humanise(s.subject_type)} <Id value={s.subject_id} /></span>],
            ["Provider", `${s.provider_code} (${s.environment} simulator)`],
            ["Provider reference", <Id key="r" value={s.provider_reference} />],
            ["Potential matches", String(s.match_count)],
            ["Last error", s.last_error ? `${humanise(s.last_error)} (not cleared)` : "—"],
            ["Analyst review", s.reviewed_by ? `${screeningStatusLabel(s.status)}: ${s.review_reason ?? ""}` : "—"],
            ["Case", s.case_id ? <Link key="c" href={caseHref(s.case_id)} className="text-brand-700 underline">Open case</Link> : "—"],
            ["Requested", <When key="w" at={s.created_at} />],
          ]}
        />
      </section>
      <Section id="review" title="Analyst review">
        <p className="text-ink-700">A potential match is not a finding about anyone. You cannot review a screening you requested.</p>
        <div className="flex flex-wrap gap-3">
          <RiskAction target={{ kind: "screening-confirm", id: s.id }} available={reviewable} />
          <RiskAction target={{ kind: "screening-clear", id: s.id }} available={reviewable} />
          <RiskAction target={{ kind: "screening-retry", id: s.id }} available={s.status === "ERROR"} />
        </div>
      </Section>
      <Section id="results" title="Provider results (as received)">
        {d.results.length === 0 ? (
          <p className="text-ink-700">No provider result yet.</p>
        ) : (
          <TableShell caption="Provider results" head={["Event", "Source", "Sequence", "Reported", "Matches", "Applied", "Received"]} minWidthClass="min-w-[60rem]">
            {d.results.map((r) => (
              <tr key={r.provider_event_id} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  <Id value={r.provider_event_id} /> <TestOnlyBadge />
                </th>
                <td className="p-3">{humanise(r.source)}</td>
                <td className="p-3">{r.sequence}</td>
                <td className="p-3">{screeningStatusLabel(r.reported_status)}</td>
                <td className="p-3 text-sm">{r.matches.map((m) => `${m.match_id} (${m.list}, score ${m.score})`).join("; ") || "—"}</td>
                <td className="p-3">{r.applied ? "Applied" : "Stored, not applied"}</td>
                <td className="p-3 text-sm">
                  <When at={r.received_at} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
