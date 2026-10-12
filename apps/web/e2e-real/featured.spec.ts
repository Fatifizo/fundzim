import { expect, test, type Browser } from "@playwright/test";

import { answerStepUpIfAsked, staff, staffPage } from "./support/browser";
import { publicGet } from "./support/http";
import { loadRun } from "./support/state";

/**
 * Staff picks against the REAL stack (contract §4, `campaign.feature`: ADMIN and COMPLIANCE, step-up). A
 * COMPLIANCE officer features a live campaign in the staff UI, it shows on the home page as a staff pick, the
 * officer removes it in the UI and it disappears. Desktop only (it changes shared home-page state).
 *
 * The campaign used is this run's oldest published one (pubDetail): 13 newer catalogue campaigns were
 * published after it, so it is not among the "recently published" fillers and is on the home page only while
 * it is a staff pick.
 */
type Featured = { items: { slug: string; title: string; selection: string }[] };

async function homeFeaturedTitles(browser: Browser): Promise<string[]> {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  expect((await page.goto("/"))?.status()).toBe(200);
  const list = page.getByRole("list", { name: "Featured campaigns" });
  await expect(list).toBeVisible();
  const titles = (await list.getByRole("heading").allInnerTexts()).map((t) => t.trim());
  await ctx.close();
  return titles;
}

test("a COMPLIANCE officer features a live campaign in the UI; it is a staff pick on the home page until removed", async ({ browser }) => {
  const r = loadRun();
  const c = r.campaigns.pubDetail;
  const officer = staff(r.staffEmails.compliance);
  expect(await homeFeaturedTitles(browser)).not.toContain(c.title);

  const { ctx, page } = await staffPage(browser, officer, `/admin/campaigns/featured?campaign_id=${c.id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Featured campaigns" })).toBeVisible();
  await expect(page.getByLabel("Campaign id")).toHaveValue(c.id);
  await page.getByLabel("Position (1–100)").fill("1");
  await page.getByLabel("Why this campaign (staff only)").fill("Shows community library work (e2e).");
  await page.getByRole("button", { name: "Feature campaign" }).click();
  const featured = page.getByRole("status").filter({ hasText: "Campaign featured." });
  await answerStepUpIfAsked(page, officer, featured.or(page.getByRole("alert")));
  await expect(featured).toBeVisible();
  const pick = page.getByRole("list", { name: "Staff picks" }).getByRole("listitem").filter({ hasText: c.title });
  await expect(pick).toContainText("Shown on the home page");

  // Public: first in the featured list, marked as a staff pick by the API.
  const api = await publicGet<Featured>("/public/campaigns/featured?limit=6");
  expect(api.data.items.find((x) => x.slug === c.slug)?.selection).toBe("STAFF_PICK");
  expect(await homeFeaturedTitles(browser)).toContain(c.title);

  // Removed in the UI → gone from the home page.
  await pick.getByRole("button", { name: `Remove ${c.title} from the featured list` }).click();
  const dialog = page.getByRole("dialog", { name: "Remove this staff pick?" });
  await dialog.getByLabel("Reason (staff only)").fill("Rotation of staff picks (e2e).");
  await dialog.getByRole("button", { name: "Confirm: Remove" }).click();
  const removed = page.getByRole("status").filter({ hasText: "Campaign removed from the featured list." });
  await answerStepUpIfAsked(page, officer, removed);
  await expect(removed).toBeVisible();
  await expect(page.getByRole("list", { name: "Staff picks" }).getByRole("listitem").filter({ hasText: c.title })).toHaveCount(0);
  await ctx.close();

  expect((await publicGet<Featured>("/public/campaigns/featured?limit=12")).data.items.map((x) => x.slug)).not.toContain(c.slug);
  expect(await homeFeaturedTitles(browser)).not.toContain(c.title);
});
