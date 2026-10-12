import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { AccountPage } from "@/components/account/account-shell";
import { CampaignReviewView } from "@/components/admin/campaign-review";
import { requireStaff, serverGetOptional, serverGetOr404 } from "@/lib/auth/session";
import { readMedia, readStaffDetail } from "@/lib/campaigns/normalise";
import { isUuid } from "@/lib/campaigns/paths";

export const metadata: Metadata = { title: "Campaign review", robots: { index: false, follow: false, nocache: true } };

export default async function CampaignReviewPage({ params }: PageProps<"/admin/campaigns/review/[id]">) {
  const me = await requireStaff();
  const { id } = await params;
  if (!isUuid(id)) notFound();
  const path = `/admin/campaigns/review/${id}`;
  const [raw, staffMedia] = await Promise.all([
    serverGetOr404<unknown>(`/api/v1/admin/campaigns/${id}/review`, path),
    serverGetOptional<unknown>(`/api/v1/admin/campaigns/${id}/media/all`, path, null),
  ]);
  const base = readStaffDetail(raw.data);
  // The review detail carries media readiness only; the staff media list is a separate call.
  const media = readMedia(staffMedia);
  const detail = media.length > 0 ? { ...base, media } : base;
  return (
    <AccountPage
      title="Campaign review"
      intro={
        <span className="flex flex-wrap gap-x-6 gap-y-2 text-base">
          <Link href="/admin/campaigns/review" className="font-semibold text-brand-700 underline">
            Back to the queue
          </Link>
          {/* Only offered for live campaigns; the featured page itself needs campaign.feature (404 otherwise). */}
          {["ACTIVE", "PAUSED"].includes(String(detail.campaign.status)) ? (
            <Link href={`/admin/campaigns/featured?campaign_id=${id}`} className="font-semibold text-brand-700 underline">
              Feature on the home page
            </Link>
          ) : null}
        </span>
      }
    >
      <CampaignReviewView detail={detail} meId={me.id} />
    </AccountPage>
  );
}
