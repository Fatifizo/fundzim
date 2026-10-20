import { describe, expect, it } from "vitest";

import { readCampaigns, readCanonicalSlug, readMedia, readNextCursor, readPublicCampaign, readPublicCategories, readPublicSummaries, readPublicUpdates, readSitemap, readStaffSummaries, readTotal, readTrust, readUpdates, withCategoryName } from "./normalise";
import { isCategoryFilter, isPublicCode } from "./paths";
import { activeTrustIndicators, TRUST_DISCLAIMER } from "./trust";

describe("Stage 7 list shapes (contract §2)", () => {
  it("reads {items, next_cursor} for owner, staff, media and update lists", () => {
    expect(readCampaigns({ items: [{ id: "c1", title: "T" }], next_cursor: "x" })).toHaveLength(1);
    expect(readNextCursor({ items: [], next_cursor: "2026-10-01T00:00:00Z|c1" })).toBe("2026-10-01T00:00:00Z|c1");
    expect(readNextCursor({ items: [], next_cursor: null })).toBeNull();
    expect(readStaffSummaries({ items: [{ id: "c1", title: "T" }], next_cursor: null })).toHaveLength(1);
    expect(readMedia({ items: [{ id: "m1", kind: "COVER", version: 3 }], next_cursor: null })[0]).toMatchObject({ id: "m1", version: 3 });
    expect(readUpdates({ items: [{ id: "u1", title: "T", version: 2 }], next_cursor: null })[0]).toMatchObject({ id: "u1", version: 2 });
  });
});

describe("GET /campaign-categories (Stage 7 shape)", () => {
  it("reads {items, next_cursor} with requires_organisation", async () => {
    const { readCategories } = await import("./normalise");
    expect(readCategories({ items: [{ code: "CHARITY", slug: "charity", name: "Registered charity", requires_organisation: true, live_campaigns: 0 }], next_cursor: null })).toEqual([
      { code: "CHARITY", name: "Registered charity", description: null, requires_organisation: true },
    ]);
  });
});

describe("public campaign detail", () => {
  const raw = {
    slug: "old-title-abc123",
    canonical_slug: "help-chipo-abc123",
    public_code: "abc123",
    title: "Help Chipo",
    summary: "S",
    story: "Story",
    category: "EDUCATION",
    category_name: "Education",
    goal: { amount_minor: "150000", currency: "USD" },
    status: "ACTIVE",
    organiser: { type: "ORGANISATION", display_name: "Harare Trust" },
    beneficiary: { disclosed: false },
    cover_media_id: "00000000-0000-0000-0000-000000000001",
    media_ids: ["00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"],
    media: [
      { id: "00000000-0000-0000-0000-000000000001", kind: "COVER", alt_text: "Chipo at school", variants: [{ width: 640, height: 360 }, { width: 320, height: 180 }] },
      { id: "00000000-0000-0000-0000-000000000002", kind: "GALLERY", alt_text: "Classroom" },
    ],
    trust: { organiser_identity_verified: true, organisation_verified: "true", campaign_reviewed: true },
  };

  it("reads canonical slug, public code, category name, alt texts and trust", () => {
    const c = readPublicCampaign(raw);
    expect(c.canonical_slug).toBe("help-chipo-abc123");
    expect(c.public_code).toBe("abc123");
    expect(c.category).toEqual({ code: "EDUCATION", name: "Education", slug: null });
    expect(c.cover).toEqual({ id: raw.cover_media_id, alt_text: "Chipo at school", widths: [320, 640] });
    // No `variants` field (older API): widths unknown.
    expect(c.gallery).toEqual([{ id: raw.media_ids[1], alt_text: "Classroom", widths: null }]);
    expect(c.organisation).toEqual({ display_name: "Harare Trust" });
    // Only an explicit true counts.
    expect(c.trust).toEqual({ organiser_identity_verified: true, organisation_verified: false, beneficiary_verified: false, campaign_reviewed: true });
  });

  it("defaults the canonical slug to the slug", () => {
    expect(readPublicCampaign({ ...raw, canonical_slug: undefined }).canonical_slug).toBe("old-title-abc123");
  });

  it("adds the category slug from the categories list", () => {
    const c = withCategoryName(readPublicCampaign(raw), [{ code: "EDUCATION", name: "Education", slug: "education" }]);
    expect(c.category).toEqual({ code: "EDUCATION", name: "Education", slug: "education" });
  });
});

