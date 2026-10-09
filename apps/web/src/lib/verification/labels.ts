/**
 * Display mapping for verification states (ADR-034) and the pure "what happens next" logic. The API owns
 * every decision; these functions only translate codes into words and decide which controls to show. An
 * unknown code is shown humanised rather than hidden, so a new backend state is never silently swallowed.
 */
import type { BadgeTone } from "@/components/ui/badge";

import {
  EDITABLE_CASE_STATUSES,
  FINAL_CASE_STATUSES,
  type ComplianceAction,
  type DocumentRequirement,
  type KycCase,
  type KycIdentity,
  type ReviewAction,
  type ReviewCaseType,
} from "./types";

export function humanise(code: string | null | undefined): string {
  if (!code) return "—";
  const words = code.toLowerCase().replace(/[_.]+/g, " ").trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

interface Label {
  label: string;
  tone: BadgeTone;
}

const CASE_STATUS: Record<string, Label> = {
  NOT_STARTED: { label: "Not started", tone: "neutral" },
  DRAFT: { label: "Draft — not submitted", tone: "neutral" },
  SUBMITTED: { label: "Submitted — waiting for review", tone: "info" },
  UNDER_REVIEW: { label: "Under review", tone: "info" },
  ADDITIONAL_INFORMATION_REQUIRED: { label: "More information needed", tone: "gold" },
  ESCALATED: { label: "Under additional review", tone: "info" },
  APPROVED: { label: "Verified", tone: "brand" },
  REJECTED: { label: "Not approved", tone: "gold" },
  EXPIRED: { label: "Expired", tone: "gold" },
  SUSPENDED: { label: "Suspended", tone: "gold" },
  REVOKED: { label: "Revoked", tone: "gold" },
  WITHDRAWN: { label: "Withdrawn", tone: "neutral" },
  AWAITING_SECOND_APPROVAL: { label: "Awaiting second approval", tone: "info" },
};

/** Reviewer view: ESCALATED is shown as such (users see "Under additional review"). */
const REVIEWER_CASE_STATUS: Record<string, Label> = {
  ...CASE_STATUS,
  ESCALATED: { label: "Escalated to compliance", tone: "gold" },
  APPROVED: { label: "Approved", tone: "brand" },
  REJECTED: { label: "Rejected", tone: "gold" },
  DRAFT: { label: "Draft", tone: "neutral" },
  SUBMITTED: { label: "Submitted", tone: "info" },
};

export function caseStatusLabel(status: string | null | undefined, audience: "user" | "reviewer" = "user"): Label {
  const key = status ?? "NOT_STARTED";
  const table = audience === "reviewer" ? REVIEWER_CASE_STATUS : CASE_STATUS;
  return table[key] ?? DESTINATION_STATUS[key] ?? { label: humanise(key), tone: "neutral" };
}

const SCAN_STATUS: Record<string, Label & { description: string }> = {
  UPLOADED: { label: "Uploading", tone: "neutral", description: "The file is still being received." },
  QUARANTINED: { label: "Waiting for security check", tone: "info", description: "The file is held securely until it has been checked." },
  SCANNING: { label: "Security check in progress", tone: "info", description: "The file is being checked. This usually takes a minute." },
  CLEAN: { label: "Ready", tone: "brand", description: "The file passed the security check." },
  REJECTED: { label: "Rejected", tone: "gold", description: "The file failed the security check and cannot be used. Delete it and upload a different file." },
  FAILED_SCAN: { label: "Check delayed", tone: "gold", description: "The security check could not run yet. It will be retried automatically." },
  DELETED: { label: "Deleted", tone: "neutral", description: "The file was deleted." },
};

export function scanStatusLabel(status: string): Label & { description: string } {
  return SCAN_STATUS[status] ?? { label: humanise(status), tone: "neutral", description: "" };
}

const DESTINATION_STATUS: Record<string, Label> = {
  UNVERIFIED: { label: "Not verified", tone: "neutral" },
  PENDING_VERIFICATION: { label: "Verification pending", tone: "info" },
  VERIFIED: { label: "Verified", tone: "brand" },
  RETIRED: { label: "Removed", tone: "neutral" },
};

export function destinationStatusLabel(status: string): Label {
  return DESTINATION_STATUS[status] ?? CASE_STATUS[status] ?? { label: humanise(status), tone: "neutral" };
}

const OWNERSHIP: Record<string, Label & { description: string }> = {
  NOT_STARTED: { label: "Not started", tone: "neutral", description: "Ownership of this account has not been checked yet." },
  PENDING: { label: "Pending", tone: "info", description: "Your ownership evidence is waiting for review." },
  PROVIDER_CONFIRMATION_REQUIRED: {
    label: "Provider confirmation required",
    tone: "gold",
    description:
      "An automatic ownership check with this provider is not available yet. Ownership cannot be confirmed until the provider can confirm it or you send other evidence, such as a bank letter or statement.",
  },
  CONFIRMED: { label: "Confirmed", tone: "brand", description: "Ownership of this account has been confirmed." },
  FAILED: { label: "Not confirmed", tone: "gold", description: "Ownership could not be confirmed from the evidence provided." },
};

export function ownershipLabel(code: string): Label & { description: string } {
  return OWNERSHIP[code] ?? { label: humanise(code), tone: "neutral", description: "" };
}

const COMPLIANCE_CHECK: Record<string, Label> = {
  PENDING: { label: "Pending", tone: "info" },
  APPROVED: { label: "Cleared", tone: "brand" },
  REJECTED: { label: "Not cleared", tone: "gold" },
};

export function complianceCheckLabel(code: string): Label {
  return COMPLIANCE_CHECK[code] ?? { label: humanise(code), tone: "neutral" };
}

const COMPLIANCE_STATUS: Record<string, Label> = {
  OPEN: { label: "Open", tone: "info" },
  ASSIGNED: { label: "Assigned", tone: "info" },
  IN_REVIEW: { label: "In review", tone: "info" },
  AWAITING_INFORMATION: { label: "Awaiting information", tone: "gold" },
  ESCALATED: { label: "Escalated", tone: "gold" },
  RESOLVED: { label: "Resolved", tone: "brand" },
  CLOSED: { label: "Closed", tone: "neutral" },
};

export function complianceStatusLabel(status: string): Label {
  return COMPLIANCE_STATUS[status] ?? { label: humanise(status), tone: "neutral" };
}

const LEVELS: Record<string, string> = {
  UNVERIFIED: "Not verified",
  BASIC_VERIFIED: "Basic (email and phone confirmed)",
  IDENTITY_VERIFIED: "Identity verified",
  PAYOUT_VERIFIED: "Payout verified",
  ORG_UNVERIFIED: "Not verified",
  ORG_BASIC: "Basic",
  ORG_VERIFIED: "Organisation verified",
};

export function levelLabel(level: string | null | undefined): string {
  if (!level) return "Not verified";
  return LEVELS[level] ?? humanise(level);
}

const PROFILE_STATUS: Record<string, string> = {
  ACTIVE: "Active",
  PENDING_REVIEW: "Review pending",
  REJECTED: "Not approved",
  SUSPENDED: "Suspended",
};

export function profileStatusLabel(status: string | null | undefined): string {
  if (!status) return "—";
  return PROFILE_STATUS[status] ?? humanise(status);
}

// ---- Document types (vocabulary not fixed by the contract: known codes get labels, others are humanised) ----

const DOCUMENT_TYPES: Record<string, string> = {
  ZW_NATIONAL_ID: "Zimbabwe national ID",
  NATIONAL_ID: "National ID",
  ZW_PASSPORT: "Zimbabwe passport",
  FOREIGN_PASSPORT: "Passport (other country)",
  PASSPORT: "Passport",
  ID_DOCUMENT: "Identity document",
  SELFIE: "Photo of you holding your ID",
  PROOF_OF_ADDRESS: "Proof of address",
  CERTIFICATE_OF_INCORPORATION: "Certificate of incorporation",
  REGISTRATION_CERTIFICATE: "Registration certificate",
  CONSTITUTION: "Constitution or founding document",
  TRUST_DEED: "Trust deed",
  CR14: "CR14 (register of directors)",
  BOARD_RESOLUTION: "Board resolution or authority letter",
  BIRTH_CERTIFICATE: "Birth certificate",
  GUARDIANSHIP_ORDER: "Guardianship order",
  CONSENT_LETTER: "Signed consent from the beneficiary",
  MEDICAL_LETTER: "Letter from a medical institution",
  INSTITUTION_LETTER: "Letter from the institution",
  POWER_OF_ATTORNEY: "Power of attorney",
  GROUP_MANDATE: "Group mandate or minutes",
  BANK_LETTER: "Bank confirmation letter",
  MOBILE_MONEY_STATEMENT: "Mobile money statement",
  BANK_STATEMENT: "Bank statement",
  OTHER: "Other supporting document",
};

export function documentTypeLabel(code: string): string {
  return DOCUMENT_TYPES[code] ?? humanise(code);
}

/** Side of a document; "NA" (the API's "no side") and empty give "". */
export function sideLabel(side: string | null | undefined): string {
  if (!side || side === "NA") return "";
  const map: Record<string, string> = { FRONT: "Front", BACK: "Back", SINGLE: "Single page", PHOTO_PAGE: "Photo page" };
  return map[side] ?? humanise(side);
}

/** Fallback document choices per subject when the API sends no `requirements`. */
export const DEFAULT_DOCUMENT_TYPES: Record<string, string[]> = {
  KYC_CASE: ["ZW_NATIONAL_ID", "ZW_PASSPORT", "FOREIGN_PASSPORT", "SELFIE", "PROOF_OF_ADDRESS"],
  KYB_CASE: ["CERTIFICATE_OF_INCORPORATION", "REGISTRATION_CERTIFICATE", "CONSTITUTION", "TRUST_DEED", "CR14", "BOARD_RESOLUTION", "PROOF_OF_ADDRESS", "OTHER"],
  KYB_PERSON: ["ZW_NATIONAL_ID", "ZW_PASSPORT", "FOREIGN_PASSPORT"],
  BENEFICIARY: ["BIRTH_CERTIFICATE", "GUARDIANSHIP_ORDER", "CONSENT_LETTER", "ID_DOCUMENT", "MEDICAL_LETTER", "INSTITUTION_LETTER", "POWER_OF_ATTORNEY", "GROUP_MANDATE", "OTHER"],
  PAYOUT_DESTINATION: ["BANK_LETTER", "MOBILE_MONEY_STATEMENT", "BANK_STATEMENT"],
};

/** Accepts either requirement shape (see DocumentRequirement) and returns complete objects. */
export function normaliseRequirements(raw: unknown): DocumentRequirement[] {
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((r): r is Record<string, unknown> => typeof r === "object" && r !== null)
    .map((r) => {
      const types = Array.isArray(r.document_types)
        ? r.document_types.filter((t): t is string => typeof t === "string" && t !== "")
        : typeof r.document_type === "string"
          ? [r.document_type]
          : [];
      const sides = Array.isArray(r.sides) ? r.sides.filter((s): s is string => typeof s === "string" && s !== "" && s !== "NA") : [];
      return { document_type: types[0] ?? "OTHER", document_types: types.length ? types : ["OTHER"], sides, satisfied: r.satisfied === true };
    });
}

/** "Zimbabwe national ID, Zimbabwe passport or Passport from another country (front and back)". */
export function requirementLabel(req: DocumentRequirement): string {
  const types = (req.document_types?.length ? req.document_types : [req.document_type]).map(documentTypeLabel);
  const joined = types.length > 1 ? `${types.slice(0, -1).join(", ")} or ${types[types.length - 1]}` : types[0] ?? "";
  return `${joined}${req.sides.length > 0 ? ` (${req.sides.map((s) => sideLabel(s).toLowerCase()).join(" and ")})` : ""}`;
}

/** Document-type options: requirements first (in API order), then the subject's defaults, de-duplicated. */
export function documentTypeOptions(subjectType: string, requirements: DocumentRequirement[] = []): string[] {
  const out: string[] = [];
  for (const code of [...normaliseRequirements(requirements).flatMap((r) => r.document_types ?? [r.document_type]), ...(DEFAULT_DOCUMENT_TYPES[subjectType] ?? ["OTHER"])]) {
    if (!out.includes(code)) out.push(code);
  }
  return out;
}

/** Sides for a document type: from the requirement when present; ID cards default to front/back. */
export function sidesFor(documentType: string, requirements: DocumentRequirement[] = []): string[] {
  const req = normaliseRequirements(requirements).find((r) => (r.document_types ?? [r.document_type]).includes(documentType));
  if (req && req.sides.length > 0) return req.sides;
  if (documentType === "ZW_NATIONAL_ID" || documentType === "NATIONAL_ID") return ["FRONT", "BACK"];
  return [];
}

// ---- Labels for enumerations ----

export const ID_DOCUMENT_TYPE_LABELS: Record<string, string> = {
  ZW_NATIONAL_ID: "Zimbabwe national ID",
  ZW_PASSPORT: "Zimbabwe passport",
  FOREIGN_PASSPORT: "Passport from another country",
};

export const BENEFICIARY_TYPE_LABELS: Record<string, string> = {
  SELF: "Myself",
  INDIVIDUAL: "Another adult",
  MINOR: "A child (under 18)",
  INCAPACITATED_ADULT: "An adult who cannot act for themselves",
  ORGANISATION: "A registered organisation",
  INSTITUTION: "An institution (school, hospital…)",
  COMMUNITY_GROUP: "A community group",
};

export const RELATIONSHIP_LABELS: Record<string, string> = {
  SELF: "It is me",
  PARENT_GUARDIAN: "Parent or guardian",
  FAMILY_MEMBER: "Family member",
  AUTHORIZED_REPRESENTATIVE: "Authorised representative",
  ORGANISATION_REPRESENTATIVE: "Representative of the organisation",
  THIRD_PARTY_ORGANISER: "Organiser on their behalf",
  OTHER: "Other",
};

export const AUTHORITY_BASIS_LABELS: Record<string, string> = {
  NOT_REQUIRED: "Not required (I am the beneficiary)",
  BENEFICIARY_CONSENT: "The beneficiary has consented",
  PARENTAL_RESPONSIBILITY: "Parental responsibility",
  GUARDIANSHIP_ORDER: "Guardianship order",
  LEGAL_REPRESENTATION: "Legal representation (e.g. power of attorney)",
  ORGANISATION_AUTHORITY: "Authority from the organisation",
  GROUP_MANDATE: "Mandate from the group",
  INSTITUTION_CONFIRMATION: "Confirmation from the institution",
};

export const RAIL_LABELS: Record<string, string> = {
  ECOCASH: "EcoCash",
  ONEMONEY: "OneMoney",
  INNBUCKS: "InnBucks",
  OMARI: "O'mari",
  BANK_TRANSFER: "Bank transfer",
  ZIMSWITCH: "ZimSwitch",
};

export const CURRENCY_LABELS: Record<string, string> = { USD: "US dollars (USD)", ZWG: "ZiG (ZWG)" };

export const PERSON_ROLE_LABELS: Record<string, string> = {
  DIRECTOR: "Director",
  TRUSTEE: "Trustee",
  OFFICE_BEARER: "Office bearer",
  BENEFICIAL_OWNER: "Beneficial owner",
  CONTROLLER: "Controller",
  REPRESENTATIVE: "Representative",
};

export const METHOD_LABELS: Record<string, string> = {
  BANK_LETTER: "Bank confirmation letter",
  MOBILE_MONEY_STATEMENT: "Mobile money statement",
  PROVIDER_LOOKUP: "Automatic check with the provider",
};

/** Beneficiary types that need evidence of authority beyond the owner's own identity. */
export function beneficiaryNeedsExtraEvidence(type: string): boolean {
  return type === "MINOR" || type === "INCAPACITATED_ADULT";
}

// ---- KYC progress and next steps ----

export const REQUIRED_IDENTITY_FIELDS = [
  "legal_first_name",
  "legal_last_name",
  "date_of_birth",
  "nationality",
  "country_of_residence",
  "id_document_type",
  "id_document_number_masked",
  "residential_address",
] as const satisfies ReadonlyArray<keyof KycIdentity>;

export function identityComplete(identity: KycIdentity | null | undefined): boolean {
  if (!identity) return false;
  return REQUIRED_IDENTITY_FIELDS.every((field) => {
    const value = identity[field];
    return value !== null && value !== undefined && value !== "";
  });
}

export function requirementsSatisfied(requirements: DocumentRequirement[] | undefined): boolean {
  return (requirements ?? []).every((r) => r.satisfied);
}

export type StepState = "done" | "current" | "todo";

export interface ProgressStep {
  key: "details" | "documents" | "submit" | "review" | "decision";
  label: string;
  state: StepState;
}

/** Progress through a KYC case, for the overview. */
export function kycProgress(kycCase: KycCase | null): ProgressStep[] {
  const status = kycCase?.status;
  const details = identityComplete(kycCase?.identity);
  const documents = !!kycCase && kycCase.requirements.length > 0 && requirementsSatisfied(kycCase.requirements);
  const submitted = !!kycCase && !!status && status !== "DRAFT" && status !== "WITHDRAWN";
  const decided = !!kycCase?.decision || (!!status && ["APPROVED", "REJECTED", "SUSPENDED", "REVOKED", "EXPIRED"].includes(status));
  const raw: Array<[ProgressStep["key"], string, boolean]> = [
    ["details", "Personal details", details],
    ["documents", "Identity documents", documents],
    ["submit", "Submit for review", submitted],
    ["review", "Review by our team", decided],
    ["decision", "Decision", decided],
  ];
  let currentAssigned = false;
  return raw.map(([key, label, done]) => {
    if (done) return { key, label, state: "done" as const };
    if (!currentAssigned) {
      currentAssigned = true;
      return { key, label, state: "current" as const };
    }
    return { key, label, state: "todo" as const };
  });
}

export interface NextStep {
  title: string;
  description: string;
  href?: string;
  action?: string;
}

/** What the user should do next, from the KYC status response. */
export function kycNextStep(kycCase: KycCase | null, level: string | null | undefined): NextStep {
  const status = kycCase?.status;
  if (!kycCase || FINAL_CASE_STATUSES.has(status ?? "")) {
    if (level === "IDENTITY_VERIFIED" || level === "PAYOUT_VERIFIED") {
      return { title: "Your identity is verified", description: "There is nothing you need to do right now." };
    }
    const again = kycCase && FINAL_CASE_STATUSES.has(kycCase.status);
    return {
      title: again ? "Start a new verification" : "Verify your identity",
      description: again
        ? "Your previous verification is closed. You can start a new one with up-to-date details and documents."
        : "To raise funds you will need to confirm who you are. It takes about ten minutes; have your ID ready.",
      href: "/dashboard/verification/identity",
      action: again ? "Start a new verification" : "Start verification",
    };
  }
  switch (status) {
    case "DRAFT": {
      if (!identityComplete(kycCase.identity)) {
        return { title: "Add your personal details", description: "Enter your details exactly as they appear on your ID.", href: "/dashboard/verification/identity", action: "Continue with your details" };
      }
      if (!requirementsSatisfied(kycCase.requirements)) {
        return { title: "Upload your documents", description: "Upload clear photos or scans of the documents listed.", href: "/dashboard/verification/documents", action: "Upload documents" };
      }
      return { title: "Check and submit", description: "Review your details and submit them for verification.", href: "/dashboard/verification/identity#review", action: "Review and submit" };
    }
    case "ADDITIONAL_INFORMATION_REQUIRED":
      return { title: "We need more information", description: "Our team has asked for more information. Read the request, update your details or documents, and submit again.", href: "/dashboard/verification/identity#requests", action: "See what is needed" };
    case "SUBMITTED":
    case "UNDER_REVIEW":
    case "ESCALATED":
      return { title: "We are reviewing your details", description: "You don't need to do anything. We will email you when there is a decision or if we need more information." };
    case "APPROVED":
      return { title: "Your identity is verified", description: "There is nothing you need to do right now." };
    case "SUSPENDED":
      return { title: "Your verification is suspended", description: "Please contact us. Some features are unavailable until this is resolved." };
    default:
      return { title: caseStatusLabel(status).label, description: "" };
  }
}

export function isEditable(status: string | null | undefined): boolean {
  return !!status && EDITABLE_CASE_STATUSES.has(status);
}

export function canWithdraw(status: string | null | undefined): boolean {
  return status === "DRAFT" || status === "SUBMITTED" || status === "ADDITIONAL_INFORMATION_REQUIRED";
}

// ---- Reviewer actions ----

/**
 * Reviewer actions to offer for a case, when the API does not send `allowed_actions`. Mirrors ADR-034 §1 and
 * §4; the API enforces assignment, permissions, maker-checker and self-decision rules regardless.
 */
export function deriveReviewActions(input: {
  type: ReviewCaseType | string;
  status: string;
  assignedToMe: boolean;
  /** A first approval is recorded and waits for a second one. */
  hasPendingApproval: boolean;
  /** The caller gave that first approval (maker-checker: they cannot give the second). */
  pendingApprovalByMe?: boolean;
}): ReviewAction[] {
  const { type, status, assignedToMe, hasPendingApproval, pendingApprovalByMe = false } = input;
  if (hasPendingApproval || status === "AWAITING_SECOND_APPROVAL") {
    // The second approver is deliberately someone other than the assigned (first) reviewer.
    if (pendingApprovalByMe) return assignedToMe ? ["reject"] : [];
    return assignedToMe ? ["reject"] : ["second-approval"];
  }
  const actions: ReviewAction[] = [];
  const open = !FINAL_CASE_STATUSES.has(status) && status !== "APPROVED" && status !== "VERIFIED" && status !== "RETIRED";
  if (open && !assignedToMe) actions.push("assign");
  if (!assignedToMe) return actions;
  if (type === "PAYOUT_DESTINATION") {
    // ADR-034 §4: no separate review state — assignment starts the review; no escalate/reopen/revoke.
    switch (status) {
      case "PENDING_VERIFICATION":
        actions.push("request-info", "approve", "reject");
        break;
      case "VERIFIED":
        actions.push("suspend");
        break;
      case "SUSPENDED":
        actions.push("reinstate");
        break;
    }
    return actions;
  }
  switch (status) {
    case "SUBMITTED":
      actions.push("start-review");
      break;
    case "UNDER_REVIEW":
      actions.push("request-info", "approve", "reject", "escalate");
      break;
    case "ESCALATED":
      actions.push("approve", "reject", "return");
      break;
    case "APPROVED":
      actions.push("suspend", "revoke");
      break;
    case "SUSPENDED":
      actions.push("reinstate", "reopen", "revoke");
      break;
  }
  return actions;
}

export const REVIEW_ACTION_LABELS: Record<ReviewAction, string> = {
  assign: "Assign to me",
  "start-review": "Start review",
  "request-info": "Request information",
  approve: "Approve",
  "second-approval": "Give second approval",
  reject: "Reject",
  escalate: "Escalate to compliance",
  suspend: "Suspend",
  reinstate: "Reinstate",
  reopen: "Reopen",
  return: "Return to review",
  revoke: "Revoke",
};

/**
 * Reason codes offered to reviewers. The contract does not publish a catalogue yet (reported to the lead);
 * these are descriptive codes, never legal conclusions. OTHER always requires a note.
 */
export const REASON_CODES: Partial<Record<ReviewAction, string[]>> = {
  approve: ["DOCUMENTS_CONSISTENT", "IDENTITY_CONFIRMED", "AUTHORITY_CONFIRMED", "OWNERSHIP_CONFIRMED", "OTHER"],
  reject: ["DOCUMENT_ILLEGIBLE", "DOCUMENT_EXPIRED", "DETAILS_DO_NOT_MATCH", "DOCUMENT_NOT_ACCEPTED", "UNDERAGE", "AUTHORITY_NOT_SHOWN", "OWNERSHIP_NOT_SHOWN", "OTHER"],
  escalate: ["RISK_INDICATORS", "POSSIBLE_DUPLICATE_IDENTITY", "SANCTIONS_OR_PEP_CHECK_NEEDED", "POLICY_EXCEPTION", "OTHER"],
  suspend: ["NEW_INFORMATION_RECEIVED", "DOCUMENT_VALIDITY_CONCERN", "COMPLIANCE_INSTRUCTION", "OTHER"],
  reopen: ["NEW_INFORMATION_RECEIVED", "DECISION_ERROR", "COMPLIANCE_INSTRUCTION", "OTHER"],
  reinstate: ["CONCERN_RESOLVED", "NEW_INFORMATION_RECEIVED", "COMPLIANCE_INSTRUCTION", "OTHER"],
  return: ["ADDITIONAL_REVIEW_NEEDED", "COMPLIANCE_INSTRUCTION", "OTHER"],
  revoke: ["DOCUMENT_VALIDITY_CONCERN", "COMPLIANCE_INSTRUCTION", "OTHER"],
};

export const COMPLIANCE_ACTION_LABELS: Record<ComplianceAction, string> = {
  assign: "Assign to me",
  start: "Start review",
  "request-info": "Request information",
  escalate: "Escalate",
  resolve: "Propose resolution",
  "approve-resolution": "Approve resolution",
  close: "Close case",
  reopen: "Reopen",
};

/** Resolution decisions of the compliance module (internal/compliance `Decisions`); RESTRICT, SUSPEND, OFFBOARD and CONFIRMED_FRAUD need a second officer's approval. */
export const COMPLIANCE_DECISIONS = ["CLEARED", "EDD_CONDITIONS", "RESTRICT", "SUSPEND", "OFFBOARD", "CONFIRMED_FRAUD"] as const;

/** Reason codes offered per compliance action (UPPER_SNAKE; the API validates the format, not a catalogue). */
export const COMPLIANCE_REASON_CODES: Partial<Record<ComplianceAction, string[]>> = {
  "request-info": ["INFORMATION_NEEDED", "IDENTITY_CLARIFICATION", "SOURCE_OF_FUNDS_QUESTION", "OTHER"],
  escalate: ["RISK_INDICATORS", "POSSIBLE_DUPLICATE_IDENTITY", "SANCTIONS_OR_PEP_CHECK_NEEDED", "POLICY_EXCEPTION", "OTHER"],
  resolve: ["NO_CONCERNS_FOUND", "CONCERNS_CONFIRMED", "INSUFFICIENT_INFORMATION", "POLICY_EXCEPTION", "OTHER"],
  close: ["RESOLUTION_IMPLEMENTED", "DUPLICATE_CASE", "OPENED_IN_ERROR", "OTHER"],
  reopen: ["NEW_INFORMATION_RECEIVED", "DECISION_ERROR", "OTHER"],
};

/**
 * Compliance actions to offer when the API does not send `allowed_actions`. A proposed resolution is
 * approved by someone other than the proposer (maker-checker; the API enforces it too).
 */
export function deriveComplianceActions(input: {
  status: string;
  assignedToMe: boolean;
  resolutionPending: boolean;
  proposedByMe: boolean;
}): ComplianceAction[] {
  const { status, assignedToMe, resolutionPending, proposedByMe } = input;
  const actions: ComplianceAction[] = [];
  if (status !== "CLOSED" && status !== "RESOLVED" && !assignedToMe) actions.push("assign");
  if (status === "RESOLVED" && resolutionPending && !proposedByMe) actions.push("approve-resolution");
  if (!assignedToMe) return actions;
  switch (status) {
    case "OPEN":
    case "ASSIGNED":
    case "AWAITING_INFORMATION":
      actions.push("start");
      break;
    case "IN_REVIEW":
      actions.push("request-info", "escalate", "resolve");
      break;
    case "ESCALATED":
      actions.push("start", "resolve");
      break;
    case "RESOLVED":
      if (!resolutionPending) actions.push("close");
      actions.push("reopen");
      break;
    case "CLOSED":
      actions.push("reopen");
      break;
  }
  return actions;
}
