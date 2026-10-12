/**
 * Per-page metadata for public pages: title, description, canonical, Open Graph and Twitter card. Absolute
 * URLs come only from PUBLIC_SITE_URL; without it the URL-based fields are left out (never guessed from
 * request headers). Images are an approved campaign cover or the generic FundZim image in public/.
 */
import type { Metadata } from "next";

import { truncate } from "@/lib/campaigns/text";
import { absoluteUrl, DEFAULT_OG_IMAGE, indexingAllowed, siteOrigin } from "@/lib/site/url";

export interface PageMetaInput {
  title: string;
  description: string;
  /** Same-site path of the canonical page ("/campaigns/x"). */
  path: string;
  /** Same-site path of an approved image, with its alt text. Defaults to the generic FundZim image. */
  image?: { path: string; alt: string } | null;
  type?: "website" | "article";
}

type Env = Record<string, string | undefined>;

/** Robots directive for a public page: indexable only when indexing is explicitly allowed. */
export function publicRobots(env: Env = process.env): Metadata["robots"] {
  return indexingAllowed(env) ? { index: true, follow: true } : { index: false, follow: false, nocache: true };
}

export function pageMetadata({ title, description, path, image, type = "website" }: PageMetaInput, env: Env = process.env): Metadata {
  const origin = siteOrigin(env);
  const desc = truncate(description, 200);
  const url = absoluteUrl(path, origin);
  const img = image ?? { path: DEFAULT_OG_IMAGE.path, alt: DEFAULT_OG_IMAGE.alt };
  const imageUrl = absoluteUrl(img.path, origin);
  const isDefault = img.path === DEFAULT_OG_IMAGE.path;
  const ogImage = imageUrl ? [{ url: imageUrl, alt: img.alt, ...(isDefault ? { width: DEFAULT_OG_IMAGE.width, height: DEFAULT_OG_IMAGE.height } : {}) }] : undefined;
  return {
    title,
    description: desc,
    robots: publicRobots(env),
    ...(url ? { alternates: { canonical: url } } : {}),
    openGraph: {
      title,
      description: desc,
      type,
      siteName: "FundZim",
      locale: "en_ZW",
      ...(url ? { url } : {}),
      ...(ogImage ? { images: ogImage } : {}),
    },
    twitter: {
      card: ogImage ? "summary_large_image" : "summary",
      title,
      description: desc,
      ...(imageUrl ? { images: [imageUrl] } : {}),
    },
  };
}
