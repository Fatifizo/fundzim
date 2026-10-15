import { LedgerAmount } from "@/components/admin/finance/common";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/ui/empty-state";
import { basisLabel, bpsToPercent, describeTerms, responsibilityLabel } from "@/lib/fees/labels";
import type { ActivePolicy } from "@/lib/fees/types";
import { humanise } from "@/lib/payments/labels";

/** Organiser and donor fee pages: everything is TEST; nothing is charged. */
export function FeeTestBanner() {
  return (
    <div className="rounded-card border-2 border-gold-500/50 bg-gold-100 p-4">
      <p className="text-lg font-semibold text-ink-900">
        Test fees only <Badge tone="gold">TEST</Badge>
      </p>
      <p className="text-ink-800">
        TEST — synthetic configuration, not FundZim&apos;s commercial rates. Donations are not yet available, no fee is charged and nothing here is a real
        donation.
      </p>
    </div>
  );
}

/** A fee amount; null is UNKNOWN (never shown as zero). */
export function MaybeAmount({ minor, currency }: { minor: string | null; currency: string }) {
  if (minor === null) return <Badge tone="info">UNKNOWN</Badge>;
  return <LedgerAmount minor={minor} currency={currency} />;
}

export function PolicyList({ items }: { items: ActivePolicy[] }) {
  if (items.length === 0) return <EmptyState title="No fee policy applies" description="Without an approved, active policy nothing is charged. Provider fees stay UNKNOWN." />;
  return (
    <ul className="space-y-3">
      {items.map((p) => (
        <li key={p.version_id} className="space-y-1 rounded-card border border-line bg-surface p-4">
          <p className="flex flex-wrap items-center gap-2 font-semibold text-ink-900">
            {humanise(p.category)} · {p.currency} <Badge tone="gold">TEST</Badge>
          </p>
          <p className="text-ink-800">{describeTerms(p.terms, p.currency)}</p>
          <p className="text-sm text-ink-700">
            {responsibilityLabel(p.responsibility)}
            {p.donor_share_bps ? ` (donor share ${bpsToPercent(p.donor_share_bps)})` : ""} · {p.scope}
            {p.refund_treatment ? ` · on refunds: ${humanise(p.refund_treatment).toLowerCase()}` : ""}
          </p>
        </li>
      ))}
    </ul>
  );
}

/** The payout fee estimate of a withdrawal: amount (or UNKNOWN), who bears it and what the beneficiary receives. */
export function PayoutFeeFacts({ currency, fee, basis, responsibility, receives }: { currency: string; fee: string | null; basis: string | null; responsibility: string | null; receives: string | null }) {
  return (
    <dl className="grid gap-3 sm:grid-cols-3">
      <div>
        <dt className="text-sm font-semibold text-ink-600">Payout fee (estimate)</dt>
        <dd>
          <MaybeAmount minor={fee} currency={currency} /> <span className="text-sm text-ink-700">{basisLabel(basis ?? "UNKNOWN")}</span>
        </dd>
      </div>
      <div>
        <dt className="text-sm font-semibold text-ink-600">Who bears it</dt>
        <dd>{responsibilityLabel(responsibility)}</dd>
      </div>
      <div>
        <dt className="text-sm font-semibold text-ink-600">The beneficiary receives</dt>
        <dd>
          <MaybeAmount minor={receives} currency={currency} />
        </dd>
      </div>
    </dl>
  );
}
