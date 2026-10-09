import Link from "next/link";
import { notFound } from "next/navigation";

import { CampaignNav } from "@/components/campaigns/campaign-nav";
import { isUuid } from "@/lib/campaigns/paths";

/** Section navigation for one campaign. Not an access check: each page loads the campaign through the API. */
export default async function CampaignLayout({ children, params }: LayoutProps<"/dashboard/campaigns/[id]">) {
  const { id } = await params;
  if (!isUuid(id)) notFound();
  return (
    <div>
      <p className="mb-4">
        <Link href="/dashboard/campaigns" className="font-semibold text-brand-700 underline">
          All your campaigns
        </Link>
      </p>
      <CampaignNav campaignId={id} />
      {children}
    </div>
  );
}
