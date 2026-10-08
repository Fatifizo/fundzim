import { describe, expect, it, vi } from "vitest";

import { createApiClient, MAX_RETRIES_CEILING, parseRetryAfter, type ApiClientOptions } from "./client";
import { AbortedError, ApiError, ClientErrorCode, NetworkError, TimeoutError } from "./errors";

type FetchMock = ReturnType<typeof vi.fn<typeof fetch>>;

function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

function envelope<T>(data: T, requestId = "req-1") {
  return { data, meta: { request_id: requestId } };
}

function errorEnvelope(code: string, retryable: boolean, requestId = "req-err", details?: unknown[]) {
  return { error: { code, message: `msg ${code}`, retryable, ...(details ? { details } : {}) }, meta: { request_id: requestId } };
}

function setup(responses: Array<Response | Error | ((init?: RequestInit) => Promise<Response>)>, opts: Partial<ApiClientOptions> = {}) {
  const fetchMock: FetchMock = vi.fn<typeof fetch>();
  for (const r of responses) {
    if (typeof r === "function") fetchMock.mockImplementationOnce((_input, init) => r(init));
    else if (r instanceof Error) fetchMock.mockRejectedValueOnce(r);
    else fetchMock.mockResolvedValueOnce(r);
  }
  const sleep = vi.fn<(ms: number, signal?: AbortSignal) => Promise<void>>(async () => {});
  const client = createApiClient({
    baseUrl: "http://api.test",
    fetch: fetchMock,
    sleep,
    random: () => 0.5,
    generateRequestId: () => "generated-id",
    ...opts,
  });
  return { client, fetchMock, sleep };
}

function sentHeaders(fetchMock: FetchMock, call = 0): Record<string, string> {
  const init = fetchMock.mock.calls[call]?.[1];
  return (init?.headers ?? {}) as Record<string, string>;
}

