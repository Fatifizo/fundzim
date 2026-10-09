"use client";

import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from "react";

import { FormError } from "@/components/forms/form-error";
import { SelectField } from "@/components/forms/select-field";
import { SubmitButton } from "@/components/forms/submit-button";
import { TextField } from "@/components/forms/text-field";
import { Button } from "@/components/ui/button";
import { campaignApi, type CampaignApi } from "@/lib/campaigns/api";
import { describeCampaignError } from "@/lib/campaigns/errors";
import { mediaStatusLabel } from "@/lib/campaigns/labels";
import { ownerMediaUrl } from "@/lib/campaigns/paths";
import { PENDING_MEDIA_STATUSES, type CampaignMedia } from "@/lib/campaigns/types";

import { checkImage, IMAGE_ACCEPT_ATTRIBUTE, IMAGE_PROBLEM_MESSAGES, uploadCampaignMedia } from "@/lib/campaigns/upload";
import { formatBytes } from "@/lib/verification/upload";

import { MediaStatusBadge } from "./status";

const POLL_MS = 2_000;

/**
 * Campaign photos: upload (with progress) to the API only, list with processing states (UPLOADED,
 * QUARANTINED, SCANNING, APPROVED, REJECTED, REMOVED), remove. Pending items are polled until settled.
 * Previews are shown only for APPROVED photos, from the API's processed image (never the original file).
 */
