import { describe, expect, it } from "vitest";

import { pageMetadata, publicRobots } from "./metadata";
import { buildRobots, buildSitemap, DISALLOWED_PATHS, STATIC_PUBLIC_PATHS } from "./robots";

const origin = new URL("https://fundzim.test");

describe("robots.txt", () => {
  it("keeps every crawler out unless indexing is allowed", () => {
    expect(buildRobots(null, true)).toEqual({ rules: { userAgent: "*", disallow: "/" } });
    expect(buildRobots(origin, false)).toEqual({ rules: { userAgent: "*", disallow: "/" } });
  });

  it("when allowed: public site open, private areas disallowed, sitemap listed", () => {
    const robots = buildRobots(origin, true);
    expect(robots.sitemap).toBe("https://fundzim.test/sitemap.xml");
    const rules = robots.rules as { allow: string; disallow: string[] };
    expect(rules.allow).toBe("/");
    expect(rules.disallow).toEqual(["/dashboard", "/admin", "/login", "/register", "/settings", "/staff", "/verify-email", "/reset-password", "/forgot-password", "/api"]);
    expect(rules.disallow).toEqual([...DISALLOWED_PATHS]);
  });
});

describe("sitemap.xml", () => {
  it("is empty without an origin (no absolute URLs)", () => {
    expect(buildSitemap(null, { campaigns: [{ slug: "a-1", updated_at: null }], categories: [] })).toEqual([]);
  });

  it("lists static pages, categories and live campaigns once each, with absolute URLs", () => {
    const entries = buildSitemap(origin, {
      campaigns: [
        { slug: "help-chipo-abc123", updated_at: "2026-10-02T10:00:00Z" },
        { slug: "help-chipo-abc123", updated_at: "2026-10-02T10:00:00Z" },
      ],
      categories: [{ slug: "education", updated_at: "garbage" }],
    });
    const urls = entries.map((e) => e.url);
    for (const p of STATIC_PUBLIC_PATHS) expect(urls).toContain(new URL(p, origin).toString());
    expect(urls).toContain("https://fundzim.test/categories/education");
    expect(urls.filter((u) => u === "https://fundzim.test/campaigns/help-chipo-abc123")).toHaveLength(1);
    expect(entries.find((e) => e.url.endsWith("help-chipo-abc123"))?.lastModified).toEqual(new Date("2026-10-02T10:00:00Z"));
    expect(entries.find((e) => e.url.endsWith("/education"))?.lastModified).toBeUndefined();
    expect(urls.some((u) => /dashboard|admin|login|privacy|terms/.test(u))).toBe(false);
    expect(urls.every((u) => u.startsWith("https://fundzim.test/"))).toBe(true);
  });

  it("still lists static pages when the API is unavailable", () => {
    expect(buildSitemap(origin, null)).toHaveLength(STATIC_PUBLIC_PATHS.length);
  });
});

describe("page metadata", () => {
  const env = { PUBLIC_SITE_URL: "https://fundzim.test", SITE_INDEXING: "allow" };

  it("emits canonical, Open Graph and Twitter fields with absolute URLs", () => {
    const meta = pageMetadata({ title: "Help Chipo", description: "School fees.", path: "/campaigns/help-chipo-abc123", image: { path: "/api/v1/public/campaigns/help-chipo-abc123/media/m", alt: "Chipo" }, type: "article" }, env);
    expect(meta.alternates?.canonical).toBe("https://fundzim.test/campaigns/help-chipo-abc123");
    expect(meta.openGraph).toMatchObject({ url: "https://fundzim.test/campaigns/help-chipo-abc123", type: "article", images: [{ url: "https://fundzim.test/api/v1/public/campaigns/help-chipo-abc123/media/m", alt: "Chipo" }] });
    expect(meta.twitter).toMatchObject({ card: "summary_large_image", images: ["https://fundzim.test/api/v1/public/campaigns/help-chipo-abc123/media/m"] });
    expect(meta.robots).toEqual({ index: true, follow: true });
  });

  it("falls back to the generic FundZim image", () => {
    const meta = pageMetadata({ title: "About", description: "x", path: "/about" }, env);
    expect(meta.openGraph).toMatchObject({ images: [{ url: "https://fundzim.test/og/fundzim-og.png", width: 1200, height: 630 }] });
  });

  it("leaves URL fields out without PUBLIC_SITE_URL (never guessed) and is noindex", () => {
    const meta = pageMetadata({ title: "About", description: "x", path: "/about" }, {});
    expect(meta.alternates).toBeUndefined();
    expect(meta.openGraph).not.toHaveProperty("url");
    expect(meta.openGraph).not.toHaveProperty("images");
    expect(meta.twitter).toMatchObject({ card: "summary" });
    expect(JSON.stringify(meta)).not.toMatch(/example\.com|localhost/);
    expect(publicRobots({})).toMatchObject({ index: false });
  });
});
