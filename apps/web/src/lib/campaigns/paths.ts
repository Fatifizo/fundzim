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

/** Public image URL for an approved media item, or null when either identifier is not well formed. */
export function publicMediaUrl(slug: string, mediaId: string): string | null {
  if (!isSlug(slug) || !isUuid(mediaId)) return null;
  return `/api/v1/public/campaigns/${slug}/media/${mediaId}`;
}

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