export function MediaManager({
  campaignId,
  initial,
  editable,
  onChange,
  api = campaignApi,
}: {
  campaignId: string;
  initial: CampaignMedia[];
  editable: boolean;
  onChange?: (media: CampaignMedia[]) => void;
  api?: CampaignApi;
}) {
  const [media, setMedia] = useState<CampaignMedia[]>(initial);
  const [progress, setProgress] = useState<{ loaded: number; total: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [announcement, setAnnouncement] = useState("");
  const errorRef = useRef<HTMLDivElement>(null);
  const formRef = useRef<HTMLFormElement>(null);
  const onChangeRef = useRef(onChange);
  const progressId = useId();
  useEffect(() => {
    onChangeRef.current = onChange;
  }, [onChange]);

  const apply = useCallback((next: CampaignMedia[]) => {
    setMedia(next);
    onChangeRef.current?.(next);
  }, []);

  const refresh = useCallback(async () => {
    try {
      apply(await api.listMedia(campaignId));
    } catch {
      // Keep the current list; the next poll or a reload will try again.
    }
  }, [api, apply, campaignId]);

  const pending = media.some((m) => PENDING_MEDIA_STATUSES.has(m.status));
  useEffect(() => {
    if (!pending) return;
    const timer = setInterval(refresh, POLL_MS);
    return () => clearInterval(timer);
  }, [pending, refresh]);

  const live = media.filter((m) => m.status !== "REMOVED");
  // The API refuses a second cover (409 COVER_ALREADY_EXISTS): the current one must be removed first.
  const hasCover = live.some((m) => m.kind === "COVER" && m.status !== "REJECTED");

  function fail(message: string, fields: Record<string, string> = {}) {
    setError(message);
    setFieldErrors(fields);
    requestAnimationFrame(() => {
      const invalid = formRef.current?.querySelector<HTMLElement>('[aria-invalid="true"]');
      (invalid ?? errorRef.current)?.focus();
    });
  }

  async function onSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    const file = data.get("file");
    const kind = data.get("kind") === "GALLERY" ? "GALLERY" : "COVER";
    const altText = String(data.get("alt_text") ?? "").trim();
    const depictsMinor = data.get("depicts_minor") === "yes";
    const errors: Record<string, string> = {};
    if (!(file instanceof File) || (file.size === 0 && file.name === "")) errors.file = "Choose a photo to upload.";
    else {
      const problem = checkImage(file);
      if (problem) errors.file = IMAGE_PROBLEM_MESSAGES[problem];
    }
    if (altText.length < 3) errors.alt_text = "Describe the photo in at least 3 characters, for people who cannot see it.";
    else if (altText.length > 250) errors.alt_text = "Keep the description to 250 characters or fewer.";
    if (Object.keys(errors).length > 0) return fail("Please correct the errors below.", errors);
    if (depictsMinor) return fail("Photos that show a child can't be added to a campaign for now. Choose a photo without children.");
    setError(null);
    setFieldErrors({});
    setBusy(true);
    setProgress({ loaded: 0, total: (file as File).size });
    setAnnouncement("Uploading photo…");
    try {
      await uploadCampaignMedia(
        { campaignId, file: file as File, kind, altText, depictsMinor },
        { onProgress: (loaded, total) => setProgress({ loaded, total }) },
      );
      form.reset();
      setAnnouncement("Photo uploaded. It is now being checked.");
      await refresh();
    } catch (err) {
      const described = describeCampaignError(err, "The photo could not be uploaded. Please try again.");
      if (described.code === "TIMEOUT") await refresh();
      fail(described.code === "TIMEOUT" ? "The upload took too long. Check the list below before trying again: it may have arrived." : described.message, described.fieldErrors);
      setAnnouncement("");
    } finally {
      setBusy(false);
      setProgress(null);
    }
  }

  async function remove(item: CampaignMedia) {
    setError(null);
    try {
      await api.deleteMedia(campaignId, item.id);
      setAnnouncement("Photo removed.");
      await refresh();
    } catch (err) {
      fail(describeCampaignError(err, "The photo could not be removed.").message);
    }
  }

  const percent = progress && progress.total > 0 ? Math.min(100, Math.floor((progress.loaded * 100) / progress.total)) : 0;

  return (
    <div className="space-y-6">
      <p role="status" aria-live="polite" className="sr-only">
        {announcement}
      </p>
      <section aria-labelledby="media-list-title" className="space-y-3">
        <h2 id="media-list-title" className="font-display text-xl font-semibold text-ink-900">
          Your photos
        </h2>
        {live.length === 0 ? <p className="text-ink-700">No photos yet. Every campaign needs a cover photo.</p> : null}
        <ul className="grid gap-4 sm:grid-cols-2" aria-label="Campaign photos">
          {media.map((m) => {
            const status = mediaStatusLabel(m.status);
            const src = m.status === "APPROVED" ? ownerMediaUrl(campaignId, m.id) : null;
            return (
              <li key={m.id} className="overflow-hidden rounded-card border border-line bg-surface">
                {src ? (
                  // Processed, API-served image (same origin). next/image is not used: it emits inline styles the strict CSP blocks.
                  // eslint-disable-next-line @next/next/no-img-element
                  <img src={src} alt={m.alt_text} width={640} height={360} className="aspect-video w-full bg-surface-muted object-cover" loading="lazy" />
                ) : (
                  <div aria-hidden="true" className="flex aspect-video w-full items-center justify-center bg-surface-muted text-sm text-ink-600">
                    No preview
                  </div>
                )}
                <div className="space-y-2 p-4">
                  <p className="flex flex-wrap items-center gap-2">
                    <span className="font-semibold text-ink-900">{m.kind === "COVER" ? "Cover photo" : "Gallery photo"}</span>
                    <MediaStatusBadge status={m.status} />
                  </p>
                  <p className="text-sm text-ink-700">{status.description}</p>
                  {m.alt_text ? <p className="text-sm break-words text-ink-600">Description: {m.alt_text}</p> : null}
                  {editable && m.status !== "REMOVED" ? (
                    <Button variant="outline" onClick={() => remove(m)} aria-label={`Remove ${m.kind === "COVER" ? "cover" : "gallery"} photo: ${m.alt_text || m.id}`}>
                      Remove
                    </Button>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ul>
      </section>

      {editable ? (
        <form ref={formRef} noValidate onSubmit={onSubmit} aria-labelledby="upload-title" className="space-y-5 rounded-card border border-line bg-surface p-5">
          <h2 id="upload-title" className="font-display text-xl font-semibold text-ink-900">
            Upload a photo
          </h2>
          <FormError ref={errorRef} message={error} />
          <SelectField
            id="field-kind"
            label="Type of photo"
            name="kind"
            key={hasCover ? "gallery" : "cover"}
            defaultValue={hasCover ? "GALLERY" : "COVER"}
            options={hasCover ? [{ value: "GALLERY", label: "Gallery photo" }] : [
              { value: "COVER", label: "Cover photo (shown at the top)" },
              { value: "GALLERY", label: "Gallery photo" },
            ]}
            hint={hasCover ? "You already have a cover photo. To change it, remove the current cover first, then upload the new one." : "Start with a cover photo: it is required."}
          />
          <TextField id="field-alt_text" label="Describe the photo" name="alt_text" maxLength={250} hint="For people who use screen readers, e.g. “Chipo in her school uniform outside the classroom”." error={fieldErrors.alt_text} />
          <TextField id="field-file" label="Photo" name="file" type="file" accept={IMAGE_ACCEPT_ATTRIBUTE} hint="JPEG or PNG, up to 10 MB. Location and camera data are removed when the photo is processed." error={fieldErrors.file} />
          <div className="space-y-1">
            <label className="flex min-h-11 items-start gap-3 text-ink-900">
              <input type="checkbox" name="depicts_minor" value="yes" aria-describedby="depicts-minor-hint" className="mt-1 size-6 shrink-0 accent-brand-700" />
              <span>This photo shows a child (under 18)</span>
            </label>
            <p id="depicts-minor-hint" className="pl-9 text-sm text-ink-600">
              Tell us honestly. Photos of children can&apos;t be added to campaigns for now, to protect them.
            </p>
          </div>
          {progress ? (
            <div className="space-y-1">
              <label htmlFor={progressId} className="block text-sm font-semibold text-ink-900">
                Upload progress: {percent}% ({formatBytes(progress.loaded)} of {formatBytes(progress.total)})
              </label>
              <progress id={progressId} max={100} value={percent} className="h-3 w-full accent-brand-700" />
            </div>
          ) : null}
          <SubmitButton busy={busy} busyLabel="Uploading…">
            Upload photo
          </SubmitButton>
        </form>
      ) : null}
    </div>
  );
}
