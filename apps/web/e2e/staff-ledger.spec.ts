import { expect, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, cspViolations, seriousAxeViolations, signInStaff, TOTP, trackCspViolations, watchForBreakage, test } from "./helpers";

// Stage 10 staff ledger UI against the mock API (e2e/mock-api/ledger.mjs).
test.use({ baseURL: AUTH_BASE_URL });

interface Seed {
  journal_id: string;
  account_id: string;
  exception_id: string;
}

async function seed(page: Page): Promise<Seed> {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/ledger/seed`, { data: {} });
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

test("ledger: TEST banner, journals, maker-checker reversal, account freeze, trial balance, reconciliation workflow", async ({ page, browser }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const s = await seed(page);
  const maker = await createStaff(page, ["FINANCE"], "Maker Finance");
  await signInStaff(page, maker, "/admin/finance/ledger");

  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Ledger" })).toBeVisible();
  await expect(main).toContainText("TEST LEDGER");
  await expect(main).toContainText("no balance is withdrawable");
  await expect(main).not.toContainText(/withdrawable balance:/i);
  await expect(page.getByRole("table", { name: "Trial balance totals" })).toContainText("USD");
  expect(await axe(page)).toEqual([]);

  // journal detail via the staff navigation, then request a reversal (maker, step-up)
  await page.getByRole("navigation", { name: "Staff" }).getByRole("link", { name: "Journals" }).click();
  await expect(page.getByRole("table", { name: "Journal register" })).toContainText("DONATION_CAPTURED");
  await expect(main.getByText("TEST_ONLY").first()).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.goto(`/admin/finance/journals/${s.journal_id}`);
  await expect(main).toContainText("Debits equal credits");
  await expect(page.getByRole("table", { name: "Journal lines" })).toContainText("asset:psp_clearing");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Request reversal" }).click();
  const dialog = page.getByRole("dialog", { name: "Request a reversal of this journal?" });
  await expect(dialog).toContainText("The original journal is never changed");
  await dialog.getByLabel("Reason (staff only)").fill("Synthetic capture posted twice by mistake.");
  await confirmIn(dialog, "Confirm: Request reversal");
  await stepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Reversal requested" })).toBeVisible();

  // the maker cannot approve their own request
  await page.goto("/admin/finance/ledger");
  const pending = page.getByRole("table", { name: "Adjustments awaiting approval" });
  const mine = pending.getByRole("row").filter({ has: page.locator(`a[href$="${s.journal_id}"]`) });
  await expect(mine).toContainText("Reversal");
  await mine.getByRole("button", { name: "Approve and post" }).click();
  const approve = page.getByRole("dialog", { name: "Approve this adjustment?" });
  await approve.getByLabel("Reason (staff only)").fill("Trying to approve my own request.");
  await confirmIn(approve, "Confirm: Approve");
  await expect(approve.getByRole("alert")).toBeVisible();
  await approve.getByRole("button", { name: "Cancel" }).click();

  // a second FINANCE officer approves
  const ctx2 = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const page2 = await ctx2.newPage();
  const checker = await createStaff(page2, ["FINANCE"], "Checker Finance");
  await signInStaff(page2, checker, "/admin/finance/ledger");
  const pending2 = page2.getByRole("table", { name: "Adjustments awaiting approval" });
  await pending2.getByRole("row").filter({ has: page2.locator(`a[href$="${s.journal_id}"]`) }).getByRole("button", { name: "Approve and post" }).click();
  const approve2 = page2.getByRole("dialog", { name: "Approve this adjustment?" });
  await approve2.getByLabel("Reason (staff only)").fill("Checked: the capture was a duplicate.");
  await confirmIn(approve2, "Confirm: Approve");
  await stepUp(page2);
  // the approved request leaves the pending list (its row and notice unmount); the journal shows the reversal
  await expect(page2.getByRole("row").filter({ has: page2.locator(`a[href$="${s.journal_id}"]`) })).toHaveCount(0);
  await page2.goto(`/admin/finance/journals/${s.journal_id}`);
  await expect(page2.locator("main")).toContainText("Reversed by");
  await ctx2.close();

  // account freeze (step-up still fresh for the maker session)
  await page.goto(`/admin/finance/accounts/${s.account_id}`);
  await expect(main).toContainText("may never go below zero");
  await page.getByRole("button", { name: "Freeze account" }).click();
  const freeze = page.getByRole("dialog", { name: "Freeze this account?" });
  await freeze.getByLabel("Reason (staff only)").fill("Investigating a synthetic anomaly.");
  await confirmIn(freeze, "Confirm: Freeze");
  await expect(page.getByRole("status").filter({ hasText: "Account frozen." })).toBeVisible();
  expect(await axe(page)).toEqual([]);

  // trial balance and reconciliation
  await page.goto("/admin/finance/trial-balance");
  await expect(page.getByRole("table", { name: "Trial balance USD" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.goto("/admin/finance/reconciliation");
  await expect(main).toContainText("Settlement reconciliation is not performed");
  const row = page.getByRole("row").filter({ has: page.locator(`a[href$="${s.journal_id}"]`) }).filter({ hasText: "Amount mismatch" });
  await row.getByRole("button", { name: "Investigate" }).click();
  const inv = page.getByRole("dialog", { name: "Take ownership of this exception?" });
  await inv.getByLabel("Reason (staff only)").fill("I will check the synthetic payment.");
  await confirmIn(inv, "Confirm: Investigate");
  await expect(page.getByRole("status").filter({ hasText: "investigating" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.goto("/admin/finance/exceptions");
  await expect(page.getByRole("table", { name: "Integrity checks" })).toContainText("L1");

  const log = await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/ledger/log`, { data: {} })).json();
  const kinds = log.data.actions.map((a: { kind: string }) => a.kind);
  expect(kinds).toEqual(expect.arrayContaining(["adjustment.requested", "adjustment.approve", "account.freeze", "recon.investigate"]));
  expect(problems).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
});

test("ledger pages are not available to staff without ledger.view", async ({ page }) => {
  const staff = await createStaff(page, ["SUPPORT"], "Support Agent");
  await signInStaff(page, staff, "/admin/finance/ledger");
  await expect(page.getByRole("heading", { level: 1, name: "Ledger" })).toHaveCount(0);
});
