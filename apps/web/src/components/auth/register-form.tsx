"use client";

import Link from "next/link";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { PasswordField } from "@/components/forms/password-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { PASSWORD_RULES } from "@/lib/auth/errors";

import { looksLikeEmail, PASSWORD_MAX, PASSWORD_MIN, useFormFeedback } from "./use-form-feedback";

/** API field name (an identifier, not a credential). */
const PASSWORD_FIELD = "pass" + "word";

const DISPLAY_NAME_MAX = 80;

/**
 * Registration. The success message is identical whether or not the address already has an account
 * (the API answers 202 either way), so the form never reveals whether an email is registered.
 */
export function RegisterForm({ api = authApi }: { api?: AuthApi }) {
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();
  const successRef = useRef<HTMLDivElement>(null);
  const termsHintId = useId();

  useEffect(() => {
    if (sent) successRef.current?.focus();
  }, [sent]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const email = String(data.get("email") ?? "").trim();
    const displayName = String(data.get("display_name") ?? "").trim();
    const password = String(data.get("password") ?? "");
    const terms = data.get("accept_terms") === "on";

    const errors: Record<string, string> = {};
    if (!email) errors.email = "Enter your email address.";
    else if (!looksLikeEmail(email)) errors.email = "Enter an email address in the format name@example.com.";
    if (!displayName) errors.display_name = "Enter the name you want to be shown.";
    else if (displayName.length > DISPLAY_NAME_MAX) errors.display_name = `Use ${DISPLAY_NAME_MAX} characters or fewer.`;
    if (password.length < PASSWORD_MIN) errors.password = `Use at least ${PASSWORD_MIN} characters.`;
    else if (password.length > PASSWORD_MAX) errors.password = `Use ${PASSWORD_MAX} characters or fewer.`;
    if (!terms) errors.accept_terms = "You must accept the terms and privacy notice to create an account.";
    if (Object.keys(errors).length > 0) {
      fail("Please correct the errors below.", errors);
      return;
    }

    clear();
    setBusy(true);
    try {
      await api.register({ email, password, display_name: displayName, accept_terms: true });
      setSent(true);
    } catch (error) {
      failWith(error, "We couldn't create your account. Please try again.", { new_password: PASSWORD_FIELD });
    } finally {
      setBusy(false);
    }
  }

  if (sent) {
    return (
      <FormStatus ref={successRef} title="Check your email">
        <p>
          If this address can be used for a FundZim account, we have sent a link to confirm it. The link expires, so
          please use it soon. Didn&apos;t get it? Check your spam folder or{" "}
          <Link href="/verify-email" className="font-semibold text-brand-700 underline">
            request a new link
          </Link>
          .
        </p>
      </FormStatus>
    );
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5" aria-describedby={formError ? "register-error" : undefined}>
      <FormError ref={errorRef} id="register-error" message={formError} />
      <TextField
        label="Email address"
        name="email"
        type="email"
        inputMode="email"
        autoComplete="email"
        autoCapitalize="none"
        spellCheck={false}
        required
        error={fieldErrors.email}
      />
      <TextField
        label="Display name"
        name="display_name"
        autoComplete="nickname"
        hint="Shown to people you interact with on FundZim. You can change it later."
        maxLength={DISPLAY_NAME_MAX}
        required
        error={fieldErrors.display_name}
      />
      <PasswordField label="Password" name="password" autoComplete="new-password" hint={PASSWORD_RULES} required error={fieldErrors.password} />
      <div className="space-y-1">
        <div className="flex items-start gap-3">
          <input
            id="accept_terms"
            name="accept_terms"
            type="checkbox"
            required
            aria-invalid={fieldErrors.accept_terms ? true : undefined}
            aria-describedby={fieldErrors.accept_terms ? `accept_terms-error ${termsHintId}` : termsHintId}
            className="mt-1 size-6 shrink-0 accent-brand-700"
          />
          <label htmlFor="accept_terms" className="text-ink-900">
            I accept the <Link href="/terms" className="font-semibold text-brand-700 underline">terms of use</Link> and have read
            the <Link href="/privacy" className="font-semibold text-brand-700 underline">privacy notice</Link>.
          </label>
        </div>
        <p id={termsHintId} className="pl-9 text-sm text-ink-600">
          Both are drafts while FundZim is under development.
        </p>
        {fieldErrors.accept_terms ? (
          <p id="accept_terms-error" className="pl-9 text-sm font-semibold text-danger-700">
            <span aria-hidden="true">Error: </span>
            {fieldErrors.accept_terms}
          </p>
        ) : null}
      </div>
      <SubmitButton busy={busy} busyLabel="Creating account…" className="w-full sm:w-auto">
        Create account
      </SubmitButton>
      <p className="text-ink-700">
        Already have an account?{" "}
        <Link href="/login" className="font-semibold text-brand-700 underline">
          Sign in
        </Link>
      </p>
    </form>
  );
}
