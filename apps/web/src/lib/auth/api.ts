import { browserApi, type ApiClient } from "@/lib/api/client";

import type { LoginResult, Me, MfaEnrollment, RecoveryCodes, SecurityOverview, SessionItem, SessionState, StaffInvitationStart } from "./types";

/**
 * Browser-side calls for the authentication API (same-origin /api/v1, CSRF header added by the client).
 * Every function takes an optional client for tests. Passwords, codes and tokens are passed straight
 * through to the request body and never logged or stored.
 */
const AUTH_TIMEOUT_MS = 15_000;

export function createAuthApi(api: ApiClient = browserApi) {
  const post = <T>(path: string, body?: unknown) => api.post<T>(path, { body, timeoutMs: AUTH_TIMEOUT_MS }).then((r) => r.data);
  return {
    session: () => api.get<SessionState>("/api/v1/auth/session", { retries: 0 }).then((r) => r.data),
    register: (input: { email: string; password: string; display_name: string; accept_terms: true; age_attestation?: true }) =>
      post<{ status: "verification_sent" }>("/api/v1/auth/register", input),
    verifyEmail: (token: string) => post<{ status: "verified" }>("/api/v1/auth/verify-email", { token }),
    resendVerification: (email: string) => post<{ status: "verification_sent" }>("/api/v1/auth/resend-verification", { email }),
    login: (email: string, password: string) => post<LoginResult>("/api/v1/auth/login", { email, password }),
    mfaVerify: (code: string) => post<LoginResult>("/api/v1/auth/mfa/verify", { code }),
    mfaRecovery: (recovery_code: string) => post<LoginResult>("/api/v1/auth/mfa/recovery", { recovery_code }),
    logout: () => post<undefined>("/api/v1/auth/logout"),
    logoutAll: () => post<undefined>("/api/v1/auth/logout-all"),
    forgotPassword: (email: string) => post<{ status: "reset_requested" }>("/api/v1/auth/forgot-password", { email }),
    resetPassword: (token: string, new_password: string) =>
      post<{ status: "password_reset" }>("/api/v1/auth/reset-password", { token, new_password }),
    stepUp: (proof: { password: string } | { code: string }) =>
      post<{ step_up_expires_at: string }>("/api/v1/auth/step-up/verify", proof),
    me: () => api.get<Me>("/api/v1/me", { retries: 0 }).then((r) => r.data),
    updateProfile: (display_name: string) =>
      api.patch<Me>("/api/v1/me", { body: { display_name }, timeoutMs: AUTH_TIMEOUT_MS }).then((r) => r.data),
    changePassword: (current_password: string, new_password: string) =>
      post<undefined>("/api/v1/me/password", { current_password, new_password }),
    security: () => api.get<SecurityOverview>("/api/v1/me/security", { retries: 0 }).then((r) => r.data),
    sessions: () => api.get<SessionItem[]>("/api/v1/me/sessions", { retries: 0 }).then((r) => r.data),
    revokeSession: (id: string) =>
      api.delete<undefined>(`/api/v1/me/sessions/${encodeURIComponent(id)}`, { timeoutMs: AUTH_TIMEOUT_MS }).then((r) => r.data),
    mfaEnroll: () => post<MfaEnrollment>("/api/v1/me/mfa/enroll"),
    mfaConfirm: (enrollment_id: string, code: string) => post<RecoveryCodes>("/api/v1/me/mfa/confirm", { enrollment_id, code }),
    mfaDisable: (code: string) => post<undefined>("/api/v1/me/mfa/disable", { code }),
    regenerateRecoveryCodes: () => post<RecoveryCodes>("/api/v1/me/mfa/recovery-codes"),
    phoneVerifyRequest: (phone: string) =>
      post<{ phone_masked: string; expires_at: string }>("/api/v1/me/phone/verify-request", { phone }),
    phoneVerifyConfirm: (phone: string, code: string) =>
      post<{ phone_verified: true; phone_masked: string }>("/api/v1/me/phone/verify-confirm", { phone, code }),
    staffInvitationStart: (token: string) => post<StaffInvitationStart>("/api/v1/auth/staff-invitation/start", { token }),
    staffInvitationFinish: (input: { token: string; enrollment_id: string; code: string; password: string }) =>
      post<{ status: "activated"; recovery_codes: string[] }>("/api/v1/auth/staff-invitation/finish", input),
  };
}

export type AuthApi = ReturnType<typeof createAuthApi>;

export const authApi: AuthApi = createAuthApi();
