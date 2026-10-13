"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";

import { ReasonDialog } from "@/components/admin/payments/payment-action";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormStatus } from "@/components/forms/form-error";
import { Button } from "@/components/ui/button";
import { ledgerApi, type AccountAction, type AdjustmentAction, type LedgerApi, type PeriodAction, type ReconAction } from "@/lib/ledger/api";
import { describePaymentError } from "@/lib/payments/errors";

/**
 * Ledger staff actions (reason + fresh MFA via useStepUp; every one audited). None of them posts a free-form journal:
 * a correction is a reversal REQUEST that a different person must approve.
 */
export type LedgerActionTarget =
  | { kind: "account"; id: string; action: AccountAction }
  | { kind: "period"; id: string; action: PeriodAction }
  | { kind: "adjustment"; id: string; action: AdjustmentAction }
  | { kind: "recon"; id: string; action: ReconAction }
  | { kind: "reversal"; transactionId: string };

interface Copy {
  button: string;
  title: string;
  description: string;
  confirm: string;
  notice: string;
}

function copyFor(t: LedgerActionTarget): Copy {
  switch (t.kind) {
    case "account":
      return {
        freeze: { button: "Freeze account", title: "Freeze this account?", description: "A frozen account accepts only maker-checker approved adjustments. Existing entries are not changed.", confirm: "Confirm: Freeze", notice: "Account frozen." },
        unfreeze: { button: "Unfreeze account", title: "Unfreeze this account?", description: "Ordinary postings resume.", confirm: "Confirm: Unfreeze", notice: "Account unfrozen." },
        close: { button: "Close account", title: "Close this account?", description: "Only an account with a zero balance can be closed. A closed account accepts no postings; its history stays.", confirm: "Confirm: Close", notice: "Account closed." },
      }[t.action];
    case "period":
      return {
        "request-close": { button: "Request period close", title: "Request closing this period?", description: "Maker step. A different person must approve the close; the trial balance must balance.", confirm: "Confirm: Request close", notice: "Close requested." },
        "approve-close": { button: "Approve period close", title: "Approve closing this period?", description: "Checker step (you cannot approve your own request). Later postings for this period go to the current period, marked prior-period, or are refused.", confirm: "Confirm: Approve close", notice: "Period closed." },
        "abort-close": { button: "Abort close", title: "Abort the close?", description: "The period returns to OPEN.", confirm: "Confirm: Abort", notice: "Close aborted." },
      }[t.action];
    case "adjustment":
      return {
        approve: { button: "Approve and post", title: "Approve this adjustment?", description: "Checker step: you cannot approve your own request. The journal(s) are posted immediately with exactly the requested lines.", confirm: "Confirm: Approve", notice: "Adjustment approved and posted." },
        reject: { button: "Reject", title: "Reject this adjustment?", description: "Nothing is posted.", confirm: "Confirm: Reject", notice: "Adjustment rejected." },
        withdraw: { button: "Withdraw", title: "Withdraw your request?", description: "Nothing is posted.", confirm: "Confirm: Withdraw", notice: "Adjustment withdrawn." },
      }[t.action];
    case "recon":
      return {
        investigate: { button: "Investigate", title: "Take ownership of this exception?", description: "You become the investigation owner.", confirm: "Confirm: Investigate", notice: "You are now investigating this exception." },
        resolve: { button: "Resolve", title: "Resolve this exception?", description: "Records your resolution note. It never changes the ledger; corrections are separate adjustments.", confirm: "Confirm: Resolve", notice: "Exception resolved." },
      }[t.action];
    case "reversal":
      return { button: "Request reversal", title: "Request a reversal of this journal?", description: "Creates a maker-checker request for the exact mirror of this journal. Nothing is posted until a different person approves it. The original journal is never changed.", confirm: "Confirm: Request reversal", notice: "Reversal requested. A different person must approve it." };
  }
}

export function LedgerAction({ target, api = ledgerApi }: { target: LedgerActionTarget; api?: LedgerApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const copy = copyFor(target);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function run(reason: string) {
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => {
        switch (target.kind) {
          case "account":
            return api.accountAction(target.id, target.action, reason);
          case "period":
            return api.periodAction(target.id, target.action, reason);
          case "adjustment":
            return api.adjustmentAction(target.id, target.action, reason);
          case "recon":
            return api.reconAction(target.id, target.action, reason);
          case "reversal":
            return api.requestReversal(target.transactionId, reason, crypto.randomUUID());
        }
      });
      setOpen(false);
      setNotice(copy.notice);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        setError(describePaymentError(err).message);
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
        <ReasonDialog
          title={copy.title}
          description={copy.description}
          field="Reason (staff only)"
          name="reason"
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
