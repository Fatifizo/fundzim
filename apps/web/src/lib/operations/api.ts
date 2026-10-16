import { browserApi, type ApiClient } from "@/lib/api/client";
import { ApiError } from "@/lib/api/errors";
import { readCsrfToken } from "@/lib/auth/cookies";

/**
 * Browser-side operations portal calls through the same-origin /api/v1 proxy (Stage 14). Step-up is handled by the
 * caller (useStepUp). Creating support cases, incidents, access reviews and SLA targets needs an Idempotency-Key.
 * Decisions on other modules' approvals are never sent from here: the approval center links to the owning module.
 */
const enc = encodeURIComponent;
const OPS = "/api/v1/admin/operations";
const WRITE_TIMEOUT_MS = 20_000;

export function createOpsApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, key?: string) =>
    api.post<T>(path, { body, timeoutMs: WRITE_TIMEOUT_MS, headers: key ? { "Idempotency-Key": key } : undefined }).then((r) => r.data);
  return {
    tasks: () => api.get<unknown>(`${OPS}/tasks`, { timeoutMs: 8_000 }).then((r) => r.data),
    openSupportCase: (body: Record<string, string>, key: string) => post<unknown>("/api/v1/admin/support/cases", body, key),
    supportAction: (caseId: string, action: "assign" | "transition" | "escalate" | "notes", body: Record<string, unknown>) =>
      post<unknown>(`/api/v1/admin/support/cases/${enc(caseId)}/${action}`, body),
    triage: (module: string, ref: string, body: Record<string, unknown>) => post<unknown>(`${OPS}/exceptions/${enc(module)}/${enc(ref)}/triage`, body),
    declareIncident: (body: Record<string, string>, key: string) => post<unknown>(`${OPS}/incidents`, body, key),
    updateIncident: (id: string, body: Record<string, unknown>) => post<unknown>(`${OPS}/incidents/${enc(id)}/update`, body),
    proposeSLA: (body: { metric_key: string; target_seconds: number; reason: string }, key: string) => post<unknown>(`${OPS}/sla-targets`, body, key),
    decideSLA: (id: string, approve: boolean, reason: string) => post<unknown>(`${OPS}/sla-targets/${enc(id)}/${approve ? "approve" : "reject"}`, { reason }),
    startAccessReview: (body: Record<string, string>, key: string) => post<unknown>("/api/v1/admin/access-reviews", body, key),
    decideReviewItem: (reviewId: string, itemId: string, decision: string, reason: string) =>
      post<unknown>(`/api/v1/admin/access-reviews/${enc(reviewId)}/items/${enc(itemId)}/decide`, { decision, reason }),
    completeAccessReview: (reviewId: string) => post<unknown>(`/api/v1/admin/access-reviews/${enc(reviewId)}/complete`, {}),
    revokeSessions: (staffId: string, reason: string) => post<unknown>(`/api/v1/admin/staff/${enc(staffId)}/sessions/revoke`, { reason }),
    decideRoleRequest: (id: string, approve: boolean, reason: string) =>
      post<unknown>(`/api/v1/admin/role-assignment-requests/${enc(id)}/${approve ? "approve" : "reject"}`, { reason }),
  };
}

export type OpsApi = ReturnType<typeof createOpsApi>;
export const opsApi = createOpsApi();

/**
 * Audit export (audit.export, step-up). The response is a file, not the JSON envelope, so it is fetched directly from
 * the same-origin proxy; an error response keeps the envelope and is thrown as an ApiError (so useStepUp can retry).
 */
export async function downloadAuditExport(body: Record<string, string>, fetcher: typeof fetch = fetch): Promise<{ blob: Blob; filename: string }> {
  const csrf = typeof document === "undefined" ? undefined : readCsrfToken(document.cookie);
  const res = await fetcher("/api/v1/admin/audit/exports", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(csrf ? { "X-CSRF-Token": csrf } : {}) },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let code = "INTERNAL_ERROR";
    try {
      const env = (await res.json()) as { error?: { code?: string } };
      code = env.error?.code ?? code;
    } catch {
      // not JSON
    }
    throw new ApiError({ code, status: res.status, message: "The export could not be created.", retryable: false });
  }
  const m = /filename="?([A-Za-z0-9._-]+)"?/.exec(res.headers.get("Content-Disposition") ?? "");
  return { blob: await res.blob(), filename: m?.[1] ?? (body.format === "json" ? "audit-export.json" : "audit-export.csv") };
}