describe("envelope parsing", () => {
  it("returns data, meta and request id from a success envelope", async () => {
    const { client, fetchMock } = setup([json(200, envelope({ status: "ok" }, "srv-1"), { "X-Request-ID": "srv-1" })]);
    const result = await client.get<{ status: string }>("/api/v1/health");
    expect(result.data).toEqual({ status: "ok" });
    expect(result.requestId).toBe("srv-1");
    expect(result.status).toBe(200);
    expect(fetchMock.mock.calls[0]?.[0]).toBe("http://api.test/api/v1/health");
  });

  it("joins base URLs without duplicate slashes and same-origin with an empty base", async () => {
    const a = setup([json(200, envelope({}))], { baseUrl: "http://api.test/" });
    await a.client.get("/api/v1/version");
    expect(a.fetchMock.mock.calls[0]?.[0]).toBe("http://api.test/api/v1/version");
    const b = setup([json(200, envelope({}))], { baseUrl: "" });
    await b.client.get("/api/v1/version");
    expect(b.fetchMock.mock.calls[0]?.[0]).toBe("/api/v1/version");
  });

  it("refuses paths outside /api/v1/", async () => {
    const { client } = setup([]);
    await expect(client.get("/admin")).rejects.toThrow(/\/api\/v1\//);
  });

  it("rejects a 200 response that is not an envelope", async () => {
    const { client } = setup([json(200, { status: "ok" })]);
    await expect(client.get("/api/v1/health")).rejects.toMatchObject({
      code: ClientErrorCode.INVALID_RESPONSE,
      status: 200,
    });
  });
});

describe("error mapping", () => {
  it("maps a 404 error envelope to ApiError with code, status, request id and details", async () => {
    const { client } = setup([
      json(404, errorEnvelope("ROUTE_NOT_FOUND", false, "rid-404", [{ field: "x", code: "Y" }]), { "X-Request-ID": "rid-404" }),
    ]);
    const error = await client.get("/api/v1/nope").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({
      code: "ROUTE_NOT_FOUND",
      status: 404,
      requestId: "rid-404",
      retryable: false,
      details: [{ field: "x", code: "Y" }],
    });
  });

  it("maps a non-JSON 502 (e.g. HTML from a proxy) to INVALID_RESPONSE, retryable", async () => {
    const html = () => Promise.resolve(new Response("<html>Bad Gateway</html>", { status: 502, headers: { "Content-Type": "text/html" } }));
    const { client, fetchMock } = setup([html, html, html]);
    const error = await client.get("/api/v1/health").catch((e: unknown) => e);
    expect(error).toMatchObject({ code: ClientErrorCode.INVALID_RESPONSE, status: 502, retryable: true });
    expect(fetchMock).toHaveBeenCalledTimes(3); // 1 + 2 retries
  });

  it("maps fetch rejection to NetworkError", async () => {
    const { client } = setup([new TypeError("fetch failed")], { maxRetries: 0 });
    const error = await client.get("/api/v1/health").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(NetworkError);
    expect(error).toMatchObject({ code: "NETWORK_ERROR", status: 0, requestId: "generated-id" });
  });

  it("maps a timeout to TimeoutError", async () => {
    const hang = (init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => reject(init.signal?.reason));
      });
    const { client } = setup([hang], { maxRetries: 0, timeoutMs: 20 });
    const error = await client.get("/api/v1/health").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(TimeoutError);
    expect(error).toMatchObject({ code: "TIMEOUT" });
  });

  it("maps a caller abort to AbortedError and does not retry", async () => {
    const controller = new AbortController();
    const hang = (init?: RequestInit) =>
      new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener("abort", () => reject(init.signal?.reason));
        controller.abort();
      });
    const { client, fetchMock } = setup([hang]);
    const error = await client.get("/api/v1/health", { signal: controller.signal }).catch((e: unknown) => e);
    expect(error).toBeInstanceOf(AbortedError);
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe("retry policy", () => {
  it("retries GET on 503 and succeeds", async () => {
    const { client, fetchMock, sleep } = setup([
      json(503, errorEnvelope("SERVICE_UNAVAILABLE", true)),
      json(200, envelope({ status: "ready" })),
    ]);
    const result = await client.get("/api/v1/ready");
    expect(result.data).toEqual({ status: "ready" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(sleep).toHaveBeenCalledTimes(1);
  });

  it("retries GET on network error at most MAX_RETRIES_CEILING times", async () => {
    const err = () => Promise.reject(new TypeError("fetch failed"));
    const { client, fetchMock } = setup([err, err, err, err], { maxRetries: 10 });
    await expect(client.get("/api/v1/health", { retries: 10 })).rejects.toBeInstanceOf(NetworkError);
    expect(fetchMock).toHaveBeenCalledTimes(1 + MAX_RETRIES_CEILING);
  });

  it("does not retry GET on non-retryable errors (4xx, retryable=false)", async () => {
    const { client, fetchMock } = setup([json(404, errorEnvelope("ROUTE_NOT_FOUND", false))]);
    await expect(client.get("/api/v1/x")).rejects.toMatchObject({ code: "ROUTE_NOT_FOUND" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("retries GET when the envelope says retryable=true", async () => {
    const { client, fetchMock } = setup([
      json(429, errorEnvelope("RATE_LIMITED", true), { "Retry-After": "1" }),
      json(200, envelope({ ok: true })),
    ]);
    await client.get("/api/v1/x");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("honours Retry-After as the minimum delay", async () => {
    const { client, sleep } = setup([
      json(503, errorEnvelope("SERVICE_UNAVAILABLE", true), { "Retry-After": "2" }),
      json(200, envelope({})),
    ]);
    await client.get("/api/v1/ready");
    expect(sleep.mock.calls[0]?.[0]).toBe(2000);
  });

  it("stops retrying when Retry-After exceeds the cap", async () => {
    const { client, fetchMock } = setup([json(503, errorEnvelope("SERVICE_UNAVAILABLE", true), { "Retry-After": "120" })]);
    await expect(client.get("/api/v1/ready")).rejects.toMatchObject({ code: "SERVICE_UNAVAILABLE", retryAfterMs: 120_000 });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("uses exponential backoff with jitter", async () => {
    const err = () => Promise.reject(new TypeError("fetch failed"));
    const { client, sleep } = setup([err, err, err], { backoffBaseMs: 100, random: () => 1 });
    await expect(client.get("/api/v1/health")).rejects.toBeInstanceOf(NetworkError);
    expect(sleep.mock.calls.map((c) => c[0])).toEqual([100, 200]);
  });

  it.each(["POST", "PUT", "PATCH", "DELETE"] as const)("never retries %s, even on 503/network error/timeout", async (method) => {
    const a = setup([json(503, errorEnvelope("SERVICE_UNAVAILABLE", true)), json(200, envelope({}))]);
    await expect(a.client.request(method, "/api/v1/x", { body: { a: 1 }, retries: 2 })).rejects.toMatchObject({
      code: "SERVICE_UNAVAILABLE",
    });
    expect(a.fetchMock).toHaveBeenCalledTimes(1);

    const b = setup([new TypeError("fetch failed"), json(200, envelope({}))]);
    await expect(b.client.request(method, "/api/v1/x", { retries: 2 })).rejects.toBeInstanceOf(NetworkError);
    expect(b.fetchMock).toHaveBeenCalledTimes(1);
    expect(b.sleep).not.toHaveBeenCalled();
  });

  it("reuses the same X-Request-ID across retries", async () => {
    const { client, fetchMock } = setup([json(503, errorEnvelope("SERVICE_UNAVAILABLE", true)), json(200, envelope({}))]);
    await client.get("/api/v1/ready");
    expect(sentHeaders(fetchMock, 0)["X-Request-ID"]).toBe("generated-id");
    expect(sentHeaders(fetchMock, 1)["X-Request-ID"]).toBe("generated-id");
  });
});

describe("request headers and body", () => {
  it("generates an X-Request-ID when none is given", async () => {
    const { client, fetchMock } = setup([json(200, envelope({}))]);
    await client.get("/api/v1/health");
    expect(sentHeaders(fetchMock)["X-Request-ID"]).toBe("generated-id");
    expect(sentHeaders(fetchMock).Accept).toBe("application/json");
  });

  it("forwards a valid caller request id and replaces a malformed one", async () => {
    const a = setup([json(200, envelope({}))]);
    await a.client.get("/api/v1/health", { requestId: "upstream-123" });
    expect(sentHeaders(a.fetchMock)["X-Request-ID"]).toBe("upstream-123");

    const b = setup([json(200, envelope({}))]);
    await b.client.get("/api/v1/health", { requestId: "bad id\r\ninjected: 1" });
    expect(sentHeaders(b.fetchMock)["X-Request-ID"]).toBe("generated-id");
  });

  it("does not let caller headers override X-Request-ID", async () => {
    const { client, fetchMock } = setup([json(200, envelope({}))]);
    await client.get("/api/v1/health", { headers: { "X-Request-ID": "spoof" } });
    expect(sentHeaders(fetchMock)["X-Request-ID"]).toBe("generated-id");
  });

  it("serialises JSON bodies for mutations and forbids bodies on GET", async () => {
    const { client, fetchMock } = setup([json(201, envelope({ id: "1" }))]);
    await client.post("/api/v1/things", { body: { amount_minor: "100", currency: "USD" } });
    const init = fetchMock.mock.calls[0]?.[1];
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe('{"amount_minor":"100","currency":"USD"}');
    expect(sentHeaders(fetchMock)["Content-Type"]).toBe("application/json");
    await expect(client.get("/api/v1/x", { body: {} })).rejects.toThrow(/cannot have a body/);
  });

  it("handles 204 No Content", async () => {
    const { client } = setup([new Response(null, { status: 204 })]);
    const result = await client.delete("/api/v1/things/1");
    expect(result.status).toBe(204);
    expect(result.data).toBeUndefined();
  });
});

describe("CSRF and credentials", () => {
  it.each(["POST", "PUT", "PATCH", "DELETE"] as const)("sends X-CSRF-Token on %s, and caller headers cannot override it", async (method) => {
    const { client, fetchMock } = setup([json(200, envelope({}))], { csrfToken: () => "tok-123" });
    await client.request(method, "/api/v1/x", { headers: { "x-csrf-token": "attacker" } });
    const headers = sentHeaders(fetchMock);
    expect(headers["X-CSRF-Token"]).toBe("tok-123");
    expect(headers["x-csrf-token"]).toBeUndefined();
    expect(fetchMock.mock.calls[0]?.[1]?.credentials).toBe("same-origin");
  });

  it("never sends the CSRF token on GET/HEAD and omits it when there is no cookie", async () => {
    const a = setup([json(200, envelope({}))], { csrfToken: () => "tok-123" });
    await a.client.get("/api/v1/x");
    expect(sentHeaders(a.fetchMock)["X-CSRF-Token"]).toBeUndefined();
    const b = setup([json(200, envelope({}))], { csrfToken: () => undefined });
    await b.client.post("/api/v1/x");
    expect(sentHeaders(b.fetchMock)["X-CSRF-Token"]).toBeUndefined();
  });
});

describe("parseRetryAfter", () => {
  it("parses seconds and HTTP dates", () => {
    expect(parseRetryAfter("5")).toBe(5000);
    expect(parseRetryAfter(null)).toBeUndefined();
    expect(parseRetryAfter("soon")).toBeUndefined();
    const now = Date.parse("2026-10-08T10:00:00Z");
    expect(parseRetryAfter("Thu, 08 Oct 2026 10:00:03 GMT", now)).toBe(3000);
  });
});
