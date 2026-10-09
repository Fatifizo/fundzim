import { browserApi, type ApiClient } from "@/lib/api/client";

import {
  readAgeAttestation,
  readCampaign,
  readEligibility,
  readMedia,
  readStaffDetail,
  readStaffLink,
  readUpdates,
} from "./normalise";
import type { CampaignPatch, CreateCampaignInput, Disclosure, EligibilityAction, StaffAction } from "./types";

/**
 * Browser-side calls for the Stage 6 campaign API (contract §6) through the same-origin /api/v1 proxy. The
 * client adds X-CSRF-Token on unsafe methods and never retries them automatically. Edits send If-Match with
 * the campaign version (contract: ETag = version).
 */
const WRITE_TIMEOUT_MS = 20_000;
const enc = encodeURIComponent;

export function ifMatch(version: number): Record<string, string> {
  return { "If-Match": `"${version}"` };
}

export function createCampaignApi(api: ApiClient = browserApi) {
  const get = <T>(path: string) => api.get<T>(path, { retries: 1 }).then((r) => r.data);
  const post = <T>(path: string, body: unknown = {}, headers?: Record<string, string>) =>
    api.post<T>(path, { body, timeoutMs: WRITE_TIMEOUT_MS, headers }).then((r) => r.data);
  const c = (id: string) => `/api/v1/campaigns/${enc(id)}`;

  return {
    // Owner
    create: (input: CreateCampaignInput, idempotencyKey: string) => post<unknown>("/api/v1/campaigns", input, { "Idempotency-Key": idempotencyKey }).then(readCampaign),
    get: (id: string) => get<unknown>(c(id)).then(readCampaign),
    update: (id: string, patch: CampaignPatch, version: number) =>
      api.patch<unknown>(c(id), { body: patch, timeoutMs: WRITE_TIMEOUT_MS, headers: ifMatch(version) }).then((r) => readCampaign(r.data)),
    eligibility: (id: string, action: EligibilityAction) =>
      get<unknown>(`${c(id)}/eligibility?action=${enc(action)}`).then((d) => readEligibility(d)[0] ?? { action, allowed: false, reasons: [] }),
    lifecycle: (id: string, action: "submit" | "withdraw" | "cancel" | "publish" | "pause" | "resume" | "complete" | "archive" | "revise", body: Record<string, unknown> = {}) =>
      post<unknown>(`${c(id)}/${action}`, body).then(readCampaign),
    linkBeneficiary: (id: string, body: { beneficiary_id: string; disclosure: Disclosure; consent_declared: boolean; reason?: string }) =>
      post<unknown>(`${c(id)}/beneficiaries`, body),
    unlinkBeneficiary: (id: string, beneficiaryId: string, reason: string) =>
      api.delete<unknown>(`${c(id)}/beneficiaries/${enc(beneficiaryId)}`, { body: { reason }, timeoutMs: WRITE_TIMEOUT_MS }).then(() => undefined),

    // Media (upload is ./upload.ts: multipart with progress)
    listMedia: (id: string) => api.get<unknown>(`${c(id)}/media`, { retries: 0 }).then((r) => readMedia(r.data)),
    updateMedia: (id: string, mediaId: string, body: { alt_text?: string; position?: number }) =>
      api.patch<unknown>(`${c(id)}/media/${enc(mediaId)}`, { body, timeoutMs: WRITE_TIMEOUT_MS }).then((r) => r.data),
    deleteMedia: (id: string, mediaId: string) => api.delete<unknown>(`${c(id)}/media/${enc(mediaId)}`, { timeoutMs: WRITE_TIMEOUT_MS }).then(() => undefined),

    // Updates
    listUpdates: (id: string) => get<unknown>(`${c(id)}/updates`).then(readUpdates),
    /** `publish: true` publishes at once (or queues for moderation where policy pre-moderates); false saves a draft. */
    createUpdate: (id: string, body: { title: string; body: string; publish: boolean }) => post<unknown>(`${c(id)}/updates`, body),
    editUpdate: (id: string, updateId: string, body: { title: string; body: string }) =>
      api.patch<unknown>(`${c(id)}/updates/${enc(updateId)}`, { body, timeoutMs: WRITE_TIMEOUT_MS }).then((r) => r.data),
    deleteUpdate: (id: string, updateId: string) => api.delete<unknown>(`${c(id)}/updates/${enc(updateId)}`, { timeoutMs: WRITE_TIMEOUT_MS }).then(() => undefined),

    // Age attestation (ADR-037 §1)
    ageAttestation: () => get<unknown>("/api/v1/me/age-attestation").then(readAgeAttestation),
    attestAge: (outcome: "ATTESTED" | "DECLINED", statement_version: string) =>
      post<unknown>("/api/v1/me/age-attestation", { outcome, statement_version }).then(readAgeAttestation),

    // Staff ↔ personal account links (ADR-037 §4)
    staffLinkStatus: () => get<unknown>("/api/v1/admin/me/personal-account-link").then(readStaffLink),
    /** 202 `{status: "confirmation_sent_if_eligible", expires_at}` whether or not the email belongs to an eligible account. */
    requestStaffLink: (email: string) => post<{ status?: string; expires_at?: string }>("/api/v1/admin/me/personal-account-link", { email }),
    confirmStaffLink: (token: string) => post<unknown>("/api/v1/me/staff-link/confirm", { token }),

    // Staff review
    reviewDetail: (id: string) => get<unknown>(`/api/v1/admin/campaigns/${enc(id)}/review`).then(readStaffDetail),
    staffAction: (id: string, action: StaffAction, body: Record<string, unknown>) =>
      api.post<unknown>(`/api/v1/admin/campaigns/${enc(id)}/${action}`, { body, timeoutMs: WRITE_TIMEOUT_MS }),
    staffMedia: (id: string) => get<unknown>(`/api/v1/admin/campaigns/${enc(id)}/media/all`).then(readMedia),
    removeMedia: (id: string, mediaId: string, body: { reason_code: string; note: string }) =>
      post<unknown>(`/api/v1/admin/campaigns/${enc(id)}/media/${enc(mediaId)}/remove`, body),
    moderateUpdate: (updateId: string, action: "approve" | "hide", body: { reason_code: string; note: string }) =>
      post<unknown>(`/api/v1/admin/campaigns/updates/${enc(updateId)}/${action}`, body),
  };
}

export type CampaignApi = ReturnType<typeof createCampaignApi>;

export const campaignApi: CampaignApi = createCampaignApi();
