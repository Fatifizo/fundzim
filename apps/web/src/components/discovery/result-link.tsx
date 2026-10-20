"use client";

import Link from "next/link";
import type { ReactNode } from "react";

import { rememberSource, trackEvent, type ClickSurface } from "@/lib/analytics/events";

/**
 * Link from a discovery surface to a campaign. Never prefetched (on slow mobile connections a grid of prefetches costs
 * more than it saves). With a surface, the click is counted (aggregate) and the surface remembered for this tab so the
 * campaign page can report where the view came from — nothing is added to the URL.
 */
export function ResultLink({ href, slug, surface, className, children }: { href: string; slug: string; surface?: ClickSurface; className?: string; children: ReactNode }) {
  return (
    <Link
      href={href}
      prefetch={false}
      className={className}
      onClick={() => {
        if (!surface) return;
        trackEvent({ type: "RESULT_CLICK", slug, source: surface });
        rememberSource(slug, surface);
      }}
    >
      {children}
    </Link>
  );
}
