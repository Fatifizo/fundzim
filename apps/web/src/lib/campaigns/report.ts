/**
 * "Report this campaign" (contract §3, ADR-038 §3). Client-side checks mirror the API's rules for early
 * feedback; the API decides. A report never changes a campaign's status by itself, so no copy may promise
 * removal.
 */
import { contentProblem } from "./text";

export const REPORT_REASONS = [
  { value: "MISLEADING_INFORMATION", label: "Misleading or false information" },
  { value: "IMPERSONATION", label: "Someone is pretending to be another person or organisation" },
  { value: "SUSPICIOUS_ACTIVITY", label: "Suspicious activity, such as requests for money outside FundZim" },
  { value: "INAPPROPRIATE_CONTENT", label: "Inappropriate or offensive content" },
  { value: "PRIVACY_CONCERN", label: "It shares someone's private information" },
  { value: "OTHER", label: "Something else (please explain)" },
] as const;

export type ReportReason = (typeof REPORT_REASONS)[number]["value"];

export const DETAILS_MIN_FOR_OTHER = 10;
export const DETAILS_MAX = 2000;
export const EMAIL_MAX = 254;

export interface ReportInput {
  reason: string;
  details: string;
  contactEmail: string;
  /** Honeypot: people never see it; it must stay empty. */
  website: string;
}

export type ReportField = "reason" | "details" | "contact_email";

export interface ReportPayload {
  reason: ReportReason;
  details?: string;
  contact_email?: string;
  website?: string;
}

const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

export function isReportReason(value: string): value is ReportReason {
  return REPORT_REASONS.some((r) => r.value === value);
}

function length(value: string): number {
  return [...value].length;
}

/** Field errors (fixed copy) for the form; empty when the input can be sent. */
export function validateReport(input: ReportInput): Partial<Record<ReportField, string>> {
  const errors: Partial<Record<ReportField, string>> = {};
  if (!isReportReason(input.reason)) errors.reason = "Choose a reason.";
  const details = input.details.trim();
  if (input.reason === "OTHER" && length(details) < DETAILS_MIN_FOR_OTHER) errors.details = `Tell us what is wrong (at least ${DETAILS_MIN_FOR_OTHER} characters).`;
  else if (length(details) > DETAILS_MAX) errors.details = `Keep the details to ${DETAILS_MAX.toLocaleString("en-GB")} characters or fewer.`;
  else if (details && contentProblem(details) === "HTML_NOT_ALLOWED") errors.details = "Use plain text only: HTML tags are not allowed.";
  else if (details && contentProblem(details) === "UNSAFE_LINK") errors.details = "Remove links that start with javascript:, data: or vbscript:.";
  const email = input.contactEmail.trim();
  if (email && (email.length > EMAIL_MAX || !EMAIL.test(email))) errors.contact_email = "Enter an email address in the format name@example.com, or leave it empty.";
  return errors;
}

/** Request body: optional fields are left out when empty. The honeypot is sent only if something filled it. */
export function reportPayload(input: ReportInput): ReportPayload {
  const payload: ReportPayload = { reason: input.reason as ReportReason };
  const details = input.details.trim();
  const email = input.contactEmail.trim();
  if (details) payload.details = details;
  if (email) payload.contact_email = email;
  if (input.website) payload.website = input.website;
  return payload;
}
