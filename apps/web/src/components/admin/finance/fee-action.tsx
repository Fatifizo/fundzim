"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { feesApi, type FeesApi } from "@/lib/fees/api";
import { describeFeeError } from "@/lib/fees/errors";

/**
 * Staff fee actions (Stage 12). Every one is audited and needs a fresh MFA code (useStepUp retries once). The maker
 * of a fee change can never be its checker (the API also refuses linked accounts). None of them moves money.
 */
export type FeeActionTarget =
  | { kind: "submit" | "activate"; id: string }
  | { kind: "approve" | "reject"; id: string; termsHash: string | null }
  | { kind: "suspend" | "reactivate" | "retire"; id: string }
  | { kind: "schedule-approve" | "schedule-retire"; id: string }
  | { kind: "adjustment-approve" | "adjustment-reject"; id: string }
  | { kind: "recon-run" }
  | { kind: "recon-resolve"; id: string };

interface Copy {
  button: string;
  title: string;
  description: string;
  confirm: string;
  notice: string;
  field?: string;
}

const COPY: Record<FeeActionTarget["kind"], Copy> = {
  submit: { button: "Submit for approval", title: "Submit this draft for approval?", description: "The terms are frozen and hashed. A different person must approve them.", confirm: "Confirm: Submit", notice: "Submitted for approval." },
  approve: { button: "Approve version", title: "Approve this fee policy version?", description: "Checker step. You cannot approve a version you drafted or submitted, or one made by an account linked to yours. Your approval binds to the terms hash you reviewed.", confirm: "Confirm: Approve", notice: "Version approved.", field: "Reason (staff only)" },
  reject: { button: "Reject version", title: "Reject this fee policy version?", description: "A rejected version can never be activated; draft a new version instead.", confirm: "Confirm: Reject", notice: "Version rejected.", field: "Reason (staff only)" },
  activate: { button: "Activate", title: "Activate this version?", description: "It applies from its effective start. Active fee quotes in this currency are invalidated.", confirm: "Confirm: Activate", notice: "Version activated." },
  suspend: { button: "Suspend", title: "Suspend this version?", description: "It stops applying immediately. Active fee quotes in this currency are invalidated.", confirm: "Confirm: Suspend", notice: "Version suspended.", field: "Reason (staff only)" },
  reactivate: { button: "Reactivate", title: "Reactivate this version?", description: "Checker step: the person who suspended it cannot reactivate it.", confirm: "Confirm: Reactivate", notice: "Version reactivated.", field: "Reason (staff only)" },
  retire: { button: "Retire", title: "Retire this version?", description: "Retired versions never apply again. Historical fees keep their version.", confirm: "Confirm: Retire", notice: "Version retired.", field: "Reason (staff only)" },
  "schedule-approve": { button: "Approve evidence", title: "Approve this provider fee evidence?", description: "Checker step: the person who recorded it cannot approve it. Approval does not make it VERIFIED.", confirm: "Confirm: Approve", notice: "Provider fee evidence approved." },
  "schedule-retire": { button: "Retire evidence", title: "Retire this provider fee evidence?", description: "Provider fees without evidence are UNKNOWN.", confirm: "Confirm: Retire", notice: "Provider fee evidence retired.", field: "Reason (staff only)" },
  "adjustment-approve": { button: "Approve adjustment", title: "Approve this fee adjustment?", description: "Checker step: the requester cannot approve. It never posts: correcting the books is a separate ledger adjustment.", confirm: "Confirm: Approve", notice: "Adjustment approved.", field: "Reason (staff only)" },
  "adjustment-reject": { button: "Reject adjustment", title: "Reject this fee adjustment?", description: "The assessed fee is unchanged.", confirm: "Confirm: Reject", notice: "Adjustment rejected.", field: "Reason (staff only)" },
  "recon-run": { button: "Run fee reconciliation", title: "Run fee reconciliation now?", description: "Compares calculations, assessments, ledger journals and provider-reported fees. Settlement against statements is not performed.", confirm: "Confirm: Run", notice: "Fee reconciliation run finished." },
  "recon-resolve": { button: "Resolve", title: "Resolve this reconciliation item?", description: "Records your note. It never changes a fee, an assessment or the ledger.", confirm: "Confirm: Resolve", notice: "Item resolved.", field: "Note (staff only)" },
};

// available=false keeps the component mounted without its button, so a confirmation notice survives the refresh
// that moves the record out of the state where this action applies
export function FeeAction({ target, available = true, api = feesApi }: { target: FeeActionTarget; available?: boolean; api?: FeesApi }) {
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
    setFieldError(undefined);
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => {
        switch (target.kind) {
          case "submit":
          case "activate":
          case "suspend":
          case "reactivate":
          case "retire":
            return api.versionAction(target.id, target.kind, text);
          case "approve":
          case "reject":
            return api.versionAction(target.id, target.kind, text, form.get("bind") === "yes" ? target.termsHash : null);
          case "schedule-approve":
            return api.scheduleAction(target.id, "approve", "");
          case "schedule-retire":
            return api.scheduleAction(target.id, "retire", text);
          case "adjustment-approve":
            return api.adjustmentAction(target.id, "approve", text);
          case "adjustment-reject":
            return api.adjustmentAction(target.id, "reject", text);
          case "recon-run":
            return api.runReconciliation();
          case "recon-resolve":
            return api.resolveRecon(target.id, text);
        }
      });
      setOpen(false);
      setNotice(copy.notice);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        setError(describeFeeError(err).message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  if (!available && !notice) return null;
  const hash = target.kind === "approve" || target.kind === "reject" ? target.termsHash : null;
  return (
    <div className="space-y-3">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      {available ? (
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
      ) : null}
      {open && available ? (
        <Dialog title={copy.title} description={copy.description} onClose={() => setOpen(false)}>
          <form noValidate onSubmit={submit} className="space-y-5">
            <FormError ref={errorRef} message={error} />
            {copy.field ? <TextAreaField label={copy.field} name="text" required maxLength={2000} error={fieldError} /> : null}
            {hash ? (
              <div className="flex items-start gap-3">
                <input id={`bind-${target.kind}`} name="bind" value="yes" type="checkbox" defaultChecked className="mt-1 size-6 shrink-0 accent-brand-700" />
                <label htmlFor={`bind-${target.kind}`} className="text-ink-900">
                  Bind my decision to the terms I reviewed (terms hash <code className="break-all">{hash.slice(0, 16)}…</code>)
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
