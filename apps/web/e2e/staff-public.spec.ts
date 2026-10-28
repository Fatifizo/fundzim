import { expect, type Page } from "@playwright/test";

import { AUTH_BASE_URL, createStaff, createUser, seriousAxeViolations, signIn, signInStaff, TOTP, watchForBreakage, test } from "./helpers";

// Stage 7 staff pages (contract §4) against the mock API: campaign reports triage; Stage 16 featured placements
// (maker-checker; they replaced the Stage 7 staff picks).
test.use({ baseURL: AUTH_BASE_URL });

let n = 0;
const token = () => `${Date.now().toString(36)}${process.pid.toString(36)}${n++}`;

async function seedLive(page: Page, title: string) {
  const email = await createUser(page, { phoneVerified: true, kycLevel: "IDENTITY_VERIFIED" });
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/campaign`, { data: { email, status: "ACTIVE", title } });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: { id: string; slug: string } }).data;
}

const MOCK_API = `http://127.0.0.1:${process.env.MOCK_API_PORT ?? 3199}`;

/**
 * Reports are deduplicated per reporter, campaign and day, and the web proxy (correctly) replaces any client
 * X-Forwarded-For, so seed reports go straight to the mock API with a distinct address each.
 */
async function report(page: Page, slug: string, data: Record<string, unknown>, ip: string) {
  const res = await page.request.post(`${MOCK_API}/api/v1/public/campaigns/${slug}/reports`, { data, headers: { "X-Forwarded-For": ip } });
  expect(res.status()).toBe(202);
}

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function stepUpIfAsked(page: Page) {
  const stepUp = page.getByRole("dialog", { name: "Confirm it's you" });
  await stepUp.getByLabel("Authentication code").fill(TOTP);
  await stepUp.getByRole("button", { name: "Confirm" }).click();
}

test("reports: reviewer sees reason, escaped details, contact flag (never the email); dismiss and escalate", async ({ page }) => {
  const problems = watchForBreakage(page);
  const title = `Reported campaign ${token()}`;
  const { slug } = await seedLive(page, title);
  await report(page, slug, { reason: "IMPERSONATION", details: "Uses the name of my church & its photos without permission.", contact_email: "worried.reporter@example.test" }, "203.0.113.10");
  await report(page, slug, { reason: "OTHER", details: "The phone number in the story asks for EcoCash payments." }, "203.0.113.11");

  const staff = await createStaff(page, ["REVIEWER"], "Rumbi Reviewer");
  await signInStaff(page, staff, "/admin/campaigns/reports");
  await expect(page.getByRole("heading", { level: 1, name: "Campaign reports" })).toBeVisible();
  await expect(page.getByText("A report never suspends a campaign by itself")).toBeVisible();
  const ours = page.getByRole("list", { name: "Reports" }).locator(":scope > li").filter({ hasText: title });
  await expect(ours).toHaveCount(2);
  const impersonation = ours.filter({ hasText: "pretending to be another" });
  await expect(impersonation).toContainText("Uses the name of my church & its photos without permission.");
  await expect(impersonation).toContainText("Yes (not shown here)");
  await expect(impersonation).toContainText("Anonymous visitor");
  await expect(impersonation.getByRole("link", { name: title })).toHaveAttribute("href", /\/admin\/campaigns\/review\/[0-9a-f-]{36}$/);
  await expect(page.locator("main")).not.toContainText("worried.reporter@example.test");
  expect(await axe(page)).toEqual([]);

  // Dismiss: validation, then success.
  await impersonation.getByRole("button", { name: /^Dismiss report/ }).click();
  const dialog = page.getByRole("dialog", { name: "Dismiss this report?" });
  await expect(dialog).toContainText("The campaign is not changed.");
  await dialog.getByRole("button", { name: "Confirm: Dismiss" }).click();
  await expect(dialog.getByText("Choose a reason.").last()).toBeVisible();
  await dialog.getByLabel("Reason").selectOption("NO_POLICY_BREACH");
  await dialog.getByLabel("Internal note (staff only)").fill("Spoke to the church; the organiser is their treasurer.");
  expect(await axe(page)).toEqual([]);
  await dialog.getByRole("button", { name: "Confirm: Dismiss" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Report dismissed." })).toBeVisible();
  await expect(ours).toHaveCount(1);

  // Escalate the other one.
  await ours.getByRole("button", { name: /^Escalate report/ }).click();
  const esc = page.getByRole("dialog", { name: "Escalate this report?" });
  await expect(esc).toContainText("not suspended or changed");
  await esc.getByLabel("Reason").selectOption("POSSIBLE_FRAUD");
  await esc.getByLabel("Internal note (staff only)").fill("Payment request outside FundZim.");
  await esc.getByRole("button", { name: "Confirm: Escalate" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Report escalated to compliance." })).toBeVisible();
  await expect(ours).toHaveCount(0);

  await page.getByRole("navigation", { name: "Report status" }).getByRole("link", { name: "Escalated" }).click();
  await expect(page).toHaveURL(/status=ESCALATED/);
  const escalated = page.getByRole("list", { name: "Reports" }).locator(":scope > li").filter({ hasText: title });
  await expect(escalated).toContainText("Possible fraud");
  await expect(escalated.getByRole("button")).toHaveCount(0);
  await page.goto("/admin/campaigns/reports?status=DISMISSED");
  await expect(page.getByRole("list", { name: "Reports" }).locator(":scope > li").filter({ hasText: title })).toContainText("No policy breach");
  expect(await axe(page)).toEqual([]);

  // The campaign is still public.
  expect((await page.request.get(`/campaigns/${slug}`)).status()).toBe(200);
  expect(problems).toEqual([]);
});

test("featured placements: a reviewer requests one (step-up), a compliance officer approves it, it is shown until the campaign stops qualifying, then removed", async ({ page }) => {
  const problems = watchForBreakage(page);
  const title = `Pick me ${token()}`;
  const { id, slug } = await seedLive(page, title);
  const reviewer = await createStaff(page, ["REVIEWER"], "Rudo Reviewer");
  const officer = await createStaff(page, ["COMPLIANCE"], "Farai Compliance");
  await signInStaff(page, reviewer, `/admin/campaigns/review/${id}`);
  await page.getByRole("link", { name: "Request a featured placement" }).click();
  await expect(page).toHaveURL(new RegExp(`/admin/discovery/featured\\?campaign_id=${id}$`));
  await expect(page.getByLabel("Campaign id")).toHaveValue(id);
  expect(await axe(page)).toEqual([]);

  await page.getByLabel("Position (1–100)").fill("0");
  await page.getByRole("button", { name: "Request placement" }).click();
  await expect(page.getByText("Choose a position from 1 to 100.").last()).toBeVisible();
  await expect(page.getByText(/Write 10 to 500 characters/).last()).toBeVisible();
  await page.getByLabel("Position (1–100)").fill("40");
  await page.getByLabel(/Why this campaign/).fill("Shows community fundraising for a clinic.");
  await page.getByRole("button", { name: "Request placement" }).click();
  await stepUpIfAsked(page);
  await expect(page.getByRole("status").filter({ hasText: "Placement requested." })).toBeVisible();
  const item = page.getByRole("list", { name: "Featured placements" }).locator(":scope > li").filter({ hasText: title });
  await expect(item).toContainText("Requested");
  // not shown before a second person approves it
  expect(await (await page.request.get("/campaigns/featured")).text()).not.toContain(title);
  // the reviewer has no approval permission: the API refuses (the button is offered; the API is the authority)
  await item.getByRole("button", { name: "Approve" }).click();
  const selfDialog = page.getByRole("dialog", { name: "Approve this placement?" });
  await selfDialog.getByLabel("Note (staff only)").fill("Trying to approve my own request.");
  await selfDialog.getByRole("button", { name: "Confirm: Approve" }).click();
  await expect(selfDialog.getByRole("alert")).toContainText(/permission/i);
  await selfDialog.getByRole("button", { name: "Cancel" }).click();

  await page.context().clearCookies();
  await signInStaff(page, officer, "/admin/discovery/featured");
  await item.getByRole("button", { name: "Approve" }).click();
  const dialog = page.getByRole("dialog", { name: "Approve this placement?" });
  await dialog.getByLabel("Note (staff only)").fill("Checked the campaign and the reason.");
  await dialog.getByRole("button", { name: "Confirm: Approve" }).click();
  await stepUpIfAsked(page);
  await expect(page.getByRole("status").filter({ hasText: "Placement approved." })).toBeVisible();
  await expect(item).toContainText("Shown now");
  const featured = await (await page.request.get("/campaigns/featured")).text();
  expect(featured).toContain(title);
  expect(featured).toContain("Featured by FundZim");
  expect(await axe(page)).toEqual([]);

  // a campaign that stops being active is no longer shown; staff see why
  await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/campaign/${id}/status`, { data: { status: "SUSPENDED" } });
  await page.reload();
  await expect(item).toContainText("Not shown: campaign not promotable");
  expect(await (await page.request.get("/campaigns/featured")).text()).not.toContain(title);
  expect((await page.request.get(`/campaigns/${slug}`)).status()).toBe(404);

  await item.getByRole("button", { name: "Remove" }).click();
  const remove = page.getByRole("dialog", { name: "Remove this placement now?" });
  await remove.getByRole("button", { name: "Confirm: Remove" }).click();
  await expect(remove.getByText("This is required.")).toBeVisible();
  await remove.getByLabel("Reason (staff only)").fill("Campaign suspended.");
  await remove.getByRole("button", { name: "Confirm: Remove" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Placement removed." })).toBeVisible();
  await expect(item).toContainText("Removed");
  expect(problems).toEqual([]);
});

test("featured placements: a campaign that is not active cannot be requested", async ({ page }) => {
  const { id } = await seedLive(page, `Not live ${token()}`);
  await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/campaign/${id}/status`, { data: { status: "COMPLETED" } });
  const staff = await createStaff(page, ["ADMIN"], "Admin Person");
  await signInStaff(page, staff, "/admin/discovery/featured");
  await page.goto(`/admin/discovery/featured?campaign_id=${id}`);
  await page.getByLabel(/Why this campaign/).fill("Trying an ended campaign.");
  await page.getByRole("button", { name: "Request placement" }).click();
  await stepUpIfAsked(page);
  await expect(page.getByRole("alert").filter({ hasText: "can't be featured now" })).toBeVisible();
});

test("staff pages are a 404 for users and for staff without the permission", async ({ page }) => {
  const email = await createUser(page, { phoneVerified: true });
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  for (const path of ["/admin/campaigns/reports", "/admin/discovery", "/admin/discovery/featured", "/admin/discovery/ranking"]) {
    expect((await page.goto(path))?.status(), path).toBe(404);
  }
  await page.context().clearCookies();
  const kyc = await createStaff(page, ["KYC_REVIEWER"]);
  await signInStaff(page, kyc, "/admin/verification");
  for (const path of ["/admin/campaigns/reports", "/admin/discovery", "/admin/discovery/featured"]) {
    expect((await page.goto(path))?.status(), path).toBe(404);
  }
  await page.context().clearCookies();
  const support = await createStaff(page, ["SUPPORT"]);
  await signInStaff(page, support, "/admin/campaigns");
  expect((await page.goto("/admin/discovery/featured"))?.status()).toBe(404); // SUPPORT lacks discovery.view
});
