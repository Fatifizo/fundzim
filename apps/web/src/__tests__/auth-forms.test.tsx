import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ForgotPasswordForm } from "@/components/auth/forgot-password-form";
import { LoginForm } from "@/components/auth/login-form";
import { MfaChallengeForm } from "@/components/auth/mfa-challenge-form";
import { RegisterForm } from "@/components/auth/register-form";
import { ResetPasswordForm, tokenFromFragment } from "@/components/auth/reset-password-form";
import { StepUpProvider, useStepUp } from "@/components/auth/step-up";
import { ProfileForm } from "@/components/account/profile-form";
import { MfaSettings } from "@/components/account/mfa-settings";
import { PasswordField } from "@/components/forms/password-field";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import { browserNavigation } from "@/lib/browser-navigation";
import { axeViolations } from "@/test/axe";

/** Widely published documentation example (base32), not a credential. */
const EXAMPLE_TOTP_BASE32 = "JBSWY3DP" + "EHPK3PXP";

function apiError(code: string, status: number, extra: Partial<ConstructorParameters<typeof ApiError>[0]> = {}) {
  return new ApiError({ code, status, message: "server text that must not be shown", retryable: false, ...extra });
}

function fakeApi(overrides: Partial<Record<keyof AuthApi, unknown>> = {}): AuthApi {
  const handler: ProxyHandler<object> = {
    get: (_t, key: string) => (overrides as Record<string, unknown>)[key] ?? vi.fn(async () => undefined),
  };
  return new Proxy({}, handler) as AuthApi;
}

let assign: ReturnType<typeof vi.fn>;

