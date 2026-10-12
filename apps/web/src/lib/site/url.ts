/**
 * Canonical site origin and absolute URLs (contract §5: canonical and Open Graph absolute URLs use
 * PUBLIC_SITE_URL, never request headers). Pure functions over an env object so they are testable and safe
 * to import anywhere; callers on the server pass `process.env` (the default).
 */

type Env = Record<string, string | undefined>;

/**
 * Canonical site origin from the server-side PUBLIC_SITE_URL (an origin only: scheme + host [+ port], no
 * path, query, fragment or credentials). Anything else is treated as unset: URL-based metadata and share
 * links are then left out rather than guessed.
 */
export function siteOrigin(env: Env = process.env): URL | null {
  const raw = env.PUBLIC_SITE_URL?.trim();
  if (!raw) return null;
  try {
    const url = new URL(raw);
    if ((url.protocol !== "https:" && url.protocol !== "http:") || url.username || url.password || (url.pathname !== "/" && url.pathname !== "") || url.search || url.hash) return null;
    return new URL(url.origin);
  } catch {
    return null;
  }
}

/**
 * Whether search engines may index the public site. Off unless BOTH a valid PUBLIC_SITE_URL is configured
 * and SITE_INDEXING=allow: FundZim is still a development preview (donations are not available), so
 * indexing is an explicit deployment decision, never a side effect of setting the site URL for share links.
 */
export function indexingAllowed(env: Env = process.env): boolean {
  return siteOrigin(env) !== null && env.SITE_INDEXING?.trim().toLowerCase() === "allow";
}

/** Absolute URL for a same-site path ("/campaigns/x"), or null when the origin is not configured. */
export function absoluteUrl(path: string, origin: URL | null): string | null {
  if (!origin || !path.startsWith("/") || path.startsWith("//")) return null;
  return new URL(path, origin).toString();
}

/** Canonical public path of a campaign. */
export function campaignPath(slug: string): string {
  return `/campaigns/${slug}`;
}

/** Canonical public path of a category page. */
export function categoryPath(slug: string): string {
  return `/categories/${slug}`;
}

/** Generic Open Graph image shipped in public/ (used when a campaign has no approved cover). */
export const DEFAULT_OG_IMAGE = { path: "/og/fundzim-og.png", width: 1200, height: 630, alt: "FundZim" } as const;
