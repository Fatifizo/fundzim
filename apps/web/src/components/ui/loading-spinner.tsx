import { cx } from "@/lib/cx";

export function LoadingSpinner({ label = "Loading…", className }: { label?: string; className?: string }) {
  return (
    <div role="status" aria-live="polite" className={cx("inline-flex items-center gap-3 text-ink-600", className)}>
      <svg
        aria-hidden="true"
        className="size-6 animate-spin motion-reduce:animate-none"
        viewBox="0 0 24 24"
        fill="none"
      >
        <circle cx="12" cy="12" r="10" stroke="currentColor" strokeOpacity="0.25" strokeWidth="4" />
        <path d="M22 12a10 10 0 0 0-10-10" stroke="var(--color-brand-700)" strokeWidth="4" strokeLinecap="round" />
      </svg>
      <span>{label}</span>
    </div>
  );
}
