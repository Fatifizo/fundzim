"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { useStepUp } from "@/components/auth/step-up";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { StepUpCancelledError } from "@/components/auth/step-up";
import { isAuthRequired } from "@/lib/auth/errors";
import { loginUrl } from "@/lib/auth/safe-redirect";
import { browserNavigation } from "@/lib/browser-navigation";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { COMPLETION_REASONS, OWNER_ACTION_DESCRIPTIONS, OWNER_ACTION_LABELS, ownerActions, type OwnerAction } from "@/lib/campaigns/labels";
import type { EligibilityReason } from "@/lib/campaigns/types";

import { EligibilityList } from "./eligibility-list";

const DONE: Record<OwnerAction, string> = {
  submit: "Your campaign was submitted for review. Reviewers may approve it, ask for changes or decline it.",
  withdraw: "Your campaign was withdrawn from review and is a draft again.",
  publish: "Your campaign is published.",
  pause: "Your campaign is paused.",
  resume: "Your campaign is active again.",
  complete: "Your campaign is completed.",
  cancel: "Your campaign was cancelled.",
  archive: "Your campaign was archived.",
  revise: "Your campaign is a draft again. Make your changes, then submit it for review.",
};

/**
 * Owner lifecycle buttons for a campaign's status (ADR-036 edges). Each opens a confirmation dialog; the
 * API re-checks eligibility and answers 422 NOT_ELIGIBLE with reasons, which are listed with fix links.
 * `only` restricts the buttons (the settings page shows pause/resume/complete/cancel/archive).
 */
export function LifecycleActions({ campaignId, status, organisationId, only, api = campaignApi }: { campaignId: string; status: string; organisationId: string | null; only?: readonly OwnerAction[]; api?: CampaignApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [action, setAction] = useState<OwnerAction | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldError, setFieldError] = useState<string | undefined>();
  const [reasons, setReasons] = useState<EligibilityReason[]>([]);
  const [done, setDone] = useState<string | null>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const doneRef = useRef<HTMLDivElement>(null);
  const actions = ownerActions(status).filter((a) => !only || only.includes(a));

  useEffect(() => {
    if (done) doneRef.current?.focus();
  }, [done]);

  function close() {
    setAction(null);
    setError(null);
    setReasons([]);
    setFieldError(undefined);
  }

  async function onConfirm(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!action) return;
    const form = new FormData(event.currentTarget);
    const reason = String(form.get("reason") ?? "");
    const note = String(form.get("note") ?? "").trim();
    if (action === "complete" && !reason) {
      setFieldError("Choose why the campaign is ending.");
      setError("Please correct the errors below.");
      return;
    }
    setBusy(true);
    setError(null);
    setReasons([]);
    try {
      await withStepUp(() => api.lifecycle(campaignId, action, action === "complete" ? { reason, ...(note ? { note } : {}) } : {}));
      const message = DONE[action];
      close();
      setDone(message);
      router.refresh();
    } catch (err) {
      if (err instanceof StepUpCancelledError) return;
      if (isAuthRequired(err)) return browserNavigation.assign(loginUrl(browserNavigation.currentPath()));
      const d = describeCampaignError(err, "This could not be done. Please try again.");
      setReasons(d.reasons);
      setError(d.message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  if (actions.length === 0 && !done) return <p className="text-ink-700">No actions are available in the campaign&apos;s current status.</p>;

  return (
    <div className="space-y-4">
      {done ? <FormStatus ref={doneRef} title={done} /> : null}
      <div className="flex flex-col gap-3 sm:flex-row sm:flex-wrap">
        {actions.map((a) => (
          <Button key={a} variant={a === "submit" || a === "publish" || a === "resume" || a === "revise" ? "primary" : a === "cancel" ? "outline" : "secondary"} onClick={() => { setDone(null); setAction(a); }}>
            {OWNER_ACTION_LABELS[a]}
          </Button>
        ))}
      </div>
      {action ? (
        <Dialog title={`${OWNER_ACTION_LABELS[action]}?`} description={OWNER_ACTION_DESCRIPTIONS[action]} onClose={close}>
          <form noValidate onSubmit={onConfirm} className="space-y-5">
            <FormError ref={errorRef} message={error} />
            {reasons.length > 0 ? <EligibilityList reasons={reasons} allowed={false} allowedText="" context={{ campaignId, organisationId }} /> : null}
            {action === "complete" ? (
              <>
                <SelectField label="Why is the campaign ending?" name="reason" placeholderOption="Choose a reason" options={COMPLETION_REASONS} error={fieldError} hint="Shown to you and to reviewers. It never says that money was collected." />
                <TextAreaField label="Note (optional)" name="note" rows={3} maxLength={2000} hint="Plain text, up to 2,000 characters." />
              </>
            ) : null}
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy} busyLabel="Working…">
                {`Yes, ${OWNER_ACTION_LABELS[action].toLowerCase()}`}
              </SubmitButton>
              <Button variant="outline" onClick={close}>
                No, go back
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}
    </div>
  );
}
