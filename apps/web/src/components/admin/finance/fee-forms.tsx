"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { feesApi, type FeesApi } from "@/lib/fees/api";
import { describeFeeError } from "@/lib/fees/errors";
import { amountToMinor, percentToBps } from "@/lib/fees/labels";
import { CONFIRMATION_STATUSES, FEE_CATEGORIES, MANUAL_ADJUSTMENT_REASONS, PROVIDER_FEE_TYPES, REFUND_TREATMENTS, RESPONSIBILITIES, ROUNDING_MODES } from "@/lib/fees/types";
import { humanise } from "@/lib/payments/labels";

const opts = (values: readonly string[]) => values.map((v) => ({ value: v, label: humanise(v) }));
const CODE = /^[a-z][a-z0-9_.]{2,63}$/;
const METHOD = /^[A-Z][A-Z0-9_]{1,31}$/;
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Shared submit plumbing: one Idempotency-Key per distinct body, step-up retry, focus on notice or error. */
function useFormSubmit() {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const errorRef = useRef<HTMLDivElement>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const keyRef = useRef<{ body: string; key: string } | null>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);
  const fail = (message: string, errs: Record<string, string> = {}) => {
    setFieldErrors(errs);
    setError(message);
    requestAnimationFrame(() => errorRef.current?.focus());
  };
  async function run<T>(body: unknown, call: (key: string) => Promise<T>, done: (r: T) => void) {
    setFieldErrors({});
    setBusy(true);
    setError(null);
    const raw = JSON.stringify(body);
    if (!keyRef.current || keyRef.current.body !== raw) keyRef.current = { body: raw, key: crypto.randomUUID() };
    const key = keyRef.current.key;
    try {
      const r = await withStepUp(() => call(key));
      keyRef.current = null;
      done(r);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) fail(describeFeeError(err).message);
    } finally {
      setBusy(false);
    }
  }
  const noticeStatus = notice ? <FormStatus ref={noticeRef} title={notice} /> : null;
  const errorStatus = <FormError ref={errorRef} message={error} />;
  const status = (
    <>
      {noticeStatus}
      {errorStatus}
    </>
  );
  return { router, busy, fieldErrors, run, fail, setNotice, status, noticeStatus, errorStatus };
}

function FormShell({ label, children, onSubmit }: { label: string; children: ReactNode; onSubmit: (e: FormEvent<HTMLFormElement>) => void }) {
  return (
    <form noValidate onSubmit={onSubmit} className="space-y-5" aria-label={label}>
      {children}
    </form>
  );
}

const value = (f: FormData, k: string) => String(f.get(k) ?? "").trim();

/** Create a fee policy scope (fee.config.request). Its scope is immutable; terms live in versions. */
export function CreatePolicyForm({ api = feesApi }: { api?: FeesApi }) {
  const f = useFormSubmit();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const d = new FormData(form);
    const body = { code: value(d, "code"), name: value(d, "name"), description: value(d, "description"), category: value(d, "category"),
      currency: value(d, "currency"), payment_method: value(d, "payment_method") || null, campaign_id: value(d, "campaign_id") || null };
    const errs: Record<string, string> = {};
    if (!CODE.test(body.code)) errs.code = "Use 3–64 lowercase letters, digits, dots or underscores, starting with a letter.";
    if ([...body.name].length < 3 || [...body.name].length > 120) errs.name = "Write 3 to 120 characters.";
    if (body.payment_method && !METHOD.test(body.payment_method)) errs.payment_method = "Use the method code, for example ECOCASH.";
    if (body.campaign_id && !UUID.test(body.campaign_id)) errs.campaign_id = "Paste the campaign id.";
    if (Object.keys(errs).length) return f.fail("Please correct the errors below.", errs);
    await f.run(body, (key) => api.createPolicy(body, key), (r) => {
      form.reset();
      const id = (r as { id?: unknown })?.id;
      if (typeof id === "string" && UUID.test(id)) f.router.push(`/admin/finance/fees/policies/${encodeURIComponent(id)}`);
      else f.setNotice("Fee policy created.");
    });
  }
  return (
    <FormShell label="Create a fee policy" onSubmit={submit}>
      {f.status}
      <TextField label="Code" name="code" required autoComplete="off" hint="For example platform.usd" error={f.fieldErrors.code} />
      <TextField label="Name" name="name" required autoComplete="off" error={f.fieldErrors.name} />
      <TextAreaField label="Description (optional)" name="description" maxLength={2000} />
      <SelectField label="Category" name="category" options={opts(FEE_CATEGORIES)} hint="Currency conversion and other fees cannot be enabled." />
      <SelectField label="Currency" name="currency" options={[{ value: "USD", label: "USD" }]} />
      <TextField label="Payment method (optional)" name="payment_method" autoComplete="off" hint="Leave empty for every method." error={f.fieldErrors.payment_method} />
      <TextField label="Campaign id (optional)" name="campaign_id" autoComplete="off" hint="Leave empty for every campaign." error={f.fieldErrors.campaign_id} />
      <SubmitButton busy={f.busy} busyLabel="Creating…">
        Create policy
      </SubmitButton>
    </FormShell>
  );
}

