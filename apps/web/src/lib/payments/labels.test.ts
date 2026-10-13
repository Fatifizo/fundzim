import { describe, expect, it } from "vitest";

import { buildQuery, pick, reasonProblem, refundActions, statusTone } from "./labels";

describe("payment labels", () => {
  it("offers refund decisions to non-requesters only, withdraw to the requester only", () => {
    expect(refundActions("PENDING_APPROVAL", false)).toEqual(["approve", "reject", "hold"]);
    expect(refundActions("PENDING_APPROVAL", true)).toEqual(["withdraw"]);
    expect(refundActions("ON_HOLD", false)).toEqual(["resume", "reject"]);
    expect(refundActions("ON_HOLD", true)).toEqual([]);
    for (const s of ["APPROVED", "REJECTED", "WITHDRAWN", "EXECUTING", "SOMETHING_NEW"]) expect(refundActions(s, false), s).toEqual([]);
  });

  it("reason length 3–2000 code points", () => {
    expect(reasonProblem("ab")).not.toBeNull();
    expect(reasonProblem("  abc  ")).toBeNull();
    expect(reasonProblem("x".repeat(2001))).not.toBeNull();
  });

  it("filters accept allowed values only and build safe query strings", () => {
    expect(pick("UNKNOWN", ["UNKNOWN", "FAILED"] as const)).toBe("UNKNOWN");
    expect(pick(["FAILED", "x"], ["UNKNOWN", "FAILED"] as const)).toBe("FAILED");
    expect(pick("../x", ["UNKNOWN"] as const)).toBeUndefined();
    expect(buildQuery({ status: "UNKNOWN", method: undefined, cursor: "a&b" })).toBe("?status=UNKNOWN&cursor=a%26b");
    expect(buildQuery({})).toBe("");
  });

  it("status tones never rely on colour alone (text is always the status)", () => {
    expect(statusTone("SUCCEEDED")).toBe("brand");
    expect(statusTone("UNKNOWN")).toBe("gold");
    expect(statusTone("NEW_STATUS")).toBe("neutral");
  });
});
