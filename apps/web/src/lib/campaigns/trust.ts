/**
 * Trust indicator copy (ADR-038 §5). Each indicator is listed only when the API says it is true now, with
 * an honest explanation of what it means. None of them guarantees a campaign's claims.
 */
import type { PublicTrust } from "./types";

export interface TrustIndicator {
  key: keyof PublicTrust;
  /** Short label for cards. */
  short: string;
  label: string;
  explanation: string;
}

const INDICATORS: readonly TrustIndicator[] = [
  {
    key: "campaign_reviewed",
    short: "Reviewed",
    label: "Reviewed before publishing",
    explanation: "A FundZim reviewer checked this campaign against FundZim's rules before it was published.",
  },
  {
    key: "organiser_identity_verified",
    short: "Identity verified",
    label: "Organiser's identity verified",
    explanation: "The organiser completed FundZim's identity check.",
  },
  {
    key: "organisation_verified",
    short: "Organisation verified",
    label: "Organisation verified",
    explanation: "The organisation running this campaign completed FundZim's organisation check.",
  },
  {
    key: "beneficiary_verified",
    short: "Beneficiary verified",
    label: "Beneficiary verified",
    explanation: "The person or organisation the campaign is for completed FundZim's beneficiary check.",
  },
];

export const TRUST_DISCLAIMER =
  "Verified identity does not guarantee the campaign's claims. Checks confirm who people are, not that every statement in a story is true. Read carefully, and report anything that worries you.";

export function activeTrustIndicators(trust: PublicTrust): TrustIndicator[] {
  return INDICATORS.filter((i) => trust[i.key] === true);
}
