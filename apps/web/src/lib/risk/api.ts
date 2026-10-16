import { browserApi, type ApiClient } from "@/lib/api/client";

/**
 * Browser-side risk operations calls through the same-origin /api/v1 proxy. Step-up is handled by the caller
 * (useStepUp). Creating rules, rule versions, cases, evidence, holds and screening requests needs an Idempotency-Key.
 */
const enc = encodeURIComponent;
const ADMIN = "/api/v1/admin/risk";
const WRITE_TIMEOUT_MS = 20_000;

export type RuleAction = "submit" | "approve" | "reject" | "activate" | "suspend" | "reactivate" | "retire";
export type CaseAction = "assign" | "transition" | "signal-reviews" | "escalate" | "propose-decision" | "approve-decision" | "return-decision" | "reopen";
export type HoldAction = "activate" | "revoke" | "review" | "request-release" | "approve-release" | "reject-release";

export interface RuleVersionBody {
  definition: unknown;
  contribution: number;
  severity: string;
  recommended_action: string;
  hold_type: string | null;
  hold_duration_seconds: number | null;
  case_priority: string;
}

export interface NewRuleBody {
  code: string;
  name: string;
  description: string;
  subject_type: string;
  category: string;
  version: RuleVersionBody;
}

export interface NewCaseBody {
  case_type: string;
  subject_type: string;
  subject_id: string;
  priority: string;
  reason: string;
}

export interface EvidenceBody {
  evidence_type: string;
  note: string;
  reference_type?: string;
  reference_id?: string;
  supersedes_id?: string;
}

export interface NewHoldBody {
  hold_type: string;
  subject_type: string;
  subject_id: string;
  reason_code: string;
  description: string;
  case_id?: string;
}

export interface NewScreeningBody {
  subject_type: string;
  subject_id: string;
  scenario: string;
}

export function createRiskApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body: unknown = {}, headers?: Record<string, string>) =>
    api.post<T>(path, { body, timeoutMs: WRITE_TIMEOUT_MS, headers }).then((r) => r.data);
  const idem = (key: string) => ({ "Idempotency-Key": key });
  return {
    createRule: (body: NewRuleBody, key: string) => post<unknown>(`${ADMIN}/rules`, body, idem(key)),
    createRuleVersion: (ruleId: string, body: RuleVersionBody, key: string) => post<unknown>(`${ADMIN}/rules/${enc(ruleId)}/versions`, body, idem(key)),
    updateDraft: (versionId: string, body: RuleVersionBody) =>
      api.put<unknown>(`${ADMIN}/rule-versions/${enc(versionId)}`, { body, timeoutMs: WRITE_TIMEOUT_MS }).then((r) => r.data),
    ruleAction: (versionId: string, action: RuleAction, reason: string, definitionHash?: string | null) => {
      const path = `${ADMIN}/rule-versions/${enc(versionId)}/${action}`;
      if (action === "submit" || action === "activate") return post<unknown>(path, {});
      if (action === "approve" && definitionHash) return post<unknown>(path, { reason, definition_hash: definitionHash });
      return post<unknown>(path, { reason });
    },
    simulate: (versionId: string, subjectId: string) => post<unknown>(`${ADMIN}/rule-versions/${enc(versionId)}/simulate`, { subject_id: subjectId }),
    openCase: (body: NewCaseBody, key: string) => post<unknown>(`${ADMIN}/cases`, body, idem(key)),
    addEvidence: (caseId: string, body: EvidenceBody, key: string) => post<unknown>(`${ADMIN}/cases/${enc(caseId)}/evidence`, body, idem(key)),
    caseAction: (caseId: string, action: CaseAction, body: Record<string, string>) => post<unknown>(`${ADMIN}/cases/${enc(caseId)}/${action}`, body),
    createHold: (body: NewHoldBody, key: string) => post<unknown>(`${ADMIN}/holds`, body, idem(key)),
    holdAction: (holdId: string, action: HoldAction, reason: string) => post<unknown>(`${ADMIN}/holds/${enc(holdId)}/${action}`, reason ? { reason } : {}),
    requestScreening: (body: NewScreeningBody, key: string) => post<unknown>(`${ADMIN}/screening`, body, idem(key)),
    reviewScreening: (id: string, decision: string, reason: string) => post<unknown>(`${ADMIN}/screening/${enc(id)}/review`, { decision, reason }),
    retryScreening: (id: string) => post<unknown>(`${ADMIN}/screening/${enc(id)}/retry`, {}),
    runReconciliation: () => post<unknown>(`${ADMIN}/reconciliation/run`, {}),
  };
}

export type RiskApi = ReturnType<typeof createRiskApi>;
export const riskApi: RiskApi = createRiskApi();
