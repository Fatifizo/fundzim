/**
 * Same-origin proxy: /api/v1/* → ${API_BASE_URL}/api/v1/* (read at REQUEST time).
 *
 * Why a Route Handler and not `rewrites()`: next.config.ts is evaluated at `next build` and serialised
 * into the standalone server, so a rewrite destination would bake in the build-time API_BASE_URL. This
 * handler reads the env per request, so one image runs in every environment. In deployed environments
 * the reverse proxy may route /api/v1 straight to the API instead (ARCHITECTURE §3); this handler is then
 * simply not hit.
 *
 * Safety:
 *  - Fixed upstream origin (no SSRF): only the path and query of the incoming URL are forwarded.
 *  - Hop-by-hop headers and client-supplied forwarding headers are stripped (the API must not trust a
 *    browser-supplied X-Forwarded-For); `Host` is set by fetch for the upstream.
 *  - Client address (security review F-02 / KI-04): `X-Forwarded-For` is REPLACED by exactly one address,
 *    the client this process actually observed (src/lib/net/client-ip.ts): the TCP peer, or — only when
 *    the peer is in WEB_TRUSTED_PROXY_CIDRS — the right-most untrusted X-Forwarded-For entry. If the peer
 *    is unknown, no X-Forwarded-For is sent (the API then sees the web container; coarse, not spoofable).
 *    The API trusts X-Forwarded-For only from the web container's address (TRUSTED_PROXY_CIDRS).
 *  - Internal `x-fz-peer-*` headers never leave this process.
 *  - Request body capped at MAX_BODY_BYTES (413 PAYLOAD_TOO_LARGE), upstream timeout UPSTREAM_TIMEOUT_MS.
 *    Exception (Stage 5): `POST /api/v1/verification/documents` — identity-document uploads go through the
 *    API (ADR-035 §5, never browser-to-bucket) — gets UPLOAD_MAX_BODY_BYTES (the API's 10 MiB default
 *    `UPLOAD_MAX_BYTES` plus multipart overhead) and a longer timeout. Only that exact method and path; the
 *    API applies its own per-purpose cap and content checks. Stage 6 adds exactly one more upload route,
 *    `POST /api/v1/campaigns/{uuid}/media` (campaign photos); the allow-list is src/lib/api/upload-routes.ts.
 *  - Never retries: a forwarded mutation with an unknown outcome is reported, not replayed.
 *  - Redirects are passed through (redirect: "manual"), not followed.
 */
import type { NextRequest } from "next/server";

import { generateRequestId, isValidRequestId } from "@/lib/api/client";
import { ApiConfigError, resolveApiBaseUrl } from "@/lib/api/config";
import { isUploadRequest } from "@/lib/api/upload-routes";
import { clientIpFromHeaders, isInternalHeader } from "@/lib/net/client-ip";

const MAX_BODY_BYTES = 1024 * 1024; // 1 MiB for JSON requests
const UPSTREAM_TIMEOUT_MS = 30_000;
const UPLOAD_MAX_BODY_BYTES = 10 * 1024 * 1024 + 256 * 1024;
const UPLOAD_TIMEOUT_MS = 180_000;

function isUpload(method: string, pathname: string): boolean {
  return isUploadRequest(method, pathname);
}

const HOP_BY_HOP = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "proxy-connection",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "host",
  "content-length",
]);

const STRIP_FROM_CLIENT = new Set([
  "forwarded",
  "x-forwarded-for",
  "x-forwarded-host",
  "x-forwarded-port",
  "x-forwarded-proto",
  "x-real-ip",
]);

// fetch() transparently decompresses, so upstream encoding/length headers no longer describe the body.
const STRIP_FROM_UPSTREAM = new Set(["content-encoding", "content-length"]);

function connectionTokens(headers: Headers): Set<string> {
  const value = headers.get("connection");
  if (!value) return new Set();
  return new Set(value.split(",").map((token) => token.trim().toLowerCase()).filter(Boolean));
}

function errorResponse(status: number, code: string, message: string, retryable: boolean, requestId: string) {
  return Response.json(
    { error: { code, message, retryable }, meta: { request_id: requestId } },
    {
      status,
      headers: {
        "X-Request-ID": requestId,
        "Cache-Control": "no-store",
        ...(status === 503 ? { "Retry-After": "5" } : {}),
      },
    },
  );
}

class BodyTooLargeError extends Error {}

