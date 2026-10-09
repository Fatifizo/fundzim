import { describe, expect, it } from "vitest";

import { ApiError, NetworkError } from "@/lib/api/errors";

import { describeVerificationError, detailFieldLabel, VerificationErrorCode } from "./errors";

const SERVER_TEXT = "server text that must never be shown";

function apiError(code: string, status = 422, details: Array<{ field: string; code: string }> = []) {
  return new ApiError({ code, status, message: SERVER_TEXT, retryable: false, details });
}

describe("describeVerificationError", () => {
  it("maps every documented Stage 5 code to its own copy, never the server's message", () => {
    const seen = new Set<string>();
    for (const code of Object.values(VerificationErrorCode)) {
      const described = describeVerificationError(apiError(code));
      expect(described.message, code).not.toContain(SERVER_TEXT);
      expect(described.message, code).not.toBe("Something went wrong. Please try again.");
      expect(described.code).toBe(code);
      seen.add(described.message);
    }
    expect(seen.size).toBeGreaterThan(15);
  });

  it("SUBMISSION_INCOMPLETE lists each problem with a field label and message", () => {
    const described = describeVerificationError(
      apiError("SUBMISSION_INCOMPLETE", 422, [
        { field: "date_of_birth", code: "UNDERAGE" },
        { field: "documents.ZW_NATIONAL_ID.BACK", code: "DOCUMENT_REQUIRED" },
        { field: "documents.ZW_NATIONAL_ID.FRONT", code: "DOCUMENT_NOT_CLEAN" },
        { field: "legal_last_name", code: "MISSING_FIELD" },
        { field: "id_document_expiry", code: "DOCUMENT_EXPIRED" },
        { field: "id_document_number", code: "INVALID_FORMAT" },
        { field: "x", code: "SOMETHING_NEW" },
      ]),
    );
    expect(described.message).toBe("Some things need attention before you can submit.");
    expect(described.problems.map((p) => p.label)).toEqual([
      "Date of birth",
      "Zimbabwe national ID (back)",
      "Zimbabwe national ID (front)",
      "Surname",
      "ID expiry date",
      "ID document number",
      "X",
    ]);
    expect(described.problems[0]!.message).toMatch(/18 or older/);
    expect(described.problems[2]!.message).toMatch(/security check/);
    expect(described.fieldErrors.legal_last_name).toMatch(/required/);
    expect(described.problems[6]!.message).toBe("This needs attention before you can submit.");
  });

  it("INVALID_ACCOUNT_FORMAT marks the account field", () => {
    expect(describeVerificationError(apiError("INVALID_ACCOUNT_FORMAT")).fieldErrors.account_identifier).toMatch(/not in a valid format/);
  });

  it("falls back to the Stage 4 mapping for shared codes", () => {
    expect(describeVerificationError(apiError("CSRF_TOKEN_INVALID", 403)).message).toMatch(/security check failed/);
    expect(describeVerificationError(apiError("STEP_UP_REQUIRED", 403)).message).toMatch(/confirm it's you/);
    expect(describeVerificationError(new NetworkError("r")).message).toMatch(/couldn't reach/);
    expect(describeVerificationError(apiError("VALIDATION_FAILED", 422, [{ field: "display_name", code: "REQUIRED" }])).fieldErrors.display_name).toBe("This field is required.");
    expect(describeVerificationError(new Error("x"), "Fallback").message).toBe("Fallback");
  });

  it("labels detail fields", () => {
    expect(detailFieldLabel("residential_address.city")).toBe("Town or city");
    expect(detailFieldLabel("documents.BIRTH_CERTIFICATE")).toBe("Birth certificate");
    expect(detailFieldLabel("some_field")).toBe("Some field");
  });
});

describe("backend detail fields", () => {
  it("labels a `documents.A|B|C` alternatives field", () => {
    expect(detailFieldLabel("documents.ZW_NATIONAL_ID|ZW_PASSPORT|FOREIGN_PASSPORT")).toBe("Zimbabwe national ID, Zimbabwe passport or Passport (other country)");
    expect(detailFieldLabel("documents.SELFIE")).toBe("Photo of you holding your ID");
  });
});
