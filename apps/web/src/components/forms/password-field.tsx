"use client";

import { forwardRef, useId, useState } from "react";

import { TextField, type TextFieldProps } from "./text-field";

export interface PasswordFieldProps extends Omit<TextFieldProps, "type" | "adornment"> {
  autoComplete: "current-password" | "new-password";
}

/**
 * Password input with a show/hide toggle. The toggle is a real <button> with aria-pressed and
 * aria-controls; its accessible name stays constant ("Show password") while aria-pressed conveys state.
 */
export const PasswordField = forwardRef<HTMLInputElement, PasswordFieldProps>(function PasswordField(
  { name, id, label, ...props },
  ref,
) {
  const [visible, setVisible] = useState(false);
  const generated = useId();
  const inputId = id ?? `${name}-${generated}`;

  return (
    <TextField
      ref={ref}
      id={inputId}
      name={name}
      label={label}
      type={visible ? "text" : "password"}
      spellCheck={false}
      autoCapitalize="none"
      autoCorrect="off"
      {...props}
      adornment={
        <button
          type="button"
          aria-pressed={visible}
          aria-controls={inputId}
          aria-label={`Show ${label.toLowerCase()}`}
          onClick={() => setVisible((value) => !value)}
          className="inline-flex min-h-11 min-w-16 shrink-0 items-center justify-center rounded-xl border-2 border-brand-700 px-3 text-sm font-semibold text-brand-700 hover:bg-brand-50"
        >
          {visible ? "Hide" : "Show"}
        </button>
      }
    />
  );
});
