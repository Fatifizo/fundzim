import type { ReactNode } from "react";

import { cx } from "@/lib/cx";

export function EmptyState({ title, description, action, className }: { title: string; description?: ReactNode; action?: ReactNode; className?: string }) {
  return (
    <div className={cx("rounded-card border-2 border-dashed border-line-strong/60 bg-surface px-6 py-10 text-center", className)}>
      <p className="font-display text-xl font-semibold text-ink-900">{title}</p>
      {description ? <div className="mx-auto mt-2 max-w-prose text-ink-600">{description}</div> : null}
      {action ? <div className="mt-6 flex justify-center">{action}</div> : null}
    </div>
  );
}
