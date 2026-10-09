import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import HomePage from "@/app/page";
import { DevPreviewBanner } from "@/components/layout/dev-preview-banner";
import { SiteHeader } from "@/components/layout/site-header";
import { axeViolations } from "@/test/axe";

function renderHome() {
  return render(
    <>
      <DevPreviewBanner />
      <SiteHeader />
      <main>
        <HomePage />
      </main>
    </>,
  );
}

describe("homepage", () => {
  it("renders the hero copy and CTAs", () => {
    renderHome();
    expect(screen.getByRole("heading", { level: 1, name: "Together, We Can Make a Difference." })).toBeInTheDocument();
    expect(screen.getByText("Support people and causes across Zimbabwe through simple, secure fundraising.")).toBeInTheDocument();
    expect(screen.getByText("Fund anyone in Zimbabwe, from anywhere.")).toBeInTheDocument();
    const main = screen.getByRole("main");
    expect(within(main).getByRole("link", { name: "Start a Fundraiser" })).toHaveAttribute("href", "/dashboard/campaigns/new");
    expect(within(main).getByRole("link", { name: "Explore Campaigns" })).toHaveAttribute("href", "/campaigns");
  });

  it("renders the three how-it-works steps in order", () => {
    renderHome();
    const steps = screen.getAllByRole("heading", { level: 3, name: /^Step \d: / });
    expect(steps.map((s) => s.textContent)).toEqual([
      "Step 1: Create a campaign",
      "Step 2: Share your story",
      "Step 3: Receive support",
    ]);
  });

  it("states honestly that payments are not live and no provider is selected", () => {
    renderHome();
    expect(screen.getByText("Payments are not live yet")).toBeInTheDocument();
    expect(screen.getByText(/No payment provider has been selected/)).toBeInTheDocument();
    expect(screen.getByText(/EcoCash, OneMoney, InnBucks and O'Mari/)).toBeInTheDocument();
  });

  it("shows an empty state and only a clearly labelled design preview, with no money totals", () => {
    renderHome();
    expect(screen.getByText("Campaigns will appear here once fundraising opens")).toBeInTheDocument();
    expect(screen.getByText("Design preview — not a real campaign")).toBeInTheDocument();
    const main = screen.getByRole("main");
    expect(main.textContent).not.toMatch(/US\$|ZiG \d|raised/i);
  });

  it("marks trust features as planned, not active", () => {
    renderHome();
    expect(screen.getAllByText("Planned — being built")).toHaveLength(3);
    expect(screen.getByText(/None of them is active yet/)).toBeInTheDocument();
  });

  it("shows the development preview notice", () => {
    renderHome();
    expect(screen.getByText(/Development preview — FundZim is not open for fundraising/)).toBeInTheDocument();
  });

  it("never claims compliance, licensing or certification", () => {
    const { container } = renderHome();
    expect(container.textContent).not.toMatch(/\b(compliant|certified|approved by|PCI)\b/i);
    expect(container.textContent).not.toMatch(/\bwe are licensed\b|\bFundZim is licensed\b/i);
  });

  it("has no detectable axe violations", async () => {
    const { container } = renderHome();
    expect(await axeViolations(container)).toEqual([]);
  });
});
