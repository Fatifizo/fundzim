"use client";

import { forwardRef, type ReactNode } from "react";

/**
 * Form-level error/status message. Rendered with role="alert" so it is announced, and focusable
 * (tabIndex=-1) so a form can move focus to it after a failed submit.
 */
export const FormError = forwardRef<HTMLDivElement, { message?: string | null; id?: string }>(function FormError(
  { message, id },
  ref,
) {
  if (!message) return null;
  return (
    <div
      ref={ref}
      id={id}
      role="alert"
      tabIndex={-1}
      className="rounded-xl border border-l-4 border-danger-700/30 bg-danger-50 p-4 font-semibold text-danger-700 focus:outline-none focus-visible:outline-3"
    >
      {message}
    </div>
  );
});

/** Polite success/status message. Focusable so flows can move focus to the outcome. */
export const FormStatus = forwardRef<HTMLDivElement, { title: string; children?: ReactNode; id?: string }>(
  function FormStatus({ title, children, id }, ref) {
    return (
      <div
        ref={ref}
        id={id}
        role="status"
        tabIndex={-1}
        className="rounded-xl border border-l-4 border-brand-700/30 bg-brand-50 p-4 text-ink-900 focus:outline-none focus-visible:outline-3"
      >
        <p className="font-semibold text-brand-800">{title}</p>
        {children ? <div className="mt-1 text-ink-700">{children}</div> : null}
      </div>
    );
  },
);
