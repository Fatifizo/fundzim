/**
 * Display and input helpers for fees. Rates are integer basis points (MONEY.md §5): they are shown and parsed with
 * string arithmetic only. Money is never computed here — the API calculates every fee.
 */
import { parseDecimalToMinor } from "@/lib/campaigns/money";
import { formatLedgerAmount } from "@/lib/ledger/money";
import { humanise } from "@/lib/payments/labels";
import { minorUnitsFor } from "@/lib/payments/money";

import type { Terms } from "./types";

/** 250 → "2.50%" (basis points are integers, not money). */
export function bpsToPercent(bps: number | null | undefined): string {
  if (typeof bps !== "number" || !Number.isInteger(bps) || bps < 0) return "—";
  const whole = Math.floor(bps / 100);
  const frac = String(bps % 100).padStart(2, "0");
  return `${whole}.${frac}%`;
}

/** "2.5" → 250 basis points (0–100 %, at most two decimals); null when invalid. */
export function percentToBps(input: string): number | null {
  const m = /^(\d{1,3})(?:\.(\d{1,2}))?$/.exec(input.trim());
  if (!m) return null;
  const bps = Number.parseInt(m[1]!, 10) * 100 + Number.parseInt((m[2] ?? "").padEnd(2, "0") || "0", 10);
  return bps <= 10_000 ? bps : null;
}

/** A typed amount in currency units → minor-unit digit string ("0" allowed); null when invalid. */
export function amountToMinor(input: string, currency: string): string | null {
  const units = minorUnitsFor(currency);
  if (units === null) return null;
  const raw = input.trim();
  if (/^0+(\.0+)?$/.test(raw)) return "0";
  const p = parseDecimalToMinor(raw, units);
  return p.ok ? p.amountMinor : null;
}

const money = (minor: string | null, currency: string) => (minor === null ? null : formatLedgerAmount(minor, currency).text);

/** Plain-language summary of fee terms, e.g. "2.90% + US$0.30, max US$10.00, rounded half up". */
export function describeTerms(t: Terms, currency: string): string {
  const parts: string[] = [];
  switch (t.model) {
    case "ZERO_FEE":
      return "No fee";
    case "PROVIDER_PASS_THROUGH":
      return "Provider's fee, passed through (amount from provider fee evidence)";
    case "FIXED":
      parts.push(money(t.fixed_minor, currency) ?? "—");
      break;
    case "PERCENTAGE_PLUS_FIXED":
      parts.push(`${bpsToPercent(t.rate_bps)} + ${money(t.fixed_minor, currency) ?? "—"}`);
      break;
    case "TIERED":
      parts.push(
        t.tiers
          .map((tier) => `${tier.up_to_minor ? `up to ${money(tier.up_to_minor, currency)}` : "above"}: ${bpsToPercent(tier.rate_bps)}${tier.fixed_minor !== "0" ? ` + ${money(tier.fixed_minor, currency)}` : ""}`)
          .join("; "),
      );
      break;
    default:
      parts.push(bpsToPercent(t.rate_bps));
  }
  if (t.min_fee_minor) parts.push(`min ${money(t.min_fee_minor, currency)}`);
  if (t.max_fee_minor) parts.push(`max ${money(t.max_fee_minor, currency)}`);
  if (t.rounding_mode) parts.push(`rounded ${humanise(t.rounding_mode).toLowerCase()}`);
  return parts.join(", ");
}

const BASIS: Record<string, string> = {
  QUOTED: "Quoted (fee policy)",
  ESTIMATED: "Estimated (unverified or synthetic provider evidence)",
  PROVIDER_REPORTED: "Reported by the provider",
  PROVIDER_CONFIRMED: "Confirmed by the provider",
  RECONCILED: "Reconciled",
  UNKNOWN: "Unknown (no provider evidence)",
  NOT_CONFIGURED: "No active policy (nothing charged)",
};
export const basisLabel = (basis: string) => BASIS[basis] ?? humanise(basis);

const RESPONSIBILITY: Record<string, string> = {
  DONOR_COVERED: "Paid by the donor",
  ORGANISER_COVERED: "Paid by the campaign",
  SHARED: "Shared by donor and campaign",
  PLATFORM_ABSORBED: "Absorbed by FundZim",
};
export const responsibilityLabel = (r: string | null | undefined) => (r ? RESPONSIBILITY[r] ?? humanise(r) : "Not decided (no policy)");
