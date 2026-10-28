import http from "node:http";
import type { AddressInfo, Socket } from "node:net";

import { expect, request, type APIRequestContext } from "@playwright/test";

import { test } from "./helpers";

// G-03: Playwright's request client keeps idle connections, and a Node server closes an idle keep-alive connection at
// keepAliveTimeout + keepAliveTimeoutBuffer (5 s + 1 s for the apps under test). A setup request sent in the moment the
// server closes that connection fails with "socket hang up" / ECONNRESET before the server reads it (CI run
// 40521729530). Test-setup requests therefore use a fresh connection each (helpers.ts `test`). This probe server lives in
// the test process with a 2 s idle timeout and no grace buffer, so the close lines up with the request. (At 1 s or less
// the advertised `Keep-Alive: timeout=1` falls within Node's client-side buffer and connections are not reused at all,
// which would hide the condition.)

const IDLE_CLOSE_MS = 2000;

async function probeServer() {
  const seen: { socket: Socket; connection: string | undefined; path: string }[] = [];
  const server = http.createServer((req, res) => {
    req.resume();
    req.on("end", () => {
      seen.push({ socket: req.socket, connection: req.headers.connection, path: req.url ?? "" });
      if (req.url === "/page") {
        res.setHeader("content-type", "text/html");
        res.end("<!doctype html><title>probe</title><p>probe</p>");
        return;
      }
      res.statusCode = req.url === "/fail" ? 500 : 201;
      res.setHeader("content-type", "application/json");
      res.end(JSON.stringify({ ok: res.statusCode === 201 }));
    });
  });
  server.keepAliveTimeout = IDLE_CLOSE_MS;
  server.keepAliveTimeoutBuffer = 0;
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const url = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  return { url, seen, close: () => new Promise<void>((resolve) => server.close(() => resolve())) };
}

const pause = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** Sends `rounds` POSTs, each exactly when the server closes the previous request's idle connection. */
async function postAcrossIdleClose(api: APIRequestContext, url: string, rounds: number) {
  const outcomes: string[] = [];
  for (let i = 0; i < rounds; i++) {
    if (i > 0) await pause(IDLE_CLOSE_MS);
    outcomes.push(await api.post(`${url}/setup`, { data: { i } }).then((r) => String(r.status()), (e: Error) => e.message.split("\n")[0] ?? e.message));
  }
  return outcomes;
}

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
