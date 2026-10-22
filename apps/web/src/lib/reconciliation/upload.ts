import { generateRequestId } from "@/lib/api/client";
import { AbortedError, ApiError, ClientErrorCode, NetworkError, TimeoutError } from "@/lib/api/errors";
import { readCsrfToken } from "@/lib/auth/cookies";

/**
 * TEST statement upload (`POST /admin/reconciliation/imports`, multipart: source, optional supersedes_import_id, then the
 * file; Stage 17). Same rules as the other uploads: same-origin XMLHttpRequest, CSRF and request-id headers, envelope
 * parsing into ApiError, never an automatic retry (a timed-out upload has an unknown outcome — and an identical file is
 * refused as DUPLICATE_IMPORT anyway). The file goes straight from the <input> into the request: it is never read,
 * previewed, stored or logged by the browser. The API hashes, scans and validates it; nothing here is a check of truth.
 */
export const STATEMENT_ACCEPT = ".csv,.json,text/csv,application/json";
/** Convenience pre-check mirroring the API's 10 MiB default; the API decides. */
export const MAX_STATEMENT_BYTES = 10 * 1024 * 1024;
const UPLOAD_TIMEOUT_MS = 180_000;

export type StatementProblem = "EMPTY_FILE" | "FILE_TOO_LARGE" | "UNSUPPORTED_FILE_TYPE";

export const STATEMENT_PROBLEMS: Record<StatementProblem, string> = {
  EMPTY_FILE: "This file is empty.",
  FILE_TOO_LARGE: "This file is larger than 10 MB.",
  UNSUPPORTED_FILE_TYPE: "Choose a .csv or .json file in FundZim's canonical statement format.",
};

/** The media type the API expects for a file (by extension: operating systems label CSV files inconsistently). */
export function statementType(file: File): "text/csv" | "application/json" | null {
  if (/\.json$/i.test(file.name)) return "application/json";
  if (/\.csv$/i.test(file.name)) return "text/csv";
  return null;
}

export function checkStatement(file: File, maxBytes = MAX_STATEMENT_BYTES): StatementProblem | null {
  if (file.size === 0) return "EMPTY_FILE";
  if (file.size > maxBytes) return "FILE_TOO_LARGE";
  if (!statementType(file)) return "UNSUPPORTED_FILE_TYPE";
  return null;
}

export interface StatementUploadInput {
  source: string;
  file: File;
  supersedesImportId?: string;
}

export interface StatementUploadOptions {
  signal?: AbortSignal;
  createXhr?: () => XMLHttpRequest;
  csrfToken?: () => string | undefined;
}

function defaultCsrf(): string | undefined {
  return typeof document === "undefined" ? undefined : readCsrfToken(document.cookie);
}

export function uploadStatement(input: StatementUploadInput, options: StatementUploadOptions = {}): Promise<{ id?: string }> {
  const requestId = generateRequestId();
  const type = statementType(input.file);
  if (!type) return Promise.reject(new Error("unsupported statement file"));
  return new Promise((resolve, reject) => {
    const xhr = options.createXhr ? options.createXhr() : new XMLHttpRequest();
    const form = new FormData();
    form.append("source", input.source);
    if (input.supersedesImportId) form.append("supersedes_import_id", input.supersedesImportId);
    form.append("file", new Blob([input.file], { type }), input.file.name);

    let settled = false;
    const finish = (fn: () => void) => {
      if (settled) return;
      settled = true;
      options.signal?.removeEventListener("abort", onAbort);
      fn();
    };
    const onAbort = () => xhr.abort();

    xhr.open("POST", "/api/v1/admin/reconciliation/imports");
    xhr.timeout = UPLOAD_TIMEOUT_MS;
    xhr.setRequestHeader("Accept", "application/json");
    xhr.setRequestHeader("X-Request-ID", requestId);
    const token = (options.csrfToken ?? defaultCsrf)();
    if (token) xhr.setRequestHeader("X-CSRF-Token", token);

    xhr.onload = () => {
      const responseId = xhr.getResponseHeader("X-Request-ID") ?? requestId;
      let parsed: { data?: unknown; error?: { code?: unknown; message?: unknown; retryable?: unknown; details?: unknown } } | undefined;
      try {
        parsed = xhr.responseText ? JSON.parse(xhr.responseText) : undefined;
      } catch {
        parsed = undefined;
      }
      if (xhr.status >= 200 && xhr.status < 300 && parsed && "data" in parsed) {
        const d = parsed.data;
        finish(() => resolve(typeof d === "object" && d !== null && typeof (d as { id?: unknown }).id === "string" ? { id: (d as { id: string }).id } : {}));
        return;
      }
      const error = parsed?.error;
      finish(() =>
        reject(
          error && typeof error.code === "string"
            ? new ApiError({
                code: error.code,
                message: typeof error.message === "string" ? error.message : "",
                status: xhr.status,
                requestId: responseId,
                retryable: error.retryable === true,
                details: Array.isArray(error.details) ? (error.details as ApiError["details"]) : [],
              })
            : new ApiError({
                code: xhr.status === 413 ? "FILE_TOO_LARGE" : ClientErrorCode.INVALID_RESPONSE,
                message: "Unexpected response",
                status: xhr.status,
                requestId: responseId,
                retryable: false,
              }),
        ),
      );
    };
    xhr.onerror = () => finish(() => reject(new NetworkError(requestId)));
    xhr.ontimeout = () => finish(() => reject(new TimeoutError(requestId, UPLOAD_TIMEOUT_MS)));
    xhr.onabort = () => finish(() => reject(new AbortedError(requestId)));
    if (options.signal) {
      if (options.signal.aborted) {
        finish(() => reject(new AbortedError(requestId)));
        return;
      }
      options.signal.addEventListener("abort", onAbort, { once: true });
    }
    xhr.send(form);
  });
}
