import { afterEach, describe, expect, it } from "vitest";

import { clientIpFromHeaders, isInternalHeader, resolveClientIp } from "./client-ip";
import { formatIp, parseCidr, parseCidrList, parseIp } from "./ip";
import { clearPeerHeaderName, setPeerHeaderName } from "./peer-header";
import { stampPeer } from "./peer-stamp";

const trusted = parseCidrList("10.0.0.0/8, 172.28.0.10/32, fd00::/8");

describe("ip parsing", () => {
  it.each([
    ["192.0.2.1", "192.0.2.1"],
    ["::ffff:192.0.2.1", "192.0.2.1"],
    ["2001:db8::1", "2001:db8::1"],
    ["2001:0db8:0000:0000:0000:0000:0000:0001", "2001:db8::1"],
    ["::1", "::1"],
  ])("parses %s", (input, canonical) => {
    expect(formatIp(parseIp(input)!)).toBe(canonical);
  });

  it.each(["", "1.2.3", "1.2.3.4.5", "256.1.1.1", "01.2.3.4", "1.2.3.4:80", "[::1]", "fe80::1%eth0", "1::2::3", "unknown", "6.6.6.6 x"])(
    "rejects %j",
    (input) => {
      expect(parseIp(input)).toBeNull();
    },
  );

  it("parses CIDRs and refuses bad lists", () => {
    expect(parseCidr("172.28.0.10")?.prefix).toBe(32);
    expect(parseCidr("10.0.0.0/33")).toBeNull();
    expect(() => parseCidrList("10.0.0.0/8, nope")).toThrow(/nope/);
    expect(parseCidrList(undefined)).toEqual([]);
  });
});

describe("resolveClientIp (F-02)", () => {
  it("untrusted peer: the peer is the client, a spoofed X-Forwarded-For is ignored", () => {
    expect(resolveClientIp({ peer: "203.0.113.7", forwardedFor: "6.6.6.6", trusted })).toBe("203.0.113.7");
    expect(resolveClientIp({ peer: "::ffff:203.0.113.7", forwardedFor: "6.6.6.6", trusted: [] })).toBe("203.0.113.7");
  });

  it("trusted peer: right-most untrusted entry wins, trusted hops are skipped", () => {
    expect(resolveClientIp({ peer: "10.1.1.1", forwardedFor: "6.6.6.6, 198.51.100.4, 10.2.2.2", trusted })).toBe("198.51.100.4");
  });

  it("trusted peer: malformed entry stops the walk at the last trusted hop", () => {
    expect(resolveClientIp({ peer: "10.1.1.1", forwardedFor: "198.51.100.4, garbage, 10.2.2.2", trusted })).toBe("10.2.2.2");
    expect(resolveClientIp({ peer: "10.1.1.1", forwardedFor: "1.2.3.4:5678", trusted })).toBe("10.1.1.1");
  });

  it("trusted peer without X-Forwarded-For: the peer itself", () => {
    expect(resolveClientIp({ peer: "10.1.1.1", forwardedFor: null, trusted })).toBe("10.1.1.1");
  });

  it("unknown or malformed peer → null", () => {
    expect(resolveClientIp({ peer: undefined, forwardedFor: "6.6.6.6", trusted })).toBeNull();
    expect(resolveClientIp({ peer: "nope", forwardedFor: "6.6.6.6", trusted })).toBeNull();
  });
});

describe("clientIpFromHeaders + peer stamp", () => {
  afterEach(() => clearPeerHeaderName());

  it("returns null when the stamp is not installed (a client cannot supply the peer)", () => {
    const headers = new Headers({ "x-fz-peer-anything": "9.9.9.9", "x-forwarded-for": "6.6.6.6" });
    expect(clientIpFromHeaders(headers, {})).toBeNull();
  });

  it("uses the stamped peer and the configured trusted CIDRs", () => {
    setPeerHeaderName("x-fz-peer-test");
    const headers = new Headers({ "x-fz-peer-test": "10.9.9.9", "x-forwarded-for": "6.6.6.6, 198.51.100.4" });
    expect(clientIpFromHeaders(headers, {})).toBe("10.9.9.9");
    expect(clientIpFromHeaders(headers, { WEB_TRUSTED_PROXY_CIDRS: "10.0.0.0/8" })).toBe("198.51.100.4");
    expect(clientIpFromHeaders(headers, { WEB_TRUSTED_PROXY_CIDRS: "bogus" })).toBeNull();
  });

  it("stampPeer deletes client-supplied x-fz-peer-* headers and records the socket address", () => {
    const req = { headers: { "x-fz-peer-guess": "9.9.9.9", "x-fz-peer-real": "8.8.8.8", host: "x" } as Record<string, string>, socket: { remoteAddress: "::ffff:127.0.0.1" } };
    stampPeer(req as never, "x-fz-peer-real");
    expect(req.headers).toEqual({ host: "x", "x-fz-peer-real": "::ffff:127.0.0.1" });
    expect(isInternalHeader("X-FZ-PEER-real")).toBe(true);
  });
});
