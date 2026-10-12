import type { MetadataRoute } from "next";
import { connection } from "next/server";

import { fetchSitemapData } from "@/lib/campaigns/public";
import { buildSitemap } from "@/lib/seo/robots";
import { siteOrigin } from "@/lib/site/url";

/**
 * /sitemap.xml: static public pages plus live PUBLIC campaigns and active categories (GET /public/sitemap),
 * rendered per request. Without PUBLIC_SITE_URL there are no absolute URLs, so it is empty.
 */
export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  await connection();
  const origin = siteOrigin();
  return buildSitemap(origin, origin ? await fetchSitemapData() : null);
}
