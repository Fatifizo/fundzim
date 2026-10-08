import { ClientErrorCode, isApiError } from "@/lib/api/errors";

import { AuthErrorCode } from "./types";

/**
 * Maps API errors to user-facing copy. Messages are deliberately generic where the contract requires it
 * (no account enumeration: login, register, forgot-password, resend). Branches only on `code`, never on
 * the server's message text, and never echoes server messages (which may change or leak detail).
 */
export interface DescribedError {
  message: string;
  /** Field-level errors keyed by request field name (from VALIDATION_FAILED / policy details). */
  fieldErrors: Record<string, string>;
  /** Present for 429: seconds to wait. */
  retryAfterSeconds?: number;
  code: string;
}

export const PASSWORD_RULES = "Use at least 12 characters. Longer passphrases are best; avoid passwords used on other sites.";

const PASSWORD_DETAIL_MESSAGES: Record<string, string> = {
  TOO_SHORT: "This password is too short. Use at least 12 characters.",
  TOO_LONG: "This password is too long.",
  BREACHED: "This password has appeared in a data breach. Choose a different one.",
  COMMON: "This password is too common. Choose a different one.",
  SAME_AS_CURRENT: "Choose a password you have not used for this account.",
  COMMONLY_USED: "This password is too common. Choose a different one.",
  TOO_REPETITIVE: "This password is too repetitive. Choose a less predictable one.",
  INVALID_CHARACTERS: "This password contains characters that cannot be used.",
  CONTAINS_PERSONAL_INFO: "Don't use your name or email address in your password.",
};

const FIELD_DETAIL_MESSAGES: Record<string, string> = {
  REQUIRED: "This field is required.",
  INVALID_FORMAT: "This value is not in the right format.",
  INVALID: "This value is not valid.",
  TOO_LONG: "This value is too long.",
  TOO_SHORT: "This value is too short.",
  INVALID_LENGTH: "This value is too short or too long.",
  INVALID_EMAIL: "Enter an email address in the format name@example.com.",
  INVALID_PHONE: "Enter a valid phone number.",
  INVALID_VALUE: "This value is not valid.",
  MUST_ACCEPT: "You must accept this to continue.",
};

export function formatWait(seconds: number): string {
  if (seconds < 60) return `${seconds} second${seconds === 1 ? "" : "s"}`;
  const minutes = Math.ceil(seconds / 60);
  return `${minutes} minute${minutes === 1 ? "" : "s"}`;
}

export function describeError(error: unknown, fallback = "Something went wrong. Please try again."): DescribedError {
  if (!isApiError(error)) return { message: fallback, fieldErrors: {}, code: "UNKNOWN" };
  const fieldErrors: Record<string, string> = {};
  const base = { fieldErrors, code: error.code };

  switch (error.code) {
    case AuthErrorCode.RATE_LIMITED: {
      const seconds = error.retryAfterMs !== undefined ? Math.max(1, Math.ceil(error.retryAfterMs / 1000)) : undefined;
      return {
        ...base,
        retryAfterSeconds: seconds,
        message: seconds
          ? `Too many attempts. Please wait ${formatWait(seconds)} and try again.`
          : "Too many attempts. Please wait a little and try again.",
      };
    }
    case AuthErrorCode.INVALID_CREDENTIALS:
      return { ...base, message: "The email address or password is incorrect." };
    case AuthErrorCode.ACCOUNT_SUSPENDED:
      return { ...base, message: "This account is suspended. Please contact us if you think this is a mistake." };
    case AuthErrorCode.MFA_CODE_INVALID:
      return { ...base, message: "That code is not valid. Check the code and try again." };
    case AuthErrorCode.OTP_INVALID:
      return { ...base, message: "That code is not valid or has expired. Check the code or request a new one." };
    case AuthErrorCode.PHONE_IN_USE:
      return { ...base, message: "This phone number cannot be used. Please use a different number." };
    case AuthErrorCode.EMAIL_NOT_VERIFIED:
      return { ...base, message: "Please confirm your email address first." };
    case AuthErrorCode.MFA_CHALLENGE_EXPIRED:
      return { ...base, message: "Your sign-in attempt has expired. Please sign in again." };
    case AuthErrorCode.MFA_REQUIRED_FOR_ROLE:
      return { ...base, message: "Two-step verification is required for your account and cannot be turned off." };
    case AuthErrorCode.TOKEN_INVALID:
      return { ...base, message: "This link is invalid or has expired." };
    case AuthErrorCode.AUTHENTICATION_REQUIRED:
      return { ...base, message: "Your session has ended. Please sign in again." };
    case AuthErrorCode.STEP_UP_REQUIRED:
      return { ...base, message: "Please confirm it's you to continue." };
    case AuthErrorCode.CSRF_TOKEN_INVALID:
    case AuthErrorCode.CSRF_ORIGIN_MISMATCH:
      return { ...base, message: "Your security check failed. Reload the page and try again." };
    case AuthErrorCode.PASSWORD_POLICY_VIOLATION: {
      let first: string | undefined;
      for (const detail of error.details) {
        const text = PASSWORD_DETAIL_MESSAGES[detail.code] ?? "This password does not meet the requirements.";
        const field = detail.field || "password";
        fieldErrors[field] ??= text;
        first ??= text;
      }
      return { ...base, message: first ?? `This password does not meet the requirements. ${PASSWORD_RULES}` };
    }
    case AuthErrorCode.VALIDATION_FAILED: {
      for (const detail of error.details) {
        if (detail.field) fieldErrors[detail.field] ??= FIELD_DETAIL_MESSAGES[detail.code] ?? "This value is not valid.";
      }
      return { ...base, message: "Please check the highlighted fields." };
    }
    case ClientErrorCode.NETWORK_ERROR:
      return { ...base, message: "We couldn't reach FundZim. Check your connection and try again." };
    case ClientErrorCode.TIMEOUT:
      return { ...base, message: "FundZim did not respond in time. Please check whether your change was applied before trying again." };
    case "SERVICE_UNAVAILABLE":
      return { ...base, message: "FundZim is temporarily unavailable. Please try again shortly." };
    default:
      return { ...base, message: fallback };
  }
}

export function isAuthRequired(error: unknown): boolean {
  return isApiError(error) && error.code === AuthErrorCode.AUTHENTICATION_REQUIRED;
}

export function isStepUpRequired(error: unknown): boolean {
  return isApiError(error) && error.code === AuthErrorCode.STEP_UP_REQUIRED;
}
