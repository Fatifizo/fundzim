import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { AdjustmentsTable, AssessmentsTable } from "@/components/admin/finance/fees";
import { FeeTestBanner, PolicyList } from "@/components/fees/common";
import { requireUser, serverGetOr404 } from "@/lib/auth/session";
import { isUuid } from "@/lib/campaigns/paths";
import { readCampaignFees } from "@/lib/fees/normalise";

export const metadata: Metadata = { title: "Campaign fees", robots: { index: false, follow: false, nocache: true } };

/** Fee disclosure for one campaign (owner or member): policies that would apply, assessed fees and adjustments. */
export default async function CampaignFeesPage({ params }: PageProps<"/dashboard/campaigns/[id]/fees">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const path = `/dashboard/campaigns/${id}/fees`;
  await requireUser(path);
  const f = readCampaignFees((await serverGetOr404<unknown>(`/api/v1/campaigns/${id}/fees`, path)).data);
  return (
    <AccountPage title="Fees" intro="Which fees would apply to donations and withdrawals for this campaign, who bears them, and the fees assessed so far.">
      <FeeTestBanner />
      <section aria-labelledby="fee-policies" className="space-y-3">
        <h2 id="fee-policies" className="font-display text-2xl font-semibold text-ink-900">
          Fee policies that apply
        </h2>
        <PolicyList items={f.policies} />
        <p>
          <Link href={`/dashboard/donation-preview?campaign=${encodeURIComponent(id)}`} className="font-semibold text-brand-700 underline">
            Preview what a donor would see
          </Link>
        </p>
      </section>
      <section aria-labelledby="fee-assessed" className="space-y-3">
        <h2 id="fee-assessed" className="font-display text-2xl font-semibold text-ink-900">
          Fees assessed
        </h2>
        <p className="text-ink-700">Estimated provider fees come from unverified or synthetic evidence; a fee without evidence is UNKNOWN.</p>
        <AssessmentsTable items={f.assessments} caption="Fees assessed for this campaign" staff={false} />
      </section>
      <section aria-labelledby="fee-adjustments" className="space-y-3">
        <h2 id="fee-adjustments" className="font-display text-2xl font-semibold text-ink-900">
          Fee adjustments
        </h2>
        <AdjustmentsTable items={f.adjustments} caption="Fee adjustments for this campaign" staff={false} />
      </section>
      {f.restrictions.length ? (
        <section aria-labelledby="fee-restrictions" className="space-y-2">
          <h2 id="fee-restrictions" className="font-display text-2xl font-semibold text-ink-900">
            Restrictions
          </h2>
          <ul className="list-disc space-y-1 pl-5 text-ink-800">
            {f.restrictions.map((r) => (
              <li key={r}>{r}</li>
            ))}
          </ul>
        </section>
      ) : null}
    </AccountPage>
  );
}
