import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { FeeAction } from "@/components/admin/finance/fee-action";
import { AdjustmentForm, CreatePolicyForm, ScheduleForm, VersionForm } from "@/components/admin/finance/fee-forms";
import { AdjustmentsTable, AssessmentsTable, FeeBanner, QuotesTable } from "@/components/admin/finance/fees";
import { StepUpProvider } from "@/components/auth/step-up";
import { FeeTestBanner, PayoutFeeFacts } from "@/components/fees/common";
import { DonationQuotePreview, QuoteBreakdown } from "@/components/fees/quote-preview";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import type { FeesApi } from "@/lib/fees/api";
import { amountToMinor, bpsToPercent, describeTerms, percentToBps } from "@/lib/fees/labels";
import { readAdjustment, readAssessment, readQuote, readSchedule, readVersion } from "@/lib/fees/normalise";
import { axeViolations } from "@/test/axe";

function apiError(code: string, status: number) {
  return new ApiError({ code, status, message: "server text that must not be shown", retryable: false, details: [] });
}

function fakeApi(overrides: Partial<Record<keyof FeesApi, unknown>> = {}): FeesApi {
  return new Proxy({}, { get: (_t, key: string) => (overrides as Record<string, unknown>)[key] ?? vi.fn(async () => undefined) }) as FeesApi;
}

function fakeAuth(stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-15T10:00:00Z" }))): AuthApi {
  return new Proxy({}, { get: (_t, key: string) => (key === "stepUp" ? stepUp : vi.fn()) }) as AuthApi;
}

const staffShell = (ui: ReactNode, auth: AuthApi = fakeAuth()) => (
  <StepUpProvider mfaEnabled codeOnly api={auth}>
    {ui}
  </StepUpProvider>
);
const ownerShell = (ui: ReactNode) => (
  <StepUpProvider mfaEnabled={false} api={fakeAuth()}>
    {ui}
  </StepUpProvider>
);

const VERSION_ID = "0192a000-0000-7000-8000-0000000000f1";
const CAMPAIGN_ID = "0192a000-0000-7000-8000-0000000000c1";
const HASH = "a".repeat(64);

const QUOTE = {
  id: "0192a000-0000-7000-8000-0000000000q1".replace("q", "a"),
  campaign_id: CAMPAIGN_ID,
  currency: "USD",
  amount_minor: "10000",
  payment_method: null,
  platform_fee_minor: "500",
  platform_fee_basis: "QUOTED",
  provider_fee_minor: null,
  provider_fee_basis: "UNKNOWN",
  donor_charge_minor: "10000",
  campaign_allocation_minor: null,
  platform_net_minor: null,
  components: [{ category: "PLATFORM_FEE", basis: "QUOTED", amount_minor: "500", responsibility: "ORGANISER_COVERED" }],
  status: "ACTIVE",
  expires_at: "2026-10-15T10:15:00Z",
  notice: "A fee quote is an estimate for a TEST donation. It is not a charge, and nothing has been paid.",
};

