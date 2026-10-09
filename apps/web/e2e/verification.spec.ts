import { expect, test, type Locator, type Page } from "@playwright/test";

import {
  AUTH_BASE_URL,
  createStaff,
  createUser,
  cspViolations,
  seedSubmittedKyc,
  seriousAxeViolations,
  signIn,
  signInStaff,
  TOTP,
  trackCspViolations,
  watchForBreakage,
} from "./helpers";

// Stage 5 verification UI against the in-memory mock API (e2e/mock-api/verification.mjs).
test.use({ baseURL: AUTH_BASE_URL });

/** Smallest valid PNG header bytes: enough for the mock's magic-byte sniffing. */
const PNG = Buffer.from("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c489", "hex");
/** A "PDF" carrying the mock scanner's malware marker (not real malware; see verification.mjs). */
const FLAGGED_PDF = Buffer.from("%PDF-1.4\nFUNDZIM-MOCK-MALWARE-MARKER\n%%EOF");

/**
 * axe from the top of the page: the site header is sticky, so after scrolling down it overlaps the side
 * navigation and axe's target-size rule reports the links as obscured (a scroll-position artefact).
 */
async function axe(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 0));
  return seriousAxeViolations(page);
}

const statusWith = (page: Page | Locator, text: string | RegExp) => page.getByRole("status").filter({ hasText: text });

let seq = 0;
const unique = (prefix: string) => `${prefix} ${Date.now().toString(36)}${(seq++).toString(36)}`;

async function signInNewUser(page: Page, opts: Parameters<typeof createUser>[1] = {}) {
  const email = await createUser(page, { phoneVerified: true, ...opts });
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  return email;
}

/** Uploads through one DocumentManager's form (`Upload to <heading>`), so several managers on a page don't clash. */
async function upload(scope: Page | Locator, heading: string, opts: { type?: string; side?: string; name: string; buffer: Buffer; mimeType: string }) {
  const form = scope.getByRole("form", { name: `Upload to ${heading}` });
  if (opts.type) await form.getByLabel("Document type").selectOption(opts.type);
  if (opts.side) await form.getByLabel("Side").selectOption(opts.side);
  await form.getByLabel("File", { exact: true }).setInputFiles({ name: opts.name, mimeType: opts.mimeType, buffer: opts.buffer });
  await form.getByRole("button", { name: "Upload", exact: true }).click();
}

async function fillIdentity(page: Page) {
  await page.getByLabel("First name(s)").fill("Chipo");
  await page.getByLabel("Surname").fill("Ncube");
  await page.getByLabel("Date of birth").fill("1990-04-18");
  await page.getByLabel("Nationality").selectOption("ZW");
  await page.getByLabel("Country you live in").selectOption("ZW");
  await page.getByLabel("Document type").selectOption("ZW_NATIONAL_ID");
  await page.getByLabel("Document number").fill("63-123456 A 12");
  await page.getByLabel("Address line 1").fill("12 Samora Machel Avenue");
  await page.getByLabel("Town or city").fill("Harare");
}

