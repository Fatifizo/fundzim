import { describe, expect, it } from "vitest";

import { campaignJsonLd, FORBIDDEN_JSON_LD_KEYS, organizationJsonLd, serializeJsonLd } from "./json-ld";

const origin = new URL("https://fundzim.test");

function keysDeep(value: unknown, out: string[] = []): string[] {
  if (Array.isArray(value)) value.forEach((v) => keysDeep(v, out));
  else if (value && typeof value === "object")
    for (const [k, v] of Object.entries(value)) {
      out.push(k);
      keysDeep(v, out);
    }
  return out;
}

function typesDeep(value: unknown, out: string[] = []): string[] {
  if (Array.isArray(value)) value.forEach((v) => typesDeep(v, out));
  else if (value && typeof value === "object")
    for (const [k, v] of Object.entries(value)) {
      if (k === "@type") out.push(String(v));
      else typesDeep(v, out);
    }
  return out;
}

describe("Organization JSON-LD (home page)", () => {
  it("names FundZim with absolute URL and logo", () => {
    expect(organizationJsonLd(origin)).toEqual({
      "@context": "https://schema.org",
      "@type": "Organization",
      name: "FundZim",
      url: "https://fundzim.test/",
      logo: "https://fundzim.test/icons/icon-512.png",
      description: expect.any(String),
    });
  });
});

describe("campaign JSON-LD", () => {
  const data = campaignJsonLd(
    { slug: "help-chipo-abc123", title: "Help Chipo", summary: "School fees.", imageUrl: "https://fundzim.test/api/v1/public/campaigns/help-chipo-abc123/media/x", publishedAt: "2026-10-01T10:00:00Z", categoryName: "Education" },
    origin,
  );

  it("is a WebPage with canonical URL, image, date and publisher", () => {
    expect(data).toMatchObject({
      "@context": "https://schema.org",
      "@type": "WebPage",
      "@id": "https://fundzim.test/campaigns/help-chipo-abc123",
      url: "https://fundzim.test/campaigns/help-chipo-abc123",
      name: "Help Chipo",
      description: "School fees.",
      datePublished: "2026-10-01T10:00:00.000Z",
      primaryImageOfPage: { "@type": "ImageObject", url: expect.stringMatching(/^https:\/\/fundzim\.test\//) },
      publisher: { "@type": "Organization", name: "FundZim" },
    });
  });

  it("never carries donation actions, money, ratings, reviews or certifications", () => {
    const keys = keysDeep(data);
    for (const k of FORBIDDEN_JSON_LD_KEYS) expect(keys).not.toContain(k);
    expect(typesDeep(data).every((t) => ["WebPage", "WebSite", "Organization", "ImageObject", "Thing"].includes(t))).toBe(true);
    expect(JSON.stringify(data)).not.toMatch(/DonateAction|MonetaryAmount|Rating|Review|amount_minor|US\$/);
  });

  it("omits optional fields that are unknown or invalid", () => {
    const bare = campaignJsonLd({ slug: "x-1", title: "T", summary: "S", imageUrl: null, publishedAt: "not a date", categoryName: null }, origin);
    expect(bare).not.toHaveProperty("primaryImageOfPage");
    expect(bare).not.toHaveProperty("datePublished");
    expect(bare).not.toHaveProperty("about");
  });
});

describe("serializeJsonLd", () => {
  it("cannot be broken out of by campaign text and still round-trips", () => {
    const data = campaignJsonLd({ slug: "x-1", title: "</script><script>alert(1)</script> & \u2028", summary: "<!-- -->", imageUrl: null, publishedAt: null, categoryName: null }, origin);
    const out = serializeJsonLd(data);
    expect(out).not.toMatch(/[<>&\u2028\u2029]/);
    expect(JSON.parse(out)).toEqual(data);
  });
});
