"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { authApi, type AuthApi } from "@/lib/auth/api";

import { looksLikeEmail, useFormFeedback } from "./use-form-feedback";

/** Password reset request. Always shows the same message: it never reveals whether an account exists. */
export function ForgotPasswordForm({ api = authApi }: { api?: AuthApi }) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();
  const doneRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (done) doneRef.current?.focus();
  }, [done]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const email = String(new FormData(event.currentTarget).get("email") ?? "").trim();
    if (!looksLikeEmail(email)) {
      fail("Please correct the errors below.", { email: email ? "Enter an email address in the format name@example.com." : "Enter your email address." });
      return;
    }
    clear();
    setBusy(true);
    try {
      await api.forgotPassword(email);
      setDone(true);
    } catch (error) {
      failWith(error, "We couldn't send the request. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  if (done) {
    return (
      <FormStatus ref={doneRef} title="Check your email">
        <p>
          If an account exists for that address, we have sent a link to reset the password. The link expires soon
          and works once. If nothing arrives, check your spam folder or try again later.
        </p>
      </FormStatus>
    );
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
      <FormError ref={errorRef} message={formError} />
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
      <SubmitButton busy={busy} busyLabel="Sending…">
        Send reset link
      </SubmitButton>
    </form>
  );
}
