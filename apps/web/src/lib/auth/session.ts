import "server-only";

import { cookies, headers } from "next/headers";
import { redirect } from "next/navigation";
import { connection } from "next/server";
import { cache } from "react";

import { isApiError } from "@/lib/api/errors";
import { getServerApi } from "@/lib/api/server";
import { clientIpFromHeaders } from "@/lib/net/client-ip";

import { AUTH_COOKIES } from "./cookies";
import { loginUrl } from "./safe-redirect";
import type { Me, SessionState } from "./types";

/**
 * Server-side session access for Server Components (Data Access Layer pattern, Next.js authentication
 * guide). Calls the API directly at API_BASE_URL, forwarding ONLY the FundZim auth cookies of the incoming
 * request plus the observed client address (X-Forwarded-For; the API trusts it only from the web container).
 *
 * The API is the authority: a page is shown only if GET /auth/session says the session is valid. Results
 * are memoised per request with React `cache()` (header + page share one call) and are never cached across
 * requests or users.
 */

export class SessionUnavailableError extends Error {
  constructor(cause?: unknown) {
    super("The session could not be checked because the API is unavailable.", { cause });
    this.name = "SessionUnavailableError";
  }
}

async function forwardedHeaders(): Promise<Record<string, string>> {
  // Request-time work only (request ids use crypto.randomUUID): never part of any prerender.
  await connection();
  const [jar, incoming] = await Promise.all([cookies(), headers()]);
  const out: Record<string, string> = {};
  const cookiePairs = AUTH_COOKIES.flatMap((name) => {
    const value = jar.get(name)?.value;
    return value ? [`${name}=${value}`] : [];
  });
  if (cookiePairs.length > 0) out.Cookie = cookiePairs.join("; ");
  const ip = clientIpFromHeaders(incoming);
  if (ip) out["X-Forwarded-For"] = ip;
  const ua = incoming.get("user-agent");
  if (ua) out["User-Agent"] = ua.slice(0, 512);
  return out;
}

async function hasAnyAuthCookie(): Promise<boolean> {
  const jar = await cookies();
  return AUTH_COOKIES.some((name) => jar.has(name));
}

/**
 * Current session according to the API. Never throws for "not signed in"; throws
 * SessionUnavailableError when the API cannot be reached (callers decide how to degrade).
 */
export const getSession = cache(async (): Promise<SessionState> => {
  // No auth cookie at all → anonymous; no need to ask the API.
  if (!(await hasAnyAuthCookie())) return { authenticated: false };
  try {
    const api = getServerApi({ timeoutMs: 5_000, maxRetries: 1 });
    const result = await api.get<SessionState>("/api/v1/auth/session", { headers: await forwardedHeaders() });
    const data = result.data;
    if (data && data.authenticated === true && data.user) return { authenticated: true, user: data.user };
    return { authenticated: false, mfa_pending: data?.mfa_pending === true };
  } catch (error) {
    throw new SessionUnavailableError(error);
  }
});

/** For non-critical UI (the header): anonymous if the API is unreachable. */
export async function getSessionOrAnonymous(): Promise<SessionState> {
  try {
    return await getSession();
  } catch (error) {
    if (error instanceof SessionUnavailableError) return { authenticated: false };
    throw error;
  }
}

/**
 * Protected pages call this first. Redirects to /login?next=<path> when there is no valid session; lets
 * SessionUnavailableError propagate to the error boundary (fail closed, without a login redirect loop).
 */
export async function requireUser(currentPath: string): Promise<Me> {
  const session = await getSession();
  if (!session.authenticated || !session.user) redirect(loginUrl(currentPath));
  return session.user;
}

/** Auth pages (login/register) send signed-in users to their destination instead. */
export async function redirectIfAuthenticated(destination: string): Promise<void> {
  let session: SessionState;
  try {
    session = await getSession();
  } catch {
    return; // API down: show the form; submitting will report the outage.
  }
  if (session.authenticated) redirect(destination);
}

/**
 * Authenticated GET from a Server Component on behalf of the signed-in user. A 401 sends the user to the
 * login page; other errors propagate.
 */
export async function serverGet<T>(path: string, currentPath: string): Promise<T> {
  const api = getServerApi({ timeoutMs: 8_000, maxRetries: 1 });
  try {
    return (await api.get<T>(path, { headers: await forwardedHeaders() })).data;
  } catch (error) {
    if (isApiError(error) && error.status === 401) redirect(loginUrl(currentPath));
    throw error;
  }
}

