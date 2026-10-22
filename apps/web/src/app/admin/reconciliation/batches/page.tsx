import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { ShortId } from "@/components/admin/operations/ops";
import { Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { ReplayAction, RunRequestForm, SandboxStatementForm, StatementUploadForm } from "@/components/admin/reconciliation/recon-admin";
import { ReconTabs, RunTable, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readImports, readRuns } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Reconciliation imports and runs", robots: { index: false, follow: false, nocache: true } };

/** Statement imports (with the upload and simulator forms) and matching runs (with on-demand runs and replays). */
export default async function ReconciliationBatchesPage() {
  await requireStaff();
  const [imports, runs] = await Promise.all([
    serverGetOr404<unknown>("/api/v1/admin/reconciliation/imports?limit=50", "/admin/reconciliation/batches"),
    serverGetOr404<unknown>("/api/v1/admin/reconciliation/runs?limit=30", "/admin/reconciliation/batches"),
  ]);
  const list = readImports(imports.data);
  return (
    <AccountPage title="Imports and runs" intro="TEST statements in FundZim's canonical format, and the matching runs that decide their lines.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/batches" />
      <Section id="imports" title="Statement imports" description="Each file is identified by its SHA-256, never its name. Parsing is all or nothing.">
        {list.length === 0 ? (
          <p className="text-ink-700">No statement has been imported yet.</p>
        ) : (
          <TableShell caption="Statement imports" head={["Received", "Source", "Statement", "Status", "Lines", "Duplicates", "Conflicts", "Origin", "File hash"]} minWidthClass="min-w-[60rem]">
            {list.map((i) => (
              <tr key={i.id} className="border-b border-line last:border-0">
                <td className="p-3">
                  <Link href={`/admin/reconciliation/batches/${i.id}`} className="font-semibold text-brand-700 underline">
                    <When at={i.receivedAt} />
                  </Link>
                </td>
                <td className="p-3">{i.sourceCode}</td>
                <td className="p-3">{i.statementRef ?? "—"}</td>
                <td className="p-3">
                  <StatusBadge status={i.status} />
                  {i.failureCode ? <span className="block text-sm text-ink-600">{humanise(i.failureCode)}</span> : null}
                </td>
                <td className="p-3">{i.parsed}</td>
                <td className="p-3">{i.duplicate}</td>
                <td className="p-3">{i.conflicting}</td>
                <td className="p-3">{humanise(i.origin)}</td>
                <td className="p-3">
                  <ShortId value={i.sha256} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="upload" title="Import a TEST statement" description="Format: docs/stage-17/settlement-management.md (canonical CSV or JSON). Amounts are whole minor units; every line needs a reference.">
        <StatementUploadForm />
      </Section>
      <Section id="sandbox" title="Generate a TEST statement from the simulator" description="The simulator describes its own payments and payouts; it reports no fees.">
        <SandboxStatementForm />
      </Section>
      <Section id="runs" title="Matching runs" description="One active run per source and currency. A run never posts to the ledger and never changes a payment, payout or reservation.">
        <RunTable runs={readRuns(runs.data)} caption="Reconciliation runs" actions={(r) => <ReplayAction runId={r.id} />} />
      </Section>
      <Section id="run" title="Run now">
        <RunRequestForm />
      </Section>
    </AccountPage>
  );
}
