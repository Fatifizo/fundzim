import { expect } from "@playwright/test";

import { postAcrossIdleClose, probeServer } from "../e2e-common/connection-probe";
import { test } from "../e2e-common/fresh-connections";
import { WEB_URL } from "./support/env";

// G-03 (docs/governance/evidence/g03-investigation-report.md §12): the compose `fundzim-web` container closes an idle
// connection at 6 s like the mock-suite apps, and Playwright's request client hangs up there when it reuses one. The
// real-stack specs therefore use the shared fixture (e2e-common/fresh-connections.ts). These tests check that wiring with
// the suite's own `test`, against the in-process probe (deterministic) and against the real web container (reachability).

test("real-stack test API clients use a fresh connection each: default context, extra and storageState contexts, request fixture", async ({
  page,
  browser,
  request: requestFixture,
}) => {
  const probe = await probeServer();
  const extra = await browser.newContext();
  const fromState = await browser.newContext({ storageState: { cookies: [], origins: [] } }); // as support/browser.ts signs staff in
  try {
    for (const api of [page.request, extra.request, fromState.request, requestFixture]) {
      probe.seen.length = 0;
      for (let i = 0; i < 3; i++) expect((await api.post(`${probe.url}/setup`, { data: { i } })).status()).toBe(201); // back to back
      expect(await postAcrossIdleClose(api, probe.url, 2)).toEqual(["201", "201"]);
      expect(new Set(probe.seen.map((s) => s.socket)).size, "every request on its own connection").toBe(probe.seen.length);
      expect(probe.seen.every((s) => s.connection === "close")).toBe(true);
    }
  } finally {
    await fromState.close();
    await extra.close();
    await probe.close();
  }
});

test("real-stack: a server failure is reported once; the browser's own traffic keeps its connections", async ({ page }) => {
  const probe = await probeServer();
  try {
    expect((await page.request.post(`${probe.url}/fail`, { data: {} })).status()).toBe(500);
    expect(probe.seen.filter((s) => s.path === "/fail")).toHaveLength(1);
    for (let i = 0; i < 3; i++) expect((await page.goto(`${probe.url}/page`))?.status()).toBe(200);
    const pages = probe.seen.filter((s) => s.path === "/page");
    expect(pages.every((s) => s.connection !== "close")).toBe(true);
    expect(new Set(pages.map((s) => s.socket)).size, "the browser reuses its connection").toBeLessThan(pages.length);
  } finally {
    await probe.close();
  }
});

test("real-stack: every wrapped client still reaches fundzim-web", async ({ page, browser, request: requestFixture }) => {
  const extra = await browser.newContext();
  try {
    for (const api of [page.request, extra.request, requestFixture]) expect((await api.get(`${WEB_URL}/healthz`)).status()).toBe(200);
  } finally {
    await extra.close();
  }
});
