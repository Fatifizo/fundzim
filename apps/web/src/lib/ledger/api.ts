import { browserApi, type ApiClient } from "@/lib/api/client";

/**
 * Browser-side ledger staff actions through the same-origin /api/v1 proxy. Every action takes a reason; step-up is
 * handled by the caller (useStepUp). There is deliberately no call that posts a free-form journal or edits one:
 * corrections are maker-checker adjustments (reversals), approved by a different person.
 */
const enc = encodeURIComponent;
const BASE = "/api/v1/admin/ledger";
const WRITE_TIMEOUT_MS = 20_000;

export type AccountAction = "freeze" | "unfreeze" | "close";
export type PeriodAction = "request-close" | "approve-close" | "abort-close";
export type AdjustmentAction = "approve" | "reject" | "withdraw";
export type ReconAction = "investigate" | "resolve";

export function createLedgerApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, headers?: Record<string, string>) =>
    api.post<T>(path, { body, timeoutMs: WRITE_TIMEOUT_MS, headers }).then((r) => r.data);
  return {
    accountAction: (id: string, action: AccountAction, reason: string) => post<unknown>(`${BASE}/accounts/${enc(id)}/${action}`, { reason }),
    periodAction: (id: string, action: PeriodAction, reason: string) => post<unknown>(`${BASE}/periods/${enc(id)}/${action}`, { reason }),
    /** Maker: request the exact mirror of a journal (Idempotency-Key required). */
    requestReversal: (transactionId: string, reason: string, idempotencyKey: string) =>
      post<unknown>(`${BASE}/adjustments`, { kind: "REVERSAL", target_transaction_id: transactionId, reason_code: "POSTING_ERROR", reason },
        { "Idempotency-Key": idempotencyKey }),
    adjustmentAction: (id: string, action: AdjustmentAction, reason: string) => post<unknown>(`${BASE}/adjustments/${enc(id)}/${action}`, { reason }),
    reconAction: (id: string, action: ReconAction, reason: string) => post<unknown>(`${BASE}/reconciliation/${enc(id)}/${action}`, { reason }),
    runReconciliation: () => post<unknown>(`${BASE}/reconciliation/run`, {}),
    runIntegrity: () => post<unknown>(`${BASE}/integrity/run`, {}),
  };
}

export type LedgerApi = ReturnType<typeof createLedgerApi>;
export const ledgerApi: LedgerApi = createLedgerApi();
