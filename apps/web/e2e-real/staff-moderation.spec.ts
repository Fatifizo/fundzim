import { expect, test, type Browser, type BrowserContext, type Page } from "@playwright/test";

import { answerStepUpIfAsked, discoveryTitles, ownerContext, staff, staffAction, staffPage, uiStaffLogin } from "./support/browser";
import { WEB_URL } from "./support/env";
import { ApiSession } from "./support/http";
import { linkFrom, waitForMail } from "./support/mailpit";
import { loadFixture, loadRun, storageStatePath, type StaffCred } from "./support/state";

/**
 * Staff moderation against the REAL stack (web container → Go API → PostgreSQL, worker, Mailpit). Global
 * setup only provisioned accounts and submitted campaigns through the API; every moderation step below is
 * done in the browser UI. The tests share one dataset and run in order.
 */
test.describe.configure({ mode: "serial" });

const run = () => loadRun();
const reviewer = () => staff(run().staffEmails.reviewer);
const compliance = () => staff(run().staffEmails.compliance);

const MESSAGE_TO_OWNER = "Please name the school in your story so donors know where the fees go.";
const INTERNAL_NOTE = "Internal only: the story should name the school (checked against policy 4.2).";

async function staffContext(browser: Browser, name: string): Promise<BrowserContext> {
  return browser.newContext({ storageState: storageStatePath(name) });
}

/** Owner API session (for reading back status/slug; the moderation itself is done in the UI). */
function ownerApi(): ApiSession {
  return ApiSession.fromJSON(run().owner.session);
}

async function ownerView(id: string) {
  const r = await ownerApi().get<{ status: string; slug: string | null }>(`/campaigns/${id}`, 200);
  return r.data;
}

/** Walks the review queue (oldest first, keyset pages) until the campaign's row appears; opens it. */
async function openFromQueue(page: Page, title: string, id: string) {
  await page.goto("/admin/campaigns/review?assigned=any");
  await expect(page.getByRole("heading", { level: 1, name: "Campaign reviews" })).toBeVisible();
  for (let i = 0; i < 60; i++) {
    const link = page.getByRole("table", { name: "Campaign review queue" }).getByRole("link", { name: title, exact: true });
    if ((await link.count()) > 0) {
      await expect(link).toHaveAttribute("href", `/admin/campaigns/review/${id}`);
      await link.click();
      await expect(page).toHaveURL(new RegExp(`/admin/campaigns/review/${id}$`));
      await expect(page.getByRole("heading", { level: 1, name: "Campaign review" })).toBeVisible();
      return;
    }
    const next = page.getByRole("link", { name: "Next page" });
    if ((await next.count()) === 0) break;
    // "Next page" is a client-side link: clicking it returns before the new page renders, so the next check
    // could read the old rows. Follow its href with a full navigation instead.
    const href = await next.getAttribute("href");
    if (!href) break;
    await page.goto(href);
    await expect(page.getByRole("heading", { level: 1, name: "Campaign reviews" })).toBeVisible();
  }
  throw new Error(`campaign "${title}" is not in the review queue`);
}

async function expectStatus(page: Page, label: string) {
  const summary = page.locator("section", { has: page.getByRole("heading", { name: "Campaign", exact: true }) });
  await expect(summary.getByText(label, { exact: true }).first()).toBeVisible();
}

/** The public page as an anonymous visitor: status code and heading. */
async function publicStatus(browser: Browser, slug: string) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const response = await page.goto(`/campaigns/${slug}`);
  const status = response?.status() ?? 0;
  const heading = status === 200 ? await page.getByRole("heading", { level: 1 }).first().innerText() : "";
  await ctx.close();
  return { status, heading };
}

async function listedInDiscovery(browser: Browser, title: string) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(`/campaigns?q=${encodeURIComponent(title)}`);
  await expect(page.getByRole("heading", { level: 1, name: /campaigns/i })).toBeVisible();
  const titles = await discoveryTitles(page);
  await ctx.close();
  return titles.includes(title);
}

