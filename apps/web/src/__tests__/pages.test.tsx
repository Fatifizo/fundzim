import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import ExplorePage from "@/app/explore/page";
import StartPage from "@/app/start/page";
import PrivacyPage from "@/app/privacy/page";
import TermsPage from "@/app/terms/page";

describe("former placeholder routes", () => {
  it("/start and /explore redirect to the campaign wizard and listing", () => {
    expect(() => StartPage()).toThrow("NEXT_REDIRECT:/dashboard/campaigns/new");
    expect(() => ExplorePage()).toThrow("NEXT_REDIRECT:/campaigns");
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
