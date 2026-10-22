import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { Code, Facts, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { ExceptionTable, LineTable, Money, ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readImportDetail } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Statement import", robots: { index: false, follow: false, nocache: true } };

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** One statement import: its evidence (hash, evidence record), validation report, lines with their matches, exceptions. */
export default async function ReconciliationImportPage({ params }: PageProps<"/admin/reconciliation/batches/[id]">) {
  const { id } = await params;
  if (!UUID.test(id)) notFound();
  await requireStaff();
  const d = readImportDetail((await serverGetOr404<unknown>(`/api/v1/admin/reconciliation/imports/${id}?limit=500`, `/admin/reconciliation/batches/${id}`)).data);
  const i = d.imp;
  return (
    <AccountPage title={`Statement ${i.statementRef ?? "(not parsed)"}`} intro={`${i.sourceCode} · ${humanise(i.origin)}`}>
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/batches" />
      <Section id="evidence" title="Evidence">
        <Facts items={[
          ["Status", <StatusBadge key="s" status={i.status} />],
          ["Failure", i.failureCode ? humanise(i.failureCode) : "—"],
          ["SHA-256 of the file", <Code key="h">{i.sha256}</Code>],
          ["Evidence record", <Code key="e">{i.evidenceRecordId}</Code>],
          ["File", `${i.filename ?? "—"} · ${i.mediaType} · ${i.sizeBytes.toLocaleString("en-GB")} bytes`],
          ["Period", i.periodStart ? <span key="p"><When at={i.periodStart} /> to <When at={i.periodEnd} /></span> : "—"],
          ["Currency", i.currency ?? "per line"],
          ["Opening / closing balance", i.openingBalance ? <span key="b"><Money minor={i.openingBalance} currency={i.currency} /> / <Money minor={i.closingBalance} currency={i.currency} /></span> : "not reported"],
          ["Account", i.accountHint ? `…${i.accountHint} (the number is never stored)` : "—"],
          ["Lines declared / recorded", `${i.declaredLines ?? "—"} / ${i.parsed}`],
          ["Duplicates of lines already recorded", String(i.duplicate)],
          ["Lines that conflict with earlier evidence", String(i.conflicting)],
          ["Supersedes", i.supersedes ? <Code key="x">{i.supersedes}</Code> : "—"],
          ["Received", <When key="r" at={i.receivedAt} />],
        ]} />
      </Section>
      {i.report.length > 0 ? (
        <Section id="validation" title="Validation report" description="The file was refused as a whole. Nothing from it was recorded.">
          <TableShell caption="Validation errors" head={["Where", "Field", "Problem"]}>
            {i.report.map((e, n) => (
              <tr key={n} className="border-b border-line last:border-0">
                <td className="p-3">
                  <Code>{e.locator}</Code>
                </td>
                <td className="p-3">{e.field || "—"}</td>
                <td className="p-3">{humanise(e.code)}</td>
              </tr>
            ))}
          </TableShell>
        </Section>
      ) : null}
      <Section id="lines" title="Lines">
        <LineTable lines={d.lines} caption="Statement lines" />
      </Section>
      <Section id="exceptions" title="Exceptions on these lines">
        <ExceptionTable items={d.exceptions} caption="Exceptions on this statement" />
      </Section>
    </AccountPage>
  );
}
