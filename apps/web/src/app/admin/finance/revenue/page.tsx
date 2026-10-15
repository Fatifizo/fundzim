import type { Metadata } from "next";

import { FINANCE_BASE, LedgerAmount } from "@/components/admin/finance/common";
import { AccountPage } from "@/components/account/account-shell";
import { FeeBanner } from "@/components/admin/finance/fees";
import { Section, TableShell, When } from "@/components/admin/payments/common";
import { EmptyState } from "@/components/ui/empty-state";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { readRevenue } from "@/lib/fees/normalise";
import type { CurrencyRevenue } from "@/lib/fees/types";
import { humanise } from "@/lib/payments/labels";

export const metadata: Metadata = { title: "Revenue (TEST)", robots: { index: false, follow: false, nocache: true } };

const ROWS: Array<[keyof CurrencyRevenue, string]> = [
  ["quoted_platform_fees_minor", "Quoted platform fees (not revenue)"],
  ["assessed_platform_fees_minor", "Assessed platform fees"],
  ["assessed_on_test_payments_minor", "of which on TEST payments (never posted)"],
  ["assessed_on_synthetic_donations_minor", "of which on TEST_ONLY synthetic donations"],
  ["refunded_platform_fees_minor", "Platform fees reversed on refunds"],
  ["chargeback_reversed_platform_fees_minor", "Platform fees reversed on chargebacks"],
  ["manual_adjustments_net_minor", "Manual adjustments (net)"],
  ["recognised_revenue_minor", "Recognised revenue (TEST ledger)"],
  ["revenue_reversals_minor", "Revenue reversals (TEST ledger)"],
  ["provider_fees_estimated_minor", "Provider fees (estimated)"],
  ["provider_fee_expense_minor", "Provider fee expense (TEST ledger)"],
  ["payout_fees_minor", "Payout fees"],
  ["chargeback_fee_exposure_minor", "Chargeback fee exposure"],
  ["net_revenue_minor", "Net revenue (TEST ledger)"],
];

/** Restricted fee and revenue report (`fee.view`). One table per currency: currencies are never combined. */
export default async function RevenuePage() {
  await requireStaff();
  const r = readRevenue((await serverGetOr404<unknown>("/api/v1/admin/finance/revenue", `${FINANCE_BASE}/revenue`)).data);
  return (
    <AccountPage title="Revenue (TEST)" intro={<>Synthetic data, not FundZim&apos;s financial performance. From <When at={r.from} /> to <When at={r.to} />.</>}>
      <FeeBanner />
      <p className="font-semibold text-ink-900">{r.label}</p>
      {r.currencies.length === 0 ? <EmptyState title="No fee activity in this period" /> : null}
      {r.currencies.map((c) => (
        <Section key={c.currency} id={`ccy-${c.currency}`} title={`${c.currency} (no cross-currency total)`}>
          <TableShell caption={`Fees and revenue in ${c.currency}`} head={["Measure", "Amount"]} minWidthClass="min-w-[32rem]">
            {ROWS.map(([k, label]) => (
              <tr key={k} className="border-b border-line last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  {label}
                </th>
                <td className="p-3">
                  <LedgerAmount minor={String(c[k])} currency={c.currency} />
                </td>
              </tr>
            ))}
            <tr className="border-b border-line last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                Provider fees with unknown amount
              </th>
              <td className="p-3">{c.provider_fees_unknown}</td>
            </tr>
            <tr>
              <th scope="row" className="p-3 font-normal">
                Cash received
              </th>
              <td className="p-3">Not available (no settlement evidence)</td>
            </tr>
          </TableShell>
        </Section>
      ))}
      {r.by_period.length ? (
        <Section id="by-period" title="Ledger movements by month">
          <TableShell caption="TEST ledger fee movements by month and currency" head={["Month", "Currency", "Recognised", "Reversals", "Provider fee expense", "Journals"]} minWidthClass="min-w-[48rem]">
            {r.by_period.map((p) => (
              <tr key={`${p.currency}-${p.month}`} className="border-b border-line last:border-b-0">
                <th scope="row" className="p-3 font-normal">
                  {p.month}
                </th>
                <td className="p-3">{p.currency}</td>
                <td className="p-3">
                  <LedgerAmount minor={p.recognised_revenue_minor} currency={p.currency} />
                </td>
                <td className="p-3">
                  <LedgerAmount minor={p.revenue_reversals_minor} currency={p.currency} />
                </td>
                <td className="p-3">
                  <LedgerAmount minor={p.provider_fee_expense_minor} currency={p.currency} />
                </td>
                <td className="p-3">{p.journals}</td>
              </tr>
            ))}
          </TableShell>
        </Section>
      ) : null}
      {(["by_payment_method", "by_provider"] as const).map((k) =>
        r[k].length ? (
          <Section key={k} id={k} title={k === "by_payment_method" ? "Assessed platform fees by payment method" : "Assessed platform fees by provider"}>
            <TableShell caption={k === "by_payment_method" ? "Assessed platform fees by payment method" : "Assessed platform fees by provider"} head={["Currency", k === "by_payment_method" ? "Method" : "Provider", "Assessed", "Assessments"]} minWidthClass="min-w-[40rem]">
              {r[k].map((g) => (
                <tr key={`${g.currency}-${g.key}`} className="border-b border-line last:border-b-0">
                  <th scope="row" className="p-3 font-normal">
                    {g.currency}
                  </th>
                  <td className="p-3 break-all">{g.key === "NONE" ? "Not chosen" : humanise(g.key)}</td>
                  <td className="p-3">
                    <LedgerAmount minor={g.assessed_platform_fees_minor} currency={g.currency} />
                  </td>
                  <td className="p-3">{g.assessments}</td>
                </tr>
              ))}
            </TableShell>
          </Section>
        ) : null,
      )}
      {r.notes.length ? (
        <Section id="notes" title="Notes">
          <ul className="list-disc space-y-1 pl-5 text-ink-800">
            {r.notes.map((n) => (
              <li key={n}>{n}</li>
            ))}
          </ul>
        </Section>
      ) : null}
    </AccountPage>
  );
}
