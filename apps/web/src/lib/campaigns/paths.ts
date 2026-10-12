/**
 * Guards for identifiers that end up in URLs (route params, API paths, image sources). Anything that does
 * not match is treated as "not found" before any request is made, so a crafted value can never change the
 * API path (no "../", encoded slashes or query injection).
 */

const UUID = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;
/** ASCII slug: lowercase letters and digits in hyphen-separated groups (`<base>-<public_code>`, contract §3). */
const SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const SLUG_MAX = 160;
/** Opaque pagination cursors: printable ASCII only, bounded. */
const CURSOR = /^[\x21-\x7e]{1,512}$/;
const CODE = /^[A-Z][A-Z0-9_]{0,63}$/;

export function isUuid(value: unknown): value is string {
  return typeof value === "string" && UUID.test(value);
}

export function isSlug(value: unknown): value is string {
  return typeof value === "string" && value.length <= SLUG_MAX && SLUG.test(value);
}

export function isCursor(value: unknown): value is string {
  return typeof value === "string" && CURSOR.test(value);
}

export function isCode(value: unknown): value is string {
  return typeof value === "string" && CODE.test(value);
}

/** Category page slug (`children-welfare`). */
const CATEGORY_SLUG = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
export function isCategorySlug(value: unknown): value is string {
  return typeof value === "string" && value.length <= 64 && CATEGORY_SLUG.test(value);
}

/** The listing's `category` filter: a category code (`MEDICAL`) or a category slug (`medical`), contract §3. */
export function isCategoryFilter(value: unknown): value is string {
  return isCode(value) || isCategorySlug(value);
}

/** Short-link public code (Crockford base32; lower case on the wire, accepted in either case in links). */
const PUBLIC_CODE = /^[0-9a-zA-Z]{6,32}$/;
export function isPublicCode(value: unknown): value is string {
  return typeof value === "string" && PUBLIC_CODE.test(value);
}

/** Wire form of a public code (lower case). */
export function normalisePublicCode(value: string): string {
  return value.toLowerCase();
}

/** Public image URL for an approved media item, or null when either identifier is not well formed. */
export function publicMediaUrl(slug: string, mediaId: string): string | null {
  if (!isSlug(slug) || !isUuid(mediaId)) return null;
  return `/api/v1/public/campaigns/${slug}/media/${mediaId}`;
}

/** Widths the API can serve with `?w=` (JPEG width variants; the full derivative when a variant is missing). */
export const VARIANT_WIDTHS = [320, 640, 1024] as const;

export interface ImageSources {
  src: string;
  srcSet: string | undefined;
}

/**
 * `src` + `srcset` for a public image. `widths` are the variants the API listed for this image (`media[].variants`,
 * detail page); `null` means unknown (cards: the list endpoints send only `cover_media_id`), so all standard widths
 * are offered — safe, because the API falls back to the full image for a missing width. The full derivative is
 * never a candidate (it can be up to 8000 px wide). `preferred` picks the `src` fallback width.
 */
export function publicImageSources(slug: string, mediaId: string, widths: readonly number[] | null, preferred: number = 640): ImageSources | null {
  const base = publicMediaUrl(slug, mediaId);
  if (!base) return null;
  const usable = (widths ?? VARIANT_WIDTHS).filter((w): w is (typeof VARIANT_WIDTHS)[number] => (VARIANT_WIDTHS as readonly number[]).includes(w));
  const sorted = [...new Set(usable)].sort((a, b) => a - b);
  if (sorted.length === 0) return { src: base, srcSet: undefined }; // smaller than every variant: the full image is already small
  const fallback = sorted.filter((w) => w <= preferred).at(-1) ?? sorted[0]!;
  return { src: `${base}?w=${fallback}`, srcSet: sorted.map((w) => `${base}?w=${w} ${w}w`).join(", ") };
}

/**
 * `sizes` for the layouts that show campaign images (container max-w-6xl = 1152 px, 16/24/32 px gutters):
 * cards are about the full width on phones, half from 640 px and a third from 1024 px; the detail cover fills
 * the main column (beside the 22rem sidebar from 1024 px); gallery photos are half the main column from 640 px.
 */
export const IMAGE_SIZES = {
  card: "(min-width: 1152px) 352px, (min-width: 1024px) calc((100vw - 112px) / 3), (min-width: 640px) calc((100vw - 72px) / 2), calc(100vw - 32px)",
  cover: "(min-width: 1152px) 736px, (min-width: 1024px) calc(100vw - 448px), (min-width: 640px) calc(100vw - 48px), calc(100vw - 32px)",
  gallery: "(min-width: 1152px) 360px, (min-width: 1024px) calc((100vw - 480px) / 2), (min-width: 640px) calc((100vw - 64px) / 2), calc(100vw - 32px)",
} as const;

/** Owner preview of a processed image. */
export function ownerMediaUrl(campaignId: string, mediaId: string): string | null {
  if (!isUuid(campaignId) || !isUuid(mediaId)) return null;
  return `/api/v1/campaigns/${campaignId}/media/${mediaId}/content`;
}

/** Public page path for a slug (null for a malformed slug). */
export function publicCampaignPath(slug: string | null | undefined): string | null {
  return slug && isSlug(slug) ? `/campaigns/${slug}` : null;
}

export function first(value: string | string[] | undefined): string | undefined {
  return Array.isArray(value) ? value[0] : value;
}
