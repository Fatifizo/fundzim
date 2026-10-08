import type { ReactNode } from "react";

import { cx } from "@/lib/cx";

export type BadgeTone = "neutral" | "brand" | "gold" | "info";

const tones: Record<BadgeTone, string> = {
  neutral: "bg-surface-muted text-ink-700 border-line",
  brand: "bg-brand-50 text-brand-800 border-brand-100",
  gold: "bg-gold-100 text-gold-800 border-gold-200",
  info: "bg-info-50 text-info-700 border-info-50",
};

/** Text label; meaning is always carried by the text, never by colour alone. */
export function Badge({ tone = "neutral", children, className }: { tone?: BadgeTone; children: ReactNode; className?: string }) {
  return (
    <span
      className={cx(
        "inline-flex items-center rounded-full border px-3 py-0.5 text-sm font-semibold leading-6",
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}
