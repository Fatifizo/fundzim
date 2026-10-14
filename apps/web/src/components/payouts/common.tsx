import Link from "next/link";

import { LedgerAmount } from "@/components/admin/finance/common";
import { formatDateTime } from "@/components/account/format";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import type { OwnerPayout } from "@/lib/payouts/types";

/** Shown on every organiser withdrawal page: test balances only, production payouts blocked. */
export function TestFundsBanner() {
  return (
    <div className="rounded-card border-2 border-gold-500/50 bg-gold-100 p-4">
      <p className="text-lg font-semibold text-ink-900">
        Test withdrawals only <Badge tone="gold">TEST_ONLY</Badge>
      </p>
      <p className="text-ink-800">
        Amounts here are synthetic test balances, not donations and not real money. Nothing can be withdrawn for real yet. Production payouts:
        BLOCKED.
      </p>
    </div>
  );
}

export const ownerWithdrawalHref = (id: string) => `/dashboard/withdrawals/${encodeURIComponent(id)}`;

export function OwnerPayoutList({ items, empty = "No withdrawal requests yet" }: { items: OwnerPayout[]; empty?: string }) {
  if (items.length === 0) return <EmptyState title={empty} />;
  return (
    <ul className="space-y-3">
      {items.map((p) => (
        <li key={p.id} className="rounded-card border border-line bg-surface p-4">
          <p className="flex flex-wrap items-center gap-2 font-semibold text-ink-900">
            <Link href={ownerWithdrawalHref(p.id)} className="text-brand-700 underline">
              <LedgerAmount minor={p.amount_minor} currency={p.currency} />
            </Link>
            <Badge tone="gold">TEST_ONLY</Badge>
            <Badge tone="neutral">{p.status_label}</Badge>
          </p>
          <p className="text-sm text-ink-700">
            To {p.destination_masked} · requested {formatDateTime(p.requested_at)}
          </p>
        </li>
      ))}
    </ul>
  );
}
