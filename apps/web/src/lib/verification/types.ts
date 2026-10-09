/**
 * Wire types for the Stage 5 verification API (docs/stage-5/interface-contracts.md §7; vocabularies from
 * ADR-034). Hand-written from the contract until OpenAPI type generation exists (KI-14). Field names must
 * match the contract. Where the contract leaves a shape open (reviewer and compliance case detail), the
 * optional fields below are the frontend's reading of it and are listed in the Stage 5 frontend report so
 * the backend can align; the UI renders defensively when they are absent.
 */

// ---- Shared vocabularies (ADR-034) ----

export const CASE_STATUSES = [
  "DRAFT",
  "SUBMITTED",
  "UNDER_REVIEW",
  "ADDITIONAL_INFORMATION_REQUIRED",
  "ESCALATED",
  "APPROVED",
  "REJECTED",
  "EXPIRED",
  "SUSPENDED",
  "REVOKED",
  "WITHDRAWN",
] as const;
export type CaseStatus = (typeof CASE_STATUSES)[number];

export const FINAL_CASE_STATUSES: ReadonlySet<string> = new Set(["REJECTED", "EXPIRED", "REVOKED", "WITHDRAWN"]);
/** States in which the subject may edit details and documents (contract §7.2: `CASE_NOT_EDITABLE` otherwise). */
export const EDITABLE_CASE_STATUSES: ReadonlySet<string> = new Set(["DRAFT", "ADDITIONAL_INFORMATION_REQUIRED"]);

export const SCAN_STATUSES = ["UPLOADED", "QUARANTINED", "SCANNING", "CLEAN", "REJECTED", "FAILED_SCAN", "DELETED"] as const;
export type ScanStatus = (typeof SCAN_STATUSES)[number];
/** Scan states that will still change on their own (poll them). FAILED_SCAN is retried by the worker. */
export const PENDING_SCAN_STATUSES: ReadonlySet<string> = new Set(["UPLOADED", "QUARANTINED", "SCANNING", "FAILED_SCAN"]);

export type RiskLevel = "LOW" | "STANDARD" | "ENHANCED" | "RESTRICTED";

export type SubjectType = "KYC_CASE" | "KYB_CASE" | "KYB_PERSON" | "BENEFICIARY" | "PAYOUT_DESTINATION";

// ---- Documents ----

export interface VerificationDocument {
  id: string;
  subject_type: SubjectType;
  subject_id: string;
  document_type: string;
  side: string | null;
  status: ScanStatus;
  media_type: string | null;
  size_bytes: number;
  uploaded_at: string;
  rejected_reason: string | null;
}

/**
 * One document requirement. The contract describes `{document_type, sides, satisfied}`; the backend sends
 * `{document_types: [...alternatives], satisfied}` (one CLEAN document of any listed type). Read it through
 * `normaliseRequirements`, which always fills `document_type` (first alternative), `document_types` and `sides`.
 */
export interface DocumentRequirement {
  document_type: string;
  document_types?: string[];
  sides: string[];
  satisfied: boolean;
}

export interface InformationRequest {
  id: string;
  message: string;
  items: string[];
  requested_at: string;
  responded_at: string | null;
}

export interface Decision {
  outcome: string;
  reason_code: string | null;
  message: string | null;
  decided_at: string;
}

export interface DocumentAccess {
  url: string;
  expires_at: string;
}

// ---- KYC ----

export type IdDocumentType = "ZW_NATIONAL_ID" | "ZW_PASSPORT" | "FOREIGN_PASSPORT";

export interface Address {
  line1: string;
  line2: string | null;
  city: string;
  province: string | null;
  postal_code: string | null;
  country: string;
}

export interface KycIdentity {
  legal_first_name: string | null;
  legal_last_name: string | null;
  date_of_birth: string | null;
  nationality: string | null;
  country_of_residence: string | null;
  id_document_type: IdDocumentType | null;
  id_document_number_masked: string | null;
  id_document_expiry: string | null;
  residential_address: Address | null;
}

export interface KycCase {
  id: string;
  kind: "KYC";
  status: CaseStatus;
  target_level: string;
  policy_version: string | number;
  identity: KycIdentity | null;
  documents: VerificationDocument[];
  requirements: DocumentRequirement[];
  information_requests: InformationRequest[];
  decision: Decision | null;
  submitted_at: string | null;
  created_at: string;
  updated_at: string;
  version: number;
}

export interface KycGates {
  create_draft: boolean;
  submit_campaign: boolean;
  withdraw: boolean;
}

export interface KycStatus {
  level: string;
  status: string;
  risk_level: RiskLevel | null;
  case: KycCase | null;
  gates: KycGates;
}

/** PATCH /kyc/cases/{id} body. `id_document_number` is write-only: the API only ever returns it masked. */
export interface KycCasePatch {
  legal_first_name?: string;
  legal_last_name?: string;
  date_of_birth?: string;
  nationality?: string;
  country_of_residence?: string;
  id_document_type?: IdDocumentType;
  id_document_number?: string;
  id_document_expiry?: string | null;
  residential_address?: Address;
}

