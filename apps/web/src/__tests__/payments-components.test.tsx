import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { AttemptsTable, EventTimeline, LiveExecutionPanel, SimulatedBadge } from "@/components/admin/payments/common";
import { RefundList } from "@/components/admin/payments/lists";
import { PaymentAction, RefundActions } from "@/components/admin/payments/payment-action";
import { RefundRequestForm } from "@/components/admin/payments/refund-request-form";
import { SandboxWebhookForm } from "@/components/admin/payments/sandbox-panel";
import { StepUpProvider } from "@/components/auth/step-up";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import type { PaymentsApi } from "@/lib/payments/api";
import type { AdminAttempt, PaymentEvent, RefundRequest } from "@/lib/payments/types";
import { axeViolations } from "@/test/axe";

function apiError(code: string, status: number) {
  return new ApiError({ code, status, message: "server text that must not be shown", retryable: false });
}

function fakeApi(overrides: Partial<Record<keyof PaymentsApi, unknown>> = {}): PaymentsApi {
  return new Proxy({}, { get: (_t, key: string) => (overrides as Record<string, unknown>)[key] ?? vi.fn(async () => undefined) }) as PaymentsApi;
}

function fakeAuth(stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-12T10:00:00Z" }))): AuthApi {
  return new Proxy({}, { get: (_t, key: string) => (key === "stepUp" ? stepUp : vi.fn()) }) as AuthApi;
}

function withStepUp(ui: ReactNode, auth: AuthApi = fakeAuth()) {
  return (
    <StepUpProvider mfaEnabled codeOnly api={auth}>
      {ui}
    </StepUpProvider>
  );
}

const attempt = (o: Partial<AdminAttempt> = {}): AdminAttempt => ({
  id: "0192a000-0000-7000-8000-000000000001",
  donation_intent_id: "i1",
  campaign_id: "c1",
  attempt_number: 1,
  method: "ECOCASH",
  method_category: "MOBILE_MONEY",
  status: "UNKNOWN",
  amount: { amount_minor: "2550", currency: "USD" },
  provider: { code: "sandbox", display_name: "FundZim Sandbox", simulated: true },
  simulated: true,
  needs_reconciliation: true,
  unknown_since: "2026-10-12T08:00:00Z",
  unknown_reason: "TIMEOUT",
  failure_code: null,
  created_at: "2026-10-12T07:59:00Z",
  updated_at: "2026-10-12T08:00:00Z",
  ...o,
});

const refund = (o: Partial<RefundRequest> = {}): RefundRequest => ({
  id: "r1",
  payment_id: "p1",
  amount: { amount_minor: "50000", currency: "USD" },
  reason_code: "DONOR_REQUEST",
  reason_text: "Donor asked.",
  status: "PENDING_APPROVAL",
  requested_by: { id: "maker", display_name: "Maker Staff" },
  requested_at: "2026-10-12T08:00:00Z",
  decided_by: null,
  decided_at: null,
  decision_reason: null,
  expires_at: "2026-10-19T08:00:00Z",
  execution: "DISABLED",
  ...o,
});

