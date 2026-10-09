import { isApiError } from "@/lib/api/errors";
import { describeError, type DescribedError } from "@/lib/auth/errors";

import { documentTypeLabel, humanise } from "./labels";

/**
 * Maps Stage 5 verification error codes (interface-contracts §3 and §7) to user-facing copy. Branches on
 * `code` and `details[].code` only and never echoes the server's message text. Codes it does not know fall
 * through to the Stage 4 mapping (CSRF, rate limits, network, validation, step-up…).
 */
export const VerificationErrorCode = {
  CASE_ALREADY_OPEN: "CASE_ALREADY_OPEN",
  PREREQUISITES_NOT_MET: "PREREQUISITES_NOT_MET",
  CASE_NOT_EDITABLE: "CASE_NOT_EDITABLE",
  SUBMISSION_INCOMPLETE: "SUBMISSION_INCOMPLETE",
  REPRESENTATIVE_NOT_VERIFIED: "REPRESENTATIVE_NOT_VERIFIED",
  INVALID_ACCOUNT_FORMAT: "INVALID_ACCOUNT_FORMAT",
  DESTINATION_EXISTS: "DESTINATION_EXISTS",
  FILE_TOO_LARGE: "FILE_TOO_LARGE",
  PAYLOAD_TOO_LARGE: "PAYLOAD_TOO_LARGE",
  UNSUPPORTED_FILE_TYPE: "UNSUPPORTED_FILE_TYPE",
  FILE_TYPE_MISMATCH: "FILE_TYPE_MISMATCH",
  EMPTY_FILE: "EMPTY_FILE",
  DOCUMENT_NOT_AVAILABLE: "DOCUMENT_NOT_AVAILABLE",
  TICKET_EXPIRED: "TICKET_EXPIRED",
  TICKET_INVALID: "TICKET_INVALID",
  SELF_DECISION_FORBIDDEN: "SELF_DECISION_FORBIDDEN",
  NOT_ASSIGNED: "NOT_ASSIGNED",
  CASE_STATE_CHANGED: "CASE_STATE_CHANGED",
  VERSION_CONFLICT: "VERSION_CONFLICT",
  PRECONDITION_FAILED: "PRECONDITION_FAILED",
  PERMISSION_DENIED: "PERMISSION_DENIED",
  RESOURCE_NOT_FOUND: "RESOURCE_NOT_FOUND",
  ORGANISATION_NOT_FOUND: "ORGANISATION_NOT_FOUND",
  ORGANISATION_LIMIT_REACHED: "ORGANISATION_LIMIT_REACHED",
  SECOND_APPROVER_REQUIRED: "SECOND_APPROVER_REQUIRED",
  // Codes emitted by the Stage 5 backend streams (internal/kyc, verification, storage, payouts, compliance).
  DOCUMENT_TICKET_EXPIRED: "DOCUMENT_TICKET_EXPIRED",
  DOCUMENT_TICKET_INVALID: "DOCUMENT_TICKET_INVALID",
  CASE_NOT_FOUND: "CASE_NOT_FOUND",
  DOCUMENT_NOT_FOUND: "DOCUMENT_NOT_FOUND",
  DESTINATION_NOT_FOUND: "DESTINATION_NOT_FOUND",
  BENEFICIARY_NOT_FOUND: "BENEFICIARY_NOT_FOUND",
  PERSON_NOT_FOUND: "PERSON_NOT_FOUND",
  ACTION_NOT_ALLOWED: "ACTION_NOT_ALLOWED",
  ALREADY_ASSIGNED: "ALREADY_ASSIGNED",
  ASSIGNEE_NOT_ELIGIBLE: "ASSIGNEE_NOT_ELIGIBLE",
  NO_PENDING_APPROVAL: "NO_PENDING_APPROVAL",
  SECOND_APPROVER_MUST_DIFFER: "SECOND_APPROVER_MUST_DIFFER",
  SELF_APPROVAL_FORBIDDEN: "SELF_APPROVAL_FORBIDDEN",
  SUBJECT_CHECK_UNAVAILABLE: "SUBJECT_CHECK_UNAVAILABLE",
  STORAGE_UNAVAILABLE: "STORAGE_UNAVAILABLE",
  MALFORMED_UPLOAD: "MALFORMED_UPLOAD",
  MALFORMED_REQUEST: "MALFORMED_REQUEST",
  OWNERSHIP_EVIDENCE_MISSING: "OWNERSHIP_EVIDENCE_MISSING",
  AUTHORITY_EVIDENCE_MISSING: "AUTHORITY_EVIDENCE_MISSING",
  NAME_MATCH_REQUIRED: "NAME_MATCH_REQUIRED",
  DUPLICATE_IDENTITY_ACTIVE: "DUPLICATE_IDENTITY_ACTIVE",
  NO_IDENTITY_SUBMITTED: "NO_IDENTITY_SUBMITTED",
  RESOLUTION_NOT_APPROVED: "RESOLUTION_NOT_APPROVED",
  REPRESENTATIVE_NO_LONGER_AUTHORISED: "REPRESENTATIVE_NO_LONGER_AUTHORISED",
  MINOR_MUST_USE_MINOR_TYPE: "MINOR_MUST_USE_MINOR_TYPE",
  MINOR_REQUIRES_GUARDIAN_AUTHORITY: "MINOR_REQUIRES_GUARDIAN_AUTHORITY",
} as const;

