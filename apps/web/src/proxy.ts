import { NextResponse, type NextRequest } from "next/server";

import { hasSessionCookie } from "@/lib/auth/cookies";
import { loginUrl } from "@/lib/auth/safe-redirect";
import { buildCsp, generateNonce } from "@/lib/security/csp";

/**
 * Next.js 16 Proxy (formerly Middleware). Runs before every page render:
 *
 * 1. Per-request CSP nonce (src/lib/security/csp.ts). The policy goes on the response and on the request
 *    headers; Next.js reads the nonce from the request's Content-Security-Policy and applies it to the
 *    scripts and stylesheets it renders.
 * 2. Defence in depth for protected areas: without any session cookie, /dashboard and /settings/* redirect
 *    to /login?next=… before rendering. This is NOT the access check — the pages verify the session with
 *    the API server-side (src/lib/auth/session.ts), and the API authorises every call. /admin is NOT
 *    redirected: the staff area must not be discoverable, so its pages answer 404 to anyone who is not a
 *    signed-in staff member with the needed permission (requireStaff / serverGetOr404).
 * 3. Authenticated areas (including /admin) are marked `Cache-Control: private, no-store`.
 *
 * Excluded: /api/* (the API proxy route; its responses get a non-document CSP in next.config.ts), static
 * build assets, the icon and /healthz.
 */
const PROTECTED = [/^\/dashboard(?:\/|$)/, /^\/settings(?:\/|$)/];
const PRIVATE = [...PROTECTED, /^\/admin(?:\/|$)/];

export function proxy(request: NextRequest) {
  const { pathname, search } = request.nextUrl;
  const isProtected = PROTECTED.some((re) => re.test(pathname));
  const isPrivate = PRIVATE.some((re) => re.test(pathname));

  if (isProtected && !hasSessionCookie((name) => request.cookies.has(name))) {
    const target = new URL(loginUrl(`${pathname}${search}`), request.url);
    const redirect = NextResponse.redirect(target, 307);
    redirect.headers.set("Cache-Control", "private, no-store");
    return redirect;
  }

  const nonce = generateNonce();
  const csp = buildCsp({ nonce, isDev: process.env.NODE_ENV === "development" });

  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("Content-Security-Policy", csp);
  requestHeaders.set("x-nonce", nonce);

  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set("Content-Security-Policy", csp);
  if (isPrivate) response.headers.set("Cache-Control", "private, no-store");
  return response;
}

export const config = {
  matcher: ["/((?!api/|_next/static/|_next/image|icon\\.svg|favicon\\.ico|robots\\.txt|healthz).*)"],
};
