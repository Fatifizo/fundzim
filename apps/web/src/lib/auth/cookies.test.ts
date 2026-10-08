import { describe, expect, it } from "vitest";

import { parseCookieString, readCsrfToken } from "./cookies";

describe("CSRF cookie reading", () => {
  it("prefers __Host-fz_csrf, falls back to fz_csrf, ignores others", () => {
    expect(readCsrfToken("a=1; fz_csrf=local; __Host-fz_csrf=secure")).toBe("secure");
    expect(readCsrfToken("fz_csrf=local%2Btoken")).toBe("local+token");
    expect(readCsrfToken("fz_session=abc")).toBeUndefined();
    expect(readCsrfToken("")).toBeUndefined();
  });

  it("first occurrence wins", () => {
    expect(parseCookieString("x=1; x=2").get("x")).toBe("1");
  });
});
