import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/api/errors";

import { describeCampaignError } from "./errors";

const apiError = (status: number, code: string) => new ApiError({ status, code, message: "server text", requestId: "req-12345678", retryable: false });

describe("If-Match errors (ADR-038 §7)", () => {
  it("428 PRECONDITION_REQUIRED asks for a reload", () => {
    expect(describeCampaignError(apiError(428, "PRECONDITION_REQUIRED")).message).toMatch(/Reload the page/);
  });

  it("412 PRECONDITION_FAILED says it changed elsewhere; Stage 6 409 codes are still understood", () => {
    for (const [status, code] of [[412, "PRECONDITION_FAILED"], [409, "CAMPAIGN_STATE_CHANGED"], [409, "VERSION_CONFLICT"]] as const) {
      expect(describeCampaignError(apiError(status, code)).message).toMatch(/changed somewhere else/);
    }
  });

  it("never echoes the server message", () => {
    expect(describeCampaignError(apiError(412, "PRECONDITION_FAILED")).message).not.toContain("server text");
  });
});
