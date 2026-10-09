import { describe, expect, it } from "vitest";

import { reasonGuidance, reasonsFromDetails } from "./eligibility";

const r = (code: string, field: string | null = null) => ({ code, field, message: null });

describe("eligibility reason mapping", () => {
  it("links each fixable reason to the place to fix it", () => {
    const ctx = { campaignId: "c1", stepHref: (s: number) => `/dashboard/campaigns/new?id=c1&step=${s}`, returnTo: "/dashboard/campaigns/new?id=c1&step=7" };
    expect(reasonGuidance(r("AGE_ATTESTATION_REQUIRED"), ctx).fix?.href).toBe("/dashboard/verification/age?next=%2Fdashboard%2Fcampaigns%2Fnew%3Fid%3Dc1%26step%3D7");
    expect(reasonGuidance(r("IDENTITY_VERIFICATION_REQUIRED"), ctx).fix?.href).toBe("/dashboard/verification/identity");
    expect(reasonGuidance(r("BENEFICIARY_NOT_VERIFIED"), ctx).fix?.href).toBe("/dashboard/verification/beneficiaries");
    expect(reasonGuidance(r("BENEFICIARY_REQUIRED"), ctx).fix?.href).toBe("/dashboard/campaigns/new?id=c1&step=5");
    expect(reasonGuidance(r("COVER_IMAGE_REQUIRED"), ctx).fix?.href).toBe("/dashboard/campaigns/new?id=c1&step=6");
    expect(reasonGuidance(r("CURRENCY_NOT_AVAILABLE"), ctx).fix?.href).toBe("/dashboard/campaigns/new?id=c1&step=4");
    expect(reasonGuidance(r("MISSING_FIELD", "story"), ctx).fix?.href).toBe("/dashboard/campaigns/new?id=c1&step=3");
    expect(reasonGuidance(r("CATEGORY_INACTIVE"), ctx).fix?.href).toBe("/dashboard/campaigns/new?id=c1&step=1");
  });

  it("uses campaign pages outside the wizard and organisation links when known", () => {
    expect(reasonGuidance(r("COVER_IMAGE_REQUIRED"), { campaignId: "c1" }).fix?.href).toBe("/dashboard/campaigns/c1/media");
    expect(reasonGuidance(r("BENEFICIARY_REQUIRED"), { campaignId: "c1" }).fix?.href).toBe("/dashboard/campaigns/c1/beneficiaries");
    expect(reasonGuidance(r("ORGANISATION_VERIFICATION_REQUIRED"), { organisationId: "o1" }).fix?.href).toBe("/dashboard/organisations/o1/verification");
    expect(reasonGuidance(r("AGE_ATTESTATION_REQUIRED")).fix?.href).toBe("/dashboard/verification/age");
  });

  it("keeps restrictions generic and unknown codes visible", () => {
    const restricted = reasonGuidance({ code: "ACCOUNT_RESTRICTED", field: null, message: "Suspected fraud case 123" });
    expect(restricted.fix).toBeNull();
    expect(`${restricted.title} ${restricted.description}`).not.toMatch(/fraud|case|compliance|suspend/i);
    expect(reasonGuidance(r("SOMETHING_NEW")).title).toBe("Something needs attention");
  });

  it("reads NOT_ELIGIBLE details defensively", () => {
    expect(reasonsFromDetails([{ code: "COVER_IMAGE_REQUIRED", field: "" }, { field: "x" }, { code: "MISSING_FIELD", field: "title", message: "m" }])).toEqual([
      { code: "COVER_IMAGE_REQUIRED", field: null, message: null },
      { code: "MISSING_FIELD", field: "title", message: "m" },
    ]);
  });
});
