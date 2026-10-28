import { expect, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, cspViolations, seriousAxeViolations, signInStaff, TOTP, trackCspViolations, watchForBreakage, test } from "./helpers";

// Stage 18 security alerts against the mock API (e2e/mock-api/operations.mjs). Runs on desktop and Pixel 7.
test.use({ baseURL: AUTH_BASE_URL });

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function maybeStepUp(page: Page) {
  const dialog = page.getByRole("dialog", { name: "Confirm it's you" });
  if (await dialog.waitFor({ timeout: 3000 }).then(() => true, () => false)) {
    await dialog.getByLabel("Authentication code").fill(TOTP);
    await dialog.getByRole("button", { name: "Confirm" }).click();
    await expect(dialog).toHaveCount(0);
  }
}

async function confirmIn(dialog: ReturnType<Page["getByRole"]>, name: string) {
  const button = dialog.getByRole("button", { name });
  await button.focus();
  await button.press("Enter");
}

const staffNav = (page: Page) => page.getByRole("navigation", { name: "Staff", exact: true });

test("security admin: alert queue, acknowledge, escalate to an incident and resolve", async ({ page }) => {
  test.setTimeout(120_000);
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const seeded = (await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/ops/seed-security-alert`, { data: {} })).json()) as { data: { id: string; title: string } };
  const staff = await createStaff(page, ["SECURITY_ADMIN"], "Security Admin");
  await signInStaff(page, staff, "/admin/security");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Security alerts" })).toBeVisible();
  await expect(main).toContainText("no notification leaves FundZim");
  await expect(main).toContainText("Last verified restore drill");
  await expect(staffNav(page).getByRole("link", { name: "Security alerts" })).toBeVisible();
  expect(await axe(page)).toEqual([]);

  await page.goto(`/admin/security/alerts/${seeded.data.id}`);
  await expect(page.getByRole("heading", { level: 1, name: seeded.data.title })).toBeVisible();
  await expect(main).toContainText("Present at the last scan");
  await page.getByRole("button", { name: "Acknowledge" }).click();
  const ack = page.getByRole("dialog", { name: "Acknowledge this alert?" });
  await confirmIn(ack, "Confirm: Acknowledge");
  await maybeStepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Alert acknowledged." })).toBeVisible();
  await page.reload();
  await page.getByRole("button", { name: "Escalate to an incident" }).click();
  const esc = page.getByRole("dialog", { name: "Escalate this alert?" });
  await esc.getByLabel("Note (staff only)").fill("Break-glass grant under review.");
  await confirmIn(esc, "Confirm: Escalate");
  await maybeStepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Alert escalated." })).toBeVisible();
  await page.reload();
  await expect(main.getByRole("link", { name: "Open the incident" })).toBeVisible();
  await page.getByRole("button", { name: "Resolve" }).click();
  const res = page.getByRole("dialog", { name: "Resolve this alert?" });
  await expect(res).toContainText("the next scan opens a new alert");
  await res.getByLabel("Note (staff only)").fill("Grant revoked after review.");
  await confirmIn(res, "Confirm: Resolve");
  await maybeStepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Alert resolved." })).toBeVisible();
  await page.reload();
  await expect(main).toContainText("Grant revoked after review.");
  await expect(page.getByRole("table", { name: "Alert history" })).toContainText("Resolved");
  expect(await axe(page)).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("support staff: no security navigation, and the page is not found", async ({ page }) => {
  const staff = await createStaff(page, ["SUPPORT"], "Support Agent");
  await signInStaff(page, staff, "/admin/operations");
  await expect(staffNav(page).getByRole("link", { name: "Security alerts" })).toHaveCount(0);
  const r = await page.goto("/admin/security");
  expect(r?.status()).toBe(404);
});
