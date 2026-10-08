import type { MetadataRoute } from "next";

/** Development preview: ask all crawlers to stay out (see robots metadata in layout.tsx). */
export default function robots(): MetadataRoute.Robots {
  return { rules: { userAgent: "*", disallow: "/" } };
}
