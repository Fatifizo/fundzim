import { browserApi, type ApiClient } from "@/lib/api/client";

/** Browser-side discovery staff calls through the same-origin /api/v1 proxy. Step-up is handled by the caller (useStepUp). */
const enc = encodeURIComponent;
const A = "/api/v1/admin/discovery";
const T = 20_000;

export function createDiscoveryApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, key?: string) =>
    api.post<T>(path, { body, timeoutMs: T, headers: key ? { "Idempotency-Key": key } : undefined }).then((r) => r.data);
  return {
    requestPlacement: (body: { campaign_id: string; position: number; reason: string; starts_at?: string; ends_at: string }, key: string) =>
      post<unknown>(`${A}/featured`, body, key),
    decidePlacement: (id: string, action: "approve" | "reject" | "cancel" | "remove", note: string) => post<unknown>(`${A}/featured/${enc(id)}/${action}`, { note }),
    proposeRanking: (weights: Record<string, number>, reason: string, key: string) => post<unknown>(`${A}/ranking`, { weights, reason }, key),
    decideRanking: (id: string, action: "approve" | "reject" | "withdraw", note: string) => post<unknown>(`${A}/ranking/${enc(id)}/${action}`, { note }),
    revokeReferral: (code: string, note: string) => post<unknown>(`${A}/referrals/${enc(code)}/revoke`, { note }),
    reconcile: () => post<{ checked: number; reindexed: number; failed: number }>(`${A}/index/reconcile`),
  };
}

export type DiscoveryApi = ReturnType<typeof createDiscoveryApi>;
export const discoveryApi = createDiscoveryApi();
