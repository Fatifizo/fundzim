import { describe, expect, it } from "vitest";

import { buildCsp, generateNonce } from "./csp";

describe("CSP", () => {
  it("production policy is nonce-based and strict", () => {
    const csp = buildCsp({ nonce: "abc123==" });
    expect(csp).toContain("script-src 'self' 'nonce-abc123==' 'strict-dynamic'");
    expect(csp).toContain("style-src 'self' 'nonce-abc123=='");
    expect(csp).not.toMatch(/unsafe-inline|unsafe-eval/);
    for (const d of ["object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "form-action 'self'", "connect-src 'self'"]) expect(csp).toContain(d);
  });

  it("development adds only what next dev needs", () => {
    const csp = buildCsp({ nonce: "n", isDev: true });
    expect(csp).toContain("'unsafe-eval'");
    expect(csp).toContain("style-src 'self' 'unsafe-inline'");
  });

  it("nonces are 128-bit, base64 and unique", () => {
    const a = generateNonce();
    expect(a).toMatch(/^[A-Za-z0-9+/]{22}==$/);
    expect(generateNonce()).not.toBe(a);
  });
});
