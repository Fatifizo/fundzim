/**
 * Cookie names from docs/stage-4/interface-contracts.md §4.1. The `__Host-` variants are used over https,
 * the plain names locally. Session and MFA cookies are HttpOnly (never readable here); only the CSRF cookie
 * is readable by JavaScript, by design.
 */
export const SESSION_COOKIES = ["__Host-fz_session", "fz_session"] as const;
export const CSRF_COOKIES = ["__Host-fz_csrf", "fz_csrf"] as const;
export const MFA_COOKIES = ["__Host-fz_mfa", "fz_mfa"] as const;

/** Every auth cookie the web tier forwards to the API on server-side calls (nothing else is forwarded). */
export const AUTH_COOKIES: readonly string[] = [...SESSION_COOKIES, ...CSRF_COOKIES, ...MFA_COOKIES];

/** Parses a `Cookie` header / `document.cookie` string. Later duplicates do not override earlier ones. */
export function parseCookieString(cookieString: string): Map<string, string> {
  const out = new Map<string, string>();
  for (const part of cookieString.split(";")) {
    const eq = part.indexOf("=");
    if (eq <= 0) continue;
    const name = part.slice(0, eq).trim();
    if (!name || out.has(name)) continue;
    let value = part.slice(eq + 1).trim();
    if (value.startsWith('"') && value.endsWith('"') && value.length >= 2) value = value.slice(1, -1);
    try {
      value = decodeURIComponent(value);
    } catch {
      // keep raw value
    }
    out.set(name, value);
  }
  return out;
}

/** CSRF token from a cookie string, preferring the `__Host-` cookie. */
export function readCsrfToken(cookieString: string): string | undefined {
  const cookies = parseCookieString(cookieString);
  for (const name of CSRF_COOKIES) {
    const value = cookies.get(name);
    if (value) return value;
  }
  return undefined;
}

export function hasSessionCookie(has: (name: string) => boolean): boolean {
  return SESSION_COOKIES.some((name) => has(name));
}
