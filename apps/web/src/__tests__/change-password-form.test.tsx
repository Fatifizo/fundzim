import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { ChangePasswordForm } from "@/components/account/change-password-form";
import { StepUpProvider } from "@/components/auth/step-up";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";

// Stage 18 (F-S18-01): a staff password change needs a TOTP step-up first; the form asks for it and retries once.
describe("ChangePasswordForm", () => {
  it("steps up with an authenticator code when the API answers STEP_UP_REQUIRED, then retries once", async () => {
    const user = userEvent.setup();
    const changePassword = vi.fn()
      .mockRejectedValueOnce(new ApiError({ code: "STEP_UP_REQUIRED", status: 403, message: "x", retryable: false }))
      .mockResolvedValueOnce(undefined);
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-23T10:00:00Z" }));
    const api = new Proxy({}, { get: (_t, key: string) => (key === "stepUp" ? stepUp : key === "changePassword" ? changePassword : vi.fn()) }) as AuthApi;
    render(
      <StepUpProvider mfaEnabled codeOnly api={api}>
        <ChangePasswordForm email="staff@example.test" api={api} />
      </StepUpProvider>,
    );
    await user.type(screen.getByLabelText("Current password"), "current password value");
    await user.type(screen.getByLabelText("New password"), "a brand new long passphrase");
    await user.type(screen.getByLabelText("Confirm new password"), "a brand new long passphrase");
    await user.click(screen.getByRole("button", { name: "Change password" }));
    await user.type(await screen.findByLabelText("Authentication code"), "135790");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(changePassword).toHaveBeenCalledTimes(2));
    expect(stepUp).toHaveBeenCalledWith({ code: "135790" });
    expect(await screen.findByText("Password changed")).toBeInTheDocument();
  });
});
