import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";

import { DeadLetterActions, DraftForm, SuppressForm } from "@/components/admin/communications/comms-actions";
import { StepUpProvider } from "@/components/auth/step-up";
import { Inbox, UnreadLink } from "@/components/notifications/inbox";
import { ConsentPanel, PreferencesForm } from "@/components/notifications/preferences";
import { ApiError } from "@/lib/api/errors";
import type { AuthApi } from "@/lib/auth/api";
import type { NotificationsApi } from "@/lib/notifications/api";
import type { Consent, InboxItem, Preference } from "@/lib/notifications/types";
import { axeViolations } from "@/test/axe";

// Stage 15 notification UI: presentation only — the API decides routing, consent, permissions and step-up.

function fakeApi(overrides: Partial<Record<keyof NotificationsApi, unknown>> = {}): NotificationsApi {
  return new Proxy({}, { get: (_t, key: string) => (overrides as Record<string, unknown>)[key] ?? vi.fn(async () => ({})) }) as NotificationsApi;
}

function fakeAuth(stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-16T10:00:00Z" }))): AuthApi {
  return new Proxy({}, { get: (_t, key: string) => (key === "stepUp" ? stepUp : vi.fn()) }) as AuthApi;
}

const shell = (ui: ReactNode, auth: AuthApi = fakeAuth()) => (
  <StepUpProvider mfaEnabled codeOnly api={auth}>
    {ui}
  </StepUpProvider>
);

const item = (over: Partial<InboxItem> = {}): InboxItem => ({
  id: "0199a000-0000-7000-8000-000000000001", category: "CAMPAIGN", title: "Your campaign was approved", body: "Sign in to publish it.",
  priority: "NORMAL", deep_link: "/dashboard/campaigns", related_type: null, related_id: null, test_data: false, read_at: null,
  created_at: "2026-10-16T08:00:00Z", ...over,
});

