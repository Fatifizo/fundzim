import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { Facts, Section, StatusBadge, TableShell } from "@/components/admin/payments/common";
import { ExceptionTable, Money, ReconTabs, TestEvidenceBanner } from "@/components/admin/reconciliation/recon-view";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readLedgerView } from "@/lib/reconciliation/admin";

export const metadata: Metadata = { title: "Ledger reconciliation", robots: { index: false, follow: false, nocache: true } };

/**
 * Journal integrity (the ledger's own checks), the trial balance per currency (debits must equal credits; currencies are
 * never added together), the ledger's open reconciliation exceptions and reconciliation's journal checks of matched records.
 */
export default async function ReconciliationLedgerPage() {
  await requireStaff();
  const v = readLedgerView((await serverGetOr404<unknown>("/api/v1/admin/reconciliation/ledger", "/admin/reconciliation/ledger")).data);
  return (
    <AccountPage title="Ledger reconciliation" intro="TEST ledger. Journals are never edited; corrections are approved adjustments.">
      <TestEvidenceBanner />
      <ReconTabs current="/admin/reconciliation/ledger" />
      <Section id="posting" title="Settlement postings">
        <p className="text-ink-700">{v.settlementPosting}</p>
      </Section>
      <Section id="trial" title="Trial balance by currency">
        <TableShell caption="Trial balance by currency" head={["Currency", "Debits", "Credits", "Difference", "Balanced"]}>
          {v.trialBalance.map((t) => (
            <tr key={t.currency} className="border-b border-line last:border-0">
              <td className="p-3">{t.currency}</td>
              <td className="p-3">
                <Money minor={t.debit} currency={t.currency} />
              </td>
              <td className="p-3">
                <Money minor={t.credit} currency={t.currency} />
              </td>
              <td className="p-3">
                <Money minor={t.difference} currency={t.currency} />
              </td>
              <td className="p-3">{t.balanced ? "Yes" : <strong>No — stop and report</strong>}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="integrity" title="Journal integrity (latest run of each check)">
        <TableShell caption="Ledger integrity checks" head={["Check", "Status", "Checked", "Violations"]}>
          {v.integrity.map((c) => (
            <tr key={c.code} className="border-b border-line last:border-0">
              <td className="p-3">
                {c.code} {c.name}
              </td>
              <td className="p-3">
                <StatusBadge status={c.status} />
              </td>
              <td className="p-3">{c.checked.toLocaleString("en-GB")}</td>
              <td className="p-3">{c.violations}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="matches" title="Matched records and their journals">
        <Facts items={v.matches.length === 0 ? [["Matches", "0"]] : v.matches.map((m): [string, string] => [humanise(m.key), String(m.value)])} />
      </Section>
      <Section id="missing" title="Matched records without their journal">
        <ExceptionTable items={v.missing} caption="Missing ledger entries" />
      </Section>
      <Section id="ledger-exceptions" title="Ledger reconciliation exceptions (open)">
        {v.ledgerExceptions.length === 0 ? (
          <p className="text-ink-700">None open.</p>
        ) : (
          <TableShell caption="Open ledger reconciliation exceptions" head={["Type", "Status", "Source", "Currency"]}>
            {v.ledgerExceptions.map((x) => (
              <tr key={x.id} className="border-b border-line last:border-0">
                <td className="p-3">{humanise(x.type)}</td>
                <td className="p-3">
                  <StatusBadge status={x.status} />
                </td>
                <td className="p-3">{x.sourceType}</td>
                <td className="p-3">{x.currency ?? "—"}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
