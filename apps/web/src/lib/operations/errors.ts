import { isApiError } from "@/lib/api/errors";
import { describePaymentError } from "@/lib/payments/errors";

/** Operations portal error copy (codes only; never server text). Falls back to the shared payments/auth mapping. */
const MESSAGES: Record<string, string> = {
  OPS_NOT_FOUND: "This record was not found. Reload the page.",
  OPS_STATE: "This isn't possible in the record's current status. Reload the page to see the latest state.",
  OPS_RECORD_CHANGED: "Someone else changed this record since you loaded it. Reload and try again.",
  OPS_SLA_TARGET_PENDING: "A proposed target for this metric is already waiting for a decision.",
  OPS_CONFLICT_OF_INTEREST: "You can't decide this: it concerns your own access or an account linked to yours.",
  OPS_NOT_REVIEWER: "Only the named reviewer can decide the items of this access review.",
  OPS_EXCEPTION_NOT_OPEN: "The module no longer reports this exception, so it cannot enter triage.",
  OPS_EXCEPTION_DOMAIN_OPEN: "The module still reports this exception. Resolve it in the module first, or record that you are waiting.",
  SELF_APPROVAL_FORBIDDEN: "You can't decide this: you made it, or an account linked to yours did.",
  SELF_DECISION_FORBIDDEN: "You can't decide this: you made the request, or an account linked to yours did.",
  CAMPAIGN_NOT_PROMOTABLE: "This campaign can't be featured now: it must be active and public, without restrictions or open reviews.",
  PLACEMENT_EXISTS: "This campaign already has an open featured placement or request.",
  INVALID_STATE: "This isn't possible in the record's current status. Reload the page to see the latest state.",
  SELF_ACTION_FORBIDDEN: "You can't do this to your own account here.",
  STAFF_NOT_FOUND: "No such staff account.",
  VALIDATION_FAILED: "Check the highlighted fields and try again.",
  IDEMPOTENCY_KEY_REUSED: "This request was already sent with different details. Reload and try again.",
  RATE_LIMITED: "Too many requests. Try again later.",
  FORBIDDEN: "Your roles do not allow this action.",
  NOT_FOUND: "This was not found, or your roles do not allow it.",
};

const FIELD_MESSAGES: Record<string, string> = {
  assignee_id: "This person cannot be assigned: they need the permission to work these records.",
  reason: "Write 5 to 2,000 characters.",
  note: "Write a note (5 to 2,000 characters).",
};

export function describeOpsError(error: unknown, fallback = "The action could not be completed."): { message: string; field?: string } {
  if (isApiError(error)) {
    const d = error.details?.[0];
    if (error.code === "VALIDATION_FAILED" && d?.field) return { message: FIELD_MESSAGES[d.field] ?? `Check “${d.field.replace(/_/g, " ")}”.`, field: d.field };
    if (MESSAGES[error.code]) return { message: MESSAGES[error.code]! };
  }
  return { message: describePaymentError(error, fallback).message };
}