describe("fee normalisers and labels", () => {
  it("keep UNKNOWN amounts null (never zero), force TEST and never read VERIFIED evidence", () => {
    const q = readQuote({ ...QUOTE, provider_fee_minor: 0.5, provider_fee_basis: "ESTIMATED", fee_environment: "PRODUCTION" })!;
    expect(q.provider_fee_minor).toBeNull(); // a float is malformed: UNKNOWN, not 0
    expect(q.provider_fee_basis).toBe("UNKNOWN");
    expect(q.fee_environment).toBe("TEST");
    const a = readAssessment({ id: "a1", fee_minor: null, basis: "ESTIMATED", currency: "USD", basis_minor: "100" })!;
    expect(a.fee_minor).toBeNull();
    expect(a.basis).toBe("UNKNOWN");
    expect(a.net_fee_minor).toBeNull();
    expect(readSchedule({ id: "s1", confirmation_status: "VERIFIED" })!.confirmation_status).toBe("UNVERIFIED");
    expect(readVersion({ id: VERSION_ID, fee_environment: "PRODUCTION", terms: { model: "PERCENTAGE", rate_bps: 500, fixed_minor: "x" } })).toMatchObject({
      fee_environment: "TEST",
      terms: { rate_bps: 500, fixed_minor: null },
    });
    expect(readAdjustment({ id: "j1", amount_minor: "9007199254740993" })!.amount_minor).toBe("9007199254740993");
  });

  it("convert rates and amounts with string arithmetic only", () => {
    expect(bpsToPercent(250)).toBe("2.50%");
    expect(bpsToPercent(10000)).toBe("100.00%");
    expect(bpsToPercent(5)).toBe("0.05%");
    expect(percentToBps("2.9")).toBe(290);
    expect(percentToBps("100")).toBe(10000);
    expect(percentToBps("100.01")).toBeNull();
    expect(percentToBps("1.234")).toBeNull();
    expect(percentToBps("-1")).toBeNull();
    expect(amountToMinor("0.30", "USD")).toBe("30");
    expect(amountToMinor("0", "USD")).toBe("0");
    expect(amountToMinor("1.234", "USD")).toBeNull();
    expect(describeTerms({ model: "PERCENTAGE_PLUS_FIXED", rate_bps: 290, fixed_minor: "30", min_fee_minor: null, max_fee_minor: "1000", tiers: [], rounding_mode: "HALF_EVEN" }, "USD")).toMatch(
      /2\.90% \+ .*0\.30, max .*10\.00, rounded half even/,
    );
    expect(describeTerms({ model: "PROVIDER_PASS_THROUGH", rate_bps: null, fixed_minor: null, min_fee_minor: null, max_fee_minor: null, tiers: [], rounding_mode: "HALF_UP" }, "USD")).toMatch(/passed through/);
  });
});

describe("fee displays", () => {
  it("label everything TEST, show UNKNOWN instead of zero, and pass axe", async () => {
    const q = readQuote(QUOTE)!;
    const a = readAssessment({ id: "a1", category: "PAYMENT_PROCESSING_FEE", fee_minor: null, basis: "UNKNOWN", currency: "USD", basis_minor: "10000", synthetic: true })!;
    const { container } = render(
      <>
        <FeeBanner />
        <FeeTestBanner />
        <QuoteBreakdown quote={q} />
        <QuotesTable items={[q]} caption="Fee quotes" />
        <AssessmentsTable items={[a]} caption="Fee assessments" />
        <PayoutFeeFacts currency="USD" fee={null} basis="UNKNOWN" responsibility="ORGANISER_COVERED" receives={null} />
      </>,
    );
    expect(container).toHaveTextContent("TEST FEES ONLY");
    expect(container).toHaveTextContent(/Production fees:\s*BLOCKED/);
    expect(container).toHaveTextContent("It is not a charge");
    expect(screen.getAllByText("UNKNOWN").length).toBeGreaterThanOrEqual(4);
    expect(container).toHaveTextContent("Paid by the campaign");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("the organiser view of adjustments hides staff-only notes", () => {
    const j = readAdjustment({ id: "j1", reason: "PROVIDER_CORRECTION", amount_minor: "50", currency: "USD", note: "Internal staff note", status: "PENDING_APPROVAL" })!;
    const { rerender } = render(<AdjustmentsTable items={[j]} caption="Adjustments" />);
    expect(screen.getByText("Internal staff note")).toBeInTheDocument();
    rerender(<AdjustmentsTable items={[j]} caption="Adjustments" staff={false} />);
    expect(screen.queryByText("Internal staff note")).not.toBeInTheDocument();
  });
});

describe("FeeAction", () => {
  it("approve requires a reason, binds the terms hash, steps up and retries once", async () => {
    const user = userEvent.setup();
    const versionAction = vi.fn().mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403)).mockResolvedValueOnce({});
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "x" }));
    render(staffShell(<FeeAction target={{ kind: "approve", id: VERSION_ID, termsHash: HASH }} api={fakeApi({ versionAction })} />, fakeAuth(stepUp)));
    await user.click(screen.getByRole("button", { name: "Approve version" }));
    const dialog = screen.getByRole("dialog", { name: "Approve this fee policy version?" });
    expect(await axeViolations(dialog)).toEqual([]);
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    expect(within(dialog).getByText("Write 5 to 2,000 characters.")).toBeInTheDocument();
    expect(versionAction).not.toHaveBeenCalled();
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Synthetic rate reviewed.");
    expect(within(dialog).getByLabelText(/Bind my decision/)).toBeChecked();
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    await user.type(await screen.findByLabelText("Authentication code"), "246810");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(versionAction).toHaveBeenCalledTimes(2));
    expect(versionAction).toHaveBeenLastCalledWith(VERSION_ID, "approve", "Synthetic rate reviewed.", HASH);
    expect(await screen.findByRole("status")).toHaveTextContent("Version approved.");
  });

  it("maps self-approval without echoing server text; the notice survives losing the action", async () => {
    const user = userEvent.setup();
    const versionAction = vi.fn().mockRejectedValueOnce(apiError("SELF_APPROVAL_FORBIDDEN", 403)).mockResolvedValueOnce({});
    const { rerender } = render(staffShell(<FeeAction target={{ kind: "reject", id: VERSION_ID, termsHash: null }} api={fakeApi({ versionAction })} />));
    await user.click(screen.getByRole("button", { name: "Reject version" }));
    const dialog = screen.getByRole("dialog", { name: "Reject this fee policy version?" });
    expect(within(dialog).queryByLabelText(/Bind my decision/)).not.toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Rate looks wrong.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Reject" }));
    expect(await within(dialog).findByText(/You can't decide this/)).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("server text that must not be shown");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Reject" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Version rejected.");
    expect(versionAction).toHaveBeenLastCalledWith(VERSION_ID, "reject", "Rate looks wrong.", null);
    rerender(staffShell(<FeeAction target={{ kind: "reject", id: VERSION_ID, termsHash: null }} available={false} api={fakeApi({ versionAction })} />));
    expect(screen.getByRole("status")).toHaveTextContent("Version rejected.");
    expect(screen.queryByRole("button", { name: "Reject version" })).not.toBeInTheDocument();
  });
});

