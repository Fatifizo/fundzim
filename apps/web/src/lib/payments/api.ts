import { browserApi, type ApiClient } from "@/lib/api/client";

import { readRefund } from "./normalise";
import type { ProviderAction, RefundAction } from "./types";

/**
 * Browser-side calls for the Stage 8 staff payment actions (interface-contracts §4) through the same-origin
 * /api/v1 proxy. The client adds X-CSRF-Token and never retries a mutation automatically. Step-up is handled by
 * the caller (useStepUp). There is deliberately NO call that sets a payment's status: a status query only asks
 * the provider, and the API applies whatever the provider authoritatively reports.
 */
const WRITE_TIMEOUT_MS = 20_000;
const enc = encodeURIComponent;
const BASE = "/api/v1/admin/payments";

export interface RefundRequestInput {
  amount_minor: string;
  reason_code: string;
  reason_text: string;
}

export interface SandboxWebhookInput {
  status: string;
  delay_seconds?: number;
  copies?: number;
  sign_with?: string;
  timestamp_offset_seconds?: number;
}

export function createPaymentsApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, headers?: Record<string, string>) =>
    api.post<T>(path, { body, timeoutMs: WRITE_TIMEOUT_MS, headers }).then((r) => r.data);
  return {
    /** Schedules an authenticated provider status query now (payment.operate, step-up). */
    requestStatusQuery: (paymentId: string, reason: string) => post<unknown>(`${BASE}/attempts/${enc(paymentId)}/status-query`, { reason }),
    /** Only DEAD_LETTERED or UNRECOGNISED inbox items (payment.operate, step-up). */
    requeueWebhook: (inboxId: string, reason: string) => post<unknown>(`${BASE}/webhooks/${enc(inboxId)}/requeue`, { reason }),
    /** OPEN → AWAITING_SETTLEMENT_DATA (payment.operate, step-up). */
    awaitSettlement: (referenceId: string, note: string) => post<unknown>(`${BASE}/reconciliation/${enc(referenceId)}/await-settlement`, { note }),
    /** refund.request; Idempotency-Key required. */
    createRefundRequest: (paymentId: string, input: RefundRequestInput, idempotencyKey: string) =>
      post<unknown>(`${BASE}/attempts/${enc(paymentId)}/refund-requests`, input, { "Idempotency-Key": idempotencyKey }).then(readRefund),
    /** approve/reject/hold/resume: refund.approve + step-up; withdraw: the requester. Approval never executes a refund in Stage 8. */
    refundAction: (refundId: string, action: RefundAction, reason: string) => post<unknown>(`${BASE}/refund-requests/${enc(refundId)}/${action}`, { reason }).then(readRefund),
    /**
     * Stage 9 provider controls (payment.operate, step-up): enable SANDBOX execution, disable, declare or resolve an
     * outage. None of them can enable production execution (migration + ADR only) or change a payment.
     */
    providerAction: (providerId: string, action: ProviderAction, reason: string) => post<unknown>(`${BASE}/providers/${enc(providerId)}/${action}`, { reason }),
    /** DEVELOPMENT ONLY: asks the sandbox simulator to send signed webhooks through the real endpoint. */
    sandboxWebhook: (paymentId: string, input: SandboxWebhookInput) => post<unknown>(`${BASE}/sandbox/attempts/${enc(paymentId)}/webhooks`, input),
  };
}

export type PaymentsApi = ReturnType<typeof createPaymentsApi>;

export const paymentsApi: PaymentsApi = createPaymentsApi();
