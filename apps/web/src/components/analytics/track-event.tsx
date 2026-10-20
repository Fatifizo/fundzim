"use client";

import { useEffect, useRef } from "react";

import { takeSource, trackEvent, type AnalyticsEvent } from "@/lib/analytics/events";

/** Sends one first-party analytics event after the page has rendered (once per mount). Renders nothing. */
export function TrackEvent({ event }: { event: AnalyticsEvent }) {
  const sent = useRef(false);
  const payload = JSON.stringify(event);
  useEffect(() => {
    if (sent.current) return;
    sent.current = true;
    trackEvent(JSON.parse(payload) as AnalyticsEvent);
  }, [payload]);
  return null;
}

const REF = /^[0-9a-hjkmnp-tv-z]{12}$/;

/**
 * Counts one campaign view with its traffic source and, when the page was opened from a referral link (`?ref=`), the
 * referral code. The code is then removed from the address bar (history.replaceState), so a link copied from the page
 * is the clean canonical one. Renders nothing.
 */
export function TrackCampaignView({ slug }: { slug: string }) {
  const sent = useRef(false);
  useEffect(() => {
    if (sent.current) return;
    sent.current = true;
    const url = new URL(window.location.href);
    const ref = url.searchParams.get("ref") ?? "";
    const source = takeSource(slug, document.referrer, window.location.origin);
    trackEvent({ type: "CAMPAIGN_VIEW", slug, source, ...(REF.test(ref) ? { ref } : {}) });
    if (url.searchParams.has("ref")) {
      url.searchParams.delete("ref");
      window.history.replaceState(window.history.state, "", url.pathname + (url.search || "") + url.hash);
    }
  }, [slug]);
  return null;
}
