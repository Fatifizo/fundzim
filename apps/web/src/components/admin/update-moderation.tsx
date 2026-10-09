"use client";

import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { StoryText } from "@/components/campaigns/story-text";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { SelectField, TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { humanise } from "@/lib/campaigns/labels";
import type { ModerationItem } from "@/lib/campaigns/types";

const REASONS: Record<"approve" | "hide", string[]> = {
  approve: ["MEETS_POLICY", "OTHER"],
  hide: ["INAPPROPRIATE_CONTENT", "PRIVACY", "MISLEADING", "SPAM", "OTHER"],
};

/** Updates moderation queue (`content.moderate`). Each decision needs a reason code and an internal note. */
export function UpdateModeration({ items, api = campaignApi }: { items: ModerationItem[]; api?: CampaignApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [target, setTarget] = useState<{ item: ModerationItem; action: "approve" | "hide" } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!target) return;
    const data = new FormData(event.currentTarget);
    const reason_code = String(data.get("reason_code") ?? "");
    const note = String(data.get("note") ?? "").trim();
    const errs: Record<string, string> = {};
    if (!reason_code) errs.reason_code = "Choose a reason.";
    if (note.length < 3 || note.length > 5000) errs.note = "Write an internal note of 3 to 5,000 characters.";
    setFieldErrors(errs);
    if (Object.keys(errs).length > 0) {
      setError("Please correct the errors below.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.moderateUpdate(target.item.id, target.action, { reason_code, note }));
      setNotice(target.action === "approve" ? "Update approved." : "Update hidden.");
      setTarget(null);
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        setError(describeCampaignError(err, "The decision could not be recorded.").message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      {items.length === 0 ? <EmptyState title="Nothing to moderate" description="New and reported updates appear here." /> : null}
      <ul className="space-y-4">
        {items.map((u) => (
          <li key={u.id} className="rounded-card border border-line bg-surface p-5">
            <p className="text-sm text-ink-600">
              {u.campaign_title ? <span className="break-words">{u.campaign_title} · </span> : null}
              {humanise(u.status)} · {formatDateTime(u.created_at)}
            </p>
            <h2 className="mt-1 font-semibold break-words text-ink-900">{u.title}</h2>
            <StoryText text={u.body} className="mt-2 text-ink-700" />
            <div className="mt-3 flex flex-wrap gap-3">
              <Button variant="primary" onClick={() => { setNotice(null); setTarget({ item: u, action: "approve" }); }} aria-label={`Approve update: ${u.title}`}>
                Approve
              </Button>
              <Button variant="outline" onClick={() => { setNotice(null); setTarget({ item: u, action: "hide" }); }} aria-label={`Hide update: ${u.title}`}>
                Hide
              </Button>
            </div>
          </li>
        ))}
      </ul>
      {target ? (
        <Dialog title={target.action === "approve" ? "Approve this update?" : "Hide this update?"} description={target.action === "hide" ? "The update is removed from the public page. The owner is told it was hidden." : "The update stays public."} onClose={() => setTarget(null)}>
          <form noValidate onSubmit={onSubmit} className="space-y-5">
            <FormError ref={errorRef} message={error} />
            <SelectField label="Reason" name="reason_code" placeholderOption="Choose a reason" options={REASONS[target.action].map((c) => ({ value: c, label: humanise(c) }))} error={fieldErrors.reason_code} />
            <TextAreaField label="Internal note (staff only)" name="note" required maxLength={5000} error={fieldErrors.note} />
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy} busyLabel="Working…">
                {target.action === "approve" ? "Approve" : "Hide"}
              </SubmitButton>
              <Button variant="outline" onClick={() => setTarget(null)}>
                Cancel
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}
    </div>
  );
}
