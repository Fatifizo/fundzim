/**
 * Money display helpers (docs/MONEY.md §3.3, §3.4, §8; docs/FRONTEND.md §7).
 *
 * Rules enforced here:
 *  - `amount_minor` is a string of digits (wire format `^-?[0-9]{1,19}$`, no leading zeros, within int64).
 *  - Values are handled as strings / BigInt only. This module never calls Number() or parseFloat on an
 *    amount and never divides by 100.
 *  - The currency is always rendered next to the amount (USD → "US$", ZWG → "ZiG").
 *  - No arithmetic across currencies; this module only formats a single Money value.
 *
 * Minor-unit counts below are the display defaults for the two Stage 3 currencies. The authoritative
 * registry is the API's `currencies` table (ZWG minor units are flagged `minor_units_verified=false`
 * there); when the API exposes currency config, pass `minorUnits` explicitly.
 */
import type { CurrencyCode, Money } from "@/lib/api/types";

export interface CurrencyDisplay {
  /** ISO 4217 code as it travels in data. */
  code: CurrencyCode;
  /** Number of minor-unit digits. */
  minorUnits: number;
  /** Visible symbol/prefix. */
  symbol: string;
  /** Name for assistive technology. */
  name: string;
}

export const CURRENCIES: Readonly<Record<CurrencyCode, CurrencyDisplay>> = Object.freeze({
  USD: { code: "USD", minorUnits: 2, symbol: "US$", name: "US dollars" },
  ZWG: { code: "ZWG", minorUnits: 2, symbol: "ZiG", name: "ZiG" },
});

const INT64_MAX = BigInt("9223372036854775807");
const INT64_MIN = BigInt("-9223372036854775808");
const AMOUNT_MINOR_PATTERN = /^-?(0|[1-9][0-9]{0,18})$/;

export class MoneyFormatError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "MoneyFormatError";
  }
}

export function isCurrencyCode(value: string): value is CurrencyCode {
  return Object.prototype.hasOwnProperty.call(CURRENCIES, value);
}

/** Validates the wire format of `amount_minor` and returns it as a BigInt. */
export function parseAmountMinor(amountMinor: string): bigint {
  if (typeof amountMinor !== "string" || !AMOUNT_MINOR_PATTERN.test(amountMinor) || amountMinor === "-0") {
    throw new MoneyFormatError("amount_minor must be a string of digits without leading zeros");
  }
  const value = BigInt(amountMinor);
  if (value > INT64_MAX || value < INT64_MIN) {
    throw new MoneyFormatError("amount_minor is outside the int64 range");
  }
  return value;
}

/**
 * Converts minor units to an exact decimal string, e.g. ("123456", 2) → "1234.56", ("-5", 2) → "-0.05".
 * Pure string/BigInt manipulation; no floating point.
 */
export function minorToDecimalString(amountMinor: string, minorUnits: number): string {
  if (!Number.isInteger(minorUnits) || minorUnits < 0 || minorUnits > 6) {
    throw new MoneyFormatError("minorUnits must be an integer between 0 and 6");
  }
  const value = parseAmountMinor(amountMinor);
  const negative = value < BigInt(0);
  const digits = (negative ? -value : value).toString();
  if (minorUnits === 0) return (negative ? "-" : "") + digits;
  const padded = digits.padStart(minorUnits + 1, "0");
  const whole = padded.slice(0, padded.length - minorUnits);
  const fraction = padded.slice(padded.length - minorUnits);
  return `${negative ? "-" : ""}${whole}.${fraction}`;
}

/** Inserts thousands separators into a string of digits (en-style grouping). */
function groupThousands(digits: string): string {
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

export interface FormatMoneyOptions {
  /** Override the minor-unit count (e.g. from API currency config). */
  minorUnits?: number;
}

export interface FormattedMoney {
  /** Visual text, e.g. "US$1,250.00" or "ZiG 1,250.00". */
  text: string;
  /** Screen-reader text, e.g. "1,250.00 US dollars". */
  accessibleText: string;
}

/**
 * Formats a Money value for display. Grouping is done on the exact decimal string (en-ZW style:
 * comma thousands separator, dot decimal separator), which is identical to Intl's `en` output but
 * independent of engine support for string input to Intl.NumberFormat.
 */
export function formatMoneyParts(money: Money, options: FormatMoneyOptions = {}): FormattedMoney {
  if (!isCurrencyCode(money.currency)) {
    throw new MoneyFormatError(`Unsupported currency: ${String(money.currency)}`);
  }
  const currency = CURRENCIES[money.currency];
  const decimal = minorToDecimalString(money.amount_minor, options.minorUnits ?? currency.minorUnits);
  const negative = decimal.startsWith("-");
  const unsigned = negative ? decimal.slice(1) : decimal;
  const [whole = "0", fraction] = unsigned.split(".");
  const number = groupThousands(whole) + (fraction !== undefined ? `.${fraction}` : "");
  const sign = negative ? "-" : "";
  const separator = currency.symbol === "US$" ? "" : " ";
  return {
    text: `${sign}${currency.symbol}${separator}${number}`,
    accessibleText: `${sign}${number} ${currency.name}`,
  };
}

export function formatMoney(money: Money, options: FormatMoneyOptions = {}): string {
  return formatMoneyParts(money, options).text;
}
