"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState, type FormEvent } from "react";

import { formatDateTime } from "@/components/account/format";
import { StepUpCancelledError, useStepUp } from "@/components/auth/step-up";
import { CampaignStatusBadge } from "@/components/campaigns/status";
import { FormError, FormStatus } from "@/components/forms/form-error";
import { TextAreaField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { isUuid } from "@/lib/campaigns/paths";
import type { StaffFeaturedPick } from "@/lib/campaigns/types";

const len = (s: string) => [...s].length;

/**
 * Staff picks for the home page (`campaign.feature`; ADR-038 §2). Picks come first in the public featured list,
 * ordered by position; recently published campaigns fill the rest. A pick whose campaign is no longer live and
 * listed stays here marked "Hidden from the public" — the API never shows it.
 */
export function FeaturedManager({ items, initialCampaignId = "", api = campaignApi }: { items: StaffFeaturedPick[]; initialCampaignId?: string; api?: CampaignApi }) {
  const router = useRouter();
  const withStepUp = useStepUp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const [removing, setRemoving] = useState<StaffFeaturedPick | null>(null);
  const [removeError, setRemoveError] = useState<string | null>(null);
  const noticeRef = useRef<HTMLDivElement>(null);
  const errorRef = useRef<HTMLDivElement>(null);
  const removeErrorRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (notice) noticeRef.current?.focus();
  }, [notice]);

  async function onAdd(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const campaignId = String(data.get("campaign_id") ?? "").trim();
    const positionRaw = String(data.get("position") ?? "").trim();
    const note = String(data.get("note") ?? "").trim();
    const errs: Record<string, string> = {};
    if (!isUuid(campaignId)) errs.campaign_id = "Enter the campaign's id (shown in the review page address).";
    const position = /^\d{1,3}$/.test(positionRaw) ? Number.parseInt(positionRaw, 10) : NaN;
    if (!(position >= 1 && position <= 100)) errs.position = "Choose a position from 1 to 100.";
    if (len(note) < 3 || len(note) > 500) errs.note = "Write a note of 3 to 500 characters explaining why.";
    setFieldErrors(errs);
    setNotice(null);
    if (Object.keys(errs).length > 0) {
      setError("Please correct the errors below.");
      requestAnimationFrame(() => errorRef.current?.focus());
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await withStepUp(() => api.feature(campaignId, { position, note }));
      form.reset();
      setNotice("Campaign featured.");
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        const d = describeCampaignError(err, "The campaign could not be featured.");
        setFieldErrors(d.fieldErrors);
        setError(d.message);
        requestAnimationFrame(() => errorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  async function onRemove(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!removing) return;
    const reason = String(new FormData(event.currentTarget).get("reason") ?? "").trim();
    if (len(reason) < 3 || len(reason) > 500) {
      setRemoveError("Write a reason of 3 to 500 characters.");
      requestAnimationFrame(() => removeErrorRef.current?.focus());
      return;
    }
    setBusy(true);
    setRemoveError(null);
    try {
      await withStepUp(() => api.unfeature(removing.campaign_id, reason));
      setRemoving(null);
      setNotice("Campaign removed from the featured list.");
      router.refresh();
    } catch (err) {
      if (!(err instanceof StepUpCancelledError)) {
        setRemoveError(describeCampaignError(err, "The pick could not be removed.").message);
        requestAnimationFrame(() => removeErrorRef.current?.focus());
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-8">
      {notice ? <FormStatus ref={noticeRef} title={notice} /> : null}
      <section aria-labelledby="picks-title" className="space-y-4">
        <h2 id="picks-title" className="font-display text-2xl font-semibold text-ink-900">
          Current staff picks
        </h2>
        {items.length === 0 ? (
          <EmptyState title="No staff picks" description="The home page shows the most recently published campaigns." />
        ) : (
          <ol className="space-y-3" aria-label="Staff picks">
            {items.map((f) => (
              <li key={f.campaign_id} className="space-y-2 rounded-card border border-line bg-surface p-5">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge tone="neutral">Position {f.position}</Badge>
                  <CampaignStatusBadge status={f.status} audience="staff" />
                  {f.shown ? <Badge tone="brand">Shown on the home page</Badge> : <Badge tone="gold">Hidden from the public</Badge>}
                </div>
                <h3 className="font-semibold break-words text-ink-900">
                  <Link href={`/admin/campaigns/review/${encodeURIComponent(f.campaign_id)}`} className="text-brand-700 underline">
                    {f.title || "Untitled campaign"}
                  </Link>
                </h3>
                {!f.shown ? <p className="text-sm text-ink-700">This campaign is not live and listed, so it is not shown publicly. Remove the pick or wait until it is live again.</p> : null}
                <p className="text-sm break-words text-ink-700">Note: {f.note}</p>
                <p className="text-sm text-ink-600">Added {formatDateTime(f.added_at)}</p>
                <Button variant="outline" onClick={() => { setNotice(null); setRemoveError(null); setRemoving(f); }} aria-label={`Remove ${f.title} from the featured list`}>
                  Remove
                </Button>
              </li>
            ))}
          </ol>
        )}
      </section>

      <section aria-labelledby="add-title" className="space-y-4 rounded-card border border-line bg-surface p-5">
        <h2 id="add-title" className="font-display text-2xl font-semibold text-ink-900">
          Add a staff pick
        </h2>
        <p className="text-ink-700">Only live, listed campaigns can be featured. Featuring is not an endorsement; choose campaigns that show the range of causes on FundZim.</p>
        <form noValidate onSubmit={onAdd} className="space-y-4">
          <FormError ref={errorRef} message={error} />
          <TextField label="Campaign id" name="campaign_id" defaultValue={initialCampaignId} autoComplete="off" spellCheck={false} error={fieldErrors.campaign_id} />
          <TextField label="Position (1–100)" name="position" inputMode="numeric" defaultValue={String(Math.min(100, (items.at(-1)?.position ?? 0) + 1))} error={fieldErrors.position} />
          <TextAreaField label="Why this campaign (staff only)" name="note" required maxLength={500} error={fieldErrors.note} />
          <SubmitButton busy={busy} busyLabel="Working…">
            Feature campaign
          </SubmitButton>
        </form>
      </section>

      {removing ? (
        <Dialog title="Remove this staff pick?" description={`"${removing.title}" will no longer be featured. The campaign itself is not changed.`} onClose={() => setRemoving(null)}>
          <form noValidate onSubmit={onRemove} className="space-y-5">
            <FormError ref={removeErrorRef} message={removeError} />
            <TextAreaField label="Reason (staff only)" name="reason" required maxLength={500} />
            <div className="flex flex-wrap gap-3">
              <SubmitButton busy={busy} busyLabel="Working…">
                Confirm: Remove
              </SubmitButton>
              <Button variant="outline" onClick={() => setRemoving(null)}>
                Cancel
              </Button>
            </div>
          </form>
        </Dialog>
      ) : null}
    </div>
  );
}
