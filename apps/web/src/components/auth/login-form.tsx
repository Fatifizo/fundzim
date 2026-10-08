"use client";

import Link from "next/link";
import { useState, type FormEvent } from "react";

import { FormError } from "@/components/forms/form-error";
import { PasswordField } from "@/components/forms/password-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { afterLoginPath, loginUrl } from "@/lib/auth/safe-redirect";
import { browserNavigation } from "@/lib/browser-navigation";

import { looksLikeEmail, useFormFeedback } from "./use-form-feedback";

/**
 * Email + password sign-in. INVALID_CREDENTIALS is shown with one generic message (no hint whether the
 * email exists). `mfa_required` continues at /login/mfa, keeping the validated `next` destination.
 */
export function LoginForm({ next, api = authApi }: { next?: string | null; api?: AuthApi }) {
  const [busy, setBusy] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();
  const destination = afterLoginPath(next);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const email = String(data.get("email") ?? "").trim();
    const password = String(data.get("password") ?? "");

    const errors: Record<string, string> = {};
    if (!email) errors.email = "Enter your email address.";
    else if (!looksLikeEmail(email)) errors.email = "Enter an email address in the format name@example.com.";
    if (!password) errors.password = "Enter your password.";
    if (Object.keys(errors).length > 0) {
      fail("Please correct the errors below.", errors);
      return;
    }

    clear();
    setBusy(true);
    try {
      const result = await api.login(email, password);
      if (result.status === "mfa_required") {
        browserNavigation.assign(loginUrl(destination, "/login/mfa"));
        return;
      }
      browserNavigation.assign(destination);
      // Keep the busy state: the page is navigating away.
      return;
    } catch (error) {
      failWith(error, "We couldn't sign you in. Please try again.");
    }
    setBusy(false);
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
      <FormError ref={errorRef} message={formError} />
      <TextField
        label="Email address"
        name="email"
        type="email"
        inputMode="email"
        autoComplete="username"
        autoCapitalize="none"
        spellCheck={false}
        required
        error={fieldErrors.email}
      />
      <PasswordField label="Password" name="password" autoComplete="current-password" required error={fieldErrors.password} />
      <div className="flex flex-wrap items-center justify-between gap-4">
        <SubmitButton busy={busy} busyLabel="Signing in…">
          Sign in
        </SubmitButton>
        <Link href="/forgot-password" className="font-semibold text-brand-700 underline">
          Forgot your password?
        </Link>
      </div>
      <p className="text-ink-700">
        New to FundZim?{" "}
        <Link href="/register" className="font-semibold text-brand-700 underline">
          Create an account
        </Link>
      </p>
    </form>
  );
}
