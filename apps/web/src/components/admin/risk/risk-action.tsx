"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { humanise } from "@/lib/payments/labels";
import { riskApi, type HoldAction, type RiskApi, type RuleAction } from "@/lib/risk/api";
import { describeRiskError } from "@/lib/risk/errors";
import { CASE_OUTCOMES, SIGNAL_REVIEW_CLASSES } from "@/lib/risk/types";

/**
 * Staff risk actions (Stage 13). Every one is audited and needs a fresh MFA code (useStepUp retries once). A maker is
 * never the checker of the same decision (the API also refuses linked accounts and anyone linked to the subject).
 * None of them moves money, declares a finding about a person or applies a legal restriction.
 */
export type RiskActionTarget =
  | { kind: `rule-${RuleAction}`; id: string; definitionHash?: string | null }
  | { kind: "case-assign" | "case-escalate" | "case-propose" | "case-approve" | "case-return" | "case-reopen"; id: string }
  | { kind: "case-transition"; id: string; options: string[] }
  | { kind: "case-review-signal"; id: string; signalId: string }
  | { kind: `hold-${HoldAction}`; id: string }
  | { kind: "screening-confirm" | "screening-clear" | "screening-retry"; id: string };

type Need = "required" | "optional" | "none";
interface Copy {
  button: string;
  title: string;
  description: string;
  confirm: string;
  notice: string;
  reason: Need;
  reasonLabel?: string;
}