describe("payment display components", () => {
  it("simulated records carry a visible SIMULATED label; amounts are exact; table passes axe", async () => {
    const { container } = render(<AttemptsTable items={[attempt(), attempt({ id: "p2", simulated: false, status: "SUCCEEDED", amount: { amount_minor: "9007199254740993", currency: "USD" } })]} caption="Payments" />);
    expect(screen.getAllByText(/Simulated — development only/i)).toHaveLength(1);
    expect(screen.getByText("US$25.50")).toBeInTheDocument();
    expect(screen.getByText("US$90,071,992,547,409.93")).toBeInTheDocument();
    expect(screen.getByText("Reason: Timeout")).toBeInTheDocument();
    expect(screen.getAllByText("Needs reconciliation")).toHaveLength(2);
    expect(await axeViolations(container)).toEqual([]);
  });

  it("SimulatedBadge renders nothing for real records", () => {
    const { container } = render(<SimulatedBadge simulated={false} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("event timeline shows applied and not-applied events with the reason and the raw status", async () => {
    const base: PaymentEvent = { id: "e1", subject_type: "PAYMENT_INTENT", from_status: "CREATED", to_status: "UNKNOWN", applied: true, not_applied_reason: null, transition_code: "CREATED->UNKNOWN", source: "SYNC", provider_event_id: null, raw_status: null, actor_type: "SYSTEM", reason: null, correlation_id: null, occurred_at: null, recorded_at: null };
    const { container } = render(<EventTimeline events={[base, { ...base, id: "e2", applied: false, not_applied_reason: "LOWER_RANK", from_status: "UNKNOWN", to_status: "PENDING", raw_status: "pending", source: "WEBHOOK" }]} />);
    expect(screen.getByText("Applied")).toBeInTheDocument();
    expect(screen.getByText("Not applied: Lower rank")).toBeInTheDocument();
    expect(screen.getByText("pending")).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("live execution panel shows DISABLED with the three gates", async () => {
    const { container } = render(<LiveExecutionPanel live={{ enabled: false, constant: false, kill_switch: false }} sandboxEnabled />);
    expect(screen.getByText("DISABLED")).toBeInTheDocument();
    expect(screen.getByText("constant = false")).toBeInTheDocument();
    expect(screen.getByText("kill_switch = false")).toBeInTheDocument();
    expect(screen.getByText("enabled = false")).toBeInTheDocument();
    expect(screen.getByText("enabled (development only)")).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
  });
});

describe("staff payment actions", () => {
  it("status query: validates the reason, steps up on STEP_UP_REQUIRED, retries once, never claims a status", async () => {
    const user = userEvent.setup();
    const requestStatusQuery = vi.fn().mockRejectedValueOnce(apiError("STEP_UP_REQUIRED", 403)).mockResolvedValueOnce({ scheduled: true });
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "x" }));
    const { container } = render(withStepUp(<PaymentAction target={{ kind: "status-query", paymentId: "p1" }} api={fakeApi({ requestStatusQuery })} />, fakeAuth(stepUp)));
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Request provider status query" }));
    const dialog = screen.getByRole("dialog", { name: "Request a provider status query?" });
    expect(dialog).toHaveTextContent("never marks a payment as succeeded or failed");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Request status query" }));
    expect(within(dialog).getByText(/3 to 2,000 characters/)).toBeInTheDocument();
    expect(requestStatusQuery).not.toHaveBeenCalled();
    expect(await axeViolations(dialog)).toEqual([]);
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Donor says they were charged.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Request status query" }));
    await user.type(await screen.findByLabelText("Authentication code"), "246810");
    await user.click(screen.getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(requestStatusQuery).toHaveBeenCalledTimes(2));
    expect(requestStatusQuery).toHaveBeenLastCalledWith("p1", "Donor says they were charged.");
    expect(stepUp).toHaveBeenCalledWith({ code: "246810" });
    expect(await screen.findByRole("status")).toHaveTextContent("Status query scheduled");
  });

  it("requeue: maps INBOX_EVENT_NOT_REQUEUEABLE without echoing server text; hidden when not offered", async () => {
    const user = userEvent.setup();
    const requeueWebhook = vi.fn().mockRejectedValue(apiError("INBOX_EVENT_NOT_REQUEUEABLE", 409));
    const { rerender } = render(withStepUp(<PaymentAction target={{ kind: "requeue", inboxId: "w1" }} api={fakeApi({ requeueWebhook })} />));
    await user.click(screen.getByRole("button", { name: "Re-queue" }));
    const dialog = screen.getByRole("dialog", { name: "Re-queue this webhook?" });
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Handler fixed.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Re-queue" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Only dead-lettered or unrecognised webhooks can be re-queued");
    expect(dialog).not.toHaveTextContent("server text");
    rerender(withStepUp(<PaymentAction target={{ kind: "requeue", inboxId: "w1" }} available={false} api={fakeApi({ requeueWebhook })} />));
    expect(screen.queryByRole("button", { name: "Re-queue" })).toBeNull();
  });

  it("await settlement posts the note", async () => {
    const user = userEvent.setup();
    const awaitSettlement = vi.fn().mockResolvedValue({});
    render(withStepUp(<PaymentAction target={{ kind: "await-settlement", referenceId: "rec1" }} api={fakeApi({ awaitSettlement })} />));
    await user.click(screen.getByRole("button", { name: "Mark awaiting settlement data" }));
    const dialog = screen.getByRole("dialog", { name: "Mark as awaiting settlement data?" });
    await user.type(within(dialog).getByLabelText("Note (staff only)"), "Asked the PSP for the statement.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Awaiting settlement data" }));
    await waitFor(() => expect(awaitSettlement).toHaveBeenCalledWith("rec1", "Asked the PSP for the statement."));
    expect(await screen.findByRole("status")).toHaveTextContent("Marked as awaiting settlement data.");
  });

  it("refund approve says execution is DISABLED; the requester sees only Withdraw; self-approval is mapped", async () => {
    const user = userEvent.setup();
    const refundAction = vi.fn().mockResolvedValueOnce(refund({ status: "APPROVED" })).mockRejectedValueOnce(apiError("REFUND_SELF_APPROVAL_FORBIDDEN", 403));
    const { container, unmount } = render(withStepUp(<RefundActions refundId="r1" status="PENDING_APPROVAL" isRequester={false} api={fakeApi({ refundAction })} />));
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Approve", "Reject", "Put on hold"]);
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Approve" }));
    const dialog = screen.getByRole("dialog", { name: "Approve this refund request?" });
    expect(dialog).toHaveTextContent("Refund execution is DISABLED in Stage 8");
    await user.type(within(dialog).getByLabelText("Reason (staff only)"), "Duplicate confirmed.");
    await user.click(within(dialog).getByRole("button", { name: "Confirm: Approve" }));
    await waitFor(() => expect(refundAction).toHaveBeenCalledWith("r1", "approve", "Duplicate confirmed."));
    expect(await screen.findByRole("status")).toHaveTextContent("Refund execution is DISABLED in Stage 8: nothing was sent to any provider.");

    await user.click(screen.getByRole("button", { name: "Reject" }));
    const reject = screen.getByRole("dialog", { name: "Reject this refund request?" });
    await user.type(within(reject).getByLabelText("Reason (staff only)"), "Not eligible.");
    await user.click(within(reject).getByRole("button", { name: "Confirm: Reject" }));
    expect(await within(reject).findByRole("alert")).toHaveTextContent("You requested this refund, so you can't decide on it.");
    unmount();

    render(withStepUp(<RefundActions refundId="r1" status="PENDING_APPROVAL" isRequester api={fakeApi()} />));
    expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Withdraw"]);
  });

  it("refund list shows Execution: DISABLED and the approved note", () => {
    render(withStepUp(<RefundList items={[refund({ status: "APPROVED", decided_at: "2026-10-12T09:00:00Z", decided_by: { id: "checker", display_name: "Checker" }, decision_reason: "OK" })]} meId="someone" />));
    expect(screen.getByText("Execution: DISABLED")).toBeInTheDocument();
    expect(screen.getByText(/this refund was not sent to any provider/)).toBeInTheDocument();
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("refund request form converts the decimal amount to minor units (no floats) with one idempotency key per request", async () => {
    const user = userEvent.setup();
    const createRefundRequest = vi.fn().mockRejectedValueOnce(apiError("TIMEOUT", 0)).mockResolvedValueOnce(refund());
    const { container } = render(withStepUp(<RefundRequestForm paymentId="p1" currency="USD" paymentAmountMinor="125050" api={fakeApi({ createRefundRequest })} />));
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Record refund request" }));
    expect(await screen.findByText("Enter the refund amount.")).toBeInTheDocument();
    expect(await screen.findByText("Choose a reason.")).toBeInTheDocument();
    // an invalid submit moves focus to the error summary on the next frame: let it land before typing again
    await waitFor(() => expect(screen.getByText("Please correct the errors below.")).toHaveFocus());
    await user.type(screen.getByLabelText("Amount (USD)"), "0.295");
    await user.click(screen.getByRole("button", { name: "Record refund request" }));
    expect(await screen.findByText(/fewer decimal places/)).toBeInTheDocument();
    // an invalid submit moves focus to the error summary on the next frame: let it land before typing again
    await waitFor(() => expect(screen.getByText("Please correct the errors below.")).toHaveFocus());
    await user.clear(screen.getByLabelText("Amount (USD)"));
    await user.type(screen.getByLabelText("Amount (USD)"), "2000");
    await user.selectOptions(screen.getByLabelText("Reason"), "DUPLICATE_PAYMENT");
    await user.type(screen.getByLabelText("Explanation (staff only)"), "Paid twice.");
    await user.click(screen.getByRole("button", { name: "Record refund request" }));
    expect(await screen.findByText(/more than the payment amount/)).toBeInTheDocument();
    // an invalid submit moves focus to the error summary on the next frame: let it land before typing again
    await waitFor(() => expect(screen.getByText("Please correct the errors below.")).toHaveFocus());
    expect(createRefundRequest).not.toHaveBeenCalled();
    await user.clear(screen.getByLabelText("Amount (USD)"));
    await user.type(screen.getByLabelText("Amount (USD)"), "0.29");
    await user.click(screen.getByRole("button", { name: "Record refund request" }));
    await waitFor(() => expect(createRefundRequest).toHaveBeenCalledTimes(1));
    // The outcome of the timed-out request is unknown: wait until the form is usable again, then retry the same request.
    await waitFor(() => expect(screen.getByRole("button", { name: "Record refund request" })).toBeEnabled());
    await user.click(screen.getByRole("button", { name: "Record refund request" }));
    await waitFor(() => expect(createRefundRequest).toHaveBeenCalledTimes(2));
    const [first, second] = createRefundRequest.mock.calls;
    expect(first![1]).toEqual({ amount_minor: "29", reason_code: "DUPLICATE_PAYMENT", reason_text: "Paid twice." });
    expect(first![2]).toMatch(/^[0-9a-f-]{36}$/);
    expect(second![2]).toBe(first![2]); // the same request retried keeps its Idempotency-Key
    expect(await screen.findByRole("status")).toHaveTextContent("Refund request for US$0.29 recorded");
    expect(screen.getByRole("status")).toHaveTextContent("refund execution is DISABLED");
  });

  it("sandbox form validates and posts only the chosen knobs", async () => {
    const user = userEvent.setup();
    const sandboxWebhook = vi.fn().mockResolvedValue({ scheduled: 2 });
    const { container } = render(withStepUp(<SandboxWebhookForm paymentId="p1" api={fakeApi({ sandboxWebhook })} />));
    expect(await axeViolations(container)).toEqual([]);
    await user.click(screen.getByRole("button", { name: "Send simulated webhook" }));
    expect(screen.getByText("Choose a simulated status.")).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText("Simulated provider status"), "paid");
    await user.clear(screen.getByLabelText("Copies"));
    await user.type(screen.getByLabelText("Copies"), "2");
    await user.selectOptions(screen.getByLabelText("Sign with"), "previous");
    await user.clear(screen.getByLabelText("Timestamp offset (seconds)"));
    await user.type(screen.getByLabelText("Timestamp offset (seconds)"), "-30");
    await user.click(screen.getByRole("button", { name: "Send simulated webhook" }));
    await waitFor(() => expect(sandboxWebhook).toHaveBeenCalledWith("p1", { status: "paid", delay_seconds: 0, copies: 2, sign_with: "previous", timestamp_offset_seconds: -30 }));
    expect(await screen.findByRole("status")).toHaveTextContent("Simulated webhook scheduled");
  });

  it("no payment component offers to mark a payment succeeded or failed", () => {
    render(
      withStepUp(
        <>
          <PaymentAction target={{ kind: "status-query", paymentId: "p1" }} api={fakeApi()} />
          <RefundActions refundId="r1" status="PENDING_APPROVAL" isRequester={false} api={fakeApi()} />
          <AttemptsTable items={[attempt()]} caption="x" />
        </>,
      ),
    );
    for (const b of screen.getAllByRole("button")) expect(b.textContent).not.toMatch(/succeed|success|fail|mark (as )?paid/i);
  });
});
