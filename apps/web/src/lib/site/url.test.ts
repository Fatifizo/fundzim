import { describe, expect, it } from "vitest";

import { absoluteUrl, campaignPath, indexingAllowed, siteOrigin } from "./url";

describe("siteOrigin", () => {
  it("accepts an origin, with or without a trailing slash", () => {
    expect(siteOrigin({ PUBLIC_SITE_URL: "https://fundzim.test" })?.toString()).toBe("https://fundzim.test/");
    expect(siteOrigin({ PUBLIC_SITE_URL: " https://fundzim.test/ " })?.origin).toBe("https://fundzim.test");
    expect(siteOrigin({ PUBLIC_SITE_URL: "http://127.0.0.1:3101" })?.origin).toBe("http://127.0.0.1:3101");
  });

  it.each([undefined, "", "fundzim.test", "ftp://fundzim.test", "https://user:pw@fundzim.test", "https://fundzim.test/app", "https://fundzim.test/?x=1", "https://fundzim.test/#a", "javascript:alert(1)"])(
    "treats %s as unset",
    (value) => {
      expect(siteOrigin({ PUBLIC_SITE_URL: value })).toBeNull();
    },
  );
});

describe("canonical URLs", () => {
  const origin = new URL("https://fundzim.test");
  it("builds absolute URLs from the configured origin only", () => {
    expect(absoluteUrl(campaignPath("help-chipo-abc123"), origin)).toBe("https://fundzim.test/campaigns/help-chipo-abc123");
    expect(absoluteUrl("/campaigns/x", null)).toBeNull();
  });

  it("refuses protocol-relative or relative paths (no host switching)", () => {
    expect(absoluteUrl("//evil.example/x", origin)).toBeNull();
    expect(absoluteUrl("campaigns/x", origin)).toBeNull();
    expect(absoluteUrl("https://evil.example/", origin)).toBeNull();
  });
});

describe("indexingAllowed", () => {
  it("needs both a valid site URL and SITE_INDEXING=allow", () => {
    expect(indexingAllowed({ PUBLIC_SITE_URL: "https://fundzim.test", SITE_INDEXING: "allow" })).toBe(true);
    expect(indexingAllowed({ PUBLIC_SITE_URL: "https://fundzim.test" })).toBe(false);
    expect(indexingAllowed({ SITE_INDEXING: "allow" })).toBe(false);
    expect(indexingAllowed({ PUBLIC_SITE_URL: "https://fundzim.test", SITE_INDEXING: "yes" })).toBe(false);
  });
});
