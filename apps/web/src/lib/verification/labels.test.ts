import { describe, expect, it } from "vitest";

import {
  caseStatusLabel,
  deriveComplianceActions,
  deriveReviewActions,
  documentTypeOptions,
  humanise,
  identityComplete,
  kycNextStep,
  kycProgress,
  ownershipLabel,
  scanStatusLabel,
  sidesFor,
} from "./labels";
import { CASE_STATUSES, SCAN_STATUSES, type KycCase } from "./types";

function kycCase(overrides: Partial<KycCase> = {}): KycCase {
  return {
    id: "c1",
    kind: "KYC",
    status: "DRAFT",
    target_level: "IDENTITY_VERIFIED",
    policy_version: 1,
    identity: null,
    documents: [],
    requirements: [{ document_type: "ZW_NATIONAL_ID", sides: ["FRONT", "BACK"], satisfied: false }],
    information_requests: [],
    decision: null,
    submitted_at: null,
    created_at: "2026-10-01T08:00:00Z",
    updated_at: "2026-10-01T08:00:00Z",
    version: 1,
    ...overrides,
  };
}

const FULL_IDENTITY = {
  legal_first_name: "Chipo",
  legal_last_name: "Ncube",
  date_of_birth: "1990-04-18",
  nationality: "ZW",
  country_of_residence: "ZW",
  id_document_type: "ZW_NATIONAL_ID" as const,
  id_document_number_masked: "••••A 12",
  id_document_expiry: null,
  residential_address: { line1: "1 Road", line2: null, city: "Harare", province: null, postal_code: null, country: "ZW" },
};

describe("status labels", () => {
  it("every ADR-034 case status and scan status has a text label (never just a code)", () => {
    for (const status of CASE_STATUSES) expect(caseStatusLabel(status).label).not.toBe(status);
    for (const status of SCAN_STATUSES) expect(scanStatusLabel(status).label).not.toBe(status);
    expect(caseStatusLabel(null).label).toBe("Not started");
  });

  it("users see escalation neutrally; reviewers see it as such", () => {
    expect(caseStatusLabel("ESCALATED").label).toBe("Under additional review");
    expect(caseStatusLabel("ESCALATED", "reviewer").label).toBe("Escalated to compliance");
  });

  it("unknown codes are humanised, not hidden", () => {
    expect(caseStatusLabel("SOMETHING_NEW").label).toBe("Something new");
    expect(humanise("kyc.case_submitted")).toBe("Kyc case submitted");
  });

  it("PROVIDER_CONFIRMATION_REQUIRED is described honestly", () => {
    const label = ownershipLabel("PROVIDER_CONFIRMATION_REQUIRED");
    expect(label.label).toBe("Provider confirmation required");
    expect(label.description).toMatch(/not available yet/);
    expect(label.description).not.toMatch(/confirmed\./i);
  });
});

describe("document options", () => {
  it("puts requirement types first and de-duplicates", () => {
    const opts = documentTypeOptions("KYC_CASE", [{ document_type: "ZW_PASSPORT", sides: [], satisfied: false }]);
    expect(opts[0]).toBe("ZW_PASSPORT");
    expect(new Set(opts).size).toBe(opts.length);
    expect(opts).toContain("ZW_NATIONAL_ID");
  });

  it("national IDs default to front and back; requirements override", () => {
    expect(sidesFor("ZW_NATIONAL_ID")).toEqual(["FRONT", "BACK"]);
    expect(sidesFor("ZW_PASSPORT")).toEqual([]);
    expect(sidesFor("X", [{ document_type: "X", sides: ["SINGLE"], satisfied: false }])).toEqual(["SINGLE"]);
  });
});

describe("KYC progress and next step", () => {
  it("no case → start verification", () => {
    expect(kycNextStep(null, "BASIC_VERIFIED")).toMatchObject({ action: "Start verification", href: "/dashboard/verification/identity" });
    expect(kycProgress(null).map((s) => s.state)).toEqual(["current", "todo", "todo", "todo", "todo"]);
  });

  it("draft without details → details; with details → documents; all done → review and submit", () => {
    expect(kycNextStep(kycCase(), "BASIC_VERIFIED").title).toBe("Add your personal details");
    expect(kycNextStep(kycCase({ identity: FULL_IDENTITY }), "BASIC_VERIFIED").title).toBe("Upload your documents");
    const ready = kycCase({ identity: FULL_IDENTITY, requirements: [{ document_type: "ZW_NATIONAL_ID", sides: ["FRONT", "BACK"], satisfied: true }] });
    expect(kycNextStep(ready, "BASIC_VERIFIED").title).toBe("Check and submit");
    expect(kycProgress(ready).map((s) => s.state)).toEqual(["done", "done", "current", "todo", "todo"]);
  });

  it("additional information required points at the requests", () => {
    expect(kycNextStep(kycCase({ status: "ADDITIONAL_INFORMATION_REQUIRED" }), "BASIC_VERIFIED").href).toContain("#requests");
  });

  it("final cases offer a new verification; verified users have nothing to do", () => {
    expect(kycNextStep(kycCase({ status: "REJECTED" }), "BASIC_VERIFIED").action).toBe("Start a new verification");
    expect(kycNextStep(kycCase({ status: "APPROVED" }), "IDENTITY_VERIFIED").title).toBe("Your identity is verified");
  });

  it("identityComplete requires every required field", () => {
    expect(identityComplete(FULL_IDENTITY)).toBe(true);
    expect(identityComplete({ ...FULL_IDENTITY, id_document_number_masked: null })).toBe(false);
    expect(identityComplete(null)).toBe(false);
  });
});

