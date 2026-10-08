/**
 * Typed fetch wrapper for the FundZim `/api/v1` API.
 *
 * - Base URL: on the server, the API origin from `API_BASE_URL` (see ./server.ts); in the browser,
 *   "" (same-origin `/api/v1/...`, proxied by src/app/api/v1/[...path]/route.ts or the reverse proxy).
 * - Every logical request carries an `X-Request-ID` (caller-supplied or generated). The same id is
 *   reused on automatic retries so all attempts correlate in the API logs.
 * - Timeout per attempt via AbortSignal.timeout, combined with the caller's signal (AbortSignal.any).
 * - Envelope parsing: `{data, meta}` on success; `{error, meta}` on failure → ApiError. Anything else
 *   (HTML 502 from a proxy, empty body, invalid JSON) → ApiError INVALID_RESPONSE with the HTTP status.
 * - Retry policy (safe by construction): ONLY GET and HEAD are ever retried automatically, at most 2
 *   retries, with exponential backoff + jitter, and only on network error, timeout, HTTP 502/503/504
 *   or an error envelope with `retryable: true`. A Retry-After longer than `maxRetryAfterMs` stops
 *   retrying. POST/PUT/PATCH/DELETE are NEVER retried automatically: a timeout on a mutation has an
 *   UNKNOWN outcome (CLAUDE.md rule 9); the caller must re-query state or retry deliberately with the
 *   same Idempotency-Key.
 * - No auth tokens are handled here. Session cookies (Stage 4) are first-party HttpOnly cookies and are
 *   never readable by this code.
 */
import { AbortedError, ApiError, ClientErrorCode, NetworkError, TimeoutError } from "./errors";
import type { ApiErrorBody, ApiMeta, ApiSuccess } from "./types";

export type HttpMethod = "GET" | "HEAD" | "POST" | "PUT" | "PATCH" | "DELETE";

const SAFE_METHODS: ReadonlySet<HttpMethod> = new Set<HttpMethod>(["GET", "HEAD"]);
const RETRYABLE_STATUSES: ReadonlySet<number> = new Set([502, 503, 504]);

/** Hard ceiling on automatic retries regardless of configuration. */
export const MAX_RETRIES_CEILING = 2;
export const DEFAULT_TIMEOUT_MS = 10_000;
// Matches the API (internal/platform/httpx/requestid.go): 8–64 characters, so forwarded IDs are not replaced.
const REQUEST_ID_PATTERN = /^[A-Za-z0-9._:-]{8,64}$/;

export interface ApiClientOptions {
  /** Origin of the API (server) or "" for same-origin relative URLs (browser). No trailing slash. */
  baseUrl: string;
  fetch?: typeof fetch;
  timeoutMs?: number;
  /** Automatic retries for GET/HEAD (0–2). Default 2. */
  maxRetries?: number;
  /** Base delay for exponential backoff. Default 250 ms. */
  backoffBaseMs?: number;
  /** Longest Retry-After the client will wait before retrying. Default 5 s. */
  maxRetryAfterMs?: number;
  /** Injected for tests. */
  sleep?: (ms: number, signal?: AbortSignal) => Promise<void>;
  random?: () => number;
  generateRequestId?: () => string;
}

export interface RequestOptions {
  signal?: AbortSignal;
  headers?: Record<string, string>;
  /** Forward an upstream request id (validated; replaced if malformed). */
  requestId?: string;
  timeoutMs?: number;
  /** Override retries for this call (GET/HEAD only; capped at MAX_RETRIES_CEILING). */
  retries?: number;
  /** JSON-serialisable body (non-GET only). */
  body?: unknown;
  cache?: RequestCache;
}

export interface ApiResult<T> {
  data: T;
  meta: ApiMeta;
  status: number;
  requestId: string;
}

export interface ApiClient {
  request<T>(method: HttpMethod, path: string, options?: RequestOptions): Promise<ApiResult<T>>;
  get<T>(path: string, options?: RequestOptions): Promise<ApiResult<T>>;
  head(path: string, options?: RequestOptions): Promise<ApiResult<undefined>>;
  post<T>(path: string, options?: RequestOptions): Promise<ApiResult<T>>;
  put<T>(path: string, options?: RequestOptions): Promise<ApiResult<T>>;
  patch<T>(path: string, options?: RequestOptions): Promise<ApiResult<T>>;
  delete<T>(path: string, options?: RequestOptions): Promise<ApiResult<T>>;
}

export function generateRequestId(): string {
  return globalThis.crypto.randomUUID();
}

export function isValidRequestId(value: string | null | undefined): value is string {
  return typeof value === "string" && REQUEST_ID_PATTERN.test(value);
}

function defaultSleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(signal.reason);
      return;
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal?.reason);
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

/** Parses Retry-After (delta-seconds or HTTP-date) into milliseconds. */
export function parseRetryAfter(value: string | null, now: number = Date.now()): number | undefined {
  if (value === null || value.trim() === "") return undefined;
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) return parseInt(trimmed, 10) * 1000;
  const date = Date.parse(trimmed);
  if (Number.isNaN(date)) return undefined;
  return Math.max(0, date - now);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isErrorEnvelope(value: unknown): value is ApiErrorBody {
  if (!isRecord(value) || !isRecord(value.error)) return false;
  return typeof value.error.code === "string" && typeof value.error.message === "string";
}

function isSuccessEnvelope(value: unknown): value is ApiSuccess<unknown> {
  return isRecord(value) && "data" in value && isRecord(value.meta);
}

function joinUrl(baseUrl: string, path: string): string {
  if (!path.startsWith("/api/v1/")) {
    throw new Error(`API paths must start with /api/v1/ (got ${path})`);
  }
  return `${baseUrl.replace(/\/+$/, "")}${path}`;
}