test("staff sign in with password and authenticator code through the login page", async ({ page }) => {
  const c = reviewer();
  // Password alone is not enough: the login page sends staff to the authenticator step.
  await uiStaffLogin(page, c, "/admin/campaigns/review");
  await expect(page.getByRole("heading", { level: 1, name: "Campaign reviews" })).toBeVisible();
  const me = await page.request.get("/api/v1/me");
  expect(me.status()).toBe(200);
  const body = (await me.json()) as { data: { account_kind: string; roles: string[]; email: string } };
  expect(body.data.account_kind).toBe("STAFF");
  expect(body.data.roles).toEqual(["REVIEWER"]);
  await page.context().storageState({ path: storageStatePath("reviewer") });
});

test("review queue → assign → start review → request changes; the owner sees only the message and resubmits", async ({ browser }) => {
  const c = run().campaigns.modStandard;
  const ctx = await staffContext(browser, "reviewer");
  const page = await ctx.newPage();
  await openFromQueue(page, c.title, c.id);
  await expectStatus(page, "Submitted");

  await staffAction(page, reviewer(), "Assign to me", { reason: "REVIEW_ASSIGNMENT", note: "Taking this review (e2e)." });
  await expect(page.getByText(/\(you\)$/).first()).toBeVisible();
  await staffAction(page, reviewer(), "Start review", { reason: "REVIEW_STARTED", note: "Starting the review (e2e)." });
  await expectStatus(page, "Under review");
  await staffAction(page, reviewer(), "Request changes", { reason: "CONTENT_UNCLEAR", note: INTERNAL_NOTE, userMessage: MESSAGE_TO_OWNER });
  await expectStatus(page, "Changes requested");
  expect((await ownerView(c.id)).status).toBe("CHANGES_REQUESTED");
  await ctx.close();

  // Owner: sees the reviewer's message (never the internal note), edits, resubmits in the UI.
  const octx = await ownerContext(browser);
  const owner = await octx.newPage();
  await owner.goto(`/dashboard/campaigns/${c.id}`);
  const feedback = owner.getByRole("alert").filter({ hasText: "Reviewers asked for changes" }).or(owner.locator("section, div").filter({ hasText: "Reviewers asked for changes" }).last());
  await expect(feedback.first()).toContainText(MESSAGE_TO_OWNER);
  await expect(owner.locator("body")).not.toContainText("Internal only");
  const cur = await ownerApi().get(`/campaigns/${c.id}`, 200);
  await ownerApi().patch(
    `/campaigns/${c.id}`,
    { story: `${(cur.data as { story: string }).story} The school is Harare High School.` },
    cur.headers.get("ETag") ?? "",
    200,
  );
  await owner.reload();
  await owner.getByRole("button", { name: "Submit for review" }).click();
  await owner.getByRole("dialog").getByRole("button", { name: "Yes, submit for review" }).click();
  await expect(owner.getByRole("status").filter({ hasText: "Your campaign was submitted for review" })).toBeVisible();
  expect((await ownerView(c.id)).status).toBe("SUBMITTED");
  await octx.close();
});

