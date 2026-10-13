"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { paymentsApi, type PaymentsApi } from "@/lib/payments/api";
import { describePaymentError } from "@/lib/payments/errors";
import { REFUND_REASON_LABELS } from "@/lib/payments/labels";
import { formatPaymentMoney, parseRefundAmount } from "@/lib/payments/money";
import { REFUND_REASON_CODES } from "@/lib/payments/types";

/**
 * Refund request (maker side, `refund.request`; interface-contracts §4). The amount is typed as a decimal string
 * and converted to minor units with string arithmetic only (lib/payments/money.ts); the currency is the
 * payment's and cannot be changed. One Idempotency-Key per distinct request: a retried submit of the same values
 * reuses it, so a lost response can never create two requests. A request is only a request: it needs an approval
 * by someone else, and even then refund execution is DISABLED in Stage 8.
 */
export function RefundRequestForm({ paymentId, currency, paymentAmountMinor, api = paymentsApi }: { paymentId: string; currency: string; paymentAmountMinor: string | null; api?: PaymentsApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
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

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const amount = parseRefundAmount(String(data.get("amount") ?? ""), currency, paymentAmountMinor);
    const reason_code = String(data.get("reason_code") ?? "");
    const reason_text = String(data.get("reason_text") ?? "").trim();
    const errs: Record<string, string> = {};
    if (!amount.ok) errs.amount = amount.message;
    if (!(REFUND_REASON_CODES as readonly string[]).includes(reason_code)) errs.reason_code = "Choose a reason.";
    const n = [...reason_text].length;
    if (n < 3 || n > 2000) errs.reason_text = "Explain the refund in 3 to 2,000 characters.";
    setFieldErrors(errs);
    setNotice(null);
    if (!amount.ok || Object.keys(errs).length > 0) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    const input = { amount_minor: amount.amountMinor, reason_code, reason_text };
    const body = JSON.stringify(input);
    if (!keyRef.current || keyRef.current.body !== body) keyRef.current = { body, key: crypto.randomUUID() };
    setBusy(true);
    setError(null);
    try {
      const key = keyRef.current.key;
      await withStepUp(() => api.createRefundRequest(paymentId, input, key));
      keyRef.current = null;
      form.reset();
      setNotice(`Refund request for ${formatPaymentMoney({ amount_minor: input.amount_minor, currency }).text} recorded. It needs approval by another authorised person; refund execution is DISABLED in Stage 8.`);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        const d = describePaymentError(err, "The refund request could not be recorded.");
        setFieldErrors(d.fieldErrors);
        setError(d.message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={onSubmit} className="space-y-4 rounded-card border border-line bg-surface p-5" aria-labelledby="refund-form-title">
      <h3 id="refund-form-title" className="font-display text-xl font-semibold text-ink-900">
        Request a refund
      </h3>
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <FormError ref={errorRef} message={error} />
      <TextField
        label={`Amount (${currency})`}
        name="amount"
        inputMode="decimal"
        autoComplete="off"
        hint={`In ${currency}, the payment's currency. For example 25 or 25.50.`}
        error={fieldErrors.amount}
      />
      <SelectField
        label="Reason"
        name="reason_code"
        placeholderOption="Choose a reason"
        options={REFUND_REASON_CODES.map((c) => ({ value: c, label: REFUND_REASON_LABELS[c] ?? c }))}
        error={fieldErrors.reason_code}
      />
      <TextAreaField label="Explanation (staff only)" name="reason_text" required maxLength={2000} error={fieldErrors.reason_text} />
      <SubmitButton busy={busy} busyLabel="Recording…">
        Record refund request
      </SubmitButton>
    </form>
  );
}
