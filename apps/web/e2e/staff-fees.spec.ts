import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, cspViolations, seriousAxeViolations, signInStaff, TOTP, trackCspViolations, watchForBreakage } from "./helpers";

// Stage 12 staff fee UI against the mock API (e2e/mock-api/fees.mjs). TEST fees only.
test.use({ baseURL: AUTH_BASE_URL });

interface Seed {
  campaign_id: string;
  assessment_id: string;
  adjustment_id: string;
  recon_id: string;
  sandbox_provider_id: string;
}

async function seed(page: Page): Promise<Seed> {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/fees/seed`, { data: {} });
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

async function approveVersion(page: Page, reason: string) {
  await page.getByRole("button", { name: "Approve version" }).click();
  const dialog = page.getByRole("dialog", { name: "Approve this fee policy version?" });
  await dialog.getByLabel("Reason (staff only)").fill(reason);
  await expect(dialog.getByLabel(/Bind my decision to the terms I reviewed/)).toBeChecked();
  await confirmIn(dialog, "Confirm: Approve");
  return dialog;
}

test("fees: TEST banner, policy maker-checker with step-up and the adjustment checker", async ({ page, browser }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const s = await seed(page);
  // the maker also holds the approver role, so the self-approval refusal can be shown
  const maker = await createStaff(page, ["FINANCE", "BUSINESS_APPROVER"], "Fee Maker");
  await signInStaff(page, maker, "/admin/finance/fees");

  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Fees" })).toBeVisible();
  await expect(main).toContainText("TEST FEES ONLY");
  await expect(main).toContainText("Production fees: BLOCKED");
  expect(await axe(page)).toEqual([]);

  // create a campaign-scoped policy (step-up), then land on its page
  await page.getByRole("navigation", { name: "Staff" }).getByRole("link", { name: "Fee policies" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Fee policies" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  const code = `e2e.${s.campaign_id.slice(0, 8)}`;
  const create = page.getByRole("form", { name: "Create a fee policy" });
  await create.getByLabel("Code").fill("Not A Code");
  await create.getByLabel("Name").fill("E2E platform fee");
  await create.getByRole("button", { name: "Create policy" }).click();
  await expect(create.getByRole("alert").first()).toBeVisible();
  await create.getByLabel("Code").fill(code);
  await create.getByLabel("Campaign id (optional)").fill(s.campaign_id);
  await create.getByRole("button", { name: "Create policy" }).click();
  await stepUp(page);
  await expect(page).toHaveURL(/\/admin\/finance\/fees\/policies\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { level: 1, name: "Fee policy" })).toBeVisible();
  await expect(main).toContainText(code);

  // draft a version: 3 %, organiser covers it, proportional refunds
  const draft = page.getByRole("form", { name: "Draft a fee policy version" });
  await draft.getByLabel("Rate (%)").fill("3");
  await draft.getByRole("button", { name: "Create draft" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Draft version created." })).toBeVisible();
  const v1 = page.getByRole("region", { name: "Version 1" });
  await expect(v1).toContainText("3.00%");
  await expect(v1).toContainText("Draft");
  expect(await axe(page)).toEqual([]);

  await v1.getByRole("button", { name: "Submit for approval" }).click();
  await confirmIn(page.getByRole("dialog", { name: "Submit this draft for approval?" }), "Confirm: Submit");
  await expect(page.getByRole("status").filter({ hasText: "Submitted for approval." })).toBeVisible();
  await expect(v1).toContainText("Pending approval");

  // the maker cannot approve their own version
  const self = await approveVersion(page, "Approving my own draft should fail.");
  await expect(self.getByRole("alert")).toContainText("You can't decide this");
  await self.getByRole("button", { name: "Cancel" }).click();
  const policyUrl = page.url();

  // a different person (BUSINESS_APPROVER) approves with step-up, bound to the terms hash
  const ctx2 = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const page2 = await ctx2.newPage();
  const checker = await createStaff(page2, ["BUSINESS_APPROVER"], "Fee Checker");
  await signInStaff(page2, checker, new URL(policyUrl).pathname);
  await approveVersion(page2, "Synthetic TEST rate reviewed against the brief.");
  await stepUp(page2);
  await expect(page2.getByRole("status").filter({ hasText: "Version approved." })).toBeVisible();
  await expect(page2.getByRole("region", { name: "Version 1" })).toContainText("Approved");

  // the checker also decides the seeded adjustment (requested by someone else)
  await page2.goto("/admin/finance/fees/adjustments");
  await expect(page2.getByRole("table", { name: "Fee adjustments" })).toBeVisible();
  expect(await axe(page2)).toEqual([]);
  const adjRow = page2.getByRole("row").filter({ hasText: `TEST_ONLY seeded correction request ${s.campaign_id.slice(0, 8)}` });
  await adjRow.getByRole("button", { name: "Approve adjustment" }).click();
  const adj = page2.getByRole("dialog", { name: "Approve this fee adjustment?" });
  await adj.getByLabel("Reason (staff only)").fill("Provider correction checked.");
  await confirmIn(adj, "Confirm: Approve");
  await expect(page2.getByRole("status").filter({ hasText: "Adjustment approved." })).toBeVisible();
  await ctx2.close();

  // the maker activates (step-up still fresh)
  await page.reload();
  await v1.getByRole("button", { name: "Activate" }).click();
  await confirmIn(page.getByRole("dialog", { name: "Activate this version?" }), "Confirm: Activate");
  await expect(page.getByRole("status").filter({ hasText: "Version activated." })).toBeVisible();
  // active from its future effective start: not yet in force (activation never makes a version retroactive)
  await expect(v1).toContainText("Active");
  await expect(v1.getByRole("button", { name: "Suspend" })).toBeVisible();

  const log = await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/fees/log`, { data: {} })).json();
  const kinds = log.data.actions.filter((a: { id?: string; kind: string }) => a.kind.startsWith("version.")).map((a: { kind: string; terms_hash_bound?: boolean }) => `${a.kind}:${a.terms_hash_bound ?? false}`);
  expect(kinds).toEqual(expect.arrayContaining(["version.submit:false", "version.approve:true", "version.activate:false"]));
  expect(log.data.actions.some((a: { kind: string; id?: string }) => a.kind === "adjustment.approve" && a.id === s.adjustment_id)).toBe(true);
  expect(problems).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
});

