import type { ApiErrorDetail } from "./types";

/**
 * Error codes produced by the client itself (no API response was available). They are NOT part of
 * the API error catalogue (docs/api/error-model.md) and never come from the server.
 */
export const ClientErrorCode = {
  /** The request never reached the server or the connection failed. */
  NETWORK_ERROR: "NETWORK_ERROR",
  /** No response within the client timeout. Outcome of a non-GET request is UNKNOWN. */
  TIMEOUT: "TIMEOUT",
  /** The caller aborted the request. */
  REQUEST_ABORTED: "REQUEST_ABORTED",
  /** The server answered with something that is not a valid FundZim envelope (e.g. HTML 502). */
  INVALID_RESPONSE: "INVALID_RESPONSE",
} as const;

export type ClientErrorCode = (typeof ClientErrorCode)[keyof typeof ClientErrorCode];

export interface ApiErrorInit {
  code: string;
  message: string;
  /** HTTP status, or 0 when no response was received. */
  status: number;
  requestId?: string;
  retryable: boolean;
  details?: ApiErrorDetail[];
  /** Parsed Retry-After in milliseconds, if the server sent one. */
  retryAfterMs?: number;
  cause?: unknown;
}

/** Structured error for every failed API call. Callers branch on `code`, never on `message`. */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly requestId: string | undefined;
  readonly retryable: boolean;
  readonly details: ApiErrorDetail[];
  readonly retryAfterMs: number | undefined;

  constructor(init: ApiErrorInit) {
    super(init.message, init.cause !== undefined ? { cause: init.cause } : undefined);
    this.name = "ApiError";
    this.code = init.code;
    this.status = init.status;
    this.requestId = init.requestId;
    this.retryable = init.retryable;
    this.details = init.details ?? [];
    this.retryAfterMs = init.retryAfterMs;
  }
}

export class NetworkError extends ApiError {
  constructor(requestId: string | undefined, cause?: unknown) {
    super({
      code: ClientErrorCode.NETWORK_ERROR,
      message: "The FundZim service could not be reached. Check your connection and try again.",
      status: 0,
      requestId,
      retryable: true,
      cause,
    });
    this.name = "NetworkError";
  }
}

export class TimeoutError extends ApiError {
  constructor(requestId: string | undefined, timeoutMs: number) {
    super({
      code: ClientErrorCode.TIMEOUT,
      message: `The FundZim service did not respond within ${timeoutMs} ms.`,
      status: 0,
      requestId,
      retryable: true,
    });
    this.name = "TimeoutError";
  }
}

export class AbortedError extends ApiError {
  constructor(requestId: string | undefined, cause?: unknown) {
    super({
      code: ClientErrorCode.REQUEST_ABORTED,
      message: "The request was cancelled.",
      status: 0,
      requestId,
      retryable: false,
      cause,
    });
    this.name = "AbortedError";
  }
}

export function isApiError(value: unknown): value is ApiError {
  return value instanceof ApiError;
}