export function createApiClient(options: ApiClientOptions): ApiClient {
  const doFetch = options.fetch ?? globalThis.fetch.bind(globalThis);
  const defaultTimeout = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
  const defaultRetries = Math.min(Math.max(options.maxRetries ?? MAX_RETRIES_CEILING, 0), MAX_RETRIES_CEILING);
  const backoffBase = options.backoffBaseMs ?? 250;
  const maxRetryAfter = options.maxRetryAfterMs ?? 5_000;
  const sleep = options.sleep ?? defaultSleep;
  const random = options.random ?? Math.random;
  const newRequestId = options.generateRequestId ?? generateRequestId;

  async function attempt<T>(
    method: HttpMethod,
    url: string,
    requestId: string,
    opts: RequestOptions,
  ): Promise<ApiResult<T>> {
    const timeoutMs = opts.timeoutMs ?? defaultTimeout;
    const timeoutSignal = AbortSignal.timeout(timeoutMs);
    const signal = opts.signal ? AbortSignal.any([opts.signal, timeoutSignal]) : timeoutSignal;

    const headers: Record<string, string> = {
      Accept: "application/json",
      ...opts.headers,
      "X-Request-ID": requestId,
    };
    let body: string | undefined;
    if (opts.body !== undefined) {
      if (SAFE_METHODS.has(method)) throw new Error(`${method} requests cannot have a body`);
      body = JSON.stringify(opts.body);
      headers["Content-Type"] = "application/json";
    }

    let response: Response;
    let text: string;
    try {
      response = await doFetch(url, {
        method,
        headers,
        body,
        signal,
        cache: opts.cache ?? "no-store",
        redirect: "error",
      });
      text = method === "HEAD" ? "" : await response.text();
    } catch (error) {
      if (opts.signal?.aborted) throw new AbortedError(requestId, error);
      if (timeoutSignal.aborted) throw new TimeoutError(requestId, timeoutMs);
      throw new NetworkError(requestId, error);
    }

    const responseRequestId = response.headers.get("X-Request-ID") ?? requestId;
    const retryAfterMs = parseRetryAfter(response.headers.get("Retry-After"));

    let parsed: unknown = undefined;
    let parseFailed = false;
    if (text !== "") {
      try {
        parsed = JSON.parse(text);
      } catch {
        parseFailed = true;
      }
    }

    if (response.ok) {
      if (method === "HEAD" || response.status === 204) {
        return {
          data: undefined as T,
          meta: { request_id: responseRequestId },
          status: response.status,
          requestId: responseRequestId,
        };
      }
      if (!parseFailed && isSuccessEnvelope(parsed)) {
        return {
          data: parsed.data as T,
          meta: parsed.meta,
          status: response.status,
          requestId: parsed.meta.request_id || responseRequestId,
        };
      }
      throw new ApiError({
        code: ClientErrorCode.INVALID_RESPONSE,
        message: "The FundZim service returned an unexpected response.",
        status: response.status,
        requestId: responseRequestId,
        retryable: false,
      });
    }

    if (!parseFailed && isErrorEnvelope(parsed)) {
      throw new ApiError({
        code: parsed.error.code,
        message: parsed.error.message,
        status: response.status,
        requestId: parsed.meta?.request_id || responseRequestId,
        retryable: parsed.error.retryable === true,
        details: Array.isArray(parsed.error.details) ? parsed.error.details : [],
        retryAfterMs,
      });
    }

    throw new ApiError({
      code: ClientErrorCode.INVALID_RESPONSE,
      message: `The FundZim service returned an unexpected response (HTTP ${response.status}).`,
      status: response.status,
      requestId: responseRequestId,
      retryable: RETRYABLE_STATUSES.has(response.status),
      retryAfterMs,
    });
  }

  function shouldRetry(error: unknown): error is ApiError {
    if (!(error instanceof ApiError)) return false;
    if (error instanceof AbortedError) return false;
    if (error instanceof NetworkError || error instanceof TimeoutError) return true;
    return RETRYABLE_STATUSES.has(error.status) || error.retryable;
  }

  async function request<T>(method: HttpMethod, path: string, opts: RequestOptions = {}): Promise<ApiResult<T>> {
    const url = joinUrl(options.baseUrl, path);
    const requestId = isValidRequestId(opts.requestId) ? opts.requestId : newRequestId();
    const retries = SAFE_METHODS.has(method)
      ? Math.min(Math.max(opts.retries ?? defaultRetries, 0), MAX_RETRIES_CEILING)
      : 0; // never retry mutations automatically

    for (let attemptNo = 0; ; attemptNo++) {
      try {
        return await attempt<T>(method, url, requestId, opts);
      } catch (error) {
        if (attemptNo >= retries || !shouldRetry(error)) throw error;
        if (error.retryAfterMs !== undefined && error.retryAfterMs > maxRetryAfter) throw error;
        const exp = backoffBase * 2 ** attemptNo;
        const jittered = exp / 2 + random() * (exp / 2);
        const delay = Math.min(Math.max(jittered, error.retryAfterMs ?? 0), maxRetryAfter);
        try {
          await sleep(delay, opts.signal);
        } catch (abortReason) {
          throw new AbortedError(requestId, abortReason);
        }
      }
    }
  }

  return {
    request,
    get: (path, opts) => request("GET", path, opts),
    head: (path, opts) => request<undefined>("HEAD", path, opts),
    post: (path, opts) => request("POST", path, opts),
    put: (path, opts) => request("PUT", path, opts),
    patch: (path, opts) => request("PATCH", path, opts),
    delete: (path, opts) => request("DELETE", path, opts),
  };
}

/** Browser/same-origin client. Safe to import from client components: holds no configuration secrets. */
export const browserApi: ApiClient = createApiClient({ baseUrl: "" });
