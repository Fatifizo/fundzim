import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";

/** App instance whose API_BASE_URL is the mock auth API (see e2e/serve.mjs). */
export const AUTH_BASE_URL = process.env.E2E_AUTH_BASE_URL ?? `http://127.0.0.1:${process.env.E2E_AUTH_PORT ?? 3101}`;

export const PASSWORD = "correct horse battery staple";
export const TOTP = "246810";

/** Collects CSP violations (console + securitypolicyviolation events) and uncaught page errors. */
export function watchForBreakage(page: Page) {
  const problems: string[] = [];
  page.on("pageerror", (error) => problems.push(`pageerror: ${error.message}`));
  page.on("console", (msg) => {
    if (msg.type() === "error" && /Content Security Policy|Refused to/i.test(msg.text())) problems.push(msg.text());
  });
  return problems;
}

/** Registers a listener in every document for CSP violation events; read with cspViolations(). */
export async function trackCspViolations(page: Page) {
  await page.addInitScript(() => {
    const store: string[] = [];
    (window as unknown as { __cspViolations: string[] }).__cspViolations = store;
    document.addEventListener("securitypolicyviolation", (event) => {
      store.push(`${event.violatedDirective} ${event.blockedURI}`);
    });
  });
}

export async function cspViolations(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { __cspViolations?: string[] }).__cspViolations ?? []);
}

export async function seriousAxeViolations(page: Page) {
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"]).analyze();
  return results.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical")
    .map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(" ")) }));
}

let counter = 0;
/** Creates a fresh verified user in the mock API so mutating tests do not interfere with each other. */
export async function createUser(page: Page, opts: { mfa?: boolean; verified?: boolean } = {}) {
  const email = `e2e-${Date.now()}-${process.pid}-${counter++}@example.test`;
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/users`, {
    data: { email, display_name: "E2E Person", mfa_enabled: !!opts.mfa, email_verified: opts.verified !== false },
  });
  expect(res.status()).toBe(201);
  return email;
}

export async function signIn(page: Page, email: string, password = PASSWORD, path = "/login") {
  await page.goto(`${AUTH_BASE_URL}${path}`);
  await page.getByLabel("Email address").fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}
