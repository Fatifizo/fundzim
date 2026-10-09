import { describe, expect, it } from "vitest";

import { isUploadRequest } from "./upload-routes";

const ID = "0192f0c4-7a1b-7c3d-8e4f-0123456789ab";

describe("isUploadRequest (proxy upload allow-list)", () => {
  it("allows exactly POST to the two upload routes", () => {
    expect(isUploadRequest("POST", "/api/v1/verification/documents")).toBe(true);
    expect(isUploadRequest("POST", `/api/v1/campaigns/${ID}/media`)).toBe(true);
    expect(isUploadRequest("POST", `/api/v1/campaigns/${ID.toUpperCase()}/media`)).toBe(true);
  });

  it("refuses other methods on the same paths", () => {
    for (const method of ["GET", "PUT", "PATCH", "DELETE", "post"]) {
      expect(isUploadRequest(method, `/api/v1/campaigns/${ID}/media`), method).toBe(false);
    }
  });

  it("refuses look-alike paths", () => {
    for (const path of [
      `/api/v1/campaigns/${ID}/media/`,
      `/api/v1/campaigns/${ID}/media/x`,
      `/api/v1/campaigns/${ID}/mediax`,
      `/api/v1/campaigns/${ID}/media/${ID}`,
      `/api/v1/campaigns/${ID}/updates`,
      `/api/v1/campaigns/${ID}`,
      "/api/v1/campaigns/not-a-uuid/media",
      "/api/v1/campaigns/../verification/documents/media",
      `/api/v1/campaigns/${ID}%2F..%2Fx/media`,
      `/api/v1/campaigns/${ID.slice(0, -1)}g/media`,
      `/api/v1/campaigns//${ID}/media`,
      `/api/v1/admin/campaigns/${ID}/media`,
      `/api/v2/campaigns/${ID}/media`,
      `/api/v1/campaigns/${ID}/media?x=1`,
      "/api/v1/campaigns/media",
    ]) {
      expect(isUploadRequest("POST", path), path).toBe(false);
    }
  });
});
