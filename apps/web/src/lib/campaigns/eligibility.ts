/**
 * Maps eligibility reasons (contract §5) to plain-language guidance and a link to where the person can fix
 * them. The API decides eligibility; this only explains it. Unknown codes are shown with a generic message
 * so a new backend reason is never hidden. `ACCOUNT_RESTRICTED` deliberately carries no detail and no link
 * (ADR-037 §3: messages say only that the action is not available on the account).
 */
import type { EligibilityReason } from "./types";

/** Wizard steps (1-based), used for "fix it" links inside the guided flow. */
export const WIZARD_STEPS = ["Category", "Title and summary", "Story", "Goal", "Beneficiary", "Photos", "Check", "Submit"] as const;
export type WizardStep = 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8;

export interface FixContext {
  campaignId?: string;
  organisationId?: string | null;
  /** Link to a wizard step; when absent, campaign pages are used instead. */
  stepHref?: (step: WizardStep) => string;
  /** Where to come back to after an external fix (age attestation). */
  returnTo?: string;
}

export interface ReasonGuidance {
  code: string;
  title: string;
  description: string;
  fix: { href: string; label: string } | null;
}

const FIELD_STEP: Record<string, WizardStep> = {
  category: 1,
  title: 2,
  summary: 2,
  story: 3,
  goal: 4,
  "goal.amount_minor": 4,
  "goal.currency": 4,
  currency: 4,
  beneficiary: 5,
  beneficiary_id: 5,
  media: 6,
  cover: 6,
};

function campaignPage(ctx: FixContext, sub: string, step: WizardStep): string {
  if (ctx.stepHref) return ctx.stepHref(step);
  if (ctx.campaignId) return `/dashboard/campaigns/${ctx.campaignId}${sub}`;
  return "/dashboard/campaigns/new";
}

function withReturn(path: string, returnTo?: string): string {
  return returnTo ? `${path}?next=${encodeURIComponent(returnTo)}` : path;
}

