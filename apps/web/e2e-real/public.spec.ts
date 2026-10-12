import { expect, test, type Page } from "@playwright/test";

import { discoveryTitles } from "./support/browser";
import { PUBLIC_SITE_URL, SITE_INDEXING_ALLOWED, WEB_URL } from "./support/env";
import { publicGet } from "./support/http";
import { loadRun, type CampaignRef } from "./support/state";

/**
 * Public experience against the REAL stack (docs/stage-7/interface-contracts.md §3, §5). Read-only: the data
 * was provisioned by global setup (published, suspended, unlisted and draft campaigns, published / hidden /
 * pending updates, a 13-campaign catalogue sharing one search token). Runs on desktop and mobile Chromium.
 */

/** Canonical origin the web container is configured with (PUBLIC_SITE_URL). */
const SITE = PUBLIC_SITE_URL;

const run = () => loadRun();

function slugOf(c: CampaignRef): string {
  if (!c.slug) throw new Error(`fixture ${c.title} has no slug`);
  return c.slug;
}

/** Fails the test on uncaught page errors. */
function watchErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  return errors;
}

async function resultTitles(page: Page): Promise<string[]> {
  return discoveryTitles(page);
}

async function search(page: Page, q: string, categoryLabel?: string) {
  await page.goto("/campaigns");
  const form = page.getByRole("search", { name: "Search campaigns" });
  await form.getByRole("searchbox").fill(q);
  if (categoryLabel) await form.getByLabel("Category").selectOption({ label: categoryLabel });
  await form.getByRole("searchbox").press("Enter");
  await page.waitForURL(/\/campaigns\?/);
}

/** Expected share text, per the frontend's builder (lib/share/links.ts), for short titles and summaries. */
function expectedShareText(title: string, summary: string, url: string) {
  return ["Support this FundZim campaign:", title, summary, `Read the story: ${url}`].join("\n");
}

test("home page loads with navigation into discovery", async ({ page }) => {
  const errors = watchErrors(page);
  const response = await page.goto("/");
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.locator('a[href="/campaigns"]:visible').first()).toBeVisible();
  await expect(page.getByRole("list", { name: "Categories" }).first()).toBeVisible();
  expect(errors).toEqual([]);
});

test("discovery: search finds every catalogue campaign across keyset pages", async ({ page }) => {
  const errors = watchErrors(page);
  const { token, campaigns } = run().catalogue;
  await search(page, token);
  await expect(page.getByRole("status").filter({ hasText: /13 campaigns/ })).toBeVisible();
  const first = await resultTitles(page);
  expect(first).toHaveLength(12);
  await page.getByRole("navigation", { name: "Pagination" }).getByRole("link", { name: "Next page" }).click();
  await expect(page).toHaveURL(/cursor=/);
  const second = await resultTitles(page);
  expect(second).toHaveLength(1);
  expect([...first, ...second].sort()).toEqual(campaigns.map((c) => c.title).sort());
  await expect(page.getByRole("link", { name: "Next page" })).toHaveCount(0);
  await page.getByRole("link", { name: "Back to the first page" }).click();
  await expect(page).not.toHaveURL(/cursor=/);
  expect(await resultTitles(page)).toEqual(first);
  expect(errors).toEqual([]);
});

test("discovery: category filtering (search form and category page)", async ({ page }) => {
  const { token, byCategory, campaigns } = run().catalogue;
  await search(page, token, "Education");
  const edu = await resultTitles(page);
  expect(edu).toHaveLength(byCategory.EDUCATION!);
  expect(edu.sort()).toEqual(campaigns.filter((c) => c.category === "EDUCATION").map((c) => c.title).sort());

  await page.goto(`/categories/community?q=${encodeURIComponent(token)}`);
  await expect(page.getByRole("heading", { level: 1, name: "Community" })).toBeVisible();
  const com = await resultTitles(page);
  expect(com.sort()).toEqual(campaigns.filter((c) => c.category === "COMMUNITY").map((c) => c.title).sort());

  await page.goto("/categories");
  await expect(page.getByRole("heading", { level: 1, name: "Categories" })).toBeVisible();
  await expect(page.getByRole("link", { name: /Education/ }).first()).toBeVisible();
});

