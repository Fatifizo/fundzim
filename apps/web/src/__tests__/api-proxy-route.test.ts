import type { NextRequest } from "next/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DELETE, GET, POST } from "@/app/api/v1/[...path]/route";

function req(url: string, init?: RequestInit): NextRequest {
  return new Request(url, init) as unknown as NextRequest;
}

describe("/api/v1 runtime proxy", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubEnv("API_BASE_URL", "http://api.internal:8080");
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(console, "error").mockImplementation(() => {});
  });

  afterEach(() => {
    fetchMock.mockReset();
    vi.unstubAllEnvs();
    vi.unstubAllGlobals();
  });

  it("forwards path and query to API_BASE_URL (read at request time) with a request id", async () => {
    fetchMock.mockResolvedValueOnce(Response.json({ data: { status: "ok" }, meta: { request_id: "r" } }));
    const res = await GET(req("http://web.local/api/v1/health?x=1"));
    expect(res.status).toBe(200);
    const [target, init] = fetchMock.mock.calls[0]!;
    expect(String(target)).toBe("http://api.internal:8080/api/v1/health?x=1");
    const headers = new Headers(init?.headers);
    expect(headers.get("x-request-id")).toMatch(/^[0-9a-f-]{36}$/);
    expect(init?.redirect).toBe("manual");
  });

  it("strips hop-by-hop and client-supplied forwarding headers, keeps cookies and valid request ids", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await DELETE(
      req("http://web.local/api/v1/things/1", {
        method: "DELETE",
        headers: {
          "X-Forwarded-For": "6.6.6.6",
          Forwarded: "for=6.6.6.6",
          "X-Real-IP": "6.6.6.6",
          Connection: "keep-alive, x-secret-hop",
          "X-Secret-Hop": "1",
          "Proxy-Authorization": "Basic abc",
          Cookie: "__Host-session=abc",
          "X-Request-ID": "client-req-1",
        },
      }),
    );
    const headers = new Headers(fetchMock.mock.calls[0]![1]?.headers);
    expect(headers.get("x-forwarded-for")).toBeNull();
    expect(headers.get("forwarded")).toBeNull();
    expect(headers.get("x-real-ip")).toBeNull();
    expect(headers.get("x-secret-hop")).toBeNull();
    expect(headers.get("proxy-authorization")).toBeNull();
    expect(headers.get("cookie")).toBe("__Host-session=abc");
    expect(headers.get("x-request-id")).toBe("client-req-1");
  });

  it("forwards a POST body", async () => {
    fetchMock.mockResolvedValueOnce(Response.json({ data: {}, meta: { request_id: "r" } }, { status: 201 }));
    const res = await POST(
      req("http://web.local/api/v1/things", { method: "POST", body: '{"a":1}', headers: { "Content-Type": "application/json" } }),
    );
    expect(res.status).toBe(201);
    const body = fetchMock.mock.calls[0]![1]?.body as Uint8Array;
    expect(new TextDecoder().decode(body)).toBe('{"a":1}');
  });

  it("rejects bodies over 1 MiB with a 413 envelope and never calls the API", async () => {
    const big = "x".repeat(1024 * 1024 + 1);
    const res = await POST(req("http://web.local/api/v1/things", { method: "POST", body: big }));
    expect(res.status).toBe(413);
    expect(await res.json()).toMatchObject({ error: { code: "PAYLOAD_TOO_LARGE", retryable: false } });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("returns a 503 envelope when the API is unreachable; retryable only for GET", async () => {
    fetchMock.mockRejectedValueOnce(new TypeError("fetch failed"));
    const getRes = await GET(req("http://web.local/api/v1/health"));
    expect(getRes.status).toBe(503);
    expect(getRes.headers.get("x-request-id")).toBeTruthy();
    expect(await getRes.json()).toMatchObject({ error: { code: "SERVICE_UNAVAILABLE", retryable: true } });

    fetchMock.mockRejectedValueOnce(new TypeError("fetch failed"));
    const postRes = await POST(req("http://web.local/api/v1/things", { method: "POST", body: "{}" }));
    expect(await postRes.json()).toMatchObject({ error: { code: "SERVICE_UNAVAILABLE", retryable: false } });
  });

  it("strips encoding/length and hop-by-hop headers from the upstream response, passes Set-Cookie", async () => {
    const upstreamHeaders = new Headers({ "Content-Type": "application/json", "Content-Encoding": "gzip", "Keep-Alive": "timeout=5" });
    upstreamHeaders.append("Set-Cookie", "a=1; Path=/");
    upstreamHeaders.append("Set-Cookie", "b=2; Path=/");
    fetchMock.mockResolvedValueOnce(new Response('{"data":{},"meta":{"request_id":"r"}}', { status: 200, headers: upstreamHeaders }));
    const res = await GET(req("http://web.local/api/v1/x"));
    expect(res.headers.get("content-encoding")).toBeNull();
    expect(res.headers.get("keep-alive")).toBeNull();
    expect(res.headers.getSetCookie()).toEqual(["a=1; Path=/", "b=2; Path=/"]);
  });

  it("fails closed with 503 when API_BASE_URL is missing in production", async () => {
    vi.stubEnv("API_BASE_URL", "");
    vi.stubEnv("NODE_ENV", "production");
    const res = await GET(req("http://web.local/api/v1/health"));
    expect(res.status).toBe(503);
    expect(fetchMock).not.toHaveBeenCalled();
  });
});
