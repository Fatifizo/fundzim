/**
 * Wire types for the Stage 6 campaign API (docs/stage-6/interface-contracts.md §3, §5, §6; ADR-036/037).
 * Hand-written from the contract. Where the contract names an endpoint but leaves the response shape open,
 * the fields below are the frontend's reading of it (listed in the Stage 6 frontend report); the
 * normalisers in ./normalise.ts read responses defensively so a missing optional field never breaks a page.
 */
import type { Money } from "@/lib/api/types";

export const CAMPAIGN_STATUSES = [
  "DRAFT",
  "SUBMITTED",
  "UNDER_REVIEW",
  "CHANGES_REQUESTED",
  "APPROVED",
  "ACTIVE",
  "PAUSED",
  "SUSPENDED",
  "REJECTED",
  "COMPLETED",
  "CANCELLED",
  "ARCHIVED",
] as const;
export type CampaignStatus = (typeof CAMPAIGN_STATUSES)[number];

/** Owner listing choice (contract §3): PUBLIC = listed and searchable, UNLISTED = reachable by link only. */
export const VISIBILITIES = ["PUBLIC", "UNLISTED"] as const;
export type Visibility = (typeof VISIBILITIES)[number];

export const ELIGIBILITY_ACTIONS = ["CREATE_DRAFT", "EDIT_DRAFT", "SUBMIT_FOR_REVIEW", "APPROVE", "PUBLISH", "UPDATE_PUBLISHED", "REACTIVATE"] as const;
export type EligibilityAction = (typeof ELIGIBILITY_ACTIONS)[number];

export interface EligibilityReason {
  code: string;
  field: string | null;
  message: string | null;
}

export interface EligibilityResult {
  action: string;
  allowed: boolean;
  reasons: EligibilityReason[];
}

export interface Category {
  code: string;
  name: string;
  description: string | null;
  requires_organisation: boolean;
}

export interface CurrencyInfo {
  code: string;
  minor_units: number;
  display_symbol: string;
}

export type Disclosure = "NONE" | "DISPLAY_NAME";

export interface CampaignBeneficiary {
  beneficiary_id: string;
  display_name: string | null;
  disclosure: Disclosure;
  consent_declared: boolean;
  verification_status: string | null;
}

export interface ReviewFeedback {
  outcome: string;
  reason_code: string | null;
  message: string | null;
  decided_at: string | null;
}

export interface Campaign {
  id: string;
  public_code: string | null;
  slug: string | null;
  status: CampaignStatus | string;
  visibility: Visibility | string;
  category: string;
  organisation_id: string | null;
  title: string;
  summary: string;
  story: string;
  goal: Money | null;
  risk_tier: string | null;
  re_review_required: boolean;
  resubmission_count: number;
  beneficiary: CampaignBeneficiary | null;
  review_feedback: ReviewFeedback | null;
  eligibility: EligibilityResult[];
  completion_reason: string | null;
  created_at: string | null;
  submitted_at: string | null;
  approved_at: string | null;
  published_at: string | null;
  paused_at: string | null;
  completed_at: string | null;
  cancelled_at: string | null;
  archived_at: string | null;
  updated_at: string | null;
  version: number;
}

export const MEDIA_STATUSES = ["UPLOADED", "QUARANTINED", "SCANNING", "APPROVED", "REJECTED", "REMOVED"] as const;
export type MediaStatus = (typeof MEDIA_STATUSES)[number];
export const PENDING_MEDIA_STATUSES: ReadonlySet<string> = new Set(["UPLOADED", "QUARANTINED", "SCANNING"]);

export type MediaKind = "COVER" | "GALLERY";

export interface CampaignMedia {
  id: string;
  kind: MediaKind | string;
  position: number;
  alt_text: string;
  status: MediaStatus | string;
  rejection_reason: string | null;
  created_at: string | null;
}

export interface CampaignUpdate {
  id: string;
  title: string;
  body: string;
  status: string;
  created_at: string | null;
  published_at: string | null;
}

