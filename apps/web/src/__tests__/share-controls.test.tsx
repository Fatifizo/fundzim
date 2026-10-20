import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ShareControls } from "@/components/campaigns/share-controls";
import { resetReferralCache } from "@/lib/share/referral";
import { axeViolations } from "@/test/axe";

const URL_ = "https://fundzim.test/campaigns/help-chipo-abc123";
const props = { slug: "help-chipo-abc123", title: "Help Chipo", summary: "School fees for Chipo.", url: URL_ };

afterEach(() => {
  vi.unstubAllGlobals();
  resetReferralCache();
});

/** fetch stub: referral requests get a code (or a status), engagement events get 202. */
function apiStub(referral: { status: number; code?: string } = { status: 201, code: "0123456789ab" }) {
  return vi.fn(async (url: string) => {
    if (url.endsWith("/discovery/referrals")) {
      return referral.status === 201
        ? new Response(JSON.stringify({ data: { code: referral.code, channel: "X", expires_at: "2099-01-01T00:00:00Z" } }), { status: 201 })
        : new Response("{}", { status: referral.status });
    }
    return new Response(null, { status: 202 });
  });
}

describe("ShareControls", () => {
  it("renders the share links with the canonical URL and opens external ones safely", async () => {
    const { container } = render(<ShareControls {...props} />);
    const wa = screen.getByRole("link", { name: /WhatsApp/ });
    expect(wa.getAttribute("href")).toMatch(/^https:\/\/wa\.me\/\?text=/);
    expect(decodeURIComponent(wa.getAttribute("href")!)).toContain(`Read the story: ${URL_}`);
    expect(wa).toHaveAttribute("target", "_blank");
    expect(wa).toHaveAttribute("rel", "noopener noreferrer");
    expect(wa).toHaveAccessibleName("WhatsApp (opens in a new tab)");
    expect(screen.getByRole("link", { name: /Facebook/ }).getAttribute("href")).toBe(`https://www.facebook.com/sharer/sharer.php?u=${encodeURIComponent(URL_)}`);
    expect(screen.getByRole("link", { name: /^X/ }).getAttribute("href")).toMatch(/^https:\/\/x\.com\/intent\/post\?text=/);
    expect(screen.getByRole("link", { name: "Email" }).getAttribute("href")).toMatch(/^mailto:\?subject=/);
    expect(screen.getByRole("link", { name: "Email" })).not.toHaveAttribute("target");
    expect(await axeViolations(container)).toEqual([]);
  });

  it("copies the canonical link and announces success; records a share click without the URL", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    const fetch = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetch);
    render(<ShareControls {...props} />);
    await user.click(screen.getByRole("button", { name: "Copy link" }));
    expect(writeText).toHaveBeenCalledWith(URL_);
    expect(screen.getByRole("status")).toHaveTextContent("Link copied.");
    expect(JSON.parse(String((fetch.mock.calls[0] as unknown as [string, RequestInit])[1].body))).toEqual({ events: [{ type: "SHARE_CLICK", slug: "help-chipo-abc123", channel: "COPY_LINK" }] });
  });

  it("copies the link with a referral code when the API issues one", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    vi.stubGlobal("fetch", apiStub());
    render(<ShareControls {...props} />);
    await user.click(screen.getByRole("button", { name: "Copy link" }));
    expect(writeText).toHaveBeenCalledWith(`${URL_}?ref=0123456789ab`);
  });

  it("opens WhatsApp in a window created during the click, cuts the opener, then adds the referral code", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", apiStub());
    const win = { opener: {} as unknown, location: { replace: vi.fn() } };
    const open = vi.fn(() => win);
    vi.stubGlobal("open", open);
    render(<ShareControls {...props} />);
    await user.click(screen.getByRole("link", { name: /WhatsApp/ }));
    expect(open).toHaveBeenCalledWith("about:blank", "_blank");
    expect(win.opener).toBeNull();
    await vi.waitFor(() => expect(win.location.replace).toHaveBeenCalled());
    const target = new URL(win.location.replace.mock.calls[0]![0] as string);
    expect(target.origin).toBe("https://wa.me");
    expect(target.searchParams.get("text")).toContain(`Read the story: ${URL_}?ref=0123456789ab`);
  });

  it("shares the plain canonical link when referral codes are switched off", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", apiStub({ status: 503 }));
    const win = { opener: {} as unknown, location: { replace: vi.fn() } };
    vi.stubGlobal("open", vi.fn(() => win));
    render(<ShareControls {...props} />);
    await user.click(screen.getByRole("link", { name: /LinkedIn/ }));
    await vi.waitFor(() => expect(win.location.replace).toHaveBeenCalled());
    expect(win.location.replace.mock.calls[0]![0]).toBe(`https://www.linkedin.com/sharing/share-offsite/?url=${encodeURIComponent(URL_)}`);
  });

  it("falls back to a selectable field when copying is refused", async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", { value: { writeText: vi.fn(async () => Promise.reject(new Error("denied"))) }, configurable: true });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
    render(<ShareControls {...props} />);
    await user.click(screen.getByRole("button", { name: "Copy link" }));
    expect(screen.getByRole("status")).toHaveTextContent(/did not allow copying/);
    expect(screen.getByLabelText("Campaign link")).toHaveValue(URL_);
  });

  it("offers native sharing only where the Web Share API exists", async () => {
    render(<ShareControls {...props} />);
    expect(screen.queryByRole("button", { name: "More options…" })).toBeNull();
    const share = vi.fn(async () => undefined);
    Object.defineProperty(navigator, "share", { value: share, configurable: true });
    render(<ShareControls {...props} />);
    const user = userEvent.setup();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(null, { status: 204 })));
    await user.click(await screen.findByRole("button", { name: "More options…" }));
    expect(share).toHaveBeenCalledWith(expect.objectContaining({ url: URL_, title: "Help Chipo" }));
    Reflect.deleteProperty(navigator, "share");
  });

  it("offers no share options without a canonical URL; explains why only when asked", () => {
    const { container, rerender } = render(<ShareControls {...props} url={null} />);
    expect(container).toBeEmptyDOMElement();
    rerender(<ShareControls {...props} url={null} showConfigNote />);
    expect(screen.getByText(/PUBLIC_SITE_URL is not configured/)).toBeInTheDocument();
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.queryByRole("button")).toBeNull();
  });
});
