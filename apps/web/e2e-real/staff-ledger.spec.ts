import { expect, test } from "@playwright/test";

import { staffPage } from "./support/browser";
import { WEB_URL } from "./support/env";
import { loadFixture } from "./support/state";

/**
 * Stage 10 ledger against the REAL stack (web → Go API → fundzim_ledger pool → PostgreSQL). The ADMIN posts a
 * TEST_ONLY synthetic fixture through the staff API (there is no UI control for fixtures); a COMPLIANCE officer
 * (ledger.view) then reads it in the finance UI. Staff without ledger.view get a 404. The development database is
 * a TEST ledger, so every journal here is synthetic and labelled as such.
 */
test.describe.configure({ mode: "serial" });

test("synthetic fixture posted by an ADMIN appears balanced and TEST_ONLY in the finance UI", async ({ browser }) => {
  const staff = loadFixture().staff;
  const S = (key: string) => {
    const c = staff[key];
    if (!c) throw new Error(`staff fixture ${key} missing`);
    return c;
  };
  const admin = await staffPage(browser, S("moderator"), "/admin/operations");
  const csrf = (await admin.ctx.cookies()).find((k) => k.name === "fz_csrf")?.value ?? "";
  const res = await admin.page.request.post("/api/v1/admin/ledger/synthetic-fixtures", {
    data: { scenario: "donations" },
    headers: { "X-CSRF-Token": csrf, Origin: WEB_URL },
  });
  expect(res.status(), await res.text()).toBe(201);
  const run = ((await res.json()) as { data: { transaction_ids: string[]; label: string; synthetic_campaign_id: string } }).data;
  expect(run.label).toBe("TEST_ONLY");
  expect(run.transaction_ids).toHaveLength(3);
  // ADMIN holds ledger.synthetic.post but not ledger.view
  expect((await admin.page.request.get("/api/v1/admin/ledger/overview")).status()).toBe(403);
  await admin.ctx.close();

  const { ctx, page } = await staffPage(browser, S("compliance"), "/admin/finance/ledger");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Ledger" })).toBeVisible();
  await expect(main).toContainText("TEST LEDGER");
  await expect(page.getByRole("table", { name: "Trial balance totals" })).toContainText("USD");

  // the first journal: USD 10.00 captured to the synthetic campaign, balanced, labelled TEST_ONLY
  await page.goto(`/admin/finance/journals/${run.transaction_ids[0]}`);
  await expect(main).toContainText("Debits equal credits");
  await expect(main).toContainText("TEST_ONLY");
  const lines = page.getByRole("table", { name: "Journal lines" });
  await expect(lines).toContainText("asset:psp_clearing");
  await expect(lines).toContainText(`liability:campaign_unsettled:${run.synthetic_campaign_id}`);
  await expect(lines).toContainText("10.00");

  await page.goto("/admin/finance/trial-balance");
  await expect(page.getByRole("table", { name: "Trial balance USD" })).toBeVisible();
  await expect(main).toContainText("Difference US$0.00"); // USD is the only currency a TEST ledger can hold
  await ctx.close();

  // a REVIEWER (no ledger.view) does not find the finance pages
  const reviewer = await staffPage(browser, S("reviewer"), "/admin/operations");
  const r = await reviewer.page.goto("/admin/finance/ledger");
  expect(r?.status()).toBe(404);
  await reviewer.ctx.close();
});

test("payout operations overview: TEST payouts through the simulator only, production BLOCKED; no access without payout.view", async ({ browser }) => {
  const staff = loadFixture().staff;
  const S = (key: string) => {
    const c = staff[key];
    if (!c) throw new Error(`staff fixture ${key} missing`);
    return c;
  };
  // COMPLIANCE holds payout.view (read only)
  const { ctx, page } = await staffPage(browser, S("compliance"), "/admin/finance/payouts");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Payouts" })).toBeVisible();
  await expect(main).toContainText("Production payouts");
  await expect(main).toContainText("BLOCKED");
  await expect(main).toContainText("LOCAL_SIMULATOR");
  await expect(main).toContainText("NOT_PERFORMED");
  await page.goto("/admin/finance/payouts/exceptions");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await ctx.close();
  // the API itself: a reviewer without payout.view is refused
  const reviewer = await staffPage(browser, S("reviewer"), "/admin/operations");
  expect((await reviewer.page.request.get("/api/v1/admin/payouts/overview")).status()).toBe(403);
  expect((await reviewer.page.goto("/admin/finance/payouts"))?.status()).toBe(404);
  await reviewer.ctx.close();
});

test("fees: TEST fees only, production fee charging BLOCKED, per-currency revenue; no access without fee.view", async ({ browser }) => {
  const staff = loadFixture().staff;
  const S = (key: string) => {
    const c = staff[key];
    if (!c) throw new Error(`staff fixture ${key} missing`);
    return c;
  };
  // COMPLIANCE holds fee.view (read only)
  const { ctx, page } = await staffPage(browser, S("compliance"), "/admin/finance/fees");
  const main = page.locator("main");
  await expect(page.getByRole("heading", { level: 1, name: "Fees" })).toBeVisible();
  await expect(main).toContainText("BLOCKED");
  await expect(main).toContainText("TEST");
  const api = await page.request.get("/api/v1/admin/fees/overview");
  expect(api.status()).toBe(200);
  const overview = (await api.json()).data;
  expect(overview.fee_environment).toBe("TEST");
  expect(overview.production_fees).toBe("BLOCKED");
  await page.goto("/admin/finance/revenue");
  await expect(page.getByRole("heading", { level: 1, name: "Revenue (TEST)" })).toBeVisible();
  const rev = (await (await page.request.get("/api/v1/admin/finance/revenue")).json()).data;
  expect(rev.fee_environment).toBe("TEST");
  for (const c of rev.currencies) expect(c.cash_received).toBe("NOT_AVAILABLE");
  await ctx.close();
  // a reviewer without fee.view is refused by the API and the page does not exist for them
  const reviewer = await staffPage(browser, S("reviewer"), "/admin/operations");
  expect((await reviewer.page.request.get("/api/v1/admin/fees/overview")).status()).toBe(403);
  expect((await reviewer.page.goto("/admin/finance/fees"))?.status()).toBe(404);
  await reviewer.ctx.close();
});
