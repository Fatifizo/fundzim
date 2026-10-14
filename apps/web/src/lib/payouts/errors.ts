import { isApiError } from "@/lib/api/errors";
import { describePaymentError } from "@/lib/payments/errors";

import { reasonMessage } from "./normalise";

/** Payout error copy (codes only; never server text). Falls back to the payments/campaign/auth mapping. */
const MESSAGES: Record<string, string> = {
  PAYOUT_REQUEST_NOT_FOUND: "This withdrawal request was not found. Reload the page.",
  PAYOUT_IN_PROGRESS: "Another withdrawal for this campaign is already in progress. Wait until it finishes.",
  PAYOUT_STATE: "This isn't possible in the withdrawal's current status. Reload the page to see the latest state.",
  PAYOUTS_DISABLED: "Withdrawals are not available in this environment.",
  SELF_APPROVAL_FORBIDDEN:
    "You can't decide this payout: you requested it, own the campaign, are linked to the owner, or changed or verified its payout account.",
  ALREADY_DECIDED: "You have already decided this payout in the current approval round.",
  IDEMPOTENCY_KEY_REUSED: "This request was already sent with different details. Reload and try again.",
  PAYOUT_EXCEPTION_NOT_FOUND: "This exception was not found. Reload the page.",
  PAYOUT_EXCEPTION_STATE: "This exception has already moved on. Reload the page.",
};

export function describePayoutError(error: unknown, fallback = "The action could not be completed."): { message: string } {
  if (isApiError(error)) {
    if (error.code === "PAYOUT_NOT_ELIGIBLE") {
      const cat = error.details.find((d) => d.field === "reason_category")?.code;
      return { message: reasonMessage(cat) };
    }
    if (MESSAGES[error.code]) return { message: MESSAGES[error.code]! };
  }
  return { message: describePaymentError(error, fallback).message };
}
