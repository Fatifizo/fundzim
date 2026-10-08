/**
 * Wire types for the Stage 4 authentication API (docs/stage-4/interface-contracts.md §4.2). Hand-written
 * from the contract until OpenAPI type generation exists (KI-14); field names must match the contract.
 */
export interface Me {
  id: string;
  account_kind: string;
  email: string;
  email_verified: boolean;
  display_name: string;
  phone_masked: string | null;
  phone_verified: boolean;
  mfa_enabled: boolean;
  /** Staff only. */
  roles?: string[];
  created_at: string;
}

export interface SessionState {
  authenticated: boolean;
  user?: Me;
  mfa_pending?: boolean;
}

export type LoginResult =
  | { status: "authenticated"; user: Me }
  | { status: "mfa_required"; methods: Array<"totp" | "recovery_code" | string> };

export interface SecurityOverview {
  email_verified: boolean;
  phone_verified: boolean;
  mfa_enabled: boolean;
  recovery_codes_remaining: number;
  password_changed_at: string | null;
  recent_events: Array<{ type: string; occurred_at: string }>;
}

export interface SessionItem {
  id: string;
  current: boolean;
  created_at: string;
  last_seen_at: string;
  ip_masked: string;
  user_agent: string;
  auth_method: string;
  mfa: boolean;
}

export interface MfaEnrollment {
  enrollment_id: string;
  secret: string;
  otpauth_uri: string;
}

export interface RecoveryCodes {
  recovery_codes: string[];
}

/** API error codes the frontend branches on (stable UPPER_SNAKE codes from the contract). */
export const AuthErrorCode = {
  AUTHENTICATION_REQUIRED: "AUTHENTICATION_REQUIRED",
  STEP_UP_REQUIRED: "STEP_UP_REQUIRED",
  INVALID_CREDENTIALS: "INVALID_CREDENTIALS",
  ACCOUNT_SUSPENDED: "ACCOUNT_SUSPENDED",
  RATE_LIMITED: "RATE_LIMITED",
  MFA_CODE_INVALID: "MFA_CODE_INVALID",
  MFA_CHALLENGE_EXPIRED: "MFA_CHALLENGE_EXPIRED",
  MFA_REQUIRED_FOR_ROLE: "MFA_REQUIRED_FOR_ROLE",
  TOKEN_INVALID: "TOKEN_INVALID",
  PASSWORD_POLICY_VIOLATION: "PASSWORD_POLICY_VIOLATION",
  VALIDATION_FAILED: "VALIDATION_FAILED",
  CSRF_TOKEN_INVALID: "CSRF_TOKEN_INVALID",
  CSRF_ORIGIN_MISMATCH: "CSRF_ORIGIN_MISMATCH",
  OTP_INVALID: "OTP_INVALID",
  PHONE_IN_USE: "PHONE_IN_USE",
  EMAIL_NOT_VERIFIED: "EMAIL_NOT_VERIFIED",
} as const;

/** POST /auth/staff-invitation/start (lead's addendum to interface-contracts §4). */
export interface StaffInvitationStart {
  email: string;
  display_name: string;
  enrollment_id: string;
  secret: string;
  otpauth_uri: string;
}
