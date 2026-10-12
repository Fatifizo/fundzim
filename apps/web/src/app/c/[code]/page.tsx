import type { Metadata } from "next";
import { notFound, permanentRedirect } from "next/navigation";

import { isPublicCode, isSlug } from "@/lib/campaigns/paths";
import { fetchSlugByCode } from "@/lib/campaigns/public";
import { campaignPath } from "@/lib/site/url";

export const metadata: Metadata = { robots: { index: false, follow: true } };

/**
 * Short link `/c/{public_code}` → permanent redirect to the canonical campaign URL (ADR-038 §6), looked up
 * with GET /public/campaign-codes/{code}. Unknown or non-public codes get this app's 404.
 */
export default async function ShortLinkPage({ params }: PageProps<"/c/[code]">) {
  const { code } = await params;
  if (!isPublicCode(code)) notFound();
  const slug = await fetchSlugByCode(code);
  if (!slug || !isSlug(slug)) notFound();
  permanentRedirect(campaignPath(slug));
}
