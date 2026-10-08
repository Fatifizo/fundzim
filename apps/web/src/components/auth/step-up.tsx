"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";

import { FormError } from "@/components/forms/form-error";
import { PasswordField } from "@/components/forms/password-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { isStepUpRequired } from "@/lib/auth/errors";

import { useFormFeedback } from "./use-form-feedback";

/**
 * Step-up re-authentication (interface-contracts §4.2): when the API answers 403 STEP_UP_REQUIRED, ask the
 * user to confirm with their password (or an authenticator code if MFA is on), POST /auth/step-up/verify,
 * then retry the original operation ONCE. Cancelling rejects with StepUpCancelledError.
 *
 * The dialog is a native <dialog> opened with showModal(): the browser provides the focus trap, inert
 * background and Escape-to-close; focus returns to the element that was focused before it opened.
 */
export class StepUpCancelledError extends Error {
  constructor() {
    super("Step-up verification was cancelled.");
    this.name = "StepUpCancelledError";
  }
}

type WithStepUp = <T>(operation: () => Promise<T>) => Promise<T>;

const StepUpContext = createContext<WithStepUp | null>(null);

export function useStepUp(): WithStepUp {
  const value = useContext(StepUpContext);
  if (!value) throw new Error("useStepUp must be used inside <StepUpProvider>");
  return value;
}

interface Pending {
  resolve: () => void;
  reject: (error: unknown) => void;
}

/**
 * `codeOnly`: staff sessions must step up with a TOTP code (the API refuses password-only step-up for staff).
 */
export function StepUpProvider({ children, mfaEnabled, codeOnly = false, api = authApi }: { children: ReactNode; mfaEnabled: boolean; codeOnly?: boolean; api?: AuthApi }) {
  const [pending, setPending] = useState<Pending | null>(null);
  const pendingRef = useRef<Pending | null>(null);

  const requestStepUp = useCallback(
    () =>
      new Promise<void>((resolve, reject) => {
        const entry = { resolve, reject };
        pendingRef.current = entry;
        setPending(entry);
      }),
    [],
  );

  const withStepUp = useCallback<WithStepUp>(
    async (operation) => {
      try {
        return await operation();
      } catch (error) {
        if (!isStepUpRequired(error)) throw error;
        await requestStepUp();
        return operation();
      }
    },
    [requestStepUp],
  );

  const finish = useCallback((outcome: "ok" | "cancel") => {
    const entry = pendingRef.current;
    pendingRef.current = null;
    setPending(null);
    if (!entry) return;
    if (outcome === "ok") entry.resolve();
    else entry.reject(new StepUpCancelledError());
  }, []);

  return (
    <StepUpContext value={withStepUp}>
      {children}
      {pending ? <StepUpDialog api={api} mfaEnabled={mfaEnabled} codeOnly={codeOnly} onDone={() => finish("ok")} onCancel={() => finish("cancel")} /> : null}
    </StepUpContext>
  );
}

function StepUpDialog({ api, mfaEnabled, codeOnly, onDone, onCancel }: { api: AuthApi; mfaEnabled: boolean; codeOnly: boolean; onDone: () => void; onCancel: () => void }) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [useCode, setUseCode] = useState(codeOnly);
  const [busy, setBusy] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();
  const onCancelRef = useRef(onCancel);
  useEffect(() => {
    onCancelRef.current = onCancel;
  }, [onCancel]);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    const previouslyFocused = document.activeElement as HTMLElement | null;
    if (typeof dialog.showModal === "function" && !dialog.open) dialog.showModal();
    else dialog.setAttribute("open", "");
    dialog.querySelector<HTMLInputElement>("input")?.focus();
    const onCancelEvent = (event: Event) => {
      event.preventDefault();
      onCancelRef.current();
    };
    dialog.addEventListener("cancel", onCancelEvent);
    return () => {
      dialog.removeEventListener("cancel", onCancelEvent);
      if (typeof dialog.close === "function" && dialog.open) dialog.close();
      previouslyFocused?.focus?.();
    };
  }, []);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const password = String(data.get("password") ?? "");
    const code = String(data.get("code") ?? "").replace(/\s+/g, "");
    if (useCode ? !/^\d{6,8}$/.test(code) : password === "") {
      fail("Please correct the errors below.", useCode ? { code: "Enter the 6-digit code from your authenticator app." } : { password: "Enter your password." });
      return;
    }
    clear();
    setBusy(true);
    try {
      await api.stepUp(useCode ? { code } : { password });
      onDone();
      return;
    } catch (error) {
      failWith(error, "We couldn't confirm it's you. Please try again.", { code: useCode ? "code" : "password" });
    }
    setBusy(false);
  }

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby="step-up-title"
      aria-describedby="step-up-description"
      className="m-auto w-[min(32rem,calc(100%-2rem))] rounded-card border border-line bg-surface p-6 text-ink-900 shadow-card backdrop:bg-ink-900/60"
    >
      <h2 id="step-up-title" className="font-display text-2xl font-semibold">
        Confirm it&apos;s you
      </h2>
      <p id="step-up-description" className="mt-2 text-ink-700">
        This is a sensitive change. {useCode ? "Enter a code from your authenticator app" : "Enter your password"} to
        continue.
      </p>
      <form ref={formRef} noValidate onSubmit={onSubmit} className="mt-5 space-y-5">
        <FormError ref={errorRef} message={formError} />
        {useCode ? (
          <TextField
            key="code"
            label="Authentication code"
            name="code"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]*"
            maxLength={8}
            required
            error={fieldErrors.code}
          />
        ) : (
          <PasswordField key="password" label="Password" name="password" autoComplete="current-password" required error={fieldErrors.password} />
        )}
        <div className="flex flex-wrap gap-3">
          <SubmitButton busy={busy} busyLabel="Checking…">
            Confirm
          </SubmitButton>
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
          {mfaEnabled && !codeOnly ? (
            <Button
              variant="ghost"
              onClick={() => {
                clear();
                setUseCode((value) => !value);
              }}
            >
              {useCode ? "Use my password instead" : "Use an authentication code instead"}
            </Button>
          ) : null}
        </div>
      </form>
    </dialog>
  );
}
