import { browserApi, type ApiClient } from "@/lib/api/client";

import type {
  Beneficiary,
  BeneficiaryInput,
  ComplianceAction,
  DocumentAccess,
  KybCase,
  KybPerson,
  KybPersonInput,
  KybStatus,
  KycCase,
  KycCasePatch,
  KycStatus,
  MyOrganisation,
  PayoutDestination,
  PayoutDestinationInput,
  ReviewAction,
  SubjectType,
  VerificationDocument,
  VerificationMethod,
} from "./types";

/**
 * Browser-side calls for the Stage 5 verification API (same-origin /api/v1 through the web proxy; the
 * client adds X-CSRF-Token on unsafe methods and never retries them). Identity values and account numbers
 * are sent in request bodies only — never in URLs, logs or browser storage.
 */
const WRITE_TIMEOUT_MS = 20_000;
const enc = encodeURIComponent;

function ifMatch(version: number | undefined): Record<string, string> | undefined {
  return version === undefined ? undefined : { "If-Match": `"${version}"` };
}

export function createVerificationApi(api: ApiClient = browserApi) {
  const get = <T>(path: string) => api.get<T>(path, { retries: 1 }).then((r) => r.data);
  const post = <T>(path: string, body?: unknown, headers?: Record<string, string>) =>
    api.post<T>(path, { body: body ?? {}, timeoutMs: WRITE_TIMEOUT_MS, headers }).then((r) => r.data);
  const patch = <T>(path: string, body: unknown, headers?: Record<string, string>) =>
    api.patch<T>(path, { body, timeoutMs: WRITE_TIMEOUT_MS, headers }).then((r) => r.data);
  const del = (path: string) => api.delete<undefined>(path, { timeoutMs: WRITE_TIMEOUT_MS }).then(() => undefined);

  return {
    // KYC (user)
    kycStatus: () => get<KycStatus>("/api/v1/kyc/status"),
    startKycCase: () => post<KycCase>("/api/v1/kyc/cases", { target_level: "IDENTITY_VERIFIED" }),
    currentKycCase: () => get<KycCase | null>("/api/v1/kyc/cases/current"),
    updateKycCase: (id: string, body: KycCasePatch, version?: number) => patch<KycCase>(`/api/v1/kyc/cases/${enc(id)}`, body, ifMatch(version)),
    submitKycCase: (id: string) => post<KycCase>(`/api/v1/kyc/cases/${enc(id)}/submit`),
    withdrawKycCase: (id: string) => post<KycCase>(`/api/v1/kyc/cases/${enc(id)}/withdraw`),

    // Documents
    listDocuments: (subjectType: SubjectType, subjectId: string) =>
      get<VerificationDocument[]>(`/api/v1/verification/documents?subject_type=${enc(subjectType)}&subject_id=${enc(subjectId)}`),
    getDocument: (id: string) => api.get<VerificationDocument>(`/api/v1/verification/documents/${enc(id)}`, { retries: 0 }).then((r) => r.data),
    documentAccess: (id: string) => post<DocumentAccess>(`/api/v1/verification/documents/${enc(id)}/access`),
    deleteDocument: (id: string) => del(`/api/v1/verification/documents/${enc(id)}`),

    // Organisations (Stage 4) and KYB
    myOrganisations: () => get<MyOrganisation[]>("/api/v1/me/organisations"),
    createOrganisation: (display_name: string, org_type: string) => post<MyOrganisation>("/api/v1/organisations", { display_name, org_type }),
    kybStatus: (orgId: string) => get<KybStatus>(`/api/v1/organisations/${enc(orgId)}/kyb`),
    startKyb: (orgId: string) => post<KybCase>(`/api/v1/organisations/${enc(orgId)}/kyb`, {}),
    updateKyb: (orgId: string, body: Record<string, unknown>) => patch<KybCase>(`/api/v1/organisations/${enc(orgId)}/kyb`, body),
    addKybPerson: (orgId: string, body: KybPersonInput) => post<KybPerson>(`/api/v1/organisations/${enc(orgId)}/kyb/persons`, body),
    removeKybPerson: (orgId: string, personId: string) => del(`/api/v1/organisations/${enc(orgId)}/kyb/persons/${enc(personId)}`),
    submitKyb: (orgId: string) => post<KybCase>(`/api/v1/organisations/${enc(orgId)}/kyb/submit`),
    withdrawKyb: (orgId: string) => post<KybCase>(`/api/v1/organisations/${enc(orgId)}/kyb/withdraw`),

    // Beneficiaries
    listBeneficiaries: () => get<Beneficiary[]>("/api/v1/beneficiaries"),
    getBeneficiary: (id: string) => get<Beneficiary>(`/api/v1/beneficiaries/${enc(id)}`),
    createBeneficiary: (body: BeneficiaryInput) => post<Beneficiary>("/api/v1/beneficiaries", body),
    updateBeneficiary: (id: string, body: Partial<BeneficiaryInput>, version?: number) =>
      patch<Beneficiary>(`/api/v1/beneficiaries/${enc(id)}`, body, ifMatch(version)),
    submitBeneficiary: (id: string) => post<Beneficiary>(`/api/v1/beneficiaries/${enc(id)}/submit`),

    // Payout destinations
    listDestinations: () => get<PayoutDestination[]>("/api/v1/payout-destinations"),
    getDestination: (id: string) => get<PayoutDestination>(`/api/v1/payout-destinations/${enc(id)}`),
    createDestination: (body: PayoutDestinationInput) => post<PayoutDestination>("/api/v1/payout-destinations", body),
    updateDestination: (id: string, body: { holder_name?: string; account_identifier?: string; bank_code?: string }, version?: number) =>
      patch<PayoutDestination>(`/api/v1/payout-destinations/${enc(id)}`, body, ifMatch(version)),
    requestDestinationVerification: (id: string, method: VerificationMethod, documentIds: string[]) =>
      post<PayoutDestination>(`/api/v1/payout-destinations/${enc(id)}/verification`, { method, document_ids: documentIds }),
    retireDestination: (id: string) => del(`/api/v1/payout-destinations/${enc(id)}`),

    // Reviewer (staff)
    reviewAction: (caseId: string, action: ReviewAction, body: Record<string, unknown> = {}) =>
      api.post<{ status?: string } | unknown>(`/api/v1/admin/verification/cases/${enc(caseId)}/${action}`, { body, timeoutMs: WRITE_TIMEOUT_MS }),
    revealIdentityNumber: (caseId: string, justification: string) =>
      post<{ id_document_number: string }>(`/api/v1/admin/verification/cases/${enc(caseId)}/reveal-identity-number`, { justification }),

    // Compliance (staff)
    complianceAction: (caseId: string, action: ComplianceAction | "notes", body: Record<string, unknown> = {}) =>
      post<unknown>(`/api/v1/admin/compliance/cases/${enc(caseId)}/${action}`, body),
  };
}

export type VerificationApi = ReturnType<typeof createVerificationApi>;

export const verificationApi: VerificationApi = createVerificationApi();
