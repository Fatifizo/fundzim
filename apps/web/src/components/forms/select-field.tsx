"use client";

import { forwardRef, useId, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";

import { cx } from "@/lib/cx";

import { inputClasses } from "./text-field";

function FieldShell({
  inputId,
  label,
  hint,
  error,
  className,
  children,
}: {
  inputId: string;
  label: string;
  hint?: ReactNode;
  error?: string;
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cx("space-y-1", className)}>
      <label htmlFor={inputId} className="block font-semibold text-ink-900">
        {label}
      </label>
      {hint ? (
        <p id={`${inputId}-hint`} className="text-sm text-ink-600">
          {hint}
        </p>
      ) : null}
      {children}
      {error ? (
        <p id={`${inputId}-error`} className="text-sm font-semibold text-danger-700">
          <span aria-hidden="true">Error: </span>
          {error}
        </p>
      ) : null}
    </div>
  );
}

function describedBy(inputId: string, hint: unknown, error: unknown): string | undefined {
  return [error ? `${inputId}-error` : null, hint ? `${inputId}-hint` : null].filter(Boolean).join(" ") || undefined;
}

export interface SelectFieldProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, "id"> {
  label: string;
  name: string;
  options: ReadonlyArray<{ value: string; label: string }>;
  hint?: ReactNode;
  error?: string;
  id?: string;
  /** Adds a first empty option with this text (e.g. "Choose…"). */
  placeholderOption?: string;
}

/** Labelled <select>; hint and error linked with aria-describedby, aria-invalid on error (as TextField). */
export const SelectField = forwardRef<HTMLSelectElement, SelectFieldProps>(function SelectField(
  { label, name, options, hint, error, id, className, placeholderOption, ...props },
  ref,
) {
  const generated = useId();
  const inputId = id ?? `${name}-${generated}`;
  return (
    <FieldShell inputId={inputId} label={label} hint={hint} error={error} className={className}>
      <select
        ref={ref}
        id={inputId}
        name={name}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(inputId, hint, error)}
        className={inputClasses}
        {...props}
      >
        {placeholderOption !== undefined ? <option value="">{placeholderOption}</option> : null}
        {options.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    </FieldShell>
  );
});

export interface TextAreaFieldProps extends Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "id"> {
  label: string;
  name: string;
  hint?: ReactNode;
  error?: string;
  id?: string;
}

export const TextAreaField = forwardRef<HTMLTextAreaElement, TextAreaFieldProps>(function TextAreaField(
  { label, name, hint, error, id, className, rows = 4, ...props },
  ref,
) {
  const generated = useId();
  const inputId = id ?? `${name}-${generated}`;
  return (
    <FieldShell inputId={inputId} label={label} hint={hint} error={error} className={className}>
      <textarea
        ref={ref}
        id={inputId}
        name={name}
        rows={rows}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy(inputId, hint, error)}
        className={inputClasses}
        {...props}
      />
    </FieldShell>
  );
});

/** A group of checkboxes in a fieldset with a legend; the error is linked to the fieldset. */
export function CheckboxGroup({
  legend,
  name,
  options,
  defaultValues = [],
  hint,
  error,
}: {
  legend: string;
  name: string;
  options: ReadonlyArray<{ value: string; label: string }>;
  defaultValues?: readonly string[];
  hint?: ReactNode;
  error?: string;
}) {
  const id = useId();
  return (
    <fieldset
      aria-describedby={describedBy(id, hint, error)}
      aria-invalid={error ? true : undefined}
      className="space-y-2"
      tabIndex={error ? -1 : undefined}
    >
      <legend className="font-semibold text-ink-900">{legend}</legend>
      {hint ? (
        <p id={`${id}-hint`} className="text-sm text-ink-600">
          {hint}
        </p>
      ) : null}
      <div className="grid gap-1 sm:grid-cols-2">
        {options.map((option) => (
          <label key={option.value} className="flex min-h-11 items-center gap-3 text-ink-900">
            <input type="checkbox" name={name} value={option.value} defaultChecked={defaultValues.includes(option.value)} className="size-5 accent-brand-700" />
            {option.label}
          </label>
        ))}
      </div>
      {error ? (
        <p id={`${id}-error`} className="text-sm font-semibold text-danger-700">
          <span aria-hidden="true">Error: </span>
          {error}
        </p>
      ) : null}
    </fieldset>
  );
}

export function toOptions(labels: Record<string, string>, values?: readonly string[]): Array<{ value: string; label: string }> {
  return (values ?? Object.keys(labels)).map((value) => ({ value, label: labels[value] ?? value }));
}
