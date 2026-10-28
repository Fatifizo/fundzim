import AxeBuilder from "@axe-core/playwright";
import { test as base, expect, type APIRequestContext, type BrowserContext, type Page } from "@playwright/test";

/**
 * G-03 (docs/governance/evidence/g03-investigation-report.md): Playwright's request client (`page.request`) keeps idle
 * keep-alive connections, and the apps under test close an idle connection after 6 s (Node: keepAliveTimeout 5 s +
 * keepAliveTimeoutBuffer 1 s). A setup request sent in the moment of that close fails with "socket hang up" before the
 * server reads it. Every test-setup API request therefore uses a fresh connection (`Connection: close`; localhost, so the
 * cost is negligible). Only the request client is changed: the browser's own traffic keeps its normal connections, and
 * nothing is retried, so a real server failure still fails the test once.
 */
function freshConnectionPerRequest(api: APIRequestContext) {
  const fetch = api.fetch.bind(api); // post/get/put/patch/delete/head all go through fetch
  api.fetch = (urlOrRequest, options = {}) => fetch(urlOrRequest, { ...options, headers: { ...options.headers, connection: "close" } });
}

const covered = new WeakSet<BrowserContext>();
function withFreshConnections(context: BrowserContext) {
  if (!covered.has(context)) {
    covered.add(context);
    freshConnectionPerRequest(context.request);
  }
  return context;
}

/**
 * Use this `test` in every spec: the default context, every `browser.newContext()` and the built-in `request` fixture
 * get fresh setup connections. (The fixture callback is named `provide`, not `use`: the React hooks lint rule would
 * mistake it for a hook.)
 */
export const test = base.extend({
  context: async ({ context }, provide) => {
    await provide(withFreshConnections(context));
  },
  request: async ({ request }, provide) => {
    freshConnectionPerRequest(request);
    await provide(request);
  },
  browser: [
    async ({ browser }, provide) => {
      const newContext = browser.newContext.bind(browser);
      browser.newContext = async (options) => withFreshConnections(await newContext(options));
      await provide(browser);
    },
    { scope: "worker" },
  ],
});

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
  // after a client-side navigation Next streams the new page's metadata: let the <title> settle before scanning. A page
  // that never gets a title still fails (this wait times out), and axe's document-title rule stays enabled.
  await page.waitForFunction(() => document.title.trim() !== "");
  const results = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"]).analyze();
  return results.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical")
    .map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(" ")) }));
}

let counter = 0;
/** Creates a fresh verified user in the mock API so mutating tests do not interfere with each other. */
export async function createUser(
  page: Page,
  opts: { mfa?: boolean; verified?: boolean; phoneVerified?: boolean; kycLevel?: string; displayName?: string } = {},
) {
  const email = `e2e-${Date.now()}-${process.pid}-${counter++}@example.test`;
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/users`, {
    data: {
      email,
      display_name: opts.displayName ?? "E2E Person",
      mfa_enabled: !!opts.mfa,
      email_verified: opts.verified !== false,
      phone_verified: !!opts.phoneVerified,
      kyc_level: opts.kycLevel,
    },
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

/** Creates a staff account (MFA always on) with the given roles in the mock API. */
export async function createStaff(page: Page, roles: string[], displayName = "Reviewer") {
  const email = `staff-${Date.now()}-${process.pid}-${counter++}@example.test`;
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/staff`, { data: { email, display_name: displayName, roles } });
  expect(res.status()).toBe(201);
  return email;
}

/** Staff sign-in: password, then the authenticator code. */
export async function signInStaff(page: Page, email: string, next = "/admin/verification") {
  await signIn(page, email, PASSWORD, `/login?next=${encodeURIComponent(next)}`);
  // wait until the MFA page has loaded and hydrated: a click before hydration submits the form natively (GET)
  await page.waitForURL(/\/login\/mfa/);
  await page.waitForLoadState("networkidle");
  await page.getByLabel("Authentication code").fill(TOTP);
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page).toHaveURL(new RegExp(`${next.replace(/[/]/g, "\\/")}$`));
}

/** A submitted KYC case (identity + clean ID documents) for an existing mock user; returns the case id. */
export async function seedSubmittedKyc(page: Page, email: string, firstName: string, opts: { fourEyes?: boolean } = {}) {
  const res = await page.request.post(`${AUTH_BASE_URL}/api/v1/__mock/kyc-submitted`, { data: { email, first_name: firstName, last_name: "Ncube", four_eyes: !!opts.fourEyes } });
  expect(res.status()).toBe(201);
  return ((await res.json()) as { data: { case_id: string } }).data.case_id;
}
