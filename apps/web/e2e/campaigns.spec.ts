import { expect, test, type Page } from "@playwright/test";

import {
  AUTH_BASE_URL,
  createStaff,
  createUser,
  cspViolations,
  seriousAxeViolations,
  signIn,
  signInStaff,
  TOTP,
  trackCspViolations,
  watchForBreakage,
} from "./helpers";

// Stage 6 campaign UI against the in-memory mock API (e2e/mock-api/campaigns.mjs).
test.use({ baseURL: AUTH_BASE_URL });

const PNG = Buffer.from("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489", "hex");

async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

async function signInNewUser(page: Page, opts: Parameters<typeof createUser>[1] = {}) {
  const email = await createUser(page, { phoneVerified: true, ...opts });
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  return email;
}

async function seedBeneficiary(page: Page, email: string, displayName = "Chipo Moyo") {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/beneficiary`, { data: { email, display_name: displayName } });
  expect(res.status()).toBe(201);
}

async function seedCampaign(page: Page, email: string, data: Record<string, unknown> = {}) {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/campaign`, { data: { email, ...data } });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: { id: string; slug: string | null } }).data;
}

async function campaignInfo(page: Page, id: string) {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/campaign/${id}`, { data: {} });
  return ((await res.json()) as { data: { slug: string | null; status: string; goal: { amount_minor: string; currency: string } } }).data;
}

const STORY = "Chipo is in her final year of secondary school in Harare and wants to finish her exams.\n\nHer family cannot pay the last two terms of school fees. The funds go straight to the school.";

test("wizard: category → … → submit for review", async ({ page }) => {
  const problems = watchForBreakage(page);
  const email = await signInNewUser(page, { kycLevel: "IDENTITY_VERIFIED" });
  await seedBeneficiary(page, email);

  await page.goto("/dashboard/campaigns");
  await expect(page.getByRole("heading", { level: 1, name: "Your campaigns" })).toBeVisible();
  await page.getByRole("link", { name: "Start a campaign" }).first().click();
  await expect(page).toHaveURL(/\/dashboard\/campaigns\/new$/);
  expect(await axe(page)).toEqual([]);

  // Step 1: Next without a choice keeps you here with an error.
  await page.getByRole("button", { name: "Next" }).click();
  await expect(page.getByText("Choose a category.").last()).toBeVisible();
  await page.getByRole("radio", { name: /Education/ }).check();
  await page.getByRole("button", { name: "Next" }).click();
  await expect(page.getByRole("heading", { name: "Step 2 of 8: Title and summary" })).toBeFocused();

  // Step 2: client-side checks keep the input.
  await page.getByRole("textbox", { name: "Title", exact: true }).fill("Short");
  await page.getByRole("textbox", { name: "Summary", exact: true }).fill("School fees for Chipo's final year at secondary school.");
  await page.getByRole("button", { name: "Next" }).click();
  await expect(page.getByRole("textbox", { name: "Title", exact: true })).toBeFocused();
  await expect(page.getByText(/Title must be at least 10 characters/)).toBeVisible();
  await page.getByRole("textbox", { name: "Title", exact: true }).fill("Help Chipo finish secondary school");
  await page.getByRole("button", { name: "Next" }).click();

  // Step 3: story with a live character count.
  await expect(page.getByRole("heading", { name: "Step 3 of 8: Story" })).toBeFocused();
  await page.getByRole("textbox", { name: "Story", exact: true }).fill(STORY);
  await expect(page.getByText(new RegExp(`${[...STORY].length} of 20,000 characters`))).toBeVisible();
  await page.getByRole("button", { name: "Back" }).click();
  await expect(page.getByRole("textbox", { name: "Title", exact: true })).toHaveValue("Help Chipo finish secondary school");
  await page.getByRole("button", { name: "Next" }).click();
  await expect(page.getByRole("textbox", { name: "Story", exact: true })).toHaveValue(STORY);
  await page.getByRole("button", { name: "Next" }).click();

  // Step 4: only available currencies; amount entered as a decimal string.
  await expect(page.getByRole("heading", { name: "Step 4 of 8: Goal" })).toBeFocused();
  const currency = page.getByLabel("Currency");
  await expect(currency.locator("option", { hasText: "USD" })).toHaveCount(1);
  await expect(currency.locator("option", { hasText: "ZiG" })).toHaveCount(0);
  await currency.selectOption("USD");
  await page.getByLabel(/Goal amount/).fill("1,500.555");
  await page.getByRole("button", { name: "Save and continue" }).click();
  await expect(page.getByText("This currency has fewer decimal places. Remove the extra digits.")).toBeVisible();
  await page.getByLabel(/Goal amount/).fill("1,500.50");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Save and continue" }).click();

  // Step 5: the draft now exists (URL carries the id); choose the beneficiary.
  await expect(page.getByRole("heading", { name: "Step 5 of 8: Beneficiary" })).toBeFocused();
  await expect(page).toHaveURL(/\/dashboard\/campaigns\/new\?id=[0-9a-f-]{36}&step=5$/);
  const id = /id=([0-9a-f-]{36})/.exec(page.url())![1]!;
  expect(await campaignInfo(page, id)).toMatchObject({ status: "DRAFT", goal: { amount_minor: "150050", currency: "USD" } });
  await expect(page.getByRole("link", { name: "Register a new beneficiary" })).toHaveAttribute("href", "/dashboard/verification/beneficiaries");
  await page.getByRole("radio", { name: /Chipo Moyo/ }).check();
  await page.getByRole("checkbox", { name: /I am the beneficiary/ }).check();
  await page.getByRole("button", { name: "Save beneficiary" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Beneficiary saved" })).toBeVisible();
  await page.getByRole("button", { name: "Next" }).click();

  // Step 6: cover upload with progress and processing states.
  await expect(page.getByRole("heading", { name: "Step 6 of 8: Photos" })).toBeFocused();
  await page.getByLabel("Describe the photo").fill("Chipo in her school uniform");
  await page.getByLabel("Photo", { exact: true }).setInputFiles({ name: "cover.png", mimeType: "image/png", buffer: PNG });
  await page.getByRole("button", { name: "Upload photo" }).click();
  const photos = page.getByRole("list", { name: "Campaign photos" });
  await expect(photos.getByText("Ready")).toHaveCount(1, { timeout: 15_000 });
  await expect(photos.getByRole("img", { name: "Chipo in her school uniform" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Next" }).click();

  // Step 7: eligibility check passes.
  await expect(page.getByRole("heading", { name: "Step 7 of 8: Check" })).toBeFocused();
  await expect(page.getByText("Everything needed for review is in place.")).toBeVisible();
  await page.getByRole("button", { name: "Continue to submit" }).click();

  // Step 8: submit; copy never implies approval.
  await expect(page.getByText(/submitting does not guarantee approval/)).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Submit for review" }).click();
  await expect(page).toHaveURL(new RegExp(`/dashboard/campaigns/${id}\\?submitted=1$`));
  await expect(page.getByText("Submitted for review")).toBeVisible();
  await expect(page.getByText("Waiting for review").first()).toBeVisible();
  expect(await axe(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("wizard eligibility lists what is missing with fix links", async ({ page }) => {
  const email = await signInNewUser(page, { kycLevel: "BASIC_VERIFIED" });
  const { id } = await seedCampaign(page, email, { status: "DRAFT" });
  await page.goto(`/dashboard/campaigns/new?id=${id}&step=7`);
  await expect(page.getByRole("heading", { name: "Step 7 of 8: Check" })).toBeVisible();
  const fix = page.getByRole("link", { name: "Verify your identity" });
  await expect(fix).toHaveAttribute("href", "/dashboard/verification/identity");
  await expect(page.getByRole("button", { name: "Continue to submit" })).toHaveCount(0);
  await page.getByRole("button", { name: "Next" }).click();
  await expect(page.getByRole("button", { name: "Submit for review" })).toBeDisabled();
});

test("age attestation grants BASIC_VERIFIED; the wizard links to it when needed", async ({ page }) => {
  await signInNewUser(page, { kycLevel: "UNVERIFIED" });
  await page.goto("/dashboard/campaigns/new");
  await expect(page.getByText("Before you can save a campaign")).toBeVisible();
  await page.getByRole("link", { name: "Confirm your age" }).click();
  await expect(page).toHaveURL(/\/dashboard\/verification\/age\?next=%2Fdashboard%2Fcampaigns%2Fnew$/);
  await expect(page.getByText("This is a self-declaration, not proof of age")).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Save my declaration" }).click();
  await expect(page.getByText("Choose one of the options.")).toBeVisible();
  await page.getByRole("radio", { name: "I declare that I am at least 18 years old." }).check();
  await page.getByRole("button", { name: "Save my declaration" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Your declaration was recorded" })).toBeFocused();
  await expect(page.getByRole("link", { name: "Continue where you were" })).toHaveAttribute("href", "/dashboard/campaigns/new");

  await page.goto("/dashboard/verification");
  await expect(page.getByText("Basic verified (email, phone and age declaration)")).toBeVisible();
  await page.goto("/dashboard/campaigns/new");
  await expect(page.getByText("Before you can save a campaign")).toHaveCount(0);
});

test("registration can include the optional age declaration", async ({ page }) => {
  const email = `reg-${Date.now()}@example.test`;
  await page.goto("/register");
  await page.getByLabel("Email address").fill(email);
  await page.getByLabel("Display name").fill("Rudo");
  await page.getByLabel("Password", { exact: true }).fill("a long enough passphrase");
  await page.getByRole("checkbox", { name: /I accept/ }).check();
  await page.getByRole("checkbox", { name: /18 or older/ }).check();
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.getByText("Check your email")).toBeVisible();
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/age-attestation`, { data: { email } });
  expect(((await res.json()) as { data: { attestation: { outcome: string; source: string } } }).data.attestation).toMatchObject({ outcome: "ATTESTED", source: "REGISTRATION" });
});