/** Messages for `SUBMISSION_INCOMPLETE` detail codes (contract §7.2). */
export const SUBMISSION_DETAIL_MESSAGES: Record<string, string> = {
  MISSING_FIELD: "This is required before you can submit.",
  DOCUMENT_REQUIRED: "Upload this document before you submit.",
  DOCUMENT_NOT_CLEAN: "This document has not passed the security check yet. Wait for it to be ready, or replace it.",
  UNDERAGE: "You must be 18 or older to verify your identity. A parent or guardian can raise funds for a child instead.",
  DOCUMENT_EXPIRED: "This document has expired. Use a document that is still valid.",
  INVALID_FORMAT: "This value is not in the right format.",
  INVALID_VALUE: "This value is not valid.",
  AUTHORITY_EVIDENCE_MISSING: "Upload evidence of your authority to raise funds for this beneficiary.",
  OWNERSHIP_EVIDENCE_MISSING: "Upload evidence that you own this account.",
  NAME_MATCH_REQUIRED: "The name must match the one on the evidence.",
  NO_IDENTITY_SUBMITTED: "Add your personal details first.",
  MINOR_MUST_USE_MINOR_TYPE: "This person is under 18: choose \u201cA child (under 18)\u201d as the beneficiary type.",
  MINOR_REQUIRES_GUARDIAN_AUTHORITY: "For a child, the authority must be parental responsibility or a guardianship order.",
  NOT_A_MINOR: "This date of birth is not under 18. Choose a different beneficiary type.",
};

const FIELD_LABELS: Record<string, string> = {
  legal_first_name: "First name(s)",
  legal_last_name: "Surname",
  date_of_birth: "Date of birth",
  nationality: "Nationality",
  country_of_residence: "Country you live in",
  id_document_type: "ID document type",
  id_document_number: "ID document number",
  id_document_expiry: "ID expiry date",
  residential_address: "Home address",
  "residential_address.line1": "Address line 1",
  "residential_address.city": "Town or city",
  "residential_address.country": "Address country",
  registered_name: "Registered name",
  registration_number: "Registration number",
  registry: "Registry",
  country_of_registration: "Country of registration",
  registered_address: "Registered address",
  persons: "Directors, trustees and owners",
  relationship: "Relationship",
  authority_basis: "Authority",
  documents: "Documents",
};

/** Human label for a detail `field`, which may name a form field or a required document (`documents.<TYPE>[.<SIDE>]`). */
export function detailFieldLabel(field: string): string {
  if (FIELD_LABELS[field]) return FIELD_LABELS[field];
  const doc = /^documents?\.([A-Z0-9_|]+)(?:\.([A-Z_]+))?$/.exec(field);
  if (doc) {
    // `documents.A|B|C` (backend: one of several alternatives) or `documents.TYPE.SIDE`.
    const types = doc[1]!.split("|").filter(Boolean).map(documentTypeLabel);
    const joined = types.length > 1 ? `${types.slice(0, -1).join(", ")} or ${types[types.length - 1]}` : types[0]!;
    return `${joined}${doc[2] ? ` (${humanise(doc[2]).toLowerCase()})` : ""}`;
  }
  return humanise(field.split(".").pop() ?? field);
}

