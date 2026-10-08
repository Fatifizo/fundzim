// Starts everything the E2E suite needs (used as Playwright's webServer command):
//   1. production build + standalone assets (skip with E2E_SKIP_BUILD=1 when .next is already current),
//   2. the mock auth API (e2e/mock-api/server.mjs),
//   3. "auth" app server  — API_BASE_URL → mock API (auth flows, CSP, client-IP forwarding),
//   4. "site" app server  — API_BASE_URL → closed port (pages must degrade without the API).
// The site server starts last; Playwright waits on its /healthz, so everything is up when tests begin.
import { spawn, spawnSync } from "node:child_process";

const SITE_PORT = Number(process.env.E2E_PORT ?? 3100);
const AUTH_PORT = Number(process.env.E2E_AUTH_PORT ?? 3101);
const MOCK_PORT = Number(process.env.MOCK_API_PORT ?? 3199);

const children = [];

function run(cmd, args) {
  const result = spawnSync(cmd, args, { stdio: "inherit" });
  if (result.status !== 0) process.exit(result.status ?? 1);
}

function start(name, args, env) {
  const child = spawn(process.execPath, args, { stdio: ["ignore", "inherit", "inherit"], env: { ...process.env, ...env } });
  child.on("exit", (code) => {
    console.error(`[e2e] ${name} exited with ${code}`);
    shutdown(1);
  });
  children.push(child);
  return child;
}

async function waitFor(url, name) {
  for (let i = 0; i < 120; i++) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  console.error(`[e2e] ${name} did not become ready at ${url}`);
  shutdown(1);
}

function shutdown(code = 0) {
  for (const child of children) {
    child.removeAllListeners("exit");
    child.kill("SIGTERM");
  }
  process.exit(code);
}

process.on("SIGINT", () => shutdown(0));
process.on("SIGTERM", () => shutdown(0));

if (process.env.E2E_SKIP_BUILD !== "1") run("npm", ["run", "build"]);
run(process.execPath, ["scripts/prepare-standalone.mjs"]);

const common = { NODE_ENV: "production", HOSTNAME: "127.0.0.1" };

start("mock-api", ["e2e/mock-api/server.mjs"], { MOCK_API_PORT: String(MOCK_PORT), MOCK_APP_ORIGIN: `http://127.0.0.1:${AUTH_PORT}` });
await waitFor(`http://127.0.0.1:${MOCK_PORT}/__mock/health`, "mock-api");

start("auth app", [".next/standalone/server.js"], { ...common, PORT: String(AUTH_PORT), API_BASE_URL: `http://127.0.0.1:${MOCK_PORT}` });
await waitFor(`http://127.0.0.1:${AUTH_PORT}/healthz`, "auth app");

start("site app", [".next/standalone/server.js"], { ...common, PORT: String(SITE_PORT), API_BASE_URL: "http://127.0.0.1:1" });
await waitFor(`http://127.0.0.1:${SITE_PORT}/healthz`, "site app");
console.log("[e2e] all servers ready");
