import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { HealthBadge } from "@/components/admin/operations/ops";
import { Facts, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { ReconTabs, RunTable, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readOverview, type Count } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Reconciliation", robots: { index: false, follow: false, nocache: true } };

const facts = (c: Count[], empty: string): Array<[string, string]> => (c.length === 0 ? [[empty, "0"]] : c.map((x) => [humanise(x.key), x.value.toLocaleString("en-GB")]));

/**
 * Reconciliation dashboard (`reconciliation.view`, Stage 17): sources and freshness, imports, statement lines per currency,
 * open exceptions, approvals waiting, settlement batches, unknown-outcome evidence, worker heartbeats, recent runs and
 * the production gates. Every figure is TEST evidence; currencies are never added together.
 */
export default async function ReconciliationPage() {
  await requireStaff();
  const o = readOverview((await serverGetOr404<unknown>("/api/v1/admin/reconciliation/overview", "/admin/reconciliation")).data);
  return (
    <AccountPage title="Reconciliation" intro="Statements matched against payments, payouts, fees and the ledger. TEST evidence only.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation" />
      <Section id="gates" title="Production status">
        <Facts items={o.production.map(([k, v]): [string, string] => [humanise(k), humanise(v.split(":")[0] ?? v) + (v.includes(":") ? `: ${v.split(":").slice(1).join(":").trim()}` : "")])} />
        <p className="text-ink-700">
          Matching rule set:{" "}
          {o.ruleSet ? (
            <>
              version {o.ruleSet.version} (date window {o.ruleSet.dateWindowDays} days, settlement horizon {o.ruleSet.settlementHorizonDays} days)
            </>
          ) : (
            <strong>none active — matching is stopped until two people approve one.</strong>
          )}{" "}
          <Link href="/admin/reconciliation/rules" className="font-semibold text-brand-700 underline">
            Matching rules
          </Link>
        </p>
      </Section>
      <Section id="exceptions" title="Open exceptions">
        <Facts items={[...facts(o.bySeverity, "Open exceptions"), ["Waiting for a checker", String(o.pendingResolutions)],
          ["Statement evidence for unknown outcomes", String(o.unknownOutcomes)], ["Oldest open", o.oldestOpenHours == null ? "—" : `${o.oldestOpenHours} hours`]]} />
        {o.byType.length > 0 ? <Facts items={facts(o.byType, "")} /> : null}
        <p>
          <Link href="/admin/reconciliation/exceptions?open=true" className="font-semibold text-brand-700 underline">
            Work the exception queue
          </Link>
        </p>
      </Section>
      <Section id="sources" title="Sources" description="Freshness is “never” until a statement from the source has been parsed; it is not judged without an active rule set.">
        <TableShell caption="Statement sources" head={["Source", "Kind", "Authority", "Last statement parsed", "Last completed run", "Open lines", "Freshness"]}>
          {o.sources.map((s) => (
            <tr key={s.code} className="border-b border-line last:border-0">
              <td className="p-3">{s.code}</td>
              <td className="p-3">{humanise(s.kind)}</td>
              <td className="p-3">{s.rank}</td>
              <td className="p-3">
                <When at={s.lastParsedAt} />
              </td>
              <td className="p-3">
                <When at={s.lastRunAt} />
              </td>
              <td className="p-3">{s.openLines}</td>
              <td className="p-3">
                <StatusBadge status={s.freshness} />
              </td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="volumes" title="Imports, lines and settlement batches">
        <Facts items={[...facts(o.imports, "Imports"), ...facts(o.lines, "Lines"), ...facts(o.batches, "Settlement batches")]} />
      </Section>
      <Section id="workers" title="Workers" description="A job that has never succeeded is unknown, never healthy.">
        <TableShell caption="Reconciliation workers" head={["Job", "Status", "Last success", "Last failure", "Failures in a row"]}>
          {o.workers.map((w) => (
            <tr key={w.job} className="border-b border-line last:border-0">
              <td className="p-3">{w.job}</td>
              <td className="p-3">
                <HealthBadge status={w.status} />
              </td>
              <td className="p-3">
                <When at={w.lastSucceededAt} />
              </td>
              <td className="p-3">
                <When at={w.lastFailedAt} /> {w.lastError ? `(${humanise(w.lastError)})` : null}
              </td>
              <td className="p-3">{w.consecutiveFailures}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="runs" title="Recent runs">
        <RunTable runs={o.recentRuns} caption="Recent reconciliation runs" />
      </Section>
    </AccountPage>
  );
}
