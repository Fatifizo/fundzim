import { describe, expect, it } from "vitest";

import { isCursor, isSlug, isUuid, ownerMediaUrl, publicCampaignPath, publicMediaUrl } from "./paths";

const ID = "0192f0c4-7a1b-7c3d-8e4f-0123456789ab";

describe("slug and id guards", () => {
  it("accepts well-formed slugs only", () => {
    expect(isSlug("help-chipo-with-school-fees-7k3m9q2x4b")).toBe(true);
    expect(isSlug("abc")).toBe(true);
    for (const bad of ["", "Abc", "a--b", "-a", "a-", "a/b", "../x", "a%2Fb", "a b", "a.b", "<script>", "ä", "x".repeat(161), "a\nb"]) {
      expect(isSlug(bad), JSON.stringify(bad)).toBe(false);
    }
  });

  it("accepts canonical UUIDs only", () => {
    expect(isUuid(ID)).toBe(true);
    for (const bad of ["", "1", `${ID}x`, ID.replace(/-/g, ""), `../${ID}`, `${ID}/media`]) expect(isUuid(bad), bad).toBe(false);
  });

  it("builds media URLs only from valid identifiers", () => {
    expect(publicMediaUrl("my-campaign-abc", ID)).toBe(`/api/v1/public/campaigns/my-campaign-abc/media/${ID}`);
    expect(publicMediaUrl("../admin", ID)).toBeNull();
    expect(publicMediaUrl("ok", "../../x")).toBeNull();
    expect(ownerMediaUrl(ID, ID)).toBe(`/api/v1/campaigns/${ID}/media/${ID}/content`);
    expect(ownerMediaUrl("x", ID)).toBeNull();
    expect(publicCampaignPath("abc-1")).toBe("/campaigns/abc-1");
    expect(publicCampaignPath("javascript:alert(1)")).toBeNull();
    expect(publicCampaignPath(null)).toBeNull();
  });

  it("bounds cursors to printable ASCII", () => {
    expect(isCursor("2026-10-09T10:00:00Z|abc")).toBe(true);
    expect(isCursor("a b")).toBe(false);
    expect(isCursor("x".repeat(513))).toBe(false);
  });
});
