import { describe, expect, it } from "vitest";

import { campaignStatusLabel, nextEligibilityAction, ownerActions, staffActions } from "./labels";

const review = (o: Partial<{ assigned_to: { id: string; display_name: string } | null; pending_outcome: string | null; pending_decided_by: string | null }> = {}) => ({
  assigned_to: null,
  pending_outcome: null,
  pending_decided_by: null,
  ...o,
});

describe("campaign labels and actions", () => {
  it("labels statuses with text and keeps unknown ones visible", () => {
    expect(campaignStatusLabel("ACTIVE").label).toBe("Published");
    expect(campaignStatusLabel("SUBMITTED").label).toBe("Waiting for review");
    expect(campaignStatusLabel("FROZEN").label).toBe("Frozen");
  });

  it("offers owner actions per ADR-036 edges", () => {
    expect(ownerActions("DRAFT")).toEqual(["submit", "cancel"]);
    expect(ownerActions("APPROVED")).toContain("publish");
    expect(ownerActions("ACTIVE")).toEqual(["pause", "complete", "cancel"]);
    expect(ownerActions("PAUSED")).toContain("resume");
    expect(ownerActions("ARCHIVED")).toEqual([]);
    expect(ownerActions("SUSPENDED")).toEqual([]);
    expect(nextEligibilityAction("PAUSED")).toBe("REACTIVATE");
    expect(nextEligibilityAction("ACTIVE")).toBeNull();
  });

  it("never offers second approval to the first approver", () => {
    const base = { status: "UNDER_REVIEW", meId: "me" };
    expect(staffActions({ ...base, review: review({ pending_outcome: "APPROVE", pending_decided_by: "me" }) })).not.toContain("second-approval");
    expect(staffActions({ ...base, review: review({ pending_outcome: "APPROVE", pending_decided_by: "other" }) })).toContain("second-approval");
    expect(staffActions({ ...base, review: review() })).not.toContain("second-approval");
    // Even when the API lists it.
    expect(staffActions({ ...base, review: review({ pending_outcome: "APPROVE", pending_decided_by: "me" }) }, ["second-approval", "reject"])).toEqual(["reject"]);
  });

  it("derives staff actions from status", () => {
    expect(staffActions({ status: "SUBMITTED", meId: "me", review: review() })).toContain("assign");
    expect(staffActions({ status: "SUBMITTED", meId: "me", review: review({ assigned_to: { id: "me", display_name: "Me" } }) })).toContain("start-review");
    expect(staffActions({ status: "APPROVED", meId: "me", review: review() })).toContain("publish");
    expect(staffActions({ status: "SUSPENDED", meId: "me", review: review() })).toContain("reactivate");
    expect(staffActions({ status: "REJECTED", meId: "me", review: review() })).toEqual(["reopen"]);
  });
});
