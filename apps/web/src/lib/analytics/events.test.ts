import { describe, expect, it, vi } from "vitest";

import { analyticsAllowed, EVENTS_PATH, eventBody, rememberSource, takeSource, trackEvent } from "./events";

describe("analytics privacy signals", () => {
  it("is allowed without DNT or GPC", () => {
    expect(analyticsAllowed({ doNotTrack: null })).toBe(true);
    expect(analyticsAllowed({ doNotTrack: "0", globalPrivacyControl: false })).toBe(true);
  });

  it.each([
    [{ doNotTrack: "1" }, undefined],
    [{ doNotTrack: "yes" }, undefined],
    [{ msDoNotTrack: "1" }, undefined],
    [{}, { doNotTrack: "1" }],
    [{ globalPrivacyControl: true }, undefined],
  ])("is refused for %o / window %o", (nav, win) => {
    expect(analyticsAllowed(nav, win)).toBe(false);
  });

  it("is refused without a navigator (server)", () => {
    expect(analyticsAllowed(undefined)).toBe(false);
  });
});

describe("trackEvent", () => {
  it("posts only the documented fields, without cookies, and never throws", () => {
    const fetch = vi.fn(async () => new Response(null, { status: 204 }));
    trackEvent({ type: "SHARE_CLICK", slug: "help-chipo-abc123", channel: "WHATSAPP" }, { fetch, navigator: {} });
    expect(fetch).toHaveBeenCalledTimes(1);
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(EVENTS_PATH);
    expect(init).toMatchObject({ method: "POST", credentials: "omit", keepalive: true });
    expect(JSON.parse(String(init.body))).toEqual({ events: [{ type: "SHARE_CLICK", slug: "help-chipo-abc123", channel: "WHATSAPP" }] });
  });

  it("sends nothing when Do Not Track or Global Privacy Control is on", () => {
    const fetch = vi.fn();
    trackEvent({ type: "CAMPAIGN_VIEW", slug: "x-1" }, { fetch, navigator: { doNotTrack: "1" } });
    trackEvent({ type: "CAMPAIGN_VIEW", slug: "x-1" }, { fetch, navigator: { globalPrivacyControl: true } });
    expect(fetch).not.toHaveBeenCalled();
  });

  it("bodies carry only the documented fields (never query text, URLs or a malformed referral code)", () => {
    const sneaky = { type: "RESULT_CLICK", slug: "a-1", source: "SEARCH", q: "secret words", url: "https://x" } as unknown as Parameters<typeof eventBody>[0];
    expect(eventBody(sneaky)).toEqual({ type: "RESULT_CLICK", slug: "a-1", source: "SEARCH" });
    expect(eventBody({ type: "CAMPAIGN_VIEW", slug: "a-1", source: "DIRECT", ref: "0123456789ab" })).toEqual({ type: "CAMPAIGN_VIEW", slug: "a-1", source: "DIRECT", ref: "0123456789ab" });
    expect(eventBody({ type: "CAMPAIGN_VIEW", slug: "a-1", ref: "../../x?y=1" })).toEqual({ type: "CAMPAIGN_VIEW", slug: "a-1" });
  });

  it("swallows network failures and synchronous errors", async () => {
    const rejecting = vi.fn(async () => {
      throw new TypeError("offline");
    });
    expect(() => trackEvent({ type: "CAMPAIGN_VIEW", slug: "a-1" }, { fetch: rejecting, navigator: {} })).not.toThrow();
    const throwing = vi.fn(() => {
      throw new Error("boom");
    });
    expect(() => trackEvent({ type: "CAMPAIGN_VIEW", slug: "a-1" }, { fetch: throwing as unknown as typeof fetch, navigator: {} })).not.toThrow();
    await Promise.resolve();
  });
});

describe("traffic source hand-off", () => {
  it("returns the remembered surface once, for the same campaign, within 30 minutes", () => {
    rememberSource("a-1", "TRENDING", 1_000);
    expect(takeSource("a-1", "", "https://fundzim.test", 2_000)).toBe("TRENDING");
    expect(takeSource("a-1", "", "https://fundzim.test", 3_000)).toBe("DIRECT"); // consumed
    rememberSource("a-1", "SEARCH", 0);
    expect(takeSource("b-2", "", "https://fundzim.test", 1)).toBe("DIRECT"); // another campaign
    rememberSource("a-1", "SEARCH", 0);
    expect(takeSource("a-1", "", "https://fundzim.test", 31 * 60 * 1000)).toBe("DIRECT"); // stale
  });

  it("falls back to the referrer rule without sending the referrer", () => {
    expect(takeSource("a-1", "https://fundzim.test/campaigns", "https://fundzim.test")).toBe("INTERNAL");
    expect(takeSource("a-1", "https://www.google.com/", "https://fundzim.test")).toBe("UNKNOWN");
    expect(takeSource("a-1", "not a url", "https://fundzim.test")).toBe("UNKNOWN");
  });
});
