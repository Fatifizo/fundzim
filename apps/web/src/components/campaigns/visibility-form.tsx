"use client";

import { useRouter } from "next/navigation";
import { useRef, useState, type FormEvent } from "react";

import { FormError, FormStatus } from "@/components/forms/form-error";
import { SubmitButton } from "@/components/forms/submit-button";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { VISIBILITY_LABELS } from "@/lib/campaigns/labels";
import { VISIBILITIES, type Visibility } from "@/lib/campaigns/types";

/** Listing choice (contract §3): PUBLIC or UNLISTED, sent as PATCH /campaigns/{id} with If-Match. */
export function VisibilityForm({ campaignId, visibility, version, api = campaignApi }: { campaignId: string; visibility: string; version: number; api?: CampaignApi }) {
  const router = useRouter();
  const [value, setValue] = useState<Visibility>(visibility === "UNLISTED" ? "UNLISTED" : "PUBLIC");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const errorRef = useRef<HTMLDivElement>(null);
  const savedRef = useRef<HTMLDivElement>(null);

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await api.update(campaignId, { visibility: value }, version);
      setSaved(true);
      requestAnimationFrame(() => savedRef.current?.focus());
      router.refresh();
    } catch (err) {
      setError(describeCampaignError(err, "The setting could not be saved.").message);
      requestAnimationFrame(() => errorRef.current?.focus());
    } finally {
      setBusy(false);
    }
  }

  return (
    <form noValidate onSubmit={onSubmit} className="space-y-4">
      <FormError ref={errorRef} message={error} />
      {saved ? <FormStatus ref={savedRef} title="Visibility saved" /> : null}
      <fieldset className="space-y-2">
        <legend className="font-semibold text-ink-900">Who can find this campaign</legend>
        {VISIBILITIES.map((v) => (
          <label key={v} className="flex min-h-11 items-start gap-3 rounded-xl border-2 border-line p-3 has-[:checked]:border-brand-700 has-[:checked]:bg-brand-50">
            <input type="radio" name="visibility" value={v} checked={value === v} onChange={() => setValue(v)} className="mt-1 size-5 shrink-0 accent-brand-700" />
            <span>
              <span className="block font-semibold text-ink-900">{VISIBILITY_LABELS[v]!.label}</span>
              <span className="block text-sm text-ink-700">{VISIBILITY_LABELS[v]!.description}</span>
            </span>
          </label>
        ))}
        <p className="text-sm text-ink-600">Either way, the campaign is only visible to the public while it is published, paused or completed.</p>
      </fieldset>
      <SubmitButton busy={busy} busyLabel="Saving…">
        Save visibility
      </SubmitButton>
    </form>
  );
}
