import type { Metadata } from "next";
import Link from "next/link";

import { AccountPage } from "@/components/account/account-shell";
import { FINANCE_BASE, LedgerAmount, LedgerEnvironmentBanner } from "@/components/admin/finance/common";
import { LedgerAction } from "@/components/admin/finance/ledger-action";
import { Code, Section, StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { readAdjustment, readOverview, readPage } from "@/lib/ledger/normalise";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Ledger", robots: { index: false, follow: false, nocache: true } };

/**
 * Ledger overview (`ledger.view`): environment, journal/account counts, per-currency trial balance totals (never
 * summed across currencies), latest integrity results, open periods (maker-checker close) and pending adjustments
 * (checker approval). Never a withdrawable balance.
 */
export default async function LedgerOverviewPage() {
  await requireStaff();
  const path = `${FINANCE_BASE}/ledger`;
  const [result, adjRaw] = await Promise.all([
    serverGetOr404<unknown>("/api/v1/admin/ledger/overview", path),
    serverGetOptional<unknown>("/api/v1/admin/ledger/adjustments?status=PENDING_APPROVAL", path, null),
  ]);
  const o = readOverview(result.data);
  const pending = adjRaw !== null ? readPage(adjRaw, readAdjustment).items : [];
  return (
    <AccountPage title="Ledger" intro="The double-entry ledger at a glance. Balances are derived from posted journals only.">
      <LedgerEnvironmentBanner environment={o.environment} notice={o.notice} />
      {o.production_posting_enabled ? <p className="font-semibold text-danger-700">The API reports production posting as enabled — check the gates.</p> : null}
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[
          ["Journals", o.journals, `${FINANCE_BASE}/journals`],
          ["Accounts", o.accounts, `${FINANCE_BASE}/accounts`],
          ["Open reconciliation exceptions", o.open_exceptions, `${FINANCE_BASE}/reconciliation`],
          ["Adjustments awaiting approval", o.pending_adjustments, "#adjustments-title"],
        ].map(([label, value, href]) => (
          <div key={String(label)} className="rounded-card border border-line bg-surface p-4">
            <dt className="text-sm font-semibold text-ink-600">{label}</dt>
            <dd className="font-display text-3xl font-bold text-ink-900">
              <Link href={String(href)} className="text-brand-700 underline">
                {value}
              </Link>
            </dd>
          </div>
        ))}
      </dl>
      <Section id="tb-title" title="Trial balance totals" description="Per currency. Debits must equal credits.">
        {o.trial_balance.length === 0 ? (
          <p className="text-ink-700">No journals posted yet.</p>
        ) : (
          <TableShell caption="Trial balance totals" head={["Currency", "Total debits", "Total credits", "Difference", "Balanced"]} minWidthClass="min-w-[36rem]">
            {o.trial_balance.map((c) => (
              <tr key={c.currency} className="border-b border-line last:border-b-0">
                <th scope="row" className="p-3 font-semibold">
                  <Link href={`${FINANCE_BASE}/trial-balance?currency=${encodeURIComponent(c.currency)}`} className="text-brand-700 underline">
                    {c.currency}
                  </Link>
                </th>
                <td className="p-3">
                  <LedgerAmount minor={c.total_debit_minor} currency={c.currency} />
                </td>
                <td className="p-3">
                  <LedgerAmount minor={c.total_credit_minor} currency={c.currency} />
                </td>
                <td className="p-3">
                  <LedgerAmount minor={c.difference_minor} currency={c.currency} />
                </td>
                <td className="p-3">{c.balanced ? <StatusBadge status="PASSED" /> : <StatusBadge status="FAILED" />}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="integrity-title" title="Integrity" description={<Link href={`${FINANCE_BASE}/exceptions`} className="text-brand-700 underline">All integrity results and exceptions</Link>}>
        {o.integrity.length === 0 ? (
          <p className="text-ink-700">The integrity checker has not run yet.</p>
        ) : (
          <TableShell caption="Latest integrity results" head={["Invariant", "Result", "Checked", "Violations"]} minWidthClass="min-w-[36rem]">
            {o.integrity.map((c) => (
              <tr key={c.code} className="border-b border-line last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  <Code>{c.code}</Code> {c.name}
                </th>
                <td className="p-3">
                  <StatusBadge status={c.status} />
                </td>
                <td className="p-3">{c.checked}</td>
                <td className="p-3">{c.violations}</td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="periods-title" title="Open accounting periods" description="Closing is maker-checker: one person requests, a different person approves.">
        {o.open_periods.length === 0 ? (
          <p className="text-ink-700">No open periods.</p>
        ) : (
          <TableShell caption="Open periods" head={["Period", "Status", "Ends", "Actions"]} minWidthClass="min-w-[40rem]">
            {o.open_periods.map((p) => (
              <tr key={p.id} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-semibold">
                  {p.code}
                </th>
                <td className="p-3">
                  <StatusBadge status={p.status} />
                </td>
                <td className="p-3">
                  <When at={p.ends_at} />
                </td>
                <td className="space-y-2 p-3">
                  {/* always mounted: the confirmation notice must survive the refresh that changes the status */}
                  <LedgerAction target={{ kind: "period", id: p.id, action: "request-close" }} available={p.status === "OPEN"} />
                  <LedgerAction target={{ kind: "period", id: p.id, action: "approve-close" }} available={p.status === "CLOSING"} />
                  <LedgerAction target={{ kind: "period", id: p.id, action: "abort-close" }} available={p.status === "CLOSING"} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
      <Section id="adjustments-title" title="Adjustments awaiting approval" description="Reversals and corrections requested by a maker. You cannot approve your own request.">
        {pending.length === 0 ? (
          <p className="text-ink-700">Nothing awaits approval.</p>
        ) : (
          <TableShell caption="Adjustments awaiting approval" head={["Request", "Target", "Reason", "Expires", "Actions"]} minWidthClass="min-w-[52rem]">
            {pending.map((a) => (
              <tr key={a.id} className="border-b border-line align-top last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  {humanise(a.kind)} · {a.posting_rule}
                  <span className="block text-sm">
                    <Code>{a.id}</Code>
                  </span>
                </th>
                <td className="p-3">
                  {a.target_transaction_id ? (
                    <Link href={`${FINANCE_BASE}/journals/${encodeURIComponent(a.target_transaction_id)}`} className="text-brand-700 underline">
                      Journal
                    </Link>
                  ) : (
                    "—"
                  )}
                </td>
                <td className="p-3 text-sm">
                  {humanise(a.reason_code)}
                  <span className="block">{a.reason}</span>
                </td>
                <td className="p-3">
                  <When at={a.expires_at} />
                </td>
                <td className="space-y-2 p-3">
                  <LedgerAction target={{ kind: "adjustment", id: a.id, action: "approve" }} />
                  <LedgerAction target={{ kind: "adjustment", id: a.id, action: "reject" }} />
                  <LedgerAction target={{ kind: "adjustment", id: a.id, action: "withdraw" }} />
                </td>
              </tr>
            ))}
          </TableShell>
        )}
      </Section>
    </AccountPage>
  );
}
