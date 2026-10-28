import { expect, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, cspViolations, seriousAxeViolations, signInStaff, TOTP, trackCspViolations, watchForBreakage, test } from "./helpers";

// Stage 13 staff risk UI against the mock API (e2e/mock-api/risk.mjs). TEST data only: a score is never proof of fraud,
// production screening is BLOCKED, a provider "no match" is never a clearance.
test.use({ baseURL: AUTH_BASE_URL });

interface Seed {
  rule_id: string;
  version_id: string;
  case_id: string;
  hold_id: string;
  screening_id: string;
}

async function seed(page: Page): Promise<Seed> {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/risk/seed`, { data: {} });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: Seed }).data;
}

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function confirmIn(dialog: ReturnType<Page["getByRole"]>, name: string | RegExp) {
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

const FINDING = /\b(fraudster|fraudulent|sanctioned)\b/i;

test("risk: overview banner, rule approval (checker), case assignment and hold release with step-up", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const s = await seed(page);
  const staff = await createStaff(page, ["COMPLIANCE"], "Risk Analyst");
  await signInStaff(page, staff, "/admin/risk");

  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Risk operations" })).toBeVisible();
  await expect(main).toContainText("never proof of fraud");
  await expect(main).toContainText("production screening BLOCKED");
  expect(await axe(page)).toEqual([]);

  // a seeded pending rule version (made by another person): approve it as the checker, binding the definition hash
  await page.goto(`/admin/risk/rules/${s.rule_id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Risk rule" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Approve version" }).click();
  const approve = page.getByRole("dialog", { name: "Approve this rule version?" });
  await approve.getByLabel("Reason (staff only)").fill("E2E: definition reviewed by the checker");
  await expect(approve.getByLabel(/Bind my decision/)).toBeChecked();
  await confirmIn(approve, "Confirm: Approve");
  await stepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Version approved." })).toBeVisible();

  // the case workspace: take the case
  await page.goto(`/admin/risk/cases/${s.case_id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Risk case" })).toBeVisible();
  await expect(main).toContainText("Risk record legal review required");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Assign" }).first().click();
  const assign = page.getByRole("dialog", { name: "Assign this case?" });
  await confirmIn(assign, "Confirm: Assign");
  await expect(page.getByRole("status").filter({ hasText: "Case assigned." })).toBeVisible();

  // a release requested by someone else: approve it as the checker
  await page.goto("/admin/risk/holds");
  await expect(page.getByRole("heading", { level: 1, name: "Risk holds" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  const row = page.getByRole("row").filter({ hasText: s.hold_id.slice(0, 12) });
  await row.getByRole("button", { name: "Approve release" }).click();
  const release = page.getByRole("dialog", { name: "Approve the release of this hold?" });
  await release.getByLabel("Reason (staff only)").fill("E2E: review completed, release checked");
  await confirmIn(release, /^Confirm/);
  // a released hold leaves the open list (its row, and the action's notice with it)
  await expect(release).toHaveCount(0);
  await expect(page.getByRole("row").filter({ hasText: s.hold_id.slice(0, 12) })).toHaveCount(0);

  expect(await main.textContent()).not.toMatch(FINDING);
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("risk: TEST screening wording and the finance read-only view", async ({ page, browser }) => {
  await trackCspViolations(page);
  const s = await seed(page);
  const staff = await createStaff(page, ["COMPLIANCE"], "Screening Analyst");
  await signInStaff(page, staff, "/admin/risk/screening");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Screening (TEST)" })).toBeVisible();
  await expect(main).toContainText("BLOCKED");
  await expect(main).toContainText("TEST_ONLY");
  expect(await axe(page)).toEqual([]);
  await page.goto(`/admin/risk/screening/${s.screening_id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Screening request (TEST)" })).toBeVisible();
  await expect(main).toContainText(/analyst review required/i);
  expect(await main.textContent()).not.toMatch(FINDING);
  expect(await axe(page)).toEqual([]);

  // FINANCE may read the risk pages; any change is refused by the API (risk.operations.view only)
  const ctx = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const other = await ctx.newPage();
  const finance = await createStaff(other, ["FINANCE"], "Finance Viewer");
  await signInStaff(other, finance, "/admin/risk");
  await expect(other.getByRole("heading", { level: 1, name: "Risk operations" })).toBeVisible();
  await other.goto(`/admin/risk/rules/${s.rule_id}`);
  await expect(other.getByRole("heading", { level: 1, name: "Risk rule" })).toBeVisible();
  // Stage 14 (KI-S13-21): actions the caller's roles do not include are not offered at all …
  await expect(other.getByRole("button", { name: "Approve version" })).toHaveCount(0);
  // … and the API still refuses them when sent directly
  const status = await other.evaluate(async (vid) => {
    const csrf = /(?:^|; )fz_csrf=([^;]+)/.exec(document.cookie)?.[1] ?? "";
    const r = await fetch(`/api/v1/admin/risk/rule-versions/${vid}/approve`, {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      body: JSON.stringify({ reason: "E2E: finance has no approval permission" }),
    });
    return r.status;
  }, s.version_id);
  expect(status).toBe(403);
  await ctx.close();
  expect(await cspViolations(page)).toEqual([]);
});
