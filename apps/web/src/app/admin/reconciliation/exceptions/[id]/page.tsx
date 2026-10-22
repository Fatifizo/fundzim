import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { ShortId } from "@/components/admin/operations/ops";
import { Code, Facts, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { ExceptionActions, ProposalForm, ResolutionActions } from "@/components/admin/reconciliation/recon-admin";
import { LineTable, Money, ReconTabs, Severity, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readException } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Reconciliation exception", robots: { index: false, follow: false, nocache: true } };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/**
 * One exception: what was compared, the statement line and its match, the append-only history, resolutions with their
 * proposal hash, and the actions the API allows. Nothing here changes money until a second person approves.
 */
export default async function ReconciliationExceptionPage({ params }: PageProps<"/admin/reconciliation/exceptions/[id]">) {
  const { id } = await params;
  if (!UUID.test(id)) notFound();
  await requireStaff();
  const raw = (await serverGetOr404<unknown>(`/api/v1/admin/reconciliation/exceptions/${id}`, `/admin/reconciliation/exceptions/${id}`)).data;
  const e = readException(typeof raw === "object" && raw !== null ? (raw as Record<string, unknown>) : {});
  const pending = e.resolutions.find((r) => r.status === "PENDING_REVIEW" || r.status === "APPROVED");
  return (
    <AccountPage title={humanise(e.type)} intro={`${e.sourceCode} · ${e.currency} · seen ${e.occurrences} time(s)`}>
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/exceptions" />
      <Section id="summary" title="Summary">
        <Facts items={[
          ["Severity", <Severity key="v" severity={e.severity} />],
          ["Status", <StatusBadge key="s" status={e.status} />],
          ["Expected", <Money key="x" minor={e.expected} currency={e.currency} />],
          ["Reported", <Money key="a" minor={e.actual} currency={e.rawCurrency ?? e.currency} />],
          ["Record", e.subjectId ? <span key="r">{humanise(e.subjectType ?? "")} <ShortId value={e.subjectId} /></span> : "—"],
          ["Settlement batch", e.batchId ? <Link key="b" href={`/admin/reconciliation/settlements/${e.batchId}`} className="text-brand-700 underline"><ShortId value={e.batchId} /></Link> : "—"],
          ["Detected", <When key="d" at={e.detectedAt} />],
          ["Assigned to", e.assignedTo ? <ShortId key="t" value={e.assignedTo} /> : "nobody"],
          ["Due", e.dueAt ? <When key="u" at={e.dueAt} /> : "no due date (no service level is configured)"],
          ["Escalated", e.escalatedAt ? <When key="z" at={e.escalatedAt} /> : "—"],
        ]} />
        {e.detail.length > 0 ? (
          <Facts items={e.detail.map(([k, v]): [string, string] => [humanise(k), v])} />
        ) : null}
        <ExceptionActions exc={e} />
      </Section>
      {e.line ? (
        <Section id="line" title="Statement line">
          <LineTable lines={[e.line]} caption="The statement line" />
          {e.line.match ? <Facts items={e.line.match.explanation.map(([k, v]): [string, string] => [humanise(k), v])} /> : null}
        </Section>
      ) : null}
      <Section id="resolutions" title="Resolutions" description="Proposed by one person, approved by another (never the proposer or a linked account) against the exact proposal hash, within 24 hours.">
        {e.resolutions.length === 0 ? <p className="text-ink-700">No resolution yet.</p> : null}
        {e.resolutions.map((r) => (
          <div key={r.id} className="space-y-3 rounded-card border border-line bg-surface p-4">
            <Facts items={[
              ["Resolution", humanise(r.type)],
              ["Status", <StatusBadge key="s" status={r.status} />],
              ["Justification", r.justification],
              ["Evidence", r.evidenceRefs.join(", ")],
              ["Proposed", <span key="p"><When at={r.proposedAt} /> by <ShortId value={r.proposedBy} /></span>],
              ["Expires", <When key="x" at={r.expiresAt} />],
              ["Decision", r.decidedBy ? <span key="d"><ShortId value={r.decidedBy} /> — {r.decisionNote}</span> : "—"],
              ["Proposal hash", <Code key="h">{r.sha256}</Code>],
              ["Ledger adjustment", r.ledgerAdjustmentId ? <Code key="l">{r.ledgerAdjustmentId}</Code> : "—"],
              ["Result", r.result.length > 0 ? r.result.map(([k, v]) => `${humanise(k)}: ${v}`).join("; ") : r.lastError ? `not applied yet (${r.lastError})` : "—"],
            ]} />
            {r.proposal.length > 0 ? <Facts items={r.proposal.map(([k, v]): [string, string] => [humanise(k), v])} /> : null}
            <ResolutionActions res={r} />
          </div>
        ))}
        {(e.status === "INVESTIGATING" && !pending) || e.status === "PENDING_APPROVAL" ? (
          // Stays mounted after a proposal (the refresh moves the exception to PENDING_APPROVAL) so its confirmation remains.
          <div className="space-y-3">
            <ProposalForm exc={e} open={e.status === "INVESTIGATING" && !pending} />
          </div>
        ) : null}
      </Section>
      <Section id="history" title="History" description="Append-only.">
        <TableShell caption="Exception history" head={["When", "Event", "Status", "By", "Note", "Evidence"]}>
          {e.events.map((ev) => (
            <tr key={ev.id} className="border-b border-line align-top last:border-0">
              <td className="p-3">
                <When at={ev.at} />
              </td>
              <td className="p-3">{humanise(ev.type)}</td>
              <td className="p-3">{ev.to ? `${humanise(ev.from ?? "")} → ${humanise(ev.to)}` : "—"}</td>
              <td className="p-3">{ev.actorId ? <ShortId value={ev.actorId} /> : "System"}</td>
              <td className="p-3">{ev.note ?? "—"}</td>
              <td className="p-3">{ev.evidenceRef ? <Code>{ev.evidenceRef}</Code> : "—"}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
    </AccountPage>
  );
}