test("campaign page: approved content, no donations, canonical URL and the exact WhatsApp share link", async ({ page }) => {
  const errors = watchErrors(page);
  const c = run().campaigns.pubDetail;
  const slug = slugOf(c);
  const response = await page.goto(`/campaigns/${slug}`);
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { level: 1, name: c.title })).toBeVisible();
  await expect(page.getByRole("button", { name: "Donations are not yet available." })).toBeDisabled();

  const canonical = `${SITE}/campaigns/${slug}`;
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", canonical);
  await expect(page.locator('meta[property="og:url"]')).toHaveAttribute("content", canonical);

  const summary = (await publicGet<{ summary: string }>(`/public/campaigns/${slug}`)).data.summary;
  const whatsapp = page.getByRole("link", { name: /WhatsApp/ });
  const href = await whatsapp.getAttribute("href");
  expect(href).toBe(`https://wa.me/?text=${encodeURIComponent(expectedShareText(c.title, summary, canonical))}`);
  expect(decodeURIComponent(href!.slice("https://wa.me/?text=".length))).toContain(`Read the story: ${canonical}`);
  await expect(whatsapp).toHaveAttribute("target", "_blank");
  await expect(whatsapp).toHaveAttribute("rel", /noopener/);
  await expect(page.getByRole("link", { name: /Facebook/ })).toHaveAttribute("href", `https://www.facebook.com/sharer/sharer.php?u=${encodeURIComponent(canonical)}`);
  expect(errors).toEqual([]);
});

test("copy link puts the canonical URL on the clipboard", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: WEB_URL });
  const slug = slugOf(run().campaigns.pubDetail);
  await page.goto(`/campaigns/${slug}`);
  await page.getByRole("button", { name: "Copy link" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Link copied" })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(`${SITE}/campaigns/${slug}`);
});

test("suspended campaigns are gone: 404 page, absent from discovery and the public API", async ({ page }) => {
  const c = run().campaigns.pubSuspended;
  const response = await page.goto(`/campaigns/${slugOf(c)}`);
  expect(response?.status()).toBe(404);
  await expect(page.getByRole("heading", { level: 1, name: "We couldn't find that page" })).toBeVisible();
  await expect(page.locator("body")).not.toContainText(c.title);
  await search(page, c.title);
  expect(await resultTitles(page)).not.toContain(c.title);
  expect((await publicGet(`/public/campaigns/${slugOf(c)}`)).status).toBe(404);
});

test("unlisted campaigns stay out of discovery but open by link", async ({ page }) => {
  const c = run().campaigns.pubUnlisted;
  await search(page, c.title);
  expect(await resultTitles(page)).not.toContain(c.title);
  await page.goto(`/categories/education?q=${encodeURIComponent(c.title)}`);
  expect(await resultTitles(page)).not.toContain(c.title);
  const response = await page.goto(`/campaigns/${slugOf(c)}`);
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { level: 1, name: c.title })).toBeVisible();
});

test("only approved updates are public: hidden and pending-moderation updates never appear", async ({ page }) => {
  const r = run();
  const detail = slugOf(r.campaigns.pubDetail);
  await page.goto(`/campaigns/${detail}`);
  const updates = page.locator("section", { has: page.getByRole("heading", { name: "Updates", exact: true }) });
  await expect(updates.getByRole("heading", { name: r.updates.visibleTitle })).toBeVisible();
  await expect(page.locator("body")).not.toContainText(r.updates.hiddenTitle);
  expect(await page.content()).not.toContain(r.updates.hiddenTitle);

  const medical = slugOf(r.campaigns.pubMedical);
  await page.goto(`/campaigns/${medical}`);
  await expect(page.getByRole("heading", { level: 1, name: r.campaigns.pubMedical.title })).toBeVisible();
  expect(await page.content()).not.toContain(r.updates.pendingTitle);

  const apiDetail = await publicGet(`/public/campaigns/${detail}/updates`);
  expect(apiDetail.text).toContain(r.updates.visibleTitle);
  expect(apiDetail.text).not.toContain(r.updates.hiddenTitle);
  expect((await publicGet(`/public/campaigns/${medical}/updates`)).text).not.toContain(r.updates.pendingTitle);
});

test("private data stays private: no beneficiary name, owner email, legal name or internal ids in the HTML", async ({ page, request }) => {
  const r = run();
  const forbidden = [
    r.owner.email,
    r.owner.id,
    r.owner.beneficiaryId,
    r.owner.legalLastName,
    `${r.owner.legalFirstName} ${r.owner.legalLastName}`,
    r.linkedStaff.id,
  ];
  for (const c of [r.campaigns.pubDetail, r.campaigns.pubMedical, r.campaigns.modStandard]) {
    const slug = c.slug ?? (await publicGet<{ items: { slug: string; title: string }[] }>(`/public/campaigns?q=${encodeURIComponent(c.title)}`)).data.items.find((x) => x.title === c.title)?.slug;
    if (!slug) continue; // modStandard is published by the staff spec (desktop project) only
    await page.goto(`/campaigns/${slug}`);
    await expect(page.getByRole("heading", { level: 1, name: c.title })).toBeVisible();
    // The SELF beneficiary with disclosure NONE: no name, "Details kept private".
    await expect(page.getByText("Details kept private")).toBeVisible();
    await expect(page.getByText(r.owner.displayName).first()).toBeVisible(); // public organiser display name
    const html = await (await request.get(`/campaigns/${slug}`)).text();
    for (const value of [...forbidden, c.id]) {
      expect(html, `public HTML of ${slug} must not contain ${value}`).not.toContain(value);
      expect(await page.content()).not.toContain(value);
    }
    const api = await publicGet(`/public/campaigns/${slug}`);
    for (const value of [...forbidden, c.id]) expect(api.text, `public API of ${slug}`).not.toContain(value);
  }
});

