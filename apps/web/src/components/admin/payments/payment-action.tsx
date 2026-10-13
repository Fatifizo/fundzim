"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent, type RefObject } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { paymentsApi, type PaymentsApi } from "@/lib/payments/api";
import { describePaymentError } from "@/lib/payments/errors";
import { REFUND_ACTION_LABELS, REFUND_ACTION_NOTICES, reasonProblem, refundActions } from "@/lib/payments/labels";
import type { RefundAction } from "@/lib/payments/types";

/**
 * Staff payment actions that take a reason or note (interface-contracts §4), using the existing staff-action +
 * step-up pattern (useStepUp retries once after a fresh MFA code). None of them sets a payment's status: a
 * status query asks the provider, a re-queue re-runs processing, "awaiting settlement data" records reconciliation
 * progress, and a refund decision never executes a refund in Stage 8.
 *
 * The component stays mounted after success (even when the action is no longer offered) so the outcome message
 * survives the server refresh.
 */
export type PaymentActionTarget =
  | { kind: "status-query"; paymentId: string }
  | { kind: "requeue"; inboxId: string }
  | { kind: "await-settlement"; referenceId: string };

interface Copy {
  button: string;
  title: string;
  description: string;
  field: string;
  confirm: string;
  notice: string;
}

const COPY: Record<PaymentActionTarget["kind"], Copy> = {
  "status-query": {
    button: "Request provider status query",
    title: "Request a provider status query?",
    description:
      "An authenticated status query is scheduled now. The payment changes only if the provider reports an authoritative status; this never marks a payment as succeeded or failed by itself.",
    field: "Reason (staff only)",
    confirm: "Confirm: Request status query",
    notice: "Status query scheduled. The payment changes only if the provider reports an authoritative status.",
  },
  requeue: {
    button: "Re-queue",
    title: "Re-queue this webhook?",
    description: "The stored, verified webhook is processed again. Duplicates and lower-precedence statuses are still recorded as not applied.",
    field: "Reason (staff only)",
    confirm: "Confirm: Re-queue",
    notice: "Webhook re-queued for processing.",
  },
  "await-settlement": {
    button: "Mark awaiting settlement data",
    title: "Mark as awaiting settlement data?",
    description: "Records that this reference is waiting for the provider's settlement data. The payment itself is not changed.",
    field: "Note (staff only)",
    confirm: "Confirm: Awaiting settlement data",
    notice: "Marked as awaiting settlement data.",
  },
};

function useActionFeedback() {
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);
  const fail = (message: string) => {
    setError(message);
    requestAnimationFrame(() => errorRef.current?.focus());
  };
  return { error, setError, notice, setNotice, noticeRef, errorRef, fail };
}

function ReasonDialog({
  title,
  description,
  field,
  name,
  confirm,
  busy,
  error,
  errorRef,
  onSubmit,
  onClose,
}: {
  title: string;
  description: string;
  field: string;
  name: string;
  confirm: string;
  busy: boolean;
  error: string | null;
  errorRef: RefObject<HTMLDivElement | null>;
  onSubmit: (value: string) => void;
  onClose: () => void;
}) {
  const [fieldError, setFieldError] = useState<string | undefined>();
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = String(new FormData(event.currentTarget).get(name) ?? "").trim();
    const problem = reasonProblem(value);
    setFieldError(problem ?? undefined);
    if (problem) return;
    onSubmit(value);
  }
  return (
    <Dialog title={title} description={description} onClose={onClose}>
      <form noValidate onSubmit={submit} className="space-y-5">
        <FormError ref={errorRef} message={error} />
        <TextAreaField label={field} name={name} required maxLength={2000} error={fieldError} />
        <div className="flex flex-wrap gap-3">
          <SubmitButton busy={busy} busyLabel="Working…">
            {confirm}
          </SubmitButton>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
        </div>
      </form>
    </Dialog>
  );
}

export function PaymentAction({ target, available = true, api = paymentsApi }: { target: PaymentActionTarget; available?: boolean; api?: PaymentsApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const copy = COPY[target.kind];
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const { error, setError, notice, setNotice, noticeRef, errorRef, fail } = useActionFeedback();

  async function run(value: string) {
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => {
        switch (target.kind) {
          case "status-query":
            return api.requestStatusQuery(target.paymentId, value);
          case "requeue":
            return api.requeueWebhook(target.inboxId, value);
          case "await-settlement":
            return api.awaitSettlement(target.referenceId, value);
        }
      });
      setOpen(false);
      setNotice(copy.notice);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) fail(describePaymentError(err).message);
    } finally {
      setBusy(false);
    }
  }

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
      {open ? (
        <ReasonDialog
          title={copy.title}
          description={copy.description}
          field={copy.field}
          name={target.kind === "await-settlement" ? "note" : "reason"}
          confirm={copy.confirm}
          busy={busy}
          error={error}
          errorRef={errorRef}
          onSubmit={run}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </div>
  );
}

const REFUND_DESCRIPTIONS: Record<RefundAction, string> = {
  approve:
    "Approving records the decision only. Refund execution is DISABLED in Stage 8: an approved refund is not sent to any provider and no money moves. You cannot approve a refund you requested.",
  reject: "The refund request is closed without a refund.",
  hold: "The request is paused until someone resumes or rejects it.",
  resume: "The request goes back to waiting for a decision.",
  withdraw: "Your refund request is withdrawn. Only the person who requested it can withdraw it.",
};

/** Refund decision buttons for one request. Offered per status and requester; the API enforces maker ≠ checker. */
export function RefundActions({ refundId, status, isRequester, api = paymentsApi }: { refundId: string; status: string; isRequester: boolean; api?: PaymentsApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [action, setAction] = useState<RefundAction | null>(null);
  const [busy, setBusy] = useState(false);
  const { error, setError, notice, setNotice, noticeRef, errorRef, fail } = useActionFeedback();
  const actions = refundActions(status, isRequester);

  async function run(value: string) {
    if (!action) return;
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.refundAction(refundId, action, value));
      setNotice(REFUND_ACTION_NOTICES[action]);
      setAction(null);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) fail(describePaymentError(err, "The decision could not be recorded.").message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-3">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      {actions.length > 0 ? (
        <div className="flex flex-wrap gap-3">
          {actions.map((a) => (
            <Button
              key={a}
              variant={a === "approve" ? "primary" : "outline"}
              onClick={() => {
                setNotice(null);
                setError(null);
                setAction(a);
              }}
            >
              {REFUND_ACTION_LABELS[a]}
            </Button>
          ))}
        </div>
      ) : null}
      {action ? (
        <ReasonDialog
          title={`${REFUND_ACTION_LABELS[action]} this refund request?`}
          description={REFUND_DESCRIPTIONS[action]}
          field="Reason (staff only)"
          name="reason"
          confirm={`Confirm: ${REFUND_ACTION_LABELS[action]}`}
          busy={busy}
          error={error}
          errorRef={errorRef}
          onSubmit={run}
          onClose={() => setAction(null)}
        />
      ) : null}
    </div>
  );
}
