import { describe, expect, it } from "vitest";

import { REPORT_REASONS, reportPayload, validateReport } from "./report";

const base = { reason: "MISLEADING_INFORMATION", details: "", contactEmail: "", website: "" };

describe("report validation", () => {
  it("offers exactly the contract's reasons", () => {
    expect(REPORT_REASONS.map((r) => r.value)).toEqual(["MISLEADING_INFORMATION", "IMPERSONATION", "SUSPICIOUS_ACTIVITY", "INAPPROPRIATE_CONTENT", "PRIVACY_CONCERN", "OTHER"]);
  });

  it("needs a known reason", () => {
    expect(validateReport({ ...base, reason: "" })).toEqual({ reason: "Choose a reason." });
    expect(validateReport({ ...base, reason: "SPAM" })).toHaveProperty("reason");
    expect(validateReport(base)).toEqual({});
  });

  it("requires 10–2000 characters of details for OTHER only", () => {
    expect(validateReport({ ...base, reason: "OTHER", details: "too short" })).toHaveProperty("details");
    expect(validateReport({ ...base, reason: "OTHER", details: "  " + "a".repeat(9) + "  " })).toHaveProperty("details");
    expect(validateReport({ ...base, reason: "OTHER", details: "a".repeat(10) })).toEqual({});
    expect(validateReport({ ...base, details: "a".repeat(2001) })).toHaveProperty("details");
    expect(validateReport({ ...base, details: "😀".repeat(2000) })).toEqual({});
  });

  it("refuses HTML and unsafe links in details (plain text rules)", () => {
    expect(validateReport({ ...base, details: "see <script>x</script>" }).details).toMatch(/plain text/);
    expect(validateReport({ ...base, details: "javascript:alert(1)" }).details).toMatch(/javascript/);
  });

  it("checks the optional email only when given", () => {
    expect(validateReport({ ...base, contactEmail: "not-an-email" })).toHaveProperty("contact_email");
    expect(validateReport({ ...base, contactEmail: "me@example.test" })).toEqual({});
    expect(validateReport({ ...base, contactEmail: `${"a".repeat(250)}@x.co` })).toHaveProperty("contact_email");
  });
});

describe("report payload", () => {
  it("leaves empty optional fields out and trims", () => {
    expect(reportPayload({ ...base, details: "  details  ", contactEmail: " me@example.test " })).toEqual({ reason: "MISLEADING_INFORMATION", details: "details", contact_email: "me@example.test" });
    expect(reportPayload(base)).toEqual({ reason: "MISLEADING_INFORMATION" });
  });

  it("passes a filled honeypot through so the API can drop it silently", () => {
    expect(reportPayload({ ...base, website: "http://spam" })).toMatchObject({ website: "http://spam" });
  });
});