/** INVALID_LENGTH on reviewer free-text fields (decision note 3–5000 characters, etc.). */
const LENGTH_MESSAGES: Record<string, string> = {
  note: "Write an internal note of at least 3 characters (at most 5,000).",
  user_message: "Write a message to the person of at least 10 characters.",
  message: "Write the request in at least 10 characters.",
  justification: "Give a justification of at least 10 characters.",
};

export interface SubmissionProblem {
  field: string;
  label: string;
  message: string;
}

export interface DescribedVerificationError extends DescribedError {
  /** SUBMISSION_INCOMPLETE: every problem, in API order, for an error summary. */
  problems: SubmissionProblem[];
}

export function describeVerificationError(error: unknown, fallback = "Something went wrong. Please try again."): DescribedVerificationError {
  const base = describeError(error, fallback);
  const out: DescribedVerificationError = { ...base, problems: [] };
  if (!isApiError(error)) return out;
  const set = (message: string) => ({ ...out, message });
  switch (error.code) {
    case VerificationErrorCode.CASE_ALREADY_OPEN:
      return set("You already have a verification in progress. Reload the page to continue with it.");
    case VerificationErrorCode.PREREQUISITES_NOT_MET:
      return set("Before verifying your identity, confirm your email address and your phone number in your security settings.");
    case VerificationErrorCode.CASE_NOT_EDITABLE:
      return set("This can no longer be changed because it has been submitted or decided. Reload the page to see its current status.");
    case VerificationErrorCode.SUBMISSION_INCOMPLETE: {
      for (const detail of error.details) {
        const message = SUBMISSION_DETAIL_MESSAGES[detail.code] ?? "This needs attention before you can submit.";
        const field = detail.field || "form";
        out.problems.push({ field, label: detailFieldLabel(field), message });
        out.fieldErrors[field] ??= message;
      }
      return set(out.problems.length > 0 ? "Some things need attention before you can submit." : "Some information is missing. Check your details and documents.");
    }
    case VerificationErrorCode.REPRESENTATIVE_NOT_VERIFIED:
      return set("You need to verify your own identity before you can submit this organisation for verification.");
    case VerificationErrorCode.INVALID_ACCOUNT_FORMAT:
      out.fieldErrors.account_identifier ??= "This account number or phone number is not in a valid format for the selected provider.";
      return set("Please correct the errors below.");
    case VerificationErrorCode.DESTINATION_EXISTS:
      return set("You have already added this account. Use the existing entry instead.");
    case VerificationErrorCode.FILE_TOO_LARGE:
    case VerificationErrorCode.PAYLOAD_TOO_LARGE:
      return set("This file is too large. Upload a file of 10 MB or less — a photo at a lower resolution, or a PDF.");
    case VerificationErrorCode.UNSUPPORTED_FILE_TYPE:
      return set("This type of file can't be used. Upload a JPEG or PNG photo, or a PDF.");
    case VerificationErrorCode.FILE_TYPE_MISMATCH:
      return set("The file's contents don't match its type (for example, a renamed file). Upload the original JPEG, PNG or PDF.");
    case VerificationErrorCode.EMPTY_FILE:
      return set("This file is empty. Choose a different file.");
    case VerificationErrorCode.DOCUMENT_NOT_AVAILABLE:
      return set("This document can't be opened yet: it has not passed the security check.");
    case VerificationErrorCode.TICKET_EXPIRED:
    case VerificationErrorCode.TICKET_INVALID:
    case VerificationErrorCode.DOCUMENT_TICKET_EXPIRED:
    case VerificationErrorCode.DOCUMENT_TICKET_INVALID:
      return set("The link to view this document has expired. Select “View” again.");
    case VerificationErrorCode.SELF_DECISION_FORBIDDEN:
      return set("You can't decide on a verification that involves you or your organisation. Ask another reviewer.");
    case VerificationErrorCode.NOT_ASSIGNED:
      return set("Only the assigned reviewer can do this. Assign the case to yourself first.");
    case VerificationErrorCode.CASE_STATE_CHANGED:
      return set("This case was changed by someone else while you were working on it. Reload to see its current state.");
    case VerificationErrorCode.VERSION_CONFLICT:
    case VerificationErrorCode.PRECONDITION_FAILED:
      return set("This was changed somewhere else (perhaps in another tab). Reload the page and try again.");
    case "VALIDATION_FAILED": {
      for (const detail of error.details) {
        const text = LENGTH_MESSAGES[detail.field];
        if (detail.code === "INVALID_LENGTH" && text) out.fieldErrors[detail.field] = text;
      }
      return set("Please check the highlighted fields.");
    }
    case VerificationErrorCode.SECOND_APPROVER_REQUIRED:
    case VerificationErrorCode.SECOND_APPROVER_MUST_DIFFER:
      return set("A second approval must come from a different reviewer.");
    case VerificationErrorCode.SELF_APPROVAL_FORBIDDEN:
      return set("A resolution must be approved by someone other than the person who proposed it.");
    case VerificationErrorCode.ACTION_NOT_ALLOWED:
      return set("This action isn't available for this case in its current state. Reload to see what you can do.");
    case VerificationErrorCode.ALREADY_ASSIGNED:
      return set("This case is already assigned to someone else. Reload to see who.");
    case VerificationErrorCode.ASSIGNEE_NOT_ELIGIBLE:
      return set("That person can't review verification cases.");
    case VerificationErrorCode.NO_PENDING_APPROVAL:
      return set("There is no first approval waiting on this case. Reload to see its current state.");
    case VerificationErrorCode.SUBJECT_CHECK_UNAVAILABLE:
      return set("A check needed for this decision is temporarily unavailable. Try again shortly.");
    case VerificationErrorCode.STORAGE_UNAVAILABLE:
      return set("File storage is temporarily unavailable. Please try again shortly.");
    case VerificationErrorCode.MALFORMED_UPLOAD:
      return set("The upload arrived incomplete. Choose the file again and retry.");
    case VerificationErrorCode.MALFORMED_REQUEST:
      return set("The request could not be processed. Reload the page and try again.");
    case VerificationErrorCode.OWNERSHIP_EVIDENCE_MISSING:
      return set("Upload evidence that you own this account (and select it) before requesting verification.");
    case VerificationErrorCode.AUTHORITY_EVIDENCE_MISSING:
      return set("Upload evidence of your authority to raise funds for this beneficiary, then submit again.");
    case VerificationErrorCode.NAME_MATCH_REQUIRED:
      return set("Confirm that the account holder name matches the evidence before approving.");
    case VerificationErrorCode.DUPLICATE_IDENTITY_ACTIVE:
      return set("We can't accept these identity details for this account. Please contact us.");
    case VerificationErrorCode.NO_IDENTITY_SUBMITTED:
      return set("Add your personal details before you submit.");
    case VerificationErrorCode.RESOLUTION_NOT_APPROVED:
      return set("The resolution needs a second officer's approval before the case can be closed.");
    case VerificationErrorCode.REPRESENTATIVE_NO_LONGER_AUTHORISED:
      return set("You are no longer an administrator of this organisation, so you can't act on its verification.");
    case VerificationErrorCode.MINOR_MUST_USE_MINOR_TYPE:
    case VerificationErrorCode.MINOR_REQUIRES_GUARDIAN_AUTHORITY:
      return set(SUBMISSION_DETAIL_MESSAGES[error.code]!);
    case VerificationErrorCode.PERMISSION_DENIED:
      return set("You don't have permission to do this.");
    case VerificationErrorCode.RESOURCE_NOT_FOUND:
    case VerificationErrorCode.ORGANISATION_NOT_FOUND:
    case VerificationErrorCode.CASE_NOT_FOUND:
    case VerificationErrorCode.DOCUMENT_NOT_FOUND:
    case VerificationErrorCode.DESTINATION_NOT_FOUND:
    case VerificationErrorCode.BENEFICIARY_NOT_FOUND:
    case VerificationErrorCode.PERSON_NOT_FOUND:
      return set("We couldn't find this. It may have been removed. Reload the page.");
    case VerificationErrorCode.ORGANISATION_LIMIT_REACHED:
      return set("You have reached the maximum number of organisations you can create.");
    case "EMAIL_NOT_VERIFIED":
      return set("Please confirm your email address first.");
    case "SERVICE_UNAVAILABLE":
      return set("This service is temporarily unavailable. Please try again shortly.");
    default:
      return out;
  }
}
