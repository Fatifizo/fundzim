import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { formatDateTime } from "@/components/account/format";
import { LedgerAmount } from "@/components/admin/finance/common";
import { PayoutFeeFacts } from "@/components/fees/common";
import { TestFundsBanner } from "@/components/payouts/common";
import { CancelWithdrawal } from "@/components/payouts/withdrawal-form";
import { Badge } from "@/components/ui/badge";
import { requireUser, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { readOwnerPayout, readPage, readTimelineItem, reasonMessage } from "@/lib/payouts/normalise";

export const metadata: Metadata = { title: "Withdrawal", robots: { index: false, follow: false, nocache: true } };

/** One withdrawal request as its owner sees it (no approver identities, no risk or compliance detail). */
export default async function WithdrawalPage({ params }: PageProps<"/dashboard/withdrawals/[id]">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const path = `/dashboard/withdrawals/${id}`;
  await requireUser(path);
  const p = readOwnerPayout((await serverGetOr404<unknown>(`/api/v1/payout-requests/${id}`, path)).data);
  if (!p) notFound();
  const timeline = readPage(await serverGetOptional<unknown>(`/api/v1/payout-requests/${id}/timeline`, path, { items: [] }), readTimelineItem);
  return (
    <AccountPage title="Withdrawal" intro={<Link href="/dashboard/withdrawals">All withdrawals</Link>}>
      <TestFundsBanner />
      <section aria-labelledby="w-summary" className="space-y-2 rounded-card border border-line bg-surface p-4">
        <h2 id="w-summary" className="font-display text-2xl font-semibold text-ink-900">
          <LedgerAmount minor={p.amount_minor} currency={p.currency} /> <Badge tone="gold">TEST_ONLY</Badge>
        </h2>
        <p className="text-ink-900">
          Status: <strong>{p.status_label}</strong>
        </p>
        {p.reason_category ? <p className="text-ink-800">{reasonMessage(p.reason_category)}</p> : null}
        <p className="text-sm text-ink-700">
          To {p.destination_masked} · requested {formatDateTime(p.requested_at)}
          {p.purpose ? ` · ${p.purpose}` : ""}
        </p>
        <PayoutFeeFacts currency={p.currency} fee={p.fee_estimate_minor} basis={p.fee_basis} responsibility={p.fee_responsibility} receives={p.beneficiary_receives_minor} />
        <p>
          <Link href={`/dashboard/campaigns/${encodeURIComponent(p.campaign_id)}/withdrawals`} className="text-brand-700 underline">
            Campaign withdrawals
          </Link>
        </p>
      </section>
      {/* kept mounted after a cancel so its confirmation survives the refresh */}
      <CancelWithdrawal id={p.id} available={p.can_cancel} />
      <section aria-labelledby="w-timeline" className="space-y-2">
        <h2 id="w-timeline" className="font-display text-2xl font-semibold text-ink-900">
          Timeline
        </h2>
        <ol className="space-y-2">
          {timeline.items.map((t, i) => (
            <li key={i} className="text-ink-800">
              {formatDateTime(t.at)} — {t.label}
            </li>
          ))}
        </ol>
      </section>
    </AccountPage>
  );
}
