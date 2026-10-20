/**
 * First-party engagement events (ADR-038 §4, Stage 16 ADR-048): a tiny fire-and-forget POST to
 * /api/v1/discovery/events. No cookies (`credentials: "omit"`), no identifiers, no query text, no third parties, no
 * retries (a retried event could be counted twice; losing one is acceptable). Nothing is sent when the browser signals
 * Do Not Track or Global Privacy Control. Failures are ignored: analytics never affects the page.
 *
 * Search and listing impressions are counted by the API when it returns results; the browser reports only views, share
 * clicks and result clicks.
 */
import type { ShareChannel } from "@/lib/share/links";

/** Where a campaign view came from (closed set, mirrored by the API). */
export const TRAFFIC_SOURCES = ["DIRECT", "SEARCH", "CATEGORY", "FEATURED", "TRENDING", "RECOMMENDED", "INTERNAL", "UNKNOWN"] as const;
export type TrafficSource = (typeof TRAFFIC_SOURCES)[number];
/** Discovery surfaces whose result clicks are counted. */
export type ClickSurface = "SEARCH" | "CATEGORY" | "FEATURED" | "TRENDING" | "RECOMMENDED";

export type AnalyticsEvent =
  | { type: "CAMPAIGN_VIEW"; slug: string; source?: TrafficSource; ref?: string }
  | { type: "SHARE_CLICK"; slug: string; channel: ShareChannel }
  | { type: "RESULT_CLICK"; slug: string; source: ClickSurface };

export const EVENTS_PATH = "/api/v1/discovery/events";

const REF = /^[0-9a-hjkmnp-tv-z]{12}$/;

interface PrivacySignals {
  doNotTrack?: string | null;
  globalPrivacyControl?: boolean;
  msDoNotTrack?: string | null;
}

/** True unless the browser asks not to be tracked (DNT "1"/"yes" or GPC true). */
export function analyticsAllowed(nav: PrivacySignals | undefined, win?: { doNotTrack?: string | null }): boolean {
  if (!nav) return false;
  const dnt = [nav.doNotTrack, nav.msDoNotTrack, win?.doNotTrack].some((v) => v === "1" || v === "yes");
  return !dnt && nav.globalPrivacyControl !== true;
}

/** Only the documented fields, so nothing else can leak into the payload. */
export function eventBody(event: AnalyticsEvent): Record<string, string> {
  const body: Record<string, string> = { type: event.type, slug: event.slug };
  if (event.type === "SHARE_CLICK") body.channel = event.channel;
  if (event.type === "RESULT_CLICK") body.source = event.source;
  if (event.type === "CAMPAIGN_VIEW") {
    if (event.source) body.source = event.source;
    if (event.ref && REF.test(event.ref)) body.ref = event.ref;
  }
  return body;
}

export function trackEvent(event: AnalyticsEvent, deps: { fetch?: typeof fetch; navigator?: PrivacySignals; window?: { doNotTrack?: string | null } } = {}): void {
  const nav = deps.navigator ?? (typeof navigator !== "undefined" ? (navigator as unknown as PrivacySignals) : undefined);
  const win = deps.window ?? (typeof window !== "undefined" ? (window as unknown as { doNotTrack?: string | null }) : undefined);
  if (!analyticsAllowed(nav, win)) return;
  const doFetch = deps.fetch ?? (typeof fetch !== "undefined" ? fetch : undefined);
  if (!doFetch) return;
  try {
    void doFetch(EVENTS_PATH, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ events: [eventBody(event)] }),
      credentials: "omit",
      keepalive: true,
      cache: "no-store",
      referrerPolicy: "no-referrer",
    }).catch(() => undefined);
  } catch {
    // Never let analytics break the page.
  }
}

// ---- traffic source hand-off (in memory, this page session only) --------------------------------------------------------

/**
 * The last discovery surface a campaign was opened from. Module memory only (no browser storage: CLAUDE.md / FRONTEND
 * rules): it survives client-side navigation within the app and is gone on a full page load, which then falls back to the
 * referrer rule. Never sent anywhere except as the closed `source` value of one view event.
 */
let lastSource: { slug: string; source: ClickSurface; at: number } | null = null;

const SOURCE_TTL_MS = 30 * 60 * 1000;

/** Remembers which discovery surface a campaign was opened from (read once by the campaign page). */
export function rememberSource(slug: string, source: ClickSurface, now = Date.now()): void {
  lastSource = { slug, source, at: now };
}

/**
 * The traffic source of a campaign view: the remembered discovery surface for this campaign (if recent), otherwise
 * DIRECT without a referrer, INTERNAL for a same-site referrer, UNKNOWN for anything else. The remembered value is
 * consumed. Never reads cookies or sends the referrer itself.
 */
export function takeSource(slug: string, referrer: string, origin: string, now = Date.now()): TrafficSource {
  const remembered = lastSource;
  lastSource = null;
  if (remembered && remembered.slug === slug && now - remembered.at < SOURCE_TTL_MS) return remembered.source;
  if (!referrer) return "DIRECT";
  try {
    return new URL(referrer).origin === origin ? "INTERNAL" : "UNKNOWN";
  } catch {
    return "UNKNOWN";
  }
}
