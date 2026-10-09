import { isApiError } from "@/lib/api/errors";
import { describeVerificationError, type DescribedVerificationError } from "@/lib/verification/errors";

import { reasonsFromDetails } from "./eligibility";
import type { EligibilityReason } from "./types";

/**
 * Maps Stage 6 campaign error codes (contract §6, §9) to user-facing copy. Branches on codes only and never
 * echoes server messages. `NOT_ELIGIBLE` carries structured reasons in `details`, returned as `reasons`
 * for the eligibility checklist. Codes not known here fall through to the Stage 5/4 mapping.
 */
export interface DescribedCampaignError extends DescribedVerificationError {
  reasons: EligibilityReason[];
}

const CONTENT_FIELD_MESSAGES: Record<string, string> = {
  HTML_NOT_ALLOWED: "Use plain text only: HTML tags such as <b> or <script> are not allowed.",
  UNSAFE_LINK: "Remove links that start with javascript:, data: or vbscript:.",
  INVALID_LENGTH: "This is too short or too long.",
  TOO_SHORT: "This is too short.",
  TOO_LONG: "This is too long.",
  REQUIRED: "This is required.",
  INVALID_FORMAT: "This is not in the right format.",
  INVALID_VALUE: "This value is not valid.",
  CONTROL_CHARACTERS: "Remove unusual invisible characters (such as zero-width or direction-override characters).",
  INVALID_ENCODING: "This text contains characters that can't be used.",
  CURRENCY_NOT_AVAILABLE: "This currency cannot be used for campaigns yet.",
  UNAVAILABLE: "This currency cannot be used for campaigns yet.",
  GOAL_OUT_OF_RANGE: "Choose a goal within the allowed range.",
  MISSING_FIELD: "This is required.",
  CONSENT_REQUIRED: "Confirm the beneficiary's consent to show their name.",
  BENEFICIARY_NOT_AUTHORISED: "This beneficiary does not belong to the campaign owner.",
  MINOR_DISCLOSURE_NOT_ALLOWED: "A child's details can't be shown publicly. Keep their details private.",
  INVALID_EMAIL: "Enter an email address in the format name@example.com.",
};

export function describeCampaignError(error: unknown, fallback = "Something went wrong. Please try again."): DescribedCampaignError {
  const base = describeVerificationError(error, fallback);
  const out: DescribedCampaignError = { ...base, reasons: [] };
  if (!isApiError(error)) return out;
  const set = (message: string) => ({ ...out, message });
  switch (error.code) {
    case "NOT_ELIGIBLE":
      out.reasons = reasonsFromDetails(error.details as Array<{ code?: unknown; field?: unknown; message?: unknown }>);
      return set("Some things need to be done first. See the list below.");
    case "INVALID_STATUS":
      return set("This isn't possible in the campaign's current status. Reload the page to see its latest state.");
    case "CAMPAIGN_NOT_FOUND":
    case "MEDIA_NOT_FOUND":
    case "UPDATE_NOT_FOUND":
      return set("We couldn't find this. It may have been removed. Reload the page.");
    case "HTML_NOT_ALLOWED":
      return set(CONTENT_FIELD_MESSAGES.HTML_NOT_ALLOWED!);
    case "UNSAFE_LINK":
      return set(CONTENT_FIELD_MESSAGES.UNSAFE_LINK!);
    case "CURRENCY_NOT_AVAILABLE":
      out.fieldErrors.currency ??= "This currency cannot be used for campaigns yet.";
      return set("This currency cannot be used for campaigns yet. Choose an available currency.");
    case "GOAL_OUT_OF_RANGE":
      out.fieldErrors.amount ??= "Choose a goal within the allowed range.";
      return set("The goal is outside the allowed range.");
    case "IF_MATCH_REQUIRED":
    case "CAMPAIGN_STATE_CHANGED":
    case "MEDIA_STATE_CHANGED":
    case "PRECONDITION_REQUIRED":
    case "PRECONDITION_FAILED":
    case "VERSION_CONFLICT":
      return set("This campaign was changed somewhere else (perhaps in another tab). Reload the page; your unsaved text is still in the form.");
    case "SELF_DECISION_FORBIDDEN":
      return set("You can't decide on a campaign that involves you, your organisation or your linked personal account. Ask another reviewer.");
    case "SECOND_APPROVAL_REQUIRED":
    case "SECOND_APPROVER_MUST_DIFFER":
      return set("A second approval must come from a different reviewer.");
    case "NOT_ASSIGNED":
      return set("Only the assigned reviewer can do this. Assign the review to yourself first.");
    case "ACCOUNT_RESTRICTED":
      return set("This action is not available on your account. Contact us if you need help.");
    case "COVER_ALREADY_EXISTS":
      return set("This campaign already has a cover photo. Remove the current cover first, then upload the new one.");
    case "MINOR_MEDIA_NOT_SUPPORTED":
      return set("Photos that show a child can't be added to a campaign for now. Choose a photo without children.");
    case "MEDIA_NOT_AVAILABLE":
      return set("This photo isn't available. It may still be being checked, or it was removed.");
    case "NO_OPEN_REVIEW":
      return set("There is no open review for this campaign. Reload to see its current state.");
    case "SECOND_APPROVAL_PENDING":
      return set("This review is waiting for a second approval from a different reviewer.");
    case "REVIEW_NOT_STARTED":
      return set("Start the review first.");
    case "NO_PENDING_APPROVAL":
      return set("There is no first approval waiting. Reload to see the review's current state.");
    case "ASSIGNEE_NOT_ELIGIBLE":
      return set("That person can't review campaigns.");
    case "STAFF_ALREADY_LINKED":
    case "PERSONAL_ACCOUNT_ALREADY_LINKED":
      return set("This account is already linked. Links can't be changed here.");
    case "STAFF_LINK_EMAIL_MISMATCH":
      return set("This link was sent for a different account. Sign in with the personal account whose email received the link.");
    case "STATEMENT_VERSION_NOT_CURRENT":
      return set("The age statement was updated. Reload the page and confirm again.");
    case "AGE_ATTESTATION_UNAVAILABLE":
    case "POLICY_UNAVAILABLE":
      return set("This is temporarily unavailable. Please try again shortly.");
    case "GALLERY_LIMIT_REACHED":
    case "MEDIA_LIMIT_REACHED":
      return set("You have reached the maximum number of photos for this campaign. Remove one first.");
    case "UNSUPPORTED_MEDIA_TYPE":
    case "UNSUPPORTED_FILE_TYPE":
      return set("This type of file can't be used. Upload a JPEG or PNG photo.");
    case "FILE_TOO_LARGE":
    case "PAYLOAD_TOO_LARGE":
      return set("This photo is too large. Upload a photo of 10 MB or less.");
    case "FILE_TYPE_MISMATCH":
    case "IMAGE_INVALID":
      return set("This file isn't a usable JPEG or PNG photo. Choose a different photo.");
    case "TOKEN_INVALID":
      return set("This link is invalid, has expired, or was sent to a different account.");
    case "STAFF_LINK_EXISTS":
      return set("Your staff account is already linked to a personal account. Links cannot be removed here.");
    case "VALIDATION_FAILED": {
      for (const detail of error.details) {
        if (!detail.field) continue;
        const text = CONTENT_FIELD_MESSAGES[detail.code];
        if (text) out.fieldErrors[detail.field] = text;
      }
      return Object.keys(out.fieldErrors).length > 0 ? set("Please check the highlighted fields.") : out;
    }
    default:
      return out;
  }
}