export interface CreateCampaignInput {
  title: string;
  summary: string;
  category: string;
  goal: { amount_minor: string; currency: string };
  organisation_id?: string;
}

export interface CampaignPatch {
  title?: string;
  summary?: string;
  story?: string;
  category?: string;
  goal?: { amount_minor: string; currency: string };
  visibility?: Visibility;
}

// ---- Public ----

export interface PublicCampaign {
  slug: string;
  title: string;
  summary: string;
  story: string;
  category: { code: string; name: string };
  goal: Money | null;
  status: string;
  organiser: { display_name: string };
  organisation: { display_name: string } | null;
  beneficiary: { disclosure: Disclosure | string; display_name: string | null };
  cover: { id: string; alt_text: string } | null;
  gallery: Array<{ id: string; alt_text: string }>;
  published_at: string | null;
  completed_at: string | null;
}

export interface PublicCampaignSummary {
  slug: string;
  title: string;
  summary: string;
  category: { code: string; name: string };
  goal: Money | null;
  status: string;
  cover: { id: string; alt_text: string } | null;
  published_at: string | null;
}

// ---- Staff ----

export interface PersonRef {
  id: string;
  display_name: string;
}

export interface StaffCampaignSummary {
  id: string;
  title: string;
  status: string;
  review_status: string | null;
  category: string;
  risk_tier: string | null;
  owner_display_name: string | null;
  assigned_to: PersonRef | null;
  escalated: boolean;
  awaiting_second_approval: boolean;
  /** `queued_at` in the review queue. */
  submitted_at: string | null;
  updated_at: string | null;
  slug: string | null;
}

export interface ReviewHistoryEntry {
  id: string;
  action: string;
  from_status: string | null;
  to_status: string | null;
  actor: { type: string; display_name: string | null } | null;
  reason_code: string | null;
  note: string | null;
  occurred_at: string | null;
}

export interface CampaignReview {
  id: string | null;
  status: string | null;
  assigned_to: PersonRef | null;
  risk_tier: string | null;
  pending_outcome: string | null;
  pending_decided_by: string | null;
  requires_second_approval: boolean;
  escalated: boolean;
  compliance_case_id: string | null;
}

export interface StaffCampaignDetail {
  campaign: Campaign & { owner_display_name: string | null };
  review: CampaignReview;
  eligibility: EligibilityResult[];
  beneficiary: { display_name: string | null; beneficiary_type: string | null; verification_status: string | null } | null;
  /** Generic indicator only: a compliance restriction applies to a party. Never a reason. */
  restricted: boolean;
  media: CampaignMedia[];
  history: ReviewHistoryEntry[];
  allowed_actions: StaffAction[] | null;
}

export const STAFF_ACTIONS = [
  "assign",
  "start-review",
  "request-changes",
  "approve",
  "second-approval",
  "reject",
  "escalate",
  "suspend",
  "reactivate",
  "publish",
  "reopen",
  "cancel",
] as const;
export type StaffAction = (typeof STAFF_ACTIONS)[number];

export interface ModerationItem {
  id: string;
  campaign_id: string;
  campaign_title: string | null;
  title: string;
  body: string;
  status: string;
  created_at: string | null;
}

// ---- Age attestation and staff links (ADR-037) ----

export interface AgeAttestation {
  outcome: "ATTESTED" | "DECLINED" | null;
  statement_version: string;
  adult_age: number;
  attested_at: string | null;
  source: string | null;
  /** Verification level after the call (Go `level`). */
  level: string | null;
  /** POST only: BASIC_VERIFIED conditions that do not hold yet (EMAIL_NOT_VERIFIED, PHONE_NOT_VERIFIED, …). */
  basic_unmet: string[];
}

export interface StaffLinkStatus {
  status: "NONE" | "PENDING" | "LINKED" | string;
  email_masked: string | null;
  requested_at: string | null;
  linked_at: string | null;
  expires_at: string | null;
}
