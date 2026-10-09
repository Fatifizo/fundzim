import { describe, expect, it, vi } from "vitest";

import { ApiError } from "@/lib/api/errors";

import { checkFile, formatBytes, MAX_UPLOAD_BYTES, safeDocumentUrl, uploadDocument } from "./upload";

/** Minimal XMLHttpRequest double: records what was sent and lets the test drive progress and completion. */
class FakeXhr {
  method = "";
  url = "";
  headers: Record<string, string> = {};
  body: FormData | null = null;
  status = 0;
  responseText = "";
  timeout = 0;
  withCredentials = false;
  responseHeaders: Record<string, string> = {};
  upload: { onprogress: ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  ontimeout: (() => void) | null = null;
  onabort: (() => void) | null = null;
  aborted = false;
  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(name: string, value: string) {
    this.headers[name] = value;
  }
  getResponseHeader(name: string) {
    return this.responseHeaders[name] ?? null;
  }
  send(body: FormData) {
    this.body = body;
  }
  abort() {
    this.aborted = true;
    this.onabort?.();
  }
  respond(status: number, json: unknown) {
    this.status = status;
    this.responseText = JSON.stringify(json);
    this.onload?.();
  }
}

function file(bytes: number, name = "id.png", type = "image/png") {
  return new File([new Uint8Array(bytes)], name, { type });
}

describe("checkFile", () => {
  it("accepts JPEG/PNG/PDF up to the cap, rejects the rest", () => {
    expect(checkFile(file(10))).toBeNull();
    expect(checkFile(file(10, "a.pdf", "application/pdf"))).toBeNull();
    expect(checkFile(file(10, "scan.JPG", ""))).toBeNull();
    expect(checkFile(file(0))).toBe("EMPTY_FILE");
    expect(checkFile(file(MAX_UPLOAD_BYTES + 1))).toBe("FILE_TOO_LARGE");
    expect(checkFile(file(10, "a.heic", "image/heic"))).toBe("UNSUPPORTED_FILE_TYPE");
    expect(checkFile(file(10, "a.exe", ""))).toBe("UNSUPPORTED_FILE_TYPE");
  });

  it("formats sizes without floating-point surprises", () => {
    expect(formatBytes(512)).toBe("512 B");
    expect(formatBytes(2048)).toBe("2 KB");
    expect(formatBytes(1.5 * 1024 * 1024)).toBe("1.5 MB");
  });
});

describe("uploadDocument", () => {
  it("posts multipart with CSRF + request id, reports progress, resolves the document", async () => {
    const xhr = new FakeXhr();
    const progress = vi.fn();
    const promise = uploadDocument(
      { file: file(100), subjectType: "KYC_CASE", subjectId: "case-1", documentType: "ZW_NATIONAL_ID", side: "FRONT" },
      { createXhr: () => xhr as unknown as XMLHttpRequest, csrfToken: () => "csrf-1", onProgress: progress },
    );
    expect(xhr.method).toBe("POST");
    expect(xhr.url).toBe("/api/v1/verification/documents");
    expect(xhr.headers["X-CSRF-Token"]).toBe("csrf-1");
    expect(xhr.headers["X-Request-ID"]).toMatch(/^[0-9a-f-]{36}$/);
    expect(xhr.body?.get("subject_type")).toBe("KYC_CASE");
    expect(xhr.body?.get("side")).toBe("FRONT");
    expect((xhr.body?.get("file") as File).name).toBe("id.png");
    xhr.upload.onprogress?.({ lengthComputable: true, loaded: 50, total: 100 });
    expect(progress).toHaveBeenCalledWith(50, 100);
    xhr.respond(201, { data: { id: "d1", status: "QUARANTINED" }, meta: { request_id: "r" } });
    await expect(promise).resolves.toMatchObject({ id: "d1", status: "QUARANTINED" });
  });

  it("turns an error envelope into ApiError with details", async () => {
    const xhr = new FakeXhr();
    const promise = uploadDocument({ file: file(10), subjectType: "BENEFICIARY", subjectId: "b", documentType: "OTHER" }, { createXhr: () => xhr as unknown as XMLHttpRequest, csrfToken: () => undefined });
    expect(xhr.headers["X-CSRF-Token"]).toBeUndefined();
    expect(xhr.body?.has("side")).toBe(false);
    xhr.respond(422, { error: { code: "FILE_TYPE_MISMATCH", message: "x", retryable: false }, meta: { request_id: "r" } });
    const error = await promise.catch((e) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error.code).toBe("FILE_TYPE_MISMATCH");
    expect(error.status).toBe(422);
  });

  it("a non-envelope 413 (from a proxy) becomes FILE_TOO_LARGE; abort rejects", async () => {
    const xhr = new FakeXhr();
    const p1 = uploadDocument({ file: file(10), subjectType: "BENEFICIARY", subjectId: "b", documentType: "OTHER" }, { createXhr: () => xhr as unknown as XMLHttpRequest });
    xhr.status = 413;
    xhr.responseText = "<html>too big</html>";
    xhr.onload?.();
    await expect(p1).rejects.toMatchObject({ code: "FILE_TOO_LARGE" });

    const xhr2 = new FakeXhr();
    const controller = new AbortController();
    const p2 = uploadDocument({ file: file(10), subjectType: "BENEFICIARY", subjectId: "b", documentType: "OTHER" }, { createXhr: () => xhr2 as unknown as XMLHttpRequest, signal: controller.signal });
    controller.abort();
    await expect(p2).rejects.toMatchObject({ code: "REQUEST_ABORTED" });
    expect(xhr2.aborted).toBe(true);
  });
});

describe("safeDocumentUrl", () => {
  it("accepts only this document's same-origin content path", () => {
    expect(safeDocumentUrl("/api/v1/verification/documents/d1/content?ticket=abc", "d1")).toBe("/api/v1/verification/documents/d1/content?ticket=abc");
    for (const bad of [
      "https://evil.example/api/v1/verification/documents/d1/content?ticket=abc",
      "//evil.example/api/v1/verification/documents/d1/content",
      "/api/v1/verification/documents/d2/content?ticket=abc",
      "/api/v1/verification/documents/d1/content/../../other",
      "javascript:alert(1)",
      "/\\evil.example",
      "",
    ]) {
      expect(safeDocumentUrl(bad, "d1"), bad).toBeNull();
    }
  });
});