const R = "Reason (staff only)";
const COPY: Record<RiskActionTarget["kind"], Copy> = {
  "rule-submit": { button: "Submit for approval", title: "Submit this rule version for approval?", description: "The definition is frozen and hashed. A different person must approve it.", confirm: "Confirm: Submit", notice: "Submitted for approval.", reason: "none" },
  "rule-approve": { button: "Approve version", title: "Approve this rule version?", description: "Checker step. You cannot approve a version you drafted or submitted, or one made by an account linked to yours. Your approval binds to the definition hash you reviewed.", confirm: "Confirm: Approve", notice: "Version approved.", reason: "required" },
  "rule-reject": { button: "Reject version", title: "Reject this rule version?", description: "A rejected version can never be activated; draft a new version instead.", confirm: "Confirm: Reject", notice: "Version rejected.", reason: "required" },
  "rule-activate": { button: "Activate", title: "Activate this rule version?", description: "It applies to new signals from now on. The rule's previous active version is retired.", confirm: "Confirm: Activate", notice: "Version activated.", reason: "none" },
  "rule-suspend": { button: "Suspend", title: "Suspend this rule version?", description: "It stops applying to new signals immediately.", confirm: "Confirm: Suspend", notice: "Version suspended.", reason: "required" },
  "rule-reactivate": { button: "Reactivate", title: "Reactivate this rule version?", description: "Checker step: the person who suspended it cannot reactivate it.", confirm: "Confirm: Reactivate", notice: "Version reactivated.", reason: "required" },
  "rule-retire": { button: "Retire", title: "Retire this rule version?", description: "Retired versions never apply again. Past evaluations keep their version.", confirm: "Confirm: Retire", notice: "Version retired.", reason: "required" },
  "case-assign": { button: "Assign", title: "Assign this case?", description: "Leave the assignee empty to take it yourself. The assignee needs risk case permission and is never the subject or linked to it.", confirm: "Confirm: Assign", notice: "Case assigned.", reason: "optional" },
  "case-transition": { button: "Change status", title: "Change this case's status?", description: "Working steps only; decisions and escalation have their own actions.", confirm: "Confirm: Change status", notice: "Case status changed.", reason: "optional" },
  "case-escalate": { button: "Escalate to compliance", title: "Escalate this case to compliance?", description: "Compliance opens a review case. Only compliance can apply a restriction.", confirm: "Confirm: Escalate", notice: "Case escalated to compliance.", reason: "required" },
  "case-propose": { button: "Propose outcome", title: "Propose an outcome for this case?", description: "Maker step: a different person must approve the outcome.", confirm: "Confirm: Propose", notice: "Outcome proposed.", reason: "required" },
  "case-approve": { button: "Approve outcome", title: "Approve the proposed outcome?", description: "Checker step. No further action and false positive release the case's holds; the other outcomes keep them as manual holds.", confirm: "Confirm: Approve", notice: "Outcome approved.", reason: "required" },
  "case-return": { button: "Return proposal", title: "Return the proposal for more investigation?", description: "The case goes back to investigation.", confirm: "Confirm: Return", notice: "Proposal returned.", reason: "required" },
  "case-reopen": { button: "Reopen case", title: "Reopen this case?", description: "The previous decision stays in the case history.", confirm: "Confirm: Reopen", notice: "Case reopened.", reason: "required" },
  "case-review-signal": { button: "Review signal", title: "Review this signal?", description: "A false positive stays recorded and labelled; signals are never deleted.", confirm: "Confirm: Review", notice: "Signal reviewed.", reason: "required", reasonLabel: "Explanation (staff only)" },
  "hold-activate": { button: "Activate hold", title: "Activate this requested hold?", description: "Second-person step: the person who requested it cannot activate it.", confirm: "Confirm: Activate", notice: "Hold activated.", reason: "optional" },
  "hold-revoke": { button: "Revoke request", title: "Revoke this hold request?", description: "The hold never takes effect.", confirm: "Confirm: Revoke", notice: "Hold request revoked.", reason: "required" },
  "hold-review": { button: "Mark under review", title: "Mark this hold under review?", description: "The hold stays in force while it is reviewed.", confirm: "Confirm: Mark under review", notice: "Hold under review.", reason: "none" },
  "hold-request-release": { button: "Request release", title: "Request the release of this hold?", description: "Maker step: the hold stays in force until a different person approves the release.", confirm: "Confirm: Request release", notice: "Release requested.", reason: "required" },
  "hold-approve-release": { button: "Approve release", title: "Approve the release of this hold?", description: "Checker step: never the requester, anyone linked to them or anyone linked to the held account.", confirm: "Confirm: Approve release", notice: "Hold released.", reason: "required" },
  "hold-reject-release": { button: "Reject release", title: "Reject the release of this hold?", description: "The hold stays in force.", confirm: "Confirm: Reject release", notice: "Release rejected.", reason: "required" },
  "screening-confirm": { button: "Confirm match (TEST)", title: "Confirm this potential match?", description: "Analyst decision on synthetic TEST data. The case is escalated to compliance; no restriction is applied here. You cannot review a screening you requested.", confirm: "Confirm: Confirm match", notice: "Match confirmed (TEST); escalated to compliance.", reason: "required" },
  "screening-clear": { button: "Clear potential match (TEST)", title: "Clear this potential match?", description: "Analyst decision on synthetic TEST data. You cannot review a screening you requested.", confirm: "Confirm: Clear", notice: "Potential match cleared (TEST).", reason: "required" },
  "screening-retry": { button: "Retry screening", title: "Retry this screening request?", description: "A screening in error is never treated as cleared; retrying submits it again to the TEST simulator.", confirm: "Confirm: Retry", notice: "Screening resubmitted.", reason: "none" },
};

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// available=false keeps the component mounted without its button, so a confirmation notice survives the refresh
// that moves the record out of the state where this action applies
export function RiskAction({ target, available = true, api = riskApi }: { target: RiskActionTarget; available?: boolean; api?: RiskApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const copy = COPY[target.kind];
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | undefined>();
  const [assigneeError, setAssigneeError] = useState<string | undefined>();
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
    const choice = String(form.get("choice") ?? "");
    const assignee = String(form.get("assignee") ?? "").trim();
    const n = [...text].length;
    if ((copy.reason === "required" && (n < 5 || n > 2000)) || (copy.reason === "optional" && n > 0 && (n < 3 || n > 2000))) {
      setFieldError(copy.reason === "required" ? "Write 5 to 2,000 characters." : "Write 3 to 2,000 characters, or leave it empty.");
      return;
    }
    setFieldError(undefined);
    if (target.kind === "case-assign" && assignee && !UUID.test(assignee)) {
      setAssigneeError("Paste the staff member's id, or leave it empty to take the case yourself.");
      return;
    }
    setAssigneeError(undefined);
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => {
        const t = target;
        switch (t.kind) {
          case "rule-submit":
          case "rule-approve":
          case "rule-reject":
          case "rule-activate":
          case "rule-suspend":
          case "rule-reactivate":
          case "rule-retire":
            return api.ruleAction(t.id, t.kind.slice(5) as RuleAction, text, form.get("bind") === "yes" ? (t.definitionHash ?? null) : null);
          case "case-assign":
            return api.caseAction(t.id, "assign", { ...(assignee ? { assignee_id: assignee } : {}), ...(text ? { reason: text } : {}) });
          case "case-transition":
            return api.caseAction(t.id, "transition", { to: choice, ...(text ? { reason: text } : {}) });
          case "case-escalate":
            return api.caseAction(t.id, "escalate", { reason: text });
          case "case-propose":
            return api.caseAction(t.id, "propose-decision", { outcome: choice, reason: text });
          case "case-approve":
            return api.caseAction(t.id, "approve-decision", { reason: text });
          case "case-return":
            return api.caseAction(t.id, "return-decision", { reason: text });
          case "case-reopen":
            return api.caseAction(t.id, "reopen", { reason: text });
          case "case-review-signal":
            return api.caseAction(t.id, "signal-reviews", { signal_id: t.signalId, classification: choice, explanation: text } as Record<string, string>);
          case "screening-confirm":
            return api.reviewScreening(t.id, "CONFIRMED_MATCH", text);
          case "screening-clear":
            return api.reviewScreening(t.id, "CLEARED", text);
          case "screening-retry":
            return api.retryScreening(t.id);
          default:
            return api.holdAction(t.id, t.kind.slice(5) as HoldAction, text);
        }
      });
      setOpen(false);
      setNotice(copy.notice);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        setError(describeRiskError(err).message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  if (!available && !notice) return null;
  const hash = target.kind === "rule-approve" ? (target.definitionHash ?? null) : null;
  const choices =
    target.kind === "case-transition"
      ? { label: "New status", values: target.options }
      : target.kind === "case-propose"
        ? { label: "Outcome", values: [...CASE_OUTCOMES] as string[] }
        : target.kind === "case-review-signal"
          ? { label: "Classification", values: [...SIGNAL_REVIEW_CLASSES] as string[] }
          : null;
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
            {choices ? <SelectField label={choices.label} name="choice" options={choices.values.map((v) => ({ value: v, label: humanise(v) }))} /> : null}
            {target.kind === "case-assign" ? <TextField label="Assignee id (optional)" name="assignee" autoComplete="off" hint="Leave empty to take the case yourself." error={assigneeError} /> : null}
            {copy.reason !== "none" ? (
              <TextAreaField label={copy.reasonLabel ?? (copy.reason === "optional" ? "Reason (optional, staff only)" : R)} name="text" required={copy.reason === "required"} maxLength={2000} error={fieldError} />
            ) : null}
            {hash ? (
              <div className="flex items-start gap-3">
                <input id={`bind-${target.id}`} name="bind" value="yes" type="checkbox" defaultChecked className="mt-1 size-6 shrink-0 accent-brand-700" />
                <label htmlFor={`bind-${target.id}`} className="text-ink-900">
                  Bind my decision to the definition I reviewed (definition hash <code className="break-all">{hash.slice(0, 16)}…</code>)
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
