import { expect, test } from "@playwright/test";

import { AUTH_BASE_URL, createUser, PASSWORD, seriousAxeViolations, signIn, TOTP, watchForBreakage } from "./helpers";

// Runs against the app instance wired to the in-memory mock auth API (e2e/mock-api/server.mjs).
test.use({ baseURL: AUTH_BASE_URL });

test("register shows the generic check-your-email message", async ({ page }) => {
  const problems = watchForBreakage(page);
  await page.goto("/register");
  expect(await seriousAxeViolations(page)).toEqual([]);

  // Client-side validation first: errors are linked to their fields.
  await page.getByRole("button", { name: "Create account" }).click();
  const email = page.getByLabel("Email address");
  await expect(email).toHaveAttribute("aria-invalid", "true");
  await expect(email).toBeFocused();

  await email.fill(`new-${Date.now()}@example.test`);
  await page.getByRole("textbox", { name: "Display name" }).fill("Chipo");
  await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.getByLabel(/I accept the terms of use/).check();
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page.locator("main").getByRole("status")).toContainText("Check your email");
  await expect(page.locator("main").getByRole("status")).toBeFocused();
  expect(problems).toEqual([]);
});

test("login success lands on the dashboard; sign out returns to anonymous", async ({ page }) => {
  const problems = watchForBreakage(page);
  await signIn(page, "user@example.test");
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole("heading", { level: 1, name: "Welcome, Tendai Moyo" })).toBeVisible();
  expect(await seriousAxeViolations(page)).toEqual([]);

  const menuButton = page.getByRole("button", { name: /Account menu for Tendai Moyo/ });
  await menuButton.click();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(new RegExp(`${AUTH_BASE_URL}/$`));
  await expect(page.getByRole("banner").getByRole("link", { name: "Sign In" })).toBeVisible();
  expect(problems).toEqual([]);
});

test("wrong password shows one generic error", async ({ page }) => {
  await signIn(page, "user@example.test", "not the password at all");
  await expect(page.locator("main").getByRole("alert")).toHaveText("The email address or password is incorrect.");
  await expect(page).toHaveURL(/\/login$/);
});

test("login with MFA: mfa_required → code → dashboard", async ({ page }) => {
  const problems = watchForBreakage(page);
  await signIn(page, "mfa@example.test", PASSWORD, "/login?next=%2Fsettings%2Fsecurity");
  await expect(page).toHaveURL(/\/login\/mfa\?next=%2Fsettings%2Fsecurity$/);
  expect(await seriousAxeViolations(page)).toEqual([]);

  await page.getByLabel("Authentication code").fill("000000");
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page.locator("main").getByRole("alert")).toHaveText("That code is not valid. Check the code and try again.");

  // Recovery-code option exists and is reversible.
  await page.getByRole("button", { name: "Use a recovery code instead" }).click();
  await expect(page.getByLabel("Recovery code")).toBeFocused();
  await page.getByRole("button", { name: "Use an authentication code instead" }).click();

  await page.getByLabel("Authentication code").fill(TOTP);
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page).toHaveURL(/\/settings\/security$/);
  await expect(page.getByRole("heading", { level: 1, name: "Security" })).toBeVisible();
  expect(problems).toEqual([]);
});

test("protected route without a session redirects to login and back", async ({ page }) => {
  const response = await page.goto("/settings/sessions");
  expect(response?.url()).toMatch(/\/login\?next=%2Fsettings%2Fsessions$/);
  await page.getByLabel("Email address").fill("user@example.test");
  await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).toHaveURL(/\/settings\/sessions$/);
  await expect(page.getByText("This device")).toBeVisible();
});

test("a stale session cookie is checked with the API server-side, not trusted", async ({ page, context }) => {
  await context.addCookies([{ name: "fz_session", value: "forged-or-expired", url: AUTH_BASE_URL }]);
  await page.goto("/dashboard");
  await expect(page).toHaveURL(/\/login\?next=%2Fdashboard$/);
});

test("authenticated pages are private/no-store; signed-in users skip /login", async ({ page }) => {
  await signIn(page, "user@example.test");
  await expect(page).toHaveURL(/\/dashboard$/);
  const res = await page.goto("/dashboard");
  const cacheControl = res?.headers()["cache-control"] ?? "";
  expect(cacheControl).toContain("private");
  expect(cacheControl).toContain("no-store");
  await page.goto("/login");
  await expect(page).toHaveURL(/\/dashboard$/);
});