function localNowPlus(minutes: number): string {
  const d = new Date(Date.now() + minutes * 60_000);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/**
 * Draft a new version of a policy (fee.config.request). Terms are frozen when the draft is submitted.
 * available=false (a draft or pending version exists) keeps the component mounted without its form, so the confirmation
 * survives the refresh that follows a successful draft (G-02; the same pattern as FeeAction).
 */
export function VersionForm({ policyId, category, currency, available = true, api = feesApi }: { policyId: string; category: string; currency: string; available?: boolean; api?: FeesApi }) {
  const f = useFormSubmit();
  const passThrough = category !== "PLATFORM_FEE";
  const models = passThrough ? ["PROVIDER_PASS_THROUGH"] : ["PERCENTAGE", "FIXED", "PERCENTAGE_PLUS_FIXED", "CAPPED", "MINIMUM_FEE", "ZERO_FEE"];
  const responsibilities = category === "PLATFORM_FEE" ? ["ORGANISER_COVERED", "DONOR_COVERED", "SHARED"]
    : category === "PAYMENT_PROCESSING_FEE" ? [...RESPONSIBILITIES] : ["ORGANISER_COVERED", "PLATFORM_ABSORBED"];
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const d = new FormData(form);
    const model = value(d, "model");
    const errs: Record<string, string> = {};
    const needsRate = ["PERCENTAGE", "PERCENTAGE_PLUS_FIXED", "CAPPED", "MINIMUM_FEE"].includes(model);
    const needsFixed = ["FIXED", "PERCENTAGE_PLUS_FIXED"].includes(model);
    const rate = needsRate ? percentToBps(value(d, "rate")) : null;
    if (needsRate && rate === null) errs.rate = "Enter a rate from 0 to 100, at most two decimals.";
    const amount = (k: string, required: boolean) => {
      const raw = value(d, k);
      if (!raw) {
        if (required) errs[k] = "Enter an amount.";
        return null;
      }
      const m = amountToMinor(raw, currency);
      if (m === null) errs[k] = "Enter an amount, for example 0.30.";
      return m;
    };
    const fixed = needsFixed ? amount("fixed", true) : null;
    const min = model === "ZERO_FEE" || passThrough ? null : amount("min", model === "MINIMUM_FEE");
    const max = model === "ZERO_FEE" || passThrough ? null : amount("max", model === "CAPPED");
    const responsibility = value(d, "responsibility");
    const share = responsibility === "SHARED" ? percentToBps(value(d, "donor_share")) : null;
    if (responsibility === "SHARED" && (share === null || share < 1 || share > 9999)) errs.donor_share = "Enter the donor's share, between 0.01 and 99.99.";
    const start = value(d, "effective_from");
    const startDate = new Date(start);
    if (!start || Number.isNaN(startDate.getTime())) errs.effective_from = "Choose when this version starts.";
    if (Object.keys(errs).length) return f.fail("Please correct the errors below.", errs);
    const body = { model, rate_bps: rate, fixed_minor: fixed, min_fee_minor: min, max_fee_minor: max, rounding_mode: value(d, "rounding_mode"),
      responsibility, donor_share_bps: share, refund_treatment: category === "PLATFORM_FEE" ? value(d, "refund_treatment") : null,
      effective_from: startDate.toISOString() };
    await f.run(body, (key) => api.createVersion(policyId, body, key), () => {
      form.reset();
      f.setNotice("Draft version created. Submit it for approval when ready.");
    });
  }
  // the notice keeps one position whether or not the form is shown, so its element (and focus) survive the swap
  return (
    <div className="space-y-5">
      {f.noticeStatus}
      {available ? (
        <FormShell label="Draft a fee policy version" onSubmit={submit}>
          {f.errorStatus}
          <SelectField label="Fee model" name="model" options={opts(models)} />
          {passThrough ? (
            <p className="text-ink-700">The amount comes from provider fee evidence; this version decides only who bears it.</p>
          ) : (
            <>
              <TextField label="Rate (%)" name="rate" inputMode="decimal" autoComplete="off" hint="For percentage models. For example 5 or 2.9." error={f.fieldErrors.rate} />
              <TextField label={`Fixed amount (${currency})`} name="fixed" inputMode="decimal" autoComplete="off" hint="For fixed models." error={f.fieldErrors.fixed} />
              <TextField label={`Minimum fee (${currency}, optional)`} name="min" inputMode="decimal" autoComplete="off" error={f.fieldErrors.min} />
              <TextField label={`Maximum fee (${currency}, optional)`} name="max" inputMode="decimal" autoComplete="off" error={f.fieldErrors.max} />
            </>
          )}
          <SelectField label="Rounding" name="rounding_mode" options={opts(ROUNDING_MODES)} />
          <SelectField label="Who bears the fee" name="responsibility" options={opts(responsibilities)} />
          <TextField label="Donor share (%) when shared" name="donor_share" inputMode="decimal" autoComplete="off" error={f.fieldErrors.donor_share} />
          {category === "PLATFORM_FEE" ? <SelectField label="Refund treatment" name="refund_treatment" options={opts(REFUND_TREATMENTS)} /> : null}
          <TextField label="Effective from" name="effective_from" type="datetime-local" defaultValue={localNowPlus(5)} required error={f.fieldErrors.effective_from} hint="Never retroactive: it must start after it is submitted." />
          <SubmitButton busy={f.busy} busyLabel="Saving…">
            Create draft
          </SubmitButton>
        </FormShell>
      ) : (
        <p className="text-ink-700">A draft or pending version already exists. Decide it first.</p>
      )}
    </div>
  );
}

