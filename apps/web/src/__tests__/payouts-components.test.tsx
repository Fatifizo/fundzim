import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { ChecksTable, PayoutBanner, PayoutsTable } from "@/components/admin/finance/payouts";
import { PayoutAction } from "@/components/admin/finance/payout-action";
import { StepUpProvider } from "@/components/auth/step-up";
import { OwnerPayoutList, TestFundsBanner } from "@/components/payouts/common";
import { CancelWithdrawal, WithdrawalForm } from "@/components/payouts/withdrawal-form";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import type { PayoutsApi } from "@/lib/payouts/api";
import { readOwnerPayout, readStaffPayout } from "@/lib/payouts/normalise";
import { parseWithdrawalAmount } from "@/lib/payouts/money";
import { axeViolations } from "@/test/axe";

function apiError(code: string, status: number, details: { field: string; code: string }[] = []) {
  return new ApiError({ code, status, message: "server text that must not be shown", retryable: false, details });
}

function fakeApi(overrides: Partial<Record<keyof PayoutsApi, unknown>> = {}): PayoutsApi {
  return new Proxy({}, { get: (_t, key: string) => (overrides as Record<string, unknown>)[key] ?? vi.fn(async () => undefined) }) as PayoutsApi;
}

function fakeAuth(stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-14T10:00:00Z" }))): AuthApi {
  return new Proxy({}, { get: (_t, key: string) => (key === "stepUp" ? stepUp : vi.fn()) }) as AuthApi;
}

function staffShell(ui: ReactNode, auth: AuthApi = fakeAuth()) {
  return (
    <StepUpProvider mfaEnabled codeOnly api={auth}>
      {ui}
    </StepUpProvider>
  );
}

function ownerShell(ui: ReactNode, auth: AuthApi = fakeAuth()) {
  return (
    <StepUpProvider mfaEnabled={false} api={auth}>
      {ui}
    </StepUpProvider>
  );
}

const PAYOUT_ID = "0192a000-0000-7000-8000-0000000000aa";
const destinations = [{ id: "d1", label: "EcoCash 07*****567 (Chipo Ncube)" }];

describe("payout banners and lists", () => {
  it("label everything TEST_ONLY and production payouts BLOCKED; amounts stay exact strings", async () => {
    const staff = readStaffPayout({ id: PAYOUT_ID, amount_minor: "9007199254740993", currency: "USD", status: "PENDING_REVIEW", test_payout: true, approval_policy: "DUAL" })!;
    const owner = readOwnerPayout({ id: PAYOUT_ID, amount_minor: "12550", currency: "USD", status: "PENDING_REVIEW", status_label: "Under review", test_payout: true, funds_label: "REAL" })!;
    expect(owner.funds_label).toBe("TEST_ONLY"); // never upgraded to real funds by a payload
    const { container } = render(
      <>
        <PayoutBanner />
        <TestFundsBanner />
        <PayoutsTable items={[staff]} caption="Withdrawal requests" />
        <OwnerPayoutList items={[owner]} />
        <ChecksTable checks={[{ check_id: "EC-01", result: "PASS", detail_code: "OK" }, { check_id: "EC-14", result: "REVIEW", detail_code: "LIMIT_UNCONFIGURED" }]} caption="Eligibility checks" />
      </>,
    );
    expect(container).toHaveTextContent("TEST PAYOUTS ONLY");
    expect(container).toHaveTextContent("Test withdrawals only");
    expect(screen.getAllByText(/Production payouts:\s*BLOCKED/).length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("TEST_ONLY").length).toBeGreaterThanOrEqual(3);
    expect(container).toHaveTextContent("90,071,992,547,409.93"); // beyond Number precision: string arithmetic only
    expect(container).toHaveTextContent("125.50");
    expect(container).toHaveTextContent("Under review");
    expect(container).not.toHaveTextContent(/withdrawable balance/i);
    expect(await axeViolations(container)).toEqual([]);
  });
});

