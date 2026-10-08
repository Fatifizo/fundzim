import { describe, expect, it } from "vitest";

import { afterLoginPath, loginUrl, safeNextPath } from "./safe-redirect";

describe("safeNextPath (open-redirect protection)", () => {
  it.each(["/dashboard", "/settings/mfa?tab=1", "/settings/sessions#x"])("accepts same-site path %s", (path) => {
    expect(safeNextPath(path)).toBe(path);
  });

  it.each([
    "//evil.example",
    "//evil.example/dashboard",
    "/\\evil.example",
    "\\\\evil.example",
    "https://evil.example/",
    "javascript:alert(1)",
    "dashboard",
    "/ok\nnext",
    "/\tevil",
    "/login",
    "/login/mfa",
    "/register",
    "/api/v1/auth/logout",
    "",
    "/" + "a".repeat(600),
    null,
    42,
  ])("refuses %j", (value) => {
    expect(safeNextPath(value)).toBeNull();
  });

  it("keeps percent-encoded bytes encoded (harmless, still same-site)", () => {
    expect(safeNextPath("/%0d%0aSet-Cookie:x")).toBe("/%0d%0aSet-Cookie:x");
  });

  it("defaults to /dashboard and builds login URLs", () => {
    expect(afterLoginPath("//evil.example")).toBe("/dashboard");
    expect(loginUrl("/settings/mfa")).toBe("/login?next=%2Fsettings%2Fmfa");
    expect(loginUrl("https://evil.example")).toBe("/login");
  });
});
