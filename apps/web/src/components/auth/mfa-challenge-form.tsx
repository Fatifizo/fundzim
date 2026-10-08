"use client";

import Link from "next/link";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { FormError } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { afterLoginPath } from "@/lib/auth/safe-redirect";
import { AuthErrorCode } from "@/lib/auth/types";
import { browserNavigation } from "@/lib/browser-navigation";

import { useFormFeedback } from "./use-form-feedback";

type Mode = "totp" | "recovery";

/**
 * Second sign-in step after a correct password: a TOTP code from an authenticator app, or a one-time
 * recovery code. The pending challenge lives in the HttpOnly `fz_mfa` cookie; nothing is stored here.
 */
export function MfaChallengeForm({ next, api = authApi }: { next?: string | null; api?: AuthApi }) {
  const [mode, setMode] = useState<Mode>("totp");
  const [busy, setBusy] = useState(false);
  const [expired, setExpired] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();
  const inputRef = useRef<HTMLInputElement>(null);
  const switched = useRef(false);

  useEffect(() => {
    if (switched.current) inputRef.current?.focus();
  }, [mode]);

  function switchMode() {
    switched.current = true;
    clear();
    setMode((current) => (current === "totp" ? "recovery" : "totp"));
  }

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const raw = String(data.get(mode === "totp" ? "code" : "recovery_code") ?? "");
    const value = mode === "totp" ? raw.replace(/\s+/g, "") : raw.trim();

    if (mode === "totp" && !/^\d{6,8}$/.test(value)) {
      fail("Please correct the errors below.", { code: "Enter the 6-digit code from your authenticator app." });
      return;
    }
    if (mode === "recovery" && value.length < 6) {
      fail("Please correct the errors below.", { recovery_code: "Enter one of your recovery codes." });
      return;
    }

    clear();
    setBusy(true);
    try {
      if (mode === "totp") await api.mfaVerify(value);
      else await api.mfaRecovery(value);
      browserNavigation.assign(afterLoginPath(next));
      return;
    } catch (error) {
      const described = failWith(error, "We couldn't verify the code. Please try again.");
      if (described?.code === AuthErrorCode.MFA_CHALLENGE_EXPIRED) setExpired(true);
    }
    setBusy(false);
  }

  if (expired) {
    return (
      <div className="space-y-4">
        <FormError ref={errorRef} message={formError} />
        <Link href="/login" className="inline-flex min-h-11 items-center rounded-full bg-brand-700 px-5 font-semibold text-white hover:bg-brand-800">
          Sign in again
        </Link>
      </div>
    );
  }

  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5">
      <FormError ref={errorRef} message={formError} />
      {mode === "totp" ? (
        <TextField
          ref={inputRef}
          key="totp"
          label="Authentication code"
          name="code"
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern="[0-9]*"
          maxLength={8}
          hint="Open your authenticator app and enter the 6-digit code for FundZim."
          required
          error={fieldErrors.code}
        />
      ) : (
        <TextField
          ref={inputRef}
          key="recovery"
          label="Recovery code"
          name="recovery_code"
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          hint="Each recovery code works only once."
          required
          error={fieldErrors.recovery_code}
        />
      )}
      <div className="flex flex-wrap items-center gap-3">
        <SubmitButton busy={busy} busyLabel="Verifying…">
          Verify and sign in
        </SubmitButton>
        <Button variant="ghost" onClick={switchMode}>
          {mode === "totp" ? "Use a recovery code instead" : "Use an authentication code instead"}
        </Button>
      </div>
    </form>
  );
}
