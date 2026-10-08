"use client";

import { useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { authApi, type AuthApi } from "@/lib/auth/api";

import { looksLikeEmail, useFormFeedback } from "./use-form-feedback";

/** Asks for a new verification email. Generic outcome (no account enumeration). */
export function ResendVerificationForm({ api = authApi, defaultEmail }: { api?: AuthApi; defaultEmail?: string }) {
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();

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
      await api.resendVerification(email);
      setDone(true);
    } catch (error) {
      failWith(error, "We couldn't send a new link. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-4">
      <FormError ref={errorRef} message={formError} />
      {done ? (
        <FormStatus title="Check your email">
          If this address belongs to an account that still needs confirming, a new link is on its way.
        </FormStatus>
      ) : null}
      <TextField
        label="Email address"
        name="email"
        type="email"
        inputMode="email"
        autoComplete="email"
        autoCapitalize="none"
        spellCheck={false}
        defaultValue={defaultEmail}
        required
        error={fieldErrors.email}
      />
      <SubmitButton busy={busy} busyLabel="Sending…" variant="outline">
        Send a new link
      </SubmitButton>
    </form>
  );
}
