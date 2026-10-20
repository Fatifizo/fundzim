import { expect, test } from "@playwright/test";

import { staff, staffPage } from "./support/browser";
import { cspViolations, seriousAxeViolations, watchCsp } from "./support/quality";
import { loadRun } from "./support/state";

/**
 * Accessibility (axe: WCAG 2.x A/AA, serious and critical) and Content-Security-Policy checks on the REAL
 * stack: public pages on desktop and mobile, the Stage 7 staff pages signed in as real staff.
 */
const run = () => loadRun();

const PUBLIC_PAGES: [string, () => string][] = [
  ["home", () => "/"],
  ["discovery", () => "/campaigns"],
  ["category", () => "/categories/education"],
  ["trending", () => "/campaigns/trending"],
  ["featured", () => "/campaigns/featured"],
  ["province", () => "/campaigns/location/harare"],
  ["campaign", () => `/campaigns/${run().campaigns.pubDetail.slug}`],
  ["safety", () => "/safety"],
];

for (const [name, path] of PUBLIC_PAGES) {
  test(`public ${name} page: no serious axe violations, no CSP violations`, async ({ page }) => {
    const problems = await watchCsp(page);
    const response = await page.goto(path());
    expect(response?.status()).toBe(200);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    await expect(page.getByText("Loading campaigns…")).toHaveCount(0);
    expect(await seriousAxeViolations(page)).toEqual([]);
    expect(await cspViolations(page)).toEqual([]);
    expect(problems).toEqual([]);
  });
}

const STAFF_PAGES: [string, string, () => string][] = [
  ["reports", "/admin/campaigns/reports", () => run().staffEmails.reviewer],
  ["featured placements", "/admin/discovery/featured", () => run().staffEmails.compliance],
  ["discovery overview", "/admin/discovery", () => run().staffEmails.compliance],
  ["trending ranking", "/admin/discovery/ranking", () => run().staffEmails.compliance],
];

for (const [name, path, who] of STAFF_PAGES) {
  test(`staff ${name} page: no serious axe violations, no CSP violations`, async ({ browser }) => {
    const { ctx, page } = await staffPage(browser, staff(who()), path);
    const problems = await watchCsp(page);
    await page.goto(path);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    expect(await seriousAxeViolations(page)).toEqual([]);
    expect(await cspViolations(page)).toEqual([]);
    expect(problems).toEqual([]);
    await ctx.close();
  });
}
