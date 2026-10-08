import { describe, expect, it } from "vitest";

import { ApiConfigError, DEV_DEFAULT_API_BASE_URL, resolveApiBaseUrl } from "./config";

describe("resolveApiBaseUrl", () => {
  it("defaults to the local API in development and test", () => {
    expect(resolveApiBaseUrl({ NODE_ENV: "development" })).toBe(DEV_DEFAULT_API_BASE_URL);
    expect(resolveApiBaseUrl({ NODE_ENV: "test" })).toBe(DEV_DEFAULT_API_BASE_URL);
  });

  it("has no default in production", () => {
    expect(() => resolveApiBaseUrl({ NODE_ENV: "production" })).toThrow(ApiConfigError);
    expect(() => resolveApiBaseUrl({ NODE_ENV: "production", API_BASE_URL: "  " })).toThrow(ApiConfigError);
  });

  it("normalises to an origin", () => {
    expect(resolveApiBaseUrl({ NODE_ENV: "production", API_BASE_URL: "http://api:8080/" })).toBe("http://api:8080");
  });

  it.each(["ftp://api", "not a url", "http://user:pw@api:8080", "http://api:8080/v1", "http://api:8080?x=1"])(
    "rejects %s",
    (value) => {
      expect(() => resolveApiBaseUrl({ NODE_ENV: "production", API_BASE_URL: value })).toThrow(ApiConfigError);
    },
  );
});
