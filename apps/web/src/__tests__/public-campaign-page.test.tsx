import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const fetchPublicCampaign = vi.fn();
const fetchPublicUpdates = vi.fn(async () => ({ ok: true, data: [] as unknown[] }));

vi.mock("server-only", () => ({}));
vi.mock("@/lib/campaigns/public", () => ({
  fetchPublicCampaign: (slug: string) => fetchPublicCampaign(slug),
  fetchPublicUpdates: () => fetchPublicUpdates(),
}));

const { default: PublicCampaignPage, generateMetadata } = await import("@/app/campaigns/[slug]/page");

const props = (slug: string) => ({ params: Promise.resolve({ slug }), searchParams: Promise.resolve({}) }) as unknown as PageProps<"/campaigns/[slug]">;

const CAMPAIGN = {
  slug: "help-chipo-abc123",
  canonical_slug: "help-chipo-abc123",
  public_code: "abc123",
  trust: { organiser_identity_verified: true, organisation_verified: false, beneficiary_verified: false, campaign_reviewed: true },
  title: "Help Chipo finish school",
  summary: "School fees for the final year.",
  story: "First paragraph.\n\n<script>alert(1)</script> https://evil.example",
  category: { code: "EDUCATION", name: "Education", slug: "education" },
  goal: { amount_minor: "150000", currency: "USD" },
  status: "PAUSED",
  organiser: { type: "INDIVIDUAL", display_name: "Tendai" },
  organisation: null,
  beneficiary: { disclosure: "NONE", display_name: null },
  cover: { id: "00000000-0000-0000-0000-000000000001", alt_text: "Chipo at school", widths: [320, 640] },
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

  it("redirects permanently from a non-canonical slug", async () => {
    fetchPublicCampaign.mockResolvedValue(CAMPAIGN);
    await expect(PublicCampaignPage(props("old-title-abc123"))).rejects.toThrow("NEXT_PERMANENT_REDIRECT:/campaigns/help-chipo-abc123");
  });

  it("shows only true trust indicators with the honest disclaimer, the report button, and no share links without PUBLIC_SITE_URL", async () => {
    fetchPublicCampaign.mockResolvedValue(CAMPAIGN);
    render(await PublicCampaignPage(props(CAMPAIGN.slug)));
    expect(screen.getByText("Organiser's identity verified")).toBeInTheDocument();
    expect(screen.getByText("Reviewed before publishing")).toBeInTheDocument();
    expect(screen.queryByText("Organisation verified")).toBeNull();
    expect(screen.getByText(/Verified identity does not guarantee the campaign's claims/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Report this campaign" })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /WhatsApp/ })).toBeNull();
    expect(screen.getByRole("link", { name: "Education" })).toHaveAttribute("href", "/categories/education");
  });

  it("renders updates with their approved photos", async () => {
    fetchPublicCampaign.mockResolvedValue(CAMPAIGN);
    fetchPublicUpdates.mockResolvedValueOnce({ ok: true, data: [{ id: "u1", title: "Fees paid", body: "Thank you.", media_ids: ["00000000-0000-0000-0000-000000000009", "../x"], published_at: "2026-10-02T10:00:00Z" }] });
    const { container } = render(await PublicCampaignPage(props(CAMPAIGN.slug)));
    expect(screen.getByRole("heading", { level: 3, name: "Fees paid" })).toBeInTheDocument();
    const imgs = [...container.querySelectorAll("img")].slice(1).map((i) => i.getAttribute("src"));
    const base = "/api/v1/public/campaigns/help-chipo-abc123/media/00000000-0000-0000-0000-000000000009";
    expect(imgs).toEqual([`${base}?w=640`]);
    expect(container.querySelectorAll("img")[1]?.getAttribute("srcset")).toBe(`${base}?w=320 320w, ${base}?w=640 640w, ${base}?w=1024 1024w`);
  });

  it("serves the cover with the listed variants only, eager and high priority", async () => {
    fetchPublicCampaign.mockResolvedValue(CAMPAIGN);
    const { container } = render(await PublicCampaignPage(props(CAMPAIGN.slug)));
    const cover = container.querySelector("img")!;
    const base = "/api/v1/public/campaigns/help-chipo-abc123/media/00000000-0000-0000-0000-000000000001";
    expect(cover).toHaveAttribute("src", `${base}?w=640`);
    expect(cover).toHaveAttribute("srcset", `${base}?w=320 320w, ${base}?w=640 640w`);
    expect(cover).toHaveAttribute("sizes", expect.stringContaining("736px"));
    expect(cover).toHaveAttribute("loading", "eager");
    expect(cover).toHaveAttribute("alt", "Chipo at school");
  });

  it("builds metadata without totals and marks the status", async () => {
    fetchPublicCampaign.mockResolvedValue(CAMPAIGN);
    const meta = await generateMetadata(props(CAMPAIGN.slug));
    expect(meta.title).toBe("Help Chipo finish school (paused)");
    expect(JSON.stringify(meta)).not.toMatch(/raised|amount|total/i);
    expect(meta.openGraph).toMatchObject({ title: "Help Chipo finish school (paused)", type: "article" });
    // No PUBLIC_SITE_URL in the test environment: no guessed absolute URLs.
    expect(meta.alternates).toBeUndefined();
  });
});
