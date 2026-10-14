import type { Metadata } from "next";

import { AccountPage } from "@/components/account/account-shell";
import { LedgerAmount } from "@/components/admin/finance/common";
import { PAYOUTS_BASE, PayoutBanner } from "@/components/admin/finance/payouts";
import { Facts, Section, TableShell } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { requireStaff, serverGetOr404 } from "@/lib/auth/session";
import { humanise } from "@/lib/payments/labels";
import { readOverview } from "@/lib/payouts/normalise";

export const metadata: Metadata = { title: "Payouts", robots: { index: false, follow: false, nocache: true } };

/** Payout operations overview (`payout.view`). Execution gates, counts, reserved totals per currency. */
export default async function PayoutsOverviewPage() {
  await requireStaff();
  const result = await serverGetOr404<unknown>("/api/v1/admin/payouts/overview", PAYOUTS_BASE);
  const o = readOverview(result.data);
  return (
    <AccountPage title="Payouts" intro="Withdrawal orchestration: eligibility, reservations, maker-checker approval, simulator execution and reconciliation.">
      <PayoutBanner />
      <Section id="gates" title="Execution gates">
        <Facts
          items={[
            ["Execution environment", o.execution_environment],
            ["Live payout execution", o.live_execution ? <Badge key="l" tone="gold">ENABLED — report this</Badge> : "false"],
            ["Production payouts", o.production_status],
            ["Ledger environment", o.ledger_environment],
            ["Settlement reconciliation", o.settlement_reconciliation],
          ]}
        />
      </Section>
      <Section id="by-status" title="Requests by status">
        <TableShell caption="Payout requests by status" head={["Status", "Count"]} minWidthClass="min-w-[20rem]">
          {Object.entries(o.by_status).map(([k, n]) => (
            <tr key={k} className="border-b border-line last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                {humanise(k)}
              </th>
              <td className="p-3">{n}</td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="reserved" title="Reserved (TEST_ONLY)">
        <TableShell caption="Active reservations by currency" head={["Currency", "Reserved"]} minWidthClass="min-w-[20rem]">
          {Object.entries(o.active_reserved_minor_by_currency).map(([c, v]) => (
            <tr key={c} className="border-b border-line last:border-b-0">
              <th scope="row" className="p-3 font-normal">
                {c}
              </th>
              <td className="p-3">
                <LedgerAmount minor={v} currency={c} />
              </td>
            </tr>
          ))}
        </TableShell>
      </Section>
      <Section id="exceptions" title="Open exceptions">
        <Facts items={(["SEV1", "SEV2", "SEV3"] as const).map((s) => [s, String(o.open_exceptions_by_severity[s] ?? 0)])} />
      </Section>
    </AccountPage>
  );
}
