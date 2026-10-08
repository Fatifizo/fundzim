"use client";

import { forwardRef, useId, type InputHTMLAttributes, type ReactNode } from "react";

import { cx } from "@/lib/cx";

export interface TextFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "id"> {
  label: string;
  name: string;
  hint?: ReactNode;
  error?: string;
  /** Rendered after the input inside the same row (e.g. a visibility toggle). */
  adornment?: ReactNode;
  id?: string;
}

export const inputClasses =
  "block min-h-11 w-full rounded-xl border-2 border-line-strong bg-surface px-3 py-2 text-base text-ink-900 " +
  "placeholder:text-ink-600 focus-visible:border-brand-700 aria-[invalid=true]:border-danger-700";

/**
 * Labelled input. Hint and error are linked with aria-describedby; an error sets aria-invalid. Errors are
 * rendered as text (never colour alone).
 */
export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(function TextField(
  { label, name, hint, error, adornment, id, className, ...props },
  ref,
) {
  const generated = useId();
  const inputId = id ?? `${name}-${generated}`;
  const hintId = hint ? `${inputId}-hint` : undefined;
  const errorId = error ? `${inputId}-error` : undefined;
  const describedBy = [errorId, hintId].filter(Boolean).join(" ") || undefined;

  return (
    <div className={cx("space-y-1", className)}>
      <label htmlFor={inputId} className="block font-semibold text-ink-900">
        {label}
      </label>
      {hint ? (
        <p id={hintId} className="text-sm text-ink-600">
          {hint}
        </p>
      ) : null}
      <div className="flex items-stretch gap-2">
        <input
          ref={ref}
          id={inputId}
          name={name}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={inputClasses}
          {...props}
        />
        {adornment}
      </div>
      {error ? (
        <p id={errorId} className="text-sm font-semibold text-danger-700">
          <span aria-hidden="true">Error: </span>
          {error}
        </p>
      ) : null}
    </div>
  );
});
