import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ReviewCaseView, buildActionBody } from "@/components/admin/review-case";
import { normaliseReviewCase } from "@/lib/verification/normalise";
import { StepUpProvider } from "@/components/auth/step-up";
import { BeneficiariesManager, buildBeneficiaryInput } from "@/components/verification/beneficiaries";
import { DocumentManager } from "@/components/verification/document-manager";
import { KycIdentity, buildKycPatch } from "@/components/verification/kyc-identity";
import { OrganisationVerification, buildPersonInput } from "@/components/verification/organisations";
import { PayoutDestinationsManager, buildDestinationInput } from "@/components/verification/payout-destinations";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import { browserNavigation } from "@/lib/browser-navigation";
import type { VerificationApi } from "@/lib/verification/api";
import type { Beneficiary, KybStatus, KycCase, MyOrganisation, PayoutDestination, ReviewCase, VerificationDocument } from "@/lib/verification/types";
import { axeViolations } from "@/test/axe";

function apiError(code: string, status: number, details: Array<{ field: string; code: string }> = []) {
  return new ApiError({ code, status, message: "server text that must not be shown", retryable: false, details });
}

function fakeApi(overrides: Partial<Record<keyof VerificationApi, unknown>> = {}): VerificationApi {
  return new Proxy({}, { get: (_t, key: string) => (overrides as Record<string, unknown>)[key] ?? vi.fn(async () => undefined) }) as VerificationApi;
}

function fakeAuth(stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-09T10:00:00Z" }))): AuthApi {
  return new Proxy({}, { get: (_t, key: string) => (key === "stepUp" ? stepUp : vi.fn()) }) as AuthApi;
}

function withStepUp(ui: ReactNode, auth: AuthApi = fakeAuth(), codeOnly = false) {
  return (
    <StepUpProvider mfaEnabled codeOnly={codeOnly} api={auth}>
      {ui}
    </StepUpProvider>
  );
}

const doc = (overrides: Partial<VerificationDocument> = {}): VerificationDocument => ({
  id: "doc-1",
  subject_type: "KYC_CASE",
  subject_id: "case-1",
  document_type: "ZW_NATIONAL_ID",
  side: "FRONT",
  status: "CLEAN",
  media_type: "image/png",
  size_bytes: 2048,
  uploaded_at: "2026-10-09T08:00:00Z",
  rejected_reason: null,
  ...overrides,
});

function kycCase(overrides: Partial<KycCase> = {}): KycCase {
  return {
    id: "case-1",
    kind: "KYC",
    status: "DRAFT",
    target_level: "IDENTITY_VERIFIED",
    policy_version: 1,
    identity: null,
    documents: [],
    requirements: [{ document_type: "ZW_NATIONAL_ID", sides: ["FRONT", "BACK"], satisfied: false }],
    information_requests: [],
    decision: null,
    submitted_at: null,
    created_at: "2026-10-09T08:00:00Z",
    updated_at: "2026-10-09T08:00:00Z",
    version: 3,
    ...overrides,
  };
}

