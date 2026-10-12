import { describe, expect, it } from "vitest";

import { readFeaturedPicks, readStaffReports } from "./normalise";

describe("staff report queue reader", () => {
  it("reads the Go ReportItem shape and never carries an email", () => {
    const [r] = readStaffReports({
      items: [{ id: "r1", campaign_id: "c1", campaign_title: "T", campaign_slug: "t-1", status: "OPEN", reason: "OTHER", details: "x", has_contact: true, reporter_signed_in: false, created_at: "2026-10-09T10:00:00Z", triaged_at: null, triage_reason_code: null, triage_note: null, campaign_status: "ACTIVE", contact_email: "leak@example.test" }],
      next_cursor: null,
    });
    expect(r).toMatchObject({ id: "r1", has_contact: true, reporter_signed_in: false, campaign_status: "ACTIVE" });
    expect(JSON.stringify(r)).not.toContain("leak@example.test");
  });

  it("drops malformed rows", () => {
    expect(readStaffReports({ items: [{ id: 1 }, { id: "r2" }] })).toEqual([]);
  });
});

describe("featured picks reader", () => {
  it("sorts by position and treats only shown: true as public", () => {
    const picks = readFeaturedPicks({
      items: [
        { campaign_id: "b", title: "B", position: 5, shown: "true" },
        { campaign_id: "a", title: "A", position: 1, shown: true, note: "n" },
      ],
      next_cursor: null,
    });
    expect(picks.map((p) => [p.campaign_id, p.shown])).toEqual([
      ["a", true],
      ["b", false],
    ]);
  });
});
