import type { ReactNode } from "react";

import { cx } from "@/lib/cx";

export interface ErrorStateProps {
  title?: string;
  description?: ReactNode;
  /** Reference the user can quote to support (e.g. an error digest or request id). Never internals. */
  reference?: string;
  action?: ReactNode;
  className?: string;
}

export function ErrorState({
  title = "Something went wrong",
  description = "Please try again. If the problem continues, come back a little later.",
  reference,
  action,
  className,
}: ErrorStateProps) {
  return (
    <div role="alert" className={cx("rounded-card border border-danger-700/30 bg-danger-50 p-6", className)}>
      <p className="font-display text-xl font-semibold text-danger-700">{title}</p>
      <div className="mt-2 text-ink-700">{description}</div>
      {reference ? (
        <p className="mt-3 text-sm text-ink-600">
          Reference: <code className="font-mono">{reference}</code>
        </p>
      ) : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}
