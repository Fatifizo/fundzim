import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createUser, cspViolations, seriousAxeViolations, signIn, trackCspViolations, watchForBreakage } from "./helpers";

// Stage 12 organiser fee disclosure and the donor fee preview against the mock API (e2e/mock-api/fees.mjs).
test.use({ baseURL: AUTH_BASE_URL });

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function seedOwner(page: Page, email: string) {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/fees/seed`, { data: { owner_email: email } });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: { campaign_id: string } }).data.campaign_id;
}

test("organiser: campaign fees disclosure, then the donor fee preview (a quote is not a charge)", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const email = await createUser(page);
  const campaignId = await seedOwner(page, email);
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard/);

  await page.goto(`/dashboard/campaigns/${campaignId}/fees`);
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Fees" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Campaign sections" }).getByRole("link", { name: "Fees" })).toHaveAttribute("aria-current", "page");
  await expect(main).toContainText("Test fees only");
  await expect(main).toContainText("5.00%");
  await expect(main).toContainText("Paid by the campaign");
  const assessed = page.getByRole("table", { name: "Fees assessed for this campaign" });
  await expect(assessed).toContainText("UNKNOWN");
  await expect(assessed).toContainText("$5.00");
  // staff-only notes are not shown to organisers
  await expect(main).not.toContainText("seeded correction request");
  expect(await axe(page)).toEqual([]);

  await page.getByRole("link", { name: "Preview what a donor would see" }).click();
  await expect(page).toHaveURL(/\/dashboard\/donation-preview\?campaign=/);
  await expect(page.getByRole("heading", { level: 1, name: "Donation fee preview" })).toBeVisible();
  const form = page.getByRole("form", { name: "Preview donation fees" });
  await expect(form.getByLabel("Campaign id")).toHaveValue(campaignId);
  await form.getByLabel("Donation (USD)").fill("abc");
  await form.getByRole("button", { name: "Preview fees" }).click();
  await expect(form.getByRole("alert").first()).toBeVisible();

  // with a method that has synthetic provider evidence: everything is known
  await form.getByLabel("Donation (USD)").fill("100");
  await form.getByLabel("Payment method (optional)").selectOption("ECOCASH");
  await form.getByRole("button", { name: "Preview fees" }).click();
  const breakdown = page.getByRole("region", { name: /Fee breakdown/ });
  await expect(breakdown).toContainText("$5.00");
  await expect(breakdown).toContainText("$2.00");
  await expect(breakdown).toContainText("$93.00");
  await expect(breakdown).toContainText("It is not a charge");
  await expect(breakdown).toContainText("Estimated");
  expect(await axe(page)).toEqual([]);

  // without a method the provider fee is UNKNOWN, never zero
  await form.getByLabel("Payment method (optional)").selectOption("");
  await form.getByRole("button", { name: "Preview fees" }).click();
  await expect(breakdown).toContainText("UNKNOWN");
  await expect(breakdown).not.toContainText("$93.00");

  const log = await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/fees/log`, { data: {} })).json();
  const mine = log.data.actions.filter((a: { kind: string; campaign_id?: string }) => a.kind === "quote.create" && a.campaign_id === campaignId);
  expect(mine).toHaveLength(2);
  expect(problems).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
});

test("organiser: another user cannot see the campaign's fees or the owner's quotes", async ({ page, browser }) => {
  const owner = await createUser(page);
  const campaignId = await seedOwner(page, owner);
  await signIn(page, owner);
  await expect(page).toHaveURL(/\/dashboard/);
  await page.goto(`/dashboard/donation-preview?campaign=${campaignId}`);
  const form = page.getByRole("form", { name: "Preview donation fees" });
  await form.getByLabel("Donation (USD)").fill("20");
  await form.getByRole("button", { name: "Preview fees" }).click();
  await expect(page.getByRole("region", { name: /Fee breakdown/ })).toBeVisible();
  const log = await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/fees/log`, { data: {} })).json();
  const quoteId = log.data.actions.find((a: { kind: string; campaign_id?: string }) => a.kind === "quote.create" && a.campaign_id === campaignId).id as string;
  expect((await page.request.get(`${AUTH_BASE_URL}/api/v1/fees/quotes/${quoteId}`)).status()).toBe(200);

  const ctx = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const other = await ctx.newPage();
  const otherEmail = await createUser(other);
  await signIn(other, otherEmail);
  await expect(other).toHaveURL(/\/dashboard/);
  await other.goto(`/dashboard/campaigns/${campaignId}/fees`);
  await expect(other.getByRole("heading", { level: 1, name: "Fees" })).toHaveCount(0);
  expect((await other.request.get(`${AUTH_BASE_URL}/api/v1/fees/quotes/${quoteId}`)).status()).toBe(404);
  await ctx.close();
});
