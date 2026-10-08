import { expect, test, type Page } from "@playwright/test";

import { AUTH_BASE_URL, cspViolations, signIn, trackCspViolations, watchForBreakage } from "./helpers";

// Strict nonce-based CSP (src/proxy.ts, src/lib/security/csp.ts) verified against the production build.
test.use({ baseURL: AUTH_BASE_URL });

const PUBLIC_PAGES = ["/", "/about", "/how-it-works", "/explore", "/start", "/terms", "/privacy", "/contact", "/register", "/login", "/login/mfa", "/forgot-password", "/reset-password", "/verify-email", "/staff/accept-invitation", "/does-not-exist"];
const SIGNED_IN_PAGES = ["/dashboard", "/settings/profile", "/settings/security", "/settings/mfa", "/settings/sessions"];

function nonceFrom(csp: string): string {
  const match = /script-src [^;]*'nonce-([A-Za-z0-9+/_=-]+)'/.exec(csp);
  expect(match, `nonce in ${csp}`).not.toBeNull();
  return match![1]!;
}

async function checkPage(page: Page, path: string) {
  const response = await page.goto(path);
  expect(response, path).not.toBeNull();
  const csp = response!.headers()["content-security-policy"] ?? "";
  expect(csp).toContain("'strict-dynamic'");
  expect(csp).not.toContain("unsafe-inline");
  expect(csp).not.toContain("unsafe-eval");
  for (const directive of ["object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "form-action 'self'", "connect-src 'self'"]) {
    expect(csp).toContain(directive);
  }
  const nonce = nonceFrom(csp);
  expect(csp).toContain(`style-src 'self' 'nonce-${nonce}'`);

  // Every script/style element the server sent carries this request's nonce; no inline style attributes.
  const html = await response!.text();
  const scripts = [...html.matchAll(/<script\b[^>]*>/g)].map((m) => m[0]);
  expect(scripts.length, `${path} has scripts`).toBeGreaterThan(0);
  for (const tag of scripts) expect(tag, `${path}: ${tag}`).toContain(`nonce="${nonce}"`);
  for (const tag of [...html.matchAll(/<style\b[^>]*>/g)].map((m) => m[0])) expect(tag).toContain(`nonce="${nonce}"`);
  expect(html, `${path} inline style attribute`).not.toMatch(/<[a-z][^>]*\sstyle="/i);

  // Hydration works under the policy: the header's interactive control responds.
  await page.waitForLoadState("networkidle");
  expect(await cspViolations(page), path).toEqual([]);
  return nonce;
}

test("every public page is served with a per-request nonce and no CSP violations", async ({ page }) => {
  const problems = watchForBreakage(page);
  await trackCspViolations(page);
  const nonces = new Set<string>();
  for (const path of PUBLIC_PAGES) nonces.add(await checkPage(page, path));
  expect(nonces.size).toBe(PUBLIC_PAGES.length); // fresh nonce per response
  expect(problems).toEqual([]);
});

test("signed-in pages: nonce CSP, no violations, hydration works", async ({ page }) => {
  const problems = watchForBreakage(page);
  await trackCspViolations(page);
  await signIn(page, "user@example.test");
  await expect(page).toHaveURL(/\/dashboard$/);
  for (const path of SIGNED_IN_PAGES) await checkPage(page, path);

  // Client components hydrated: the password visibility toggle works.
  await page.goto("/settings/profile");
  const field = page.getByLabel("Current password", { exact: true });
  await expect(field).toHaveAttribute("type", "password");
  await page.getByRole("button", { name: "Show current password" }).click();
  await expect(field).toHaveAttribute("type", "text");
  await expect(page.getByRole("button", { name: "Show current password" })).toHaveAttribute("aria-pressed", "true");
  expect(await cspViolations(page)).toEqual([]);
  expect(problems).toEqual([]);
});

test("an injected inline script without the nonce is blocked", async ({ page }) => {
  await trackCspViolations(page);
  // Simulate an HTML injection: add an un-nonced inline script to the real server response (CSP header kept).
  await page.route("**/login", async (route) => {
    const response = await route.fetch();
    const body = (await response.text()).replace("</body>", "<script>window.__injected = true</script></body>");
    await route.fulfill({ response, body });
  });
  await page.goto("/login");
  await page.waitForLoadState("networkidle");
  expect(await page.evaluate(() => (window as unknown as { __injected?: boolean }).__injected === true)).toBe(false);
  await expect.poll(() => cspViolations(page)).toContainEqual(expect.stringMatching(/^script-src/));
  // The legitimate, nonced framework scripts still ran: the form is interactive.
  await page.getByRole("button", { name: "Show password" }).click();
  await expect(page.getByLabel("Password", { exact: true })).toHaveAttribute("type", "text");
});
