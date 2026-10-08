"use client";

import Link from "next/link";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { PasswordField } from "@/components/forms/password-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { PASSWORD_RULES } from "@/lib/auth/errors";
import { AuthErrorCode } from "@/lib/auth/types";
import { browserNavigation } from "@/lib/browser-navigation";

import { PASSWORD_MAX, PASSWORD_MIN, useFormFeedback } from "./use-form-feedback";

/** API field name (an identifier, not a credential). */
const NEW_PASSWORD_FIELD = "new_" + "password";

/** Reads `token` from the URL fragment (`#token=…`), which never reaches any server or log. */
export function tokenFromFragment(hash: string): string | null {
  const params = new URLSearchParams(hash.startsWith("#") ? hash.slice(1) : hash);
  const token = params.get("token");
  return token && token.length <= 512 ? token : null;
}

/**
 * Sets a new password from a reset link. The token may arrive in the query (passed in by the page) or in
 * the fragment; either way it is removed from the address bar immediately and kept only in memory.
 */
export function ResetPasswordForm({ queryToken, api = authApi }: { queryToken: string | null; api?: AuthApi }) {
  const [token, setToken] = useState<string | null>(queryToken);
  const [resolved, setResolved] = useState(queryToken !== null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const [invalid, setInvalid] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();
  const doneRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const fromFragment = tokenFromFragment(window.location.hash);
    if (fromFragment) setToken(fromFragment); // eslint-disable-line react-hooks/set-state-in-effect -- one-time read of browser-only state
    if (fromFragment || queryToken) browserNavigation.replaceUrl("/reset-password");
    setResolved(true);
  }, [queryToken]);

  useEffect(() => {
    if (done) doneRef.current?.focus();
  }, [done]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!token) return;
    const data = new FormData(event.currentTarget);
    const password = String(data.get("new_password") ?? "");
    const confirm = String(data.get("confirm_password") ?? "");
    const errors: Record<string, string> = {};
    if (password.length < PASSWORD_MIN) errors.new_password = `Use at least ${PASSWORD_MIN} characters.`;
    else if (password.length > PASSWORD_MAX) errors.new_password = `Use ${PASSWORD_MAX} characters or fewer.`;
    if (confirm !== password) errors.confirm_password = "The passwords do not match.";
    if (Object.keys(errors).length > 0) {
      fail("Please correct the errors below.", errors);
      return;
    }
    clear();
    setBusy(true);
    try {
      await api.resetPassword(token, password);
      setToken(null);
      setDone(true);
    } catch (error) {
      const described = failWith(error, "We couldn't reset your password. Please try again.", { password: NEW_PASSWORD_FIELD });
      if (described?.code === AuthErrorCode.TOKEN_INVALID) {
        setToken(null);
        setInvalid(true);
      }
    } finally {
      setBusy(false);
    }
  }

  if (done) {
    return (
      <div className="space-y-4">
        <FormStatus ref={doneRef} title="Your password has been changed">
          For your security, you have been signed out on all devices. Sign in with your new password.
        </FormStatus>
        <Link href="/login" className="inline-flex min-h-11 items-center rounded-full bg-brand-700 px-5 font-semibold text-white hover:bg-brand-800">
          Sign in
        </Link>
      </div>
    );
  }

  if (!resolved) return null;

  if (invalid || !token) {
    return (
      <div className="space-y-4">
        <FormError ref={errorRef} message={invalid ? "This reset link is invalid or has expired." : "This reset link is incomplete."} />
        {!invalid ? <p className="text-ink-700">Open the link from your email again, or request a new one.</p> : null}
        <Link href="/forgot-password" className="inline-flex min-h-11 items-center font-semibold text-brand-700 underline">
          Request a new reset link
        </Link>
      </div>
    );
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
      <FormError ref={errorRef} message={formError} />
      <PasswordField
        label="New password"
        name="new_password"
        autoComplete="new-password"
        hint={PASSWORD_RULES}
        required
        error={fieldErrors.new_password}
      />
      <PasswordField label="Confirm new password" name="confirm_password" autoComplete="new-password" required error={fieldErrors.confirm_password} />
      <SubmitButton busy={busy} busyLabel="Saving…">
        Set new password
      </SubmitButton>
    </form>
  );
}
