import { browserApi, type ApiClient } from "@/lib/api/client";

/**
 * Browser-side fee calls through the same-origin /api/v1 proxy. Step-up is handled by the caller (useStepUp).
 * The client never sends a rate or a fee amount for a quote: the API selects the policy and calculates.
 */
const enc = encodeURIComponent;
const ADMIN = "/api/v1/admin/fees";
const WRITE_TIMEOUT_MS = 20_000;

export type VersionAction = "submit" | "approve" | "reject" | "activate" | "suspend" | "reactivate" | "retire";

export interface NewPolicyBody {
  code: string;
  name: string;
  description: string;
  category: string;
  currency: string;
  payment_method: string | null;
  campaign_id: string | null;
}

export interface VersionBody {
  model: string;
  rate_bps: number | null;
  fixed_minor: string | null;
  min_fee_minor: string | null;
  max_fee_minor: string | null;
  rounding_mode: string;
  responsibility: string;
  donor_share_bps: number | null;
  refund_treatment: string | null;
  effective_from: string;
}

export interface ScheduleBody {
  provider_id: string;
  contract_reference: string;
  schedule_version: string;
  currency: string;
  payment_method: string | null;
  fee_type: string;
  model: string;
  rate_bps: number | null;
  fixed_minor: string | null;
  rounding_mode: string | null;
  effective_from: string;
  evidence_source: string;
  evidence_reference: string | null;
  confirmation_status: string;
}

export interface AdjustmentBody {
  reason: string;
  direction: string;
  amount_minor: string;
  note: string;
}

export function createFeesApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, headers?: Record<string, string>) =>
    api.post<T>(path, { body, timeoutMs: WRITE_TIMEOUT_MS, headers }).then((r) => r.data);
  const idem = (key: string) => ({ "Idempotency-Key": key });
  return {
    /** Donor: a fee quote for a TEST donation (not a charge). */
    createQuote: (body: { campaign_id: string; amount_minor: string; currency: string; payment_method: string | null }, key: string) =>
      post<unknown>("/api/v1/fees/quotes", body, idem(key)),
    createPolicy: (body: NewPolicyBody, key: string) => post<unknown>(`${ADMIN}/policies`, body, idem(key)),
    createVersion: (policyId: string, body: VersionBody, key: string) => post<unknown>(`${ADMIN}/policies/${enc(policyId)}/versions`, body, idem(key)),
    versionAction: (versionId: string, action: VersionAction, reason: string, termsHash?: string | null) => {
      const path = `${ADMIN}/policy-versions/${enc(versionId)}/${action}`;
      if (action === "submit" || action === "activate") return post<unknown>(path, {});
      if (action === "approve" || action === "reject") return post<unknown>(path, termsHash ? { reason, terms_hash: termsHash } : { reason });
      return post<unknown>(path, { reason });
    },
    createSchedule: (body: ScheduleBody, key: string) => post<unknown>(`${ADMIN}/provider-schedules`, body, idem(key)),
    scheduleAction: (id: string, action: "approve" | "retire", reason: string) =>
      post<unknown>(`${ADMIN}/provider-schedules/${enc(id)}/${action}`, action === "retire" ? { reason } : {}),
    requestAdjustment: (assessmentId: string, body: AdjustmentBody, key: string) =>
      post<unknown>(`${ADMIN}/assessments/${enc(assessmentId)}/adjustments`, body, idem(key)),
    adjustmentAction: (id: string, action: "approve" | "reject", reason: string) => post<unknown>(`${ADMIN}/adjustments/${enc(id)}/${action}`, { reason }),
    runReconciliation: () => post<unknown>(`${ADMIN}/reconciliation/run`, {}),
    resolveRecon: (id: string, note: string) => post<unknown>(`${ADMIN}/reconciliation/${enc(id)}/resolve`, { note }),
  };
}

export type FeesApi = ReturnType<typeof createFeesApi>;
export const feesApi: FeesApi = createFeesApi();