test(`robots.txt and noindex follow SITE_INDEXING (${SITE_INDEXING_ALLOWED ? "allow" : "disallow"})`, async ({ page, request }) => {
  const robots = await request.get("/robots.txt");
  expect(robots.status()).toBe(200);
  expect(robots.headers()["content-type"]).toContain("text/plain");
  const rb = await robots.text();
  expect(rb).toMatch(/^User-Agent: \*$/im);
  await page.goto(`/campaigns/${slugOf(run().campaigns.pubDetail)}`);
  const robotsMeta = page.locator('meta[name="robots"]');
  if (SITE_INDEXING_ALLOWED) {
    expect(rb).toMatch(/^Allow: \/$/im);
    for (const p of ["/dashboard", "/admin", "/login", "/register", "/settings", "/staff", "/api"]) expect(rb).toMatch(new RegExp(`^Disallow: ${p}$`, "im"));
    expect(rb).not.toMatch(/^Disallow: \/$/im);
    expect(rb).toContain(`Sitemap: ${SITE}/sitemap.xml`);
    // Public pages are indexable; private areas still carry noindex.
    expect(((await robotsMeta.count()) ? await robotsMeta.first().getAttribute("content") : "") ?? "").not.toMatch(/noindex/);
    await page.goto("/login");
    await expect(page.locator('meta[name="robots"]').first()).toHaveAttribute("content", /noindex/);
  } else {
    expect(rb).toMatch(/^Disallow: \/$/im);
    await expect(robotsMeta.first()).toHaveAttribute("content", /noindex/);
  }
});

test("sitemap.xml lists live public campaigns and categories, never drafts, suspended or unlisted campaigns", async ({ request }) => {
  const r = run();
  const sitemap = await request.get("/sitemap.xml");
  expect(sitemap.status()).toBe(200);
  expect(sitemap.headers()["content-type"]).toMatch(/xml/);
  const xml = await sitemap.text();
  expect(xml).toContain("<urlset");
  const loc = (path: string) => `<loc>${SITE}${path}</loc>`;
  expect(xml).toContain(loc("/campaigns"));
  expect(xml).toContain(loc("/categories/education"));
  expect(xml).toContain(loc(`/campaigns/${slugOf(r.campaigns.pubDetail)}`));
  for (const c of r.catalogue.campaigns) expect(xml).toContain(loc(`/campaigns/${slugOf(c)}`));
  expect(xml).not.toContain(slugOf(r.campaigns.pubSuspended));
  expect(xml).not.toContain(slugOf(r.campaigns.pubUnlisted));
  if (r.campaigns.pubDraft.slug) expect(xml).not.toContain(r.campaigns.pubDraft.slug);
  for (const value of [r.owner.email, r.owner.id, "/dashboard", "/admin"]) expect(xml).not.toContain(value);
});

test("short links /c/{code} and non-canonical slugs redirect permanently to the canonical campaign URL", async ({ page, request }) => {
  const c = run().campaigns.pubDetail;
  const slug = slugOf(c);
  const pub = await publicGet<{ public_code: string; canonical_slug: string }>(`/public/campaigns/${slug}`);
  const code = pub.data.public_code;
  expect(pub.data.canonical_slug).toBe(slug);
  expect(slug.endsWith(`-${code}`)).toBe(true);

  for (const from of [`/c/${code}`, `/campaigns/a-completely-different-title-${code}`]) {
    const res = await request.get(from, { maxRedirects: 0 });
    expect([301, 308], from).toContain(res.status());
    expect(new URL(res.headers().location!, WEB_URL).pathname, from).toBe(`/campaigns/${slug}`);
    await page.goto(from);
    await expect(page).toHaveURL(new RegExp(`/campaigns/${slug}$`));
    await expect(page.getByRole("heading", { level: 1, name: c.title })).toBeVisible();
  }
  // Codes of campaigns that are not public are the app's 404.
  const hidden = await publicGet<{ items: unknown[] }>(`/public/campaigns/${slugOf(run().campaigns.pubSuspended)}`);
  expect(hidden.status).toBe(404);
  const suspendedCode = slugOf(run().campaigns.pubSuspended).split("-").pop()!;
  expect((await request.get(`/c/${suspendedCode}`, { maxRedirects: 0 })).status()).toBe(404);
});
