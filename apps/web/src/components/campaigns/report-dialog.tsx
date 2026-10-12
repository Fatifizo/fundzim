"use client";

import { useId, useRef, useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { inputClasses, TextField } from "@/components/forms/text-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { browserApi } from "@/lib/api/client";
import { isApiError } from "@/lib/api/errors";
import { DETAILS_MAX, REPORT_REASONS, reportPayload, validateReport, type ReportField, type ReportInput } from "@/lib/campaigns/report";

const EMPTY: ReportInput = { reason: "", details: "", contactEmail: "", website: "" };

function describeError(error: unknown): { message: string; fields: Partial<Record<ReportField, string>> } {
  if (!isApiError(error)) return { message: "Your report could not be sent. Check your connection and try again.", fields: {} };
  switch (error.code) {
    case "CAMPAIGN_NOT_FOUND":
      return { message: "This campaign is no longer public, so it can't be reported here. If you are worried about it, contact FundZim.", fields: {} };
    case "RATE_LIMITED": {
      const minutes = error.retryAfterMs !== undefined ? Math.max(1, Math.ceil(error.retryAfterMs / 60_000)) : null;
      return { message: `Too many reports were sent from this connection. Please try again${minutes ? ` in about ${minutes} minute${minutes === 1 ? "" : "s"}` : " later"}.`, fields: {} };
    }
    case "VALIDATION_FAILED": {
      const fields: Partial<Record<ReportField, string>> = {};
      for (const d of error.details) {
        if (d.field === "reason") fields.reason = "Choose a reason.";
        if (d.field === "details") fields.details = d.code === "HTML_NOT_ALLOWED" ? "Use plain text only: HTML tags are not allowed." : `Check the details (required for "Something else", up to ${DETAILS_MAX.toLocaleString("en-GB")} characters).`;
        if (d.field === "contact_email") fields.contact_email = "Enter an email address in the format name@example.com, or leave it empty.";
      }
      return { message: "Please check the highlighted fields.", fields };
    }
    default:
      return { message: "Your report could not be sent. Please try again in a moment.", fields: {} };
  }
}

/**
 * "Report this campaign" (contract §3): an accessible <dialog> with the contract's reasons, optional details
 * and contact email, and a hidden honeypot field (`website`). Posting is never retried automatically. The
 * confirmation says the report will be reviewed — never that the campaign will be removed (ADR-038 §3).
 */
export function ReportCampaign({ slug, title }: { slug: string; title: string }) {
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState<ReportInput>(EMPTY);
  const [errors, setErrors] = useState<Partial<Record<ReportField, string>>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [done, setDone] = useState(false);
  const errorRef = useRef<HTMLDivElement>(null);
  const doneRef = useRef<HTMLDivElement>(null);
  const id = useId();
  const reasonError = `${id}-reason-error`;
  const detailsId = `${id}-details`;
  const detailsHint = `${id}-details-hint`;
  const detailsError = `${id}-details-error`;

  function close() {
    setOpen(false);
    if (done) {
      setDone(false);
      setInput(EMPTY);
    }
    setErrors({});
    setFormError(null);
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (busy) return;
    const problems = validateReport(input);
    setErrors(problems);
    if (Object.keys(problems).length > 0) {
      setFormError("Please check the highlighted fields.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    setBusy(true);
    setFormError(null);
    try {
      await browserApi.post(`/api/v1/public/campaigns/${slug}/reports`, { body: reportPayload(input), timeoutMs: 15_000 });
      setDone(true);
      requestAnimationFrame(() => doneRef.current?.focus());
    } catch (error) {
      const described = describeError(error);
      setErrors(described.fields);
      setFormError(described.message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  const set = (patch: Partial<ReportInput>) => setInput((v) => ({ ...v, ...patch }));

  return (
    <>
      <Button variant="ghost" className="px-0 underline underline-offset-4" onClick={() => setOpen(true)}>
        Report this campaign
      </Button>
      {open ? (
        <Dialog
          title="Report this campaign"
          description={
            <p>
              Tell FundZim&apos;s review team what worries you about &ldquo;{title}&rdquo;. Reports are confidential: the organiser is not told who
              reported. A report does not by itself remove or pause a campaign; the team reviews it and decides what to do.
            </p>
          }
          onClose={close}
        >
          {done ? (
            <div className="space-y-4">
              <FormStatus ref={doneRef} title="Thank you — your report was received.">
                <p>FundZim&apos;s review team will look at it. We may not be able to tell you the outcome. If you gave an email address, we will only use it to ask about this report.</p>
                <p className="mt-2">If someone is in immediate danger, contact the police or local emergency services.</p>
              </FormStatus>
              <Button onClick={close}>Close</Button>
            </div>
          ) : (
            <form noValidate onSubmit={(e) => void submit(e)} className="space-y-5">
              <FormError ref={errorRef} message={formError} />
              <fieldset aria-describedby={errors.reason ? reasonError : undefined} className="space-y-2">
                <legend className="font-semibold text-ink-900">What is the problem? (required)</legend>
                {REPORT_REASONS.map((r) => (
                  <label key={r.value} className="flex min-h-11 items-start gap-3 rounded-lg px-1 py-2">
                    <input type="radio" name="reason" value={r.value} checked={input.reason === r.value} onChange={() => set({ reason: r.value })} className="mt-1 size-5 shrink-0 accent-brand-700" />
                    <span className="text-ink-900">{r.label}</span>
                  </label>
                ))}
                {errors.reason ? (
                  <p id={reasonError} className="text-sm font-semibold text-danger-700">
                    <span aria-hidden="true">Error: </span>
                    {errors.reason}
                  </p>
                ) : null}
              </fieldset>
              <div className="space-y-1">
                <label htmlFor={detailsId} className="block font-semibold text-ink-900">
                  Details {input.reason === "OTHER" ? "(required)" : "(optional)"}
                </label>
                <p id={detailsHint} className="text-sm text-ink-600">
                  Plain text, up to {DETAILS_MAX.toLocaleString("en-GB")} characters. Please don&apos;t include passwords, card numbers or ID numbers.
                </p>
                <textarea
                  id={detailsId}
                  name="details"
                  rows={4}
                  maxLength={DETAILS_MAX}
                  value={input.details}
                  onChange={(e) => set({ details: e.target.value })}
                  aria-invalid={errors.details ? true : undefined}
                  aria-describedby={[errors.details ? detailsError : null, detailsHint].filter(Boolean).join(" ")}
                  className={inputClasses}
                />
                {errors.details ? (
                  <p id={detailsError} className="text-sm font-semibold text-danger-700">
                    <span aria-hidden="true">Error: </span>
                    {errors.details}
                  </p>
                ) : null}
              </div>
              <TextField
                label="Your email address (optional)"
                name="contact_email"
                type="email"
                inputMode="email"
                autoComplete="email"
                maxLength={254}
                hint="Only if you are happy for the review team to contact you about this report. It is never shown to the organiser."
                value={input.contactEmail}
                onChange={(e) => set({ contactEmail: e.target.value })}
                error={errors.contact_email}
              />
              {/* Honeypot (contract §3): hidden from people and assistive technology; bots that fill it are dropped. */}
              <div aria-hidden="true" className="absolute -left-[10000px] h-px w-px overflow-hidden">
                <label htmlFor={`${id}-website`}>Leave this field empty</label>
                <input id={`${id}-website`} name="website" type="text" tabIndex={-1} autoComplete="off" value={input.website} onChange={(e) => set({ website: e.target.value })} />
              </div>
              <div className="flex flex-wrap gap-3">
                <SubmitButton busy={busy} busyLabel="Sending…">
                  Send report
                </SubmitButton>
                <Button variant="outline" onClick={close}>
                  Cancel
                </Button>
              </div>
            </form>
          )}
        </Dialog>
      ) : null}
    </>
  );
}
