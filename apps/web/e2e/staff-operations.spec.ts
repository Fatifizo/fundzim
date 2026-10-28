import { expect, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, cspViolations, seriousAxeViolations, signInStaff, TOTP, trackCspViolations, watchForBreakage, test } from "./helpers";

// Stage 14 operations portal against the mock API (e2e/mock-api/operations.mjs). TEST data only. Navigation and
// actions follow the caller's permissions (presentation only — the API authorizes every route).
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

const staffNav = (page: Page) => page.getByRole("navigation", { name: "Staff", exact: true });

test("support: command center, permission-aware navigation and a support case lifecycle", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const staff = await createStaff(page, ["SUPPORT"], "Support Agent");
  await signInStaff(page, staff, "/admin/operations");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Operations command center" })).toBeVisible();
  await expect(main).toContainText("never business performance");
  // a module that could not be read is reported, never shown as zero
  await expect(main.getByText("Some modules could not be read")).toBeVisible();
  await expect(main).toContainText("Beneficiaries: Unavailable");
  await expect(main.getByText("Open support cases")).toBeVisible();
  await expect(main).not.toContainText("Open risk cases");
  await expect(page.getByText("My tasks: 0")).toBeVisible();
  // navigation: only what SUPPORT can open
  await expect(staffNav(page).getByRole("link", { name: "Support cases" })).toBeVisible();
  await expect(staffNav(page).getByRole("link", { name: "Ledger", exact: true })).toHaveCount(0);
  await expect(staffNav(page).getByRole("link", { name: "Audit log" })).toHaveCount(0);
  await expect(staffNav(page).getByRole("link", { name: "Risk cases" })).toHaveCount(0);
  expect(await axe(page)).toEqual([]);
  // frontend visibility is not authorization: the page itself 404s
  const audit = await page.goto("/admin/audit");
  expect(audit?.status()).toBe(404);

  await page.goto("/admin/support");
  await expect(page.getByRole("heading", { level: 1, name: "Support cases" })).toBeVisible();
  const form = page.getByRole("form", { name: "Open a support case" });
  await form.getByLabel("Subject").fill("Donor asks about a TEST donation");
  await form.getByLabel("Category").selectOption("DONATION");
  await form.getByRole("button", { name: "Open support case" }).click();
  await stepUp(page);
  await expect(page).toHaveURL(/\/admin\/support\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { level: 1, name: /Support case #\d+/ })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "Breadcrumb" }).getByRole("link", { name: "Support cases" })).toBeVisible();

  await page.getByRole("button", { name: "Assign" }).click();
  let dialog = page.getByRole("dialog", { name: "Assign this support case?" });
  await confirmIn(dialog, "Confirm: Assign");
  await expect(page.getByRole("status").filter({ hasText: "Case assigned." })).toBeVisible();
  await expect(main).toContainText("Triage");

  await page.getByRole("button", { name: "Escalate" }).click();
  dialog = page.getByRole("dialog", { name: "Escalate this case?" });
  await expect(dialog).toContainText("The case stays yours");
  await dialog.getByLabel("Team").selectOption("PAYMENTS");
  await dialog.getByLabel("Note (staff only)").fill("Needs the payments team");
  await confirmIn(dialog, "Confirm: Escalate");
  await expect(page.getByRole("status").filter({ hasText: "Case escalated." })).toBeVisible();
  await expect(main).toContainText(/Escalated to\s*Payments/);

  // resolving needs a note: the dialog says so and nothing is sent
  await page.getByRole("button", { name: "Change status" }).click();
  dialog = page.getByRole("dialog", { name: "Change this case's status?" });
  await dialog.getByLabel("New status").selectOption("RESOLVED");
  await confirmIn(dialog, "Confirm: Change status");
  await expect(dialog.getByRole("alert").first()).toBeVisible();
  await dialog.getByLabel("Note (optional, staff only)").fill("Explained how TEST donations work");
  await confirmIn(dialog, "Confirm: Change status");
  await expect(page.getByRole("status").filter({ hasText: "Case status changed." })).toBeVisible();
  await expect(page.getByRole("table", { name: "Support case history" })).toContainText("Escalated");
  expect(await axe(page)).toEqual([]);
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("compliance: approvals route to the module, exception triage, audit search and export, SLA proposal", async ({ page }, testInfo) => {
  await trackCspViolations(page);
  const seeded = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/ops/seed-exception`, { data: {} });
  expect(seeded.status()).toBe(201);
  const { token } = ((await seeded.json()) as { data: { token: string } }).data;
  const staff = await createStaff(page, ["COMPLIANCE"], "Compliance Officer");
  await signInStaff(page, staff, "/admin/approvals");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Approval center" })).toBeVisible();
  await expect(main.getByRole("link", { name: "Review and decide in Risk" })).toHaveAttribute("href", /\/admin\/risk\/rules\//);
  await expect(main).not.toContainText("Ledger adjustment"); // no ledger permission, not the maker: not listed
  await expect(main.getByRole("button", { name: /approve/i })).toHaveCount(0);
  expect(await axe(page)).toEqual([]);

  await page.goto("/admin/operations/exceptions");
  await expect(page.getByRole("heading", { level: 1, name: "Exception center" })).toBeVisible();
  await main.getByRole("row").filter({ hasText: `TEST screening ended in error ${token}` }).getByRole("link", { name: "Triage" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Exception triage" })).toBeVisible();
  await page.getByRole("button", { name: "Take ownership" }).click();
  let dialog = page.getByRole("dialog", { name: "Assign this exception?" });
  await confirmIn(dialog, "Confirm: Assign");
  await stepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Exception assigned." })).toBeVisible();
  await expect(page.getByRole("table", { name: "Triage history" })).toContainText("Status acknowledged");
  await page.getByRole("button", { name: "Change triage status" }).click();
  dialog = page.getByRole("dialog", { name: "Change the triage status?" });
  await dialog.getByLabel("New status").selectOption("RESOLVED");
  await dialog.getByLabel("Note (optional, staff only)").fill("Looks fine from here");
  await confirmIn(dialog, "Confirm: Change status");
  await expect(dialog.getByText(/still reports this exception/).first()).toBeVisible();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  expect(await axe(page)).toEqual([]);

  await page.goto("/admin/audit");
  await expect(page.getByRole("heading", { level: 1, name: "Audit log" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Audit records" })).toBeVisible();
  await page.getByRole("link", { name: "Older records" }).click();
  await expect(page).toHaveURL(/cursor=\d+/);
  const exportForm = page.getByRole("form", { name: "Export audit records" });
  const today = new Date().toISOString().slice(0, 10);
  await exportForm.getByLabel("From (date)").fill(today);
  await exportForm.getByLabel("To (date)").fill(today);
  const download = page.waitForEvent("download");
  await exportForm.getByRole("button", { name: "Export" }).click();
  expect((await download).suggestedFilename()).toBe("fundzim-audit-mock.csv");
  await expect(page.getByRole("status").filter({ hasText: "recorded in the security audit log" })).toBeVisible();
  expect(await axe(page)).toEqual([]);

  await page.goto("/admin/operations/sla");
  await expect(page.getByRole("heading", { level: 1, name: "Service targets" })).toBeVisible();
  const sla = page.getByRole("form", { name: "Propose an internal service target" });
  // one pending proposal per metric (shared mock): each project proposes its own
  await sla.getByLabel("Metric").selectOption(testInfo.project.name.startsWith("mobile") ? "SUPPORT_FIRST_RESPONSE" : "SUPPORT_RESOLUTION");
  await sla.getByLabel("Target (hours)").fill("72");
  await sla.getByLabel("Reason").fill("Internal target agreed with the support lead");
  await sla.getByRole("button", { name: "Propose target" }).click();
  await expect(page.getByRole("status").filter({ hasText: "only after a different person approves it" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Service targets" })).toContainText("Waiting for a different person");
  expect(await cspViolations(page)).toEqual([]);
});

test("security and super admin: staff directory, session revocation and an access review", async ({ page, browser }) => {
  const secName = `Security Admin ${Date.now() % 100000}`;
  const sec = await createStaff(page, ["SECURITY_ADMIN"], secName);
  const ctx = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const other = await ctx.newPage();
  const supName = `Super Admin ${Date.now() % 100000}`;
  const sup = await createStaff(other, ["SUPER_ADMIN"], supName);
  await signInStaff(other, sup, "/admin/staff");
  const superId = await other.evaluate(async () => ((await (await fetch("/api/v1/me")).json()) as { data: { id: string } }).data.id);

  await signInStaff(page, sec, "/admin/staff");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Staff directory" })).toBeVisible();
  await expect(main).not.toContainText("@example.test"); // no email addresses in the directory
  await main.getByRole("link", { name: supName }).click();
  await expect(page.getByRole("heading", { level: 1, name: supName })).toBeVisible();
  await expect(page.getByRole("heading", { name: /Effective permissions/ })).toBeVisible();
  await page.getByRole("button", { name: "End all sessions" }).click();
  const dialog = page.getByRole("dialog", { name: `End all of ${supName}'s sessions?` });
  await dialog.getByLabel("Reason (staff only)").fill("Suspected compromise drill");
  await confirmIn(dialog, "Confirm: End sessions");
  await stepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Sessions ended." })).toBeVisible();
  expect(await axe(page)).toEqual([]);

  await page.goto("/admin/staff/access-reviews");
  const form = page.getByRole("form", { name: "Start an access review" });
  await form.getByLabel("Title").fill("Quarterly drill review");
  await form.getByLabel("Role in scope").selectOption("SECURITY_ADMIN");
  await form.getByLabel("Reviewer staff id").fill(superId);
  await form.getByRole("button", { name: "Start access review" }).click();
  await expect(page).toHaveURL(/\/admin\/staff\/access-reviews\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { level: 1, name: "Quarterly drill review" })).toBeVisible();
  const reviewUrl = page.url();
  // the security admin is not the reviewer: no decision buttons
  await expect(main.getByRole("button", { name: "Decide" })).toHaveCount(0);
  expect(await axe(page)).toEqual([]);

  // the reviewer recommends revocation: a dual-controlled role request is filed, nothing is removed yet
  await other.goto(reviewUrl);
  await other.getByRole("row").filter({ hasText: secName }).getByRole("button", { name: "Decide" }).click();
  const d2 = other.getByRole("dialog", { name: `Decide ${secName}'s Security admin access?` });
  await d2.getByLabel("Decision").selectOption("REVOKE_RECOMMENDED");
  await d2.getByLabel("Reason (staff only)").fill("No longer needs this role");
  await confirmIn(d2, "Confirm: Record decision");
  await stepUp(other);
  await expect(other.getByRole("table", { name: "Access review items" })).toContainText("Revoke recommended");
  await ctx.close();
});
