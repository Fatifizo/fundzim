import Link from "next/link";

import { FINANCE_BASE, LedgerAmount } from "@/components/admin/finance/common";
import { StatusBadge, TableShell, When } from "@/components/admin/payments/common";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { humanise } from "@/lib/payments/labels";
import type { CheckResult, StaffPayout } from "@/lib/payouts/types";

export const WITHDRAWALS_BASE = `${FINANCE_BASE}/withdrawals`;
export const PAYOUTS_BASE = `${FINANCE_BASE}/payouts`;
export const withdrawalHref = (id: string) => `${WITHDRAWALS_BASE}/${encodeURIComponent(id)}`;

/** Every payout page: TEST payouts of synthetic funds through the simulator; production payouts are BLOCKED. */
export function PayoutBanner() {
  return (
    <div className="rounded-card border-2 border-gold-500/50 bg-gold-100 p-4">
      <p className="text-lg font-semibold text-ink-900">
        TEST PAYOUTS ONLY <Badge tone="gold">TEST_ONLY</Badge>
      </p>
      <p className="text-ink-800">
        Every payout here moves synthetic TEST_ONLY ledger funds through the sandbox simulator. No real money is paid out.
        Production payouts: BLOCKED.
      </p>
    </div>
  );
}

export function PayoutsTable({ items, caption, empty = "No payout requests match" }: { items: StaffPayout[]; caption: string; empty?: string }) {
  if (items.length === 0) return <EmptyState title={empty} />;
  return (
    <TableShell caption={caption} head={["Requested", "Amount", "Status", "Approval", "Destination", "Flags"]} minWidthClass="min-w-[60rem]">
      {items.map((p) => (
        <tr key={p.id} className="border-b border-line align-top last:border-b-0">
          <td className="p-3">
            <When at={p.requested_at} />
          </td>
          <th scope="row" className="p-3 font-normal">
            <Link href={withdrawalHref(p.id)} className="text-brand-700 underline">
              <LedgerAmount minor={p.amount_minor} currency={p.currency} />
            </Link>{" "}
            {p.test_payout ? <Badge tone="gold">TEST_ONLY</Badge> : null}
          </th>
          <td className="p-3">
            <StatusBadge status={p.status} />
          </td>
          <td className="p-3 text-sm">
            {humanise(p.approval_policy)} · round {p.approval_round}
          </td>
          <td className="p-3 text-sm">
            {humanise(p.destination_rail)} {p.destination_masked}
          </td>
          <td className="p-3 text-sm">{p.needs_reconciliation ? <Badge tone="gold">Needs reconciliation</Badge> : "—"}</td>
        </tr>
      ))}
    </TableShell>
  );
}

const RESULT_TONE: Record<string, "brand" | "info" | "gold" | "neutral"> = { PASS: "brand", FAIL: "gold", REVIEW: "info", DEFER: "info", NOT_APPLICABLE: "neutral" };

/** The 22 eligibility checks of one decision. */
export function ChecksTable({ checks, caption }: { checks: CheckResult[]; caption: string }) {
  return (
    <TableShell caption={caption} head={["Check", "Result", "Detail"]} minWidthClass="min-w-[36rem]">
      {checks.map((c) => (
        <tr key={c.check_id} className="border-b border-line align-top last:border-b-0">
          <th scope="row" className="p-3 font-normal">
            {c.check_id}
          </th>
          <td className="p-3">
            <Badge tone={RESULT_TONE[c.result] ?? "neutral"}>{humanise(c.result)}</Badge>
          </td>
          <td className="p-3 text-sm">{c.detail_code ? humanise(c.detail_code) : "—"}</td>
        </tr>
      ))}
    </TableShell>
  );
}