describe("parseWithdrawalAmount", () => {
  it("converts decimals to minor units without floats and checks the available balance with BigInt", () => {
    expect(parseWithdrawalAmount("125.50", "USD", "50000")).toEqual({ ok: true, amountMinor: "12550" });
    expect(parseWithdrawalAmount("0.29", "USD", "50000")).toEqual({ ok: true, amountMinor: "29" });
    expect(parseWithdrawalAmount("500.01", "USD", "50000")).toEqual({ ok: false, message: "This is more than the available test balance." });
    expect(parseWithdrawalAmount("1.234", "USD")).toMatchObject({ ok: false });
    expect(parseWithdrawalAmount("", "USD")).toMatchObject({ ok: false });
    expect(parseWithdrawalAmount("10", "XXX")).toMatchObject({ ok: false });
  });
});

describe("WithdrawalForm", () => {
  it("validates, steps up with the password, and reuses one Idempotency-Key for a retried identical request", async () => {
    const user = userEvent.setup();
    const requestPayout = vi
      .fn()
      .mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403))
      .mockRejectedValueOnce(apiError("TIMEOUT", 0))
      .mockResolvedValueOnce({ id: PAYOUT_ID });
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "x" }));
    const { container } = render(
      ownerShell(<WithdrawalForm campaignId="c1" currency="USD" availableMinor="50000" destinations={destinations} enabled api={fakeApi({ requestPayout })} />, fakeAuth(stepUp)),
    );
    expect(await axeViolations(container)).toEqual([]);
    const form = screen.getByRole("form", { name: "Request a test withdrawal" });
    await user.type(within(form).getByLabelText("Amount (USD)"), "600");
    await user.click(screen.getByRole("button", { name: "Request test withdrawal" }));
    expect(await screen.findByText("This is more than the available test balance.")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("Please correct the errors below.")).toHaveFocus());
    expect(requestPayout).not.toHaveBeenCalled();

    await user.clear(within(form).getByLabelText("Amount (USD)"));
    await user.type(within(form).getByLabelText("Amount (USD)"), "125.50");
    await user.click(screen.getByRole("button", { name: "Request test withdrawal" }));
    await user.type(await screen.findByLabelText("Password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(requestPayout).toHaveBeenCalledTimes(2));
    expect(stepUp).toHaveBeenCalledWith({ password: "correct horse battery staple" });
    // the timed-out request's outcome is unknown: retrying the same values must reuse its key
    await waitFor(() => expect(screen.getByRole("button", { name: "Request test withdrawal" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "Request test withdrawal" }));
    await waitFor(() => expect(requestPayout).toHaveBeenCalledTimes(3));
    const calls = requestPayout.mock.calls;
    expect(calls[0]![1]).toEqual({ currency: "USD", amount_minor: "12550", destination_id: "d1", purpose: null });
    expect(calls[0]![2]).toMatch(/^[0-9a-f-]{36}$/);
    expect(calls[1]![2]).toBe(calls[0]![2]);
    expect(calls[2]![2]).toBe(calls[0]![2]);
    expect(await screen.findByRole("status")).toHaveTextContent("Withdrawal requested. A finance officer will review it.");
  });

  it("shows plain-language refusals only (no compliance or risk detail, no server text)", async () => {
    const user = userEvent.setup();
    const requestPayout = vi.fn().mockRejectedValue(apiError("PAYOUT_NOT_ELIGIBLE", 422, [{ field: "reason_category", code: "INSUFFICIENT_FUNDS" }]));
    render(ownerShell(<WithdrawalForm campaignId="c1" currency="USD" availableMinor="50000" destinations={destinations} enabled api={fakeApi({ requestPayout })} />));
    await user.type(screen.getByLabelText("Amount (USD)"), "10");
    await user.click(screen.getByRole("button", { name: "Request test withdrawal" }));
    expect(await screen.findByText("There aren't enough settled funds available for this amount.")).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("server text that must not be shown");
    expect(document.body).not.toHaveTextContent(/compliance|risk/i);
  });

  it("offers nothing without an eligible payout account or when withdrawals are disabled", () => {
    const { rerender } = render(ownerShell(<WithdrawalForm campaignId="c1" currency="USD" availableMinor="50000" destinations={[]} enabled api={fakeApi()} />));
    expect(screen.queryByRole("form")).not.toBeInTheDocument();
    expect(screen.getByText(/Add and verify a payout account first/)).toBeInTheDocument();
    rerender(ownerShell(<WithdrawalForm campaignId="c1" currency="USD" availableMinor="50000" destinations={destinations} enabled={false} api={fakeApi()} />));
    expect(screen.getByText("Withdrawals are not available in this environment.")).toBeInTheDocument();
  });

  it("cancel keeps its confirmation after the refresh and renders nothing when not available", async () => {
    const user = userEvent.setup();
    const cancelOwn = vi.fn(async () => ({}));
    const { rerender } = render(ownerShell(<CancelWithdrawal id={PAYOUT_ID} available api={fakeApi({ cancelOwn })} />));
    await user.click(screen.getByRole("button", { name: "Cancel withdrawal" }));
    expect(cancelOwn).toHaveBeenCalledWith(PAYOUT_ID);
    expect(await screen.findByRole("status")).toHaveTextContent("Withdrawal cancelled.");
    rerender(ownerShell(<CancelWithdrawal id={PAYOUT_ID} available={false} api={fakeApi({ cancelOwn })} />));
    expect(screen.getByRole("status")).toHaveTextContent("Withdrawal cancelled.");
    expect(screen.queryByRole("button", { name: "Cancel withdrawal" })).not.toBeInTheDocument();
  });
});

