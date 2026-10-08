"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { useFormFeedback } from "@/components/auth/use-form-feedback";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { authApi, type AuthApi } from "@/lib/auth/api";
import type { MfaEnrollment } from "@/lib/auth/types";

import { QrCode } from "./qr-code";
import { RecoveryCodes } from "./recovery-codes";

/** Groups a base32 secret in blocks of four for manual entry. */
export function formatSecret(secret: string): string {
  return secret.replace(/\s+/g, "").replace(/(.{4})/g, "$1 ").trim();
}

type View =
  | { kind: "overview" }
  | { kind: "enrolling"; enrollment: MfaEnrollment }
  | { kind: "codes"; codes: string[]; reason: "enabled" | "regenerated" };

/**
 * Authenticator-app (TOTP) management: enroll → scan QR / type key → confirm code → recovery codes shown
 * once; regenerate recovery codes; turn off. Sensitive calls go through step-up.
 *
 * The TOTP secret and otpauth URI exist only in this component's state while enrolling. They are dropped
 * the moment enrolment is confirmed or cancelled (and on unmount), and are never logged or stored.
 */
export function MfaSettings({ mfaEnabled, recoveryCodesRemaining, api = authApi }: { mfaEnabled: boolean; recoveryCodesRemaining: number; api?: AuthApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [view, setView] = useState<View>({ kind: "overview" });
  const [busy, setBusy] = useState<null | "enroll" | "confirm" | "regenerate" | "disable">(null);
  const [notice, setNotice] = useState<string | null>(null);
  const {
    formError: enrolError,
    fieldErrors: enrolFields,
    errorRef: enrolErrorRef,
    formRef: enrolFormRef,
    clear: enrolClear,
    fail: enrolFail,
    failWith: enrolFailWith,
  } = useFormFeedback();
  const {
    formError: disableError,
    fieldErrors: disableFields,
    errorRef: disableErrorRef,
    formRef: disableFormRef,
    clear: disableClear,
    fail: disableFail,
    failWith: disableFailWith,
  } = useFormFeedback();
  const { formError: actionError, errorRef: actionErrorRef, clear: actionClear, failWith: actionFailWith } = useFormFeedback();
  const setupHeadingRef = useRef<HTMLHeadingElement>(null);

  useEffect(() => {
    if (view.kind === "enrolling") setupHeadingRef.current?.focus();
  }, [view.kind]);

  async function startEnrollment() {
    setNotice(null);
    actionClear();
    setBusy("enroll");
    try {
      const enrollment = await withStepUp(() => api.mfaEnroll());
      setView({ kind: "enrolling", enrollment });
    } catch (error) {
      if (!(error instanceof StepUpCancelledError)) actionFailWith(error, "We couldn't start the set-up. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  async function confirmEnrollment(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (view.kind !== "enrolling") return;
    const code = String(new FormData(event.currentTarget).get("code") ?? "").replace(/\s+/g, "");
    if (!/^\d{6,8}$/.test(code)) return enrolFail("Please correct the errors below.", { code: "Enter the 6-digit code shown in your authenticator app." });
    enrolClear();
    setBusy("confirm");
    const enrollmentId = view.enrollment.enrollment_id;
    try {
      const result = await withStepUp(() => api.mfaConfirm(enrollmentId, code));
      // Replacing the view drops the secret and URI from memory.
      setView({ kind: "codes", codes: result.recovery_codes, reason: "enabled" });
    } catch (error) {
      if (!(error instanceof StepUpCancelledError)) enrolFailWith(error, "We couldn't confirm the code. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  async function regenerate() {
    setNotice(null);
    actionClear();
    setBusy("regenerate");
    try {
      const result = await withStepUp(() => api.regenerateRecoveryCodes());
      setView({ kind: "codes", codes: result.recovery_codes, reason: "regenerated" });
    } catch (error) {
      if (!(error instanceof StepUpCancelledError)) actionFailWith(error, "We couldn't create new recovery codes. Please try again.");
    } finally {
      setBusy(null);
    }
  }

  async function turnOff(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const code = String(new FormData(form).get("disable_code") ?? "").trim();
    if (code.length < 6) return disableFail("Please correct the errors below.", { disable_code: "Enter a code from your authenticator app or a recovery code." });
    disableClear();
    setBusy("disable");
    try {
      await withStepUp(() => api.mfaDisable(code));
      form.reset();
      setNotice("Two-step verification is now off.");
      router.refresh();
    } catch (error) {
      if (!(error instanceof StepUpCancelledError)) disableFailWith(error, "We couldn't turn off two-step verification. Please try again.", { code: "disable_code" });
    } finally {
      setBusy(null);
    }
  }

  if (view.kind === "codes") {
    return (
      <div className="space-y-4">
        <FormStatus title={view.reason === "enabled" ? "Two-step verification is on" : "New recovery codes created"}>
          {view.reason === "regenerated" ? "Your previous recovery codes no longer work." : "You will be asked for a code when you sign in."}
        </FormStatus>
        <RecoveryCodes
          codes={view.codes}
          onDone={() => {
            setView({ kind: "overview" });
            router.refresh();
          }}
        />
      </div>
    );
  }

  if (view.kind === "enrolling") {
    const { enrollment } = view;
    return (
      <section aria-labelledby="mfa-setup-title" className="space-y-5">
        <h2 id="mfa-setup-title" ref={setupHeadingRef} tabIndex={-1} className="font-display text-xl font-semibold text-ink-900 focus:outline-none">
          Set up your authenticator app
        </h2>
        <ol className="list-decimal space-y-4 pl-6 text-ink-700">
          <li>
            <p>Open an authenticator app on your phone and scan this QR code.</p>
            <div className="mt-3">
              <QrCode value={enrollment.otpauth_uri} label="QR code for adding FundZim to your authenticator app" />
            </div>
          </li>
          <li>
            <p>Can&apos;t scan it? Enter this key in the app instead (time-based):</p>
            <p className="mt-2 rounded-lg bg-surface-muted px-3 py-2 font-mono text-lg tracking-wider break-all text-ink-900" aria-label="Set-up key">
              {formatSecret(enrollment.secret)}
            </p>
          </li>
          <li>
            <p>Enter the 6-digit code the app shows to finish.</p>
          </li>
        </ol>
        <form ref={enrolFormRef} noValidate onSubmit={confirmEnrollment} className="space-y-4">
          <FormError ref={enrolErrorRef} message={enrolError} />
          <TextField
            label="Code from your authenticator app"
            name="code"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]*"
            maxLength={8}
            required
            error={enrolFields.code}
          />
          <div className="flex flex-wrap gap-3">
            <SubmitButton busy={busy === "confirm"} busyLabel="Confirming…">
              Turn on two-step verification
            </SubmitButton>
            <Button
              variant="outline"
              onClick={() => {
                enrolClear();
                setView({ kind: "overview" });
              }}
            >
              Cancel
            </Button>
          </div>
        </form>
      </section>
    );
  }

  return (
    <div className="space-y-6">
      {notice ? <FormStatus title={notice} /> : null}
      <FormError ref={actionErrorRef} message={actionError} />
      <p className="flex flex-wrap items-center gap-2 text-ink-700">
        Status: {mfaEnabled ? <Badge tone="brand">On</Badge> : <Badge tone="neutral">Off</Badge>}
      </p>
      {mfaEnabled ? (
        <>
          <section aria-labelledby="recovery-title" className="space-y-2">
            <h2 id="recovery-title" className="font-display text-xl font-semibold text-ink-900">
              Recovery codes
            </h2>
            <p className="text-ink-700">
              You have <strong>{recoveryCodesRemaining}</strong> unused recovery code{recoveryCodesRemaining === 1 ? "" : "s"}.
              Creating new codes makes the old ones stop working.
            </p>
            <Button variant="outline" onClick={regenerate} disabled={busy !== null}>
              {busy === "regenerate" ? "Creating…" : "Create new recovery codes"}
            </Button>
          </section>
          <section aria-labelledby="disable-title" className="space-y-3">
            <h2 id="disable-title" className="font-display text-xl font-semibold text-ink-900">
              Turn off two-step verification
            </h2>
            <form ref={disableFormRef} noValidate onSubmit={turnOff} className="space-y-4">
              <FormError ref={disableErrorRef} message={disableError} />
              <TextField
                label="Authentication or recovery code"
                name="disable_code"
                autoComplete="one-time-code"
                autoCapitalize="characters"
                spellCheck={false}
                hint="Your account is less protected without two-step verification."
                required
                error={disableFields.disable_code}
              />
              <SubmitButton busy={busy === "disable"} busyLabel="Turning off…" variant="outline">
                Turn off
              </SubmitButton>
            </form>
          </section>
        </>
      ) : (
        <section aria-labelledby="enable-title" className="space-y-3">
          <h2 id="enable-title" className="font-display text-xl font-semibold text-ink-900">
            Add an authenticator app
          </h2>
          <p className="text-ink-700">
            After your password, you will also enter a code from an app on your phone. This protects your account even
            if your password is stolen.
          </p>
          <Button onClick={startEnrollment} disabled={busy !== null}>
            {busy === "enroll" ? "Starting…" : "Set up two-step verification"}
          </Button>
        </section>
      )}
    </div>
  );
}
