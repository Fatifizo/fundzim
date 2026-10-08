import { cx } from "@/lib/cx";

/**
 * FundZim wordmark. Original artwork: a rising sun / open hand motif in gold over a green field.
 * Decorative SVG; the accessible name comes from the surrounding link or the visible text.
 */
export function Logo({ className, inverted = false }: { className?: string; inverted?: boolean }) {
  return (
    <span className={cx("inline-flex items-center gap-2", className)}>
      <svg aria-hidden="true" viewBox="0 0 32 32" className="size-8 shrink-0">
        <rect width="32" height="32" rx="9" fill={inverted ? "var(--color-gold-400)" : "var(--color-brand-700)"} />
        <path
          d="M7 21.5a9 9 0 0 1 18 0"
          fill="none"
          stroke={inverted ? "var(--color-brand-900)" : "var(--color-gold-400)"}
          strokeWidth="3"
          strokeLinecap="round"
        />
        <path
          d="M16 8v6M10 10.5l2.2 4.3M22 10.5l-2.2 4.3"
          stroke={inverted ? "var(--color-brand-900)" : "var(--color-gold-400)"}
          strokeWidth="2.4"
          strokeLinecap="round"
        />
        <path d="M5 24.5h22" stroke={inverted ? "var(--color-brand-900)" : "#ffffff"} strokeWidth="2.4" strokeLinecap="round" />
      </svg>
      <span className={cx("font-display text-xl font-bold tracking-tight", inverted ? "text-white" : "text-ink-900")}>
        Fund<span className={inverted ? "text-gold-400" : "text-brand-700"}>Zim</span>
      </span>
    </span>
  );
}
