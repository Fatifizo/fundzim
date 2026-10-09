/**
 * Campaign goal amounts (docs/MONEY.md, docs/FRONTEND.md §7). Typed decimal strings are converted to minor
 * units with STRING arithmetic only: no Number(), parseFloat or division ever touches an amount. The API
 * re-validates every amount; this is a convenience for early, precise feedback.
 */
import type { Money } from "@/lib/api/types";
import { CURRENCIES, isCurrencyCode, minorToDecimalString, MoneyFormatError, parseAmountMinor } from "@/lib/money";

const INT64_MAX = BigInt("9223372036854775807");

export type AmountProblem = "EMPTY" | "INVALID_FORMAT" | "TOO_MANY_DECIMALS" | "NOT_POSITIVE" | "TOO_LARGE";

export type ParsedAmount = { ok: true; amountMinor: string } | { ok: false; problem: AmountProblem };

/** Plain digits with an optional fraction, or digits grouped with commas in threes ("12,500.50"). */
const PLAIN = /^(\d+)(?:\.(\d*))?$/;
const GROUPED = /^(\d{1,3}(?:,\d{3})+)(?:\.(\d*))?$/;

/**
 * Parses a decimal string typed by a person ("50", "1,250.5", "0.99") into an `amount_minor` digit string
 * for a currency with `minorUnits` decimals. Strict: no sign, exponent, spaces inside the number, or more
 * decimals than the currency has (rejected, never rounded). Leading zeros are normalised away.
 */
export function parseDecimalToMinor(input: string, minorUnits: number): ParsedAmount {
  if (!Number.isInteger(minorUnits) || minorUnits < 0 || minorUnits > 6) throw new MoneyFormatError("minorUnits must be an integer between 0 and 6");
  const raw = typeof input === "string" ? input.trim() : "";
  if (raw === "") return { ok: false, problem: "EMPTY" };
  const match = PLAIN.exec(raw) ?? GROUPED.exec(raw);
  if (!match) return { ok: false, problem: "INVALID_FORMAT" };
  const whole = match[1]!.replace(/,/g, "");
  const fraction = match[2] ?? "";
  if (raw.endsWith(".") && fraction === "") return { ok: false, problem: "INVALID_FORMAT" };
  if (fraction.length > minorUnits) return { ok: false, problem: "TOO_MANY_DECIMALS" };
  const digits = (whole + fraction.padEnd(minorUnits, "0")).replace(/^0+(?=\d)/, "");
  if (digits.length > 19) return { ok: false, problem: "TOO_LARGE" };
  const value = BigInt(digits);
  if (value > INT64_MAX) return { ok: false, problem: "TOO_LARGE" };
  if (value === BigInt(0)) return { ok: false, problem: "NOT_POSITIVE" };
  return { ok: true, amountMinor: value.toString() };
}

/** Pre-fills an amount input from minor units ("125050", 2 → "1250.50"). */
export function minorToInput(amountMinor: string, minorUnits: number): string {
  return minorToDecimalString(amountMinor, minorUnits);
}

export const AMOUNT_PROBLEM_MESSAGES: Record<AmountProblem, string> = {
  EMPTY: "Enter a goal amount.",
  INVALID_FORMAT: "Enter the amount as a number, for example 1500 or 1,500.50.",
  TOO_MANY_DECIMALS: "This currency has fewer decimal places. Remove the extra digits.",
  NOT_POSITIVE: "The goal must be more than zero.",
  TOO_LARGE: "This amount is too large.",
};

export interface CurrencyOption {
  code: string;
  minorUnits: number;
  symbol: string;
}

/**
 * Formats a Money value with the currency's minor units. Uses src/lib/money.ts for USD/ZWG and a plain
 * "<CODE> <decimal>" for any other code the API sends, so an unexpected currency is shown, never hidden or
 * guessed. Returns "—" for a malformed amount instead of throwing during render.
 */
export function formatGoal(money: Money | { amount_minor: string; currency: string } | null | undefined, minorUnits?: number): { text: string; accessibleText: string } {
  if (!money || typeof money.amount_minor !== "string") return { text: "—", accessibleText: "not set" };
  try {
    parseAmountMinor(money.amount_minor);
    if (isCurrencyCode(money.currency)) {
      const units = minorUnits ?? CURRENCIES[money.currency].minorUnits;
      const decimal = minorToDecimalString(money.amount_minor, units);
      const [whole = "0", fraction] = decimal.split(".");
      const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",") + (fraction !== undefined ? `.${fraction}` : "");
      const currency = CURRENCIES[money.currency];
      return { text: `${currency.symbol}${currency.symbol === "US$" ? "" : " "}${grouped}`, accessibleText: `${grouped} ${currency.name}` };
    }
    const code = /^[A-Z]{3}$/.test(String(money.currency)) ? String(money.currency) : "???";
    const decimal = minorToDecimalString(money.amount_minor, minorUnits ?? 2);
    return { text: `${code} ${decimal}`, accessibleText: `${decimal} ${code}` };
  } catch {
    return { text: "—", accessibleText: "not available" };
  }
}