describe("cards, totals and featured", () => {
  it("reads organiser, trust and featured selection", () => {
    const [card] = readPublicSummaries({ items: [{ slug: "a-1", title: "T", category: "MEDICAL", organiser: { type: "INDIVIDUAL", display_name: "Tendai" }, trust: { campaign_reviewed: true }, selection: "FEATURED" }] });
    expect(card).toMatchObject({ organiser: { display_name: "Tendai" }, trust: { campaign_reviewed: true, organiser_identity_verified: false }, selection: "FEATURED" });
    expect(readPublicSummaries({ items: [{ slug: "a-1", selection: "MOST_POPULAR" }] })[0]?.selection).toBeNull();
  });

  it("reads exact and capped totals", () => {
    expect(readTotal({ items: [], total: { value: 7, capped: false } })).toEqual({ value: 7, capped: false });
    expect(readTotal({ items: [], total: { value: 1000, capped: true } })).toEqual({ value: 1000, capped: true });
    expect(readTotal({ items: [] }, { total: 3 })).toEqual({ value: 3, capped: false });
    expect(readTotal({ items: [], total: { value: -1 } })).toBeNull();
    expect(readTotal({ items: [] })).toBeNull();
  });
});

describe("categories, updates, sitemap, short links", () => {
  it("reads categories with slugs and live counts, sorted by display order", () => {
    const cats = readPublicCategories({
      items: [
        { code: "MEDICAL", slug: "medical", name: "Medical", display_order: 20, sensitive: true, live_campaigns: 3 },
        { code: "EDUCATION", slug: "education", name: "Education", display_order: 10, live_campaigns: 0 },
        { code: "BAD", slug: "../x", name: "Bad" },
        { code: "OFF", slug: "off", active: false },
      ],
      next_cursor: null,
    });
    expect(cats.map((c) => [c.slug, c.live_campaigns, c.sensitive])).toEqual([
      ["education", 0, false],
      ["medical", 3, true],
    ]);
  });

  it("reads public updates with media ids", () => {
    expect(readPublicUpdates({ items: [{ id: "u1", title: "T", body: "B", media_ids: ["m1", 4], published_at: "2026-10-01T00:00:00Z" }], next_cursor: null })).toEqual([
      { id: "u1", title: "T", body: "B", media_ids: ["m1"], published_at: "2026-10-01T00:00:00Z" },
    ]);
  });

  it("drops malformed sitemap slugs", () => {
    expect(readSitemap({ campaigns: [{ slug: "ok-1", updated_at: "x" }, { slug: "../../admin" }, { slug: 4 }], categories: [{ slug: "medical" }] })).toEqual({
      campaigns: [{ slug: "ok-1", updated_at: "x" }],
      categories: [{ slug: "medical", updated_at: null }],
    });
  });

  it("validates canonical slugs and short codes", () => {
    expect(readCanonicalSlug({ canonical_slug: "help-chipo-abc123" })).toBe("help-chipo-abc123");
    expect(readCanonicalSlug({ canonical_slug: "https://evil.example" })).toBeNull();
    expect(isPublicCode("ABC123DEFG")).toBe(true);
    expect(isPublicCode("abc123defg")).toBe(true);
    expect(isPublicCode("abc/../x")).toBe(false);
    expect(isCategoryFilter("MEDICAL")).toBe(true);
    expect(isCategoryFilter("child-welfare")).toBe(true);
    expect(isCategoryFilter("Medical stuff")).toBe(false);
  });
});


describe("trust indicators", () => {
  it("lists only the true indicators, and the disclaimer never promises claims are true", () => {
    expect(activeTrustIndicators(readTrust({ campaign_reviewed: true, beneficiary_verified: true })).map((i) => i.key)).toEqual(["campaign_reviewed", "beneficiary_verified"]);
    expect(activeTrustIndicators(readTrust(null))).toEqual([]);
    expect(TRUST_DISCLAIMER).toContain("Verified identity does not guarantee the campaign's claims");
  });
});
