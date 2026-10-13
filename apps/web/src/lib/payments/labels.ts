/**
 * Display helpers for the staff payment pages. The API owns every decision; these only choose text and badge
 * tones, and decide which controls to OFFER (the API re-checks every action). Unknown values are humanised,
 * never hidden.
 */
import type { BadgeTone } from "@/components/ui/badge";
import { humanise } from "@/lib/campaigns/labels";
import { first, isCursor, isUuid } from "@/lib/campaigns/paths";

import type { RefundAction } from "./types";

export { humanise };

const GOOD = new Set(["SUCCEEDED", "PAID", "PROCESSED", "APPROVED", "CLOSED", "ENABLED", "RESOLVED_BY_PROVIDER_STATUS", "ACCEPTED"]);
const ATTENTION = new Set([
  "UNKNOWN",
  "FAILED",
  "DEAD_LETTERED",
  "UNRECOGNISED",
  "FAILED_RETRYING",
  "OPEN_CIRCUIT",
  "ON_HOLD",
  "REJECTED",
  "CHARGED_BACK",
  "DISPUTED",
  "OUTCOME_UNKNOWN",
  "AWAITING_SETTLEMENT_DATA",
]);
const PROGRESS = new Set(["CREATED", "PENDING", "REQUIRES_ACTION", "AUTHORISED", "RECEIVED", "DISPATCHED", "PARKED", "REQUESTED", "PENDING_APPROVAL", "IN_FLIGHT", "HALF_OPEN"]);

export function statusTone(status: string): BadgeTone {
  if (GOOD.has(status)) return "brand";
  if (ATTENTION.has(status)) return "gold";
  if (PROGRESS.has(status)) return "info";
  return "neutral";
}

/** Refund actions offered for a status (Stage 8 machine, ADR-039 §3; the API is the authority). */
export function refundActions(status: string, isRequester: boolean): RefundAction[] {
  const out: RefundAction[] = [];
  if (status === "REQUESTED" || status === "PENDING_APPROVAL") {
    if (!isRequester) out.push("approve", "reject", "hold");
    else out.push("withdraw");
  } else if (status === "ON_HOLD" && !isRequester) {
    out.push("resume", "reject");
  }
  return out;
}

export const REFUND_ACTION_LABELS: Record<RefundAction, string> = {
  approve: "Approve",
  reject: "Reject",
  hold: "Put on hold",
  resume: "Resume",
  withdraw: "Withdraw",
};

export const REFUND_ACTION_NOTICES: Record<RefundAction, string> = {
  approve: "Refund request approved. Refund execution is DISABLED in Stage 8: nothing was sent to any provider.",
  reject: "Refund request rejected.",
  hold: "Refund request put on hold.",
  resume: "Refund request resumed and waiting for a decision.",
  withdraw: "Refund request withdrawn.",
};

export const REFUND_REASON_LABELS: Record<string, string> = {
  DONOR_REQUEST: "Donor request",
  DUPLICATE_PAYMENT: "Duplicate payment",
  PAYMENT_ERROR: "Payment error",
  CAMPAIGN_CANCELLED: "Campaign cancelled",
  CAMPAIGN_FRAUD: "Campaign fraud",
  COMPLIANCE_ORDER: "Compliance order",
  OTHER: "Other",
};

/** Reasons and notes on staff actions: 3–2,000 characters (counted as code points). The API re-validates. */
export function reasonProblem(value: string): string | null {
  const n = [...value.trim()].length;
  return n < 3 || n > 2000 ? "Write a reason of 3 to 2,000 characters." : null;
}

/** Reads one allowed value from a search param, else undefined. */
export function pick<T extends string>(value: string | string[] | undefined, allowed: readonly T[]): T | undefined {
  const v = first(value);
  return v !== undefined && (allowed as readonly string[]).includes(v) ? (v as T) : undefined;
}

/** A query string from defined, well-formed values only (cursor and ids validated before they reach an API path). */
export function buildQuery(values: Record<string, string | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(values)) if (v !== undefined && v !== "") q.set(k, v);
  const s = q.toString();
  return s ? `?${s}` : "";
}

export function cursorParam(value: string | string[] | undefined): string | undefined {
  const v = first(value);
  return isCursor(v) ? v : undefined;
}

export function uuidParam(value: string | string[] | undefined): string | undefined {
  const v = first(value);
  return isUuid(v) ? v : undefined;
}