describe("PayoutAction", () => {
  it("approve requires a reason and the conflict attestation, steps up and retries once", async () => {
    const user = userEvent.setup();
    const staffAction = vi.fn().mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403)).mockResolvedValueOnce({});
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "x" }));
    render(staffShell(<PayoutAction target={{ kind: "approve", id: PAYOUT_ID }} api={fakeApi({ staffAction })} />, fakeAuth(stepUp)));
    await user.click(screen.getByRole("button", { name: "Approve payout" }));
    const dialog = screen.getByRole("dialog", { name: "Approve this payout?" });
    expect(await axeViolations(dialog)).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    expect(within(dialog).getByText("Write 5 to 2,000 characters.")).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Checks reviewed and amount matches.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    expect(within(dialog).getByRole("alert")).toHaveTextContent("no conflict of interest");
    expect(staffAction).not.toHaveBeenCalled();
    await user.click(within(dialog).getByLabelText(/I have no conflict of interest/));
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    await user.type(await screen.findByLabelText("Authentication code"), "246810");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(staffAction).toHaveBeenCalledTimes(2));
    expect(staffAction).toHaveBeenLastCalledWith(PAYOUT_ID, "approve", "Checks reviewed and amount matches.");
    expect(await screen.findByRole("status")).toHaveTextContent("Approval recorded.");
  });

  it("maps self-approval and repeat decisions without echoing server text", async () => {
    const user = userEvent.setup();
    const staffAction = vi.fn().mockRejectedValueOnce(apiError("SELF_APPROVAL_FORBIDDEN", 403)).mockRejectedValueOnce(apiError("ALREADY_DECIDED", 409));
    render(staffShell(<PayoutAction target={{ kind: "reject", id: PAYOUT_ID }} api={fakeApi({ staffAction })} />));
    await user.click(screen.getByRole("button", { name: "Reject payout" }));
    const dialog = screen.getByRole("dialog", { name: "Reject this payout?" });
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Destination looks wrong.");
    await user.click(within(dialog).getByLabelText(/I have no conflict of interest/));
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Reject" }));
    expect(await within(dialog).findByText(/You can't decide this payout/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Reject" }));
    expect(await within(dialog).findByText(/already decided this payout/)).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("server text that must not be shown");
  });

  it("resolve sends the note and resolution code; no action marks a payout paid or failed", async () => {
    const user = userEvent.setup();
    const exceptionAction = vi.fn(async () => ({}));
    render(staffShell(<PayoutAction target={{ kind: "resolve", id: "e1" }} api={fakeApi({ exceptionAction })} />));
    await user.click(screen.getByRole("button", { name: "Resolve" }));
    const dialog = screen.getByRole("dialog", { name: "Resolve this exception?" });
    expect(within(dialog).queryByLabelText(/conflict of interest/)).not.toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Note (staff only)"), "Provider confirmed the status.");
    await user.selectOptions(within(dialog).getByLabelText("Resolution"), "PROVIDER_CONFIRMED");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Resolve" }));
    await waitFor(() => expect(exceptionAction).toHaveBeenCalledWith("e1", "resolve", "Provider confirmed the status.", "PROVIDER_CONFIRMED"));
    expect(document.body).not.toHaveTextContent(/mark(ed)? (as )?(paid|failed)/i);
  });
});
