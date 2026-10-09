import type { WizardStep } from "./eligibility";
import { PENDING_MEDIA_STATUSES, type Campaign, type CampaignMedia } from "./types";

/** Readiness the wizard needs: a cover that passed the checks, and nothing still pending. */
export function mediaReadiness(media: CampaignMedia[]) {
  const live = media.filter((m) => m.status !== "REMOVED");
  return {
    coverApproved: live.some((m) => m.kind === "COVER" && m.status === "APPROVED"),
    pending: live.filter((m) => PENDING_MEDIA_STATUSES.has(m.status)).length,
    rejected: live.filter((m) => m.status === "REJECTED").length,
  };
}

/** First step that still needs something, for resuming a saved draft. */
export function resumeStep(c: Campaign, media: CampaignMedia[]): WizardStep {
  if (!c.category) return 1;
  if (!c.title || !c.summary) return 2;
  if (!c.story) return 3;
  if (!c.goal) return 4;
  if (!c.beneficiary) return 5;
  if (!mediaReadiness(media).coverApproved) return 6;
  return 7;
}

export function parseStep(raw: string | undefined): WizardStep | null {
  return raw && /^[1-8]$/.test(raw) ? (Number(raw) as WizardStep) : null;
}
