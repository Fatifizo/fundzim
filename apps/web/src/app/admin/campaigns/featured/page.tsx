import { permanentRedirect } from "next/navigation";

import { requireStaff } from "@/lib/auth/session";
import { first, isUuid } from "@/lib/campaigns/paths";

/** Stage 7 staff picks became maker-checker featured placements (Stage 16, ADR-048): staff are sent to the new page. */
export default async function FeaturedCampaignsRedirect({ searchParams }: PageProps<"/admin/campaigns/featured">) {
  await requireStaff();
  const id = first((await searchParams).campaign_id);
  permanentRedirect(isUuid(id) ? `/admin/discovery/featured?campaign_id=${id}` : "/admin/discovery/featured");
}
