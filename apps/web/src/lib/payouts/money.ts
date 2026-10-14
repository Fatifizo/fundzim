/**
 * Withdrawal amounts: typed decimals → minor-unit digit strings with string arithmetic only (no Number, no float),
 * compared with BigInt in ONE currency. The API is the authority.
 */
import { parseDecimalToMinor, type AmountProblem } from "@/lib/campaigns/money";
import { minorUnitsFor } from "@/lib/payments/money";

export type WithdrawalAmountResult = { ok: true; amountMinor: string } | { ok: false; message: string };

const MESSAGES: Record<AmountProblem, string> = {
  EMPTY: "Enter the amount to withdraw.",
  INVALID_FORMAT: "Enter the amount as a number, for example 25 or 25.50.",
  TOO_MANY_DECIMALS: "This currency has fewer decimal places. Remove the extra digits.",
  NOT_POSITIVE: "The amount must be more than zero.",
  TOO_LARGE: "This amount is too large.",
};

export function parseWithdrawalAmount(input: string, currency: string, availableMinor?: string | null): WithdrawalAmountResult {
  const units = minorUnitsFor(currency);
  if (units === null) return { ok: false, message: "Withdrawals in this currency aren't available." };
  const parsed = parseDecimalToMinor(input, units);
  if (!parsed.ok) return { ok: false, message: MESSAGES[parsed.problem] };
  if (availableMinor && /^\d{1,19}$/.test(availableMinor) && BigInt(parsed.amountMinor) > BigInt(availableMinor)) {
    return { ok: false, message: "This is more than the available test balance." };
  }
  return { ok: true, amountMinor: parsed.amountMinor };
}
