"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { useFormFeedback } from "@/components/auth/use-form-feedback";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { authApi, type AuthApi } from "@/lib/auth/api";

import { formatDateTime } from "./format";

/** Light client-side shape check; the API normalises to E.164 (Zimbabwe default region) and decides. */
function plausiblePhone(value: string): boolean {
  const digits = value.replace(/[\s()-]/g, "");
  return /^\+?\d{7,15}$/.test(digits);
}

/**
 * Phone verification: request a code (POST /me/phone/verify-request), then confirm it
 * (POST /me/phone/verify-confirm). Wrapped in step-up handling in case the API demands it.
 */
export function PhoneVerification({ phoneMasked, phoneVerified, api = authApi }: { phoneMasked: string | null; phoneVerified: boolean; api?: AuthApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [phone, setPhone] = useState<string | null>(null);
  const [sentTo, setSentTo] = useState<string | null>(null);
  const [expiresAt, setExpiresAt] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [verified, setVerified] = useState(false);
  const { formError, fieldErrors, errorRef, formRef, clear, fail, failWith } = useFormFeedback();
  const codeRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (phone) codeRef.current?.focus();
  }, [phone]);

  async function requestCode(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = String(new FormData(event.currentTarget).get("phone") ?? "").trim();
    if (!plausiblePhone(value)) return fail("Please correct the errors below.", { phone: "Enter a phone number, for example 077 123 4567 or +263 77 123 4567." });
    clear();
    setBusy(true);
    try {
      const result = await withStepUp(() => api.phoneVerifyRequest(value));
      setPhone(value);
      setSentTo(result.phone_masked || value);
      setExpiresAt(result.expires_at);
    } catch (error) {
      if (!(error instanceof StepUpCancelledError)) failWith(error, "We couldn't send a code. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  async function confirmCode(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!phone) return;
    const code = String(new FormData(event.currentTarget).get("code") ?? "").replace(/\s+/g, "");
    if (!/^\d{4,8}$/.test(code)) return fail("Please correct the errors below.", { code: "Enter the code we sent by SMS." });
    clear();
    setBusy(true);
    try {
      await withStepUp(() => api.phoneVerifyConfirm(phone, code));
      setVerified(true);
      setPhone(null);
      router.refresh();
    } catch (error) {
      if (!(error instanceof StepUpCancelledError)) failWith(error, "We couldn't confirm the code. Please try again.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      <p className="text-ink-700">
        {phoneVerified && phoneMasked ? (
          <>
            Confirmed phone number: <strong>{phoneMasked}</strong>
          </>
        ) : phoneMasked ? (
          <>
            Phone number <strong>{phoneMasked}</strong> is not confirmed yet.
          </>
        ) : (
          "No phone number confirmed."
        )}
      </p>
      {verified ? <FormStatus title="Phone number confirmed" /> : null}
      {phone ? (
        <form ref={formRef} noValidate onSubmit={confirmCode} className="space-y-4">
          <FormError ref={errorRef} message={formError} />
          <TextField
            ref={codeRef}
            label="Code from SMS"
            name="code"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]*"
            maxLength={8}
            hint={expiresAt ? `We sent a code to ${sentTo ?? phone}. It expires at ${formatDateTime(expiresAt)}.` : `We sent a code to ${sentTo ?? phone}.`}
            required
            error={fieldErrors.code}
          />
          <div className="flex flex-wrap gap-3">
            <SubmitButton busy={busy} busyLabel="Confirming…">
              Confirm phone number
            </SubmitButton>
            <Button
              variant="ghost"
              onClick={() => {
                clear();
                setPhone(null);
              }}
            >
              Use a different number
            </Button>
          </div>
        </form>
      ) : (
        <form ref={formRef} noValidate onSubmit={requestCode} className="space-y-4">
          <FormError ref={errorRef} message={formError} />
          <TextField
            label="Mobile phone number"
            name="phone"
            type="tel"
            inputMode="tel"
            autoComplete="tel"
            hint="Zimbabwe numbers can be entered without +263. We will send a code by SMS."
            required
            error={fieldErrors.phone}
          />
          <SubmitButton busy={busy} busyLabel="Sending code…" variant="outline">
            {phoneVerified ? "Change phone number" : "Send confirmation code"}
          </SubmitButton>
        </form>
      )}
    </div>
  );
}
