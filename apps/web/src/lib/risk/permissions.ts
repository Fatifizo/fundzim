import type { RiskActionTarget } from "@/components/admin/risk/risk-action";

/**
 * The permission each risk action's API route requires (internal/risk/http.go), so a risk page shows only the actions
 * the caller's roles include (KI-S13-21). Presentation only: the API enforces every route.
 */
export const RISK_ACTION_PERMISSION: Record<RiskActionTarget["kind"], string> = {
  "rule-submit": "risk.rule.request",
  "rule-approve": "risk.rule.approve",
  "rule-reject": "risk.rule.approve",
  "rule-activate": "risk.rule.request",
  "rule-suspend": "risk.rule.request",
  "rule-reactivate": "risk.rule.approve",
  "rule-retire": "risk.rule.request",
  "case-assign": "risk.case.work",
  "case-transition": "risk.case.work",
  "case-escalate": "risk.case.work",
  "case-propose": "risk.case.work",
  "case-approve": "risk.case.approve",
  "case-return": "risk.case.approve",
  "case-reopen": "risk.case.work",
  "case-review-signal": "risk.case.work",
  "hold-activate": "risk.hold.release",
  "hold-revoke": "risk.hold.create",
  "hold-review": "risk.hold.create",
  "hold-request-release": "risk.hold.create",
  "hold-approve-release": "risk.hold.release",
  "hold-reject-release": "risk.hold.release",
  "screening-confirm": "risk.screening.review",
  "screening-clear": "risk.screening.review",
  "screening-retry": "risk.screening.request",
};

/** Forms on the risk pages and the permission their route requires. */
export const RISK_FORM_PERMISSION = {
  createRule: "risk.rule.request",
  ruleVersion: "risk.rule.request",
  simulate: "risk.rule.request",
  openCase: "risk.case.work",
  evidence: "risk.case.work",
  createHold: "risk.hold.create",
  screening: "risk.screening.request",
  reconciliation: "risk.case.work",
} as const;

export function mayRisk(permissions: readonly string[] | undefined, kind: RiskActionTarget["kind"]): boolean {
  return (permissions ?? []).includes(RISK_ACTION_PERMISSION[kind]);
}
