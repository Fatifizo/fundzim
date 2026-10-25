import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { PlacementRequestForm, RankingProposalForm } from "@/components/admin/discovery/discovery-admin";
import { CampaignCard } from "@/components/campaigns/campaign-card";
import { Highlighted } from "@/components/discovery/highlighted";
import { DiscoverySearchForm } from "@/components/discovery/search-form";
import type { DiscoveryApi } from "@/lib/discovery/api";
import { EMPTY_QUERY } from "@/lib/discovery/query";
import type { DiscoveryCard } from "@/lib/discovery/types";
import { axeViolations } from "@/test/axe";

vi.mock("next/navigation", () => ({ useRouter: () => ({ refresh: vi.fn(), push: vi.fn() }) }));
vi.mock("@/components/auth/step-up", () => ({
  useStepUp: () => (fn: () => Promise<unknown>) => fn(),
  StepUpCancelledError: class extends Error {},
}));

const CARD: DiscoveryCard = {
  slug: "help-tariro-abc1234567",
  title: "Help Tariro go to school",
  summary: "School fees for Tariro in Mutare.",
  category: { code: "EDUCATION", name: "Education", slug: "education" },
  goal: { amount_minor: "150000", currency: "USD" },
  status: "ACTIVE",
  organiser: { type: "INDIVIDUAL", display_name: "Rudo" },
  trust: { organiser_identity_verified: false, organisation_verified: false, beneficiary_verified: false, campaign_reviewed: true },
  cover: null,
  published_at: null,
  selection: null,
  location: { code: "TOWN_MUTARE", name: "Mutare", kind: "TOWN", province_name: "Manicaland", province_slug: "manicaland", town_slug: "mutare" },
  highlights: {
    title: [{ text: "Help ", match: false }, { text: "Tariro", match: true }, { text: " go to school", match: false }],
    summary: [{ text: "School fees for ", match: false }, { text: "Tariro", match: true }, { text: " in Mutare.", match: false }],
  },
  reason: { code: "POPULAR_NOW", text: "Popular right now" },
};

describe("discovery components", () => {
  it("cards show the reason, place and marked search terms as plain text, never a money claim", async () => {
    const { container } = render(
      <ul>
        <CampaignCard campaign={CARD} surface="SEARCH" />
      </ul>,
    );
    expect(screen.getByText("Popular right now")).toBeInTheDocument();
    expect(screen.getByText("Mutare, Manicaland")).toBeInTheDocument();
    expect(container.querySelectorAll("mark")).toHaveLength(2);
    expect(screen.getByRole("link", { name: "Help Tariro go to school" })).toHaveAttribute("href", "/campaigns/help-tariro-abc1234567");
    expect(container.textContent).not.toMatch(/raised|donors|% funded/i);
    expect(await axeViolations(container)).toEqual([]);
  });

  it("highlights render markup as text", () => {
    const { container } = render(<Highlighted text="<b>x</b>" segments={[{ text: "<b>x</b>", match: true }]} />);
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toBe("<b>x</b>");
  });

  it("the search form is a labelled GET form with filters behind a disclosure", async () => {
    const { container } = render(
      <DiscoverySearchForm action="/campaigns" query={{ ...EMPTY_QUERY, q: "school", status: "ended" }} categories={[{ code: "EDUCATION", slug: "education", name: "Education", description: null, display_order: 1, sensitive: false, live_campaigns: 2 }]}
        provinces={[{ code: "PROVINCE_HARARE", kind: "PROVINCE", slug: "harare", name: "Harare", province_code: null, count: 3 }]} />,
    );
    const form = screen.getByRole("search", { name: "Search campaigns" });
    expect(form).toHaveAttribute("method", "get");
    for (const label of ["Search", "Category", "Province", "Sort by", "Status", "Published", "Organised by"]) expect(screen.getByLabelText(label)).toBeInTheDocument();
    expect(container.querySelector("details")).toHaveAttribute("open"); // a "more" filter is set
    expect(await axeViolations(container)).toEqual([]);
  });

  it("placement requests validate locally and send an idempotency key once", async () => {
    const user = userEvent.setup();
    const api = { requestPlacement: vi.fn(async () => ({})) } as unknown as DiscoveryApi;
    const { container } = render(<PlacementRequestForm api={api} />);
    await user.click(screen.getByRole("button", { name: "Request placement" }));
    expect(screen.getByText(/Paste the campaign's id/)).toBeInTheDocument();
    expect(api.requestPlacement).not.toHaveBeenCalled();
    // the error summary takes focus on the next animation frame; typing before it lands would lose keystrokes (KI-R1-10)
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Please correct the errors below."));
    await user.type(screen.getByLabelText("Campaign id"), "0192f000-0000-7000-8000-000000000601");
    await user.type(screen.getByLabelText(/Why this campaign/), "Shows the range of education causes");
    await user.click(screen.getByRole("button", { name: "Request placement" }));
    expect(api.requestPlacement).toHaveBeenCalledTimes(1);
    const [body, key] = (api.requestPlacement as unknown as { mock: { calls: [Record<string, unknown>, string][] } }).mock.calls[0]!;
    expect(body).toMatchObject({ campaign_id: "0192f000-0000-7000-8000-000000000601", position: 1 });
    expect(key).toMatch(/^[0-9a-f-]{36}$/);
    expect(await screen.findByText(/second team member must approve/)).toBeInTheDocument();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("ranking proposals accept only bounded whole numbers", async () => {
    const user = userEvent.setup();
    const api = { proposeRanking: vi.fn(async () => ({})) } as unknown as DiscoveryApi;
    render(<RankingProposalForm api={api} current={{ view_weight: 10, share_weight: 25, freshness_half_life_hours: 168, window_days: 7, min_unique_visitors: 3 }} />);
    const views = screen.getByLabelText("Weight of unique visitors");
    await user.clear(views);
    await user.type(views, "500");
    await user.type(screen.getByLabelText("Why (staff only)"), "Testing the bounds of the form");
    await user.click(screen.getByRole("button", { name: "Submit proposal" }));
    expect(screen.getByText("Enter a whole number from 0 to 100.")).toBeInTheDocument();
    expect(api.proposeRanking).not.toHaveBeenCalled();
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Please correct the errors below."));
    await user.clear(views);
    await user.type(views, "12");
    await user.click(screen.getByRole("button", { name: "Submit proposal" }));
    expect(api.proposeRanking).toHaveBeenCalledWith({ view_weight: 12, share_weight: 25, freshness_half_life_hours: 168, window_days: 7, min_unique_visitors: 3 }, "Testing the bounds of the form", expect.any(String));
  });
});