test("open redirects via ?next= are refused", async ({ page }) => {
  for (const next of ["//evil.example/x", "https://evil.example", "/\\evil.example", "javascript:alert(1)"]) {
    await signIn(page, "user@example.test", PASSWORD, `/login?next=${encodeURIComponent(next)}`);
    await expect(page).toHaveURL(new RegExp(`^${AUTH_BASE_URL}/dashboard$`));
    await page.context().clearCookies();
  }
});

test("forgot-password always answers with the same generic message", async ({ page }) => {
  await page.goto("/forgot-password");
  expect(await seriousAxeViolations(page)).toEqual([]);
  await page.getByLabel("Email address").fill("nobody-here@example.test");
  await page.getByRole("button", { name: "Send reset link" }).click();
  await expect(page.locator("main").getByRole("status")).toContainText("If an account exists for that address");
});

test("reset password reads the token from the fragment and removes it from the URL", async ({ page }) => {
  await page.goto("/reset-password#token=valid-reset-token");
  await expect(page).toHaveURL(/\/reset-password$/);
  expect(await seriousAxeViolations(page)).toEqual([]);
  await page.getByLabel("New password", { exact: true }).fill("a brand new passphrase");
  await page.getByLabel("Confirm new password", { exact: true }).fill("a brand new passphrase");
  await page.getByRole("button", { name: "Set new password" }).click();
  await expect(page.locator("main").getByRole("status")).toContainText("Your password has been changed");
  await expect(page.getByRole("link", { name: "Sign in", exact: true }).last()).toHaveAttribute("href", "/login");
});

test("verify-email: valid token confirms; invalid token is generic with a resend form", async ({ page }) => {
  await page.goto("/verify-email?token=valid-verify-token");
  await expect(page.locator("main").getByRole("status")).toContainText("Email address confirmed");
  await expect(page).toHaveURL(/\/verify-email$/);

  await page.goto("/verify-email?token=nope");
  await expect(page.locator("main").getByRole("alert")).toContainText("invalid or has expired");
  expect(await seriousAxeViolations(page)).toEqual([]);
  await page.getByLabel("Email address").fill("someone@example.test");
  await page.getByRole("button", { name: "Send a new link" }).click();
  await expect(page.locator("main").getByRole("status")).toContainText("Check your email");
});

test("429 on login shows the Retry-After wait (route interception)", async ({ page }) => {
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill({
      status: 429,
      headers: { "Content-Type": "application/json", "Retry-After": "90" },
      body: JSON.stringify({ error: { code: "RATE_LIMITED", message: "x", retryable: true }, meta: { request_id: "r-429" } }),
    }),
  );
  await signIn(page, "user@example.test");
  await expect(page.locator("main").getByRole("alert")).toHaveText("Too many attempts. Please wait 2 minutes and try again.");
});

