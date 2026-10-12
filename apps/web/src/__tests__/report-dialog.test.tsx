import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ReportCampaign } from "@/components/campaigns/report-dialog";
import { browserApi } from "@/lib/api/client";
import { ApiError } from "@/lib/api/errors";
import { axeViolations } from "@/test/axe";

afterEach(() => {
  vi.restoreAllMocks();
});

async function open() {
  const user = userEvent.setup();
  render(<ReportCampaign slug="help-chipo-abc123" title="Help Chipo" />);
  await user.click(screen.getByRole("button", { name: "Report this campaign" }));
  return { user, dialog: screen.getByRole("dialog", { name: "Report this campaign" }) };
}

describe("Report this campaign", () => {
  it("opens an accessible dialog with the contract's reasons and a hidden honeypot", async () => {
    const { dialog } = await open();
    expect(within(dialog).getAllByRole("radio")).toHaveLength(6);
    expect(within(dialog).getByText(/does not by itself remove or pause a campaign/)).toBeInTheDocument();
    const honeypot = dialog.querySelector<HTMLInputElement>('input[name="website"]')!;
    expect(honeypot).toHaveAttribute("tabindex", "-1");
    expect(honeypot.closest("[aria-hidden='true']")).not.toBeNull();
    expect(await axeViolations(dialog)).toEqual([]);
  });

  it("validates before sending: a reason, and details for 'Something else'", async () => {
    const post = vi.spyOn(browserApi, "post");
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("button", { name: "Send report" }));
    expect(within(dialog).getByText("Choose a reason.")).toBeInTheDocument();
    await user.click(within(dialog).getByRole("radio", { name: /Something else/ }));
    await user.click(within(dialog).getByRole("button", { name: "Send report" }));
    expect(within(dialog).getByText(/at least 10 characters/)).toBeInTheDocument();
    expect(within(dialog).getByLabelText(/Details \(required\)/)).toHaveAttribute("aria-invalid", "true");
    expect(post).not.toHaveBeenCalled();
  });

  it("posts the report and confirms without promising removal", async () => {
    const post = vi.spyOn(browserApi, "post").mockResolvedValue({ data: { received: true }, meta: { request_id: "req-12345678" }, status: 202, requestId: "req-12345678" });
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("radio", { name: /pretending to be another/ }));
    await user.type(within(dialog).getByLabelText(/Details/), "This is not the real school.");
    await user.type(within(dialog).getByLabelText(/Your email address/), "me@example.test");
    await user.click(within(dialog).getByRole("button", { name: "Send report" }));
    const status = await within(dialog).findByRole("status");
    expect(status).toHaveTextContent("your report was received");
    expect(dialog.textContent).not.toMatch(/will be removed|has been removed|taken down/i);
    const [url, init] = post.mock.calls[0]!;
    expect(url).toBe("/api/v1/public/campaigns/help-chipo-abc123/reports");
    expect(init?.body).toEqual({ reason: "IMPERSONATION", details: "This is not the real school.", contact_email: "me@example.test" });
  });

  it("explains rate limiting and keeps the input", async () => {
    vi.spyOn(browserApi, "post").mockRejectedValue(new ApiError({ status: 429, code: "RATE_LIMITED", message: "x", requestId: "req-12345678", retryable: true, retryAfterMs: 120_000 }));
    const { user, dialog } = await open();
    await user.click(within(dialog).getByRole("radio", { name: /Misleading/ }));
    await user.click(within(dialog).getByRole("button", { name: "Send report" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(/Too many reports.*about 2 minutes/);
    expect(within(dialog).getByRole("radio", { name: /Misleading/ })).toBeChecked();
  });
});
