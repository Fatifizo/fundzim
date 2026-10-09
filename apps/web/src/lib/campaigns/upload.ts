import { generateRequestId } from "@/lib/api/client";
import { AbortedError, ApiError, ClientErrorCode, NetworkError, TimeoutError } from "@/lib/api/errors";
import { readCsrfToken } from "@/lib/auth/cookies";

import { isUuid } from "./paths";

/**
 * Campaign photo upload (`POST /campaigns/{id}/media`, multipart, fields BEFORE the file: kind (COVER |
 * GALLERY), position?, alt_text (3–250), depicts_minor ("true" | "false"), then file; internal/campaigns/media). Same rules as the Stage 5 document upload: XMLHttpRequest for progress, same-origin only, CSRF and
 * request-id headers, envelope parsing into ApiError, and never an automatic retry (a timed-out upload has
 * an unknown outcome; the caller re-lists the media). The file goes straight from the <input> into the
 * request: never read into a data: URL, stored or logged. Uploads go only to the API, never to a bucket.
 */
/** The API accepts JPEG and PNG only (it re-encodes and strips metadata). */
export const ACCEPTED_IMAGE_TYPES = ["image/jpeg", "image/png"] as const;
export const IMAGE_ACCEPT_ATTRIBUTE = ".jpg,.jpeg,.png,image/jpeg,image/png";
/** Convenience pre-check mirroring the API's 10 MiB default; the API decides. */
export const MAX_IMAGE_BYTES = 10 * 1024 * 1024;
const UPLOAD_TIMEOUT_MS = 180_000;

export type ImageProblem = "EMPTY_FILE" | "FILE_TOO_LARGE" | "UNSUPPORTED_FILE_TYPE";

export function checkImage(file: File, maxBytes = MAX_IMAGE_BYTES): ImageProblem | null {
  if (file.size === 0) return "EMPTY_FILE";
  if (file.size > maxBytes) return "FILE_TOO_LARGE";
  const type = file.type.toLowerCase();
  const extOk = /\.(jpe?g|png)$/i.test(file.name);
  if (!(ACCEPTED_IMAGE_TYPES as readonly string[]).includes(type) && !(type === "" && extOk)) return "UNSUPPORTED_FILE_TYPE";
  return null;
}

export const IMAGE_PROBLEM_MESSAGES: Record<ImageProblem, string> = {
  EMPTY_FILE: "This file is empty. Choose a different photo.",
  FILE_TOO_LARGE: "This photo is larger than 10 MB. Choose a smaller photo.",
  UNSUPPORTED_FILE_TYPE: "Choose a JPEG or PNG photo.",
};

export interface MediaUploadInput {
  campaignId: string;
  file: File;
  kind: "COVER" | "GALLERY";
  altText: string;
  depictsMinor: boolean;
  position?: number;
}

export interface MediaUploadOptions {
  onProgress?: (loaded: number, total: number) => void;
  signal?: AbortSignal;
  createXhr?: () => XMLHttpRequest;
  csrfToken?: () => string | undefined;
}

function defaultCsrf(): string | undefined {
  return typeof document === "undefined" ? undefined : readCsrfToken(document.cookie);
}

export function uploadCampaignMedia(input: MediaUploadInput, options: MediaUploadOptions = {}): Promise<unknown> {
  const requestId = generateRequestId();
  if (!isUuid(input.campaignId)) return Promise.reject(new Error("invalid campaign id"));
  return new Promise((resolve, reject) => {
    const xhr = options.createXhr ? options.createXhr() : new XMLHttpRequest();
    const form = new FormData();
    form.append("kind", input.kind);
    if (input.position !== undefined) form.append("position", String(input.position));
    form.append("alt_text", input.altText);
    form.append("depicts_minor", input.depictsMinor ? "true" : "false");
    form.append("file", input.file, input.file.name);

    let settled = false;
    const finish = (fn: () => void) => {
      if (settled) return;
      settled = true;
      options.signal?.removeEventListener("abort", onAbort);
      fn();
    };
    const onAbort = () => xhr.abort();

    xhr.open("POST", `/api/v1/campaigns/${input.campaignId}/media`);
    xhr.timeout = UPLOAD_TIMEOUT_MS;
    xhr.setRequestHeader("Accept", "application/json");
    xhr.setRequestHeader("X-Request-ID", requestId);
    const token = (options.csrfToken ?? defaultCsrf)();
    if (token) xhr.setRequestHeader("X-CSRF-Token", token);

    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) options.onProgress?.(event.loaded, event.total);
    };
    xhr.onload = () => {
      const responseId = xhr.getResponseHeader("X-Request-ID") ?? requestId;
      let parsed: { data?: unknown; error?: { code?: unknown; message?: unknown; retryable?: unknown; details?: unknown } } | undefined;
      try {
        parsed = xhr.responseText ? JSON.parse(xhr.responseText) : undefined;
      } catch {
        parsed = undefined;
      }
      if (xhr.status >= 200 && xhr.status < 300 && parsed && "data" in parsed) {
        finish(() => resolve(parsed!.data));
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
