/**
 * Open-redirect protection for `?next=` (OWASP Unvalidated Redirects). Only same-site, relative,
 * path-absolute URLs are accepted: must start with a single "/", no "//" or "/\" (protocol-relative), no
 * backslashes, control characters or schemes, and must stay on the same origin after URL parsing. Paths
 * into the auth flow itself and the API are refused so a crafted link cannot create loops.
 */
export const DEFAULT_AFTER_LOGIN = "/dashboard";
const MAX_LENGTH = 512;
const PROBE_ORIGIN = "https://fundzim.invalid";
const REFUSED_ROOTS = ["/login", "/register", "/logout", "/api", "/_next"];

export function safeNextPath(raw: unknown): string | null {
  if (typeof raw !== "string") return null;
  if (raw.length === 0 || raw.length > MAX_LENGTH) return null;
  if (!raw.startsWith("/") || raw.startsWith("//")) return null;
  // Backslashes are normalised to "/" by browsers ("/\evil.com" → "//evil.com"); reject outright.
  if (raw.includes("\\")) return null;
  if (/[\u0000-\u001f\u007f\s]/.test(raw)) return null;
  let url: URL;
  try {
    url = new URL(raw, PROBE_ORIGIN);
  } catch {
    return null;
  }
  if (url.origin !== PROBE_ORIGIN) return null;
  const path = `${url.pathname}${url.search}${url.hash}`;
  if (path.startsWith("//")) return null;
  const lower = url.pathname.toLowerCase();
  if (REFUSED_ROOTS.some((root) => lower === root || lower.startsWith(`${root}/`))) return null;
  return path;
}

/** Where to go after a successful sign-in. */
export function afterLoginPath(raw: unknown): string {
  return safeNextPath(raw) ?? DEFAULT_AFTER_LOGIN;
}

/** `/login?next=<path>` for a protected path (the path is validated; invalid → plain /login). */
export function loginUrl(nextPath?: string | null, base = "/login"): string {
  const safe = safeNextPath(nextPath);
  return safe ? `${base}?next=${encodeURIComponent(safe)}` : base;
}