describe("reviewer action derivation (ADR-034 §1)", () => {
  const derive = (status: string, assignedToMe: boolean, extra: Partial<Parameters<typeof deriveReviewActions>[0]> = {}) =>
    deriveReviewActions({ type: "KYC", status, assignedToMe, hasPendingApproval: false, ...extra });

  it("unassigned reviewers can only assign", () => {
    expect(derive("SUBMITTED", false)).toEqual(["assign"]);
    expect(derive("UNDER_REVIEW", false)).toEqual(["assign"]);
    expect(derive("REJECTED", false)).toEqual([]);
  });

  it("assigned reviewers follow the state machine", () => {
    expect(derive("SUBMITTED", true)).toEqual(["start-review"]);
    expect(derive("UNDER_REVIEW", true)).toEqual(["request-info", "approve", "reject", "escalate"]);
    expect(derive("ESCALATED", true)).toEqual(["approve", "reject", "return"]);
    expect(derive("APPROVED", true)).toEqual(["suspend", "revoke"]);
    expect(derive("SUSPENDED", true)).toEqual(["reinstate", "reopen", "revoke"]);
    expect(derive("ADDITIONAL_INFORMATION_REQUIRED", true)).toEqual([]);
  });

  it("payout destinations: assignment starts the review; suspend/reinstate only", () => {
    const dest = (status: string, mine: boolean) => deriveReviewActions({ type: "PAYOUT_DESTINATION", status, assignedToMe: mine, hasPendingApproval: false });
    expect(dest("PENDING_VERIFICATION", false)).toEqual(["assign"]);
    expect(dest("PENDING_VERIFICATION", true)).toEqual(["request-info", "approve", "reject"]);
    expect(dest("VERIFIED", true)).toEqual(["suspend"]);
    expect(dest("SUSPENDED", true)).toEqual(["reinstate"]);
  });

  it("four-eyes: the second approval is offered to someone other than the first approver", () => {
    expect(derive("UNDER_REVIEW", false, { hasPendingApproval: true })).toEqual(["second-approval"]);
    expect(derive("UNDER_REVIEW", true, { hasPendingApproval: true, pendingApprovalByMe: true })).toEqual(["reject"]);
    expect(derive("UNDER_REVIEW", false, { hasPendingApproval: true, pendingApprovalByMe: true })).toEqual([]);
  });
});

describe("compliance action derivation (ADR-034 §5)", () => {
  it("maker-checker on resolutions", () => {
    expect(deriveComplianceActions({ status: "RESOLVED", assignedToMe: true, resolutionPending: true, proposedByMe: true })).toEqual(["reopen"]);
    expect(deriveComplianceActions({ status: "RESOLVED", assignedToMe: false, resolutionPending: true, proposedByMe: false })).toEqual(["approve-resolution"]);
    expect(deriveComplianceActions({ status: "RESOLVED", assignedToMe: true, resolutionPending: false, proposedByMe: true })).toEqual(["close", "reopen"]);
  });

  it("open cases are assigned, then started", () => {
    expect(deriveComplianceActions({ status: "OPEN", assignedToMe: false, resolutionPending: false, proposedByMe: false })).toEqual(["assign"]);
    expect(deriveComplianceActions({ status: "ASSIGNED", assignedToMe: true, resolutionPending: false, proposedByMe: false })).toEqual(["start"]);
    expect(deriveComplianceActions({ status: "IN_REVIEW", assignedToMe: true, resolutionPending: false, proposedByMe: false })).toEqual(["request-info", "escalate", "resolve"]);
    expect(deriveComplianceActions({ status: "ESCALATED", assignedToMe: true, resolutionPending: false, proposedByMe: false })).toEqual(["start", "resolve"]);
  });
});

describe("requirements in either shape", () => {
  it("normalises the backend's alternatives shape and the contract shape", async () => {
    const { normaliseRequirements, requirementLabel } = await import("./labels");
    const backend = normaliseRequirements([{ document_types: ["ZW_NATIONAL_ID", "ZW_PASSPORT", "FOREIGN_PASSPORT"], satisfied: false }, { document_types: ["SELFIE"], satisfied: true }]);
    expect(backend[0]).toEqual({ document_type: "ZW_NATIONAL_ID", document_types: ["ZW_NATIONAL_ID", "ZW_PASSPORT", "FOREIGN_PASSPORT"], sides: [], satisfied: false });
    expect(requirementLabel(backend[0]!)).toBe("Zimbabwe national ID, Zimbabwe passport or Passport (other country)");
    const contract = normaliseRequirements([{ document_type: "ZW_NATIONAL_ID", sides: ["FRONT", "BACK"], satisfied: true }]);
    expect(requirementLabel(contract[0]!)).toBe("Zimbabwe national ID (front and back)");
    expect(normaliseRequirements(null)).toEqual([]);
    expect(documentTypeOptions("KYC_CASE", backend as never).slice(0, 4)).toEqual(["ZW_NATIONAL_ID", "ZW_PASSPORT", "FOREIGN_PASSPORT", "SELFIE"]);
    expect(sidesFor("ZW_PASSPORT", backend)).toEqual([]);
  });
});
