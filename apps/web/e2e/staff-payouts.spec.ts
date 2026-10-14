import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, cspViolations, seriousAxeViolations, signInStaff, TOTP, trackCspViolations, watchForBreakage } from "./helpers";

// Stage 11 staff payout UI against the mock API (e2e/mock-api/payouts.mjs). TEST payouts only.
test.use({ baseURL: AUTH_BASE_URL });

interface Seed {
  campaign_id: string;
  payout_id: string;
  exception_id: string;
}

async function seed(page: Page): Promise<Seed> {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/payouts/seed`, { data: {} });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: Seed }).data;
}

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

/** Submits a dialog's confirm button by keyboard (robust inside a scrollable <dialog> on small viewports). */
async function confirmIn(dialog: ReturnType<Page["getByRole"]>, name: string) {
  const button = dialog.getByRole("button", { name });
  await button.focus();
  await button.press("Enter");
}

async function stepUp(page: Page) {
  const dialog = page.getByRole("dialog", { name: "Confirm it's you" });
  await dialog.getByLabel("Authentication code").fill(TOTP);
  await dialog.getByRole("button", { name: "Confirm" }).click();
  await expect(dialog).toHaveCount(0);
}

async function approve(page: Page, reason: string) {
  await page.getByRole("button", { name: "Approve payout" }).click();
  const dialog = page.getByRole("dialog", { name: "Approve this payout?" });
  await dialog.getByLabel("Reason (staff only)").fill(reason);
  await dialog.getByLabel("I have no conflict of interest with this payout, its campaign or its owner.").check();
  await confirmIn(dialog, "Confirm: Approve");
  return dialog;
}

test("payouts: TEST banner, dual maker-checker approval with step-up, exceptions and reconciliation", async ({ page, browser }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const s = await seed(page);
  const first = await createStaff(page, ["FINANCE"], "First Finance");
  await signInStaff(page, first, "/admin/finance/payouts");

  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Payouts" })).toBeVisible();
  await expect(main).toContainText("TEST PAYOUTS ONLY");
  await expect(main).toContainText("Production payouts: BLOCKED");
  await expect(main).toContainText("NOT_PERFORMED");
  expect(await axe(page)).toEqual([]);

  // the approvals queue via the staff navigation
  await page.getByRole("navigation", { name: "Staff" }).getByRole("link", { name: "Payout approvals" }).click();
  const queue = page.getByRole("table", { name: "Payouts awaiting approval" });
  const row = queue.getByRole("row").filter({ has: page.locator(`a[href$="${s.payout_id}"]`) });
  await expect(row).toContainText("TEST_ONLY");
  expect(await axe(page)).toEqual([]);
  await row.getByRole("link").first().click();
  await expect(page).toHaveURL(new RegExp(`/admin/finance/withdrawals/${s.payout_id}$`));
  await expect(page.getByRole("heading", { level: 1, name: "Withdrawal request" })).toBeVisible();
  await expect(main).toContainText("Production payouts: BLOCKED");
  await expect(main).toContainText("EC-01");
  expect(await axe(page)).toEqual([]);

  // the conflict attestation is required before anything is sent
  await page.getByRole("button", { name: "Approve payout" }).click();
  const dialog = page.getByRole("dialog", { name: "Approve this payout?" });
  await dialog.getByLabel("Reason (staff only)").fill("Checks reviewed; amount matches the request.");
  await confirmIn(dialog, "Confirm: Approve");
  await expect(dialog.getByRole("alert")).toContainText("no conflict of interest");
  await dialog.getByRole("button", { name: "Cancel" }).click();

  // first approver (step-up) — DUAL policy keeps it pending
  await approve(page, "Checks reviewed; amount matches the request.");
  await stepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Approval recorded." })).toBeVisible();
  await expect(main).toContainText("Pending review");

  // the same approver cannot decide twice in this round
  const again = await approve(page, "Trying to approve a second time.");
  await expect(again.getByRole("alert")).toContainText("already decided");
  await again.getByRole("button", { name: "Cancel" }).click();

  // a second, distinct FINANCE officer completes the approval
  const ctx2 = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const page2 = await ctx2.newPage();
  const second = await createStaff(page2, ["FINANCE"], "Second Finance");
  await signInStaff(page2, second, `/admin/finance/withdrawals/${s.payout_id}`);
  await approve(page2, "Second check done; destination verified.");
  await stepUp(page2);
  // fully approved: the decision actions unmount with the refresh
  await expect(page2.getByRole("button", { name: "Approve payout" })).toHaveCount(0);
  await expect(page2.getByRole("region", { name: "Payout", exact: true })).toContainText("Approved");
  await ctx2.close();

  // exceptions: investigate (step-up still fresh)
  await page.goto("/admin/finance/payouts/exceptions");
  const exRow = page.getByRole("table", { name: "Payout exceptions" }).getByRole("row").filter({ has: page.locator(`a[href$="${s.payout_id}"]`) });
  await expect(exRow).toContainText("SEV2");
  expect(await axe(page)).toEqual([]);
  await exRow.getByRole("button", { name: "Investigate" }).click();
  const inv = page.getByRole("dialog", { name: "Take ownership of this exception?" });
  await inv.getByLabel("Note (staff only)").fill("Checking the simulator status for this payout.");
  await confirmIn(inv, "Confirm: Investigate");
  await expect(page.getByRole("status").filter({ hasText: "You are now investigating this exception." })).toBeVisible();

  // reservations and reconciliation
  await page.goto("/admin/finance/payouts/reservations");
  await expect(page.getByRole("table", { name: "Payout reservations" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.goto("/admin/finance/payouts/reconciliation");
  await expect(page.getByRole("table", { name: "Payout reconciliation mismatches" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Run reconciliation" }).click();
  await confirmIn(page.getByRole("dialog", { name: "Run payout reconciliation now?" }), "Confirm: Run");
  await expect(page.getByRole("status").filter({ hasText: "Reconciliation run finished." })).toBeVisible();

  const log = await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/payouts/log`, { data: {} })).json();
  const mine = log.data.actions.filter((a: { id?: string }) => a.id === s.payout_id || a.id === s.exception_id).map((a: { kind: string }) => a.kind);
  expect(mine).toEqual(["payout.approve", "payout.approve", "exception.investigate"]);
  expect(problems).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
});

test("payout pages are not available to staff without payout.view", async ({ page }) => {
  const s = await seed(page);
  const staff = await createStaff(page, ["SUPPORT"], "Support Agent");
  await signInStaff(page, staff, "/admin/finance/payouts");
  await expect(page.getByRole("heading", { level: 1, name: "Payouts" })).toHaveCount(0);
  await page.goto(`/admin/finance/withdrawals/${s.payout_id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Withdrawal request" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Approve payout" })).toHaveCount(0);
  await page.goto("/admin/finance/payouts/approvals");
  await expect(page.locator(`a[href$="${s.payout_id}"]`)).toHaveCount(0);
});
