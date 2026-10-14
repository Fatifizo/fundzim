"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { payoutsApi, type PayoutsApi } from "@/lib/payouts/api";
import { describePayoutError } from "@/lib/payouts/errors";

/**
 * Staff payout actions (Stage 11). Each one is audited; approve/reject/cancel/re-check/status query and the
 * exception workflow need a fresh MFA code (useStepUp retries once). None of them marks a payout paid or failed:
 * provider evidence does that. Approvals bind to the payout as requested; an approver attests no conflict.
 */
export type PayoutActionTarget =
  | { kind: "approve"; id: string }
  | { kind: "reject"; id: string }
  | { kind: "cancel"; id: string }
  | { kind: "recheck"; id: string }
  | { kind: "status"; id: string }
  | { kind: "investigate"; id: string }
  | { kind: "resolve"; id: string }
  | { kind: "reconcile" };

interface Copy {
  button: string;
  title: string;
  description: string;
  confirm: string;
  notice: string;
  field?: string;
  conflict?: boolean;
  resolution?: boolean;
}

const COPY: Record<PayoutActionTarget["kind"], Copy> = {
  approve: { button: "Approve payout", title: "Approve this payout?", description: "Checker step. You cannot approve a payout you requested, own or are linked to, or whose payout account you changed or verified. Your approval binds to this amount and destination snapshot.", confirm: "Confirm: Approve", notice: "Approval recorded.", field: "Reason (staff only)", conflict: true },
  reject: { button: "Reject payout", title: "Reject this payout?", description: "The reservation is released back to the campaign's settled balance.", confirm: "Confirm: Reject", notice: "Payout rejected.", field: "Reason (staff only)", conflict: true },
  cancel: { button: "Cancel payout", title: "Cancel this payout?", description: "Possible only before submission. The reservation is released.", confirm: "Confirm: Cancel payout", notice: "Payout cancelled.", field: "Reason (staff only)" },
  recheck: { button: "Re-check eligibility", title: "Re-check eligibility?", description: "Runs all 22 checks again and records the decision. It never lowers the approval tier.", confirm: "Confirm: Re-check", notice: "Eligibility re-checked." },
  status: { button: "Query provider status", title: "Ask the provider for this payout's status?", description: "A status query by our reference. The answer is applied through the state machine; there is no manual override.", confirm: "Confirm: Query status", notice: "Status queried." },
  investigate: { button: "Investigate", title: "Take ownership of this exception?", description: "You become the investigation owner.", confirm: "Confirm: Investigate", notice: "You are now investigating this exception.", field: "Note (staff only)" },
  resolve: { button: "Resolve", title: "Resolve this exception?", description: "Records your resolution. It never moves money or changes the payout; corrections are separate ledger adjustments.", confirm: "Confirm: Resolve", notice: "Exception resolved.", field: "Note (staff only)", resolution: true },
  reconcile: { button: "Run reconciliation", title: "Run payout reconciliation now?", description: "Compares requests, reservations, ledger journals and the provider's status. Settlement against statements is not performed.", confirm: "Confirm: Run", notice: "Reconciliation run finished." },
};

const RESOLUTIONS = ["CONFIRMED_NO_ACTION", "CORRECTED_BY_ADJUSTMENT", "PROVIDER_CONFIRMED", "DUPLICATE_DETECTION", "ESCALATED_INCIDENT", "OTHER"];

export function PayoutAction({ target, api = payoutsApi }: { target: PayoutActionTarget; api?: PayoutsApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const copy = COPY[target.kind];
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | undefined>();
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const text = String(form.get("text") ?? "").trim();
    if (copy.field && ([...text].length < 5 || [...text].length > 2000)) {
      setFieldError("Write 5 to 2,000 characters.");
      return;
    }
    if (copy.conflict && form.get("conflict") !== "yes") {
      setFieldError(undefined);
      setError("Confirm that you have no conflict of interest with this payout.");
      return;
    }
    setFieldError(undefined);
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => {
        switch (target.kind) {
          case "approve":
          case "reject":
          case "cancel":
            return api.staffAction(target.id, target.kind, text);
          case "recheck":
            return api.staffAction(target.id, "eligibility-checks", "");
          case "status":
            return api.staffAction(target.id, "status-checks", "");
          case "investigate":
            return api.exceptionAction(target.id, "investigate", text);
          case "resolve":
            return api.exceptionAction(target.id, "resolve", text, String(form.get("resolution_code") ?? "CONFIRMED_NO_ACTION"));
          case "reconcile":
            return api.runReconciliation();
        }
      });
      setOpen(false);
      setNotice(copy.notice);
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
    <div className="space-y-3">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <Button
        variant="outline"
        onClick={() => {
          setNotice(null);
          setError(null);
          setOpen(true);
        }}
      >
        {copy.button}
      </Button>
      {open ? (
        <Dialog title={copy.title} description={copy.description} onClose={() => setOpen(false)}>
          <form noValidate onSubmit={submit} className="space-y-5">
            <FormError ref={errorRef} message={error} />
            {copy.field ? <TextAreaField label={copy.field} name="text" required maxLength={2000} error={fieldError} /> : null}
            {copy.resolution ? (
              <SelectField label="Resolution" name="resolution_code" options={RESOLUTIONS.map((r) => ({ value: r, label: r.replaceAll("_", " ").toLowerCase() }))} />
            ) : null}
            {copy.conflict ? (
              <div className="flex items-start gap-3">
                <input id={`conflict-${target.kind}`} name="conflict" value="yes" type="checkbox" className="mt-1 size-6 shrink-0 accent-brand-700" />
                <label htmlFor={`conflict-${target.kind}`} className="text-ink-900">
                  I have no conflict of interest with this payout, its campaign or its owner.
                </label>
              </div>
            ) : null}
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy} busyLabel="Working…">
                {copy.confirm}
              </SubmitButton>
              <Button variant="outline" onClick={() => setOpen(false)}>
                Cancel
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}
    </div>
  );
}
