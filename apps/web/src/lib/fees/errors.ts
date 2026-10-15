import { isApiError } from "@/lib/api/errors";
import { describePaymentError } from "@/lib/payments/errors";

/** Fee error copy (codes only; never server text). Falls back to the payments/campaign/auth mapping. */
const MESSAGES: Record<string, string> = {
  FEE_NOT_FOUND: "This fee record was not found. Reload the page.",
  FEE_CATEGORY_NOT_ENABLED: "This fee category cannot be enabled: FundZim never converts currencies, and other fees have no posting rule.",
  FEE_POLICY_STATE: "This isn't possible in the version's current status. Reload the page to see the latest state.",
  FEE_POLICY_CONFLICT: "Another version or policy already covers this. Reload the page.",
  FEE_POLICY_CODE_TAKEN: "A fee policy with this code already exists.",
  FEE_POLICY_AMBIGUOUS: "More than one fee policy matches. Ask finance to review the policies.",
  FEE_POLICY_RETROACTIVE: "A fee policy version cannot take effect before it is submitted. Choose a later start.",
  FEE_TERMS_CHANGED: "The terms you reviewed are not this version's terms. Reload and review again.",
  FEE_DRAFT_NOT_YOURS: "Only the author of a draft can edit it.",
  SELF_APPROVAL_FORBIDDEN: "You can't decide this: you made it, or an account linked to yours did.",
  FEE_QUOTE_EXPIRED: "The fee quote has expired. Request a new quote.",
  FEE_QUOTE_STALE: "The fees changed since this quote. Request a new quote.",
  FEE_QUOTE_ALREADY_USED: "This fee quote was already used.",
  FEE_EXCEEDS_AMOUNT: "The fees borne by the campaign would leave nothing. Use a larger amount.",
  CAMPAIGN_NOT_ACCEPTING_DONATIONS: "This campaign is not accepting donations.",
  CAMPAIGN_NOT_FOUND: "This campaign was not found.",
  PAYMENT_METHOD_UNAVAILABLE: "This payment method is not available.",
  CURRENCY_NOT_SUPPORTED: "This currency cannot be used.",
  FEE_ADJUSTMENT_STATE: "This adjustment has already been decided. Reload the page.",
  FEE_ADJUSTMENT_EXCEEDS_ASSESSMENT: "The adjustment would take the fee below zero.",
  FEE_AMOUNT_UNKNOWN: "This fee's amount is unknown and cannot be adjusted.",
  FEE_RECONCILIATION_STATE: "This item is already resolved. Reload the page.",
  PROVIDER_FEE_VERIFICATION_UNAVAILABLE: "Provider fees cannot be marked verified: no provider is selected.",
  PROVIDER_FEE_SYNTHETIC_REAL_PROVIDER: "Synthetic provider fees may only be recorded for the sandbox simulator.",
  IDEMPOTENCY_KEY_REUSED: "This request was already sent with different details. Reload and try again.",
  RATE_LIMITED: "Too many requests. Try again later.",
};

export function describeFeeError(error: unknown, fallback = "The action could not be completed."): { message: string } {
  if (isApiError(error) && MESSAGES[error.code]) return { message: MESSAGES[error.code]! };
  return { message: describePaymentError(error, fallback).message };
}