test("reviewer approves with step-up and publishes; the public page shows no totals", async ({ page, browser }) => {
  const problems = watchForBreakage(page);
  const ownerEmail = await createUser(page, { phoneVerified: true, kycLevel: "IDENTITY_VERIFIED", displayName: "Tendai Organiser" });
  const title = `Rebuild the Mbare clinic roof ${Date.now().toString(36)}`;
  const { id } = await seedCampaign(page, ownerEmail, { title, category: "COMMUNITY" });
  const staff = await createStaff(page, ["REVIEWER"], "Rumbi Reviewer");
  await signInStaff(page, staff, "/admin/campaigns/review");

  await expect(page.getByRole("heading", { level: 1, name: "Campaign reviews" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.getByRole("link", { name: title }).click();
  await expect(page).toHaveURL(new RegExp(`/admin/campaigns/review/${id}$`));
  expect(await axe(page)).toEqual([]);

  const act = async (button: string, reason: string, note: string, extra?: (dialog: ReturnType<Page["getByRole"]>) => Promise<void>) => {
    await page.getByRole("button", { name: button, exact: true }).click();
    const dialog = page.getByRole("dialog", { name: button });
    await dialog.getByLabel("Reason").selectOption(reason);
    await dialog.getByLabel("Internal note (staff only)").fill(note);
    if (extra) await extra(dialog);
    await dialog.getByRole("button", { name: `Confirm: ${button}` }).click();
  };

  // A note is required (3–5000 characters).
  await page.getByRole("button", { name: "Assign to me" }).click();
  const assign = page.getByRole("dialog", { name: "Assign to me" });
  await assign.getByLabel("Reason").selectOption("REVIEW_ASSIGNMENT");
  await assign.getByRole("button", { name: "Confirm: Assign to me" }).click();
  await expect(assign.getByText(/Write an internal note of 3 to 5,000 characters/)).toBeVisible();
  await assign.getByLabel("Internal note (staff only)").fill("Taking this one.");
  await assign.getByRole("button", { name: "Confirm: Assign to me" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Assign to me: done." })).toBeVisible();

  await act("Start review", "REVIEW_STARTED", "Starting the review.");
  await expect(page.getByRole("status").filter({ hasText: "Start review: done." })).toBeVisible();

  // Approve → STEP_UP_REQUIRED → authenticator code → retried once.
  await act("Approve", "MEETS_POLICY", "Story, beneficiary and cover meet policy.");
  const stepUp = page.getByRole("dialog", { name: "Confirm it's you" });
  await expect(stepUp).toBeVisible();
  await stepUp.getByLabel("Authentication code").fill(TOTP);
  await stepUp.getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Approve: done." })).toBeVisible();
  await expect(page.getByText("Approved", { exact: true }).first()).toBeVisible();

  await act("Publish", "APPROVED_FOR_PUBLICATION", "Publishing after approval.");
  await expect(page.getByRole("status").filter({ hasText: "Publish: done." })).toBeVisible();
  await expect(page.getByText("Active (published)").first()).toBeVisible();
  expect(problems).toEqual([]);

  // Public page, as an anonymous visitor.
  const { slug, status } = await campaignInfo(page, id);
  expect(status).toBe("ACTIVE");
  const anonContext = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const anon = await anonContext.newPage();
  await trackCspViolations(anon);
  const response = await anon.goto(`/campaigns/${slug}`);
  expect(response?.status()).toBe(200);
  await expect(anon.getByRole("heading", { level: 1, name: title })).toBeVisible();
  await expect(anon.getByRole("button", { name: "Donations are not yet available." })).toBeDisabled();
  await expect(anon.getByText("US$1,500.00")).toBeVisible();
  await expect(anon.getByText("Details kept private")).toBeVisible();
  await expect(anon.getByText("Tendai Organiser")).toBeVisible();
  await expect(anon.getByRole("img", { name: `Cover photo for ${title}` })).toBeVisible();
  await expect(anon.locator("main")).not.toContainText(/raised|donors|funded/i);
  await expect(anon.locator("progress, [role=progressbar]")).toHaveCount(0);
  await expect(anon.getByRole("button", { name: "Copy link" })).toBeVisible();
  await expect(anon.locator('meta[property="og:title"]')).toHaveAttribute("content", title);
  expect(await seriousAxeViolations(anon)).toEqual([]);
  expect(await cspViolations(anon)).toEqual([]);

  // Listed publicly and searchable.
  await anon.goto(`/campaigns?q=${encodeURIComponent("mbare clinic")}`);
  await expect(anon.getByRole("link", { name: title })).toBeVisible();
  expect(await seriousAxeViolations(anon)).toEqual([]);
  await anonContext.close();
});

test("second approval is not offered to the first approver (four eyes)", async ({ page }) => {
  const ownerEmail = await createUser(page, { phoneVerified: true, kycLevel: "IDENTITY_VERIFIED" });
  const title = `Support for a child ${Date.now().toString(36)}`;
  await seedCampaign(page, ownerEmail, { title, category: "CHILD_WELFARE" });
  const staff = await createStaff(page, ["COMPLIANCE"], "Farai Compliance");
  await signInStaff(page, staff, "/admin/campaigns/review");
  await page.getByRole("link", { name: title }).click();
  for (const [button, reason] of [["Assign to me", "REVIEW_ASSIGNMENT"], ["Start review", "REVIEW_STARTED"], ["Approve", "MEETS_POLICY"]] as const) {
    await page.getByRole("button", { name: button, exact: true }).click();
    const dialog = page.getByRole("dialog", { name: button });
    await dialog.getByLabel("Reason").selectOption(reason);
    await dialog.getByLabel("Internal note (staff only)").fill(`${button} note`);
    await dialog.getByRole("button", { name: `Confirm: ${button}` }).click();
    if (button === "Approve") {
      const stepUp = page.getByRole("dialog", { name: "Confirm it's you" });
      await stepUp.getByLabel("Authentication code").fill(TOTP);
      await stepUp.getByRole("button", { name: "Confirm" }).click();
    }
    await expect(page.getByRole("dialog")).toHaveCount(0);
  }
  await expect(page.getByRole("status").filter({ hasText: "First approval recorded" })).toBeVisible();
  await expect(page.getByText("You gave the first approval. A different reviewer must give the second approval.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Give second approval" })).toHaveCount(0);
});

test("owner publishes an approved campaign; story text is rendered inert", async ({ page }) => {
  await trackCspViolations(page);
  const email = await signInNewUser(page, { kycLevel: "IDENTITY_VERIFIED" });
  const payload = `Normal first paragraph.\n\n<script>window.__xss=1</script><img src=x onerror="window.__xss=2"> javascript:alert(3) https://evil.example/\n\n<a href="https://evil.example">click me</a>`;
  const { id } = await seedCampaign(page, email, { status: "APPROVED", story: payload, title: "Inert story campaign test" });
  await page.goto(`/dashboard/campaigns/${id}`);
  await expect(page.getByText("Your approved campaign can be published.")).toBeVisible();
  await page.getByRole("button", { name: "Publish" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Yes, publish" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Your campaign is published." })).toBeVisible();
  await page.getByRole("link", { name: "View the public page" }).click();

  await expect(page.getByRole("heading", { level: 1, name: "Inert story campaign test" })).toBeVisible();
  const story = page.locator("section", { has: page.getByRole("heading", { name: "The story" }) });
  await expect(story).toContainText("<script>window.__xss=1</script>");
  await expect(story).toContainText('<a href="https://evil.example">click me</a>');
  await expect(story.locator("script, img, a")).toHaveCount(0);
  await expect(story.locator("p")).toHaveCount(3);
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
  expect(await cspViolations(page)).toEqual([]);
});

test("unpublished, suspended and malformed slugs get the app's 404", async ({ page }) => {
  const email = await createUser(page, { phoneVerified: true, kycLevel: "IDENTITY_VERIFIED" });
  const suspended = await seedCampaign(page, email, { status: "SUSPENDED", title: "Suspended campaign title here" });
  for (const path of ["/campaigns/not-a-published-campaign-x1", `/campaigns/${suspended.slug}`, "/campaigns/UPPER-case"]) {
    const response = await page.goto(path);
    expect(response?.status(), path).toBe(404);
    await expect(page.getByRole("heading", { level: 1, name: "We couldn't find that page" })).toBeVisible();
    await expect(page.locator("main")).not.toContainText("Suspended campaign title here");
  }
  // Paused and completed campaigns stay public with a notice; unlisted ones are reachable but not listed.
  const paused = await seedCampaign(page, email, { status: "PAUSED", title: "Paused unlisted campaign", visibility: "UNLISTED" });
  await page.goto(`/campaigns/${paused.slug}`);
  await expect(page.getByText("This campaign is paused")).toBeVisible();
  await page.goto("/campaigns?q=paused%20unlisted");
  await expect(page.getByText("No campaigns found")).toBeVisible();
});

test("staff area is a 404 for users and for staff without campaign permissions", async ({ page }) => {
  await signInNewUser(page);
  for (const path of ["/admin/campaigns/review", "/admin/campaigns", "/admin/campaigns/updates", "/admin/account/personal-link"]) {
    const response = await page.goto(path);
    expect(response?.status(), path).toBe(404);
  }
  await page.context().clearCookies();
  const kyc = await createStaff(page, ["KYC_REVIEWER"]);
  await signInStaff(page, kyc, "/admin/verification");
  for (const path of ["/admin/campaigns/review", "/admin/campaigns/updates"]) {
    const response = await page.goto(path);
    expect(response?.status(), path).toBe(404);
  }
});

test("owner settings: visibility, pause, resume and complete with a reason; updates", async ({ page }) => {
  const email = await signInNewUser(page, { kycLevel: "IDENTITY_VERIFIED" });
  const { id, slug } = await seedCampaign(page, email, { status: "ACTIVE", title: "Settings flow campaign" });
  await page.goto(`/dashboard/campaigns/${id}/settings`);
  expect(await axe(page)).toEqual([]);
  await page.getByRole("radio", { name: /Unlisted/ }).check();
  await page.getByRole("button", { name: "Save visibility" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Visibility saved" })).toBeVisible();

  await page.getByRole("button", { name: "Pause", exact: true }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Yes, pause" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Your campaign is paused." })).toBeVisible();
  await page.getByRole("button", { name: "Complete", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Yes, complete" }).click();
  await expect(dialog.getByText("Choose why the campaign is ending.").last()).toBeVisible();
  await dialog.getByLabel("Why is the campaign ending?").selectOption("ORGANISER_COMPLETED");
  await dialog.getByRole("button", { name: "Yes, complete" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Your campaign is completed." })).toBeVisible();

  await page.goto(`/dashboard/campaigns/${id}/updates`);
  await page.getByRole("textbox", { name: "Title", exact: true }).fill("Thank you all");
  await page.getByRole("textbox", { name: "Update", exact: true }).fill("The school fees are covered.\n\nThank you for the support.");
  await page.getByRole("button", { name: "Post update" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Update posted." })).toBeVisible();
  await page.goto(`/campaigns/${slug}`);
  await expect(page.getByText("This campaign has ended")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Thank you all" })).toBeVisible();
});

test("staff links a personal account (step-up), the personal account confirms", async ({ page, browser }) => {
  const personalEmail = await createUser(page, { phoneVerified: true });
  const staff = await createStaff(page, ["REVIEWER"], "Linked Reviewer");
  await signInStaff(page, staff, "/admin/account/personal-link");
  expect(await axe(page)).toEqual([]);
  await page.getByLabel("Your personal account's email address").fill(personalEmail);
  await page.getByRole("button", { name: "Send link request" }).click();
  const stepUp = page.getByRole("dialog", { name: "Confirm it's you" });
  await stepUp.getByLabel("Authentication code").fill(TOTP);
  await stepUp.getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Request sent" })).toBeVisible();

  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/staff-link-token`, { data: { staff_email: staff } });
  const token = ((await res.json()) as { data: { token: string } }).data.token;
  const personalContext = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const personal = await personalContext.newPage();
  await signIn(personal, personalEmail);
  await expect(personal).toHaveURL(/\/dashboard$/);
  await personal.goto(`/dashboard/account/staff-link?token=${token}`);
  expect(await axe(personal)).toEqual([]);
  await personal.getByRole("button", { name: "Confirm the link" }).click();
  await expect(personal.getByRole("status").filter({ hasText: "Your personal account is now linked" })).toBeVisible();
  await personalContext.close();

  // The linked staff member can't decide their own campaign.
  const { id } = await seedCampaign(page, personalEmail, { title: "My own campaign as staff" });
  await page.goto(`/admin/campaigns/review/${id}`);
  await page.getByRole("button", { name: "Assign to me" }).click();
  const dialog = page.getByRole("dialog", { name: "Assign to me" });
  await dialog.getByLabel("Reason").selectOption("REVIEW_ASSIGNMENT");
  await dialog.getByLabel("Internal note (staff only)").fill("Should be refused.");
  await dialog.getByRole("button", { name: "Confirm: Assign to me" }).click();
  await expect(dialog.getByText(/can't decide on a campaign that involves you/)).toBeVisible();
});
