"use client";

import { useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { humanise } from "@/lib/campaigns/labels";
import { contentProblem } from "@/lib/campaigns/text";
import type { CampaignUpdate } from "@/lib/campaigns/types";
import { LIMITS } from "@/lib/campaigns/validate";

import { StoryText } from "./story-text";

const UPDATE_STATUS: Record<string, string> = {
  PUBLISHED: "Published",
  PENDING_MODERATION: "Waiting for moderation",
  DRAFT: "Draft",
  HIDDEN: "Hidden by FundZim",
};

/** Owner updates (plain text, contract §9). Updates can be posted while the campaign is live or completed. */
export function UpdatesManager({ campaignId, initial, canPost, api = campaignApi }: { campaignId: string; initial: CampaignUpdate[]; canPost: boolean; api?: CampaignApi }) {
  const [updates, setUpdates] = useState(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const errorRef = useRef<HTMLDivElement>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const formRef = useRef<HTMLFormElement>(null);

  async function reload() {
    try {
      setUpdates(await api.listUpdates(campaignId));
    } catch {
      // keep the list
    }
  }

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const title = String(data.get("title") ?? "").trim();
    const body = String(data.get("body") ?? "").trim();
    const errs: Record<string, string> = {};
    if (title.length < LIMITS.updateTitle.min || title.length > LIMITS.updateTitle.max) errs.title = `Use ${LIMITS.updateTitle.min}–${LIMITS.updateTitle.max} characters.`;
    if (body.length < LIMITS.updateBody.min || body.length > LIMITS.updateBody.max) errs.body = `Use ${LIMITS.updateBody.min}–${LIMITS.updateBody.max.toLocaleString("en")} characters.`;
    for (const [k, v] of Object.entries({ title, body })) if (!errs[k] && contentProblem(v)) errs[k] = "Use plain text only, without HTML tags or javascript:/data: links.";
    setErrors(errs);
    setNotice(null);
    if (Object.keys(errs).length > 0) {
      setFormError("Please correct the errors below.");
      requestAnimationFrame(() => formRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus());
      return;
    }
    setBusy(true);
    setFormError(null);
    try {
      await api.createUpdate(campaignId, { title, body, publish: true });
      form.reset();
      setNotice("Update posted. It appears on the public page once published (some campaigns' updates are checked by FundZim first).");
      requestAnimationFrame(() => noticeRef.current?.focus());
      await reload();
    } catch (err) {
      const d = describeCampaignError(err, "The update could not be posted.");
      setErrors(d.fieldErrors);
      setFormError(d.message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  async function remove(u: CampaignUpdate) {
    try {
      await api.deleteUpdate(campaignId, u.id);
      setNotice("Update deleted.");
      requestAnimationFrame(() => noticeRef.current?.focus());
      await reload();
    } catch (err) {
      setFormError(describeCampaignError(err, "The update could not be deleted.").message);
    }
  }

  return (
    <div className="space-y-8">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      {canPost ? (
        <form ref={formRef} noValidate onSubmit={onSubmit} className="space-y-5 rounded-card border border-line bg-surface p-5" aria-labelledby="new-update-title">
          <h2 id="new-update-title" className="font-display text-xl font-semibold text-ink-900">
            Post an update
          </h2>
          <FormError ref={errorRef} message={formError} />
          <TextField label="Title" name="title" maxLength={LIMITS.updateTitle.max} error={errors.title} />
          <TextAreaField label="Update" name="body" rows={6} maxLength={LIMITS.updateBody.max} hint="Plain text. Leave a blank line between paragraphs. Updates are public and may be reviewed by FundZim." error={errors.body} />
          <SubmitButton busy={busy} busyLabel="Posting…">
            Post update
          </SubmitButton>
        </form>
      ) : (
        <p className="text-ink-700">Updates can be posted once the campaign is published.</p>
      )}
      <section aria-labelledby="updates-list-title" className="space-y-3">
        <h2 id="updates-list-title" className="font-display text-xl font-semibold text-ink-900">
          Posted updates
        </h2>
        {updates.length === 0 ? <p className="text-ink-700">No updates yet.</p> : null}
        <ul className="space-y-4">
          {updates.map((u) => (
            <li key={u.id} className="rounded-card border border-line bg-surface p-5">
              <p className="flex flex-wrap items-center gap-2">
                <span className="font-semibold break-words text-ink-900">{u.title}</span>
                <Badge tone={u.status === "PUBLISHED" ? "brand" : "neutral"}>{UPDATE_STATUS[u.status] ?? humanise(u.status)}</Badge>
              </p>
              <p className="text-sm text-ink-600">{formatDateTime(u.published_at ?? u.created_at)}</p>
              <StoryText text={u.body} className="mt-2 text-ink-700" />
              {u.status !== "DELETED" ? (
                <Button variant="ghost" className="mt-2" onClick={() => void remove(u)} aria-label={`Delete update: ${u.title}`}>
                  Delete
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