/** Record provider fee evidence (fee.config.request). VERIFIED is not offered: no provider is selected (ADR-040). */
export function ScheduleForm({ sandboxProviderId, api = feesApi }: { sandboxProviderId: string | null; api?: FeesApi }) {
  const f = useFormSubmit();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const d = new FormData(form);
    const errs: Record<string, string> = {};
    const provider = value(d, "provider_id");
    if (!UUID.test(provider)) errs.provider_id = "Paste the provider id.";
    const contract = value(d, "contract_reference");
    if ([...contract].length < 3) errs.contract_reference = "Write at least 3 characters.";
    const source = value(d, "evidence_source");
    if ([...source].length < 3) errs.evidence_source = "Describe where the figures come from.";
    const rate = percentToBps(value(d, "rate"));
    if (rate === null) errs.rate = "Enter a rate from 0 to 100, at most two decimals.";
    const method = value(d, "payment_method");
    if (method && !METHOD.test(method)) errs.payment_method = "Use the method or rail code, for example ECOCASH.";
    if (Object.keys(errs).length) return f.fail("Please correct the errors below.", errs);
    const rounding = value(d, "rounding_mode");
    const body = { provider_id: provider, contract_reference: contract, schedule_version: value(d, "schedule_version") || "1", currency: "USD",
      payment_method: method || null, fee_type: value(d, "fee_type"), model: "PERCENTAGE", rate_bps: rate, fixed_minor: null,
      rounding_mode: rounding === "UNKNOWN" ? null : rounding, effective_from: new Date(Date.now() - 60_000).toISOString(), evidence_source: source,
      evidence_reference: value(d, "evidence_reference") || null, confirmation_status: value(d, "confirmation_status") };
    await f.run(body, (key) => api.createSchedule(body, key), () => {
      form.reset();
      f.setNotice("Provider fee evidence recorded. A different person must approve it.");
    });
  }
  return (
    <FormShell label="Record provider fee evidence" onSubmit={submit}>
      {f.status}
      <TextField label="Provider id" name="provider_id" autoComplete="off" defaultValue={sandboxProviderId ?? ""} error={f.fieldErrors.provider_id} hint="Synthetic evidence is accepted for the sandbox simulator only." />
      <TextField label="Contract reference" name="contract_reference" autoComplete="off" error={f.fieldErrors.contract_reference} />
      <TextField label="Schedule version" name="schedule_version" autoComplete="off" defaultValue="1" />
      <SelectField label="Fee type" name="fee_type" options={opts(PROVIDER_FEE_TYPES)} />
      <TextField label="Payment method or payout rail (optional)" name="payment_method" autoComplete="off" error={f.fieldErrors.payment_method} />
      <TextField label="Rate (%)" name="rate" inputMode="decimal" autoComplete="off" error={f.fieldErrors.rate} />
      <SelectField label="Provider rounding" name="rounding_mode" options={[{ value: "UNKNOWN", label: "Unknown" }, ...opts(ROUNDING_MODES)]} />
      <SelectField label="Confirmation" name="confirmation_status" options={opts(CONFIRMATION_STATUSES)} hint="VERIFIED is not available: no provider is selected." />
      <TextField label="Evidence source" name="evidence_source" autoComplete="off" error={f.fieldErrors.evidence_source} />
      <TextField label="Evidence reference (optional)" name="evidence_reference" autoComplete="off" />
      <SubmitButton busy={f.busy} busyLabel="Saving…">
        Record evidence
      </SubmitButton>
    </FormShell>
  );
}

