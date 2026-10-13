import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, createUser, cspViolations, seriousAxeViolations, signIn, signInStaff, TOTP, trackCspViolations, watchForBreakage } from "./helpers";

// Stage 8 staff payment-operations UI (docs/stage-8/interface-contracts.md §4, §9, §10) against the mock API.
test.use({ baseURL: AUTH_BASE_URL });

interface Seed {
  unknown_payment_id: string;
  succeeded_payment_id: string;
  dead_lettered_inbox_id: string;
  reconciliation_id: string;
  refund_request_id: string;
  intent_id: string;
}

/** Fresh payment records per test (the mock keeps state in memory; tests run in parallel). */
async function seed(page: Page): Promise<Seed> {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/payments/seed`, { data: {} });
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

/** No control anywhere may mark a payment as succeeded or failed (contract §4, brief §37). */
async function expectNoStatusSettingButton(page: Page) {
  const labels = await page.locator("main").getByRole("button").allTextContents();
  for (const label of labels) expect(label).not.toMatch(/succeed|success|fail|mark (as )?paid/i);
}

test("overview, navigation and attempt detail: SIMULATED labels, raw vs mapped status, timeline, status query with step-up, sandbox panel", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const s = await seed(page);
  const staff = await createStaff(page, ["FINANCE", "ADMIN"], "Fadzai Finance");
  await signInStaff(page, staff, "/admin/payments");

  await expect(page.getByRole("heading", { level: 1, name: "Payments" })).toBeVisible();
  await expect(page.getByText("Live payment execution:")).toContainText("DISABLED");
  for (const gate of ["constant = false", "kill_switch = false", "enabled = false"]) await expect(page.getByText(gate)).toBeVisible();
  await expect(page.getByText("enabled (development only)")).toBeVisible();
  await expect(page.getByText("Refund execution is DISABLED in Stage 8")).toBeVisible();
  await expect(page.locator("main")).not.toContainText("US$"); // counts only, never sums of money
  expect(await axe(page)).toEqual([]);

  // Navigate through the staff nav to the UNKNOWN queue and open the simulated payment.
  await page.getByRole("navigation", { name: "Staff" }).getByRole("link", { name: "Unknown payments" }).click();
  await expect(page).toHaveURL(/\/admin\/payments\/unknown$/);
  await expect(page.getByText("Unknown is not failed")).toBeVisible();
  const row = page.getByRole("row").filter({ hasText: s.unknown_payment_id });
  await expect(row).toContainText("Simulated — development only");
  await expect(row).toContainText("US$25.50");
  await expect(row).toContainText("Reason: Timeout");
  expect(await axe(page)).toEqual([]);
  await row.getByRole("link", { name: s.unknown_payment_id }).click();
  await expect(page).toHaveURL(new RegExp(`/admin/payments/attempts/${s.unknown_payment_id}$`));

  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Payment" })).toBeVisible();
  await expect(main.getByText("Simulated — development only").first()).toBeVisible();
  const calls = page.getByRole("table", { name: "Provider calls" });
  await expect(calls.getByRole("row").filter({ hasText: "Create" })).toContainText("Outcome unknown");
  await expect(calls.getByRole("row").filter({ hasText: "Create" })).toContainText("30000 ms");
  await expect(calls.getByRole("row").filter({ hasText: "Get status" })).toContainText("limbo");
  const tx = page.getByRole("region", { name: "Provider transaction" });
  await expect(tx.getByText("Raw provider status (verbatim)")).toBeVisible();
  await expect(tx).toContainText("limbo");
  await expect(tx).toContainText("sandbox-2026-10-01");
  await expect(tx.locator("dd").filter({ hasText: /^Unknown$/ })).toBeVisible();
  const timeline = page.getByRole("list", { name: "Event timeline" });
  await expect(timeline).toContainText("Applied");
  await expect(timeline).toContainText("Not applied: Lower rank");
  await expect(page.getByRole("table", { name: "Webhooks for this payment" })).toContainText("Dead lettered");
  await expect(page.getByRole("list", { name: "Reconciliation references" })).toContainText("Unknown horizon exceeded");
  await expect(main).toContainText("Handset approval");
  const sandbox = page.getByRole("region", { name: "DEVELOPMENT ONLY — SIMULATOR" });
  await expect(sandbox).toBeVisible();
  await expect(sandbox).toContainText("limbo");
  await expectNoStatusSettingButton(page);
  expect(await axe(page)).toEqual([]);

  // Staff action with step-up: request a provider status query.
  await page.getByRole("button", { name: "Request provider status query" }).click();
  const dialog = page.getByRole("dialog", { name: "Request a provider status query?" });
  await expect(dialog).toContainText("never marks a payment as succeeded or failed");
  await dialog.getByLabel("Reason (staff only)").fill("Donor reports a deduction on their phone.");
  expect(await axe(page)).toEqual([]);
  await dialog.getByRole("button", { name: "Confirm: Request status query" }).click();
  await stepUp(page);
  await expect(page.getByRole("status").filter({ hasText: "Status query scheduled." })).toBeVisible();
  // The status is unchanged: only the provider's authoritative answer can change it.
  await expect(main.getByText("Unknown", { exact: true }).first()).toBeVisible();
  const log = await (await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/payments/log`, { data: {} })).json();
  expect(log.data.status_queries).toContainEqual({ payment_id: s.unknown_payment_id, reason: "Donor reports a deduction on their phone." });

  // Development-only simulator: send a signed webhook through the real endpoint.
  await sandbox.getByLabel("Simulated provider status").selectOption("paid");
  await sandbox.getByLabel("Copies").fill("2");
  await sandbox.getByRole("button", { name: "Send simulated webhook" }).click();
  await expect(sandbox.getByRole("status")).toContainText("Simulated webhook scheduled");

  // Re-queue the dead-lettered webhook from its detail page.
  await page.getByRole("table", { name: "Webhooks for this payment" }).getByRole("link").first().click();
  await expect(page).toHaveURL(new RegExp(`/admin/payments/webhooks/${s.dead_lettered_inbox_id}$`));
  await expect(main).toContainText("Dead lettered");
  await expect(page.getByLabel("Redacted payload")).toContainText("[REDACTED]");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Re-queue" }).click();
  const requeue = page.getByRole("dialog", { name: "Re-queue this webhook?" });
  await requeue.getByLabel("Reason (staff only)").fill("Handler bug fixed in release 42.");
  await requeue.getByRole("button", { name: "Confirm: Re-queue" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Webhook re-queued for processing." })).toBeVisible();
  await expect(main.locator("span", { hasText: /^Received$/ }).first()).toBeVisible();
  await expect(main).not.toContainText("Dead lettered");
  await expect(page.getByRole("button", { name: "Re-queue" })).toHaveCount(0);

  // Webhooks list with the rejections section.
  await page.getByRole("navigation", { name: "Staff" }).getByRole("link", { name: "Payment webhooks" }).click();
  await expect(page.getByRole("heading", { name: "Rejected requests" })).toBeVisible();
  await expect(page.getByRole("table", { name: "Rejected webhook requests" })).toContainText("Signature invalid");
  await page.getByLabel("Status").selectOption("DEAD_LETTERED");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/status=DEAD_LETTERED/);
  expect(await axe(page)).toEqual([]);

  // Reconciliation and providers render; providers show secret reference names only.
  await page.goto("/admin/payments/reconciliation");
  await expect(page.getByRole("heading", { level: 1, name: "Reconciliation" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.goto("/admin/payments/providers");
  await expect(page.getByText("env:PAYMENTS_SANDBOX_WEBHOOK_SECRET")).toBeVisible();
  await expect(page.getByText("Live payment execution:")).toContainText("DISABLED");
  expect(await axe(page)).toEqual([]);
  await page.goto(`/admin/payments/intents/${s.intent_id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Donation intent" })).toBeVisible();
  await expect(page.locator("main")).toContainText("US$25.50");
  expect(await axe(page)).toEqual([]);

  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("refunds: approval with step-up records the decision and shows execution DISABLED", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const s = await seed(page);
  const checker = await createStaff(page, ["FINANCE"], "Chiedza Checker");
  await signInStaff(page, checker, "/admin/payments/refunds");
  await expect(page.getByText("Refund execution is DISABLED in Stage 8")).toBeVisible();
  const item = page.getByRole("list", { name: "Refund requests" }).locator(":scope > li").filter({ hasText: s.succeeded_payment_id });
  await expect(item).toContainText("US$500.00");
  await expect(item).toContainText("Execution: DISABLED");
  await expect(item).toContainText("Maker Staff");
  expect(await axe(page)).toEqual([]);

  await item.getByRole("button", { name: "Approve" }).click();
  const dialog = page.getByRole("dialog", { name: "Approve this refund request?" });
  await expect(dialog).toContainText("Refund execution is DISABLED in Stage 8");
  await dialog.getByRole("button", { name: "Confirm: Approve" }).click();
  await expect(dialog.getByText(/3 to 2,000 characters/).last()).toBeVisible();
  await dialog.getByLabel("Reason (staff only)").fill("Duplicate debit confirmed with the donor.");
  await dialog.getByRole("button", { name: "Confirm: Approve" }).click();
  await stepUp(page);
  await expect(item.getByRole("status")).toContainText("Refund execution is DISABLED in Stage 8: nothing was sent to any provider.");
  await expect(item).toContainText("Approved");
  await expect(item).toContainText("Approved — execution DISABLED: this refund was not sent to any provider.");
  await expect(item.getByRole("button")).toHaveCount(0);
  expect(await axe(page)).toEqual([]);

  // The payment itself is unchanged (no refund executed).
  await page.goto(`/admin/payments/attempts/${s.succeeded_payment_id}`);
  await expect(page.locator("main").getByText("Succeeded", { exact: true }).first()).toBeVisible();
  await expect(page.getByRole("list", { name: "Refund requests" })).toContainText("Execution: DISABLED");
  await expectNoStatusSettingButton(page);
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("refunds: a maker records a request in minor units and cannot approve it", async ({ page }) => {
  const s = await seed(page);
  const maker = await createStaff(page, ["COMPLIANCE"], "Mazvita Maker");
  await signInStaff(page, maker, `/admin/payments/attempts/${s.succeeded_payment_id}`);
  // A simulated payment for a staff member without payment.sandbox.control: no simulator panel.
  await expect(page.getByRole("region", { name: "DEVELOPMENT ONLY — SIMULATOR" })).toHaveCount(0);
  const form = page.getByRole("form", { name: "Request a refund" });
  await form.getByLabel("Amount (USD)").fill("12.345");
  await form.getByLabel("Reason").selectOption("DUPLICATE_PAYMENT");
  await form.getByLabel("Explanation (staff only)").fill("Donor paid twice by mistake.");
  await form.getByRole("button", { name: "Record refund request" }).click();
  await expect(form.getByText(/fewer decimal places/).last()).toBeVisible();
  await form.getByLabel("Amount (USD)").fill("12.34");
  await form.getByRole("button", { name: "Record refund request" }).click();
  await expect(form.getByRole("status")).toContainText("Refund request for US$12.34 recorded");
  const mine = page.getByRole("list", { name: "Refund requests" }).locator(":scope > li").filter({ hasText: "US$12.34" });
  await expect(mine).toContainText("(you)");
  await expect(mine.getByRole("button")).toHaveText(["Withdraw"]);
  expect(await axe(page)).toEqual([]);
});

test("payment pages are a 404 for users and for staff without payment.view", async ({ page }) => {
  const paths = ["/admin/payments", "/admin/payments/attempts", "/admin/payments/unknown", "/admin/payments/webhooks", "/admin/payments/refunds", "/admin/payments/providers"];
  const email = await createUser(page, { phoneVerified: true });
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  for (const path of paths) expect((await page.goto(path))?.status(), path).toBe(404);
  await page.context().clearCookies();
  const kyc = await createStaff(page, ["KYC_REVIEWER"]);
  await signInStaff(page, kyc, "/admin/verification");
  for (const path of paths) expect((await page.goto(path))?.status(), path).toBe(404);
  await page.context().clearCookies();
  const admin = await createStaff(page, ["ADMIN"]); // payment.sandbox.control only
  await signInStaff(page, admin, "/admin/campaigns/featured");
  expect((await page.goto("/admin/payments"))?.status()).toBe(404);
});
