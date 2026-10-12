/**
 * First-party analytics (ADR-038 §4): a tiny fire-and-forget POST to /api/v1/public/events. No cookies
 * (`credentials: "omit"`), no identifiers, no query text, no third parties. Nothing is sent when the browser
 * signals Do Not Track or Global Privacy Control. Failures are ignored: analytics never affects the page.
 */
import type { ShareChannel } from "@/lib/share/links";

export type AnalyticsEvent =
  | { type: "CAMPAIGN_PAGE_VIEW"; slug: string }
  | { type: "CAMPAIGN_SHARE_CLICK"; slug: string; channel: ShareChannel }
  /** Only that a search happened, and its category filter if any: never the query text. */
  | { type: "CAMPAIGN_SEARCH"; category?: string }
  | { type: "CATEGORY_VIEW"; category: string };

export const EVENTS_PATH = "/api/v1/public/events";

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
  const body: Record<string, string> = { type: event.type };
  if ("slug" in event) body.slug = event.slug;
  if ("channel" in event) body.channel = event.channel;
  if ("category" in event && event.category) body.category = event.category;
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
      body: JSON.stringify(eventBody(event)),
      credentials: "omit",
      keepalive: true,
      cache: "no-store",
      referrerPolicy: "no-referrer",
    }).catch(() => undefined);
  } catch {
    // Never let analytics break the page.
  }
}