describe("staff fee forms", () => {
  it("create policy validates locally before sending and uses an Idempotency-Key", async () => {
    const user = userEvent.setup();
    const createPolicy = vi.fn(async () => ({ id: "not-a-uuid" }));
    const { container } = render(staffShell(<CreatePolicyForm api={fakeApi({ createPolicy })} />));
    expect(await axeViolations(container)).toEqual([]);
    const form = screen.getByRole("form", { name: "Create a fee policy" });
    await user.type(within(form).getByLabelText("Code"), "Bad Code");
    await user.type(within(form).getByLabelText("Name"), "Platform fee");
    await user.click(within(form).getByRole("button", { name: "Create policy" }));
    expect(await within(form).findByText(/Use 3–64 lowercase letters/)).toBeInTheDocument();
    await waitFor(() => expect(within(form).getByText("Please correct the errors below.")).toHaveFocus());
    expect(createPolicy).not.toHaveBeenCalled();
    expect(within(form).getByLabelText("Category").querySelectorAll("option")).toHaveLength(5); // no FX or "other" category
    await user.clear(within(form).getByLabelText("Code"));
    await user.type(within(form).getByLabelText("Code"), "platform.usd");
    await user.click(within(form).getByRole("button", { name: "Create policy" }));
    await waitFor(() => expect(createPolicy).toHaveBeenCalledTimes(1));
    const [body, key] = createPolicy.mock.calls[0] as unknown as [Record<string, unknown>, string];
    expect(body).toMatchObject({ code: "platform.usd", name: "Platform fee", category: "PLATFORM_FEE", currency: "USD", payment_method: null, campaign_id: null });
    expect(key).toMatch(/^[0-9a-f-]{36}$/);
  });

  it("draft version sends basis points and minor units, never floats", async () => {
    const user = userEvent.setup();
    const createVersion = vi.fn(async () => ({}));
    render(staffShell(<VersionForm policyId="p1" category="PLATFORM_FEE" currency="USD" api={fakeApi({ createVersion })} />));
    const form = screen.getByRole("form", { name: "Draft a fee policy version" });
    await user.selectOptions(within(form).getByLabelText("Fee model"), "PERCENTAGE_PLUS_FIXED");
    await user.type(within(form).getByLabelText("Rate (%)"), "2.9");
    await user.type(within(form).getByLabelText("Fixed amount (USD)"), "0.30");
    await user.type(within(form).getByLabelText("Maximum fee (USD, optional)"), "10");
    await user.click(within(form).getByRole("button", { name: "Create draft" }));
    await waitFor(() => expect(createVersion).toHaveBeenCalledTimes(1));
    const [, body] = createVersion.mock.calls[0] as unknown as [string, Record<string, unknown>];
    expect(body).toMatchObject({ model: "PERCENTAGE_PLUS_FIXED", rate_bps: 290, fixed_minor: "30", max_fee_minor: "1000", min_fee_minor: null, responsibility: "ORGANISER_COVERED", refund_treatment: "PROPORTIONAL" });
    expect(within(form).getByLabelText("Who bears the fee").querySelectorAll("option")).toHaveLength(3); // never platform-absorbed for a platform fee
  });

  it("provider evidence never offers VERIFIED; adjustment requests need an amount and a note", async () => {
    const user = userEvent.setup();
    const requestAdjustment = vi.fn(async () => ({}));
    render(
      staffShell(
        <>
          <ScheduleForm sandboxProviderId={null} api={fakeApi()} />
          <AdjustmentForm assessmentId="a1" currency="USD" api={fakeApi({ requestAdjustment })} />
        </>,
      ),
    );
    const evidence = screen.getByRole("form", { name: "Record provider fee evidence" });
    const options = [...within(evidence).getByLabelText("Confirmation").querySelectorAll("option")].map((o) => o.getAttribute("value"));
    expect(options).toEqual(["SYNTHETIC", "UNVERIFIED"]);
    const adj = screen.getByRole("form", { name: "Request a fee adjustment" });
    await user.click(within(adj).getByRole("button", { name: "Request adjustment" }));
    // focus moves to the error summary first: wait for it before typing again
    await waitFor(() => expect(within(adj).getByText("Please correct the errors below.")).toHaveFocus());
    expect(requestAdjustment).not.toHaveBeenCalled();
    await user.type(within(adj).getByLabelText("Amount (USD)"), "0.50");
    await user.type(within(adj).getByLabelText("Note (staff only)"), "Provider corrected its estimate.");
    await user.click(within(adj).getByRole("button", { name: "Request adjustment" }));
    await waitFor(() => expect(requestAdjustment).toHaveBeenCalledWith("a1", { reason: "PROVIDER_CORRECTION", direction: "DECREASE", amount_minor: "50", note: "Provider corrected its estimate." }, expect.stringMatching(/^[0-9a-f-]{36}$/)));
  });
});

