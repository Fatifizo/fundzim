import { CidrListError, cidrContains, formatIp, parseCidrList, parseIp, type Cidr } from "./ip";
import { getPeerHeaderName, PEER_HEADER_PREFIX } from "./peer-header";

/**
 * Client address the web tier forwards to the API as `X-Forwarded-For` (security review F-02, KI-04).
 *
 * Mirrors the API's rule (docs/stage-4/interface-contracts.md §3.1), one hop earlier:
 *  - The direct peer is the TCP peer of THIS process (see ./peer-stamp.ts), never a header value.
 *  - If the peer is NOT in WEB_TRUSTED_PROXY_CIDRS (default: empty), the peer is the client and every
 *    client-supplied forwarding header is ignored.
 *  - If the peer IS trusted (a reverse proxy in front of the web container), walk X-Forwarded-For right to
 *    left, skipping trusted addresses, and take the first untrusted one. A malformed entry stops the walk
 *    and the last trusted hop's view wins. X-Real-IP and Forwarded are ignored.
 *  - Unknown peer (stamp not installed) → null: the caller forwards no address at all, so the API falls
 *    back to the web container's address (coarse, but never spoofable).
 */

export const TRUSTED_PROXY_ENV = "WEB_TRUSTED_PROXY_CIDRS";

let cached: { raw: string | undefined; cidrs: Cidr[] } | undefined;

/** Parsed WEB_TRUSTED_PROXY_CIDRS (memoised per raw value). Throws CidrListError when malformed. */
export function trustedProxyCidrs(env: Record<string, string | undefined> = process.env): Cidr[] {
  const raw = env[TRUSTED_PROXY_ENV];
  if (cached && cached.raw === raw) return cached.cidrs;
  const cidrs = parseCidrList(raw);
  cached = { raw, cidrs };
  return cidrs;
}

function isTrusted(cidrs: readonly Cidr[], ip: ReturnType<typeof parseIp>): boolean {
  return ip !== null && cidrs.some((cidr) => cidrContains(cidr, ip));
}

export interface ResolveInput {
  /** TCP peer address as observed by this process. */
  peer: string | null | undefined;
  /** Raw X-Forwarded-For header values (possibly several header lines joined with ", "). */
  forwardedFor: string | null | undefined;
  trusted: readonly Cidr[];
}

/** Pure resolution logic; returns the canonical client IP or null when it cannot be determined. */
export function resolveClientIp({ peer, forwardedFor, trusted }: ResolveInput): string | null {
  const peerIp = parseIp(peer);
  if (!peerIp) return null;
  if (!isTrusted(trusted, peerIp)) return formatIp(peerIp);

  let lastTrusted = peerIp;
  const entries = (forwardedFor ?? "").split(",").map((entry) => entry.trim());
  for (let i = entries.length - 1; i >= 0; i--) {
    const entry = entries[i]!;
    if (entry === "" && entries.length === 1) break; // header absent
    const ip = parseIp(entry);
    if (!ip) break; // malformed: stop, the last trusted hop's view wins
    if (!isTrusted(trusted, ip)) return formatIp(ip);
    lastTrusted = ip;
  }
  return formatIp(lastTrusted);
}

/** Resolves the client IP for an incoming request's headers. Never throws. */
export function clientIpFromHeaders(headers: Headers, env: Record<string, string | undefined> = process.env): string | null {
  const name = getPeerHeaderName();
  if (!name) return null;
  let trusted: Cidr[];
  try {
    trusted = trustedProxyCidrs(env);
  } catch (error) {
    if (error instanceof CidrListError) return null; // refused at start in production (instrumentation)
    throw error;
  }
  return resolveClientIp({ peer: headers.get(name), forwardedFor: headers.get("x-forwarded-for"), trusted });
}

/** True for header names that are internal to the web process and must never be forwarded. */
export function isInternalHeader(name: string): boolean {
  return name.toLowerCase().startsWith(PEER_HEADER_PREFIX);
}
