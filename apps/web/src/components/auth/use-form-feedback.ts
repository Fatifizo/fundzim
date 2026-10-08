"use client";

import { useCallback, useEffect, useRef, useState } from "react";

import { browserNavigation } from "@/lib/browser-navigation";
import { describeError, isAuthRequired, type DescribedError } from "@/lib/auth/errors";
import { loginUrl } from "@/lib/auth/safe-redirect";

/**
 * Error state + focus management shared by the auth forms:
 *  - `fail()` stores a form-level message and field errors, then moves focus to the first invalid field
 *    (or to the form-level alert when there are none) after the next render.
 *  - An AUTHENTICATION_REQUIRED (401) response means the session ended: redirect to /login?next=<here>.
 */
export function useFormFeedback() {
  const [formError, setFormError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [retryAfterSeconds, setRetryAfterSeconds] = useState<number | undefined>(undefined);
  const errorRef = useRef<HTMLDivElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const [focusTick, setFocusTick] = useState(0);

  useEffect(() => {
    if (focusTick === 0) return;
    const invalid = formRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]');
    if (invalid) invalid.focus();
    else errorRef.current?.focus();
  }, [focusTick]);

  const clear = useCallback(() => {
    setFormError(null);
    setFieldErrors({});
    setRetryAfterSeconds(undefined);
  }, []);

  const fail = useCallback((message: string | null, fields: Record<string, string> = {}, retryAfter?: number) => {
    setFormError(message);
    setFieldErrors(fields);
    setRetryAfterSeconds(retryAfter);
    setFocusTick((tick) => tick + 1);
  }, []);

  /** Maps an API error onto the form. Returns the described error (or null if it redirected). */
  const failWith = useCallback(
    (error: unknown, fallback?: string, fieldMap: Record<string, string> = {}): DescribedError | null => {
      if (isAuthRequired(error)) {
        browserNavigation.assign(loginUrl(browserNavigation.currentPath()));
        return null;
      }
      const described = describeError(error, fallback);
      const fields: Record<string, string> = {};
      for (const [apiField, text] of Object.entries(described.fieldErrors)) fields[fieldMap[apiField] ?? apiField] = text;
      const onlyFields = Object.keys(fields).length > 0 && described.code !== "RATE_LIMITED";
      fail(onlyFields ? "Please correct the errors below." : described.message, fields, described.retryAfterSeconds);
      return described;
    },
    [fail],
  );

  return { formError, fieldErrors, retryAfterSeconds, errorRef, formRef, clear, fail, failWith };
}

/** Minimal client-side email check (the API is authoritative). */
export function looksLikeEmail(value: string): boolean {
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value) && value.length <= 254;
}

export const PASSWORD_MIN = 12;
export const PASSWORD_MAX = 256;
