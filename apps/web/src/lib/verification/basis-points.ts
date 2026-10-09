/**
 * Ownership shares travel as integer basis points (`ownership_bp`, 0–10000; 1 bp = 0.01 %). The UI takes
 * and shows percentages. Conversion is pure string/integer work — no floating point (a parse of "29.3"
 * through a JS number gives 2929.9999…): the decimal string is split into whole and fractional digits and
 * combined as integers. Invalid input is rejected, never rounded.
 */
export const MAX_BASIS_POINTS = 10_000;

export type PercentParseResult =
  | { ok: true; basisPoints: number }
  | { ok: false; error: "EMPTY" | "INVALID_FORMAT" | "TOO_MANY_DECIMALS" | "OUT_OF_RANGE" };

const PERCENT_PATTERN = /^(\d{1,3})(?:[.,](\d*))?$/;

/** Parses a user-typed percentage ("25", "25.5", "0.01", "100", "12,5", optional trailing "%") into basis points. */
export function percentToBasisPoints(input: string): PercentParseResult {
  const trimmed = input.trim().replace(/\s*%$/, "");
  if (trimmed === "") return { ok: false, error: "EMPTY" };
  const match = PERCENT_PATTERN.exec(trimmed);
  if (!match) return { ok: false, error: "INVALID_FORMAT" };
  const whole = match[1]!;
  const fraction = match[2] ?? "";
  if (fraction.length > 2) return { ok: false, error: "TOO_MANY_DECIMALS" };
  const basisPoints = parseInt(whole, 10) * 100 + parseInt(fraction.padEnd(2, "0") || "0", 10);
  if (basisPoints > MAX_BASIS_POINTS) return { ok: false, error: "OUT_OF_RANGE" };
  return { ok: true, basisPoints };
}

/** Formats basis points as a percentage string without a trailing "%": 2550 → "25.5", 10000 → "100", 1 → "0.01". */
export function basisPointsToPercent(basisPoints: number): string {
  if (!Number.isInteger(basisPoints) || basisPoints < 0 || basisPoints > MAX_BASIS_POINTS) {
    throw new RangeError(`basis points out of range: ${basisPoints}`);
  }
  const whole = Math.trunc(basisPoints / 100); // exact: integer division of a safe integer
  const fraction = String(basisPoints % 100).padStart(2, "0").replace(/0+$/, "");
  return fraction ? `${whole}.${fraction}` : String(whole);
}

export function formatOwnership(basisPoints: number | null | undefined): string {
  if (basisPoints === null || basisPoints === undefined) return "—";
  return `${basisPointsToPercent(basisPoints)}%`;
}

export const PERCENT_ERROR_MESSAGES: Record<Exclude<PercentParseResult, { ok: true }>["error"], string> = {
  EMPTY: "Enter the ownership share as a percentage.",
  INVALID_FORMAT: "Enter a number between 0 and 100, for example 25 or 12.5.",
  TOO_MANY_DECIMALS: "Use at most two decimal places, for example 33.33.",
  OUT_OF_RANGE: "The ownership share cannot be more than 100%.",
};