test("MFA enrolment: step-up → QR + key → confirm → recovery codes shown once", async ({ page }) => {
  const problems = watchForBreakage(page);
  const email = await createUser(page);
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto("/settings/mfa");
  expect(await seriousAxeViolations(page)).toEqual([]);

  await page.getByRole("button", { name: "Set up two-step verification" }).click();
  const dialog = page.getByRole("dialog", { name: "Confirm it's you" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Password", { exact: true })).toBeFocused();
  expect(await seriousAxeViolations(page)).toEqual([]);
  await dialog.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await dialog.getByRole("button", { name: "Confirm" }).click();

  await expect(page.getByRole("img", { name: /QR code for adding FundZim/ })).toBeVisible();
  await expect(page.getByLabel("Set-up key")).toHaveText("JBSW Y3DP EHPK 3PXP");
  expect(await seriousAxeViolations(page)).toEqual([]);
  await page.getByLabel("Code from your authenticator app").fill(TOTP);
  await page.getByRole("button", { name: "Turn on two-step verification" }).click();

  const codes = page.getByRole("list", { name: "Recovery codes" }).getByRole("listitem");
  await expect(codes).toHaveCount(10);
  await expect(page.getByLabel("Set-up key")).toHaveCount(0); // secret gone once confirmed
  await page.getByRole("button", { name: "I have saved my codes" }).click();
  await expect(page.getByText("Status:")).toContainText("On");
  await expect(page.getByRole("list", { name: "Recovery codes" })).toHaveCount(0);
  expect(problems).toEqual([]);
});

test("sessions page lists sessions; logout-all signs out", async ({ page }) => {
  const email = await createUser(page);
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  await page.goto("/settings/sessions");
  expect(await seriousAxeViolations(page)).toEqual([]);
  await page.getByRole("button", { name: "Sign out of all sessions" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.goto("/dashboard");
  await expect(page).toHaveURL(/\/login\?next=%2Fdashboard$/);
});

test("profile and security settings pages are accessible; phone verification flow", async ({ page }) => {
  const email = await createUser(page);
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);

  await page.goto("/settings/profile");
  expect(await seriousAxeViolations(page)).toEqual([]);
  await page.getByRole("textbox", { name: "Display name" }).fill("Renamed Person");
  await page.getByRole("button", { name: "Save display name" }).click();
  await expect(page.locator("main").getByRole("status").filter({ hasText: "Display name saved" })).toBeVisible();

  await page.goto("/settings/security");
  expect(await seriousAxeViolations(page)).toEqual([]);
  await page.getByLabel("Mobile phone number").fill("077 123 4567");
  await page.getByRole("button", { name: "Send confirmation code" }).click();
  await expect(page.getByLabel("Code from SMS")).toBeFocused();
  await page.getByLabel("Code from SMS").fill("135790");
  await page.getByRole("button", { name: "Confirm phone number" }).click();
  await expect(page.locator("main").getByRole("status").filter({ hasText: "Phone number confirmed" })).toBeVisible();
});

test("the web proxy forwards the observed client address, not a spoofed one", async ({ request }) => {
  const res = await request.get("/api/v1/__mock/echo", {
    headers: { "X-Forwarded-For": "6.6.6.6", "X-Real-IP": "7.7.7.7", Forwarded: "for=8.8.8.8", "x-fz-peer-0000": "9.9.9.9" },
  });
  expect(res.status()).toBe(200);
  const { data } = await res.json();
  // WEB_TRUSTED_PROXY_CIDRS is empty for this server: the TCP peer (the test runner) is the client.
  expect(data).toEqual({ x_forwarded_for: "127.0.0.1", x_real_ip: null, forwarded: null, internal_headers: [] });
});

test("unsafe API calls from the browser carry the CSRF token from the cookie", async ({ page }) => {
  const email = await createUser(page);
  await signIn(page, email);
  await expect(page).toHaveURL(/\/dashboard$/);
  const csrf = (await page.context().cookies()).find((c) => c.name === "fz_csrf")?.value;
  expect(csrf).toBeTruthy();
  const sent = page.waitForRequest((req) => req.url().endsWith("/api/v1/me") && req.method() === "PATCH");
  await page.goto("/settings/profile");
  await page.getByRole("textbox", { name: "Display name" }).fill("CSRF Check");
  await page.getByRole("button", { name: "Save display name" }).click();
  expect((await sent).headers()["x-csrf-token"]).toBe(csrf);
  await expect(page.locator("main").getByRole("status").filter({ hasText: "Display name saved" })).toBeVisible();
});

test("staff invitation: token stripped, password + mandatory TOTP, recovery codes once, then sign in", async ({ page }) => {
  const problems = watchForBreakage(page);
  await page.goto("/staff/accept-invitation?token=valid-staff-token");
  await expect(page).toHaveURL(/\/staff\/accept-invitation$/);
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", /noindex/);
  await expect(page.getByText("staff@example.test")).toBeVisible();
  expect(await seriousAxeViolations(page)).toEqual([]);
  await page.getByLabel("New password", { exact: true }).fill("a long staff passphrase");
  await page.getByLabel("Confirm new password", { exact: true }).fill("a long staff passphrase");
  await page.getByLabel("Code from your authenticator app").fill("111111");
  await page.getByRole("button", { name: "Activate staff account" }).click();
  await expect(page.getByLabel("Code from your authenticator app")).toHaveAttribute("aria-invalid", "true");
  await page.getByLabel("Code from your authenticator app").fill(TOTP);
  await page.getByRole("button", { name: "Activate staff account" }).click();
  await expect(page.getByRole("list", { name: "Recovery codes" }).getByRole("listitem")).toHaveCount(10);
  await page.getByRole("button", { name: "I have saved my codes" }).click();
  await expect(page.locator("main").getByRole("link", { name: "Sign in" })).toHaveAttribute("href", "/login");
  await page.goto("/staff/accept-invitation?token=bogus");
  await expect(page.locator("main").getByRole("alert")).toContainText("invalid or has expired");
  expect(problems).toEqual([]);
});