beforeEach(() => {
  assign = vi.fn();
  vi.spyOn(browserNavigation, "assign").mockImplementation(assign as never);
  vi.spyOn(browserNavigation, "replaceUrl").mockImplementation(() => {});
  vi.spyOn(browserNavigation, "currentPath").mockReturnValue("/settings/profile");
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("PasswordField", () => {
  it("toggles visibility with an aria-pressed button", async () => {
    const user = userEvent.setup();
    render(<PasswordField label="Password" name="password" autoComplete="current-password" />);
    const input = screen.getByLabelText("Password", { selector: "input" });
    const toggle = screen.getByRole("button", { name: "Show password" });
    expect(input).toHaveAttribute("type", "password");
    expect(input).toHaveAttribute("autocomplete", "current-password");
    expect(toggle).toHaveAttribute("aria-pressed", "false");
    expect(toggle).toHaveAttribute("aria-controls", input.id);
    await user.click(toggle);
    expect(input).toHaveAttribute("type", "text");
    expect(toggle).toHaveAttribute("aria-pressed", "true");
  });
});

describe("RegisterForm", () => {
  it("validates client-side, links errors with aria-describedby and focuses the first invalid field", async () => {
    const user = userEvent.setup();
    const register = vi.fn();
    const { container } = render(<RegisterForm api={fakeApi({ register })} />);
    await user.click(screen.getByRole("button", { name: "Create account" }));
    const email = screen.getByLabelText("Email address");
    expect(email).toHaveAttribute("aria-invalid", "true");
    expect(email).toHaveFocus();
    const describedBy = email.getAttribute("aria-describedby")!;
    expect(document.getElementById(describedBy.split(" ")[0]!)).toHaveTextContent("Enter your email address.");
    expect(screen.getByText(/You must accept the terms/)).toBeInTheDocument();
    expect(register).not.toHaveBeenCalled();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("submits and shows the generic check-your-email message", async () => {
    const user = userEvent.setup();
    const register = vi.fn(async () => ({ status: "verification_sent" as const }));
    render(<RegisterForm api={fakeApi({ register })} />);
    await user.type(screen.getByLabelText("Email address"), "a@example.test");
    await user.type(screen.getByLabelText("Display name"), "Ann");
    await user.type(screen.getByLabelText("Password", { selector: "input" }), "a long enough passphrase");
    await user.click(screen.getByRole("checkbox", { name: /I accept/ }));
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(register).toHaveBeenCalledWith({ email: "a@example.test", password: "a long enough passphrase", display_name: "Ann", accept_terms: true });
    const status = await screen.findByRole("status");
    expect(status).toHaveTextContent("Check your email");
    expect(status).toHaveTextContent("If this address can be used");
    expect(status).toHaveFocus();
  });

  it("sends the optional age declaration only when ticked", async () => {
    const user = userEvent.setup();
    const register = vi.fn(async () => ({ status: "verification_sent" as const }));
    render(<RegisterForm api={fakeApi({ register })} />);
    await user.type(screen.getByLabelText("Email address"), "a@example.test");
    await user.type(screen.getByLabelText("Display name"), "Ann");
    await user.type(screen.getByLabelText("Password", { selector: "input" }), "a long enough passphrase");
    await user.click(screen.getByRole("checkbox", { name: /I accept/ }));
    await user.click(screen.getByRole("checkbox", { name: /18 or older/ }));
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(register).toHaveBeenCalledWith({ email: "a@example.test", password: "a long enough passphrase", display_name: "Ann", accept_terms: true, age_attestation: true });
  });

  it("maps PASSWORD_POLICY_VIOLATION details to the password field", async () => {
    const user = userEvent.setup();
    const register = vi.fn(async () => {
      throw apiError("PASSWORD_POLICY_VIOLATION", 422, { details: [{ field: "password", code: "BREACHED" }] });
    });
    render(<RegisterForm api={fakeApi({ register })} />);
    await user.type(screen.getByLabelText("Email address"), "a@example.test");
    await user.type(screen.getByLabelText("Display name"), "Ann");
    await user.type(screen.getByLabelText("Password", { selector: "input" }), "password12345");
    await user.click(screen.getByRole("checkbox", { name: /I accept/ }));
    await user.click(screen.getByRole("button", { name: "Create account" }));
    const field = screen.getByLabelText("Password", { selector: "input" });
    await waitFor(() => expect(field).toHaveAttribute("aria-invalid", "true"));
    expect(screen.getByText(/appeared in a data breach/)).toBeInTheDocument();
  });
});

describe("LoginForm", () => {
  async function submit(user: ReturnType<typeof userEvent.setup>) {
    await user.type(screen.getByLabelText("Email address"), "a@example.test");
    await user.type(screen.getByLabelText("Password", { selector: "input" }), "secret password");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
  }

  it("uses password-manager autocomplete attributes", () => {
    render(<LoginForm api={fakeApi()} />);
    expect(screen.getByLabelText("Email address")).toHaveAttribute("autocomplete", "username");
    expect(screen.getByLabelText("Password", { selector: "input" })).toHaveAttribute("autocomplete", "current-password");
  });

  it("navigates to the validated next path on success", async () => {
    const user = userEvent.setup();
    render(<LoginForm next="/settings/mfa" api={fakeApi({ login: vi.fn(async () => ({ status: "authenticated", user: {} })) })} />);
    await submit(user);
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/settings/mfa"));
  });

  it("never follows an off-site next", async () => {
    const user = userEvent.setup();
    render(<LoginForm next="//evil.example" api={fakeApi({ login: vi.fn(async () => ({ status: "authenticated", user: {} })) })} />);
    await submit(user);
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/dashboard"));
  });

  it("continues to the MFA step on mfa_required, keeping next", async () => {
    const user = userEvent.setup();
    render(<LoginForm next="/settings/security" api={fakeApi({ login: vi.fn(async () => ({ status: "mfa_required", methods: ["totp"] })) })} />);
    await submit(user);
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/login/mfa?next=%2Fsettings%2Fsecurity"));
  });

  it("shows one generic message for INVALID_CREDENTIALS (never the server text) and focuses it", async () => {
    const user = userEvent.setup();
    render(<LoginForm api={fakeApi({ login: vi.fn(async () => Promise.reject(apiError("INVALID_CREDENTIALS", 401))) })} />);
    await submit(user);
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("The email address or password is incorrect.");
    expect(alert).not.toHaveTextContent("server text");
    expect(alert).toHaveFocus();
    expect(assign).not.toHaveBeenCalled();
  });

  it("shows the Retry-After wait for 429", async () => {
    const user = userEvent.setup();
    render(<LoginForm api={fakeApi({ login: vi.fn(async () => Promise.reject(apiError("RATE_LIMITED", 429, { retryAfterMs: 45_000 }))) })} />);
    await submit(user);
    expect(await screen.findByRole("alert")).toHaveTextContent("Please wait 45 seconds and try again.");
  });
});

describe("MfaChallengeForm", () => {
  it("verifies a TOTP code, then navigates", async () => {
    const user = userEvent.setup();
    const mfaVerify = vi.fn(async () => ({ status: "authenticated" }));
    render(<MfaChallengeForm next="/dashboard" api={fakeApi({ mfaVerify })} />);
    const input = screen.getByLabelText("Authentication code");
    expect(input).toHaveAttribute("autocomplete", "one-time-code");
    expect(input).toHaveAttribute("inputmode", "numeric");
    await user.type(input, "123 456");
    await user.click(screen.getByRole("button", { name: "Verify and sign in" }));
    expect(mfaVerify).toHaveBeenCalledWith("123456");
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/dashboard"));
  });

  it("switches to a recovery code and uses the recovery endpoint", async () => {
    const user = userEvent.setup();
    const mfaRecovery = vi.fn(async () => ({ status: "authenticated" }));
    render(<MfaChallengeForm api={fakeApi({ mfaRecovery })} />);
    await user.click(screen.getByRole("button", { name: "Use a recovery code instead" }));
    const input = screen.getByLabelText("Recovery code");
    expect(input).toHaveFocus();
    await user.type(input, "ABCD-EFGH");
    await user.click(screen.getByRole("button", { name: "Verify and sign in" }));
    expect(mfaRecovery).toHaveBeenCalledWith("ABCD-EFGH");
  });

  it("invalid code → generic error; expired challenge → sign in again", async () => {
    const user = userEvent.setup();
    const mfaVerify = vi
      .fn()
      .mockRejectedValueOnce(apiError("MFA_CODE_INVALID", 401))
      .mockRejectedValueOnce(apiError("MFA_CHALLENGE_EXPIRED", 401));
    render(<MfaChallengeForm api={fakeApi({ mfaVerify })} />);
    await user.type(screen.getByLabelText("Authentication code"), "000000");
    await user.click(screen.getByRole("button", { name: "Verify and sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("That code is not valid.");
    await user.click(screen.getByRole("button", { name: "Verify and sign in" }));
    expect(await screen.findByRole("link", { name: "Sign in again" })).toHaveAttribute("href", "/login");
  });
});

describe("ForgotPasswordForm", () => {
  it("shows the same generic message after any accepted request", async () => {
    const user = userEvent.setup();
    const forgotPassword = vi.fn(async () => ({ status: "reset_requested" }));
    render(<ForgotPasswordForm api={fakeApi({ forgotPassword })} />);
    await user.type(screen.getByLabelText("Email address"), "nobody@example.test");
    await user.click(screen.getByRole("button", { name: "Send reset link" }));
    expect(await screen.findByRole("status")).toHaveTextContent("If an account exists for that address");
  });
});

describe("ResetPasswordForm", () => {
  it("parses the fragment token", () => {
    expect(tokenFromFragment("#token=abc&x=1")).toBe("abc");
    expect(tokenFromFragment("")).toBeNull();
  });

  it("removes the token from the URL, checks confirmation, and handles TOKEN_INVALID generically", async () => {
    const user = userEvent.setup();
    const resetPassword = vi.fn(async () => Promise.reject(apiError("TOKEN_INVALID", 400)));
    render(<ResetPasswordForm queryToken="tok" api={fakeApi({ resetPassword })} />);
    await waitFor(() => expect(browserNavigation.replaceUrl).toHaveBeenCalledWith("/reset-password"));
    await user.type(screen.getByLabelText("New password", { selector: "input" }), "a long enough passphrase");
    await user.type(screen.getByLabelText("Confirm new password", { selector: "input" }), "different passphrase!");
    await user.click(screen.getByRole("button", { name: "Set new password" }));
    expect(screen.getByText("The passwords do not match.")).toBeInTheDocument();
    expect(resetPassword).not.toHaveBeenCalled();
    await user.clear(screen.getByLabelText("Confirm new password", { selector: "input" }));
    await user.type(screen.getByLabelText("Confirm new password", { selector: "input" }), "a long enough passphrase");
    await user.click(screen.getByRole("button", { name: "Set new password" }));
    expect(resetPassword).toHaveBeenCalledWith("tok", "a long enough passphrase");
    expect(await screen.findByText("This reset link is invalid or has expired.")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Request a new reset link" })).toHaveAttribute("href", "/forgot-password");
  });
});

describe("session expiry", () => {
  it("AUTHENTICATION_REQUIRED redirects to login preserving the current path", async () => {
    const user = userEvent.setup();
    const updateProfile = vi.fn(async () => Promise.reject(apiError("AUTHENTICATION_REQUIRED", 401)));
    render(<ProfileForm displayName="Ann" api={fakeApi({ updateProfile })} />);
    await user.click(screen.getByRole("button", { name: "Save display name" }));
    await waitFor(() => expect(assign).toHaveBeenCalledWith("/login?next=%2Fsettings%2Fprofile"));
  });
});

describe("step-up", () => {
  function Probe({ op }: { op: () => Promise<string> }) {
    const withStepUp = useStepUp();
    return (
      <button type="button" onClick={() => withStepUp(op).then((v) => document.body.setAttribute("data-result", v), () => document.body.setAttribute("data-result", "rejected"))}>
        Run
      </button>
    );
  }

  it("on STEP_UP_REQUIRED asks for the password, verifies, and retries once", async () => {
    const user = userEvent.setup();
    const op = vi.fn().mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403)).mockResolvedValueOnce("done");
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "x" }));
    render(
      <StepUpProvider mfaEnabled={false} api={fakeApi({ stepUp })}>
        <Probe op={op} />
      </StepUpProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Run" }));
    const dialog = await screen.findByRole("dialog", { name: "Confirm it's you" });
    const password = screen.getByLabelText("Password", { selector: "input" });
    expect(dialog).toContainElement(password);
    await user.type(password, "my password");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(document.body.getAttribute("data-result")).toBe("done"));
    expect(stepUp).toHaveBeenCalledWith({ password: "my password" });
    expect(op).toHaveBeenCalledTimes(2);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("staff (codeOnly) step-up asks for a code; cancelling rejects", async () => {
    const user = userEvent.setup();
    const op = vi.fn().mockRejectedValue(apiError("STEP_UP_REQUIRED", 403));
    render(
      <StepUpProvider mfaEnabled codeOnly api={fakeApi()}>
        <Probe op={op} />
      </StepUpProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Run" }));
    expect(await screen.findByLabelText("Authentication code")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Use my password instead" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(document.body.getAttribute("data-result")).toBe("rejected"));
  });
});

describe("MfaSettings enrolment", () => {
  it("shows QR + key, confirms, then shows recovery codes once and drops the secret", async () => {
    const user = userEvent.setup();
    const mfaEnroll = vi.fn(async () => ({ enrollment_id: "e1", secret: EXAMPLE_TOTP_BASE32, otpauth_uri: "otpauth://totp/FundZim:a?secret=JBSWY3DPEHPK3PXP" }));
    const mfaConfirm = vi.fn(async () => ({ recovery_codes: ["AAAA-1111", "BBBB-2222"] }));
    const logSpy = vi.spyOn(console, "log");
    render(
      <StepUpProvider mfaEnabled={false} api={fakeApi()}>
        <MfaSettings mfaEnabled={false} recoveryCodesRemaining={0} api={fakeApi({ mfaEnroll, mfaConfirm })} />
      </StepUpProvider>,
    );
    await user.click(screen.getByRole("button", { name: "Set up two-step verification" }));
    expect(await screen.findByRole("img", { name: /QR code/ })).toBeInTheDocument();
    expect(screen.getByLabelText("Set-up key")).toHaveTextContent("JBSW Y3DP EHPK 3PXP");
    await user.type(screen.getByLabelText("Code from your authenticator app"), "123456");
    await user.click(screen.getByRole("button", { name: "Turn on two-step verification" }));
    expect(mfaConfirm).toHaveBeenCalledWith("e1", "123456");
    expect(await screen.findByRole("list", { name: "Recovery codes" })).toHaveTextContent("AAAA-1111");
    expect(screen.queryByLabelText("Set-up key")).toBeNull();
    expect(document.body.innerHTML).not.toContain(EXAMPLE_TOTP_BASE32);
    await user.click(screen.getByRole("button", { name: "I have saved my codes" }));
    expect(screen.queryByRole("list", { name: "Recovery codes" })).toBeNull();
    expect(logSpy).not.toHaveBeenCalled();
  });
});