test("resubmitted campaign: approve with step-up; the owner publishes in the UI; the public page is live", async ({ browser }) => {
  const c = run().campaigns.modStandard;
  const ctx = await staffContext(browser, "reviewer");
  const page = await ctx.newPage();
  await openFromQueue(page, c.title, c.id);
  await staffAction(page, reviewer(), "Assign to me", { reason: "REVIEW_ASSIGNMENT", note: "Re-review after changes (e2e)." });
  await staffAction(page, reviewer(), "Start review", { reason: "REVIEW_STARTED", note: "Starting the re-review (e2e)." });
  await expect(page.getByText("The school is Harare High School.")).toBeVisible();
  // Approve requires a fresh step-up; the helper answers the "Confirm it's you" dialog with a TOTP code.
  await staffAction(page, reviewer(), "Approve", { reason: "MEETS_POLICY", note: "Story, beneficiary and cover meet policy (e2e)." });
  await expectStatus(page, "Approved");
  expect((await ownerView(c.id)).status).toBe("APPROVED");
  await ctx.close();

  const octx = await ownerContext(browser);
  const owner = await octx.newPage();
  await owner.goto(`/dashboard/campaigns/${c.id}`);
  await owner.getByRole("button", { name: "Publish", exact: true }).click();
  await owner.getByRole("dialog").getByRole("button", { name: "Yes, publish" }).click();
  await expect(owner.getByRole("status").filter({ hasText: "Your campaign is published." })).toBeVisible();
  await owner.getByRole("link", { name: "View the public page" }).click();
  await expect(owner.getByRole("heading", { level: 1, name: c.title })).toBeVisible();
  await octx.close();

  const { slug, status } = await ownerView(c.id);
  expect(status).toBe("ACTIVE");
  expect(await publicStatus(browser, slug!)).toEqual({ status: 200, heading: c.title });
  expect(await listedInDiscovery(browser, c.title)).toBe(true);
});

test("suspension in the UI removes the public page and the listing; reactivation restores both", async ({ browser }) => {
  const c = run().campaigns.modStandard;
  const { slug } = await ownerView(c.id);
  const ctx = await staffContext(browser, "reviewer");
  const page = await ctx.newPage();
  await page.goto(`/admin/campaigns/review/${c.id}`);
  await expectStatus(page, "Active (published)");

  await staffAction(page, reviewer(), "Suspend", { reason: "INVESTIGATION", note: "Complaint under investigation (e2e)." });
  await expectStatus(page, "Suspended");
  expect((await ownerView(c.id)).status).toBe("SUSPENDED");
  expect((await publicStatus(browser, slug!)).status).toBe(404);
  expect(await listedInDiscovery(browser, c.title)).toBe(false);
  const api = await page.request.get(`${WEB_URL}/api/v1/public/campaigns/${slug}`);
  expect(api.status()).toBe(404);

  await staffAction(page, reviewer(), "Reactivate", { reason: "ISSUE_RESOLVED", note: "Investigation closed, no issue (e2e)." });
  await expectStatus(page, "Active (published)");
  expect(await publicStatus(browser, slug!)).toEqual({ status: 200, heading: c.title });
  expect(await listedInDiscovery(browser, c.title)).toBe(true);
  await ctx.close();
});

