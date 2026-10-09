import { NextRequest } from "next/server";
import { describe, expect, it } from "vitest";

import { config, proxy } from "@/proxy";

function run(url: string, cookie?: string) {
  return proxy(new NextRequest(url, { headers: cookie ? { cookie } : {} }));
}

describe("proxy.ts", () => {
  it("sets a fresh nonce CSP on the response and the request", () => {
    const a = run("http://web.local/login");
    const b = run("http://web.local/login");
    const csp = a.headers.get("content-security-policy")!;
    expect(csp).toMatch(/script-src 'self' 'nonce-[A-Za-z0-9+/=]+' 'strict-dynamic'/);
    expect(csp).not.toBe(b.headers.get("content-security-policy"));
    // Request header override consumed by Next.js to apply the nonce while rendering.
    expect(a.headers.get("x-middleware-request-content-security-policy")).toBe(csp);
  });

  it("redirects protected paths without a session cookie to /login?next=…", () => {
    const res = run("http://web.local/settings/mfa?x=1");
    expect(res.status).toBe(307);
    expect(res.headers.get("location")).toBe("http://web.local/login?next=%2Fsettings%2Fmfa%3Fx%3D1");
    expect(res.headers.get("cache-control")).toBe("private, no-store");
  });

  it("lets protected paths with a session cookie through, marked private/no-store (the page verifies it)", () => {
    for (const cookie of ["fz_session=abc", "__Host-fz_session=abc"]) {
      const res = run("http://web.local/dashboard", cookie);
      expect(res.status).toBe(200);
      expect(res.headers.get("cache-control")).toBe("private, no-store");
    }
    expect(run("http://web.local/dashboard", "fz_csrf=only").status).toBe(307);
  });

  it("never redirects /admin to login (not discoverable) but marks it private/no-store", () => {
    for (const cookie of [undefined, "fz_session=abc"]) {
      const res = run("http://web.local/admin/verification?type=KYC", cookie);
      expect(res.status).toBe(200);
      expect(res.headers.get("location")).toBeNull();
      expect(res.headers.get("cache-control")).toBe("private, no-store");
    }
    expect(run("http://web.local/administrator").headers.get("cache-control")).toBeNull();
  });

  it("does not run on the API proxy or static assets", () => {
    const re = new RegExp(`^${config.matcher[0]}$`);
    expect(re.test("/api/v1/auth/session")).toBe(false);
    expect(re.test("/_next/static/chunks/a.js")).toBe(false);
    expect(re.test("/healthz")).toBe(false);
    expect(re.test("/dashboard")).toBe(true);
    expect(re.test("/")).toBe(true);
  });
});
