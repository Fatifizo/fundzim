import type { Metadata } from "next";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { LedgerAmount } from "@/components/admin/finance/common";
import { OwnerPayoutList, TestFundsBanner } from "@/components/payouts/common";
import { WithdrawalForm, type DestinationOption } from "@/components/payouts/withdrawal-form";
import { requireUser, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { readAvailability, readOwnerPayout, readPage, reasonMessage } from "@/lib/payouts/normalise";
import type { PayoutDestination } from "@/lib/verification/types";

export const metadata: Metadata = { title: "Campaign withdrawals", robots: { index: false, follow: false, nocache: true } };

/** Test withdrawals for one campaign: availability preview, the request form and the campaign's requests. */
export default async function CampaignWithdrawalsPage({ params }: PageProps<"/dashboard/campaigns/[id]/withdrawals">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const path = `/dashboard/campaigns/${id}/withdrawals`;
  await requireUser(path);
  const a = readAvailability((await serverGetOr404<unknown>(`/api/v1/campaigns/${id}/payout-eligibility?currency=USD`, path)).data);
  const requests = readPage(await serverGetOptional<unknown>(`/api/v1/campaigns/${id}/payout-requests`, path, { items: [] }), readOwnerPayout);
  const destinations = await serverGetOptional<PayoutDestination[]>("/api/v1/payout-destinations", path, []);
  const options: DestinationOption[] = (Array.isArray(destinations) ? destinations : [])
    .filter((d) => d.payout_readiness?.payout_eligible === true && d.currency === a.currency)
    .map((d) => ({ id: d.id, label: `${d.provider_name ?? d.rail} ${d.masked_identifier} (${d.holder_name})` }));
  return (
    <AccountPage title="Withdrawals" intro="Request a withdrawal of settled funds to a verified payout account. A finance officer reviews every request.">
      <TestFundsBanner />
      <section aria-labelledby="availability" className="space-y-2 rounded-card border border-line bg-surface p-4">
        <h2 id="availability" className="font-display text-2xl font-semibold text-ink-900">
          Test balance ({a.currency})
        </h2>
        <dl className="grid gap-3 sm:grid-cols-2">
          <div>
            <dt className="text-sm font-semibold text-ink-600">Settled and released (TEST_ONLY)</dt>
            <dd>
              <LedgerAmount minor={a.settled_released_minor} currency={a.currency} />
            </dd>
          </div>
          <div>
            <dt className="text-sm font-semibold text-ink-600">Held for open refund requests</dt>
            <dd>
              <LedgerAmount minor={a.refund_exposure_minor} currency={a.currency} />
            </dd>
          </div>
          <div>
            <dt className="text-sm font-semibold text-ink-600">Reserved for withdrawals in progress</dt>
            <dd>
              <LedgerAmount minor={a.reserved_minor} currency={a.currency} />
            </dd>
          </div>
          <div>
            <dt className="text-sm font-semibold text-ink-600">Available for a test withdrawal</dt>
            <dd>
              <LedgerAmount minor={a.available_minor} currency={a.currency} />
            </dd>
          </div>
        </dl>
        <p className="text-sm text-ink-700">Production payouts: BLOCKED. These are not real funds and cannot be withdrawn for real.</p>
        {a.blockers.length > 0 ? (
          <ul className="list-disc pl-5 text-ink-800">
            {a.blockers.map((b) => (
              <li key={b}>{reasonMessage(b)}</li>
            ))}
          </ul>
        ) : null}
      </section>
      <section aria-labelledby="request" className="space-y-3">
        <h2 id="request" className="font-display text-2xl font-semibold text-ink-900">
          Request a test withdrawal
        </h2>
        <WithdrawalForm campaignId={id} currency={a.currency} availableMinor={a.available_minor} destinations={options} enabled={a.payouts_enabled} />
      </section>
      <section aria-labelledby="requests" className="space-y-3">
        <h2 id="requests" className="font-display text-2xl font-semibold text-ink-900">
          This campaign&apos;s withdrawal requests
        </h2>
        <OwnerPayoutList items={requests.items} />
      </section>
    </AccountPage>
  );
}
