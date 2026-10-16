import { isApiError } from "@/lib/api/errors";
import { describePaymentError } from "@/lib/payments/errors";

/** Risk operations error copy (codes only; never server text). Falls back to the payments/campaign/auth mapping. */
const MESSAGES: Record<string, string> = {
  RISK_NOT_FOUND: "This risk record was not found. Reload the page.",
  RISK_RULE_STATE: "This isn't possible in the rule version's current status. Reload the page to see the latest state.",
  RISK_RULE_CONFLICT: "This rule already has a draft or pending version, or the code is taken.",
  RISK_RULE_INVALID: "The rule definition is not valid: only catalogued signals, allowed facts and bounded windows can be used.",
  RISK_RULE_CHANGED: "The rule you reviewed is not this version's rule. Reload and review again.",
  RISK_CASE_STATE: "This isn't possible in the case's current status. Reload the page.",
  RISK_CASE_NOT_ESCALATED: "Escalate the case to compliance before resolving it as escalated.",
  RISK_CASE_DUPLICATE_OPEN: "Another open case already covers this subject and case type. Work that case instead.",
  RISK_SIGNAL_ALREADY_REVIEWED: "This signal was already reviewed in this case.",
  RISK_HOLD_STATE: "This isn't possible in the hold's current status. Reload the page.",
  RISK_HOLD_EXISTS: "An open hold of this type already exists for this subject.",
  RISK_SCREENING_STATE: "This isn't possible in the screening request's current status. Reload the page.",
  RISK_RECORD_CHANGED: "The record changed since you loaded it. Reload and try again.",
  RISK_CONFLICT_OF_INTEREST: "You can't work on this: it concerns you or an account linked to yours.",
  SCREENING_UNAVAILABLE: "Screening is not available: no screening provider is selected (TEST simulator only).",
  SELF_APPROVAL_FORBIDDEN: "You can't decide this: you made it, or an account linked to yours did.",
  IDEMPOTENCY_KEY_REUSED: "This request was already sent with different details. Reload and try again.",
  RATE_LIMITED: "Too many requests. Try again later.",
};

export function describeRiskError(error: unknown, fallback = "The action could not be completed."): { message: string } {
  if (isApiError(error) && MESSAGES[error.code]) return { message: MESSAGES[error.code]! };
  return { message: describePaymentError(error, fallback).message };
}