/** Request a manual fee correction (maker: fee.adjust.request). Refunds and chargebacks are computed, never typed. */
export function AdjustmentForm({ assessmentId, currency, api = feesApi }: { assessmentId: string; currency: string; api?: FeesApi }) {
  const f = useFormSubmit();
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const d = new FormData(form);
    const errs: Record<string, string> = {};
    const amount = amountToMinor(value(d, "amount"), currency);
    if (amount === null || amount === "0") errs.amount = "Enter an amount, for example 0.50.";
    const note = value(d, "note");
    if ([...note].length < 5 || [...note].length > 2000) errs.note = "Write 5 to 2,000 characters.";
    if (Object.keys(errs).length) return f.fail("Please correct the errors below.", errs);
    const body = { reason: value(d, "reason"), direction: value(d, "direction"), amount_minor: amount!, note };
    await f.run(body, (key) => api.requestAdjustment(assessmentId, body, key), () => {
      form.reset();
      f.setNotice("Adjustment requested. A different person must approve it.");
    });
  }
  return (
    <FormShell label="Request a fee adjustment" onSubmit={submit}>
      {f.status}
      <SelectField label="Reason" name="reason" options={opts(MANUAL_ADJUSTMENT_REASONS)} />
      <SelectField label="Direction" name="direction" options={[{ value: "DECREASE", label: "Reduce the fee" }, { value: "INCREASE", label: "Increase the fee" }]} />
      <TextField label={`Amount (${currency})`} name="amount" inputMode="decimal" autoComplete="off" error={f.fieldErrors.amount} />
      <TextAreaField label="Note (staff only)" name="note" maxLength={2000} error={f.fieldErrors.note} />
      <SubmitButton busy={f.busy} busyLabel="Requesting…">
        Request adjustment
      </SubmitButton>
    </FormShell>
  );
}
