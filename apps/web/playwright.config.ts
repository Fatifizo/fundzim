import { defineConfig, devices } from "@playwright/test";

const PORT = Number(process.env.E2E_PORT ?? 3100);
const baseURL = `http://127.0.0.1:${PORT}`;

/**
 * E2E runs against the production standalone build. API_BASE_URL deliberately points at a port where
 * nothing listens, so these tests prove pages render and degrade gracefully without the Go API.
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
    command: "npm run build && node scripts/prepare-standalone.mjs && node .next/standalone/server.js",
    url: `${baseURL}/healthz`,
    timeout: 240_000,
    reuseExistingServer: false,
    env: {
      PORT: String(PORT),
      HOSTNAME: "127.0.0.1",
      API_BASE_URL: "http://127.0.0.1:1",
      NODE_ENV: "production",
    },
  },
});
