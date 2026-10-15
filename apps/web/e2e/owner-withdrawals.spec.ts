import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createUser, cspViolations, PASSWORD, seriousAxeViolations, signIn, trackCspViolations, watchForBreakage } from "./helpers";

// Stage 11 organiser withdrawals against the mock API (e2e/mock-api/payouts.mjs). TEST balances only.
test.use({ baseURL: AUTH_BASE_URL });

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function seedOwner(page: Page, email: string) {
  const dest = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/payout-destination-verified`, { data: { email } });
  expect(dest.status()).toBe(201);
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/payouts/seed`, { data: { owner_email: email, with_payout: false, settled_minor: "50000" } });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: { campaign_id: string } }).data.campaign_id;
}

async function stepUp(page: Page) {
  const dialog = page.getByRole("dialog", { name: "Confirm it's you" });
  await dialog.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await dialog.getByRole("button", { name: "Confirm" }).click();
  await expect(dialog).toHaveCount(0);
}

test("owner: request a test withdrawal (step-up), see it under review, then cancel it", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const email = await createUser(page);
  const campaignId = await seedOwner(page, email);
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard/);

  await page.goto(`/dashboard/campaigns/${campaignId}/withdrawals`);
  const main = page.locator("main");
  await expect(page.getByRole("heading", { name: "Test balance (USD)" })).toBeVisible();
  await expect(main).toContainText("Test withdrawals only");
  await expect(main).toContainText("TEST_ONLY");
  await expect(main).toContainText("Production payouts: BLOCKED");
  await expect(main).toContainText("$500.00");
  expect(await axe(page)).toEqual([]);

  // client-side validation: more than the available test balance never reaches the API
  const form = page.getByRole("form", { name: "Request a test withdrawal" });
  await form.getByLabel("Amount (USD)").fill("600");
  await form.getByRole("button", { name: "Request test withdrawal" }).click();
  await expect(form.getByRole("alert").first()).toBeVisible();

  await form.getByLabel("Amount (USD)").fill("125.50");
  await form.getByLabel("Purpose (optional)").fill("Hospital deposit");
  await form.getByRole("button", { name: "Request test withdrawal" }).click();
  await stepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Withdrawal requested. A finance officer will review it." })).toBeVisible();
  await expect(main).toContainText("Under review");
  await expect(main).toContainText("$125.50");

  // the request in the organiser's list, then its detail page
  await page.goto("/dashboard/withdrawals");
  await expect(main).toContainText("Under review");
  await expect(main).toContainText("TEST_ONLY");
  expect(await axe(page)).toEqual([]);
  await main.getByRole("link", { name: /125\.50/ }).click();
  await expect(page).toHaveURL(/\/dashboard\/withdrawals\/[0-9a-f-]{36}$/);
  await expect(main).toContainText("Status: Under review");
  await expect(main).toContainText("Production payouts: BLOCKED");
  // Stage 12: the payout fee estimate, who bears it and what the beneficiary receives (mock: 1 %)
  await expect(main).toContainText("Payout fee (estimate)");
  await expect(main).toContainText("$1.25");
  await expect(main).toContainText("Paid by the campaign");
  await expect(main).toContainText("$124.25");
  expect(await axe(page)).toEqual([]);

  // cancel (step-up still fresh)
  await page.getByRole("button", { name: "Cancel withdrawal" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Withdrawal cancelled." })).toBeVisible();
  await expect(main).toContainText("Status: Cancelled");
  await page.reload();
  await expect(main).toContainText("Status: Cancelled");
  await expect(page.getByRole("button", { name: "Cancel withdrawal" })).toHaveCount(0);

  const log = await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/payouts/log`, { data: {} })).json();
  const mine = log.data.actions.filter((a: { kind: string; amount_minor?: string }) => a.kind.startsWith("owner."));
  expect(mine.some((a: { kind: string; amount_minor?: string }) => a.kind === "owner.request" && a.amount_minor === "12550")).toBe(true);
  expect(problems).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
});

test("owner: another user's campaign withdrawals are not visible", async ({ page }) => {
  const owner = await createUser(page);
  const campaignId = await seedOwner(page, owner);
  const other = await createUser(page);
  await signIn(page, other);
  await expect(page).toHaveURL(/\/dashboard/);
  await page.goto(`/dashboard/campaigns/${campaignId}/withdrawals`);
  await expect(page.getByRole("heading", { name: "Test balance (USD)" })).toHaveCount(0);
  await expect(page.getByRole("form", { name: "Request a test withdrawal" })).toHaveCount(0);
});
