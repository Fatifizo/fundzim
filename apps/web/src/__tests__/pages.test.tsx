import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import CampaignNotFound from "@/app/campaigns/[slug]/not-found";
import CampaignPage, { metadata as campaignMetadata } from "@/app/campaigns/[slug]/page";
import LoginPage, { metadata as loginMetadata } from "@/app/login/page";
import PrivacyPage from "@/app/privacy/page";
import TermsPage from "@/app/terms/page";
import { axeViolations } from "@/test/axe";

describe("placeholder routes", () => {
  it("login is a coming-soon page with noindex and no auth form", async () => {
    const { container } = render(
      <main>
        <LoginPage />
      </main>,
    );
    expect(screen.getByText(/Coming soon — under development \(Stage 4\)/)).toBeInTheDocument();
    expect(container.querySelector("form, input")).toBeNull();
    expect(loginMetadata.robots).toMatchObject({ index: false });
    expect(await axeViolations(container)).toEqual([]);
  });

  it("campaign pages never render a slug as a campaign: they 404", () => {
    expect(() => CampaignPage()).toThrow("NEXT_NOT_FOUND");
    expect(campaignMetadata.robots).toMatchObject({ index: false });
    render(<CampaignNotFound />);
    expect(screen.getByRole("heading", { level: 1, name: "Campaign pages are coming soon" })).toBeInTheDocument();
  });
});

describe("legal drafts", () => {
  it.each([
    ["privacy", PrivacyPage, "This is not the FundZim privacy notice."],
    ["terms", TermsPage, "This is not the FundZim terms of use."],
  ])("%s is marked DRAFT and LEGAL_REVIEW_REQUIRED", (_name, Page, text) => {
    render(<Page />);
    expect(screen.getByText("DRAFT — placeholder only")).toBeInTheDocument();
    expect(screen.getByText(new RegExp(text.replace(".", "\\.")))).toHaveTextContent("LEGAL_REVIEW_REQUIRED");
  });
});