test("fees: provider evidence, reconciliation, assessment detail and the revenue report", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const s = await seed(page);
  const maker = await createStaff(page, ["FINANCE", "BUSINESS_APPROVER"], "Fee Operator");
  await signInStaff(page, maker, "/admin/finance/fees/provider-schedules");
  const main = page.locator("main");

  // provider evidence: VERIFIED is never offered; the recorder cannot approve their own evidence (step-up first)
  await expect(page.getByRole("heading", { level: 1, name: "Provider fee evidence" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  const ev = page.getByRole("form", { name: "Record provider fee evidence" });
  await expect(ev.getByLabel("Confirmation").locator("option")).toHaveText(["Synthetic", "Unverified"]);
  await ev.getByLabel("Provider id").fill(s.sandbox_provider_id);
  await ev.getByLabel("Contract reference").fill(`E2E-${s.campaign_id.slice(0, 8)}`);
  await ev.getByLabel("Rate (%)").fill("1.5");
  await ev.getByLabel("Evidence source").fill("Synthetic TEST fixture for E2E");
  await ev.getByRole("button", { name: "Record evidence" }).click();
  await stepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Provider fee evidence recorded." })).toBeVisible();
  const evRow = page.getByRole("table", { name: "Provider fee evidence" }).getByRole("row").filter({ hasText: `E2E-${s.campaign_id.slice(0, 8)}` });
  await evRow.getByRole("button", { name: "Approve evidence" }).click();
  const evDialog = page.getByRole("dialog", { name: "Approve this provider fee evidence?" });
  await confirmIn(evDialog, "Confirm: Approve");
  await expect(evDialog.getByRole("alert")).toContainText("You can't decide this");
  await evDialog.getByRole("button", { name: "Cancel" }).click();

  // reconciliation: run, then resolve the seeded item
  await page.goto("/admin/finance/fees/reconciliation");
  await expect(page.getByRole("heading", { level: 1, name: "Fee reconciliation" })).toBeVisible();
  await expect(main).toContainText("NOT_PERFORMED");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Run fee reconciliation" }).click();
  await confirmIn(page.getByRole("dialog", { name: "Run fee reconciliation now?" }), "Confirm: Run");
  await expect(page.getByRole("status").filter({ hasText: "Fee reconciliation run finished." })).toBeVisible();
  const recRow = page.getByRole("table", { name: "Fee reconciliation items" }).getByRole("row").filter({ hasText: s.campaign_id.slice(0, 8) });
  await recRow.getByRole("button", { name: "Resolve" }).click();
  const res = page.getByRole("dialog", { name: "Resolve this reconciliation item?" });
  await res.getByLabel("Note (staff only)").fill("No provider evidence exists in Stage 12.");
  await confirmIn(res, "Confirm: Resolve");
  await expect(page.getByRole("status").filter({ hasText: "Item resolved." })).toBeVisible();
  await expect(recRow.getByRole("button", { name: "Resolve" })).toHaveCount(0);

  // assessment detail and revenue
  await page.goto(`/admin/finance/fees/assessments/${s.assessment_id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Fee assessment" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Fee journals" })).toContainText("Capture");
  expect(await axe(page)).toEqual([]);
  await page.goto("/admin/finance/revenue");
  await expect(page.getByRole("heading", { level: 1, name: "Revenue (TEST)" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Fees and revenue in USD" })).toBeVisible();
  await expect(main).toContainText("no cross-currency total");
  await expect(main).toContainText("Not available (no settlement evidence)");
  expect(await axe(page)).toEqual([]);

  const log = await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/fees/log`, { data: {} })).json();
  expect(log.data.actions.some((a: { kind: string; id?: string }) => a.kind === "reconciliation.resolve" && a.id === s.recon_id)).toBe(true);
  expect(problems).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
});

test("fee pages are not available to staff without fee.view", async ({ page }) => {
  const s = await seed(page);
  const staff = await createStaff(page, ["SUPPORT"], "Support Agent");
  await signInStaff(page, staff, "/admin/finance/fees");
  await expect(page.getByRole("heading", { level: 1, name: "Fees" })).toHaveCount(0);
  await page.goto(`/admin/finance/fees/assessments/${s.assessment_id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Fee assessment" })).toHaveCount(0);
  await page.goto("/admin/finance/revenue");
  await expect(page.getByRole("heading", { level: 1, name: "Revenue (TEST)" })).toHaveCount(0);
});
