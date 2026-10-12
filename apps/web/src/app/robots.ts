import type { MetadataRoute } from "next";
import { connection } from "next/server";

import { buildRobots } from "@/lib/seo/robots";
import { indexingAllowed, siteOrigin } from "@/lib/site/url";

/**
 * robots.txt, evaluated per request (environment read at runtime, not baked in at build). Crawlers are kept
 * out entirely unless indexing is explicitly allowed; private areas are always disallowed.
 */
export default async function robots(): Promise<MetadataRoute.Robots> {
  await connection();
  return buildRobots(siteOrigin(), indexingAllowed());
}
