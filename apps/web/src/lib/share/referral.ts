/**
 * Referral codes for shared links (POST /api/v1/discovery/referrals). A code names only a campaign and a share channel;
 * it carries no personal data and grants nothing. Codes are kept in module memory (never browser storage) so repeated
 * shares to the same channel during this page session reuse one code. Every failure — referrals disabled (always in production while LR-023 is open),
 * timeout, network, rate limit — yields null and the plain canonical URL is shared instead.
 */
import type { ShareChannel } from "./links";

const PATH = "/api/v1/discovery/referrals";
const CODE = /^[0-9a-hjkmnp-tv-z]{12}$/;
export const REFERRAL_TIMEOUT_MS = 800;

interface Cache {
  disabled: boolean;
  codes: Map<string, { code: string; expires: number }>;
}

/** Module memory: lost on a full page load (then a new code is issued; codes are cheap and anonymous). */
const memory: Cache = { disabled: false, codes: new Map() };

/** For tests: forget cached codes and the disabled flag. */
export function resetReferralCache(): void {
  memory.disabled = false;
  memory.codes.clear();
}

export async function referralCode(
  slug: string,
  channel: ShareChannel,
  deps: { fetch?: typeof fetch; now?: number; timeoutMs?: number } = {},
): Promise<string | null> {
  const now = deps.now ?? Date.now();
  const cache = memory;
  if (cache.disabled) return null;
  const key = `${slug}|${channel}`;
  const hit = cache.codes.get(key);
  if (hit && CODE.test(hit.code) && hit.expires > now) return hit.code;
  const doFetch = deps.fetch ?? (typeof fetch !== "undefined" ? fetch : undefined);
  if (!doFetch) return null;
  const ctrl = typeof AbortController !== "undefined" ? new AbortController() : null;
  const timer = setTimeout(() => ctrl?.abort(), deps.timeoutMs ?? REFERRAL_TIMEOUT_MS);
  try {
    const res = await doFetch(PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ slug, channel }),
      credentials: "omit",
      cache: "no-store",
      referrerPolicy: "no-referrer",
      signal: ctrl?.signal,
    });
    if (res.status === 503) {
      cache.disabled = true; // referrals switched off: stop asking during this page session
      return null;
    }
    if (res.status !== 201) return null;
    const body = (await res.json()) as { data?: { code?: unknown; expires_at?: unknown } };
    const code = body.data?.code;
    if (typeof code !== "string" || !CODE.test(code)) return null;
    const exp = typeof body.data?.expires_at === "string" ? Date.parse(body.data.expires_at) : NaN;
    cache.codes.set(key, { code, expires: Number.isNaN(exp) ? now + 24 * 3600 * 1000 : Math.min(exp, now + 7 * 24 * 3600 * 1000) });
    return code;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}
