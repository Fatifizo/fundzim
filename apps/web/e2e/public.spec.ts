import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createUser, cspViolations, seriousAxeViolations, trackCspViolations, watchForBreakage } from "./helpers";

// Stage 7 public experience against the mock API (e2e/mock-api/campaigns.mjs). This app instance has
// PUBLIC_SITE_URL = its own origin and SITE_INDEXING=allow (e2e/serve.mjs).
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

async function mockControl<T>(page: Page, path: string, data: Record<string, unknown> = {}): Promise<T> {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/${path}`, { data });
  expect(res.ok()).toBe(true);
  return ((await res.json()) as { data: T }).data;
}

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function noHorizontalOverflow(page: Page) {
  return page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
}

test("home: hero, featured campaigns (approved placement first), categories with counts, steps, trust, footer", async ({ page }) => {
  const problems = watchForBreakage(page);
  await trackCspViolations(page);
  const email = await owner(page);
  const title = `Borehole for Chiweshe ${token()}`;
  const { id } = await seed(page, email, { title, category: "COMMUNITY" });
  await mockControl(page, "discovery/feature", { add: id });

  const response = await page.goto("/");
  expect(response?.status()).toBe(200);
  await expect(page.getByRole("heading", { level: 1, name: "Together, We Can Make a Difference." })).toBeVisible();
  await expect(page.getByText("Support people and causes across Zimbabwe through simple, transparent fundraising.")).toBeVisible();
  const featured = page.getByRole("list", { name: "Featured campaigns" });
  await expect(featured.getByRole("link").first()).toHaveText(title);
  await expect(page.getByText(/not an endorsement or a ranking/)).toBeVisible();
  const categories = page.getByRole("list", { name: "Categories" });
  await expect(categories.getByRole("link", { name: /Community/ })).toHaveAttribute("href", "/categories/community");
  await expect(categories.getByRole("link", { name: /Community/ })).toContainText(/live campaign/);
  await expect(page.getByRole("heading", { level: 3, name: "Step 4: Receive support when payments become available" })).toBeVisible();
  await expect(page.locator("main")).not.toContainText(/raised|\d+ donors|funded/i);
  const footer = page.getByRole("navigation", { name: "Footer" });
  for (const name of ["About", "How It Works", "Privacy", "Terms", "Contact", "Safety"]) await expect(footer.getByRole("link", { name, exact: true })).toBeVisible();

  // Organization structured data, nonced like every script.
  const jsonLd = JSON.parse((await page.locator('script[type="application/ld+json"]').first().textContent()) ?? "{}");
  expect(jsonLd).toMatchObject({ "@type": "Organization", name: "FundZim", url: `${AUTH_BASE_URL}/` });
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", `${AUTH_BASE_URL}/`);
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /^index, follow/);
  await expect(page.locator('link[rel="manifest"]')).toHaveAttribute("href", "/manifest.webmanifest");

  expect(await axe(page)).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
  await mockControl(page, "discovery/feature", { remove: id });
});

test("discovery: search with highlights, result count, category filter, sort and keyset pagination; the query text is never sent as analytics", async ({ page }) => {
  const problems = watchForBreakage(page);
  const email = await owner(page);
  const word = token();
  for (let i = 0; i < 13; i++) await seed(page, email, { title: `Clinic roof ${word} number ${i}`, category: i === 0 ? "MEDICAL" : "COMMUNITY" });

  await page.goto("/campaigns");
  await expect(page.getByRole("heading", { level: 1, name: "Explore campaigns" })).toBeVisible();
  await page.getByRole("searchbox", { name: "Search" }).fill(word);
  const analytics: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/api/v1/discovery/events")) analytics.push(r.postData() ?? "");
  });
  await page.getByRole("button", { name: "Search" }).click();
  await expect(page).toHaveURL(new RegExp(`q=${word}`));
  await expect(page.getByText("13 campaigns", { exact: true })).toBeVisible();
  const list = page.getByRole("list", { name: "Campaigns" });
  const cards = list.locator(":scope > li");
  await expect(cards).toHaveCount(12);
  await expect(list.getByText("By Tendai Organiser").first()).toBeVisible();
  await expect(list.getByText("Identity verified").first()).toBeVisible();
  await expect(page.locator("progress, [role=progressbar]")).toHaveCount(0);
  expect(await axe(page)).toEqual([]);

  // matched words are marked; searches are counted by the API, never sent (or stored) as text by the browser
  await expect(list.locator("mark").first()).toHaveText(word);
  expect(analytics.join("")).not.toContain(word);

  await page.getByRole("link", { name: "Next page" }).click();
  await expect(page).toHaveURL(/cursor=/);
  await expect(cards).toHaveCount(1);
  await expect(page.getByRole("link", { name: "Next page" })).toHaveCount(0);
  await page.getByRole("link", { name: "Back to the first page" }).click();
  await expect(cards).toHaveCount(12);

  await page.getByLabel("Category").selectOption("medical");
  await page.getByLabel("Sort by").selectOption("oldest");
  await page.getByRole("button", { name: "Search" }).click();
  await expect(page).toHaveURL(/category=medical/);
  await expect(page.getByText("1 campaign", { exact: true })).toBeVisible();
  await expect(cards.getByRole("link")).toHaveText(`Clinic roof ${word} number 0`);

  // a result click is counted (aggregate) and the campaign page reports where the view came from
  await cards.getByRole("link").first().click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText(`Clinic roof ${word} number 0`);
  await page.goto("/campaigns?q=vvvwwwxxxyyy"); // nothing matches, not even approximately
  await expect(page.getByText("No campaigns found")).toBeVisible();
  await expect(page.getByText("0 campaigns", { exact: true })).toBeVisible();
  expect(problems).toEqual([]);
});

test("discovery: an API failure shows an error state (API-less app instance)", async ({ page }) => {
  // Server-side fetches cannot be intercepted from the browser, so this uses the app whose API is down.
  const site = process.env.E2E_PORT ? `http://127.0.0.1:${process.env.E2E_PORT}` : "http://127.0.0.1:3100";
  await page.goto(`${site}/campaigns`);
  await expect(page.getByText("Campaigns could not be loaded")).toBeVisible();
  await expect(page.getByRole("link", { name: "Try again" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
});

test("categories: list with live counts, category page with description and listing, unknown slug 404", async ({ page }) => {
  const problems = watchForBreakage(page);
  const email = await owner(page);
  const title = `School shoes for Mbare ${token()}`;
  await seed(page, email, { title, category: "EDUCATION" });

  await page.goto("/categories");
  await expect(page.getByRole("heading", { level: 1, name: "Categories" })).toBeVisible();
  const education = page.getByRole("listitem").filter({ has: page.getByRole("heading", { name: "Education" }) });
  await expect(education).toContainText(/\d+ live campaigns?/);
  expect(await axe(page)).toEqual([]);
  await education.getByRole("link", { name: "Education" }).click();

  await expect(page).toHaveURL(/\/categories\/education$/);
  await expect(page.getByRole("heading", { level: 1, name: "Education" })).toBeVisible();
  await expect(page.getByText("School fees, uniforms and learning costs.")).toBeVisible();
  await expect(page.getByRole("link", { name: title })).toBeVisible();
  await expect(page).toHaveTitle("Education campaigns · FundZim");
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", `${AUTH_BASE_URL}/categories/education`);
  expect(await axe(page)).toEqual([]);

  await page.goto("/categories/medical");
  await expect(page.getByText("Please be respectful")).toBeVisible();

  const missing = await page.goto("/categories/not-a-category");
  expect(missing?.status()).toBe(404);
  expect(problems).toEqual([]);
});

test("detail: canonical redirect, short link, trust, updates with photos, metadata, JSON-LD, disabled donations", async ({ page }) => {
  const problems = watchForBreakage(page);
  await trackCspViolations(page);
  const email = await owner(page);
  const title = `Help Rufaro walk again ${token()}`;
  const { id, slug, public_code } = await seed(page, email, { title, category: "MEDICAL", story: "First paragraph with https://evil.example/link.\n\nSecond paragraph\nwith a line break." });
  await mockControl(page, "update", { campaign_id: id, title: "Surgery booked" });

  // Old slug with the same public code → permanent redirect to the canonical slug.
  const redirect = await page.request.get(`/campaigns/an-older-title-${public_code}`, { maxRedirects: 0 });
  expect(redirect.status()).toBe(308);
  expect(redirect.headers().location).toBe(`/campaigns/${slug}`);
  const short = await page.request.get(`/c/${public_code.toUpperCase()}`, { maxRedirects: 0 });
  expect(short.status()).toBe(308);
  expect(short.headers().location).toBe(`/campaigns/${slug}`);
  expect((await page.request.get("/c/ZZZZZZZZZZ", { maxRedirects: 0 })).status()).toBe(404);

  await page.goto(`/c/${public_code}`);
  await expect(page).toHaveURL(new RegExp(`/campaigns/${slug}$`));
  await expect(page.getByRole("heading", { level: 1, name: title })).toBeVisible();
  const story = page.locator("section", { has: page.getByRole("heading", { name: "The story" }) });
  await expect(story.locator("p")).toHaveCount(2);
  await expect(story.locator("a")).toHaveCount(0);
  await expect(story).toContainText("https://evil.example/link.");
  await expect(page.getByRole("button", { name: "Donations are not yet available." })).toBeDisabled();
  await expect(page.getByText("Organiser's identity verified")).toBeVisible();
  await expect(page.getByText(/Verified identity does not guarantee the campaign's claims/)).toBeVisible();
  await expect(page.getByRole("heading", { level: 3, name: "Surgery booked" })).toBeVisible();
  await expect(page.getByRole("img", { name: /Photo 1 from the update/ })).toBeVisible();
  // Responsive covers: width variants via ?w=, sized for the layout; the server answers each listed width.
  const cover = page.locator("main img").first();
  const srcset = (await cover.getAttribute("srcset"))!;
  expect(srcset).toMatch(/\?w=320 320w, .*\?w=640 640w, .*\?w=1024 1024w$/);
  expect(await cover.getAttribute("sizes")).toContain("736px");
  for (const candidate of srcset.split(", ").map((c) => c.split(" ")[0]!)) expect((await page.request.get(candidate)).status(), candidate).toBe(200);
  expect((await page.request.get(`${srcset.split(" ")[0]!.replace(/\?w=\d+$/, "")}?w=500`)).status()).toBe(422);
  await expect(page.locator("main")).not.toContainText(/raised|donors|funded/i);

  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", `${AUTH_BASE_URL}/campaigns/${slug}`);
  await expect(page.locator('meta[property="og:url"]')).toHaveAttribute("content", `${AUTH_BASE_URL}/campaigns/${slug}`);
  await expect(page.locator('meta[property="og:image"]').first()).toHaveAttribute("content", new RegExp(`^${AUTH_BASE_URL}/api/v1/public/campaigns/${slug}/media/`));
  await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute("content", "summary_large_image");
  const jsonLd = JSON.parse((await page.locator('script[type="application/ld+json"]').first().textContent()) ?? "{}");
  expect(jsonLd).toMatchObject({ "@type": "WebPage", url: `${AUTH_BASE_URL}/campaigns/${slug}`, name: title });
  expect(JSON.stringify(jsonLd)).not.toMatch(/DonateAction|amount|rating|review/i);
  // one view, counted with its traffic source (opened from a short link: no referrer → DIRECT)
  await expect.poll(async () => (await mockControl<{ counts: Record<string, number> }>(page, "discovery/counts", { campaign_id: id })).counts["VIEW_RAW|DIRECT"] ?? 0).toBeGreaterThan(0);
  const breadcrumbs = await page.locator('script[type="application/ld+json"]').allTextContents();
  expect(breadcrumbs.map((b) => JSON.parse(b)["@type"])).toContain("BreadcrumbList");

  expect(await axe(page)).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("sharing: encoded WhatsApp/Facebook/X/email links from PUBLIC_SITE_URL and copy-link feedback", async ({ page, context, browserName }) => {
  test.skip(browserName !== "chromium", "clipboard permissions are Chromium-only here");
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: AUTH_BASE_URL });
  const email = await owner(page);
  const title = `Funeral costs for Gogo Ncube ${token()}`;
  const { id, slug } = await seed(page, email, { title, category: "FUNERAL", summary: "Help the family & community give Gogo a dignified farewell." });
  const url = `${AUTH_BASE_URL}/campaigns/${slug}`;
  await page.goto(`/campaigns/${slug}`);

  const share = page.getByRole("list", { name: "Share options" });
  const wa = new URL((await share.getByRole("link", { name: /WhatsApp/ }).getAttribute("href"))!);
  expect(wa.origin + wa.pathname).toBe("https://wa.me/");
  expect(wa.searchParams.get("text")).toBe(`Support this FundZim campaign:\n${title}\nHelp the family & community give Gogo a dignified farewell.\nRead the story: ${url}`);
  await expect(share.getByRole("link", { name: /Facebook/ })).toHaveAttribute("href", `https://www.facebook.com/sharer/sharer.php?u=${encodeURIComponent(url)}`);
  const x = new URL((await share.getByRole("link", { name: /^X/ }).getAttribute("href"))!);
  expect(x.searchParams.get("url")).toBe(url);
  const mail = (await share.getByRole("link", { name: "Email" }).getAttribute("href"))!;
  expect(mail).toMatch(/^mailto:\?subject=Support%20this%20FundZim%20campaign/);
  expect(mail).toContain(encodeURIComponent(url));
  await expect(share.getByRole("link", { name: /WhatsApp/ })).toHaveAttribute("rel", "noopener noreferrer");
  expect(JSON.stringify([wa.toString(), x.toString(), mail])).not.toMatch(/example\.com|beneficiary|Tendai/);

  await expect(share.getByRole("link", { name: /LinkedIn/ })).toHaveAttribute("href", `https://www.linkedin.com/sharing/share-offsite/?url=${encodeURIComponent(url)}`);

  // copy link: the canonical URL plus a referral code naming only the campaign and the channel
  await share.getByRole("button", { name: "Copy link" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Link copied." })).toBeVisible();
  const copied = await page.evaluate(() => navigator.clipboard.readText());
  expect(copied).toMatch(new RegExp(`^${url.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\?ref=[0-9a-hjkmnp-tv-z]{12}$`));
  await expect.poll(async () => (await mockControl<{ counts: Record<string, number> }>(page, "discovery/counts", { campaign_id: id })).counts["SHARE_RAW|COPY_LINK"] ?? 0).toBe(1);
  const codes = await mockControl<{ referrals: Array<{ code: string; channel: string }> }>(page, "discovery/counts", { campaign_id: id });
  expect(codes.referrals.map((r) => r.channel)).toEqual(["COPY_LINK"]);

  // a visit through the shared link counts as a referral visit and the code leaves the address bar
  await page.goto(copied.replace(AUTH_BASE_URL, ""));
  await expect(page).toHaveURL(new RegExp(`/campaigns/${slug}$`));
  await expect.poll(async () => (await mockControl<{ counts: Record<string, number> }>(page, "discovery/counts", { campaign_id: id })).counts["REFERRAL_VISIT_RAW|COPY_LINK"] ?? 0).toBe(1);
});

test("analytics: nothing is sent when Do Not Track or Global Privacy Control is on", async ({ page }) => {
  const email = await owner(page);
  const { slug } = await seed(page, email, { title: `Privacy signal test ${token()}` });
  await page.addInitScript(() => {
    Object.defineProperty(Navigator.prototype, "globalPrivacyControl", { get: () => true, configurable: true });
  });
  const events: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/api/v1/discovery/events") || r.url().includes("/api/v1/public/events")) events.push(r.url());
  });
  await page.goto(`/campaigns/${slug}`);
  await page.waitForLoadState("networkidle");
  expect(events).toEqual([]);
});

test("report dialog: validation, honeypot hidden, confirmation without promising removal", async ({ page }) => {
  const problems = watchForBreakage(page);
  const email = await owner(page);
  const { slug } = await seed(page, email, { title: `Report flow campaign ${token()}` });
  await page.goto(`/campaigns/${slug}`);
  const opener = page.getByRole("button", { name: "Report this campaign" });
  await opener.click();
  const dialog = page.getByRole("dialog", { name: "Report this campaign" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("radio").first()).toBeFocused();
  await expect(dialog.locator('input[name="website"]')).not.toBeInViewport();
  expect(await axe(page)).toEqual([]);

  await dialog.getByRole("radio", { name: /Something else/ }).check();
  await dialog.getByRole("button", { name: "Send report" }).click();
  await expect(dialog.getByRole("alert")).toBeFocused();
  await expect(dialog.getByText(/at least 10 characters/)).toBeVisible();
  await dialog.getByLabel(/Details/).fill("The photos look copied from a news site.");
  await dialog.getByLabel(/Your email address/).fill("worried@example.test");
  await dialog.getByRole("button", { name: "Send report" }).click();
  await expect(dialog.getByRole("status")).toContainText("your report was received");
  await expect(dialog).not.toContainText(/will be removed|taken down/i);
  const reports = await mockControl<{ items: Array<Record<string, unknown>> }>(page, "reports", { slug });
  expect(reports.items).toEqual([expect.objectContaining({ reason: "OTHER", details: "The photos look copied from a news site.", has_contact: true, honeypot: false })]);
  await dialog.getByRole("button", { name: "Close" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();

  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(problems).toEqual([]);
});

test("report dialog: a rate-limited report (429, checked before validation) gets a friendly message", async ({ page }) => {
  const email = await owner(page);
  const { slug } = await seed(page, email, { title: `Rate limit campaign ${token()}` });
  await page.route("**/api/v1/public/campaigns/*/reports", (route) =>
    route.fulfill({ status: 429, headers: { "Content-Type": "application/json", "Retry-After": "1800" }, body: JSON.stringify({ error: { code: "RATE_LIMITED", message: "x", retryable: true }, meta: { request_id: "req-12345678" } }) }),
  );
  await page.goto(`/campaigns/${slug}`);
  await page.getByRole("button", { name: "Report this campaign" }).click();
  const dialog = page.getByRole("dialog", { name: "Report this campaign" });
  await dialog.getByRole("radio", { name: /Misleading/ }).check();
  await dialog.getByRole("button", { name: "Send report" }).click();
  await expect(dialog.getByRole("alert")).toHaveText(/Too many reports were sent from this connection\. Please try again in about 30 minutes\./);
  await expect(dialog.getByRole("radio", { name: /Misleading/ })).toBeChecked();
});

test("robots.txt and sitemap.xml (indexing allowed; private areas disallowed; live campaigns only)", async ({ page, request }) => {
  const email = await owner(page);
  const live = await seed(page, email, { title: `Sitemap live ${token()}` });
  const unlisted = await seed(page, email, { title: `Sitemap unlisted ${token()}`, visibility: "UNLISTED" });
  const suspended = await seed(page, email, { title: `Sitemap suspended ${token()}`, status: "SUSPENDED" });

  const robots = await request.get("/robots.txt");
  expect(robots.status()).toBe(200);
  const text = await robots.text();
  expect(text).toMatch(/User-Agent: \*\nAllow: \//);
  for (const p of ["/dashboard", "/admin", "/login", "/register", "/settings", "/staff", "/verify-email", "/reset-password", "/forgot-password", "/api"]) expect(text).toContain(`Disallow: ${p}\n`);
  expect(text).toContain(`Sitemap: ${AUTH_BASE_URL}/sitemap.xml`);

  const sitemap = await request.get("/sitemap.xml");
  expect(sitemap.status()).toBe(200);
  const xml = await sitemap.text();
  for (const p of ["/", "/campaigns", "/campaigns/trending", "/campaigns/featured", "/categories", "/how-it-works", "/safety", "/about", "/contact"]) expect(xml).toContain(`<loc>${AUTH_BASE_URL}${p}</loc>`);
  expect(xml).toContain(`<loc>${AUTH_BASE_URL}/campaigns/${live.slug}</loc>`);
  expect(xml).not.toContain(unlisted.slug);
  expect(xml).not.toContain(suspended.slug!);
  expect(xml).not.toMatch(/dashboard|admin|login/);

  const manifest = await request.get("/manifest.webmanifest");
  expect(await manifest.json()).toMatchObject({ name: "FundZim", short_name: "FundZim", theme_color: "#1b5e40", background_color: "#faf7f0", display: "standalone" });
  for (const icon of ["/icons/icon-192.png", "/icons/icon-512.png", "/icons/maskable-512.png", "/og/fundzim-og.png"]) {
    const res = await request.get(icon);
    expect(res.status(), icon).toBe(200);
    expect(res.headers()["content-type"]).toBe("image/png");
  }
});

test("private pages are noindex even when indexing is allowed", async ({ page }) => {
  for (const path of ["/login", "/register", "/forgot-password", "/reset-password", "/verify-email", "/staff/accept-invitation", "/privacy", "/terms"]) {
    await page.goto(path);
    await expect(page.locator('meta[name="robots"]'), path).toHaveAttribute("content", /noindex/);
  }
});

test("without PUBLIC_SITE_URL: robots keeps crawlers out, the sitemap is empty, no share links or canonical", async ({ request }) => {
  const site = `http://127.0.0.1:${process.env.E2E_PORT ?? 3100}`;
  const robots = await (await request.get(`${site}/robots.txt`)).text();
  expect(robots).toMatch(/User-Agent: \*\nDisallow: \/\n/);
  expect(robots).not.toContain("Sitemap:");
  const xml = await (await request.get(`${site}/sitemap.xml`)).text();
  expect(xml).not.toContain("<loc>");
  const home = await (await request.get(`${site}/`)).text();
  expect(home).not.toContain('rel="canonical"');
  expect(home).toMatch(/<meta name="robots" content="noindex/);
});

const PAGES = ["/", "/campaigns", "/categories", "/categories/education", "/safety", "/how-it-works", "/about", "/contact", "/privacy", "/terms"];

test("axe: every public page has no serious or critical violations", async ({ page }) => {
  const email = await owner(page);
  const { slug } = await seed(page, email, { title: `Axe campaign ${token()}` });
  for (const path of [...PAGES, `/campaigns/${slug}`]) {
    await page.goto(path);
    expect(await axe(page), path).toEqual([]);
  }
});

for (const width of [320, 375]) {
  test(`no horizontal overflow at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 });
    const email = await owner(page);
    const { slug } = await seed(page, email, { title: `A deliberately long campaign title to test wrapping at narrow widths ${token()}` });
    for (const path of [...PAGES, `/campaigns/${slug}`]) {
      await page.goto(path);
      expect(await noHorizontalOverflow(page), `${path} at ${width}px`).toBe(true);
    }
    // The mobile menu is a disclosure; its links are at least 44px tall.
    await page.goto("/");
    await page.getByRole("button", { name: "Menu" }).click();
    const link = page.getByRole("navigation", { name: "Main menu" }).getByRole("link", { name: "Explore Campaigns" });
    expect((await link.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  });
}

test("service worker: registers, caches only build assets, serves the offline page when offline", async ({ page, context, browserName }) => {
  test.skip(browserName !== "chromium", "Chromium only");
  await page.goto("/");
  await page.evaluate(async () => {
    await navigator.serviceWorker.ready;
  });
  await page.reload(); // now controlled: static assets go through the worker
  await page.waitForLoadState("networkidle");
  const cached = await page.evaluate(async () => {
    const out: string[] = [];
    for (const name of await caches.keys()) for (const req of await (await caches.open(name)).keys()) out.push(new URL(req.url).pathname);
    return out;
  });
  expect(cached).toEqual(expect.arrayContaining(["/offline.html", "/offline.css"]));
  expect(cached.every((p) => p.startsWith("/_next/static/") || p === "/offline.html" || p === "/offline.css")).toBe(true);

  await context.setOffline(true);
  await page.goto("/campaigns").catch(() => undefined);
  await expect(page.getByRole("heading", { level: 1, name: "You are offline" })).toBeVisible();
  await context.setOffline(false);
});
