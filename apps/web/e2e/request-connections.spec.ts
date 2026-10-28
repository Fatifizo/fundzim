import { expect, request } from "@playwright/test";

import { postAcrossIdleClose, probeServer } from "../e2e-common/connection-probe";
import { test } from "./helpers";

// G-03: Playwright's request client keeps idle connections, and a Node server closes an idle keep-alive connection at
// keepAliveTimeout + keepAliveTimeoutBuffer (5 s + 1 s for the apps under test). A setup request sent in the moment the
// server closes that connection fails with "socket hang up" / ECONNRESET before the server reads it (CI run
// 40521729530). Test-setup requests therefore use a fresh connection each (helpers.ts `test`, from
// e2e-common/fresh-connections.ts). The probe server is e2e-common/connection-probe.ts.

test("test-setup requests use a fresh connection each, so an idle-connection close cannot hang them up", async ({ page, browser, request: requestFixture }) => {
  const probe = await probeServer();
  const extra = await browser.newContext();
  try {
    // the default context's client, a browser.newContext() client and the built-in `request` fixture
    for (const api of [page.request, extra.request, requestFixture]) {
      probe.seen.length = 0;
      for (let i = 0; i < 3; i++) expect((await api.post(`${probe.url}/setup`, { data: { i } })).status()).toBe(201); // back to back
      const outcomes = await postAcrossIdleClose(api, probe.url, 3);
      expect(outcomes).toEqual(["201", "201", "201"]);
      expect(new Set(probe.seen.map((s) => s.socket)).size, "every request on its own connection").toBe(probe.seen.length);
      expect(probe.seen.every((s) => s.connection === "close")).toBe(true);
    }
  } finally {
    await extra.close();
    await probe.close();
  }
});

test("the browser's own traffic keeps its normal keep-alive connections", async ({ page }) => {
  const probe = await probeServer();
  try {
    for (let i = 0; i < 3; i++) expect((await page.goto(`${probe.url}/page`))?.status()).toBe(200);
    const pages = probe.seen.filter((s) => s.path === "/page");
    expect(pages).toHaveLength(3);
    expect(pages.every((s) => s.connection !== "close")).toBe(true);
    expect(new Set(pages.map((s) => s.socket)).size, "the browser reuses its connection").toBeLessThan(pages.length);
  } finally {
    await probe.close();
  }
});

test("a server failure on a test-setup request is reported once, not retried or hidden", async ({ page }) => {
  const probe = await probeServer();
  try {
    const res = await page.request.post(`${probe.url}/fail`, { data: {} });
    expect(res.status()).toBe(500);
    expect(probe.seen.filter((s) => s.path === "/fail")).toHaveLength(1);
  } finally {
    await probe.close();
  }
});

test("control: a plain request client does reuse idle connections (the condition the fix removes)", async () => {
  const probe = await probeServer();
  const plain = await request.newContext();
  try {
    for (let i = 0; i < 3; i++) expect((await plain.post(`${probe.url}/setup`, { data: { i } })).status()).toBe(201);
    expect(new Set(probe.seen.map((s) => s.socket)).size).toBeLessThan(probe.seen.length);
    // evidence only (timing-dependent, not asserted): the same client sending exactly when the server closes
    const outcomes = await postAcrossIdleClose(plain, probe.url, 3);
    test.info().annotations.push({ type: "G-03 control across the idle close", description: outcomes.join(", ") });
  } finally {
    await plain.dispose();
    await probe.close();
  }
});

// G-03 report §12: route.fetch() is not wrapped by the fixture. It keeps its own keep-alive connection, but it never
// inherits one from the test's setup requests (they close theirs), so a single route.fetch per test (csp.spec.ts) always
// starts on a fresh connection. Repeated route.fetch calls across an idle gap would need their own protection.
test("route.fetch never inherits a pooled connection from the test's setup requests", async ({ page }) => {
  const probe = await probeServer();
  try {
    await page.route("**/routed*", async (route) => route.fulfill({ response: await route.fetch() }));
    expect((await page.request.post(`${probe.url}/setup`, { data: {} })).status()).toBe(201);
    await page.goto(`${probe.url}/routed-1`);
    const setup = probe.seen.find((s) => s.path === "/setup");
    const routed = probe.seen.find((s) => s.path === "/routed-1");
    expect(setup?.connection).toBe("close");
    expect(routed, "the routed request reached the server").toBeDefined();
    expect(routed?.socket, "route.fetch started on its own connection").not.toBe(setup?.socket);
  } finally {
    await probe.close();
  }
});