async function readBodyCapped(request: NextRequest, maxBytes: number): Promise<Uint8Array | undefined> {
  if (request.method === "GET" || request.method === "HEAD" || request.body === null) return undefined;
  const declared = request.headers.get("content-length");
  if (declared !== null && /^\d+$/.test(declared) && parseInt(declared, 10) > maxBytes) {
    throw new BodyTooLargeError();
  }
  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > maxBytes) {
      await reader.cancel();
      throw new BodyTooLargeError();
    }
    chunks.push(value);
  }
  const body = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    body.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return body;
}

async function forward(request: NextRequest): Promise<Response> {
  const incomingId = request.headers.get("x-request-id");
  const requestId = isValidRequestId(incomingId) ? incomingId : generateRequestId();

  let baseUrl: string;
  try {
    baseUrl = resolveApiBaseUrl(process.env);
  } catch (error) {
    if (error instanceof ApiConfigError) {
      console.error(JSON.stringify({ level: "error", msg: "api proxy misconfigured", request_id: requestId }));
      return errorResponse(503, "SERVICE_UNAVAILABLE", "The service is temporarily unavailable.", true, requestId);
    }
    throw error;
  }

  const incoming = new URL(request.url);
  if (!incoming.pathname.startsWith("/api/v1/")) {
    return errorResponse(404, "ROUTE_NOT_FOUND", "No such route.", false, requestId);
  }
  const target = new URL(`${incoming.pathname}${incoming.search}`, baseUrl);

  const dropped = connectionTokens(request.headers);
  const headers = new Headers();
  request.headers.forEach((value, key) => {
    const name = key.toLowerCase();
    if (HOP_BY_HOP.has(name) || STRIP_FROM_CLIENT.has(name) || dropped.has(name) || isInternalHeader(name)) return;
    headers.append(key, value);
  });
  headers.set("X-Request-ID", requestId);
  const clientIp = clientIpFromHeaders(request.headers);
  if (clientIp) headers.set("X-Forwarded-For", clientIp);

  const upload = isUpload(request.method, incoming.pathname);
  let body: Uint8Array | undefined;
  try {
    body = await readBodyCapped(request, upload ? UPLOAD_MAX_BODY_BYTES : MAX_BODY_BYTES);
  } catch (error) {
    if (error instanceof BodyTooLargeError) {
      return errorResponse(413, "PAYLOAD_TOO_LARGE", "The request body is too large.", false, requestId);
    }
    throw error;
  }

  const timeout = AbortSignal.timeout(upload ? UPLOAD_TIMEOUT_MS : UPSTREAM_TIMEOUT_MS);
  let upstream: Response;
  try {
    upstream = await fetch(target, {
      method: request.method,
      headers,
      body: body as BodyInit | undefined,
      redirect: "manual",
      cache: "no-store",
      signal: AbortSignal.any([request.signal, timeout]),
    });
  } catch {
    if (request.signal.aborted) {
      // Client went away; nothing useful to send.
      return new Response(null, { status: 499 });
    }
    const timedOut = timeout.aborted;
    console.error(
      JSON.stringify({
        level: "warn",
        msg: timedOut ? "api upstream timeout" : "api upstream unreachable",
        request_id: requestId,
        method: request.method,
        path: incoming.pathname,
      }),
    );
    const safeMethod = request.method === "GET" || request.method === "HEAD";
    return errorResponse(
      timedOut ? 504 : 503,
      "SERVICE_UNAVAILABLE",
      timedOut ? "The service did not respond in time." : "The service is temporarily unavailable.",
      // A failed mutation may still have been applied upstream (outcome UNKNOWN): never mark it retryable.
      safeMethod,
      requestId,
    );
  }

  const upstreamDropped = connectionTokens(upstream.headers);
  const responseHeaders = new Headers();
  upstream.headers.forEach((value, key) => {
    const name = key.toLowerCase();
    if (HOP_BY_HOP.has(name) || STRIP_FROM_UPSTREAM.has(name) || upstreamDropped.has(name)) return;
    responseHeaders.append(key, value);
  });
  if (!responseHeaders.has("x-request-id")) responseHeaders.set("X-Request-ID", requestId);

  return new Response(request.method === "HEAD" ? null : upstream.body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers: responseHeaders,
  });
}

export const GET = forward;
export const HEAD = forward;
export const POST = forward;
export const PUT = forward;
export const PATCH = forward;
export const DELETE = forward;
export const OPTIONS = forward;