describe("Inbox", () => {
  it("labels TEST data and unread items and marks one read", async () => {
    const markRead = vi.fn(async () => ({}));
    const items = [item(), item({ id: "0199a000-0000-7000-8000-000000000002", title: "[TEST] Thank you", category: "PAYMENT", test_data: true }),
      item({ id: "0199a000-0000-7000-8000-000000000003", title: "Password changed", read_at: "2026-10-16T09:00:00Z", deep_link: null })];
    const { container } = render(<Inbox items={items} api={fakeApi({ markRead })} />);
    const list = screen.getByRole("list", { name: "Notifications" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(3);
    expect(within(list).getByText("TEST data")).toBeInTheDocument();
    expect(within(list).getAllByRole("button", { name: /^Mark as read: / })).toHaveLength(2); // the read item has none
    expect(await axeViolations(container)).toEqual([]);
    await userEvent.click(screen.getByRole("button", { name: "Mark as read: Your campaign was approved" }));
    expect(markRead).toHaveBeenCalledWith("0199a000-0000-7000-8000-000000000001");
    expect(await screen.findByRole("status")).toHaveTextContent("Marked as read.");
  });

  it("shows a generic error, never the server text", async () => {
    const markAllRead = vi.fn(async () => {
      throw new ApiError({ code: "INTERNAL", status: 500, message: "server text that must not be shown", retryable: false, details: [] });
    });
    render(<Inbox items={[item()]} api={fakeApi({ markAllRead })} />);
    await userEvent.click(screen.getByRole("button", { name: "Mark all as read" }));
    expect(await screen.findByText("That did not work. Reload the page and try again.")).toBeInTheDocument();
    expect(screen.queryByText(/server text/)).toBeNull();
  });

  it("has an empty state and no mark-all button without unread items", () => {
    render(<Inbox items={[]} api={fakeApi()} />);
    expect(screen.getByText("No notifications to show.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Mark all as read" })).toBeNull();
  });
});

describe("UnreadLink", () => {
  it("shows the count, and an explicit failure instead of 0", async () => {
    render(<UnreadLink href="/dashboard/notifications" api={fakeApi({ unread: vi.fn(async () => ({ unread: 4 })) })} />);
    expect(await screen.findByRole("link", { name: "Notifications: 4 unread" })).toHaveAttribute("href", "/dashboard/notifications");
  });

  it("never shows 0 when the count fails", async () => {
    render(<UnreadLink href="/x" api={fakeApi({ unread: vi.fn(async () => Promise.reject(new Error("down"))) })} />);
    expect(await screen.findByRole("link", { name: "Notifications (could not load)" })).toBeInTheDocument();
    expect(screen.queryByText(/0 unread/)).toBeNull();
  });
});

const pref = (category: string, channel: string, over: Partial<Preference> = {}): Preference => ({
  category, channel, enabled: true, mandatory: false, consent_required: false, available: true, ...over,
});

describe("PreferencesForm", () => {
  const items = [
    pref("SECURITY", "EMAIL", { mandatory: true }), pref("SECURITY", "IN_APP", { mandatory: true }),
    pref("CAMPAIGN", "EMAIL"), pref("CAMPAIGN", "SMS", { enabled: false, consent_required: true }),
    pref("MARKETING", "EMAIL", { enabled: false, available: false }),
  ];

  it("locks mandatory and unavailable channels and sends only real changes", async () => {
    const setPreferences = vi.fn(async () => ({}));
    const { container } = render(<PreferencesForm items={items} api={fakeApi({ setPreferences })} />);
    const security = screen.getByRole("group", { name: "Security" });
    expect(within(security).getByLabelText(/E-mail/)).toBeDisabled();
    expect(within(security).getByLabelText(/E-mail/)).toBeChecked();
    expect(within(screen.getByRole("group", { name: "Marketing" })).getByLabelText(/E-mail/)).toBeDisabled();
    expect(within(screen.getByRole("group", { name: "Campaign" })).getByLabelText(/SMS/)).toHaveAccessibleDescription("Also needs your consent below");
    expect(await axeViolations(container)).toEqual([]);
    await userEvent.click(screen.getByRole("button", { name: "Save preferences" }));
    expect(setPreferences).not.toHaveBeenCalled();
    expect(await screen.findByRole("status")).toHaveTextContent("Nothing to save.");
    await userEvent.click(within(screen.getByRole("group", { name: "Campaign" })).getByLabelText(/E-mail/));
    await userEvent.click(screen.getByRole("button", { name: "Save preferences" }));
    expect(setPreferences).toHaveBeenCalledWith([{ category: "CAMPAIGN", channel: "EMAIL", enabled: false }]);
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Preferences saved."));
  });
});

const consent = (channel: string, purpose: Consent["purpose"], status: Consent["status"] = "NOT_REQUESTED"): Consent => ({
  channel, purpose, status, policy_version: null, current_policy_version: "consent-v1-engineering-placeholder", granted_at: null, withdrawn_at: null,
  available: purpose !== "MARKETING",
});

describe("ConsentPanel", () => {
  it("gives and withdraws service consent; marketing consent cannot be given", async () => {
    const setConsent = vi.fn(async () => ({}));
    const items = [consent("SMS", "SERVICE_MESSAGES"), consent("WHATSAPP", "SERVICE_MESSAGES", "GRANTED"), consent("EMAIL", "MARKETING"), consent("SMS", "MARKETING")];
    const { container } = render(<ConsentPanel items={items} api={fakeApi({ setConsent })} />);
    expect(screen.queryByRole("button", { name: /marketing/i })).toBeNull();
    expect(screen.getByText(/Marketing messages: not available/)).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
    await userEvent.click(screen.getByRole("button", { name: "Give consent for SMS" }));
    expect(setConsent).toHaveBeenCalledWith("SMS", "SERVICE_MESSAGES", true);
    expect(await screen.findByRole("status")).toHaveTextContent("Consent for SMS recorded.");
    await userEvent.click(screen.getByRole("button", { name: "Withdraw consent for WhatsApp" }));
    expect(setConsent).toHaveBeenCalledWith("WHATSAPP", "SERVICE_MESSAGES", false);
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Consent for WhatsApp withdrawn."));
  });
});

describe("communications staff actions", () => {
  it("replays a dead letter after a step-up and explains a blocked replay", async () => {
    let calls = 0;
    const replay = vi.fn(async () => {
      calls++;
      if (calls === 1) throw new ApiError({ code: "STEP_UP_REQUIRED", status: 403, message: "x", retryable: false, details: [] });
      return { replayed: false, blocked_reason: "CONSENT_WITHDRAWN", delivery_status: "FAILED_PERMANENT" };
    });
    const stepUp = vi.fn(async () => ({ step_up_expires_at: "2026-10-16T10:00:00Z" }));
    render(shell(<DeadLetterActions id="0199a000-0000-7000-8000-00000000000a" api={fakeApi({ replay })} />, fakeAuth(stepUp)));
    await userEvent.click(screen.getByRole("button", { name: "Replay" }));
    const dialog = screen.getByRole("dialog", { name: "Replay this delivery?" });
    await userEvent.type(within(dialog).getByLabelText("Reason (staff only)"), "Provider recovered");
    await userEvent.click(within(dialog).getByRole("button", { name: "Confirm: Replay" }));
    const step = await screen.findByRole("dialog", { name: "Confirm it's you" });
    await userEvent.type(within(step).getByLabelText("Authentication code"), "123456");
    await userEvent.click(within(step).getByRole("button", { name: "Confirm" }));
    await waitFor(() => expect(replay).toHaveBeenCalledTimes(2));
    expect(replay).toHaveBeenLastCalledWith("0199a000-0000-7000-8000-00000000000a", "Provider recovered");
    expect(await screen.findByText("Not replayed: the person withdrew consent.")).toBeInTheDocument();
  });

  it("keeps the component without buttons once the dead letter is no longer open", () => {
    render(shell(<DeadLetterActions id="0199a000-0000-7000-8000-00000000000a" open={false} api={fakeApi()} />));
    expect(screen.queryByRole("button", { name: "Replay" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Discard" })).toBeNull();
  });

  it("validates the account id before suppressing", async () => {
    const suppress = vi.fn(async () => ({}));
    render(shell(<SuppressForm api={fakeApi({ suppress })} />));
    await userEvent.type(screen.getByLabelText("Account id"), "not-an-id");
    await userEvent.click(screen.getByRole("button", { name: "Suppress" }));
    expect(screen.getByText("Paste the person's account id.")).toBeInTheDocument();
    expect(suppress).not.toHaveBeenCalled();
  });

  it("drafts with the allowed variables listed and a mapped error", async () => {
    const createDraft = vi.fn(async () => {
      throw new ApiError({ code: "VALIDATION_FAILED", status: 422, message: "server text that must not be shown", retryable: false, details: [] });
    });
    render(shell(<DraftForm templateId="0199a000-0000-7000-8000-0000000000aa" base={null} allowed={["campaign_title"]} api={fakeApi({ createDraft })} />));
    expect(screen.getByText(/\{\{display_name\}\}, \{\{campaign_title\}\}/)).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText("In-app title"), "Paused");
    await userEvent.type(screen.getByLabelText("In-app text"), "Your campaign is paused.");
    await userEvent.click(screen.getByRole("button", { name: "Save draft" }));
    await waitFor(() => expect(createDraft).toHaveBeenCalled());
    const [, body] = createDraft.mock.calls[0] as unknown as [string, Record<string, unknown>];
    expect(body).toEqual({ inapp_title: "Paused", inapp_body: "Your campaign is paused.", email_subject: null, email_text: null, sms_text: null });
    expect(screen.queryByText(/server text/)).toBeNull();
  });
});
