import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE } from "@/components/admin/finance/common";
import { Code, Section, StatusBadge, TableShell } from "@/components/admin/payments/common";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readCheck, readException, readSource } from "@/lib/ledger/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Ledger exceptions", robots: { index: false, follow: false, nocache: true } };

/**
 * Integrity results (L1 balanced, L2 ≥ 2 lines, L4 currency, L6 duplicates, L7 reversals, L9 trial balance, L10 drift,
 * L13 non-negative, L15 environment), open reconciliation exceptions and blocked source events (`ledger.view`).
 * The checker reports; it never repairs financial history.
 */
export default async function LedgerExceptionsPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/ledger/exceptions", `${FINANCE_BASE}/exceptions`);
  const d = (typeof result.data === "object" && result.data !== null ? result.data : {}) as Record<string, unknown>;
  const checks = (Array.isArray(d.integrity) ? d.integrity : []).map(readCheck).filter((x) => x !== null);
  const open = (Array.isArray(d.open_reconciliation_exceptions) ? d.open_reconciliation_exceptions : []).map(readException).filter((x) => x !== null);
  const blocked = (Array.isArray(d.blocked_sources) ? d.blocked_sources : []).map(readSource).filter((x) => x !== null);
  return (
    <AccountPage title="Ledger exceptions" intro="Integrity checks and anything that needs a person to look at it.">
      <Section id="integrity-title" title="Integrity checks">
        {checks.length === 0 ? (
          <p className="text-ink-700">The integrity checker has not run yet.</p>
        ) : (
          <TableShell caption="Integrity checks" head={["Invariant", "Result", "Checked", "Violations", "Sample ids"]} minWidthClass="min-w-[56rem]">
            {checks.map((c) => (
              <tr key={c.code} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  <Code>{c.code}</Code> {c.name}
                </th>
                <td className="p-3">
                  <StatusBadge status={c.status} />
                </td>
                <td className="p-3">{c.checked}</td>
                <td className="p-3">{c.violations}</td>
                <td className="p-3 text-sm">{c.sample.length ? c.sample.slice(0, 3).map((s) => <Code key={s}>{s} </Code>) : "—"}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="open-title" title="Open reconciliation exceptions" description={<a href={`${FINANCE_BASE}/reconciliation`} className="text-brand-700 underline">Work them on the reconciliation page</a>}>
        <p>{open.length === 0 ? "None." : `${open.length} open exception${open.length === 1 ? "" : "s"}.`}</p>
      </Section>
      <Section id="blocked-title" title="Blocked source events" description="Source events the ledger refused to post (for example production posting disabled).">
        {blocked.length === 0 ? (
          <p className="text-ink-700">None.</p>
        ) : (
          <TableShell caption="Blocked source events" head={["Source", "Rule", "Reason"]} minWidthClass="min-w-[40rem]">
            {blocked.map((s) => (
              <tr key={s.id} className="border-b border-line last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  {s.source_system} · <Code>{s.source_id}</Code>
                </th>
                <td className="p-3">{s.posting_rule}</td>
                <td className="p-3">{humanise(s.reason_code)}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
