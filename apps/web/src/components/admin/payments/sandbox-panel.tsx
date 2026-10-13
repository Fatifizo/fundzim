"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { paymentsApi, type PaymentsApi, type SandboxWebhookInput } from "@/lib/payments/api";
import { describePaymentError } from "@/lib/payments/errors";
import { SANDBOX_SIGN_WITH, SANDBOX_WEBHOOK_STATUSES } from "@/lib/payments/types";

/** Small bounded integers for simulator knobs (never money). */
function intField(raw: string, min: number, max: number): number | null | undefined {
  const v = raw.trim();
  if (v === "") return undefined;
  if (!/^-?\d{1,6}$/.test(v)) return null;
  const n = Number.parseInt(v, 10);
  return n >= min && n <= max ? n : null;
}

/**
 * DEVELOPMENT ONLY sandbox simulator control (interface-contracts §4, `payment.sandbox.control`). The simulator
 * signs webhooks and sends them through the REAL webhook endpoint, so they go through verification, the inbox
 * and normal processing; this form never sets a payment status itself. Rendered only when the API says the
 * sandbox is enabled and the payment is simulated.
 */
export function SandboxWebhookForm({ paymentId, api = paymentsApi }: { paymentId: string; api?: PaymentsApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const noticeRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const status = String(data.get("status") ?? "");
    const signWith = String(data.get("sign_with") ?? "");
    const delay = intField(String(data.get("delay_seconds") ?? ""), 0, 3600);
    const copies = intField(String(data.get("copies") ?? ""), 1, 10);
    const offset = intField(String(data.get("timestamp_offset_seconds") ?? ""), -86400, 86400);
    const errs: Record<string, string> = {};
    if (!(SANDBOX_WEBHOOK_STATUSES as readonly string[]).includes(status)) errs.status = "Choose a simulated status.";
    if (signWith !== "" && !(SANDBOX_SIGN_WITH as readonly string[]).includes(signWith)) errs.sign_with = "Choose a key.";
    if (delay === null) errs.delay_seconds = "Enter whole seconds from 0 to 3600.";
    if (copies === null) errs.copies = "Enter a whole number from 1 to 10.";
    if (offset === null) errs.timestamp_offset_seconds = "Enter whole seconds from -86400 to 86400.";
    setFieldErrors(errs);
    setNotice(null);
    if (Object.keys(errs).length > 0) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    const input: SandboxWebhookInput = { status };
    if (delay !== undefined && delay !== null) input.delay_seconds = delay;
    if (copies !== undefined && copies !== null) input.copies = copies;
    if (signWith) input.sign_with = signWith;
    if (offset !== undefined && offset !== null) input.timestamp_offset_seconds = offset;
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.sandboxWebhook(paymentId, input));
      setNotice("Simulated webhook scheduled. It goes through the real webhook endpoint and normal processing.");
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        const d = describePaymentError(err, "The simulator could not schedule the webhook.");
        setFieldErrors(d.fieldErrors);
        setError(d.message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={onSubmit} className="space-y-4" aria-label="Send a simulated webhook">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError ref={errorRef} message={error} />
      <div className="grid gap-4 sm:grid-cols-2">
        <SelectField label="Simulated provider status" name="status" placeholderOption="Choose a status" options={SANDBOX_WEBHOOK_STATUSES.map((s) => ({ value: s, label: s }))} error={fieldErrors.status} />
        <SelectField label="Sign with" name="sign_with" options={SANDBOX_SIGN_WITH.map((s) => ({ value: s, label: s }))} defaultValue="current" error={fieldErrors.sign_with} />
        <TextField label="Delay (seconds)" name="delay_seconds" inputMode="numeric" defaultValue="0" error={fieldErrors.delay_seconds} />
        <TextField label="Copies" name="copies" inputMode="numeric" defaultValue="1" error={fieldErrors.copies} />
        <TextField label="Timestamp offset (seconds)" name="timestamp_offset_seconds" defaultValue="0" hint="Negative or positive; outside the replay window the webhook is rejected." error={fieldErrors.timestamp_offset_seconds} />
      </div>
      <SubmitButton busy={busy} busyLabel="Scheduling…">
        Send simulated webhook
      </SubmitButton>
    </form>
  );
}