// ---- KYB ----

export const KYB_PERSON_ROLES = ["DIRECTOR", "TRUSTEE", "OFFICE_BEARER", "BENEFICIAL_OWNER", "CONTROLLER", "REPRESENTATIVE"] as const;
export type KybPersonRole = (typeof KYB_PERSON_ROLES)[number];

export interface KybPerson {
  id: string;
  full_name: string;
  roles: KybPersonRole[];
  ownership_bp: number | null;
  id_document_type: IdDocumentType | null;
  id_document_number_masked: string | null;
}

export interface KybDetails {
  registered_name: string | null;
  trading_name: string | null;
  registration_number: string | null;
  registry: string | null;
  country_of_registration: string | null;
  registered_address: Address | null;
}

export interface KybCase {
  id: string;
  kind: "KYB";
  organisation_id: string;
  status: CaseStatus;
  target_level: string;
  details: KybDetails;
  persons: KybPerson[];
  documents: VerificationDocument[];
  requirements: DocumentRequirement[];
  information_requests: InformationRequest[];
  decision: Decision | null;
  submitted_at: string | null;
  created_at: string;
  updated_at: string;
  version: number;
}

export interface KybStatus {
  level: string;
  status: string;
  case: KybCase | null;
}

export interface KybPersonInput {
  full_name: string;
  roles: KybPersonRole[];
  ownership_bp?: number;
  date_of_birth?: string;
  nationality?: string;
  id_document_type?: IdDocumentType;
  id_document_number?: string;
}

// ---- Organisations (Stage 4, ORG-02/03) ----

export interface MyOrganisation {
  id: string;
  display_name: string;
  slug: string;
  org_type: string;
  status: string;
  my_role: "ORG_ADMIN" | "ORG_MEMBER" | string;
  my_permissions: string[];
  created_at: string;
}

// ---- Beneficiaries ----

export const BENEFICIARY_TYPES = ["SELF", "INDIVIDUAL", "MINOR", "INCAPACITATED_ADULT", "ORGANISATION", "INSTITUTION", "COMMUNITY_GROUP"] as const;
export type BeneficiaryType = (typeof BENEFICIARY_TYPES)[number];

export const RELATIONSHIP_TYPES = [
  "SELF",
  "PARENT_GUARDIAN",
  "FAMILY_MEMBER",
  "AUTHORIZED_REPRESENTATIVE",
  "ORGANISATION_REPRESENTATIVE",
  "THIRD_PARTY_ORGANISER",
  "OTHER",
] as const;
export type RelationshipType = (typeof RELATIONSHIP_TYPES)[number];

export const AUTHORITY_BASES = [
  "NOT_REQUIRED",
  "BENEFICIARY_CONSENT",
  "PARENTAL_RESPONSIBILITY",
  "GUARDIANSHIP_ORDER",
  "LEGAL_REPRESENTATION",
  "ORGANISATION_AUTHORITY",
  "GROUP_MANDATE",
  "INSTITUTION_CONFIRMATION",
] as const;
export type AuthorityBasis = (typeof AUTHORITY_BASES)[number];

export interface Beneficiary {
  id: string;
  owner: { type: "USER" | "ORGANISATION" | string; id: string };
  beneficiary_type: BeneficiaryType;
  kind: "INDIVIDUAL" | "ORGANISATION";
  display_name: string;
  full_name: string | null;
  date_of_birth?: string | null;
  relationship: { type: RelationshipType; description: string | null };
  authority_basis: AuthorityBasis;
  verification: {
    status: CaseStatus;
    risk_level: RiskLevel | null;
    requires_second_approval: boolean;
    information_requests: InformationRequest[];
    decision: Decision | null;
  };
  documents: VerificationDocument[];
  requirements: DocumentRequirement[];
  created_at: string;
  updated_at: string;
  version: number;
}

export interface BeneficiaryInput {
  owner_organisation_id?: string;
  beneficiary_type: BeneficiaryType;
  display_name: string;
  full_name?: string;
  date_of_birth?: string;
  relationship: { type: RelationshipType; description?: string };
  authority_basis: AuthorityBasis;
}

// ---- Payout destinations ----

export const RAILS = ["ECOCASH", "ONEMONEY", "INNBUCKS", "OMARI", "BANK_TRANSFER", "ZIMSWITCH"] as const;
export type Rail = (typeof RAILS)[number];
export const BANK_RAILS: ReadonlySet<string> = new Set(["BANK_TRANSFER", "ZIMSWITCH"]);

