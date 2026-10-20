import { expect, test, type Browser } from "@playwright/test";

import { answerStepUpIfAsked, staff, staffPage } from "./support/browser";
import { publicGet } from "./support/http";
import { loadRun } from "./support/state";

/**
 * Featured placements against the REAL stack (Stage 16, ADR-048: maker-checker; they replaced the Stage 7 staff picks).
 * A REVIEWER requests a placement for a live campaign in the staff UI (discovery.feature.request + step-up); it is not
 * shown until a COMPLIANCE officer approves it in the UI (discovery.feature.approve + step-up); then it is the first
 * FEATURED item of the home page; the officer removes it and it disappears. Desktop only (shared home-page state).
 *
 * The campaign used is this run's oldest published one (pubDetail): 13 newer catalogue campaigns were published after
 * it, so it is not among the "recently published" fillers and is on the home page only while it is featured.
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

test("a reviewer requests a featured placement, a compliance officer approves it; it is featured on the home page until removed", async ({ browser }) => {
  const r = loadRun();
  const c = r.campaigns.pubDetail;
  const reviewer = staff(r.staffEmails.reviewer);
  const officer = staff(r.staffEmails.compliance);
  expect(await homeFeaturedTitles(browser)).not.toContain(c.title);

  const maker = await staffPage(browser, reviewer, `/admin/discovery/featured?campaign_id=${c.id}`);
  await expect(maker.page.getByRole("heading", { level: 1, name: "Featured placements" })).toBeVisible();
  await expect(maker.page.getByLabel("Campaign id")).toHaveValue(c.id);
  await maker.page.getByLabel("Position (1–100)").fill("1");
  await maker.page.getByLabel(/Why this campaign/).fill("Shows community library work (e2e).");
  await maker.page.getByRole("button", { name: "Request placement" }).click();
  const requested = maker.page.getByRole("status").filter({ hasText: "Placement requested." });
  await answerStepUpIfAsked(maker.page, reviewer, requested.or(maker.page.getByRole("alert")));
  await expect(requested).toBeVisible();
  await maker.ctx.close();
  expect((await publicGet<Featured>("/discovery/featured?limit=12&fill=false")).data.items.map((x) => x.slug)).not.toContain(c.slug);

  const checker = await staffPage(browser, officer, "/admin/discovery/featured");
  const item = checker.page.getByRole("list", { name: "Featured placements" }).getByRole("listitem").filter({ hasText: c.title }).first();
  await item.getByRole("button", { name: "Approve" }).click();
  const approve = checker.page.getByRole("dialog", { name: "Approve this placement?" });
  await approve.getByLabel("Note (staff only)").fill("Checked the campaign and the reason (e2e).");
  await approve.getByRole("button", { name: "Confirm: Approve" }).click();
  const approved = checker.page.getByRole("status").filter({ hasText: "Placement approved." });
  await answerStepUpIfAsked(checker.page, officer, approved.or(approve.getByRole("alert")));
  await expect(approved).toBeVisible();
  await expect(item).toContainText("Shown now");

  // Public: the first item, marked FEATURED by the API (approved placements come before the fill).
  const api = await publicGet<Featured>("/discovery/featured?limit=6");
  expect(api.data.items[0]?.slug).toBe(c.slug);
  expect(api.data.items[0]?.selection).toBe("FEATURED");
  expect((await publicGet<Featured>("/public/campaigns/featured?limit=6")).data.items.find((x) => x.slug === c.slug)?.selection).toBe("FEATURED");
  expect(await homeFeaturedTitles(browser)).toContain(c.title);

  // Removed in the UI → gone from the home page.
  await item.getByRole("button", { name: "Remove" }).click();
  const remove = checker.page.getByRole("dialog", { name: "Remove this placement now?" });
  await remove.getByLabel("Reason (staff only)").fill("Rotation of featured placements (e2e).");
  await remove.getByRole("button", { name: "Confirm: Remove" }).click();
  const removed = checker.page.getByRole("status").filter({ hasText: "Placement removed." });
  await answerStepUpIfAsked(checker.page, officer, removed);
  await expect(removed).toBeVisible();
  await checker.ctx.close();

  expect((await publicGet<Featured>("/discovery/featured?limit=12&fill=false")).data.items.map((x) => x.slug)).not.toContain(c.slug);
  expect(await homeFeaturedTitles(browser)).not.toContain(c.title);
});
