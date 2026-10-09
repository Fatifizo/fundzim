import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const fetchPublicCampaign = vi.fn();
const fetchPublicUpdates = vi.fn(async () => []);

vi.mock("server-only", () => ({}));
vi.mock("@/lib/campaigns/public", () => ({
  fetchPublicCampaign: (slug: string) => fetchPublicCampaign(slug),
  fetchPublicUpdates: () => fetchPublicUpdates(),
  siteOrigin: () => null,
}));

const { default: PublicCampaignPage, generateMetadata } = await import("@/app/campaigns/[slug]/page");

const props = (slug: string) => ({ params: Promise.resolve({ slug }), searchParams: Promise.resolve({}) }) as unknown as PageProps<"/campaigns/[slug]">;

const CAMPAIGN = {
  slug: "help-chipo-abc123",
  title: "Help Chipo finish school",
  summary: "School fees for the final year.",
  story: "First paragraph.\n\n<script>alert(1)</script> https://evil.example",
  category: { code: "EDUCATION", name: "Education" },
  goal: { amount_minor: "150000", currency: "USD" },
  status: "PAUSED",
  organiser: { display_name: "Tendai" },
  organisation: null,
  beneficiary: { disclosure: "NONE", display_name: null },
  cover: null,
  gallery: [],
  published_at: "2026-10-01T10:00:00Z",
  completed_at: null,
};

describe("public campaign page", () => {
  beforeEach(() => {
    fetchPublicCampaign.mockReset();
  });

  it("answers 404 for malformed slugs without calling the API", async () => {
    for (const slug of ["../admin", "UPPER", "a%2Fb", "<x>"]) {
      await expect(PublicCampaignPage(props(slug))).rejects.toThrow("NEXT_NOT_FOUND");
    }
    expect(fetchPublicCampaign).not.toHaveBeenCalled();
  });

  it("answers 404 when the API says the campaign is not public", async () => {
    fetchPublicCampaign.mockResolvedValue(null);
    await expect(PublicCampaignPage(props("draft-campaign-x1"))).rejects.toThrow("NEXT_NOT_FOUND");
  });

  it("renders the goal, a disabled donate control, no totals, private beneficiary and inert story", async () => {
    fetchPublicCampaign.mockResolvedValue(CAMPAIGN);
    const { container } = render(await PublicCampaignPage(props(CAMPAIGN.slug)));
    expect(screen.getByRole("heading", { level: 1, name: CAMPAIGN.title })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Donations are not yet available." })).toBeDisabled();
    expect(screen.getByText("Details kept private")).toBeInTheDocument();
    expect(screen.getByText("This campaign is paused")).toBeInTheDocument();
    expect(container.textContent).toContain("US$1,500.00");
    expect(container.textContent).not.toMatch(/raised|donors|% funded/i);
    expect(container.querySelector("progress, [role=progressbar], script, a[href^='https://evil']")).toBeNull();
  });

  it("builds metadata without totals and marks the status", async () => {
    fetchPublicCampaign.mockResolvedValue(CAMPAIGN);
    const meta = await generateMetadata(props(CAMPAIGN.slug));
    expect(meta.title).toBe("Help Chipo finish school (paused)");
    expect(JSON.stringify(meta)).not.toMatch(/raised|amount|total/i);
    expect(meta.openGraph).toMatchObject({ title: "Help Chipo finish school (paused)", type: "website" });
  });
});
