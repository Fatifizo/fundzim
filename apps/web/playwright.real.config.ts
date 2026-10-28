import { defineConfig, devices } from "@playwright/test";

/**
 * Real-stack E2E (docs/stage-7/interface-contracts.md §6): Playwright against the RUNNING compose stack —
 * fundzim-web at http://localhost:3000, the Go API, worker, PostgreSQL, Mailpit and ClamAV. No mocks and no
 * webServer: start the stack first (`docker compose up -d --build`, see e2e-real/README.md).
 *
 * localhost, not 127.0.0.1: the API checks Origin against APP_PUBLIC_URL (http://localhost:3000).
 * One worker: the specs share one provisioned dataset and the API's per-IP rate limits.
 */
const baseURL = process.env.FUNDZIM_E2E_WEB_URL ?? "http://localhost:3000";

export default defineConfig({
  testDir: "./e2e-real",
  // Not ./test-results: the mock-API suite (npm run test:e2e) empties that directory when it starts.
  outputDir: "./e2e-real/.state/test-results",
  globalSetup: "./e2e-real/global-setup.ts",
  // Provisioning waits for ClamAV scans and the media pipeline; the specs wait for worker-driven emails.
  globalTimeout: 45 * 60_000,
  timeout: 5 * 60_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? [["list"], ["github"], ["html", { open: "never", outputFolder: "./e2e-real/.state/report" }]] : "list",
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    actionTimeout: 30_000,
    navigationTimeout: 60_000,
  },
  projects: [
    // Staff moderation mutates shared state and runs once (desktop). Listed first so its UI sign-ins are reused
    // by the later projects (support/browser.ts staffPage signs in again when a saved session is gone). No
    // `dependencies`: a staff failure must not hide the public results.
    { name: "staff-desktop", use: { ...devices["Desktop Chrome"] }, testMatch: ["staff-moderation.spec.ts", "staff-ledger.spec.ts", "staff-reconciliation.spec.ts"] },
    {
      name: "desktop-chromium",
      use: { ...devices["Desktop Chrome"] },
      // discovery.spec.ts: Stage 16's real-stack spec, wired in Stage 17 (it had been left out of every project)
      testMatch: ["public.spec.ts", "a11y.spec.ts", "featured.spec.ts", "report.spec.ts", "discovery.spec.ts", "request-connections.spec.ts"],
    },
    { name: "mobile-chromium", use: { ...devices["Pixel 7"] }, testMatch: ["public.spec.ts", "a11y.spec.ts", "request-connections.spec.ts"] },
  ],
});
