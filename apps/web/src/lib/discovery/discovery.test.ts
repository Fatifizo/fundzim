import { afterEach, describe, expect, it, vi } from "vitest";

import { breadcrumbJsonLd, serializeJsonLd } from "@/lib/seo/json-ld";
import { pageMetadata } from "@/lib/seo/metadata";
import { buildSitemap } from "@/lib/seo/robots";
import { linkedinShareUrl, shareLinks, withReferral } from "@/lib/share/links";
import { referralCode, resetReferralCache } from "@/lib/share/referral";

import { readCampaignAnalytics, readDiscoveryPage, readLocations, readRecommendations, readTrending } from "./normalise";
import { discoveryApiParams, discoveryHref, EMPTY_QUERY, isFiltered, readDiscoveryQuery } from "./query";

const CARD = {
  slug: "help-tariro-abc1234567",
  title: "Help Tariro go to school",
  summary: "School fees for Tariro.",
  category: "EDUCATION",
  goal: { amount_minor: "150000", currency: "USD" },
  status: "ACTIVE",
  organiser: { type: "INDIVIDUAL", display_name: "Rudo" },
  trust: { campaign_reviewed: true },
  cover_media_id: null,
  published_at: "2026-10-19T10:00:00Z",
  location: { code: "TOWN_MUTARE", name: "Mutare", kind: "TOWN", province_code: "PROVINCE_MANICALAND", province_name: "Manicaland", province_slug: "manicaland", town_slug: "mutare" },
};

describe("discovery query", () => {
  it("keeps only valid, bounded values", () => {
    const q = readDiscoveryQuery({ q: `  school\u0007 ${"x".repeat(150)}`, category: "education", province: "manicaland", town: "mutare", status: "ended",
      published: "30d", organiser: "organisation", sort: "trending", cursor: "bm9wZQ" });
    expect([...q.q].length).toBeLessThanOrEqual(100);
    expect(q.q.startsWith("school")).toBe(true);
    expect(q).toMatchObject({ category: "education", province: "manicaland", town: "mutare", status: "ended", published: "30d", organiser: "organisation", sort: "trending", cursor: "bm9wZQ" });
  });

  it("drops anything unknown or unsafe instead of forwarding it", () => {
    const q = readDiscoveryQuery({ category: "../admin", province: "Harare Province", town: "a/b", status: "DRAFT", published: "2d", organiser: "company",
      sort: "popular", cursor: "a|b c" });
    expect(q).toEqual({ ...EMPTY_QUERY });
  });

  it("builds stable URLs without fixed or empty parameters", () => {
    const q = { ...EMPTY_QUERY, q: "school fees", category: "education", sort: "newest" as const };
    expect(discoveryHref("/campaigns", q)).toBe("/campaigns?q=school+fees&category=education&sort=newest");
    expect(discoveryHref("/categories/education", q, { cursor: "abc" }, ["category"])).toBe("/categories/education?q=school+fees&sort=newest&cursor=abc");
    expect(discoveryApiParams(q, 12).toString()).toBe("q=school+fees&category=education&sort=newest&limit=12");
    expect(isFiltered(q)).toBe(true);
    expect(isFiltered({ ...EMPTY_QUERY, category: "education" }, ["category"])).toBe(false);
  });
});

describe("discovery readers", () => {
  it("reads cards with location, reason and highlights that rebuild the text", () => {
    const page = readDiscoveryPage({
      items: [
        { ...CARD, reason: { code: "POPULAR_NOW", text: "Popular right now" }, highlights: { title: [{ text: "Help ", match: false }, { text: "Tariro", match: true }, { text: " go to school", match: false }], summary: [{ text: "School fees for Tariro.", match: false }] } },
        { ...CARD, slug: "Bad Slug" },
      ],
      next_cursor: "abc",
      total: { value: 1, capped: false },
      match: "EXACT",
      query: "tariro",
      sort: "relevance",
    });
    expect(page.items).toHaveLength(1);
    const c = page.items[0]!;
    expect(c.location).toMatchObject({ name: "Mutare", province_slug: "manicaland", town_slug: "mutare" });
    expect(c.reason).toEqual({ code: "POPULAR_NOW", text: "Popular right now" });
    expect(c.highlights?.title.map((s) => s.text).join("")).toBe(c.title);
    expect(page.nextCursor).toBe("abc");
  });

  it("ignores highlights that do not rebuild the exact text, unknown reasons and odd locations", () => {
    const [c] = readDiscoveryPage({
      items: [{ ...CARD, reason: { code: "PAID", text: "Sponsored" }, location: { name: "x", province_slug: "../etc" }, highlights: { title: [{ text: "Injected", match: true }], summary: [] } }],
    }).items;
    expect(c!.highlights).toBeNull();
    expect(c!.reason).toBeNull();
    expect(c!.location).toBeNull();
  });

  it("never shows recommendations that claim to be personalised", () => {
    expect(readRecommendations({ items: [CARD], personalised: true, rules: "x" }).items).toEqual([]);
    expect(readRecommendations({ items: [CARD], personalised: false, rules: "Fixed rules" }).items).toHaveLength(1);
  });

  it("reads trending and locations defensively", () => {
    expect(readTrending({ items: [CARD], rule: "r", policy_version: null, window_days: 7 })).toMatchObject({ rule: "r", policyVersion: null, windowDays: 7 });
    const l = readLocations({ provinces: [{ code: "PROVINCE_HARARE", kind: "PROVINCE", slug: "harare", name: "Harare", discoverable_campaigns: 4 }, { slug: "../x" }], towns: [], unspecified_campaigns: -3 });
    expect(l.provinces).toHaveLength(1);
    expect(l.provinces[0]!.count).toBe(4);
    expect(l.unspecified).toBe(0);
  });

  it("reads analytics as integers only, never money", () => {
    const a = readCampaignAnalytics({ totals: { views: 5, unique_visitors: 2.5, shares: "7" }, click_through_bp: 4285, daily: [{ day: "2026-10-19", views: 5, unique_visitors: 2, shares: 1 }],
      donations: { available: false }, definitions: { views: "Page views", evil: 1 } });
    expect(a.totals).toEqual({ views: 5, unique_visitors: 0, shares: 0 });
    expect(a.clickThroughBp).toBe(4285);
    expect(a.definitions).toEqual({ views: "Page views" });
  });
});