export type DestinationStatus = "UNVERIFIED" | "PENDING_VERIFICATION" | "VERIFIED" | "REJECTED" | "SUSPENDED" | "EXPIRED" | "RETIRED";
export type OwnershipCheck = "NOT_STARTED" | "PENDING" | "PROVIDER_CONFIRMATION_REQUIRED" | "CONFIRMED" | "FAILED";
export type ComplianceCheck = "PENDING" | "APPROVED" | "REJECTED";
export type VerificationMethod = "BANK_LETTER" | "MOBILE_MONEY_STATEMENT" | "PROVIDER_LOOKUP";

export interface PayoutDestination {
  id: string;
  owner: { type: string; id: string };
  payee: { type: "OWNER" | "BENEFICIARY"; beneficiary_id: string | null };
  category: "MOBILE_MONEY_WALLET" | "BANK_ACCOUNT";
  rail: Rail;
  provider_name: string | null;
  currency: "USD" | "ZWG";
  holder_name: string;
  masked_identifier: string;
  status: DestinationStatus;
  checks: { format_validated: boolean; ownership: OwnershipCheck; compliance: ComplianceCheck };
  last_reviewed_at: string | null;
  eligible_for_payout: false;
  created_at: string;
  updated_at: string;
  version: number;
}

export interface PayoutDestinationInput {
  owner_organisation_id?: string;
  payee: { type: "OWNER" | "BENEFICIARY"; beneficiary_id?: string };
  rail: Rail;
  currency: "USD" | "ZWG";
  holder_name: string;
  account_identifier: string;
  bank_code?: string;
}

// ---- Reviewer (contract §7.3) ----

export type ReviewCaseType = "KYC" | "KYB" | "BENEFICIARY" | "PAYOUT_DESTINATION";

export interface PersonRef {
  id: string;
  display_name: string;
}

export interface ReviewCaseSummary {
  id: string;
  type: ReviewCaseType;
  status: string;
  subject: { type: string; id: string; display_name?: string | null };
  assigned_to: PersonRef | null;
  risk_level: RiskLevel | null;
  submitted_at: string | null;
  updated_at: string;
  requires_second_approval?: boolean;
}

export interface ReviewHistoryEntry {
  id: string;
  action: string;
  actor: { type: string; display_name: string | null } | null;
  occurred_at: string;
  reason_code?: string | null;
  note?: string | null;
}

export interface ReviewCase extends ReviewCaseSummary {
  /** Masked, reviewer-safe subject facts as label/value pairs (never a full ID or account number). */
  subject_summary?: Array<{ label: string; value: string }>;
  identity?: KycIdentity | null;
  details?: KybDetails | null;
  persons?: KybPerson[];
  documents: VerificationDocument[];
  requirements?: DocumentRequirement[];
  information_requests?: InformationRequest[];
  decision?: Decision | null;
  /** First approval of a four-eyes case, while the second is outstanding. */
  pending_approval?: { approved_by: PersonRef; approved_at: string; outcome?: string | null } | null;
  history: ReviewHistoryEntry[];
  risk?: { level: RiskLevel | null; signals: Array<{ code: string; description?: string | null }> } | null;
  /** Actions the API will accept from the caller now. When absent the UI derives them from `status`. */
  allowed_actions?: ReviewAction[];
}

export const REVIEW_ACTIONS = [
  "assign",
  "start-review",
  "request-info",
  "approve",
  "second-approval",
  "reject",
  "escalate",
  "suspend",
  "reinstate",
  "reopen",
  "return",
  "revoke",
] as const;
export type ReviewAction = (typeof REVIEW_ACTIONS)[number];

// ---- Compliance (contract §7.4, ADR-034 §5) ----

export const COMPLIANCE_STATUSES = ["OPEN", "ASSIGNED", "IN_REVIEW", "AWAITING_INFORMATION", "ESCALATED", "RESOLVED", "CLOSED"] as const;
export type ComplianceStatus = (typeof COMPLIANCE_STATUSES)[number];

export interface ComplianceCaseSummary {
  id: string;
  reference?: string | null;
  case_type: string;
  status: ComplianceStatus;
  severity: string;
  subject: { type: string; id: string; display_name?: string | null } | null;
  assigned_to: PersonRef | null;
  opened_at: string;
  updated_at: string;
}

export interface ComplianceCase extends ComplianceCaseSummary {
  summary?: string | null;
  links: Array<{ type: string; id: string; label?: string | null }>;
  events: ReviewHistoryEntry[];
  notes: Array<{ id: string; author: PersonRef | null; body: string; created_at: string }>;
  resolution?: {
    decision: string;
    resolution_status: "PROPOSED" | "APPROVED";
    proposed_by: PersonRef | null;
    approved_by: PersonRef | null;
    note?: string | null;
  } | null;
  allowed_actions?: ComplianceAction[];
}

export const COMPLIANCE_ACTIONS = ["assign", "start", "request-info", "escalate", "resolve", "approve-resolution", "close", "reopen"] as const;
export type ComplianceAction = (typeof COMPLIANCE_ACTIONS)[number];
