import { expect, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, createUser, cspViolations, seriousAxeViolations, signIn, signInStaff, TOTP, trackCspViolations, watchForBreakage, test } from "./helpers";

// Stage 16 discovery against the mock API (e2e/mock-api/discovery.mjs): location browsing, filters, trending and featured
// pages, recommendations, owner analytics and the location editor, staff discovery pages with maker-checker ranking
// policies, SEO behaviour of listing variants, generated preview images and mobile layout.
test.use({ baseURL: AUTH_BASE_URL });

let counter = 0;
const token = () => `zq${Date.now().toString(36)}${process.pid.toString(36)}${counter++}`;

async function owner(page: Page) {
  return createUser(page, { phoneVerified: true, kycLevel: "IDENTITY_VERIFIED", displayName: "Tendai Organiser" });
}

async function seed(page: Page, email: string, data: Record<string, unknown> = {}) {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/campaign`, { data: { email, status: "ACTIVE", ...data } });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: { id: string; slug: string; public_code: string } }).data;
}

/** Views from distinct visitors (straight to the mock API, which keys visitors by address). */
async function views(page: Page, slug: string, visitors: number) {
  const api = `http://127.0.0.1:${process.env.MOCK_API_PORT ?? 3199}`;
  for (let i = 0; i < visitors; i++) {
    const res = await page.request.post(`${api}/api/v1/discovery/events`, {
      data: { events: [{ type: "CAMPAIGN_VIEW", slug, source: "DIRECT" }] },
      headers: { "X-Forwarded-For": `198.51.100.${(counter * 7 + i) % 250}`, "User-Agent": "Mozilla/5.0 (Linux; Android 13) Chrome/120 Mobile Safari/537.36" },
    });
    expect(res.status()).toBe(204);
  }
}

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function stepUpIfAsked(page: Page) {
  const stepUp = page.getByRole("dialog", { name: "Confirm it's you" });
  await stepUp.getByLabel("Authentication code").fill(TOTP);
  await stepUp.getByRole("button", { name: "Confirm" }).click();
}

