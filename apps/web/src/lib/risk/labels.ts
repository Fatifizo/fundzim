/**
 * Display helpers for the staff risk pages. The API owns every decision; these only choose wording and decide which
 * controls to OFFER (the API re-checks every action). Wording rules (ADR-045): a score is never presented as a
 * finding about a person; a provider "no match" is never a clearance; nobody is described as a listed person.
 */
import type { BadgeTone } from "@/components/ui/badge";
import { humanise } from "@/lib/payments/labels";

const SCREENING: Record<string, string> = {
  PENDING: "Pending provider result",
  POTENTIAL_MATCH: "Potential match — analyst review required",
  NO_MATCH_REPORTED: "Provider reported no match (not a clearance)",
  REVIEW_REQUIRED: "Review required",
  CONFIRMED_MATCH: "Match confirmed by an analyst (TEST) — with compliance",
  CLEARED: "Cleared by an analyst (TEST)",
  ERROR: "Error — not cleared",
};

export function screeningStatusLabel(status: string): string {
  return SCREENING[status] ?? humanise(status);
}

const CLASS_TONE: Record<string, BadgeTone> = { LOW: "neutral", MEDIUM: "info", HIGH: "gold", CRITICAL: "gold" };
export function classificationTone(c: string | null | undefined): BadgeTone {
  return (c && CLASS_TONE[c]) || "neutral";
}

/** Seconds as whole hours or days ("24 hours", "7 days"). */
export function durationText(seconds: number | null): string {
  if (seconds === null || seconds <= 0) return "No expiry";
  if (seconds % 86_400 === 0) return `${seconds / 86_400} day${seconds === 86_400 ? "" : "s"}`;
  if (seconds % 3_600 === 0) return `${seconds / 3_600} hour${seconds === 3_600 ? "" : "s"}`;
  return `${seconds} seconds`;
}

/** Case working edges a worker may take directly (internal/risk/cases.go manualTransitions). */
const MANUAL: Record<string, string[]> = {
  OPEN: ["TRIAGE"],
  TRIAGE: ["UNDER_INVESTIGATION"],
  REOPENED: ["TRIAGE"],
  UNDER_INVESTIGATION: ["AWAITING_INFORMATION"],
  AWAITING_INFORMATION: ["UNDER_INVESTIGATION"],
  ESCALATED: ["UNDER_INVESTIGATION"],
  RESOLVED: ["CLOSED"],
};
export function caseTransitions(status: string): string[] {
  return MANUAL[status] ?? [];
}

export const caseIsOpen = (status: string) => status !== "RESOLVED" && status !== "CLOSED";
export const canEscalate = (status: string) => status === "TRIAGE" || status === "UNDER_INVESTIGATION";
export const canPropose = (status: string) => status === "TRIAGE" || status === "UNDER_INVESTIGATION" || status === "ESCALATED";

const OUTCOME: Record<string, string> = {
  NO_FURTHER_ACTION: "No further action",
  FALSE_POSITIVE: "False positive",
  RISK_CONFIRMED_HOLD_MAINTAINED: "Risk confirmed — hold maintained",
  ESCALATED_TO_COMPLIANCE: "Escalated to compliance",
};
export function outcomeLabel(o: string | null): string {
  return o ? (OUTCOME[o] ?? humanise(o)) : "—";
}

/** A rule version's lifecycle actions offered for its status. */
export function ruleActions(status: string): string[] {
  switch (status) {
    case "DRAFT":
      return ["submit", "retire"];
    case "PENDING_APPROVAL":
      return ["approve", "reject"];
    case "APPROVED":
      return ["activate", "retire"];
    case "ACTIVE":
      return ["suspend", "retire"];
    case "SUSPENDED":
      return ["reactivate", "retire"];
  }
  return [];
}

/** A hold's actions offered for its status. */
export function holdActions(status: string): string[] {
  switch (status) {
    case "REQUESTED":
      return ["activate", "revoke"];
    case "ACTIVE":
      return ["review", "request-release"];
    case "UNDER_REVIEW":
      return ["request-release"];
    case "RELEASE_PENDING":
      return ["approve-release", "reject-release"];
  }
  return [];
}

/** First 12 characters of an id or hash, for compact tables (the full value is in the title/detail). */
export const short = (s: string | null | undefined) => (s ? s.slice(0, 12) : "—");