export function reasonGuidance(reason: EligibilityReason, ctx: FixContext = {}): ReasonGuidance {
  const code = reason.code;
  const g = (title: string, description: string, fix: ReasonGuidance["fix"] = null): ReasonGuidance => ({ code, title, description, fix });
  switch (code) {
    case "NOT_AUTHENTICATED":
      return g("Sign in", "Your session has ended. Sign in again to continue.", { href: "/login", label: "Sign in" });
    case "ACCOUNT_NOT_ACTIVE":
      return g("Your account is not active", "This action is not available while your account is not active. Contact us if you need help.");
    case "AGE_ATTESTATION_REQUIRED":
      return g("Confirm your age", "Confirm that you are an adult before you raise funds. This is a self-declaration.", {
        href: withReturn("/dashboard/verification/age", ctx.returnTo),
        label: "Confirm your age",
      });
    case "BASIC_VERIFICATION_REQUIRED":
      return g("Complete basic verification", "Confirm your email address and phone number, and confirm your age.", {
        href: "/dashboard/verification",
        label: "Go to verification",
      });
    case "IDENTITY_VERIFICATION_REQUIRED":
      return g("Verify your identity", "Your identity must be verified before a campaign can be submitted, published or resumed.", {
        href: "/dashboard/verification/identity",
        label: "Verify your identity",
      });
    case "ORGANISATION_VERIFICATION_REQUIRED":
      return g("Verify the organisation", "The organisation must be verified before this campaign can go further.", {
        href: ctx.organisationId ? `/dashboard/organisations/${ctx.organisationId}/verification` : "/dashboard/organisations",
        label: "Organisation verification",
      });
    case "NOT_ORGANISATION_ADMIN":
      return g("Organisation administrators only", "Only an administrator of the organisation can do this.", { href: "/dashboard/organisations", label: "Your organisations" });
    case "REPRESENTATIVE_AUTHORITY_REQUIRED":
      return g("Authority to act for the organisation", "You need the organisation's authority to submit campaigns for it.", { href: "/dashboard/organisations", label: "Your organisations" });
    case "BENEFICIARY_REQUIRED":
      return g("Choose a beneficiary", "Say who will benefit from the funds.", { href: campaignPage(ctx, "/beneficiaries", 5), label: "Choose a beneficiary" });
    case "BENEFICIARY_NOT_VERIFIED":
      return g("Beneficiary not verified yet", "The beneficiary must be verified before this campaign can go further.", {
        href: "/dashboard/verification/beneficiaries",
        label: "Beneficiary verification",
      });
    case "BENEFICIARY_NOT_AUTHORISED":
      return g("Authority for the beneficiary", "Your authority to raise funds for this beneficiary has not been established. Choose another beneficiary or complete their verification.", {
        href: campaignPage(ctx, "/beneficiaries", 5),
        label: "Review the beneficiary",
      });
    case "INDIVIDUAL_FOR_OTHERS_DISABLED":
      return g("Fundraising for someone else is not available yet", "Individuals can currently raise funds only for themselves. Choose yourself as the beneficiary, or raise funds through a verified organisation.", {
        href: campaignPage(ctx, "/beneficiaries", 5),
        label: "Change the beneficiary",
      });
    case "ACCOUNT_RESTRICTED":
      return g("Not available on your account", "This action is not available on your account. Contact us if you need help.");
    case "CATEGORY_INACTIVE":
      return g("Choose another category", "This category is no longer available.", { href: campaignPage(ctx, "/edit", 1), label: "Change the category" });
    case "CATEGORY_REQUIRES_ORGANISATION":
      return g("Organisation category", "Campaigns in this category can only be run by a verified organisation.", { href: campaignPage(ctx, "/edit", 1), label: "Change the category" });
    case "COVER_IMAGE_REQUIRED":
      return g("Add a cover photo", "Every campaign needs a cover photo that has passed our checks.", { href: campaignPage(ctx, "/media", 6), label: "Add a cover photo" });
    case "MEDIA_NOT_READY":
      return g("Photos still being checked", "Wait for your photos to finish their checks, or remove photos that were not accepted.", { href: campaignPage(ctx, "/media", 6), label: "Check your photos" });
    case "CURRENCY_NOT_AVAILABLE":
      return g("Currency not available", "This currency cannot be used for campaigns yet. Choose an available currency.", { href: campaignPage(ctx, "/edit", 4), label: "Change the goal" });
    case "GOAL_OUT_OF_RANGE":
      return g("Goal outside the allowed range", "Choose a goal within the allowed range.", { href: campaignPage(ctx, "/edit", 4), label: "Change the goal" });
    case "MISSING_FIELD": {
      const step = (reason.field && FIELD_STEP[reason.field]) || 2;
      const label = reason.field ? fieldLabel(reason.field) : "Some information";
      return g(`${label} missing`, `Add ${label.toLowerCase()} before continuing.`, { href: campaignPage(ctx, step === 5 ? "/beneficiaries" : step === 6 ? "/media" : "/edit", step), label: `Add ${label.toLowerCase()}` });
    }
    case "AGE_REQUIREMENT_NOT_MET":
      return g("Age requirement not met", "Raising funds is for adults. A parent or guardian can raise funds on a child's behalf.");
    case "MINOR_DISCLOSURE_NOT_ALLOWED":
      return g("Keep the child's details private", "A child's details can't be shown publicly.", { href: campaignPage(ctx, "/beneficiaries", 5), label: "Change what is shown" });
    case "INVALID_LENGTH": {
      const step = (reason.field && FIELD_STEP[reason.field]) || 2;
      const label = reason.field ? fieldLabel(reason.field) : "A field";
      return g(`${label} is too short or too long`, `Adjust the ${label.toLowerCase()} to the allowed length.`, { href: campaignPage(ctx, "/edit", step), label: `Edit the ${label.toLowerCase()}` });
    }
    case "POLICY_UNAVAILABLE":
      return g("Temporarily unavailable", "Campaign rules are temporarily unavailable. Try again later.");
    case "RESUBMISSION_LIMIT_REACHED":
      return g("Resubmission limit reached", "This campaign has been resubmitted the maximum number of times. Contact us if you need help.");
    case "SECOND_APPROVAL_REQUIRED":
      return g("Second approval required", "A second, different reviewer must approve this campaign.");
    default:
      return g("Something needs attention", "This needs attention before you can continue.");
  }
}

export function fieldLabel(field: string): string {
  const labels: Record<string, string> = {
    category: "Category",
    title: "Title",
    summary: "Summary",
    story: "Story",
    goal: "Goal",
    "goal.amount_minor": "Goal amount",
    "goal.currency": "Currency",
    currency: "Currency",
    beneficiary: "Beneficiary",
    beneficiary_id: "Beneficiary",
    media: "Photos",
    cover: "Cover photo",
  };
  return labels[field] ?? field.replace(/[_.]+/g, " ").replace(/^\w/, (c) => c.toUpperCase());
}

/** Normalises the `details` of a 422 NOT_ELIGIBLE error (or any list of reason-like objects). */
export function reasonsFromDetails(details: ReadonlyArray<{ code?: unknown; field?: unknown; message?: unknown }>): EligibilityReason[] {
  return details
    .filter((d) => typeof d?.code === "string")
    .map((d) => ({ code: d.code as string, field: typeof d.field === "string" && d.field ? d.field : null, message: typeof d.message === "string" ? d.message : null }));
}