test("locations: browse by province, filter by town, card and page show the coarse place; filtered listings are noindex", async ({ page }) => {
  const problems = watchForBreakage(page);
  await trackCspViolations(page);
  const email = await owner(page);
  const word = token();
  const mutare = await seed(page, email, { title: `Clinic beds ${word} Mutare`, category: "MEDICAL", location_code: "TOWN_MUTARE" });
  await seed(page, email, { title: `School desks ${word} Harare`, category: "EDUCATION", location_code: "PROVINCE_HARARE" });

  await page.goto("/campaigns");
  await expect(page.getByRole("navigation", { name: "Browse by province" }).getByRole("link", { name: /Manicaland/ })).toBeVisible();
  await page.getByRole("navigation", { name: "Browse by province" }).getByRole("link", { name: /Manicaland/ }).click();
  await expect(page).toHaveURL(/\/campaigns\/location\/manicaland$/);
  await expect(page.getByRole("heading", { level: 1, name: "Manicaland province" })).toBeVisible();
  await page.getByRole("searchbox", { name: "Search" }).fill(word);
  await page.getByRole("button", { name: "Search" }).click();
  const cards = page.getByRole("list", { name: "Campaigns" }).locator(":scope > li");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText("Mutare, Manicaland");
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", `${AUTH_BASE_URL}/campaigns/location/manicaland`);
  expect(await axe(page)).toEqual([]);

  await page.getByRole("navigation", { name: "Towns in Manicaland" }).getByRole("link", { name: "Mutare" }).click();
  await expect(page).toHaveURL(/town=mutare/);
  await expect(page.getByRole("heading", { level: 1, name: "Mutare, Manicaland" })).toBeVisible();

  // the directory's province filter and "more filters"
  await page.goto(`/campaigns?q=${word}&province=harare`);
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText("Harare province");
  await page.goto(`/campaigns?q=${word}&status=ended`);
  await expect(page.getByText("No campaigns found")).toBeVisible();
  await expect(page.locator("details")).toHaveAttribute("open", "");

  await page.goto(`/campaigns/${mutare.slug}`);
  await expect(page.getByText("Where:")).toBeVisible();
  await expect(page.getByRole("link", { name: "Mutare, Manicaland" })).toHaveAttribute("href", "/campaigns/location/manicaland?town=mutare");
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("trending and featured pages explain their rules; recommendations say why; nothing claims money or endorsement", async ({ page }) => {
  const problems = watchForBreakage(page);
  const email = await owner(page);
  const word = token();
  const hot = await seed(page, email, { title: `Water tank ${word}`, category: "COMMUNITY" });
  const other = await seed(page, email, { title: `Community garden ${word}`, category: "COMMUNITY" });
  await views(page, hot.slug, 4);

  await page.goto("/campaigns/trending");
  await expect(page.getByRole("heading", { level: 1, name: "Popular right now" })).toBeVisible();
  await expect(page.getByText(/Money is never part of the ranking/)).toBeVisible();
  await expect(page.getByRole("list", { name: "Popular campaigns" }).getByRole("link", { name: `Water tank ${word}` })).toBeVisible();
  await expect(page.locator("main")).not.toContainText(/raised|donors|funded/i);
  expect(await axe(page)).toEqual([]);

  await page.goto("/campaigns/featured");
  await expect(page.getByRole("heading", { level: 2, name: "What featured means" })).toBeVisible();
  await expect(page.getByText(/nobody can pay to be featured/)).toBeVisible();

  await page.goto(`/campaigns/${other.slug}`);
  const more = page.getByRole("region", { name: "More campaigns" }).or(page.locator("section", { has: page.getByRole("heading", { name: "More campaigns" }) }));
  await expect(more.getByText(/Nothing about you is used/)).toBeVisible();
  await expect(more.getByText("More in this category").first()).toBeVisible();
  await expect(more.getByRole("link", { name: `Community garden ${word}` })).toHaveCount(0);
  expect(problems).toEqual([]);
});

test("aliases: /campaigns/search and /campaigns/category/<slug> redirect permanently to the canonical pages", async ({ page }) => {
  const search = await page.request.get("/campaigns/search?q=water&sort=bogus", { maxRedirects: 0 });
  expect(search.status()).toBe(308);
  expect(search.headers().location).toBe("/campaigns?q=water");
  const category = await page.request.get("/campaigns/category/medical", { maxRedirects: 0 });
  expect(category.status()).toBe(308);
  expect(category.headers().location).toBe("/categories/medical");
  expect((await page.request.get("/campaigns/category/Not%20A%20Slug", { maxRedirects: 0 })).status()).toBe(404);
});

test("preview image: generated for a public campaign without a cover; 404 once it is not public", async ({ page, request }) => {
  const email = await owner(page);
  const { id, slug } = await seed(page, email, { title: `Preview card ${token()}` });
  const res = await request.get(`/og/campaigns/${slug}`);
  expect(res.status()).toBe(200);
  expect(res.headers()["content-type"]).toBe("image/png");
  expect(res.headers()["cache-control"]).toBe("public, no-cache");
  expect((await res.body()).subarray(1, 4).toString("latin1")).toBe("PNG");
  const etag = res.headers().etag!;
  expect((await request.get(`/og/campaigns/${slug}`, { headers: { "If-None-Match": etag } })).status()).toBe(304);
  await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/campaign/${id}/status`, { data: { status: "SUSPENDED" } });
  const gone = await request.get(`/og/campaigns/${slug}`);
  expect(gone.status()).toBe(404);
  expect(gone.headers()["cache-control"]).toBe("no-store");
  expect((await request.get("/og/campaigns/..%2Fadmin")).status()).toBe(404);
});

test("owner: chooses a location in the editor and reads the analytics page; others get a 404", async ({ page }) => {
  const problems = watchForBreakage(page);
  const email = await owner(page);
  const { id } = await seed(page, email, { title: `Analytics campaign ${token()}`, status: "DRAFT" });
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto(`/dashboard/campaigns/${id}/edit`);
  const where = page.getByLabel("Where is the cause? (optional)");
  await expect(where).toHaveValue("");
  await where.selectOption("TOWN_GWERU");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Changes saved" })).toBeVisible();
  await page.reload();
  await expect(where).toHaveValue("TOWN_GWERU");

  await page.goto(`/dashboard/campaigns/${id}/analytics?days=7`);
  await expect(page.getByRole("heading", { level: 1, name: "Analytics" })).toBeVisible();
  await expect(page.getByText(/They are not donations/)).toBeVisible();
  await expect(page.getByRole("link", { name: "Last 7 days" })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("table", { name: "Views, unique visitors and shares per day" })).toBeVisible();
  expect(await axe(page)).toEqual([]);

  await page.context().clearCookies();
  const stranger = await createUser(page, { phoneVerified: true });
  await signIn(page, stranger);
  await expect(page).toHaveURL(/\/dashboard$/);
  expect((await page.goto(`/dashboard/campaigns/${id}/analytics`))?.status()).toBe(404);
  expect(problems).toEqual([]);
});

test("staff: discovery overview and ranking policies with maker-checker", async ({ page }) => {
  const problems = watchForBreakage(page);
  const admin = await createStaff(page, ["ADMIN"], "Ada Admin");
  const officer = await createStaff(page, ["COMPLIANCE"], "Farai Compliance");
  await signInStaff(page, admin, "/admin/discovery");
  await expect(page.getByRole("heading", { level: 1, name: "Discovery" })).toBeVisible();
  await expect(page.getByText("Non-production data")).toBeVisible();
  await expect(page.getByRole("heading", { level: 2, name: "Search index" })).toBeVisible();
  expect(await axe(page)).toEqual([]);

  await page.goto("/admin/discovery/ranking");
  await expect(page.getByText("Engineering default: no policy has been approved yet.").or(page.getByText(/Approved policy version/))).toBeVisible();
  await page.getByLabel("Weight of unique sharers").fill("30");
  const reason = `Shares are a stronger signal than views (e2e ${token()}).`; // the mock API is shared by parallel projects
  await page.getByLabel("Why (staff only)").fill(reason);
  await page.getByRole("button", { name: "Submit proposal" }).click();
  await stepUpIfAsked(page);
  await expect(page.getByRole("status").filter({ hasText: "Proposal submitted." })).toBeVisible();
  const row = page.getByRole("row").filter({ hasText: reason });
  await expect(row).toContainText("Submitted");
  expect(await axe(page)).toEqual([]);

  await page.context().clearCookies();
  await signInStaff(page, officer, "/admin/discovery/ranking");
  await row.getByRole("button", { name: "Approve" }).click();
  const dialog = page.getByRole("dialog", { name: "Approve and activate this version?" });
  await dialog.getByLabel("Note (staff only)").fill("Checked the bounds and the reason.");
  await dialog.getByRole("button", { name: "Confirm: Approve" }).click();
  await stepUpIfAsked(page);
  await expect(page.getByRole("status").filter({ hasText: "Version approved and active." })).toBeVisible();
  await expect(row).toContainText("Active");
  expect(problems).toEqual([]);
});

test("mobile: the directory, province page and campaign page fit the screen; filters stay usable", async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 740 });
  const email = await owner(page);
  const { slug } = await seed(page, email, { title: `Narrow screen ${token()}`, location_code: "PROVINCE_BULAWAYO" });
  for (const path of ["/campaigns", "/campaigns/location/bulawayo", `/campaigns/${slug}`, "/campaigns/trending"]) {
    await page.goto(path);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), path).toBe(true);
  }
  await page.goto("/campaigns");
  await page.getByText("More filters").click();
  await expect(page.getByLabel("Status")).toBeVisible();
  const box = await page.getByRole("button", { name: "Search" }).boundingBox();
  expect(box!.height).toBeGreaterThanOrEqual(44); // touch target
});

test("poor connectivity: going offline is announced and the page stays readable; nothing is queued", async ({ page, context }) => {
  const email = await owner(page);
  const { slug } = await seed(page, email, { title: `Offline notice ${token()}` });
  await page.goto(`/campaigns/${slug}`);
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/api/v1/discovery/")) sent.push(r.url());
  });
  await context.setOffline(true);
  await expect(page.getByRole("status").filter({ hasText: "You are offline." }).first()).toBeVisible();
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await context.setOffline(false);
  await expect(page.getByRole("status").filter({ hasText: "You are offline." })).toHaveCount(0);
  expect(sent).toEqual([]); // no replay of anything sent while offline
});

test("poster: a printable page with a QR code of the short link; only for public campaigns; never a money claim", async ({ page }) => {
  const problems = watchForBreakage(page);
  await trackCspViolations(page);
  const email = await owner(page);
  const title = `Poster campaign ${token()}`;
  const { id, slug, public_code } = await seed(page, email, { title, location_code: "TOWN_MUTARE" });
  await page.goto(`/campaigns/${slug}`);
  await page.getByRole("link", { name: "Print a poster with a QR code" }).click();
  await expect(page).toHaveURL(new RegExp(`/campaigns/${slug}/poster$`));
  await expect(page.getByRole("heading", { level: 1, name: title })).toBeVisible();
  const shortUrl = `${AUTH_BASE_URL}/c/${public_code}`;
  await expect(page.getByRole("img", { name: `QR code linking to ${shortUrl}` })).toBeVisible();
  await expect(page.getByText(shortUrl, { exact: true })).toBeVisible();
  await expect(page.getByText("Education · Mutare, Manicaland")).toBeVisible();
  await expect(page.locator("main")).not.toContainText(/raised|donors|funded|US\$/i);
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);
  await expect(page.getByRole("button", { name: "Print this poster" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
  await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/campaign/${id}/status`, { data: { status: "SUSPENDED" } });
  expect((await page.goto(`/campaigns/${slug}/poster`))?.status()).toBe(404);
  expect(problems).toEqual([]);
});
