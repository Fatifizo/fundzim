"use client";

import { useState, type FormEvent } from "react";

import { PASSWORD_MAX, PASSWORD_MIN, useFormFeedback } from "@/components/auth/use-form-feedback";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { PasswordField } from "@/components/forms/password-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { PASSWORD_RULES } from "@/lib/auth/errors";

/** API field name (an identifier, not a credential). */
const NEW_PASSWORD_FIELD = "new_" + "password";

/** POST /me/password. The API signs out the user's other sessions on success. */
export function ChangePasswordForm({ email, api = authApi }: { email: string; api?: AuthApi }) {
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setSaved(false);
    const form = event.currentTarget;
    const data = new FormData(form);
    const current = String(data.get("current_password") ?? "");
    const next = String(data.get("new_password") ?? "");
    const confirm = String(data.get("confirm_password") ?? "");
    const errors: Record<string, string> = {};
    if (!current) errors.current_password = "Enter your current password.";
    if (next.length < PASSWORD_MIN) errors.new_password = `Use at least ${PASSWORD_MIN} characters.`;
    else if (next.length > PASSWORD_MAX) errors.new_password = `Use ${PASSWORD_MAX} characters or fewer.`;
    if (confirm !== next) errors.confirm_password = "The passwords do not match.";
    if (Object.keys(errors).length > 0) return fail("Please correct the errors below.", errors);
    clear();
    setBusy(true);
    try {
      await api.changePassword(current, next);
      form.reset();
      setSaved(true);
    } catch (error) {
      failWith(error, "We couldn't change your password. Please try again.", { password: NEW_PASSWORD_FIELD });
    } finally {
      setBusy(false);
    }
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
      {/* Lets password managers associate the new password with the right account. */}
      <input type="email" name="username" autoComplete="username" value={email} readOnly hidden />
      <FormError ref={errorRef} message={formError} />
      {saved ? <FormStatus title="Password changed">You have been signed out on your other devices.</FormStatus> : null}
      <PasswordField label="Current password" name="current_password" autoComplete="current-password" required error={fieldErrors.current_password} />
      <PasswordField label="New password" name="new_password" autoComplete="new-password" hint={PASSWORD_RULES} required error={fieldErrors.new_password} />
      <PasswordField label="Confirm new password" name="confirm_password" autoComplete="new-password" required error={fieldErrors.confirm_password} />
      <SubmitButton busy={busy} busyLabel="Changing…">
        Change password
      </SubmitButton>
    </form>
  );
}
