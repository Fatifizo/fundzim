import { describe, expect, it, vi } from "vitest";

import { analyticsAllowed, EVENTS_PATH, eventBody, trackEvent } from "./events";

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
    trackEvent({ type: "CAMPAIGN_SHARE_CLICK", slug: "help-chipo-abc123", channel: "WHATSAPP" }, { fetch, navigator: {} });
    expect(fetch).toHaveBeenCalledTimes(1);
    const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe(EVENTS_PATH);
    expect(init).toMatchObject({ method: "POST", credentials: "omit", keepalive: true });
    expect(JSON.parse(String(init.body))).toEqual({ type: "CAMPAIGN_SHARE_CLICK", slug: "help-chipo-abc123", channel: "WHATSAPP" });
  });

  it("sends nothing when Do Not Track or Global Privacy Control is on", () => {
    const fetch = vi.fn();
    trackEvent({ type: "CAMPAIGN_PAGE_VIEW", slug: "x-1" }, { fetch, navigator: { doNotTrack: "1" } });
    trackEvent({ type: "CAMPAIGN_PAGE_VIEW", slug: "x-1" }, { fetch, navigator: { globalPrivacyControl: true } });
    expect(fetch).not.toHaveBeenCalled();
  });

  it("search events never carry the query text", () => {
    const sneaky = { type: "CAMPAIGN_SEARCH", category: "medical", q: "secret words" } as unknown as Parameters<typeof eventBody>[0];
    expect(eventBody(sneaky)).toEqual({ type: "CAMPAIGN_SEARCH", category: "medical" });
    expect(eventBody({ type: "CAMPAIGN_SEARCH" })).toEqual({ type: "CAMPAIGN_SEARCH" });
  });

  it("swallows network failures and synchronous errors", async () => {
    const rejecting = vi.fn(async () => {
      throw new TypeError("offline");
    });
    expect(() => trackEvent({ type: "CATEGORY_VIEW", category: "medical" }, { fetch: rejecting, navigator: {} })).not.toThrow();
    const throwing = vi.fn(() => {
      throw new Error("boom");
    });
    expect(() => trackEvent({ type: "CATEGORY_VIEW", category: "medical" }, { fetch: throwing as unknown as typeof fetch, navigator: {} })).not.toThrow();
    await Promise.resolve();
  });
});
