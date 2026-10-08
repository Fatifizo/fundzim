/** Date-time display in Africa/Harare (CLAUDE.md: UTC internally, display in Harare or user preference). */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat("en-GB", {
    dateStyle: "medium",
    timeStyle: "short",
    timeZone: "Africa/Harare",
  }).format(date);
}

const EVENT_LABELS: Record<string, string> = {
  login: "Signed in",
  login_succeeded: "Signed in",
  login_failed: "Failed sign-in attempt",
  logout: "Signed out",
  password_changed: "Password changed",
  password_reset: "Password reset",
  mfa_enabled: "Two-step verification turned on",
  mfa_disabled: "Two-step verification turned off",
  recovery_codes_regenerated: "New recovery codes created",
  email_verified: "Email address confirmed",
  phone_verified: "Phone number confirmed",
  session_revoked: "A session was signed out",
  logout_all: "Signed out everywhere",
  login_blocked: "Sign-in blocked",
  mfa_enrolled: "Two-step verification turned on",
  mfa_removed: "Two-step verification turned off",
  mfa_failed: "Failed two-step verification attempt",
  recovery_code_used: "Recovery code used",
  password_reset_requested: "Password reset requested",
  step_up_succeeded: "Identity confirmed for a sensitive change",
  step_up_failed: "Failed identity confirmation",
};

export function describeSecurityEvent(type: string): string {
  return EVENT_LABELS[type.toLowerCase()] ?? type.replace(/[_.]+/g, " ").replace(/^\w/, (c) => c.toUpperCase());
}