describe("DonationQuotePreview", () => {
  it("asks the API for a quote (never computes fees) and shows the breakdown; errors are plain language", async () => {
    const user = userEvent.setup();
    const createQuote = vi.fn().mockRejectedValueOnce(apiError("CAMPAIGN_NOT_ACCEPTING_DONATIONS", 422)).mockResolvedValueOnce(QUOTE);
    const { container } = render(ownerShell(<DonationQuotePreview campaignId={CAMPAIGN_ID} api={fakeApi({ createQuote })} />));
    expect(await axeViolations(container)).toEqual([]);
    const form = screen.getByRole("form", { name: "Preview donation fees" });
    await user.click(within(form).getByRole("button", { name: "Preview fees" }));
    await waitFor(() => expect(within(form).getByText("Please correct the errors below.")).toHaveFocus());
    expect(createQuote).not.toHaveBeenCalled();
    await user.type(within(form).getByLabelText("Donation (USD)"), "100");
    await user.click(within(form).getByRole("button", { name: "Preview fees" }));
    expect(await within(form).findByText("This campaign is not accepting donations.")).toBeInTheDocument();
    await user.click(within(form).getByRole("button", { name: "Preview fees" }));
    const breakdown = await screen.findByRole("region", { name: /Fee breakdown/ });
    expect(breakdown).toHaveTextContent("It is not a charge");
    expect(breakdown).toHaveTextContent("UNKNOWN");
    const [body, key] = createQuote.mock.calls[1] as unknown as [Record<string, unknown>, string];
    expect(body).toEqual({ campaign_id: CAMPAIGN_ID, amount_minor: "10000", currency: "USD", payment_method: null });
    expect(key).toBe((createQuote.mock.calls[0] as unknown as [unknown, string])[1]); // same request ⇒ same Idempotency-Key
    expect(document.body).not.toHaveTextContent("server text that must not be shown");
  });
});
