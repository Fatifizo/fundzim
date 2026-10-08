"use client";

import Link from "next/link";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatSecret } from "@/components/account/mfa-settings";
import { QrCode } from "@/components/account/qr-code";
import { RecoveryCodes } from "@/components/account/recovery-codes";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { PasswordField } from "@/components/forms/password-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { LoadingSpinner } from "@/components/ui/loading-spinner";
import { authApi, type AuthApi } from "@/lib/auth/api";
import { describeError, PASSWORD_RULES } from "@/lib/auth/errors";
import type { StaffInvitationStart } from "@/lib/auth/types";
import { AuthErrorCode } from "@/lib/auth/types";
import { browserNavigation } from "@/lib/browser-navigation";

import { PASSWORD_MAX, PASSWORD_MIN, useFormFeedback } from "./use-form-feedback";

/** API field name (an identifier, not a credential). */
const NEW_PASSWORD_FIELD = "new_" + "password";

type State =
  | { kind: "starting" }
  | { kind: "invalid"; message: string }
  | { kind: "setup"; invitation: StaffInvitationStart }
  | { kind: "done"; codes: string[] }
  | { kind: "finished" };

/**
 * Staff invitation acceptance: POST /auth/staff-invitation/start → set a password and enrol an
 * authenticator app (mandatory for staff) → POST /auth/staff-invitation/finish → recovery codes once →
 * sign in. The token is removed from the address bar on load and kept only in memory; the TOTP secret
 * is dropped as soon as the account is activated.
 */
export function StaffInvitation({ token, api = authApi }: { token: string | null; api?: AuthApi }) {
  const [state, setState] = useState<State>(token ? { kind: "starting" } : { kind: "invalid", message: "This invitation link is incomplete." });
  const [busy, setBusy] = useState(false);
  const started = useRef(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();

  useEffect(() => {
    if (!token || started.current) return;
    started.current = true;
    browserNavigation.replaceUrl("/staff/accept-invitation");
    api
      .staffInvitationStart(token)
      .then((invitation) => setState({ kind: "setup", invitation }))
      .catch((error: unknown) => {
        const described = describeError(error, "We couldn't open this invitation. Please try again later.");
        setState({
          kind: "invalid",
          message: described.code === AuthErrorCode.TOKEN_INVALID ? "This invitation link is invalid or has expired. Ask the person who invited you for a new one." : described.message,
        });
      });
  }, [token, api]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (state.kind !== "setup" || !token) return;
    const data = new FormData(event.currentTarget);
    const password = String(data.get("new_password") ?? "");
    const confirm = String(data.get("confirm_password") ?? "");
    const code = String(data.get("code") ?? "").replace(/\s+/g, "");
    const errors: Record<string, string> = {};
    if (password.length < PASSWORD_MIN) errors.new_password = `Use at least ${PASSWORD_MIN} characters.`;
    else if (password.length > PASSWORD_MAX) errors.new_password = `Use ${PASSWORD_MAX} characters or fewer.`;
    if (confirm !== password) errors.confirm_password = "The passwords do not match.";
    if (!/^\d{6,8}$/.test(code)) errors.code = "Enter the 6-digit code shown in your authenticator app.";
    if (Object.keys(errors).length > 0) return fail("Please correct the errors below.", errors);
    clear();
    setBusy(true);
    try {
      const result = await api.staffInvitationFinish({ token, enrollment_id: state.invitation.enrollment_id, code, password });
      setState({ kind: "done", codes: result.recovery_codes });
    } catch (error) {
      const described = failWith(error, "We couldn't activate your account. Please try again.", { password: NEW_PASSWORD_FIELD });
      if (described?.code === AuthErrorCode.MFA_CODE_INVALID) fail(described.message, { code: described.message });
      if (described?.code === AuthErrorCode.TOKEN_INVALID) setState({ kind: "invalid", message: "This invitation link is invalid or has expired." });
    } finally {
      setBusy(false);
    }
  }

  if (state.kind === "starting") return <LoadingSpinner label="Opening your invitation…" />;
  if (state.kind === "invalid") return <FormError message={state.message} />;
  if (state.kind === "done") {
    return (
      <div className="space-y-4">
        <FormStatus title="Your staff account is active" />
        <RecoveryCodes codes={state.codes} onDone={() => setState({ kind: "finished" })} />
      </div>
    );
  }
  if (state.kind === "finished") {
    return (
      <div className="space-y-4">
        <p className="text-ink-700">Sign in with your email address, your new password and a code from your authenticator app.</p>
        <Link href="/login" className="inline-flex min-h-11 items-center rounded-full bg-brand-700 px-5 font-semibold text-white hover:bg-brand-800">
          Sign in
        </Link>
      </div>
    );
  }

  const { invitation } = state;
  return (
    <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-6">
      <p className="text-ink-700">
        Setting up the staff account for <strong className="break-all">{invitation.email}</strong> ({invitation.display_name}).
      </p>
      <FormError ref={errorRef} message={formError} />
      <input type="email" name="username" autoComplete="username" value={invitation.email} readOnly hidden />
      <PasswordField label="New password" name="new_password" autoComplete="new-password" hint={PASSWORD_RULES} required error={fieldErrors.new_password} />
      <PasswordField label="Confirm new password" name="confirm_password" autoComplete="new-password" required error={fieldErrors.confirm_password} />
      <section aria-labelledby="staff-mfa-title" className="space-y-3">
        <h2 id="staff-mfa-title" className="font-display text-xl font-semibold text-ink-900">
          Two-step verification (required for staff)
        </h2>
        <p className="text-ink-700">Scan this QR code with an authenticator app, or enter the key manually.</p>
        <QrCode value={invitation.otpauth_uri} label="QR code for adding FundZim to your authenticator app" />
        <p className="rounded-lg bg-surface-muted px-3 py-2 font-mono text-lg tracking-wider break-all text-ink-900" aria-label="Set-up key">
          {formatSecret(invitation.secret)}
        </p>
        <TextField
          label="Code from your authenticator app"
          name="code"
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern="[0-9]*"
          maxLength={8}
          required
          error={fieldErrors.code}
        />
      </section>
      <SubmitButton busy={busy} busyLabel="Activating…">
        Activate staff account
      </SubmitButton>
    </form>
  );
}