test("HIGH tier (MEDICAL): first approval waits for a second, different approver; COMPLIANCE gives it in the UI; staff publish", async ({ browser }) => {
  const c = run().campaigns.modMedical;
  const ctx = await staffContext(browser, "reviewer");
  const page = await ctx.newPage();
  await openFromQueue(page, c.title, c.id);
  await expect(page.getByText("Four eyes: two different approvers needed")).toBeVisible();
  await staffAction(page, reviewer(), "Assign to me", { reason: "REVIEW_ASSIGNMENT", note: "Taking the medical review (e2e)." });
  await staffAction(page, reviewer(), "Start review", { reason: "REVIEW_STARTED", note: "Starting (e2e)." });
  await staffAction(page, reviewer(), "Approve", { reason: "MEETS_POLICY", note: "First approval for a HIGH tier campaign (e2e)." });
  await expect(page.getByRole("status").filter({ hasText: "First approval recorded" })).toBeVisible();
  await page.reload();
  await expect(page.getByText("You gave the first approval. A different reviewer must give the second approval.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Give second approval" })).toHaveCount(0);
  expect((await ownerView(c.id)).status).toBe("UNDER_REVIEW");

  // The API refuses the first approver too (REVIEWER does not hold campaign.decide.high).
  const csrf = (await ctx.cookies()).find((k) => k.name === "fz_csrf")?.value ?? "";
  const refused = await page.request.post(`/api/v1/admin/campaigns/${c.id}/second-approval`, {
    data: { note: "my own second approval" },
    headers: { "X-CSRF-Token": csrf, Origin: WEB_URL },
  });
  expect(refused.status()).toBe(403);
  expect(((await refused.json()) as { error: { code: string } }).error.code).toMatch(/^(PERMISSION_DENIED|SECOND_APPROVER_MUST_DIFFER)$/);
  expect((await ownerView(c.id)).status).toBe("UNDER_REVIEW");
  await ctx.close();

  // A COMPLIANCE officer signs in (password + TOTP) and gives the second approval in the UI.
  const cctx = await browser.newContext();
  const cpage = await cctx.newPage();
  await uiStaffLogin(cpage, compliance(), `/admin/campaigns/review/${c.id}`);
  await cctx.storageState({ path: storageStatePath("compliance") });
  await expect(cpage.getByText(/A first decision \(Approve\) was recorded by another reviewer/)).toBeVisible();
  await staffAction(cpage, compliance(), "Give second approval", { reason: "MEETS_POLICY", note: "Independent second approval (e2e)." });
  await expectStatus(cpage, "Approved");
  expect((await ownerView(c.id)).status).toBe("APPROVED");

  // Staff publication in the UI; the public page is then live.
  await staffAction(cpage, compliance(), "Publish", { reason: "APPROVED_FOR_PUBLICATION", note: "Publishing after four-eyes approval (e2e)." });
  await expectStatus(cpage, "Active (published)");
  await cctx.close();
  const { slug, status } = await ownerView(c.id);
  expect(status).toBe("ACTIVE");
  expect(await publicStatus(browser, slug!)).toEqual({ status: 200, heading: c.title });
});

test("HIGH tier: a COMPLIANCE officer who gave the first approval cannot give the second (UI hides it, API refuses)", async ({ browser }) => {
  const c = run().campaigns.modMedical2;
  const { ctx, page } = await staffPage(browser, compliance(), `/admin/campaigns/review/${c.id}`);
  await expect(page.getByRole("heading", { level: 1, name: "Campaign review" })).toBeVisible();
  await staffAction(page, compliance(), "Assign to me", { reason: "REVIEW_ASSIGNMENT", note: "Compliance takes this review (e2e)." });
  await staffAction(page, compliance(), "Start review", { reason: "REVIEW_STARTED", note: "Starting (e2e)." });
  await staffAction(page, compliance(), "Approve", { reason: "MEETS_POLICY", note: "First approval by compliance (e2e)." });
  await expect(page.getByRole("status").filter({ hasText: "First approval recorded" })).toBeVisible();
  await page.reload();
  await expect(page.getByText("You gave the first approval. A different reviewer must give the second approval.")).toBeVisible();
  // This officer holds campaign.decide.high, so only the four-eyes rule stops them.
  await expect(page.getByRole("button", { name: "Give second approval" })).toHaveCount(0);
  const csrf = (await ctx.cookies()).find((k) => k.name === "fz_csrf")?.value ?? "";
  const refused = await page.request.post(`/api/v1/admin/campaigns/${c.id}/second-approval`, {
    data: { note: "second approval by the first approver" },
    headers: { "X-CSRF-Token": csrf, Origin: WEB_URL },
  });
  expect(refused.status()).toBe(403);
  expect(((await refused.json()) as { error: { code: string } }).error.code).toBe("SECOND_APPROVER_MUST_DIFFER");
  expect((await ownerView(c.id)).status).toBe("UNDER_REVIEW");
  await ctx.close();
});

