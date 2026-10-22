import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, cspViolations, seriousAxeViolations, signInStaff, TOTP, trackCspViolations, watchForBreakage } from "./helpers";

// Stage 17 staff reconciliation UI against the mock API (e2e/mock-api/reconciliation.mjs). Runs on desktop and Pixel 7.
test.use({ baseURL: AUTH_BASE_URL });

interface Seed {
  import_id: string;
  unknown_exception_id: string;
  missing_exception_id: string;
  batch_id: string;
  run_id: string;
}

async function seed(page: Page): Promise<Seed> {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/reconciliation/seed`, { data: {} });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: Seed }).data;
}

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

/** Steps up when the API asks (a fresh step-up from an earlier action may still be valid). */
async function maybeStepUp(page: Page) {
  const dialog = page.getByRole("dialog", { name: "Confirm it's you" });
  if (await dialog.waitFor({ timeout: 3000 }).then(() => true, () => false)) await stepUp(page);
}

async function confirmIn(dialog: ReturnType<Page["getByRole"]>, name: string) {
  const button = dialog.getByRole("button", { name });
  await button.focus();
  await button.press("Enter");
}

async function myId(page: Page): Promise<string> {
  const res = await page.request.get(`${AUTH_BASE_URL}/api/v1/me`);
  return ((await res.json()) as { data: { id: string } }).data.id;
}

test("reconciliation: TEST evidence, imports, exception lifecycle with maker-checker resolution, rules, reports", async ({ page, browser }) => {
  test.setTimeout(150_000);
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const s = await seed(page);
  const maker = await createStaff(page, ["FINANCE"], "Maker Finance");
  await signInStaff(page, maker, "/admin/reconciliation");
  const main = page.locator("main");

  // dashboard: TEST banner, production gates blocked, no money claims
  await expect(page.getByRole("heading", { level: 1, name: "Reconciliation" })).toBeVisible();
  await expect(main).toContainText("TEST evidence only");
  await expect(main).toContainText("Blocked");
  await expect(main).not.toContainText(/settled funds:|available balance/i);
  await expect(page.getByRole("table", { name: "Statement sources" })).toContainText("fundzim_operating_bank_test");
  expect(await axe(page)).toEqual([]);

  // imports: a statement upload (step-up), the duplicate refused by hash, a malformed file failed with its report
  await page.getByRole("navigation", { name: "Reconciliation sections" }).getByRole("link", { name: "Imports and runs" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Imports and runs" })).toBeVisible();
  const form = page.locator("form").filter({ has: page.getByRole("button", { name: "Import statement" }) });
  await form.getByLabel("Source").selectOption("sandbox_transactions_test");
  const good = { name: "statement.csv", mimeType: "text/csv", buffer: Buffer.from(`record_type,ref\nSTATEMENT,E2E-${Date.now()}\n`) };
  await form.getByLabel(/Statement file/).setInputFiles(good);
  await form.getByRole("button", { name: "Import statement" }).click();
  await stepUp(page);
  await expect(form.getByRole("status")).toContainText("Statement received");
  await form.getByLabel("Source").selectOption("sandbox_transactions_test");
  await form.getByLabel(/Statement file/).setInputFiles(good);
  await form.getByRole("button", { name: "Import statement" }).click();
  await expect(form.getByRole("alert")).toContainText("already imported");
  await form.getByLabel("Source").selectOption("sandbox_settlement_test");
  await form.getByLabel(/Statement file/).setInputFiles({ name: "bad.csv", mimeType: "text/csv", buffer: Buffer.from(`record_type,amount_minor\nLINE,12.50 ${Date.now()}\n`) });
  await form.getByRole("button", { name: "Import statement" }).click();
  await form.getByRole("link", { name: "View the import" }).click();
  await expect(page.getByRole("table", { name: "Validation errors" })).toContainText("Not minor units");
  await expect(main).toContainText("Nothing from it was recorded");
  expect(await axe(page)).toEqual([]);

  // exception: assign to myself, start, propose applying the statement outcome
  await page.goto(`/admin/reconciliation/exceptions/${s.unknown_exception_id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Unknown payment outcome" })).toBeVisible();
  await expect(main).toContainText("nothing is applied automatically");
  const me = await myId(page);
  await page.getByRole("button", { name: "Assign" }).click();
  const assign = page.getByRole("dialog", { name: "Assign an investigator" });
  await assign.getByLabel("Staff member id").fill(me);
  await confirmIn(assign, "Confirm: Assign");
  await expect(page.getByRole("status").filter({ hasText: "Assigned." })).toBeVisible();
  await page.getByRole("button", { name: "Start investigation" }).click();
  const start = page.getByRole("dialog", { name: "Start the investigation?" });
  await start.getByLabel("Note (staff only)").fill("Checking the statement line.");
  await confirmIn(start, "Confirm: Start");
  await expect(page.getByRole("status").filter({ hasText: "Investigation started." })).toBeVisible();
  const proposal = page.locator("form").filter({ has: page.getByRole("button", { name: "Propose resolution" }) });
  await proposal.getByLabel("Resolution").selectOption("PROVIDER_OUTCOME_APPLIED");
  await proposal.getByLabel(/Justification/).fill("The TEST statement reports the payment as succeeded at the provider.");
  await proposal.getByRole("button", { name: "Propose resolution" }).click();
  // the confirmation survives the refresh that moves the exception to PENDING_APPROVAL (and removes the form)
  await expect(page.getByRole("status").filter({ hasText: "second team member" })).toBeVisible();
  await expect(proposal).toHaveCount(0);
  await page.reload();
  await expect(main).toContainText("Pending review");
  expect(await axe(page)).toEqual([]);

  // the maker cannot approve their own proposal
  await page.getByRole("button", { name: "Approve and apply" }).click();
  const self = page.getByRole("dialog", { name: "Approve this resolution?" });
  await self.getByLabel("Note (staff only)").fill("Approving my own proposal.");
  await confirmIn(self, "Confirm: Approve");
  await maybeStepUp(page);
  await expect(self.getByRole("alert")).toContainText("You can't decide this");
  await self.getByRole("button", { name: "Cancel" }).click();

  // a second FINANCE officer approves; the exception is resolved
  const ctx2 = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const page2 = await ctx2.newPage();
  const checker = await createStaff(page2, ["FINANCE"], "Checker Finance");
  await signInStaff(page2, checker, `/admin/reconciliation/exceptions/${s.unknown_exception_id}`);
  await page2.getByRole("button", { name: "Approve and apply" }).click();
  const approve = page2.getByRole("dialog", { name: "Approve this resolution?" });
  await approve.getByLabel("Note (staff only)").fill("Statement evidence checked.");
  await confirmIn(approve, "Confirm: Approve");
  await stepUp(page2);
  await page2.reload();
  await expect(page2.locator("main")).toContainText("Executed");
  await expect(page2.locator("main")).toContainText("Resolved");
  await ctx2.close();

  // settlements, payouts, ledger and fees views
  await page.goto("/admin/reconciliation/settlements");
  await expect(page.getByRole("table", { name: "Settlement batches" })).toContainText("USD 28.95");
  await page.goto("/admin/reconciliation/payouts");
  await expect(main).toContainText("never resubmitted");
  await expect(page.getByRole("table", { name: "Payouts in flight or unknown" })).toContainText("Unknown");
  expect(await axe(page)).toEqual([]);
  await page.goto("/admin/reconciliation/ledger");
  await expect(page.getByRole("table", { name: "Trial balance by currency" })).toContainText("Yes");
  await expect(main).toContainText("no settlement postings in Stage 17");
  await page.goto("/admin/reconciliation/fees");
  await expect(main).toContainText("SYNTHETIC or UNVERIFIED");

  // rules: the active set and a new proposal
  await page.goto("/admin/reconciliation/rules");
  await expect(main).toContainText("Active");
  const rules = page.locator("form").filter({ has: page.getByRole("button", { name: "Propose rule set" }) });
  await rules.getByLabel("Why (staff only)").fill("Wider date window for a TEST provider report.");
  await rules.getByLabel("Date window (days)").fill("3");
  await rules.getByRole("button", { name: "Propose rule set" }).click();
  // desktop and mobile share one mock: the other project's proposal may already be pending (one pending set at a time)
  await expect(rules.getByRole("status").or(rules.getByRole("alert"))).toContainText(/second team member|waiting for a decision/);
  expect(await axe(page)).toEqual([]);

  // reports: a bounded table and an audited CSV export
  await page.goto("/admin/reconciliation/reports?kind=daily_summary");
  await expect(page.getByRole("table", { name: "Daily summary" })).toContainText("sandbox_transactions_test");
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export CSV" }).click();
  await maybeStepUp(page);
  expect((await download).suggestedFilename()).toMatch(/TEST\.csv$/);

  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("reconciliation pages are staff-only and need the permission", async ({ page }) => {
  const reviewer = await createStaff(page, ["REVIEWER"], "Campaign Reviewer");
  await signInStaff(page, reviewer, "/admin/campaigns/review");
  await expect(page.getByRole("navigation", { name: "Staff" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Staff" })).not.toContainText("Reconciliation overview");
  await page.goto("/admin/reconciliation");
  await expect(page.getByRole("heading", { level: 1 })).toContainText(/couldn't find that page/i);
});