test("KYC happy path: start → details → upload → submit → status", async ({ page }) => {
  const problems = watchForBreakage(page);
  await signInNewUser(page);

  await page.goto("/dashboard/verification");
  await expect(page.getByRole("heading", { level: 1, name: "Verification" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "What to do next: Verify your identity" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.getByRole("link", { name: "Start verification" }).click();

  await expect(page).toHaveURL(/\/dashboard\/verification\/identity$/);
  await page.getByRole("button", { name: "Start verification" }).click();
  await expect(page.getByRole("heading", { name: "Step 1: Your personal details" })).toBeFocused();
  await fillIdentity(page);
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Save and continue" }).click();
  await expect(page.getByRole("heading", { name: "Step 2: Check and submit" })).toBeFocused();
  // Only the masked number comes back.
  await expect(page.locator("main")).not.toContainText("63-123456");

  // Submitting before the documents are uploaded is refused with a linked summary.
  await page.getByRole("button", { name: "Submit for verification" }).click();
  await expect(page.getByText("Some things need attention before you can submit.")).toBeFocused();
  await expect(page.getByRole("link", { name: "Zimbabwe national ID (front)" })).toBeVisible();

  await page.getByRole("link", { name: "Upload or manage documents" }).click();
  await expect(page).toHaveURL(/\/dashboard\/verification\/documents$/);
  expect(await axe(page)).toEqual([]);
  await upload(page, "Your documents", { side: "FRONT", name: "front.png", buffer: PNG, mimeType: "image/png" });
  await upload(page, "Your documents", { side: "BACK", name: "back.png", buffer: PNG, mimeType: "image/png" });
  const list = page.getByRole("list", { name: "Your documents: uploaded files" });
  await expect(list.getByText("Ready")).toHaveCount(2, { timeout: 15_000 });
  await expect(page.getByText("Provided")).toHaveCount(1);

  // The owner can open a clean document through the access-ticket flow (download, page stays).
  const download = page.waitForEvent("download");
  await list.getByRole("button", { name: "View Zimbabwe national ID front" }).click();
  expect((await download).suggestedFilename()).toMatch(/^document-.*\.png$/);
  await expect(page).toHaveURL(/\/dashboard\/verification\/documents$/);

  await page.getByRole("link", { name: "Continue to check and submit" }).click();
  await page.getByRole("button", { name: "Submit for verification" }).click();
  await expect(statusWith(page, /Thank you\. Your details were submitted for review/)).toBeFocused();
  await expect(page.getByText("Your details can't be changed while they are being reviewed.")).toBeVisible();

  await page.goto("/dashboard/verification");
  await expect(page.getByText("Submitted — waiting for review")).toBeVisible();
  await expect(page.getByRole("heading", { name: "What to do next: We are reviewing your details" })).toBeVisible();
  expect(problems).toEqual([]);
});

test("additional-information loop: request shown → update documents → submit again", async ({ page }) => {
  const email = await createUser(page, { phoneVerified: true });
  const caseId = await seedSubmittedKyc(page, email, "Loop");
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/review/${caseId}/request-info`, {
    data: { message: "The back of your ID is blurry. Please upload a clearer photo.", items: ["ZW_NATIONAL_ID"] },
  });
  expect(res.status()).toBe(200);
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto("/dashboard/verification");
  await expect(page.getByRole("heading", { name: "What to do next: We need more information" })).toBeVisible();
  await page.getByRole("link", { name: "See what is needed" }).click();
  await expect(page.getByText("The back of your ID is blurry. Please upload a clearer photo.")).toBeVisible();
  await expect(page.getByText("Waiting for you")).toBeVisible();

  await page.goto("/dashboard/verification/documents");
  const list = page.getByRole("list", { name: "Your documents: uploaded files" });
  await list.getByRole("button", { name: "Delete Zimbabwe national ID back" }).click();
  await page.getByRole("dialog", { name: "Delete this document?" }).getByRole("button", { name: "Delete document" }).click();
  await expect(list.getByText(/— Back/)).toHaveCount(0);
  await upload(page, "Your documents", { side: "BACK", name: "back-clear.png", buffer: PNG, mimeType: "image/png" });
  await expect(list.getByText("Ready")).toHaveCount(2, { timeout: 15_000 });

  await page.goto("/dashboard/verification/identity");
  await page.getByRole("button", { name: "Submit again" }).click();
  await expect(page.getByText(/Thank you\. Your details were submitted for review/)).toBeVisible();
  await page.reload();
  await expect(page.getByText(/^Answered/)).toBeVisible();
  await expect(page.getByText("Submitted — waiting for review")).toBeVisible();
});

test("a document rejected by the scan is shown as rejected with the reason", async ({ page }) => {
  await signInNewUser(page);
  await page.goto("/dashboard/verification/identity");
  await page.getByRole("button", { name: "Start verification" }).click();
  await page.goto("/dashboard/verification/documents");
  await upload(page, "Your documents", { type: "ZW_PASSPORT", name: "passport.pdf", buffer: FLAGGED_PDF, mimeType: "application/pdf" });
  const list = page.getByRole("list", { name: "Your documents: uploaded files" });
  await expect(list.getByText("Rejected", { exact: true })).toBeVisible({ timeout: 15_000 });
  await expect(list.getByText("Reason: the file failed the malware check")).toBeVisible();
  await expect(list.getByRole("button", { name: /^View/ })).toHaveCount(0);

  // A renamed file (declared type ≠ content) is refused at upload with a clear message.
  await upload(page, "Your documents", { type: "ZW_PASSPORT", name: "photo.png", buffer: FLAGGED_PDF, mimeType: "image/png" });
  await expect(page.getByText(/contents don't match its type/)).toBeVisible();
});

test("beneficiary: create a minor (extra evidence flagged) → upload evidence → submit", async ({ page }) => {
  const problems = watchForBreakage(page);
  await signInNewUser(page);
  await page.goto("/dashboard/verification/beneficiaries");
  await page.getByLabel("Who are the funds for?").selectOption("MINOR");
  await expect(page.getByText("Extra evidence needed")).toBeVisible();
  await expect(page.getByLabel("What gives you authority to raise funds for them?")).toHaveValue("PARENTAL_RESPONSIBILITY");
  const name = unique("Tafadzwa");
  await page.getByLabel("Name to show on campaigns").fill(name);
  await page.getByLabel("Full legal name").fill(`${name} Moyo`);
  await page.getByLabel("Date of birth").fill("2016-05-01");
  await page.getByLabel("Your relationship to them").selectOption("PARENT_GUARDIAN");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Add beneficiary" }).click();
  await expect(statusWith(page, `${name} was added.`)).toBeFocused();

  const card = page.getByRole("listitem").filter({ has: page.getByRole("heading", { name }) });
  await expect(card.getByText("Two reviewers required")).toBeVisible();
  await card.getByRole("button", { name: `Submit ${name} for verification` }).click();
  await expect(card.getByRole("link", { name: "Birth certificate" })).toBeVisible();

  await upload(card, "Evidence", { type: "BIRTH_CERTIFICATE", name: "birth.pdf", buffer: Buffer.from("%PDF-1.4\n%%EOF"), mimeType: "application/pdf" });
  await expect(card.getByText("Ready", { exact: true })).toBeVisible({ timeout: 15_000 });
  await card.getByRole("button", { name: `Submit ${name} for verification` }).click();
  await expect(card.getByText(/Submitted for verification/)).toBeVisible();
  await expect(card.getByText("Submitted — waiting for review")).toBeVisible();
  expect(problems).toEqual([]);
});

test("payout destination: masked only, three separate checks, PROVIDER_CONFIRMATION_REQUIRED shown honestly", async ({ page }) => {
  await signInNewUser(page);
  await page.goto("/dashboard/verification/payout-destinations");
  await expect(page.getByText("Payouts are not available yet")).toBeVisible();
  await page.getByRole("button", { name: "Add a payout account" }).click();
  await page.getByLabel("How would money be received?").selectOption("ECOCASH");
  await page.getByLabel("Currency").selectOption("USD");
  await page.getByLabel("Account holder name").fill("Chipo Ncube");
  await page.getByLabel("Mobile money number").fill("0771234567");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("button", { name: "Add account" }).click();
  await expect(page.getByRole("heading", { name: /EcoCash · •+4567/ })).toBeVisible();
  await expect(page.locator("main")).not.toContainText("0771234567");
  await expect(page.getByText("Valid format")).toBeVisible();
  await expect(page.locator("p", { hasText: "Can receive payouts:" })).toContainText("No — payouts are not available yet");

  await page.getByRole("button", { name: "Verify this account" }).click();
  await page.getByLabel("How can you show this account is yours?").selectOption("PROVIDER_LOOKUP");
  await page.getByRole("button", { name: "Request verification" }).click();
  await expect(page.getByText(/An automatic check with this provider is not available yet/).first()).toBeVisible();
  await expect(page.getByText("Provider confirmation required", { exact: true })).toBeVisible();
  await expect(page.getByText("Verification pending")).toBeVisible();
  // Reloading shows the same honest state from the API; the full number never appears.
  await page.reload();
  await expect(page.getByText("Provider confirmation required", { exact: true })).toBeVisible();
  await expect(page.locator("main")).not.toContainText("0771234567");
});

test("organisation KYB: create organisation → details → persons (percent → basis points) → document → submit", async ({ page }) => {
  const problems = watchForBreakage(page);
  await signInNewUser(page, { kycLevel: "IDENTITY_VERIFIED" });
  await page.goto("/dashboard/organisations");
  const orgName = unique("Harare Community Trust");
  await page.getByLabel("Organisation name").fill(orgName);
  await page.getByLabel("Type of organisation").selectOption("TRUST");
  await page.getByRole("button", { name: "Create organisation" }).click();
  await expect(page.getByText(`${orgName} was created.`, { exact: false })).toBeVisible();
  await page.getByRole("link", { name: `Verification for ${orgName}` }).click();
  await expect(page.getByRole("heading", { level: 1, name: `Verify ${orgName}` })).toBeVisible();
  await expect(page.getByText("Your identity is verified")).toBeVisible();
  await page.getByRole("button", { name: "Start verification" }).click();

  await page.getByLabel("Registered name").fill(orgName);
  await page.getByLabel("Registration number").fill("MA 123/2020");
  await page.getByLabel("Registry", { exact: true }).fill("Deeds Office");
  await page.getByLabel("Address line 1").fill("4 Josiah Tongogara St");
  await page.getByLabel("Town or city").fill("Harare");
  await page.getByRole("button", { name: "Save details" }).click();
  await expect(page.getByText("Organisation details saved.")).toBeVisible();

  const addPerson = async (name: string, roles: string[], percent?: string) => {
    const form = page.getByRole("form", { name: "Add a person" });
    await form.getByLabel("Full legal name").fill(name);
    for (const role of roles) await form.getByRole("checkbox", { name: role }).check();
    if (percent) await form.getByLabel(/Ownership share/).fill(percent);
    await form.getByRole("button", { name: "Add person" }).click();
    await expect(page.getByText(`${name} was added.`)).toBeVisible();
  };
  // Invalid share is rejected, not rounded.
  const form = page.getByRole("form", { name: "Add a person" });
  await form.getByLabel("Full legal name").fill("Too Precise");
  await form.getByRole("checkbox", { name: "Beneficial owner" }).check();
  await form.getByLabel(/Ownership share/).fill("12.345");
  await form.getByRole("button", { name: "Add person" }).click();
  await expect(form.getByText("Use at most two decimal places, for example 33.33.")).toBeVisible();
  await form.getByLabel(/Ownership share/).fill("");
  await form.getByRole("checkbox", { name: "Beneficial owner" }).uncheck();
  await form.getByLabel("Full legal name").fill("");

  await addPerson("Rudo Dube", ["Trustee", "Beneficial owner"], "25.5");
  await addPerson("Tendai Moyo", ["Trustee"]);
  const table = page.getByRole("table", { name: "People connected to the organisation" });
  await expect(table.getByRole("row", { name: /Rudo Dube/ })).toContainText("25.5%");
  await expect(table.getByRole("row", { name: /Total recorded ownership/ })).toContainText("25.5%");
  expect(await axe(page)).toEqual([]);

  await upload(page, "Registration documents", { type: "REGISTRATION_CERTIFICATE", name: "registration.pdf", buffer: Buffer.from("%PDF-1.4\n%%EOF"), mimeType: "application/pdf" });
  await expect(page.getByRole("list", { name: "Registration documents: uploaded files" }).getByText("Ready", { exact: true })).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "Submit for verification" }).click();
  await expect(statusWith(page, /Submitted for review\./)).toBeVisible();
  await expect(page.getByText("Submitted — waiting for review").first()).toBeVisible();
  await expect(page.getByRole("heading", { name: "Submission history" })).toBeVisible();
  expect(problems).toEqual([]);
});

test("organisation pages: non-members get 404", async ({ page }) => {
  await signInNewUser(page);
  const response = await page.goto("/dashboard/organisations/0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b/verification");
  expect(response?.status()).toBe(404);
  await expect(page.getByRole("heading", { name: "We couldn't find that page" })).toBeVisible();
});

test("admin: queue → assign → start review → approve with TOTP step-up", async ({ page, browser }) => {
  const problems = watchForBreakage(page);
  const subjectName = unique("Approve Subject");
  const userEmail = await createUser(page, { phoneVerified: true, displayName: subjectName });
  await seedSubmittedKyc(page, userEmail, "Approve");
  const staff = await createStaff(page, ["KYC_REVIEWER"], "Reviewer Approve");
  await signInStaff(page, staff);

  await expect(page.getByRole("heading", { level: 1, name: "Verification queue" })).toBeVisible();
  expect(await axe(page)).toEqual([]);
  await page.getByLabel("Type").selectOption("KYC");
  await page.getByLabel("Assigned").selectOption("unassigned");
  await page.getByRole("button", { name: "Apply filters" }).click();
  await expect(page).toHaveURL(/type=KYC/);
  await page.getByRole("link", { name: subjectName }).click();

  await expect(page.getByRole("heading", { level: 1, name: "KYC verification" })).toBeVisible();
  await expect(page.locator("main")).not.toContainText("63-123456");
  expect(await axe(page)).toEqual([]);
  const actions = page.getByRole("region", { name: "Actions" });
  await actions.getByRole("button", { name: "Assign to me" }).click();
  await page.getByRole("dialog", { name: "Assign to me" }).getByRole("button", { name: "Confirm: Assign to me" }).click();
  await expect(page.getByText("Assign to me: done.")).toBeVisible();
  await actions.getByRole("button", { name: "Start review" }).click();
  await page.getByRole("dialog", { name: "Start review" }).getByRole("button", { name: "Confirm: Start review" }).click();
  await expect(page.getByText("Under review", { exact: true })).toBeVisible();

  // Viewing a document needs step-up too.
  await actions.getByRole("button", { name: "Approve" }).click();
  const dialog = page.getByRole("dialog", { name: "Approve" });
  await dialog.getByLabel("Reason").selectOption("IDENTITY_CONFIRMED");
  await dialog.getByLabel("Internal note (reviewers only)").fill("Photo and details consistent.");
  await dialog.getByRole("button", { name: "Confirm: Approve" }).click();
  const stepUp = page.getByRole("dialog", { name: "Confirm it's you" });
  await expect(stepUp.getByLabel("Authentication code")).toBeFocused();
  await stepUp.getByLabel("Authentication code").fill(TOTP);
  await stepUp.getByRole("button", { name: "Confirm" }).click();
  await expect(statusWith(page, "Approve: done.")).toBeFocused();
  await expect(page.getByText("Approved", { exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Audit history" })).toContainText("Verification approved");

  // Step-up is fresh now: a document view goes straight to the download.
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "View Zimbabwe national ID front" }).click();
  await download;

  // The subject now sees the decision.
  const userContext = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const userPage = await userContext.newPage();
  await signIn(userPage, userEmail);
  await expect(userPage).toHaveURL(/\/dashboard$/);
  await userPage.goto("/dashboard/verification");
  await expect(userPage.getByText("Identity verified")).toBeVisible();
  await userContext.close();
  expect(problems).toEqual([]);
});

test("admin: four-eyes KYC — the first approver cannot give the second approval; another reviewer can", async ({ page, browser }) => {
  const subjectName = unique("FourEyes Subject");
  const userEmail = await createUser(page, { phoneVerified: true, displayName: subjectName });
  const caseId = await seedSubmittedKyc(page, userEmail, "Four", { fourEyes: true });
  const first = await createStaff(page, ["KYC_REVIEWER"], "First Reviewer");
  await signInStaff(page, first);
  await page.goto(`/admin/verification/cases/${caseId}`);
  await expect(page.getByText("Four-eyes: two different reviewers must approve")).toBeVisible();
  const actions = page.getByRole("region", { name: "Actions" });
  await actions.getByRole("button", { name: "Assign to me" }).click();
  await page.getByRole("button", { name: "Confirm: Assign to me" }).click();
  await actions.getByRole("button", { name: "Start review" }).click();
  await page.getByRole("button", { name: "Confirm: Start review" }).click();
  await actions.getByRole("button", { name: "Approve" }).click();
  const dialog = page.getByRole("dialog", { name: "Approve" });
  await dialog.getByLabel("Reason").selectOption("IDENTITY_CONFIRMED");
  await dialog.getByLabel("Internal note (reviewers only)").fill("First check done.");
  await dialog.getByRole("button", { name: "Confirm: Approve" }).click();
  await page.getByRole("dialog", { name: "Confirm it's you" }).getByLabel("Authentication code").fill(TOTP);
  await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(statusWith(page, /needs a second approval from a different reviewer/)).toBeVisible();
  await expect(page.getByText("You gave the first approval. A different reviewer must give the second approval.")).toBeVisible();
  await expect(actions.getByRole("button", { name: "Give second approval" })).toHaveCount(0);

  const ctx = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const second = await ctx.newPage();
  await signInStaff(second, await createStaff(second, ["KYC_REVIEWER"], "Second Reviewer"));
  await second.goto(`/admin/verification/cases/${caseId}`);
  await expect(second.getByText(/First approval \(Approve\) by Another staff member/)).toBeVisible();
  await second.getByRole("region", { name: "Actions" }).getByRole("button", { name: "Give second approval" }).click();
  const d2 = second.getByRole("dialog", { name: "Give second approval" });
  await d2.getByRole("button", { name: "Confirm: Give second approval" }).click();
  await expect(d2.getByText(/at least 3 characters/)).toBeVisible();
  await d2.getByLabel("Internal note (reviewers only)").fill("Independent check agrees.");
  await d2.getByRole("button", { name: "Confirm: Give second approval" }).click();
  await second.getByRole("dialog", { name: "Confirm it's you" }).getByLabel("Authentication code").fill(TOTP);
  await second.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(second.getByText("Approved", { exact: true })).toBeVisible();
  await ctx.close();
});

test("admin: reject records the decision and the user sees the message", async ({ page, browser }) => {
  const subjectName = unique("Reject Subject");
  const userEmail = await createUser(page, { phoneVerified: true, displayName: subjectName });
  const caseId = await seedSubmittedKyc(page, userEmail, "Reject");
  const staff = await createStaff(page, ["KYC_REVIEWER"], "Reviewer Reject");
  await signInStaff(page, staff, `/admin/verification/kyc`);
  await page.goto(`/admin/verification/cases/${caseId}`);
  const actions = page.getByRole("region", { name: "Actions" });
  await actions.getByRole("button", { name: "Assign to me" }).click();
  await page.getByRole("button", { name: "Confirm: Assign to me" }).click();
  await actions.getByRole("button", { name: "Start review" }).click();
  await page.getByRole("button", { name: "Confirm: Start review" }).click();
  await actions.getByRole("button", { name: "Reject" }).click();
  const dialog = page.getByRole("dialog", { name: "Reject" });
  await dialog.getByRole("button", { name: "Confirm: Reject" }).click();
  await expect(dialog.getByText("Choose a reason.")).toBeVisible();
  await dialog.getByLabel("Reason").selectOption("DOCUMENT_ILLEGIBLE");
  await dialog.getByLabel("Message to the person").fill("The photo of your ID is too blurry to read. Please start again with a clear photo.");
  await dialog.getByLabel("Internal note (reviewers only)").fill("Unreadable ID photo.");
  // Keyboard activation. (On the phone viewport this dialog scrolls internally, and Playwright's pointer
  // click on a scrolled top-layer <dialog> in mobile emulation intermittently hit-tests the element above.)
  await dialog.getByRole("button", { name: "Confirm: Reject" }).focus();
  await page.keyboard.press("Enter");
  await page.getByRole("dialog", { name: "Confirm it's you" }).getByLabel("Authentication code").fill(TOTP);
  await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(page.getByText("Reject: done.")).toBeVisible();
  await expect(page.getByText("Rejected", { exact: true })).toBeVisible();

  const userContext = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const userPage = await userContext.newPage();
  await signIn(userPage, userEmail);
  await expect(userPage).toHaveURL(/\/dashboard$/);
  await userPage.goto("/dashboard/verification/identity");
  await expect(userPage.getByText("Verification decision: not approved")).toBeVisible();
  await expect(userPage.getByText("The photo of your ID is too blurry to read. Please start again with a clear photo.")).toBeVisible();
  await expect(userPage.getByRole("button", { name: "Start verification" })).toBeVisible();
  await userContext.close();
});

test("admin area is not discoverable: anonymous, users and staff without the permission get 404", async ({ page, browser }) => {
  for (const path of ["/admin/verification", "/admin/verification/kyb", "/admin/compliance/cases"]) {
    const anonymous = await page.goto(path);
    expect(anonymous?.status(), path).toBe(404);
    expect(anonymous?.headers()["cache-control"]).toContain("no-store");
  }
  await expect(page).toHaveURL(/\/admin\/compliance\/cases$/); // no login redirect

  await signInNewUser(page);
  for (const path of ["/admin/verification", "/admin/verification/payout-destinations", "/admin/verification/cases/0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "/admin/compliance/cases"]) {
    const response = await page.goto(path);
    expect(response?.status(), path).toBe(404);
    await expect(page.getByRole("heading", { name: "We couldn't find that page" })).toBeVisible();
    await expect(page.getByRole("navigation", { name: "Staff" })).toHaveCount(0);
  }

  const supportContext = await browser.newContext({ baseURL: AUTH_BASE_URL });
  const support = await supportContext.newPage();
  const supportEmail = await createStaff(support, ["SUPPORT"], "Support Person");
  await signIn(support, supportEmail, undefined, "/login?next=%2Fdashboard");
  await support.getByLabel("Authentication code").fill(TOTP);
  await support.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(support).toHaveURL(/\/dashboard$/);
  const response = await support.goto("/admin/verification");
  expect(response?.status()).toBe(404);
  const reviewerOnly = await support.goto("/admin/compliance/cases");
  expect(reviewerOnly?.status()).toBe(404);
  await supportContext.close();
});

test("compliance: escalated case appears in compliance cases; notes and actions work", async ({ page }) => {
  const subjectName = unique("Escalate Subject");
  const userEmail = await createUser(page, { phoneVerified: true, displayName: subjectName });
  const caseId = await seedSubmittedKyc(page, userEmail, "Escalate");
  const staff = await createStaff(page, ["KYC_REVIEWER", "COMPLIANCE"], "Compliance Reviewer");
  await signInStaff(page, staff);
  await page.goto(`/admin/verification/cases/${caseId}`);
  const actions = page.getByRole("region", { name: "Actions" });
  await actions.getByRole("button", { name: "Assign to me" }).click();
  await page.getByRole("button", { name: "Confirm: Assign to me" }).click();
  await actions.getByRole("button", { name: "Start review" }).click();
  await page.getByRole("button", { name: "Confirm: Start review" }).click();
  await actions.getByRole("button", { name: "Escalate to compliance" }).click();
  const dialog = page.getByRole("dialog", { name: "Escalate to compliance" });
  await dialog.getByLabel("Reason").selectOption("POSSIBLE_DUPLICATE_IDENTITY");
  await dialog.getByLabel("Internal note (reviewers only)").fill("Same ID number on another account.");
  await dialog.getByRole("button", { name: "Confirm: Escalate to compliance" }).click();
  await expect(page.getByText("Escalated to compliance", { exact: true })).toBeVisible();

  const found = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/compliance-case-for/${caseId}`, { data: {} });
  expect(found.status()).toBe(200);
  const { id: complianceId, case_number: caseNumber } = ((await found.json()) as { data: { id: string; case_number: string } }).data;
  await page.goto("/admin/compliance/cases?status=OPEN&severity=S2");
  expect(await axe(page)).toEqual([]);
  await page.getByRole("link", { name: caseNumber, exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/admin/compliance/cases/${complianceId}$`));
  await expect(page.getByRole("heading", { level: 1, name: `Compliance case ${caseNumber}` })).toBeVisible();
  const ccActions = page.getByRole("region", { name: "Actions" });
  await ccActions.getByRole("button", { name: "Assign to me" }).click();
  await page.getByRole("button", { name: "Confirm: Assign to me" }).click();
  await expect(page.getByText("Assign to me: done.")).toBeVisible();
  await ccActions.getByRole("button", { name: "Start review" }).click();
  await page.getByRole("button", { name: "Confirm: Start review" }).click();
  await expect(page.getByText("In review", { exact: true })).toBeVisible();
  await page.getByLabel("Add a note").fill("Second account with the same ID number found; checking.");
  await page.getByRole("button", { name: "Add note" }).click();
  await expect(page.getByText("Second account with the same ID number found; checking.")).toBeVisible();
  await ccActions.getByRole("button", { name: "Propose resolution" }).click();
  const resolve = page.getByRole("dialog", { name: "Propose resolution" });
  await resolve.getByLabel("Resolution").selectOption("RESTRICT");
  await resolve.getByLabel("Reason").selectOption("CONCERNS_CONFIRMED");
  await resolve.getByLabel("Note").fill("Second account confirmed; restrict pending checker approval.");
  await resolve.getByRole("button", { name: "Confirm: Propose resolution" }).focus();
  await page.keyboard.press("Enter");
  await page.getByRole("dialog", { name: "Confirm it's you" }).getByLabel("Authentication code").fill(TOTP);
  await page.getByRole("button", { name: "Confirm", exact: true }).click();
  await expect(page.getByText(/Resolution proposed: Restrict/)).toBeVisible();
  // Maker-checker: the proposer is not offered the approval.
  await expect(ccActions.getByRole("button", { name: "Approve resolution" })).toHaveCount(0);
});

test("new pages keep the nonce-only CSP with no violations", async ({ page }) => {
  await trackCspViolations(page);
  const problems = watchForBreakage(page);
  const check = async (path: string) => {
    const response = await page.goto(path);
    expect(response?.status(), path).toBe(200);
    const csp = response!.headers()["content-security-policy"] ?? "";
    expect(csp).toContain("'strict-dynamic'");
    expect(csp).not.toContain("unsafe-inline");
    expect(csp).not.toContain("unsafe-eval");
    const nonce = /'nonce-([^']+)'/.exec(csp)?.[1];
    expect(nonce, path).toBeTruthy();
    expect(response!.headers()["cache-control"]).toContain("no-store");
    const html = await response!.text();
    for (const tag of [...html.matchAll(/<script\b[^>]*>/g)].map((m) => m[0])) expect(tag, `${path}: ${tag}`).toContain(`nonce="${nonce}"`);
    expect(html, `${path} inline style attribute`).not.toMatch(/<[a-z][^>]*\sstyle="/i);
    await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);
    await page.waitForLoadState("networkidle");
    expect(await cspViolations(page), path).toEqual([]);
  };
  await signInNewUser(page, { kycLevel: "IDENTITY_VERIFIED" });
  for (const path of [
    "/dashboard/verification",
    "/dashboard/verification/identity",
    "/dashboard/verification/documents",
    "/dashboard/verification/beneficiaries",
    "/dashboard/verification/payout-destinations",
    "/dashboard/organisations",
  ]) {
    await check(path);
  }
  // Hydrated client components work under the policy (a dialog opens, a select drives state).
  await page.goto("/dashboard/verification/payout-destinations");
  await page.getByRole("button", { name: "Add a payout account" }).click();
  await page.getByLabel("How would money be received?").selectOption("BANK_TRANSFER");
  await expect(page.getByLabel("Bank or branch code")).toBeVisible();
  expect(await cspViolations(page)).toEqual([]);

  const subjectName = unique("Csp Subject");
  const userEmail = await createUser(page, { phoneVerified: true, displayName: subjectName });
  const caseId = await seedSubmittedKyc(page, userEmail, "Csp");
  await page.context().clearCookies();
  const staff = await createStaff(page, ["KYC_REVIEWER", "COMPLIANCE"]);
  await signInStaff(page, staff);
  for (const path of ["/admin/verification", "/admin/verification/kyc", "/admin/verification/beneficiaries", `/admin/verification/cases/${caseId}`, "/admin/compliance/cases"]) {
    await check(path);
  }
  expect(problems).toEqual([]);
});
