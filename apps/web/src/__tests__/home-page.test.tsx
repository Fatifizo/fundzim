import { render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { PublicCampaignSummary, PublicCategory } from "@/lib/campaigns/types";

const fetchFeatured = vi.fn();
const fetchPublicCategories = vi.fn();
vi.mock("server-only", () => ({}));
vi.mock("@/lib/campaigns/public", () => ({
  fetchFeatured: () => fetchFeatured(),
  fetchPublicCategories: () => fetchPublicCategories(),
}));

const { default: HomePage } = await import("@/app/page");
const { DevPreviewBanner } = await import("@/components/layout/dev-preview-banner");
const { SiteHeader } = await import("@/components/layout/site-header");
const { SiteFooter } = await import("@/components/layout/site-footer");
const { axeViolations } = await import("@/test/axe");

const CARD: PublicCampaignSummary = {
  slug: "help-chipo-abc123",
  title: "Help Chipo finish school",
  summary: "School fees.",
  category: { code: "EDUCATION", name: "Education", slug: "education" },
  goal: { amount_minor: "150000", currency: "USD" },
  status: "ACTIVE",
  organiser: { type: "INDIVIDUAL", display_name: "Tendai" },
  trust: { organiser_identity_verified: false, organisation_verified: false, beneficiary_verified: false, campaign_reviewed: true },
  cover: null,
  published_at: null,
  selection: "STAFF_PICK",
};
const CATEGORY: PublicCategory = { code: "MEDICAL", slug: "medical", name: "Medical", description: null, display_order: 1, sensitive: true, live_campaigns: 3 };

async function renderHome() {
  const home = await HomePage();
  return render(
    <>
      <DevPreviewBanner />
      <SiteHeader />
      <main>{home}</main>
    </>,
  );
}

describe("homepage", () => {
  beforeEach(() => {
    fetchFeatured.mockResolvedValue({ ok: true, data: [CARD] });
    fetchPublicCategories.mockResolvedValue({ ok: true, data: [CATEGORY] });
  });

  it("renders the exact hero copy and CTAs", async () => {
    await renderHome();
    expect(screen.getByRole("heading", { level: 1, name: "Together, We Can Make a Difference." })).toBeInTheDocument();
    expect(screen.getByText("Support people and causes across Zimbabwe through simple, transparent fundraising.")).toBeInTheDocument();
    const hero = screen.getByRole("heading", { level: 1 }).closest("section")!;
    expect(within(hero).getAllByRole("link").map((l) => [l.textContent, l.getAttribute("href")])).toEqual([
      ["Explore Campaigns", "/campaigns"],
      ["Start a Fundraiser", "/dashboard/campaigns/new"],
    ]);
  });

  it("shows featured campaigns from the API and explains selection without implying endorsement", async () => {
    await renderHome();
    expect(screen.getByRole("list", { name: "Featured campaigns" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: CARD.title })).toHaveAttribute("href", "/campaigns/help-chipo-abc123");
    expect(screen.getByText(/not an endorsement or a ranking/)).toBeInTheDocument();
  });

  it("shows a meaningful empty state and no fabricated campaigns when none are published", async () => {
    fetchFeatured.mockResolvedValue({ ok: true, data: [] });
    const { container } = await renderHome();
    expect(screen.getByText("No campaigns are published yet")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Featured campaigns" })).toBeNull();
    expect(container.textContent).not.toMatch(/example|design preview|US\$/i);
  });

  it("degrades when the API is unavailable", async () => {
    fetchFeatured.mockResolvedValue({ ok: false });
    fetchPublicCategories.mockResolvedValue({ ok: false });
    await renderHome();
    expect(screen.getByText("Featured campaigns could not be loaded")).toBeInTheDocument();
    expect(screen.getByText(/Categories could not be loaded/)).toBeInTheDocument();
  });

  it("lists categories with live counts", async () => {
    await renderHome();
    const list = screen.getByRole("list", { name: "Categories" });
    expect(within(list).getByRole("link", { name: /Medical/ })).toHaveAttribute("href", "/categories/medical");
    expect(within(list).getByText("3 live campaigns")).toBeInTheDocument();
  });

  it("renders four how-it-works steps; step 4 never implies payments work today", async () => {
    await renderHome();
    const steps = screen.getAllByRole("heading", { level: 3, name: /^Step \d: / });
    expect(steps.map((s) => s.textContent)).toEqual([
      "Step 1: Create your campaign",
      "Step 2: Get reviewed",
      "Step 3: Share your story",
      "Step 4: Receive support when payments become available",
    ]);
    expect(screen.getAllByText(/Donations are not available yet/).length).toBeGreaterThan(0);
  });

  it("explains trust and safety honestly, including what checks cannot promise", async () => {
    await renderHome();
    expect(screen.getByText("What checks cannot promise")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Read our safety guidance" })).toHaveAttribute("href", "/safety");
  });

  it("never claims compliance, licensing or certification", async () => {
    const { container } = await renderHome();
    expect(container.textContent).not.toMatch(/\b(compliant|certified|approved by|PCI|guaranteed)\b/i);
    expect(container.textContent).not.toMatch(/\bwe are licensed\b|\bFundZim is licensed\b/i);
  });

  it("header navigation and footer links match the brief", async () => {
    render(
      <>
        <SiteHeader />
        <SiteFooter />
      </>,
    );
    const nav = screen.getByRole("navigation", { name: "Main" });
    expect(within(nav).getAllByRole("link").map((l) => l.textContent)).toEqual(["Explore Campaigns", "How It Works", "Start a Fundraiser"]);
    expect(screen.getAllByRole("link", { name: "Sign In" })[0]).toHaveAttribute("href", "/login");
    const footer = screen.getByRole("navigation", { name: "Footer" });
    expect(within(footer).getAllByRole("link").map((l) => l.textContent)).toEqual(["About", "How It Works", "Privacy", "Terms", "Contact", "Safety"]);
  });

  it("has no detectable axe violations", async () => {
    const { container } = await renderHome();
    expect(await axeViolations(container)).toEqual([]);
  });
});