describe("share links and referral codes", () => {
  afterEach(() => resetReferralCache());

  it("adds LinkedIn and appends a well-formed referral code only", () => {
    const url = "https://fundzim.test/campaigns/help-tariro-abc1234567";
    expect(linkedinShareUrl({ url })).toBe(`https://www.linkedin.com/sharing/share-offsite/?url=${encodeURIComponent(url)}`);
    expect(shareLinks({ title: "t", summary: "s", url }).map((l) => l.channel)).toEqual(["WHATSAPP", "FACEBOOK", "X", "LINKEDIN", "EMAIL"]);
    expect(withReferral(url, "0123456789ab")).toBe(`${url}?ref=0123456789ab`);
    expect(withReferral(url, "../evil")).toBe(url);
    expect(withReferral(url, null)).toBe(url);
  });

  it("asks once per campaign and channel, caches, and never sends cookies", async () => {
    const fetch = vi.fn(async () => new Response(JSON.stringify({ data: { code: "0123456789ab", channel: "WHATSAPP", expires_at: "2027-01-01T00:00:00Z" } }), { status: 201 }));
    expect(await referralCode("a-1", "WHATSAPP", { fetch, now: 0 })).toBe("0123456789ab");
    expect(await referralCode("a-1", "WHATSAPP", { fetch, now: 1 })).toBe("0123456789ab");
    expect(fetch).toHaveBeenCalledTimes(1);
    const init = (fetch.mock.calls[0] as unknown as [string, RequestInit])[1];
    expect(init).toMatchObject({ method: "POST", credentials: "omit" });
    expect(JSON.parse(String(init.body))).toEqual({ slug: "a-1", channel: "WHATSAPP" });
  });

  it("falls back to no code when disabled, rate limited, slow, offline or malformed", async () => {
    const disabled = vi.fn(async () => new Response("{}", { status: 503 }));
    expect(await referralCode("a-1", "X", { fetch: disabled })).toBeNull();
    expect(await referralCode("a-1", "FACEBOOK", { fetch: disabled })).toBeNull();
    expect(disabled).toHaveBeenCalledTimes(1); // switched off: not asked again
    resetReferralCache();
    expect(await referralCode("a-1", "X", { fetch: vi.fn(async () => new Response("{}", { status: 429 })) })).toBeNull();
    expect(await referralCode("a-1", "X", { fetch: vi.fn(async () => { throw new TypeError("offline"); }) })).toBeNull();
    expect(await referralCode("a-1", "X", { fetch: vi.fn(async () => new Response(JSON.stringify({ data: { code: "<script>" } }), { status: 201 })) })).toBeNull();
    const slow = vi.fn((_: string, init?: RequestInit) => new Promise<Response>((_res, rej) => init?.signal?.addEventListener("abort", () => rej(new Error("aborted")))));
    expect(await referralCode("a-1", "X", { fetch: slow as unknown as typeof fetch, timeoutMs: 20 })).toBeNull();
  });
});

describe("SEO", () => {
  const env = { PUBLIC_SITE_URL: "https://fundzim.test", SITE_INDEXING: "allow" };

  it("listing variants are noindex with the canonical pointing at the unfiltered page", () => {
    const m = pageMetadata({ title: "Search", description: "d", path: "/campaigns", indexable: false }, env);
    expect(m.robots).toEqual({ index: false, follow: true });
    expect(m.alternates?.canonical).toBe("https://fundzim.test/campaigns");
    expect(pageMetadata({ title: "x", description: "d", path: "/campaigns" }, env).robots).toEqual({ index: true, follow: true });
  });

  it("generated preview images carry their dimensions", () => {
    const m = pageMetadata({ title: "x", description: "d", path: "/campaigns/a-1", image: { path: "/og/campaigns/a-1", alt: "a", width: 1200, height: 630 } }, env);
    expect(m.openGraph?.images).toEqual([{ url: "https://fundzim.test/og/campaigns/a-1", alt: "a", width: 1200, height: 630 }]);
  });

  it("sitemap lists provinces and never a filter permutation", () => {
    const entries = buildSitemap(new URL("https://fundzim.test"), { campaigns: [{ slug: "a-1", updated_at: null }], categories: [], provinces: [{ slug: "harare", updated_at: null }] });
    const urls = entries.map((e) => e.url);
    expect(urls).toContain("https://fundzim.test/campaigns/location/harare");
    expect(urls).toContain("https://fundzim.test/campaigns/trending");
    expect(urls.some((u) => u.includes("?"))).toBe(false);
  });

  it("breadcrumbs escape user text in the script body", () => {
    const ld = breadcrumbJsonLd([{ name: "Campaigns", path: "/campaigns" }, { name: "</script><script>alert(1)</script>", path: "/campaigns/a-1" }], new URL("https://fundzim.test"));
    const body = serializeJsonLd(ld);
    expect(body).not.toContain("</script>");
    expect(JSON.parse(body).itemListElement[1].item).toBe("https://fundzim.test/campaigns/a-1");
  });
});
