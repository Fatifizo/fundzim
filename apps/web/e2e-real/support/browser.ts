import { expect, test, type Browser, type BrowserContext, type Locator, type Page } from "@playwright/test";

import { TEST_PASSWORD } from "./env";
import { ApiSession } from "./http";
import { loadRun, staffByEmail, storageStatePath, type StaffCred } from "./state";
import { totpCode } from "./totp";

/** Browser helpers for the real-stack specs (the UI under test; no API shortcuts for the steps tested). */

/** Signs in through /login: email + password, then the authenticator code on /login/mfa. */
export async function uiStaffLogin(page: Page, c: StaffCred, next: string) {
  await page.goto(`/login?next=${encodeURIComponent(next)}`);
  await page.getByLabel("Email address").fill(c.email);
  await page.getByLabel("Password", { exact: true }).fill(TEST_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/login\/mfa/);
  await expect(page.getByRole("heading", { level: 1, name: "Two-step verification" })).toBeVisible();
  await page.getByLabel("Authentication code").fill(await totpCode(c.secret));
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page).toHaveURL(new RegExp(`${next.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(\\?.*)?$`));
}

/** A browser context signed in as the run's owner (the session provisioned through the API). */
export async function ownerContext(browser: Browser, opts: Parameters<Browser["newContext"]>[0] = {}): Promise<BrowserContext> {
  const run = loadRun();
  const ctx = await browser.newContext(opts);
  await ctx.addCookies(ApiSession.fromJSON(run.owner.session).playwrightCookies());
  return ctx;
}

/** Answers the step-up dialog with a fresh TOTP code if the API asked for one. */
export async function answerStepUpIfAsked(page: Page, c: StaffCred, settle: Locator) {
  const stepUp = page.getByRole("dialog", { name: "Confirm it's you" });
  await expect(stepUp.or(settle).first()).toBeVisible({ timeout: 30_000 });
  if (await stepUp.isVisible()) {
    test.info().annotations.push({ type: "step-up", description: `${c.key}: the API asked for step-up; answered with a TOTP code` });
    await stepUp.getByLabel("Authentication code").fill(await totpCode(c.secret));
    await stepUp.getByRole("button", { name: "Confirm" }).click();
    await expect(stepUp).toHaveCount(0);
  }
}

/**
 * Runs a staff action from the campaign review page: opens the action's dialog, picks the reason, writes
 * the internal note (and the owner message when asked), confirms, and handles step-up when prompted.
 * Resolves once the dialog closed with the "done" notice, or returns the dialog's error text.
 */
export async function staffAction(
  page: Page,
  c: StaffCred,
  label: string,
  opts: { reason: string; note: string; userMessage?: string; expectError?: boolean },
): Promise<string | null> {
  await page.getByRole("button", { name: label, exact: true }).click();
  const dialog = page.getByRole("dialog", { name: label });
  await expect(dialog).toBeVisible();
  await dialog.getByLabel("Reason").selectOption(opts.reason);
  if (opts.userMessage !== undefined) await dialog.getByLabel("Message to the owner").fill(opts.userMessage);
  await dialog.getByLabel("Internal note (staff only)").fill(opts.note);
  await dialog.getByRole("button", { name: `Confirm: ${label}` }).click();
  const done = page.getByRole("status").filter({ hasText: /done\.|First approval recorded/ });
  const formError = dialog.getByRole("alert");
  await answerStepUpIfAsked(page, c, done.or(formError));
  if (opts.expectError) {
    await expect(formError).toBeVisible();
    return (await formError.innerText()).trim();
  }
  await expect(done).toBeVisible();
  await expect(dialog).toHaveCount(0);
  return null;
}

export function staff(email: string): StaffCred {
  return staffByEmail(email);
}

/**
 * Discovery results stream in behind a skeleton (Suspense): waits until the results list or the empty state
 * is rendered, then returns the titles of the campaign cards (fails on the error state).
 */
export async function discoveryTitles(page: Page): Promise<string[]> {
  const list = page.getByRole("list", { name: "Campaigns", exact: true });
  const empty = page.getByText("No campaigns found");
  const failed = page.getByText("Campaigns could not be loaded");
  const loading = page.getByText("Loading campaigns…");
  await expect(loading).toHaveCount(0);
  await expect(list.or(empty).or(failed).first()).toBeVisible();
  await expect(loading).toHaveCount(0);
  await expect(failed).toHaveCount(0);
  if ((await list.count()) === 0) return [];
  return (await list.getByRole("heading").allInnerTexts()).map((t) => t.trim());
}

/**
 * A page signed in as a staff member: reuses the browser session saved by an earlier UI sign-in when it is
 * still alive, otherwise signs in through the login page (password + TOTP) and saves the new session.
 */
export async function staffPage(browser: Browser, c: StaffCred, path: string): Promise<{ ctx: BrowserContext; page: Page }> {
  const file = storageStatePath(c.key);
  let ctx: BrowserContext;
  try {
    ctx = await browser.newContext({ storageState: file });
  } catch {
    ctx = await browser.newContext();
  }
  const page = await ctx.newPage();
  // Staff pages answer 404 (not a /login redirect) to a signed-out visitor, so check the saved session first.
  const me = await page.request.get("/api/v1/me");
  if (me.status() !== 200) {
    await uiStaffLogin(page, c, path);
    await ctx.storageState({ path: file });
  } else {
    await page.goto(path);
  }
  return { ctx, page };
}
