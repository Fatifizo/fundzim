import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { CampaignGrid } from "@/components/campaigns/campaign-card";
import { resultCountText } from "@/lib/campaigns/list-query";
import type { PublicCampaignSummary } from "@/lib/campaigns/types";
import { axeViolations } from "@/test/axe";

const CARD: PublicCampaignSummary = {
  slug: "help-chipo-abc123",
  title: "Help Chipo finish school",
  summary: "School fees for the final year.",
  category: { code: "EDUCATION", name: "Education", slug: "education" },
  goal: { amount_minor: "150050", currency: "USD" },
  status: "PAUSED",
  organiser: { type: "INDIVIDUAL", display_name: "Tendai Moyo" },
  trust: { organiser_identity_verified: true, organisation_verified: false, beneficiary_verified: false, campaign_reviewed: true },
  cover: { id: "00000000-0000-0000-0000-000000000001", alt_text: "x", widths: null },
  published_at: null,
  selection: null,
};

describe("CampaignCard", () => {
  it("shows the approved cover, title link, summary, category, organiser, goal, status and true trust indicators only", async () => {
    const { container } = render(<CampaignGrid campaigns={[CARD]} label="Campaigns" />);
    expect(screen.getByRole("link", { name: CARD.title })).toHaveAttribute("href", "/campaigns/help-chipo-abc123");
    expect(screen.getByText("Education")).toBeInTheDocument();
    expect(screen.getByText("Paused")).toBeInTheDocument();
    expect(screen.getByText("By Tendai Moyo")).toBeInTheDocument();
    expect(container.textContent).toContain("US$1,500.50");
    expect(screen.getByText("1,500.50 US dollars")).toHaveClass("sr-only");
    expect(screen.getByText("Identity verified")).toBeInTheDocument();
    expect(screen.getByText("Reviewed")).toBeInTheDocument();
    expect(screen.queryByText("Organisation verified")).toBeNull();
    const img = container.querySelector("img")!;
    const base = "/api/v1/public/campaigns/help-chipo-abc123/media/00000000-0000-0000-0000-000000000001";
    expect(img).toHaveAttribute("src", `${base}?w=640`);
    expect(img).toHaveAttribute("srcset", `${base}?w=320 320w, ${base}?w=640 640w, ${base}?w=1024 1024w`);
    expect(img.getAttribute("sizes")).toMatch(/calc\(100vw - 32px\)$/);
    expect(img).toHaveAttribute("loading", "lazy");
    expect(img).toHaveAttribute("width", "640");
    expect(img).toHaveAttribute("height", "360");
    expect(container.innerHTML).not.toMatch(/style=/);
    expect(container.textContent).not.toMatch(/raised|donors|funded|%/i);
    expect(container.querySelector("progress, [role=progressbar]")).toBeNull();
    expect(await axeViolations(container)).toEqual([]);
  });

  it("shows no trust list when nothing is verified, and a neutral block without a cover", () => {
    const { container } = render(<CampaignGrid campaigns={[{ ...CARD, cover: null, organiser: null, trust: { organiser_identity_verified: false, organisation_verified: false, beneficiary_verified: false, campaign_reviewed: false } }]} label="Campaigns" />);
    expect(screen.queryByRole("list", { name: "Checks completed" })).toBeNull();
    expect(container.querySelector("img")).toBeNull();
    expect(screen.queryByText(/^By /)).toBeNull();
  });
});

describe("result count", () => {
  it("shows exact totals and 1000+ when capped", () => {
    expect(resultCountText({ value: 0, capped: false })).toBe("0 campaigns");
    expect(resultCountText({ value: 1, capped: false })).toBe("1 campaign");
    expect(resultCountText({ value: 1234, capped: false })).toBe("1,234 campaigns");
    expect(resultCountText({ value: 1000, capped: true })).toBe("1000+ campaigns");
  });
});
