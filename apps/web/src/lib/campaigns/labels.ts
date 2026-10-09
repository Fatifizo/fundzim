/**
 * Display mapping for campaign states (ADR-036) and the pure logic that decides which controls to show.
 * The API owns every decision and re-checks every action; an unknown status is shown humanised, never hidden.
 */
import type { BadgeTone } from "@/components/ui/badge";

import type { CampaignReview, StaffAction } from "./types";

export function humanise(code: string | null | undefined): string {
  if (!code) return "—";
  const words = code.toLowerCase().replace(/[_.-]+/g, " ").trim();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

interface Label {
  label: string;
  tone: BadgeTone;
}

const OWNER_STATUS: Record<string, Label> = {
  DRAFT: { label: "Draft", tone: "neutral" },
  SUBMITTED: { label: "Waiting for review", tone: "info" },
  UNDER_REVIEW: { label: "Under review", tone: "info" },
  CHANGES_REQUESTED: { label: "Changes requested", tone: "gold" },
  APPROVED: { label: "Approved — not yet published", tone: "brand" },
  ACTIVE: { label: "Published", tone: "brand" },
  PAUSED: { label: "Paused", tone: "gold" },
  SUSPENDED: { label: "Suspended", tone: "gold" },
  REJECTED: { label: "Not approved", tone: "gold" },
  COMPLETED: { label: "Completed", tone: "neutral" },
  CANCELLED: { label: "Cancelled", tone: "neutral" },
  ARCHIVED: { label: "Archived", tone: "neutral" },
};

const STAFF_STATUS: Record<string, Label> = {
  ...OWNER_STATUS,
  SUBMITTED: { label: "Submitted (pending review)", tone: "info" },
  APPROVED: { label: "Approved", tone: "brand" },
  ACTIVE: { label: "Active (published)", tone: "brand" },
  REJECTED: { label: "Rejected", tone: "gold" },
};

export function campaignStatusLabel(status: string | null | undefined, audience: "owner" | "staff" = "owner"): Label {
  const key = status ?? "DRAFT";
  return (audience === "staff" ? STAFF_STATUS : OWNER_STATUS)[key] ?? { label: humanise(key), tone: "neutral" };
}

export const PUBLIC_STATUS_NOTICE: Record<string, { title: string; body: string } | undefined> = {
  PAUSED: { title: "This campaign is paused", body: "The organiser has paused this campaign for now. You can still read the story and updates." },
  COMPLETED: { title: "This campaign has ended", body: "The organiser has completed this campaign. The story and updates remain available to read." },
};

const MEDIA_STATUS: Record<string, Label & { description: string }> = {
  UPLOADED: { label: "Uploaded", tone: "neutral", description: "The photo was received and is waiting to be checked." },
  QUARANTINED: { label: "Waiting for checks", tone: "info", description: "The photo is held securely until it has been checked." },
  SCANNING: { label: "Being checked", tone: "info", description: "The photo is being checked and processed. This usually takes a minute." },
  APPROVED: { label: "Ready", tone: "brand", description: "The photo passed the checks and can be shown." },
  REJECTED: { label: "Not accepted", tone: "gold", description: "The photo could not be accepted. Remove it and upload a different photo." },
  REMOVED: { label: "Removed", tone: "neutral", description: "The photo was removed and is no longer shown." },
};

export function mediaStatusLabel(status: string): Label & { description: string } {
  return MEDIA_STATUS[status] ?? { label: humanise(status), tone: "neutral", description: "" };
}

export const VISIBILITY_LABELS: Record<string, { label: string; description: string }> = {
  PUBLIC: { label: "Public", description: "Listed on FundZim and shown in search results while the campaign is published." },
  UNLISTED: { label: "Unlisted", description: "Not listed or searchable. Anyone with the link can still open it while it is published." },
};

/**
 * Completion reasons accepted by the API (`ORGANISER_COMPLETED` | `OTHER`, optional note). There is no
 * "goal reached": no donations or totals exist, so a reason may never imply money was collected.
 */
export const COMPLETION_REASONS: ReadonlyArray<{ value: string; label: string }> = [
  { value: "ORGANISER_COMPLETED", label: "I have finished this campaign" },
  { value: "OTHER", label: "Another reason (explain below)" },
];

// ---- Owner lifecycle ----

export type OwnerAction = "submit" | "withdraw" | "publish" | "pause" | "resume" | "complete" | "cancel" | "archive" | "revise";

/** Actions an owner can attempt in each status (ADR-036 §2 edges taken by the owner). The API re-checks. */
export function ownerActions(status: string): OwnerAction[] {
  switch (status) {
    case "DRAFT":
      return ["submit", "cancel"];
    case "SUBMITTED":
      return ["withdraw"];
    case "CHANGES_REQUESTED":
      return ["submit", "cancel"];
    case "APPROVED":
      return ["publish", "cancel"];
    case "ACTIVE":
      return ["pause", "complete", "cancel"];
    case "PAUSED":
      return ["resume", "complete", "cancel"];
    case "REJECTED":
      return ["revise", "archive"];
    case "COMPLETED":
    case "CANCELLED":
      return ["archive"];
    default:
      return [];
  }
}

export const OWNER_ACTION_LABELS: Record<OwnerAction, string> = {
  submit: "Submit for review",
  withdraw: "Withdraw from review",
  publish: "Publish",
  pause: "Pause",
  resume: "Resume",
  complete: "Complete",
  cancel: "Cancel campaign",
  archive: "Archive",
  revise: "Revise and resubmit",
};

export const OWNER_ACTION_DESCRIPTIONS: Record<OwnerAction, string> = {
  submit: "FundZim reviewers will check your campaign. Submitting does not guarantee approval; reviewers may ask for changes or decline it.",
  withdraw: "Your campaign leaves the review queue and returns to draft so you can change it.",
  publish: "Your approved campaign becomes visible to the public. FundZim re-checks eligibility at this moment.",
  pause: "Your campaign stays visible but is marked as paused.",
  resume: "Your campaign is shown as active again. FundZim re-checks eligibility first.",
  complete: "Your campaign is marked as ended. It stays readable but cannot be resumed.",
  cancel: "Your campaign is cancelled and will not be shown publicly. This cannot be undone.",
  archive: "Your campaign is archived. Its history is kept; it cannot be restored.",
  revise: "Your campaign returns to draft so you can address the reviewers' feedback and submit it again. The number of resubmissions is limited.",
};

/** Next eligibility check worth showing for a status (or null when none applies). */
export function nextEligibilityAction(status: string): "SUBMIT_FOR_REVIEW" | "PUBLISH" | "REACTIVATE" | null {
  if (status === "DRAFT" || status === "CHANGES_REQUESTED") return "SUBMIT_FOR_REVIEW";
  if (status === "APPROVED") return "PUBLISH";
  if (status === "PAUSED") return "REACTIVATE";
  return null;
}

export const EDITABLE_STATUSES: ReadonlySet<string> = new Set(["DRAFT", "CHANGES_REQUESTED", "ACTIVE", "PAUSED"]);
/** Edits to a live campaign create a pending version that is reviewed again (ADR-036 §3). */
export const LIVE_STATUSES: ReadonlySet<string> = new Set(["ACTIVE", "PAUSED"]);
export const VISIBILITY_EDITABLE: ReadonlySet<string> = new Set(["DRAFT", "CHANGES_REQUESTED", "SUBMITTED", "UNDER_REVIEW", "APPROVED", "ACTIVE", "PAUSED", "COMPLETED"]);

// ---- Staff ----

export const STAFF_ACTION_LABELS: Record<StaffAction, string> = {
  assign: "Assign to me",
  "start-review": "Start review",
  "request-changes": "Request changes",
  approve: "Approve",
  "second-approval": "Give second approval",
  reject: "Reject",
  escalate: "Escalate to compliance",
  suspend: "Suspend",
  reactivate: "Reactivate",
  publish: "Publish",
  reopen: "Reopen review",
  cancel: "Cancel campaign",
};

export const STAFF_ACTION_DESCRIPTIONS: Record<StaffAction, string> = {
  assign: "The review is assigned to you. Only the assigned reviewer works it.",
  "start-review": "The campaign moves to Under review.",
  "request-changes": "The owner is asked to change the campaign and can resubmit. Your message to them is shown; your note is not.",
  approve: "Record an approval. High-risk campaigns then need a second approval from a different reviewer.",
  "second-approval": "Give the second, independent approval. You must not be the first approver.",
  reject: "Reject the campaign. The owner sees the message for them, never your internal note.",
  escalate: "Send the campaign to compliance. A compliance case is opened.",
  suspend: "Suspend the campaign. It is no longer shown publicly.",
  reactivate: "Reactivate a suspended campaign. Eligibility is re-checked first.",
  publish: "Publish the approved campaign. Eligibility is re-checked first.",
  reopen: "Reopen a rejected campaign for review.",
  cancel: "Cancel the campaign permanently. History is kept.",
};

/** Reason codes offered per action (UPPER_SNAKE, contract §6 decision bodies). OTHER needs a fuller note. */
export const STAFF_REASON_CODES: Record<StaffAction, readonly string[]> = {
  assign: ["REVIEW_ASSIGNMENT", "WORKLOAD_BALANCING", "OTHER"],
  "start-review": ["REVIEW_STARTED", "OTHER"],
  "request-changes": ["CONTENT_UNCLEAR", "MISSING_INFORMATION", "MEDIA_ISSUE", "BENEFICIARY_INFORMATION", "GOAL_JUSTIFICATION", "OTHER"],
  approve: ["MEETS_POLICY", "OTHER"],
  "second-approval": ["MEETS_POLICY", "OTHER"],
  reject: ["PROHIBITED_PURPOSE", "MISLEADING_CONTENT", "INSUFFICIENT_EVIDENCE", "POLICY_VIOLATION", "OTHER"],
  escalate: ["COMPLIANCE_CONCERN", "RISK_INDICATORS", "OTHER"],
  suspend: ["POLICY_VIOLATION", "COMPLIANCE_RESTRICTION", "INVESTIGATION", "OTHER"],
  reactivate: ["ISSUE_RESOLVED", "OTHER"],
  publish: ["APPROVED_FOR_PUBLICATION", "OTHER"],
  reopen: ["NEW_INFORMATION", "DECISION_ERROR", "OTHER"],
  cancel: ["POLICY_VIOLATION", "OWNER_REQUEST", "OTHER"],
};

/** Actions that send a message to the owner (user_message). */
export const USER_MESSAGE_ACTIONS: ReadonlySet<StaffAction> = new Set(["request-changes", "reject"]);

export interface StaffActionContext {
  status: string;
  review: Pick<CampaignReview, "assigned_to" | "pending_outcome" | "pending_decided_by">;
  meId: string;
}

/**
 * Staff actions for a campaign. Uses the API's `allowed_actions` when sent, otherwise derives them from the
 * status. Either way, second approval is never offered to the person who gave the first approval
 * (pending_decided_by), and only offered while a first approval is pending.
 */
export function staffActions(ctx: StaffActionContext, allowed: StaffAction[] | null = null): StaffAction[] {
  const pending = !!ctx.review.pending_outcome;
  const firstApproverIsMe = !!ctx.review.pending_decided_by && ctx.review.pending_decided_by === ctx.meId;
  const assignedToMe = ctx.review.assigned_to?.id === ctx.meId;
  let actions: StaffAction[];
  if (allowed) actions = [...allowed];
  else {
    switch (ctx.status) {
      case "SUBMITTED":
        actions = assignedToMe ? ["start-review", "escalate", "cancel"] : ["assign", "escalate", "cancel"];
        break;
      case "UNDER_REVIEW":
        actions = pending
          ? ["second-approval", "request-changes", "reject", "escalate"]
          : [...(assignedToMe ? [] : (["assign"] as StaffAction[])), "approve", "request-changes", "reject", "escalate"];
        break;
      case "APPROVED":
        actions = ["publish", "suspend", "cancel"];
        break;
      case "ACTIVE":
      case "PAUSED":
        actions = ["suspend", "cancel"];
        break;
      case "SUSPENDED":
        actions = ["reactivate", "cancel"];
        break;
      case "REJECTED":
        actions = ["reopen"];
        break;
      default:
        actions = [];
    }
  }
  return actions.filter((a) => a !== "second-approval" || (pending && !firstApproverIsMe));
}
