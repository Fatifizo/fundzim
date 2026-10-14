"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useOptionalStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { payoutsApi, type PayoutsApi } from "@/lib/payouts/api";
import { describePayoutError } from "@/lib/payouts/errors";
import { parseWithdrawalAmount } from "@/lib/payouts/money";

export interface DestinationOption {
  id: string;
  label: string;
}

/**
 * Request a TEST withdrawal. The amount is typed in currency units and converted to minor units with string
 * arithmetic; one Idempotency-Key per distinct request (a retry of the same values reuses it); a STEP_UP_REQUIRED answer asks for the password and
 * retries once. Refusals show plain language only (no compliance or risk detail).
 */
export function WithdrawalForm({ campaignId, currency, availableMinor, destinations, enabled, api = payoutsApi }: {
  campaignId: string;
  currency: string;
  availableMinor: string;
  destinations: DestinationOption[];
  enabled: boolean;
  api?: PayoutsApi;
}) {
  const router = useRouter();
  const withStepUp = useOptionalStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const keyRef = useRef<{ body: string; key: string } | null>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  if (!enabled) return <p className="text-ink-700">Withdrawals are not available in this environment.</p>;
  if (destinations.length === 0) {
    return <p className="text-ink-700">Add and verify a payout account first. It can be used once its waiting period has passed.</p>;
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const amount = parseWithdrawalAmount(String(data.get("amount") ?? ""), currency, availableMinor);
    const destination = String(data.get("destination_id") ?? "");
    const purpose = String(data.get("purpose") ?? "").trim();
    const errs: Record<string, string> = {};
    if (!amount.ok) errs.amount = amount.message;
    if (!destination) errs.destination_id = "Choose a payout account.";
    if ([...purpose].length > 500) errs.purpose = "Use at most 500 characters.";
    setFieldErrors(errs);
    if (!amount.ok || Object.keys(errs).length > 0) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    setBusy(true);
    setError(null);
    const payload = { currency, amount_minor: amount.amountMinor, destination_id: destination, purpose: purpose || null };
    // one Idempotency-Key per distinct request: retrying the same values after a timeout reuses it (no duplicate)
    const body = JSON.stringify(payload);
    if (!keyRef.current || keyRef.current.body !== body) keyRef.current = { body, key: crypto.randomUUID() };
    const key = keyRef.current.key;
    try {
      await withStepUp(() => api.requestPayout(campaignId, payload, key));
      keyRef.current = null;
      form.reset();
      setNotice("Withdrawal requested. A finance officer will review it.");
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        setError(describePayoutError(err).message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={submit} className="space-y-5" aria-label="Request a test withdrawal">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError ref={errorRef} message={error} />
      <SelectField label="Payout account" name="destination_id" options={destinations.map((d) => ({ value: d.id, label: d.label }))} error={fieldErrors.destination_id} />
      <TextField label={`Amount (${currency})`} name="amount" inputMode="decimal" autoComplete="off" required error={fieldErrors.amount} hint="For example 25 or 25.50." />
      <TextAreaField label="Purpose (optional)" name="purpose" maxLength={500} error={fieldErrors.purpose} />
      <SubmitButton busy={busy} busyLabel="Requesting…">
        Request test withdrawal
      </SubmitButton>
    </form>
  );
}

/** Cancel a withdrawal before it is sent (step-up). Renders nothing when cancelling is not available. */
export function CancelWithdrawal({ id, available, api = payoutsApi }: { id: string; available: boolean; api?: PayoutsApi }) {
  const router = useRouter();
  const withStepUp = useOptionalStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  async function cancel() {
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.cancelOwn(id));
      setDone(true);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) setError(describePayoutError(err).message);
    } finally {
      setBusy(false);
    }
  }
  if (!available && !done) return null;
  return (
    <div className="space-y-3">
      {done ? <FormStatus title="Withdrawal cancelled. The reserved amount is back in the campaign's test balance." /> : null}
      <FormError message={error} />
      {!done ? (
        <SubmitButton type="button" busy={busy} busyLabel="Cancelling…" onClick={cancel}>
          Cancel withdrawal
        </SubmitButton>
      ) : null}
    </div>
  );
}
