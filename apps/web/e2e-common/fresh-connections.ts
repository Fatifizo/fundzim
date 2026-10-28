import { test as base, type APIRequestContext, type BrowserContext } from "@playwright/test";

/**
 * G-03 (docs/governance/evidence/g03-investigation-report.md), shared by the mock-API suite (e2e/, through helpers.ts)
 * and the real-stack suite (e2e-real/). Playwright's request client keeps idle keep-alive connections, and the web apps
 * under test (Next standalone on Node 24, locally and in the compose `fundzim-web` container) close an idle connection
 * after 6 s (keepAliveTimeout 5 s + keepAliveTimeoutBuffer 1 s). A request sent in the moment of that close fails with
 * "socket hang up" before the server reads it. Every test API request through Playwright's request client therefore
 * uses a fresh connection (`Connection: close`; loopback, so the cost is negligible). Only the request client is
 * changed: the browser's own traffic keeps its normal connections, and nothing is retried, so a real server failure
 * still fails the test once. `route.fetch()` is not wrapped (report §12: it reuses only connections it opened itself).
 */
export function freshConnectionPerRequest(api: APIRequestContext) {
  const fetch = api.fetch.bind(api); // post/get/put/patch/delete/head all go through fetch
  api.fetch = (urlOrRequest, options = {}) => fetch(urlOrRequest, { ...options, headers: { ...options.headers, connection: "close" } });
}

const covered = new WeakSet<BrowserContext>();
export function withFreshConnections(context: BrowserContext) {
  if (!covered.has(context)) {
    covered.add(context);
    freshConnectionPerRequest(context.request);
  }
  return context;
}

/**
 * Use this `test` in every spec (e2e/ imports it through helpers.ts): the default context, every `browser.newContext()`
 * and the built-in `request` fixture get fresh connections. (The fixture callback is named `provide`, not `use`: the
 * React hooks lint rule would mistake it for a hook.)
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
