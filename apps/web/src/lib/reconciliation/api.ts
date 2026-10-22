import { browserApi, type ApiClient } from "@/lib/api/client";

/**
 * Browser-side reconciliation staff calls through the same-origin /api/v1 proxy (Stage 17). Step-up is handled by the
 * caller (useStepUp); creations carry one Idempotency-Key per submission so a retry after step-up reuses it.
 */
const enc = encodeURIComponent;
const A = "/api/v1/admin/reconciliation";
const T = 30_000;

export interface ProposalBody {
  resolution_type: string;
  justification: string;
  evidence_refs: string[];
  version?: number;
  subject_type?: string;
  subject_id?: string;
  case_ref?: string;
  posting_rule?: string;
  lines?: Array<{ account_code: string; direction: string; amount_minor: string }>;
}

export function createReconciliationApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, key?: string) =>
    api.post<T>(path, { body, timeoutMs: T, headers: key ? { "Idempotency-Key": key } : undefined }).then((r) => r.data);
  return {
    sandboxStatement: (body: { source: string; currency: string; period_start: string; period_end: string }, key: string) =>
      post<{ id: string }>(`${A}/imports/sandbox`, body, key),
    requestRun: (body: { source?: string; currency?: string; reason: string; replay_of_run_id?: string }, key: string) => post<{ id: string }>(`${A}/runs`, body, key),
    act: (id: string, action: "assign" | "start" | "await-evidence" | "resume" | "comment" | "close" | "reopen", body: Record<string, unknown>) =>
      post<unknown>(`${A}/exceptions/${enc(id)}/${action}`, body),
    propose: (id: string, body: ProposalBody, key: string) => post<unknown>(`${A}/exceptions/${enc(id)}/resolutions`, body, key),
    decide: (id: string, action: "approve" | "reject" | "cancel", body: { note: string; proposal_sha256?: string }) =>
      post<unknown>(`${A}/resolutions/${enc(id)}/${action}`, body),
    proposeRules: (body: { config: Record<string, number>; change_reason: string }, key: string) => post<unknown>(`${A}/rule-sets`, body, key),
    decideRules: (id: string, action: "approve" | "reject" | "retire" | "withdraw", body: { note: string; config_sha256?: string }) =>
      post<unknown>(`${A}/rule-sets/${enc(id)}/${action}`, body),
  };
}

export type ReconciliationApi = ReturnType<typeof createReconciliationApi>;
export const reconciliationApi = createReconciliationApi();

/** The export link (a same-origin GET; the API answers with a CSV attachment after step-up). */
export const exportHref = (kind: string) => `${A}/reports/export?kind=${enc(kind)}`;
