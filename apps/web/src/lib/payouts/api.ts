import { browserApi, type ApiClient } from "@/lib/api/client";

/**
 * Browser-side payout calls through the same-origin /api/v1 proxy. Step-up is handled by the caller (useStepUp).
 * There is no call that marks a payout paid or failed by hand: provider evidence drives those states.
 */
const enc = encodeURIComponent;
const ADMIN = "/api/v1/admin/payout-requests";
const OPS = "/api/v1/admin/payouts";
const WRITE_TIMEOUT_MS = 20_000;

export type StaffPayoutAction = "approve" | "reject" | "cancel" | "eligibility-checks" | "status-checks";
export type ExceptionAction = "investigate" | "resolve";

export function createPayoutsApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, headers?: Record<string, string>) =>
    api.post<T>(path, { body, timeoutMs: WRITE_TIMEOUT_MS, headers }).then((r) => r.data);
  return {
    /** Owner: request a withdrawal (Idempotency-Key per attempt). */
    requestPayout: (campaignId: string, body: { currency: string; amount_minor: string; destination_id: string; purpose?: string | null }, idempotencyKey: string) =>
      post<unknown>(`/api/v1/campaigns/${enc(campaignId)}/payout-requests`, body, { "Idempotency-Key": idempotencyKey }),
    cancelOwn: (id: string) => post<unknown>(`/api/v1/payout-requests/${enc(id)}/cancel`, {}),
    staffAction: (id: string, action: StaffPayoutAction, reason: string) => {
      if (action === "approve" || action === "reject") return post<unknown>(`${ADMIN}/${enc(id)}/${action}`, { reason, conflict_attested: true });
      if (action === "cancel") return post<unknown>(`${ADMIN}/${enc(id)}/cancel`, { reason });
      return post<unknown>(`${ADMIN}/${enc(id)}/${action}`, {});
    },
    exceptionAction: (id: string, action: ExceptionAction, note: string, resolutionCode?: string) =>
      post<unknown>(`${OPS}/exceptions/${enc(id)}/${action}`, action === "resolve" ? { note, resolution_code: resolutionCode ?? "CONFIRMED_NO_ACTION" } : { note }),
    runReconciliation: () => post<unknown>(`${OPS}/reconciliation/run`, {}),
  };
}

export type PayoutsApi = ReturnType<typeof createPayoutsApi>;
export const payoutsApi: PayoutsApi = createPayoutsApi();
