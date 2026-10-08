import type { ReactNode } from "react";

import { cx } from "@/lib/cx";

export type AlertTone = "info" | "notice" | "danger";

const tones: Record<AlertTone, string> = {
  info: "border-info-700/30 bg-info-50 text-info-700",
  notice: "border-gold-500/50 bg-gold-100 text-gold-800",
  danger: "border-danger-700/30 bg-danger-50 text-danger-700",
};

export interface AlertProps {
  tone?: AlertTone;
  title: string;
  children?: ReactNode;
  /** Announce to screen readers when it appears dynamically. Static page notices should not. */
  live?: boolean;
  className?: string;
}

export function Alert({ tone = "info", title, children, live = false, className }: AlertProps) {
  return (
    <div role={live ? "alert" : undefined} className={cx("rounded-xl border-l-4 border p-4", tones[tone], className)}>
      <p className="font-semibold">{title}</p>
      {children ? <div className="mt-1 text-ink-700">{children}</div> : null}
    </div>
  );
}
