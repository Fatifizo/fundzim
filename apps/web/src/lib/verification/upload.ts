import { generateRequestId } from "@/lib/api/client";
import { AbortedError, ApiError, ClientErrorCode, NetworkError, TimeoutError } from "@/lib/api/errors";
import { readCsrfToken } from "@/lib/auth/cookies";

import type { SubjectType, VerificationDocument } from "./types";

/**
 * Identity-document upload (contract §7.2 `POST /verification/documents`, multipart). Uses XMLHttpRequest
 * because fetch() cannot report upload progress in browsers. Same rules as the JSON client: same-origin,
 * X-CSRF-Token from the readable CSRF cookie, X-Request-ID, envelope parsing into ApiError, and NEVER an
 * automatic retry (a timed-out upload has an unknown outcome; the caller re-lists the documents).
 *
 * The file goes straight from the <input> to the request body: it is never read into a data: URL, written
 * to browser storage or logged.
 */
export const ACCEPTED_MEDIA_TYPES = ["image/jpeg", "image/png", "application/pdf"] as const;
export const ACCEPT_ATTRIBUTE = ".jpg,.jpeg,.png,.pdf,image/jpeg,image/png,application/pdf";
/** Mirrors the API default (`UPLOAD_MAX_BYTES`, 10 MiB). A convenience pre-check only; the API decides. */
export const MAX_UPLOAD_BYTES = 10 * 1024 * 1024;
const UPLOAD_TIMEOUT_MS = 180_000;

export interface UploadInput {
  file: File;
  subjectType: SubjectType;
  subjectId: string;
  documentType: string;
  side?: string;
}

export interface UploadOptions {
  onProgress?: (loaded: number, total: number) => void;
  signal?: AbortSignal;
  /** Injected for tests. */
  createXhr?: () => XMLHttpRequest;
  csrfToken?: () => string | undefined;
}

export type FileProblem = "EMPTY_FILE" | "FILE_TOO_LARGE" | "UNSUPPORTED_FILE_TYPE";

/** Client-side pre-check of a chosen file (the API re-checks size and sniffs the real type). */
export function checkFile(file: File, maxBytes = MAX_UPLOAD_BYTES): FileProblem | null {
  if (file.size === 0) return "EMPTY_FILE";
  if (file.size > maxBytes) return "FILE_TOO_LARGE";
  const type = file.type.toLowerCase();
  const name = file.name.toLowerCase();
  const typeOk = (ACCEPTED_MEDIA_TYPES as readonly string[]).includes(type);
  const extOk = /\.(jpe?g|png|pdf)$/.test(name);
  if (!typeOk && !(type === "" && extOk)) return "UNSUPPORTED_FILE_TYPE";
  return null;
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  const tenths = Math.round((bytes * 10) / (1024 * 1024));
  return `${Math.trunc(tenths / 10)}.${tenths % 10} MB`;
}

function defaultCsrf(): string | undefined {
  return typeof document === "undefined" ? undefined : readCsrfToken(document.cookie);
}

export function uploadDocument(input: UploadInput, options: UploadOptions = {}): Promise<VerificationDocument> {
  const requestId = generateRequestId();
  return new Promise((resolve, reject) => {
    const xhr = options.createXhr ? options.createXhr() : new XMLHttpRequest();
    const form = new FormData();
    form.append("subject_type", input.subjectType);
    form.append("subject_id", input.subjectId);
    form.append("document_type", input.documentType);
    if (input.side) form.append("side", input.side);
    form.append("file", input.file, input.file.name);

    let settled = false;
    const finish = (fn: () => void) => {
      if (settled) return;
      settled = true;
      options.signal?.removeEventListener("abort", onAbort);
      fn();
    };
    const onAbort = () => xhr.abort();

    xhr.open("POST", "/api/v1/verification/documents");
    xhr.timeout = UPLOAD_TIMEOUT_MS;
    xhr.withCredentials = false; // same-origin: cookies are sent anyway
    xhr.setRequestHeader("Accept", "application/json");
    xhr.setRequestHeader("X-Request-ID", requestId);
    const token = (options.csrfToken ?? defaultCsrf)();
    if (token) xhr.setRequestHeader("X-CSRF-Token", token);

    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) options.onProgress?.(event.loaded, event.total);
    };
    xhr.onload = () => {
      const responseId = xhr.getResponseHeader("X-Request-ID") ?? requestId;
      let parsed: unknown;
      try {
        parsed = xhr.responseText ? JSON.parse(xhr.responseText) : undefined;
      } catch {
        parsed = undefined;
      }
      const envelope = parsed as { data?: VerificationDocument; error?: { code?: unknown; message?: unknown; retryable?: unknown; details?: unknown } } | undefined;
      if (xhr.status >= 200 && xhr.status < 300 && envelope && envelope.data) {
        finish(() => resolve(envelope.data as VerificationDocument));
        return;
      }
      const error = envelope?.error;
      if (error && typeof error.code === "string") {
        finish(() =>
          reject(
            new ApiError({
              code: error.code as string,
              message: typeof error.message === "string" ? error.message : "",
              status: xhr.status,
              requestId: responseId,
              retryable: error.retryable === true,
              details: Array.isArray(error.details) ? (error.details as ApiError["details"]) : [],
            }),
          ),
        );
        return;
      }
      finish(() =>
        reject(
          new ApiError({
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

/**
 * Validates an access-ticket URL from `POST …/access` before the browser is sent to it: it must be a
 * same-origin path to this document's content endpoint. Anything else (absolute URL to another host,
 * protocol-relative, javascript:) is refused, so a compromised or buggy response cannot redirect the user.
 */
export function safeDocumentUrl(url: string, documentId: string): string | null {
  if (typeof url !== "string" || !url.startsWith("/") || url.startsWith("//") || url.includes("\\")) return null;
  let parsed: URL;
  try {
    parsed = new URL(url, "https://fundzim.invalid");
  } catch {
    return null;
  }
  if (parsed.origin !== "https://fundzim.invalid") return null;
  if (parsed.pathname !== `/api/v1/verification/documents/${encodeURIComponent(documentId)}/content`) return null;
  return `${parsed.pathname}${parsed.search}`;
}
