import { expect, type Page } from "@playwright/test";

import { test } from "../e2e-common/fresh-connections";
import { answerStepUpIfAsked, staffPage } from "./support/browser";
import { WEB_URL } from "./support/env";
import { loadFixture } from "./support/state";

/**
 * Stage 17 reconciliation against the REAL stack (web → Go API → storage + ClamAV → worker parser and matcher →
 * PostgreSQL). A FINANCE officer imports a canonical TEST statement through the UI; the compose worker scans and parses
 * it; a run decides its line (a reference that names no FundZim payment: MISSING_PAYMENT, never matched by amount).
 * COMPLIANCE reads the dashboard; a REVIEWER does not find it. TEST evidence only.
 */
test.describe.configure({ mode: "serial" });

const S = (key: string) => {
  const c = loadFixture().staff[key];
  if (!c) throw new Error(`staff fixture ${key} missing`);
  return c;
};

async function csrfPost(page: Page, path: string, data: unknown) {
  const csrf = (await page.context().cookies()).find((k) => k.name === "fz_csrf")?.value ?? "";
  return page.request.post(path, { data, headers: { "X-CSRF-Token": csrf, Origin: WEB_URL, "Idempotency-Key": crypto.randomUUID() } });
}

test("a TEST statement imported in the UI is scanned, parsed and reconciled; the dashboard stays TEST-only", async ({ browser }) => {
  test.setTimeout(300_000);
  const finance1 = S("finance1");
  const { ctx, page } = await staffPage(browser, finance1, "/admin/reconciliation/batches");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Imports and runs" })).toBeVisible();
  await expect(main).toContainText("TEST evidence only");

  // a matching rule set must be active (none is seeded): finance1 proposes, finance2 approves (step-up), over the API
  const sets = (await (await page.request.get("/api/v1/admin/reconciliation/rule-sets")).json()) as { data: { items: Array<{ id: string; status: string }> } };
  if (!sets.data.items.some((r) => r.status === "ACTIVE")) {
    let pending = sets.data.items.find((r) => r.status === "PENDING_APPROVAL");
    if (!pending) {
      const p = await csrfPost(page, "/api/v1/admin/reconciliation/rule-sets", { config: { date_window_days: 2, settlement_horizon_days: 1, stale_after_hours: 720 },
        change_reason: "real-stack e2e: engineering values for TEST statements" });
      expect(p.status(), await p.text()).toBe(201);
      pending = ((await p.json()) as { data: { id: string; status: string } }).data;
    }
    const checker = await staffPage(browser, S("finance2"), "/admin/reconciliation/rules");
    await checker.page.getByRole("button", { name: "Approve" }).first().click();
    const dialog = checker.page.getByRole("dialog", { name: "Approve and activate this rule set?" });
    await dialog.getByLabel("Note (staff only)").fill("Checked for the TEST statements.");
    await dialog.getByRole("button", { name: "Confirm: Approve" }).click();
    await answerStepUpIfAsked(checker.page, S("finance2"), checker.page.getByRole("status").filter({ hasText: "approved" }));
    await expect(checker.page.getByRole("status").filter({ hasText: "Rule set approved and active." })).toBeVisible();
    await checker.ctx.close();
  }

  // import through the UI (step-up)
  const token = `${Date.now()}`;
  const now = new Date();
  const statement = {
    format: "FUNDZIM_CANONICAL_V1",
    statement: { ref: `E2E-${token}`, currency: "USD", period_start: new Date(now.getTime() - 3600_000).toISOString(), period_end: now.toISOString(), declared_lines: "1" },
    lines: [{ ref: `E2E-L-${token}`, kind: "PAYMENT", direction: "CREDIT", status: "SUCCEEDED", amount_minor: "1234", currency: "USD",
      provider_ref: `e2e_unknown_${token}`, booked_at: new Date(now.getTime() - 60_000).toISOString() }],
  };
  const form = page.locator("form").filter({ has: page.getByRole("button", { name: "Import statement" }) });
  await form.getByLabel("Source").selectOption("sandbox_transactions_test");
  await form.getByLabel(/Statement file/).setInputFiles({ name: `e2e-${token}.json`, mimeType: "application/json", buffer: Buffer.from(JSON.stringify(statement)) });
  await form.getByRole("button", { name: "Import statement" }).click();
  await answerStepUpIfAsked(page, finance1, form.getByRole("status").or(form.getByRole("alert")));
  await expect(form.getByRole("status")).toContainText("Statement received");
  await form.getByRole("link", { name: "View the import" }).click();
  await page.waitForURL(/\/admin\/reconciliation\/batches\/[0-9a-f-]{36}$/);
  // the title carries the statement reference once the worker scanned and parsed the file
  await expect(async () => {
    await page.reload();
    await expect(page.getByRole("heading", { level: 1 })).toContainText(`E2E-${token}`, { timeout: 2000 });
  }).toPass({ timeout: 120_000 });
  await expect(page.getByRole("table", { name: "Statement lines" })).toContainText(`E2E-L-${token}`);

  // a run decides the line: the reference names no FundZim payment (never matched by the amount alone)
  const run = await csrfPost(page, "/api/v1/admin/reconciliation/runs", { source: "sandbox_transactions_test", currency: "USD", reason: "real-stack e2e run" });
  expect([202, 409]).toContain(run.status()); // 409: the worker's own run for this scope is active (it decides the line too)
  await expect(async () => {
    await page.reload();
    await expect(page.getByRole("table", { name: "Exceptions on this statement" })).toContainText("Missing payment", { timeout: 2000 });
  }).toPass({ timeout: 180_000 });
  await page.getByRole("table", { name: "Exceptions on this statement" }).getByRole("link", { name: "Missing payment" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Missing payment" })).toBeVisible();
  await expect(main).toContainText("Provider ref given");
  await ctx.close();

  // COMPLIANCE reads the dashboard (TEST only, gates blocked); a REVIEWER does not find it
  const c = await staffPage(browser, S("compliance"), "/admin/reconciliation");
  await expect(c.page.getByRole("heading", { level: 1, name: "Reconciliation" })).toBeVisible();
  await expect(c.page.locator("main")).toContainText("TEST evidence only");
  await expect(c.page.getByRole("table", { name: "Statement sources" })).toContainText("sandbox_transactions_test");
  await c.ctx.close();
  const reviewer = await staffPage(browser, S("reviewer"), "/admin/campaigns/review");
  expect((await reviewer.page.goto("/admin/reconciliation"))?.status()).toBe(404);
  await reviewer.ctx.close();
});
