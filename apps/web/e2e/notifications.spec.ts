import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, createUser, cspViolations, seriousAxeViolations, signIn, signInStaff, TOTP, trackCspViolations, watchForBreakage } from "./helpers";

// Stage 15 notifications against the mock API (e2e/mock-api/notifications.mjs). TEST data only; nothing is sent.
test.use({ baseURL: AUTH_BASE_URL });

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function stepUp(page: Page) {
  const dialog = page.getByRole("dialog", { name: "Confirm it's you" });
  await dialog.getByLabel("Authentication code").fill(TOTP);
  await dialog.getByRole("button", { name: "Confirm" }).click();
  await expect(dialog).toHaveCount(0);
}

async function confirmIn(dialog: ReturnType<Page["getByRole"]>, name: string | RegExp) {
  const button = dialog.getByRole("button", { name });
  await button.focus();
  await button.press("Enter");
}

test("user: notification center, mark as read, preferences and consent", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const email = await createUser(page);
  await signIn(page, email);
  await page.waitForURL(/\/dashboard/);
  await expect(page.getByText(/Notifications: 2 unread/)).toBeVisible();
  await page.goto("/dashboard/notifications");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Notifications" })).toBeVisible();
  const list = page.getByRole("list", { name: "Notifications" });
  await expect(list.getByRole("listitem")).toHaveCount(3);
  await expect(list.getByText("TEST data")).toBeVisible(); // a TEST payment is labelled
  expect(await axe(page)).toEqual([]);
  await list.getByRole("button", { name: /Mark as read: Your campaign was approved/ }).click();
  await expect(page.getByRole("status").filter({ hasText: "Marked as read." })).toBeVisible();
  await page.getByRole("button", { name: "Mark all as read" }).click();
  await expect(page.getByRole("status").filter({ hasText: "All notifications marked as read." })).toBeVisible();
  await expect(page.getByRole("button", { name: "Mark all as read" })).toHaveCount(0);

  await page.getByRole("link", { name: "Choose how you are notified" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Notification preferences" })).toBeVisible();
  const security = page.getByRole("group", { name: "Security" });
  await expect(security.getByLabel(/E-mail/)).toBeDisabled(); // security e-mail cannot be turned off
  await expect(security.getByLabel(/E-mail/)).toBeChecked();
  const campaign = page.getByRole("group", { name: "Campaign" });
  await campaign.getByLabel(/E-mail/).uncheck();
  await page.getByRole("button", { name: "Save preferences" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Preferences saved." })).toBeVisible();
  await expect(page.getByRole("group", { name: "Marketing" }).getByLabel(/E-mail/)).toBeDisabled();
  await page.getByRole("button", { name: "Give consent for SMS" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Consent for SMS recorded." })).toBeVisible();
  await expect(page.getByRole("button", { name: "Withdraw consent for SMS" })).toBeVisible();
  await expect(main).toContainText("Marketing messages: not available");
  expect(await axe(page)).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("staff: communications dashboard, template maker-checker, dead-letter replay and RBAC", async ({ page, browser }) => {
  await trackCspViolations(page);
  const admin = await createStaff(page, ["ADMIN"], "Comms Admin");
  await signInStaff(page, admin, "/admin/communications");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Communications" })).toBeVisible();
  await expect(main).toContainText("Disabled (no provider selected)");
  await expect(main).toContainText("Not measured (no attempts in the last hour)");
  expect(await axe(page)).toEqual([]);

  // dead letter replay (step-up)
  const seeded = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/notifications/dead-letter`, { data: {} });
  expect(seeded.status()).toBe(201);
  const { delivery_id: deliveryId } = (await seeded.json()).data;
  await page.goto("/admin/communications/dead-letters");
  // the mock is shared by parallel projects: act on this test's own dead letter
  const row = page.getByRole("row").filter({ has: page.locator(`a[href*="${deliveryId}"]`) });
  await row.getByRole("button", { name: "Replay" }).click();
  const dialog = page.getByRole("dialog", { name: "Replay this delivery?" });
  await dialog.getByLabel("Reason (staff only)").fill("Provider recovered after the outage");
  await confirmIn(dialog, "Confirm: Replay");
  await stepUp(page);
  await expect(row.getByRole("status").filter({ hasText: "Replay processed." })).toBeVisible();
  await expect(row).toContainText("Replayed");
  await expect(row.getByRole("button", { name: "Replay" })).toHaveCount(0);
  expect(await axe(page)).toEqual([]);

  // template: the maker drafts and submits
  const tpl = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/notifications/template`, { data: {} });
  expect(tpl.status()).toBe(201);
  const { key } = (await tpl.json()).data;
  await page.goto("/admin/communications/templates");
  await page.getByRole("link", { name: key, exact: true }).click();
  await expect(page.getByRole("heading", { level: 1, name: key })).toBeVisible();
  const form = page.getByRole("form", { name: "Draft a new version" });
  await form.getByLabel("In-app text").fill("Your campaign is paused. Resume it from your dashboard when you are ready.");
  await form.getByRole("button", { name: "Save draft" }).click();
  await expect(page.getByRole("heading", { name: /Version 2 — Draft/ })).toBeVisible();
  await page.getByRole("button", { name: "Submit for approval" }).click();
  await confirmIn(page.getByRole("dialog", { name: "Submit this version for approval?" }), "Confirm: Submit for approval");
  await expect(page.getByRole("heading", { name: /Version 2 — Pending approval/ })).toBeVisible();
  await expect(main).toContainText("a different person decides it");
  await expect(page.getByRole("button", { name: "Approve" })).toHaveCount(0); // the maker cannot approve
  const templateUrl = page.url();
  expect(await axe(page)).toEqual([]);

  // the checker approves and activates
  const ctx = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const other = await ctx.newPage();
  const checker = await createStaff(other, ["COMPLIANCE"], "Comms Checker");
  await signInStaff(other, checker, "/admin/communications");
  await other.goto(templateUrl);
  await other.getByRole("button", { name: "Approve" }).click();
  const ad = other.getByRole("dialog", { name: "Approve this version?" });
  await ad.getByLabel("Reason (staff only)").fill("Wording reviewed");
  await confirmIn(ad, "Confirm: Approve");
  await stepUp(other);
  await expect(other.getByRole("heading", { name: /Version 2 — Approved/ })).toBeVisible();
  await other.getByRole("button", { name: "Activate" }).click();
  const act = other.getByRole("dialog", { name: "Activate this version?" });
  await act.getByLabel("Reason (staff only)").fill("Go live with the new wording");
  await confirmIn(act, "Confirm: Activate");
  await expect(other.getByRole("heading", { name: /Version 2 — Active/ })).toBeVisible();
  await expect(other.getByRole("heading", { name: /Version 1 — Retired/ })).toBeVisible();
  // consent records: compliance only
  await other.goto("/admin/communications/consent");
  await expect(other.getByRole("heading", { level: 1, name: "Consent records" })).toBeVisible();
  await ctx.close();
  const support = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const sp = await support.newPage();
  const agent = await createStaff(sp, ["SUPPORT"], "Support Agent");
  await signInStaff(sp, agent, "/admin/communications/deliveries");
  await expect(sp.getByRole("heading", { level: 1, name: "Deliveries" })).toBeVisible();
  await expect(sp.getByRole("navigation", { name: "Staff", exact: true }).getByRole("link", { name: "Consent records" })).toHaveCount(0);
  expect((await sp.goto("/admin/communications/consent"))?.status()).toBe(404);
  await support.close();
  expect(await cspViolations(page)).toEqual([]);
});
