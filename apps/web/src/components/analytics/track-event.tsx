"use client";

import { useEffect, useRef } from "react";

import { trackEvent, type AnalyticsEvent } from "@/lib/analytics/events";

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
