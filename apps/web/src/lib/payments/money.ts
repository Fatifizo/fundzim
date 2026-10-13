/**
 * Money for the staff payment pages (docs/MONEY.md, docs/FRONTEND.md §7). Reuses the shared string/BigInt
 * helpers: display via formatGoal (lib/campaigns/money.ts → lib/money.ts) and input via parseDecimalToMinor. No
 * Number(), parseFloat or division ever touches an amount, and amounts of different currencies are never combined.
 */
import { formatGoal, parseDecimalToMinor, type AmountProblem } from "@/lib/campaigns/money";
import { CURRENCIES, isCurrencyCode } from "@/lib/money";

import type { PaymentMoney } from "./types";

export interface DisplayMoney {
  text: string;
  accessibleText: string;
}

/** "US$12.50" / "ZiG 12.50" / "XYZ 12.50"; "—" for a missing or malformed amount (never throws). */
export function formatPaymentMoney(money: PaymentMoney | null | undefined): DisplayMoney {
  return formatGoal(money ?? null);
}

/** Minor-unit digits for a currency the web app knows, else null (the form is then not offered). */
export function minorUnitsFor(currency: string): number | null {
  return isCurrencyCode(currency) ? CURRENCIES[currency].minorUnits : null;
}

export type RefundAmountResult = { ok: true; amountMinor: string } | { ok: false; message: string };

const MESSAGES: Record<AmountProblem, string> = {
  EMPTY: "Enter the refund amount.",
  INVALID_FORMAT: "Enter the amount as a number, for example 25 or 25.50.",
  TOO_MANY_DECIMALS: "This currency has fewer decimal places. Remove the extra digits.",
  NOT_POSITIVE: "The refund amount must be more than zero.",
  TOO_LARGE: "This amount is too large.",
};

/**
 * Converts a typed decimal string ("25", "1,250.50") into an `amount_minor` digit string for the payment's
 * currency, with string arithmetic only. Invalid input is rejected, never rounded. When `maxMinor` (the payment
 * amount) is given, a larger refund is refused here as a convenience; comparison is BigInt on the SAME currency.
 * The API is the authority (over-refund is impossible in the database).
 */
export function parseRefundAmount(input: string, currency: string, maxMinor?: string | null): RefundAmountResult {
  const units = minorUnitsFor(currency);
  if (units === null) return { ok: false, message: "Refunds in this currency can't be requested here." };
  const parsed = parseDecimalToMinor(input, units);
  if (!parsed.ok) return { ok: false, message: MESSAGES[parsed.problem] };
  if (maxMinor && /^\d{1,19}$/.test(maxMinor) && BigInt(parsed.amountMinor) > BigInt(maxMinor)) {
    return { ok: false, message: "A refund can't be more than the payment amount." };
  }
  return { ok: true, amountMinor: parsed.amountMinor };
}