let assign: ReturnType<typeof vi.fn>;
beforeEach(() => {
  assign = vi.fn();
  vi.spyOn(browserNavigation, "assign").mockImplementation(assign as never);
  vi.spyOn(browserNavigation, "currentPath").mockReturnValue("/dashboard/verification");
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

// ---------------------------------------------------------------------------------------------------------

describe("buildKycPatch", () => {
  const now = new Date("2026-10-09T10:00:00Z");
  function form(values: Record<string, string>) {
    const data = new FormData();
    for (const [k, v] of Object.entries(values)) data.set(k, v);
    return data;
  }

  it("sends only filled fields; the ID number only when typed; expiry null when cleared", () => {
    const { body, errors } = buildKycPatch(form({ legal_first_name: "  Chipo  Rudo ", date_of_birth: "1990-04-18", nationality: "ZW", id_document_number: "", id_document_expiry: "" }), now);
    expect(errors).toEqual({});
    expect(body).toEqual({ legal_first_name: "Chipo Rudo", date_of_birth: "1990-04-18", nationality: "ZW", id_document_expiry: null });
  });

  it("validates dates and address completeness without deciding policy", () => {
    const { errors } = buildKycPatch(form({ date_of_birth: "2026-02-30", id_document_expiry: "31/01/2030", "residential_address.line1": "1 Road", "residential_address.country": "ZW" }), now);
    expect(errors.date_of_birth).toMatch(/real date/);
    expect(errors.id_document_expiry).toMatch(/real date/);
    expect(errors["residential_address.city"]).toMatch(/town or city/);
    expect(buildKycPatch(form({ date_of_birth: "2027-01-01" }), now).errors.date_of_birth).toMatch(/future/);
  });
});

describe("KycIdentity", () => {
  it("explains the prerequisites instead of offering to start", () => {
    render(withStepUp(<KycIdentity initialCase={null} level="UNVERIFIED" emailVerified phoneVerified={false} api={fakeApi()} />));
    expect(screen.getByText(/confirm your\s+phone number/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Start verification" })).toBeNull();
    expect(screen.getByRole("link", { name: "Go to security settings" })).toHaveAttribute("href", "/settings/security");
  });

  it("start → fill → save (If-Match version) → review step", async () => {
    const user = userEvent.setup();
    const startKycCase = vi.fn(async () => kycCase());
    const updateKycCase = vi.fn(async () =>
      kycCase({
        version: 4,
        identity: {
          legal_first_name: "Chipo",
          legal_last_name: "Ncube",
          date_of_birth: "1990-04-18",
          nationality: "ZW",
          country_of_residence: "ZW",
          id_document_type: "ZW_NATIONAL_ID",
          id_document_number_masked: "••••••A 12",
          id_document_expiry: null,
          residential_address: { line1: "12 Samora Machel Ave", line2: null, city: "Harare", province: null, postal_code: null, country: "ZW" },
        },
      }),
    );
    const { container } = render(withStepUp(<KycIdentity initialCase={null} level="BASIC_VERIFIED" emailVerified phoneVerified api={fakeApi({ startKycCase, updateKycCase })} />));
    await user.click(screen.getByRole("button", { name: "Start verification" }));
    await screen.findByRole("heading", { name: "Step 1: Your personal details" });
    await user.type(screen.getByLabelText("First name(s)"), "Chipo");
    await user.type(screen.getByLabelText("Surname"), "Ncube");
    await user.type(screen.getByLabelText("Date of birth"), "1990-04-18");
    await user.selectOptions(screen.getByLabelText("Nationality"), "ZW");
    await user.selectOptions(screen.getByLabelText("Country you live in"), "ZW");
    await user.selectOptions(screen.getByLabelText("Document type"), "ZW_NATIONAL_ID");
    await user.type(screen.getByLabelText("Document number"), "63-123456 A 12");
    await user.type(screen.getByLabelText("Address line 1"), "12 Samora Machel Ave");
    await user.type(screen.getByLabelText("Town or city"), "Harare");
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Save and continue" }));
    await waitFor(() => expect(updateKycCase).toHaveBeenCalledTimes(1));
    const [id, body, version] = updateKycCase.mock.calls[0] as unknown as [string, Record<string, unknown>, number];
    expect(id).toBe("case-1");
    expect(version).toBe(3);
    expect(body).toMatchObject({ legal_first_name: "Chipo", id_document_number: "63-123456 A 12", residential_address: { line1: "12 Samora Machel Ave", city: "Harare", country: "ZW" } });
    const review = await screen.findByRole("heading", { name: "Step 2: Check and submit" });
    await waitFor(() => expect(review).toHaveFocus());
    // Only the masked number is ever shown back.
    expect(screen.getByText("••••••A 12")).toBeInTheDocument();
    expect(screen.queryByText(/63-123456/)).toBeNull();
  });

  it("SUBMISSION_INCOMPLETE shows a linked problem summary and moves focus to it", async () => {
    const user = userEvent.setup();
    const submitKycCase = vi.fn(async () => {
      throw apiError("SUBMISSION_INCOMPLETE", 422, [
        { field: "documents.ZW_NATIONAL_ID.BACK", code: "DOCUMENT_REQUIRED" },
        { field: "date_of_birth", code: "UNDERAGE" },
      ]);
    });
    const identity = { legal_first_name: "A", legal_last_name: "B", date_of_birth: "2015-01-01", nationality: "ZW", country_of_residence: "ZW", id_document_type: "ZW_NATIONAL_ID" as const, id_document_number_masked: "••12", id_document_expiry: null, residential_address: { line1: "x", line2: null, city: "y", province: null, postal_code: null, country: "ZW" } };
    render(withStepUp(<KycIdentity initialCase={kycCase({ identity })} level="BASIC_VERIFIED" emailVerified phoneVerified api={fakeApi({ submitKycCase })} />));
    await user.click(screen.getByRole("button", { name: "Submit for verification" }));
    const message = await screen.findByText("Some things need attention before you can submit.");
    expect(message).toHaveAttribute("role", "alert");
    await waitFor(() => expect(message).toHaveFocus());
    expect(screen.getByRole("link", { name: "Zimbabwe national ID (back)" })).toHaveAttribute("href", "#field-documents");
    expect(screen.getByRole("link", { name: "Date of birth" })).toHaveAttribute("href", "#field-date_of_birth");
    expect(screen.getByText(/18 or older/)).toBeInTheDocument();
    expect(screen.queryByText(/server text/)).toBeNull();
  });

  it("additional information requests are shown with how to respond; resubmission is 'Submit again'", () => {
    const identity = { legal_first_name: "A", legal_last_name: "B", date_of_birth: "1990-01-01", nationality: "ZW", country_of_residence: "ZW", id_document_type: "ZW_NATIONAL_ID" as const, id_document_number_masked: "••12", id_document_expiry: null, residential_address: { line1: "x", line2: null, city: "y", province: null, postal_code: null, country: "ZW" } };
    render(
      withStepUp(
        <KycIdentity
          initialCase={kycCase({ status: "ADDITIONAL_INFORMATION_REQUIRED", identity, information_requests: [{ id: "r1", message: "Please upload a clearer photo of the back of your ID.", items: ["ZW_NATIONAL_ID"], requested_at: "2026-10-09T09:00:00Z", responded_at: null }] })}
          level="BASIC_VERIFIED"
          emailVerified
          phoneVerified
          api={fakeApi()}
        />,
      ),
    );
    expect(screen.getByText("Please upload a clearer photo of the back of your ID.")).toBeInTheDocument();
    expect(screen.getByText("Waiting for you")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit again" })).toBeInTheDocument();
  });

  it("shows the decision message of a rejected case and offers a new verification", () => {
    render(
      withStepUp(
        <KycIdentity initialCase={kycCase({ status: "REJECTED", decision: { outcome: "REJECTED", reason_code: "DOCUMENT_ILLEGIBLE", message: "The photo of your ID was too blurry to read.", decided_at: "2026-10-09T09:00:00Z" } })} level="BASIC_VERIFIED" emailVerified phoneVerified api={fakeApi()} />,
      ),
    );
    expect(screen.getByText("Verification decision: not approved")).toBeInTheDocument();
    expect(screen.getByText("The photo of your ID was too blurry to read.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Start verification" })).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------------------------------------

describe("DocumentManager", () => {
  it("uploads with progress, then polls the scan status until CLEAN and announces it", async () => {
    const user = userEvent.setup();
    let resolveUpload: (d: VerificationDocument) => void = () => {};
    let progress: (loaded: number, total: number) => void = () => {};
    const upload = vi.fn((_input, options) => {
      progress = options.onProgress;
      return new Promise<VerificationDocument>((resolve) => (resolveUpload = resolve));
    });
    const getDocument = vi.fn().mockResolvedValueOnce(doc({ id: "new", status: "SCANNING" })).mockResolvedValue(doc({ id: "new", status: "CLEAN" }));
    render(
      withStepUp(
        <DocumentManager subjectType="KYC_CASE" subjectId="case-1" initialDocuments={[]} requirements={[{ document_type: "ZW_NATIONAL_ID", sides: ["FRONT", "BACK"], satisfied: false }]} editable api={fakeApi({ getDocument })} upload={upload} pollDelaysMs={[10, 10]} />,
      ),
    );
    await user.selectOptions(screen.getByLabelText("Side"), "FRONT");
    await user.upload(screen.getByLabelText("File"), new File([new Uint8Array(100)], "front.png", { type: "image/png" }));
    await user.click(screen.getByRole("button", { name: "Upload" }));
    expect(upload).toHaveBeenCalledWith(expect.objectContaining({ subjectType: "KYC_CASE", subjectId: "case-1", documentType: "ZW_NATIONAL_ID", side: "FRONT" }), expect.anything());
    act(() => progress(40, 100));
    expect(screen.getByRole("progressbar")).toHaveAttribute("value", "40");
    expect(screen.getByText("Upload progress: 40%")).toBeInTheDocument();
    await act(async () => resolveUpload(doc({ id: "new", status: "QUARANTINED" })));
    expect(await screen.findByText("Waiting for security check")).toBeInTheDocument();
    await screen.findByText("Ready", {}, { timeout: 2000 });
    expect(getDocument).toHaveBeenCalledWith("new");
    // The polite live region announces the change for screen-reader users.
    expect(screen.getByText(/Zimbabwe national ID \(front\): Ready\./)).toHaveAttribute("aria-live", "polite");
  });

  it("validates the file and side before uploading", async () => {
    const user = userEvent.setup();
    const upload = vi.fn();
    render(withStepUp(<DocumentManager subjectType="KYC_CASE" subjectId="c" initialDocuments={[]} editable api={fakeApi()} upload={upload} />));
    await user.click(screen.getByRole("button", { name: "Upload" }));
    expect(await screen.findByText("Choose a file to upload.")).toBeInTheDocument();
    expect(screen.getByLabelText("File")).toHaveAttribute("aria-invalid", "true");
    await user.upload(screen.getByLabelText("File"), new File([new Uint8Array(10)], "a.png", { type: "image/png" }));
    await user.click(screen.getByRole("button", { name: "Upload" }));
    expect(await screen.findByText("Choose which side of the document this is.")).toBeInTheDocument();
    expect(upload).not.toHaveBeenCalled();
  });

  it("maps upload error codes (scan rejection shows the reason; type mismatch is explained)", async () => {
    const user = userEvent.setup();
    const upload = vi.fn(async () => {
      throw apiError("FILE_TYPE_MISMATCH", 422);
    });
    render(withStepUp(<DocumentManager subjectType="BENEFICIARY" subjectId="b" initialDocuments={[doc({ id: "bad", subject_type: "BENEFICIARY", document_type: "BIRTH_CERTIFICATE", side: null, status: "REJECTED", rejected_reason: "MALWARE_DETECTED" })]} editable api={fakeApi()} upload={upload} />));
    expect(screen.getByText("Rejected")).toBeInTheDocument();
    expect(screen.getByText(/failed the malware check/)).toBeInTheDocument();
    await user.upload(screen.getByLabelText("File"), new File([new Uint8Array(10)], "a.pdf", { type: "application/pdf" }));
    await user.click(screen.getByRole("button", { name: "Upload" }));
    expect(await screen.findByText(/contents don't match its type/)).toBeInTheDocument();
  });

  it("view: STEP_UP_REQUIRED prompts for a code, retries once and opens only a safe same-origin URL", async () => {
    const user = userEvent.setup();
    const documentAccess = vi
      .fn()
      .mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403))
      .mockResolvedValueOnce({ url: "/api/v1/verification/documents/doc-1/content?ticket=t1", expires_at: "2026-10-09T10:01:00Z" });
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "x" }));
    render(withStepUp(<DocumentManager subjectType="KYC_CASE" subjectId="case-1" initialDocuments={[doc()]} editable={false} api={fakeApi({ documentAccess })} />, fakeAuth(stepUp), true));
    expect(screen.queryByRole("button", { name: /Delete/ })).toBeNull();
    await user.click(screen.getByRole("button", { name: "View Zimbabwe national ID front" }));
    const code = await screen.findByLabelText("Authentication code");
    await user.type(code, "246810");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/api/v1/verification/documents/doc-1/content?ticket=t1"));
    expect(stepUp).toHaveBeenCalledWith({ code: "246810" });
    expect(documentAccess).toHaveBeenCalledTimes(2);
  });

  it("refuses a ticket URL that points elsewhere", async () => {
    const user = userEvent.setup();
    const documentAccess = vi.fn(async () => ({ url: "https://evil.example/x", expires_at: "x" }));
    render(withStepUp(<DocumentManager subjectType="KYC_CASE" subjectId="case-1" initialDocuments={[doc()]} editable={false} api={fakeApi({ documentAccess })} />));
    await user.click(screen.getByRole("button", { name: /^View/ }));
    expect(await screen.findByText("We couldn't open this document. Please try again.")).toBeInTheDocument();
    expect(assign).not.toHaveBeenCalled();
  });

  it("delete asks for confirmation in a dialog", async () => {
    const user = userEvent.setup();
    const deleteDocument = vi.fn(async () => undefined);
    render(withStepUp(<DocumentManager subjectType="KYC_CASE" subjectId="case-1" initialDocuments={[doc()]} editable api={fakeApi({ deleteDocument })} />));
    await user.click(screen.getByRole("button", { name: "Delete Zimbabwe national ID front" }));
    const dialog = screen.getByRole("dialog", { name: "Delete this document?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete document" }));
    await waitFor(() => expect(deleteDocument).toHaveBeenCalledWith("doc-1"));
    expect(await screen.findByText("No documents uploaded yet.")).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------------------------------------

const beneficiary = (overrides: Partial<Beneficiary> = {}): Beneficiary => ({
  id: "ben-1",
  owner: { type: "USER", id: "u1" },
  beneficiary_type: "MINOR",
  kind: "INDIVIDUAL",
  display_name: "Tafadzwa",
  full_name: "Tafadzwa Moyo",
  relationship: { type: "PARENT_GUARDIAN", description: null },
  authority_basis: "PARENTAL_RESPONSIBILITY",
  verification: { status: "DRAFT", risk_level: "ENHANCED", requires_second_approval: true, information_requests: [], decision: null },
  documents: [],
  requirements: [{ document_type: "BIRTH_CERTIFICATE", sides: [], satisfied: false }],
  created_at: "2026-10-09T08:00:00Z",
  updated_at: "2026-10-09T08:00:00Z",
  version: 1,
  ...overrides,
});

describe("Beneficiaries", () => {
  it("buildBeneficiaryInput requires a date of birth for a minor and a description for OTHER", () => {
    const data = new FormData();
    data.set("beneficiary_type", "MINOR");
    data.set("display_name", "T");
    data.set("relationship_type", "OTHER");
    data.set("authority_basis", "PARENTAL_RESPONSIBILITY");
    const { errors } = buildBeneficiaryInput(data);
    expect(Object.keys(errors).sort()).toEqual(["date_of_birth", "display_name", "full_name", "relationship_description"]);
  });

  it("flags minors as needing extra evidence, suggests the authority basis and creates", async () => {
    const user = userEvent.setup();
    const createBeneficiary = vi.fn(async () => beneficiary());
    const { container } = render(withStepUp(<BeneficiariesManager initial={[]} organisations={[]} api={fakeApi({ createBeneficiary })} />));
    await user.selectOptions(screen.getByLabelText("Who are the funds for?"), "MINOR");
    expect(screen.getByText("Extra evidence needed")).toBeInTheDocument();
    expect(screen.getByLabelText("What gives you authority to raise funds for them?")).toHaveValue("PARENTAL_RESPONSIBILITY");
    await user.type(screen.getByLabelText("Name to show on campaigns"), "Tafadzwa");
    await user.type(screen.getByLabelText("Full legal name"), "Tafadzwa Moyo");
    await user.type(screen.getByLabelText("Date of birth"), "2016-05-01");
    await user.selectOptions(screen.getByLabelText("Your relationship to them"), "PARENT_GUARDIAN");
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Add beneficiary" }));
    await waitFor(() => expect(createBeneficiary).toHaveBeenCalledTimes(1));
    expect(createBeneficiary.mock.calls[0]).toEqual([
      { beneficiary_type: "MINOR", display_name: "Tafadzwa", full_name: "Tafadzwa Moyo", date_of_birth: "2016-05-01", relationship: { type: "PARENT_GUARDIAN" }, authority_basis: "PARENTAL_RESPONSIBILITY" },
    ]);
    expect(await screen.findByText(/Tafadzwa was added/)).toBeInTheDocument();
    expect(screen.getByText("Two reviewers required")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit Tafadzwa for verification" })).toBeInTheDocument();
  });

  it("submit maps SUBMISSION_INCOMPLETE for missing evidence", async () => {
    const user = userEvent.setup();
    const submitBeneficiary = vi.fn(async () => {
      throw apiError("SUBMISSION_INCOMPLETE", 422, [{ field: "documents.BIRTH_CERTIFICATE", code: "DOCUMENT_REQUIRED" }]);
    });
    render(withStepUp(<BeneficiariesManager initial={[beneficiary()]} organisations={[]} api={fakeApi({ submitBeneficiary })} />));
    await user.click(screen.getByRole("button", { name: "Submit Tafadzwa for verification" }));
    expect(await screen.findByRole("link", { name: "Birth certificate" })).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------------------------------------

const destination = (overrides: Partial<PayoutDestination> = {}): PayoutDestination => ({
  id: "dest-1",
  owner: { type: "USER", id: "u1" },
  payee: { type: "OWNER", beneficiary_id: null },
  category: "MOBILE_MONEY_WALLET",
  rail: "ECOCASH",
  provider_name: "EcoCash",
  currency: "USD",
  holder_name: "Chipo Ncube",
  masked_identifier: "••••••4567",
  status: "UNVERIFIED",
  checks: { format_validated: true, ownership: "NOT_STARTED", compliance: "PENDING" },
  last_reviewed_at: null,
  eligible_for_payout: false,
  created_at: "2026-10-09T08:00:00Z",
  updated_at: "2026-10-09T08:00:00Z",
  version: 1,
  ...overrides,
});

describe("Payout destinations", () => {
  it("buildDestinationInput normalises the number and requires a bank code for banks", () => {
    const data = new FormData();
    data.set("rail", "BANK_TRANSFER");
    data.set("currency", "USD");
    data.set("holder_name", "Chipo");
    data.set("account_identifier", "1234 5678-90");
    const { body, errors } = buildDestinationInput(data);
    expect(body.account_identifier).toBe("1234567890");
    expect(errors.bank_code).toBeDefined();
  });

  it("always says payouts are unavailable; create shows only the masked number and three separate checks", async () => {
    const user = userEvent.setup();
    const createDestination = vi.fn(async () => destination());
    const requestDestinationVerification = vi.fn(async () => destination({ status: "PENDING_VERIFICATION", checks: { format_validated: true, ownership: "PROVIDER_CONFIRMATION_REQUIRED", compliance: "PENDING" } }));
    const { container } = render(withStepUp(<PayoutDestinationsManager initial={[]} beneficiaries={[]} organisations={[]} api={fakeApi({ createDestination, requestDestinationVerification })} />));
    expect(screen.getByText("Payouts are not available yet")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add a payout account" }));
    await user.selectOptions(screen.getByLabelText("How would money be received?"), "ECOCASH");
    await user.selectOptions(screen.getByLabelText("Currency"), "USD");
    await user.type(screen.getByLabelText("Account holder name"), "Chipo Ncube");
    const account = screen.getByLabelText("Mobile money number");
    expect(account).toHaveAttribute("autocomplete", "off");
    await user.type(account, "077 123 4567");
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Add account" }));
    await waitFor(() => expect(createDestination).toHaveBeenCalledWith(expect.objectContaining({ rail: "ECOCASH", currency: "USD", account_identifier: "0771234567", payee: { type: "OWNER" } })));
    expect(await screen.findByRole("heading", { name: "EcoCash · ••••••4567" })).toBeInTheDocument();
    expect(container.textContent).not.toContain("0771234567");
    expect(screen.getByText("Valid format")).toBeInTheDocument();
    expect(screen.getByText(/No — payouts are not available yet/)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Verify this account" }));
    await user.selectOptions(screen.getByLabelText("How can you show this account is yours?"), "PROVIDER_LOOKUP");
    await user.click(screen.getByRole("button", { name: "Request verification" }));
    await waitFor(() => expect(requestDestinationVerification).toHaveBeenCalledWith("dest-1", "PROVIDER_LOOKUP", []));
    expect(await screen.findByText("Provider confirmation required")).toBeInTheDocument();
    expect(screen.getAllByText(/not available yet/).length).toBeGreaterThan(1);
  });

  it("maps INVALID_ACCOUNT_FORMAT and DESTINATION_EXISTS", async () => {
    const user = userEvent.setup();
    const createDestination = vi.fn().mockRejectedValueOnce(apiError("INVALID_ACCOUNT_FORMAT", 422)).mockRejectedValueOnce(apiError("DESTINATION_EXISTS", 409));
    render(withStepUp(<PayoutDestinationsManager initial={[]} beneficiaries={[]} organisations={[]} api={fakeApi({ createDestination })} />));
    await user.click(screen.getByRole("button", { name: "Add a payout account" }));
    await user.selectOptions(screen.getByLabelText("How would money be received?"), "ONEMONEY");
    await user.selectOptions(screen.getByLabelText("Currency"), "ZWG");
    await user.type(screen.getByLabelText("Account holder name"), "Chipo");
    await user.type(screen.getByLabelText("Mobile money number"), "0711111");
    await user.click(screen.getByRole("button", { name: "Add account" }));
    expect(await screen.findByText(/not in a valid format for the selected provider/)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText("Mobile money number")).toHaveFocus());
    await user.click(screen.getByRole("button", { name: "Add account" }));
    expect(await screen.findByText(/already added this account/)).toBeInTheDocument();
  });
});

// ---------------------------------------------------------------------------------------------------------

const org = (role: "ORG_ADMIN" | "ORG_MEMBER"): MyOrganisation => ({ id: "org-1", display_name: "Harare Trust", slug: "harare-trust", org_type: "TRUST", status: "ACTIVE", my_role: role, my_permissions: [], created_at: "2026-10-01T08:00:00Z" });
const kyb = (overrides: Partial<NonNullable<KybStatus["case"]>> = {}): KybStatus => ({
  level: "ORG_UNVERIFIED",
  status: "ACTIVE",
  case: {
    id: "kyb-1",
    kind: "KYB",
    organisation_id: "org-1",
    status: "DRAFT",
    target_level: "ORG_VERIFIED",
    details: { registered_name: null, trading_name: null, registration_number: null, registry: null, country_of_registration: null, registered_address: null },
    persons: [],
    documents: [],
    requirements: [],
    information_requests: [],
    decision: null,
    submitted_at: null,
    created_at: "2026-10-09T08:00:00Z",
    updated_at: "2026-10-09T08:00:00Z",
    version: 1,
    ...overrides,
  },
});

describe("Organisation verification", () => {
  it("buildPersonInput converts a percentage string to integer basis points", () => {
    const data = new FormData();
    data.set("full_name", "Rudo Dube");
    data.append("roles", "BENEFICIAL_OWNER");
    data.append("roles", "DIRECTOR");
    data.set("ownership_percent", "29.3");
    expect(buildPersonInput(data).body).toEqual({ full_name: "Rudo Dube", roles: ["BENEFICIAL_OWNER", "DIRECTOR"], ownership_bp: 2930 });
    data.set("ownership_percent", "12.345");
    expect(buildPersonInput(data).errors.ownership_percent).toMatch(/two decimal places/);
    data.delete("ownership_percent");
    expect(buildPersonInput(data).errors.ownership_percent).toMatch(/beneficial owner/);
  });

  it("ORG_ADMIN adds a person with a percentage share and sees it as a percent", async () => {
    const user = userEvent.setup();
    const addKybPerson = vi.fn(async () => ({ id: "p1", full_name: "Rudo Dube", roles: ["BENEFICIAL_OWNER" as const], ownership_bp: 1250, id_document_type: null, id_document_number_masked: null }));
    const { container } = render(withStepUp(<OrganisationVerification organisation={org("ORG_ADMIN")} initial={kyb()} representativeVerified={false} api={fakeApi({ addKybPerson })} />));
    expect(screen.getByText("Your identity is not verified yet")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Full legal name"), "Rudo Dube");
    await user.click(screen.getByRole("checkbox", { name: "Beneficial owner" }));
    await user.type(screen.getByLabelText(/Ownership share/), "12.5");
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Add person" }));
    await waitFor(() => expect(addKybPerson).toHaveBeenCalledWith("org-1", { full_name: "Rudo Dube", roles: ["BENEFICIAL_OWNER"], ownership_bp: 1250 }));
    const row = await screen.findByRole("row", { name: /Rudo Dube/ });
    expect(within(row).getByText("12.5%")).toBeInTheDocument();
  });

  it("REPRESENTATIVE_NOT_VERIFIED on submit is explained", async () => {
    const user = userEvent.setup();
    const submitKyb = vi.fn(async () => {
      throw apiError("REPRESENTATIVE_NOT_VERIFIED", 422);
    });
    render(withStepUp(<OrganisationVerification organisation={org("ORG_ADMIN")} initial={kyb()} representativeVerified={false} api={fakeApi({ submitKyb })} />));
    await user.click(screen.getByRole("button", { name: "Submit for verification" }));
    expect(await screen.findByText(/verify your own identity before you can submit this organisation/)).toBeInTheDocument();
  });

  it("ORG_MEMBER sees status read-only", () => {
    render(withStepUp(<OrganisationVerification organisation={org("ORG_MEMBER")} initial={kyb({ status: "SUBMITTED" })} representativeVerified api={fakeApi()} />));
    expect(screen.getByText(/Only the organisation's administrators can manage/)).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByLabelText("Full legal name")).toBeNull();
  });
});

// ---------------------------------------------------------------------------------------------------------

const reviewCase = (overrides: Partial<ReviewCase> = {}): ReviewCase => ({
  id: "case-9",
  type: "KYC",
  status: "UNDER_REVIEW",
  subject: { type: "USER", id: "u9", display_name: "Chipo Ncube" },
  assigned_to: { id: "staff-1", display_name: "Reviewer One" },
  risk_level: "STANDARD",
  submitted_at: "2026-10-09T08:00:00Z",
  updated_at: "2026-10-09T08:00:00Z",
  identity: { legal_first_name: "Chipo", legal_last_name: "Ncube", date_of_birth: "1990-04-18", nationality: "ZW", country_of_residence: "ZW", id_document_type: "ZW_NATIONAL_ID", id_document_number_masked: "••••A 12", id_document_expiry: null, residential_address: null },
  documents: [doc()],
  history: [{ id: "h1", action: "kyc.case_submitted", actor: { type: "USER", display_name: "Chipo Ncube" }, occurred_at: "2026-10-09T08:00:00Z" }],
  ...overrides,
});

describe("Reviewer case view", () => {
  it("buildActionBody validates reason codes and user messages", () => {
    const data = new FormData();
    data.set("reason_code", "OTHER");
    data.set("note", "short");
    expect(buildActionBody("approve", data).errors.note).toMatch(/10 characters/);
    const reject = new FormData();
    reject.set("reason_code", "DOCUMENT_ILLEGIBLE");
    expect(Object.keys(buildActionBody("reject", reject).errors).sort()).toEqual(["note", "user_message"]);
    // Every decision needs a non-empty internal note (API: INVALID_LENGTH otherwise); assign/start do not.
    for (const action of ["approve", "second-approval", "escalate", "suspend", "reinstate", "reopen", "return", "revoke"] as const) {
      const d = new FormData();
      d.set("reason_code", "COMPLIANCE_INSTRUCTION");
      expect(buildActionBody(action, d).errors.note, action).toMatch(/at least 3 characters/);
    }
    expect(buildActionBody("assign", new FormData()).errors).toEqual({});
    const approve = new FormData();
    approve.set("reason_code", "IDENTITY_CONFIRMED");
    approve.set("note", "Consistent.");
    approve.set("conditions", "ignored");
    expect(buildActionBody("approve", approve)).toEqual({ body: { reason_code: "IDENTITY_CONFIRMED", note: "Consistent." }, errors: {} });
    const info = new FormData();
    info.set("message", "Please upload a clearer photo.");
    info.set("items", "ZW_NATIONAL_ID\nPROOF_OF_ADDRESS, SELFIE");
    expect(buildActionBody("request-info", info).body).toEqual({ message: "Please upload a clearer photo.", items: ["ZW_NATIONAL_ID", "PROOF_OF_ADDRESS", "SELFIE"] });
  });

  it("approve: confirmation dialog with reason, TOTP step-up on STEP_UP_REQUIRED, then retry", async () => {
    const user = userEvent.setup();
    const reviewAction = vi.fn().mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403)).mockResolvedValueOnce({ data: {}, status: 200, meta: { request_id: "r" }, requestId: "r" });
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "x" }));
    const { container } = render(withStepUp(<ReviewCaseView reviewCase={reviewCase()} meId="staff-1" api={fakeApi({ reviewAction })} />, fakeAuth(stepUp), true));
    expect(screen.getByText("••••A 12")).toBeInTheDocument();
    expect(screen.getByText("Kyc case submitted")).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Approve" }));
    const dialog = screen.getByRole("dialog", { name: "Approve" });
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    expect(await within(dialog).findByText("Choose a reason.")).toBeInTheDocument();
    expect(within(dialog).getByText(/at least 3 characters/)).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(/Conditions/)).toBeNull();
    await user.selectOptions(within(dialog).getByLabelText("Reason"), "IDENTITY_CONFIRMED");
    await user.type(within(dialog).getByLabelText("Internal note (reviewers only)"), "Photo matches.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    await user.type(await screen.findByLabelText("Authentication code"), "246810");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(reviewAction).toHaveBeenCalledTimes(2));
    expect(reviewAction).toHaveBeenLastCalledWith("case-9", "approve", { reason_code: "IDENTITY_CONFIRMED", note: "Photo matches." });
    expect(stepUp).toHaveBeenCalledWith({ code: "246810" });
    expect(await screen.findByText("Approve: done.")).toBeInTheDocument();
  });

  it("a 202 from a four-eyes approval is reported as awaiting a second approval; reviewer error codes are mapped", async () => {
    const user = userEvent.setup();
    const reviewAction = vi.fn().mockResolvedValueOnce({ data: { status: "AWAITING_SECOND_APPROVAL" }, status: 202, meta: { request_id: "r" }, requestId: "r" }).mockRejectedValueOnce(apiError("NOT_ASSIGNED", 409));
    render(withStepUp(<ReviewCaseView reviewCase={reviewCase({ type: "BENEFICIARY", identity: null })} meId="staff-1" api={fakeApi({ reviewAction })} />));
    await user.click(screen.getByRole("button", { name: "Approve" }));
    await user.selectOptions(screen.getByLabelText("Reason"), "AUTHORITY_CONFIRMED");
    await user.type(screen.getByLabelText("Internal note (reviewers only)"), "Order seen.");
    await user.click(screen.getByRole("button", { name: "Confirm: Approve" }));
    expect(await screen.findByText(/needs a second approval from a different reviewer/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Escalate to compliance" }));
    await user.selectOptions(screen.getByLabelText("Reason"), "RISK_INDICATORS");
    await user.type(screen.getByLabelText("Internal note (reviewers only)"), "Second account.");
    await user.click(screen.getByRole("button", { name: "Confirm: Escalate to compliance" }));
    expect(await screen.findByText(/Only the assigned reviewer can do this/)).toBeInTheDocument();
  });

  it("four-eyes from the backend `review` block: the first approver is not offered the second approval", () => {
    const raw = { id: "k2", type: "KYC", case: { status: "UNDER_REVIEW", identity: null, documents: [] }, assigned_to: "staff-1", subject: { type: "USER", id: "u", display_name: "S" }, history: [], review: { risk_level: "ENHANCED", requires_second_approval: true, pending_outcome: "APPROVE", pending_decided_by: "staff-1" } };
    const { unmount } = render(withStepUp(<ReviewCaseView reviewCase={normaliseReviewCase(raw, "staff-1")} meId="staff-1" api={fakeApi()} />));
    const actions = screen.getByRole("region", { name: "Actions" });
    expect(within(actions).queryByRole("button", { name: "Give second approval" })).toBeNull();
    expect(screen.getByText(/You gave the first approval/)).toBeInTheDocument();
    expect(screen.getByText("Enhanced")).toBeInTheDocument();
    expect(screen.getByText(/Four-eyes/)).toBeInTheDocument();
    unmount();
    render(withStepUp(<ReviewCaseView reviewCase={normaliseReviewCase(raw, "staff-2")} meId="staff-2" api={fakeApi()} />));
    expect(within(screen.getByRole("region", { name: "Actions" })).getByRole("button", { name: "Give second approval" })).toBeInTheDocument();
    expect(screen.getByText(/First approval \(Approve\) by Another staff member/)).toBeInTheDocument();
  });

  it("maps ASSIGNEE_NOT_ELIGIBLE and a 422 note/INVALID_LENGTH detail onto the note field", async () => {
    const user = userEvent.setup();
    const assignFail = vi.fn().mockRejectedValue(apiError("ASSIGNEE_NOT_ELIGIBLE", 422));
    const { unmount } = render(withStepUp(<ReviewCaseView reviewCase={reviewCase({ assigned_to: null })} meId="staff-1" api={fakeApi({ reviewAction: assignFail })} />));
    await user.click(screen.getByRole("button", { name: "Assign to me" }));
    await user.click(screen.getByRole("button", { name: "Confirm: Assign to me" }));
    expect(await screen.findByText("That person can't review verification cases.")).toBeInTheDocument();
    unmount();

    const noteFail = vi.fn().mockRejectedValue(apiError("VALIDATION_FAILED", 422, [{ field: "note", code: "INVALID_LENGTH" }]));
    render(withStepUp(<ReviewCaseView reviewCase={reviewCase()} meId="staff-1" api={fakeApi({ reviewAction: noteFail })} />));
    await user.click(screen.getByRole("button", { name: "Approve" }));
    await user.selectOptions(screen.getByLabelText("Reason"), "IDENTITY_CONFIRMED");
    await user.type(screen.getByLabelText("Internal note (reviewers only)"), "abc");
    await user.click(screen.getByRole("button", { name: "Confirm: Approve" }));
    expect(await screen.findByText("Write an internal note of at least 3 characters (at most 5,000).")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByLabelText("Internal note (reviewers only)")).toHaveFocus());
  });

  it("not assigned → only 'Assign to me'; reveal of the ID number needs a justification", async () => {
    const user = userEvent.setup();
    const revealIdentityNumber = vi.fn(async () => ({ id_document_number: "63-123456 A 12" }));
    render(withStepUp(<ReviewCaseView reviewCase={reviewCase({ assigned_to: null })} meId="staff-1" api={fakeApi({ revealIdentityNumber })} />));
    const actions = screen.getByRole("region", { name: "Actions" });
    expect(within(actions).getAllByRole("button").map((b) => b.textContent)).toEqual(["Assign to me"]);
    await user.click(screen.getByRole("button", { name: "Reveal full ID number" }));
    await user.click(screen.getByRole("button", { name: "Reveal" }));
    expect(await screen.findByText(/at least 10 characters/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Justification"), "Name mismatch on the document needs checking");
    await user.click(screen.getByRole("button", { name: "Reveal" }));
    expect(await screen.findByText("63-123456 A 12")).toBeInTheDocument();
    expect(revealIdentityNumber).toHaveBeenCalledWith("case-9", "Name mismatch on the document needs checking");
    await user.click(screen.getByRole("button", { name: "Hide now" }));
    expect(screen.queryByText("63-123456 A 12")).toBeNull();
  });
});
