import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.E2E_PORT ?? 3100);
const AUTH_PORT = Number(process.env.E2E_AUTH_PORT ?? 3101);
const baseURL = `http://127.0.0.1:${PORT}`;
/** Read by e2e/*.spec.ts for the app instance wired to the mock auth API. */
process.env.E2E_AUTH_BASE_URL = `http://127.0.0.1:${AUTH_PORT}`;

/**
 * E2E runs against the production standalone build (e2e/serve.mjs), two instances of it:
 *  - baseURL (site): API_BASE_URL points at a port where nothing listens, so tests prove pages render and
 *    degrade gracefully without the Go API;
 *  - E2E_AUTH_BASE_URL: API_BASE_URL points at the in-memory mock auth API (e2e/mock-api/server.mjs).
 */
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : "list",
  use: {
    baseURL,
    trace: "retain-on-failure",
  },
  projects: [
    { name: "desktop-chromium", use: { ...devices["Desktop Chrome"] } },
    { name: "mobile-chromium", use: { ...devices["Pixel 7"] } },
  ],
  webServer: {
    command: "node e2e/serve.mjs",
    url: `${baseURL}/healthz`,
    timeout: 300_000,
    reuseExistingServer: false,
    env: {
      E2E_PORT: String(PORT),
      E2E_AUTH_PORT: String(AUTH_PORT),
    },
  },
});