test("self-approval prevention: a staff member linked (real ceremony) to the owner's personal account is refused", async ({ browser }) => {
  test.setTimeout(20 * 60_000); // may wait out the per-IP email-token budget (see below)
  const r = run();
  const linked: StaffCred = r.linkedStaff;
  // 1. The staff member requests the link in the staff UI (step-up with an authenticator code).
  const sctx = await browser.newContext();
  const spage = await sctx.newPage();
  await uiStaffLogin(spage, linked, "/admin/account/personal-link");
  await spage.getByLabel("Your personal account's email address").fill(r.owner.email);
  await spage.getByRole("button", { name: "Send link request" }).click();
  const sent = spage.getByRole("status").filter({ hasText: "Request sent" });
  await answerStepUpIfAsked(spage, linked, sent);
  await expect(sent).toBeVisible();

  // 2. The personal account confirms from the emailed link while signed in.
  const mail = await waitForMail(r.owner.email, "Confirm the link to your FundZim staff account");
  const link = new URL(linkFrom(mail.text));
  expect(link.pathname).toBe("/dashboard/account/staff-link");
  const octx = await ownerContext(browser);
  const opage = await octx.newPage();
  await opage.goto(`${link.pathname}${link.search}`);
  // The button is server-rendered; on a slow runner a click can land before React attaches its handler, so click
  // again while neither the result nor an error has appeared. The confirmation shares the per-IP `auth.token.ip`
  // budget (20 / 15 min) with every email-token action, and in the compose stack every client has the same address
  // (KI-S7-03): when the API says "Please wait N seconds", wait as a person would and confirm again. Any other
  // error fails the test with its message.
  const linkedStatus = opage.getByRole("status").filter({ hasText: "Your personal account is now linked to your staff account" });
  const linkError = opage.locator("main").getByRole("alert").filter({ hasText: /\S/ });
  const confirmButton = opage.getByRole("button", { name: "Confirm the link" });
  for (let attempt = 0; attempt < 20 && (await linkedStatus.count()) === 0; attempt++) {
    if ((await linkError.count()) > 0) {
      const message = (await linkError.allInnerTexts()).join(" ");
      const wait = /Please wait (\d+) seconds?/.exec(message);
      expect(wait, `link confirmation failed: ${message}`).not.toBeNull();
      test.info().annotations.push({ type: "rate-limit", description: `staff-link confirm: waited ${wait![1]} s (auth.token.ip)` });
      await opage.waitForTimeout((Number(wait![1]) + 1) * 1000);
    }
    await confirmButton.click();
    await expect(linkedStatus.or(linkError).first()).toBeVisible({ timeout: 5_000 }).catch(() => undefined);
  }
  await expect(linkedStatus).toBeVisible();
  await octx.close();
  await spage.reload();
  await expect(spage.getByText(/Your staff account is linked to/)).toBeVisible();

  // 3. Assigning a campaign owned by the linked personal account is refused with the explanation.
  const refusal = /You can't decide on a campaign that involves you, your organisation or your linked personal account/;
  const a = r.campaigns.linkAssign;
  await openFromQueue(spage, a.title, a.id);
  const assignErr = await staffAction(spage, linked, "Assign to me", { reason: "REVIEW_ASSIGNMENT", note: "Trying my own campaign (e2e).", expectError: true });
  expect(assignErr).toMatch(refusal);
  await spage.reload();
  await expectStatus(spage, "Submitted");
  await expect(spage.getByText("Unassigned").first()).toBeVisible();

  // 4. Approving one under review (assigned to another reviewer) is refused too, and nothing changes.
  const b = r.campaigns.linkApprove;
  await spage.goto(`/admin/campaigns/review/${b.id}`);
  await expectStatus(spage, "Under review");
  const approveErr = await staffAction(spage, linked, "Approve", { reason: "MEETS_POLICY", note: "Trying to approve my own campaign (e2e).", expectError: true });
  expect(approveErr).toMatch(refusal);
  expect((await ownerView(b.id)).status).toBe("UNDER_REVIEW");
  expect((await ownerView(a.id)).status).toBe("SUBMITTED");
  await sctx.close();

  // The fixture records that the linked staff member exists only for this run.
  expect(loadFixture().staff[linked.key]).toBeUndefined();
});
