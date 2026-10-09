import { expect, test } from "@playwright/test";

import { seriousAxeViolations, watchForBreakage } from "./helpers";

test("homepage renders key sections, honest notices and security headers", async ({ page }) => {
  const problems = watchForBreakage(page);
  const response = await page.goto("/");
  expect(response?.status()).toBe(200);
  const headers = response!.headers();
  expect(headers["content-security-policy"]).toContain("frame-ancestors 'none'");
  expect(headers["content-security-policy"]).not.toContain("unsafe-eval");
  expect(headers["content-security-policy"]).not.toContain("unsafe-inline");
  expect(headers["x-content-type-options"]).toBe("nosniff");
  expect(headers["x-frame-options"]).toBe("DENY");
  expect(headers["referrer-policy"]).toBe("strict-origin-when-cross-origin");
  expect(headers["x-powered-by"]).toBeUndefined();

  await expect(page.getByRole("heading", { level: 1, name: "Together, We Can Make a Difference." })).toBeVisible();
  await expect(page.getByText(/Development preview — FundZim is not open for fundraising/)).toBeVisible();
  await expect(page.getByText("Payments are not live yet")).toBeVisible();
  await expect(page.getByText("Design preview — not a real campaign")).toBeVisible();
  // API is intentionally unreachable in this run: the footer degrades gracefully and the header offers sign-in.
  await expect(page.getByText(/Platform API: not reachable right now/)).toBeVisible();
  await expect(page.getByRole("banner").getByRole("link", { name: "Sign In" })).toHaveAttribute("href", "/login");
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);

  expect(await seriousAxeViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("navigates to How it works from the header", async ({ page, isMobile }) => {
  const problems = watchForBreakage(page);
  await page.goto("/");
  if (isMobile) await page.getByRole("button", { name: "Menu" }).click();
  const nav = page.getByRole("navigation", { name: isMobile ? "Main menu" : "Main" });
  await nav.getByRole("link", { name: "How It Works" }).click();
  await expect(page).toHaveURL(/\/how-it-works$/);
  await expect(page.getByRole("heading", { level: 1, name: "How FundZim will work" })).toBeVisible();
  await expect(page).toHaveTitle("How it works · FundZim");
  expect(await seriousAxeViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("unknown routes return the 404 page", async ({ page }) => {
  const response = await page.goto("/this-page-does-not-exist");
  expect(response?.status()).toBe(404);
  await expect(page.getByRole("heading", { level: 1, name: "We couldn't find that page" })).toBeVisible();
});

test("without the API, a campaign slug is never rendered as a campaign", async ({ page }) => {
  const response = await page.goto("/campaigns/help-my-family-urgent");
  expect(response?.status()).not.toBe(200);
  await expect(page.getByRole("heading", { level: 1, name: "Error" })).toBeAttached();
  await expect(page.getByText("This page could not be loaded")).toBeVisible();
  await expect(page.getByText("help-my-family-urgent")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /donat/i })).toHaveCount(0);
});

test("malformed campaign slugs are a 404 without calling the API", async ({ page }) => {
  const response = await page.goto("/campaigns/Not_A_Slug");
  expect(response?.status()).toBe(404);
  await expect(page.getByRole("heading", { level: 1, name: "We couldn't find that page" })).toBeVisible();
});

test("former placeholder URLs redirect: /explore to the listing, /start to the wizard (via sign-in)", async ({ page }) => {
  await page.goto("/explore");
  await expect(page).toHaveURL(/\/campaigns$/);
  await page.goto("/start");
  await expect(page).toHaveURL(/\/login\?next=%2Fdashboard%2Fcampaigns%2Fnew$/);
  expect(await seriousAxeViolations(page)).toEqual([]);
});

test("liveness endpoint does not depend on the API", async ({ request }) => {
  const res = await request.get("/healthz");
  expect(res.status()).toBe(200);
  expect(await res.json()).toEqual({ status: "ok" });
});

test("same-origin API proxy answers with an error envelope when the API is down", async ({ request }) => {
  const res = await request.get("/api/v1/health");
  expect(res.status()).toBe(503);
  expect(res.headers()["x-request-id"]).toBeTruthy();
  expect(await res.json()).toMatchObject({ error: { code: "SERVICE_UNAVAILABLE" } });
});

test.describe("mobile menu", () => {
  test.skip(({ isMobile }) => !isMobile, "mobile viewport only");

  test("opens, closes with Escape and restores focus", async ({ page }) => {
    const problems = watchForBreakage(page);
    await page.goto("/");
    const button = page.getByRole("button", { name: "Menu" });
    await expect(button).toHaveAttribute("aria-expanded", "false");
    await button.click();
    await expect(button).toHaveAttribute("aria-expanded", "true");
    const menu = page.getByRole("navigation", { name: "Main menu" });
    await expect(menu.getByRole("link", { name: "How It Works" })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(button).toHaveAttribute("aria-expanded", "false");
    await expect(menu).toBeHidden();
    await expect(button).toBeFocused();
    expect(problems).toEqual([]);
  });
});
