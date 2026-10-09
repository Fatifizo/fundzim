import { describe, expect, it } from "vitest";

import { checkImage, uploadCampaignMedia } from "./upload";

class FakeXhr {
  url = "";
  method = "";
  headers: Record<string, string> = {};
  body: FormData | null = null;
  status = 0;
  responseText = "";
  timeout = 0;
  upload: { onprogress: ((e: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  ontimeout: (() => void) | null = null;
  onabort: (() => void) | null = null;
  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }
  setRequestHeader(n: string, v: string) {
    this.headers[n] = v;
  }
  getResponseHeader() {
    return null;
  }
  send(body: FormData) {
    this.body = body;
  }
  abort() {
    this.onabort?.();
  }
}

const ID = "0192f0c4-7a1b-7c3d-8e4f-0123456789ab";
const img = (bytes: number, name = "a.png", type = "image/png") => new File([new Uint8Array(bytes)], name, { type });

describe("campaign media upload", () => {
  it("pre-checks images", () => {
    expect(checkImage(img(10))).toBeNull();
    expect(checkImage(img(10, "a.jpg", "image/jpeg"))).toBeNull();
    expect(checkImage(img(10, "a.webp", "image/webp"))).toBe("UNSUPPORTED_FILE_TYPE");
    expect(checkImage(img(0))).toBe("EMPTY_FILE");
    expect(checkImage(img(10, "a.pdf", "application/pdf"))).toBe("UNSUPPORTED_FILE_TYPE");
    expect(checkImage(img(10, "a.svg", "image/svg+xml"))).toBe("UNSUPPORTED_FILE_TYPE");
  });

  it("posts multipart to the campaign's media endpoint with CSRF and resolves the data", async () => {
    const xhr = new FakeXhr();
    const p = uploadCampaignMedia({ campaignId: ID, file: img(5), kind: "COVER", altText: "A school", depictsMinor: false }, { createXhr: () => xhr as unknown as XMLHttpRequest, csrfToken: () => "t" });
    expect(xhr.url).toBe(`/api/v1/campaigns/${ID}/media`);
    expect(xhr.headers["X-CSRF-Token"]).toBe("t");
    expect(xhr.body?.get("kind")).toBe("COVER");
    expect(xhr.body?.get("depicts_minor")).toBe("false");
    expect([...xhr.body!.keys()]).toEqual(["kind", "alt_text", "depicts_minor", "file"]);
    expect(xhr.body?.get("alt_text")).toBe("A school");
    xhr.status = 201;
    xhr.responseText = JSON.stringify({ data: { id: "m1", status: "UPLOADED" }, meta: { request_id: "r" } });
    xhr.onload?.();
    await expect(p).resolves.toEqual({ id: "m1", status: "UPLOADED" });
  });

  it("refuses a malformed campaign id without sending anything", async () => {
    await expect(uploadCampaignMedia({ campaignId: "../x", file: img(5), kind: "COVER", altText: "x", depictsMinor: false })).rejects.toThrow();
  });
});
