import { isApiError } from "@/lib/api/errors";
import { describeCampaignError } from "@/lib/campaigns/errors";

/**
 * Maps the Stage 8 payment error codes (interface-contracts §5) to staff-facing copy. Branches on codes only and
 * never echoes server text. Unknown codes fall through to the campaign/verification/auth mapping (CSRF, rate
 * limits, network, step-up…).
 */
const MESSAGES: Record<string, string> = {
  PAYMENT_NOT_FOUND: "This payment was not found. Reload the page.",
  INBOX_EVENT_NOT_REQUEUEABLE: "Only dead-lettered or unrecognised webhooks can be re-queued. Reload to see its current state.",
  REFUND_NOT_ALLOWED: "A refund can't be requested or decided for this payment in its current state. Reload the page.",
  REFUND_EXCEEDS_REFUNDABLE: "This is more than the amount that can still be refunded for this payment.",
  REFUND_REQUEST_NOT_FOUND: "This refund request was not found. Reload the page.",
  REFUND_SELF_APPROVAL_FORBIDDEN: "You requested this refund, so you can't decide on it. Another authorised person must do this.",
  RECONCILIATION_REFERENCE_NOT_FOUND: "This reconciliation reference was not found. Reload the page.",
  SANDBOX_DISABLED: "The sandbox simulator is not enabled in this environment.",
  PAYMENT_EXECUTION_DISABLED: "Payment execution is disabled.",
  PAYMENTS_UNAVAILABLE: "Payments are not available in this environment.",
  INVALID_STATUS: "This isn't possible in the current state. Reload the page to see the latest state.",
  CONFLICT: "This changed somewhere else. Reload the page and try again.",
};

export interface DescribedPaymentError {
  message: string;
  fieldErrors: Record<string, string>;
}

export function describePaymentError(error: unknown, fallback = "The action could not be completed."): DescribedPaymentError {
  const base = describeCampaignError(error, fallback);
  const out: DescribedPaymentError = { message: base.message, fieldErrors: { ...base.fieldErrors } };
  if (isApiError(error) && MESSAGES[error.code]) {
    out.message = MESSAGES[error.code]!;
    if (error.code === "REFUND_EXCEEDS_REFUNDABLE") out.fieldErrors = { ...out.fieldErrors, amount: "More than the refundable amount." };
  }
  return out;
}
