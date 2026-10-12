import { readFileSync } from "node:fs";
import { join } from "node:path";
import vm from "node:vm";

import { describe, expect, it } from "vitest";

// public/sw.js is plain script; evaluate it without a service-worker global to reach its policy function.
const source = readFileSync(join(process.cwd(), "public/sw.js"), "utf8");
const { isCacheableStatic } = vm.runInNewContext(`${source};({ isCacheableStatic })`, { URL }) as { isCacheableStatic: (url: string, origin: string, method: string) => boolean };
const ORIGIN = "https://fundzim.test";

describe("service worker cache policy", () => {
  it("caches only same-origin GETs of hashed build assets", () => {
    expect(isCacheableStatic(`${ORIGIN}/_next/static/chunks/app-abc123.js`, ORIGIN, "GET")).toBe(true);
    expect(isCacheableStatic(`${ORIGIN}/_next/static/css/x.css`, ORIGIN, "GET")).toBe(true);
  });

  it.each([
    "/api/v1/public/campaigns/x-1",
    "/api/v1/public/campaigns/x-1/media/00000000-0000-0000-0000-000000000001",
    "/api/v1/verification/documents/x/content",
    "/campaigns/x-1",
    "/dashboard",
    "/admin/campaigns",
    "/login",
    "/settings/security",
    "/",
    "/_next/static/chunks/a.js?v=1",
  ])("never caches %s", (path) => {
    expect(isCacheableStatic(`${ORIGIN}${path}`, ORIGIN, "GET")).toBe(false);
  });

  it("never caches other origins or other methods", () => {
    expect(isCacheableStatic("https://evil.example/_next/static/a.js", ORIGIN, "GET")).toBe(false);
    expect(isCacheableStatic(`${ORIGIN}/_next/static/a.js`, ORIGIN, "POST")).toBe(false);
  });

  it("does not intercept online navigations and never queues requests", () => {
    expect(source).toMatch(/onLine === false/);
    expect(source).not.toMatch(/sync|backgroundFetch|indexedDB|localStorage/i);
  });
});
