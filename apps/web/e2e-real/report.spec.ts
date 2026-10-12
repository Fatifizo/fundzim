import { expect, test, type Page } from "@playwright/test";

import { answerStepUpIfAsked, staff, staffPage } from "./support/browser";
import { loadRun } from "./support/state";

/**
 * Public report → staff triage in the UI, against the REAL stack. Desktop project only: `public.report.ip`
 * allows 5 reports per hour per address, and every request of the suite shares one address.
 */
test.setTimeout(20 * 60_000);

/** Walks the reports queue (oldest first, keyset pages) until the report with `marker` in its details appears. */
async function findReport(page: Page, status: "OPEN" | "ESCALATED" | "DISMISSED", marker: string) {
  await page.goto(`/admin/campaigns/reports?status=${status}`);
  await expect(page.getByRole("heading", { level: 1, name: "Campaign reports" })).toBeVisible();
  for (let i = 0; i < 60; i++) {
    const item = page.getByRole("list", { name: "Reports" }).getByRole("listitem").filter({ hasText: marker });
    if ((await item.count()) > 0) return item.first();
    const next = page.getByRole("link", { name: "Next page" });
    if ((await next.count()) === 0) break;
    // A client-side link: follow its href with a full navigation so the next check reads the new page.
    const href = await next.getAttribute("href");
    if (!href) break;
    await page.goto(href);
    await expect(page.getByRole("heading", { level: 1, name: "Campaign reports" })).toBeVisible();
  }
  throw new Error(`report "${marker}" not found in the ${status} queue`);
}

test("a visitor reports a campaign; staff see it without the contact email and escalate it in the UI", async ({ page, browser }) => {
  const r = loadRun();
  const c = r.campaigns.pubDetail;
  const marker = `Real-stack E2E report ${r.runId}`;
  const contact = `e2e-real-reporter-${r.runId}@example.test`;
  await page.goto(`/campaigns/${c.slug}`);
  await page.getByRole("button", { name: "Report this campaign" }).click();
  const dialog = page.getByRole("dialog", { name: "Report this campaign" });
  await expect(dialog).toBeVisible();
  // Validation: a reason is required.
  await dialog.getByRole("button", { name: "Send report" }).click();
  await expect(dialog.getByText(/Error:/).first()).toBeVisible();
  await dialog.getByRole("radio", { name: "Misleading or false information" }).check();
  await dialog.getByLabel(/^Details/).fill(`${marker}: the story does not match the photos.`);
  await dialog.getByLabel("Your email address (optional)").fill(contact);
  // `public.report.ip` (5 per hour, one more every 12 minutes) is shared by every client on this host. When it
  // is exhausted the dialog says how long to wait (from Retry-After); wait that long (bounded) and send again.
  const done = dialog.getByRole("status").filter({ hasText: "Thank you — your report was received." });
  const limited = dialog.getByRole("alert").filter({ hasText: /Too many reports/ });
  const deadline = Date.now() + 14 * 60_000;
  for (;;) {
    await dialog.getByRole("button", { name: "Send report" }).click();
    await expect(done.or(limited).first()).toBeVisible();
    if (await done.isVisible()) break;
    const minutes = Number(/about (\d+) minute/.exec(await limited.innerText())?.[1] ?? "2");
    expect(Date.now() + minutes * 60_000, "public.report.ip would not refill within the test's budget").toBeLessThan(deadline);
    test.info().annotations.push({ type: "rate-limit", description: `public.report.ip exhausted; waited ${minutes} min` });
    await page.waitForTimeout(minutes * 60_000 + 2_000);
  }

  // Staff triage in the UI (campaign.review).
  const reviewer = staff(r.staffEmails.reviewer);
  const { ctx, page: spage } = await staffPage(browser, reviewer, "/admin/campaigns/reports?status=OPEN");
  const item = await findReport(spage, "OPEN", marker);
  await expect(item.getByRole("heading", { name: "Misleading or false information" })).toBeVisible();
  await expect(item.getByRole("link", { name: c.title })).toHaveAttribute("href", `/admin/campaigns/review/${c.id}`);
  await expect(item).toContainText("Yes (not shown here)");
  expect(await spage.content(), "the reporter's email is never shown to staff").not.toContain(contact);

  await item.getByRole("button", { name: `Escalate report about ${c.title}` }).click();
  const triage = spage.getByRole("dialog", { name: "Escalate this report?" });
  await triage.getByLabel("Reason").selectOption("POLICY_BREACH");
  await triage.getByLabel("Internal note (staff only)").fill("Photos do not match the story; compliance to check (e2e).");
  await triage.getByRole("button", { name: "Confirm: Escalate" }).click();
  const escalated = spage.getByRole("status").filter({ hasText: "Report escalated to compliance." });
  await answerStepUpIfAsked(spage, reviewer, escalated);
  await expect(escalated).toBeVisible();

  const after = await findReport(spage, "ESCALATED", marker);
  await expect(after).toContainText("Policy breach");
  await expect(after.getByRole("button", { name: /Escalate|Dismiss/ })).toHaveCount(0);
  // The campaign itself is unchanged: escalation never suspends it.
  expect((await page.goto(`/campaigns/${c.slug}`))?.status()).toBe(200);
  await ctx.close();
});
